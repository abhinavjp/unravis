package bbsql

import (
	"sync"
	"testing"
)

// W11-07: the registry registers immutable factories and allocates fresh sampler
// state per runner, so sequential or concurrent runners never share baseline maps.

func semOfKind(list []Sampler) Sampler {
	for _, s := range list {
		if s.Kind() == "semaphores" {
			return s
		}
	}
	return nil
}

// TestSequentialRunnersHaveIndependentBaselines: a baseline decoded into one
// runner's sampler must not leak into a freshly allocated sampler.
func TestSequentialRunnersHaveIndependentBaselines(t *testing.T) {
	a := semOfKind(freshProductionSamplerList())
	b := semOfKind(freshProductionSamplerList())
	if a == nil || b == nil {
		t.Fatal("semaphores sampler not registered")
	}
	if a == b {
		t.Fatal("two fresh sampler lists returned the SAME instance (shared state)")
	}
	// A records a baseline.
	if p, _, _ := a.Decode(semRows(semFixture{id: 0, forced: 100})); p != nil {
		t.Fatalf("A first cycle emitted %#v, want nil", p)
	}
	// B's first cycle is still baseline-only: it must not see A's history.
	if p, _, _ := b.Decode(semRows(semFixture{id: 0, forced: 100})); p != nil {
		t.Fatalf("B first cycle emitted %#v, want nil (baseline leaked across runners)", p)
	}
	// A's second cycle emits its own delta.
	if p, _, _ := a.Decode(semRows(semFixture{id: 0, forced: 110})); p == nil {
		t.Fatal("A second cycle emitted no delta, want 10")
	}
}

// TestConcurrentRunnersIsolated exercises independent fresh samplers concurrently
// (safe under the race detector): each goroutine owns its state and sees its own
// baseline-then-delta sequence.
func TestConcurrentRunnersIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := freshProductionSamplers()["semaphores"]
			if s == nil {
				t.Errorf("goroutine %d: no semaphores sampler", i)
				return
			}
			id := int32(i)
			if p, _, _ := s.Decode(semRows(semFixture{id: id, forced: 100 + int32(i)})); p != nil {
				t.Errorf("goroutine %d first cycle emitted %#v, want nil", i, p)
			}
			p, _, err := s.Decode(semRows(semFixture{id: id, forced: 101 + int32(i)}))
			if err != nil || p == nil {
				t.Errorf("goroutine %d second cycle: payload=%v err=%v, want a delta", i, p, err)
			}
		}(i)
	}
	wg.Wait()
}
