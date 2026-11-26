package server

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

func TestPresenceWindow(t *testing.T) {
	var p Presence
	now := time.Now()
	if p.Active(now, time.Minute) {
		t.Fatal("active before any sign of use")
	}
	p.Touch()
	if !p.Active(time.Now(), time.Minute) {
		t.Fatal("not active right after a touch")
	}
	if p.Active(time.Now().Add(2*time.Minute), time.Minute) {
		t.Fatal("still active after the window")
	}
}

// autoRig is an Updater whose verified update is downloaded and ready; silent applies are counted.
func autoRig(t *testing.T) (*installRig, *atomic.Int32) {
	t.Helper()
	rg := newInstallRig(t)
	rg.readyDownload()
	var applied atomic.Int32
	rg.u.EnableSilentApply(func(path, sha, ver string) error { applied.Add(1); return nil }, func() bool { return true }, nil)
	return rg, &applied
}

func TestAutoStepAppliesOnlyWhenNobodyIsUsingTheApp(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	u.presence.Touch()
	u.autoStep()
	if applied.Load() != 0 {
		t.Fatal("applied while the app was in use")
	}
	u.presence.mu.Lock()
	u.presence.last = time.Now().Add(-10 * time.Minute)
	u.presence.mu.Unlock()
	u.autoStep()
	if applied.Load() != 1 {
		t.Fatalf("not applied while idle (applied %d)", applied.Load())
	}
}

func TestAutoStepNeedsNoAdminPromptSettingAndStopsAfterTwoTries(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	u.canSilently = func() bool { return false }
	u.autoStep()
	if applied.Load() != 0 {
		t.Fatal("applied although it would need an administrator prompt")
	}
	u.canSilently = func() bool { return true }
	s := u.Settings()
	s.ApplyAuto = false
	u.mu.Lock()
	u.settings = s
	u.mu.Unlock()
	u.autoStep()
	if applied.Load() != 0 {
		t.Fatal("applied although automatic apply is off")
	}
	s.ApplyAuto = true
	u.mu.Lock()
	u.settings = s
	u.mu.Unlock()
	for i := 0; i < 5; i++ {
		u.inst.mu.Lock()
		u.inst.info.State = "ready"
		u.inst.mu.Unlock()
		u.autoStep()
	}
	if applied.Load() != autoMaxTries {
		t.Fatalf("applied %d times, want %d", applied.Load(), autoMaxTries)
	}
}

func TestStatusReportsTheResultOfAnUnattendedUpdateUntilDismissed(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	dir := t.TempDir()
	u.SetResultDir(dir)
	if u.Status().Result != nil {
		t.Fatal("a result from nowhere")
	}
	if err := update.WriteApplyResult(dir, update.ApplyResult{From: "1.0.0", To: "1.1.0", Outcome: "rolled-back", Message: "It did not start.", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if r := u.Status().Result; r == nil || r.Outcome != "rolled-back" || r.To != "1.1.0" {
		t.Fatalf("result %+v", r)
	}
	u.DismissResult()
	if u.Status().Result != nil {
		t.Fatal("still shown after dismissing")
	}
}

func TestDownloadBackoffGrowsAndIsCapped(t *testing.T) {
	want := []time.Duration{0, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := dlBackoff(i); got != w {
			t.Errorf("fails %d: %v, want %v", i, got, w)
		}
	}
}

func TestAutoStepWaitsAfterAFailedDownload(t *testing.T) {
	rg := newInstallRig(t)
	rg.u.CheckNow(t.Context())
	rg.feed.code = 503 // the file cannot be fetched
	var applied atomic.Int32
	rg.u.EnableSilentApply(func(path, sha, ver string) error { applied.Add(1); return nil }, func() bool { return true }, nil)
	rg.u.autoStep() // starts the download
	rg.waitFor("failed")
	rg.u.autoStep() // sees the failure and starts the wait
	rg.feed.code = 0
	rg.u.autoStep() // still waiting: no new download
	time.Sleep(50 * time.Millisecond)
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "failed" {
		t.Fatalf("a download started during the wait: %+v", s.Install)
	}
	rg.u.auto.mu.Lock()
	rg.u.auto.dlNext = time.Now().Add(-time.Second)
	rg.u.auto.mu.Unlock()
	rg.u.autoStep() // wait over: tries again, and now it works
	rg.waitFor("ready")
}

func TestAutoTriesSurviveARestartSoAFailingUpdateIsNotAppliedForever(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	for i := 0; i < autoMaxTries; i++ {
		u.inst.mu.Lock()
		u.inst.info.State = "ready"
		u.inst.mu.Unlock()
		u.autoStep()
	}
	if applied.Load() != autoMaxTries {
		t.Fatalf("applied %d", applied.Load())
	}
	// The app restarts: a new Updater over the same settings folder, with the same update ready again.
	rg2 := newInstallRig(t)
	rg2.u.dir = u.dir
	rg2.readyDownload()
	var again atomic.Int32
	rg2.u.EnableSilentApply(func(path, sha, ver string) error { again.Add(1); return nil }, func() bool { return true }, nil)
	rg2.u.autoStep()
	if again.Load() != 0 {
		t.Fatal("the restarted program applied the same failing version again")
	}
}

func TestALaunchFailureGoesBackToReadyAndIsNotADownloadFailure(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	u.inst.mu.Lock()
	u.inst.info.State, u.inst.info.Error = "failed", "the setup program closed right away"
	u.inst.mu.Unlock()
	u.autoStep() // the person's own failed Apply now stays on screen: no reset
	if s := u.Status(); s.Install == nil || s.Install.State != "failed" || s.Install.Error == "" {
		t.Fatalf("a failed Apply now was reset: %+v", s.Install)
	}
	u.auto.mu.Lock()
	u.auto.failedByAuto = true // as if the automatic apply had caused it
	u.auto.mu.Unlock()
	u.autoStep() // file is fine: back to ready, no new download, no wait recorded
	if s := u.Status(); s.Install == nil || s.Install.State != "ready" {
		t.Fatalf("state %+v", s.Install)
	}
	u.auto.mu.Lock()
	fails := u.auto.dlFails
	u.auto.mu.Unlock()
	if fails != 0 {
		t.Fatalf("a launch failure counted as a download failure (%d)", fails)
	}
	u.autoStep() // now it applies (second try)
	if applied.Load() != 1 {
		t.Fatalf("applied %d", applied.Load())
	}
}

func TestLaunchedBarDoesNotStayWhenTheRunFailed(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	dir := t.TempDir()
	u.SetResultDir(dir)
	u.inst.mu.Lock()
	u.inst.info.State = "launched"
	ver := u.inst.info.Version
	u.inst.launchedAt = time.Now().Add(-time.Minute)
	u.inst.mu.Unlock()
	// a note from BEFORE this launch is an earlier run's: it does not change the state
	if err := update.WriteApplyResult(dir, update.ApplyResult{From: "0.1.0-rc.47", To: ver, Outcome: "failed", Message: "old", At: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.Install == nil || s.Install.State != "launched" {
		t.Fatalf("an earlier run's note changed this launch: %+v", s.Install)
	}
	if err := update.WriteApplyResult(dir, update.ApplyResult{From: "0.1.0-rc.47", To: ver, Outcome: "failed", Message: "Windows asked for approval and it was refused.", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.Install == nil || s.Install.State != "failed" || s.Install.Error == "" {
		t.Fatalf("%+v", s.Install)
	}
}

func TestABusyInstallIsNotCountedAsATry(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	u.inst.mu.Lock()
	u.inst.busy = true // the person pressed Apply now at the same moment
	u.inst.mu.Unlock()
	u.autoStep()
	if applied.Load() != 0 || u.autoTries(u.inst.info.Version) != 0 {
		t.Fatalf("applied %d, tries %d", applied.Load(), u.autoTries(u.inst.info.Version))
	}
}

func TestInUseCountsMCPRequestsRunningWorkAndExtraBusyChecks(t *testing.T) {
	u := &Updater{}
	if u.InUse() {
		t.Fatal("a fresh updater is idle")
	}
	end := u.WorkBegin()
	if !u.InUse() {
		t.Fatal("a request in flight is work")
	}
	end()
	if u.InUse() {
		t.Fatal("the request ended")
	}
	u.NoteMCP()
	if !u.InUse() {
		t.Fatal("an MCP client that just asked counts as in use")
	}
	u2 := &Updater{}
	var busy atomic.Bool
	u2.AddBusy(busy.Load)
	busy.Store(true)
	if !u2.InUse() {
		t.Fatal("an extra busy check counts")
	}
	busy.Store(false)
	if u2.InUse() {
		t.Fatal("the extra check says idle")
	}
}

func TestAnImportJobStillRunningIsBusy(t *testing.T) {
	var j importJobs
	if j.anyRunning() {
		t.Fatal("no jobs")
	}
	j.m = map[string]*importJob{"a": {state: jobDone}, "b": {state: jobRunning}}
	if !j.anyRunning() {
		t.Fatal("a running job must count")
	}
	j.m["b"].state = jobFailed
	if j.anyRunning() {
		t.Fatal("only finished jobs")
	}
}

func idleNow(u *Updater) {
	u.presence.mu.Lock()
	u.presence.last = time.Now().Add(-10 * time.Minute)
	u.presence.mu.Unlock()
}

func TestAutoStepRecheckHeldIsNotATry(t *testing.T) {
	rg, applied := autoRig(t)
	idleNow(rg.u)
	rg.publishControl(rg.control(100, func(c *update.Control) { c.Pause = true }))
	rg.u.autoStep()
	if applied.Load() != 0 {
		t.Fatal("applied while the publisher had paused it")
	}
	if n := rg.u.autoTries("0.1.0-rc.48"); n != 0 {
		t.Fatalf("a refused re-check counted as %d tries", n)
	}
}

func TestAutoStepRecheckOfflineBacksOff(t *testing.T) {
	rg, applied := autoRig(t)
	idleNow(rg.u)
	rg.feed.code = http.StatusServiceUnavailable
	rg.u.autoStep()
	hits := rg.feed.hits.Load()
	if applied.Load() != 0 || hits == 0 {
		t.Fatalf("applied %d, hits %d", applied.Load(), hits)
	}
	rg.u.autoStep() // inside the wait: no call to the feed
	if rg.feed.hits.Load() != hits {
		t.Fatal("the feed was asked again inside the wait")
	}
	if n := rg.u.autoTries("0.1.0-rc.48"); n != 0 {
		t.Fatalf("an unreachable feed counted as %d tries", n)
	}
	rg.u.auto.mu.Lock()
	rg.u.auto.rcNext = time.Now().Add(-time.Second)
	rg.u.auto.mu.Unlock()
	rg.feed.code = 0
	rg.u.autoStep()
	if applied.Load() != 1 {
		t.Fatalf("not applied after the wait (applied %d)", applied.Load())
	}
}

// The downloaded file is hashed once, not every minute, while a failed Apply now stays on screen.
func TestFileStillThereIsRemembered(t *testing.T) {
	rg, _ := autoRig(t)
	m := rg.u.availableManifest()
	if m == nil || !rg.u.fileStillThere(m) {
		t.Fatal("the downloaded file was not found")
	}
	got, ok := rg.u.dl.Recheck(m, update.AssetSetup)
	if !ok {
		t.Fatal("recheck")
	}
	if err := os.Remove(got.Path); err != nil {
		t.Fatal(err)
	}
	if !rg.u.fileStillThere(m) { // the answer is remembered for ten minutes; Install hashes again before any start
		t.Fatal("the remembered answer was dropped")
	}
	rg.u.auto.mu.Lock()
	rg.u.auto.fileOKAt = time.Now().Add(-11 * time.Minute)
	rg.u.auto.mu.Unlock()
	if rg.u.fileStillThere(m) {
		t.Fatal("a missing file was reported as there after the memory expired")
	}
}

// Apply now uses the launcher that reopens the page; the automatic apply never does.
func TestApplyNowUsesTheReopenLauncherAndAutoDoesNot(t *testing.T) {
	rg, applied := autoRig(t)
	u := rg.u
	var reopened atomic.Int32
	u.EnableSilentReopen(func(path, sha, ver string) error { reopened.Add(1); return nil })
	u.presence.mu.Lock()
	u.presence.last = time.Now().Add(-10 * time.Minute)
	u.presence.mu.Unlock()
	u.autoStep()
	if applied.Load() != 1 || reopened.Load() != 0 {
		t.Fatalf("auto: silent %d, reopen %d", applied.Load(), reopened.Load())
	}
	u.inst.mu.Lock()
	u.inst.info.State = "ready"
	u.inst.mu.Unlock()
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if applied.Load() != 1 || reopened.Load() != 1 {
		t.Fatalf("apply now: silent %d, reopen %d", applied.Load(), reopened.Load())
	}
}

func TestSecondApplyNowRightAfterALaunchIsRefused(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	var reopened atomic.Int32
	u.EnableSilentReopen(func(path, sha, ver string) error { reopened.Add(1); return nil })
	u.inst.mu.Lock()
	u.inst.info.State = "ready"
	u.inst.mu.Unlock()
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); err != errStarting {
		t.Fatalf("second press: %v", err)
	}
	if reopened.Load() != 1 {
		t.Fatalf("setup started %d times", reopened.Load())
	}
	u.inst.mu.Lock()
	u.inst.launchedAt = time.Now().Add(-time.Minute)
	u.inst.mu.Unlock()
	if err := u.Install(); err != nil || reopened.Load() != 2 {
		t.Fatalf("a press a minute later must start again: %v, %d", err, reopened.Load())
	}
}

// The 20 second guard is for the silent setup only (no window to close). With the window installer the person may close it and press
// again at once, so a second press right after "launched" must start it again.
func TestWindowInstallerAgainRightAfterALaunchIsNotBlockedByTheStartingGuard(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	u := rg.u // no EnableSilentApply: this is the installer window path
	if u.silent != nil {
		t.Fatal("the rig must have no silent launcher")
	}
	if err := u.Install(); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(); err != nil {
		t.Fatalf("second press with the window installer: %v", err)
	}
	if rg.launch.Load() != 2 {
		t.Fatalf("installer started %d times, want 2", rg.launch.Load())
	}
}

// Close waits for the quiet-time loop (RV-SRV-3): while a step is still running, Close does not return; once it ends, it does.
func TestCloseWaitsForTheAutomaticLoop(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	oldTick := autoTick
	autoTick = 5 * time.Millisecond
	t.Cleanup(func() { autoTick = oldTick })
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	u.canSilently = func() bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return false
	}
	ctx, cancel := context.WithCancel(t.Context())
	u.StartAuto(ctx)
	<-entered // the loop is inside a step
	u.auto.mu.Lock()
	done := u.auto.loops[0]
	u.auto.mu.Unlock()
	cancel()
	closed := make(chan struct{})
	go func() { u.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while the automatic loop was still running")
	case <-time.After(150 * time.Millisecond):
	}
	close(gate)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the loop ended")
	}
	select {
	case <-done:
	default:
		t.Fatal("Close returned before the loop had exited")
	}
	// A second Close has nothing left to wait for.
	started := time.Now()
	u.Close()
	if time.Since(started) > autoCloseWait/2 {
		t.Fatal("a second Close waited again")
	}
}

// A step still in flight must not be able to start a download after Close stopped it: the loop is waited for first, the download
// is stopped after.
func TestCloseStopsTheDownloadOnlyAfterTheLoopEnded(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	oldTick := autoTick
	autoTick = 5 * time.Millisecond
	t.Cleanup(func() { autoTick = oldTick })
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	var loopEnded atomic.Bool
	u.canSilently = func() bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		loopEnded.Store(true)
		return false
	}
	var loopEndedAtStop atomic.Bool
	u.inst.mu.Lock()
	u.inst.cancel = func() { loopEndedAtStop.Store(loopEnded.Load()) }
	u.inst.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	u.StartAuto(ctx)
	<-entered
	cancel()
	time.AfterFunc(50*time.Millisecond, func() { close(gate) })
	u.Close()
	if !loopEndedAtStop.Load() {
		t.Fatal("the download was stopped while the automatic loop was still running")
	}
}

// A step that never ends does not hold the program: Close gives up after its limit.
func TestCloseGivesUpOnAStuckAutomaticLoop(t *testing.T) {
	rg, _ := autoRig(t)
	u := rg.u
	oldTick, oldWait := autoTick, autoCloseWait
	autoTick, autoCloseWait = 5*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { autoTick, autoCloseWait = oldTick, oldWait })
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	u.canSilently = func() bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		return false
	}
	ctx, cancel := context.WithCancel(t.Context())
	u.StartAuto(ctx)
	<-entered
	cancel()
	start := time.Now()
	u.Close()
	if d := time.Since(start); d < autoCloseWait || d > 3*time.Second {
		t.Fatalf("Close took %v, want about %v", d, autoCloseWait)
	}
}
