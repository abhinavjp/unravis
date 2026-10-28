package sqlite

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func TestSweepSkipsAReopenedCase(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s, _ := newTestStore(t, func(o *Options) { o.Now = func() time.Time { return now } })
	info := mustCreate(t, s, "Reopened")
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReopenCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	late := now.Add(31 * 24 * time.Hour)
	if got, err := s.Sweep(ctx, late); err != nil || len(got) != 0 {
		t.Fatalf("a reopened case was swept at 31 days: %v, %v", got, err)
	}
	if infos, _ := s.List(ctx); len(infos) != 1 || infos[0].Closed != nil {
		t.Fatalf("List = %+v", infos)
	}
	// Control: closing it again starts a fresh clock that does run out.
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Sweep(ctx, late); err != nil || len(got) != 1 || got[0] != info.ID {
		t.Fatalf("a closed case at 31 days = %v, %v", got, err)
	}
}

func TestSweepSkipsClosedCaseWithLockedOwnKey(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s, _ := ckStore(t, func(o *Options) { o.Now = func() time.Time { return now } })
	info := ckCreate(t, s, "Keyed and closed")
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.LockCaseKey(info.ID); err != nil {
		t.Fatal(err)
	}
	late := now.Add(casestore.Retention + time.Hour)
	if got, err := s.Sweep(ctx, late); err != nil || len(got) != 0 {
		t.Fatalf("a locked keyed case was swept: %v, %v", got, err)
	}
	if infos, _ := s.List(ctx); len(infos) != 1 || infos[0].ID != info.ID {
		t.Fatalf("the locked case left the list: %+v", infos)
	}
	// Unlocked, its clock shows again and it is swept.
	if err := s.UnlockCase(ctx, info.ID, []byte(ckPass)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Sweep(ctx, late); err != nil || len(got) != 1 || got[0] != info.ID {
		t.Fatalf("an unlocked closed keyed case = %v, %v", got, err)
	}
}
