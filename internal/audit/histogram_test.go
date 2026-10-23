package audit

import (
	"math"
	"testing"
)

func TestBinIndexBoundaries(t *testing.T) {
	cases := []struct {
		value float64
		want  int
	}{
		{0, 0}, {0.999, 0}, {1, 1}, {1.099999, 1}, {1.1, 2}, {math.Pow(1.1, 192), 193}, {math.MaxFloat64, 193},
	}
	for _, tc := range cases {
		if got := BinIndex(tc.value); got != tc.want {
			t.Errorf("BinIndex(%g)=%d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestBinIndexRejectsInvalidDurations(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := BinIndex(value); got != -1 {
			t.Errorf("BinIndex(%g)=%d, want -1", value, got)
		}
	}
}

func TestBinBounds(t *testing.T) {
	lower, upper := BinBounds(0)
	if lower != 0 || upper == nil || *upper != 1 {
		t.Fatalf("bin0 bounds = %v,%v", lower, upper)
	}
	lower, upper = BinBounds(1)
	if lower != 1 || upper == nil || math.Abs(*upper-1.1) > 1e-12 {
		t.Fatalf("bin1 bounds = %v,%v", lower, upper)
	}
	lower, upper = BinBounds(193)
	if math.Abs(lower-math.Pow(1.1, 192)) > 1e-12 || upper != nil {
		t.Fatalf("overflow bounds = %v,%v", lower, upper)
	}
	lower, upper = BinBounds(-1)
	if !math.IsNaN(lower) || upper != nil {
		t.Fatalf("invalid bin bounds = %v,%v", lower, upper)
	}
}

func TestHistogramEmptyHasNullStatistics(t *testing.T) {
	stats := newHistogramAccumulator().stats()
	if stats.Count != 0 || stats.MeanMs != nil || stats.MinMs != nil || stats.MaxMs != nil || stats.P50 != nil || stats.P95 != nil {
		t.Fatalf("empty stats should be null: %#v", stats)
	}
}

func TestHistogramNearestRankQuantileIntervals(t *testing.T) {
	h := newHistogramAccumulator()
	if err := h.add(BinIndex(1.05), 2, 2.1, 1.02, 1.08); err != nil {
		t.Fatal(err)
	}
	if err := h.add(BinIndex(2), 2, 4, 2, 2); err != nil {
		t.Fatal(err)
	}
	stats := h.stats()
	if stats.Count != 4 || stats.P50 == nil || math.Abs(stats.P50.LowerMs-1.02) > 1e-12 || stats.P50.UpperMs == nil || math.Abs(*stats.P50.UpperMs-1.1) > 1e-12 {
		t.Fatalf("p50 interval = %#v", stats.P50)
	}
	if stats.P95 == nil || math.Abs(stats.P95.LowerMs-math.Pow(1.1, 7)) > 1e-12 {
		t.Fatalf("p95 interval = %#v", stats.P95)
	}
	if stats.MeanMs == nil || math.Abs(*stats.MeanMs-1.525) > 1e-12 {
		t.Fatalf("mean = %v", stats.MeanMs)
	}
}

func TestHistogramMergesCountsBeforeQuantiles(t *testing.T) {
	h := newHistogramAccumulator()
	if err := h.add(BinIndex(1.05), 19, 19*1.05, 1.05, 1.05); err != nil {
		t.Fatal(err)
	}
	if err := h.add(BinIndex(1.16), 1, 1.16, 1.16, 1.16); err != nil {
		t.Fatal(err)
	}
	stats := h.stats()
	if stats.P95 == nil || stats.P95.ValueMs == nil || math.Abs(*stats.P95.ValueMs-1.075) > 1e-12 {
		t.Fatalf("pooled p95 should be 1.05, got %#v", stats.P95)
	}
	averageOfPerSourceP95 := (1.05 + 1.155) / 2
	if *stats.P95.ValueMs == averageOfPerSourceP95 {
		t.Fatal("pooled quantile must not be an average of source quantiles")
	}
}

func TestHistogramRejectsInvalidAndOverflowingInputs(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bin           int
		count         int64
		sum, min, max float64
	}{
		{"invalid bin", 194, 1, 1, 1, 1}, {"negative count", 0, -1, 1, 0, .9}, {"invalid sum", 0, 1, math.NaN(), 0, .9}, {"invalid min", 0, 1, 1, -1, .9}, {"invalid range", 0, 1, 1, .8, .7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := newHistogramAccumulator().add(tc.bin, tc.count, tc.sum, tc.min, tc.max); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	h := newHistogramAccumulator()
	if err := h.add(0, math.MaxInt64, 1, 0, .9); err != nil {
		t.Fatal(err)
	}
	if err := h.add(0, 1, 1, 0, .9); err == nil {
		t.Fatal("expected count overflow")
	}
	h = newHistogramAccumulator()
	if err := h.add(0, 1, math.MaxFloat64, 0, .9); err != nil {
		t.Fatal(err)
	}
	if err := h.add(0, 1, math.MaxFloat64, 0, .9); err == nil {
		t.Fatal("expected sum overflow")
	}
}
