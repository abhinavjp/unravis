package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type rollupHarness struct {
	*bdHarness
	mu    sync.Mutex
	calls map[string]int
}

// slotDoc is one document of the rollup aggregation: a slot, an optional latency
// bin, and the counters and sums of its rows.
func slotDoc(t *testing.T, slot time.Time, bin any, total, sync, bg, c4, c5, slow int64, sum, lo, hi any) any {
	t.Helper()
	raw, err := bson.Marshal(bson.D{
		{Key: "_id", Value: bson.D{{Key: "s", Value: slot.UnixMilli()}, {Key: "b", Value: bin}}},
		{Key: "total", Value: total}, {Key: "sync", Value: sync}, {Key: "bg", Value: bg},
		{Key: "c4xx", Value: c4}, {Key: "c5xx", Value: c5}, {Key: "slow", Value: slow},
		{Key: "sum", Value: sum}, {Key: "min", Value: lo}, {Key: "max", Value: hi},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bson.Raw(raw)
}

// rollupStore serves each collection the documents perCollection returns for it.
func rollupStore(t *testing.T, perCollection func(collection string) ([]any, error), extra ...string) *rollupHarness {
	t.Helper()
	h := &rollupHarness{bdHarness: bdStore(t, false, nil, extra...), calls: map[string]int{}}
	h.handle.aggregateFn = func(ctx context.Context, _, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		h.mu.Lock()
		h.calls[collection]++
		h.pipelines = append(h.pipelines, pipeline)
		h.mu.Unlock()
		docs, err := perCollection(collection)
		if err != nil {
			return nil, err
		}
		return &fakeCursor{docs: docs}, nil
	}
	return h
}

func (h *rollupHarness) totalCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		n += c
	}
	return n
}

func rollupPlan(t *testing.T, ids []string, from, to time.Time, zone string) audit.RollupPlan {
	t.Helper()
	plan, err := audit.PlanRollup(audit.RollupInput{SourceIDs: ids, From: from, To: to, TimeZone: zone})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRollupRefusesBroadWindowsOnLiveSourcesBeforeAnyRead(t *testing.T) {
	h := rollupStore(t, func(string) ([]any, error) { return nil, nil })
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	broad := rollupPlan(t, h.ids(), from, from.Add(24*time.Hour+time.Minute), "")
	if _, err := h.store.Rollup(context.Background(), broad); !errors.Is(err, audit.ErrBroadScanRefused) {
		t.Fatalf("a window over 24 hours on a live source: err = %v, want ErrBroadScanRefused", err)
	}
	if h.totalCalls() != 0 {
		t.Fatalf("a refused rollup must not touch the source, got %d calls", h.totalCalls())
	}
	short := rollupPlan(t, h.ids(), from, from.Add(24*time.Hour), "")
	out, err := h.store.Rollup(context.Background(), short)
	if err != nil || len(out.Sources) != 1 || !out.Sources[0].OK || h.totalCalls() != 1 {
		t.Fatalf("a window of exactly 24 hours runs: %+v err %v calls %d", out, err, h.totalCalls())
	}
	if got := out.Provenance[0].MaxTimeMs; got != audit.InteractiveQueryBudget.Milliseconds() {
		t.Errorf("a short window uses the interactive budget, MaxTimeMs = %d", got)
	}

	h.store.allowBroadScans = true
	out, err = h.store.Rollup(context.Background(), broad)
	if err != nil || len(out.Sources) != 1 || !out.Sources[0].OK {
		t.Fatalf("with broad scans allowed: %+v err %v", out, err)
	}
	if got := out.Provenance[0].MaxTimeMs; got != audit.CensusQueryBudget.Milliseconds() {
		t.Errorf("a broad window uses the census budget, MaxTimeMs = %d", got)
	}
}

func TestRollupIsOnePassPerSourceAndMergesSlots(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	slot := from.Add(10 * time.Hour)
	h := rollupStore(t, func(collection string) ([]any, error) {
		return []any{
			slotDoc(t, slot, nil, 3, 1, 1, 1, 0, 0, nil, nil, nil),
			slotDoc(t, slot, int32(audit.BinIndex(120)), 5, 5, 0, 0, 2, 1, 600.0, 120.0, 120.0),
		}, nil
	}, "audit2")
	h.store.allowBroadScans = true
	plan := rollupPlan(t, h.ids(), from, from.Add(48*time.Hour), "")
	out, err := h.store.Rollup(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for collection, n := range h.calls {
		if n != 1 {
			t.Errorf("%s was read %d times, want one aggregation", collection, n)
		}
	}
	if len(h.calls) != 2 || len(out.Sources) != 2 || len(out.Provenance) != 2 {
		t.Fatalf("calls %v sources %d provenance %d", h.calls, len(out.Sources), len(out.Provenance))
	}
	days, err := audit.BuildDaily(plan, out.Builder)
	if err != nil || len(days) != 2 {
		t.Fatalf("days %d err %v", len(days), err)
	}
	d := days[0]
	if d.Total != 16 || d.Sync != 12 || d.Background != 2 || d.Other != 2 || d.Count4xx != 2 || d.Count5xx != 4 || d.LatencyCount != 10 {
		t.Errorf("two sources must add: %+v", d)
	}
	if days[1].Total != 0 {
		t.Errorf("an empty day = %+v", days[1])
	}
	last := h.pipelines[len(h.pipelines)-1]
	limit := last[len(last)-1]
	if limit[0].Key != "$limit" || limit[0].Value != audit.MaxRollupGroups+1 {
		t.Errorf("the aggregation must end with a group cap, last stage = %v", limit)
	}
	if last[len(last)-2][0].Key != "$group" {
		t.Errorf("one $group before the cap, got %v", last[len(last)-2])
	}
}

func TestRollupReportsAFailedSourceAndKeepsTheOthers(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	h := rollupStore(t, func(collection string) ([]any, error) {
		if collection == "audit2" {
			return nil, errors.New("mongodb://reader:secret@host exploded")
		}
		return []any{slotDoc(t, from.Add(time.Hour), nil, 7, 7, 0, 0, 0, 0, nil, nil, nil)}, nil
	}, "audit2")
	plan := rollupPlan(t, h.ids(), from, from.Add(12*time.Hour), "")
	out, err := h.store.Rollup(context.Background(), plan)
	if err != nil {
		t.Fatalf("a failed source is reported in sources, not as an error: %v", err)
	}
	if len(out.Sources) != 2 || !out.Sources[0].OK || out.Sources[1].OK || out.Sources[1].ErrorCode == "" {
		t.Fatalf("sources = %+v", out.Sources)
	}
	if got := out.Sources[1].Error; got == "" || strings.Contains(got, "secret") {
		t.Errorf("the failure message must be sanitized: %q", got)
	}
	days, _ := audit.BuildDaily(plan, out.Builder)
	if days[0].Total != 7 {
		t.Errorf("the good source's rows must remain: %+v", days[0])
	}
}

func TestRollupFailsClosedPastTheGroupCap(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	doc := slotDoc(t, from, nil, 1, 1, 0, 0, 0, 0, nil, nil, nil)
	h := rollupStore(t, func(string) ([]any, error) {
		docs := make([]any, audit.MaxRollupGroups+1)
		for i := range docs {
			docs[i] = doc
		}
		return docs, nil
	})
	plan := rollupPlan(t, h.ids(), from, from.Add(time.Hour), "")
	_, err := h.store.Rollup(context.Background(), plan)
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrRollupGroupLimit) {
		t.Fatalf("err = %v, want ErrLimit and ErrRollupGroupLimit", err)
	}
	// Exactly at the cap is fine.
	h2 := rollupStore(t, func(string) ([]any, error) {
		docs := make([]any, audit.MaxRollupGroups)
		for i := range docs {
			docs[i] = doc
		}
		return docs, nil
	})
	if _, err := h2.store.Rollup(context.Background(), rollupPlan(t, h2.ids(), from, from.Add(time.Hour), "")); err != nil {
		t.Fatalf("exactly %d groups must pass: %v", audit.MaxRollupGroups, err)
	}
}

func TestRollupUnknownSourceIsAnError(t *testing.T) {
	h := rollupStore(t, func(string) ([]any, error) { return nil, nil })
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	plan := rollupPlan(t, []string{"missing"}, from, from.Add(time.Hour), "")
	if _, err := h.store.Rollup(context.Background(), plan); err == nil {
		t.Fatal("a source that does not exist must fail the call")
	}
}

func TestRollupLegacyPathSlotsRowsInGo(t *testing.T) {
	now := bdNow()
	cases := bdCases(now)
	h := bdStore(t, true, bdDocs(cases))
	plan := rollupPlan(t, h.ids(), now.Add(-time.Hour), now, "Asia/Kolkata")
	out, err := h.store.Rollup(context.Background(), plan)
	if err != nil || len(out.Sources) != 1 || !out.Sources[0].OK {
		t.Fatalf("legacy rollup: %+v err %v", out, err)
	}
	var c4, c5 int64
	for _, c := range cases {
		switch c.class {
		case audit.StatusClientError:
			c4++
		case audit.StatusServerError:
			c5++
		}
	}
	grid, err := audit.BuildHourGrid(plan, out.Builder)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, cell := range grid.Cells {
		total += cell.Count
	}
	days, _ := audit.BuildDaily(plan, out.Builder)
	var d4, d5 int64
	for _, d := range days {
		d4, d5 = d4+d.Count4xx, d5+d.Count5xx
	}
	if total != int64(len(cases)) || d4 != c4 || d5 != c5 {
		t.Errorf("legacy rows: total %d (want %d), 4xx %d (want %d), 5xx %d (want %d)", total, len(cases), d4, c4, d5, c5)
	}
	if out.Provenance[0].SampleSize != int64(len(cases)) {
		t.Errorf("provenance matched %d, want %d", out.Provenance[0].SampleSize, len(cases))
	}
}
