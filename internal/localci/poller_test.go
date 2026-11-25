package localci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// watchStub gives a test fake the read calls it does not use.
type watchStub struct{}

func (watchStub) OpenPullRequests(context.Context, string, string) (PullList, error) {
	return PullList{}, errors.New("not used")
}

func (watchStub) BranchTip(context.Context, string, string, string) (BranchInfo, error) {
	return BranchInfo{}, errors.New("not used")
}

func (watchStub) FileAt(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("not used")
}

func (watchStub) ListDir(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("not used")
}

// watchHost is a repository in memory: pull requests, branch tips and files, with real etag behaviour.
type watchHost struct {
	mu      sync.Mutex
	prs     []PullRequest
	prRev   int
	tips    map[string]string
	tipRev  map[string]int
	files   map[string]string // "sha:path" or "branch:path"
	asked   int               // calls that were answered in full
	notMod  int
	tipErr  error
	listErr error
}

func newWatchHost() *watchHost {
	return &watchHost{tips: map[string]string{}, tipRev: map[string]int{}, files: map[string]string{}}
}

func (h *watchHost) PullRequest(context.Context, string, int) (PullRequest, error) {
	return PullRequest{}, errors.New("not used")
}
func (h *watchHost) Collaborators(context.Context, string) ([]string, error)  { return nil, nil }
func (h *watchHost) Commits(context.Context, string, int) ([]PRCommit, error) { return nil, nil }
func (h *watchHost) CloneURL(string) string                                   { return "" }

func (h *watchHost) setPR(pr PullRequest) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.prs {
		if h.prs[i].Number == pr.Number {
			h.prs[i] = pr
			h.prRev++
			return
		}
	}
	h.prs = append(h.prs, pr)
	h.prRev++
}

func (h *watchHost) closePR(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.prs {
		if h.prs[i].Number == n {
			h.prs = append(h.prs[:i], h.prs[i+1:]...)
			h.prRev++
			return
		}
	}
}

func (h *watchHost) setTip(branch, sha string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tips[branch] = sha
	h.tipRev[branch]++
}

func (h *watchHost) OpenPullRequests(_ context.Context, _, etag string) (PullList, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listErr != nil {
		return PullList{}, h.listErr
	}
	tag := fmt.Sprintf("W/%d", h.prRev)
	if etag == tag {
		h.notMod++
		return PullList{ETag: tag, NotModified: true}, nil
	}
	h.asked++
	return PullList{PRs: append([]PullRequest(nil), h.prs...), ETag: tag}, nil
}

func (h *watchHost) BranchTip(_ context.Context, _, branch, etag string) (BranchInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tipErr != nil {
		return BranchInfo{}, h.tipErr
	}
	sha, ok := h.tips[branch]
	if !ok {
		return BranchInfo{}, fmt.Errorf("%w: %s", ErrNoBranch, branch)
	}
	tag := fmt.Sprintf("W/%s/%d", branch, h.tipRev[branch])
	if etag == tag {
		h.notMod++
		return BranchInfo{ETag: tag, NotModified: true}, nil
	}
	h.asked++
	return BranchInfo{SHA: sha, ETag: tag}, nil
}

func (h *watchHost) FileAt(_ context.Context, _, ref, path string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v, ok := h.files[ref+":"+path]; ok {
		return []byte(v), nil
	}
	if v, ok := h.files["*:"+path]; ok {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("no file %s at %s", path, ref)
}

func (h *watchHost) ListDir(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"
const sha3 = "3333333333333333333333333333333333333333"

const pushWF = "name: gate\non:\n  pull_request:\n    branches: [develop]\n  push:\n    branches: [develop]\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
const prOnlyWF = "name: gate\non:\n  pull_request:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
const cronWF = "name: nightly\non:\n  schedule:\n    - cron: '47 1 * * *'\n    - cron: '*/30 * * * *'\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"

func newTestPoller(t *testing.T) (*Poller, *watchHost, *Queue, *fakeClock, *bytes.Buffer) {
	t.Helper()
	q, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clk := newFakeClock()
	h := newWatchHost()
	var log bytes.Buffer
	p := &Poller{Host: h, Queue: q, Repo: "o/r", Now: clk.Now, Log: &log,
		PRWorkflows: map[string]string{"develop": "gate.yml", "master": "full.yml"}, PushWorkflows: map[string][]string{},
		Quiet: time.Minute}
	return p, h, q, clk, &log
}

func queuedOf(t *testing.T, q *Queue) []Request {
	t.Helper()
	var out []Request
	for _, r := range mustList(t, q) {
		if r.State == StateQueued {
			out = append(out, r)
		}
	}
	return out
}

func TestPollerNewPullRequestHeadsAreQueuedOnce(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	ctx := context.Background()
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha1})
	h.setPR(PullRequest{Number: 9, State: "OPEN", BaseRef: "master", HeadRef: "g", HeadSHA: sha2})
	h.setPR(PullRequest{Number: 7, State: "OPEN", BaseRef: "other", HeadRef: "x", HeadSHA: sha3}) // not a watched base
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	got := queuedOf(t, q)
	if len(got) != 2 || got[0].Number != 5 || got[1].Number != 9 {
		// both are pull request gates, first in first out: the catch-up enqueues the highest number first
		t.Logf("queue: %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("%d queued, want 2 (no request for a base that is not watched)", len(got))
	}
	for _, r := range got {
		if r.Number == 9 && r.Workflow != "full.yml" || r.Number == 5 && r.Workflow != "gate.yml" {
			t.Errorf("wrong workflow: %+v", r)
		}
	}
	// no change: the host answers "not modified" and nothing new is queued
	if err := p.Poll(ctx); err != nil || h.notMod == 0 || len(queuedOf(t, q)) != 2 {
		t.Fatalf("second poll: err %v, not-modified answers %d, queued %d", err, h.notMod, len(queuedOf(t, q)))
	}
	// a changed list with the same heads queues nothing
	h.setPR(PullRequest{Number: 11, State: "OPEN", BaseRef: "other", HeadRef: "y", HeadSHA: sha1})
	if err := p.Poll(ctx); err != nil || len(queuedOf(t, q)) != 2 {
		t.Fatalf("unrelated change: err %v, queued %d", err, len(queuedOf(t, q)))
	}
}

func TestPollerNewHeadSupersedesAndClosedPullRequestIsCancelled(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	ctx := context.Background()
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha1})
	_ = p.Poll(ctx)
	first := queuedOf(t, q)[0]
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha2})
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if old, _ := q.Get(first.ID); old.State != StateSuperseded {
		t.Fatalf("old head: %+v", old)
	}
	now := queuedOf(t, q)
	if len(now) != 1 || now[0].SHA != sha2 {
		t.Fatalf("queued after the new head: %+v", now)
	}
	h.closePR(5)
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if left := queuedOf(t, q); len(left) != 0 {
		t.Fatalf("a closed pull request is still queued: %+v", left)
	}
	if got, _ := q.Get(now[0].ID); got.State != StateCancelled {
		t.Fatalf("closed pull request request: %+v", got)
	}
}

func TestPollerCatchUpSkipsHeadsWithAResult(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha1})
	h.setPR(PullRequest{Number: 6, State: "OPEN", BaseRef: "develop", HeadRef: "g", HeadSHA: sha2})
	p.Ran = func(r Request) bool { return r.Repo == p.Repo && r.Number == 5 && r.SHA == sha1 }
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := queuedOf(t, q)
	if len(got) != 1 || got[0].Number != 6 {
		t.Fatalf("queued: %+v", got)
	}
	// a new head of pull request 5 is new work even though the older head has a result
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha3})
	_ = p.Poll(context.Background())
	if n := len(queuedOf(t, q)); n != 2 {
		t.Fatalf("%d queued after a new head, want 2", n)
	}
}

func TestPollerCatchUpQueuesNewestPullRequestFirst(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	for _, n := range []int{3, 8, 5} {
		h.setPR(PullRequest{Number: n, State: "OPEN", BaseRef: "develop", HeadRef: fmt.Sprint("b", n), HeadSHA: sha1})
	}
	_ = p.Poll(context.Background())
	var got []int
	for {
		r, _ := q.Claim("a", nil)
		if r == nil {
			break
		}
		got = append(got, r.Number)
	}
	if fmt.Sprint(got) != "[8 5 3]" {
		t.Fatalf("claim order %v, want newest first", got)
	}
}

func TestPollerPushWaitsForTheQuietPeriod(t *testing.T) {
	p, h, q, clk, log := newTestPoller(t)
	ctx := context.Background()
	p.PRWorkflows = map[string]string{"develop": "gate.yml"}
	p.PushWorkflows = pushOf(p.PRWorkflows)
	h.files["*:.github/workflows/gate.yml"] = pushWF
	h.setTip("develop", sha1)
	if err := p.Poll(ctx); err != nil { // first look: the tip has no result, so it is queued at once (catch-up)
		t.Fatal(err)
	}
	got := queuedOf(t, q)
	if len(got) != 1 || got[0].Kind != KindPush || got[0].SHA != sha1 || got[0].Ref != "develop" || got[0].Workflow != "gate.yml" {
		t.Fatalf("first look: %+v", got)
	}
	// the branch moves: nothing yet
	clk.Advance(10 * time.Second)
	h.setTip("develop", sha2)
	_ = p.Poll(ctx)
	clk.Advance(50 * time.Second)
	_ = p.Poll(ctx)
	if n := len(queuedOf(t, q)); n != 1 {
		t.Fatalf("queued inside the quiet period: %d\n%s", n, log)
	}
	// another push resets the timer
	h.setTip("develop", sha3)
	_ = p.Poll(ctx)
	clk.Advance(59 * time.Second)
	_ = p.Poll(ctx)
	if n := len(queuedOf(t, q)); n != 1 {
		t.Fatalf("queued before 60 s of quiet after the last change: %d", n)
	}
	clk.Advance(2 * time.Second)
	_ = p.Poll(ctx)
	got = queuedOf(t, q)
	// the first push request was still queued, so the new head replaced it
	if len(got) != 1 || got[0].SHA != sha3 {
		t.Fatalf("after the quiet period: %+v", got)
	}
	// and it is queued only once
	_ = p.Poll(ctx)
	clk.Advance(5 * time.Minute)
	_ = p.Poll(ctx)
	if n := len(queuedOf(t, q)); n != 1 {
		t.Fatalf("queued again without a change: %d", n)
	}
}

// The push map, not the workflow's own on:, says which workflow serves a branch (BL-586d, Blocker R): a gate that
// GitHub no longer starts on a push (pull_request only, or workflow_dispatch only) is still queued for its branch
// tip. The branch filter of a push trigger, when there is one, still applies.
func TestPollerPushRunsAConfiguredWorkflowWhateverItsOnSays(t *testing.T) {
	const dispatchOnlyWF = "name: gate\non:\n  workflow_dispatch:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
	for name, wf := range map[string]string{"pull_request only": prOnlyWF, "workflow_dispatch only": dispatchOnlyWF} {
		t.Run(name, func(t *testing.T) {
			p, h, q, clk, log := newTestPoller(t)
			p.PRWorkflows = map[string]string{"develop": "gate.yml"}
			p.PushWorkflows = pushOf(p.PRWorkflows)
			h.files["*:.github/workflows/gate.yml"] = wf
			h.setTip("develop", sha1)
			if err := p.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := queuedOf(t, q)
			if len(got) != 1 || got[0].Kind != KindPush || got[0].SHA != sha1 || got[0].Ref != "develop" || got[0].Workflow != "gate.yml" {
				t.Fatalf("queued %+v\n%s", got, log)
			}
			if !strings.Contains(log.String(), "has no push trigger; it is named for pushes to this branch") {
				t.Fatalf("the log does not say why a workflow without a push trigger runs:\n%s", log)
			}
			// the note is made once, and the tip is queued once
			clk.Advance(5 * time.Minute)
			_ = p.Poll(context.Background())
			if n := len(queuedOf(t, q)); n != 1 || strings.Count(log.String(), "has no push trigger") != 1 {
				t.Fatalf("queued %d, note repeated:\n%s", n, log)
			}
		})
	}
}

// A push to a branch the push map does not name is never queued, whatever the workflow says.
func TestPollerPushIgnoresABranchTheMapDoesNotName(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	p.PRWorkflows = map[string]string{"develop": "gate.yml"}
	p.PushWorkflows = map[string][]string{"release": {"gate.yml"}}
	h.files["*:.github/workflows/gate.yml"] = pushWF
	h.setTip("develop", sha1)
	h.setTip("release", sha1)
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range queuedOf(t, q) {
		if r.Kind == KindPush && r.Ref == "develop" { // release is queued, develop is not
			t.Fatalf("a push to a branch outside the push map was queued: %+v", r)
		}
	}
}

// A push trigger's own branch filter still applies when the workflow has one.
func TestPollerPushBranchFilterStillApplies(t *testing.T) {
	p, h, q, _, log := newTestPoller(t)
	p.PRWorkflows = map[string]string{"develop": "gate.yml"}
	p.PushWorkflows = pushOf(p.PRWorkflows)
	h.files["*:.github/workflows/gate.yml"] = strings.Replace(pushWF, "push:\n    branches: [develop]", "push:\n    branches: [release/**]", 1)
	h.setTip("develop", sha1)
	_ = p.Poll(context.Background())
	if n := len(queuedOf(t, q)); n != 0 || !strings.Contains(log.String(), "only for release/**") {
		t.Fatalf("queued %d\n%s", n, log)
	}
}

func TestPollerPushCatchUpSkipsATipWithAResult(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	p.PRWorkflows = map[string]string{"develop": "gate.yml"}
	p.PushWorkflows = pushOf(p.PRWorkflows)
	h.files["*:.github/workflows/gate.yml"] = pushWF
	h.setTip("develop", sha1)
	p.Ran = func(r Request) bool { return r.Repo == p.Repo && r.Kind == KindPush && r.SHA == sha1 }
	_ = p.Poll(context.Background())
	if n := len(queuedOf(t, q)); n != 0 {
		t.Fatalf("%d queued for a tip that already ran", n)
	}
}

func TestPollerReportsAMissingBranchOnceAndKeepsGoing(t *testing.T) {
	p, h, q, _, _ := newTestPoller(t)
	p.PushWorkflows = pushOf(p.PRWorkflows)
	h.files["*:.github/workflows/gate.yml"] = pushWF
	h.setTip("develop", sha1) // master does not exist
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha1})
	err := p.Poll(context.Background())
	if !errors.Is(err, ErrNoBranch) {
		t.Fatalf("error = %v", err)
	}
	if n := len(queuedOf(t, q)); n != 2 { // the pull request and the develop push were still queued
		t.Fatalf("%d queued", n)
	}
	if err := p.Poll(context.Background()); err != nil { // the same problem is not repeated
		t.Fatalf("repeat error: %v", err)
	}
}

func TestPollerSchedule(t *testing.T) {
	p, h, q, clk, _ := newTestPoller(t)
	ctx := context.Background()
	p.PRWorkflows = nil
	p.ScheduleBranch, p.ScheduleWorkflows = "main", []string{"nightly.yml"}
	p.StatePath = filepath.Join(t.TempDir(), "poller.json")
	h.setTip("main", sha1)
	h.files["*:.github/workflows/nightly.yml"] = cronWF
	clk.t = time.Date(2026, 11, 25, 0, 40, 0, 0, time.UTC)
	if err := p.Poll(ctx); err != nil { // first look: nothing fires, the clock starts here
		t.Fatal(err)
	}
	if n := len(queuedOf(t, q)); n != 0 {
		t.Fatalf("%d queued at the first look", n)
	}
	clk.Advance(5 * time.Minute) // 00:45: */30 fired at 00:30? no: the clock started at 00:40
	_ = p.Poll(ctx)
	if n := len(queuedOf(t, q)); n != 0 {
		t.Fatalf("%d queued at 00:45", n)
	}
	clk.Advance(20 * time.Minute) // 01:05: */30 at 01:00
	_ = p.Poll(ctx)
	got := queuedOf(t, q)
	if len(got) != 1 || got[0].Kind != KindSchedule || got[0].Cron != "*/30 * * * *" || got[0].SHA != sha1 || got[0].Ref != "main" || got[0].Priority != PrioritySchedule {
		t.Fatalf("at 01:05: %+v", got)
	}
	// a restart (a new poller over the same state file) a minute later does not fire it again
	p2 := &Poller{Host: p.Host, Queue: p.Queue, Repo: p.Repo, ScheduleBranch: p.ScheduleBranch, ScheduleWorkflows: p.ScheduleWorkflows,
		StatePath: p.StatePath, Now: p.Now}
	clk.Advance(time.Minute)
	if err := p2.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(queuedOf(t, q)); n != 1 {
		t.Fatalf("restart fired again: %d queued", n)
	}
	// an absence of 30 hours is one run per entry, not a flood
	clk.Advance(30 * time.Hour)
	_ = p2.Poll(ctx)
	n := 0
	for _, r := range mustList(t, q) {
		if r.Kind == KindSchedule && r.State == StateQueued {
			n++
		}
	}
	if n != 2 { // 30 hours fired each entry once; the */30 request of 01:00 was still queued with the same commit, so it stands for the new one
		t.Fatalf("%d schedule requests after a long absence, want 2", n)
	}
}

func TestPollerScheduleFailsLoudlyOnUnsupportedCron(t *testing.T) {
	p, h, _, _, _ := newTestPoller(t)
	p.PRWorkflows = nil
	p.ScheduleBranch, p.ScheduleWorkflows = "main", []string{"nightly.yml"}
	h.setTip("main", sha1)
	h.files["*:.github/workflows/nightly.yml"] = strings.Replace(cronWF, "'47 1 * * *'", "'@daily'", 1)
	err := p.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nightly.yml") || !strings.Contains(err.Error(), "5 fields") {
		t.Fatalf("error = %v", err)
	}
	if _, serr := p.Schedules(context.Background()); serr == nil {
		t.Fatal("Schedules hid the problem")
	}
}

func TestPollerSchedulesListNextTimes(t *testing.T) {
	p, h, _, clk, _ := newTestPoller(t)
	p.ScheduleBranch, p.ScheduleWorkflows = "main", []string{"nightly.yml"}
	h.setTip("main", sha1)
	h.files["*:.github/workflows/nightly.yml"] = cronWF
	clk.t = time.Date(2026, 11, 25, 10, 10, 0, 0, time.UTC)
	got, err := p.Schedules(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if !got[0].Next.Equal(time.Date(2026, 11, 26, 1, 47, 0, 0, time.UTC)) || !got[1].Next.Equal(time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("%v", got)
	}
}

func TestPollerRunStopsWithTheContext(t *testing.T) {
	p, h, _, _, _ := newTestPoller(t)
	h.setPR(PullRequest{Number: 5, State: "OPEN", BaseRef: "develop", HeadRef: "f", HeadSHA: sha1})
	p.Interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if h.asked == 0 {
		t.Fatal("Run never polled")
	}
}

func pushOf(m map[string]string) map[string][]string {
	out := map[string][]string{}
	for b, wf := range m {
		out[b] = []string{wf}
	}
	return out
}
