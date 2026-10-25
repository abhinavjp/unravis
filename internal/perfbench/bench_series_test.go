package perfbench

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/resource/rules"
)

// The D1/D2 measurements: series and resource queries straight over the case
// store (no HTTP), on the synthetic counters case. Gated by UNRAVIS_BENCH=1.
// UNRAVIS_BENCH_COUNTER_SECONDS shortens the span (default 24 h).

func synthSelector(object, counter, instance string) string {
	s := "kind=counter;source=win-counters;host=web-a;attr.object=" + object + ";attr.counter=" + counter
	if instance != "" {
		s += ";attr.instance=" + instance
	}
	return s
}

func synthSelectors(n int) []string {
	if n == 1 {
		return []string{synthSelector("Processor", "% Processor Time", "_Total")}
	}
	out := make([]string, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, synthSelector("Process", "% Processor Time", fmt.Sprintf("w3wp#%d", k)))
	}
	return out
}

func allocNow() (total uint64, heap uint64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc, ms.HeapInuse
}

func TestBenchSynthQueries(t *testing.T) {
	need(t)
	span := envInt("UNRAVIS_BENCH_COUNTER_SECONDS", 86400)
	root := benchDir(t)
	s := newSynthStore(t, root, storetest.RandomProtector())
	t0 := time.Now()
	c, n := buildSynthCase(t, s, span, 15)
	defer c.Close()
	t.Logf("built %d events in %s", n, time.Since(t0).Round(time.Millisecond))
	ctx := context.Background()
	from := synthStart
	to := synthStart.Add(24 * time.Hour)

	for _, k := range []int{1, 10, 50} {
		m := startMeter("")
		t1 := time.Now()
		res, err := casestore.Series(ctx, c, casestore.SeriesQuery{From: from, To: to, BucketMs: 300_000, Sel: synthSelectors(k)})
		wall := time.Since(t1)
		meas := m.finish()
		if err != nil {
			t.Fatal(err)
		}
		var pts int64
		for _, so := range res.Series {
			pts += so.Count
		}
		report(t, fmt.Sprintf("synth series %d selectors", k), meas, fmt.Sprintf("wall=%s partial=%v scanned=%d counted=%d", wall.Round(time.Millisecond), res.Partial, res.Scanned, pts))
		// D1: a complete answer inside the 10 s budget, reading only the series asked for.
		if res.Partial || pts == 0 || pts != res.Scanned {
			t.Errorf("series with %d selectors: partial=%v counted=%d scanned=%d, want a complete answer reading only those events", k, res.Partial, pts, res.Scanned)
		}
		if wall > casestore.MaxScanTime {
			t.Errorf("series with %d selectors took %s, over the %s budget", k, wall, casestore.MaxScanTime)
		}
	}
	// The budget still signals: one far below the case size gives a partial answer.
	part, err := casestore.Series(ctx, c, casestore.SeriesQuery{From: from, To: to, BucketMs: 300_000, Sel: synthSelectors(50), MaxScan: 20_000})
	if err != nil || !part.Partial || part.Scanned != 20_000 {
		t.Errorf("series over a tiny budget: partial=%v scanned=%d err=%v, want partial after 20000", part.Partial, part.Scanned, err)
	}
	ep := join.Interval{ID: "ep-0", Start: from.Add(10 * time.Hour), End: from.Add(10*time.Hour + 10*time.Minute)}
	a0, _ := allocNow()
	m := startMeter("")
	t1 := time.Now()
	res, err := rules.Evaluate(ctx, c, importer.Window{From: from, To: to}, []join.Interval{ep}, rules.DefaultThresholds())
	wall := time.Since(t1)
	meas := m.finish()
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := allocNow()
	report(t, "synth resource", meas, fmt.Sprintf("wall=%s partial=%v totalAlloc=%dMiB rows=%d hosts=%d", wall.Round(time.Millisecond), res.Partial, (a1-a0)>>20, len(res.Rules), len(res.Hosts)))
	// D1/D2: the whole case is read inside the budget, and the heap the build
	// holds stays within what the compact rows need (about 115 B per event).
	if res.Partial {
		t.Errorf("resource evaluation over %d events was partial after %s", n, wall)
	}
	if wall > casestore.MaxScanTime {
		t.Errorf("resource evaluation took %s, over the %s budget", wall, casestore.MaxScanTime)
	}
	if peak := meas.MaxHeapInuse; peak > uint64(n)*500 {
		t.Errorf("resource evaluation peaked at %d MiB of heap for %d events, over 500 B per event", peak>>20, n)
	}
}
