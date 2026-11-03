//go:build windows

package instance

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// localPrefix is the per-session namespace every object name must live in.
const localPrefix = `Local\`

// utf16Name checks that name lives in the Local\ namespace and converts it for
// the Windows API. A Global\ or other name is refused so a name can never reach
// outside the current sign-in session.
func utf16Name(name string) (*uint16, error) {
	if !strings.HasPrefix(name, localPrefix) {
		return nil, fmt.Errorf("instance: object name %q must start with %s", name, localPrefix)
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("instance: object name: %w", err)
	}
	return p, nil
}

// Acquire takes the named mutex in names. first is true for the first copy,
// which must call release when it exits. A second copy gets first=false and a
// nil error. A mutex that exists but is owned by someone else yields
// ErrNameHeldByOther (squatting is detected, not prevented); a mutex this
// process may not open yields ErrAccessDenied. In every case but first=true the
// returned release is a no-op.
func Acquire(names Names) (release func(), first bool, err error) {
	noop := func() {}
	name, err := utf16Name(names.Mutex)
	if err != nil {
		return noop, false, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	switch {
	case err == nil:
		var once sync.Once
		return func() { once.Do(func() { _ = windows.CloseHandle(h) }) }, true, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		// h is a handle to somebody's mutex, never one this process created.
		ok, ownErr := ownerCheck(h)
		_ = windows.CloseHandle(h)
		if ownErr != nil || !ok {
			return noop, false, ErrNameHeldByOther
		}
		return noop, false, nil
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return noop, false, ErrAccessDenied
	}
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
	return noop, false, fmt.Errorf("instance: create mutex: %w", err)
}
