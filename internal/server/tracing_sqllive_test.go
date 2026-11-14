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

// heldSQLTwoRunCase is a case holding two saved SQL days of the SAME source, newest first, each with the
// given Query Store points. Neither run carries a Key, so both fall back to source = kind|label|database
// ("sql|sql|AuditDb|") and share it: a T they both hold must be counted once, the newest day's value winning.
func heldSQLTwoRunCase(t *testing.T, newer, older []dbhealth.LaneSeriesPoint) *healthyCase {
	t.Helper()
	inc := dbhealth.Window{Start: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)}
	day := func(series []dbhealth.LaneSeriesPoint) casestore.HealthRun {
		run := laneReportJSON(t, "sql", "AuditDb", inc, nil, nil)
		var rep dbhealth.Report
		if err := json.Unmarshal(run.Report, &rep); err != nil {
			t.Fatal(err)
		}
		rep.Lane.Series = series
		enc, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		run.Report = enc
		return run
	}
	// newest first, the order laneHeld returns and tracingHeldSQLLane walks.
	return &healthyCase{stubTraceCase: &stubTraceCase{}, runs: []casestore.HealthRun{day(newer), day(older)}}
}

func heldSQLTwoRunLane(t *testing.T, newer, older []dbhealth.LaneSeriesPoint) tracingLane {
	t.Helper()
	c := heldSQLTwoRunCase(t, newer, older)
	axis := newTracingAxis(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC))
	lane, ok := tracingHeldSQLLane(context.Background(), c, axis, time.Now())
	if !ok {
		t.Fatal("no lane drawn from the two saved days")
	}
	return lane
}

// Two saved days of one source can share a T (their incident windows differ but their reference times
// overlap). The shared T is counted once with the newest day's figures — never summed — while a T only the
// older day holds is kept. This is the DATA-03 overlap proof.
func TestHeldSQLLaneOverlappingRunsDedupeByTNewestWins(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	newer := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 100, AvgMs: 20, MaxMs: 90}}
	older := []dbhealth.LaneSeriesPoint{
		{T: t0, Execs: 50, AvgMs: 10, MaxMs: 40},      // same T as the newer day: must be dropped
		{T: t0 + 900, Execs: 30, AvgMs: 5, MaxMs: 15}, // a T only the older day holds: must be kept
	}
	lane := heldSQLTwoRunLane(t, newer, older)

	// Scanned is the deduped total: 100 (shared T, newer) + 30 (older-only) = 130, not the 180 a sum gives.
	if lane.Scanned != 130 {
		t.Fatalf("scanned %d, want 130 (deduped, newest wins)", lane.Scanned)
	}
	// The shared-T bucket holds ONLY the newer day's contribution, never the sum (150 execs / 2500 busy).
	shared := heldBucketAt(t, lane, t0)
	if shared.Count != 100 || shared.TimedCount != 100 {
		t.Fatalf("shared bucket count %d timed %d, want 100 (newer only)", shared.Count, shared.TimedCount)
	}
	if shared.BusyMs == nil || *shared.BusyMs != 2000 {
		t.Fatalf("shared bucket busyMs %v, want 2000 (newer only, not 2500)", shared.BusyMs)
	}
	if shared.AvgMs == nil || *shared.AvgMs != 20 {
		t.Fatalf("shared bucket avgMs %v, want 20 (newer only)", shared.AvgMs)
	}
	if shared.MaxMs == nil || *shared.MaxMs != 90 {
		t.Fatalf("shared bucket maxMs %v, want 90 (newer only)", shared.MaxMs)
	}
	// A T only the older day holds still appears, with its own figures.
	olderOnly := heldBucketAt(t, lane, t0+900)
	if olderOnly.TimedCount != 30 || olderOnly.BusyMs == nil || *olderOnly.BusyMs != 150 {
		t.Fatalf("older-only bucket %+v, want timed 30 busy 150", olderOnly)
	}
}

// Newest wins even when the newer day's shared T ran nothing: its 0 replaces the older non-zero (no fall
// back), so the bucket is busy 0 with no average and the older day's executions are not added. A T only the
// older day holds keeps the lane non-empty so it is drawn at all.
func TestHeldSQLLaneOverlappingRunsNewestZeroExecsWins(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	newer := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 0, AvgMs: 0}}
	older := []dbhealth.LaneSeriesPoint{
		{T: t0, Execs: 80, AvgMs: 25},      // shared T, older non-zero: must be dropped, not fallen back to
		{T: t0 + 900, Execs: 10, AvgMs: 5}, // older-only: keeps the lane non-empty
	}
	lane := heldSQLTwoRunLane(t, newer, older)
	if lane.Scanned != 10 {
		t.Fatalf("scanned %d, want 10 (the older shared-T 80 must not be counted)", lane.Scanned)
	}
	shared := heldBucketAt(t, lane, t0)
	if shared.BusyMs == nil || *shared.BusyMs != 0 || shared.AvgMs != nil || shared.TimedCount != 0 {
		t.Fatalf("shared bucket %+v, want busy 0, no average, timed 0 (newer 0 wins)", shared)
	}
}

// Dedupe is by exact T, not by bucket: two days holding DIFFERENT T that fall in the SAME bucket are both
// kept and added. (The single-run "two blocks in one bucket" case above asserts the same within one day.)
func TestHeldSQLLaneDifferentTSameBucketStillAdds(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	// t0 and t0+1 are one second apart: the same 60 s bucket, different T. Both must count.
	newer := []dbhealth.LaneSeriesPoint{{T: t0, Execs: 100, AvgMs: 20}}
	older := []dbhealth.LaneSeriesPoint{{T: t0 + 1, Execs: 50, AvgMs: 10}}
	lane := heldSQLTwoRunLane(t, newer, older)
	if lane.Scanned != 150 {
		t.Fatalf("scanned %d, want 150 (different T in one bucket both add)", lane.Scanned)
	}
	b := heldBucketAt(t, lane, t0)
	if b.TimedCount != 150 {
		t.Fatalf("bucket timed %d, want 150", b.TimedCount)
	}
	if b.BusyMs == nil || *b.BusyMs != 2500 {
		t.Fatalf("bucket busyMs %v, want 2500 (100*20 + 50*10)", b.BusyMs)
	}
}
