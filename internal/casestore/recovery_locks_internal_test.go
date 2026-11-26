package casestore

import (
	"context"
	"sync"
	"testing"
)

// W-43: the per-case recovery lock must not outlive its last user, or a store that opens and erases many cases keeps one entry
// for every case it ever saw.
func TestRecoveryLockEntryIsDroppedWhenNobodyUsesIt(t *testing.T) {
	ctx := context.Background()
	st := NewMemory()
	l := st.(Lifecycle)
	r, err := NewRecoverer(st)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Register(MarkErasing, func(ctx context.Context, id CaseID) error { return l.SetMark(ctx, id, "") })
	for i := 0; i < 20; i++ {
		info, err := st.Create(ctx, "case")
		if err != nil {
			t.Fatal(err)
		}
		_ = l.SetMark(ctx, info.ID, MarkErasing)
		if _, err := r.RecoverOne(ctx, info.ID); err != nil {
			t.Fatal(err)
		}
	}
	// An id that was never created is a call that finds nothing; it must leave nothing behind either.
	_, _ = r.RecoverOne(ctx, CaseID("no-such-case"))
	r.mu.Lock()
	left := len(r.locks)
	r.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d recovery lock entries left after every recovery finished, want 0", left)
	}
}

func TestRecoveryLockStaysWhileSomeoneWaits(t *testing.T) {
	r, err := NewRecoverer(NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	m1, release1 := r.lockFor("x")
	m2, release2 := r.lockFor("x")
	if m1 != m2 {
		t.Fatal("two callers for one case got different locks, so recovery would run twice at once")
	}
	release1()
	r.mu.Lock()
	still := len(r.locks)
	r.mu.Unlock()
	if still != 1 {
		t.Fatalf("entry dropped while a second caller still holds it (%d entries)", still)
	}
	release2()
	r.mu.Lock()
	gone := len(r.locks)
	r.mu.Unlock()
	if gone != 0 {
		t.Fatalf("entry kept after the last release (%d entries)", gone)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, rel := r.lockFor("y")
			rel()
		}()
	}
	wg.Wait()
	r.mu.Lock()
	n := len(r.locks)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d entries left after concurrent use", n)
	}
}
