package storetest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func archiver(t *testing.T, s casestore.Store) casestore.Archiver {
	t.Helper()
	a, ok := s.(casestore.Archiver)
	if !ok {
		t.Fatalf("%T does not implement casestore.Archiver", s)
	}
	return a
}

func archFile(name string) casestore.ArchiveFile {
	return casestore.ArchiveFile{
		Name: name, Folder: `D:\archives`, Bytes: 1234, SHA256: sum(name),
		KeyKind: casestore.KeyKindMaster, KeyCheck: strings.Repeat("ab", 31) + "cdef", Made: t0,
		Counts: casestore.Counts{Imports: 1, Sources: 1, Events: 1},
	}
}

// archiveContract: the archive record and the moves around it, the same in both
// stores. The case file itself is the server's business; here only the record,
// the marks, the beliefs and what each state allows.
func archiveContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a := archiver(t, e.store)
	c := e.newCase("to archive")
	id := c.Info().ID
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	pend := casestore.ArchivePending{Folder: `D:\archives`, Name: "to-archive.unravis-case", Started: t0, SamplerWas: true}

	// Begin from Open: a mark and the pending facts; a second Begin is M3.
	prior, err := a.BeginArchive(e.ctx, id, casestore.StateOpen, pend)
	if err != nil || prior != casestore.StateOpen {
		t.Fatalf("BeginArchive: %v %v", prior, err)
	}
	if _, err := a.BeginArchive(e.ctx, id, "", pend); !errors.Is(err, casestore.ErrArchiveRunning) {
		t.Errorf("second BeginArchive: %v, want ErrArchiveRunning", err)
	}
	ent, err := a.Entry(e.ctx, id)
	if err != nil || casestore.StateOf(ent) != casestore.StateArchiving || ent.Archive == nil || ent.Archive.Pending == nil ||
		ent.Archive.Pending.Name != pend.Name || !ent.Archive.Pending.SamplerWas {
		t.Fatalf("Entry while archiving: %+v %v", ent, err)
	}
	// Abort: back to the state it was in, the story says so, the pending facts go.
	if err := a.AbortArchive(e.ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := a.AbortArchive(e.ctx, id, true); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("AbortArchive twice: %v, want ErrCaseChanged", err)
	}
	ent, _ = a.Entry(e.ctx, id)
	if casestore.StateOf(ent) != casestore.StateOpen || ent.Archive != nil || ent.Mark != "" {
		t.Fatalf("after abort: %+v", ent)
	}
	if h := ent.History; len(h) != 2 || h[1].Kind != casestore.HistoryArchiveStopped {
		t.Errorf("history after abort: %+v", h)
	}
	// Commit without a mark is refused: that is what a Cancel after recovery sees.
	if err := a.CommitArchive(e.ctx, id, archFile("x.unravis-case")); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("CommitArchive with no mark: %v, want ErrCaseChanged", err)
	}

	// Begin again from Closed and commit.
	if err := e.store.CloseCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	e.clock.add(time.Hour)
	if prior, err = a.BeginArchive(e.ctx, id, "", pend); err != nil || prior != casestore.StateClosed {
		t.Fatalf("BeginArchive from closed: %v %v", prior, err)
	}
	// While the mark is set: no Close, no Reopen, no Erase.
	if err := lifecycler(t, e.store).ReopenCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Reopen while archiving: %v, want ErrCaseChanged", err)
	}
	if err := a.CommitArchive(e.ctx, id, archFile("to-archive.unravis-case")); err != nil {
		t.Fatalf("CommitArchive: %v", err)
	}
	ent, _ = a.Entry(e.ctx, id)
	if casestore.StateOf(ent) != casestore.StateArchived || ent.Mark != "" || ent.Archive == nil || !ent.Archive.Active ||
		ent.Archive.Pending != nil || ent.Archive.Current() == nil || ent.Archive.Current().KeyEnding() != "cdef" ||
		ent.Archive.ClosedAtArchive == nil || ent.Archive.At == nil || ent.Archive.Freed() {
		t.Fatalf("Entry after commit: %+v", ent)
	}
	if got := ent.History[len(ent.History)-1]; got.Kind != casestore.HistoryArchived || got.Detail != "to-archive.unravis-case" {
		t.Errorf("last history entry: %+v", got)
	}
	// The listing agrees, and an archived case is not opened, closed or reopened.
	if li, ok := listed(t, e, id); !ok || casestore.StateOf(li) != casestore.StateArchived || li.Missing {
		t.Errorf("List: %+v %v", li, ok)
	}
	if _, err := e.store.Open(e.ctx, id); !errors.Is(err, casestore.ErrArchived) || !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open of an archived case: %v, want ErrArchived (a kind of ErrNotFound)", err)
	}
	if err := e.store.CloseCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Close of an archived case: %v, want ErrCaseChanged", err)
	}
	if err := lifecycler(t, e.store).ReopenCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Reopen of an archived case: %v, want ErrCaseChanged", err)
	}
	if _, err := a.BeginArchive(e.ctx, id, "", pend); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Archive of an archived case: %v, want ErrCaseChanged", err)
	}
	// Retention does not touch an archived case, however old.
	if gone, err := e.store.Sweep(e.ctx, t0.Add(10*casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("sweep of an archived case: %v %v", gone, err)
	}

	// Restore in place: always Closed, closed now, the files and the story stay.
	e.clock.add(time.Hour)
	if err := a.RestoreInPlace(e.ctx, id, casestore.StateClosed); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Restore with a wrong belief: %v, want ErrCaseChanged", err)
	}
	if err := a.RestoreInPlace(e.ctx, id, casestore.StateArchived); err != nil {
		t.Fatalf("RestoreInPlace: %v", err)
	}
	ent, _ = a.Entry(e.ctx, id)
	if casestore.StateOf(ent) != casestore.StateClosed || ent.Closed == nil || !ent.Closed.Equal(t0.Add(2*time.Hour)) ||
		ent.Archive == nil || ent.Archive.Active || len(ent.Archive.Files) != 1 {
		t.Fatalf("Entry after restore: %+v", ent)
	}
	if got := ent.History[len(ent.History)-1]; got.Kind != casestore.HistoryRestored {
		t.Errorf("last history entry: %+v", got)
	}
	if err := a.RestoreInPlace(e.ctx, id, ""); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Restore twice: %v, want ErrCaseChanged", err)
	}
	if rc, err := e.store.Open(e.ctx, id); err != nil {
		t.Errorf("Open after restore: %v", err)
	} else {
		_ = rc.Close()
	}

	// Archive again: a second file, the first stays.
	if _, err := a.BeginArchive(e.ctx, id, casestore.StateClosed, pend); err != nil {
		t.Fatal(err)
	}
	if err := a.CommitArchive(e.ctx, id, archFile("to-archive-2.unravis-case")); err != nil {
		t.Fatal(err)
	}
	ent, _ = a.Entry(e.ctx, id)
	if ent.Archive == nil || len(ent.Archive.Files) != 2 || ent.Archive.Current().Name != "to-archive-2.unravis-case" {
		t.Fatalf("files after archiving again: %+v", ent.Archive)
	}

	// Restores list, history detail.
	if err := a.AddRestoreLine(e.ctx, id, casestore.RestoreLine{At: t0, Code: "abcd1234"}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddHistory(e.ctx, id, casestore.HistoryRestoredFromCase, "abcd1234"); err != nil {
		t.Fatal(err)
	}
	ent, _ = a.Entry(e.ctx, id)
	if len(ent.Archive.Restores) != 1 || ent.Archive.Restores[0].Code != "abcd1234" {
		t.Errorf("restores: %+v", ent.Archive.Restores)
	}
	if got := ent.History[len(ent.History)-1]; got.Kind != casestore.HistoryRestoredFromCase || got.Detail != "abcd1234" {
		t.Errorf("last history entry: %+v", got)
	}
	if err := a.AddRestoreLine(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa", casestore.RestoreLine{Code: "x"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("restore line on an unknown case: %v", err)
	}
	if _, err := a.Entry(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Entry of an unknown case: %v", err)
	}
}

// freeContract: Free's two steps, who may hold a handle, and what a freed case
// keeps.
func freeContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a := archiver(t, e.store)
	c := e.newCase("to free")
	id := c.Info().ID
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	pend := casestore.ArchivePending{Folder: `D:\archives`, Name: "f.unravis-case", Started: t0}
	// Free needs an archived case.
	if err := a.BeginFree(e.ctx, id, casestore.StateArchived); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("BeginFree of an open case: %v, want ErrCaseChanged", err)
	}
	if _, err := a.BeginArchive(e.ctx, id, "", pend); err != nil {
		t.Fatal(err)
	}
	// Commit while a handle is open is allowed (the handle sees an archived case
	// only on its next call); Free is not.
	if err := a.CommitArchive(e.ctx, id, archFile("f.unravis-case")); err != nil {
		t.Fatal(err)
	}
	if err := a.BeginFree(e.ctx, id, casestore.StateArchived); !errors.Is(err, casestore.ErrCaseInUse) {
		t.Fatalf("BeginFree with a handle open: %v, want ErrCaseInUse", err)
	}
	if ent, _ := a.Entry(e.ctx, id); ent.Mark != "" {
		t.Errorf("a refused BeginFree left mark %q", ent.Mark)
	}
	_ = c.Close()
	if err := a.BeginFree(e.ctx, id, casestore.StateClosed); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("BeginFree with a wrong belief: %v, want ErrCaseChanged", err)
	}
	if err := a.BeginFree(e.ctx, id, casestore.StateArchived); err != nil {
		t.Fatalf("BeginFree: %v", err)
	}
	// While marked Freeing every other move answers M19.
	if err := a.RestoreInPlace(e.ctx, id, ""); !errors.Is(err, casestore.ErrFreeing) {
		t.Errorf("Restore while freeing: %v, want ErrFreeing", err)
	}
	if err := a.BeginFree(e.ctx, id, ""); !errors.Is(err, casestore.ErrFreeing) {
		t.Errorf("second BeginFree: %v, want ErrFreeing", err)
	}
	if gone, err := e.store.Sweep(e.ctx, t0.Add(10*casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("sweep while freeing: %v %v", gone, err)
	}
	// FinishFree is the part recovery re-runs: twice is the same as once.
	if err := a.FinishFree(e.ctx, id); err != nil {
		t.Fatalf("FinishFree: %v", err)
	}
	if err := a.FinishFree(e.ctx, id); err != nil {
		t.Errorf("FinishFree twice: %v", err)
	}
	ent, err := a.Entry(e.ctx, id)
	if err != nil || ent.Mark != "" || !ent.Archive.Freed() || ent.Archive.FreedAt == nil || ent.Missing ||
		len(ent.Archive.Files) != 1 || casestore.StateOf(ent) != casestore.StateArchived {
		t.Fatalf("Entry after free: %+v %v", ent, err)
	}
	if got := ent.History[len(ent.History)-1]; got.Kind != casestore.HistoryFreed {
		t.Errorf("last history entry: %+v", got)
	}
	if li, ok := listed(t, e, id); !ok || !li.Archive.Freed() || li.Missing {
		t.Errorf("List after free: %+v %v", li, ok)
	}
	if _, err := e.store.Open(e.ctx, id); !errors.Is(err, casestore.ErrArchived) {
		t.Errorf("Open of a freed case: %v, want ErrArchived", err)
	}
	// A freed case cannot be restored in place or freed again; the file is the
	// only copy now.
	if err := a.RestoreInPlace(e.ctx, id, casestore.StateArchived); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Restore in place of a freed case: %v, want ErrCaseChanged", err)
	}
	if err := a.BeginFree(e.ctx, id, casestore.StateArchived); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Free of a freed case: %v, want ErrCaseChanged", err)
	}
	if gone, err := e.store.Sweep(e.ctx, t0.Add(10*casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("sweep of a freed case: %v %v", gone, err)
	}
	// Erase removes the record, which is the only thing that does.
	if err := e.store.Erase(e.ctx, id); err != nil {
		t.Fatalf("Erase of a freed case: %v", err)
	}
	if _, err := a.Entry(e.ctx, id); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Entry after erase: %v, want ErrNotFound", err)
	}
	if _, ok := listed(t, e, id); ok {
		t.Error("an erased freed case is still listed")
	}
}

// unresolvedContract: the list of finished files an archive left behind.
func unresolvedContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a := archiver(t, e.store)
	if l, err := a.Unresolved(e.ctx); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	u1 := casestore.UnresolvedFile{Name: "a.unravis-case", Folder: `D:\one`, CaseID: "aaaaaaaaaaaaaaaaaaaaaaaaaa", At: t0}
	u2 := casestore.UnresolvedFile{Name: "b.unravis-case", Folder: `D:\one`, At: t0.Add(time.Minute)}
	for _, u := range []casestore.UnresolvedFile{u1, u2, u1} { // the repeat is not added twice
		if err := a.AddUnresolved(e.ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	l, err := a.Unresolved(e.ctx)
	if err != nil || len(l) != 2 || l[0].Name != "a.unravis-case" || l[1].Name != "b.unravis-case" || l[0].CaseID != u1.CaseID {
		t.Fatalf("list: %+v %v", l, err)
	}
	if err := a.DropUnresolved(e.ctx, "a.unravis-case", `D:\one`); err != nil {
		t.Fatal(err)
	}
	if err := a.DropUnresolved(e.ctx, "nothing", `D:\one`); err != nil {
		t.Errorf("dropping an absent entry: %v", err)
	}
	if l, _ = a.Unresolved(e.ctx); len(l) != 1 || l[0].Name != "b.unravis-case" {
		t.Errorf("list after drop: %+v", l)
	}
}
