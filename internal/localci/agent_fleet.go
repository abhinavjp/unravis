package localci

import (
	"context"
	"fmt"
	"time"
)

// The agent's part in the fleet: claim before running, keep the lease, stop a run whose claim was lost, publish
// the agent record. Everything here is a no-op while Agent.Fleet is nil.

const (
	hostRetry  = 30 * time.Second // between two tries when the host cannot be asked
	hostBudget = 20 * time.Second // most one claim call may take at the end of a run
	cleanEvery = time.Hour
)

func (a *Agent) fleetEvery() time.Duration {
	if a.FleetEvery > 0 {
		return a.FleetEvery
	}
	return DefaultFleetEvery
}

// acquire takes the claim of a request the queue just handed over. It returns true when the request may run
// now. Otherwise the request is back in the queue (another PC runs it, or the host could not be asked) or ended
// as skipped (another PC ran it already).
func (a *Agent) acquire(ctx context.Context, r Request) bool {
	if a.Fleet == nil {
		return true
	}
	d, err := a.Fleet.Acquire(ctx, r)
	if err != nil {
		if ctx.Err() != nil {
			a.putBack(r)
			return false
		}
		a.logf("%s: could not claim it on the host, trying again in %s: %v", r.ID, hostRetry, err)
		a.deferRequest(r, hostRetry, "waiting for the host, to see whether another PC runs it")
		return false
	}
	switch d.Outcome {
	case ClaimWon:
		return true
	case ClaimBusy:
		who := "this PC"
		if d.Holder != "" && d.Holder != a.Fleet.ID {
			who = "PC " + shortOpaque(d.Holder)
		}
		a.logf("%s: %s is running it, looking again in %s", r.ID, who, d.RetryAfter.Round(time.Second))
		a.deferRequest(r, d.RetryAfter, "running on "+who)
		return false
	default:
		note := "already ran on PC " + shortOpaque(d.Holder) + ": " + d.Conclusion
		if _, err := a.Queue.Complete(r.ID, "", "skipped", note); err != nil {
			a.logf("%s: could not record the skip: %v", r.ID, err)
			return false
		}
		if a.Status != nil {
			a.Status.MirrorDone(r, d.Conclusion)
		}
		a.logf("%s: skipped, %s", r.ID, note)
		return false
	}
}

func (a *Agent) deferRequest(r Request, d time.Duration, note string) {
	if err := a.Queue.Defer(r.ID, a.now().Add(d), note); err != nil {
		a.logf("%s: could not put it back: %v", r.ID, err)
	}
}

func (a *Agent) putBack(r Request) {
	if err := a.Queue.Requeue(r.ID); err != nil {
		a.logf("%s: could not put it back: %v", r.ID, err)
	}
}

func hostCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), hostBudget)
}

// fleetFinish ends the claim of a request that the queue has recorded as finished: a success or a failure stays on
// the host as a done claim, anything else removes the claim so that another PC may run it.
func (a *Agent) fleetFinish(r Request, done Request) {
	if a.Fleet == nil {
		return
	}
	ctx, cancel := hostCtx()
	defer cancel()
	conclusion := done.Conclusion
	if done.State == StateCancelled || done.State == StateSuperseded {
		conclusion = done.State
	}
	a.Fleet.Finish(ctx, r, conclusion)
}

// fleetRelease removes the claim of a request that stops without a result.
func (a *Agent) fleetRelease(r Request) {
	if a.Fleet == nil {
		return
	}
	ctx, cancel := hostCtx()
	defer cancel()
	a.Fleet.Release(ctx, r)
}

// fleetLoop refreshes the leases, publishes this agent and reads the others, once at the start and then every
// FleetEvery, until ctx ends.
func (a *Agent) fleetLoop(ctx context.Context) {
	if a.Fleet == nil {
		return
	}
	// idle-ok: runs only inside the separate CI agent process (not the desktop app); the period is the agent setting.
	t := time.NewTicker(a.fleetEvery())
	defer t.Stop()
	var lastClean time.Time
	failed := false
	for {
		if err := a.fleetRound(ctx, &lastClean); err != nil {
			if !failed && ctx.Err() == nil {
				a.logf("fleet: %v (the claims still hold, the other PCs may see this one as quiet)", err)
			}
			failed = true
		} else {
			failed = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) fleetRound(ctx context.Context, lastClean *time.Time) error {
	for _, id := range a.Fleet.Refresh(ctx) {
		a.mu.Lock()
		j := a.jobs[id]
		if j != nil {
			j.lost = true
		}
		a.mu.Unlock()
		if j != nil {
			a.logf("%s: another PC holds the claim now, stopping the run", id)
			j.cancel()
		}
	}
	hb := a.Snapshot()
	if err := a.Fleet.Publish(ctx, hb, hb.State); err != nil {
		return fmt.Errorf("could not publish this agent: %w", err)
	}
	if err := a.Fleet.Sync(ctx, a.Dir); err != nil {
		return fmt.Errorf("could not read the other agents: %w", err)
	}
	if a.now().Sub(*lastClean) >= cleanEvery {
		*lastClean = a.now()
		a.Fleet.CleanClaims(ctx)
	}
	return nil
}

// fleetStop runs when the agent ends: claims still held are removed (a run that was put back has none; this is
// the safety net) and the record says "stopped", so that the other PCs show it as stopped and not as quiet.
func (a *Agent) fleetStop() {
	if a.Fleet == nil {
		return
	}
	ctx, cancel := hostCtx()
	defer cancel()
	a.Fleet.ReleaseAll(ctx)
	hb := a.Snapshot()
	if err := a.Fleet.Publish(ctx, hb, "stopped"); err != nil {
		a.logf("fleet: could not mark this agent as stopped: %v", err)
		return
	}
	_ = a.Fleet.Sync(ctx, a.Dir)
}
