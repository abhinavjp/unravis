package audit

import (
	"math"
	"testing"
	"time"
)

func phaseBucket(count int64, ms float64, audit, api, payload float64, nAudit, nApi, nPayload int64) Bucket {
	return Bucket{
		SourceID: "s", Controller: "C", Action: "A", Kind: "synchronous", Outcome: "success", State: "valid",
		At: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Bin: BinIndex(ms), Count: count,
		SumMs: ms * float64(count), MinMs: ms, MaxMs: ms,
		SumAuditMs: audit, SumApiMs: api, SumPayloadMs: payload, NAudit: nAudit, NApi: nApi, NPayload: nPayload,
	}
}

func TestPhaseMeansUsePerPhaseCounts(t *testing.T) {
	// 10 documents, but only 4 carry AuditCreateTime: the mean is 40/4, not 40/10.
	b := phaseBucket(10, 100, 40, 800, 0, 4, 10, 0)
	h := newHistogramAccumulator()
	if err := h.addBucket(b); err != nil {
		t.Fatal(err)
	}
	phases := h.stats().Phases
	if phases == nil || phases.AuditMs == nil || phases.ApiMs == nil {
		t.Fatalf("phases = %+v, want audit and api means", phases)
	}
	if *phases.AuditMs != 10 {
		t.Fatalf("AuditMs = %v, want 10 (sum over the 4 documents that have it)", *phases.AuditMs)
	}
	if *phases.ApiMs != 80 {
		t.Fatalf("ApiMs = %v, want 80", *phases.ApiMs)
	}
	if phases.PayloadMs != nil {
		t.Fatalf("PayloadMs = %v, want nil with no payload samples", *phases.PayloadMs)
	}
}

func TestPhasesNilWithoutAnyPhaseData(t *testing.T) {
	h := newHistogramAccumulator()
	if err := h.addBucket(phaseBucket(5, 50, 0, 0, 0, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if phases := h.stats().Phases; phases != nil {
		t.Fatalf("phases = %+v, want nil", phases)
	}
}

func TestAddBucketMergesPhaseSumsAndCounts(t *testing.T) {
	h := newHistogramAccumulator()
	// Different bins on purpose: the phase totals must survive across bins.
	if err := h.addBucket(phaseBucket(10, 20, 20, 160, 10, 10, 10, 10)); err != nil {
		t.Fatal(err)
	}
	if err := h.addBucket(phaseBucket(6, 400, 60, 2000, 0, 3, 5, 0)); err != nil {
		t.Fatal(err)
	}
	if h.sumAudit != 80 || h.sumApi != 2160 || h.sumPayload != 10 {
		t.Fatalf("sums = %v/%v/%v, want 80/2160/10", h.sumAudit, h.sumApi, h.sumPayload)
	}
	if h.nAudit != 13 || h.nApi != 15 || h.nPayload != 10 {
		t.Fatalf("counts = %d/%d/%d, want 13/15/10", h.nAudit, h.nApi, h.nPayload)
	}
	phases := h.stats().Phases
	if phases == nil || *phases.AuditMs != 80.0/13 || *phases.ApiMs != 2160.0/15 || *phases.PayloadMs != 1 {
		t.Fatalf("phases = %+v", phases)
	}
}

func TestAddBucketRejectsInconsistentPhases(t *testing.T) {
	cases := map[string]Bucket{
		"negative sum":      phaseBucket(5, 50, -1, 0, 0, 5, 0, 0),
		"NaN sum":           phaseBucket(5, 50, math.NaN(), 0, 0, 5, 0, 0),
		"infinite sum":      phaseBucket(5, 50, 0, math.Inf(1), 0, 0, 5, 0),
		"negative count":    phaseBucket(5, 50, 0, 0, 0, -1, 0, 0),
		"count above total": phaseBucket(5, 50, 10, 0, 0, 6, 0, 0),
		"sum without count": phaseBucket(5, 50, 10, 0, 0, 0, 0, 0),
	}
	for name, b := range cases {
		if err := newHistogramAccumulator().addBucket(b); err == nil {
			t.Errorf("%s: addBucket succeeded, want an error", name)
		}
	}
}

func TestPhaseDeltaExplainsTotalRegression(t *testing.T) {
	q := Query{
		SourceIDs: []string{"s"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 12, 15, 0, 0, time.UTC),
		BaselineStart: time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		Kind: "synchronous", Outcome: "success", Metric: "total", BucketSeconds: 10,
	}
	// 25 requests per window. Reference: audit 20 + api 80 = 100 ms; current: audit 20 + api 280 = 300 ms.
	reference := []Bucket{phaseBucket(25, 100, 25*20, 25*80, 25*5, 25, 25, 25)}
	reference[0].At = q.BaselineStart
	current := []Bucket{phaseBucket(25, 300, 25*20, 25*280, 25*5, 25, 25, 25)}
	analysis, err := BuildAnalysis(q, current, reference, []SourceResult{{SourceID: "s", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) != 1 {
		t.Fatalf("APIs = %d, want 1", len(analysis.APIs))
	}
	c := analysis.APIs[0]
	if c.Current.Phases == nil || c.Reference.Phases == nil {
		t.Fatalf("phases missing: current %+v reference %+v", c.Current.Phases, c.Reference.Phases)
	}
	delta := c.PhaseDeltaMs
	if delta == nil || delta.ApiMs == nil || delta.AuditMs == nil || delta.PayloadMs == nil {
		t.Fatalf("PhaseDeltaMs = %+v, want all three phases", delta)
	}
	if *delta.ApiMs != 200 || *delta.AuditMs != 0 || *delta.PayloadMs != 0 {
		t.Fatalf("PhaseDeltaMs = api %v audit %v payload %v, want 200/0/0", *delta.ApiMs, *delta.AuditMs, *delta.PayloadMs)
	}
}

func TestPhaseDeltaNilPerPhaseWhenEitherSideHasNoSamples(t *testing.T) {
	q := Query{
		SourceIDs: []string{"s"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 12, 15, 0, 0, time.UTC),
		BaselineStart: time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		Kind: "synchronous", Outcome: "success", Metric: "total", BucketSeconds: 10,
	}
	// The reference has no audit samples, so only api can be compared.
	reference := []Bucket{phaseBucket(25, 100, 0, 25*80, 0, 0, 25, 0)}
	reference[0].At = q.BaselineStart
	current := []Bucket{phaseBucket(25, 300, 25*20, 25*280, 0, 25, 25, 0)}
	analysis, err := BuildAnalysis(q, current, reference, []SourceResult{{SourceID: "s", OK: true}})
	if err != nil {
		t.Fatal(err)
	}
	delta := analysis.APIs[0].PhaseDeltaMs
	if delta == nil || delta.ApiMs == nil || *delta.ApiMs != 200 {
		t.Fatalf("PhaseDeltaMs = %+v, want api 200", delta)
	}
	if delta.AuditMs != nil || delta.PayloadMs != nil {
		t.Fatalf("audit/payload delta = %v/%v, want nil", delta.AuditMs, delta.PayloadMs)
	}
}

func TestPhaseDeltaNilWithoutPhaseData(t *testing.T) {
	var current, reference Stats
	if got := phaseDelta(current, reference); got != nil {
		t.Fatalf("phaseDelta = %+v, want nil", got)
	}
}
