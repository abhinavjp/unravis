package instance

import (
	"crypto/rand"
	"encoding/hex"
	"os/user"
	"runtime"
	"strings"
	"testing"
)

const testSID = "S-1-5-21-1-2-3-1001"

// uniqueNames returns names no other test or process uses.
func uniqueNames(t *testing.T) Names {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	n, err := NewNames("T"+hex.EncodeToString(b), testSID)
	if err != nil {
		t.Fatalf("NewNames: %v", err)
	}
	return n
}

func TestNewNamesBuildsTheThreeNames(t *testing.T) {
	n, err := NewNames("UnravisApp", testSID)
	if err != nil {
		t.Fatal(err)
	}
	want := Names{
		Mutex: `Local\UnravisApp.` + testSID,
		Open:  `Local\UnravisApp.` + testSID + ".Open",
		Quit:  `Local\UnravisApp.` + testSID + ".Quit",
	}
	if n != want {
		t.Fatalf("got %+v, want %+v", n, want)
	}
}

func TestNewNamesRejectsBadApp(t *testing.T) {
	for name, app := range map[string]string{
		"empty":     "",
		"backslash": `a\b`,
		"dot":       "a.b",
		"space":     "a b",
		"33 chars":  strings.Repeat("a", 33),
		"non-ASCII": "café",
	} {
		if _, err := NewNames(app, testSID); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := NewNames(strings.Repeat("a", 32), testSID); err != nil {
		t.Errorf("32 characters should be accepted: %v", err)
	}
}

func TestNewNamesRejectsBadSID(t *testing.T) {
	const prefix = "S-1-5-21-"
	long := prefix + strings.Repeat("1", 185-len(prefix))
	if len(long) != 185 {
		t.Fatalf("test SID is %d characters", len(long))
	}
	for name, sid := range map[string]string{
		"empty":      "",
		"abc":        "abc",
		"truncated":  "S-1-",
		"too long":   long,
		"backslash":  `S-1-5-21-1\2`,
		"newline":    testSID + "\n",
		"letter":     "S-1-5-x",
		"no sub-aut": "S-1-5",
	} {
		if _, err := NewNames("App", sid); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := NewNames("App", prefix+strings.Repeat("1", 184-len(prefix))); err != nil {
		t.Errorf("184 characters should be accepted: %v", err)
	}
}

func TestKindStrings(t *testing.T) {
	for k, want := range map[Kind]string{Open: "open", Quit: "quit", 0: "unknown", 99: "unknown"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
	if Open != 1 {
		t.Errorf("Open = %d, want 1", int(Open))
	}
}

func TestNamesUseTheLocalNamespace(t *testing.T) {
	n := uniqueNames(t)
	for _, s := range []string{n.Mutex, n.Open, n.Quit} {
		if !strings.HasPrefix(s, `Local\`) {
			t.Errorf("%q is not in the Local namespace", s)
		}
	}
}

func TestNamesContainTheUserSID(t *testing.T) {
	n := uniqueNames(t)
	for _, s := range []string{n.Mutex, n.Open, n.Quit} {
		if !strings.Contains(s, testSID) {
			t.Errorf("%q does not contain the SID", s)
		}
	}
}

func TestDefaultAndDemoNamesDiffer(t *testing.T) {
	d, err := CurrentNames(DefaultApp)
	if err != nil {
		t.Fatal(err)
	}
	m, err := CurrentNames(DemoApp)
	if err != nil {
		t.Fatal(err)
	}
	if d.Mutex == m.Mutex || d.Open == m.Open || d.Quit == m.Quit {
		t.Fatalf("default and demo names collide: %+v %+v", d, m)
	}
}

func TestCurrentNamesUseTheTokenUserSID(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("token SID is Windows only")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	want, err := NewNames(DefaultApp, u.Uid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CurrentNames(DefaultApp)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
