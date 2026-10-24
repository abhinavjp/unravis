package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func estimateFixtureBuckets() []audit.Bucket {
	at := time.Date(2026, 3, 9, 12, 0, 0, 123456789, time.UTC)
	max := math.MaxFloat64
	worst := audit.Bucket{
		SourceID: strings.Repeat("<", 40), Controller: strings.Repeat("\"\\&", 20), Action: "caf\u00e9   \x00\xff \u2028\u2029 \ufffd",
		Kind: strings.Repeat("\n", 5), Outcome: "o", State: "s",
		At: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), Bin: math.MinInt,
		Count: math.MinInt64, SumMs: -max, MinMs: -max, MaxMs: -max,
		SumAuditMs: -max, SumApiMs: -max, SumPayloadMs: -max,
		NAudit: math.MinInt64, NApi: math.MinInt64, NPayload: math.MinInt64,
		PendingAsync: math.MinInt64, ObservedPartial: math.MinInt64,
		Count4xx: math.MinInt64, Count5xx: math.MinInt64, CountHangfireFailed: math.MinInt64,
	}
	// Negative values just inside [1e-6, 1e-5) are the widest plain floats (25
	// bytes); just below 1e-6 and at 1e21 the encoder switches to exponent form.
	tiny := audit.Bucket{
		SourceID: "s", At: at, Count: 1,
		SumMs: -1.2345678901234567e-6, MinMs: -9.999999999999999e-6, MaxMs: -1e-6,
		SumAuditMs: -9.99999999999999e-7, SumApiMs: -1e-7, SumPayloadMs: -1e-300,
	}
	edges := audit.Bucket{
		SourceID: "s", At: at, Count: 1,
		SumMs: 1e21, MinMs: -1e21, MaxMs: 999999999999999900000,
		SumAuditMs: 1e-6, SumApiMs: 5e-324, SumPayloadMs: -123456789012345678901,
	}
	return []audit.Bucket{
		{},
		{At: at, Count: 1},
		{SourceID: "src-1", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: 12, Count: 37, SumMs: 1234.5678, MinMs: 1.5, MaxMs: 250.25, SumAuditMs: 3.25, NAudit: 4, Count5xx: 2},
		{SourceID: "s", At: at, Count: math.MaxInt64, SumMs: math.SmallestNonzeroFloat64, MinMs: math.Copysign(0, -1), MaxMs: 1e21},
		// Sub-millisecond, whole-second, offset-zone and pre-epoch times.
		{At: time.Date(2026, 3, 9, 12, 0, 0, 1, time.UTC), Count: 1},
		{At: time.Date(2026, 3, 9, 12, 0, 0, 500000000, time.UTC), Count: 1},
		{At: time.Date(2026, 3, 9, 12, 0, 0, 120000, time.UTC), Count: 1},
		{At: time.Date(2026, 3, 9, 12, 0, 0, 0, time.FixedZone("x", 5*3600+1800)), Count: 1},
		{At: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), Count: 1},
		tiny, edges, worst,
	}
}

// TestEstimateBucketJSONNeverUnderCounts checks the bound against the real
// encoder on edge buckets (every field at its widest, escaped and invalid text)
// and on random ones. Where no string needs escaping the estimate is exact.
func TestEstimateBucketJSONNeverUnderCounts(t *testing.T) {
	check := func(label string, b audit.Bucket, exact bool) {
		t.Helper()
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("%s: fixture does not marshal: %v", label, err)
		}
		size, ok := estimateBucketJSON(b)
		if !ok || size < int64(len(encoded)) {
			t.Fatalf("%s: estimate %d (ok=%v) is below the encoded size %d: %s", label, size, ok, len(encoded), encoded)
		}
		if exact && size != int64(len(encoded)) {
			t.Fatalf("%s: estimate %d is not the encoded size %d: %s", label, size, len(encoded), encoded)
		}
	}
	for i, b := range estimateFixtureBuckets() {
		check(fmt.Sprintf("fixture %d", i), b, false)
	}
	rng := rand.New(rand.NewSource(7))
	text := func() string {
		raw := make([]byte, rng.Intn(24))
		for i := range raw {
			raw[i] = byte(rng.Intn(256))
		}
		return string(raw)
	}
	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./ :"
	safeText := func() string {
		raw := make([]byte, rng.Intn(24))
		for i := range raw {
			raw[i] = safe[rng.Intn(len(safe))]
		}
		return string(raw)
	}
	number := func() float64 {
		sign := float64(1 - 2*rng.Intn(2))
		switch rng.Intn(8) {
		case 0:
			return 0
		case 1:
			return sign * float64(rng.Intn(1000))
		case 2:
			return rng.NormFloat64() * math.Pow(10, float64(rng.Intn(600)-300))
		case 3:
			// Around the 1e-6 switch to exponent form.
			return sign * (0.5 + rng.Float64()*10) * 1e-6
		case 4:
			// Around the 1e21 switch to exponent form.
			return sign * (0.5 + rng.Float64()*10) * 1e21
		case 5:
			for {
				v := math.Float64frombits(rng.Uint64())
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					return v
				}
			}
		case 6:
			return sign * math.Round(rng.Float64()*1e6) / 1000
		}
		return rng.Float64()
	}
	count := func() int64 { return rng.Int63() >> uint(rng.Intn(63)) * int64(1-2*rng.Intn(2)) }
	instant := func() time.Time {
		ns := rng.Int63n(1e9)
		switch rng.Intn(4) {
		case 0:
			ns = 0
		case 1:
			ns -= ns % 1e6
		case 2:
			ns = rng.Int63n(1e6)
		}
		return time.Unix(rng.Int63n(250000000000), ns).UTC()
	}
	for i := 0; i < 20000; i++ {
		b := audit.Bucket{
			SourceID: text(), Controller: text(), Action: text(), Kind: text(), Outcome: text(), State: text(),
			At: instant(), Bin: rng.Intn(1000) - 500,
			Count: count(), SumMs: number(), MinMs: number(), MaxMs: number(),
			SumAuditMs: number(), SumApiMs: number(), SumPayloadMs: number(),
			NAudit: count(), NApi: count(), NPayload: count(),
			PendingAsync: count(), ObservedPartial: count(), Count4xx: count(), Count5xx: count(), CountHangfireFailed: count(),
		}
		check(fmt.Sprintf("random %d", i), b, false)
		b.SourceID, b.Controller, b.Action, b.Kind, b.Outcome, b.State = safeText(), safeText(), safeText(), safeText(), safeText(), safeText()
		check(fmt.Sprintf("random safe %d", i), b, true)
	}
}

// TestEstimateBucketJSONDoesNotAllocate keeps the per-row bail-out free of heap
// work, including for floats in exponent form and a time with a zone offset.
func TestEstimateBucketJSONDoesNotAllocate(t *testing.T) {
	for i, b := range estimateFixtureBuckets() {
		if allocs := testing.AllocsPerRun(100, func() { _, _ = estimateBucketJSON(b) }); allocs != 0 {
			t.Errorf("fixture %d: %v allocations per estimate, want 0", i, allocs)
		}
	}
}

// TestEstimateBucketJSONRejectsWhatMarshalRefuses keeps the error class of a
// bucket the encoder would not accept.
func TestEstimateBucketJSONRejectsWhatMarshalRefuses(t *testing.T) {
	for name, b := range map[string]audit.Bucket{
		"NaN sum":       {SumMs: math.NaN()},
		"infinite min":  {MinMs: math.Inf(1)},
		"NaN phase sum": {SumApiMs: math.NaN()},
		"year 10000":    {At: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if _, err := json.Marshal(b); err == nil {
			t.Fatalf("%s: json.Marshal accepted the fixture", name)
		}
		if _, ok := estimateBucketJSON(b); ok {
			t.Errorf("%s: estimate accepted a bucket json.Marshal refuses", name)
		}
	}
}

// realisticDocs is n aggregation rows shaped like production data: long-ish
// names, millisecond-precision times, fractional timings, phase sums and counts.
func realisticDocs(n int) []any {
	rng := rand.New(rand.NewSource(11))
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	actions := []string{"GetEmployeeDetails", "ListPayrollRuns", "ApprovePayslip", "ExportTaxReport"}
	// Sums keep full precision; minimum, maximum and phase figures carry millisecond fractions.
	ms := func(limit float64) float64 { return math.Round(rng.Float64()*limit*1000) / 1000 }
	docs := make([]any, n)
	for i := range docs {
		count := 1 + rng.Int63n(500)
		bin := 20 + rng.Intn(40)
		lower, upper := audit.BinBounds(bin)
		min := math.Round((lower+(*upper-lower)*0.1)*1000) / 1000
		max := math.Round((lower+(*upper-lower)*0.9)*1000) / 1000
		docs[i] = bucketDocument{
			Controller: "PayrollProcessing", Action: actions[i%len(actions)] + fmt.Sprint(i/len(actions)%50),
			Kind: "synchronous", Outcome: "success", State: "valid",
			At:  start.Add(time.Duration(i) * 7777 * time.Millisecond).Add(time.Duration(rng.Intn(1000)) * time.Millisecond),
			Bin: bin, Count: count,
			SumMs: float64(count) * (min + (max-min)*rng.Float64()), MinMs: min, MaxMs: max,
			SumAuditMs: ms(5000), SumApiMs: ms(70000), SumPayloadMs: ms(900),
			NAudit: 1 + rng.Int63n(count), NApi: 1 + rng.Int63n(count), NPayload: 1 + rng.Int63n(count),
			C4xx: rng.Int63n(count/4 + 1), C5xx: rng.Int63n(count/8 + 1),
		}
	}
	return docs
}

// TestEstimateBucketJSONLeavesRoomAtTheRowCap asserts that a full row-cap result
// of realistic buckets is not rejected by the estimate: the summed estimate stays
// under the 16 MiB limit and within a few bytes per row of the encoded size.
func TestEstimateBucketJSONLeavesRoomAtTheRowCap(t *testing.T) {
	var estimated, encoded int64
	for i, doc := range realisticDocs(audit.MaxSummaryRows) {
		d := doc.(bucketDocument)
		b := audit.Bucket{
			SourceID: "prod-payroll-01", Controller: d.Controller, Action: d.Action, Kind: d.Kind,
			Outcome: d.Outcome, State: d.State, At: d.At.UTC(), Bin: d.Bin, Count: d.Count,
			SumMs: d.SumMs, MinMs: d.MinMs, MaxMs: d.MaxMs,
			SumAuditMs: d.SumAuditMs, SumApiMs: d.SumApiMs, SumPayloadMs: d.SumPayloadMs,
			NAudit: d.NAudit, NApi: d.NApi, NPayload: d.NPayload, Count4xx: d.C4xx, Count5xx: d.C5xx,
		}
		size, ok := estimateBucketJSON(b)
		raw, err := json.Marshal(b)
		if !ok || err != nil || size != int64(len(raw)) {
			t.Fatalf("row %d: estimate %d (ok=%v), encoded %d (err=%v)", i, size, ok, len(raw), err)
		}
		estimated += size + 1
		encoded += int64(len(raw)) + 1
	}
	if estimated > maxAnalysisBytes {
		t.Fatalf("%d realistic rows estimate %d bytes, over the %d limit (encoded: %d)", audit.MaxSummaryRows, estimated, maxAnalysisBytes, encoded)
	}
	t.Logf("%d realistic rows: %d bytes estimated of %d allowed", audit.MaxSummaryRows, estimated, maxAnalysisBytes)
}

// TestAnalyzeFullRowCapOfRealisticRowsPassesTheBucketByteBailOut runs realistic
// rows up to the row cap through Analyze (the fake serves the same rows for the
// current and the reference window, so half the cap per window). The per-bucket
// estimate must not reject them; whether the finished analysis fits is the
// separate exact check, which may still decide either way.
func TestAnalyzeFullRowCapOfRealisticRowsPassesTheBucketByteBailOut(t *testing.T) {
	_, err := analyzeWithBuckets(t, realisticDocs(audit.MaxSummaryRows/2))
	if errors.Is(err, audit.ErrSummaryBytesLimit) {
		t.Fatalf("realistic rows at the row cap hit the summary byte bail-out: %v", err)
	}
	if err != nil && !errors.Is(err, audit.ErrAnalysisBytesLimit) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func analyzeWithBuckets(t *testing.T, docs []any) (audit.Analysis, error) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	return s.Analyze(context.Background(), q)
}

func TestAnalyzeNormalResultIsUnchangedByByteEstimate(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	docs := []any{
		bucketDocument{Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: audit.BinIndex(10), Count: 3, SumMs: 30, MinMs: 10, MaxMs: 10},
		bucketDocument{Controller: "Payroll", Action: "Close", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: audit.BinIndex(7), Count: 1, SumMs: 7, MinMs: 7, MaxMs: 7},
	}
	analysis, err := analyzeWithBuckets(t, docs)
	if err != nil {
		t.Fatalf("a normal analysis failed: %v", err)
	}
	if len(analysis.Sources) != 1 || !analysis.Sources[0].OK {
		t.Fatalf("sources = %#v", analysis.Sources)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil || len(encoded) > maxAnalysisBytes {
		t.Fatalf("analysis size = %d, err = %v", len(encoded), err)
	}
}

// TestAnalyzeOversizedResultStillReturnsByteLimit covers the early bail-out:
// buckets whose own text already exceeds the cap stop at the per-bucket estimate
// with ErrSummaryBytesLimit, a limit error as before.
func TestAnalyzeOversizedResultStillReturnsByteLimit(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)

	// Bucket text over the cap: the per-bucket bail-out.
	huge := []any{bucketDocument{Controller: strings.Repeat("c", maxAnalysisBytes/2), Action: strings.Repeat("a", maxAnalysisBytes/2), Kind: "synchronous", Outcome: "success", State: "valid", At: at, Count: 1}}
	_, err := analyzeWithBuckets(t, huge)
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrSummaryBytesLimit) {
		t.Fatalf("oversized bucket error = %v, want ErrLimit and ErrSummaryBytesLimit", err)
	}
}

// TestAnalyzeWholeAnalysisMarshalCheckStillDecides asserts the exact check is
// still the final word: a result that passes every per-bucket estimate but whose
// serialized analysis passes the cap must yield ErrAnalysisBytesLimit.
func TestAnalyzeWholeAnalysisMarshalCheckStillDecides(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	// Long names appear once in the bucket and again in the analysis, whose
	// per-action rows repeat them across statistics, so the analysis outgrows
	// the buckets while the estimate for each bucket stays small.
	docs := make([]any, 0, 200)
	long := strings.Repeat("x", 30000)
	for i := 0; i < 200; i++ {
		docs = append(docs, bucketDocument{Controller: fmt.Sprintf("%s%d", long, i), Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: audit.BinIndex(1), Count: 1, SumMs: 1, MinMs: 1, MaxMs: 1})
	}
	_, err := analyzeWithBuckets(t, docs)
	if !errors.Is(err, audit.ErrAnalysisBytesLimit) {
		t.Fatalf("error = %v, want ErrAnalysisBytesLimit", err)
	}
	if !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("error = %v, want it to wrap ErrLimit", err)
	}
}
