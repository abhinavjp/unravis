//go:build windows

package blackbox

import (
	"testing"
	"time"
)

// RunHost only runs end to end on Windows in this package (its other tests
// fail on the Linux runner), so the whole-host wiring of the crash record is
// checked here; the same steps without the server run on every OS in
// crash_test.go.

func runHostOnce(t *testing.T, folder string) {
	t.Helper()
	g := newHostRig(t)
	g.cfg.Folder = folder
	o := g.opts()
	o.CrashCapture = true
	g.start(t, o)
	select {
	case <-g.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server never listened")
	}
	if err := g.stop(t); err != nil {
		t.Fatal(err)
	}
}

func TestCrashedRecorderIsReportedOnceByRunHost(t *testing.T) {
	g := newHostRig(t)
	crashReportedOnce(t, g.cfg.Folder, runHostOnce)
}
