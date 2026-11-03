package setup

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// All tests here use the fake PC of newEnv: temporary folders only, never the real Desktop, Start menu or Startup folder.

func containsLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestUninstallRemovesDesktopAndQuitShortcuts(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if !exists(p) {
			t.Fatalf("install did not write %s", p)
		}
	}
	e.mustRun(OpUninstall, Options{}, UninstallOptions{})
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if exists(p) {
			t.Errorf("%s survived the uninstall", p)
		}
	}
	// The folders that hold the links are the user's: they stay.
	for _, d := range []string{e.dirs.Desktop, e.dirs.Startup, e.dirs.Programs} {
		if !exists(d) {
			t.Errorf("folder %s was removed", d)
		}
	}
}

func TestUninstallToleratesMissingNewShortcuts(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	for _, p := range []string{l.QuitLink, l.DesktopLink, l.StartupLink} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	res := e.mustRun(OpUninstall, Options{}, UninstallOptions{})
	if len(res.Left) != 0 {
		t.Fatalf("left: %v", res.Left)
	}
	if exists(l.StartMenuLink) {
		t.Fatal("the Start-menu link survived")
	}
}

func TestUninstallKeepsADesktopLinkThatPointsElsewhere(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	putLink(t, l.DesktopLink, e.root+`\Portable\unravis.exe`, "")
	before, err := os.ReadFile(l.DesktopLink)
	if err != nil {
		t.Fatal(err)
	}
	res := e.run(OpUninstall, Options{}, UninstallOptions{})
	after, err := os.ReadFile(l.DesktopLink)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the user's Desktop link changed (err %v)", err)
	}
	if !containsLine(res.Left, "kept: it points to another program") {
		t.Fatalf("the result does not report the kept link: %v", res.Left)
	}
	if exists(l.QuitLink) || exists(l.StartMenuLink) || exists(l.StartupLink) {
		t.Fatal("our other links were not removed")
	}
}

func TestUninstallKeepsALinkItCannotRead(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	writeFile(t, l.DesktopLink, "not a shortcut at all")
	e.run(OpUninstall, Options{}, UninstallOptions{})
	data, err := os.ReadFile(l.DesktopLink)
	if err != nil || string(data) != "not a shortcut at all" {
		t.Fatalf("the unreadable link was touched (err %v, data %q)", err, data)
	}
}

func TestUninstallAsWorkerLeavesTheDesktopAndStartupLinks(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.machineOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if !exists(l.DesktopLink) || !exists(l.StartupLink) {
		t.Fatal("the installing user's links were not written")
	}
	e.r.NoProfileLinks = true
	e.mustRun(OpUninstall, Options{}, UninstallOptions{})
	if !exists(l.DesktopLink) || !exists(l.StartupLink) {
		t.Error("the worker removed a link inside the user's profile")
	}
	if exists(l.StartMenuLink) || exists(l.QuitLink) {
		t.Error("the machine-scope Start-menu or Quit link survived the worker")
	}
}

func TestUninstallAsWorkerLeavesUserScopeStartMenuAndQuitLinks(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	e.r.NoProfileLinks = true
	e.mustRun(OpUninstall, Options{}, UninstallOptions{})
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if !exists(p) {
			t.Errorf("the worker removed %s inside the user's profile", p)
		}
	}
}

func TestDescribeChangesMentionsDesktopAndQuitShortcuts(t *testing.T) {
	d := captureDirs()
	o := Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}
	un := DescribeChanges(WorkRequest{Op: OpUninstall, Options: o}, nil, d, &FakeServices{}, false)
	for _, want := range []string{"Remove the Start menu shortcut and the Installed apps entry", "Remove the Quit Unravis shortcut from the Start menu",
		"Remove the Desktop shortcut (only if it points to Unravis)"} {
		if !containsLine(un.WillChange, want) {
			t.Errorf("uninstall list lacks %q: %v", want, un.WillChange)
		}
	}
	for _, op := range []Op{OpInstall, OpUpgrade, OpRepair} {
		ch := DescribeChanges(WorkRequest{Op: op, Options: o}, nil, d, &FakeServices{}, false)
		for _, want := range []string{"Start menu shortcut", "Quit Unravis shortcut in the Start menu", "Desktop shortcut", "Installed apps entry"} {
			found := false
			for _, line := range ch.WillChange {
				found = found || line == want
			}
			if !found {
				t.Errorf("%s list lacks %q: %v", op, want, ch.WillChange)
			}
		}
	}
}

func TestDescribeChangesWithoutDesktopHasNoDesktopLine(t *testing.T) {
	d := captureDirs()
	d.Desktop = ""
	o := Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}
	for _, op := range []Op{OpInstall, OpUninstall} {
		ch := DescribeChanges(WorkRequest{Op: op, Options: o}, nil, d, &FakeServices{}, false)
		if containsLine(ch.WillChange, "Desktop") {
			t.Errorf("%s list names the Desktop: %v", op, ch.WillChange)
		}
		if !containsLine(ch.WillChange, "Quit Unravis shortcut") {
			t.Errorf("%s list lacks the Quit line: %v", op, ch.WillChange)
		}
	}
}
