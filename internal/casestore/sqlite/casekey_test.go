package sqlite

import (
	"bytes"
	"fmt"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

const (
	ckPass  = "correct horse battery"
	ckPass2 = "another long phrase here"
)

var ckFast = masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1}

func ckStore(t *testing.T, mut ...func(*Options)) (*Store, string) {
	t.Helper()
	return newTestStore(t, append([]func(*Options){func(o *Options) { o.KeyParams = ckFast }}, mut...)...)
}

func ckCreate(t *testing.T, s *Store, title string) casestore.CaseInfo {
	t.Helper()
	info, again, err := s.CreateWithKey(context.Background(), title, "", []byte(ckPass))
	if err != nil || again {
		t.Fatalf("CreateWithKey: %v %v", again, err)
	}
	return info
}

func ckRestart(t *testing.T, s *Store, root string, mut ...func(*Options)) *Store {
	t.Helper()
	_ = s.Close()
	return newStoreAt(t, root, s.opt.Protector, append([]func(*Options){func(o *Options) { o.KeyParams = ckFast }}, mut...)...)
}

func caseFilesText(t *testing.T, dir string) []byte {
	t.Helper()
	var all bytes.Buffer
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		all.Write(readFile(t, filepath.Join(dir, e.Name())))
	}
	return all.Bytes()
}

func TestCaseKeyCreateSealsEverythingUnderTheCaseKey(t *testing.T) {
	s, root := ckStore(t)
	info := ckCreate(t, s, "Secret title "+sentinel)
	if !info.OwnKey || len(info.KeyEnding) != 4 {
		t.Fatalf("info %+v", info)
	}
	dir := filepath.Join(root, string(info.ID))
	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindCaseKey {
		t.Fatalf("kind %q %v", k, err)
	}
	if k, _ := casestore.FileKind(filepath.Join(dir, manifestName)); k != casestore.KindCaseKey {
		t.Fatalf("manifest kind %q", k)
	}
	check, err := s.CaseKeyCheck(info.ID)
	if err != nil || len(check) != 64 || !strings.HasSuffix(check, info.KeyEnding) {
		t.Fatalf("check %q %v", check, err)
	}
	text := caseFilesText(t, dir)
	for _, secret := range []string{ckPass, check, sentinel} {
		if bytes.Contains(text, []byte(secret)) {
			t.Fatalf("%q found in the case folder", secret)
		}
	}
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "one", 5)
	if got := c.Info(); !got.OwnKey || got.KeyEnding != info.KeyEnding || got.Title != "Secret title "+sentinel {
		t.Fatalf("Info %+v", got)
	}
}

func TestCaseKeyLockedCaseListsByCodeAndRefusesOpenWithItsOwnError(t *testing.T) {
	s, root := ckStore(t)
	keyed := ckCreate(t, s, "Keyed")
	plain := mustCreate(t, s, "Plain")
	pc := mustOpen(t, s, plain.ID)
	mustImport(t, pc, "p", 3)
	kc := mustOpen(t, s, keyed.ID)
	mustImport(t, kc, "k", 4)

	// Lock the one case: its handle closes, the other is untouched.
	if err := s.LockCaseKey(keyed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Import(context.Background(), tMeta("z.csv"), tBody("z"), tRun(tEvents(kc, "z", 1))); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Fatalf("handle after lock: %v", err)
	}
	mustImport(t, pc, "p2", 2)
	if _, err := s.Open(context.Background(), keyed.ID); !errors.Is(err, casestore.ErrCaseLocked) || errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Open locked: %v", err)
	}
	all, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range all {
		switch i.ID {
		case keyed.ID:
			if !i.OwnKey || !i.KeyLocked || i.KeyEnding != keyed.KeyEnding || i.Title != "(locked)" || i.Imports != 0 {
				t.Fatalf("locked listing %+v", i)
			}
		case plain.ID:
			if i.OwnKey || i.KeyLocked || i.Title != "Plain" {
				t.Fatalf("plain listing %+v", i)
			}
		}
	}
	if info, err := s.CaseKey(keyed.ID); err != nil || !info.Has || !info.Locked || info.Ending != keyed.KeyEnding {
		t.Fatalf("CaseKey %+v %v", info, err)
	}
	if _, err := s.CaseKeyCheck(keyed.ID); !errors.Is(err, casestore.ErrCaseLocked) {
		t.Fatalf("check while locked: %v", err)
	}
	// A new process starts locked.
	s2 := ckRestart(t, s, root)
	if _, err := s2.Open(context.Background(), keyed.ID); !errors.Is(err, casestore.ErrCaseLocked) {
		t.Fatalf("after restart: %v", err)
	}
}

func TestCaseKeyWrongKeyThenRightKey(t *testing.T) {
	s, root := ckStore(t)
	info := ckCreate(t, s, "Keyed")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "one", 6)
	s2 := ckRestart(t, s, root)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s2.UnlockCase(ctx, info.ID, []byte("not the right key at all")); !errors.Is(err, casestore.ErrWrongCaseKey) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if _, err := s2.Open(ctx, info.ID); !errors.Is(err, casestore.ErrCaseLocked) {
		t.Fatalf("still locked after wrong keys: %v", err)
	}
	if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, s2, info.ID)
	if n, _, _ := counts(t, c2); n != 1 {
		t.Fatalf("imports after unlock = %d", n)
	}
	if err := s2.UnlockCase(ctx, casestore.CaseID("abcdefghijklmnopqrstuvwxyz"), []byte(ckPass)); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("unknown case: %v", err)
	}
	plain := mustCreate(t, s2, "plain")
	if err := s2.UnlockCase(ctx, plain.ID, []byte(ckPass)); !errors.Is(err, casestore.ErrNoCaseKey) {
		t.Fatalf("unlock of an unkeyed case: %v", err)
	}
}

func TestCaseKeyRulesRefuseShortAndCreateNothing(t *testing.T) {
	s, root := ckStore(t)
	for _, bad := range []string{"", "short", "            "} {
		if _, _, err := s.CreateWithKey(context.Background(), "T", "", []byte(bad)); !errors.Is(err, casestore.ErrCaseKeyRule) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("%d folders left", len(entries))
	}
	info := mustCreate(t, s, "x")
	if err := s.SetCaseKey(context.Background(), info.ID, []byte("short")); !errors.Is(err, casestore.ErrCaseKeyRule) {
		t.Fatal(err)
	}
	if ci, _ := s.CaseKey(info.ID); ci.Has {
		t.Fatal("a refused key was set")
	}
}

func TestCaseKeyCreateIsIdempotentPerRequestID(t *testing.T) {
	s, _ := ckStore(t)
	ctx := context.Background()
	a, again, err := s.CreateWithKey(ctx, "T", "req-0123456789", []byte(ckPass))
	if err != nil || again {
		t.Fatal(again, err)
	}
	b, again, err := s.CreateWithKey(ctx, "T", "req-0123456789", []byte(ckPass2))
	if err != nil || !again || b.ID != a.ID {
		t.Fatalf("%+v %v %v", b, again, err)
	}
	// The second request did not change the key.
	if err := s.LockCaseKey(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockCase(ctx, a.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
}

func TestCaseKeyChangeKeepsDataAndRetiresTheOldKey(t *testing.T) {
	s, root := ckStore(t)
	info := ckCreate(t, s, "Keyed")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "one", 6)
	ctx := context.Background()
	if err := s.ChangeCaseKey(ctx, info.ID, []byte("wrong current key here"), []byte(ckPass2)); !errors.Is(err, casestore.ErrWrongCaseKey) {
		t.Fatal(err)
	}
	if err := s.ChangeCaseKey(ctx, info.ID, []byte(ckPass), []byte("tiny")); !errors.Is(err, casestore.ErrCaseKeyRule) {
		t.Fatal(err)
	}
	before, _ := s.CaseKey(info.ID)
	if err := s.ChangeCaseKey(ctx, info.ID, []byte(ckPass), []byte(ckPass2)); err != nil {
		t.Fatal(err)
	}
	after, _ := s.CaseKey(info.ID)
	if after.Ending == before.Ending || !after.Has || after.Locked {
		t.Fatalf("before %+v after %+v", before, after)
	}
	if got := c.Info().KeyEnding; got != after.Ending {
		t.Fatalf("open handle shows %q, want %q", got, after.Ending)
	}
	s2 := ckRestart(t, s, root)
	if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass)); !errors.Is(err, casestore.ErrWrongCaseKey) {
		t.Fatalf("old key still opens: %v", err)
	}
	if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass2)); err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, s2, info.ID)
	if n, ev, _ := counts(t, c2); n != 1 || ev != 6 {
		t.Fatalf("counts %d %d", n, ev)
	}
}

func TestCaseKeySetOnExistingCaseThenRemove(t *testing.T) {
	s, root := ckStore(t)
	info := mustCreate(t, s, "Was plain")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "one", 5)
	ctx := context.Background()
	if err := s.SetCaseKey(ctx, info.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCaseKey(ctx, info.ID, []byte(ckPass2)); !errors.Is(err, casestore.ErrHasCaseKey) {
		t.Fatalf("second set: %v", err)
	}
	if k, _ := s.CaseKind(info.ID); k != casestore.KindCaseKey {
		t.Fatalf("kind %q", k)
	}
	mustImport(t, c, "two", 3) // the open handle keeps working: its manifest is now sealed under the case key
	if err := s.LockCaseKey(info.ID); err != nil {
		t.Fatal(err)
	}
	s2 := ckRestart(t, s, root)
	if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, s2, info.ID)
	if n, ev, _ := counts(t, c2); n != 2 || ev != 8 {
		t.Fatalf("counts %d %d", n, ev)
	}
	_ = c2.Close()
	if err := s2.RemoveCaseKey(ctx, info.ID, []byte("a wrong current key!!")); !errors.Is(err, casestore.ErrWrongCaseKey) {
		t.Fatal(err)
	}
	if err := s2.RemoveCaseKey(ctx, info.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	if k, _ := s2.CaseKind(info.ID); k != casestore.KindDPAPI {
		t.Fatalf("kind after remove %q", k)
	}
	if _, err := os.Stat(filepath.Join(root, string(info.ID), wrapName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrap still there: %v", err)
	}
	s3 := ckRestart(t, s2, root)
	c3 := mustOpen(t, s3, info.ID) // no key needed any more
	if n, _, _ := counts(t, c3); n != 2 {
		t.Fatalf("imports %d", n)
	}
	if ci, _ := s3.CaseKey(info.ID); ci.Has {
		t.Fatal("still keyed")
	}
}

// A cut at any step of Set leaves a case the next unlock with the new key finishes,
// or (cut before the wrap is written) one that is exactly as it was.
func TestCaseKeySetCutAtEveryStepIsFinishedByUnlock(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"set:wrap", "set:manifest", "set:key", "set:active"} {
		t.Run(stage, func(t *testing.T) {
			s, root := ckStore(t, func(o *Options) {
				o.ckStep = func(st string) error {
					if st == stage {
						return errCut
					}
					return nil
				}
			})
			info := mustCreate(t, s, "Cut me")
			c := mustOpen(t, s, info.ID)
			mustImport(t, c, "one", 4)
			_ = c.Close()
			if err := s.SetCaseKey(ctx, info.ID, []byte(ckPass)); !errors.Is(err, errCut) {
				t.Fatalf("set: %v", err)
			}
			s2 := ckRestart(t, s, root)
			if stage == "set:wrap" {
				// Nothing was written: still an ordinary case.
				mustOpen(t, s2, info.ID)
				if ci, _ := s2.CaseKey(info.ID); ci.Has {
					t.Fatal("wrap written before its step")
				}
				return
			}
			if _, err := s2.Open(ctx, info.ID); !errors.Is(err, casestore.ErrCaseLocked) {
				t.Fatalf("open of a cut set: %v", err)
			}
			if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass)); err != nil {
				t.Fatalf("unlock: %v", err)
			}
			c2 := mustOpen(t, s2, info.ID)
			if n, ev, _ := counts(t, c2); n != 1 || ev != 4 {
				t.Fatalf("counts %d %d", n, ev)
			}
			for _, name := range []string{manifestName, keyName} {
				if k, _ := casestore.FileKind(filepath.Join(root, string(info.ID), name)); k != casestore.KindCaseKey {
					t.Fatalf("%s kind %q", name, k)
				}
			}
			// Finished means active: a second unlock changes nothing and a wrong key still fails.
			if err := s2.UnlockCase(ctx, info.ID, []byte("not the right key at all")); !errors.Is(err, casestore.ErrWrongCaseKey) {
				t.Fatal(err)
			}
		})
	}
}

func TestCaseKeyRemoveCutAtEveryStepIsFinishedByUnlock(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"remove:wrap", "remove:manifest", "remove:key", "remove:done"} {
		t.Run(stage, func(t *testing.T) {
			cut := false
			s, root := ckStore(t, func(o *Options) {
				o.ckStep = func(st string) error {
					if cut && st == stage {
						return errCut
					}
					return nil
				}
			})
			info := ckCreate(t, s, "Cut me")
			c := mustOpen(t, s, info.ID)
			mustImport(t, c, "one", 4)
			_ = c.Close()
			cut = true
			if err := s.RemoveCaseKey(ctx, info.ID, []byte(ckPass)); !errors.Is(err, errCut) {
				t.Fatalf("remove: %v", err)
			}
			cut = false
			s2 := ckRestart(t, s, root, func(o *Options) { o.ckStep = nil })
			if _, err := s2.Open(ctx, info.ID); stage != "remove:done" && !errors.Is(err, casestore.ErrCaseLocked) {
				// Cut before the wrap was deleted: still keyed on disk, so locked.
				t.Fatalf("open of a cut remove: %v", err)
			}
			if err := s2.UnlockCase(ctx, info.ID, []byte(ckPass)); err != nil {
				t.Fatalf("unlock: %v", err)
			}
			wantKeyed := stage == "remove:wrap"
			if ci, _ := s2.CaseKey(info.ID); ci.Has != wantKeyed {
				t.Fatalf("keyed = %v, want %v", ci.Has, wantKeyed)
			}
			c2 := mustOpen(t, s2, info.ID)
			if n, ev, _ := counts(t, c2); n != 1 || ev != 4 {
				t.Fatalf("counts %d %d", n, ev)
			}
		})
	}
}

func TestCaseKeyFailedSetRollsBack(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("disk full")
	s, root := ckStore(t, func(o *Options) {
		o.ckStep = func(st string) error {
			if st == "set:key" {
				return boom
			}
			return nil
		}
	})
	info := mustCreate(t, s, "Roll back")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "one", 3)
	_ = c.Close()
	if err := s.SetCaseKey(ctx, info.ID, []byte(ckPass)); !errors.Is(err, boom) {
		t.Fatalf("set: %v", err)
	}
	if ci, _ := s.CaseKey(info.ID); ci.Has {
		t.Fatal("wrap left after a failed set")
	}
	s2 := ckRestart(t, s, root)
	c2 := mustOpen(t, s2, info.ID) // an ordinary case again
	if n, _, _ := counts(t, c2); n != 1 {
		t.Fatalf("imports %d", n)
	}
}

func TestCaseKeyMasterKeyChangesLeaveTheCaseAlone(t *testing.T) {
	ctx := context.Background()
	kr := rewrapKeyring(t, "correct horse battery")
	root := filepath.Join(t.TempDir(), "cases")
	useMaster := func(o *Options) {
		o.KeyParams = ckFast
		o.NewCaseKind = func() string { return casestore.KindMasterKey }
	}
	s := masterStore(t, root, kr, useMaster)
	keyed := ckCreate(t, s, "Keyed")
	other := mustCreate(t, s, "Master case")
	if k, _ := s.CaseKind(other.ID); k != casestore.KindMasterKey {
		t.Fatalf("other kind %q", k)
	}
	kc := mustOpen(t, s, keyed.ID)
	mustImport(t, kc, "one", 3)
	_ = kc.Close()

	// Locking the master key: the master case locks, the keyed case does not.
	kr.Lock()
	all, _ := s.List(ctx)
	titles := map[casestore.CaseID]string{}
	for _, i := range all {
		titles[i.ID] = i.Title
	}
	if titles[other.ID] != "(locked)" || titles[keyed.ID] != "Keyed" {
		t.Fatalf("titles %v", titles)
	}
	mustOpen(t, s, keyed.ID)
	// A master key reset: a fresh master key. The keyed case never was under the old one.
	kr2 := rewrapKeyring(t, "a brand new master phrase")
	s2 := masterStore(t, root, kr2, useMaster)
	if err := s2.UnlockCase(ctx, keyed.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, s2, keyed.ID)
	if err := s2.Rewrap(ctx, keyed.ID); err != nil {
		t.Fatalf("rewrap of a keyed case: %v", err)
	}
	if k, _ := s2.CaseKind(keyed.ID); k != casestore.KindCaseKey {
		t.Fatalf("kind after rewrap %q", k)
	}
	if s2.NeedsRewrap(keyed.ID) {
		t.Fatal("a keyed case needs no rewrap")
	}
	// Removing the key needs the (new) master key, since that is where the case goes.
	kr2.Lock()
	if err := s2.RemoveCaseKey(ctx, keyed.ID, []byte(ckPass)); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("remove with the master key locked: %v", err)
	}
	if ci, _ := s2.CaseKey(keyed.ID); !ci.Has {
		t.Fatal("a refused remove changed the case")
	}
}

func TestCaseKeyRefusedWhileTheCaseIsMidMove(t *testing.T) {
	ctx := context.Background()
	cases := map[string]error{
		casestore.MarkArchiving: casestore.ErrArchiveRunning,
		casestore.MarkFreeing:   casestore.ErrFreeing,
		casestore.MarkErasing:   casestore.ErrErasing,
	}
	for mark, want := range cases {
		t.Run(mark, func(t *testing.T) {
			s, _ := ckStore(t)
			plain := mustCreate(t, s, "plain")
			keyed := ckCreate(t, s, "keyed")
			if err := s.SetMark(ctx, plain.ID, mark); err != nil {
				t.Fatal(err)
			}
			if err := s.SetMark(ctx, keyed.ID, mark); err != nil {
				t.Fatal(err)
			}
			if err := s.SetCaseKey(ctx, plain.ID, []byte(ckPass)); !errors.Is(err, want) {
				t.Fatalf("set: %v", err)
			}
			if err := s.ChangeCaseKey(ctx, keyed.ID, []byte(ckPass), []byte(ckPass2)); err != nil {
				// Change touches only case.wrap, so a mark does not matter to it.
				t.Fatalf("change: %v", err)
			}
			if err := s.RemoveCaseKey(ctx, keyed.ID, []byte(ckPass2)); !errors.Is(err, want) {
				t.Fatalf("remove: %v", err)
			}
		})
	}
}

func TestCaseKeyEraseWorksWhileLockedAndRemovesTheWrap(t *testing.T) {
	ctx := context.Background()
	s, root := ckStore(t)
	info := ckCreate(t, s, "Erase me")
	s2 := ckRestart(t, s, root)
	if err := s2.Erase(ctx, info.ID); err != nil {
		t.Fatalf("erase of a locked case: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(info.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder left: %v", err)
	}
	// An unlocked one: the key is forgotten too.
	again := ckCreate(t, s2, "Erase me too")
	if err := s2.Erase(ctx, again.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CaseKeyCheck(again.ID); err == nil {
		t.Fatal("check value of an erased case")
	}
}

func TestCaseKeyDamagedOrSwappedWrapIsRefused(t *testing.T) {
	ctx := context.Background()
	s, root := ckStore(t)
	a := ckCreate(t, s, "A")
	b := ckCreate(t, s, "B")
	s = ckRestart(t, s, root)
	// B's wrap moved into A's folder: the right key for B, but not A's case.
	wb := readFile(t, filepath.Join(root, string(b.ID), wrapName))
	if err := os.WriteFile(filepath.Join(root, string(a.ID), wrapName), wb, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockCase(ctx, a.ID, []byte(ckPass)); !errors.Is(err, ErrDamaged) {
		t.Fatalf("swapped wrap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, string(a.ID), wrapName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockCase(ctx, a.ID, []byte(ckPass)); !errors.Is(err, ErrDamaged) {
		t.Fatalf("garbage wrap: %v", err)
	}
	if all, _ := s.List(ctx); len(all) != 2 {
		t.Fatalf("list %d", len(all))
	}
	// A wrap that is gone from a case sealed under a case key: damaged, never opened.
	if err := os.Remove(filepath.Join(root, string(a.ID), wrapName)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, a.ID); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("no wrap: %v", err)
	}
}

func TestCaseKeyNeverReachesLogs(t *testing.T) {
	var logs bytes.Buffer
	s, _ := ckStore(t, func(o *Options) {
		o.Logf = func(f string, a ...any) { logs.WriteString(strings.TrimSpace(fmt.Sprintf(f, a...)) + "\n") }
	})
	ctx := context.Background()
	info := ckCreate(t, s, "T")
	check, _ := s.CaseKeyCheck(info.ID)
	_ = s.ChangeCaseKey(ctx, info.ID, []byte(ckPass), []byte(ckPass2))
	_ = s.UnlockCase(ctx, info.ID, []byte("wrong wrong wrong wrong"))
	_ = s.LockCaseKey(info.ID)
	_ = s.UnlockCase(ctx, info.ID, []byte(ckPass2))
	if logs.Len() == 0 {
		t.Fatal("expected some log lines")
	}
	for _, secret := range []string{ckPass, ckPass2, "wrong wrong", check} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("%q in the log", secret)
		}
	}
}
