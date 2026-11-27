package hsaction

import (
	"fmt"
	"strings"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/localci"
)

func init() {
	register(
		define("queue.top", "unravis-ci queue top <id|pr>", "Put a waiting request first in line.", queueTop),
		define("queue.cancel", "unravis-ci cancel <id|pr> [--reason R]", "Cancel a queued or running request.", queueCancel),
		define("queue.retry", "unravis-ci rerun <runid>", "Queue a finished run again, for the same commit.", queueRetry),
		define("queue.pause", "unravis-ci queue pause", "Stop machines from taking new requests; running ones go on.", func(env Env, _ *NoArgs) step { return queuePause(env, true) }),
		define("queue.resume", "unravis-ci queue resume", "Let machines take requests again.", func(env Env, _ *NoArgs) step { return queuePause(env, false) }),
	)
}

// NoArgs is the arguments of an action that takes none.
type NoArgs struct{}

// Target names one request: its id, or a pull request number (every request of that pull request).
type Target struct {
	ID string `json:"id,omitempty"`
	PR int    `json:"pr,omitempty"`
}

func (t Target) check() (step, bool) {
	switch {
	case t.PR < 0:
		return fail(Invalid, "a pull request number is 1 or more"), false
	case (t.ID == "") == (t.PR == 0):
		return fail(Invalid, "give one request id or one pull request number"), false
	}
	return step{}, true
}

func (t Target) text(ids []string) string {
	if t.PR > 0 {
		return fmt.Sprintf("pr %d: %s", t.PR, strings.Join(ids, ", "))
	}
	return t.ID
}

// Item is what happened to one request of a queue action.
type Item struct {
	ID      string
	Request localci.Request
	Err     error
}

// Items is the Data of queue.top and queue.cancel.
type Items []Item

func itemsResult(t Target, items Items, line func(localci.Request) string) step {
	st := step{res: Result{Data: items}}
	var lines, ids []string
	for _, it := range items {
		if it.Err != nil {
			st.res.Status = Failed
			lines = append(lines, it.Err.Error())
			ids = append(ids, it.ID)
			continue
		}
		st.res.Changed = true
		lines = append(lines, line(it.Request))
		ids = append(ids, it.Request.ID)
	}
	st.res.Message = strings.Join(lines, "\n")
	st.target = t.text(ids)
	return st
}

// ---------------------------------------------------------------- queue.top

// TopArgs is the arguments of queue.top.
type TopArgs struct{ Target }

// TopLine is how the CLI and the page say one request was moved.
func TopLine(r localci.Request) string {
	return fmt.Sprintf("%s: %s is first in line (priority %d)", r.ID, r.Label(), r.Priority)
}

func queueTop(env Env, a *TopArgs) step {
	if st, ok := a.check(); !ok {
		return st
	}
	q, err := localci.OpenQueue(localci.QueueDir(env.Dir))
	if err != nil {
		return fail(Broken, "%v", err)
	}
	ids := []string{a.ID}
	if a.PR > 0 {
		all, err := q.List()
		if err != nil {
			return fail(Broken, "%v", err)
		}
		ids = nil
		running := false
		for _, r := range all {
			if r.Kind == localci.KindPR && r.Number == a.PR {
				switch r.State {
				case localci.StateQueued:
					ids = append(ids, r.ID)
				case localci.StateRunning:
					running = true
				}
			}
		}
		if len(ids) == 0 {
			st := fail(Refused, "pull request %d has no waiting request", a.PR)
			if running {
				st = fail(Refused, "pull request %d is running; only a waiting request can be moved", a.PR)
			}
			st.target = fmt.Sprintf("pr %d", a.PR)
			return st
		}
	}
	var items Items
	for _, id := range ids {
		r, err := q.MoveToTop(id)
		items = append(items, Item{ID: id, Request: r, Err: err})
	}
	return itemsResult(a.Target, items, TopLine)
}

// ---------------------------------------------------------------- queue.cancel

// CancelArgs is the arguments of queue.cancel. An empty reason says where the cancel came from.
type CancelArgs struct {
	Target
	Reason string `json:"reason,omitempty"`
}

// CancelLine is how the CLI and the page say one request was cancelled.
func CancelLine(r localci.Request) string {
	switch {
	case r.State == localci.StateRunning:
		return fmt.Sprintf("%s: %s is running; its agent has been told to stop it", r.ID, r.Label())
	case r.Active():
		return fmt.Sprintf("%s: cancel requested", r.ID)
	}
	return fmt.Sprintf("%s: %s (%s)", r.ID, r.State, r.Label())
}

func defaultCancelReason(actor string) string {
	switch actor {
	case activitylog.ActorUI:
		return "cancelled from the Homestretch page"
	case activitylog.ActorClaude:
		return "cancelled by Claude"
	}
	return "cancelled from the command line"
}

// maxCancelReason caps the reason kept with a request.
const maxCancelReason = 300

func queueCancel(env Env, a *CancelArgs) step {
	if st, ok := a.check(); !ok {
		return st
	}
	reason := strings.TrimSpace(a.Reason)
	if reason == "" {
		reason = defaultCancelReason(env.Actor)
	}
	if n := len([]rune(reason)); n > maxCancelReason {
		return step{res: Result{Status: Invalid, Field: "reason", Message: fmt.Sprintf("the reason is %d characters; at most %d", n, maxCancelReason)}}
	}
	q, err := localci.OpenQueue(localci.QueueDir(env.Dir))
	if err != nil {
		return fail(Broken, "%v", err)
	}
	ids := []string{a.ID}
	if a.PR > 0 {
		all, err := q.List()
		if err != nil {
			return fail(Broken, "%v", err)
		}
		ids = nil
		for _, r := range all {
			if r.Active() && r.Kind == localci.KindPR && r.Number == a.PR {
				ids = append(ids, r.ID)
			}
		}
		if len(ids) == 0 {
			st := fail(Refused, "pull request %d has no queued or running request", a.PR)
			st.target, st.reason = fmt.Sprintf("pr %d", a.PR), reason
			return st
		}
	}
	var items Items
	for _, id := range ids {
		r, err := q.Cancel(id, reason)
		items = append(items, Item{ID: id, Request: r, Err: err})
	}
	st := itemsResult(a.Target, items, CancelLine)
	st.reason = reason
	return st
}

// ---------------------------------------------------------------- queue.retry

// RetryArgs is the arguments of queue.retry: the id of a finished run, or the start of it. Only the CLI can retry by
// pull request number: that asks GitHub for the pull request's head, and it hands the request it built in Resolved.
type RetryArgs struct {
	Run      string           `json:"run"`
	Resolved *localci.Request `json:"-"`
}

// Retried is the Data of queue.retry.
type Retried struct {
	Request localci.Request
	Added   bool // false: the same request was already queued or running
}

// maxRunID caps the run id a person types.
const maxRunID = 100

// RequestOfRun rebuilds the request that made an index entry, for a rerun of the same commit.
func RequestOfRun(e localci.IndexEntry) (localci.Request, error) {
	r := localci.Request{Repo: e.Repo, Workflow: e.Workflow, Ref: e.HeadRef, Base: e.BaseRef, SHA: e.HeadSHA, Number: e.PR}
	switch e.Event {
	case "pull_request":
		if e.PR <= 0 {
			return r, fmt.Errorf("run %s has no pull request number", e.RunID)
		}
		r.Kind = localci.KindPR
	case "push":
		r.Kind = localci.KindPush
	case "schedule":
		r.Kind, r.Cron = localci.KindSchedule, "rerun"
	default:
		return r, fmt.Errorf("run %s was a %q run; only pull request, push and schedule runs can be run again", e.RunID, e.Event)
	}
	if r.Repo == "" || r.Workflow == "" || r.SHA == "" {
		return r, fmt.Errorf("run %s does not record the repository, workflow and commit", e.RunID)
	}
	return r, nil
}

// shortSHA is the first 8 characters of a commit, as unravis-ci prints it.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// RetryLine is how the CLI and the page say what a retry did.
func RetryLine(d Retried) string {
	if !d.Added {
		return fmt.Sprintf("%s: already %s (%s at %s); nothing added", d.Request.ID, d.Request.State, d.Request.Label(), shortSHA(d.Request.SHA))
	}
	return fmt.Sprintf("%s: queued %s at %s with %s", d.Request.ID, d.Request.Label(), shortSHA(d.Request.SHA), d.Request.Workflow)
}

func queueRetry(env Env, a *RetryArgs) step {
	if a.Resolved == nil {
		a.Run = strings.TrimSpace(a.Run)
		if a.Run == "" {
			return step{res: Result{Status: Invalid, Field: "run", Message: "give the id of a finished run"}}
		}
		if len(a.Run) > maxRunID {
			return step{res: Result{Status: Invalid, Field: "run", Message: "that is not a run id"}}
		}
	}
	store, err := localci.OpenStore(env.Dir)
	if err != nil {
		return fail(Broken, "%v", err)
	}
	q, err := localci.OpenQueue(localci.QueueDir(store.Dir))
	if err != nil {
		return fail(Broken, "%v", err)
	}
	var req localci.Request
	target := a.Run
	if a.Resolved != nil {
		req = *a.Resolved
		target = fmt.Sprintf("pr %d", req.Number)
	} else {
		entries, err := store.ReadIndex()
		if err != nil {
			return fail(Broken, "%v", err)
		}
		var hit []localci.IndexEntry
		for _, e := range entries {
			if e.RunID == a.Run {
				hit = []localci.IndexEntry{e}
				break
			}
			if strings.HasPrefix(e.RunID, a.Run) {
				hit = append(hit, e)
			}
		}
		switch len(hit) {
		case 0:
			st := fail(Refused, "no run %q in %s", a.Run, env.Dir)
			st.target = target
			return st
		case 1:
		default:
			st := fail(Invalid, "%q matches %d runs; give more of the id", a.Run, len(hit))
			st.target = target
			return st
		}
		target = hit[0].RunID
		if req, err = RequestOfRun(hit[0]); err != nil {
			st := fail(Refused, "%v", err)
			st.target = target
			return st
		}
	}
	req.Rerun = true // an answer another PC left for this commit does not stand in the way of a rerun
	got, added, err := q.Enqueue(req)
	if err != nil {
		st := fail(Failed, "%v", err)
		st.target = target
		return st
	}
	d := Retried{Request: got, Added: added}
	return step{res: Result{Message: RetryLine(d), Data: d, Changed: added}, target: target + " -> " + got.ID}
}

// ---------------------------------------------------------------- queue.pause and queue.resume

// Paused is the Data of queue.pause and queue.resume.
type Paused struct {
	Was  bool   // the queue was paused before
	Path string // the pause file
}

func queuePause(env Env, pause bool) step {
	was := localci.IsPaused(env.Dir)
	if err := localci.SetPaused(env.Dir, pause); err != nil {
		return fail(Broken, "%v", err)
	}
	d := Paused{Was: was, Path: localci.PausePath(env.Dir)}
	st := step{res: Result{Data: d, Changed: was != pause}, target: "claiming"}
	switch {
	case pause && was:
		st.res.Message = "claiming was already paused; nothing changed"
	case pause:
		where := ""
		if env.ShowPaths {
			where = " (" + d.Path + ")"
		}
		st.res.Message = "claiming is paused" + where + ": no agent takes a new request; what runs keeps running"
	case was:
		st.res.Message = "claiming is resumed: agents take requests again"
	default:
		st.res.Message = "claiming was not paused; nothing changed"
	}
	return st
}
