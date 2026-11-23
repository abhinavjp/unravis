package sealedfile

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAtomicReplacesAndLeavesNoTempFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.dat")
	for _, content := range []string{"first", "second, longer than the first"} {
		if err := WriteAtomic(path, ".state-*.tmp", []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, []byte(content)) {
			t.Fatalf("content = %q, %v", got, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

// LockWait waits for the holder, honours its context, and takes the lock once the
// holder lets go.
func TestLockWaitHonoursContextAndTakesTheLockAfterTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case")
	held, stop, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { // the holder takes and releases on its own goroutine (an OS thread lock on Windows)
		unlock, err := Lock(path)
		if err != nil {
			t.Error(err)
			close(held)
			close(released)
			return
		}
		close(held)
		<-stop
		unlock()
		close(released)
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if _, err := LockWait(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("LockWait with a short context = %v", err)
	}
	cancel()
	got := make(chan error, 1)
	go func() {
		release, err := LockWait(context.Background(), path)
		if err == nil {
			release()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("LockWait did not wait for the holder (%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(stop)
	<-released
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("LockWait never took the lock")
	}
}
