//go:build windows

package setup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// SWP-1021-1: locking a folder must refuse a junction in its place, and must not touch the folder the junction points to.
func TestSetSDDLRefusesAJunction(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("no junction here: %v %s", err, out)
	}
	var sec osSecurity
	before, err := sec.SDDL(target)
	if err != nil {
		t.Fatal(err)
	}
	err = sec.SetSDDL(link, AdminOnlySDDL)
	if !errors.Is(err, errIsLink) {
		t.Fatalf("SetSDDL on a junction = %v, want errIsLink", err)
	}
	after, err := sec.SDDL(target)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("the folder behind the junction was changed:\nbefore %s\nafter  %s", before, after)
	}
}

// A normal folder still gets its permissions.
func TestSetSDDLStillWorksOnAFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var sec osSecurity
	if err := sec.SetSDDL(dir, AdminOnlySDDL); err != nil {
		t.Skipf("needs rights to set permissions here: %v", err)
	}
	got, err := sec.SDDL(dir)
	if err != nil || got == "" {
		t.Fatalf("read back: %q %v", got, err)
	}
}
