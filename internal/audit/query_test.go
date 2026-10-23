package audit

import (
	"math"
	"testing"
	"time"
)

func validQuery() Query {
	currentStart := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return Query{
		SourceIDs: []string{"source-a"},
		Start:     currentStart, End: currentStart.Add(15 * time.Minute),
		BaselineStart: currentStart.Add(-15 * time.Minute), BaselineEnd: currentStart,
		Kind: "synchronous", Outcome: "all", Metric: "total", BucketSeconds: 10,
	}
}

func TestValidateQueryRejectsInvalidSourceSelection(t *testing.T) {
	cases := map[string]func(*Query){
		"none":      func(q *Query) { q.SourceIDs = nil },
		"blank":     func(q *Query) { q.SourceIDs = []string{" "} },
		"duplicate": func(q *Query) { q.SourceIDs = []string{"x", "x"} },
		"too many": func(q *Query) {
			q.SourceIDs = make([]string, MaxSources+1)
			for i := range q.SourceIDs {
				q.SourceIDs[i] = string(rune('a' + i))
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsInvalidIntervals(t *testing.T) {
	cases := map[string]func(*Query){
		"zero current":      func(q *Query) { q.End = q.Start },
		"reversed current":  func(q *Query) { q.End = q.Start.Add(-time.Second) },
		"zero baseline":     func(q *Query) { q.BaselineEnd = q.BaselineStart },
		"overlap":           func(q *Query) { q.BaselineStart = q.Start.Add(-time.Second); q.BaselineEnd = q.Start.Add(time.Second) },
		"current too long":  func(q *Query) { q.End = q.Start.Add(7*24*time.Hour + time.Second) },
		"baseline too long": func(q *Query) { q.BaselineStart = q.BaselineEnd.Add(-7*24*time.Hour - time.Second) },
		"zero time":         func(q *Query) { q.Start = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsInvalidEnumsAndBuckets(t *testing.T) {
	cases := map[string]func(*Query){
		"kind": func(q *Query) { q.Kind = "unknown" }, "empty kind": func(q *Query) { q.Kind = "" },
		"outcome": func(q *Query) { q.Outcome = "maybe" }, "metric": func(q *Query) { q.Metric = "average" },
		"seconds low": func(q *Query) { q.BucketSeconds = 9 }, "seconds high": func(q *Query) { q.BucketSeconds = 86401 },
		"too many buckets": func(q *Query) {
			q.BucketSeconds = 10
			q.Start = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
			q.End = q.Start.Add(1201 * time.Second)
			q.BaselineEnd = q.Start
			q.BaselineStart = q.BaselineEnd.Add(-10 * time.Minute)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := validQuery()
			mutate(&q)
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryRejectsLongControllerAndAction(t *testing.T) {
	for _, field := range []string{"controller", "action"} {
		t.Run(field, func(t *testing.T) {
			q := validQuery()
			if field == "controller" {
				q.Controller = string(make([]byte, 201))
				for i := range q.Controller {
					q.Controller = q.Controller[:i] + "x" + q.Controller[i+1:]
				}
			} else {
				q.Action = string(make([]byte, 201))
				for i := range q.Action {
					q.Action = q.Action[:i] + "x" + q.Action[i+1:]
				}
			}
			if err := ValidateQuery(q); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateQueryAllowsAdjacentWindowsAndUTCOffsets(t *testing.T) {
	q := validQuery()
	loc := time.FixedZone("UTC+4", 4*60*60)
	q.Start = time.Date(2026, 10, 22, 16, 0, 0, 0, loc)
	q.End = q.Start.Add(15 * time.Minute)
	q.BaselineEnd = q.Start
	q.BaselineStart = q.BaselineEnd.Add(-15 * time.Minute)
	before := q
	if err := ValidateQuery(q); err != nil {
		t.Fatalf("adjacent windows should be legal: %v", err)
	}
	if !q.Start.Equal(before.Start) || q.Start.Location() != before.Start.Location() {
		t.Fatal("ValidateQuery mutated input")
	}
}

func TestValidateQueryCountsEpochBucketsIntersectingHalfOpenWindows(t *testing.T) {
	t.Run("rejects 121 current buckets from unaligned endpoints", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 0, 5, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected 121 intersecting buckets to be rejected")
		}
	})
	t.Run("rejects 121 baseline buckets", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 20, 0, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineStart = time.Date(2026, 10, 22, 11, 39, 59, 0, time.UTC)
		q.BaselineEnd = time.Date(2026, 10, 22, 11, 59, 59, 0, time.UTC)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected 121 intersecting baseline buckets to be rejected")
		}
	})
	t.Run("rejects a subsecond endpoint that intersects bucket 121", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(2026, 10, 22, 12, 0, 0, 1, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err == nil {
			t.Fatal("expected subsecond endpoint bucket count to exceed 120")
		}
	})
	t.Run("accepts exactly 120 aligned buckets", func(t *testing.T) {
		q := validQuery()
		q.BucketSeconds = 10
		q.Start = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
		q.End = q.Start.Add(20 * time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-20 * time.Minute)
		if err := ValidateQuery(q); err != nil {
			t.Fatalf("aligned 120-bucket windows should be accepted: %v", err)
		}
	})
	t.Run("accepts arbitrary RFC3339 years without UnixNano arithmetic", func(t *testing.T) {
		q := validQuery()
		q.Start = time.Date(9999, 10, 22, 12, 0, 0, 0, time.UTC)
		q.End = q.Start.Add(time.Minute)
		q.BaselineEnd = q.Start
		q.BaselineStart = q.BaselineEnd.Add(-time.Minute)
		if err := ValidateQuery(q); err != nil {
			t.Fatalf("short intervals in year 9999 should be accepted: %v", err)
		}
	})
}

func TestValidateQueryRejectsNonFiniteTimesNotRelevant(t *testing.T) {
	_ = math.NaN() // Keeps this test file's standard-library edge-case imports available for histogram tests.
}
