//go:build windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction: %v %s", err, out)
	}
}

func TestWriteProfileLinksSkipsAJunctionedDesktopAndLogs(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	other := filepath.Join(e.root, "OtherFolder")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.dirs.Desktop); err != nil {
		t.Fatal(err)
	}
	mkJunction(t, e.dirs.Desktop, other)
	var logs []string
	if err := e.r.WriteProfileLinks(l, true, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || !strings.HasPrefix(logs[0], "shortcut skipped:") {
		t.Fatalf("logs = %q", logs)
	}
	if ents, _ := os.ReadDir(other); len(ents) != 0 {
		t.Fatalf("the other folder got %d entries", len(ents))
	}
	if !exists(l.StartupLink) {
		t.Fatal("the Startup link was not written")
	}
}

func TestWriteProfileLinksAcceptsADesktopWhoseParentIsAReparsePoint(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	real := filepath.Join(e.root, "OneDriveReal")
	if err := os.MkdirAll(filepath.Join(real, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(e.root, "OneDrive")
	mkJunction(t, parent, real)
	l.DesktopLink = filepath.Join(parent, "Desktop", ShortcutName)
	var logs []string
	if err := e.r.WriteProfileLinks(l, false, func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 || !exists(l.DesktopLink) {
		t.Fatalf("logs %q, written %v", logs, exists(l.DesktopLink))
	}
}

func TestRemoveProfileLinksDoesNotFollowASymlinkedLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := machineLayout(e)
	real := filepath.Join(e.root, "Elsewhere", "real.lnk")
	putLink(t, real, l.AppExe, "")
	if err := os.Symlink(real, l.DesktopLink); err != nil {
		t.Skipf("cannot make a symlink: %v", err)
	}
	lines := e.r.RemoveProfileLinks(l)
	if _, err := os.Lstat(l.DesktopLink); err != nil {
		t.Fatalf("the symlink was removed: %v", err)
	}
	if !exists(real) {
		t.Fatal("the link target was removed")
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %q", lines)
	}
}
