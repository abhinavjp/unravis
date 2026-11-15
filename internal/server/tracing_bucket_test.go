package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// bucketLaneWith runs bucketLane for one lane key over a stubbed store answer,
// so the mapping from store timings to buckets is tested on its own.
func bucketLaneWith(t *testing.T, key string, res casestore.BucketResult) tracingLane {
	t.Helper()
	orig := tracingCountBuckets
	t.Cleanup(func() { tracingCountBuckets = orig })
	tracingCountBuckets = func(context.Context, casestore.Case, casestore.BucketQuery) (casestore.BucketResult, error) {
		return res, nil
	}
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	lane := tracingLane{Key: key, Complete: true}
	if err := bucketLane(context.Background(), nil, axis, &lane, []string{"x"}, map[int64]bool{1: true}, false, false); err != nil {
		t.Fatal(err)
	}
	return lane
}

func bucketAt(t *testing.T, lane tracingLane, at time.Time) tracingBucket {
	t.Helper()
	for _, b := range lane.Buckets {
		if b.Start.Equal(at) {
			return b
		}
	}
	t.Fatalf("no bucket at %v", at)
	return tracingBucket{}
}

// The web server lane carries the summed request time of a bucket as busyMs; a
// bucket that has requests but no timing has no busyMs key (never a stand-in 0).
func TestBucketLaneIISBusyMs(t *testing.T) {
	res := casestore.BucketResult{
		Counts: []casestore.BucketCount{
			{Index: 0, ImportID: 1, Count: 3},
			{Index: 1, ImportID: 1, Count: 2},
		},
		Timings: []casestore.BucketTiming{
			// one cell, two log bins: the rows add up
			{Index: 0, ImportID: 1, Bin: 40, Count: 2, Min: 1000, Max: 1500, Sum: 2500},
			{Index: 0, ImportID: 1, Bin: 42, Count: 1, Min: 2000, Max: 2000, Sum: 2000},
		},
	}
	lane := bucketLaneWith(t, calltrace.LaneIIS, res)

	timed := bucketAt(t, lane, tracingLaneT0)
	if timed.BusyMs == nil || *timed.BusyMs != 4500 {
		t.Fatalf("timed bucket busyMs = %v, want 4500", timed.BusyMs)
	}
	if timed.TimedCount != 3 {
		t.Errorf("timed bucket timedCount = %d, want 3", timed.TimedCount)
	}

	untimed := lane.Buckets[1]
	if untimed.Count != 2 || untimed.BusyMs != nil {
		t.Fatalf("untimed bucket = count %d busyMs %v, want 2 and nil", untimed.Count, untimed.BusyMs)
	}
	raw, err := json.Marshal(untimed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "busyMs") {
		t.Errorf("untimed bucket JSON has a busyMs key: %s", raw)
	}
	raw, _ = json.Marshal(timed)
	if !strings.Contains(string(raw), `"busyMs":4500`) {
		t.Errorf("timed bucket JSON = %s, want busyMs 4500", raw)
	}
}

// Timings of an import that is not selected add nothing to busyMs.
func TestBucketLaneIISBusyMsSkipsUnselectedImports(t *testing.T) {
	res := casestore.BucketResult{
		Counts: []casestore.BucketCount{{Index: 0, ImportID: 1, Count: 1}, {Index: 0, ImportID: 9, Count: 1}},
		Timings: []casestore.BucketTiming{
			{Index: 0, ImportID: 1, Bin: 40, Count: 1, Min: 100, Max: 100, Sum: 100},
			{Index: 0, ImportID: 9, Bin: 40, Count: 1, Min: 700, Max: 700, Sum: 700},
		},
	}
	b := bucketAt(t, bucketLaneWith(t, calltrace.LaneIIS, res), tracingLaneT0)
	if b.BusyMs == nil || *b.BusyMs != 100 {
		t.Fatalf("busyMs = %v, want 100", b.BusyMs)
	}
}

// The SQL lane buckets from bucketLane are unchanged: no busyMs and no response
// times, even if the store answer carries timings.
func TestBucketLaneSQLHasNoBusyMs(t *testing.T) {
	res := casestore.BucketResult{
		Counts:  []casestore.BucketCount{{Index: 0, ImportID: 1, Count: 3}},
		Timings: []casestore.BucketTiming{{Index: 0, ImportID: 1, Bin: 40, Count: 3, Min: 1, Max: 2, Sum: 4500}},
	}
	lane := bucketLaneWith(t, calltrace.LaneSQL, res)
	for _, b := range lane.Buckets {
		if b.BusyMs != nil || b.TimedCount != 0 || b.MaxMs != nil || b.Errors != nil {
			t.Fatalf("sql bucket %v changed: %+v", b.Start, b)
		}
	}
	if b := bucketAt(t, lane, tracingLaneT0); b.Count != 3 {
		t.Errorf("sql bucket count = %d, want 3", b.Count)
	}
}

// End to end on both stores: the requests of an imported IIS log give busyMs
// buckets whose sum is the summed request time; a log without durations gives
// no busyMs key at all. (Live data path: needs a case with real IIS logs.)
func TestTracingIISLaneBusyMsEndToEnd(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := tracingEvents(20)
		for i := range evs {
			evs[i].Value, evs[i].Unit = nil, ""
			evs[i].Kind = "http"
			evs[i].Attrs = map[string]string{"status": "200"}
			d := float64(100)
			if i == 7 {
				d = 4000
			}
			evs[i].DurationMs = &d
		}
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", evs)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != http.StatusOK {
			t.Fatalf("status %d %s", res.Code, res.Body)
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		var busy float64
		var withBusy int
		for _, b := range tracingLaneOf(t, out, "iis").Buckets {
			if b.BusyMs != nil {
				withBusy++
				busy += *b.BusyMs
			} else if b.TimedCount != 0 {
				t.Errorf("timed bucket without busyMs: %+v", b)
			}
		}
		if withBusy == 0 || busy < 5899 || busy > 5901 {
			t.Errorf("busyMs buckets %d, sum %v; want at least one bucket and a sum of about 5900", withBusy, busy)
		}
	})
}

func TestTracingIISLaneWithoutDurationsHasNoBusyMs(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := tracingEvents(6)
		for i := range evs {
			evs[i].Attrs = map[string]string{"status": "200"}
		}
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", evs)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if strings.Contains(res.Body.String(), `"busyMs"`) {
			t.Errorf("answer has a busyMs key for a log without durations: %s", res.Body)
		}
	})
}
