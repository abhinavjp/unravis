package releasehold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// lockWait is how long a writer waits for another writer; lockStale is the age after which a lock file is taken as left
// behind by a process that died while holding it. A change of the store takes milliseconds, so both are generous.
const (
	lockWait  = 10 * time.Second
	lockStale = 30 * time.Second
)

// lock takes path with O_EXCL and returns the function that gives it back.
func lock(path string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	delay := 2 * time.Millisecond
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: the lock file beside the store
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
			_ = f.Close()
			return func() { _ = retryFS(func() error { return os.Remove(path) }) }, nil
		}
		if !errors.Is(err, fs.ErrExist) && !transientFS(err) {
			return nil, fmt.Errorf("lock the hold store: %w", err)
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(path) // left by a process that died; the next turn of the loop takes it
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the hold store is locked by another command (%s); try again, or delete the lock file if no command is running", path)
		}
		time.Sleep(delay)
		if delay < 50*time.Millisecond {
			delay *= 2
		}
	}
}

// retryFS retries a file operation that failed because another process has the file open, which is how Windows reports
// a rename or open against a file that someone is reading. A missing file is final.
func retryFS(op func() error) error {
	delay := time.Millisecond
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := op()
		if err == nil || errors.Is(err, fs.ErrNotExist) || !transientFS(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 40*time.Millisecond {
			delay *= 2
		}
	}
}

func transientFS(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "used by another process") || strings.Contains(m, "sharing violation")
}
