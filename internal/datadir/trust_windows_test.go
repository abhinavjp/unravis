//go:build windows

package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
	if err := TrustSharedFolder(dir, ReadFolderSecurity); err == nil || errors.Is(err, ErrNotShared) {
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
