package audit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

type cohortAccumulator struct {
	sourceID, controller, action, kind, outcome string
	key                                         string
	currentEvents, referenceEvents              int64
	current, reference                          *histogramAccumulator
	currentAt                                   map[int64]*histogramAccumulator

	currentClientErrors, currentServerErrors     int64
	referenceClientErrors, referenceServerErrors int64
}

func CohortKey(sourceID, controller, action, kind, outcome string) string {
	encoded, _ := json.Marshal([]string{sourceID, controller, action, kind, outcome})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func BuildAnalysis(q Query, current, reference []Bucket, sources []SourceResult) (Analysis, error) {
	if err := ValidateQuery(q); err != nil {
		return Analysis{}, err
	}
	if len(current) > MaxSummaryRows || len(reference) > MaxSummaryRows || len(current)+len(reference) > MaxSummaryRows {
		return Analysis{}, fmt.Errorf("%w: %w", ErrLimit, ErrSummaryRowLimit)
	}
	cohorts := make(map[string]*cohortAccumulator)
	var quality Quality
	for _, b := range current {
		if err := addQuality(&quality, b); err != nil {
			return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	for _, bucket := range current {
		if err := addBucket(cohorts, bucket, true); err != nil {
			return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	for _, bucket := range reference {
		if err := addBucket(cohorts, bucket, false); err != nil {
			return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
	}
	if len(cohorts) > MaxCohorts {
		return Analysis{}, fmt.Errorf("%w: %w: analysis exceeds %d API groups", ErrLimit, ErrAPIGroupLimit, MaxCohorts)
	}

	keys := make([]string, 0, len(cohorts))
	for key := range cohorts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := cohorts[keys[i]], cohorts[keys[j]]
		if a.sourceID != b.sourceID {
			return a.sourceID < b.sourceID
		}
		if a.controller != b.controller {
			return a.controller < b.controller
		}
		if a.action != b.action {
			return a.action < b.action
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.outcome < b.outcome
	})

	q.Start, q.End, q.BaselineStart, q.BaselineEnd = q.Start.UTC(), q.End.UTC(), q.BaselineStart.UTC(), q.BaselineEnd.UTC()
	// Each cohort's summed request time per bucket, in sorted-key order, so a
	// series can be compared with the load of every other cohort.
	cohortSums := make([]map[int64]float64, len(keys))
	for k, key := range keys {
		cohortSums[k] = make(map[int64]float64, len(cohorts[key].currentAt))
		for at, h := range cohorts[key].currentAt {
			cohortSums[k][at] = h.sum
		}
	}
	others := newOtherLoad(cohortSums)
	analysis := Analysis{Query: q, APIs: make([]Comparison, 0, len(keys)), Timeline: make([]Series, 0), Sources: sources, Quality: quality, Approximate: true, Warnings: []string{"Latency quantiles are estimated from logarithmic histogram buckets; compared does not mean healthy."}}
	for k, key := range keys {
		c := cohorts[key]
		currentStats, referenceStats := c.current.stats(), c.reference.stats()
		// Error counts cover every document in the cohort, valid or not, so they
		// are divided by the cohort's events rather than its valid samples.
		currentStats.ClientErrors, currentStats.ServerErrors = c.currentClientErrors, c.currentServerErrors
		currentStats.ClientErrorRate = errorShare(c.currentClientErrors, c.currentEvents, c.outcome)
		currentStats.ServerErrorRate = errorShare(c.currentServerErrors, c.currentEvents, c.outcome)
		referenceStats.ClientErrors, referenceStats.ServerErrors = c.referenceClientErrors, c.referenceServerErrors
		referenceStats.ClientErrorRate = errorShare(c.referenceClientErrors, c.referenceEvents, c.outcome)
		referenceStats.ServerErrorRate = errorShare(c.referenceServerErrors, c.referenceEvents, c.outcome)
		comparison := Comparison{Key: key, SourceID: c.sourceID, Controller: c.controller, Action: c.action, Kind: c.kind, Outcome: c.outcome, CurrentEvents: c.currentEvents, ReferenceEvents: c.referenceEvents, Current: currentStats, Reference: referenceStats}
		switch {
		case currentStats.Count == 0:
			comparison.State = "missing_current"
		case referenceStats.Count == 0:
			comparison.State = "missing_reference"
		case currentStats.Count < 20 || referenceStats.Count < 20:
			comparison.State = "insufficient_data"
			analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("%s has fewer than 20 valid samples in current or reference", key))
		case referenceStats.P50 == nil || referenceStats.P95 == nil || referenceStats.P50.LowerMs <= 0 || referenceStats.P95.LowerMs <= 0:
			comparison.State = "undefined_baseline"
			analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("%s has a near-zero reference quantile; ratios are undefined", key))
		default:
			comparison.State = "compared"
		}
		comparison.MedianDeltaMs = difference(currentStats.P50, referenceStats.P50)
		comparison.P95DeltaMs = difference(currentStats.P95, referenceStats.P95)
		comparison.P99DeltaMs = difference(currentStats.P99, referenceStats.P99)
		if comparison.State == "compared" {
			comparison.MedianRatio = ratio(currentStats.P50, referenceStats.P50)
			comparison.P95Ratio = ratio(currentStats.P95, referenceStats.P95)
			comparison.P99Ratio = ratio(currentStats.P99, referenceStats.P99)
		}
		applyImpact(&comparison)
		comparison.PhaseDeltaMs = phaseDelta(currentStats, referenceStats)
		analysis.APIs = append(analysis.APIs, comparison)

		if len(c.currentAt) > 0 {
			times := make([]int64, 0, len(c.currentAt))
			for at := range c.currentAt {
				times = append(times, at)
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			series := Series{Key: key, Points: make([]Point, 0, len(times))}
			for _, at := range times {
				stats := c.currentAt[at].stats()
				point := Point{At: time.Unix(0, at).UTC(), Count: stats.Count, P50: stats.P50, P95: stats.P95}
				if comparison.State == "compared" {
					point.P95Ratio = ratio(stats.P95, referenceStats.P95)
				}
				series.Points = append(series.Points, point)
			}
			series.LoadCorrelation = seriesLoadCorrelation(series.Points, others.forCohort(k, times), q)
			analysis.Timeline = append(analysis.Timeline, series)
		}
	}
	analysis.Rollups = buildRollups(cohorts, windowMinutes(q.Start, q.End), q.Outcome == "all")
	volume, err := requestVolume(q, current)
	if err != nil {
		return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	analysis.RequestVolume = volume
	load, err := buildLoadWindows(q, current)
	if err != nil {
		return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	analysis.LoadWindows = load
	return analysis, nil
}

func addQuality(quality *Quality, b Bucket) error {
	if b.Count <= 0 {
		return fmt.Errorf("bucket count must be positive")
	}
	switch b.State {
	case "incomplete":
		if err := checkedAdd(&quality.Incomplete, b.Count); err != nil {
			return err
		}
	case "invalid":
		if err := checkedAdd(&quality.Invalid, b.Count); err != nil {
			return err
		}
	case "valid":
	default:
		return fmt.Errorf("bucket state %q is invalid", b.State)
	}
	if b.Outcome == "unknown" {
		if err := checkedAdd(&quality.UnknownOutcome, b.Count); err != nil {
			return err
		}
	}
	if b.PendingAsync < 0 || b.PendingAsync > b.Count {
		return fmt.Errorf("pendingAsync %d must be between 0 and the bucket count %d", b.PendingAsync, b.Count)
	}
	return checkedAdd(&quality.PendingAsync, b.PendingAsync)
}

func checkedAdd(dst *int64, n int64) error {
	if n < 0 || *dst > math.MaxInt64-n {
		return fmt.Errorf("analysis count overflow")
	}
	*dst += n
	return nil
}

func addBucket(cohorts map[string]*cohortAccumulator, b Bucket, isCurrent bool) error {
	if b.SourceID == "" {
		return fmt.Errorf("bucket sourceId is required")
	}
	if b.At.IsZero() {
		return fmt.Errorf("bucket at is required")
	}
	if b.Count <= 0 {
		return fmt.Errorf("bucket count must be positive")
	}
	if b.State != "valid" && b.State != "incomplete" && b.State != "invalid" {
		return fmt.Errorf("bucket state %q is invalid", b.State)
	}
	if b.Count4xx < 0 || b.Count5xx < 0 || b.CountHangfireFailed < 0 || b.Count4xx > b.Count || b.Count5xx > b.Count-b.Count4xx || b.CountHangfireFailed > b.Count-b.Count4xx-b.Count5xx {
		return fmt.Errorf("status class counts %d/%d/%d must be non-negative and sum to at most the bucket count %d", b.Count4xx, b.Count5xx, b.CountHangfireFailed, b.Count)
	}
	key := CohortKey(b.SourceID, b.Controller, b.Action, b.Kind, b.Outcome)
	c := cohorts[key]
	if c == nil {
		c = &cohortAccumulator{sourceID: b.SourceID, controller: b.Controller, action: b.Action, kind: b.Kind, outcome: b.Outcome, key: key, current: newHistogramAccumulator(), reference: newHistogramAccumulator(), currentAt: make(map[int64]*histogramAccumulator)}
		cohorts[key] = c
	}
	events := &c.referenceEvents
	if isCurrent {
		events = &c.currentEvents
	}
	if err := checkedAdd(events, b.Count); err != nil {
		return fmt.Errorf("cohort %s event count: %w", key, err)
	}
	clientErrors, serverErrors := &c.referenceClientErrors, &c.referenceServerErrors
	if isCurrent {
		clientErrors, serverErrors = &c.currentClientErrors, &c.currentServerErrors
	}
	// 5xx and failed hangfire jobs both count as server errors; 4xx stays apart.
	if err := checkedAdd(clientErrors, b.Count4xx); err != nil {
		return fmt.Errorf("cohort %s client error count: %w", key, err)
	}
	if err := checkedAdd(serverErrors, b.Count5xx); err != nil {
		return fmt.Errorf("cohort %s server error count: %w", key, err)
	}
	if err := checkedAdd(serverErrors, b.CountHangfireFailed); err != nil {
		return fmt.Errorf("cohort %s hangfire failure count: %w", key, err)
	}
	if b.State != "valid" {
		return nil
	}
	h := c.reference
	if isCurrent {
		h = c.current
	}
	if err := h.addBucket(b); err != nil {
		return fmt.Errorf("cohort %s: %w", key, err)
	}
	if isCurrent {
		at := b.At.UTC().UnixNano()
		point := c.currentAt[at]
		if point == nil {
			point = newHistogramAccumulator()
			c.currentAt[at] = point
		}
		if err := point.addBucket(b); err != nil {
			return fmt.Errorf("timeline cohort %s: %w", key, err)
		}
	}
	return nil
}

// errorShare is errors divided by the cohort's events. It is nil when the
// cohort's outcome is unknown or it has no events, so a missing denominator is
// never reported as a zero error rate.
func errorShare(errors, events int64, outcome string) *float64 {
	if outcome == "unknown" || events <= 0 {
		return nil
	}
	value := float64(errors) / float64(events)
	return &value
}

func ratio(current, reference *Quantile) *float64 {
	if current == nil || reference == nil || current.ValueMs == nil || reference.ValueMs == nil || reference.LowerMs <= 0 || *reference.ValueMs <= 0 {
		return nil
	}
	value := *current.ValueMs / *reference.ValueMs
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func difference(current, reference *Quantile) *float64 {
	if current == nil || reference == nil || current.ValueMs == nil || reference.ValueMs == nil {
		return nil
	}
	value := *current.ValueMs - *reference.ValueMs
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
