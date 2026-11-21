package setup

import (
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

func restartPlan(t *testing.T, e *testEnv) *Plan {
	t.Helper()
	l := NewLayout(e.userOpts(), e.dirs)
	l.BlackboxDir = t.TempDir()
	return &Plan{r: e.r, Opts: e.userOpts(), Layout: l, Op: OpUpgrade}
}

func svcState(e *testEnv, name string) blackbox.SvcState {
	st, _ := e.f.Svc.State(name)
	return st
}

// An upgrade stops the Blackbox to replace its program. It must be running again afterwards.
func TestUpgradeStartsTheBlackboxAgainAfterStoppingIt(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(ServiceSpec{Name: blackbox.ServiceName}, blackbox.SvcRunning)
	p := restartPlan(t, e)
	tx := newTestTxn(t, e.sys)
	if err := p.stopServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if svcState(e, blackbox.ServiceName) != blackbox.SvcStopped {
		t.Fatal("test setup: the upgrade should have stopped it")
	}
	if err := p.restartServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if svcState(e, blackbox.ServiceName) != blackbox.SvcRunning {
		t.Fatal("the Blackbox was left stopped after the upgrade")
	}
}

// A recorder that was already stopped (an earlier upgrade left it off, a crash) comes back too, because nobody asked for it to stay off.
func TestUpgradeStartsAStoppedBlackboxNobodyStopped(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(ServiceSpec{Name: blackbox.ServiceName}, blackbox.SvcStopped)
	p := restartPlan(t, e)
	tx := newTestTxn(t, e.sys)
	if err := p.stopServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if err := p.restartServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if svcState(e, blackbox.ServiceName) != blackbox.SvcRunning {
		t.Fatal("a recorder nobody stopped on purpose must be started by an upgrade")
	}
}

// A person pressed Stop: the upgrade respects that.
func TestUpgradeLeavesABlackboxThePersonStoppedOnPurpose(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(ServiceSpec{Name: blackbox.ServiceName}, blackbox.SvcStopped)
	p := restartPlan(t, e)
	if err := os.WriteFile(filepath.Join(p.Layout.BlackboxDir, blackbox.StoppedByUserMarkerName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx := newTestTxn(t, e.sys)
	if err := p.stopServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if err := p.restartServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if svcState(e, blackbox.ServiceName) != blackbox.SvcStopped {
		t.Fatal("a recorder stopped on purpose must stay stopped")
	}
}

// No Blackbox on this PC: nothing is created or started.
func TestUpgradeStartsNothingWhenNoBlackboxIsInstalled(t *testing.T) {
	e := newEnv(t, "1.0.0")
	p := restartPlan(t, e)
	tx := newTestTxn(t, e.sys)
	if err := p.restartServices(t0ctx(), tx); err != nil {
		t.Fatal(err)
	}
	if svcState(e, blackbox.ServiceName) != blackbox.SvcNotInstalled {
		t.Fatal("nothing should have been installed")
	}
}

func TestUpgradePlansTheRestartStepAndAFirstInstallDoesNot(t *testing.T) {
	has := func(steps []Step) bool {
		for _, s := range steps {
			if s.ID == "restart-services" {
				return true
			}
		}
		return false
	}
	e := newEnv(t, "1.0.0")
	if !has(installSteps(&Plan{Op: OpUpgrade, r: e.r, Opts: e.userOpts()})) {
		t.Fatal("an upgrade must start the services again")
	}
	if has(installSteps(&Plan{Op: OpInstall, r: e.r, Opts: e.userOpts()})) {
		t.Fatal("a first install stops nothing, so it restarts nothing")
	}
}
