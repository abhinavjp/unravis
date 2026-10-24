package timeline

import (
	"context"
	"errors"
	"testing"

	"api-audit-explorer/internal/importer"
)

// A cancelled or expired context stops Build with the context error instead of
// reading on. Every file here is synthetic: web-a, sql-a, 2001 dates.
func TestBuildStopsOnCancelledContext(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 10, dur: 50}))
	load(t, c, sql(ev{kind: "lock-wait", start: 10, dur: 100}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Build(ctx, c, importer.Window{}, oneInterval)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
