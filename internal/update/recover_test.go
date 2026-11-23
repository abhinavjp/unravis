package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetupCanRecoverFindsTheHelpLineAnywhereInALargeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 3<<20)
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"at the start":  {RecoverMarker + big, true},
		"in the middle": {big + RecoverMarker + big, true},
		// the text straddles the 1 MiB read size
		"across a read": {big[:1<<20-10] + RecoverMarker + big, true},
		"at the end":    {big + RecoverMarker, true},
		"absent":        {big + "a setup run that was cut off", false},
		"empty":         {"", false},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := SetupCanRecover(p); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
	if SetupCanRecover(filepath.Join(dir, "missing")) || SetupCanRecover(dir) {
		t.Fatal("a missing file or a folder cannot recover")
	}
}

// Two journals can be stuck at once (the machine's and the person's); each keeps its own count, so neither resets the other's.
func TestEachJournalKeepsItsOwnTries(t *testing.T) {
	tr := &Tracker{StateDir: t.TempDir()}
	a := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	b := a.Add(time.Hour)
	for i := 0; i < MaxJournalTries; i++ {
		tr.MarkJournalTried(a)
		tr.MarkJournalTried(b)
	}
	if !tr.JournalTried(a) || !tr.JournalTried(b) {
		t.Fatalf("a %d b %d", tr.JournalTries(a), tr.JournalTries(b))
	}
	if tr.JournalTried(a.Add(time.Minute)) {
		t.Fatal("another run starts at zero")
	}
}

// FileHasAll reads in 1 MiB pieces and finds each text wherever it is, also when it runs over the end of a piece, and only when
// every text is there (SWP-1021-3: the rollback checks no longer read a whole setup program into memory).
func TestFileHasAllNeedsEveryTextAndFindsThemAcrossPieces(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 3<<20)
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b, c := "expect-sha256", "expect-version", "allow-downgrade"
	all := write("all", big[:1<<20-5]+a+big[:2<<20-7]+b+big[:100]+c)
	if !FileHasAll(all, a, b, c) {
		t.Error("three texts, one over a piece end, were not found")
	}
	if FileHasAll(write("two", big+a+big+b), a, b, c) {
		t.Error("a file that lacks one text passed")
	}
	if !FileHasAll(write("same", a+a), a, a) || !FileHasAll(write("one", big+a), a) {
		t.Error("one text, or the same text twice, was not found")
	}
	if FileHasAll(write("empty", ""), a) || FileHasAll(filepath.Join(dir, "missing"), a) || FileHasAll(dir, a) {
		t.Error("an empty, missing or folder path passed")
	}
	if !FileHasAll(write("none", "x")) || FileHasAll(filepath.Join(dir, "missing")) {
		t.Error("no texts: a readable file passes, a missing one does not")
	}
	// a short text that is split by the piece end by one byte
	long := big[:1<<20-1] + a
	if !FileHasAll(write("edge", long), a) {
		t.Error("a text that starts one byte before the piece end was not found")
	}
}
