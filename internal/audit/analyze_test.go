package audit

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

var anchor = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func analysisQuery() Query {
	return Query{SourceIDs: []string{"s1"}, Start: anchor, End: anchor.Add(time.Minute), BaselineStart: anchor.Add(-time.Minute), BaselineEnd: anchor, Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 10}
}

func makeBuckets(source, controller, action, kind, outcome, state string, at time.Time, count int64, ms float64) []Bucket {
	if count == 0 {
		return nil
	}
	return []Bucket{{SourceID: source, Controller: controller, Action: action, Kind: kind, Outcome: outcome, State: state, At: at, Bin: BinIndex(ms), Count: count, SumMs: float64(count) * ms, MinMs: ms, MaxMs: ms}}
}

func repeatedBuckets(count int64, ms float64, at time.Time) []Bucket {
	return makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", at, count, ms)
}

func TestCohortKeyIsReversibleAndDelimiterSafe(t *testing.T) {
	fields := []string{"s|1", "controller/one", "a,ction", "sync\n", "failure\""}
	key := CohortKey(fields[0], fields[1], fields[2], fields[3], fields[4])
	decoded, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatal(err)
	}
	for i := range fields {
		if fields[i] != got[i] {
			t.Fatalf("key field %d=%q, want %q", i, got[i], fields[i])
		}
	}
	if key == CohortKey("s", "1|controller/one", "a,ction", "sync\n", "failure\"") {
		t.Fatal("different cohort tuples collided")
	}
}

func TestBuildAnalysisUsesHistogramMidpointsForRatios(t *testing.T) {
	q := analysisQuery()
	analysis, err := BuildAnalysis(q, repeatedBuckets(20, 10.5, anchor), repeatedBuckets(20, 3.5, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) != 1 {
		t.Fatalf("got %d comparisons", len(analysis.APIs))
	}
	c := analysis.APIs[0]
	if c.State != "compared" || c.MedianRatio == nil || math.Abs(*c.MedianRatio-3) > 1e-12 {
		t.Fatalf("comparison = %#v", c)
	}
	if c.Current.P50.ValueMs == nil || c.Reference.P50.ValueMs == nil {
		t.Fatal("finite quantile estimates should be present")
	}
}

func TestBuildAnalysisEqualHistogramsHaveUnitRatios(t *testing.T) {
	q := analysisQuery()
	analysis, err := BuildAnalysis(q, repeatedBuckets(20, 10.5, anchor), repeatedBuckets(20, 10.5, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := analysis.APIs[0]
	if c.MedianRatio == nil || *c.MedianRatio != 1 || c.P95Ratio == nil || *c.P95Ratio != 1 {
		t.Fatalf("ratios = %v, %v", c.MedianRatio, c.P95Ratio)
	}
}

func TestBuildAnalysisReportsMissingAndInsufficientData(t *testing.T) {
	q := analysisQuery()
	for _, tc := range []struct {
		name, want         string
		current, reference []Bucket
	}{
		{"no reference", "missing_reference", repeatedBuckets(20, 2, anchor), nil},
		{"no current", "missing_current", nil, repeatedBuckets(20, 2, anchor.Add(-time.Minute))},
		{"sparse", "insufficient_data", repeatedBuckets(19, 2, anchor), repeatedBuckets(20, 2, anchor.Add(-time.Minute))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysis, err := BuildAnalysis(q, tc.current, tc.reference, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(analysis.APIs) != 1 || analysis.APIs[0].State != tc.want {
				t.Fatalf("comparison = %#v", analysis.APIs)
			}
			if tc.want == "insufficient_data" && (analysis.APIs[0].MedianRatio != nil || analysis.APIs[0].P95Ratio != nil) {
				t.Fatal("sparse ratios must be null")
			}
		})
	}
}

func TestBuildAnalysisNearZeroReferenceIsUndefined(t *testing.T) {
	q := analysisQuery()
	analysis, err := BuildAnalysis(q, repeatedBuckets(20, .5, anchor), repeatedBuckets(20, 0, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := analysis.APIs[0]
	if c.State != "undefined_baseline" || c.MedianRatio != nil || c.P95Ratio != nil {
		t.Fatalf("zero baseline must be undefined: %#v", c)
	}
}

func TestBuildAnalysisCountsIncompleteAndInvalidWithoutIncludingThemInStats(t *testing.T) {
	q := analysisQuery()
	current := append(repeatedBuckets(20, 2, anchor), makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 3, 0)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "invalid", anchor, 2, 0)...)
	analysis, err := BuildAnalysis(q, current, repeatedBuckets(20, 2, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Quality.Incomplete != 3 || analysis.Quality.Invalid != 2 || analysis.Quality.UnknownOutcome != 3 {
		t.Fatalf("quality = %#v", analysis.Quality)
	}
	for _, c := range analysis.APIs {
		if c.Outcome == "success" && c.Current.Count != 20 {
			t.Fatalf("non-valid samples affected stats: %#v", c.Current)
		}
	}
}

func TestBuildAnalysisCountsOutcomeEventsSeparatelyFromValidLatency(t *testing.T) {
	q := analysisQuery()
	current := repeatedBuckets(20, 2, anchor)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "invalid", anchor, 2, 0)...)
	reference := makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "incomplete", anchor.Add(-time.Minute), 4, 0)
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	byOutcome := make(map[string]Comparison, len(analysis.APIs))
	for _, comparison := range analysis.APIs {
		byOutcome[comparison.Outcome] = comparison
	}
	success := byOutcome["success"]
	if success.CurrentEvents != 20 || success.Current.Count != 20 {
		t.Fatalf("success event and valid latency counts = %d and %d, want 20 and 20", success.CurrentEvents, success.Current.Count)
	}
	failure := byOutcome["failure"]
	if failure.CurrentEvents != 2 {
		t.Fatalf("failure event count = %d, want 2", failure.CurrentEvents)
	}
	if success.CurrentEvents+failure.CurrentEvents != 22 {
		t.Fatalf("known outcome denominator = %d, want 22", success.CurrentEvents+failure.CurrentEvents)
	}
	if failure.ReferenceEvents != 4 || failure.Reference.Count != 0 {
		t.Fatalf("reference event and valid latency counts = %d and %d, want 4 and 0", failure.ReferenceEvents, failure.Reference.Count)
	}
	if failure.Current.Count != 0 || len(failure.Current.Histogram) != 0 {
		t.Fatalf("invalid failure timing entered latency statistics: %#v", failure.Current)
	}
}

func TestBuildAnalysisKeepsSourceCohortsDisjointAndTimelineSparse(t *testing.T) {
	q := analysisQuery()
	q.SourceIDs = []string{"s1", "s2"}
	current := repeatedBuckets(20, 2, anchor)
	second := makeBuckets("s2", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(20*time.Second), 20, 4)
	current = append(current, second...)
	analysis, err := BuildAnalysis(q, current, append(repeatedBuckets(20, 2, anchor.Add(-time.Minute)), makeBuckets("s2", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(-time.Minute), 20, 4)...), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) != 2 || analysis.APIs[0].SourceID == analysis.APIs[1].SourceID {
		t.Fatalf("source cohorts pooled: %#v", analysis.APIs)
	}
	for _, s := range analysis.Timeline {
		if len(s.Points) != 1 {
			t.Fatalf("timeline filled absent interval with zero: %#v", s)
		}
	}
}

func TestBuildAnalysisAllowsProductionGroupCount(t *testing.T) {
	current := make([]Bucket, 214)
	for i := range current {
		current[i] = Bucket{SourceID: "s1", Controller: time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano), Action: "A", Kind: "synchronous", Outcome: "success", State: "valid", At: anchor, Bin: 1, Count: 1, SumMs: 1.05, MinMs: 1.05, MaxMs: 1.05}
	}
	result, err := BuildAnalysis(analysisQuery(), current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.APIs) != 214 {
		t.Fatalf("groups=%d, want 214", len(result.APIs))
	}
}

func TestBuildAnalysisEnforcesRowAndCohortLimits(t *testing.T) {
	q := analysisQuery()
	if _, err := BuildAnalysis(q, make([]Bucket, MaxSummaryRows+1), nil, nil); err == nil {
		t.Fatal("expected row limit error")
	}
	current := make([]Bucket, MaxCohorts+1)
	for i := range current {
		current[i] = Bucket{SourceID: "s1", Controller: time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano), Action: "A", Kind: "synchronous", Outcome: "success", State: "valid", At: anchor, Bin: 1, Count: 1, SumMs: 1.05, MinMs: 1.05, MaxMs: 1.05}
	}
	if _, err := BuildAnalysis(q, current, nil, nil); !errors.Is(err, ErrAPIGroupLimit) || !errors.Is(err, ErrLimit) {
		t.Fatalf("expected API group limit error, got %v", err)
	}
}

func TestBuildAnalysisNormalizesTransportTimesToUTC(t *testing.T) {
	q := analysisQuery()
	loc := time.FixedZone("UTC+4", 4*60*60)
	q.Start = q.Start.In(loc)
	q.End = q.End.In(loc)
	q.BaselineStart = q.BaselineStart.In(loc)
	q.BaselineEnd = q.BaselineEnd.In(loc)
	analysis, err := BuildAnalysis(q, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []time.Time{analysis.Query.Start, analysis.Query.End, analysis.Query.BaselineStart, analysis.Query.BaselineEnd} {
		if value.Location() != time.UTC {
			t.Fatalf("transport time not normalized to UTC: %s", value.Location())
		}
	}
	if q.Start.Location() != loc {
		t.Fatal("BuildAnalysis mutated the caller's query")
	}
}

func TestReferenceCompactionPreservesAnalysis(t *testing.T) {
	q := analysisQuery()
	current := append(repeatedBuckets(15, 10.5, anchor), repeatedBuckets(15, 30.5, anchor.Add(10*time.Second))...)
	reference := append(repeatedBuckets(12, 8.5, anchor.Add(-50*time.Second)), repeatedBuckets(13, 8.5, anchor.Add(-20*time.Second))...)
	reference = append(reference, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "incomplete", anchor.Add(-30*time.Second), 2, 0)...)
	reference = append(reference, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(-10*time.Second), 10, 40.5)...)
	compact := []Bucket{reference[0], reference[2], reference[3]}
	compact[0].Count += reference[1].Count
	compact[0].SumMs += reference[1].SumMs
	for i := range compact {
		compact[i].At = q.BaselineStart
	}
	before, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := BuildAnalysis(q, current, compact, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("reference compaction changed comparisons, histogram, sample counts, quality, warnings or current timeline")
	}
}

func TestBuildAnalysisP99RatioAndDeltaFollowComparisonState(t *testing.T) {
	q := analysisQuery()
	analysis, err := BuildAnalysis(q, repeatedBuckets(100, 10.5, anchor), repeatedBuckets(100, 3.5, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := analysis.APIs[0]
	if c.State != "compared" || c.P99Ratio == nil || math.Abs(*c.P99Ratio-3) > 1e-12 || c.P99DeltaMs == nil || math.Abs(*c.P99DeltaMs-7) > 1e-12 {
		t.Fatalf("p99 ratio/delta = %v, %v (state %s)", c.P99Ratio, c.P99DeltaMs, c.State)
	}
	// Compared with fewer than 100 samples: P99 is undefined, so its ratio is null.
	analysis, err = BuildAnalysis(q, repeatedBuckets(20, 10.5, anchor), repeatedBuckets(20, 3.5, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	c = analysis.APIs[0]
	if c.State != "compared" || c.P99Ratio != nil || c.P99DeltaMs != nil || c.P95Ratio == nil {
		t.Fatalf("sparse p99 must be null while p95 is present: %#v", c)
	}
	// Insufficient data: ratio stays null even with a reference P99.
	analysis, err = BuildAnalysis(q, repeatedBuckets(19, 10.5, anchor), repeatedBuckets(100, 3.5, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c = analysis.APIs[0]; c.State != "insufficient_data" || c.P99Ratio != nil {
		t.Fatalf("insufficient data must have null p99 ratio: %#v", c)
	}
}

func TestBuildAnalysisCountsPendingAsyncInQualityAndObservedPartialInStats(t *testing.T) {
	q := analysisQuery()
	valid := repeatedBuckets(20, 2, anchor)
	valid[0].ObservedPartial = 4
	pending := makeBuckets("s1", "Payroll", "Run", "asynchronous", "unknown", "incomplete", anchor, 3, 0)
	pending[0].PendingAsync = 3
	other := makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 2, 0)
	analysis, err := BuildAnalysis(q, append(append(valid, pending...), other...), repeatedBuckets(20, 2, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Quality.PendingAsync != 3 || analysis.Quality.Incomplete != 5 {
		t.Fatalf("quality = %#v, want 3 pending of 5 incomplete", analysis.Quality)
	}
	for _, c := range analysis.APIs {
		if c.Kind == "synchronous" && c.Outcome == "success" && (c.Current.ObservedPartial != 4 || c.Reference.ObservedPartial != 0) {
			t.Fatalf("observedPartial current/reference = %d/%d, want 4/0", c.Current.ObservedPartial, c.Reference.ObservedPartial)
		}
		if c.Kind == "asynchronous" && (c.Current.Count != 0 || c.CurrentEvents != 3) {
			t.Fatalf("pending async cohort %#v must have events but no timing samples", c)
		}
	}
	encoded, _ := json.Marshal(Quality{Incomplete: 1})
	if string(encoded) != `{"incomplete":1,"invalid":0,"unknownOutcome":0}` {
		t.Fatalf("an unset pendingAsync must be omitted: %s", encoded)
	}
}

func TestBucketRejectsObservedPartialAboveCount(t *testing.T) {
	bucket := repeatedBuckets(2, 2, anchor)[0]
	bucket.ObservedPartial = 3
	if _, err := BuildAnalysis(analysisQuery(), []Bucket{bucket}, nil, nil); err == nil {
		t.Fatal("observedPartial greater than the bucket count must be rejected")
	}
}
