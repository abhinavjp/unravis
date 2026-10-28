package audit

import (
	"errors"
	"strings"
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

func TestActivityQueryMessagesAreReadable(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	_, err := ActivityQuery(ActivityInput{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(25 * time.Hour), Limit: 10})
	if err == nil || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "at most 24 hours") || strings.Contains(err.Error(), "at most24hours") {
		t.Fatalf("window error = %v", err)
	}
	for _, in := range []ActivityInput{
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 0},
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 101},
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 10, Cursor: strings.Repeat("x", 4097)},
	} {
		_, err := ActivityQuery(in)
		if err == nil || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "limit must be 1 to 100") || !strings.Contains(err.Error(), "4096 bytes") {
			t.Fatalf("page error for %+v = %v", in, err)
		}
	}
}
