package server

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/sqlserver"
)

func qsFileTestEvent(at time.Time, executions string, avg, mx float64) evidence.Event {
	return evidence.Event{Source: sqlserver.QSName, Kind: sqlserver.KindQueryInterval,
		Host: "web-a", Start: at, End: at.Add(15 * time.Minute), DurationMs: &avg, Unit: "ms", TZRule: clock.RuleUTCField,
		Attrs: map[string]string{"executions": executions, "max_duration_ms": strconv.FormatFloat(mx, 'f', -1, 64)}}
}

func qsFileTestSelection() tracingSelection {
	return tracingSelection{Refs: []tracingRef{{SourceID: "imp_1", Label: "export.csv"}}, ImportOf: map[string]int64{"imp_1": 1}}
}

func qsFileTestCase(rows []evidence.Event) *laneGoldenCase {
	c := &laneGoldenCase{rows: rows}
	c.imports = []casestore.ImportRecord{goldenImport(1, sqlserver.QSName, "utc-field", tracingLaneT0.Add(time.Hour))}
	return c
}

func TestQSFileIntervalsDrawVolumesAndImportTime(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		rows := []evidence.Event{qsFileTestEvent(tracingLaneT0, "100", 20, 90), qsFileTestEvent(tracingLaneT0.Add(15*time.Minute), "100", 20, 90)}
		imp := seedTracingImport(t, e.store, id, sqlserver.QSName, "export.csv", rows)
		before := tracingFingerprint(t, e.store, id)
		out, raw := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		lane := tracingLaneOf(t, out, calltrace.LaneSQL)
		if lane.ReasonCode != tracingReasonQSFile || lane.BlockSeconds != 900 || !lane.Complete || lane.Truncated {
			t.Fatalf("reason %s, blockSeconds %d, complete %v, truncated %v", lane.ReasonCode, lane.BlockSeconds, lane.Complete, lane.Truncated)
		}
		if lane.Origin != tracingOriginCase || !lane.AsOf.Equal(imp.Imported) || lane.Consistency != tracingConsStored {
			t.Fatal("file origin or import time was not retained")
		}
		if tracingBucketSum(lane) != 200 || lane.Scanned != 200 || len(lane.Events) != 2 {
			t.Fatalf("executions %d, scanned %d, events %d", tracingBucketSum(lane), lane.Scanned, len(lane.Events))
		}
		for _, row := range rows {
			b := heldBucketAt(t, lane, row.Start.Unix())
			if b.Count != 100 || b.BusyMs == nil || *b.BusyMs != 2000 || b.AvgMs == nil || *b.AvgMs != 20 || b.MaxMs == nil || *b.MaxMs != 90 {
				t.Fatal("interval bucket lacks its execution-weighted figures")
			}
			if b.P50Ms != nil || b.P95Ms != nil {
				t.Fatal("file averages invented percentiles")
			}
		}
		if b := heldBucketAt(t, lane, tracingLaneT0.Add(5*time.Minute).Unix()); b.BusyMs != nil {
			t.Fatal("busy time was spread into an unrecorded bucket")
		}
		if !strings.Contains(raw, `"busyMs":2000`) || !strings.Contains(raw, `"blockSeconds":900`) || !strings.Contains(raw, `"reasonCode":"qs-file"`) {
			t.Fatal("wire output lacks file figures")
		}
		if tracingFingerprint(t, e.store, id) != before {
			t.Fatal("tracing changed the imported case")
		}
	})
}

func TestQSFileBucketsWeightAveragesAndUseHalfOpenStarts(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0.Add(30*time.Second), tracingLaneT0.Add(time.Hour))
	rows := []evidence.Event{
		qsFileTestEvent(axis.Start, "100", 20, 90),
		qsFileTestEvent(axis.Start.Add(time.Second), "50", 10, 120),
		qsFileTestEvent(axis.Start.Add(-time.Millisecond), "999", 100, 1000),
		qsFileTestEvent(axis.End, "999", 100, 1000),
	}
	other := qsFileTestEvent(axis.Start, "999", 100, 1000)
	other.Kind = "wait"
	rows = append(rows, other)
	buckets, block := qsFileBuckets(rows, axis)
	b := buckets[0]
	if len(buckets) != 1 || block != 900 || b == nil || b.Count != 150 || !b.Clipped || !b.Complete {
		t.Fatal("start-based clipped bucket or interval length is wrong")
	}
	if b.BusyMs == nil || *b.BusyMs != 2500 || b.AvgMs == nil || *b.AvgMs != 2500.0/150 || b.MaxMs == nil || *b.MaxMs != 120 {
		t.Fatal("same-bucket totals or weighted average is wrong")
	}
}

func TestQSFileBlockSecondsUsesMostCommonLength(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	rows := []evidence.Event{qsFileTestEvent(axis.Start, "1", 1, 1), qsFileTestEvent(axis.Start.Add(time.Minute), "1", 1, 1), qsFileTestEvent(axis.Start.Add(2*time.Minute), "1", 1, 1)}
	rows[0].End = rows[0].Start.Add(30 * time.Minute)
	if _, block := qsFileBuckets(rows, axis); block != 900 {
		t.Fatalf("modal interval %d, want 900", block)
	}
	if _, block := qsFileBuckets(rows[:2], axis); block != 900 {
		t.Fatalf("tied interval %d, want shorter 900", block)
	}
}

func TestQSFileBrokenExecutionsAreSkippedAndIncomplete(t *testing.T) {
	for _, executions := range []string{"broken", "", "-1", "1.5", "9223372036854775808"} {
		t.Run(executions, func(t *testing.T) {
			axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
			rows := []evidence.Event{qsFileTestEvent(axis.Start, "100", 20, 90), qsFileTestEvent(axis.Start.Add(15*time.Minute), executions, 20, 90)}
			lane := tracingLane{Key: calltrace.LaneSQL, State: calltrace.StateEvents, Complete: true, Events: []tracingEvent{{ID: "kept"}}}
			got, err := tracingQSFileLane(context.Background(), qsFileTestCase(rows), axis, qsFileTestSelection(), lane)
			if err != nil || got.Complete || got.ReasonCode != tracingReasonQSFile || got.Scanned != 100 || !strings.Contains(got.Reason, "1 Query Store rows were skipped") {
				t.Fatal("unreadable execution count was not skipped and counted")
			}
			b := heldBucketAt(t, got, rows[1].Start.Unix())
			if b.Count != 0 || b.BusyMs != nil || b.AvgMs != nil || b.MaxMs != nil || b.Complete {
				t.Fatal("skipped row invented a recorded figure")
			}
			if !reflect.DeepEqual(got.Events, lane.Events) {
				t.Fatal("file fallback changed event markers")
			}
		})
	}
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, sqlserver.QSName, "broken.csv", []evidence.Event{qsFileTestEvent(tracingLaneT0, "broken", 20, 90)})
		out, _ := tracingPost(t, e, id, tracingLaneBody(tracingImportID(imp.ID)))
		lane := tracingLaneOf(t, out, calltrace.LaneSQL)
		if lane.Complete || lane.Scanned != 0 || lane.ReasonCode != tracingReasonQSFile {
			t.Fatal("handler did not mark a skipped export incomplete")
		}
	})
}

func TestQSFileMissingDurationIsNotZero(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	row := qsFileTestEvent(axis.Start, "100", 20, 0)
	row.DurationMs = nil
	got, err := tracingQSFileLane(context.Background(), qsFileTestCase([]evidence.Event{row}), axis, qsFileTestSelection(), tracingLane{Key: "sql", Complete: true})
	if err != nil || got.Complete {
		t.Fatal("missing duration claimed complete totals")
	}
	b := heldBucketAt(t, got, axis.Start.Unix())
	if b.Count != 100 || b.BusyMs != nil || b.AvgMs != nil || b.MaxMs == nil || *b.MaxMs != 0 {
		t.Fatal("missing timing became zero, or a recorded zero maximum was lost")
	}
	row.DurationMs = new(float64)
	row.Attrs["executions"] = "0"
	cells, _ := qsFileBuckets([]evidence.Event{row}, axis)
	if b := cells[0]; b.BusyMs == nil || *b.BusyMs != 0 || b.AvgMs != nil || !b.Complete {
		t.Fatal("recorded zero executions did not retain zero busy time")
	}
}

func TestQSFileHeldAndLayeredLanesAreUnchanged(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	axis := newTracingAxis(at, at.Add(time.Hour))
	c := heldSQLCase(t, []dbhealth.LaneSeriesPoint{{T: at.Unix(), Execs: 7, AvgMs: 3, MaxMs: 9}}, 15)
	held, ok := tracingHeldSQLLane(context.Background(), c, axis, at)
	if !ok {
		t.Fatal("saved live day was not available")
	}
	layered, ok := layerSQLLane(tracingLane{State: calltrace.StateEvents, Complete: true, Events: []tracingEvent{{ID: "marker"}}}, held)
	if !ok {
		t.Fatal("saved live day did not layer")
	}
	for _, lane := range []tracingLane{held, layered} {
		// A nil case panics if the fallback tries to read anything at all.
		got, err := tracingQSFileLane(context.Background(), nil, axis, qsFileTestSelection(), lane)
		if err != nil || !reflect.DeepEqual(got, lane) {
			t.Fatal("held or layered lane was changed")
		}
	}
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		handle, err := e.store.Open(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Fatal(err)
		}
		hs, ok := handle.(casestore.HealthStore)
		if !ok {
			handle.Close()
			t.Fatal("test store cannot save a live day")
		}
		_, err = hs.AddHealthRun(context.Background(), c.runs[0])
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		body := tracingWindowBodyFor(at.Format(time.RFC3339), at.Add(time.Hour).Format(time.RFC3339))
		before, _ := tracingPost(t, e, id, body)
		baseline := tracingLaneOf(t, before, "sql")
		// A file outside the window leaves the held lane intact.
		outside := qsFileTestEvent(at.Add(-24*time.Hour), "100", 20, 90)
		imp := seedSpannedImport(t, e.store, id, sqlserver.QSName, "outside.csv", []evidence.Event{outside}, &outside.Start, &outside.End)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: sqlserver.QSName, Label: "outside.csv", ImportID: imp.ID})
		out, _ := tracingPost(t, e, id, body)
		if lane := tracingLaneOf(t, out, "sql"); lane.ReasonCode != tracingReasonHeldSQL || !reflect.DeepEqual(lane.Buckets, baseline.Buckets) {
			t.Fatal("file displaced a held live day")
		}
		imp = seedTracingImport(t, e.store, id, sqlserver.QSName, "inside.csv", []evidence.Event{qsFileTestEvent(at, "100", 20, 90)})
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: sqlserver.QSName, Label: "inside.csv", ImportID: imp.ID})
		out, _ = tracingPost(t, e, id, body)
		if lane := tracingLaneOf(t, out, "sql"); lane.ReasonCode != tracingReasonLayeredSQL || !reflect.DeepEqual(lane.Buckets, baseline.Buckets) || len(lane.Events) != 1 {
			t.Fatal("file displaced layered live-day totals or lost markers")
		}
	})
}

func TestQSFileNoIntervalsLeavesLaneUnchanged(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	for _, rows := range [][]evidence.Event{nil, {qsFileTestEvent(axis.End, "100", 20, 90)}, {{Source: sqlserver.QSName, Kind: "wait", Start: axis.Start}}} {
		lane := tracingLane{Key: "sql", State: calltrace.StateEvents, Complete: true, Scanned: 1, Buckets: []tracingBucket{{Count: 1}}, Events: []tracingEvent{{ID: "kept"}}}
		got, err := tracingQSFileLane(context.Background(), qsFileTestCase(rows), axis, qsFileTestSelection(), lane)
		if err != nil || !reflect.DeepEqual(got, lane) {
			t.Fatal("fallback changed a lane without query intervals")
		}
	}
	// An unselected or unvalidated export cannot supply the lane.
	c := qsFileTestCase([]evidence.Event{qsFileTestEvent(axis.Start, "100", 20, 90)})
	for _, status := range []string{casestore.StatusQuarantined, casestore.StatusDuplicate} {
		c.imports[0].Status = status
		lane := tracingLane{Key: "sql", Complete: true}
		got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), lane)
		if err != nil || !reflect.DeepEqual(got, lane) || len(c.queries) != 0 {
			t.Fatal("set-aside file was read")
		}
	}
}

type qsFileBudgetCase struct {
	*stubTraceCase
	rows []evidence.Event
	err  error
}

func (c *qsFileBudgetCase) Events(_ context.Context, q casestore.EventQuery) ([]evidence.Event, error) {
	c.queries = append(c.queries, q)
	if q.Limit != casestore.MaxEventQuery || q.MaxScanTime != tracingLaneBudget || !reflect.DeepEqual(q.Sources, []string{sqlserver.QSName}) {
		return nil, errors.New("file read did not use the existing cap and budget")
	}
	return c.rows, c.err
}

func TestQSFileCapAndTimeoutKeepPartialTotals(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	for _, timedOut := range []bool{false, true} {
		rows := []evidence.Event{qsFileTestEvent(axis.Start, "2", 20, 90)}
		var readErr error
		if timedOut {
			readErr = casestore.ErrScanBudget
		} else {
			rows = make([]evidence.Event, casestore.MaxEventQuery)
			for i := range rows {
				rows[i] = qsFileTestEvent(axis.Start, "2", 20, 90)
			}
		}
		c := &qsFileBudgetCase{stubTraceCase: &qsFileTestCase(nil).stubTraceCase, rows: rows, err: readErr}
		lane := tracingLane{Key: "sql", Complete: true, Events: []tracingEvent{{ID: "kept"}}}
		got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), lane)
		if err != nil || got.Complete || !got.Truncated || got.ReasonCode != tracingReasonQSFile || got.Scanned != int64(len(rows))*2 {
			t.Fatal("partial file read lost its totals or completeness flag")
		}
		if timedOut && !strings.Contains(got.Reason, tracingTimeoutMsg) || !timedOut && !strings.Contains(got.Reason, "Only the first 50,000 saved events were listed") {
			t.Fatal("partial read lacks the existing limit wording")
		}
		if strings.Contains(got.Reason, "bucket counts cover every saved event") || !reflect.DeepEqual(got.Events, lane.Events) {
			t.Fatal("capped file claimed exact totals or changed markers")
		}
		for _, b := range got.Buckets {
			if b.Complete {
				t.Fatal("partial file bucket claims complete")
			}
		}
	}
}

func TestQSFileAsOfUsesExportImportNotOtherSQL(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(time.Hour))
	c := qsFileTestCase([]evidence.Event{qsFileTestEvent(axis.Start, "100", 20, 90)})
	c.imports = append(c.imports, goldenImport(2, "sql-xel", "utc-field", tracingLaneT0.Add(2*time.Hour)))
	sel := qsFileTestSelection()
	sel.Refs = append(sel.Refs, tracingRef{SourceID: "imp_2"})
	sel.ImportOf["imp_2"] = 2
	got, err := tracingQSFileLane(context.Background(), c, axis, sel, tracingLane{Key: "sql", Complete: true})
	if err != nil || !got.AsOf.Equal(c.imports[0].Imported) {
		t.Fatal("asOf used another SQL file's import time")
	}
}
