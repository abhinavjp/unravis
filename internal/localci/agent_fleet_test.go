package localci

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fleetRig is an agent of a PC of its own (its own state folder and queue) that shares the host with the others.
func fleetRig(t *testing.T, host ClaimHost, fleetID string, runner Runner) *agentRig {
	t.Helper()
	rig := newAgentRig(t, "pc-"+fleetID, 1, runner)
	rig.agent.Fleet = &Fleet{Host: host, Repo: "o/r", ID: fleetID, TTL: time.Minute, RetryEvery: 40 * time.Millisecond, Labels: []string{"win"}}
	rig.agent.FleetEvery = 20 * time.Millisecond
	return rig
}

func TestTwoPCsRunAnItemOnce(t *testing.T) {
	host := &MemClaimHost{}
	var runsA, runsB atomic.Int32
	release := make(chan struct{})
	slow := func(n *atomic.Int32) Runner {
		return func(ctx context.Context, r Request) (Outcome, error) {
			n.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
				return Outcome{}, ctx.Err()
			}
			return Outcome{RunID: "run-" + r.ID, Conclusion: "success"}, nil
		}
	}
	a := fleetRig(t, host, "m-aaaa1111", slow(&runsA))
	b := fleetRig(t, host, "m-bbbb2222", slow(&runsB))
	ra := mustEnqueue(t, a.q, prReq(7, testSHA))
	rb := mustEnqueue(t, b.q, prReq(7, testSHA))
	a.start()
	b.start()
	waitFor(t, "one of them running", func() bool { return runsA.Load()+runsB.Load() == 1 })
	time.Sleep(150 * time.Millisecond) // the other one looks again and must still not run it
	if runsA.Load()+runsB.Load() != 1 {
		t.Fatalf("ran %d and %d times", runsA.Load(), runsB.Load())
	}
	winner, loser, wreq, lreq := a, b, ra, rb
	if runsB.Load() == 1 {
		winner, loser, wreq, lreq = b, a, rb, ra
	}
	if g := loser.get(lreq.ID); g.State != StateQueued || !strings.Contains(g.DeferNote, "running on PC m-") || g.Attempts != 0 {
		t.Fatalf("the other PC keeps it queued with a plain note, not counting an attempt: %+v", g)
	}
	close(release)
	waitFor(t, "winner done", func() bool { return winner.get(wreq.ID).State == StateDone })
	waitFor(t, "loser skipped", func() bool { return loser.get(lreq.ID).State == StateDone })
	g := loser.get(lreq.ID)
	if g.Conclusion != "skipped" || !strings.Contains(g.Reason, "already ran on PC "+winner.agent.Fleet.ID) || !strings.Contains(g.Reason, "success") {
		t.Fatalf("the other PC ends it as skipped: %+v", g)
	}
	if runsA.Load()+runsB.Load() != 1 {
		t.Fatalf("ran %d and %d times", runsA.Load(), runsB.Load())
	}
	// the finished claim stays as a tombstone, so a third look still skips it
	if rec, ok := host.Record(RefNSClaim, testSHA+"-fast-gate.yml"); !ok || rec.State != ClaimDone || rec.Conclusion != "success" {
		t.Fatalf("claim: %+v %v", rec, ok)
	}
}

func TestAgentPublishesItselfAndShowsTheOthers(t *testing.T) {
	host := &MemClaimHost{}
	hold := make(chan struct{})
	a := fleetRig(t, host, "m-aaaa1111", func(ctx context.Context, r Request) (Outcome, error) {
		<-hold
		return Outcome{Conclusion: "success"}, nil
	})
	b := fleetRig(t, host, "m-bbbb2222", func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	mustEnqueue(t, a.q, prReq(7, testSHA))
	a.start()
	b.start()
	waitFor(t, "b sees a running", func() bool {
		v, _ := ReadFleetView(b.dir)
		if v == nil || len(v.Agents) != 2 {
			return false
		}
		for _, ag := range v.Agents {
			if ag.ID == "m-aaaa1111" && len(ag.Jobs) == 1 && ag.State == "ready" {
				return true
			}
		}
		return false
	})
	hbs, _ := ReadAgents(a.dir)
	if len(hbs) != 1 || hbs[0].FleetID != "m-aaaa1111" {
		t.Fatalf("the local heartbeat names the opaque id: %+v", hbs)
	}
	close(hold)
	a.stop()
	if err := a.wait(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b sees a stopped", func() bool {
		v, _ := ReadFleetView(b.dir)
		if v == nil {
			return false
		}
		for _, ag := range v.Agents {
			if ag.ID == "m-aaaa1111" && ag.State == "stopped" {
				return true
			}
		}
		return false
	})
}

func TestStoppingAnAgentReleasesItsClaim(t *testing.T) {
	host := &MemClaimHost{}
	started := make(chan struct{}, 1)
	a := fleetRig(t, host, "m-aaaa1111", func(ctx context.Context, r Request) (Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})
	req := mustEnqueue(t, a.q, prReq(7, testSHA))
	a.start()
	<-started
	if got := host.Names(RefNSClaim); len(got) != 1 {
		t.Fatalf("claim while running: %v", got)
	}
	a.stop()
	if err := a.wait(); err != nil {
		t.Fatal(err)
	}
	if got := host.Names(RefNSClaim); len(got) != 0 {
		t.Fatalf("a stopped agent must leave no claim: %v", got)
	}
	if g := a.get(req.ID); g.State != StateQueued {
		t.Fatalf("and the request is back in its queue: %+v", g)
	}
}

func TestCancellingARunReleasesItsClaim(t *testing.T) {
	host := &MemClaimHost{}
	started := make(chan struct{}, 1)
	a := fleetRig(t, host, "m-aaaa1111", func(ctx context.Context, r Request) (Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		return Outcome{Conclusion: "cancelled"}, nil
	})
	req := mustEnqueue(t, a.q, prReq(7, testSHA))
	a.start()
	<-started
	if _, err := a.q.Cancel(req.ID, "by the owner"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cancelled", func() bool { return a.get(req.ID).State == StateCancelled })
	waitFor(t, "claim released", func() bool { return len(host.Names(RefNSClaim)) == 0 })
}

func TestACrashedPCsItemIsRunByAnother(t *testing.T) {
	host := &MemClaimHost{}
	dead := newTestFleet(host, "m-dead0000")
	mustAcquire(t, dead, claimReq("dead")) // a PC that took the claim and then went to sleep for good
	var runs atomic.Int32
	b := fleetRig(t, host, "m-bbbb2222", func(ctx context.Context, r Request) (Outcome, error) {
		runs.Add(1)
		return Outcome{Conclusion: "success"}, nil
	})
	req := mustEnqueue(t, b.q, prReq(7, testSHA))
	b.start()
	waitFor(t, "deferred while the lease is fresh", func() bool { return b.get(req.ID).DeferNote != "" })
	if runs.Load() != 0 {
		t.Fatal("it ran while the other PC's lease was fresh")
	}
	host.Backdate(RefNSClaim, testSHA+"-fast-gate.yml", 2*time.Minute) // longer than the lease
	waitFor(t, "taken over and run", func() bool { return runs.Load() == 1 && b.get(req.ID).State == StateDone })
	if g := b.get(req.ID); g.Conclusion != "success" {
		t.Fatalf("%+v", g)
	}
}

func TestARunStopsWhenItsClaimWasTakenOver(t *testing.T) {
	host := &MemClaimHost{}
	started := make(chan struct{}, 4)
	var stopped atomic.Int32
	a := fleetRig(t, host, "m-aaaa1111", func(ctx context.Context, r Request) (Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		stopped.Add(1)
		return Outcome{Conclusion: "cancelled"}, nil
	})
	req := mustEnqueue(t, a.q, prReq(7, testSHA))
	a.start()
	<-started
	// this PC slept: another one found the lease stale and took the claim over
	base := testSHA + "-fast-gate.yml"
	host.Backdate(RefNSClaim, base, 10*time.Minute)
	taker := newTestFleet(host, "m-bbbb2222")
	taker.TTL = time.Minute
	// the agent refreshes every 20 ms; do the takeover in one step so it cannot refresh in between
	waitFor(t, "takeover", func() bool {
		host.Backdate(RefNSClaim, base, 10*time.Minute)
		d, err := taker.Acquire(context.Background(), claimReq("rb"))
		return err == nil && d.Outcome == ClaimWon
	})
	waitFor(t, "run stopped", func() bool { return stopped.Load() >= 1 })
	waitFor(t, "back in the queue, deferred or running again", func() bool { return a.get(req.ID).State != StateRunning || stopped.Load() >= 1 })
	// the winner's claim is untouched by the loser
	time.Sleep(100 * time.Millisecond)
	if rec, ok := host.Record(RefNSClaim, base+"+2"); !ok || rec.Agent != "m-bbbb2222" {
		t.Fatalf("the taker's claim: %+v %v", rec, ok)
	}
	if g := a.get(req.ID); g.State == StateDone && g.Conclusion != "skipped" {
		t.Fatalf("the PC that lost the claim must not post a result: %+v", g)
	}
}

func TestHostDownDefersInsteadOfRunningUnclaimed(t *testing.T) {
	host := &MemClaimHost{}
	host.Fail(context.DeadlineExceeded)
	var runs atomic.Int32
	a := fleetRig(t, host, "m-aaaa1111", func(ctx context.Context, r Request) (Outcome, error) {
		runs.Add(1)
		return Outcome{Conclusion: "success"}, nil
	})
	req := mustEnqueue(t, a.q, prReq(7, testSHA))
	a.start()
	waitFor(t, "deferred", func() bool { return a.get(req.ID).DeferNote != "" })
	time.Sleep(100 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatal("an unclaimed run started")
	}
	if g := a.get(req.ID); g.State != StateQueued || g.Attempts != 0 || !g.DeferUntil.After(time.Now()) {
		t.Fatalf("%+v", g)
	}
}

func TestQueueDeferHidesARequestUntilTheTime(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	q.Now = func() time.Time { return now }
	r := mustEnqueue(t, q, prReq(7, testSHA))
	got, err := q.Claim("a", nil)
	if err != nil || got == nil || got.ID != r.ID || got.Attempts != 1 {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if err := q.Defer(r.ID, now.Add(time.Minute), "running on PC m-x"); err != nil {
		t.Fatal(err)
	}
	if again, _ := q.Claim("a", nil); again != nil {
		t.Fatalf("a deferred request must wait: %+v", again)
	}
	now = now.Add(2 * time.Minute)
	again, err := q.Claim("a", nil)
	if err != nil || again == nil || again.Attempts != 1 || again.DeferNote != "" || !again.DeferUntil.IsZero() {
		t.Fatalf("after the time: %+v %v", again, err)
	}
	if err := q.Defer("nope", now, ""); err == nil {
		t.Fatal("deferring a request that is not running must fail")
	}
}
