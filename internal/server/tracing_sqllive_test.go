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
	"api-audit-explorer/internal/dbhealth"
)

// A saved SQL lane day (a Query Store read kept with the case) draws the Timeline's SQL lane when no SQL file was imported.
func TestTracingDrawsSQLLaneFromSavedQueryStoreDay(t *testing.T) {
	e, id := demoLaneEnv(t)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", demoSQLBody("AuditDb")); res.Code != http.StatusOK {
		t.Fatalf("run = %d %s", res.Code, res.Body)
	}
	out, _ := tracingPost(t, e, id, tracingWindowBodyFor("2026-10-20T10:00:00Z", "2026-10-20T11:00:00Z"))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.State != calltrace.StateEvents || lane.ReasonCode != tracingReasonHeldSQL || lane.Consistency != tracingConsStored {
		t.Fatalf("lane = %s %s %s", lane.State, lane.ReasonCode, lane.Consistency)
	}
	if tracingBucketSum(lane) == 0 || lane.Scanned != tracingBucketSum(lane) {
		t.Fatalf("executions %d, scanned %d", tracingBucketSum(lane), lane.Scanned)
	}
	timed := 0
	for _, b := range lane.Buckets {
		if b.AvgMs != nil {
			timed++
			if *b.AvgMs != 11 || b.MaxMs == nil || *b.MaxMs != 66 || b.TimedCount != b.Count {
				t.Fatalf("bucket %+v avg %v", b, *b.AvgMs)
			}
		}
	}
	if timed == 0 {
		t.Fatal("no bucket carries an average")
	}
}

// Only the saved days that fall in the window are drawn: a window with none leaves the lane as it was.
func TestTracingSQLLaneStaysEmptyWhenTheSavedDayIsOutsideTheWindow(t *testing.T) {
	e, id := demoLaneEnv(t)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/lanes/sql", demoSQLBody("AuditDb")); res.Code != http.StatusOK {
		t.Fatalf("run = %d %s", res.Code, res.Body)
	}
	out, _ := tracingPost(t, e, id, tracingWindowBodyFor("2026-03-10T10:00:00Z", "2026-03-10T11:00:00Z"))
	if lane := tracingLaneOf(t, out, calltrace.LaneSQL); lane.State == calltrace.StateEvents {
		t.Fatalf("lane drawn from a day outside the window: %+v", lane)
	}
}

// Imported slow-statement events and the saved Query Store day are layered, never added: bars and line from Query Store, markers from the files.
func TestLayerSQLLaneKeepsQueryStoreBucketsAndImportedEvents(t *testing.T) {
	held := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true, Scanned: 40,
		ReasonCode: tracingReasonHeldSQL, Sources: []tracingRef{{SourceID: "sql-held", Label: "Sandbox · Query Store"}},
		Buckets: []tracingBucket{{Count: 40}}, Events: []tracingEvent{}}
	imported := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true, Hidden: 2,
		Sources: []tracingRef{{SourceID: "f1", Label: "slow.xel"}},
		Buckets: []tracingBucket{{Count: 7}}, Events: []tracingEvent{{ID: "e1"}}}
	got, ok := layerSQLLane(imported, held)
	if !ok || got.ReasonCode != tracingReasonLayeredSQL {
		t.Fatalf("not layered: %+v", got)
	}
	if got.Scanned != 40 || got.Buckets[0].Count != 40 {
		t.Fatalf("counts were mixed: scanned %d bucket %d", got.Scanned, got.Buckets[0].Count)
	}
	if len(got.Events) != 1 || got.Hidden != 2 || len(got.Sources) != 2 {
		t.Fatalf("events %d hidden %d sources %d", len(got.Events), got.Hidden, len(got.Sources))
	}
	// An imported lane with no events, or one that is not plain events, is left as it is.
	noEvents := imported
	noEvents.Events = nil
	if _, ok := layerSQLLane(noEvents, held); ok {
		t.Fatal("layered a lane with no events")
	}
	incomplete := imported
	incomplete.State = calltrace.StateIncomplete
	if _, ok := layerSQLLane(incomplete, held); ok {
		t.Fatal("layered an incomplete lane")
	}
}

// The imported files' time range and partial flag survive the layering onto the saved day.
func TestHeldSQLLaneKeepsImportedRange(t *testing.T) {
	from := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 20, 10, 30, 0, 0, time.UTC)
	held := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true, Scanned: 5,
		ReasonCode: tracingReasonHeldSQL, Sources: []tracingRef{{SourceID: "sql-held", Label: "Sandbox · Query Store"}},
		Buckets: []tracingBucket{{Count: 5}}, Events: []tracingEvent{}}
	imported := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true,
		ImportedFrom: &from, ImportedTo: &to, ImportedPartial: true,
		Sources: []tracingRef{{SourceID: "f1", Label: "slow.xel"}},
		Buckets: []tracingBucket{{Count: 1}}, Events: []tracingEvent{{ID: "e1"}}}
	got, ok := layerSQLLane(imported, held)
	if !ok {
		t.Fatal("not layered")
	}
	if got.ImportedFrom == nil || !got.ImportedFrom.Equal(from) || got.ImportedTo == nil || !got.ImportedTo.Equal(to) || !got.ImportedPartial {
		t.Fatalf("imported range lost: %v..%v partial %v", got.ImportedFrom, got.ImportedTo, got.ImportedPartial)
	}
}

// heldSQLCase is a case holding one saved SQL day with the given Query Store points and recorded block length.
func heldSQLCase(t *testing.T, series []dbhealth.LaneSeriesPoint, intervalMin int) *healthyCase {
	t.Helper()
	inc := dbhealth.Window{Start: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)}
	run := laneReportJSON(t, "sql", "AuditDb", inc, nil, nil)
	var rep dbhealth.Report
	if err := json.Unmarshal(run.Report, &rep); err != nil {
		t.Fatal(err)
	}
	rep.Lane.Series = series
	if intervalMin > 0 {
		rep.Lane.QueryStore = &dbhealth.LaneQueryStore{State: dbhealth.QSStateOn, IntervalMinutes: intervalMin}
	}
	enc, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	run.Report = enc
	return &healthyCase{stubTraceCase: &stubTraceCase{}, runs: []casestore.HealthRun{run}}
}

func heldSQLTestLane(t *testing.T, series []dbhealth.LaneSeriesPoint, intervalMin int) tracingLane {
	t.Helper()
	c := heldSQLCase(t, series, intervalMin)
	axis := newTracingAxis(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC))
	lane, ok := tracingHeldSQLLane(context.Background(), c, axis, time.Now())
	if !ok {
		t.Fatal("no lane drawn from the saved day")
	}
	return lane
}

// heldBucketAt is the bucket that holds the second t (Unix seconds).
func heldBucketAt(t *testing.T, lane tracingLane, sec int64) tracingBucket {
	t.Helper()
	at := time.Unix(sec, 0).UTC()
	for _, b := range lane.Buckets {
		if !at.Before(b.Start) && at.Before(b.End) {
			return b
		}
	}
	t.Fatalf("no bucket holds %v", at)
	return tracingBucket{}
}

// Each saved block carries its busy time (executions x average, in ms) and the lane its block length; the busy time is not spread here.
func TestHeldSQLLaneCarriesBusyTimeAndBlockLength(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	var series []dbhealth.LaneSeriesPoint
	for i := int64(0); i < 3; i++ {
		series = append(series, dbhealth.LaneSeriesPoint{T: t0 + i*900, Execs: 100, AvgMs: 20, MaxMs: 90})
	}
	lane := heldSQLTestLane(t, series, 0)
	if lane.BlockSeconds != 900 {
		t.Fatalf("blockSeconds %d, want 900", lane.BlockSeconds)
	}
	for i := int64(0); i < 3; i++ {
		b := heldBucketAt(t, lane, t0+i*900)
		if b.BusyMs == nil || *b.BusyMs != 2000 {
			t.Fatalf("block %d busyMs %v, want 2000", i, b.BusyMs)
		}
	}
	raw, err := json.Marshal(lane)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"blockSeconds":900`) || !strings.Contains(string(raw), `"busyMs":2000`) {
		t.Errorf("lane JSON lacks blockSeconds or busyMs: %.300s", raw)
	}
}

// One block and no recorded interval: the block length is not known and the key is left out.
func TestHeldSQLLaneOmitsBlockSecondsForOnePointWithoutInterval(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	lane := heldSQLTestLane(t, []dbhealth.LaneSeriesPoint{{T: t0, Execs: 10, AvgMs: 5}}, 0)
	if lane.BlockSeconds != 0 {
		t.Fatalf("blockSeconds %d, want 0", lane.BlockSeconds)
	}
	raw, _ := json.Marshal(lane)
	if strings.Contains(string(raw), "blockSeconds") {
		t.Errorf("blockSeconds present: %.300s", raw)
	}
	// A gap that is not a Query Store length is not taken as the block length either.
	odd := heldSQLTestLane(t, []dbhealth.LaneSeriesPoint{{T: t0, Execs: 1, AvgMs: 1}, {T: t0 + 420, Execs: 1, AvgMs: 1}}, 0)
	if odd.BlockSeconds != 0 {
		t.Errorf("a 7 minute gap gave blockSeconds %d, want 0", odd.BlockSeconds)
	}
}

// The interval the saved day recorded wins over the gap between blocks.
func TestHeldSQLLaneIntervalWinsOverGap(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	series := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 10, AvgMs: 5}, {T: t0 + 900, Execs: 10, AvgMs: 5}}
	if lane := heldSQLTestLane(t, series, 60); lane.BlockSeconds != 3600 {
		t.Fatalf("blockSeconds %d, want 3600", lane.BlockSeconds)
	}
}

// A block that was read with nothing run has busyMs 0 (not nil); a bucket with no saved block has none.
func TestHeldSQLLaneZeroExecutionsGiveZeroBusy(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	series := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 0}, {T: t0 + 900, Execs: 100, AvgMs: 20}}
	lane := heldSQLTestLane(t, series, 0)
	if b := heldBucketAt(t, lane, t0); b.BusyMs == nil || *b.BusyMs != 0 || b.AvgMs != nil {
		t.Fatalf("empty block %+v, want busyMs 0 and no average", b)
	}
	if b := heldBucketAt(t, lane, t0+300); b.BusyMs != nil {
		t.Errorf("bucket with no saved block has busyMs %v", *b.BusyMs)
	}
	// Two blocks in one bucket are added.
	two := heldSQLTestLane(t, []dbhealth.LaneSeriesPoint{{T: t0, Execs: 100, AvgMs: 20}, {T: t0 + 1, Execs: 50, AvgMs: 10}}, 0)
	if b := heldBucketAt(t, two, t0); b.BusyMs == nil || *b.BusyMs != 2500 {
		t.Errorf("two points in one bucket busyMs %v, want 2500", b.BusyMs)
	}
}
