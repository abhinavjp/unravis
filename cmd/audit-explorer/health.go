package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// Rollback after a bad update (internal/update/health.go). Three starts in a row that did not stay up for a minute, and that the
// person did not end on purpose, mean the version the installer just put in place cannot run on this PC. The app then hands over to the
// old setup the installer kept, puts the bad version on the skip list and stops. Everything it starts comes from the marker the
// installer wrote beside the program files; nothing from the network can start a rollback.

// healthTracker watches the first starts of a freshly installed version. It is nil for a demo run, a development build (no version
// to compare) and a PC where the folders cannot be told.
func healthTracker(demo bool, getenv func(string) string) *update.Tracker {
	if demo {
		return nil
	}
	v, err := update.ParseVersion(version.Version)
	if err != nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	dir, err := mcp.SessionDir(getenv)
	if err != nil {
		return nil
	}
	return &update.Tracker{StateDir: filepath.Join(dir, "update"), PrevDir: filepath.Join(filepath.Dir(exe), update.PreviousDir), Version: v.String()}
}

// goBack starts the old setup. It reports true when the rollback window was started, so the app should stop instead of starting
// again. When it cannot be started the app starts as usual and says why in the log.
func goBack(t *update.Tracker, m update.Marker, launch func(path, sha, version string) error, logf func(string, ...any)) bool {
	logf("update %s failed to stay up %d times; going back to %s", m.To, update.MaxFailedStarts, m.From)
	t.Skip(m.To)
	t.MarkRecovered() // tried once: if the setup window is closed or refused, the next start runs the app as usual
	if err := launch(t.SetupPath(), m.SetupSHA256, m.From); err != nil {
		logf("could not start the old setup: %v", err)
		return false
	}
	return true
}

// watchHealth confirms the version after `after`. The returned func is called by run when the app ends on purpose: a short visit
// is not a failed start. A panic or an early error return never reaches it, so those stay counted (and a panic keeps its own stack).
func watchHealth(t *update.Tracker, after time.Duration) func(runErr error) {
	timer := time.AfterFunc(after, t.Confirm)
	return func(runErr error) {
		if timer.Stop() && runErr == nil {
			t.CleanExit()
		}
	}
}

// startHealth is called once, early in run. stopped is true when the app handed over to the old setup and must not start. end is
// called as soon as the app has stopped serving, before the slow clean-up, so a Windows end-session cannot cut it off.
func startHealth(demo bool, getenv func(string) string) (end func(runErr error), stopped bool) {
	end = func(error) {}
	t := healthTracker(demo, getenv)
	if t == nil {
		return end, false
	}
	switch act, m := t.Begin(); act {
	case update.ActRecover:
		return end, goBack(t, m, update.LaunchRollback, log.Printf)
	case update.ActWatch:
		return watchHealth(t, update.HealthyAfter), false
	}
	return end, false
}
