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
