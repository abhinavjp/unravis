package server

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/power"
)

func TestPowerPolicyTracksOnlyAuthorizedRequests(t *testing.T) {
	p := power.New(power.Config{Grace: 20 * time.Millisecond, Check: 5 * time.Millisecond})
	h := &handler{token: "session-token-for-test"}
	WithPower(p)(h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitFor(t, func() bool { return p.Mode() == power.Idle })
	askAs(h, "")
	askAs(h, "wrong-token-of-the-same-length")
	if p.Mode() != power.Idle {
		t.Fatal("an unauthorized request cleared idle power mode")
	}
	askAs(h, h.token)
	if p.Mode() != power.Normal {
		t.Fatal("an authorized UI request did not clear idle power mode")
	}
	waitFor(t, func() bool { return p.Mode() == power.Idle })
	askAs(h, h.token)
	if p.Mode() != power.Normal {
		t.Fatal("an authorized MCP request did not clear idle power mode")
	}
}

func TestPowerPolicyTracksBackgroundJobsWithoutAnUpdater(t *testing.T) {
	p := power.New(power.Config{Grace: 20 * time.Millisecond, Check: 5 * time.Millisecond})
	h := &handler{token: "session-token-for-test"}
	WithPower(p)(h)
	job := &importJob{id: "queued", state: jobQueued}
	h.jobs.mu.Lock()
	if h.jobs.m == nil {
		h.jobs.m = make(map[string]*importJob)
	}
	h.jobs.m[job.id] = job
	h.jobs.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	time.Sleep(60 * time.Millisecond)
	if p.Mode() != power.Normal {
		t.Fatal("background work was throttled when update checks were unavailable")
	}
	h.jobs.mu.Lock()
	delete(h.jobs.m, job.id)
	h.jobs.mu.Unlock()
	waitFor(t, func() bool { return p.Mode() == power.Idle })
}

// A burst of slow requests that finish together must not run one forced memory return each: at most one per interval.
func TestMemoryTrimIsRateLimited(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	runs := 0
	m := &memoryTrimmer{now: func() time.Time { return now }, free: func() { runs++ }}
	for i := 0; i < 20; i++ {
		m.maybe()
	}
	if runs != 1 {
		t.Fatalf("20 slow requests at once ran the trim %d times, want 1", runs)
	}
	now = now.Add(memoryTrimEvery - time.Second)
	if m.maybe() || runs != 1 {
		t.Fatalf("trim ran again inside the interval (%d runs)", runs)
	}
	now = now.Add(time.Second)
	if !m.maybe() || runs != 2 {
		t.Fatalf("trim did not run after the interval (%d runs)", runs)
	}
}

// The real zero value (no test hooks) trims on the first call and not on the second.
func TestMemoryTrimZeroValueUsesTheDefaults(t *testing.T) {
	var m memoryTrimmer
	if !m.maybe() {
		t.Fatal("the first slow request did not trim")
	}
	if m.maybe() {
		t.Fatal("the second slow request trimmed again at once")
	}
}
