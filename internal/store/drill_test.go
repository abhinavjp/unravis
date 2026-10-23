package store

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// drillFixture is one audit document of the mixed set the drill-down parity
// tests run over. Its id lets the tests compare the rows a count covered with
// the rows the drill-down returned, not only their number.
type drillFixture struct {
	name string
	id   bson.ObjectID
	doc  map[string]any
}

// drillFixtures spans every kind (synchronous, asynchronous, hangfire by polling
// type and by user agent, unknown), the status shapes the classifier reads
// (names, numeric text, numbers, Accepted, Failed, missing) and every timing
// shape (complete, never completed, no ApiTime, completed before initiated, an
// unusable ApiTime), so every state, outcome and pending combination occurs.
func drillFixtures(at time.Time) []drillFixture {
	pollings := []struct{ polling, agent string }{
		{"Synchronous ", ""}, {"Asynchronous ", ""}, {"", ""}, {"Hangfire", ""}, {"Asynchronous ", "Hangfire"},
	}
	statuses := []any{"OK", "Accepted", "202", int32(202), "NotFound", "InternalServerError", "Failed", ""}
	timings := []string{"complete", "open", "noapi", "negative", "badapi"}
	var fixtures []drillFixture
	for _, p := range pollings {
		for _, status := range statuses {
			for _, timing := range timings {
				doc := map[string]any{
					"RequestPollingType": p.polling, "StatusCode": status, "RequestInitiatedAt": at,
					"ControllerName": "Payroll", "ActionName": "Run", "_auditMetadataOversized": false,
				}
				if p.agent != "" {
					doc["UserAgent"] = p.agent
				}
				switch timing {
				case "complete":
					doc["RequestCompletedAt"], doc["ApiTime"] = at.Add(500*time.Millisecond), 480.0
				case "noapi":
					doc["RequestCompletedAt"] = at.Add(500 * time.Millisecond)
				case "negative":
					doc["RequestCompletedAt"], doc["ApiTime"] = at.Add(-500*time.Millisecond), 480.0
				case "badapi":
					doc["RequestCompletedAt"], doc["ApiTime"] = at.Add(500*time.Millisecond), "abc"
				}
				id := bson.NewObjectID()
				doc["_id"] = id
				fixtures = append(fixtures, drillFixture{name: fmt.Sprintf("%s|%s|%v|%s", p.polling, p.agent, status, timing), id: id, doc: doc})
			}
		}
	}
	return fixtures
}

func drillRaw(t *testing.T, doc map[string]any) bson.Raw {
	t.Helper()
	encoded, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return bson.Raw(encoded)
}

// drillCounted is what the summary counted for one query: the rows that
// survive its pipeline, each with the state, outcome and kind it derived.
type drillCounted struct {
	id                                  string
	kind, state, outcome, class         string
	pending                             bool
	incomplete, invalid, unclassified   bool
}

func countedByPipeline(t *testing.T, q audit.Query, fixtures []drillFixture) []drillCounted {
	t.Helper()
	summary := buildPipeline(q, q.Start, q.End)
	var counted []drillCounted
	for _, f := range fixtures {
		derived, kept := evalRowSurvives(t, summary, f.doc)
		if !kept {
			continue
		}
		c := drillCounted{id: f.id.Hex(), kind: derived["_auditKind"].(string), state: derived["_auditState"].(string), outcome: derived["_auditOutcome"].(string), class: derived["_statusClass"].(string)}
		c.pending = derived["_auditPending"] == true
		c.incomplete, c.invalid, c.unclassified = c.state == "incomplete", c.state == "invalid", c.outcome == "unknown"
		counted = append(counted, c)
	}
	return counted
}

// wantIDs lists the counted rows a quality count or state covers, using the
// definitions of audit.Quality (not StateMatches) so the test stays independent.
func wantIDs(counted []drillCounted, state string) []string {
	var ids []string
	for _, c := range counted {
		var in bool
		switch state {
		case "complete":
			in = c.state == "valid"
		case "incomplete":
			in = c.incomplete
		case "invalid":
			in = c.invalid
		case "pending":
			in = c.pending
		case "unclassified":
			in = c.unclassified
		default:
			in = true
		}
		if in {
			ids = append(ids, c.id)
		}
	}
	sort.Strings(ids)
	return ids
}

func pipelineDrillIDs(t *testing.T, q audit.Query, cohort [5]string, state string, fixtures []drillFixture) []string {
	t.Helper()
	pipeline := requestPipelineState(q, cohort, state, "source", nil, 10000)
	var ids []string
	for _, f := range fixtures {
		if _, kept := evalRowSurvives(t, pipeline, f.doc); kept {
			ids = append(ids, f.id.Hex())
		}
	}
	sort.Strings(ids)
	return ids
}

func legacyProfile(t *testing.T) (*profile, *fakeClient, func()) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "audit")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p, handle, release
}

func legacyDrillIDs(t *testing.T, p *profile, handle *fakeClient, q audit.Query, cohort [5]string, state string, fixtures []drillFixture) []string {
	t.Helper()
	raws := make([]any, 0, len(fixtures))
	for _, f := range fixtures {
		raws = append(raws, drillRaw(t, f.doc))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var records, rawBytes atomic.Int64
	rows, err := readLegacyRequestRowsState(context.Background(), p, q, cohort, state, nil, nil, 10000, &records, &rawBytes, false, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.request.ID)
		if row.request.State == "" {
			t.Errorf("legacy row %s has no state", row.request.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func legacyQuality(t *testing.T, p *profile, handle *fakeClient, q audit.Query, fixtures []drillFixture) audit.Quality {
	t.Helper()
	raws := make([]any, 0, len(fixtures))
	for _, f := range fixtures {
		raws = append(raws, drillRaw(t, f.doc))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var rows, bytes, records, rawBytes atomic.Int64
	buckets, err := readLegacyBuckets(context.Background(), p, q, q.Start, nil, &rows, &bytes, &records, &rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := audit.BuildAnalysis(q, buckets, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Quality
}

func sameIDs(got, want []string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// TestQualityCountEqualsDrillDownRowsOnBothPaths is the rule-7 property: for
// every kind, outcome and metric filter, each quality count (missing timing,
// unusable timing, unclassified result, pending async) and the complete state
// returns from the drill-down exactly the rows the summary counted, on the
// server pipeline and on the legacy (Mongo 3.4) reader.
func TestQualityCountEqualsDrillDownRowsOnBothPaths(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := queryForTest(now)
	fixtures := drillFixtures(base.Start.Add(time.Second))
	p, handle, release := legacyProfile(t)
	defer release()
	for _, kind := range []string{"synchronous", "asynchronous", "http", "hangfire", "all"} {
		for _, outcome := range []string{"all", "success", "failure", "client_error", "server_error"} {
			for _, metric := range []string{"total", "api"} {
				q := base
				q.SourceIDs, q.Kind, q.Outcome, q.Metric = []string{"source"}, kind, outcome, metric
				t.Run(kind+"/"+outcome+"/"+metric, func(t *testing.T) {
					counted := countedByPipeline(t, q, fixtures)
					if len(counted) == 0 {
						t.Fatal("the summary counted no rows")
					}
					quality := audit.Quality{}
					for _, c := range counted {
						if c.incomplete {
							quality.Incomplete++
						}
						if c.invalid {
							quality.Invalid++
						}
						if c.unclassified {
							quality.UnknownOutcome++
						}
						if c.pending {
							quality.PendingAsync++
						}
					}
					legacy := legacyQuality(t, p, handle, q, fixtures)
					if legacy.Incomplete != quality.Incomplete || legacy.Invalid != quality.Invalid || legacy.UnknownOutcome != quality.UnknownOutcome || legacy.PendingAsync != quality.PendingAsync {
						t.Fatalf("legacy summary %+v disagrees with the pipeline summary %+v", legacy, quality)
					}
					counts := map[string]int64{"incomplete": quality.Incomplete, "invalid": quality.Invalid, "unclassified": quality.UnknownOutcome, "pending": quality.PendingAsync}
					wholeCohort := [5]string{"", "", "", kind, outcome}
					for _, state := range []string{"complete", "incomplete", "invalid", "pending", "unclassified", "all", ""} {
						want := wantIDs(counted, state)
						if n, isQuality := counts[state]; isQuality && int64(len(want)) != n {
							t.Fatalf("%s: summary counted %d, the rows behind it number %d", state, n, len(want))
						}
						if got := pipelineDrillIDs(t, q, wholeCohort, state, fixtures); !sameIDs(got, want) {
							t.Errorf("pipeline drill-down state %q returned %d rows, the summary counted %d", state, len(got), len(want))
						}
						if got := legacyDrillIDs(t, p, handle, q, wholeCohort, state, fixtures); !sameIDs(got, want) {
							t.Errorf("legacy drill-down state %q returned %d rows, the summary counted %d", state, len(got), len(want))
						}
					}
				})
			}
		}
	}
}

// TestCohortDrillDownReturnsExactlyTheCohortRows opens every cohort the summary
// produces (source, controller, action, kind, outcome) under every filter: the
// API key must pass validateCohortQuery and return exactly that cohort's rows.
func TestCohortDrillDownReturnsExactlyTheCohortRows(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := queryForTest(now)
	fixtures := drillFixtures(base.Start.Add(time.Second))
	p, handle, release := legacyProfile(t)
	defer release()
	for _, kind := range []string{"synchronous", "asynchronous", "http", "hangfire", "all"} {
		for _, outcome := range []string{"all", "success", "failure", "client_error", "server_error"} {
			q := base
			q.SourceIDs, q.Kind, q.Outcome, q.Metric = []string{"source"}, kind, outcome, "total"
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				counted := countedByPipeline(t, q, fixtures)
				cohorts := map[[2]string][]string{}
				for _, c := range counted {
					key := [2]string{c.kind, c.outcome}
					cohorts[key] = append(cohorts[key], c.id)
				}
				for key, ids := range cohorts {
					sort.Strings(ids)
					cohort := [5]string{"source", "", "", key[0], key[1]}
					if err := validateCohortQuery(q, cohort); err != nil {
						t.Errorf("cohort %v under %s/%s is not drillable: %v", key, kind, outcome, err)
						continue
					}
					for _, state := range []string{"", "incomplete"} {
						want := ids
						if state == "incomplete" {
							want = nil
							for _, c := range counted {
								if c.kind == key[0] && c.outcome == key[1] && c.state == "incomplete" {
									want = append(want, c.id)
								}
							}
							sort.Strings(want)
						}
						if got := pipelineDrillIDs(t, q, cohort, state, fixtures); !sameIDs(got, want) {
							t.Errorf("pipeline cohort %v state %q returned %d rows, want %d", key, state, len(got), len(want))
						}
						legacyCohort := cohort
						legacyCohort[0] = p.source.ID
						if got := legacyDrillIDs(t, p, handle, q, legacyCohort, state, fixtures); !sameIDs(got, want) {
							t.Errorf("legacy cohort %v state %q returned %d rows, want %d", key, state, len(got), len(want))
						}
					}
				}
			})
		}
	}
}

// TestPendingAsyncStatusSpellingsAgree pins the numeric-status carry-in: a
// pending async row is Accepted by name only. The text "202" and the number 202
// classify as success, on the pipeline and on the legacy path alike, and the
// drill-down lists them under the same state the summary counted them in.
func TestPendingAsyncStatusSpellingsAgree(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome, q.Metric = []string{"source"}, "asynchronous", "all", "total"
	at := q.Start.Add(time.Second)
	var fixtures []drillFixture
	for _, status := range []any{"Accepted", "ACCEPTED", "202", int32(202), int64(202), float64(202)} {
		id := bson.NewObjectID()
		fixtures = append(fixtures, drillFixture{name: fmt.Sprint(status), id: id, doc: map[string]any{
			"_id": id, "RequestPollingType": "Asynchronous ", "StatusCode": status, "RequestInitiatedAt": at,
			"RequestCompletedAt": at.Add(time.Second), "ApiTime": 900.0, "_auditMetadataOversized": false,
		}})
	}
	p, handle, release := legacyProfile(t)
	defer release()
	counted := countedByPipeline(t, q, fixtures)
	pending := wantIDs(counted, "pending")
	if len(pending) != 2 {
		t.Fatalf("pending rows = %d, want the two Accepted spellings", len(pending))
	}
	cohort := [5]string{"", "", "", q.Kind, q.Outcome}
	if got := pipelineDrillIDs(t, q, cohort, "pending", fixtures); !sameIDs(got, pending) {
		t.Errorf("pipeline pending drill-down = %v, want %v", got, pending)
	}
	if got := legacyDrillIDs(t, p, handle, q, cohort, "pending", fixtures); !sameIDs(got, pending) {
		t.Errorf("legacy pending drill-down = %v, want %v", got, pending)
	}
	complete := wantIDs(counted, "complete")
	if len(complete) != 4 {
		t.Fatalf("complete rows = %d, want the four numeric spellings", len(complete))
	}
	if got := pipelineDrillIDs(t, q, cohort, "complete", fixtures); !sameIDs(got, complete) {
		t.Errorf("pipeline complete drill-down = %v, want %v", got, complete)
	}
	if got := legacyDrillIDs(t, p, handle, q, cohort, "complete", fixtures); !sameIDs(got, complete) {
		t.Errorf("legacy complete drill-down = %v, want %v", got, complete)
	}
}
