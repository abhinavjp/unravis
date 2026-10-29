package sqlite

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// isTransientWindowsFileErr only ever answers true on Windows: the three codes
// it watches for (5, 32, 33) mean unrelated things on other platforms (EIO,
// ENFILE, EMFILE on Linux, for instance), so treating them as "try again" there
// would be wrong. This runs on every OS and checks both sides of that gate.
func TestIsTransientWindowsFileErrOnlyFiresOnWindows(t *testing.T) {
	for _, en := range []syscall.Errno{5, 32, 33} {
		got := isTransientWindowsFileErr(en)
		want := runtime.GOOS == "windows"
		if got != want {
			t.Errorf("isTransientWindowsFileErr(%d) = %v, want %v", en, got, want)
		}
	}
	if isTransientWindowsFileErr(nil) {
		t.Error("a nil error must never be transient")
	}
	if isTransientWindowsFileErr(errors.New("boom")) {
		t.Error("an error that is not a syscall.Errno must never be transient")
	}
	if isTransientWindowsFileErr(syscall.Errno(2)) { // ERROR_FILE_NOT_FOUND / ENOENT
		t.Error("a real not-found must not be retried away")
	}
}

func TestRetryTransientStopsOnSuccessOrOnANonTransientError(t *testing.T) {
	calls := 0
	err := retryTransient(func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("a call that succeeds first time: err=%v calls=%d", err, calls)
	}

	calls = 0
	sentinel := errors.New("not a Windows errno")
	err = retryTransient(func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("a non-transient error must not be retried: err=%v calls=%d", err, calls)
	}
}

// On a non-Windows GOOS isTransientWindowsFileErr never fires, so a transient
// Windows errno still makes retryTransient return at once rather than spend the
// whole budget; this is what keeps the test deterministic off Windows.
func TestRetryTransientHonoursItsBudgetForARealGenuineFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exercised for real by the sqlite p2 tests on Windows CI")
	}
	calls := 0
	began := time.Now()
	err := retryTransient(func() error {
		calls++
		return syscall.Errno(5)
	})
	if err == nil || calls != 1 {
		t.Fatalf("a Windows errno off Windows is not transient and must not be retried: err=%v calls=%d", err, calls)
	}
	if took := time.Since(began); took > 100*time.Millisecond {
		t.Fatalf("a single non-retried call took %s", took)
	}
}
