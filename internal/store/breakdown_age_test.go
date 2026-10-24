package store

import (
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// An incomplete-age range is measured against a clock Breakdown must set itself,
// as Analyze does. Without it the age of every row is unknown and the range
// selects nothing, so a drill into an incomplete-age row listed no breakdown.
func TestBreakdownAppliesAnIncompleteAgeRangeAgainstTheStoreClock(t *testing.T) {
	now := bdNow()
	cases := bdCases(now)
	var want int64
	for _, c := range cases {
		if c.state == "incomplete" {
			want++
		}
	}
	if want == 0 {
		t.Fatal("the fixture needs unfinished rows")
	}
	// The pipeline simulator has no age operator, so only the legacy reader, which
	// filters rows in Go against q.Now, can run this end to end.
	h := bdStore(t, true, bdDocs(cases))
	h.store.now = func() time.Time { return now }
	q := bdQuery(now)
	q.IncompleteAgeMs = rgRange(rgF(60_000), rgF(600_000))
	got := bdBreakdown(t, h, q, audit.BreakdownUserAgent, 5)
	if got.Total != want || len(got.Rows) == 0 {
		t.Fatalf("total %d rows %d, want %d unfinished rows aged 1 to 10 minutes", got.Total, len(got.Rows), want)
	}
}
