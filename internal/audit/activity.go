package audit

import (
	"context"
	"fmt"
	"strings"
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
	// UserID keeps only calls made by this user. Nil means everyone.
	UserID *int `json:"userId,omitempty"`
	// Operation keeps only calls to one API, written "Controller.Action". Empty means every API.
	Operation string `json:"operation,omitempty"`
}
type ActivityReader interface {
	Activity(context.Context, ActivityInput) (Page, error)
}

func ActivityQuery(input ActivityInput) (Query, error) {
	if input.End.Sub(input.Start) > 24*time.Hour {
		return Query{}, fmt.Errorf("%w: activity window must be at most 24 hours", ErrInvalidInput)
	}
	if input.Limit < 1 || input.Limit > MaxPageSize || len(input.Cursor) > 4096 {
		return Query{}, fmt.Errorf("%w: invalid activity page: limit must be 1 to 100 and the cursor at most 4096 bytes", ErrInvalidInput)
	}
	if input.UserID != nil && *input.UserID < 0 {
		return Query{}, invalidInputf("userId must be 0 or more")
	}
	if input.Operation != "" && !validOperation(input.Operation) {
		return Query{}, invalidInputf("operation must be written Controller.Action using letters, digits and underscores")
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

// validOperation accepts exactly "Controller.Action": one dot, each side 1 to
// 100 characters of letters, digits and underscores. Stricter than Query's
// Operation rule because the live reader builds a database match from it.
func validOperation(s string) bool {
	controller, action, ok := strings.Cut(s, ".")
	if !ok {
		return false
	}
	for _, part := range [2]string{controller, action} {
		if len(part) < 1 || len(part) > 100 {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
	}
	return true
}
