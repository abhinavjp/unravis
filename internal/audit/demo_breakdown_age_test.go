package audit

import (
	"context"
	"testing"
	"time"
)

// An incomplete-age range is measured against the demo clock in the breakdown as
// it is in the analysis and the request list; without it the range selects nothing.
func TestDemoBreakdownAppliesAnIncompleteAgeRange(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemoWithStaleWork(now)
	q := demoQuery(now)
	min, max := 5*60_000.0, 20*60_000.0
	q.IncompleteAgeMs = &Range{Min: &min, Max: &max}
	page, err := d.Requests(context.Background(), PageInput{Query: q, Limit: MaxPageSize})
	if err != nil || len(page.Rows) == 0 {
		t.Fatalf("the request list finds no unfinished rows to drill into: rows=%d err=%v", len(page.Rows), err)
	}
	reader, ok := d.(BreakdownReader)
	if !ok {
		t.Fatal("demo repository must implement BreakdownReader")
	}
	got, err := reader.Breakdown(context.Background(), BreakdownInput{Query: q, Dim: BreakdownStatusCode, Top: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != int64(len(page.Rows)) || len(got.Rows) == 0 {
		t.Fatalf("breakdown total %d rows %d, want the %d rows the list shows", got.Total, len(got.Rows), len(page.Rows))
	}
}
