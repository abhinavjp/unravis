package audit

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
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
	foundValid, foundPending := false, false
	for _, c := range analysis.APIs {
		if c.Kind == "synchronous" && c.Outcome == "success" {
			foundValid = true
			if c.Current.ObservedPartial != 4 || c.Reference.ObservedPartial != 0 {
				t.Fatalf("observedPartial current/reference = %d/%d, want 4/0", c.Current.ObservedPartial, c.Reference.ObservedPartial)
			}
		}
		if c.Kind == "asynchronous" {
			foundPending = true
			if c.Current.Count != 0 || c.CurrentEvents != 3 {
				t.Fatalf("pending async cohort %#v must have events but no timing samples", c)
			}
		}
	}
	if !foundValid || !foundPending {
		t.Fatalf("expected both the synchronous success and the pending asynchronous cohorts, found valid=%v pending=%v in %d cohorts", foundValid, foundPending, len(analysis.APIs))
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

func TestQualityRespectsKindFilter(t *testing.T) {
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 30, 10)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 2, 0)...)
	current = append(current, makeBuckets("s1", "Jobs", "Nightly", "hangfire", "unknown", "incomplete", anchor, 7, 0)...)
	current = append(current, makeBuckets("s1", "Jobs", "Nightly", "hangfire", "unknown", "invalid", anchor, 3, 0)...)
	current = append(current, makeBuckets("s1", "Payroll", "Export", "unknown", "unknown", "incomplete", anchor, 4, 0)...)
	current = append(current, makeBuckets("s1", "Payroll", "Export", "asynchronous", "unknown", "incomplete", anchor, 5, 0)...)

	q := analysisQuery()
	q.Kind = "synchronous"
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Synchronous keeps its own incomplete rows and the polling-type-unknown ones; never Hangfire or Asynchronous.
	if analysis.Quality.Incomplete != 6 || analysis.Quality.Invalid != 0 {
		t.Fatalf("Quality incomplete/invalid = %d/%d, want 6/0 (hangfire and asynchronous rows must not count)", analysis.Quality.Incomplete, analysis.Quality.Invalid)
	}
	if analysis.Quality.UnknownOutcome != 6 {
		t.Fatalf("Quality.UnknownOutcome = %d, want 6", analysis.Quality.UnknownOutcome)
	}
	if got := analysis.Quality.ByKind["synchronous"]; got.Incomplete != 2 {
		t.Fatalf("ByKind[synchronous] = %+v, want incomplete 2", got)
	}
	if got := analysis.Quality.ByKind["unknown"]; got.Incomplete != 4 {
		t.Fatalf("ByKind[unknown] = %+v, want incomplete 4", got)
	}
	if _, ok := analysis.Quality.ByKind["hangfire"]; ok {
		t.Fatalf("ByKind must not report hangfire under a synchronous query: %+v", analysis.Quality.ByKind)
	}
	for _, c := range analysis.APIs {
		if c.Kind == "hangfire" || c.Kind == "asynchronous" {
			t.Fatalf("a %s cohort leaked into the synchronous view", c.Kind)
		}
	}

	q.Kind = "hangfire"
	analysis, err = BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Quality.Incomplete != 7 || analysis.Quality.Invalid != 3 || len(analysis.Quality.ByKind) != 1 {
		t.Fatalf("hangfire quality = %+v, want only the 7 incomplete and 3 invalid hangfire rows", analysis.Quality)
	}

	q.Kind = "all"
	analysis, err = BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Quality.Incomplete != 18 || analysis.Quality.Invalid != 3 || len(analysis.Quality.ByKind) != 4 {
		t.Fatalf("all-kinds quality = %+v, want every row counted and reported per kind", analysis.Quality)
	}
}

func TestKindHTTPCoversUnknownAndNeverHangfire(t *testing.T) {
	cases := []struct {
		query, row string
		valid      bool
		want       bool
	}{
		{"http", "synchronous", true, true},
		{"http", "asynchronous", true, true},
		{"http", "unknown", true, true},
		{"http", "unknown", false, true},
		{"http", "hangfire", false, false},
		{"http", "hangfire", true, false},
		{"http_unknown", "unknown", true, true},
		{"http_unknown", "unknown", false, true},
		{"http_unknown", "synchronous", false, false},
		{"synchronous", "unknown", false, true},
		{"synchronous", "unknown", true, false},
		{"asynchronous", "unknown", false, true},
		{"asynchronous", "synchronous", false, false},
		{"synchronous", "asynchronous", false, false},
		{"hangfire", "unknown", false, false},
		{"hangfire", "hangfire", false, true},
		{"all", "hangfire", false, true},
	}
	for _, c := range cases {
		if got := KindMatches(c.query, c.row, c.valid); got != c.want {
			t.Errorf("KindMatches(%q, %q, valid=%v) = %v, want %v", c.query, c.row, c.valid, got, c.want)
		}
	}
	q := analysisQuery()
	for _, kind := range []string{"http", "http_unknown"} {
		q.Kind = kind
		if err := ValidateQuery(q); err != nil {
			t.Errorf("kind %q should validate: %v", kind, err)
		}
	}
	q.Kind = "bogus"
	if err := ValidateQuery(q); err == nil {
		t.Error("an unknown kind must be rejected")
	}
}

func failureBucket(state string, at time.Time, count, c4, c5 int64, ms float64) Bucket {
	outcome := "failure"
	if state == "incomplete" {
		outcome = "unknown"
	}
	b := Bucket{SourceID: "s1", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: outcome, State: state, At: at, Count: count, Count4xx: c4, Count5xx: c5}
	if state == "valid" {
		b.Bin, b.SumMs, b.MinMs, b.MaxMs = BinIndex(ms), float64(count)*ms, ms, ms
	}
	return b
}

func timelineSeriesFor(t *testing.T, analysis Analysis, outcome string) Series {
	t.Helper()
	for _, c := range analysis.APIs {
		if c.Outcome != outcome {
			continue
		}
		for _, s := range analysis.Timeline {
			if s.Key == c.Key {
				return s
			}
		}
	}
	t.Fatalf("no %s timeline series in %#v", outcome, analysis.Timeline)
	return Series{}
}

func TestTimelinePointCountsFailuresFromAllStatesPercentilesFromValid(t *testing.T) {
	q := analysisQuery()
	slot0, slot1 := anchor, anchor.Add(10*time.Second)
	current := []Bucket{
		failureBucket("valid", slot0, 5, 3, 2, 40),     // five timed failures: 3 are 4xx, 2 are 5xx
		failureBucket("invalid", slot0, 1, 0, 1, 0),    // a 5xx whose timing is unusable still counts as a failure
		failureBucket("incomplete", slot0, 2, 0, 0, 0), // missing timing: neither success nor failure
		failureBucket("incomplete", slot1, 3, 0, 3, 0), // a slot with no valid timing at all
	}
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	series := timelineSeriesFor(t, analysis, "failure")
	if len(series.Points) != 1 {
		t.Fatalf("failure cohort points = %#v, want the one timed slot", series.Points)
	}
	first := series.Points[0]
	if first.Count != 5 || first.Count4xx != 3 || first.Count5xx != 3 || first.Incomplete != 0 {
		t.Fatalf("first slot = %#v, want 5 valid timings, 3 4xx, 3 5xx (timed and untimed)", first)
	}
	if first.P50 == nil || first.P90 == nil || first.P95 == nil || first.P99 != nil {
		t.Fatalf("first slot percentiles come from the five valid timings only: %#v", first)
	}
	// The incomplete rows have outcome unknown, so they live in their own cohort, and a
	// slot with no valid timing still gets a point carrying its counts but no percentile.
	unknown := timelineSeriesFor(t, analysis, "unknown")
	if len(unknown.Points) != 2 {
		t.Fatalf("incomplete cohort points = %#v, want two slots", unknown.Points)
	}
	a, b := unknown.Points[0], unknown.Points[1]
	if a.Incomplete != 2 || a.Count != 0 || a.P50 != nil || a.P90 != nil || a.P95 != nil || a.P99 != nil {
		t.Fatalf("first incomplete slot = %#v", a)
	}
	if b.Incomplete != 3 || b.Count5xx != 3 || b.Count != 0 || b.P50 != nil {
		t.Fatalf("second incomplete slot = %#v", b)
	}
	for _, c := range analysis.APIs {
		if c.Outcome == "failure" && c.Current.Count != 5 {
			t.Fatalf("failure-only slots changed the cohort's valid sample count: %#v", c.Current)
		}
	}
}

func TestTimelinePointP99NilBelow100(t *testing.T) {
	q := analysisQuery()
	for _, tc := range []struct {
		n       int64
		wantP99 bool
	}{{99, false}, {100, true}} {
		analysis, err := BuildAnalysis(q, repeatedBuckets(tc.n, 12, anchor), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		p := analysis.Timeline[0].Points[0]
		if p.P90 == nil || (p.P99 != nil) != tc.wantP99 {
			t.Fatalf("n=%d: p90=%v p99=%v, want p99 present=%v", tc.n, p.P90, p.P99, tc.wantP99)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, has := fields["p99"]; has != tc.wantP99 {
			t.Fatalf("n=%d: json %s, p99 key present=%v want %v", tc.n, raw, has, tc.wantP99)
		}
		for _, key := range []string{"count4xx", "count5xx", "incomplete"} {
			if _, has := fields[key]; has {
				t.Fatalf("n=%d: zero %s must be omitted: %s", tc.n, key, raw)
			}
		}
	}
}

// completionBuckets is one asynchronous operation with 5 complete rows, 3 pending
// asynchronous rows, 1 other incomplete row and 4 invalid rows, plus a
// synchronous operation that only has invalid rows.
func completionBuckets() []Bucket {
	var buckets []Bucket
	buckets = append(buckets, makeBuckets("s1", "Payroll", "Run", "asynchronous", "success", "valid", anchor, 5, 10)...)
	pending := makeBuckets("s1", "Payroll", "Run", "asynchronous", "unknown", "incomplete", anchor, 3, 0)
	pending[0].PendingAsync = 3
	buckets = append(buckets, pending...)
	buckets = append(buckets, makeBuckets("s1", "Payroll", "Run", "asynchronous", "unknown", "incomplete", anchor.Add(10*time.Second), 1, 0)...)
	buckets = append(buckets, makeBuckets("s1", "Payroll", "Run", "asynchronous", "unknown", "invalid", anchor, 4, 0)...)
	buckets = append(buckets, makeBuckets("s1", "Payroll", "Only", "synchronous", "unknown", "invalid", anchor, 3, 0)...)
	return buckets
}

func TestCompletionRateExcludesInvalidCountsPending(t *testing.T) {
	analysis, err := BuildAnalysis(analysisQuery(), completionBuckets(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	near := func(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 1e-9 }
	var sawComplete, sawUnfinished, sawInvalidOnly bool
	for _, c := range analysis.APIs {
		s := c.Current
		switch {
		case c.Action == "Run" && c.Outcome == "success":
			sawComplete = true
			if s.Events != 5 || s.Incomplete != 0 || s.Pending != 0 || s.Invalid != 0 || !near(s.CompletionRate, 1) {
				t.Fatalf("complete cohort stats = %#v", s)
			}
		case c.Action == "Run" && c.Outcome == "unknown":
			sawUnfinished = true
			// 1 incomplete + 3 pending are unfinished; the 4 invalid rows are counted apart.
			if s.Events != 8 || s.Incomplete != 1 || s.Pending != 3 || s.Invalid != 4 || !near(s.CompletionRate, 0) {
				t.Fatalf("unfinished cohort stats = %#v rate=%v", s, s.CompletionRate)
			}
		case c.Action == "Only":
			sawInvalidOnly = true
			if s.Events != 3 || s.Invalid != 3 || s.CompletionRate != nil {
				t.Fatalf("an invalid-only cohort has no rate: %#v", s)
			}
			encoded, _ := json.Marshal(s)
			if strings.Contains(string(encoded), "completionRate") {
				t.Fatalf("a missing rate must be omitted: %s", encoded)
			}
		}
	}
	if !sawComplete || !sawUnfinished || !sawInvalidOnly {
		t.Fatalf("cohorts missing: %v %v %v", sawComplete, sawUnfinished, sawInvalidOnly)
	}
	var run, only *Rollup
	for i := range analysis.Rollups {
		switch analysis.Rollups[i].Action {
		case "Run":
			run = &analysis.Rollups[i]
		case "Only":
			only = &analysis.Rollups[i]
		}
	}
	if run == nil || only == nil {
		t.Fatalf("rollups = %#v", analysis.Rollups)
	}
	// complete / (complete + incomplete + pending) = 5 / 9, not 5 / 13: invalid rows never enter the denominator.
	if run.Completed != 5 || run.Incomplete != 1 || run.Pending != 3 || run.Invalid != 4 || !near(run.CompletionShare, 5.0/9.0) {
		t.Fatalf("rollup = %#v share=%v", run, run.CompletionShare)
	}
	if only.Invalid != 3 || only.CompletionShare != nil {
		t.Fatalf("an invalid-only operation has no rate: %#v", only)
	}
	if run.CompletionRate == nil || !near(run.CompletionRate, 5.0/13.0) {
		t.Fatalf("the existing events-based completionRate must be unchanged: %v", run.CompletionRate)
	}
}

func TestCompletionTalliesAddUpAndIgnoreStrayPendingFlags(t *testing.T) {
	// PendingAsync on a bucket that is not incomplete says nothing about completion.
	valid := repeatedBuckets(4, 2, anchor)
	valid[0].PendingAsync = 2
	analysis, err := BuildAnalysis(analysisQuery(), append(valid, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 1, 0)...), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range analysis.APIs {
		s := c.Current
		if s.Count+s.Incomplete+s.Pending+s.Invalid != s.Events {
			t.Fatalf("tallies do not add up to events: %#v", s)
		}
		if c.Outcome == "success" && s.Pending != 0 {
			t.Fatalf("a stray pendingAsync on a valid bucket became pending: %#v", s)
		}
	}
}
