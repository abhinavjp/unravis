package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type agentRig struct {
	t     *testing.T
	q     *Queue
	dir   string
	agent *Agent
	log   *syncBuf
	done  chan error
	stop  context.CancelFunc
}

// newAgentRig builds an agent over a temp state folder; the runner is the test's.
func newAgentRig(t *testing.T, name string, slots int, runner Runner) *agentRig {
	t.Helper()
	dir := t.TempDir()
	q, err := OpenQueue(filepath.Join(dir, "queue"))
	if err != nil {
		t.Fatal(err)
	}
	log := &syncBuf{}
	return &agentRig{t: t, q: q, dir: dir, log: log, done: make(chan error, 1), agent: &Agent{
		Name: name, Dir: dir, Queue: q, Slots: slots, Runner: runner, ClaimEvery: 5 * time.Millisecond, BeatEvery: 50 * time.Millisecond, Log: log,
	}}
}

func (r *agentRig) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	go func() { r.done <- r.agent.Run(ctx) }()
	r.t.Cleanup(func() {
		cancel()
		select {
		case err := <-r.done:
			_ = err
		case <-time.After(10 * time.Second):
			r.t.Error("the agent did not stop")
		}
	})
}

func (r *agentRig) wait() error {
	r.t.Helper()
	select {
	case err := <-r.done:
		r.done <- err
		return err
	case <-time.After(10 * time.Second):
		r.t.Fatal("the agent did not return")
		return nil
	}
}

func (r *agentRig) get(id string) Request {
	r.t.Helper()
	got, err := r.q.Get(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

func TestAgentRunsRequestsAndRespectsSlots(t *testing.T) {
	var cur, peak atomic.Int32
	release := make(chan struct{})
	var ran sync.Map
	rig := newAgentRig(t, "a1", 2, func(ctx context.Context, r Request) (Outcome, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer cur.Add(-1)
		ran.Store(r.ID, true)
		select {
		case <-release:
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		}
		return Outcome{RunID: "run-" + r.ID, Conclusion: "success"}, nil
	})
	var ids []string
	for i := 1; i <= 5; i++ {
		ids = append(ids, mustEnqueue(t, rig.q, prReq(i, fmt.Sprintf("%040d", i))).ID)
	}
	rig.start()
	waitFor(t, "two running", func() bool { return cur.Load() == 2 })
	time.Sleep(60 * time.Millisecond) // a third one must not start
	if cur.Load() != 2 {
		t.Fatalf("%d running with 2 slots", cur.Load())
	}
	hb, _ := ReadAgents(rig.dir)
	if len(hb) != 1 || len(hb[0].Jobs) != 2 || hb[0].Slots != 2 || hb[0].State != "ready" {
		t.Fatalf("heartbeat with two jobs: %+v", hb)
	}
	close(release)
	waitFor(t, "all done", func() bool {
		for _, id := range ids {
			if rig.get(id).State != StateDone {
				return false
			}
		}
		return true
	})
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency %d, want 2", peak.Load())
	}
	for _, id := range ids {
		if g := rig.get(id); g.Conclusion != "success" || g.RunID != "run-"+id || g.ClaimedBy != "a1" {
			t.Fatalf("%s: %+v", id, g)
		}
	}
}

func TestAgentCancelsARunningRequestThroughItsContext(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return Outcome{RunID: "r1", Conclusion: "cancelled", Reason: "stopped"}, nil
	})
	req := mustEnqueue(t, rig.q, prReq(7, strings.Repeat("a", 40)))
	rig.start()
	<-started
	if _, err := rig.q.Cancel(req.ID, "by the owner"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner context was not cancelled")
	}
	waitFor(t, "cancelled", func() bool { return rig.get(req.ID).State == StateCancelled })
	if g := rig.get(req.ID); g.Reason != "by the owner" || g.RunID != "r1" {
		t.Fatalf("%+v", g)
	}
}

func TestAgentLetsARunningRequestFinishWhenANewerHeadArrives(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		started <- r.SHA
		if r.SHA[0] == 'a' {
			select {
			case <-release:
			case <-ctx.Done():
				return Outcome{Conclusion: "cancelled"}, nil
			}
		}
		return Outcome{Conclusion: "success"}, nil
	})
	old := mustEnqueue(t, rig.q, prReq(7, strings.Repeat("a", 40)))
	rig.start()
	if sha := <-started; sha[0] != 'a' {
		t.Fatal("old head should start first")
	}
	newer := mustEnqueue(t, rig.q, prReq(7, strings.Repeat("b", 40)))
	if _, _, ok := rig.q.CancelRequested(old.ID); ok {
		t.Fatal("old run marked")
	}
	close(release)
	waitFor(t, "old done", func() bool { return rig.get(old.ID).State == StateDone })
	if g := rig.get(old.ID); g.Conclusion != "success" || g.SupersededBy != "" {
		t.Fatalf("old = %+v", g)
	}
	waitFor(t, "new done", func() bool { return rig.get(newer.ID).State == StateDone })
	if sha := <-started; sha[0] != 'b' {
		t.Fatal("new head did not run")
	}
}
func TestAgentDrainFinishesRunningWorkAndClaimsNothingNew(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		started <- struct{}{}
		<-release
		return Outcome{Conclusion: "success"}, nil
	})
	first := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	second := mustEnqueue(t, rig.q, prReq(2, strings.Repeat("b", 40)))
	rig.start()
	<-started
	rig.agent.Drain()
	waitFor(t, "draining state", func() bool {
		hb, _ := ReadAgents(rig.dir)
		return len(hb) == 1 && hb[0].State == "draining"
	})
	select {
	case err := <-rig.done:
		t.Fatalf("returned before the running request ended: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	if err := rig.wait(); err != nil {
		t.Fatal(err)
	}
	if g := rig.get(first.ID); g.State != StateDone {
		t.Fatalf("the running request must finish: %+v", g)
	}
	if g := rig.get(second.ID); g.State != StateQueued {
		t.Fatalf("a drained agent must leave the next request queued: %+v", g)
	}
	if hb, _ := ReadAgents(rig.dir); len(hb) != 0 {
		t.Fatalf("the heartbeat file must be gone after a drain: %+v", hb)
	}
}

func TestAgentDrainMarkFromAnotherProcess(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.start()
	waitFor(t, "heartbeat", func() bool { hb, _ := ReadAgents(rig.dir); return len(hb) == 1 })
	if err := RequestDrain(rig.dir, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := rig.wait(); err != nil {
		t.Fatal(err)
	}
	if err := RequestDrain(rig.dir, "../x"); err == nil {
		t.Fatal("a path as an agent name must be refused")
	}
}

func TestAgentHardStopPutsRunningRequestsBack(t *testing.T) {
	started := make(chan struct{})
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		close(started)
		<-ctx.Done()
		return Outcome{Conclusion: "cancelled"}, nil
	})
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	<-started
	rig.stop()
	if err := rig.wait(); err != nil {
		t.Fatal(err)
	}
	if g := rig.get(req.ID); g.State != StateQueued || g.ClaimedBy != "" {
		t.Fatalf("a request cut off by the agent stopping must be queued again: %+v", g)
	}
}

func TestAgentPausedHookStopsClaiming(t *testing.T) {
	var paused atomic.Bool
	paused.Store(true)
	rig := newAgentRig(t, "a1", 2, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.agent.Paused = paused.Load
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	waitFor(t, "paused heartbeat", func() bool {
		hb, _ := ReadAgents(rig.dir)
		return len(hb) == 1 && hb[0].State == "paused"
	})
	time.Sleep(60 * time.Millisecond)
	if g := rig.get(req.ID); g.State != StateQueued {
		t.Fatalf("a paused agent claimed: %+v", g)
	}
	paused.Store(false)
	waitFor(t, "done after resume", func() bool { return rig.get(req.ID).State == StateDone })
}

func TestAgentTakesBackRequestsOfAnAgentThatIsGone(t *testing.T) {
	rig := newAgentRig(t, "a2", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	if r, err := rig.q.Claim("ghost", nil); err != nil || r == nil {
		t.Fatalf("claim: %v %v", r, err)
	}
	rig.start()
	waitFor(t, "taken back and run", func() bool { g := rig.get(req.ID); return g.State == StateDone && g.ClaimedBy == "a2" })
	if !strings.Contains(rig.log.String(), "taken back from ghost") {
		t.Fatalf("log: %s", rig.log.String())
	}
}

func TestAgentLeavesTheRequestsOfALiveAgentAlone(t *testing.T) {
	rig := newAgentRig(t, "a2", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	if r, err := rig.q.Claim("other", nil); err != nil || r == nil {
		t.Fatal("claim")
	}
	hb := Heartbeat{Name: "other", PID: os.Getpid() + 1, Slots: 1, LastSeen: time.Now()}
	if err := os.MkdirAll(AgentsDir(rig.dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(heartbeatPath(rig.dir, "other"), hb); err != nil {
		t.Fatal(err)
	}
	rig.agent.BeatEvery = time.Hour // "other" stays fresh for the whole test
	rig.start()
	time.Sleep(100 * time.Millisecond)
	if g := rig.get(req.ID); g.State != StateRunning || g.ClaimedBy != "other" {
		t.Fatalf("%+v", g)
	}
}

func TestAgentRefusesANameThatAnotherLiveProcessUses(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{}, nil })
	if err := os.MkdirAll(AgentsDir(rig.dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(heartbeatPath(rig.dir, "a1"), Heartbeat{Name: "a1", PID: os.Getpid() + 1, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rig.agent.PIDAlive = func(int) bool { return true }
	err := rig.agent.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("got %v", err)
	}
	rig.agent.PIDAlive = func(int) bool { return false } // the old process is dead: the name is free
	rig.agent.Name = "a1"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := rig.agent.Run(ctx); err != nil {
		t.Fatalf("a dead process must not hold the name: %v", err)
	}
	for _, bad := range []string{"", "a/b", "..", "a b", strings.Repeat("x", 80)} {
		a := &Agent{Name: bad, Dir: rig.dir, Queue: rig.q, Runner: rig.agent.Runner}
		if err := a.Run(context.Background()); err == nil {
			t.Errorf("name %q was accepted", bad)
		}
	}
}

func TestAgentHeartbeatFile(t *testing.T) {
	release := make(chan struct{})
	rig := newAgentRig(t, "ci-main", 3, func(ctx context.Context, r Request) (Outcome, error) {
		<-release
		return Outcome{Conclusion: "success"}, nil
	})
	rig.agent.Labels = []string{"windows", "go"}
	req := mustEnqueue(t, rig.q, prReq(278, strings.Repeat("c", 40)))
	rig.start()
	waitFor(t, "job in heartbeat", func() bool {
		hb, _ := ReadAgents(rig.dir)
		return len(hb) == 1 && len(hb[0].Jobs) == 1
	})
	b, err := os.ReadFile(filepath.Join(rig.dir, "agents", "ci-main.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hb Heartbeat
	if err := json.Unmarshal(b, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.Name != "ci-main" || hb.Slots != 3 || hb.PID != os.Getpid() || hb.State != "ready" || strings.Join(hb.Labels, ",") != "windows,go" {
		t.Fatalf("%+v", hb)
	}
	if j := hb.Jobs[0]; j.ID != req.ID || j.Label != "pr 278 into develop" || j.Since.IsZero() {
		t.Fatalf("job: %+v", j)
	}
	if !hb.Fresh(time.Now(), time.Minute) || hb.Fresh(time.Now().Add(time.Hour), time.Minute) {
		t.Fatal("Fresh is wrong")
	}
	first := hb.LastSeen
	waitFor(t, "a newer beat", func() bool { h, _ := ReadAgents(rig.dir); return len(h) == 1 && h[0].LastSeen.After(first) })
	close(release)
}

func TestAgentKeepAwakeFollowsTheWork(t *testing.T) {
	var mu sync.Mutex
	var calls []bool
	release := make(chan struct{})
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		<-release
		return Outcome{Conclusion: "success"}, nil
	})
	rig.agent.KeepAwake = func(on bool) { mu.Lock(); calls = append(calls, on); mu.Unlock() }
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	waitFor(t, "awake", func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) > 0 && calls[len(calls)-1] })
	close(release)
	waitFor(t, "done", func() bool { return rig.get(req.ID).State == StateDone })
	waitFor(t, "asleep again", func() bool { mu.Lock(); defer mu.Unlock(); return !calls[len(calls)-1] })
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls %v, want one true and one false", calls)
	}
}

func TestAgentRecordsARunnerErrorAPanicAndAMissingResult(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		switch r.Number {
		case 1:
			return Outcome{RunID: "r-err"}, errors.New("fetch failed")
		case 2:
			panic("boom")
		case 3:
			return Outcome{}, nil
		}
		return Outcome{Conclusion: StateSuperseded, Reason: "head moved"}, nil
	})
	var ids []string
	for i := 1; i <= 4; i++ {
		ids = append(ids, mustEnqueue(t, rig.q, prReq(i, fmt.Sprintf("%040d", i))).ID)
	}
	rig.start()
	waitFor(t, "all ended", func() bool {
		for _, id := range ids {
			if rig.get(id).Active() {
				return false
			}
		}
		return true
	})
	if g := rig.get(ids[0]); g.Conclusion != "error" || g.Reason != "fetch failed" || g.RunID != "r-err" {
		t.Errorf("error: %+v", g)
	}
	if g := rig.get(ids[1]); g.Conclusion != "error" || !strings.Contains(g.Reason, "boom") {
		t.Errorf("panic: %+v", g)
	}
	if g := rig.get(ids[2]); g.Conclusion != "error" || !strings.Contains(g.Reason, "no result") {
		t.Errorf("empty: %+v", g)
	}
	if g := rig.get(ids[3]); g.State != StateSuperseded || g.Reason != "head moved" {
		t.Errorf("superseded: %+v", g)
	}
}

func TestIndexRanIsTheCatchUpCheck(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	now := time.Now()
	add := func(e IndexEntry) {
		t.Helper()
		e.Repo, e.Start, e.End = "o/r", now, now
		if err := store.AppendIndex(e, nil); err != nil {
			t.Fatal(err)
		}
	}
	add(IndexEntry{RunID: "r1", Workflow: "fast-gate.yml", Event: "pull_request", PR: 7, HeadSHA: sha, Conclusion: "failure"})
	add(IndexEntry{RunID: "r2", Workflow: "fast-gate.yml", Event: "pull_request", PR: 8, HeadSHA: sha, Conclusion: "cancelled"})
	add(IndexEntry{RunID: "r3", Workflow: "ci.yml", Event: "push", HeadRef: "master", HeadSHA: sha, Conclusion: "success"})
	ran := IndexRan(store)
	cases := []struct {
		r    Request
		want bool
	}{
		{Request{Kind: KindPR, Repo: "o/r", Number: 7, SHA: sha, Workflow: "fast-gate.yml"}, true},
		{Request{Kind: KindPR, Repo: "o/r", Number: 7, SHA: strings.Repeat("b", 40), Workflow: "fast-gate.yml"}, false},
		{Request{Kind: KindPR, Repo: "o/r", Number: 8, SHA: sha, Workflow: "fast-gate.yml"}, false}, // only a cancelled run
		{Request{Kind: KindPR, Repo: "o/r", Number: 9, SHA: sha, Workflow: "fast-gate.yml"}, false},
		{Request{Kind: KindPR, Repo: "o/r", Number: 7, SHA: sha, Workflow: "other.yml"}, false},
		{Request{Kind: KindPush, Repo: "o/r", Ref: "master", SHA: sha, Workflow: "ci.yml"}, true},
		{Request{Kind: KindPush, Repo: "o/r", Ref: "develop", SHA: sha, Workflow: "ci.yml"}, false},
		{Request{Kind: KindSchedule, Repo: "o/r", Ref: "master", SHA: sha, Workflow: "ci.yml"}, false},
	}
	for _, c := range cases {
		if got := ran(c.r); got != c.want {
			t.Errorf("%s: Ran = %v, want %v", c.r.Label(), got, c.want)
		}
	}
}

// ---------------------------------------------------------------- child process

// TestMain lets the test binary act as the child process of the ExecRunner tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("LOCALCI_TEST_CHILD"); mode != "" {
		os.Exit(childMain(mode))
	}
	os.Exit(m.Run())
}

func childMain(mode string) int {
	state, id := os.Getenv("LOCALCI_TEST_STATE"), os.Getenv("LOCALCI_TEST_ID")
	switch mode {
	case "gh":
		return fakeGHMain(os.Args[1:], state)
	case "ok":
		fmt.Println("child ran", id)
		if err := WriteOutcome(state, id, Outcome{RunID: "child-run", Conclusion: "success"}); err != nil {
			return 2
		}
		return 0
	case "stdin":
		// wait for the parent to close stdin, then end the way run-item does
		buf := make([]byte, 16)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				break
			}
		}
		_ = WriteOutcome(state, id, Outcome{RunID: "child-run", Conclusion: "cancelled", Reason: "stdin closed"})
		return 0
	case "hang":
		time.Sleep(time.Hour)
		return 0
	}
	return 0 // crash: no outcome
}

func childRunner(t *testing.T, state, mode string) *ExecRunner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &ExecRunner{
		StateDir: state, Grace: 300 * time.Millisecond,
		Argv: func(r Request) []string { return []string{exe, "-test.run=XXXNothing"} },
		Env:  append(os.Environ(), "LOCALCI_TEST_CHILD="+mode, "LOCALCI_TEST_STATE="+state, "LOCALCI_TEST_ID=q-test-1"),
	}
}

func TestExecRunnerReadsTheChildsOutcome(t *testing.T) {
	state := t.TempDir()
	er := childRunner(t, state, "ok")
	var log syncBuf
	er.Log = &log
	out, err := er.Run(context.Background(), Request{ID: "q-test-1"})
	if err != nil || out.RunID != "child-run" || out.Conclusion != "success" {
		t.Fatalf("%+v %v", out, err)
	}
	if !strings.Contains(log.String(), "child ran q-test-1") {
		t.Fatalf("the child's output must reach the log: %q", log.String())
	}
	if _, err := os.Stat(OutcomePath(state, "q-test-1")); err == nil {
		t.Fatal("the outcome file must be removed after it is read")
	}
}

func TestExecRunnerClosesStdinToStopAChildGracefully(t *testing.T) {
	state := t.TempDir()
	er := childRunner(t, state, "stdin")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	out, err := er.Run(ctx, Request{ID: "q-test-1"})
	if err != nil || out.Conclusion != "cancelled" || out.Reason != "stdin closed" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestExecRunnerKillsAChildThatIgnoresTheStop(t *testing.T) {
	state := t.TempDir()
	er := childRunner(t, state, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	t0 := time.Now()
	_, err := er.Run(ctx, Request{ID: "q-test-1"})
	if err == nil || !strings.Contains(err.Error(), "without a result") {
		t.Fatalf("got %v", err)
	}
	if time.Since(t0) > 8*time.Second {
		t.Fatalf("took %v: the tree was not killed after the grace period", time.Since(t0))
	}
}

func TestExecRunnerReportsAChildThatCrashed(t *testing.T) {
	er := childRunner(t, t.TempDir(), "crash")
	if _, err := er.Run(context.Background(), Request{ID: "q-test-1"}); err == nil || !strings.Contains(err.Error(), "without a result") {
		t.Fatalf("got %v", err)
	}
	er.Argv = func(Request) []string { return []string{filepath.Join(t.TempDir(), "no-such-program")} }
	if _, err := er.Run(context.Background(), Request{ID: "q-test-1"}); err == nil {
		t.Fatal("a missing program must be an error")
	}
}

func TestAgentLowDiskClaimsNothingUntilThereIsRoom(t *testing.T) {
	var free atomic.Int64
	free.Store(1 << 30)
	rig := newAgentRig(t, "a1", 2, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.agent.MinFree = 10 << 30
	rig.agent.FreeBytes = func(string) (int64, error) { return free.Load(), nil }
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	waitFor(t, "low-disk heartbeat", func() bool {
		hb, _ := ReadAgents(rig.dir)
		return len(hb) == 1 && hb[0].State == "low-disk"
	})
	time.Sleep(60 * time.Millisecond)
	if g := rig.get(req.ID); g.State != StateQueued {
		t.Fatalf("an agent on a full drive claimed: %+v", g)
	}
	free.Store(50 << 30)
	waitFor(t, "done after room", func() bool { return rig.get(req.ID).State == StateDone })
	if log := rig.log.String(); !strings.Contains(log, "low disk") || !strings.Contains(log, "disk recovered") {
		t.Fatalf("log: %s", log)
	}
}

func TestAgentUnreadableFreeSpaceDoesNotBlock(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.agent.FreeBytes = func(string) (int64, error) { return 0, errors.New("no such drive") }
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	waitFor(t, "done", func() bool { return rig.get(req.ID).State == StateDone })
}

func TestAgentNegativeMinFreeTurnsTheGuardOff(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.agent.MinFree = -1
	rig.agent.FreeBytes = func(string) (int64, error) { return 0, nil }
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	waitFor(t, "done", func() bool { return rig.get(req.ID).State == StateDone })
}
