//go:build windows

package ownerlease

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// Acquire takes the lease on Path(runDir, demo) and returns a release func (idempotent). The file is
// opened with share mode 0, so a second open anywhere on the machine fails while the handle lives. Windows
// closes the handle when the process dies, so a crash needs no cleanup and the file is never deleted.
// The file stays empty: no pid, no secrets.
func Acquire(runDir string, demo bool) (release func(), err error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return func() {}, fmt.Errorf("ownerlease: create run dir: %w", err)
	}
	p, err := windows.UTF16PtrFromString(Path(runDir, demo))
	if err != nil {
		return func() {}, fmt.Errorf("ownerlease: path: %w", err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return func() {}, ErrBusy
		}
		return func() {}, fmt.Errorf("ownerlease: open lock file: %w", err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = windows.CloseHandle(h) }) }, nil
}
