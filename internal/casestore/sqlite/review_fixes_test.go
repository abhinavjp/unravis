package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Case B's key and database copied into case A's folder must be refused, whether
// or not the key blob is rewritten to name A.
func TestSwappedDatabaseIsRefused(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	a := mustCreate(t, s, "A")
	b := mustCreate(t, s, "B")
	cb := mustOpen(t, s, b.ID)
	mustImport(t, cb, "b", 7)
	if err := cb.Close(); err != nil {
		t.Fatal(err)
	}
	dirA := filepath.Join(root, string(a.ID))
	dirB := filepath.Join(root, string(b.ID))
	for _, n := range []string{dbName + "-wal", dbName + "-shm"} {
		_ = os.Remove(filepath.Join(dirA, n))
	}

	// 1. Key and database copied verbatim: the key blob names case B.
	for _, n := range []string{keyName, dbName} {
		if err := os.WriteFile(filepath.Join(dirA, n), readFile(t, filepath.Join(dirB, n)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Open(ctx, a.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("copied key and database: Open = %v, want ErrDamaged", err)
	}

	// 2. The attacker also re-seals B's key bytes under A's ID (the protector is
	// per user, so the same user can do this). The database's own case_id catches it.
	keys, err := casestore.ReadKeyFile(filepath.Join(dirB, keyName), s.opt.Protector)
	if err != nil {
		t.Fatal(err)
	}
	keys.CaseID = string(a.ID)
	if err := casestore.WriteKeyFile(filepath.Join(dirA, keyName), s.opt.Protector, keys); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, a.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("re-sealed key over a foreign database: Open = %v, want ErrDamaged", err)
	}
	// A's manifest was never rewritten to bless the foreign rows.
	man, err := readManifest(dirA, s.opt.Protector)
	if err != nil || len(man.Imports) != 0 {
		t.Fatalf("manifest of A = %+v, %v; want untouched", man, err)
	}
	// B itself is unharmed.
	if cb2, err := s.Open(ctx, b.ID); err != nil {
		t.Fatalf("Open(B): %v", err)
	} else {
		_ = cb2.Close()
	}
}

func TestCaseFilesThatAreNotRegularFilesAreRefused(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Case")
	key := filepath.Join(root, string(info.ID), keyName)
	saved := readFile(t, key)
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(key, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrReparsePoint) {
		t.Fatalf("directory in place of case.key: Open = %v, want ErrReparsePoint", err)
	}
	_ = os.Remove(key)
	if err := os.WriteFile(key, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Open(ctx, info.ID); err != nil {
		t.Fatalf("restored case: %v", err)
	} else {
		_ = c.Close()
	}
}

func TestPseudonymizeFailsClosedAfterCloseAndErase(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	zeroKey := func(kind, value string) string {
		m := hmac.New(sha256.New, make([]byte, 32))
		m.Write([]byte(kind + "\x00" + value))
		return kind + "-" + hex.EncodeToString(m.Sum(nil))[:8]
	}

	a := mustCreate(t, s, "A")
	b := mustCreate(t, s, "B")
	ca, cb := mustOpen(t, s, a.ID), mustOpen(t, s, b.ID)
	live := ca.Pseudonymize("user", "alice@example.test")
	if live == "user-"+pseudonymUnavailable || live == zeroKey("user", "alice@example.test") {
		t.Fatalf("live pseudonym = %q", live)
	}

	if err := ca.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, b.ID); err != nil { // handle still held by the caller
		t.Fatal(err)
	}
	for name, c := range map[string]*Case{"closed": ca, "erased": cb} {
		got := c.Pseudonymize("user", "alice@example.test")
		if got != "user-"+pseudonymUnavailable {
			t.Errorf("%s case: Pseudonymize = %q, want the unavailable sentinel", name, got)
		}
		if got == zeroKey("user", "alice@example.test") {
			t.Errorf("%s case: pseudonym derived from a zeroed key", name)
		}
		if c.Pseudonymize("user", "bob@example.test") != got {
			t.Errorf("%s case: sentinel varies with the value", name)
		}
	}
}

// OnErase must survive a failing hook and a process that dies inside it: the
// folder keeps a marker, and Sweep (here on a brand new Store) finishes the job.
func TestOnEraseIsNeverLost(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"error", "crash"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var mu sync.Mutex
			var seen []casestore.CaseID
			failing := true
			hook := func(_ context.Context, id casestore.CaseID) error {
				calls.Add(1)
				mu.Lock()
				defer mu.Unlock()
				if failing {
					if mode == "crash" {
						panic("process died inside OnErase")
					}
					return errors.New("export deletion failed")
				}
				seen = append(seen, id)
				return nil
			}
			s, root := newTestStore(t, func(o *Options) { o.OnErase = hook })
			info := mustCreate(t, s, "Case")
			c := mustOpen(t, s, info.ID)
			mustImport(t, c, "a", 3)

			func() {
				defer func() { _ = recover() }()
				_ = s.Erase(ctx, info.ID)
			}()
			dir := filepath.Join(root, string(info.ID))
			left, _ := os.ReadDir(dir)
			if len(left) != 1 || left[0].Name() != eraseMarker {
				t.Fatalf("folder after the interrupted erase holds %d entries, want only the marker", len(left))
			}

			// A new process: a new Store on the same folder. The key is already gone.
			mu.Lock()
			failing = false
			mu.Unlock()
			s2 := newStoreAt(t, root, s.opt.Protector, func(o *Options) { o.OnErase = hook })
			infos, err := s2.List(ctx)
			if err != nil || len(infos) != 1 || infos[0].Title != "(erase pending)" {
				t.Fatalf("List = %+v, %v", infos, err)
			}
			if _, err := s2.Open(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
				t.Fatalf("Open of a half-erased case = %v, want ErrNotFound", err)
			}
			erased, err := s2.Sweep(ctx, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
			if err != nil || len(erased) != 1 || erased[0] != info.ID {
				t.Fatalf("Sweep = %v, %v", erased, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 1 || seen[0] != info.ID {
				t.Fatalf("the hook never completed for the case: %v", seen)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("folder survived the retry: %v", err)
			}
			if got, err := s2.Sweep(ctx, time.Now()); err != nil || len(got) != 0 {
				t.Fatalf("second Sweep = %v, %v", got, err)
			}
		})
	}
}

// A marker left by a process that died right after writing it, before it touched
// the key: the case is treated as erased and Sweep completes it.
func TestMarkerWithoutDestroyedKeyIsStillErased(t *testing.T) {
	ctx := context.Background()
	var calls int
	s, root := newTestStore(t, func(o *Options) {
		o.OnErase = func(context.Context, casestore.CaseID) error { calls++; return nil }
	})
	info := mustCreate(t, s, "Case")
	dir := filepath.Join(root, string(info.ID))
	if err := markErase(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("Open with a marker = %v, want ErrNotFound", err)
	}
	if err := s.CloseCase(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("CloseCase with a marker = %v, want ErrNotFound", err)
	}
	if got, err := s.Sweep(ctx, time.Now()); err != nil || len(got) != 1 {
		t.Fatalf("Sweep = %v, %v", got, err)
	}
	if calls != 1 {
		t.Fatalf("hook calls = %d", calls)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder survived: %v", err)
	}
}

// Open must not revive a case that Erase has already taken out of the handle map.
func TestOpenCannotReviveACaseBeingErased(t *testing.T) {
	ctx := context.Background()
	var s *Store
	var duringErr, secondErase error
	var duringHandle casestore.Case
	var hookRan bool
	var root string
	s, root = newTestStore(t, func(o *Options) {
		o.duringErase = func() {
			hookRan = true
			var id casestore.CaseID
			for _, e := range mustReadDir(root) {
				id = casestore.CaseID(e)
			}
			duringHandle, duringErr = s.Open(ctx, id)
			secondErase = s.Erase(ctx, id)
		}
	})
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 3)

	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if !hookRan {
		t.Fatal("test hook did not run")
	}
	if duringHandle != nil || !errors.Is(duringErr, casestore.ErrNotFound) {
		t.Fatalf("Open during Erase = %v, %v; want ErrNotFound", duringHandle, duringErr)
	}
	if secondErase == nil {
		t.Fatal("a concurrent Erase of the same case was not refused")
	}
	if entries := mustReadDir(root); len(entries) != 0 {
		t.Fatalf("folder survived: %v", entries)
	}
	s.mu.Lock()
	n, e := len(s.open), len(s.erasing)
	s.mu.Unlock()
	if n != 0 || e != 0 {
		t.Fatalf("store state after Erase: %d open, %d erasing", n, e)
	}
}

func TestOpenRacingEraseNeverLeavesALiveHandle(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s, root := newTestStore(t)
		info := mustCreate(t, s, "Case")
		stop := make(chan struct{})
		var wg sync.WaitGroup
		var opened atomic.Int32
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					c, err := s.Open(ctx, info.ID)
					if err == nil {
						opened.Add(1)
						_ = c.Close()
					}
				}
			}()
		}
		time.Sleep(20 * time.Millisecond)
		err := s.Erase(ctx, info.ID)
		close(stop)
		wg.Wait()
		if err != nil {
			t.Fatalf("round %d: Erase = %v", i, err)
		}
		if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
			t.Fatalf("round %d: Open after Erase = %v", i, err)
		}
		if entries := mustReadDir(root); len(entries) != 0 {
			t.Fatalf("round %d: folder survived: %v", i, entries)
		}
		s.mu.Lock()
		n := len(s.open)
		s.mu.Unlock()
		if n != 0 {
			t.Fatalf("round %d: %d handles still open", i, n)
		}
	}
}

func mustReadDir(dir string) []string {
	entries, _ := os.ReadDir(dir)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
