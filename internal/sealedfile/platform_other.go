//go:build !windows

package sealedfile

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CurrentUser has no sealing backend off Windows. Failing closed keeps callers
// from writing sensitive state in plaintext on a platform that cannot protect it.
func CurrentUser() (Protector, error) { return nil, ErrUnavailable }

// LocalMachine has no sealing backend off Windows either.
func LocalMachine() (Protector, error) { return nil, ErrUnavailable }

var localLocks sync.Map

// Lock serialises callers inside this process.
func Lock(path string) (func(), error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	value, _ := localLocks.LoadOrStore(canonical, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock, nil
}

// LockWait is Lock that honours ctx: it polls the lock and gives up with the
// context's error once ctx ends, so a cancelled caller is never stuck behind a
// long holder.
func LockWait(ctx context.Context, path string) (func(), error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	value, _ := localLocks.LoadOrStore(canonical, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	delay := time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if mutex.TryLock() {
			return mutex.Unlock, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 25*time.Millisecond)
	}
}

// Replace moves from over to, replacing it.
func Replace(from, to string) error { return os.Rename(from, to) }

// MoveNoReplace renames from to to and refuses, with an error that satisfies errors.Is(err, os.ErrExist), when to already exists: the file is
// first linked to its new name (a link never replaces), then the old name is removed.
func MoveNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
