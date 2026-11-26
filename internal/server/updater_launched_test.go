package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

// RV-SUP-1: a setup program that was started but never left a result must not keep the bar on "being applied" forever.

// launchRig is an Updater with a verified, downloaded update, a silent launcher that is counted, and a clock the test moves.
type launchRig struct {
	*installRig
	applied *atomic.Int32
	now     time.Time
}

func newLaunchRig(t *testing.T) *launchRig {
	t.Helper()
	rg, applied := autoRig(t)
	lr := &launchRig{installRig: rg, applied: applied, now: time.Now()}
	rg.u.clock = func() time.Time { return lr.now }
	return lr
}

func (lr *launchRig) advance(d time.Duration) { lr.now = lr.now.Add(d) }

func (lr *launchRig) status(t *testing.T) UpdateStatus {
	t.Helper()
	rec := request(lr.h, http.MethodGet, "/api/update/status", "", true)
	var s UpdateStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func wantInstallState(t *testing.T, s UpdateStatus, want string) {
	t.Helper()
	if s.Install == nil || s.Install.State != want {
		t.Fatalf("install state = %+v, want %q", s.Install, want)
	}
}

// The failing-before test: without expireStaleLaunch the state stays "launched" and no note is ever shown.
func TestLaunchedWithNoResultTimesOutToReadyWithAPlainNote(t *testing.T) {
	lr := newLaunchRig(t)
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	wantInstallState(t, lr.status(t), "launched")

	lr.advance(launchedLimit - time.Minute)
	s := lr.status(t)
	wantInstallState(t, s, "launched")
	if s.Result != nil {
		t.Fatalf("a note before the limit: %+v", s.Result)
	}

	lr.advance(2 * time.Minute) // 21 minutes with no result
	s = lr.status(t)
	wantInstallState(t, s, "ready")
	if s.Result == nil || s.Result.Message != launchTimeoutMessage || s.Result.Outcome != "failed" || s.Result.To != s.Install.Version {
		t.Fatalf("no plain note after the time-out: %+v", s.Result)
	}
	if lr.applied.Load() != 1 {
		t.Fatalf("the time-out started something: launched %d times", lr.applied.Load())
	}

	// A person can press Apply now again: it starts, and the old note is gone.
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	s = lr.status(t)
	wantInstallState(t, s, "launched")
	if s.Result != nil || lr.applied.Load() != 2 {
		t.Fatalf("after pressing again: result %+v, launched %d", s.Result, lr.applied.Load())
	}
}

// The automatic apply: a launch that dies silently is retried after a wait, and never more than autoMaxTries times in all.
func TestAutoApplyRetriesAfterATimeoutOnlyAfterBackOffAndNeverPastTheCap(t *testing.T) {
	lr := newLaunchRig(t)
	u := lr.u

	u.autoStep()
	if lr.applied.Load() != 1 {
		t.Fatalf("first automatic apply: %d", lr.applied.Load())
	}
	lr.advance(5 * time.Minute)
	u.autoStep() // still "launched": nothing happens
	if lr.applied.Load() != 1 {
		t.Fatalf("applied again while launched: %d", lr.applied.Load())
	}

	lr.advance(launchedLimit) // the run is lost
	u.autoStep()
	wantInstallState(t, u.Status(), "ready")
	if lr.applied.Load() != 1 {
		t.Fatalf("retried at once instead of waiting: %d", lr.applied.Load())
	}
	lr.advance(time.Minute)
	u.autoStep()
	if lr.applied.Load() != 1 {
		t.Fatalf("retried inside the wait: %d", lr.applied.Load())
	}
	lr.advance(5 * time.Minute) // past the 5 minute wait
	u.autoStep()
	if lr.applied.Load() != 2 {
		t.Fatalf("not retried after the wait: %d", lr.applied.Load())
	}

	lr.advance(launchedLimit + time.Minute) // lost again; two tries are used
	u.autoStep()
	wantInstallState(t, u.Status(), "ready")
	lr.advance(24 * time.Hour)
	for i := 0; i < 5; i++ {
		u.autoStep()
	}
	if lr.applied.Load() != 2 {
		t.Fatalf("applied past the cap: %d", lr.applied.Load())
	}
}

// A result that the run wrote after the launch keeps today's behaviour: Status turns a bad one into "failed" with its own message.
func TestAResultWrittenAfterTheLaunchIsLeftToTheExistingPath(t *testing.T) {
	lr := newLaunchRig(t)
	dir := t.TempDir()
	lr.u.SetResultDir(dir)
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	ver := lr.u.Status().Install.Version
	if err := update.WriteApplyResult(dir, update.ApplyResult{From: "0.1.0-rc.47", To: ver, Outcome: "failed", Message: "Windows asked for approval and it was refused.", At: lr.now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	lr.advance(launchedLimit + 5*time.Minute)
	s := lr.status(t)
	wantInstallState(t, s, "failed")
	if s.Install.Error != "Windows asked for approval and it was refused." || s.Result == nil || s.Result.Message != s.Install.Error {
		t.Fatalf("the run's own note was replaced: %+v %+v", s.Install, s.Result)
	}
	if lr.applied.Load() != 1 {
		t.Fatalf("launched %d times", lr.applied.Load())
	}
}

// A note from before the launch is an earlier run's: it does not hold the time-out back, and the new note is shown over it.
func TestAnEarlierRunsNoteDoesNotHoldBackTheTimeout(t *testing.T) {
	lr := newLaunchRig(t)
	dir := t.TempDir()
	lr.u.SetResultDir(dir)
	if err := update.WriteApplyResult(dir, update.ApplyResult{From: "0.1.0-rc.46", To: "0.1.0-rc.47", Outcome: "failed", Message: "old note", At: lr.now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	lr.advance(launchedLimit + time.Minute)
	s := lr.status(t)
	wantInstallState(t, s, "ready")
	if s.Result == nil || s.Result.Message != launchTimeoutMessage {
		t.Fatalf("%+v", s.Result)
	}
}

// While the setup program is still running there is no time-out, however long it takes.
func TestNoTimeoutWhileTheSetupProgramIsStillRunning(t *testing.T) {
	lr := newLaunchRig(t)
	var running atomic.Bool
	running.Store(true)
	lr.u.setupAlive = running.Load
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	lr.advance(3 * launchedLimit)
	wantInstallState(t, lr.status(t), "launched")
	lr.u.autoStep()
	if lr.applied.Load() != 1 {
		t.Fatalf("a second setup was started while the first ran: %d", lr.applied.Load())
	}
	running.Store(false)
	wantInstallState(t, lr.status(t), "ready")
}

// With a window (no silent apply) "launched" means the updater window is open; a person may leave it open for a long time.
func TestNoTimeoutForTheWindowedInstaller(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	now := time.Now()
	rg.u.clock = func() time.Time { return now }
	if err := rg.u.Install(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * launchedLimit)
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "launched" || s.Result != nil {
		t.Fatalf("%+v %+v", s.Install, s.Result)
	}
}

// OK on the note clears it; the state stays "ready" and the note does not come back.
func TestDismissingTheTimeoutNoteClearsIt(t *testing.T) {
	lr := newLaunchRig(t)
	if err := lr.u.Install(); err != nil {
		t.Fatal(err)
	}
	lr.advance(launchedLimit + time.Minute)
	if lr.status(t).Result == nil {
		t.Fatal("no note")
	}
	rec := request(lr.h, http.MethodPost, "/api/update/result/dismiss", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d", rec.Code)
	}
	s := lr.status(t)
	wantInstallState(t, s, "ready")
	if s.Result != nil {
		t.Fatalf("the note came back: %+v", s.Result)
	}
}
