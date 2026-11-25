package localci

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Request kinds and states of the run queue.
const (
	KindPR       = "pr"
	KindPush     = "push"
	KindSchedule = "schedule"

	StateQueued     = "queued"
	StateRunning    = "running"
	StateDone       = "done"
	StateCancelled  = "cancelled"
	StateSuperseded = "superseded"
)

// Claim order: pull request gates first, then runs for pushes, then scheduled ones. Lower runs first.
const (
	PriorityPR       = 1
	PriorityPush     = 2
	PrioritySchedule = 3
)

// PriorityFor is the priority a kind gets when the caller leaves it empty.
func PriorityFor(kind string) int {
	switch kind {
	case KindPR:
		return PriorityPR
	case KindPush:
		return PriorityPush
	default:
		return PrioritySchedule
	}
}

// Request is one asked-for run. It is one JSON file in the queue folder; the folder it sits in
// (queued, running or done) is the truth about where it is, State is repeated inside for readers.
type Request struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // pr, push or schedule
	Repo     string `json:"repo"`
	Ref      string `json:"ref,omitempty"`  // head branch of a pull request, or the branch of a push or schedule
	Base     string `json:"base,omitempty"` // base branch of a pull request
	Number   int    `json:"number,omitempty"`
	SHA      string `json:"sha"` // head commit the request was made for
	Workflow string `json:"workflow"`
	Priority int    `json:"priority"`
	// Cron is the schedule entry that fired, for kind schedule.
	Cron       string    `json:"cron,omitempty"`
	EnqueuedAt time.Time `json:"enqueuedAt"`
	State      string    `json:"state"`
	ClaimedBy  string    `json:"claimedBy,omitempty"`
	ClaimedAt  time.Time `json:"claimedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
	// Labels the claiming agent must all have; empty means any agent.
	Labels []string `json:"labels,omitempty"`
	// RunID and Conclusion are filled when the run ended.
	RunID      string `json:"runId,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// SupersededBy is the id of the request that replaced this one.
	SupersededBy string `json:"supersededBy,omitempty"`
}

// Key names what a request is about. A new head of the same key replaces the older request.
func (r Request) Key() string {
	switch r.Kind {
	case KindPR:
		return fmt.Sprintf("pr:%s:%d", r.Repo, r.Number)
	case KindPush:
		return fmt.Sprintf("push:%s:%s:%s", r.Repo, r.Ref, r.Workflow)
	default:
		return fmt.Sprintf("schedule:%s:%s:%s:%s", r.Repo, r.Ref, r.Workflow, r.Cron)
	}
}

// Active is true while the request is queued or running.
func (r Request) Active() bool { return r.State == StateQueued || r.State == StateRunning }

// Label is a short text for consoles: "pr 278 into develop" and similar.
func (r Request) Label() string {
	switch r.Kind {
	case KindPR:
		return fmt.Sprintf("pr %d into %s", r.Number, r.Base)
	case KindPush:
		return "push " + r.Ref
	default:
		return fmt.Sprintf("schedule %s (%s)", r.Workflow, r.Cron)
	}
}

// ErrNotRunning is returned when a request is not in the running folder (finished, cancelled, taken back).
var ErrNotRunning = errors.New("the request is not running")

// ErrNoRequest is returned for an id the queue does not know.
var ErrNoRequest = errors.New("no such request")

// Queue is the on-disk run queue shared by the poller, the agents and the command line, in any number of
// processes. It uses no lock: a request is claimed by renaming its file from queued/ to running/, and a
// rename has exactly one winner. Content changes are a temp file renamed over the target.
type Queue struct {
	Dir string // <state dir>/queue
	Now func() time.Time
}

// QueueDir is the queue folder inside a state folder.
func QueueDir(stateDir string) string { return filepath.Join(stateDir, "queue") }

// OpenQueue creates the queue folders under dir.
func OpenQueue(dir string) (*Queue, error) {
	q := &Queue{Dir: dir}
	for _, s := range []string{"queued", "running", "done"} {
		if err := os.MkdirAll(filepath.Join(dir, s), 0o750); err != nil {
			return nil, err
		}
	}
	return q, nil
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *Queue) path(folder, id string) string { return filepath.Join(q.Dir, folder, id+".json") }

func (q *Queue) cancelMarker(id string) string { return filepath.Join(q.Dir, "running", id+".cancel") }

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// idSeq orders the ids made in one process within the same millisecond.
var idSeq atomic.Uint32

// newID is sortable by time: q-<yyyymmdd>-<hhmmss>-<millis>-<4 hex counter><2 random hex>. The counter keeps
// requests made in one process in the order they were made; the random part tells apart two processes.
func (q *Queue) newID(t time.Time) string {
	var b [1]byte
	_, _ = rand.Read(b[:])
	t = t.UTC()
	return fmt.Sprintf("q-%s-%03d-%04x%s", t.Format("20060102-150405"), t.Nanosecond()/1e6, idSeq.Add(1)&0xffff, hex.EncodeToString(b[:]))
}

// retryFS retries a file operation that failed because another process has the file open, which is how
// Windows reports a rename or open against a file that someone is reading. A missing file is final.
func retryFS(op func() error) error {
	delay := time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := op()
		if err == nil || errors.Is(err, fs.ErrNotExist) || !transientFS(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 40*time.Millisecond {
			delay *= 2
		}
	}
}

func transientFS(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "used by another process") || strings.Contains(m, "sharing violation")
}

func (q *Queue) read(path string) (Request, error) {
	var b []byte
	if err := retryFS(func() (e error) { b, e = os.ReadFile(path); return e }); err != nil { //nolint:gosec // path inside the queue folder
		return Request{}, err
	}
	var r Request
	if err := json.Unmarshal(b, &r); err != nil {
		return Request{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return r, nil
}

// writeAtomic writes r as path: a temp file in the same folder, renamed over the target.
func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+hex.EncodeToString(rb[:])+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := retryFS(func() error { return os.Rename(tmp, path) }); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// folderOf reads the requests of one folder, skipping temp files and ones that vanished while listing.
func (q *Queue) folderOf(folder string) ([]Request, error) {
	ents, err := os.ReadDir(filepath.Join(q.Dir, folder))
	if err != nil {
		return nil, err
	}
	var out []Request
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
			continue
		}
		r, err := q.read(filepath.Join(q.Dir, folder, n))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			continue // a half-written or foreign file is not a request
		}
		if folder != "done" {
			r.State = folder // the folder is the truth, the file may be a moment behind a rename
		} else if r.State == StateQueued || r.State == StateRunning || r.State == "" {
			r.State = StateCancelled
		}
		out = append(out, r)
	}
	return out, nil
}

// Depth counts the requests waiting and the ones running, without reading them.
func (q *Queue) Depth() (queued, running int) {
	count := func(folder string) int {
		ents, err := os.ReadDir(filepath.Join(q.Dir, folder))
		if err != nil {
			return 0
		}
		n := 0
		for _, e := range ents {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".json") {
				n++
			}
		}
		return n
	}
	return count("queued"), count("running")
}

// List returns every request: running first, then queued in claim order, then finished ones newest first.
func (q *Queue) List() ([]Request, error) {
	var all []Request
	for _, f := range []string{"running", "queued", "done"} {
		rs, err := q.folderOf(f)
		if err != nil {
			return nil, err
		}
		if f == "queued" {
			sortClaimOrder(rs)
		}
		if f == "done" {
			sort.SliceStable(rs, func(i, j int) bool { return rs[i].FinishedAt.After(rs[j].FinishedAt) })
		}
		all = append(all, rs...)
	}
	return all, nil
}

func sortClaimOrder(rs []Request) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if !a.EnqueuedAt.Equal(b.EnqueuedAt) {
			return a.EnqueuedAt.Before(b.EnqueuedAt)
		}
		return a.ID < b.ID
	})
}

// Get finds one request in any folder.
func (q *Queue) Get(id string) (Request, error) {
	if !validRequestID(id) {
		return Request{}, fmt.Errorf("%q is not a request id", id)
	}
	// A request moves queued -> running -> done and is written in its new folder before it is removed from the old
	// one. Looking in the same order therefore never misses one that moves while we look; a second pass covers a
	// request that was put back (running -> queued) in the meantime.
	for pass := 0; pass < 2; pass++ {
		for _, f := range []string{"queued", "running", "done"} {
			r, err := q.read(q.path(f, id))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return Request{}, err
			}
			if f != "done" {
				r.State = f
			}
			return r, nil
		}
	}
	return Request{}, fmt.Errorf("%w: %s", ErrNoRequest, id)
}

// Enqueue adds a request. The second result is false when an identical request (same key and head commit) is
// already queued or running: that one is returned and nothing is added. For a pull request or a push, queued
// or running requests of the same key with another head commit are superseded by the new one: a queued one is
// retired at once, a running one gets a marker its agent acts on.
func (q *Queue) Enqueue(r Request) (Request, bool, error) {
	switch r.Kind {
	case KindPR:
		if r.Number <= 0 {
			return Request{}, false, errors.New("enqueue: a pull request needs a number")
		}
	case KindPush, KindSchedule:
		if r.Ref == "" {
			return Request{}, false, errors.New("enqueue: a push or schedule needs a branch")
		}
	default:
		return Request{}, false, fmt.Errorf("enqueue: unknown kind %q", r.Kind)
	}
	if r.Repo == "" || r.Workflow == "" {
		return Request{}, false, errors.New("enqueue: a request needs a repository and a workflow")
	}
	now := q.now()
	if r.Priority == 0 {
		r.Priority = PriorityFor(r.Kind)
	}
	r.ID, r.State, r.EnqueuedAt = q.newID(now), StateQueued, now
	r.ClaimedBy, r.ClaimedAt, r.FinishedAt = "", time.Time{}, time.Time{}
	key := r.Key()
	var stale []Request
	for _, f := range []string{"queued", "running"} {
		rs, err := q.folderOf(f)
		if err != nil {
			return Request{}, false, err
		}
		for _, o := range rs {
			if o.Key() != key {
				continue
			}
			if o.SHA == r.SHA && o.Workflow == r.Workflow {
				return o, false, nil
			}
			stale = append(stale, o)
		}
	}
	if err := writeAtomic(q.path("queued", r.ID), r); err != nil {
		return Request{}, false, err
	}
	if r.Kind != KindSchedule {
		for _, o := range stale {
			if _, err := q.retire(o.ID, StateSuperseded, r.ID, fmt.Sprintf("a newer head %s arrived", shortSHA(r.SHA))); err != nil && !errors.Is(err, ErrNoRequest) {
				return r, true, err
			}
		}
	}
	return r, true, nil
}

// claimMarker is the exclusive lock of one request: whoever creates it (O_EXCL, one winner) may move the
// request out of queued/. It stays while the request runs and goes when the request ends or is taken back.
func (q *Queue) claimMarker(id string) string { return filepath.Join(q.Dir, "running", id+".claim") }

// takeMarker tries to create the claim marker; false means someone else holds it (or Windows is still deleting it).
func (q *Queue) takeMarker(id, who string) (bool, error) {
	f, err := os.OpenFile(q.claimMarker(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrPermission) {
			return false, nil
		}
		return false, err
	}
	_, _ = f.WriteString(who)
	return true, f.Close()
}

func (q *Queue) dropMarker(id string) {
	_ = retryFS(func() error { return os.Remove(q.claimMarker(id)) })
}

// staleMarker removes a claim marker whose owner died before it moved the request (older than a minute, the
// request still in queued/), so the request can be claimed again.
func (q *Queue) staleMarker(id string) {
	st, err := os.Stat(q.claimMarker(id))
	if err != nil || q.now().Sub(st.ModTime()) < time.Minute {
		return
	}
	if _, err := os.Stat(q.path("queued", id)); err == nil {
		q.dropMarker(id)
	}
}

// retire ends a request as cancelled or superseded. A queued one moves to done at once; a running one gets a
// marker file, and its agent ends it (Complete) after it has stopped the work.
func (q *Queue) retire(id, state, by, reason string) (Request, error) {
	if !validRequestID(id) {
		return Request{}, fmt.Errorf("%q is not a request id", id)
	}
	for attempt := 0; attempt < 100; attempt++ {
		held, err := q.takeMarker(id, "retire")
		if err != nil {
			return Request{}, err
		}
		if held {
			err := retryFS(func() error { return os.Rename(q.path("queued", id), q.path("done", id)) })
			if err == nil {
				defer q.dropMarker(id)
				r, rerr := q.read(q.path("done", id))
				if rerr != nil {
					return Request{}, rerr
				}
				r.State, r.FinishedAt, r.SupersededBy, r.Reason = state, q.now(), by, reason
				if r.Conclusion == "" {
					r.Conclusion = state
				}
				return r, writeAtomic(q.path("done", id), r)
			}
			q.dropMarker(id)
			if !errors.Is(err, fs.ErrNotExist) {
				return Request{}, err
			}
		}
		// not queued: running (leave a marker), already over, or being claimed this very moment
		r, rerr := q.read(q.path("running", id))
		if rerr == nil {
			r.State = StateRunning
			return r, writeAtomic(q.cancelMarker(id), cancelNote{State: state, By: by, Reason: reason})
		}
		if !errors.Is(rerr, fs.ErrNotExist) {
			return Request{}, rerr
		}
		if d, derr := q.read(q.path("done", id)); derr == nil {
			if d.State == StateQueued || d.State == StateRunning || d.State == "" {
				d.State = StateCancelled
			}
			return d, nil // already over: nothing to do
		}
		if held {
			return Request{}, fmt.Errorf("%w: %s", ErrNoRequest, id)
		}
		time.Sleep(20 * time.Millisecond) // a claimer holds the marker and is moving the file
	}
	return Request{}, fmt.Errorf("%w: %s", ErrNoRequest, id)
}

type cancelNote struct {
	State  string `json:"state"` // cancelled or superseded
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Cancel stops a request: a queued one is ended at once, a running one is marked and its agent stops it.
func (q *Queue) Cancel(id, reason string) (Request, error) {
	return q.retire(id, StateCancelled, "", reason)
}

// Supersede ends a request because the request `by` replaced it.
func (q *Queue) Supersede(id, by string) (Request, error) {
	return q.retire(id, StateSuperseded, by, "replaced by "+by)
}

// CancelRequested reports whether a running request was cancelled or superseded, and how.
func (q *Queue) CancelRequested(id string) (state, reason string, ok bool) {
	n, ok := q.cancelNoteOf(id)
	return n.State, n.Reason, ok
}

func (q *Queue) cancelNoteOf(id string) (cancelNote, bool) {
	if !validRequestID(id) {
		return cancelNote{}, false
	}
	b, err := os.ReadFile(q.cancelMarker(id))
	if err != nil {
		return cancelNote{}, false
	}
	var n cancelNote
	if json.Unmarshal(b, &n) != nil || n.State == "" {
		n.State = StateCancelled
	}
	return n, true
}

// Claim hands the next request to an agent, or nil when there is nothing it may take. Order is priority, then
// first in first out. A request with labels only goes to an agent that has all of them. Two claimers never
// get the same request: the rename of its file from queued/ to running/ succeeds for one of them only.
func (q *Queue) Claim(agent string, labels []string) (*Request, error) {
	if agent == "" {
		return nil, errors.New("claim: no agent name")
	}
	rs, err := q.folderOf("queued")
	if err != nil {
		return nil, err
	}
	sortClaimOrder(rs)
	have := map[string]bool{}
	for _, l := range labels {
		have[l] = true
	}
next:
	for _, r := range rs {
		for _, l := range r.Labels {
			if !have[l] {
				continue next
			}
		}
		held, err := q.takeMarker(r.ID, agent)
		if err != nil {
			return nil, err
		}
		if !held {
			q.staleMarker(r.ID)
			continue // another claimer has it
		}
		err = retryFS(func() error { return os.Rename(q.path("queued", r.ID), q.path("running", r.ID)) })
		if errors.Is(err, fs.ErrNotExist) {
			q.dropMarker(r.ID)
			continue // it was cancelled or taken before this claimer got the marker
		}
		if err != nil {
			q.dropMarker(r.ID)
			return nil, err
		}
		r.State, r.ClaimedBy, r.ClaimedAt = StateRunning, agent, q.now()
		r.Attempts++
		if err := writeAtomic(q.path("running", r.ID), r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	return nil, nil
}

// Complete ends a running request. A cancel or supersede marker decides the final state; otherwise it is done
// with the given conclusion. The returned request is the finished record.
func (q *Queue) Complete(id, runID, conclusion, reason string) (Request, error) {
	if !validRequestID(id) {
		return Request{}, fmt.Errorf("%q is not a request id", id)
	}
	r, err := q.read(q.path("running", id))
	if errors.Is(err, fs.ErrNotExist) {
		return Request{}, fmt.Errorf("%w: %s", ErrNotRunning, id)
	}
	if err != nil {
		return Request{}, err
	}
	r.State, r.RunID, r.Conclusion, r.Reason, r.FinishedAt = StateDone, runID, conclusion, reason, q.now()
	if n, ok := q.cancelNoteOf(id); ok {
		st := n.State
		r.State, r.SupersededBy = st, n.By
		if n.Reason != "" {
			r.Reason = n.Reason
		}
		if st == StateCancelled || st == StateSuperseded {
			r.Conclusion = st
		}
	} else if conclusion == StateCancelled || conclusion == StateSuperseded {
		r.State = conclusion // the runner found the request moot (the head moved, the pull request closed)
	}
	// write the final content to done/ first, then drop the running file: a crash in between leaves a
	// finished record, never a lost one.
	if err := writeAtomic(q.path("done", id), r); err != nil {
		return Request{}, err
	}
	if err := retryFS(func() error { return os.Remove(q.path("running", id)) }); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}
	_ = os.Remove(q.cancelMarker(id))
	q.dropMarker(id)
	return r, nil
}

// Requeue takes a running request back into the queue, for a request whose agent is gone.
func (q *Queue) Requeue(id string) error {
	if !validRequestID(id) {
		return fmt.Errorf("%q is not a request id", id)
	}
	r, err := q.read(q.path("running", id))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotRunning, id)
	}
	if err != nil {
		return err
	}
	if _, _, marked := q.CancelRequested(id); marked {
		_, err := q.Complete(id, "", StateCancelled, "the agent stopped before it could end the run")
		return err
	}
	r.State, r.ClaimedBy, r.ClaimedAt = StateQueued, "", time.Time{}
	if err := writeAtomic(q.path("queued", id), r); err != nil {
		return err
	}
	if err := retryFS(func() error { return os.Remove(q.path("running", id)) }); err != nil {
		return err
	}
	q.dropMarker(id)
	return nil
}

// RequeueStale takes back running requests whose agent is not alive. After three attempts a request is ended
// as an error instead, so one request that kills its agent does not loop forever. It returns what it moved.
func (q *Queue) RequeueStale(alive func(agent string) bool) ([]Request, error) {
	rs, err := q.folderOf("running")
	if err != nil {
		return nil, err
	}
	var moved []Request
	for _, r := range rs {
		if r.ClaimedBy != "" && alive(r.ClaimedBy) {
			continue
		}
		if r.Attempts >= 3 {
			if _, err := q.Complete(r.ID, "", "error", "the agent stopped three times while running it"); err != nil && !errors.Is(err, ErrNotRunning) {
				return moved, err
			}
			continue
		}
		if err := q.Requeue(r.ID); err != nil {
			if errors.Is(err, ErrNotRunning) {
				continue
			}
			return moved, err
		}
		moved = append(moved, r)
	}
	return moved, nil
}

// Prune deletes finished requests that ended before the cutoff.
func (q *Queue) Prune(before time.Time) (int, error) {
	rs, err := q.folderOf("done")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		if r.FinishedAt.IsZero() || r.FinishedAt.After(before) {
			continue
		}
		if err := os.Remove(q.path("done", r.ID)); err == nil {
			n++
		}
	}
	return n, nil
}
