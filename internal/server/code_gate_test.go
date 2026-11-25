package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A code run (acquireReadIdle) is refused while a mover holds the case's exclusive slot or an erase is under way, and a run that is
// already counted holds an erase until it lets go. Every id here is synthetic.

func TestReadIdleRefusedWhileTheSlotIsHeld(t *testing.T) {
	var g caseGates
	id := gateID("b")
	release, err := g.acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.acquireReadIdle(id, nil); ok {
		t.Fatal("a code run was admitted while the exclusive slot was held")
	}
	release()
	counted := false
	let, ok := g.acquireReadIdle(id, func() { counted = true })
	if !ok || !counted {
		t.Fatalf("a code run was refused with the slot free (ok=%v counted=%v)", ok, counted)
	}
	let()
}

func TestReadIdleCountedBeforeAMoverCanLook(t *testing.T) {
	var g caseGates
	id := gateID("c")
	counted := 0
	let, ok := g.acquireReadIdle(id, func() { counted++ })
	if !ok || counted != 1 {
		t.Fatalf("ok=%v counted=%d: the run is counted once, under the gate's lock", ok, counted)
	}
	// An erase that starts now must wait for the run and give up with errCaseInUse when it does not leave in time.
	if _, err := g.acquireEraseWithin(context.Background(), id, time.After(100*time.Millisecond)); !errors.Is(err, errCaseInUse) {
		t.Fatalf("erase with a code run live = %v, want errCaseInUse", err)
	}
	let()
	release, err := g.acquireEraseWithin(context.Background(), id, time.After(5*time.Second))
	if err != nil {
		t.Fatalf("erase after the run: %v", err)
	}
	release()
}

// The erase claims the case, then ends the runs: onClaim runs after new code runs are refused, before the wait for the readers.
func TestEraseStopsRunsOnlyAfterTheClaim(t *testing.T) {
	var g caseGates
	id := gateID("d")
	let, ok := g.acquireReadIdle(id, nil)
	if !ok {
		t.Fatal("refused with nothing held")
	}
	var admittedAtClaim bool
	release, err := g.acquireEraseStopping(context.Background(), id, time.After(5*time.Second), func() {
		_, again := g.acquireReadIdle(id, nil)
		admittedAtClaim = again
		let() // the run ends when it is told to stop
	})
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	release()
	if admittedAtClaim {
		t.Fatal("a code run was admitted after the erase claimed the case")
	}
}
