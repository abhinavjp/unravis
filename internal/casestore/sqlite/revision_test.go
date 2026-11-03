package sqlite

import (
	"context"
	"errors"
	"testing"
)

// mustBump commits one transaction that calls bumpCaseRevision n times.
func mustBump(t *testing.T, c *Case, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range n {
		if err := bumpCaseRevision(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustRevision(t *testing.T, c *Case) int64 {
	t.Helper()
	rev, err := c.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestRevisionDefaultsToZero(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Fresh")
	c := mustOpen(t, s, info.ID)
	if rev := mustRevision(t, c); rev != 0 {
		t.Errorf("revision of a new case = %d, want 0", rev)
	}
	if n := scalar(t, c, "SELECT COUNT(*) FROM meta WHERE k='case.revision'"); n != 0 {
		t.Errorf("case.revision rows in a new case = %d, want 0", n)
	}
}

func TestRevisionBumpIsMonotonic(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Bumps")
	c := mustOpen(t, s, info.ID)
	for want := int64(1); want <= 3; want++ {
		mustBump(t, c, 1)
		if rev := mustRevision(t, c); rev != want {
			t.Fatalf("revision = %d, want %d", rev, want)
		}
	}
	// Two bumps inside one transaction raise it by two.
	mustBump(t, c, 2)
	if rev := mustRevision(t, c); rev != 5 {
		t.Fatalf("revision after two bumps in one transaction = %d, want 5", rev)
	}
}

func TestRevisionBumpRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Rollback")
	c := mustOpen(t, s, info.ID)
	mustBump(t, c, 1)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bumpCaseRevision(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if rev := mustRevision(t, c); rev != 1 {
		t.Errorf("revision after rollback = %d, want 1", rev)
	}
}

func TestRevisionRowIsPlainMetaKey(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Meta row")
	c := mustOpen(t, s, info.ID)
	mustBump(t, c, 1)
	var v string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='case.revision'").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "1" {
		t.Errorf("case.revision = %q, want the decimal text \"1\"", v)
	}
	if v := schemaOf(t, c); v != "4" {
		t.Errorf("schema_version = %q, want 4 (unchanged)", v)
	}
}

func TestRevisionSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Reopen")
	c := mustOpen(t, s, info.ID)
	mustBump(t, c, 2)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	c = reopened.(*Case)
	defer func() { _ = c.Close() }()
	if rev := mustRevision(t, c); rev != 2 {
		t.Errorf("revision after reopen = %d, want 2", rev)
	}
}

func TestRevisionOnClosedHandleErrors(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Closed")
	c := mustOpen(t, s, info.ID)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Revision(context.Background()); err == nil {
		t.Fatal("Revision on a closed handle returned no error")
	} else if !errors.Is(err, ErrClosed) {
		t.Errorf("Revision on a closed handle = %v, want ErrClosed", err)
	}
}
