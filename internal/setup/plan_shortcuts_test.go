package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// All tests here use the fake PC of newEnv: temporary folders only, never the real Desktop, Start menu or Startup folder.

func linkArgs(t *testing.T, path string) string {
	t.Helper()
	_, args := readLink(t, path)
	return args
}

func TestInstallWritesTheQuitShortcut(t *testing.T) {
	for _, scope := range []string{"machine", "user"} {
		e := newEnv(t, "1.2.0")
		o := e.userOpts()
		if scope == "machine" {
			o = e.machineOpts()
		}
		e.mustRun(OpInstall, o, UninstallOptions{})
		l := NewLayout(o, e.dirs)
		target, args := readLink(t, l.QuitLink)
		if !samePath(target, l.AppExe) || args != "--quit" {
			t.Errorf("%s: Quit link = %q %q, want %q --quit", scope, target, args, l.AppExe)
		}
		if filepath.Base(l.QuitLink) != "Quit Unravis.lnk" || filepath.Dir(l.QuitLink) != filepath.Dir(l.StartMenuLink) {
			t.Errorf("%s: Quit link at %s", scope, l.QuitLink)
		}
		// The Start-menu link still opens Unravis as before.
		target, args = readLink(t, l.StartMenuLink)
		if !samePath(target, l.AppExe) || args != "" {
			t.Errorf("%s: Start-menu link = %q %q", scope, target, args)
		}
	}
}

func TestInstallWritesTheDesktopShortcut(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if l.DesktopLink == "" {
		t.Fatal("the layout has no Desktop link")
	}
	target, args := readLink(t, l.DesktopLink)
	if !samePath(target, l.AppExe) || args != "" {
		t.Fatalf("Desktop link = %q %q", target, args)
	}
}

func TestInstallWithoutADesktopFolderSkipsTheDesktopShortcut(t *testing.T) {
	e := newEnv(t, "1.2.0")
	if err := os.Remove(e.dirs.Desktop); err != nil {
		t.Fatal(err)
	}
	e.r.Dirs.Desktop = ""
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	if exists(e.dirs.Desktop) || exists(filepath.Join(e.dirs.Desktop, ShortcutName)) {
		t.Fatal("a Desktop folder or link was made")
	}
	l := NewLayout(o, e.r.Dirs)
	if l.DesktopLink != "" || !exists(l.StartMenuLink) || !exists(l.QuitLink) || !exists(l.StartupLink) {
		t.Fatalf("layout Desktop %q, start menu %v, quit %v, startup %v", l.DesktopLink, exists(l.StartMenuLink), exists(l.QuitLink), exists(l.StartupLink))
	}
}

func TestInstallKeepsAnExistingDesktopLinkThatPointsElsewhere(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	other := filepath.Join(e.root, "Other", "other.exe")
	putLink(t, l.DesktopLink, other, "")
	before, err := os.ReadFile(l.DesktopLink)
	if err != nil {
		t.Fatal(err)
	}
	e.mustRun(OpInstall, o, UninstallOptions{})
	after, err := os.ReadFile(l.DesktopLink)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the foreign Desktop link changed (err %v)", err)
	}
	if !exists(l.StartMenuLink) || !exists(l.QuitLink) {
		t.Fatal("the other links were not written")
	}
}

func TestStartupShortcutHasTheBackgroundArgument(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	target, args := readLink(t, l.StartupLink)
	if !samePath(target, l.AppExe) || args != "--background" {
		t.Fatalf("Startup link = %q %q", target, args)
	}
}

func TestFailedInstallRemovesTheNewShortcuts(t *testing.T) {
	e := newEnv(t, "1.2.0")
	before := e.snapshot()
	e.r.FailAfter = func(step string) error {
		if step == "shortcuts" {
			return errors.New("injected fault")
		}
		return nil
	}
	o := e.userOpts()
	res := e.run(OpInstall, o, UninstallOptions{})
	if res.Outcome == OutcomeDone {
		t.Fatalf("the install finished: %+v", res)
	}
	l := NewLayout(o, e.dirs)
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if exists(p) {
			t.Errorf("%s is left", p)
		}
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("not put back:\n%s", d)
	}
}

func TestInstallWithoutStartAtSignInWritesNoStartupLink(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	o.StartAtSignIn = false
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(l.StartupLink) {
		t.Fatal("a Startup link was written")
	}
	if !exists(l.DesktopLink) || !exists(l.QuitLink) || !exists(l.StartMenuLink) {
		t.Fatal("the other links are missing")
	}
}

func TestPlanWithNoProfileLinksWritesOnlyTheStartMenuAndQuitLinksInMachineScope(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.NoProfileLinks = true
	for _, d := range []string{e.dirs.Desktop, e.dirs.Startup} {
		if err := os.Remove(d); err != nil {
			t.Fatal(err)
		}
	}
	o := e.machineOpts() // the sign-in option is on
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	for _, p := range []string{l.StartMenuLink, l.QuitLink} {
		if !exists(p) {
			t.Errorf("%s is missing", p)
		}
	}
	for _, p := range []string{l.DesktopLink, l.StartupLink, e.dirs.Desktop, e.dirs.Startup} {
		if exists(p) {
			t.Errorf("%s exists", p)
		}
	}
}

func TestPlanWithNoProfileLinksWritesNoLinkUnderTheProfileInUserScope(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.NoProfileLinks = true
	// Desktop, Startup and the Start-menu Programs folder are gone, so a stray MkdirAll would show.
	for _, d := range []string{e.dirs.Desktop, e.dirs.Startup, e.dirs.Programs} {
		if err := os.Remove(d); err != nil {
			t.Fatal(err)
		}
	}
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink, e.dirs.Desktop, e.dirs.Startup, e.dirs.Programs} {
		if exists(p) {
			t.Errorf("%s exists", p)
		}
	}
}

func TestUpgradeRewritesAnOlderStartupShortcut(t *testing.T) {
	for _, op := range []Op{OpUpgrade, OpRepair} {
		e := newEnv(t, "1.2.0")
		o := e.userOpts()
		e.mustRun(OpInstall, o, UninstallOptions{})
		l := NewLayout(o, e.dirs)
		// What an older setup wrote: our program, no argument.
		putLink(t, l.StartupLink, l.AppExe, "")
		if got := linkArgs(t, l.StartupLink); got != "" {
			t.Fatalf("setup of the old link: args %q", got)
		}
		e.mustRun(op, Options{}, UninstallOptions{})
		target, args := readLink(t, l.StartupLink)
		if !samePath(target, l.AppExe) || args != "--background" {
			t.Errorf("%s: Startup link = %q %q, want %q --background", op, target, args, l.AppExe)
		}
	}
}

func TestUpgradeWithSignInOffLeavesTheStartupFolderAlone(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	o.StartAtSignIn = false
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	e.mustRun(OpUpgrade, Options{}, UninstallOptions{})
	if exists(l.StartupLink) {
		t.Fatal("an upgrade with the option off wrote a Startup link")
	}
}

func TestInstallKeepsAnExistingStartupLinkThatPointsElsewhere(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	o.StartAtSignIn = true
	l := NewLayout(o, e.dirs)
	putLink(t, l.StartupLink, filepath.Join(e.root, "Other", "other.exe"), "")
	before, err := os.ReadFile(l.StartupLink)
	if err != nil {
		t.Fatal(err)
	}
	e.mustRun(OpInstall, o, UninstallOptions{})
	after, err := os.ReadFile(l.StartupLink)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the foreign Startup link changed (err %v)", err)
	}
	if !exists(l.StartMenuLink) || !exists(l.QuitLink) {
		t.Fatal("the other links were not written")
	}
}
