//go:build windows

package setup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// A normal folder still gets its permissions, and they reach the files and folders already inside it.
func TestSetSDDLStillWorksOnAFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
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
	if strings.Contains(got, ";;;BU)") {
		t.Fatalf("the folder still lets Users in: %s", got)
	}
	for _, p := range []string{sub, file} {
		c, err := sec.SDDL(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(c, ";;;BU)") {
			t.Fatalf("%s kept an old inherited entry for Users: %s", p, c)
		}
	}
}

// A link above the folder is refused too, not only a link at the folder.
func TestSetSDDLRefusesALinkInThePath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "rec"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "data")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput(); err != nil {
		t.Skipf("no junction here: %v %s", err, out)
	}
	var sec osSecurity
	if err := sec.SetSDDL(filepath.Join(link, "rec"), AdminOnlySDDL); !errors.Is(err, errIsLink) {
		t.Fatalf("SetSDDL through a linked parent = %v, want errIsLink", err)
	}
}
