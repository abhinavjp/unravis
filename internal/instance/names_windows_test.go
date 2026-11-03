//go:build windows

package instance

import (
	"testing"

	"golang.org/x/sys/windows"
)

func mustSID(t *testing.T, s string) *windows.SID {
	t.Helper()
	sid, err := windows.StringToSid(s)
	if err != nil {
		t.Fatalf("StringToSid(%q): %v", s, err)
	}
	return sid
}

func TestOwnerMatchesAcceptsOnlyUserOrDefaultOwner(t *testing.T) {
	user := mustSID(t, "S-1-5-21-1-2-3-1001")
	def := mustSID(t, "S-1-5-32-544")
	for name, c := range map[string]struct {
		owner *windows.SID
		want  bool
	}{
		"user":          {mustSID(t, "S-1-5-21-1-2-3-1001"), true},
		"default owner": {mustSID(t, "S-1-5-32-544"), true},
		"unrelated":     {mustSID(t, "S-1-5-21-9-9-9-500"), false},
		"nil owner":     {nil, false},
	} {
		if got := ownerMatches(c.owner, user, def); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

func TestFreshMutexIsOwnedByCurrentUser(t *testing.T) {
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
	ok, err := OwnedByCurrentUser(h)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a mutex this process just created should be owned by the current user")
	}
}

func TestFreshEventIsOwnedByCurrentUser(t *testing.T) {
	n := uniqueNames(t)
	name, err := windows.UTF16PtrFromString(n.Open)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	defer windows.CloseHandle(h)
	ok, err := OwnedByCurrentUser(h)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("an event this process just created should be owned by the current user")
	}
}
