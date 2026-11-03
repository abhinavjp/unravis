package setup

import (
	"bytes"
	"os"
	"testing"
)

// All tests here use the fake PC of newEnv: temporary folders only, never the real Startup folder.

func savedStartAtSignIn(t *testing.T, e *testEnv) bool {
	t.Helper()
	old, err := Detect(e.sys.Reg)
	if err != nil || old == nil {
		t.Fatalf("detect: %v %v", old, err)
	}
	return old.State.StartAtSignIn
}

func TestUpgradeKeepsStartAtSignInOffWhenTheShortcutWasRemoved(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts() // saved option on
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if !savedStartAtSignIn(t, e) {
		t.Fatal("the install did not save the option as on")
	}
	if err := os.Remove(l.StartupLink); err != nil { // the tray toggle turned it off
		t.Fatal(err)
	}
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	if exists(l.StartupLink) {
		t.Fatal("the upgrade wrote the Startup link back")
	}
	if savedStartAtSignIn(t, e) {
		t.Fatal("the saved option is still on")
	}
}

func TestUpgradeKeepsStartAtSignInOnWhenTheShortcutExists(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	if _, args := readLink(t, l.StartupLink); args != startupLinkArg {
		t.Fatalf("Startup link args = %q", args)
	}
	if !savedStartAtSignIn(t, e) {
		t.Fatal("the saved option is off")
	}
}

func TestUpgradeTreatsALinkToAnotherProgramAsOff(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	putLink(t, l.StartupLink, e.root+`\Other\other.exe`, "")
	before, err := os.ReadFile(l.StartupLink)
	if err != nil {
		t.Fatal(err)
	}
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	after, err := os.ReadFile(l.StartupLink)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the foreign Startup link changed (err %v)", err)
	}
	if savedStartAtSignIn(t, e) {
		t.Fatal("the saved option is on")
	}
}

func TestUpgradeKeepsTheSavedValueWhenTheLinkCannotBeRead(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	orig := readLinkFile
	readLinkFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	defer func() { readLinkFile = orig }()
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	readLinkFile = orig
	if !savedStartAtSignIn(t, e) {
		t.Fatal("the saved value was lost on a read error")
	}
	if !exists(l.StartupLink) {
		t.Fatal("the Startup link is gone")
	}
}

func TestRepairRecreatesNothingForAUserWhoTurnedItOff(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if err := os.Remove(l.StartupLink); err != nil {
		t.Fatal(err)
	}
	e.mustRun(OpRepair, Options{}, UninstallOptions{})
	if exists(l.StartupLink) {
		t.Fatal("repair recreated the Startup link")
	}
	if savedStartAtSignIn(t, e) {
		t.Fatal("the saved option is on")
	}
}

func TestFreshInstallStillUsesTheCheckbox(t *testing.T) {
	for _, on := range []bool{true, false} {
		e := newEnv(t, "1.2.0")
		o := e.userOpts()
		o.StartAtSignIn = on
		e.mustRun(OpInstall, o, UninstallOptions{})
		l := NewLayout(o, e.dirs)
		if exists(l.StartupLink) != on {
			t.Errorf("checkbox %v: Startup link exists = %v", on, exists(l.StartupLink))
		}
		if savedStartAtSignIn(t, e) != on {
			t.Errorf("checkbox %v: saved value differs", on)
		}
	}
}

func TestSavedStateRecordsTheDerivedValue(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if err := os.Remove(l.StartupLink); err != nil {
		t.Fatal(err)
	}
	old, err := Detect(e.sys.Reg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.r.NewPlan(OpUpgrade, Options{StartAtSignIn: true}, UninstallOptions{}, old)
	if err != nil {
		t.Fatal(err)
	}
	if p.Opts.StartAtSignIn {
		t.Fatal("the plan took the request's value instead of the Startup folder's")
	}
	if res := e.r.Execute(t0ctx(), p); res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if savedStartAtSignIn(t, e) {
		t.Fatal("the saved state records the stale value")
	}
}

func TestWorkerPlanDoesNotReadTheStartupLink(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	old, err := Detect(e.sys.Reg)
	if err != nil {
		t.Fatal(err)
	}
	orig := readLinkFile
	readLinkFile = func(path string) ([]byte, error) {
		t.Errorf("the worker plan read %s", path)
		return nil, os.ErrPermission
	}
	defer func() { readLinkFile = orig }()
	e.r.NoProfileLinks = true
	for _, want := range []bool{false, true} {
		for _, op := range []Op{OpUpgrade, OpRepair} {
			p, err := e.r.NewPlan(op, Options{StartAtSignIn: want}, UninstallOptions{}, old)
			if err != nil {
				t.Fatal(err)
			}
			if p.Opts.StartAtSignIn != want {
				t.Errorf("%s: plan StartAtSignIn = %v, want the request's %v", op, p.Opts.StartAtSignIn, want)
			}
		}
	}
}
