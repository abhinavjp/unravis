package store

import (
	"context"
	"time"

	"api-audit-explorer/internal/audit"
)

type budgetKey struct{}

// withQueryBudget returns a context that carries the query budget d.
func withQueryBudget(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, budgetKey{}, d)
}

// queryBudget returns the budget carried by ctx, or audit.InteractiveQueryBudget
// when none is set.
func queryBudget(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(budgetKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return audit.InteractiveQueryBudget
}

// commandContext bounds a database command by the query budget. An earlier
// deadline already on ctx wins, because context.WithTimeout keeps the sooner one.
func commandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, queryBudget(ctx))
}
