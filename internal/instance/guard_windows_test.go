//go:build windows

package instance

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// mustAcquire acquires names and fails the test unless it is the first copy.
func mustAcquire(t *testing.T, n Names) func() {
	t.Helper()
	release, first, err := Acquire(n)
	if err != nil || !first {
		t.Fatalf("Acquire: first=%v err=%v, want first and no error", first, err)
	}
	return release
}

func TestAcquireFirstThenSecond(t *testing.T) {
	n := uniqueNames(t)
	release := mustAcquire(t, n)
	defer release()
	rel2, first, err := Acquire(n)
	if err != nil {
		t.Fatalf("second Acquire: unexpected error %v", err)
	}
	if first {
		t.Fatal("second Acquire reported first")
	}
	rel2() // a no-op, must not close the first copy's mutex
	if _, first, err := Acquire(n); first || err != nil {
		t.Fatalf("third Acquire after a no-op release: first=%v err=%v", first, err)
	}
}

func TestAcquireAfterReleaseIsFirstAgain(t *testing.T) {
	n := uniqueNames(t)
	release := mustAcquire(t, n)
	release()
	release2 := mustAcquire(t, n)
	release2()
}

func TestReleaseTwiceIsSafe(t *testing.T) {
	n := uniqueNames(t)
	release := mustAcquire(t, n)
	release()
	// A second call must not close a handle value that was since reused: take
	// the mutex again, call the old release, and the new holder must still win.
	again := mustAcquire(t, n)
	defer again()
	release()
	if _, first, err := Acquire(n); first || err != nil {
		t.Fatalf("after a double release the new holder lost the mutex: first=%v err=%v", first, err)
	}
}

func TestAcquireRejectsNamesOutsideLocal(t *testing.T) {
	for _, name := range []string{`Global\x`, `x`, ``, `local\x`} {
		release, first, err := Acquire(Names{Mutex: name})
		if err == nil || first {
			t.Errorf("Acquire(%q): first=%v err=%v, want an error", name, first, err)
		}
		release() // must be callable
	}
}

func TestAcquireDoesNotShareAcrossUsers(t *testing.T) {
	app := appOf(t, uniqueNames(t))
	a, err := NewNames(app, "S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewNames(app, "S-1-5-21-1-2-3-1002")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mutex == b.Mutex {
		t.Fatal("different SIDs gave the same mutex name")
	}
	defer mustAcquire(t, a)()
	defer mustAcquire(t, b)()
}

// appOf recovers the app part of a Names built by NewNames.
func appOf(t *testing.T, n Names) string {
	t.Helper()
	s := n.Mutex[len(`Local\`):]
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i]
		}
	}
	t.Fatalf("no app part in %q", n.Mutex)
	return ""
}

func TestAcquireDetectsAMutexOwnedBySomeoneElse(t *testing.T) {
	n := uniqueNames(t)
	name, err := windows.UTF16PtrFromString(n.Mutex)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		t.Fatalf("CreateMutex: %v", err)
	}
	defer windows.CloseHandle(h)

	// With the real owner check the mutex is ours: a plain second copy.
	rel, first, err := Acquire(n)
	if first || err != nil {
		t.Fatalf("real ownerCheck: first=%v err=%v, want not first and nil", first, err)
	}
	rel()

	// With a fake that says "someone else owns it", squatting is reported.
	old := ownerCheck
	t.Cleanup(func() { ownerCheck = old })
	ownerCheck = func(windows.Handle) (bool, error) { return false, nil }
	rel, first, err = Acquire(n)
	if first || !errors.Is(err, ErrNameHeldByOther) {
		t.Fatalf("fake ownerCheck: first=%v err=%v, want not first and ErrNameHeldByOther", first, err)
	}
	rel()

	// A failing owner check is treated the same way.
	ownerCheck = func(windows.Handle) (bool, error) { return true, errors.New("boom") }
	_, first, err = Acquire(n)
	if first || !errors.Is(err, ErrNameHeldByOther) {
		t.Fatalf("failing ownerCheck: first=%v err=%v, want not first and ErrNameHeldByOther", first, err)
	}
}
