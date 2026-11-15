package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
)

// A-2: a SQL file that is listed in the case but never imported carries the
// "import the file" reason. A saved Query Store day that says the store was
// switched off must not replace that message: the investigator would lose the
// one action that explains the empty lane. The plain no-data lane (no listed
// file, no reason) is still replaced by the held day, as before.
func TestListedButNotImportedSQLFileKeepsItsReasonOverHeldQueryStoreOff(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	seedQSOffDay := func(t *testing.T, e *caseEnv, id string) {
		t.Helper()
		run := laneReportJSON(t, "sql", "AuditDb", dbhealth.Window{Start: at, End: at.Add(time.Hour)}, nil, nil)
		var rep dbhealth.Report
		if err := json.Unmarshal(run.Report, &rep); err != nil {
			t.Fatal(err)
		}
		rep.Lane.QueryStore = &dbhealth.LaneQueryStore{State: dbhealth.QSStateOff}
		enc, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		run.Report = enc
		run.At = at.Add(2 * time.Hour)
		handle, err := e.store.Open(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Fatal(err)
		}
		defer handle.Close()
		hs, ok := handle.(casestore.HealthStore)
		if !ok {
			t.Fatal("test store cannot save a live day")
		}
		if _, err := hs.AddHealthRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}

	// The listed-but-not-imported SQL file keeps its reason over the held day.
	t.Run("listed but not imported keeps its reason", func(t *testing.T) {
		forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
			seedQSOffDay(t, e, id)
			seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "sql-xel", Label: "File · 192.168.4.2:1655"})
			out, _ := tracingPost(t, e, id, tracingWindowBodyFor(at.Format(time.RFC3339), at.Add(time.Hour).Format(time.RFC3339)))
			lane := tracingLaneOf(t, out, calltrace.LaneSQL)
			if lane.ReasonCode != tracingReasonNotImported {
				t.Fatalf("sql lane reason %q, want %q", lane.ReasonCode, tracingReasonNotImported)
			}
			if lane.State != calltrace.StateNoData {
				t.Fatalf("sql lane state %s, want %s", lane.State, calltrace.StateNoData)
			}
		})
	})

	// Without a listed file the held day still replaces the plain no-data lane.
	t.Run("no listed file keeps the held qs-off reason", func(t *testing.T) {
		forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
			seedQSOffDay(t, e, id)
			out, _ := tracingPost(t, e, id, tracingWindowBodyFor(at.Format(time.RFC3339), at.Add(time.Hour).Format(time.RFC3339)))
			lane := tracingLaneOf(t, out, calltrace.LaneSQL)
			if lane.ReasonCode != tracingReasonQSOff {
				t.Fatalf("sql lane reason %q, want %q", lane.ReasonCode, tracingReasonQSOff)
			}
			if lane.State != calltrace.StateNoneSeen {
				t.Fatalf("sql lane state %s, want %s", lane.State, calltrace.StateNoneSeen)
			}
		})
	})
}
