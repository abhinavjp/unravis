package server

import (
	"context"
	"net/http"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
)

// F6 of the P3 review: Restore as new makes the new case Closed from the moment it
// exists, so a crash anywhere inside the restore never leaves an Open case.

// openSpy records whether each case it opens is already Closed.
type openSpy struct {
	*sqlite.Store
	closedAtOpen []bool
}

func (s *openSpy) Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error) {
	c, err := s.Store.Open(ctx, id)
	if err == nil {
		s.closedAtOpen = append(s.closedAtOpen, c.Info().Closed != nil)
	}
	return c, err
}

func TestF6_RestoreAsNewIsClosedFromTheStart(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("closed from the start", 3)
	a.verifyDone(id)
	spy := &openSpy{Store: a.store}
	a.handler().cases = spy
	before := len(spy.closedAtOpen)

	res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived")
	if res.Code != http.StatusCreated {
		t.Fatalf("restore as new = %d %s", res.Code, res.Body)
	}
	var dto caseDTO
	decodeBody(t, res, &dto)
	if dto.State != "closed" {
		t.Fatalf("restored = %+v", dto)
	}
	// The restore's own opens of the new case (before the extras that follow it)
	// found it Closed already; the first open of it is the restore's check.
	if len(spy.closedAtOpen) <= before {
		t.Fatal("the new case was never opened")
	}
	for i, closed := range spy.closedAtOpen[before:] {
		if !closed {
			t.Fatalf("open %d of the new case found it Open", i)
		}
	}
}

// A case that cannot be closed after it was created is erased, not left Open.
func TestF6_CreateThatCannotCloseLeavesNothing(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	failing := &failClose{Store: a.store}
	adapter := restoreAdapter{Store: failing, root: a.root}
	if _, err := adapter.Create(context.Background(), "doomed"); err == nil {
		t.Fatal("Create succeeded though the case could not be closed")
	}
	all, err := a.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range all {
		if i.Title == "doomed" {
			t.Fatalf("an Open case was left behind: %+v", i)
		}
	}
}

type failClose struct{ *sqlite.Store }

func (f *failClose) CloseCase(context.Context, casestore.CaseID) error {
	return casestore.ErrCaseChanged
}

// F6b: the count check compares zero counts too. A record whose counts were left at
// zero for a file that holds events does not pass the check.
func TestF6_ZeroCountsInTheRecordFailTheCheck(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	archiveCountFn = func(context.Context, string, []byte) (casestore.Counts, error) {
		return casestore.Counts{}, nil
	}
	defer func() { archiveCountFn = casestore.CountCaseDB }()
	id, _, _ := a.archivedCase("zero counts", 4)
	a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived")
	if j := a.wait(id, "verify"); j.State != fileJobFailed || j.Reason != "check-failed" {
		t.Fatalf("verify = %+v", j)
	}
	archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
	if !a.dbExists(id) {
		t.Fatal("the working copy was erased")
	}
}

// F6c: a case whose size reads as nothing still needs room for the file.
func TestF6_TinyCaseStillNeedsRoom(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("tiny", 1)
	archiveFreeFn = func(string) (uint64, error) { return archiveMinRoom - 1, nil }
	defer func() { archiveFreeFn = archiveFreeBytes }()
	archReason(t, a.archiveReq(id, a.folder(), "open"), http.StatusInsufficientStorage, "no-space", archiveNoSpaceText)
	archiveFreeFn = func(string) (uint64, error) { return archiveMinRoom, nil }
	a.archiveDone(id, a.folder(), "open")
}
