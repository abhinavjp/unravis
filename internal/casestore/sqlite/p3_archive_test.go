package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

// These tests cover what the shared contract cannot: the files on disk. The
// archive record lives in the sealed manifest, a mirror of it in <root>\.index,
// and a freed case keeps a folder with the manifest and nothing else.

var p3ctx = context.Background()

func p3File(name string) casestore.ArchiveFile {
	return casestore.ArchiveFile{Name: name, Folder: `D:\archives`, Bytes: 10, SHA256: "00", KeyKind: casestore.KeyKindMaster,
		KeyCheck: "0123456789abcdef", Made: time.Now(), Counts: casestore.Counts{Imports: 1, Events: 2}}
}

func p3Archive(t *testing.T, s *Store, id casestore.CaseID, name string) {
	t.Helper()
	pend := casestore.ArchivePending{Folder: `D:\archives`, Name: name, Started: time.Now()}
	if _, err := s.BeginArchive(p3ctx, id, "", pend); err != nil {
		t.Fatalf("BeginArchive: %v", err)
	}
	if err := s.CommitArchive(p3ctx, id, p3File(name)); err != nil {
		t.Fatalf("CommitArchive: %v", err)
	}
}

func p3Find(t *testing.T, s *Store, id casestore.CaseID) (casestore.CaseInfo, bool) {
	t.Helper()
	all, err := s.List(p3ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range all {
		if i.ID == id {
			return i, true
		}
	}
	return casestore.CaseInfo{}, false
}

func TestArchiveRecordSurvivesRestart(t *testing.T) {
	prot := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, prot)
	id := mustCreate(t, s, "Keep me").ID
	p3Archive(t, s, id, "keep-me.unravis-case")
	if err := s.AddRestoreLine(p3ctx, id, casestore.RestoreLine{At: time.Now(), Code: "abcd1234"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2 := newStoreAt(t, root, prot)
	info, ok := p3Find(t, s2, id)
	if !ok || casestore.StateOf(info) != casestore.StateArchived || info.Archive == nil {
		t.Fatalf("after restart: %+v %v", info, ok)
	}
	cur := info.Archive.Current()
	if cur == nil || cur.Name != "keep-me.unravis-case" || cur.KeyEnding() != "cdef" || cur.Counts.Events != 2 || len(info.Archive.Restores) != 1 {
		t.Fatalf("record after restart: %+v", info.Archive)
	}
	if _, err := s2.Open(p3ctx, id); !errors.Is(err, casestore.ErrArchived) {
		t.Fatalf("Open after restart: %v, want ErrArchived", err)
	}
}

func TestMissingCaseIsListedAndNeverSweptOrOpened(t *testing.T) {
	s, root := newTestStore(t)
	id := mustCreate(t, s, "Vanishing").ID
	if err := s.CloseCase(p3ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, string(id))); err != nil {
		t.Fatal(err)
	}
	info, ok := p3Find(t, s, id)
	if !ok || !info.Missing || info.Title != "Vanishing" || info.Closed == nil {
		t.Fatalf("a case whose folder is gone: %+v %v", info, ok)
	}
	ent, err := s.Entry(p3ctx, id)
	if err != nil || !ent.Missing || ent.Title != "Vanishing" {
		t.Fatalf("Entry: %+v %v", ent, err)
	}
	if _, err := s.Open(p3ctx, id); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open of a missing case: %v, want ErrNotFound", err)
	}
	if gone, err := s.Sweep(p3ctx, time.Now().Add(10*casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("Sweep of a missing case: %v %v", gone, err)
	}
	if _, ok := p3Find(t, s, id); !ok {
		t.Error("Sweep removed the row of a missing case: it is never removed silently")
	}
	// Erase (record only) is what removes it, and leaves nothing behind.
	if err := s.Erase(p3ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := p3Find(t, s, id); ok {
		t.Error("a missing case is still listed after Erase")
	}
	if _, err := s.Entry(p3ctx, id); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Entry after Erase: %v", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("Erase of a missing case left %d entries in the root", len(entries))
	}
}

func TestMissingKeptArchiveStaysArchivedAndMissing(t *testing.T) {
	s, root := newTestStore(t)
	id := mustCreate(t, s, "Archived then lost").ID
	p3Archive(t, s, id, "lost.unravis-case")
	if err := os.RemoveAll(filepath.Join(root, string(id))); err != nil {
		t.Fatal(err)
	}
	info, ok := p3Find(t, s, id)
	if !ok || !info.Missing || casestore.StateOf(info) != casestore.StateArchived || info.Archive.Current() == nil {
		t.Fatalf("kept archive with no folder: %+v %v", info, ok)
	}
}

func TestFreedCaseKeepsOnlyItsRecord(t *testing.T) {
	s, root := newTestStore(t)
	c := mustCreate(t, s, "Free me")
	id := c.ID
	h := mustOpen(t, s, id)
	mustImport(t, h, "a", 3)
	_ = h.Close()
	p3Archive(t, s, id, "free-me.unravis-case")
	if err := s.BeginFree(p3ctx, id, casestore.StateArchived); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishFree(p3ctx, id); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, string(id)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != manifestName {
			t.Errorf("a freed case kept %q: only the sealed record may stay", e.Name())
		}
	}
	info, ok := p3Find(t, s, id)
	if !ok || !info.Archive.Freed() || info.Missing {
		t.Fatalf("freed case row: %+v %v", info, ok)
	}
	// Rewrap passes a freed case by.
	if err := s.Rewrap(p3ctx, id); err != nil {
		t.Errorf("Rewrap of a freed case: %v", err)
	}
	// Its folder gone too (the person deleted it): still a freed row, not Missing.
	if err := os.RemoveAll(filepath.Join(root, string(id))); err != nil {
		t.Fatal(err)
	}
	info, ok = p3Find(t, s, id)
	if !ok || !info.Archive.Freed() || info.Missing {
		t.Fatalf("freed case with no folder: %+v %v (a freed case is never Missing)", info, ok)
	}
	ent, err := s.Entry(p3ctx, id)
	if err != nil || ent.Missing || !ent.Archive.Freed() {
		t.Fatalf("Entry of a freed case with no folder: %+v %v", ent, err)
	}
}

func TestFinishFreeIsSafeToRepeatAfterACrashMidRemoval(t *testing.T) {
	s, root := newTestStore(t)
	id := mustCreate(t, s, "Half freed").ID
	p3Archive(t, s, id, "half.unravis-case")
	if err := s.BeginFree(p3ctx, id, casestore.StateArchived); err != nil {
		t.Fatal(err)
	}
	// The crash: the key is already gone, the database is still there.
	if err := os.Remove(filepath.Join(root, string(id), keyName)); err != nil {
		t.Fatal(err)
	}
	ent, _ := s.Entry(p3ctx, id)
	if ent.Mark != casestore.MarkFreeing {
		t.Fatalf("mark after the crash: %q", ent.Mark)
	}
	if err := s.FinishFree(p3ctx, id); err != nil {
		t.Fatalf("FinishFree after a crash: %v", err)
	}
	if err := s.FinishFree(p3ctx, id); err != nil {
		t.Fatalf("FinishFree again: %v", err)
	}
	if info, _ := p3Find(t, s, id); !info.Archive.Freed() || info.Mark != "" {
		t.Fatalf("after recovery: %+v", info)
	}
}

func TestBeginFreeRefusesWhileAHandleIsOpen(t *testing.T) {
	s, _ := newTestStore(t)
	id := mustCreate(t, s, "Busy").ID
	h := mustOpen(t, s, id)
	p3Archive(t, s, id, "busy.unravis-case")
	if err := s.BeginFree(p3ctx, id, casestore.StateArchived); !errors.Is(err, casestore.ErrCaseInUse) {
		t.Fatalf("BeginFree with a handle: %v, want ErrCaseInUse", err)
	}
	// The held handle sees the case as archived and takes no new file.
	if st := casestore.StateOf(h.Info()); st != casestore.StateArchived {
		t.Errorf("state seen through the open handle: %v", st)
	}
	_ = h.Close()
	if err := s.BeginFree(p3ctx, id, casestore.StateArchived); err != nil {
		t.Fatalf("BeginFree after the handle closed: %v", err)
	}
}

func TestIndexIsRemovedWithTheCaseAndTheFolderWithTheLastRecord(t *testing.T) {
	s, root := newTestStore(t)
	a := mustCreate(t, s, "One").ID
	b := mustCreate(t, s, "Two").ID
	if _, err := os.Stat(filepath.Join(root, indexDirName, string(a)+".dat")); err != nil {
		t.Fatalf("no index record after Create: %v", err)
	}
	if err := s.Erase(p3ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, indexDirName, string(a)+".dat")); !os.IsNotExist(err) {
		t.Errorf("index record of an erased case: %v", err)
	}
	if err := s.Erase(p3ctx, b); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("root not empty after the last Erase: %d entries", len(entries))
	}
}

func TestIndexRecordIsRewrittenWithEveryMove(t *testing.T) {
	s, root := newTestStore(t)
	id := mustCreate(t, s, "Moves").ID
	// Lose the mirror; the next manifest write repairs it.
	if err := os.Remove(filepath.Join(root, indexDirName, string(id)+".dat")); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCase(p3ctx, id); err != nil {
		t.Fatal(err)
	}
	rec, err := s.readIndex(id)
	if err != nil || rec.Title != "Moves" || rec.Closed == nil {
		t.Fatalf("index after Close: %+v %v", rec, err)
	}
}

func TestDamagedIndexRecordNeverMakesAGhostRow(t *testing.T) {
	s, root := newTestStore(t)
	id := mustCreate(t, s, "Ghost").ID
	if err := os.RemoveAll(filepath.Join(root, string(id))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, indexDirName, string(id)+".dat"), []byte("not a sealed record"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := p3Find(t, s, id); ok {
		t.Error("a damaged index record produced a row")
	}
	// A stray file with a case id but a different id inside is not believed either.
	other := mustCreate(t, s, "Other").ID
	raw, _ := os.ReadFile(filepath.Join(root, indexDirName, string(other)+".dat"))
	wrong := "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := os.WriteFile(filepath.Join(root, indexDirName, wrong+".dat"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := p3Find(t, s, casestore.CaseID(wrong)); ok {
		t.Error("an index record under another id produced a row")
	}
}

func TestUnresolvedListSurvivesRestart(t *testing.T) {
	prot := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, prot)
	u := casestore.UnresolvedFile{Name: "x.unravis-case", Folder: `D:\out`, CaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaa", At: time.Now()}
	if err := s.AddUnresolved(p3ctx, u); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2 := newStoreAt(t, root, prot)
	l, err := s2.Unresolved(p3ctx)
	if err != nil || len(l) != 1 || l[0].Name != u.Name || l[0].Folder != u.Folder {
		t.Fatalf("after restart: %+v %v", l, err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, indexDirName, unresolvedName))
	if len(raw) == 0 || containsFold(string(raw), "x.unravis-case") || containsFold(string(raw), `D:\out`) {
		t.Error("the unresolved list is on disk in plain text")
	}
}

func TestSweepSkipsArchivedFreedAndMarkedCases(t *testing.T) {
	s, _ := newTestStore(t)
	arch := mustCreate(t, s, "Archived old").ID
	if err := s.CloseCase(p3ctx, arch); err != nil {
		t.Fatal(err)
	}
	p3Archive(t, s, arch, "old.unravis-case")
	marked := mustCreate(t, s, "Marked old").ID
	if err := s.CloseCase(p3ctx, marked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginArchive(p3ctx, marked, "", casestore.ArchivePending{Folder: `D:\a`, Name: "m.unravis-case", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	plain := mustCreate(t, s, "Closed old").ID
	if err := s.CloseCase(p3ctx, plain); err != nil {
		t.Fatal(err)
	}
	gone, err := s.Sweep(p3ctx, time.Now().Add(casestore.Retention+time.Hour))
	if err != nil || len(gone) != 1 || gone[0] != plain {
		t.Fatalf("Sweep erased %v (%v), want only the plain closed case", gone, err)
	}
}

func TestNoImportIntoACaseBeingArchivedOrArchived(t *testing.T) {
	s, _ := newTestStore(t)
	id := mustCreate(t, s, "Frozen").ID
	h := mustOpen(t, s, id)
	c := h
	mustImport(t, c, "before", 3)
	pend := casestore.ArchivePending{Folder: `D:\archives`, Name: "frozen.unravis-case", Started: time.Now()}
	if _, err := s.BeginArchive(p3ctx, id, "", pend); err != nil {
		t.Fatal(err)
	}
	try := func(when string) {
		_, err := c.Import(p3ctx, tMeta("late.csv"), tBody("late"), tRun(tEvents(c, "late", 1)))
		if !errors.Is(err, casestore.ErrCaseClosed) {
			t.Fatalf("import %s: %v, want ErrCaseClosed", when, err)
		}
	}
	try("while Archiving")
	if err := s.AbortArchive(p3ctx, id, false); err != nil {
		t.Fatal(err)
	}
	mustImport(t, c, "after-abort", 1)
	if _, err := s.BeginArchive(p3ctx, id, "", pend); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitArchive(p3ctx, id, p3File("frozen.unravis-case")); err != nil {
		t.Fatal(err)
	}
	try("after Archived")
	_ = h.Close()
}
