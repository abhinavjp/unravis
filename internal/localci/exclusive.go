package localci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// exclusiveLocks has one one-slot channel per name in local-ci.yml "exclusive". A job instance that names a resource
// holds it for its whole run, so two of them (the legs of a matrix, or jobs of two runs the agent has going) never use
// the same per-PC object at the same time. The holder already owns a run slot, so waiting cannot deadlock the slots.
// The channel covers one process; every queue item runs in a process of its own, so a lock file (see lockFile) covers
// the processes of one PC.
var exclusiveLocks sync.Map // name -> chan struct{}

const (
	exclusivePoll    = 2 * time.Second // how often a waiter looks at the lock file again
	exclusiveMaxHold = 6 * time.Hour   // a lock file older than this is taken as left behind, even if its pid is alive
)

// acquireExclusive waits for the resource, calling onWait once when it is busy. When lockDir is set the resource is also
// held across processes through <lockDir>/<name>.lock. release must be called exactly once.
func acquireExclusive(ctx context.Context, name, lockDir string, onWait func()) (release func(), err error) {
	v, _ := exclusiveLocks.LoadOrStore(name, make(chan struct{}, 1))
	ch := v.(chan struct{})
	waited := false
	wait := func() {
		if !waited && onWait != nil {
			onWait()
		}
		waited = true
	}
	select {
	case ch <- struct{}{}:
	default:
		wait()
		select {
		case ch <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if lockDir == "" {
		return func() { <-ch }, nil
	}
	unlock, err := lockFile(ctx, filepath.Join(lockDir, safeLockName(name)+".lock"), wait)
	if err != nil {
		<-ch
		return nil, err
	}
	return func() { unlock(); <-ch }, nil
}

func safeLockName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			b[i] = '_'
		}
	}
	return string(b)
}

// lockFile takes path with O_EXCL. The file holds "<pid> <unix seconds>". A file whose pid is gone, or older than
// exclusiveMaxHold, was left by a process that died holding it: it is removed and the waiter tries again.
func lockFile(ctx context.Context, path string, onWait func()) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	mine := fmt.Sprintf("%d %d", os.Getpid(), time.Now().Unix())
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640) //nolint:gosec // path is under the store
		if err == nil {
			_, werr := f.WriteString(mine)
			_ = f.Close()
			if werr != nil {
				_ = os.Remove(path)
				return nil, werr
			}
			return func() {
				// Only remove our own file: if it was judged stale and replaced, the new holder's stays.
				if b, err := readLock(path); err == nil && string(b) == mine {
					_ = os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if b, rerr := readLock(path); rerr == nil && lockIsStale(string(b), time.Now()) {
			if again, e := readLock(path); e == nil && string(again) == string(b) {
				_ = os.Remove(path)
				continue
			}
		}
		if onWait != nil {
			onWait()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(exclusivePoll):
		}
	}
}

// lockIsStale reports whether the lock file content names a process that is gone or a hold older than exclusiveMaxHold.
// Content that cannot be read (a writer that has not finished) is not stale yet.
func lockIsStale(content string, now time.Time) bool {
	f := strings.Fields(content)
	if len(f) != 2 {
		return false
	}
	pid, err1 := strconv.Atoi(f[0])
	at, err2 := strconv.ParseInt(f[1], 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	if now.Sub(time.Unix(at, 0)) > exclusiveMaxHold {
		return true
	}
	return !processAlive(pid)
}

// readLock reads a lock file (a path under the store).
func readLock(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // path is under the store
}

// HoldExclusive takes the named resource for a caller outside a run (the pre-push gate of a developer who shares the PC with the
// agent): it waits like a job does, with the same cross-process lock under lockDir. release must be called exactly once.
func HoldExclusive(ctx context.Context, name, lockDir string, onWait func()) (release func(), err error) {
	return acquireExclusive(ctx, name, lockDir, onWait)
}
