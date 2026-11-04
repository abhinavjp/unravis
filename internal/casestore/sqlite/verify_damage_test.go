package sqlite

// Fix loop 3: the file-identity stamp of the quick_check cache must pin the file
// that was seen (an open handle, not the path), and damage met while a case is
// open must drop the cache entry on every path.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// A case.db replaced by rename with a file of the same size and the old
// modification time is a different file: the next open must check it again. On
// Windows a stamp taken with os.Stat compares the path with itself and would pass.
func TestReplacedFileSameSizeAndTimeIsCheckedAgain(t *testing.T) {
	noRacyWindow(t)
	s, id, path := cachedCase(t)
	for i := 0; i < 2; i++ { // the second open records an at-rest stamp that is skipped
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	before := quickCheckRuns.Load()
	if err := openClose(t, s, id); err != nil {
		t.Fatal(err)
	}
	if got := quickCheckRuns.Load() - before; got != 0 {
		t.Fatalf("setup: an untouched reopen ran quick_check %d times, want 0", got)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	repl := path + ".replacement"
	if err := os.WriteFile(repl, readFile(t, path), 0o600); err != nil { // a valid copy: same size
		t.Fatal(err)
	}
	if err := os.Chtimes(repl, fi.ModTime(), fi.ModTime()); err != nil { // the old modification time
		t.Fatal(err)
	}
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}
	if now, err := os.Stat(path); err != nil || now.Size() != fi.Size() || !now.ModTime().Equal(fi.ModTime()) {
		t.Fatalf("setup: replacement differs in size or time: %v %v", now, err)
	}

	before = quickCheckRuns.Load()
	if err := openClose(t, s, id); err != nil {
		t.Fatalf("replaced file: %v", err)
	}
	if got := quickCheckRuns.Load() - before; got != 1 {
		t.Fatalf("a file replaced by rename (same size, same time) ran quick_check %d times, want 1", got)
	}
}

// IntegrityCheck reporting damage on a live case drops the cache entry, so the
// handle's shutdown does not stamp the damaged file as verified.
func TestIntegrityCheckDamageDropsTheCacheEntry(t *testing.T) {
	noRacyWindow(t)
	s, id, path := cachedCase(t)
	for i := 0; i < 2; i++ {
		if err := openClose(t, s, id); err != nil {
			t.Fatal(err)
		}
	}
	c := mustOpen(t, s, id)
	entry := verified.lookup(cacheKey(filepath.Dir(path)))
	if !entry.ok || entry.stale.Load() {
		t.Fatal("setup: the open case has no cache entry")
	}
	// Damage the file under the live handle, then make the connection read it afresh.
	bad := readFile(t, path)
	bad[4096*3+100] ^= 0xff
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Skipf("cannot write the file under a live handle on this system: %v", err)
	}
	_, _ = c.db.Exec("PRAGMA shrink_memory")
	if err := c.IntegrityCheck(context.Background()); !errors.Is(err, ErrDamaged) {
		t.Fatalf("IntegrityCheck = %v, want ErrDamaged", err)
	}
	if !entry.stale.Load() {
		t.Fatal("damage found while open did not drop the cache entry")
	}
	_ = c.Close()

	before := quickCheckRuns.Load()
	if err := openClose(t, s, id); !errors.Is(err, ErrDamaged) {
		t.Fatalf("reopen of the damaged file = %v, want ErrDamaged", err)
	}
	if got := quickCheckRuns.Load() - before; got != 1 {
		t.Fatalf("reopen ran quick_check %d times, want 1", got)
	}
}

// Every damage error a live case sees drops the entry; other errors do not.
func TestNoteDamageDropsOnlyForDamage(t *testing.T) {
	noRacyWindow(t)
	s, id, path := cachedCase(t)
	dir := filepath.Dir(path)
	c := mustOpen(t, s, id)
	defer c.Close()
	damage := []error{
		ErrDamaged, sqlite3.CORRUPT, sqlite3.IOERR_DATA, sqlite3.NOTADB,
		fmt.Errorf("wrapped: %w", sqlite3.CORRUPT),
	}
	for _, e := range damage {
		entry := verified.lookup(cacheKey(dir))
		entry.ok = true
		if got := c.mapDBErr(e); got == nil {
			t.Fatalf("mapDBErr(%v) = nil", e)
		}
		if !entry.stale.Load() {
			t.Errorf("mapDBErr(%v) did not drop the cache entry", e)
		}
	}
	for _, e := range []error{sqlite3.BUSY, errors.New("plain"), nil} {
		entry := verified.lookup(cacheKey(dir))
		_ = c.noteDamage(e)
		if entry.stale.Load() {
			t.Errorf("noteDamage(%v) dropped the cache entry", e)
		}
	}
}
