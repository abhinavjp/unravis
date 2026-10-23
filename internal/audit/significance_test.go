package audit

import (
	"math"
	"testing"
	"time"
)

// statsFromSamples builds Stats the same way the analysis does, from raw
// millisecond values.
func statsFromSamples(values ...float64) Stats {
	h := newHistogramAccumulator()
	for _, ms := range values {
		if err := h.add(BinIndex(ms), 1, ms, ms, ms); err != nil {
			panic(err)
		}
	}
	return h.stats()
}

// spread returns n values evenly covering [lo, hi].
func spread(n int, lo, hi float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = lo + (hi-lo)*float64(i)/float64(n-1)
	}
	return out
}

func TestShiftZIdenticalHistogramsIsZero(t *testing.T) {
	values := spread(60, 40, 60)
	z := HistogramShiftZ(statsFromSamples(values...), statsFromSamples(values...))
	if z == nil {
		t.Fatal("identical histograms with enough samples must yield a z-score")
	}
	if math.Abs(*z) > 1e-9 {
		t.Fatalf("z = %v, want 0", *z)
	}
}

func TestShiftZClearShiftExceedsThree(t *testing.T) {
	reference := statsFromSamples(spread(125, 40, 60)...)
	current := statsFromSamples(spread(125, 230, 270)...)
	z := HistogramShiftZ(current, reference)
	if z == nil || *z < 3 {
		t.Fatalf("z = %v, want >= 3 for 50 ms vs 250 ms", z)
	}
	// The sign flips when the windows swap: positive means current is slower.
	back := HistogramShiftZ(reference, current)
	if back == nil || math.Abs(*back+*z) > 1e-9 {
		t.Fatalf("swapped z = %v, want %v", back, -*z)
	}
}

func TestShiftZNilBelowMinSamples(t *testing.T) {
	enough := statsFromSamples(spread(20, 40, 60)...)
	few := statsFromSamples(spread(19, 230, 270)...)
	if z := HistogramShiftZ(few, enough); z != nil {
		t.Fatalf("19 current samples gave z = %v, want nil", *z)
	}
	if z := HistogramShiftZ(enough, few); z != nil {
		t.Fatalf("19 reference samples gave z = %v, want nil", *z)
	}
	// Everything in a single bin leaves no variance to normalize by.
	same := statsFromSamples(spread(30, 20.0, 20.0)...)
	if z := HistogramShiftZ(same, same); z != nil {
		t.Fatalf("single-bin histograms gave z = %v, want nil", *z)
	}
}

func TestImpactRanksVolumeTimesDelta(t *testing.T) {
	q := analysisQuery()
	refAt := anchor.Add(-time.Minute)
	var current, reference []Bucket
	// A: +200 ms across 1000 requests. B: +500 ms across only 20 requests.
	current = append(current, makeBuckets("s1", "A", "Run", "synchronous", "success", "valid", anchor, 1000, 210)...)
	reference = append(reference, makeBuckets("s1", "A", "Run", "synchronous", "success", "valid", refAt, 1000, 10)...)
	current = append(current, makeBuckets("s1", "B", "Run", "synchronous", "success", "valid", anchor, 20, 510)...)
	reference = append(reference, makeBuckets("s1", "B", "Run", "synchronous", "success", "valid", refAt, 20, 10)...)
	// C: huge delta but only 2 requests, so the comparison is insufficient_data.
	current = append(current, makeBuckets("s1", "C", "Run", "synchronous", "success", "valid", anchor, 2, 5010)...)
	reference = append(reference, makeBuckets("s1", "C", "Run", "synchronous", "success", "valid", refAt, 20, 10)...)
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	byController := map[string]Comparison{}
	for _, c := range analysis.APIs {
		byController[c.Controller] = c
	}
	a, b, c := byController["A"], byController["B"], byController["C"]
	if a.ImpactMs == nil || math.Abs(*a.ImpactMs-200*1000) > 1e-6 {
		t.Fatalf("A impact = %v, want 200000", a.ImpactMs)
	}
	if b.ImpactMs == nil || math.Abs(*b.ImpactMs-500*20) > 1e-6 {
		t.Fatalf("B impact = %v, want 10000", b.ImpactMs)
	}
	if *a.ImpactMs <= *b.ImpactMs {
		t.Fatalf("volume x delta must rank A (%v) above B (%v)", *a.ImpactMs, *b.ImpactMs)
	}
	if b.P95DeltaMs == nil || a.P95DeltaMs == nil || *b.P95DeltaMs <= *a.P95DeltaMs {
		t.Fatal("B should have the larger per-request delta, otherwise the test proves nothing")
	}
	if c.State != "insufficient_data" || c.ImpactMs != nil || c.ShiftZ != nil || c.Significant {
		t.Fatalf("uncompared group must carry no impact, z or significance: %#v", c)
	}
	if math.Abs(a.TotalTimeMs-210*1000) > 1e-6 || math.Abs(c.TotalTimeMs-5010*2) > 1e-6 {
		t.Fatalf("total time = %v / %v, want current mean x count", a.TotalTimeMs, c.TotalTimeMs)
	}
}

func TestSignificantRequiresBothZAndP50ShiftBeyondOneBin(t *testing.T) {
	q := analysisQuery()
	refAt := anchor.Add(-time.Minute)
	run := func(currentMs, referenceMs float64) Comparison {
		current := makeBuckets("s1", "A", "Run", "synchronous", "success", "valid", anchor, 50, currentMs)
		reference := makeBuckets("s1", "A", "Run", "synchronous", "success", "valid", refAt, 50, referenceMs)
		analysis, err := BuildAnalysis(q, current, reference, nil)
		if err != nil {
			t.Fatal(err)
		}
		return analysis.APIs[0]
	}
	// 11.9 and 11.95 sit in adjacent bins, so z is large, but P50 moved 0.05 ms.
	tiny := run(11.95, 11.9)
	if tiny.ShiftZ == nil || *tiny.ShiftZ < 3 {
		t.Fatalf("setup: z = %v, want >= 3", tiny.ShiftZ)
	}
	if tiny.MedianDeltaMs == nil || *tiny.MedianDeltaMs >= 0.1 {
		t.Fatalf("setup: P50 shift = %v, want well inside one bin", tiny.MedianDeltaMs)
	}
	if tiny.Significant {
		t.Fatal("a P50 shift inside one bin width must not be significant")
	}
	// 50 ms to 250 ms: large z and a large P50 shift.
	big := run(250, 50)
	if big.ShiftZ == nil || *big.ShiftZ < 3 || !big.Significant {
		t.Fatalf("clear shift: z = %v significant = %v", big.ShiftZ, big.Significant)
	}
}
