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
		SourceID: strings.Repeat("<", 40), Controller: strings.Repeat("\"\\&", 20), Action: "café   \x00\xff",
		Kind: strings.Repeat("\n", 5), Outcome: "o", State: "s",
		At: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), Bin: math.MinInt,
		Count: math.MinInt64, SumMs: -max, MinMs: -max, MaxMs: -max,
		SumAuditMs: -max, SumApiMs: -max, SumPayloadMs: -max,
		NAudit: math.MinInt64, NApi: math.MinInt64, NPayload: math.MinInt64,
		PendingAsync: math.MinInt64, ObservedPartial: math.MinInt64,
		Count4xx: math.MinInt64, Count5xx: math.MinInt64, CountHangfireFailed: math.MinInt64,
	}
	return []audit.Bucket{
		{},
		{At: at, Count: 1},
		{SourceID: "src-1", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: 12, Count: 37, SumMs: 1234.5678, MinMs: 1.5, MaxMs: 250.25, SumAuditMs: 3.25, NAudit: 4, Count5xx: 2},
		{SourceID: "s", At: at, Count: math.MaxInt64, SumMs: math.SmallestNonzeroFloat64, MinMs: math.Copysign(0, -1), MaxMs: 1e21},
		worst,
	}
}

// TestEstimateBucketJSONNeverUnderCounts checks the bound against the real
// encoder on edge buckets (every field at its widest, escaped and invalid text)
// and on random ones.
func TestEstimateBucketJSONNeverUnderCounts(t *testing.T) {
	check := func(label string, b audit.Bucket) {
		t.Helper()
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("%s: fixture does not marshal: %v", label, err)
		}
		size, ok := estimateBucketJSON(b)
		if !ok || size < int64(len(encoded)) {
			t.Fatalf("%s: estimate %d (ok=%v) is below the encoded size %d: %s", label, size, ok, len(encoded), encoded)
		}
	}
	for i, b := range estimateFixtureBuckets() {
		check(fmt.Sprintf("fixture %d", i), b)
	}
	rng := rand.New(rand.NewSource(7))
	text := func() string {
		raw := make([]byte, rng.Intn(24))
		for i := range raw {
			raw[i] = byte(rng.Intn(256))
		}
		return string(raw)
	}
	number := func() float64 {
		switch rng.Intn(4) {
		case 0:
			return 0
		case 1:
			return float64(rng.Intn(1000))
		case 2:
			return rng.NormFloat64() * math.Pow(10, float64(rng.Intn(600)-300))
		}
		return rng.Float64()
	}
	count := func() int64 { return rng.Int63() >> uint(rng.Intn(63)) * int64(1-2*rng.Intn(2)) }
	for i := 0; i < 5000; i++ {
		check(fmt.Sprintf("random %d", i), audit.Bucket{
			SourceID: text(), Controller: text(), Action: text(), Kind: text(), Outcome: text(), State: text(),
			At: time.Unix(rng.Int63n(250000000000), rng.Int63n(1e9)).UTC(), Bin: rng.Intn(1000) - 500,
			Count: count(), SumMs: number(), MinMs: number(), MaxMs: number(),
			SumAuditMs: number(), SumApiMs: number(), SumPayloadMs: number(),
			NAudit: count(), NApi: count(), NPayload: count(),
			PendingAsync: count(), ObservedPartial: count(), Count4xx: count(), Count5xx: count(), CountHangfireFailed: count(),
		})
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
