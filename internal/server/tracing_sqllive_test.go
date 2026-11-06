package server

import (
	"net/http"
	"testing"

	"api-audit-explorer/internal/calltrace"
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
