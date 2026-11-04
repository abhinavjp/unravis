package sqlite

// The once-per-process quick_check (verify_cache.go): a second open of an
// untouched case skips it, anything that changes the file or the key does not.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func noRacyWindow(t *testing.T) {
	t.Helper()
	old := verifyRacyWindow
	verifyRacyWindow = 0
	t.Cleanup(func() { verifyRacyWindow = old })
}

// cachedCase makes a case with enough pages to flip a byte in, opened and closed once.
func cachedCase(t *testing.T) (*Store, casestore.CaseID, string) {
	t.Helper()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 3000)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return s, info.ID, filepath.Join(s.root, string(info.ID), dbName)
}

func openClose(t *testing.T, s *Store, id casestore.CaseID) error {
	t.Helper()
	c, err := s.Open(context.Background(), id)
	if err != nil {
		return err
	}
	return c.Close()
}

func TestReopenSkipsQuickCheck(t *testing.T) {
	noRacyWindow(t)
	s, id, _ := cachedCase(t)
	before := quickCheckRuns.Load()
	for i := 0; i < 3; i++ {
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	// The first open after the import's close may check again (the file changed
	// under no handle of ours); once it is at rest the later opens must not.
	if got := quickCheckRuns.Load() - before; got > 1 {
		t.Fatalf("3 opens of an untouched case ran quick_check %d times, want at most 1", got)
	}
	before = quickCheckRuns.Load()
	if err := openClose(t, s, id); err != nil {
		t.Fatal(err)
	}
	if got := quickCheckRuns.Load() - before; got != 0 {
		t.Fatalf("reopen ran quick_check %d times, want 0", got)
	}
	// An open handle makes a second open (another Store) skip as well.
	c := mustOpen(t, s, id)
	s2 := newStoreAt(t, s.root, s.opt.Protector)
	before = quickCheckRuns.Load()
	c2, err := s2.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2.Close()
	_ = c.Close()
	if got := quickCheckRuns.Load() - before; got != 0 {
		t.Fatalf("open beside a live handle ran quick_check %d times, want 0", got)
	}
}

// flipAndShift damages the file at rest and changes its modification time, as
// any real rewrite does.
func flipAndShift(t *testing.T, path string, off int, shift time.Duration) (good []byte, mtime time.Time) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	good = readFile(t, path)
	bad := append([]byte(nil), good...)
	bad[off] ^= 0xff
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), fi.ModTime().Add(shift)); err != nil {
		t.Fatal(err)
	}
	return good, fi.ModTime()
}

func TestChangedFileIsCheckedAgain(t *testing.T) {
	noRacyWindow(t)
	s, id, path := cachedCase(t)
	for i := 0; i < 2; i++ { // the second open records an at-rest stamp that is skipped
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	before := quickCheckRuns.Load()
	flipAndShift(t, path, 4096*3+100, time.Hour)
	if err := openClose(t, s, id); !errors.Is(err, ErrDamaged) {
		t.Fatalf("changed file: Open = %v, want ErrDamaged", err)
	}
	if got := quickCheckRuns.Load() - before; got != 1 {
		t.Fatalf("quick_check ran %d times for the changed file, want 1", got)
	}
}

func TestFailedCheckIsNotCached(t *testing.T) {
	noRacyWindow(t)
	s, id, path := cachedCase(t)
	for i := 0; i < 2; i++ {
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	good, mtime := flipAndShift(t, path, 4096*4+7, time.Hour)
	for i := 0; i < 3; i++ { // every open of the damaged file is checked and refused
		before := quickCheckRuns.Load()
		if err := openClose(t, s, id); !errors.Is(err, ErrDamaged) {
			t.Fatalf("attempt %d: Open = %v, want ErrDamaged", i, err)
		}
		if got := quickCheckRuns.Load() - before; got != 1 {
			t.Fatalf("attempt %d: quick_check ran %d times, want 1", i, got)
		}
	}
	// Put the good bytes and the old time back: not trusted for the old stamp,
	// because the failure cleared it, so it is checked and passes.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), mtime); err != nil {
		t.Fatal(err)
	}
	before := quickCheckRuns.Load()
	if err := openClose(t, s, id); err != nil {
		t.Fatalf("restored file: %v", err)
	}
	if got := quickCheckRuns.Load() - before; got != 1 {
		t.Fatalf("restored file: quick_check ran %d times, want 1", got)
	}
}

// A rewrite that keeps the size and (on a coarse clock) the modification time is
// still caught, because a stamp taken moments after the last write is not trusted.
func TestRacyStampIsNotTrusted(t *testing.T) {
	s, id, path := cachedCase(t) // default window: the stamp is fresh
	if err := openClose(t, s, id); err != nil {
		t.Fatal(err)
	}
	flipAndShift(t, path, 4096*3+100, 0) // same size, same modification time
	if err := openClose(t, s, id); !errors.Is(err, ErrDamaged) {
		t.Fatalf("same-size same-time rewrite: Open = %v, want ErrDamaged", err)
	}
}

func TestWrongKeyAfterCachedCheck(t *testing.T) {
	noRacyWindow(t)
	s, id, _ := cachedCase(t)
	for i := 0; i < 2; i++ {
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	other, err := casestore.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	other.CaseID = string(id)
	if err := casestore.WriteKeyFile(filepath.Join(s.root, string(id), keyName), s.opt.Protector, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(context.Background(), id); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key after a cached check: Open = %v, want ErrWrongKey", err)
	}
}

func TestConcurrentFirstOpensCheckOnce(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case") // never opened: nothing cached
	const n = 8
	stores := make([]*Store, n)
	for i := range stores {
		stores[i] = newStoreAt(t, s.root, s.opt.Protector)
	}
	before := quickCheckRuns.Load()
	handles := make([]casestore.Case, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			handles[i], errs[i] = stores[i].Open(context.Background(), info.ID)
		}()
	}
	close(start)
	wg.Wait()
	for i := range handles {
		if errs[i] != nil {
			t.Fatalf("open %d: %v", i, errs[i])
		}
	}
	for _, h := range handles { // closed only after every open, so none sees a bare file
		_ = h.Close()
	}
	if got := quickCheckRuns.Load() - before; got != 1 {
		t.Fatalf("%d concurrent first opens ran quick_check %d times, want 1", n, got)
	}
}

func TestEraseDropsCachedCheck(t *testing.T) {
	noRacyWindow(t)
	s, id, _ := cachedCase(t)
	for i := 0; i < 2; i++ {
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	key := cacheKey(filepath.Join(s.root, string(id)))
	has := func() bool {
		verified.mu.Lock()
		defer verified.mu.Unlock()
		return verified.m[key] != nil
	}
	if !has() {
		t.Fatal("no cached check after opens")
	}
	if err := s.Erase(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("cached check survived Erase")
	}
	if _, err := s.Open(context.Background(), id); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("Open after Erase = %v, want ErrNotFound", err)
	}
}
