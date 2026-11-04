package sqlite

import (
	"context"
	"errors"
	"testing"
)

// A cancelled context during the open-time check is reported as the context's
// error, never as a damaged case.
func TestVerifyCancelledContextIsNotDamaged(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "ctx case")
	c := mustOpen(t, s, info.ID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.verify(ctx)
	if errors.Is(err, ErrDamaged) {
		t.Fatalf("verify with a cancelled context reported damage: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verify with a cancelled context = %v, want context.Canceled", err)
	}
}
// The helper verify uses when a metadata query fails: a live context means the
// case is damaged, a done context means the caller gave up.
func TestDamagedUnlessCancelled(t *testing.T) {
	if err := damagedUnlessCancelled(context.Background()); !errors.Is(err, ErrDamaged) {
		t.Fatalf("live context = %v, want ErrDamaged", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := damagedUnlessCancelled(ctx)
	if errors.Is(err, ErrDamaged) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %v, want context.Canceled and not ErrDamaged", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 0)
	defer cancel()
	if err := damagedUnlessCancelled(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired context = %v, want context.DeadlineExceeded", err)
	}
}