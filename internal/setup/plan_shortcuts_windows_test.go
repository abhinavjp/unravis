//go:build windows

package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSkipsAJunctionedDesktopAndStillSucceeds(t *testing.T) {
	e := newEnv(t, "1.2.0")
	other := filepath.Join(e.root, "OtherFolder")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.dirs.Desktop); err != nil {
		t.Fatal(err)
	}
	mkJunction(t, e.dirs.Desktop, other)
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(filepath.Join(other, ShortcutName)) || exists(l.DesktopLink) {
		t.Fatal("a Desktop link was written through the junction")
	}
	if ents, _ := os.ReadDir(other); len(ents) != 0 {
		t.Fatalf("the other folder got %d entries", len(ents))
	}
	if !exists(l.StartupLink) || !exists(l.QuitLink) || !exists(l.StartMenuLink) {
		t.Fatal("the other links were not written")
	}
	var skipped bool
	for _, line := range e.log {
		if strings.Contains(line, "shortcut skipped:") {
			skipped = true
		}
	}
	if !skipped {
		t.Error("the skipped link was not logged")
	}
}

func TestInstallAcceptsADesktopUnderAReparsePointParent(t *testing.T) {
	e := newEnv(t, "1.2.0")
	real := filepath.Join(e.root, "OneDriveReal")
	if err := os.MkdirAll(filepath.Join(real, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(e.root, "OneDrive")
	mkJunction(t, parent, real)
	e.r.Dirs.Desktop = filepath.Join(parent, "Desktop")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.r.Dirs)
	if !exists(l.DesktopLink) {
		t.Fatalf("the Desktop link %s was not written", l.DesktopLink)
	}
}
