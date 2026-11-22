//go:build windows

package blackbox

// BL-140 (b): the settings reader on real NTFS files with real access lists (the CI runner is an administrator). Fixtures that cannot be made
// are a t.Fatal, never a skip. The rest of the real-file cases (junction, reparse leaf, owner, over the limit, retry) are plan PR 6.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// lockedSettings writes data to a file in a fresh folder and locks it as the installer does (lockAdminOnlyFile).
func lockedSettings(t *testing.T, data []byte) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // the reader wants the file's final path, with no 8.3 names
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "blackbox.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lockAdminOnlyFile(p); err != nil {
		t.Fatalf("lockAdminOnlyFile: %v", err)
	}
	return p
}

func TestReadVerifiedSettingsReadsALockedFile(t *testing.T) {
	want := []byte(`{"name":"x"}`)
	p := lockedSettings(t, want)
	got, err := readOpenedSettings(p)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, %v; want %q", got, err, want)
	}
}

func TestReadOpenedSettingsRefusesAHardLink(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	if err := os.Link(p, p+".2"); err != nil {
		t.Fatal(err)
	}
	got, err := readOpenedSettings(p)
	if err == nil || got != nil {
		t.Fatalf("a file with two names was read: %q, %v", got, err)
	}
}

func TestReadOpenedSettingsRefusesAUsersWritableList(t *testing.T) {
	p := lockedSettings(t, []byte(`{}`))
	sd, err := windows.SecurityDescriptorFromString("D:PAI(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerAndProtectedDACL(p, owner, dacl); err != nil {
		t.Fatal(err)
	}
	if got, err := readOpenedSettings(p); err == nil || got != nil {
		t.Fatalf("a list that lets Users change the file was accepted: %q, %v", got, err)
	}
}

func TestReadOpenedSettingsHoldsTheFileWhileReading(t *testing.T) {
	want := []byte(`{"name":"proven"}`)
	p := lockedSettings(t, want)
	ran := false
	afterSettingsOpen = func() {
		ran = true
		if err := os.Rename(p, p+".moved"); err == nil {
			t.Error("the file was renamed away between the proof and the read")
		}
		if err := os.Remove(p); err == nil {
			t.Error("the file was deleted between the proof and the read")
		}
		if f, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
			f.Close()
			t.Error("the file was opened for writing between the proof and the read")
		}
	}
	t.Cleanup(func() { afterSettingsOpen = nil })
	got, err := readOpenedSettings(p)
	if !ran {
		t.Fatal("the hook did not run")
	}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %q, %v; want %q", got, err, want)
	}
}
