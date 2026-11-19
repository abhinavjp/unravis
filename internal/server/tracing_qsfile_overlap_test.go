package server

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
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

func TestQSFileBucketsSpreadAStraddlingIntervalOverItsMinutes(t *testing.T) {
	// BL-037: a 15-minute interval is spread over the minutes it runs in the window,
	// not counted whole in the first one. The part before the window is left out.
	axis := newTracingAxis(tracingLaneT0.Add(5*time.Minute), tracingLaneT0.Add(time.Hour))
	straddle := qsFileTestEvent(tracingLaneT0, "100", 20, 90) // 10:00 to 10:15; 10 of its 15 minutes are in the window
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
	// Two thirds of 100 x 20 ms (rounded to 67 executions) plus all of 50 x 10 ms.
	if total != 117 || math.Abs(busy-(2000.0*2/3+500)) > 1e-6 {
		t.Fatalf("executions %d, busy %v ms; want 117 and %v", total, busy, 2000.0*2/3+500)
	}
	if axis.WidthMs != 60000 {
		t.Fatalf("axis step %d ms, want one minute", axis.WidthMs)
	}
	for m := 5; m < 30; m++ {
		at := tracingLaneT0.Add(time.Duration(m) * time.Minute)
		b := cells[int((at.UnixMilli()-axis.AxisStart.UnixMilli())/axis.WidthMs)]
		want, wantMax := 2000.0/15, 90.0
		if m >= 15 {
			want, wantMax = 500.0/15, 40
		}
		if b == nil || b.BusyMs == nil || math.Abs(*b.BusyMs-want) > 1e-6 || b.MaxMs == nil || *b.MaxMs != wantMax {
			t.Fatalf("minute %d: bucket %+v, want busy %v ms and max %v", m, b, want, wantMax)
		}
	}
	if b := cells[int((tracingLaneT0.Add(30*time.Minute).UnixMilli()-axis.AxisStart.UnixMilli())/axis.WidthMs)]; b != nil {
		t.Fatalf("a bucket after both intervals holds %+v", b)
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
	// 67 of the straddling interval's 100 executions fall in the window (BL-037), plus the 50 inside.
	if got.ReasonCode != tracingReasonQSFile || got.Scanned != 117 || tracingBucketSum(got) != 117 {
		t.Fatalf("reason %s, scanned %d, bucket sum %d; want qs-file, 117, 117", got.ReasonCode, got.Scanned, tracingBucketSum(got))
	}
	b := heldBucketAt(t, got, axis.Start.Unix())
	if b.BusyMs == nil || math.Abs(*b.BusyMs-2000.0/15) > 1e-6 {
		t.Fatalf("window-start bucket %+v does not hold its minute of the straddling interval", b)
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
	if len(c.queries) != 2 {
		t.Fatalf("expected a window read and an early read, got %+v", c.queries)
	}
	if !c.queries[0].Window.From.Equal(axis.Start) || !c.queries[0].Window.To.Equal(axis.End) {
		t.Fatalf("window read %+v is not the window %v..%v", c.queries[0].Window, axis.Start, axis.End)
	}
	if !c.queries[1].Window.From.Equal(axis.Start.Add(-qsFileLookback)) || !c.queries[1].Window.To.Equal(axis.Start) {
		t.Fatalf("early read %+v does not cover %v..%v", c.queries[1].Window, axis.Start.Add(-qsFileLookback), axis.Start)
	}
	// Only the last minute of the 20 h 1 min block is in the window, so it holds that share of the busy time.
	if got.ReasonCode != tracingReasonQSFile {
		t.Fatalf("reason %s; want qs-file", got.ReasonCode)
	}
	b := heldBucketAt(t, got, axis.Start.Unix())
	if want := 40 * 30 * 60.0 / long.End.Sub(long.Start).Seconds(); b.BusyMs == nil || math.Abs(*b.BusyMs-want) > 1e-6 {
		t.Fatalf("window-start bucket %+v, want busy %v ms", b, want)
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
		// The 10:00 interval is kept for the 10 minutes it runs in the window (67 of 100), the 09:45 one is dropped.
		if tracingBucketSum(lane) != 167 || lane.Scanned != 167 {
			t.Fatalf("executions %d, scanned %d; want 167", tracingBucketSum(lane), lane.Scanned)
		}
	})
}

// A-1: rows that began before the window are read separately, so they cannot
// fill the window read's cap. BL-037: the store lists oldest first, so the capped
// one-day look-back holds only the old rows; a shorter look-back is read instead.
// It finds nothing that reaches the window, and no interval seen is as long as
// it, so nothing can be missing: the lane is complete and not truncated.
func TestQSFilePreWindowRowsCannotFillTheWindowCap(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(3*time.Hour))
	rows := make([]evidence.Event, 0, casestore.MaxEventQuery+10)
	for i := 0; i < casestore.MaxEventQuery; i++ {
		rows = append(rows, qsFileTestEvent(axis.Start.Add(-6*time.Hour), "1", 20, 90))
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, qsFileTestEvent(axis.Start.Add(time.Duration(i)*15*time.Minute), "1", 20, 90))
	}
	c := qsFileTestCase(rows)
	got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), tracingLane{Key: calltrace.LaneSQL, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated || !got.Complete || got.Reason != tracingQSFileMsg {
		t.Fatalf("complete=%v truncated=%v reason=%q; want a complete lane", got.Complete, got.Truncated, got.Reason)
	}
	if len(c.queries) != 3 || !c.queries[2].Window.From.Equal(axis.Start.Add(-time.Hour)) {
		t.Fatalf("want the window read, the capped day read and a one-hour read; got %+v", c.queries)
	}
	if tracingBucketSum(got) != 10 || got.Scanned != 10 {
		t.Fatalf("bucket sum %d, scanned %d; want 10", tracingBucketSum(got), got.Scanned)
	}
}

// BL-037: when the look-back had to be shortened because of the row cap and an
// interval at least that long was seen, an earlier one may still reach the
// window, so the lane says the 50,000-row limit was hit.
func TestQSFileShortLookbackWithLongIntervalNamesTheCap(t *testing.T) {
	axis := newTracingAxis(tracingLaneT0, tracingLaneT0.Add(3*time.Hour))
	rows := make([]evidence.Event, 0, casestore.MaxEventQuery+1)
	for i := 0; i < casestore.MaxEventQuery; i++ {
		rows = append(rows, qsFileTestEvent(axis.Start.Add(-6*time.Hour), "1", 20, 90))
	}
	long := qsFileTestEvent(axis.Start, "1", 20, 90)
	long.End = axis.Start.Add(2 * time.Hour) // a 2-hour block: longer than the one-hour look-back
	rows = append(rows, long)
	c := qsFileTestCase(rows)
	got, err := tracingQSFileLane(context.Background(), c, axis, qsFileTestSelection(), tracingLane{Key: calltrace.LaneSQL, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Complete || got.Truncated || !strings.Contains(got.Reason, "hit the 50,000-row limit") {
		t.Fatalf("complete=%v truncated=%v reason=%q; want the cap named", got.Complete, got.Truncated, got.Reason)
	}
}
