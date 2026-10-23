package audit

import (
	"context"
	"fmt"
	"time"
)

// ActivityInput selects recorded requests overlapping one absolute context window.
// No baseline is calculated for this event-level view.
type ActivityInput struct {
	SourceIDs []string  `json:"sourceIds"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Cursor    string    `json:"cursor"`
	Limit     int       `json:"limit"`
}
type ActivityReader interface {
	Activity(context.Context, ActivityInput) (Page, error)
}

func ActivityQuery(input ActivityInput) (Query, error) {
	if input.End.Sub(input.Start) > 24*time.Hour {
		return Query{}, fmt.Errorf("%w: activity window must be at most24hours", ErrInvalidInput)
	}
	if input.Limit < 1 || input.Limit > MaxPageSize || len(input.Cursor) > 4096 {
		return Query{}, fmt.Errorf("%w: invalid activity page", ErrInvalidInput)
	}
	// Requests' common validator requires a preceding reference; it is an internal
	// pagination adapter only. Activity performs no reference query or comparison.
	q := Query{SourceIDs: input.SourceIDs, Start: input.Start, End: input.End, BaselineStart: input.Start.Add(-input.End.Sub(input.Start)), BaselineEnd: input.Start, Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 86400}
	if err := ValidateQuery(q); err != nil {
		return Query{}, err
	}
	return q, nil
}
func ActivityOverlaps(start, completed *time.Time, windowStart, windowEnd time.Time) bool {
	if start == nil || !start.Before(windowEnd) {
		return false
	}
	if !start.Before(windowStart) {
		return true
	}
	return completed != nil && completed.After(windowStart) && !completed.Before(*start)
}
