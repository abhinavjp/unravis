package localci

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestQueue(t *testing.T) (*Queue, *fakeClock) {
	t.Helper()
	q, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clk := newFakeClock()
	q.Now = func() time.Time { clk.Advance(time.Millisecond); return clk.Now() }
	return q, clk
}

func prReq(n int, sha string) Request {
	return Request{Kind: KindPR, Repo: "o/r", Ref: "feature", Base: "develop", Number: n, SHA: sha, Workflow: "fast-gate.yml"}
}

func mustEnqueue(t *testing.T, q *Queue, r Request) Request {
	t.Helper()
	got, added, err := q.Enqueue(r)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatalf("%s was not added", r.Label())
	}
	return got
}

func TestQueueClaimOrderIsPriorityThenFIFO(t *testing.T) {
	q, _ := newTestQueue(t)
	mustEnqueue(t, q, Request{Kind: KindSchedule, Repo: "o/r", Ref: "main", SHA: "a1", Workflow: "nightly.yml", Cron: "0 1 * * *"})
	mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "main", SHA: "b1", Workflow: "ci.yml"})
	mustEnqueue(t, q, prReq(1, "c1"))
	mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "develop", SHA: "b2", Workflow: "ci.yml"})
	mustEnqueue(t, q, prReq(2, "c2"))
	var got []string
	for {
		r, err := q.Claim("a1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if r == nil {
			break
		}
		got = append(got, r.Label())
	}
	want := []string{"pr 1 into develop", "pr 2 into develop", "push main", "push develop", "schedule nightly.yml (0 1 * * *)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("claim order\n got %v\nwant %v", got, want)
	}
}

func TestQueueClaimHonoursLabels(t *testing.T) {
	q, _ := newTestQueue(t)
	r := prReq(1, "c1")
	r.Labels = []string{"windows", "gpu"}
	mustEnqueue(t, q, r)
	if got, _ := q.Claim("a", []string{"windows"}); got != nil {
		t.Fatalf("an agent without every label got %v", got)
	}
	got, err := q.Claim("b", []string{"gpu", "windows", "extra"})
	if err != nil || got == nil || got.ClaimedBy != "b" || got.State != StateRunning {
		t.Fatalf("claim = %+v, %v", got, err)
	}
}

// Several agents (separate Queue values over one folder, as separate processes would be) claim while others
// list: every request is handed out exactly once.
func TestQueueConcurrentClaimEachExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	q0, err := OpenQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	const items, workers = 60, 8
	for i := 1; i <= items; i++ {
		if _, _, err := q0.Enqueue(prReq(i, fmt.Sprintf("%040d", i))); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[string]string{}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			q, _ := OpenQueue(dir)
			name := fmt.Sprintf("agent%d", w)
			for {
				r, err := q.Claim(name, nil)
				if err != nil {
					t.Errorf("%s: claim: %v", name, err)
					return
				}
				if r == nil {
					return
				}
				mu.Lock()
				if prev, dup := seen[r.ID]; dup {
					t.Errorf("request %s claimed by %s and by %s", r.ID, prev, name)
				}
				seen[r.ID] = name
				mu.Unlock()
			}
		}(w)
	}
	wg.Add(1)
	go func() { // a reader that lists all the time, which on Windows blocks renames of the file it reads
		defer wg.Done()
		q, _ := OpenQueue(dir)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := q.List(); err != nil {
					t.Errorf("list: %v", err)
					return
				}
			}
		}
	}()
	// stop the reader when the claimers are done
	go func() {
		for {
			mu.Lock()
			n := len(seen)
			mu.Unlock()
			if n >= items {
				close(stop)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	wg.Wait()
	if len(seen) != items {
		t.Fatalf("%d of %d requests were claimed", len(seen), items)
	}
	all, err := q0.List()
	if err != nil {
		t.Fatal(err)
	}
	running := 0
	for _, r := range all {
		if r.State == StateRunning && r.ClaimedBy == seen[r.ID] {
			running++
		}
	}
	if running != items {
		t.Fatalf("%d running requests carry their claimer, want %d", running, items)
	}
}

func TestQueueNewHeadSupersedesQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	old := mustEnqueue(t, q, prReq(7, "aaaa"))
	other := mustEnqueue(t, q, prReq(8, "bbbb"))
	newer := mustEnqueue(t, q, prReq(7, "cccc"))
	got, err := q.Get(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateSuperseded || got.SupersededBy != newer.ID {
		t.Fatalf("old request = %+v", got)
	}
	if o, _ := q.Get(other.ID); o.State != StateQueued {
		t.Fatalf("another pull request was touched: %+v", o)
	}
	c1, _ := q.Claim("a", nil)
	c2, _ := q.Claim("a", nil)
	c3, _ := q.Claim("a", nil)
	if c1 == nil || c2 == nil || c3 != nil || c1.ID != other.ID || c2.ID != newer.ID {
		t.Fatalf("claims = %v %v %v", c1, c2, c3)
	}
}

func TestQueueSameHeadIsNotQueuedTwice(t *testing.T) {
	q, _ := newTestQueue(t)
	first := mustEnqueue(t, q, prReq(7, "aaaa"))
	got, added, err := q.Enqueue(prReq(7, "aaaa"))
	if err != nil || added || got.ID != first.ID {
		t.Fatalf("second enqueue = %+v, %v, %v", got, added, err)
	}
	if _, err := q.Claim("a", nil); err != nil {
		t.Fatal(err)
	}
	if _, added, _ := q.Enqueue(prReq(7, "aaaa")); added {
		t.Fatal("a running request was queued again")
	}
}

func TestQueueNewHeadMarksRunningAndCompleteEndsItSuperseded(t *testing.T) {
	q, _ := newTestQueue(t)
	old := mustEnqueue(t, q, prReq(7, "aaaa"))
	run, _ := q.Claim("a", nil)
	if run == nil || run.ID != old.ID {
		t.Fatal("not claimed")
	}
	newer := mustEnqueue(t, q, prReq(7, "bbbb"))
	st, _, ok := q.CancelRequested(old.ID)
	if !ok || st != StateSuperseded {
		t.Fatalf("marker = %q %v", st, ok)
	}
	if got, _ := q.Get(old.ID); got.State != StateRunning {
		t.Fatalf("a running request must stay running until its agent ends it: %+v", got)
	}
	done, err := q.Complete(old.ID, "run1", "cancelled", "")
	if err != nil {
		t.Fatal(err)
	}
	if done.State != StateSuperseded || done.RunID != "run1" {
		t.Fatalf("completed = %+v", done)
	}
	if _, _, ok := q.CancelRequested(old.ID); ok {
		t.Fatal("marker left behind")
	}
	if got, _ := q.Claim("a", nil); got == nil || got.ID != newer.ID {
		t.Fatalf("the newer request is not next: %v", got)
	}
}

func TestQueuePushesToTheSameBranchSupersede(t *testing.T) {
	q, _ := newTestQueue(t)
	a := mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "develop", SHA: "a1", Workflow: "ci.yml"})
	mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "master", SHA: "m1", Workflow: "ci.yml"})
	mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "develop", SHA: "a2", Workflow: "ci.yml"})
	if got, _ := q.Get(a.ID); got.State != StateSuperseded {
		t.Fatalf("older push = %+v", got)
	}
	n := 0
	for _, r := range mustList(t, q) {
		if r.State == StateQueued {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d queued, want 2", n)
	}
}

func mustList(t *testing.T, q *Queue) []Request {
	t.Helper()
	rs, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestQueueCancel(t *testing.T) {
	q, _ := newTestQueue(t)
	a := mustEnqueue(t, q, prReq(1, "a1"))
	b := mustEnqueue(t, q, prReq(2, "b1"))
	got, err := q.Cancel(a.ID, "no longer needed")
	if err != nil || got.State != StateCancelled {
		t.Fatalf("cancel queued = %+v, %v", got, err)
	}
	if c, _ := q.Claim("x", nil); c == nil || c.ID != b.ID {
		t.Fatalf("claimed %v", c)
	}
	if _, err := q.Cancel(b.ID, "stop"); err != nil { // running: marker only
		t.Fatal(err)
	}
	if st, why, ok := q.CancelRequested(b.ID); !ok || st != StateCancelled || why != "stop" {
		t.Fatalf("marker = %q %q %v", st, why, ok)
	}
	done, err := q.Complete(b.ID, "", "cancelled", "")
	if err != nil || done.State != StateCancelled || done.Reason != "stop" {
		t.Fatalf("complete = %+v, %v", done, err)
	}
	if _, err := q.Complete(b.ID, "", "success", ""); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("second complete = %v", err)
	}
	if _, err := q.Cancel("q-nope", ""); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("cancel unknown = %v", err)
	}
	if _, err := q.Cancel("../x", ""); err == nil {
		t.Fatal("a path was accepted as a request id")
	}
}

func TestQueueRequeueStale(t *testing.T) {
	q, _ := newTestQueue(t)
	mustEnqueue(t, q, prReq(1, "a1"))
	mustEnqueue(t, q, prReq(2, "b1"))
	c1, _ := q.Claim("dead", nil)
	c2, _ := q.Claim("alive", nil)
	moved, err := q.RequeueStale(func(a string) bool { return a == "alive" })
	if err != nil || len(moved) != 1 || moved[0].ID != c1.ID {
		t.Fatalf("moved = %v, %v", moved, err)
	}
	if got, _ := q.Get(c1.ID); got.State != StateQueued || got.ClaimedBy != "" {
		t.Fatalf("requeued = %+v", got)
	}
	if got, _ := q.Get(c2.ID); got.State != StateRunning {
		t.Fatalf("a live agent's request moved: %+v", got)
	}
	// three crashes end the request as an error
	for i := 0; i < 3; i++ {
		if c, _ := q.Claim("dead", nil); c == nil {
			break
		}
		if _, err := q.RequeueStale(func(string) bool { return false }); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Get(c1.ID)
	if got.State != StateDone || got.Conclusion != "error" {
		t.Fatalf("after repeated crashes: %+v", got)
	}
}

func TestQueuePrune(t *testing.T) {
	q, clk := newTestQueue(t)
	a := mustEnqueue(t, q, prReq(1, "a1"))
	if _, err := q.Cancel(a.ID, ""); err != nil {
		t.Fatal(err)
	}
	clk.Advance(48 * time.Hour)
	b := mustEnqueue(t, q, prReq(2, "b1"))
	if _, err := q.Cancel(b.ID, ""); err != nil {
		t.Fatal(err)
	}
	n, err := q.Prune(clk.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	var ids []string
	for _, r := range mustList(t, q) {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	if len(ids) != 1 || ids[0] != b.ID {
		t.Fatalf("left %v", ids)
	}
}

func TestQueueRejectsBadRequests(t *testing.T) {
	q, _ := newTestQueue(t)
	for name, r := range map[string]Request{
		"no number":   {Kind: KindPR, Repo: "o/r", Workflow: "w.yml"},
		"no branch":   {Kind: KindPush, Repo: "o/r", Workflow: "w.yml"},
		"no workflow": {Kind: KindPR, Repo: "o/r", Number: 1},
		"bad kind":    {Kind: "x", Repo: "o/r", Workflow: "w.yml"},
	} {
		if _, _, err := q.Enqueue(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMoveToTopTakesTheNextClaimOverPRsAndPushes(t *testing.T) {
	q, _ := newTestQueue(t)
	mustEnqueue(t, q, prReq(1, "a1"))
	push := mustEnqueue(t, q, Request{Kind: KindPush, Repo: "o/r", Ref: "develop", SHA: "b1", Workflow: "ci.yml"})
	mustEnqueue(t, q, prReq(2, "c1"))
	got, err := q.MoveToTop(push.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != PriorityPR-1 {
		t.Fatalf("priority %d, want %d (below the lowest queued value)", got.Priority, PriorityPR-1)
	}
	next, err := q.Claim("a", nil)
	if err != nil || next == nil || next.ID != push.ID {
		t.Fatalf("the moved push was not claimed first: %+v, %v", next, err)
	}
	if next.Kind != KindPush || next.SHA != "b1" || next.EnqueuedAt.IsZero() {
		t.Fatalf("a move changed more than the priority: %+v", next)
	}
}

func TestMoveToTopTwiceKeepsTheLaterOneInFront(t *testing.T) {
	q, _ := newTestQueue(t)
	a := mustEnqueue(t, q, prReq(1, "a1"))
	b := mustEnqueue(t, q, prReq(2, "b1"))
	if _, err := q.MoveToTop(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MoveToTop(a.ID); err != nil {
		t.Fatal(err)
	}
	rs, _ := q.List()
	if rs[0].ID != a.ID || rs[1].ID != b.ID {
		t.Fatalf("order after moving b then a: %s, %s", rs[0].ID, rs[1].ID)
	}
	again, err := q.MoveToTop(a.ID)
	if err != nil || again.Priority != rs[0].Priority {
		t.Fatalf("moving the first request again must change nothing: %+v %v", again, err)
	}
}

func TestMoveToTopOfAnEqualPriorityRequestThatWasEnqueuedFirstStillGoesBelow(t *testing.T) {
	q, _ := newTestQueue(t)
	a := mustEnqueue(t, q, prReq(1, "a1"))
	mustEnqueue(t, q, prReq(2, "b1"))
	got, err := q.MoveToTop(a.ID)
	if err != nil || got.Priority >= PriorityPR {
		t.Fatalf("a request tied for first must be made strictly first: %+v %v", got, err)
	}
}

func TestMoveToTopRefusesARunningOrFinishedRequestAndAnUnknownOne(t *testing.T) {
	q, _ := newTestQueue(t)
	run := mustEnqueue(t, q, prReq(1, "a1"))
	done := mustEnqueue(t, q, prReq(2, "b1"))
	waiting := mustEnqueue(t, q, prReq(3, "c1"))
	if c, _ := q.Claim("a", nil); c == nil || c.ID != run.ID {
		t.Fatalf("setup: %+v", c)
	}
	if _, err := q.Cancel(done.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"running": run.ID, "finished": done.ID} {
		_, err := q.MoveToTop(id)
		if !errors.Is(err, ErrNotQueued) {
			t.Fatalf("a %s request: err = %v, want ErrNotQueued", name, err)
		}
	}
	if _, err := q.MoveToTop("q-20260101-000000-000-000000"); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := q.MoveToTop("../x"); err == nil {
		t.Fatal("a path as an id must be refused")
	}
	if g, _ := q.Get(waiting.ID); g.Priority != PriorityPR {
		t.Fatalf("a refused move changed another request: %+v", g)
	}
}

func TestMoveToTopLeavesNoMarkerOrTempFileBehind(t *testing.T) {
	q, _ := newTestQueue(t)
	mustEnqueue(t, q, prReq(1, "a1"))
	b := mustEnqueue(t, q, prReq(2, "b1"))
	if _, err := q.MoveToTop(b.ID); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"queued", "running"} {
		ents, err := os.ReadDir(filepath.Join(q.Dir, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if f == "running" || strings.HasPrefix(e.Name(), ".") {
				t.Fatalf("left behind in %s: %s", f, e.Name())
			}
		}
	}
	if _, err := q.Claim("a", nil); err != nil {
		t.Fatal(err)
	}
}

func TestMoveToTopWhileOthersClaimLosesNoRequest(t *testing.T) {
	q, _ := newTestQueue(t)
	var ids []string
	for i := 1; i <= 6; i++ {
		ids = append(ids, mustEnqueue(t, q, prReq(i, fmt.Sprintf("s%d", i))).ID)
	}
	var wg sync.WaitGroup
	claimed := make(chan string, 6)
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := q.Claim(fmt.Sprintf("agent%d", w), nil)
				if err != nil {
					t.Error(err)
					return
				}
				if c == nil {
					return
				}
				claimed <- c.ID
			}
		}()
	}
	for _, id := range ids {
		if _, err := q.MoveToTop(id); err != nil && !errors.Is(err, ErrNotQueued) {
			t.Error(err)
		}
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("%s was claimed twice", id)
		}
		seen[id] = true
	}
	queued, running := q.Depth()
	if len(seen)+queued != 6 || running != len(seen) {
		t.Fatalf("lost requests: claimed %d, queued %d, running %d", len(seen), queued, running)
	}
}
