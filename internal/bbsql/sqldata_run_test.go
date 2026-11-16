package bbsql

import (
	"context"
	"testing"
	"time"
)

// W10-07: Run schedules on monotonic fixed-rate deadlines and counts missed slots
// without queueing or overlapping; the tick context is rooted in Run's context.

func TestAdvanceScheduleCountsMissedSlots(t *testing.T) {
	t0 := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	iv := 15 * time.Second
	for _, tc := range []struct {
		elapsed    time.Duration
		wantMissed int
	}{
		{2 * time.Second, 0},  // tick shorter than the interval: no miss
		{15 * time.Second, 0}, // exactly on the next slot: no miss
		{20 * time.Second, 1}, // overran one slot
		{50 * time.Second, 3}, // overran three slots
	} {
		next, missed := advanceSchedule(t0, t0.Add(tc.elapsed), iv)
		if missed != tc.wantMissed {
			t.Errorf("elapsed %v: missed = %d, want %d", tc.elapsed, missed, tc.wantMissed)
		}
		if next.Before(t0.Add(tc.elapsed)) {
			t.Errorf("elapsed %v: next deadline %v is in the past", tc.elapsed, next)
		}
		// The next deadline stays on the original slot grid (fixed-rate).
		if next.Sub(t0)%iv != 0 {
			t.Errorf("elapsed %v: next deadline %v drifted off the slot grid", tc.elapsed, next)
		}
	}
}

// TestTickThreadsCancelledContext: a cancelled parent context stops the tick
// before any sample is sent (the context is threaded through connect/query).
func TestTickThreadsCancelledContext(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.tick(ctx, now); err == nil {
		t.Fatal("a cancelled tick returned nil, want a fixed error")
	}
	if got := q.countID("bb:waits"); got != 0 {
		t.Errorf("bb:waits sent %d times under a cancelled context, want 0", got)
	}
	if got := q.countID("bb:ceiling_role"); got != 0 {
		t.Errorf("ceiling sent %d times under a cancelled context, want 0", got)
	}
}

// W10-08: a slow sampler is demoted after exactly three qualifying ticks even when
// a fast sibling runs after it in the same tick; the fast sibling cannot erase the
// strike, and one or two strikes do not demote early.
func TestSlowDemotionMixedSamplers(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:requests"]:   fakeWaitRows(1),
		Statements["bb:schedulers"]: {{}},
	}}
	// requests runs first (Kind order) and uses over half its 3 s budget; the
	// fast schedulers query runs after it.
	q.onQuery = func(text string) {
		if text == Statements["bb:requests"] {
			time.Sleep(quickLimit/2 + 100*time.Millisecond)
		}
	}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{
		baseSampler("bb:requests", "requests"),
		baseSampler("bb:schedulers", "schedulers"),
	}})

	if err := r.Tick(now); err != nil {
		t.Fatalf("tick1: %v", err)
	}
	if got := r.CurrentState().SlowStrikes; got != 1 {
		t.Fatalf("after tick1 strikes = %d, want 1 (the fast sibling erased the slow strike)", got)
	}
	if got := r.CurrentState().Interval; got != BaseInterval {
		t.Fatalf("after tick1 interval = %v, want %v (no early demotion)", got, BaseInterval)
	}
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if got := r.CurrentState().SlowStrikes; got != 2 {
		t.Fatalf("after tick2 strikes = %d, want 2", got)
	}
	if err := r.Tick(now.Add(2 * BaseInterval)); err != nil {
		t.Fatalf("tick3: %v", err)
	}
	if got := r.CurrentState().Interval; got != DemotedInterval {
		t.Fatalf("after three slow ticks interval = %v, want %v (demotion at the threshold)", got, DemotedInterval)
	}
}

// W7-06: the demotion floor applies only at the three-strike threshold, not for
// any nonzero strike.
func TestOnSuccessFloorOnlyAtThreshold(t *testing.T) {
	s := State{Interval: DemotedInterval, SlowStrikes: 1, State: StateOn}
	s.OnSuccess()
	if s.Interval != 30*time.Second {
		t.Fatalf("one strike imposed the 60s floor: interval = %v, want 30s (floor only at the threshold)", s.Interval)
	}
	// At the threshold the floor holds.
	s2 := State{Interval: DemotedInterval, SlowStrikes: SlowStrikeLimit, State: StateOn}
	s2.OnSuccess()
	if s2.Interval != DemotedInterval {
		t.Fatalf("at the threshold interval = %v, want the 60s floor held", s2.Interval)
	}
}
