package server

import (
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
