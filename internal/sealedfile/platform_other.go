//go:build !windows

package sealedfile

import (
	"os"
	"path/filepath"
	"sync"
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

// Replace moves from over to, replacing it.
func Replace(from, to string) error { return os.Rename(from, to) }
