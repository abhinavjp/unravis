package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the current file cannot be rotated away, the older backups must be left
// alone (the oldest used to be deleted first, then the rename failed, and the
// next write repeated it until the whole retained log was gone), and later
// writes keep appending to the current file.
func TestRotateFailureKeepsBackupsAndKeepsAppending(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, FileName, 100, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Two existing backups: the oldest one must survive.
	if err := os.WriteFile(w.backup(1), []byte("newer-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.backup(2), []byte("oldest-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Block the rotation step: the staging name is a non-empty directory, so the
	// current file cannot be moved there on any platform.
	block := w.stagingPath()
	if err := os.MkdirAll(filepath.Join(block, "child"), 0o700); err != nil {
		t.Fatal(err)
	}

	line := strings.Repeat("x", 60) + "\n"
	for i := 0; i < 6; i++ {
		if n, err := w.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("write %d: n=%d err=%v, want the write to succeed", i, n, err)
		}
	}

	if got := readFileString(t, w.backup(1)); got != "newer-backup" {
		t.Errorf("backup 1 = %q, want it untouched", got)
	}
	if got := readFileString(t, w.backup(2)); got != "oldest-backup" {
		t.Errorf("backup 2 = %q, want it untouched", got)
	}
	if got := readFileString(t, w.path()); got != strings.Repeat(line, 6) {
		t.Errorf("current file has %d bytes, want all 6 lines (%d bytes)", len(got), 6*len(line))
	}
	if w.lastRotateErr == nil {
		t.Error("the rotation failure was not recorded")
	}
}

// Once the blocker is gone the writer rotates again and the failure is cleared.
func TestRotateRecoversAfterFailure(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, FileName, 100, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	block := w.stagingPath()
	if err := os.MkdirAll(filepath.Join(block, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 60) + "\n"
	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(block); err != nil {
		t.Fatal(err)
	}
	// Past the back-off (the size at failure plus one maxSize) a write rotates.
	big := strings.Repeat("y", 150) + "\n"
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	if w.lastRotateErr != nil {
		t.Errorf("rotation still failing: %v", w.lastRotateErr)
	}
	if _, err := os.Stat(w.backup(1)); err != nil {
		t.Errorf("no backup after recovery: %v", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
