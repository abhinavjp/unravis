package sqlite

// TSK-21 Go fixes: a half re-wrapped case heals on open (or says what to do),
// and Sweep frees stale restore staging files.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// cutRewrap leaves the case half re-wrapped: manifest sealed with the master
// key, case.key still with the (stand-in) Windows protector.
func cutRewrap(t *testing.T, s *Store, id casestore.CaseID) {
	t.Helper()
	boom := errors.New("simulated crash")
	s.opt.rewrapStep = func(stage string) error {
		if stage == "manifest-replaced" {
			return boom
		}
		return nil
	}
	if err := s.Rewrap(context.Background(), id); !errors.Is(err, boom) {
		t.Fatalf("Rewrap = %v", err)
	}
	s.opt.rewrapStep = nil
}

func TestHalfRewrappedCaseHealsOnOpen(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, p, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	cutRewrap(t, s, info.ID)
	dir := filepath.Join(root, string(info.ID))
	if !halfRewrapped(dir) {
		t.Fatal("the cut did not leave the manifest-master, key-DPAPI shape")
	}

	// A fresh Store with the same Windows protector and the unlocked keyring.
	s2 := newStoreAt(t, root, p, func(o *Options) { o.Protectors = keyringLookup(kr) })
	if !s2.NeedsRewrap(info.ID) {
		t.Fatal("NeedsRewrap = false before the heal")
	}
	c := mustOpen(t, s2, info.ID)
	if imp, ev, _ := counts(t, c); imp != 1 || ev != 5 {
		t.Fatalf("after the heal: %d imports, %d events", imp, ev)
	}
	_ = c.Close()
	if s2.NeedsRewrap(info.ID) {
		t.Fatal("NeedsRewrap = true after the heal")
	}
	if k, err := s2.CaseKind(info.ID); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("CaseKind after the heal = %q, %v", k, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// With the master key locked nothing can be finished, so Open answers the typed
// ErrNeedsRewrap (still an ErrKeyFile for old callers), writes nothing, and the
// case heals as soon as the key is unlocked.
func TestHalfRewrappedCaseNeedsRewrapWhenItCannotHeal(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, p, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	cutRewrap(t, s, info.ID)
	dir := filepath.Join(root, string(info.ID))
	keyBefore := readFile(t, filepath.Join(dir, keyName))

	locked := newStoreAt(t, root, p, func(o *Options) {
		o.Protectors = func(string) (sealedfile.Protector, error) { return nil, casestore.ErrLocked }
	})
	_, err := locked.Open(context.Background(), info.ID)
	if !errors.Is(err, casestore.ErrNeedsRewrap) || !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("Open while locked = %v, want ErrNeedsRewrap (an ErrKeyFile)", err)
	}
	if !locked.NeedsRewrap(info.ID) {
		t.Fatal("NeedsRewrap = false while locked")
	}
	if string(keyBefore) != string(readFile(t, filepath.Join(dir, keyName))) {
		t.Fatal("a refused open rewrote case.key")
	}

	// A store whose Windows protector cannot read case.key has a different
	// problem: that is an unreadable key file, not something a re-wrap can mend.
	other := masterStore(t, root, kr)
	if _, err := other.Open(context.Background(), info.ID); !errors.Is(err, casestore.ErrKeyFile) || errors.Is(err, casestore.ErrNeedsRewrap) {
		t.Fatalf("Open with the wrong Windows protector = %v, want a plain ErrKeyFile", err)
	}

	// Unlocked again, with the right protector: Open finishes it.
	again := newStoreAt(t, root, p, func(o *Options) { o.Protectors = keyringLookup(kr) })
	_ = mustOpen(t, again, info.ID).Close()
	if again.NeedsRewrap(info.ID) {
		t.Fatal("still needs a re-wrap after unlocking")
	}
}

func TestNeedsRewrapIsFalseForWholeAndMissingCases(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, _, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	if s.NeedsRewrap(info.ID) {
		t.Fatal("a plain DPAPI case does not need a re-wrap")
	}
	if err := s.Rewrap(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	if s.NeedsRewrap(info.ID) {
		t.Fatal("a re-wrapped case does not need a re-wrap")
	}
	if s.NeedsRewrap(casestore.CaseID("0123456789abcdefghjkmnpqrs")) || s.NeedsRewrap("../x") {
		t.Fatal("a missing or bad id does not need a re-wrap")
	}
}

func TestIsRestoreStagingMatchesOnlyTheExactShape(t *testing.T) {
	for name, want := range map[string]bool{
		"restore-0123456789abcdef.part":  true,
		"restore-ffffffffffffffff.part":  true,
		"restore-0123456789ABCDEF.part":  false,
		"restore-0123456789abcde.part":   false,
		"restore-0123456789abcdef0.part": false,
		"restore-0123456789abcdeg.part":  false,
		"restore-0123456789abcdef.parts": false,
		"restore-0123456789abcdef":       false,
		"xrestore-0123456789abcdef.part": false,
		"restore-.part":                  false,
		"0123456789abcdefghjkmnpqrs":     false,
		"":                               false,
	} {
		if got := isRestoreStaging(name); got != want {
			t.Errorf("isRestoreStaging(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSweepRemovesOnlyStaleRestoreStaging(t *testing.T) {
	s, root, _, info := dpapiCase(t)
	write := func(name string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, make([]byte, 4<<10), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write("restore-0123456789abcdef.part", 48*time.Hour)
	fresh := write("restore-fedcba9876543210.part", 5*time.Minute) // a restore in progress
	other := write("restore-notahexvalue0000.part", 48*time.Hour)  // not ours
	note := write("notes.part", 48*time.Hour)
	// A directory with a staging name is never removed.
	dirNamed := filepath.Join(root, "restore-aaaaaaaaaaaaaaaa.part")
	if err := os.Mkdir(dirNamed, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(dirNamed, old, old)

	if _, err := s.Sweep(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	if exists(stale) {
		t.Error("a two-day-old staging file survived Sweep")
	}
	for name, p := range map[string]string{"fresh staging": fresh, "wrongly named": other, "unrelated": note, "directory": dirNamed} {
		if !exists(p) {
			t.Errorf("Sweep removed the %s file", name)
		}
	}
	if !exists(filepath.Join(root, string(info.ID))) {
		t.Error("Sweep removed a case")
	}
}

// A staging file that is open (a restore still writing it) cannot be removed
// on Windows, so Sweep leaves it and carries on.
func TestSweepKeepsAStagingFileThatIsInUse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("open files can be unlinked on this OS; the age check is what protects them there")
	}
	s, root, _, _ := dpapiCase(t)
	path := filepath.Join(root, "restore-0123456789abcdef.part")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(context.Background(), time.Now()); err != nil {
		t.Fatalf("Sweep = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("an open staging file was removed: %v", err)
	}
}
