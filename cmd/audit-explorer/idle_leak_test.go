package main

import (
	"context"
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/leakcheck"
)

// BL-424: the real run() (demo data, loopback listener plus the ui and ag pipes)
// started and stopped five times returns the process to its goroutine baseline,
// and the idle count stays under the budget.

// idleGoroutineMax is the most goroutines the test process may hold when idle after
// the cycles (BL-424: "idle goroutines stay under about 30"). The Go test runner and
// the testing package account for a few of them.
const idleGoroutineMax = 30

func TestServerStartStopLeavesNoGoroutines(t *testing.T) {
	leakcheck.Cycles(t, 5, leakcheck.Options{IdleMax: idleGoroutineMax}, func(int) {
		getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
		var ui, ag *memListener
		var callErr error
		ui, ag, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
			defer cancel()
			for range 3 {
				if code, _, err := ui.get("/api/info", testToken); err != nil || code != 200 {
					callErr = joinErrs(err, callErr)
				}
				if _, _, err := ag.get("/api/cases", testToken); err != nil {
					callErr = joinErrs(err, callErr)
				}
			}
		})
		runUntilDone(t, []string{"--pipe-server", "--loopback", "--demo"}, getenv)
		if callErr != nil {
			t.Fatal(callErr)
		}
	})
}

// TestIdleWakeups is the BL-424 idle measurement on the real run(): serve, do
// nothing for UNRAVIS_IDLE_SECONDS (default 20) and print the wakeups and CPU per
// minute. It only runs with UNRAVIS_BENCH=1 and on Linux, where /proc counts them.
func TestIdleWakeups(t *testing.T) {
	if os.Getenv("UNRAVIS_BENCH") != "1" {
		t.Skip("set UNRAVIS_BENCH=1 to measure idle wakeups")
	}
	secs := 20
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
	var ui *memListener
	var w0, w1 uint64
	var ok bool
	ui, _, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		_, _, _ = ui.get("/api/info", testToken)
		time.Sleep(3 * time.Second) // let the start-up work settle
		w0, ok = leakcheck.Wakeups()
		time.Sleep(time.Duration(secs) * time.Second)
		w1, _ = leakcheck.Wakeups()
	})
	runUntilDone(t, []string{"--pipe-server", "--loopback", "--demo"}, getenv)
	if !ok {
		t.Skip("wakeups are only counted on Linux")
	}
	t.Logf("IDLE run(): %d wakeups in %d s = %.0f per minute, goroutines %d", w1-w0, secs, float64(w1-w0)*60/float64(secs), leakcheck.Count())
}
