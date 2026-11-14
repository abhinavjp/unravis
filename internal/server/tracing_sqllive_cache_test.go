package server

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
)

func heldCacheAxis() tracingAxis {
	return newTracingAxis(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC))
}

func heldCachePoint(execs int64) []dbhealth.LaneSeriesPoint {
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix()
	return []dbhealth.LaneSeriesPoint{{T: t0, Execs: execs, AvgMs: 10, MaxMs: 20}}
}

// DATA-15: an unchanged saved day is decoded once, however many Timeline requests read it.
func TestHeldSQLLaneDecodesAnUnchangedDayOnce(t *testing.T) {
	c := heldSQLTwoRunCase(t, heldCachePoint(100), heldCachePoint(7))
	for i := range c.runs {
		c.runs[i].ID, c.runs[i].At = int64(9100+i), time.Date(2026, 10, 21, 8, i, 0, 0, time.UTC)
	}
	before := heldReportDecodes.Load()
	var first tracingLane
	for i := 0; i < 5; i++ {
		lane, ok := tracingHeldSQLLane(context.Background(), c, heldCacheAxis(), time.Now())
		if !ok {
			t.Fatal("no lane drawn")
		}
		if i == 0 {
			first = lane
		} else if lane.Scanned != first.Scanned {
			t.Fatalf("request %d scanned %d, first %d", i, lane.Scanned, first.Scanned)
		}
	}
	if got := heldReportDecodes.Load() - before; got != 2 {
		t.Fatalf("decoded %d reports over 5 requests of 2 saved days, want 2", got)
	}
}

// A day saved again (new id and time) is decoded and drawn from its new figures, never from a stale copy.
func TestHeldSQLLaneDecodesAChangedDayAgain(t *testing.T) {
	c := heldSQLTwoRunCase(t, heldCachePoint(100), nil)
	c.runs = c.runs[:1]
	c.runs[0].ID, c.runs[0].At = 9200, time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	lane, ok := tracingHeldSQLLane(context.Background(), c, heldCacheAxis(), time.Now())
	if !ok || lane.Scanned != 100 {
		t.Fatalf("first lane ok=%v scanned=%d", ok, lane.Scanned)
	}
	again := heldSQLTwoRunCase(t, heldCachePoint(250), nil)
	again.runs = again.runs[:1]
	again.runs[0].ID, again.runs[0].At = 9201, time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	c.runs = again.runs
	lane, ok = tracingHeldSQLLane(context.Background(), c, heldCacheAxis(), time.Now())
	if !ok || lane.Scanned != 250 {
		t.Fatalf("changed day: ok=%v scanned=%d, want 250", ok, lane.Scanned)
	}
}

// Two cases can hold a run with the same id and time but different figures: the cache must not mix them up.
func TestHeldSQLLaneCacheDoesNotMixCasesWithTheSameRunId(t *testing.T) {
	stamp := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	a := heldSQLTwoRunCase(t, heldCachePoint(11), nil)
	b := heldSQLTwoRunCase(t, heldCachePoint(22), nil)
	a.runs, b.runs = a.runs[:1], b.runs[:1]
	a.runs[0].ID, a.runs[0].At, b.runs[0].ID, b.runs[0].At = 9300, stamp, 9300, stamp
	la, _ := tracingHeldSQLLane(context.Background(), a, heldCacheAxis(), time.Now())
	lb, _ := tracingHeldSQLLane(context.Background(), b, heldCacheAxis(), time.Now())
	if la.Scanned != 11 || lb.Scanned != 22 {
		t.Fatalf("case a scanned %d (want 11), case b scanned %d (want 22)", la.Scanned, lb.Scanned)
	}
}

// A report that cannot be decoded stays skipped, as before.
func TestHeldSQLLaneSkipsAnUndecodableDay(t *testing.T) {
	c := heldSQLTwoRunCase(t, heldCachePoint(5), nil)
	c.runs = append([]casestore.HealthRun{{ID: 9400, Kind: "sql", Report: []byte("not json")}}, c.runs[:1]...)
	for i := 0; i < 2; i++ {
		lane, ok := tracingHeldSQLLane(context.Background(), c, heldCacheAxis(), time.Now())
		if !ok || lane.Scanned != 5 {
			t.Fatalf("request %d: ok=%v scanned=%d, want 5", i, ok, lane.Scanned)
		}
	}
}
