package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// Measurement only (performance review F6, task T-E). Every value below is
// synthetic. The payload is built directly as an audit.Analysis, not through
// BuildAnalysis, so the summary-row limit does not decide the size. Run with
// -v to see the table:
//
//	go test ./internal/store -run TestAnalysisPayloadSizeByCohorts -v

const (
	sizeBenchSlots       = 120 // timeline points per cohort
	sizeBenchSlotSeconds = 10
	sizeBenchBins        = 30 // non-empty histogram bins per stats block (assumed)
	sizeBenchSamples     = 5  // encode timings per cohort count; the median is reported
)

var sizeBenchCohortCounts = []int{10, 100, 300, 600}

func sizeBenchFloat(v float64) *float64 { return &v }

func sizeBenchQuantile(lower, value float64) *audit.Quantile {
	upper := value * 1.1
	return &audit.Quantile{LowerMs: lower, UpperMs: &upper, ValueMs: &value}
}

func sizeBenchStats(count int64, base float64) audit.Stats {
	bins := make([]audit.BinCount, 0, sizeBenchBins)
	for i := 0; i < sizeBenchBins; i++ {
		bins = append(bins, audit.BinCount{Bin: 40 + i, Count: count/sizeBenchBins + 1})
	}
	return audit.Stats{
		Count:     count,
		MeanMs:    sizeBenchFloat(base * 1.2),
		MinMs:     sizeBenchFloat(base * 0.3),
		MaxMs:     sizeBenchFloat(base * 9),
		P50:       sizeBenchQuantile(base*0.9, base),
		P90:       sizeBenchQuantile(base*1.6, base*1.7),
		P95:       sizeBenchQuantile(base*2.1, base*2.4),
		Histogram: bins,
	}
}

// sizeBenchAnalysis returns a synthetic analysis with one API cohort per
// cohort, each with sizeBenchSlots timeline points and one request-volume row
// per slot, which is the cohort-times-slot scaling the F6 finding is about.
func sizeBenchAnalysis(cohorts int) audit.Analysis {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	end := start.Add(sizeBenchSlots * sizeBenchSlotSeconds * time.Second)
	analysis := audit.Analysis{
		Query: audit.Query{
			SourceIDs:     []string{"src-1"},
			Start:         start,
			End:           end,
			BaselineStart: start.Add(-end.Sub(start)),
			BaselineEnd:   start,
			Kind:          "synchronous",
			Outcome:       "all",
			Metric:        "duration",
			BucketSeconds: sizeBenchSlotSeconds,
		},
		APIs:          make([]audit.Comparison, 0, cohorts),
		Timeline:      make([]audit.Series, 0, cohorts),
		RequestVolume: make([]audit.VolumeBucket, 0, cohorts*sizeBenchSlots),
		Sources:       []audit.SourceResult{{SourceID: "src-1", OK: true, Version: "synthetic"}},
		Warnings:      []string{},
	}
	for c := 0; c < cohorts; c++ {
		controller := fmt.Sprintf("Controller%03d", c%60)
		action := fmt.Sprintf("Action%05d", c)
		key := fmt.Sprintf("src-1|%s|%s|synchronous|success", controller, action)
		base := 20 + float64(c%37)*3.5
		points := make([]audit.Point, 0, sizeBenchSlots)
		var total int64
		for s := 0; s < sizeBenchSlots; s++ {
			at := start.Add(time.Duration(s*sizeBenchSlotSeconds) * time.Second)
			count := int64(20 + (s*7+c)%40)
			total += count
			p95 := base*2.4 + float64((s*13+c)%11)
			points = append(points, audit.Point{
				At:         at,
				Count:      count,
				P50:        sizeBenchQuantile(base*0.9, base+float64(s%5)),
				P95:        sizeBenchQuantile(p95*0.9, p95),
				P95Ratio:   sizeBenchFloat(p95 / (base * 2.4)),
				P90:        sizeBenchQuantile(base*1.6, base*1.7),
				Count4xx:   int64(s % 3),
				Incomplete: int64(s % 2),
			})
			analysis.RequestVolume = append(analysis.RequestVolume, audit.VolumeBucket{Controller: controller, Action: action, At: at, Count: count})
		}
		analysis.Timeline = append(analysis.Timeline, audit.Series{Key: key, Points: points, LoadCorrelation: sizeBenchFloat(0.12)})
		analysis.APIs = append(analysis.APIs, audit.Comparison{
			Key:             key,
			SourceID:        "src-1",
			Controller:      controller,
			Action:          action,
			Kind:            "synchronous",
			Outcome:         "success",
			State:           "compared",
			CurrentEvents:   total,
			ReferenceEvents: total,
			Current:         sizeBenchStats(total, base),
			Reference:       sizeBenchStats(total, base*0.95),
			MedianRatio:     sizeBenchFloat(1.05),
			P95Ratio:        sizeBenchFloat(1.02),
			MedianDeltaMs:   sizeBenchFloat(base * 0.05),
			P95DeltaMs:      sizeBenchFloat(base * 0.1),
			TotalTimeMs:     float64(total) * base,
		})
	}
	return analysis
}

// sizeBenchEncodedBytes returns the JSON size of a synthetic analysis with n cohorts.
func sizeBenchEncodedBytes(t *testing.T, n int) int {
	t.Helper()
	encoded, err := json.Marshal(sizeBenchAnalysis(n))
	if err != nil {
		t.Fatalf("encode %d cohorts: %v", n, err)
	}
	return len(encoded)
}

func TestAnalysisPayloadSizeByCohorts(t *testing.T) {
	type row struct {
		cohorts int
		bytes   int
		encode  time.Duration
	}
	rows := make([]row, 0, len(sizeBenchCohortCounts))
	for _, n := range sizeBenchCohortCounts {
		analysis := sizeBenchAnalysis(n)
		encoded, err := json.Marshal(analysis)
		if err != nil {
			t.Fatalf("encode %d cohorts: %v", n, err)
		}
		samples := make([]time.Duration, 0, sizeBenchSamples)
		for i := 0; i < sizeBenchSamples; i++ {
			began := time.Now()
			if _, err := json.Marshal(analysis); err != nil {
				t.Fatalf("encode %d cohorts: %v", n, err)
			}
			samples = append(samples, time.Since(began))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		median := samples[len(samples)/2]
		rows = append(rows, row{cohorts: n, bytes: len(encoded), encode: median})
		t.Logf("cohorts=%4d slots=%d json_bytes=%10d (%5.2f MiB, %5.1f%% of 16 MiB) encode_median=%v",
			n, sizeBenchSlots, len(encoded), float64(len(encoded))/(1<<20), 100*float64(len(encoded))/float64(maxAnalysisBytes), median)
	}

	for i := 1; i < len(rows); i++ {
		if rows[i].bytes <= rows[i-1].bytes {
			t.Fatalf("payload did not grow from %d to %d cohorts (%d -> %d bytes)", rows[i-1].cohorts, rows[i].cohorts, rows[i-1].bytes, rows[i].bytes)
		}
	}

	// Linear fit through the smallest and largest measured counts.
	first, last := rows[0], rows[len(rows)-1]
	slope := float64(last.bytes-first.bytes) / float64(last.cohorts-first.cohorts)
	intercept := float64(first.bytes) - slope*float64(first.cohorts)
	t.Logf("fit: %.0f bytes per cohort (%.1f per cohort-slot point), fixed part %.0f bytes", slope, slope/sizeBenchSlots, intercept)

	// Cohort count at which the encoded payload reaches the 16 MiB cap. Inside
	// the measured range, bisect on real encodes. Beyond it, take the fit and
	// check it by encoding at the predicted count and one more.
	capBytes := maxAnalysisBytes
	var atCap int
	if last.bytes >= capBytes {
		lo, hi := first.cohorts, last.cohorts // size(lo) < cap <= size(hi)
		for hi-lo > 1 {
			mid := (lo + hi) / 2
			if sizeBenchEncodedBytes(t, mid) >= capBytes {
				hi = mid
			} else {
				lo = mid
			}
		}
		atCap = hi
		t.Logf("16 MiB reached at %d cohorts (measured by bisection: %d bytes at %d, %d bytes at %d)",
			atCap, sizeBenchEncodedBytes(t, atCap-1), atCap-1, sizeBenchEncodedBytes(t, atCap), atCap)
	} else {
		predicted := int((float64(capBytes) - intercept) / slope)
		t.Logf("16 MiB not reached within %d cohorts; linear fit predicts %d cohorts", last.cohorts, predicted)
		t.Logf("checked: %d cohorts = %d bytes, %d cohorts = %d bytes (cap %d)",
			predicted, sizeBenchEncodedBytes(t, predicted), predicted+1, sizeBenchEncodedBytes(t, predicted+1), capBytes)
		atCap = predicted
	}
	capAnalysis := sizeBenchAnalysis(atCap)
	capSamples := make([]time.Duration, 0, sizeBenchSamples)
	for i := 0; i < sizeBenchSamples; i++ {
		began := time.Now()
		if _, err := json.Marshal(capAnalysis); err != nil {
			t.Fatalf("encode %d cohorts: %v", atCap, err)
		}
		capSamples = append(capSamples, time.Since(began))
	}
	sort.Slice(capSamples, func(i, j int) bool { return capSamples[i] < capSamples[j] })
	t.Logf("encode median at %d cohorts (cap): %v", atCap, capSamples[len(capSamples)/2])
	t.Logf("cohort count at 16 MiB: %d (synthetic, 120 slots, %d bytes per cohort)", atCap, int(slope))

	// Timeline only, for comparison with the live timeline measurement in F6.
	timeline, err := json.Marshal(sizeBenchAnalysis(100).Timeline)
	if err != nil {
		t.Fatalf("encode timeline: %v", err)
	}
	t.Logf("timeline only: %d bytes for 100 cohorts x %d slots = %.0f bytes per point", len(timeline), sizeBenchSlots, float64(len(timeline))/float64(100*sizeBenchSlots))
}
