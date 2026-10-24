package store

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Range filters are checked here without a live MongoDB. The modern pipelines are
// run through the in-process evaluators of pipeline_test.go (plus rgMatch, which
// adds the $gte/$lte range operators evalMatch does not model), the Mongo 3.4
// reader runs on a fake client, and the demo repository runs as itself.

func rgF(v float64) *float64 { return &v }

func rgRange(min, max *float64) *audit.Range { return &audit.Range{Min: min, Max: max} }

func rgBounded(r *audit.Range) bool { return r != nil && (r.Min != nil || r.Max != nil) }

// rgIn is an independent statement of a range: a missing value never matches and
// the lower bound is never below zero.
func rgIn(v *float64, r *audit.Range) bool {
	if !rgBounded(r) {
		return true
	}
	if v == nil || *v < 0 {
		return false
	}
	if r.Min != nil && *v < *r.Min {
		return false
	}
	return r.Max == nil || *v <= *r.Max
}

// rgMatch is evalMatch plus numeric $gte/$lte conditions (a missing, null or
// non-numeric value fails any range, as on a server).
func rgMatch(t *testing.T, match bson.D, doc map[string]any) bool {
	t.Helper()
	for _, e := range match {
		if e.Key == "$or" || e.Key == "$and" {
			matched := e.Key == "$and"
			for _, branch := range e.Value.(bson.A) {
				ok := rgMatch(t, branch.(bson.D), doc)
				if e.Key == "$or" && ok {
					matched = true
				}
				if e.Key == "$and" && !ok {
					matched = false
				}
			}
			if !matched {
				return false
			}
			continue
		}
		if cond, ok := e.Value.(bson.D); ok {
			ranged := false
			for _, op := range cond {
				ranged = ranged || op.Key == "$gte" || op.Key == "$lte" || op.Key == "$lt"
			}
			if ranged {
				n, isNumber := pipelineNum(doc[e.Key])
				if !isNumber {
					return false
				}
				for _, op := range cond {
					bound, _ := pipelineNum(op.Value)
					switch op.Key {
					case "$gte":
						if n < bound {
							return false
						}
					case "$lte":
						if n > bound {
							return false
						}
					case "$lt":
						if n >= bound {
							return false
						}
					default:
						t.Fatalf("unsupported mixed operator %s", op.Key)
					}
				}
				continue
			}
		}
		if !evalMatch(t, bson.D{e}, doc) {
			return false
		}
	}
	return true
}

func rgSurvives(t *testing.T, pipeline mongo.Pipeline, doc map[string]any) bool {
	t.Helper()
	derived := evalSetStages(t, pipeline, doc)
	for i, stage := range pipeline {
		if stage[0].Key == "$group" {
			break
		}
		if i == 0 || stage[0].Key != "$match" {
			continue
		}
		if !rgMatch(t, stage[0].Value.(bson.D), derived) {
			return false
		}
	}
	return true
}

func rgModernIDs(t *testing.T, pipeline mongo.Pipeline, fixtures []filterFixture) []string {
	t.Helper()
	var ids []string
	for _, f := range fixtures {
		if evalFirstMatch(t, firstMatchOf(t, pipeline), f.doc) && rgSurvives(t, pipeline, f.doc) {
			ids = append(ids, f.id.Hex())
		}
	}
	sort.Strings(ids)
	return ids
}

// postMatches are the $match stages after the raw first one.
func rgPostMatches(pipeline mongo.Pipeline) []bson.D {
	var out []bson.D
	for i, stage := range pipeline {
		if i > 0 && stage[0].Key == "$match" {
			out = append(out, stage[0].Value.(bson.D))
		}
	}
	return out
}

func rgFind(match bson.D, key string) (any, bool) {
	for _, e := range match {
		if e.Key == key {
			return e.Value, true
		}
	}
	return nil, false
}

func rgPipelines(q audit.Query) map[string]mongo.Pipeline {
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	return map[string]mongo.Pipeline{
		"summary":  buildSummaryPipeline(q, q.Start, q.End, true),
		"baseline": buildSummaryPipeline(q, q.BaselineStart, q.BaselineEnd, false),
		"requests": requestPipelineState(q, cohort, "", "source", nil, 100),
		"slowest":  slowestPipeline(q, "source", nil, nil, 100),
	}
}

func rgExtJSON(t *testing.T, pipeline mongo.Pipeline) string {
	t.Helper()
	raw, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLatencyRangeImpliesComplete(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for name, apply := range map[string]func(*audit.Query) string{
		"observed":    func(q *audit.Query) string { q.ObservedMs = rgRange(rgF(10), rgF(900)); return "_auditRangeObserved" },
		"api":         func(q *audit.Query) string { q.ApiTimeMs = rgRange(rgF(10), nil); return "_auditRangeApi" },
		"audit create": func(q *audit.Query) string { q.AuditCreateTimeMs = rgRange(nil, rgF(900)); return "_auditRangeAuditCreate" },
	} {
		t.Run(name, func(t *testing.T) {
			q := queryForTest(now)
			q.Kind, q.Outcome = "all", "all"
			field := apply(&q)
			for kind, pipeline := range rgPipelines(q) {
				found := false
				for _, match := range rgPostMatches(pipeline) {
					state, hasState := rgFind(match, "_auditState")
					bounds, hasBounds := rgFind(match, field)
					if hasState && hasBounds {
						found = true
						if state != "valid" {
							t.Errorf("%s: a latency range must imply state valid, got %v", kind, state)
						}
						cond, _ := bounds.(bson.D)
						ops := map[string]bool{}
						for _, op := range cond {
							ops[op.Key] = true
						}
						if !ops["$gte"] {
							t.Errorf("%s: bounds %v have no lower bound (the minimum defaults to zero)", kind, bounds)
						}
					}
				}
				if !found {
					t.Errorf("%s: no $match holds state valid together with %s: %v", kind, field, rgPostMatches(pipeline))
				}
			}
		})
	}
	t.Run("payload ranges do not imply completion", func(t *testing.T) {
		q := queryForTest(now)
		q.Kind, q.Outcome = "all", "all"
		q.RequestKB, q.ResponseKB = rgRange(rgF(1), nil), rgRange(nil, rgF(5))
		for kind, pipeline := range rgPipelines(q) {
			for _, match := range rgPostMatches(pipeline) {
				if _, has := rgFind(match, "_auditState"); has && kind != "slowest" {
					t.Errorf("%s: payload ranges added a state condition %v", kind, match)
				}
			}
		}
	})
	t.Run("the evaluator drops incomplete and invalid rows", func(t *testing.T) {
		q := queryForTest(now)
		q.Kind, q.Outcome = "all", "all"
		q.ObservedMs = rgRange(rgF(0), nil)
		rows := rgFixtures(now)
		ids := rgModernIDs(t, buildSummaryPipeline(q, q.Start, q.End, true), rgFilterFixtures(rows))
		for _, row := range rows {
			listed := false
			for _, id := range ids {
				listed = listed || id == row.id.Hex()
			}
			if listed && row.state("total") != "valid" {
				t.Errorf("%s (%s) survived a latency range", row.label, row.state("total"))
			}
			if !listed && row.state("total") == "valid" && row.obs != nil {
				t.Errorf("%s is valid with an observed duration but was dropped", row.label)
			}
		}
	})
}

func TestIncompleteAgeUsesLiteralNow(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := queryForTest(now)
	base.Kind, base.Outcome = "all", "all"
	base.IncompleteAgeMs = rgRange(rgF(60_000), rgF(600_000))
	for _, clock := range []time.Time{now, now.Add(7 * time.Minute)} {
		q := base
		q.Now = clock
		literal := clock.Format("2006-01-02T15:04:05")
		for kind, pipeline := range rgPipelines(q) {
			text := rgExtJSON(t, pipeline)
			if strings.Contains(text, "$$NOW") || strings.Contains(text, "$$CLUSTER_TIME") || strings.Contains(text, "$dateDiff") {
				t.Errorf("%s: the pipeline reads the server clock: %s", kind, text)
			}
			if !strings.Contains(text, literal) {
				t.Errorf("%s: the injected clock %s is not rendered as a literal", kind, literal)
			}
			var gotState any
			for _, match := range rgPostMatches(pipeline) {
				if state, ok := rgFind(match, "_auditState"); ok {
					gotState = state
				}
			}
			if gotState != "incomplete" && kind != "slowest" {
				t.Errorf("%s: an age range must imply incomplete rows, got state %v", kind, gotState)
			}
		}
	}
	t.Run("pending completion narrows the implied state", func(t *testing.T) {
		q := base
		q.Now = now
		q.Completion = "pending"
		match := rgPostMatches(buildSummaryPipeline(q, q.Start, q.End, true))
		if len(match) == 0 {
			t.Fatal("no post match")
		}
		if v, ok := rgFind(match[0], "_auditPending"); !ok || v != true {
			t.Fatalf("pending age filter lost its pending condition: %v", match[0])
		}
	})
	t.Run("evaluated against rows with the CreatedOn fallback", func(t *testing.T) {
		q := base
		q.Now = now
		rows := rgFixtures(now)
		ids := rgModernIDs(t, buildSummaryPipeline(q, q.Start, q.End, true), rgFilterFixtures(rows))
		want := map[string]bool{}
		for _, row := range rows {
			if row.state("total") == "incomplete" && row.age != nil && *row.age >= 60_000 && *row.age <= 600_000 {
				want[row.label] = true
			}
		}
		if !want["created only"] || !want["middle"] || want["young"] || want["old"] {
			t.Fatalf("fixture expectations: %v", want)
		}
		got := map[string]bool{}
		for _, row := range rows {
			for _, id := range ids {
				if id == row.id.Hex() {
					got[row.label] = true
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("modern evaluator kept %v, want %v", got, want)
		}
	})
	t.Run("the clock is the injected one, not the wall clock", func(t *testing.T) {
		clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
		s, capture := rgStore(t, &clock)
		q := base
		q.SourceIDs = []string{s.source}
		if _, err := s.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 2}); err != nil {
			t.Fatal(err)
		}
		if len(capture.pipelines) != 1 {
			t.Fatalf("%d pipelines", len(capture.pipelines))
		}
		text := rgExtJSON(t, capture.pipelines[0])
		if !strings.Contains(text, "2026-10-22T12:00:00") || strings.Contains(text, "$$NOW") {
			t.Fatalf("page clock literal missing: %s", text)
		}
	})
}

// rgRow is one audit document with the values a range filter should see,
// written out independently of the code under test.
type rgRow struct {
	label                string
	id                   bson.ObjectID
	doc                  map[string]any
	completed, initiated bool
	pending              bool
	obs                  *float64
	api, ac, req, resp   *float64
	age                  *float64
}

// state is the row's derived state under a metric, stated from the fields.
func (r rgRow) state(metric string) string {
	if r.pending || !r.initiated || !r.completed {
		return "incomplete"
	}
	if metric == "api" {
		if r.api == nil {
			return "incomplete"
		}
		if *r.api < 0 {
			return "invalid"
		}
		return "valid"
	}
	if r.obs == nil || *r.obs < 0 {
		return "invalid"
	}
	return "valid"
}

func rgFixtures(now time.Time) []rgRow {
	at := now.Add(-10 * time.Minute)
	ms := func(d time.Duration) *float64 { return rgF(float64(d.Milliseconds())) }
	base := func(extra map[string]any) map[string]any {
		doc := map[string]any{
			"ControllerName": "Payroll", "ActionName": "Run", "RequestPollingType": "Synchronous ", "StatusCode": int32(200),
			"_auditMetadataOversized": false, "RequestMainUrl": "/api/Payroll/Run", "UserAgent": "Mozilla/5.0", "UserIp": "10.0.0.1",
		}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}
	var rows []rgRow
	add := func(row rgRow) {
		row.id = bson.NewObjectID()
		row.doc["_id"] = row.id
		rows = append(rows, row)
	}
	complete := func(label string, obs time.Duration, extra map[string]any, api, ac, req, resp *float64) {
		extra["RequestInitiatedAt"], extra["RequestCompletedAt"] = at, at.Add(obs)
		add(rgRow{label: label, doc: base(extra), initiated: true, completed: true, obs: ms(obs), api: api, ac: ac, req: req, resp: resp})
	}
	complete("fast", 100*time.Millisecond, map[string]any{"ApiTime": 80.0, "AuditCreateTime": 15.0, "RequestLengthInKiloBytes": 2.0, "ResponseLengthInKiloBytes": 10.0}, rgF(80), rgF(15), rgF(2), rgF(10))
	complete("medium", 500*time.Millisecond, map[string]any{"ApiTime": 450.0, "AuditCreateTime": 40.0, "RequestLengthInKiloBytes": 20.0, "ResponseLengthInKiloBytes": 200.0}, rgF(450), rgF(40), rgF(20), rgF(200))
	complete("slow explicit zero kb", 2*time.Second, map[string]any{"ApiTime": 1900.0, "AuditCreateTime": 90.0, "RequestLengthInKiloBytes": 0.0, "ResponseLengthInKiloBytes": int32(0)}, rgF(1900), rgF(90), rgF(0), rgF(0))
	complete("missing fields", 300*time.Millisecond, map[string]any{}, nil, nil, nil, nil)
	complete("text numbers", 300*time.Millisecond, map[string]any{"ApiTime": "250", "AuditCreateTime": "12", "RequestLengthInKiloBytes": "5", "ResponseLengthInKiloBytes": int32(7)}, rgF(250), rgF(12), rgF(5), rgF(7))
	complete("null fields", 100*time.Millisecond, map[string]any{"ApiTime": nil, "AuditCreateTime": nil, "RequestLengthInKiloBytes": nil, "ResponseLengthInKiloBytes": nil}, nil, nil, nil, nil)
	add(rgRow{label: "created fallback", doc: base(map[string]any{"CreatedOn": at, "RequestCompletedAt": at.Add(700 * time.Millisecond), "ApiTime": 600.0, "AuditCreateTime": 30.0, "RequestLengthInKiloBytes": 1.0, "ResponseLengthInKiloBytes": 1.0}), initiated: true, completed: true, obs: rgF(700), api: rgF(600), ac: rgF(30), req: rgF(1), resp: rgF(1)})
	add(rgRow{label: "negative", doc: base(map[string]any{"RequestInitiatedAt": at, "RequestCompletedAt": at.Add(-100 * time.Millisecond), "ApiTime": 50.0, "AuditCreateTime": 5.0, "RequestLengthInKiloBytes": 3.0, "ResponseLengthInKiloBytes": 3.0}), initiated: true, completed: true, obs: rgF(-100), api: rgF(50), ac: rgF(5), req: rgF(3), resp: rgF(3)})
	open := func(label string, ago time.Duration, extra map[string]any, pending bool) {
		age := ms(ago)
		extra["RequestLengthInKiloBytes"], extra["ResponseLengthInKiloBytes"] = 4.0, 4.0
		add(rgRow{label: label, doc: base(extra), initiated: extra["RequestInitiatedAt"] != nil, pending: pending, req: rgF(4), resp: rgF(4), age: age})
	}
	open("young", 30*time.Second, map[string]any{"RequestInitiatedAt": now.Add(-30 * time.Second)}, false)
	open("middle", 5*time.Minute, map[string]any{"RequestInitiatedAt": now.Add(-5 * time.Minute)}, false)
	open("old", 12*time.Minute, map[string]any{"RequestInitiatedAt": now.Add(-12 * time.Minute)}, false)
	open("created only", 6*time.Minute, map[string]any{"CreatedOn": now.Add(-6 * time.Minute)}, false)
	open("pending", 4*time.Minute, map[string]any{"RequestInitiatedAt": now.Add(-4 * time.Minute), "RequestPollingType": "Asynchronous ", "StatusCode": "Accepted"}, true)
	return rows
}

func rgFilterFixtures(rows []rgRow) []filterFixture {
	out := make([]filterFixture, len(rows))
	for i, r := range rows {
		out[i] = filterFixture{id: r.id, doc: r.doc, timing: r.label}
	}
	return out
}

// rgExpect is the independent definition of which rows a query keeps.
func rgExpect(row rgRow, q audit.Query) bool {
	state := row.state(q.Metric)
	switch q.Completion {
	case "complete":
		if state != "valid" {
			return false
		}
	case "incomplete":
		if state != "incomplete" {
			return false
		}
	case "pending":
		if !row.pending {
			return false
		}
	case "invalid":
		if state != "invalid" {
			return false
		}
	}
	latency := rgBounded(q.ObservedMs) || rgBounded(q.ApiTimeMs) || rgBounded(q.AuditCreateTimeMs)
	if latency && state != "valid" {
		return false
	}
	if rgBounded(q.IncompleteAgeMs) && state != "incomplete" {
		return false
	}
	return rgIn(row.obs, q.ObservedMs) && rgIn(row.api, q.ApiTimeMs) && rgIn(row.ac, q.AuditCreateTimeMs) &&
		rgIn(row.req, q.RequestKB) && rgIn(row.resp, q.ResponseKB) && rgIn(row.age, q.IncompleteAgeMs)
}

type rgCase struct {
	name  string
	apply func(*audit.Query)
	empty bool
}

func rgCases() []rgCase {
	return []rgCase{
		{name: "observed min", apply: func(q *audit.Query) { q.ObservedMs = rgRange(rgF(300), nil) }},
		{name: "observed band", apply: func(q *audit.Query) { q.ObservedMs = rgRange(rgF(100), rgF(500)) }},
		{name: "observed max only", apply: func(q *audit.Query) { q.ObservedMs = rgRange(nil, rgF(100)) }},
		{name: "api min", apply: func(q *audit.Query) { q.ApiTimeMs = rgRange(rgF(100), nil) }},
		{name: "api max keeps missing out", apply: func(q *audit.Query) { q.ApiTimeMs = rgRange(nil, rgF(100)) }},
		{name: "audit create band", apply: func(q *audit.Query) { q.AuditCreateTimeMs = rgRange(rgF(10), rgF(40)) }},
		{name: "request kb max keeps missing out", apply: func(q *audit.Query) { q.RequestKB = rgRange(nil, rgF(5)) }},
		{name: "response kb min", apply: func(q *audit.Query) { q.ResponseKB = rgRange(rgF(7), nil) }},
		{name: "both kb", apply: func(q *audit.Query) { q.RequestKB, q.ResponseKB = rgRange(rgF(1), rgF(20)), rgRange(rgF(4), rgF(10)) }},
		{name: "observed api and audit create", apply: func(q *audit.Query) {
			q.ObservedMs, q.ApiTimeMs, q.AuditCreateTimeMs = rgRange(rgF(100), rgF(700)), rgRange(rgF(50), nil), rgRange(nil, rgF(40))
		}},
		{name: "latency with kb", apply: func(q *audit.Query) { q.ObservedMs, q.RequestKB = rgRange(rgF(0), nil), rgRange(nil, rgF(2)) }},
		{name: "observed with explicit complete", apply: func(q *audit.Query) { q.ObservedMs, q.Completion = rgRange(rgF(300), nil), "complete" }},
		{name: "kb with invalid completion", apply: func(q *audit.Query) { q.RequestKB, q.Completion = rgRange(rgF(0), nil), "invalid" }},
		{name: "age min", apply: func(q *audit.Query) { q.IncompleteAgeMs = rgRange(rgF(60_000), nil) }},
		{name: "age band", apply: func(q *audit.Query) { q.IncompleteAgeMs = rgRange(rgF(5 * 60_000), rgF(10 * 60_000)) }},
		{name: "age max is inclusive", apply: func(q *audit.Query) { q.IncompleteAgeMs = rgRange(nil, rgF(5*60_000)) }},
		{name: "age with pending", apply: func(q *audit.Query) { q.IncompleteAgeMs, q.Completion = rgRange(rgF(1), nil), "pending" }},
		{name: "age with incomplete", apply: func(q *audit.Query) { q.IncompleteAgeMs, q.Completion = rgRange(rgF(1), nil), "incomplete" }},
		{name: "age with kb", apply: func(q *audit.Query) { q.IncompleteAgeMs, q.RequestKB = rgRange(rgF(1), nil), rgRange(rgF(4), nil) }},
		{name: "unmatched range", apply: func(q *audit.Query) { q.ObservedMs = rgRange(rgF(1e8), nil) }, empty: true},
		{name: "api metric observed min", apply: func(q *audit.Query) { q.Metric, q.ObservedMs = "api", rgRange(rgF(150), nil) }},
		{name: "api metric api min", apply: func(q *audit.Query) { q.Metric, q.ApiTimeMs = "api", rgRange(rgF(200), nil) }},
		{name: "api metric age", apply: func(q *audit.Query) { q.Metric, q.IncompleteAgeMs = "api", rgRange(rgF(1), nil) }},
	}
}

func TestLegacyRangeFiltersMatchModern(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	rows := rgFixtures(now)
	fixtures := rgFilterFixtures(rows)
	p, handle, release := legacyProfile(t)
	defer release()
	for _, tc := range rgCases() {
		t.Run(tc.name, func(t *testing.T) {
			q := queryForTest(now)
			q.Kind, q.Outcome = "all", "all"
			tc.apply(&q)
			q.Now = now
			if err := audit.ValidateQuery(q); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, row := range rows {
				if rgExpect(row, q) {
					want = append(want, row.id.Hex())
				}
			}
			sort.Strings(want)
			if tc.empty != (len(want) == 0) || (!tc.empty && len(want) == len(rows)) {
				t.Fatalf("the case selects %d of %d rows, so it proves nothing", len(want), len(rows))
			}
			cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
			modernRows := rgModernIDs(t, requestPipelineState(q, cohort, "", "source", nil, 100000), fixtures)
			if !sameIDs(modernRows, want) {
				t.Fatalf("modern request rows %v, want %v", rgLabels(rows, modernRows), rgLabels(rows, want))
			}
			legacyRows := legacyFilteredRows(t, p, handle, q, fixtures)
			if !sameIDs(legacyRows, want) {
				t.Fatalf("legacy request rows %v, want %v", rgLabels(rows, legacyRows), rgLabels(rows, want))
			}
			modernSummary := rgModernIDs(t, buildSummaryPipeline(q, q.Start, q.End, true), fixtures)
			if !sameIDs(modernSummary, want) {
				t.Fatalf("modern summary %v, want %v", rgLabels(rows, modernSummary), rgLabels(rows, want))
			}
			if got := legacySummaryCount(t, p, handle, q, fixtures); got != int64(len(want)) {
				t.Fatalf("legacy summary counted %d, want %d", got, len(want))
			}
			// The slowest list ranks only rows with a metric, so a latency range (valid
			// rows only) lists the same rows on the legacy reader, floor included.
			if rgBounded(q.ObservedMs) || rgBounded(q.ApiTimeMs) || rgBounded(q.AuditCreateTimeMs) {
				slow := &slowFilter{min: audit.EffectiveSlowestMin(q, nil)}
				if got := legacySlowIDs(t, p, handle, q, fixtures, slow); !sameIDs(got, want) {
					t.Fatalf("legacy slowest %v, want %v", rgLabels(rows, got), rgLabels(rows, want))
				}
			}
		})
	}
}

func legacySlowIDs(t *testing.T, p *profile, handle *fakeClient, q audit.Query, fixtures []filterFixture, slow *slowFilter) []string {
	t.Helper()
	raws := make([]any, 0, len(fixtures))
	for _, f := range fixtures {
		raws = append(raws, drillRaw(t, f.doc))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: raws}, nil
	}
	var records, rawBytes atomic.Int64
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	rows, err := readLegacyRequestRowsState(context.Background(), p, q, cohort, "", nil, nil, 100000, &records, &rawBytes, false, slow)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.request.ID)
	}
	sort.Strings(ids)
	return ids
}

func rgLabels(rows []rgRow, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		for _, r := range rows {
			if r.id.Hex() == id {
				out = append(out, r.label)
			}
		}
	}
	return out
}

// TestRangeFiltersAgreeOnModernLegacyAndDemo runs the demo repository's own
// range filtering against both server evaluators over the same records.
func TestRangeFiltersAgreeOnModernLegacyAndDemo(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	demo := audit.NewDemo(now)
	q := queryForTest(now)
	q.SourceIDs = []string{"demo-west", "demo-east"}
	q.Kind, q.Outcome, q.BucketSeconds = "all", "all", 60
	demoPage := func(q audit.Query) []audit.RequestRecord {
		var all []audit.RequestRecord
		cursor := ""
		for pages := 0; pages < 200; pages++ {
			p, err := demo.Requests(context.Background(), audit.PageInput{Query: q, Limit: audit.MaxPageSize, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, p.Rows...)
			if p.NextCursor == "" {
				return all
			}
			cursor = p.NextCursor
		}
		t.Fatal("demo paging did not finish")
		return nil
	}
	records := demoPage(q)
	if len(records) < 700 {
		t.Fatalf("demo window holds %d rows", len(records))
	}
	var fixtures []filterFixture
	names := map[string]string{}
	for _, r := range records {
		id := bson.NewObjectID()
		names[id.Hex()] = r.SourceID + "/" + r.ID
		doc := map[string]any{
			"_id": id, "ControllerName": r.Controller, "ActionName": r.Action, "RequestPollingType": r.PollingTypeRaw, "StatusCode": r.Status,
			"RequestInitiatedAt": *r.InitiatedAt, "RequestCompletedAt": *r.CompletedAt, "ApiTime": *r.ApiTimeMs, "AuditCreateTime": *r.AuditCreateTimeMs,
			"PayloadRecordTime": *r.PayloadRecordTimeMs, "RequestLengthInKiloBytes": r.RequestKB, "ResponseLengthInKiloBytes": r.ResponseKB,
			"UserId": int32(*r.UserID), "UserIp": r.UserIP, "UserAgent": r.UserAgent, "PayrollUIVersion": r.UIVersion, "RequestMainUrl": r.URL,
			"_auditMetadataOversized": false,
		}
		fixtures = append(fixtures, filterFixture{id: id, doc: doc})
	}
	byName := map[string]string{}
	for id, name := range names {
		byName[name] = id
	}
	p, handle, release := legacyProfile(t)
	defer release()
	cases := []struct {
		name  string
		apply func(*audit.Query)
	}{
		{"api min", func(q *audit.Query) { q.ApiTimeMs = rgRange(rgF(45), nil) }},
		{"audit create max", func(q *audit.Query) { q.AuditCreateTimeMs = rgRange(nil, rgF(8)) }},
		{"observed band", func(q *audit.Query) { q.ObservedMs = rgRange(rgF(30), rgF(400)) }},
		{"request kb", func(q *audit.Query) { q.RequestKB = rgRange(rgF(2), rgF(4)) }},
		{"response kb", func(q *audit.Query) { q.ResponseKB = rgRange(nil, rgF(5)) }},
		{"combined", func(q *audit.Query) { q.ObservedMs, q.ResponseKB, q.UserIDs = rgRange(rgF(60), nil), rgRange(rgF(3), nil), []int{1001, 1002} }},
		{"age on complete rows", func(q *audit.Query) { q.IncompleteAgeMs = rgRange(rgF(1), nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rq := q
			tc.apply(&rq)
			rq.Now = now
			var demoIDs []string
			for _, r := range demoPage(rq) {
				demoIDs = append(demoIDs, byName[r.SourceID+"/"+r.ID])
			}
			sort.Strings(demoIDs)
			cohort := [5]string{"", rq.Controller, rq.Action, rq.Kind, rq.Outcome}
			modern := rgModernIDs(t, requestPipelineState(rq, cohort, "", "source", nil, 100000), fixtures)
			legacy := legacyFilteredRows(t, p, handle, rq, fixtures)
			if !sameIDs(modern, demoIDs) || !sameIDs(legacy, demoIDs) {
				t.Fatalf("modern %d, legacy %d, demo %d rows", len(modern), len(legacy), len(demoIDs))
			}
			if rq.IncompleteAgeMs == nil && (len(demoIDs) == 0 || len(demoIDs) == len(records)) {
				t.Fatalf("the range selects %d of %d demo rows, so it proves nothing", len(demoIDs), len(records))
			}
			if rq.IncompleteAgeMs != nil && len(demoIDs) != 0 {
				t.Fatalf("every demo row is complete, yet an age range kept %d", len(demoIDs))
			}
		})
	}
}

func TestRangeFiltersKeepTheMetadataStreamLegacySafe(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q := queryForTest(now)
	q.Kind, q.Outcome = "all", "all"
	q.Now = now
	q.ObservedMs, q.ApiTimeMs, q.RequestKB = rgRange(rgF(1), nil), rgRange(nil, rgF(9)), rgRange(rgF(0), rgF(5))
	plain := queryForTest(now)
	plain.Kind, plain.Outcome = "all", "all"
	for name, pair := range map[string][2]mongo.Pipeline{
		"summary":  {buildSummaryPipeline(q, q.Start, q.End, true), buildSummaryPipeline(plain, q.Start, q.End, true)},
		"requests": {requestPipelineState(q, [5]string{"", "", "", "all", "all"}, "", "source", nil, 10), requestPipelineState(plain, [5]string{"", "", "", "all", "all"}, "", "source", nil, 10)},
	} {
		with := legacyRowMetadataPipeline(pair[0])
		without := legacyRowMetadataPipeline(pair[1])
		if rgExtJSON(t, with) != rgExtJSON(t, without) {
			t.Errorf("%s: ranges changed the Mongo 3.4 prefilter and projection", name)
		}
		for _, stage := range with {
			if k := stage[0].Key; k != "$match" && k != "$project" && k != "$sort" && k != "$limit" {
				t.Errorf("%s: legacy stream has a %s stage", name, stage[0].Key)
			}
		}
		text := rgExtJSON(t, with)
		for _, banned := range []string{"$regexMatch", "$dateDiff", "$toString", "_auditRange", "$$NOW"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s: legacy stream uses %s", name, banned)
			}
		}
	}
}

type rgCapture struct{ pipelines []mongo.Pipeline }

type rgHarness struct {
	store  *Store
	source string
}

// rgStore builds a store on a fake client that records every pipeline and, for a
// request page, returns three rows so a page of two has a next cursor. now is a
// pointer so a test can move the clock between pages.
func rgStore(t *testing.T, now *time.Time) (rgHarness, *rgCapture) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	s.now = func() time.Time { return *now }
	source := addFakeSource(t, s, "audit")
	capture := &rgCapture{}
	at := now.Add(-10 * time.Minute)
	handle.aggregateFn = func(_ context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		capture.pipelines = append(capture.pipelines, pipeline)
		docs := make([]any, 0, 3)
		for i := 0; i < 3; i++ {
			initiated := at.Add(-time.Duration(i) * time.Second)
			duration, api := float64(500-i), float64(400-i)
			docs = append(docs, requestDocument{ID: bson.NewObjectID(), Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", InitiatedAt: &initiated, DurationMs: &duration, ApiTimeMs: &api})
		}
		return &fakeCursor{docs: docs}, nil
	}
	return rgHarness{store: s, source: source.ID}, capture
}

func rgCursorJSON(t *testing.T, cursor string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rgEncode(t *testing.T, v map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestAgeCursorKeepsThePageOneClock pins the clock a cursor carries: a later page
// uses page one's now (so the row set does not shift while paging), the wall
// clock never enters the fingerprint, and an age cursor without a clock is refused.
func TestAgeCursorKeepsThePageOneClock(t *testing.T) {
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h, capture := rgStore(t, &clock)
	q := queryForTest(clock)
	q.SourceIDs = []string{h.source}
	q.Kind, q.Outcome = "all", "all"
	q.IncompleteAgeMs = rgRange(rgF(1_000), nil)
	first, err := h.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("page one has no cursor")
	}
	if _, ok := rgCursorJSON(t, first.NextCursor)["now"]; !ok {
		t.Fatal("the age cursor carries no clock")
	}
	clock = clock.Add(90 * time.Second)
	if _, err := h.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 2, Cursor: first.NextCursor}); err != nil {
		t.Fatalf("a later page was refused after the clock moved: %v", err)
	}
	if len(capture.pipelines) != 2 {
		t.Fatalf("%d pipelines", len(capture.pipelines))
	}
	one, two := rgExtJSON(t, capture.pipelines[0]), rgExtJSON(t, capture.pipelines[1])
	if !strings.Contains(one, "2026-10-22T12:00:00") || !strings.Contains(two, "2026-10-22T12:00:00") || strings.Contains(two, "2026-10-22T12:01:30") {
		t.Fatalf("page two did not reuse page one's clock:\n%s", two)
	}
	// A cursor without the clock cannot page an age query.
	stripped := rgCursorJSON(t, first.NextCursor)
	delete(stripped, "now")
	if _, err := h.store.Requests(context.Background(), audit.PageInput{Query: q, Limit: 2, Cursor: rgEncode(t, stripped)}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("a clockless age cursor was accepted: %v", err)
	}
	// Queries without an age range carry no clock.
	plain := queryForTest(clock)
	plain.SourceIDs = []string{h.source}
	plain.Kind, plain.Outcome = "all", "all"
	plain.ObservedMs = rgRange(rgF(1), nil)
	page, err := h.store.Requests(context.Background(), audit.PageInput{Query: plain, Limit: 2})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("%v %q", err, page.NextCursor)
	}
	if _, ok := rgCursorJSON(t, page.NextCursor)["now"]; ok {
		t.Fatal("a latency cursor must not carry a clock")
	}
}

func TestRangeIsPartOfTheCursorFingerprint(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q := queryForTest(now)
	base := pageFingerprint(q, "")
	seen := map[string]string{"none": base}
	for name, apply := range map[string]func(*audit.Query){
		"observed":    func(q *audit.Query) { q.ObservedMs = rgRange(rgF(1), nil) },
		"observed max": func(q *audit.Query) { q.ObservedMs = rgRange(nil, rgF(1)) },
		"api":         func(q *audit.Query) { q.ApiTimeMs = rgRange(rgF(1), nil) },
		"audit":       func(q *audit.Query) { q.AuditCreateTimeMs = rgRange(rgF(1), nil) },
		"request kb":  func(q *audit.Query) { q.RequestKB = rgRange(rgF(1), nil) },
		"response kb": func(q *audit.Query) { q.ResponseKB = rgRange(rgF(1), nil) },
		"age":         func(q *audit.Query) { q.IncompleteAgeMs = rgRange(rgF(1), nil) },
	} {
		other := q
		apply(&other)
		fp := pageFingerprint(other, "")
		for prior, value := range seen {
			if value == fp {
				t.Errorf("%s shares a fingerprint with %s", name, prior)
			}
		}
		seen[name] = fp
		moved := other
		moved.Now = now.Add(time.Hour)
		if pageFingerprint(moved, "") != fp {
			t.Errorf("%s: the fingerprint depends on the wall clock", name)
		}
	}
}

// TestSlowestFloorFollowsTheMetricRange maps the range minimum for the ranking
// metric onto the slowest floor and keeps the cursor bound to the ranges.
func TestSlowestFloorFollowsTheMetricRange(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	clock := now
	h, capture := rgStore(t, &clock)
	floorOf := func(pipeline mongo.Pipeline, field string) any {
		for _, match := range rgPostMatches(pipeline) {
			if v, ok := rgFind(match, field); ok {
				if cond, isCond := v.(bson.D); isCond {
					for _, op := range cond {
						if op.Key == "$gte" {
							return op.Value
						}
					}
				}
			}
		}
		return nil
	}
	q := queryForTest(now)
	q.SourceIDs = []string{h.source}
	q.Kind, q.Outcome = "all", "all"
	q.ObservedMs = rgRange(rgF(250), nil)
	if _, err := h.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if got := floorOf(capture.pipelines[0], "durationMs"); got != float64(250) {
		t.Fatalf("the ranking floor is %v, want the observed minimum 250", got)
	}
	// A higher explicit minimum wins.
	capture.pipelines = nil
	if _, err := h.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 2, MinMs: rgF(400)}); err != nil {
		t.Fatal(err)
	}
	if got := floorOf(capture.pipelines[0], "durationMs"); got != float64(400) {
		t.Fatalf("the ranking floor is %v, want the explicit minimum 400", got)
	}
	// The api metric ranks by apiTimeMs and reads the api minimum.
	capture.pipelines = nil
	api := q
	api.Metric, api.ObservedMs, api.ApiTimeMs = "api", nil, rgRange(rgF(120), nil)
	if _, err := h.store.Slowest(context.Background(), audit.PageInput{Query: api, Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if got := floorOf(capture.pipelines[0], "apiTimeMs"); got != float64(120) {
		t.Fatalf("the api floor is %v, want 120", got)
	}
	// A cursor from one range does not page another.
	capture.pipelines = nil
	page, err := h.store.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Skip("the fake page has no slowest cursor")
	}
	other := q
	other.ObservedMs = rgRange(rgF(251), nil)
	if _, err := h.store.Slowest(context.Background(), audit.PageInput{Query: other, Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("a slowest cursor crossed ranges: %v", err)
	}
}
