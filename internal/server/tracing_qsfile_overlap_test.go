package server

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/sqlserver"
)

// DATA-10: an export interval that began before the window but still runs into it
// belongs to the window. Only an interval that ends at or before the window start,
// or starts at or after the window end, is outside.

func TestQSFileIntervalKeepsOneThatStraddlesTheWindowStart(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0.Add(5*time.Minute), tracingLaneT0.Add(time.Hour))
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"starts before, ends inside", tracingLaneT0, true},
		{"starts one ms before the window, ends inside", axis.Start.Add(-time.Millisecond), true},
		{"starts at the window start", axis.Start, true},
		{"starts inside", axis.Start.Add(time.Minute), true},
		{"ends exactly at the window start", axis.Start.Add(-15 * time.Minute), false},
		{"ends well before the window", tracingLaneT0.Add(-2 * time.Hour), false},
		{"starts at the window end", axis.End, false},
		{"starts after the window", axis.End.Add(time.Hour), false},
	}
	for _, c := range cases {
		e := qsFileTestEvent(c.at, "10", 5, 9)
		if got := qsFileInterval(e, axis); got != c.want {
			t.Errorf("%s: qsFileInterval = %v, want %v", c.name, got, c.want)
		}
	}
	other := qsFileTestEvent(tracingLaneT0, "10", 5, 9)
	other.Kind = "wait"
	if qsFileInterval(other, axis) {
		t.Error("an event of another kind was taken as a Query Store interval")
	}
}

func TestQSFileBucketsCountAStraddlingIntervalOnceInTheFirstBucket(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0.Add(5*time.Minute), tracingLaneT0.Add(time.Hour))
	straddle := qsFileTestEvent(tracingLaneT0, "100", 20, 90)
	inside := qsFileTestEvent(tracingLaneT0.Add(15*time.Minute), "50", 10, 40)
	before := qsFileTestEvent(tracingLaneT0.Add(-15*time.Minute), "999", 100, 1000) // ends at 10:00, before the 10:05 window start
	after := qsFileTestEvent(axis.End, "999", 100, 1000)
	cells, block := qsFileBuckets([]evidence.Event{before, straddle, inside, after}, axis)
	if block != 900 {
		t.Fatalf("block seconds %d, want 900", block)
	}
	var total int64
	var busy float64
	for _, b := range cells {
		total += b.Count
		if b.BusyMs != nil {
			busy += *b.BusyMs
		}
	}
	// 100 x 20 ms + 50 x 10 ms, each interval once.
	if total != 150 || busy != 2500 {
		t.Fatalf("executions %d, busy %v ms; want 150 and 2500 (no drop, no double count)", total, busy)
	}
	first := cells[int((axis.Start.UnixMilli()-axis.AxisStart.UnixMilli())/axis.WidthMs)]
	if first == nil || first.Count != 100 || first.BusyMs == nil || *first.BusyMs != 2000 || first.AvgMs == nil || *first.AvgMs != 20 || first.MaxMs == nil || *first.MaxMs != 90 {
		t.Fatalf("straddling interval is not in the first bucket: %+v", first)
	}
	if !first.Clipped {
		t.Error("the first bucket holds part of an interval from before the window and must say it is clipped")
	}
	if !first.Start.Equal(axis.Start) {
		t.Errorf("first bucket starts %v, want the window start %v", first.Start, axis.Start)
	}
}

func TestQSFileLaneDrawsAStraddlingIntervalAndDropsOutsideOnes(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0.Add(5*time.Minute), tracingLaneT0.Add(time.Hour))
	rows := []evidence.Event{
		qsFileTestEvent(tracingLaneT0, "100", 20, 90),                    // straddles the window start
		qsFileTestEvent(tracingLaneT0.Add(-15*time.Minute), "7", 20, 90), // ends exactly at tracingLaneT0: outside
		qsFileTestEvent(tracingLaneT0.Add(-3*time.Hour), "7", 20, 90),    // far before: outside
		qsFileTestEvent(tracingLaneT0.Add(30*time.Minute), "50", 10, 40), // inside
		qsFileTestEvent(axis.End, "7", 20, 90),                           // at the window end: outside
		qsFileTestEvent(axis.End.Add(15*time.Minute), "7", 20, 90),       // after: outside
	}
	c := qsFileTestCase(rows)
	got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), tracingLane{Key: calltrace.LaneSQL, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ReasonCode != tracingReasonQSFile || got.Scanned != 150 || tracingBucketSum(got) != 150 {
		t.Fatalf("reason %s, scanned %d, bucket sum %d; want qs-file, 150, 150", got.ReasonCode, got.Scanned, tracingBucketSum(got))
	}
	b := heldBucketAt(t, got, axis.Start.Unix())
	if b.Count != 100 || b.BusyMs == nil || *b.BusyMs != 2000 {
		t.Fatalf("window-start bucket %+v does not hold the straddling interval", b)
	}
}

func TestQSFileLaneReadsBackFarEnoughToFindAStraddlingInterval(t *testing.T) {
	// The store lists events by their start time. A read that begins at the window
	// start would never return an interval that began earlier, so the read reaches
	// back by the longest block Query Store can be set to (one day).
	axis := newTracingAxis(tracingLaneT0.Add(5*time.Minute), tracingLaneT0.Add(time.Hour))
	long := qsFileTestEvent(axis.Start.Add(-20*time.Hour), "40", 30, 80)
	long.End = axis.Start.Add(time.Minute) // a long block, still running at the window start
	c := qsFileTestCase([]evidence.Event{long})
	got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), tracingLane{Key: calltrace.LaneSQL, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.queries) != 1 || c.queries[0].Window.From.After(axis.Start.Add(-qsFileLookback)) {
		t.Fatalf("read window %+v does not reach back %v", c.queries, qsFileLookback)
	}
	if got.ReasonCode != tracingReasonQSFile || got.Scanned != 40 {
		t.Fatalf("reason %s, scanned %d; want qs-file and 40", got.ReasonCode, got.Scanned)
	}
}

func TestQSFileStraddlingIntervalThroughTheHandler(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		rows := []evidence.Event{
			qsFileTestEvent(tracingLaneT0, "100", 20, 90),
			qsFileTestEvent(tracingLaneT0.Add(15*time.Minute), "100", 20, 90),
			qsFileTestEvent(tracingLaneT0.Add(-15*time.Minute), "100", 20, 90), // ends at tracingLaneT0: outside
		}
		imp := seedTracingImport(t, e.store, id, sqlserver.QSName, "export.csv", rows)
		body := `{"sourceIds":["` + tracingImportID(imp.ID) + `"],"incident":{"start":"2026-03-10T10:05:00Z","end":"2026-03-10T11:00:00Z"}}`
		out, _ := tracingPost(t, e, id, body)
		lane := tracingLaneOf(t, out, calltrace.LaneSQL)
		if lane.ReasonCode != tracingReasonQSFile {
			t.Fatalf("reason %s, want qs-file", lane.ReasonCode)
		}
		if tracingBucketSum(lane) != 200 || lane.Scanned != 200 {
			t.Fatalf("executions %d, scanned %d; want 200 (the 10:00 interval kept, the 09:45 one dropped)", tracingBucketSum(lane), lane.Scanned)
		}
	})
}
