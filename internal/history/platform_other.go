//go:build !windows

package history

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type unsupportedProtector struct{}

func currentUserProtector() (Protector, error)                { return nil, ErrUnavailable }
func (unsupportedProtector) Protect([]byte) ([]byte, error)   { return nil, ErrUnavailable }
func (unsupportedProtector) Unprotect([]byte) ([]byte, error) { return nil, ErrUnavailable }

var localLocks sync.Map

func acquireVaultLock(path string) (func(), error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	value, _ := localLocks.LoadOrStore(canonical, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock, nil
}

func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
