package audit

import (
	"math"
)

// Range filters bound one numeric field of a record, inclusively. They are shared
// by the summary, the request lists, the slowest list and the demo repository.
// The server pipelines compute the fields from raw values; the MongoDB 3.4
// adapter and the demo repository apply the same bounds in Go through
// RangeFilterMatches.
//
// Semantics every implementation keeps:
//   - Both bounds are inclusive and either may be omitted. A range with neither
//     bound is not a filter.
//   - A record that lacks the field never matches a bounded range, even a
//     max-only one. A missing value is never read as 0.
//   - Latency ranges (ObservedMs, ApiTimeMs, AuditCreateTimeMs) only describe
//     completed records, so they imply the "complete" state. IncompleteAgeMs only
//     describes records that have not completed, so it implies "incomplete".
//     Payload sizes (RequestKB, ResponseKB) imply no state.

// Range is an inclusive numeric interval. Nil bounds are open.
type Range struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

const (
	// MaxRangeMs bounds a millisecond range: about 11.5 days, far beyond any
	// request the audit records.
	MaxRangeMs = 1e9
	// MaxRangeKB bounds a payload size range: about 10 GB.
	MaxRangeKB = 1e7
)

// bounded reports whether r constrains anything.
func (r *Range) bounded() bool {
	return r != nil && (r.Min != nil || r.Max != nil)
}

// contains reports whether v is a present, non-negative, finite number inside r.
func (r *Range) contains(v *float64) bool {
	if !r.bounded() {
		return true
	}
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return false
	}
	if r.Min != nil && *v < *r.Min {
		return false
	}
	return r.Max == nil || *v <= *r.Max
}

// HasLatencyRange reports whether a bounded latency range is set.
func (q Query) HasLatencyRange() bool {
	return q.ObservedMs.bounded() || q.ApiTimeMs.bounded() || q.AuditCreateTimeMs.bounded()
}

// HasAgeRange reports whether a bounded incomplete-age range is set.
func (q Query) HasAgeRange() bool { return q.IncompleteAgeMs.bounded() }

// HasRanges reports whether any range constrains records.
func (q Query) HasRanges() bool {
	return q.HasLatencyRange() || q.HasAgeRange() || q.RequestKB.bounded() || q.ResponseKB.bounded()
}

// EffectiveCompletion is the completion filter every pipeline and reader applies:
// the requested Completion, or the state a bounded latency (complete) or age
// (incomplete) range implies. "all" is the same as no filter and returns "".
// ValidateQuery has already rejected a Completion that contradicts a range.
func EffectiveCompletion(q Query) string {
	completion := q.Completion
	if completion == StateAll {
		completion = ""
	}
	if completion != "" {
		return completion
	}
	switch {
	case q.HasLatencyRange():
		return StateComplete
	case q.HasAgeRange():
		return StateIncomplete
	}
	return ""
}

// RangeFacts are the values a range filter reads from one record. A nil value is
// a field the record does not carry.
type RangeFacts struct {
	ObservedMs        *float64
	ApiTimeMs         *float64
	AuditCreateTimeMs *float64
	RequestKB         *float64
	ResponseKB        *float64
	// AgeMs is the server clock minus the request start (or CreatedOn), for a
	// record that has not completed.
	AgeMs *float64
}

// RangeFilterMatches applies every bounded range of q to one record. The implied
// completion is applied separately, through EffectiveCompletion and StateMatches.
func RangeFilterMatches(q Query, f RangeFacts) bool {
	return q.ObservedMs.contains(f.ObservedMs) &&
		q.ApiTimeMs.contains(f.ApiTimeMs) &&
		q.AuditCreateTimeMs.contains(f.AuditCreateTimeMs) &&
		q.RequestKB.contains(f.RequestKB) &&
		q.ResponseKB.contains(f.ResponseKB) &&
		q.IncompleteAgeMs.contains(f.AgeMs)
}

// EffectiveSlowestMin is the minimum metric value the slowest list applies: the
// larger of the explicit minimum and the range minimum for the ranking metric
// (the observed minimum for total, the API-time minimum for api). It is nil when
// neither is set. The cursor fingerprint stays bound to the explicit minimum and
// the ranges themselves, not to this derived value.
func EffectiveSlowestMin(q Query, min *float64) *float64 {
	var floor *float64
	if min != nil {
		v := *min
		floor = &v
	}
	r := q.ObservedMs
	if q.Metric == "api" {
		r = q.ApiTimeMs
	}
	if r != nil && r.Min != nil && (floor == nil || *r.Min > *floor) {
		v := *r.Min
		floor = &v
	}
	return floor
}

// validateRanges bounds every range and rejects a Completion that contradicts the
// state a range implies.
func validateRanges(q Query) error {
	for _, entry := range []struct {
		name  string
		r     *Range
		limit float64
	}{
		{"observedMs", q.ObservedMs, MaxRangeMs},
		{"apiTimeMs", q.ApiTimeMs, MaxRangeMs},
		{"auditCreateTimeMs", q.AuditCreateTimeMs, MaxRangeMs},
		{"incompleteAgeMs", q.IncompleteAgeMs, MaxRangeMs},
		{"requestKb", q.RequestKB, MaxRangeKB},
		{"responseKb", q.ResponseKB, MaxRangeKB},
	} {
		if entry.r == nil {
			continue
		}
		for _, bound := range []*float64{entry.r.Min, entry.r.Max} {
			if bound == nil {
				continue
			}
			if math.IsNaN(*bound) || math.IsInf(*bound, 0) {
				return invalidInputf("%s bounds must be finite numbers", entry.name)
			}
			if *bound < 0 {
				return invalidInputf("%s bounds must not be negative", entry.name)
			}
			if *bound > entry.limit {
				return invalidInputf("%s bounds must be at most %g", entry.name, entry.limit)
			}
		}
		if entry.r.Min != nil && entry.r.Max != nil && *entry.r.Min > *entry.r.Max {
			return invalidInputf("%s minimum must not exceed its maximum", entry.name)
		}
	}
	if q.HasLatencyRange() && q.HasAgeRange() {
		return invalidInputf("a latency range describes completed records and an age range incomplete ones, so they cannot be combined")
	}
	completion := q.Completion
	if q.HasLatencyRange() && completion != "" && completion != StateAll && completion != StateComplete {
		return invalidInputf("latency ranges apply to complete records; completion must be complete or all")
	}
	if q.HasAgeRange() && completion != "" && completion != StateAll && completion != StateIncomplete && completion != StatePending {
		return invalidInputf("incompleteAgeMs applies to incomplete records; completion must be incomplete, pending or all")
	}
	return nil
}
