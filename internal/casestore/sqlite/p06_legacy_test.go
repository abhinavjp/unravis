package sqlite

// TSK-21 L4: once a master key exists, a case folder sealed only by DPAPI opens
// only if its id is on the legacy list (Options.LegacyCheck). A folder planted by
// another process in the cases folder never opens, is shown as untrusted, and its
// database is never read.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// legacyList is a stand-in for the session's legacy set.
type legacyList struct {
	ids  map[casestore.CaseID]bool
	done []casestore.CaseID
}

func (l *legacyList) check(id casestore.CaseID) error {
	if l.ids[id] {
		return nil
	}
	return casestore.ErrUntrustedLegacy
}

func (l *legacyList) finish(id casestore.CaseID) {
	delete(l.ids, id)
	l.done = append(l.done, id)
}

func withLegacy(l *legacyList) func(*Options) {
	return func(o *Options) { o.LegacyCheck, o.LegacyDone = l.check, l.finish }
}

func TestP06_PlantedLegacyCaseIsRefusedEverywhereAndNeverRead(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	_, root, p, info := dpapiCase(t)
	dir := filepath.Join(root, string(info.ID))
	// Poison the database: if the gate ever let a read through, the open or the
	// list would fail with something other than ErrUntrustedLegacy.
	if err := os.WriteFile(filepath.Join(dir, dbName), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &legacyList{ids: map[casestore.CaseID]bool{}}
	s := newStoreAt(t, root, p, withLegacy(l), func(o *Options) { o.Protectors = keyringLookup(kr) })
	ctx := context.Background()

	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrUntrustedLegacy) {
		t.Fatalf("Open = %v, want ErrUntrustedLegacy", err)
	}
	if err := s.Rewrap(ctx, info.ID); !errors.Is(err, casestore.ErrUntrustedLegacy) {
		t.Fatalf("Rewrap = %v, want ErrUntrustedLegacy", err)
	}
	if err := s.CloseCase(ctx, info.ID); !errors.Is(err, casestore.ErrUntrustedLegacy) {
		t.Fatalf("CloseCase = %v, want ErrUntrustedLegacy", err)
	}
	if k, _ := s.CaseKind(info.ID); k != casestore.KindDPAPI {
		t.Fatalf("a refused Rewrap changed the kind to %q", k)
	}
	if len(l.done) != 0 {
		t.Fatalf("LegacyDone ran for a refused case: %v", l.done)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if !list[0].Untrusted || list[0].ID != info.ID || list[0].Title == info.Title {
		t.Fatalf("List entry = %+v, want untrusted and no real title", list[0])
	}
	// The folder is left alone, and it can still be erased.
	if _, err := os.Stat(filepath.Join(dir, keyName)); err != nil {
		t.Fatalf("case.key gone: %v", err)
	}
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatalf("Erase of an untrusted case: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("erased case folder still there: %v", err)
	}
}

func TestP06_ListedLegacyCaseOpensRewrapsAndLeavesTheList(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	_, root, p, info := dpapiCase(t)
	l := &legacyList{ids: map[casestore.CaseID]bool{info.ID: true}}
	s := newStoreAt(t, root, p, withLegacy(l), func(o *Options) { o.Protectors = keyringLookup(kr) })
	ctx := context.Background()

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Untrusted || list[0].Title != info.Title {
		t.Fatalf("List = %+v, %v", list, err)
	}
	c, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatalf("Open of a listed legacy case: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Rewrap(ctx, info.ID); err != nil {
		t.Fatalf("Rewrap of a listed legacy case: %v", err)
	}
	if len(l.done) != 1 || l.done[0] != info.ID {
		t.Fatalf("LegacyDone calls = %v", l.done)
	}
	// Now master-key sealed: it needs no list entry.
	s2 := newStoreAt(t, root, p, withLegacy(&legacyList{ids: map[casestore.CaseID]bool{}}),
		func(o *Options) { o.Protectors = keyringLookup(kr) })
	c2, err := s2.Open(ctx, info.ID)
	if err != nil {
		t.Fatalf("Open after the re-wrap with an empty list: %v", err)
	}
	_ = c2.Close()
}

// A re-wrap cut after the manifest was replaced leaves a master-key manifest and a
// DPAPI key file. Only the owner can have written that manifest, so the case heals
// even though its id left the list.
func TestP06_HalfRewrappedCaseNotOnTheListStillHeals(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	_, root, p, info := dpapiCase(t)
	dir := filepath.Join(root, string(info.ID))
	stop := errors.New("power cut")
	err := casestore.Rewrap(dir,
		casestore.Sealing{Kind: casestore.KindDPAPI, P: p},
		casestore.Sealing{Kind: casestore.KindMasterKey, P: kr},
		casestore.RewrapOptions{Step: func(s string) error {
			if s == "manifest-replaced" {
				return stop
			}
			return nil
		}})
	if !errors.Is(err, stop) {
		t.Fatalf("cut re-wrap = %v", err)
	}
	l := &legacyList{ids: map[casestore.CaseID]bool{}}
	s := newStoreAt(t, root, p, withLegacy(l), func(o *Options) { o.Protectors = keyringLookup(kr) })
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatalf("Open of a half re-wrapped case off the list: %v", err)
	}
	_ = c.Close()
	if k, _ := s.CaseKind(info.ID); k != casestore.KindMasterKey {
		t.Fatalf("kind after the heal = %q", k)
	}
}

// No master key: no LegacyCheck, and every DPAPI case opens as before.
func TestP06_NoLegacyCheckLeavesDPAPICasesAlone(t *testing.T) {
	s, _, _, info := dpapiCase(t)
	if s.opt.LegacyCheck != nil {
		t.Fatal("test store has a LegacyCheck")
	}
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	list, err := s.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Untrusted {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

// A check that cannot decide (locked) is never taken for a yes or for "untrusted":
// List still reads the DPAPI manifest, as the startup sweep needs.
func TestP06_LockedLegacyCheckDoesNotFlagOrOpen(t *testing.T) {
	_, root, p, info := dpapiCase(t)
	s := newStoreAt(t, root, p, func(o *Options) {
		o.LegacyCheck = func(casestore.CaseID) error { return casestore.ErrLocked }
	})
	ctx := context.Background()
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Open = %v, want ErrLocked", err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Untrusted || list[0].Title != info.Title {
		t.Fatalf("List = %+v, %v", list, err)
	}
}
