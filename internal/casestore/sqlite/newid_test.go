package sqlite

import (
	"context"
	"errors"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func TestNewIDOptionGivesANewCaseItsFixedID(t *testing.T) {
	const fixed = casestore.CaseID("fixedidaaaaaaaaaaaaaaaaaaa")
	s, _ := newTestStore(t, func(o *Options) {
		o.NewID = func() (casestore.CaseID, error) { return fixed, nil }
	})
	info := mustCreate(t, s, "A fixed case")
	if info.ID != fixed {
		t.Fatalf("id = %q, want %q", info.ID, fixed)
	}
	// The same ID again is refused rather than overwriting the first case.
	if _, err := s.Create(context.Background(), "Another"); err == nil {
		t.Fatal("a second case with the same id was created")
	}
	if list, err := s.List(context.Background()); err != nil || len(list) != 1 || list[0].Title != "A fixed case" {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestNewIDOptionIsCheckedLikeAnyOtherID(t *testing.T) {
	s, root := newTestStore(t, func(o *Options) {
		o.NewID = func() (casestore.CaseID, error) { return "../escape", nil }
	})
	if _, err := s.Create(context.Background(), "Bad id"); !errors.Is(err, casestore.ErrBadID) {
		t.Fatalf("err = %v, want ErrBadID", err)
	}
	_ = root
}
