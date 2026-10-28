//go:build windows

package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readAsPerson is ReadFolderSecurity (real file IDs, real ACLs) with the owner reported as a standard person's account whenever the real
// owner is one the trust check accepts. An elevated administrator (a CI runner is one) owns the folders it makes through the
// Administrators group, and its own account is on their inherited ACL, so the "a person made this folder" precondition would not hold
// there; this makes it hold everywhere and leaves a person's real owner untouched.
func readAsPerson(path string) (FolderSecurity, error) {
	fs, err := ReadFolderSecurity(path)
	if err == nil && adminOwner(fs.Owner) {
		fs.Owner = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	}
	return fs, err
}

// The real file IDs (no registry involved: the record is a fake): a rename keeps the folder's ID, so the record also names the path; a
// copy is a new folder with a new ID. A record naming this person's own folder still fails on its owner.
func TestFolderIDSurvivesRenameNotCopy(t *testing.T) {
	base := t.TempDir()
	orig := filepath.Join(base, "Unravis")
	mk := func() {
		for _, d := range []string{orig, filepath.Join(orig, "cases")} {
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk()
	a, err := ReadFolderSecurity(orig)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ReadFolderSecurity(filepath.Join(orig, "cases"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.ID == c.ID {
		t.Fatalf("IDs %q and %q", a.ID, c.ID)
	}
	rec := record(orig, a.ID, c.ID)
	if err := TrustSharedFolder(orig, readAsPerson, rec); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("the recorded folder should get as far as the owner check: %v", err)
	}
	moved := filepath.Join(base, "Moved")
	if err := os.Rename(orig, moved); err != nil {
		t.Fatal(err)
	}
	m, err := ReadFolderSecurity(moved)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != a.ID {
		t.Fatalf("a rename changed the ID: %q -> %q", a.ID, m.ID)
	}
	refused(t, "moved folder", TrustSharedFolder(moved, readAsPerson, rec))
	mk() // a copy (a new folder) under the recorded name
	cp, err := ReadFolderSecurity(orig)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID == a.ID {
		t.Fatal("a new folder under the same name kept the old ID")
	}
	if err := TrustSharedFolder(orig, readAsPerson, rec); err == nil || !strings.Contains(err.Error(), "moved, copied or replaced") {
		t.Fatalf("copy under the recorded name: %v", err)
	}
}

// The real reader on real folders, read only: a folder this test makes belongs to the person running it, which is exactly what a
// planted shared folder looks like; C:\ProgramData itself is owned by the system and gives people no control.
func TestReadFolderSecurityOnThisPC(t *testing.T) {
	dir := t.TempDir()
	fs, err := ReadFolderSecurity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckFolder(dir, fs); err == nil {
		t.Fatalf("a folder a person made must not be trusted: %+v", fs)
	}
	if err := os.Mkdir(filepath.Join(dir, "cases"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := TrustSharedFolder(dir, ReadFolderSecurity, noRecord); err == nil || errors.Is(err, ErrNotShared) {
		t.Fatalf("planted folder: %v", err)
	}
	if _, err := ReadFolderSecurity(filepath.Join(dir, "absent")); !errors.Is(err, ErrNotShared) {
		t.Fatalf("absent: %v", err)
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		t.Skip("no ProgramData")
	}
	fs, err = ReadFolderSecurity(pd)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckFolder(pd, fs); err != nil {
		t.Fatalf("%s should pass the owner and ACL check: %v (%+v)", pd, err, fs)
	}
}
