package audit

import (
	"math"
	"testing"
	"time"
)

func ptrs(values ...float64) []*float64 {
	out := make([]*float64, len(values))
	for i := range values {
		v := values[i]
		out[i] = &v
	}
	return out
}

func TestEstimateConcurrencyLittlesLaw(t *testing.T) {
	// 60 requests x 1000 ms of work in a 60 s bucket keeps one request in flight on average.
	buckets := []Bucket{
		{At: anchor, Count: 60, SumMs: 60 * 1000},
		{At: anchor.Add(time.Minute), Count: 30, SumMs: 30 * 1000},
	}
	got := EstimateConcurrency(buckets, 60)
	if len(got) != 2 {
		t.Fatalf("got %d values, want 2", len(got))
	}
	if math.Abs(got[0]-1.0) > 1e-9 || math.Abs(got[1]-0.5) > 1e-9 {
		t.Fatalf("concurrency = %v, want [1 0.5]", got)
	}
	if EstimateConcurrency(buckets, 0) != nil {
		t.Fatal("a non-positive bucket width has no meaningful concurrency")
	}
}

func TestSpearmanPerfectMonotonic(t *testing.T) {
	// Monotonic but wildly non-linear: Spearman must still be exactly 1.
	rho := SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, ptrs(1, 4, 9, 16, 100, 101, 5000, 5001, 1e6))
	if rho == nil || math.Abs(*rho-1.0) > 1e-9 {
		t.Fatalf("rho = %v, want 1", rho)
	}
}

func TestSpearmanPerfectNegative(t *testing.T) {
	rho := SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7, 8}, ptrs(80, 70, 60, 50, 40, 30, 20, 10))
	if rho == nil || math.Abs(*rho+1.0) > 1e-9 {
		t.Fatalf("rho = %v, want -1", rho)
	}
}

func TestSpearmanNilUnderEightPoints(t *testing.T) {
	rho := SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7}, ptrs(1, 2, 3, 4, 5, 6, 7))
	if rho != nil {
		t.Fatalf("7 points gave rho = %v, want nil", *rho)
	}
	if rho := SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7, 8}, ptrs(1, 2, 3, 4, 5, 6, 7, 8)); rho == nil {
		t.Fatal("8 points must be enough")
	}
}

func TestSpearmanDropsNilP95Pairs(t *testing.T) {
	p95 := ptrs(1, 2, 3, 4, 5, 6, 7, 8, 9)
	p95[0], p95[4] = nil, nil
	// Seven pairs remain, one short of the minimum.
	if rho := SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, p95); rho != nil {
		t.Fatalf("7 paired points gave rho = %v, want nil", *rho)
	}
	// Dropping a nil pair must not shift the alignment of the others: with the
	// pairs (2,20) (3,30) ... still perfectly ordered, rho stays 1.
	conc := []float64{9, 1, 2, 3, 4, 5, 6, 7, 8, 10}
	p := ptrs(0, 10, 20, 30, 40, 50, 60, 70, 80, 90)
	p[0] = nil
	rho := SpearmanLoad(conc, p)
	if rho == nil || math.Abs(*rho-1.0) > 1e-9 {
		t.Fatalf("rho = %v, want 1 with the nil pair dropped", rho)
	}
	if SpearmanLoad([]float64{1, 2, 3}, ptrs(1, 2)) != nil {
		t.Fatal("mismatched lengths must give nil")
	}
}

func TestSpearmanTiesUseAverageRanks(t *testing.T) {
	// x ranks: 1.5 1.5 3 4 5 6 7 8; y is strictly increasing so its ranks are 1..8.
	// Pearson of those average ranks is the hand-computed value below.
	rho := SpearmanLoad([]float64{1, 1, 2, 3, 4, 5, 6, 7}, ptrs(10, 20, 30, 40, 50, 60, 70, 80))
	xr := []float64{1.5, 1.5, 3, 4, 5, 6, 7, 8}
	yr := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	want := pearsonForTest(xr, yr)
	if rho == nil || math.Abs(*rho-want) > 1e-12 {
		t.Fatalf("rho = %v, want %v", rho, want)
	}
	if want >= 1 || want < 0.99 {
		t.Fatalf("sanity: tied data should be just below 1, got %v", want)
	}
	// Ties broken by position would give exactly 1; average ranks must not.
	if math.Abs(*rho-1) < 1e-9 {
		t.Fatal("ties were ranked by position instead of averaged")
	}
}

func TestSpearmanNilWhenEitherSideIsConstant(t *testing.T) {
	if SpearmanLoad([]float64{3, 3, 3, 3, 3, 3, 3, 3}, ptrs(1, 2, 3, 4, 5, 6, 7, 8)) != nil {
		t.Fatal("constant concurrency has no rank variance")
	}
	if SpearmanLoad([]float64{1, 2, 3, 4, 5, 6, 7, 8}, ptrs(5, 5, 5, 5, 5, 5, 5, 5)) != nil {
		t.Fatal("constant latency has no rank variance")
	}
}

func pearsonForTest(x, y []float64) float64 {
	n := float64(len(x))
	var mx, my float64
	for i := range x {
		mx += x[i] / n
		my += y[i] / n
	}
	var sxy, sxx, syy float64
	for i := range x {
		sxy += (x[i] - mx) * (y[i] - my)
		sxx += (x[i] - mx) * (x[i] - mx)
		syy += (y[i] - my) * (y[i] - my)
	}
	return sxy / math.Sqrt(sxx*syy)
}

func TestRollupErrorRateIgnoresUnknownOutcome(t *testing.T) {
	q := analysisQuery() // 1 minute current window
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 90, 10)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 10, 10)...)
	// Unknown outcomes must not dilute the rate, and must be reported in Quality.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "unknown", "valid", anchor, 50, 10)...)
	// The same API under another kind is a separate rollup (see TestRollupsNeverMergeKinds).
	current = append(current, makeBuckets("s2", "Payroll", "Run", "hangfire", "success", "valid", anchor, 100, 10)...)
	// An API with no known outcomes gets no error rate.
	current = append(current, makeBuckets("s1", "Other", "Go", "synchronous", "unknown", "valid", anchor, 5, 10)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Quality.UnknownOutcome != 55 {
		t.Fatalf("Quality.UnknownOutcome = %d, want 55", analysis.Quality.UnknownOutcome)
	}
	byName := map[string]Rollup{}
	for _, r := range analysis.Rollups {
		byName[r.Controller+"."+r.Action+"."+r.Kind] = r
	}
	run := byName["Payroll.Run.synchronous"]
	if run.Requests != 100 || run.Failures != 10 {
		t.Fatalf("Payroll.Run synchronous = %d requests / %d failures, want 100 / 10", run.Requests, run.Failures)
	}
	if run.ErrorRate == nil || math.Abs(*run.ErrorRate-0.1) > 1e-12 {
		t.Fatalf("ErrorRate = %v, want 0.1", run.ErrorRate)
	}
	if math.Abs(run.PerMinute-100) > 1e-9 {
		t.Fatalf("PerMinute = %v, want 100 over a one minute window", run.PerMinute)
	}
	if hf := byName["Payroll.Run.hangfire"]; hf.Requests != 100 || hf.Failures != 0 {
		t.Fatalf("Payroll.Run hangfire = %d requests / %d failures, want 100 / 0", hf.Requests, hf.Failures)
	}
	other, ok := byName["Other.Go.synchronous"]
	if !ok || other.Requests != 0 || other.ErrorRate != nil {
		t.Fatalf("unknown-only API must have zero requests and nil error rate: %#v", other)
	}
	for i := 1; i < len(analysis.Rollups); i++ {
		a, b := analysis.Rollups[i-1], analysis.Rollups[i]
		if a.Controller > b.Controller || (a.Controller == b.Controller && (a.Action > b.Action || (a.Action == b.Action && a.Kind >= b.Kind))) {
			t.Fatalf("rollups are not sorted: %v", analysis.Rollups)
		}
	}
}

func seriesByKey(a Analysis) map[string]Series {
	out := map[string]Series{}
	for _, s := range a.Timeline {
		out[s.Key] = s
	}
	return out
}

func TestSeriesLoadCorrelationIgnoresOwnLatency(t *testing.T) {
	q := analysisQuery()
	q.End = anchor.Add(100 * time.Second)
	// Constant arrival rate, latency rising 100 -> 500 ms, and nothing else running:
	// the load did not change, so there is nothing to correlate with.
	var current []Bucket
	for i := 0; i < 10; i++ {
		current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(time.Duration(i)*10*time.Second), 10, float64(100+i*45))...)
	}
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := seriesByKey(analysis)[CohortKey("s1", "Payroll", "Run", "synchronous", "success")]
	if run.LoadCorrelation != nil {
		t.Fatalf("a lone series with constant volume got rho = %v, want nil", *run.LoadCorrelation)
	}
}

func TestSeriesLoadCorrelationUsesOtherTrafficLoad(t *testing.T) {
	q := analysisQuery()
	q.End = anchor.Add(100 * time.Second)
	var current []Bucket
	for i := 0; i < 10; i++ {
		at := anchor.Add(time.Duration(i) * 10 * time.Second)
		// Payroll.Run keeps a constant 10 requests per bucket; its latency follows the other API's volume.
		current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", at, 10, float64(100+i*45))...)
		current = append(current, makeBuckets("s1", "Other", "Go", "synchronous", "success", "valid", at, int64(5+i*5), 500)...)
		// Calm control: its latency falls while the other API's volume rises.
		current = append(current, makeBuckets("s1", "Calm", "Run", "synchronous", "success", "valid", at, 10, float64(500-i*45))...)
	}
	// A cohort that spans too few buckets gets no correlation.
	current = append(current, makeBuckets("s1", "Few", "Run", "synchronous", "success", "valid", anchor, 5, 10)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byKey := seriesByKey(analysis)
	run := byKey[CohortKey("s1", "Payroll", "Run", "synchronous", "success")]
	if run.LoadCorrelation == nil || *run.LoadCorrelation < 0.6 {
		t.Fatalf("Payroll.Run rho = %v, want clearly positive against the other traffic's load", run.LoadCorrelation)
	}
	// Calm.Run's other traffic includes Payroll.Run and Other.Go; both are rising overall, latency falls.
	calm := byKey[CohortKey("s1", "Calm", "Run", "synchronous", "success")]
	if calm.LoadCorrelation == nil || *calm.LoadCorrelation > -0.6 {
		t.Fatalf("Calm.Run rho = %v, want clearly negative", calm.LoadCorrelation)
	}
	if few := byKey[CohortKey("s1", "Few", "Run", "synchronous", "success")]; few.LoadCorrelation != nil {
		t.Fatalf("one-bucket series got correlation %v, want nil", *few.LoadCorrelation)
	}
}

func TestBucketLoadScaleDividesByInWindowOverlap(t *testing.T) {
	start, end := anchor.Add(5*time.Second), anchor.Add(25*time.Second)
	cases := []struct {
		at   time.Time
		want float64
	}{
		{anchor, 2},                       // 5 s of a 10 s bucket fall in the window
		{anchor.Add(10 * time.Second), 1}, // fully inside
		{anchor.Add(20 * time.Second), 2}, // 5 s inside
		{anchor.Add(40 * time.Second), 1}, // outside the window: leave unscaled
	}
	for _, c := range cases {
		if got := bucketLoadScale(c.at, 10, start, end); math.Abs(got-c.want) > 1e-12 {
			t.Fatalf("scale at +%v = %v, want %v", c.at.Sub(anchor), got, c.want)
		}
	}
}

func TestRollupErrorRateNilUnderOutcomeFilter(t *testing.T) {
	q := analysisQuery()
	q.Outcome = "success"
	// The sources already applied the outcome filter, so only successes arrive; a rate of 0% would be false.
	current := makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 40, 10)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Rollups) != 1 {
		t.Fatalf("rollups = %v", analysis.Rollups)
	}
	r := analysis.Rollups[0]
	if r.ErrorRate != nil {
		t.Fatalf("ErrorRate = %v under outcome filter, want nil", *r.ErrorRate)
	}
	if r.Requests != 40 || math.Abs(r.PerMinute-40) > 1e-9 {
		t.Fatalf("throughput must still be reported: %#v", r)
	}
}

func TestSeriesLoadCorrelationConstantNoisyNeighboursIsNilAndDeterministic(t *testing.T) {
	q := analysisQuery()
	q.End = anchor.Add(100 * time.Second)
	// Constant but non-integer neighbours: subtracting a running total would leave
	// rounding residue that varies with the series' own latency and with map order.
	var current []Bucket
	for i := 0; i < 10; i++ {
		at := anchor.Add(time.Duration(i) * 10 * time.Second)
		current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", at, 10, 100.1+45.3*float64(i))...)
		current = append(current, makeBuckets("s1", "Other", "Go", "synchronous", "success", "valid", at, 7, 333.37)...)
		current = append(current, makeBuckets("s1", "Third", "Go", "synchronous", "success", "valid", at, 3, 91.13)...)
	}
	key := CohortKey("s1", "Payroll", "Run", "synchronous", "success")
	var first []*float64
	for run := 0; run < 20; run++ {
		analysis, err := BuildAnalysis(q, current, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []*float64
		for _, s := range analysis.Timeline {
			got = append(got, s.LoadCorrelation)
		}
		if p := seriesByKey(analysis)[key].LoadCorrelation; p != nil {
			t.Fatalf("run %d: constant neighbours gave rho = %v, want nil", run, *p)
		}
		if run == 0 {
			first = got
			continue
		}
		for i := range got {
			if (got[i] == nil) != (first[i] == nil) || (got[i] != nil && *got[i] != *first[i]) {
				t.Fatalf("run %d: series %d correlation differs between builds", run, i)
			}
		}
	}
}

func TestRollupSplitsClientAndServerErrorRates(t *testing.T) {
	q := analysisQuery()
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 80, 10)...)
	failures := makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 10)
	failures[0].Count4xx, failures[0].Count5xx, failures[0].CountHangfireFailed = failures[0].Count/2, failures[0].Count/4, failures[0].Count-failures[0].Count/2-failures[0].Count/4
	var fourXX, serverSide int64
	for _, b := range failures {
		fourXX += b.Count4xx
		serverSide += b.Count5xx + b.CountHangfireFailed
	}
	current = append(current, failures...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := analysis.Rollups[0]
	if r.ErrorRate == nil || r.ClientErrorRate == nil || r.ServerErrorRate == nil {
		t.Fatalf("rates missing: %#v", r)
	}
	if math.Abs(*r.ClientErrorRate-float64(fourXX)/100) > 1e-12 || math.Abs(*r.ServerErrorRate-float64(serverSide)/100) > 1e-12 {
		t.Fatalf("client/server rates = %v/%v, want %v/%v", *r.ClientErrorRate, *r.ServerErrorRate, float64(fourXX)/100, float64(serverSide)/100)
	}
	if *r.ClientErrorRate+*r.ServerErrorRate > *r.ErrorRate+1e-12 {
		t.Fatalf("4xx + 5xx rates %v exceed the merged error rate %v", *r.ClientErrorRate+*r.ServerErrorRate, *r.ErrorRate)
	}
	// Under an outcome filter the share is unknowable, exactly like ErrorRate.
	q.Outcome = "client_error"
	filtered, err := BuildAnalysis(q, failures, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := filtered.Rollups[0]; got.ErrorRate != nil || got.ClientErrorRate != nil || got.ServerErrorRate != nil {
		t.Fatalf("rates under an outcome filter = %#v, want nil", got)
	}
}

func TestRollupSplitIsNilWhenCountersDoNotCoverEveryFailure(t *testing.T) {
	q := analysisQuery()
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 80, 10)...)
	// Failure buckets without status counters (a source that predates them, or demo
	// data) still give a merged error rate, but no 4xx/5xx split: 0% would be false.
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 20, 10)...)
	// A second API whose counters cover only part of its failures is not split either.
	current = append(current, makeBuckets("s1", "Other", "Go", "synchronous", "success", "valid", anchor, 80, 10)...)
	partial := makeBuckets("s1", "Other", "Go", "synchronous", "failure", "valid", anchor, 20, 10)
	partial[0].Count4xx = 5
	current = append(current, partial...)
	// A third API is fully covered and keeps its split.
	current = append(current, makeBuckets("s1", "Third", "Go", "synchronous", "success", "valid", anchor, 80, 10)...)
	covered := makeBuckets("s1", "Third", "Go", "synchronous", "failure", "valid", anchor, 20, 10)
	covered[0].Count4xx, covered[0].Count5xx = 5, covered[0].Count-5
	current = append(current, covered...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Rollup{}
	for _, r := range analysis.Rollups {
		byName[r.Controller+"."+r.Action] = r
	}
	for _, name := range []string{"Payroll.Run", "Other.Go"} {
		r := byName[name]
		if r.ErrorRate == nil || math.Abs(*r.ErrorRate-0.2) > 1e-12 {
			t.Fatalf("%s ErrorRate = %v, want 0.2", name, r.ErrorRate)
		}
		if r.ClientErrorRate != nil || r.ServerErrorRate != nil {
			t.Errorf("%s split = %v/%v, want nil when the counters miss failures", name, r.ClientErrorRate, r.ServerErrorRate)
		}
	}
	third := byName["Third.Go"]
	if third.ClientErrorRate == nil || third.ServerErrorRate == nil || math.Abs(*third.ClientErrorRate-0.05) > 1e-12 || math.Abs(*third.ServerErrorRate-0.15) > 1e-12 {
		t.Errorf("Third.Go split = %v/%v, want 0.05/0.15", third.ClientErrorRate, third.ServerErrorRate)
	}
}

func TestBuildAnalysisRejectsInconsistentStatusCounts(t *testing.T) {
	q := analysisQuery()
	buckets := makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 10, 10)
	buckets[0].Count4xx, buckets[0].Count5xx = buckets[0].Count, 1
	if _, err := BuildAnalysis(q, buckets, nil, nil); err == nil {
		t.Fatal("4xx + 5xx above the bucket count must be rejected")
	}
	buckets[0].Count4xx, buckets[0].Count5xx = -1, 0
	if _, err := BuildAnalysis(q, buckets, nil, nil); err == nil {
		t.Fatal("negative 4xx count must be rejected")
	}
}

func TestRollupsNeverMergeKinds(t *testing.T) {
	q := analysisQuery() // kind all, 1 minute current window
	var current []Bucket
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor, 40, 10)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "failure", "valid", anchor, 10, 10)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "asynchronous", "success", "valid", anchor, 30, 10)...)
	// A second source folds into the same (controller, action, kind) rollup.
	current = append(current, makeBuckets("s2", "Payroll", "Run", "synchronous", "success", "valid", anchor, 10, 10)...)
	current = append(current, makeBuckets("s1", "Payroll", "Run", "hangfire", "success", "valid", anchor, 100, 10)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Rollups) != 3 {
		t.Fatalf("got %d rollups, want 3 (synchronous, asynchronous, hangfire): %+v", len(analysis.Rollups), analysis.Rollups)
	}
	byKind := map[string]Rollup{}
	for _, r := range analysis.Rollups {
		if r.Controller != "Payroll" || r.Action != "Run" {
			t.Fatalf("unexpected rollup %+v", r)
		}
		byKind[r.Kind] = r
	}
	if r := byKind["synchronous"]; r.Requests != 60 || r.Failures != 10 {
		t.Fatalf("synchronous = %d requests / %d failures, want 60 / 10 (sources merged)", r.Requests, r.Failures)
	}
	if r := byKind["asynchronous"]; r.Requests != 30 || r.Failures != 0 {
		t.Fatalf("asynchronous = %d requests / %d failures, want 30 / 0", r.Requests, r.Failures)
	}
	if r := byKind["hangfire"]; r.Requests != 100 {
		t.Fatalf("hangfire requests = %d, want 100", r.Requests)
	}
	for i := 1; i < len(analysis.Rollups); i++ {
		if analysis.Rollups[i-1].Kind >= analysis.Rollups[i].Kind {
			t.Fatalf("rollups of one API must be sorted by kind: %+v", analysis.Rollups)
		}
	}
}
