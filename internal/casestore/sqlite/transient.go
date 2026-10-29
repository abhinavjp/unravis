package sqlite

import (
	"errors"
	"runtime"
	"syscall"
	"time"
)

// isTransientWindowsFileErr recognises the handful of Windows file errors that
// mean "something else has this file or folder open, or is mid-rename, right
// now", not a real permission problem: ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION
// and ERROR_LOCK_VIOLATION. A cold Open reads the manifest's header (the
// half-rewrap check, outside the folder lock, by design: taking the lock on
// every Open just for that rare case would add contention) at the same moment
// CloseCase or ReopenCase replaces manifest.dat under the lock; on Windows that
// race can make the replace answer one of these three instead of succeeding.
// They clear within milliseconds once the other side lets go of the file, so a
// short bounded retry is correct. The numbers mean something else on every other
// OS, so this never fires there.
func isTransientWindowsFileErr(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	var en syscall.Errno
	if !errors.As(err, &en) {
		return false
	}
	switch en {
	case 5, 32, 33: // ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
		return true
	}
	return false
}

// transientRetryBudget bounds retryTransient: long enough to outlast the race
// above, short enough that a real, lasting access problem still surfaces promptly.
const transientRetryBudget = 2 * time.Second

// retryTransient runs fn, retrying with a short sleep between tries while it
// keeps failing with isTransientWindowsFileErr, for up to transientRetryBudget.
// It returns the first success, or fn's last error once the budget is spent (so
// a non-transient failure is reported at once, and a lasting one eventually is).
func retryTransient(fn func() error) error {
	deadline := time.Now().Add(transientRetryBudget)
	for {
		err := fn()
		if err == nil || !isTransientWindowsFileErr(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
