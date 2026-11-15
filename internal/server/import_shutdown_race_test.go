package server

import (
	"testing"
	"time"
)

// SEC-07: the background-job handoff must count its run in the wait group only
// while the dispatcher is open, and must do the closed-check and the Add together
// under j.mu. shutdown sets closed under j.mu before wg.Wait, so the two cannot
// interleave into an Add-after-Wait ("WaitGroup is reused before previous Wait
// has returned") panic. These unit tests drive a synthetic importJobs (its zero
// value works) so the guard is proven without a store, a handler or a live upload.
//
// Harness limit: a handler-level 503-from-handoff test is not deterministic with
// the existing harness. After shutdown, create() already refuses with 429
// (import-busy) before the spool, so the beginRun refusal is only reachable when
// shutdown lands DURING the long spool, between create and the handoff. The
// harness cannot park the spool there without a timing-dependent slow-body hack,
// so the two unit tests below are the proof of the guard.

// TestBeginRunRefusesAfterShutdown proves the closed re-check: an open dispatcher
// lets a run begin, and after shutdown beginRun returns false and Adds nothing.
// The pre-fix inline wg.Add(1) ran outside the lock with no re-check, so a run
// begun after shutdown would Add on a group whose Wait had already returned.
func TestBeginRunRefusesAfterShutdown(t *testing.T) {
	j := &importJobs{}

	// Open: a run may begin. Balance it at once so shutdown's Wait can return.
	if !j.beginRun() {
		t.Fatal("beginRun on an open dispatcher = false, want true")
	}
	j.wg.Done()

	// shutdown sets closed under j.mu before Wait; the group is balanced, so it
	// returns promptly even with a zero wait.
	j.shutdown(0)

	// After shutdown the guard must refuse, and must not Add (no panic).
	if j.beginRun() {
		t.Fatal("beginRun after shutdown = true, want false")
	}
	// A second refusal is also clean: still closed, still no Add, still no panic.
	if j.beginRun() {
		t.Fatal("second beginRun after shutdown = true, want false")
	}
}

// TestBeginRunAddIsCounted proves the Add is counted: while a begun run is
// outstanding, a concurrent shutdown parks in wg.Wait and does not return until
// the run is balanced with Done. If beginRun returned true without Adding, Wait
// would see a zero counter and return at once, and shutdown would close early.
//
// The negative assertion uses the same select-with-a-window idiom as the existing
// gate test (TestAsyncJobHoldsTheGateAndTheSlotUntilItEnds): the window is a
// safety bound, not the synchronisation. In the correct case shutdown cannot
// return before Done (counter is one, wait is an hour), so the window always
// elapses; the channel handshake on returned is what proves the ordering.
func TestBeginRunAddIsCounted(t *testing.T) {
	j := &importJobs{}
	if !j.beginRun() {
		t.Fatal("beginRun on an open dispatcher = false, want true")
	}

	// A begun run is outstanding. Only Done can end this Wait within the test.
	returned := make(chan struct{})
	go func() {
		j.shutdown(time.Hour)
		close(returned)
	}()

	// shutdown must NOT return while the run is outstanding.
	select {
	case <-returned:
		t.Fatal("shutdown returned while a begun run was outstanding: the Add was not counted")
	case <-time.After(200 * time.Millisecond):
	}

	// Balance the begun run; now Wait must return and shutdown must complete.
	j.wg.Done()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the begun run was balanced")
	}
}
