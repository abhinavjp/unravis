package audit

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func ValidateQuery(q Query) error {
	if len(q.SourceIDs) == 0 || len(q.SourceIDs) > MaxSources {
		return invalidInputf("sourceIds must contain 1 to %d sources", MaxSources)
	}
	seen := make(map[string]struct{}, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		if strings.TrimSpace(id) == "" {
			return invalidInputf("sourceIds must not contain blank values")
		}
		if _, ok := seen[id]; ok {
			return invalidInputf("sourceIds must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	if q.Start.IsZero() || q.End.IsZero() || q.BaselineStart.IsZero() || q.BaselineEnd.IsZero() {
		return invalidInputf("all interval times are required")
	}
	if err := validateInterval(q.Start, q.End, "current"); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := validateInterval(q.BaselineStart, q.BaselineEnd, "baseline"); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if q.BaselineEnd.After(q.Start) {
		return invalidInputf("baseline interval must end at or before current start")
	}
	if q.BucketSeconds < 10 || q.BucketSeconds > 86400 {
		return invalidInputf("bucketSeconds must be between 10 and 86400")
	}
	if epochBucketCount(q.Start, q.End, q.BucketSeconds) > MaxTimeBuckets || epochBucketCount(q.BaselineStart, q.BaselineEnd, q.BucketSeconds) > MaxTimeBuckets {
		return invalidInputf("each interval may contain at most %d time buckets", MaxTimeBuckets)
	}
	switch q.Kind {
	case "synchronous", "asynchronous", "hangfire", "all":
	default:
		return invalidInputf("kind must be synchronous, asynchronous, hangfire, or all")
	}
	switch q.Outcome {
	case "success", "failure", "all":
	default:
		return invalidInputf("outcome must be success, failure, or all")
	}
	switch q.Metric {
	case "total", "api":
	default:
		return invalidInputf("metric must be total or api")
	}
	if utf8.RuneCountInString(q.Controller) > MaxFilterLength {
		return invalidInputf("controller must be at most %d characters", MaxFilterLength)
	}
	if utf8.RuneCountInString(q.Action) > MaxFilterLength {
		return invalidInputf("action must be at most %d characters", MaxFilterLength)
	}
	return nil
}

func validateInterval(start, end time.Time, name string) error {
	if !end.After(start) {
		return fmt.Errorf("%s interval end must be after start", name)
	}
	d := end.Sub(start)
	if d > MaxInterval {
		return fmt.Errorf("%s interval must not exceed 7 days", name)
	}
	return nil
}

// epochBucketCount returns how many Unix-epoch-aligned buckets intersect the
// half-open interval [start, end). Unix seconds stay in int64 range for all
// RFC3339 years, unlike UnixNano, which overflows outside 1678–2262.
func epochBucketCount(start, end time.Time, seconds int) int64 {
	unit := int64(seconds)
	first := floorDiv(start.Unix(), unit)
	last := floorDiv(end.Unix(), unit)
	count := last - first
	if end.Unix()%unit != 0 || end.Nanosecond() != 0 {
		count++
	}
	return count
}

func floorDiv(value, divisor int64) int64 {
	quotient, remainder := value/divisor, value%divisor
	if remainder < 0 {
		quotient--
	}
	return quotient
}

func invalidInputf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, args...)...)
}
