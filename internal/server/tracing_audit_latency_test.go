package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// latencyBinOf is the bin whose half-open bounds hold ms.
func latencyBinOf(ms float64) int {
	for bin := 0; ; bin++ {
		lower, upper := audit.BinBounds(bin)
		if lower <= ms && (upper == nil || ms < *upper) {
			return bin
		}
	}
}

func TestLatencyAccQuantile(t *testing.T) {
	// Ten requests of exactly 100 ms: every figure is clipped to 100.
	var a latencyAcc
	a.add([]audit.RollupWireBin{{Bin: latencyBinOf(100), Count: 10, Sum: 1000, Min: 100, Max: 100}})
	timed, p50, p95, max := a.figures()
	if timed != 10 || p50 == nil || p95 == nil || max == nil {
		t.Fatalf("figures = %d %v %v %v", timed, p50, p95, max)
	}
	if *p50 != 100 || *p95 != 100 || *max != 100 {
		t.Errorf("p50 %v p95 %v max %v, want all 100", *p50, *p95, *max)
	}

	// Nine requests of 10 ms and one of 1000 ms: P50 is the midpoint of the 10 ms
	// bin clipped to the observed range [10, 1000] (so just above 10), P95 the
	// midpoint of the 1000 ms bin clipped the same way (between its lower bound
	// and 1000), and the max is the observed 1000.
	var b latencyAcc
	b.add([]audit.RollupWireBin{
		{Bin: latencyBinOf(10), Count: 9, Sum: 90, Min: 10, Max: 10},
		{Bin: latencyBinOf(1000), Count: 1, Sum: 1000, Min: 1000, Max: 1000},
	})
	timed, p50, p95, max = b.figures()
	if timed != 10 || p50 == nil || p95 == nil || max == nil {
		t.Fatalf("figures = %d %v %v %v", timed, p50, p95, max)
	}
	if *max != 1000 {
		t.Errorf("max %v, want 1000", *max)
	}
	lower10, _ := audit.BinBounds(latencyBinOf(10))
	if *p50 < 10 || *p50 >= lower10*1.1 {
		t.Errorf("p50 %v, want inside [10, %v)", *p50, lower10*1.1)
	}
	lower1000, _ := audit.BinBounds(latencyBinOf(1000))
	if *p95 <= lower1000 || *p95 > 1000 {
		t.Errorf("p95 %v, want inside (%v, 1000]", *p95, lower1000)
	}
	if v, ok := b.quantile(0.95); !ok || v <= lower1000 || v > 1000 {
		t.Errorf("quantile(0.95) = %v %v, want inside (%v, 1000]", v, ok, lower1000)
	}
}

func TestLatencyAccEmpty(t *testing.T) {
	var a latencyAcc
	if v, ok := a.quantile(0.95); ok || v != 0 {
		t.Errorf("quantile = %v %v, want 0 false", v, ok)
	}
	timed, p50, p95, max := a.figures()
	if timed != 0 || p50 != nil || p95 != nil || max != nil {
		t.Errorf("figures = %d %v %v %v, want 0 nil nil nil", timed, p50, p95, max)
	}
}

// latencyRow is one request row the latency fake counts; a nil ms is a request
// with no timing.
type latencyRow struct {
	at time.Time
	ms *float64
}

// latencyFake is an audit.RollupReader whose Rollup builds the slots of the
// plan from its rows with AddRow, so the latency bins are populated.
type latencyFake struct {
	audit.RollupReader
	rows []latencyRow
}

func (f *latencyFake) Rollup(_ context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	b := audit.NewRollupBuilder(plan.Width)
	for _, r := range f.rows {
		if r.at.Before(plan.From) || !r.at.Before(plan.To) {
			continue
		}
		b.AddRow(r.at, "synchronous", "valid", "", r.ms, audit.DefaultRollupSlowMs)
	}
	res := make([]audit.SourceResult, 0, len(plan.SourceIDs))
	for _, id := range plan.SourceIDs {
		res = append(res, audit.SourceResult{SourceID: id, OK: true})
	}
	return audit.RollupSlots{Builder: b, Sources: res}, nil
}

func TestAuditBucketsCarryLatency(t *testing.T) {
	axis := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	ms20, ms400 := 20.0, 400.0
	f := &latencyFake{rows: []latencyRow{
		{auditT0.Add(10 * time.Second), &ms20},
		{auditT0.Add(20 * time.Second), &ms400},
		{auditT0.Add(30 * time.Second), nil}, // no timing
	}}
	got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if !got.Complete || len(got.Buckets) != 1 {
		t.Fatalf("read complete %v buckets %+v", got.Complete, got.Buckets)
	}
	b := got.Buckets[0]
	if b.Count != 3 || b.Timed != 2 {
		t.Errorf("count %d timed %d, want 3 and 2", b.Count, b.Timed)
	}
	if b.MaxMs == nil || *b.MaxMs != 400 {
		t.Errorf("maxMs %v, want 400", b.MaxMs)
	}
	if b.P95Ms == nil || *b.P95Ms < 20 || *b.P95Ms > 400 {
		t.Errorf("p95Ms %v, want between 20 and 400", b.P95Ms)
	}
	if b.P50Ms == nil {
		t.Errorf("p50Ms nil")
	}
}

func TestAuditLaneToWireLatencyJSON(t *testing.T) {
	ms20, ms400 := 20.0, 400.0
	read := auditLaneRead{
		Complete: true,
		Buckets: []auditBucket{
			{Start: auditT0, End: auditT0.Add(time.Minute), Count: 3, Timed: 2, P50Ms: &ms20, P95Ms: &ms400, MaxMs: &ms400},
			{Start: auditT0.Add(time.Minute), End: auditT0.Add(2 * time.Minute), Count: 1},
		},
	}
	lane := auditLaneToWire(read, nil)
	if len(lane.Buckets) != 2 {
		t.Fatalf("buckets %+v", lane.Buckets)
	}
	raw, err := json.Marshal(lane.Buckets[0])
	if err != nil {
		t.Fatal(err)
	}
	withFigures := string(raw)
	for _, want := range []string{`"timedCount":2`, `"p50Ms"`, `"p95Ms"`, `"maxMs"`} {
		if !strings.Contains(withFigures, want) {
			t.Errorf("timed bucket lacks %s: %s", want, withFigures)
		}
	}
	raw, err = json.Marshal(lane.Buckets[1])
	if err != nil {
		t.Fatal(err)
	}
	untimed := string(raw)
	for _, banned := range []string{"timedCount", "p50Ms", "p95Ms", "maxMs"} {
		if strings.Contains(untimed, banned) {
			t.Errorf("untimed bucket holds %s: %s", banned, untimed)
		}
	}
}
