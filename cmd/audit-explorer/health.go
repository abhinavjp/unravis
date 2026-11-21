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
	// A cut-off update is put back a little after start, when the app listens for a quit request: setup asks it to quit first, and an
	// app killed in the middle of its own start would count as a failed start. The timer stops with the app.
	var recoverTimer *time.Timer
	if exe, err := os.Executable(); err == nil {
		setupPath := filepath.Join(filepath.Dir(exe), "unravis-setup.exe")
		recoverTimer = time.AfterFunc(recoverDelay, func() {
			recoverCutOff(t, update.JournalFolders(os.Getenv("ProgramData"), os.Getenv("LOCALAPPDATA")), time.Now(),
				setupPath, update.SetupCanRecover, update.LaunchRecover, writeNote, log.Printf)
		})
	}
	stopRecover := func() {
		if recoverTimer != nil {
			recoverTimer.Stop()
		}
	}
	switch act, m := t.Begin(); act {
	case update.ActRecover:
		stopRecover()
		return end, goBack(t, m, update.LaunchRollback, log.Printf)
	case update.ActWatch:
		watch := watchHealth(t, update.HealthyAfter)
		return func(runErr error) { stopRecover(); watch(runErr) }, false
	}
	return func(error) { stopRecover() }, false
}

// recoverDelay is how long after start the app looks for a cut-off update.
const recoverDelay = 30 * time.Second

// recoverCutOff is BL-170: an update that was cut off (power loss, a killed setup) left its journal on disk. The setup program in the
// program folder knows how to put that back (`unravis-setup.exe --recover`): it starts it, hidden, when that file knows the flag, and the
// setup leaves the result note itself. It never takes the app to another version, and it never reads the journal itself, so a journal
// someone planted can make it run only that fixed command. Without a setup that knows the flag (a version from before this one) the app
// says, once, to run Setup > Repair. A run is acted on at most update.MaxJournalTries times, so a recovery that cannot finish never loops.
func recoverCutOff(t *update.Tracker, folders []string, now time.Time, setupPath string, canRecover func(string) bool, launch func(string) error, note func(update.ApplyResult), logf func(string, ...any)) bool {
	pj, ok := update.FindPendingJournal(folders, now, t.JournalTried)
	if !ok {
		return false
	}
	first := t.JournalTries(pj.Started) == 0
	t.MarkJournalTried(pj.Started)
	logf("an earlier setup run (to %s) was cut off (journal %s)", pj.Version, pj.Path)
	if canRecover(setupPath) {
		if err := launch(setupPath); err == nil {
			logf("started setup --recover")
			return true
		} else {
			logf("could not start setup --recover: %v", err)
		}
	}
	if first {
		note(update.ApplyResult{To: pj.Version, Outcome: "failed", At: now,
			Message: "An update to " + pj.Version + " was cut off before it finished. Unravis may not be fully updated. Run Unravis Setup from the Unravis program folder and choose Repair; it puts back what was changed first."})
	}
	return true
}

// writeNote leaves a result note for the app to show (the same note a silent update leaves).
func writeNote(r update.ApplyResult) {
	if cache, err := os.UserCacheDir(); err == nil {
		_ = update.WriteApplyResult(filepath.Join(cache, "Unravis", "update-result"), r)
	}
}
