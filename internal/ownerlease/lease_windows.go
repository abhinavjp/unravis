//go:build windows

package ownerlease

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// leaseSDDL is the descriptor of a new lock file: a protected DACL for the owner and SYSTEM only, and a Medium mandatory label that
// forbids reading and writing from below. A Low-integrity process of the same user (a sandboxed program, for example) can then not open
// the file to hold it, which would lock every later start out while no owner runs.
const leaseSDDL = "D:P(A;;FA;;;OW)(A;;FA;;;SY)S:(ML;;NWNR;;;ME)"

// A start that meets the lock for a moment (an antivirus scan or the indexer opening a new file) waits this long before it is told that
// another owner holds it.
const (
	busyTries = 5
	busyWait  = 100 * time.Millisecond
)

// Acquire takes the lease on Path(runDir, demo) and returns a release func (idempotent). The file is
// opened with share mode 0, so a second open anywhere on the machine fails while the handle lives. Windows
// closes the handle when the process dies, so a crash needs no cleanup. The file stays empty: no pid, no secrets.
func Acquire(runDir string, demo bool) (release func(), err error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return func() {}, fmt.Errorf("ownerlease: create run dir: %w", err)
	}
	path := Path(runDir, demo)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return func() {}, fmt.Errorf("ownerlease: path: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(leaseSDDL)
	if err != nil {
		return func() {}, fmt.Errorf("ownerlease: descriptor: %w", err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd} // InheritHandle false: the handle is not passed to a child
	// A file left by an older run has the folder's inherited rights and the default label, so it is removed first when nobody holds it
	// (a held file cannot be removed, and the open below then reports busy) and made again with the descriptor above.
	_ = os.Remove(path)
	for try := 0; ; try++ {
		// Read access is enough: share mode 0 is what makes the lock exclusive, and a file marked read-only does not stop it.
		h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, sa, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { _ = windows.CloseHandle(h) }) }, nil
		}
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			if try+1 < busyTries {
				time.Sleep(busyWait)
				continue
			}
			return func() {}, ErrBusy
		}
		return func() {}, fmt.Errorf("ownerlease: open lock file: %w", err)
	}
}
