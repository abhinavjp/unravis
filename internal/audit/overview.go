package audit

import (
	"fmt"
	"math"
	"sort"
)

// minReferenceSamples is the fewest valid (timed) samples a reference window
// needs before its rates, quantiles and the deltas against them are reported.
// It matches the rule Comparison uses for its own states.
const minReferenceSamples = 20

// overviewKinds is the fixed order and the full set of row kinds: the three
// derived kinds plus unknown, which only gets a row when something has it. The
// overview therefore never has more than four rows.
var overviewKinds = []string{"synchronous", "asynchronous", "hangfire", KindUnknown}

// OverviewWindow summarises one window of one kind. Events counts every audit
// row; Completed counts only valid (timed) ones. Incomplete and invalid rows
// are never treated as fast or successful: they lower CompletionRate and add
// nothing to the latency quantiles. ClientErrorRate and ServerErrorRate are
// shares of the events whose result is known (success or failure), so unknown
// outcomes sit outside numerator and denominator.
type OverviewWindow struct {
	Events         int64   `json:"events"`
	Completed      int64   `json:"completed"`
	Incomplete     int64   `json:"incomplete"`
	Invalid        int64   `json:"invalid"`
	UnknownOutcome int64   `json:"unknownOutcome"`
	ClientErrors   int64   `json:"clientErrors"`
	ServerErrors   int64   `json:"serverErrors"`
	PerMinute      float64 `json:"perMinute"`

	CompletionRate  *float64  `json:"completionRate,omitempty"`
	ClientErrorRate *float64  `json:"clientErrorRate,omitempty"`
	ServerErrorRate *float64  `json:"serverErrorRate,omitempty"`
	P50             *Quantile `json:"p50,omitempty"`
	P90             *Quantile `json:"p90,omitempty"`
	P95             *Quantile `json:"p95,omitempty"`
	// P99 is nil below 100 valid samples.
	P99 *Quantile `json:"p99,omitempty"`
}

// OverviewDelta is current minus reference. Traffic is the relative change of
// events per minute (0.5 means 50% more); the rates are absolute differences of
// shares; the latency fields are milliseconds. Every field is nil when the
// reference has fewer than minReferenceSamples valid samples or when either
// side of the difference is unknown. Latency deltas also need the current
// window to have enough samples (state "compared").
type OverviewDelta struct {
	Traffic         *float64 `json:"traffic,omitempty"`
	CompletionRate  *float64 `json:"completionRate,omitempty"`
	ClientErrorRate *float64 `json:"clientErrorRate,omitempty"`
	ServerErrorRate *float64 `json:"serverErrorRate,omitempty"`
	P50Ms           *float64 `json:"p50Ms,omitempty"`
	P90Ms           *float64 `json:"p90Ms,omitempty"`
	P95Ms           *float64 `json:"p95Ms,omitempty"`
	P99Ms           *float64 `json:"p99Ms,omitempty"`
}

// OverviewRow is the current window of one kind plus its reference window and
// the deltas between them. Histograms are merged within a kind, never across
// kinds. State is compared, missing_current, missing_reference or
// insufficient_data, judged on valid samples like Comparison.State.
type OverviewRow struct {
	Kind string `json:"kind"`
	OverviewWindow
	State     string         `json:"state"`
	Reference OverviewWindow `json:"reference"`
	Delta     OverviewDelta  `json:"delta"`
}

// kindWindow accumulates one window of one kind straight from the summary
// buckets.
type kindWindow struct {
	events, incomplete, invalid, unknown int64
	known, failures, client, server      int64
	hist                                 *histogramAccumulator
}

func (w *kindWindow) add(b Bucket) error {
	if err := checkedAdd(&w.events, b.Count); err != nil {
		return err
	}
	switch b.State {
	case "incomplete":
		if err := checkedAdd(&w.incomplete, b.Count); err != nil {
			return err
		}
	case "invalid":
		if err := checkedAdd(&w.invalid, b.Count); err != nil {
			return err
		}
	case "valid":
		if w.hist == nil {
			w.hist = newHistogramAccumulator()
		}
		if err := w.hist.addBucket(b); err != nil {
			return err
		}
	}
	switch b.Outcome {
	case "unknown":
		return checkedAdd(&w.unknown, b.Count)
	case "success":
		return checkedAdd(&w.known, b.Count)
	case "failure":
		for _, add := range []struct {
			dst *int64
			n   int64
		}{{&w.known, b.Count}, {&w.failures, b.Count}, {&w.client, b.Count4xx}, {&w.server, b.Count5xx}, {&w.server, b.CountHangfireFailed}} {
			if err := checkedAdd(add.dst, add.n); err != nil {
				return err
			}
		}
	}
	return nil
}

// finish turns the accumulator into a window summary. A window with fewer than
// minSamples valid samples keeps its raw counts but reports no rates or
// quantiles. allOutcomes says whether the sources returned every outcome; with
// an outcome filter the failure share is unknowable.
func (w *kindWindow) finish(minSamples int64, minutes float64, allOutcomes bool) OverviewWindow {
	out := OverviewWindow{Events: w.events, Incomplete: w.incomplete, Invalid: w.invalid, UnknownOutcome: w.unknown, ClientErrors: w.client, ServerErrors: w.server}
	if w.hist != nil {
		out.Completed = w.hist.count
	}
	if minutes > 0 {
		out.PerMinute = float64(w.events) / minutes
	}
	if out.Completed < minSamples {
		return out
	}
	if w.events > 0 {
		rate := float64(out.Completed) / float64(w.events)
		out.CompletionRate = &rate
	}
	// The split is only known when the status counters account for every
	// failure; otherwise a missing counter would show as 0%.
	if allOutcomes && w.known > 0 && saturatingAdd(w.client, w.server) == w.failures {
		client, server := float64(w.client)/float64(w.known), float64(w.server)/float64(w.known)
		out.ClientErrorRate, out.ServerErrorRate = &client, &server
	}
	if w.hist != nil && w.hist.count > 0 {
		out.P50, out.P90, out.P95 = w.hist.quantile(.50), w.hist.quantile(.90), w.hist.quantile(.95)
		if w.hist.count >= 100 {
			out.P99 = w.hist.quantile(.99)
		}
	}
	return out
}

func knownOverviewKind(kind string) bool {
	for _, k := range overviewKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// buildOverview folds the (kind-scoped) buckets of both windows into one row
// per kind present in either window. It rejects kinds outside the fixed set
// rather than merging them into another kind.
func buildOverview(q Query, current, reference []Bucket) ([]OverviewRow, error) {
	cur, ref := map[string]*kindWindow{}, map[string]*kindWindow{}
	fold := func(into map[string]*kindWindow, buckets []Bucket) error {
		for _, b := range buckets {
			if !knownOverviewKind(b.Kind) {
				return fmt.Errorf("bucket kind %q is not recognised", b.Kind)
			}
			w := into[b.Kind]
			if w == nil {
				w = &kindWindow{}
				into[b.Kind] = w
			}
			if err := w.add(b); err != nil {
				return fmt.Errorf("overview %s: %w", b.Kind, err)
			}
		}
		return nil
	}
	if err := fold(cur, current); err != nil {
		return nil, err
	}
	if err := fold(ref, reference); err != nil {
		return nil, err
	}
	currentMinutes, referenceMinutes := windowMinutes(q.Start, q.End), windowMinutes(q.BaselineStart, q.BaselineEnd)
	allOutcomes := q.Outcome == "all"
	rows := make([]OverviewRow, 0, len(overviewKinds))
	for _, kind := range overviewKinds {
		c, r := cur[kind], ref[kind]
		if c == nil && r == nil {
			continue
		}
		if c == nil {
			c = &kindWindow{}
		}
		if r == nil {
			r = &kindWindow{}
		}
		row := OverviewRow{Kind: kind, OverviewWindow: c.finish(0, currentMinutes, allOutcomes), Reference: r.finish(minReferenceSamples, referenceMinutes, allOutcomes)}
		switch {
		case row.Completed == 0:
			row.State = "missing_current"
		case row.Reference.Completed == 0:
			row.State = "missing_reference"
		case row.Completed < minReferenceSamples || row.Reference.Completed < minReferenceSamples:
			row.State = "insufficient_data"
		default:
			row.State = "compared"
		}
		row.Delta = windowDelta(row.OverviewWindow, row.Reference, row.State == "compared")
		rows = append(rows, row)
	}
	return rows, nil
}

// windowDelta is current minus reference, nil unless the reference has enough
// valid samples. Latency deltas also require the comparison to be fully
// supported (compared).
func windowDelta(cur, ref OverviewWindow, compared bool) OverviewDelta {
	var d OverviewDelta
	if ref.Completed < minReferenceSamples {
		return d
	}
	if ref.PerMinute > 0 {
		d.Traffic = floatPointer(cur.PerMinute/ref.PerMinute - 1)
	}
	d.CompletionRate = subtract(cur.CompletionRate, ref.CompletionRate)
	d.ClientErrorRate = subtract(cur.ClientErrorRate, ref.ClientErrorRate)
	d.ServerErrorRate = subtract(cur.ServerErrorRate, ref.ServerErrorRate)
	if compared {
		d.P50Ms = difference(cur.P50, ref.P50)
		d.P90Ms = difference(cur.P90, ref.P90)
		d.P95Ms = difference(cur.P95, ref.P95)
		d.P99Ms = difference(cur.P99, ref.P99)
	}
	return d
}

func subtract(current, reference *float64) *float64 {
	if current == nil || reference == nil {
		return nil
	}
	value := *current - *reference
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// merge adds another histogram's counts into h. Phase totals are not carried
// over: the merged histograms only feed latency quantiles.
func (h *histogramAccumulator) merge(o *histogramAccumulator) error {
	if o == nil || o.count == 0 {
		return nil
	}
	if math.MaxInt64-h.count < o.count {
		return fmt.Errorf("histogram count overflow")
	}
	if math.MaxFloat64-h.sum < o.sum {
		return fmt.Errorf("histogram sum overflow")
	}
	first := h.count == 0
	for bin, n := range o.counts {
		h.counts[bin] += n // each bin is at most the total, which was checked above
	}
	h.count += o.count
	h.sum += o.sum
	if first || o.min < h.min {
		h.min = o.min
	}
	if first || o.max > h.max {
		h.max = o.max
	}
	return nil
}

// rollupSide sums one window of one (controller, action, kind) over its
// cohorts, that is over sources and outcomes.
type rollupSide struct {
	events, requests, failures, client, server int64
	hist                                       *histogramAccumulator
}

func (s *rollupSide) fold(outcome string, events, client, server int64, valid *histogramAccumulator) error {
	s.events = saturatingAdd(s.events, events)
	switch outcome {
	case "success":
		s.requests = saturatingAdd(s.requests, events)
	case "failure":
		s.requests = saturatingAdd(s.requests, events)
		s.failures = saturatingAdd(s.failures, events)
		s.client = saturatingAdd(s.client, client)
		s.server = saturatingAdd(s.server, server)
	}
	if valid.count > 0 {
		if s.hist == nil {
			s.hist = newHistogramAccumulator()
		}
		return s.hist.merge(valid)
	}
	return nil
}

func (s *rollupSide) completed() int64 {
	if s == nil || s.hist == nil {
		return 0
	}
	return s.hist.count
}

// withRollupComparison adds events, completion, P95 and the reference window
// to each rollup, plus deltas against it. Rollups that only exist in the
// reference window (an API that stopped) are appended with zero current
// requests so the drop shows. Reference values and deltas stay nil when the
// reference has fewer than minReferenceSamples valid samples; the raw
// reference counts are always filled in.
func withRollupComparison(rows []Rollup, cohorts map[string]*cohortAccumulator, q Query) ([]Rollup, error) {
	type name struct{ controller, action, kind string }
	cur, ref := map[name]*rollupSide{}, map[name]*rollupSide{}
	side := func(m map[name]*rollupSide, key name) *rollupSide {
		s := m[key]
		if s == nil {
			s = &rollupSide{}
			m[key] = s
		}
		return s
	}
	for _, c := range cohorts {
		key := name{c.controller, c.action, c.kind}
		if c.currentEvents > 0 {
			if err := side(cur, key).fold(c.outcome, c.currentEvents, c.currentClientErrors, c.currentServerErrors, c.current); err != nil {
				return nil, fmt.Errorf("rollup %s.%s: %w", c.controller, c.action, err)
			}
		}
		if c.referenceEvents > 0 {
			if err := side(ref, key).fold(c.outcome, c.referenceEvents, c.referenceClientErrors, c.referenceServerErrors, c.reference); err != nil {
				return nil, fmt.Errorf("rollup %s.%s: %w", c.controller, c.action, err)
			}
		}
	}
	currentMinutes, referenceMinutes := windowMinutes(q.Start, q.End), windowMinutes(q.BaselineStart, q.BaselineEnd)
	allOutcomes := q.Outcome == "all"
	have := make(map[name]bool, len(rows))
	for i := range rows {
		key := name{rows[i].Controller, rows[i].Action, rows[i].Kind}
		have[key] = true
		fillRollup(&rows[i], cur[key], ref[key], currentMinutes, referenceMinutes, allOutcomes)
	}
	for key, r := range ref {
		if have[key] || r.requests <= 0 {
			continue
		}
		row := Rollup{Controller: key.controller, Action: key.action, Kind: key.kind}
		fillRollup(&row, nil, r, currentMinutes, referenceMinutes, allOutcomes)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Controller != rows[j].Controller {
			return rows[i].Controller < rows[j].Controller
		}
		if rows[i].Action != rows[j].Action {
			return rows[i].Action < rows[j].Action
		}
		return rows[i].Kind < rows[j].Kind
	})
	return rows, nil
}

func fillRollup(r *Rollup, cur, ref *rollupSide, currentMinutes, referenceMinutes float64, allOutcomes bool) {
	if cur != nil {
		r.Events, r.Completed = cur.events, cur.completed()
		if r.Completed >= minReferenceSamples {
			r.P95Ms = quantileValue(cur.hist.quantile(.95))
		}
	}
	if r.Events > 0 {
		r.CompletionRate = floatPointer(float64(r.Completed) / float64(r.Events))
	}
	if ref == nil {
		return
	}
	r.ReferenceRequests, r.ReferenceFailures, r.ReferenceEvents, r.ReferenceCompleted = ref.requests, ref.failures, ref.events, ref.completed()
	if r.ReferenceCompleted < minReferenceSamples {
		return
	}
	r.ReferenceCompletionRate = floatPointer(float64(r.ReferenceCompleted) / float64(ref.events))
	r.ReferenceP95Ms = quantileValue(ref.hist.quantile(.95))
	if allOutcomes && ref.requests > 0 {
		r.ReferenceErrorRate = floatPointer(float64(ref.failures) / float64(ref.requests))
		if saturatingAdd(ref.client, ref.server) == ref.failures {
			r.ReferenceClientErrorRate = floatPointer(float64(ref.client) / float64(ref.requests))
			r.ReferenceServerErrorRate = floatPointer(float64(ref.server) / float64(ref.requests))
		}
	}
	if currentMinutes > 0 && referenceMinutes > 0 && ref.events > 0 {
		r.TrafficDelta = floatPointer((float64(r.Events)/currentMinutes)/(float64(ref.events)/referenceMinutes) - 1)
	}
	r.CompletionDelta = subtract(r.CompletionRate, r.ReferenceCompletionRate)
	r.ClientErrorDelta = subtract(r.ClientErrorRate, r.ReferenceClientErrorRate)
	r.ServerErrorDelta = subtract(r.ServerErrorRate, r.ReferenceServerErrorRate)
	r.P95DeltaMs = subtract(r.P95Ms, r.ReferenceP95Ms)
}

func quantileValue(q *Quantile) *float64 {
	if q == nil || q.ValueMs == nil {
		return nil
	}
	return floatPointer(*q.ValueMs)
}
