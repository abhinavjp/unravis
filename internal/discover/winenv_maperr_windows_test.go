//go:build windows

package discover

import (
	"errors"
	"io/fs"
	"testing"

	"golang.org/x/sys/windows"
)

// A service that is not installed must read as "not found", not as an opaque
// system error (found by the real-OS smoke run: it became "unreadable").
func TestMapErrServiceDoesNotExistIsNotFound(t *testing.T) {
	err := mapErr("services", windows.ERROR_SERVICE_DOES_NOT_EXIST)
	if !IsNotFound(err) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("not found expected: %v", err)
	}
	if ReasonFromError(err) != ReasonNotFound {
		t.Fatalf("reason %s", ReasonFromError(err))
	}
	if got := ReasonFromError(mapErr("services", windows.ERROR_ACCESS_DENIED)); got != ReasonNeedsElevation {
		t.Fatalf("access denied reason %s", got)
	}
	if got := ReasonFromError(mapErr("services", windows.ERROR_INVALID_FUNCTION)); got != ReasonUnreadable {
		t.Fatalf("other error reason %s", got)
	}
}
