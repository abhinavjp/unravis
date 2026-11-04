package sqlite

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A file that is already gone is a successful removal, and an ordinary file is removed.
func TestRemoveFileMissingIsSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := removeFile(filepath.Join(dir, "never-existed")); err != nil {
		t.Fatalf("removeFile of a missing file: %v", err)
	}
	p := filepath.Join(dir, "case.db")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeFile(p); err != nil {
		t.Fatalf("removeFile: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still there after removeFile: %v", err)
	}
}

// On Windows a file another handle still has open cannot be deleted; removeFile
// waits for the handle to go (a reader unwinding after Erase) and then succeeds.
func TestRemoveFileOutlastsABriefSharingViolation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("an open file can be deleted on this OS")
	}
	p := filepath.Join(t.TempDir(), "case.db-wal")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = f.Close()
		close(released)
	}()
	if err := removeFile(p); err != nil {
		t.Fatalf("removeFile while the file was briefly open: %v", err)
	}
	<-released
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}