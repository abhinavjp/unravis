package audit

import (
	"errors"
	"math"
	"testing"
	"time"
)

func overviewByKind(t *testing.T, a Analysis) map[string]OverviewRow {
	t.Helper()
	out := map[string]OverviewRow{}
	for _, row := range a.Overview {
		if _, dup := out[row.Kind]; dup {
			t.Fatalf("kind %q appears twice in the overview: %+v", row.Kind, a.Overview)
		}
		out[row.Kind] = row
	}
	return out
}

func withClasses(buckets []Bucket, c4, c5, hf int64) []Bucket {
	for i := range buckets {
		buckets[i].Count4xx, buckets[i].Count5xx, buckets[i].CountHangfireFailed = c4, c5, hf
	}
	return buckets
}

func TestOverviewMergesHistogramsPerKindNeverAcrossKinds(t *testing.T) {
	q := analysisQuery() // one minute current window, kind all
	var current []Bucket
	// Two controllers on two sources merge into one synchronous histogram: 120 requests at 10 ms.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 60, 10)...)
	current = append(current, makeBuckets("s2", "Other", "Go", "synchronous", "success", "valid", anchor, 60, 10)...)
	// Incomplete and unusable rows are counted as events but never as fast or successful samples.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 10, 0)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "invalid", anchor, 5, 0)...)
	// A very slow kind must not drag the synchronous quantiles upward.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "hangfire", "success", "valid", anchor, 60, 5000)...)
	current = append(current, makeBuckets("s2", "Other", "Go", "hangfire", "success", "valid", anchor, 60, 5000)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "asynchronous", "success", "valid", anchor, 40, 300)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Overview) != 3 {
		t.Fatalf("overview rows = %d, want one per kind (3): %+v", len(analysis.Overview), analysis.Overview)
	}
	for i, want := range []string{"synchronous", "asynchronous", "hangfire"} {
		if analysis.Overview[i].Kind != want {
			t.Fatalf("row %d kind = %q, want %q (fixed kind order)", i, analysis.Overview[i].Kind, want)
		}
	}
	rows := overviewByKind(t, analysis)
	sync := rows["synchronous"]
	if sync.Events != 135 || sync.Completed != 120 {
		t.Fatalf("synchronous events/completed = %d/%d, want 135/120", sync.Events, sync.Completed)
	}
	if sync.CompletionRate == nil || math.Abs(*sync.CompletionRate-120.0/135.0) > 1e-12 {
		t.Fatalf("completion rate = %v, want 120/135", sync.CompletionRate)
	}
	if sync.Incomplete != 10 || sync.Invalid != 5 {
		t.Fatalf("synchronous incomplete/invalid = %d/%d, want 10/5", sync.Incomplete, sync.Invalid)
	}
	if math.Abs(sync.PerMinute-135) > 1e-9 {
		t.Fatalf("PerMinute = %v, want 135 over one minute", sync.PerMinute)
	}
	for name, got := range map[string]*Quantile{"p50": sync.P50, "p90": sync.P90, "p95": sync.P95, "p99": sync.P99} {
		if got == nil || got.UpperMs == nil || got.LowerMs > 10 || *got.UpperMs < 10 || *got.UpperMs > 11 {
			t.Fatalf("synchronous %s = %+v, want the 10 ms bin only", name, got)
		}
	}
	hangfire := rows["hangfire"]
	if hangfire.Events != 120 || hangfire.Completed != 120 || hangfire.P50 == nil || hangfire.P50.LowerMs > 5000 || hangfire.P50.UpperMs == nil || *hangfire.P50.UpperMs < 5000 || *hangfire.P50.UpperMs > 5500 {
		t.Fatalf("hangfire row = %+v, want only the 5000 ms bin", hangfire)
	}
	async := rows["asynchronous"]
	if async.Events != 40 || async.P50 == nil || async.P95 == nil {
		t.Fatalf("asynchronous row = %+v", async)
	}
	if async.P99 != nil {
		t.Fatalf("P99 below 100 samples must be nil, got %+v", async.P99)
	}
	if _, ok := rows["unknown"]; ok {
		t.Fatal("a kind with no rows must not appear")
	}
}

func TestOverviewIsBoundedByTheFourKindsAndUnknownAppearsOnlyWhenPresent(t *testing.T) {
	q := analysisQuery()
	var current []Bucket
	for _, kind := range []string{"synchronous", "asynchronous", "hangfire", "unknown"} {
		for c := 0; c < 30; c++ {
			controller := "C" + string(rune('A'+c))
			current = append(current, makeBuckets("s1", controller, "Run", kind, "success", "valid", anchor, 1, 10)...)
		}
	}
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Overview) != 4 || analysis.Overview[3].Kind != "unknown" {
		t.Fatalf("overview = %+v, want exactly four rows ending with unknown", analysis.Overview)
	}
	bad := makeBuckets("s1", "Payroll", "Run", "mystery", "success", "valid", anchor, 1, 10)
	if _, err := BuildAnalysis(q, bad, nil, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an unrecognised kind must be rejected, got %v", err)
	}
}

func TestOverviewErrorRatesAreSharesOfEventsWithAKnownResult(t *testing.T) {
	q := analysisQuery()
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 80, 10)...)
	current = append(current, withClasses(makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 10), 15, 5, 0)...)
	// Unknown outcomes sit outside both numerator and denominator.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 100, 0)...)
	// Failed Hangfire jobs count as server errors.
	current = append(current, makeBuckets("s1", "Jobs", "Nightly", "hangfire", "success", "valid", anchor, 90, 10)...)
	current = append(current, withClasses(makeBuckets("s1", "Jobs", "Nightly", "hangfire", "failure", "valid", anchor, 10, 10), 0, 0, 10)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := overviewByKind(t, analysis)
	sync := rows["synchronous"]
	if sync.ClientErrorRate == nil || math.Abs(*sync.ClientErrorRate-0.15) > 1e-12 || sync.ServerErrorRate == nil || math.Abs(*sync.ServerErrorRate-0.05) > 1e-12 {
		t.Fatalf("synchronous 4xx/5xx = %v/%v, want 0.15/0.05 of the 100 known results", sync.ClientErrorRate, sync.ServerErrorRate)
	}
	if sync.ClientErrors != 15 || sync.ServerErrors != 5 || sync.UnknownOutcome != 100 {
		t.Fatalf("counts = %d/%d, unknown %d", sync.ClientErrors, sync.ServerErrors, sync.UnknownOutcome)
	}
	hf := rows["hangfire"]
	if hf.ClientErrorRate == nil || *hf.ClientErrorRate != 0 || hf.ServerErrorRate == nil || math.Abs(*hf.ServerErrorRate-0.1) > 1e-12 {
		t.Fatalf("hangfire 4xx/5xx = %v/%v, want 0/0.1", hf.ClientErrorRate, hf.ServerErrorRate)
	}

	// Without status counters covering every failure, a rate of 0 would be false: it must be nil.
	uncovered := append(makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 80, 10), makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 10)...)
	analysis, err = BuildAnalysis(q, uncovered, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row := overviewByKind(t, analysis)["synchronous"]; row.ClientErrorRate != nil || row.ServerErrorRate != nil {
		t.Fatalf("uncovered failures must leave the split nil: %+v", row)
	}
	// Under a status filter the sources drop the other outcomes, so no share is knowable.
	q.Outcome = "client_error"
	analysis, err = BuildAnalysis(q, withClasses(makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 10), 20, 0, 0), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row := overviewByKind(t, analysis)["synchronous"]; row.ClientErrorRate != nil || row.ServerErrorRate != nil {
		t.Fatalf("a status filter must leave the rates nil: %+v", row)
	}
}

func TestOverviewDeltasNilWhenReferenceInsufficient(t *testing.T) {
	q := analysisQuery()
	current := repeatedBuckets(40, 20, anchor)
	reference := repeatedBuckets(19, 10, anchor.Add(-time.Minute))
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := overviewByKind(t, analysis)["synchronous"]
	if row.State != "insufficient_data" {
		t.Fatalf("state = %q, want insufficient_data below 20 reference samples", row.State)
	}
	if row.P95 == nil {
		t.Fatal("current values stay visible")
	}
	ref, d := row.Reference, row.Delta
	if ref.Events != 19 || ref.Completed != 19 {
		t.Fatalf("raw reference counts stay available: %+v", ref)
	}
	for name, v := range map[string]*float64{"completion": ref.CompletionRate, "4xx": ref.ClientErrorRate, "5xx": ref.ServerErrorRate,
		"traffic delta": d.Traffic, "completion delta": d.CompletionRate, "4xx delta": d.ClientErrorRate, "5xx delta": d.ServerErrorRate,
		"p50 delta": d.P50Ms, "p90 delta": d.P90Ms, "p95 delta": d.P95Ms, "p99 delta": d.P99Ms} {
		if v != nil {
			t.Fatalf("%s = %v, want nil below 20 reference samples", name, *v)
		}
	}
	if ref.P50 != nil || ref.P90 != nil || ref.P95 != nil || ref.P99 != nil {
		t.Fatalf("reference quantiles must be nil below 20 samples: %+v", ref)
	}

	// Exactly 20 reference samples is enough.
	analysis, err = BuildAnalysis(q, current, repeatedBuckets(20, 10, anchor.Add(-time.Minute)), nil)
	if err != nil {
		t.Fatal(err)
	}
	row = overviewByKind(t, analysis)["synchronous"]
	if row.State != "compared" {
		t.Fatalf("state = %q, want compared", row.State)
	}
	if row.Reference.P95 == nil || row.Delta.P95Ms == nil || *row.Delta.P95Ms <= 0 {
		t.Fatalf("P95 delta must be positive when current is slower: %+v", row.Delta)
	}
	if row.Delta.Traffic == nil || math.Abs(*row.Delta.Traffic-1) > 1e-12 { // 40/min vs 20/min
		t.Fatalf("traffic delta = %v, want +100%%", row.Delta.Traffic)
	}
	if row.Delta.CompletionRate == nil || *row.Delta.CompletionRate != 0 {
		t.Fatalf("completion delta = %v, want 0", row.Delta.CompletionRate)
	}
}

func TestOverviewStateNamesMissingAndSparseWindows(t *testing.T) {
	q := analysisQuery()
	for _, tc := range []struct {
		name, want         string
		current, reference []Bucket
	}{
		{"no reference", "missing_reference", repeatedBuckets(30, 2, anchor), nil},
		{"no current", "missing_current", nil, repeatedBuckets(30, 2, anchor.Add(-time.Minute))},
		{"sparse current", "insufficient_data", repeatedBuckets(19, 2, anchor), repeatedBuckets(30, 2, anchor.Add(-time.Minute))},
		{"incomplete only", "missing_current", makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 50, 0), repeatedBuckets(30, 2, anchor.Add(-time.Minute))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysis, err := BuildAnalysis(q, tc.current, tc.reference, nil)
			if err != nil {
				t.Fatal(err)
			}
			row := overviewByKind(t, analysis)["synchronous"]
			if row.State != tc.want {
				t.Fatalf("state = %q, want %q", row.State, tc.want)
			}
			if tc.want == "missing_current" && (row.P50 != nil || row.P95 != nil || row.Delta.P95Ms != nil) {
				t.Fatalf("a window with no valid samples has no latency: %+v", row)
			}
		})
	}
}

func TestRollupsCarryReferenceValuesAndDeltas(t *testing.T) {
	q := analysisQuery() // equal one-minute windows
	var current, reference []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 80, 20)...)
	current = append(current, withClasses(makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 20), 10, 10, 0)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "incomplete", anchor, 25, 0)...)
	reference = append(reference, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(-time.Minute), 45, 10)...)
	reference = append(reference, withClasses(makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor.Add(-time.Minute), 5, 10), 5, 0, 0)...)
	// An operation that stopped running keeps a reference-only rollup.
	reference = append(reference, makeBuckets("s1", "Gone", "Away", "synchronous", "success", "valid", anchor.Add(-time.Minute), 30, 10)...)
	// Too little reference traffic: no deltas, raw counts remain.
	current = append(current, makeBuckets("s1", "Rare", "Op", "synchronous", "success", "valid", anchor, 40, 10)...)
	reference = append(reference, makeBuckets("s1", "Rare", "Op", "synchronous", "success", "valid", anchor.Add(-time.Minute), 5, 10)...)
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Rollup{}
	for i, r := range analysis.Rollups {
		byName[r.Controller+"."+r.Action+"."+r.Kind] = r
		if i > 0 {
			a, b := analysis.Rollups[i-1], r
			if a.Controller > b.Controller || (a.Controller == b.Controller && (a.Action > b.Action || (a.Action == b.Action && a.Kind >= b.Kind))) {
				t.Fatalf("rollups are not sorted: %+v", analysis.Rollups)
			}
		}
	}
	run := byName["Payroll.Run.synchronous"]
	if run.Requests != 100 || run.Events != 125 || run.Completed != 100 {
		t.Fatalf("current counts = %d requests / %d events / %d completed", run.Requests, run.Events, run.Completed)
	}
	if run.ReferenceRequests != 50 || run.ReferenceFailures != 5 || run.ReferenceEvents != 50 || run.ReferenceCompleted != 50 {
		t.Fatalf("reference counts = %+v", run)
	}
	if run.CompletionRate == nil || math.Abs(*run.CompletionRate-0.8) > 1e-12 || run.ReferenceCompletionRate == nil || *run.ReferenceCompletionRate != 1 {
		t.Fatalf("completion = %v / reference %v, want 0.8 / 1", run.CompletionRate, run.ReferenceCompletionRate)
	}
	if run.CompletionDelta == nil || math.Abs(*run.CompletionDelta+0.2) > 1e-12 {
		t.Fatalf("completion delta = %v, want -0.2", run.CompletionDelta)
	}
	if run.TrafficDelta == nil || math.Abs(*run.TrafficDelta-1.5) > 1e-12 { // 125/min vs 50/min
		t.Fatalf("traffic delta = %v, want +150%% of events per minute", run.TrafficDelta)
	}
	if run.ReferenceClientErrorRate == nil || math.Abs(*run.ReferenceClientErrorRate-0.1) > 1e-12 || run.ClientErrorDelta == nil || math.Abs(*run.ClientErrorDelta) > 1e-12 {
		t.Fatalf("client error reference/delta = %v/%v", run.ReferenceClientErrorRate, run.ClientErrorDelta)
	}
	if run.ServerErrorDelta == nil || math.Abs(*run.ServerErrorDelta-0.1) > 1e-12 {
		t.Fatalf("server error delta = %v, want 0.10 - 0.00", run.ServerErrorDelta)
	}
	if run.P95Ms == nil || run.ReferenceP95Ms == nil || run.P95DeltaMs == nil || *run.P95DeltaMs <= 0 {
		t.Fatalf("P95 values %v / %v / %v", run.P95Ms, run.ReferenceP95Ms, run.P95DeltaMs)
	}

	gone, ok := byName["Gone.Away.synchronous"]
	if !ok || gone.Requests != 0 || gone.ReferenceRequests != 30 || gone.TrafficDelta == nil || *gone.TrafficDelta != -1 {
		t.Fatalf("a stopped operation must show a -100%% traffic delta: %+v", gone)
	}
	rare := byName["Rare.Op.synchronous"]
	if rare.ReferenceRequests != 5 || rare.TrafficDelta != nil || rare.CompletionDelta != nil || rare.P95DeltaMs != nil || rare.ReferenceP95Ms != nil || rare.ReferenceCompletionRate != nil {
		t.Fatalf("fewer than 20 reference samples must leave deltas and reference values nil: %+v", rare)
	}
}

func TestRollupDeltasStayNilUnderStatusFilter(t *testing.T) {
	q := analysisQuery()
	q.Outcome = "success"
	current := makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 40, 10)
	reference := makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(-time.Minute), 40, 10)
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := analysis.Rollups[0]
	if r.ReferenceErrorRate != nil || r.ReferenceClientErrorRate != nil || r.ClientErrorDelta != nil || r.ServerErrorDelta != nil {
		t.Fatalf("error shares are unknowable under an outcome filter: %+v", r)
	}
	if r.TrafficDelta == nil || *r.TrafficDelta != 0 {
		t.Fatalf("traffic delta = %v, want 0", r.TrafficDelta)
	}
}
