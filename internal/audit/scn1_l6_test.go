package audit

import (
	"errors"
	"testing"
	"time"
)

// The web app derives the slowest-calls bucket size from the window (frame.ts slowBucketSeconds).
// This pins the two sides together: the derived size is accepted for 7 days and 60 s over a long window is not.
func TestSlowestCallsBucketSizeFitsTheBucketCap(t *testing.T) {
	week := 7 * 24 * time.Hour
	q := analysisQuery()
	q.Start, q.End = anchor, anchor.Add(week)
	q.BaselineStart, q.BaselineEnd = anchor.Add(-week), anchor

	q.BucketSeconds = 60
	if err := ValidateQuery(q); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("60 s over 7 days should be refused, got %v", err)
	}
	q.BucketSeconds = 5100 // slowBucketSeconds for a 7 day window
	if err := ValidateQuery(q); err != nil {
		t.Fatalf("5100 s over 7 days should be accepted, got %v", err)
	}
	if got := epochBucketCount(q.Start, q.End, q.BucketSeconds); got > MaxTimeBuckets {
		t.Fatalf("buckets = %d, cap %d", got, MaxTimeBuckets)
	}

	// 150 minutes (the MAN-04 repro): 60 s is refused, 120 s is accepted, at an unaligned start too.
	start := time.Date(2026, 10, 20, 5, 37, 0, 0, time.UTC)
	q.Start, q.End = start, start.Add(150*time.Minute)
	q.BaselineStart, q.BaselineEnd = start.Add(-150*time.Minute), start
	q.BucketSeconds = 60
	if err := ValidateQuery(q); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("60 s over 150 minutes should be refused, got %v", err)
	}
	q.BucketSeconds = 120
	if err := ValidateQuery(q); err != nil {
		t.Fatalf("120 s over 150 minutes should be accepted, got %v", err)
	}
}
