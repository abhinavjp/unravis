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
