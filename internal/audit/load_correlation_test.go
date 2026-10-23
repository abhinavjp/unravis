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
	// A second source and kind for the same API fold into one rollup.
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
		byName[r.Controller+"."+r.Action] = r
	}
	run := byName["Payroll.Run"]
	if run.Requests != 200 || run.Failures != 10 {
		t.Fatalf("Payroll.Run = %d requests / %d failures, want 200 / 10", run.Requests, run.Failures)
	}
	if run.ErrorRate == nil || math.Abs(*run.ErrorRate-0.05) > 1e-12 {
		t.Fatalf("ErrorRate = %v, want 0.05", run.ErrorRate)
	}
	if math.Abs(run.PerMinute-200) > 1e-9 {
		t.Fatalf("PerMinute = %v, want 200 over a one minute window", run.PerMinute)
	}
	other, ok := byName["Other.Go"]
	if !ok || other.Requests != 0 || other.ErrorRate != nil {
		t.Fatalf("unknown-only API must have zero requests and nil error rate: %#v", other)
	}
	for i := 1; i < len(analysis.Rollups); i++ {
		a, b := analysis.Rollups[i-1], analysis.Rollups[i]
		if a.Controller > b.Controller || (a.Controller == b.Controller && a.Action >= b.Action) {
			t.Fatalf("rollups are not sorted: %v", analysis.Rollups)
		}
	}
}

func TestSeriesLoadCorrelationWiredFromBucketSums(t *testing.T) {
	q := analysisQuery()
	q.End = anchor.Add(100 * time.Second)
	// Ten 10 s buckets where more work per bucket goes with a higher latency.
	var current []Bucket
	for i := 0; i < 10; i++ {
		current = append(current, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", "valid", anchor.Add(time.Duration(i)*10*time.Second), int64(i+1), float64(10*(i+1)))...)
	}
	// A cohort that spans too few buckets gets no correlation.
	current = append(current, makeBuckets("s1", "Few", "Run", "synchronous", "success", "valid", anchor, 5, 10)...)
	analysis, err := BuildAnalysis(q, current, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Series{}
	for _, s := range analysis.Timeline {
		byKey[s.Key] = s
	}
	run := byKey[CohortKey("s1", "Payroll", "Run", "synchronous", "success")]
	if run.LoadCorrelation == nil || math.Abs(*run.LoadCorrelation-1.0) > 1e-9 {
		t.Fatalf("LoadCorrelation = %v, want 1", run.LoadCorrelation)
	}
	if few := byKey[CohortKey("s1", "Few", "Run", "synchronous", "success")]; few.LoadCorrelation != nil {
		t.Fatalf("one-bucket series got correlation %v, want nil", *few.LoadCorrelation)
	}
}
