package server

import (
	"context"
	"testing"
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
	let, ok := g.acquireReadIdle(id, nil)
	if !ok {
		t.Fatal("refused with nothing held")
	}
	// A plain read still works alongside it; an erase that starts now must wait for it.
	got := make(chan error, 1)
	go func() {
		d, err := g.acquireErase(context.Background(), id)
		if err == nil {
			d()
		}
		got <- err
	}()
	select {
	case <-got:
		t.Fatal("the erase did not wait for the code run")
	default:
	}
	let()
	if err := <-got; err != nil {
		t.Fatalf("erase after the run: %v", err)
	}
}
