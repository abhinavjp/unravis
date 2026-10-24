package store

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

func TestQueryBudgetDefaultAndOverride(t *testing.T) {
	if got := queryBudget(context.Background()); got != audit.InteractiveQueryBudget {
		t.Fatalf("default budget = %v, want %v", got, audit.InteractiveQueryBudget)
	}
	ctx := withQueryBudget(context.Background(), 3*time.Second)
	if got := queryBudget(ctx); got != 3*time.Second {
		t.Fatalf("overridden budget = %v, want 3s", got)
	}
}

func TestCommandContextParentDeadlineWins(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelParent()
	ctx, cancel := commandContext(parent)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		t.Fatalf("deadline %v (set %v) must be at most 2s away", deadline, ok)
	}

	bare, cancelBare := commandContext(context.Background())
	defer cancelBare()
	deadline, ok = bare.Deadline()
	if !ok || time.Until(deadline) > audit.InteractiveQueryBudget || time.Until(deadline) <= 0 {
		t.Fatalf("default deadline %v (set %v) must be within %v", deadline, ok, audit.InteractiveQueryBudget)
	}
}
