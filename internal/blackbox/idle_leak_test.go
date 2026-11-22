package blackbox

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/leakcheck"
)

// BL-424: starting and stopping the recorder, with and without its status server,
// leaves no goroutine behind.

// TestRecorderRunLeavesNoGoroutines runs the recorder on its real deadline timer
// (no injected ticks): start, take the first sample, stop.
func TestRecorderRunLeavesNoGoroutines(t *testing.T) {
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(int) {
		g := newRig(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- g.r.Run(ctx) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			g.col.mu.Lock()
			n := g.col.n
			g.col.mu.Unlock()
			if n >= 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the recorder took no first sample")
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("the recorder stopped with an error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the recorder did not stop")
		}
		if err := g.s.Close("stopped"); err != nil {
			t.Fatalf("closing the store: %v", err)
		}
	})
}

// TestRunHostLeavesNoGoroutines runs the whole host (recorder, status server,
// reader) through start, a few samples and stop.
func TestRunHostLeavesNoGoroutines(t *testing.T) {
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(int) {
		g := newHostRig(t)
		g.cfg.LogPaths = []string{t.TempDir()} // the default is a Windows path, which fails validation elsewhere
		g.start(t, g.opts())
		select {
		case <-g.ready:
		case err := <-g.done:
			// RunHost needs the Windows service pieces (see the baseline: the
			// other RunHost tests fail the same way here); the cycle is checked
			// where it can run.
			t.Skipf("RunHost does not start on this platform: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("server never listened")
		}
		g.tick(t, time.Now())
		g.waitProcessed(t)
		if code, _ := g.get(t, "/status", g.key); code != 200 {
			t.Fatalf("status answered %d", code)
		}
		if err := g.stop(t); err != nil {
			t.Fatal(err)
		}
	})
}
