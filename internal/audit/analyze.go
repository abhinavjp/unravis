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
	analysis := Analysis{Query: q, APIs: make([]Comparison, 0, len(keys)), Timeline: make([]Series, 0), Sources: sources, Quality: quality, Approximate: true, Warnings: []string{"Latency quantiles are estimated from logarithmic histogram buckets; compared does not mean healthy."}}
	for _, key := range keys {
		c := cohorts[key]
		currentStats, referenceStats := c.current.stats(), c.reference.stats()
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
		analysis.APIs = append(analysis.APIs, comparison)

		if len(c.currentAt) > 0 {
			times := make([]int64, 0, len(c.currentAt))
			for at := range c.currentAt {
				times = append(times, at)
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			series := Series{Key: key, Points: make([]Point, 0, len(times))}
			sums := make([]float64, 0, len(times))
			for _, at := range times {
				stats := c.currentAt[at].stats()
				sums = append(sums, c.currentAt[at].sum)
				point := Point{At: time.Unix(0, at).UTC(), Count: stats.Count, P50: stats.P50, P95: stats.P95}
				if comparison.State == "compared" {
					point.P95Ratio = ratio(stats.P95, referenceStats.P95)
				}
				series.Points = append(series.Points, point)
			}
			series.LoadCorrelation = seriesLoadCorrelation(series.Points, sums, q.BucketSeconds)
			analysis.Timeline = append(analysis.Timeline, series)
		}
	}
	analysis.Rollups = buildRollups(cohorts, windowMinutes(q.Start, q.End))
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
	return nil
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
	if b.State != "valid" {
		return nil
	}
	h := c.reference
	if isCurrent {
		h = c.current
	}
	if err := h.add(b.Bin, b.Count, b.SumMs, b.MinMs, b.MaxMs); err != nil {
		return fmt.Errorf("cohort %s: %w", key, err)
	}
	if isCurrent {
		at := b.At.UTC().UnixNano()
		point := c.currentAt[at]
		if point == nil {
			point = newHistogramAccumulator()
			c.currentAt[at] = point
		}
		if err := point.add(b.Bin, b.Count, b.SumMs, b.MinMs, b.MaxMs); err != nil {
			return fmt.Errorf("timeline cohort %s: %w", key, err)
		}
	}
	return nil
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
