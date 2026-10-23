package audit

import (
	"testing"
	"time"
)

func TestActivityIncludesKnownOverlapsWithoutInventingUnknownSpans(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	before := start.Add(-time.Hour)
	during := start.Add(time.Second)
	exact := start
	negative := before.Add(-time.Second)
	for _, tc := range []struct {
		name     string
		at, done *time.Time
		want     bool
	}{{"already_running", &before, &during, true}, {"finished_at_boundary", &before, &exact, false}, {"unknown_old", &before, nil, false}, {"unknown_inside", &during, nil, true}, {"negative_span", &before, &negative, false}, {"after_window", &end, nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActivityOverlaps(tc.at, tc.done, start, end); got != tc.want {
				t.Fatalf("overlap=%v want%v", got, tc.want)
			}
		})
	}
	if _, err := ActivityQuery(ActivityInput{SourceIDs: []string{"s"}, Start: start, End: start.Add(25 * time.Hour), Limit: 100}); err == nil {
		t.Fatal("unbounded window accepted")
	}
}
