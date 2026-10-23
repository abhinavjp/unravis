package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// tileDrillCount is the number of rows the request list shows after the UI
// drills from an overview tile: the row's kind (unknown rows use http_unknown),
// the tile's outcome when it has one and the tile's state filter.
func tileDrillCount(t *testing.T, d *demoRepository, q Query, kind, outcome, state string) int {
	t.Helper()
	q.Kind = kind
	if kind == KindUnknown {
		q.Kind = KindHTTPUnknown
	}
	if outcome != "" {
		q.Outcome = outcome
	}
	got, _ := demoDrillCount(t, d, q, state)
	return got
}

// demoDrillRepoWithValidUnknown adds a valid, complete row of unknown request
// type to demoDrillRepo. Under a Synchronous or Asynchronous query it is
// removed from the overview (valid rows need the exact kind), but the
// http_unknown drill of the unknown row's tiles lists it.
func demoDrillRepoWithValidUnknown(now time.Time) *demoRepository {
	d := demoDrillRepo(now)
	initiated := now.Add(-5 * time.Minute)
	duration := 120.0
	d.records = append(d.records, RequestRecord{ID: "x-unknown-kind-valid", SourceID: "demo-west", Controller: "Payroll", Action: "Run", Kind: "unknown", State: "valid", Status: "200 OK", Outcome: "success", InitiatedAt: &initiated, DurationMs: &duration, ApiTimeMs: &duration})
	return d
}

// TestDemoOverviewTileCountsMatchTheDrilledList compares every overview tile
// with the request list its drill opens, through the demo repository. The
// Completed tile and the Events tile of the Hangfire and unknown rows are
// exact. On the Synchronous and Asynchronous rows the list also holds the
// incomplete and invalid rows of unknown request type (they cannot be told
// apart), so the Events tile differs by exactly the unknown row's
// incomplete+invalid count. Under client_error and server_error the summary
// keeps every row whose result is unknown, so those two tiles differ by at most
// the row's own unknown results plus (on Synchronous and Asynchronous) that
// same unknown-type count. The UI states these numbers. The unknown row's
// tiles under a Synchronous or Asynchronous query are different: the overview
// there leaves out valid rows of unknown request type but the http_unknown drill
// lists them, so the gap has no bound and the UI states that instead of a
// number.
func TestDemoOverviewTileCountsMatchTheDrilledList(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := demoDrillRepoWithValidUnknown(now)
	sawGap, sawUnknownGap := false, false
	for _, kind := range []string{"all", "http", "synchronous", "asynchronous", "hangfire", "http_unknown"} {
		q := demoQuery(now)
		q.Kind = kind
		analysis, err := d.Analyze(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		unusable := int64(0)
		for _, row := range analysis.Overview {
			if row.Kind == KindUnknown {
				unusable = row.Incomplete + row.Invalid
			}
		}
		for _, row := range analysis.Overview {
			label := fmt.Sprintf("%s/%s", kind, row.Kind)
			compatible := row.Kind == "synchronous" || row.Kind == "asynchronous"
			// The same condition the UI uses to add its unbounded unknown-kind note.
			unknownKindGap := row.Kind == KindUnknown && (kind == "synchronous" || kind == "asynchronous")
			events := int64(tileDrillCount(t, d, q, row.Kind, "", "all"))
			completed := int64(tileDrillCount(t, d, q, row.Kind, "", "complete"))
			client := int64(tileDrillCount(t, d, q, row.Kind, "client_error", "all"))
			server := int64(tileDrillCount(t, d, q, row.Kind, "server_error", "all"))
			if unknownKindGap {
				if events <= row.Events || completed <= row.Completed {
					t.Errorf("%s: the valid unknown-kind row must make the list (%d events, %d complete) larger than the tiles (%d, %d)", label, events, completed, row.Events, row.Completed)
				}
				if client < row.ClientErrors || server < row.ServerErrors {
					t.Errorf("%s: the list (%d 4xx, %d 5xx) must not be smaller than the tiles (%d, %d)", label, client, server, row.ClientErrors, row.ServerErrors)
				}
				sawUnknownGap = true
				continue
			}
			if completed != row.Completed {
				t.Errorf("%s: Completed tile %d, drilled list %d", label, row.Completed, completed)
			}
			wantEventsGap, classBound := int64(0), row.UnknownOutcome
			if compatible {
				wantEventsGap, classBound = unusable, row.UnknownOutcome+unusable
			}
			if events-row.Events != wantEventsGap {
				t.Errorf("%s: Events tile %d, list %d, the stated gap is %d", label, row.Events, events, wantEventsGap)
			}
			if gap := client - row.ClientErrors; gap < 0 || gap > classBound {
				t.Errorf("%s: 4xx tile %d, list %d, want a gap within %d", label, row.ClientErrors, client, classBound)
			}
			if gap := server - row.ServerErrors; gap < 0 || gap > classBound {
				t.Errorf("%s: 5xx tile %d, list %d, want a gap within %d", label, row.ServerErrors, server, classBound)
			}
			if events != row.Events {
				sawGap = true
			}
		}
	}
	if !sawGap {
		t.Fatal("the fixture must contain unknown-kind incomplete rows so the gap is exercised")
	}
	if !sawUnknownGap {
		t.Fatal("the fixture must contain a valid unknown-kind row so the unknown tiles' unbounded gap is exercised")
	}
}

// TestRollupsOfTenThousandCohortsUseUnderHalfTheResponseCap measures the
// worst case of the Task 12 rollup growth: every cohort its own API (the
// cohort cap), every optional field present and long names.
func TestRollupsOfTenThousandCohortsUseUnderHalfTheResponseCap(t *testing.T) {
	const maxResponseBytes = 16 << 20 // the server's response cap
	q := analysisQuery()
	long := strings.Repeat("N", 60)
	var current, reference []Bucket
	for i := 0; i < MaxCohorts; i++ {
		controller, action := fmt.Sprintf("%s%05d", long, i), fmt.Sprintf("%sAction%05d", long, i)
		current = append(current, withClasses(makeBuckets("s1", controller, action, "synchronous", "failure", "valid", anchor, 25, 10), 12, 13, 0)...)
		reference = append(reference, withClasses(makeBuckets("s1", controller, action, "synchronous", "failure", "valid", anchor.Add(-time.Minute), 25, 8), 5, 20, 0)...)
	}
	analysis, err := BuildAnalysis(q, current, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Rollups) != MaxCohorts {
		t.Fatalf("rollups = %d, want %d", len(analysis.Rollups), MaxCohorts)
	}
	r := analysis.Rollups[0]
	if r.ErrorRate == nil || r.ClientErrorRate == nil || r.ServerErrorRate == nil || r.CompletionRate == nil || r.P95Ms == nil ||
		r.ReferenceErrorRate == nil || r.ReferenceClientErrorRate == nil || r.ReferenceServerErrorRate == nil || r.ReferenceCompletionRate == nil || r.ReferenceP95Ms == nil ||
		r.TrafficDelta == nil || r.CompletionDelta == nil || r.ClientErrorDelta == nil || r.ServerErrorDelta == nil || r.P95DeltaMs == nil {
		t.Fatalf("the worst case needs every optional field present: %+v", r)
	}
	encoded, err := json.Marshal(analysis.Rollups)
	if err != nil {
		t.Fatal(err)
	}
	full, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	// What the rollups carried before Task 12 added the reference and delta
	// fields: the same rows without them.
	legacy := make([]Rollup, len(analysis.Rollups))
	for i, r := range analysis.Rollups {
		legacy[i] = Rollup{Controller: r.Controller, Action: r.Action, Kind: r.Kind, Requests: r.Requests, Failures: r.Failures, ErrorRate: r.ErrorRate, PerMinute: r.PerMinute, ClientErrorRate: r.ClientErrorRate, ServerErrorRate: r.ServerErrorRate}
	}
	before, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	apis, err := json.Marshal(analysis.APIs)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d rollups marshal to %d bytes (%.1f%% of the %d byte response cap), %d of them before Task 12; the APIs alone are %d bytes and the whole analysis %d bytes", len(analysis.Rollups), len(encoded), 100*float64(len(encoded))/maxResponseBytes, maxResponseBytes, len(before), len(apis), len(full))
	if len(encoded) > maxResponseBytes/2 {
		t.Fatalf("rollups alone take %d bytes, more than half of the %d byte response cap", len(encoded), maxResponseBytes)
	}
}
