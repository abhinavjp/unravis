package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// bdCase is one audit row plus what a correct breakdown must say about it. The
// expectations are written down at construction, not derived from the code under
// test, so they are an independent oracle.
type bdCase struct {
	doc     map[string]any
	state   string
	pending bool
	class   string
	ms      float64
}

var bdAbsent = pipelineMissing{}

func bdNow() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }

func bdQuery(now time.Time, sourceIDs ...string) audit.Query {
	q := queryForTest(now)
	q.SourceIDs, q.Kind, q.Outcome = sourceIDs, "all", "all"
	return q
}

// bdCases builds 17 rows. User agents are A x5, B x4, C x3, D x2, then E, a
// value that looks like a field path, and a missing one, once each. The other
// columns are varied so every dimension has several values, blanks and padding.
func bdCases(now time.Time) []bdCase {
	var cases []bdCase
	for i := 0; i < 17; i++ {
		at := now.Add(-5*time.Minute - time.Duration(i)*10*time.Second)
		ms := float64(100 + 37*i)
		doc := map[string]any{
			"ControllerName": "Payroll", "ActionName": "Run", "RequestInitiatedAt": at, "CreatedOn": at.Add(5 * time.Millisecond),
			"RequestCompletedAt": at.Add(time.Duration(ms) * time.Millisecond), "ApiTime": float64(50 + 3*i),
			"StatusCode": "OK", "RequestPollingType": "Synchronous ", "UserIp": fmt.Sprintf("10.0.0.%d", i%5),
			"PayrollUIVersion": []string{"1.0", "2.0"}[i%2], "UserId": int32(100 + i%3),
		}
		switch {
		case i < 5:
			doc["UserAgent"] = "UA-A"
		case i < 9:
			doc["UserAgent"] = "UA-B"
		case i < 12:
			doc["UserAgent"] = "UA-C"
		case i < 14:
			doc["UserAgent"] = "UA-D"
		case i == 14:
			doc["UserAgent"] = "UA-E"
		case i == 15:
			doc["UserAgent"] = "$pathLike"
		default:
			delete(doc, "UserAgent")
		}
		c := bdCase{doc: doc, state: "valid", ms: ms}
		if i%4 == 0 {
			delete(doc, "UserId")
		}
		if i%5 == 2 {
			doc["StatusCode"], c.class = "NotFound", audit.StatusClientError
		}
		if i%6 == 3 {
			doc["StatusCode"], c.class = "InternalServerError", audit.StatusServerError
		}
		if i%5 == 1 {
			doc["RequestPollingType"] = "Synchronous"
		}
		switch i {
		case 1:
			doc["RequestPollingType"], doc["StatusCode"] = "Asynchronous ", "Accepted"
			delete(doc, "RequestCompletedAt")
			c.state, c.pending, c.ms = "incomplete", true, 0
		case 4:
			delete(doc, "RequestCompletedAt")
			c.state, c.ms = "incomplete", 0
		case 6:
			doc["RequestCompletedAt"] = at.Add(-time.Second)
			c.state, c.ms = "invalid", 0
		case 10:
			delete(doc, "RequestPollingType")
		case 13:
			doc["RequestPollingType"] = "   "
		case 14:
			doc["StatusCode"] = " OK "
		case 16:
			delete(doc, "StatusCode")
		}
		cases = append(cases, c)
	}
	return cases
}

func bdDocs(cases []bdCase) []map[string]any {
	docs := make([]map[string]any, len(cases))
	for i, c := range cases {
		docs[i] = c.doc
	}
	return docs
}

func bdText(doc map[string]any, field string) string {
	if v, ok := doc[field].(string); ok {
		return v
	}
	return ""
}

// bdKey is the oracle's value of dim for one document, as the breakdown reports it.
func bdKey(dim string, doc map[string]any) string {
	switch dim {
	case audit.BreakdownStatusCode:
		return strings.TrimSpace(bdText(doc, "StatusCode"))
	case audit.BreakdownPollingType:
		return strings.TrimSpace(bdText(doc, "RequestPollingType"))
	case audit.BreakdownUserID:
		if v, ok := doc["UserId"].(int32); ok {
			return strconv.Itoa(int(v))
		}
		return ""
	case audit.BreakdownUIVersion:
		return bdText(doc, "PayrollUIVersion")
	case audit.BreakdownUserAgent:
		return bdText(doc, "UserAgent")
	case audit.BreakdownUserIP:
		return bdText(doc, "UserIp")
	}
	panic("unknown dim " + dim)
}

var bdDims = []string{audit.BreakdownStatusCode, audit.BreakdownUserID, audit.BreakdownUIVersion, audit.BreakdownUserAgent, audit.BreakdownUserIP, audit.BreakdownPollingType}

// bdSimulate runs a pipeline over documents with the evaluators in this package.
// It supports the stages the breakdown emits: $match, $set, $group and $limit. A
// $set sees the document as the stage received it, as on a server.
func bdSimulate(t *testing.T, pipeline mongo.Pipeline, docs []map[string]any) []bson.Raw {
	t.Helper()
	rows := make([]map[string]any, len(docs))
	copy(rows, docs)
	for i, stage := range pipeline {
		switch stage[0].Key {
		case "$match":
			if i == 0 {
				continue
			}
			kept := rows[:0:0]
			for _, row := range rows {
				if evalMatch(t, stage[0].Value.(bson.D), row) {
					kept = append(kept, row)
				}
			}
			rows = kept
		case "$set":
			for r, row := range rows {
				next := make(map[string]any, len(row)+4)
				for k, v := range row {
					next[k] = v
				}
				for _, e := range stage[0].Value.(bson.D) {
					next[e.Key] = evalExpr(t, e.Value, row)
				}
				rows[r] = next
			}
		case "$limit":
			if n := int(reflect.ValueOf(stage[0].Value).Int()); len(rows) > n {
				rows = rows[:n]
			}
		case "$group":
			return bdGroup(t, stage[0].Value.(bson.D), rows)
		default:
			t.Fatalf("simulator does not support %s", stage[0].Key)
		}
	}
	t.Fatal("pipeline has no $group")
	return nil
}

type bdAccumulated struct {
	id    bson.D
	sums  map[string]float64
	ints  map[string]bool
	mins  map[string]float64
	maxes map[string]float64
}

func bdGroup(t *testing.T, group bson.D, rows []map[string]any) []bson.Raw {
	t.Helper()
	var idSpec bson.D
	switch id := group[0].Value.(type) {
	case bson.D:
		idSpec = id
	default:
		t.Fatalf("group _id %T not supported", id)
	}
	accs := group[1:]
	var order []string
	groups := map[string]*bdAccumulated{}
	for _, row := range rows {
		id := make(bson.D, 0, len(idSpec))
		for _, e := range idSpec {
			value := evalExpr(t, e.Value, row)
			if _, missing := value.(pipelineMissing); missing {
				value = nil
			}
			id = append(id, bson.E{Key: e.Key, Value: value})
		}
		key := fmt.Sprintf("%#v", id)
		g := groups[key]
		if g == nil {
			g = &bdAccumulated{id: id, sums: map[string]float64{}, ints: map[string]bool{}, mins: map[string]float64{}, maxes: map[string]float64{}}
			groups[key] = g
			order = append(order, key)
		}
		for _, acc := range accs {
			spec := acc.Value.(bson.D)[0]
			value := evalExpr(t, spec.Value, row)
			n, ok := pipelineNum(value)
			switch spec.Key {
			case "$sum":
				if _, seen := g.ints[acc.Key]; !seen {
					g.ints[acc.Key] = true
				}
				if ok {
					g.sums[acc.Key] += n
					if _, isInt := value.(float64); isInt {
						g.ints[acc.Key] = false
					}
				}
			case "$min":
				if ok {
					if cur, have := g.mins[acc.Key]; !have || n < cur {
						g.mins[acc.Key] = n
					}
				}
			case "$max":
				if ok {
					if cur, have := g.maxes[acc.Key]; !have || n > cur {
						g.maxes[acc.Key] = n
					}
				}
			default:
				t.Fatalf("accumulator %s not supported", spec.Key)
			}
		}
	}
	out := make([]bson.Raw, 0, len(order))
	for _, key := range order {
		g := groups[key]
		doc := bson.D{{Key: "_id", Value: g.id}}
		for _, acc := range accs {
			op := acc.Value.(bson.D)[0].Key
			switch op {
			case "$sum":
				if g.ints[acc.Key] {
					doc = append(doc, bson.E{Key: acc.Key, Value: int64(g.sums[acc.Key])})
				} else {
					doc = append(doc, bson.E{Key: acc.Key, Value: g.sums[acc.Key]})
				}
			case "$min":
				if v, ok := g.mins[acc.Key]; ok {
					doc = append(doc, bson.E{Key: acc.Key, Value: v})
				} else {
					doc = append(doc, bson.E{Key: acc.Key, Value: nil})
				}
			case "$max":
				if v, ok := g.maxes[acc.Key]; ok {
					doc = append(doc, bson.E{Key: acc.Key, Value: v})
				} else {
					doc = append(doc, bson.E{Key: acc.Key, Value: nil})
				}
			}
		}
		raw, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}

// bdLegacyDocs encodes documents as the MongoDB 3.4 metadata projection returns
// them: one raw document each, with an object id and the oversize flag.
func bdLegacyDocs(t *testing.T, docs []map[string]any) []any {
	t.Helper()
	out := make([]any, len(docs))
	for i, doc := range docs {
		encoded := bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "_auditMetadataOversized", Value: false}}
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, missing := doc[k].(pipelineMissing); !missing {
				encoded = append(encoded, bson.E{Key: k, Value: doc[k]})
			}
		}
		raw, err := bson.Marshal(encoded)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = bson.Raw(raw)
	}
	return out
}

type bdHarness struct {
	store     *Store
	handle    *fakeClient
	sources   []audit.Source
	mu        sync.Mutex
	pipelines []mongo.Pipeline
}

// bdStore wires a store whose server is the simulator (modern) or a metadata
// stream (legacy) over docs. Every source serves the same documents.
func bdStore(t *testing.T, legacy bool, docs []map[string]any, extraCollections ...string) *bdHarness {
	t.Helper()
	s, first, handle := recordStore(t, legacy)
	h := &bdHarness{store: s, handle: handle, sources: []audit.Source{first}}
	for _, collection := range extraCollections {
		h.sources = append(h.sources, addFakeSource(t, s, collection))
	}
	legacyRaw := bdLegacyDocs(t, docs)
	handle.aggregateFn = func(ctx context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		h.mu.Lock()
		h.pipelines = append(h.pipelines, pipeline)
		h.mu.Unlock()
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > audit.OperationTimeout {
			t.Errorf("breakdown query must carry a deadline of at most %v", audit.OperationTimeout)
		}
		if legacy {
			return &fakeCursor{docs: legacyRaw}, nil
		}
		raws := bdSimulate(t, pipeline, docs)
		out := make([]any, len(raws))
		for i, raw := range raws {
			out[i] = raw
		}
		return &fakeCursor{docs: out}, nil
	}
	return h
}

func (h *bdHarness) ids() []string {
	ids := make([]string, len(h.sources))
	for i, s := range h.sources {
		ids[i] = s.ID
	}
	return ids
}

func bdBreakdown(t *testing.T, h *bdHarness, q audit.Query, dim string, top int) audit.Breakdown {
	t.Helper()
	q.SourceIDs = h.ids()
	got, err := h.store.Breakdown(context.Background(), audit.BreakdownInput{Query: q, Dim: dim, Top: top})
	if err != nil {
		t.Fatalf("%s: %v", dim, err)
	}
	return got
}

func TestBreakdownTopNPlusOtherSumsToTotal(t *testing.T) {
	now := bdNow()
	cases := bdCases(now)
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			h := bdStore(t, legacy, bdDocs(cases))
			got := bdBreakdown(t, h, bdQuery(now), audit.BreakdownUserAgent, 3)
			if got.Total != 17 || got.Distinct != 7 || len(got.Rows) != 3 || got.Other == nil {
				t.Fatalf("total=%d distinct=%d rows=%d other=%v", got.Total, got.Distinct, len(got.Rows), got.Other)
			}
			var sum int64
			for i, want := range []struct {
				value string
				count int64
			}{{"UA-A", 5}, {"UA-B", 4}, {"UA-C", 3}} {
				row := got.Rows[i]
				if row.Value != want.value || row.Count != want.count || row.Filter == nil || len(row.Filter.UserAgents) != 1 || row.Filter.UserAgents[0] != want.value {
					t.Fatalf("row %d = %#v, want %s x%d", i, row, want.value, want.count)
				}
				sum += row.Count
			}
			if got.Other.Value != "Other" || got.Other.Values != 4 || got.Other.Count != 5 || got.Other.Filter != nil {
				t.Fatalf("other = %#v, want 4 values / 5 rows and no filter", got.Other)
			}
			if sum+got.Other.Count != got.Total {
				t.Fatalf("rows %d + other %d != total %d", sum, got.Other.Count, got.Total)
			}
			full := bdBreakdown(t, h, bdQuery(now), audit.BreakdownUserAgent, 50)
			if full.Other != nil || len(full.Rows) != 7 || full.Total != 17 {
				t.Fatalf("top 50 must list all 7 values without Other: rows=%d other=%v total=%d", len(full.Rows), full.Other, full.Total)
			}
		})
	}
}

func bdExpect(cases []bdCase, dim string) map[string]*audit.BreakdownRow {
	want := map[string]*audit.BreakdownRow{}
	valid := map[string][]float64{}
	for _, c := range cases {
		key := bdKey(dim, c.doc)
		row := want[key]
		if row == nil {
			row = &audit.BreakdownRow{}
			want[key] = row
		}
		row.Count++
		if c.state == "incomplete" {
			row.Incomplete++
		}
		if c.pending {
			row.Pending++
		}
		switch c.class {
		case audit.StatusClientError:
			row.Count4xx++
		case audit.StatusServerError:
			row.Count5xx++
		}
		if c.state == "valid" {
			valid[key] = append(valid[key], c.ms)
		}
	}
	for key, row := range want {
		row.Complete = row.Count - row.Incomplete
		row.Stats.Count = int64(len(valid[key]))
		if len(valid[key]) > 0 {
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, ms := range valid[key] {
				lo, hi = math.Min(lo, ms), math.Max(hi, ms)
			}
			row.Stats.MinMs, row.Stats.MaxMs = &lo, &hi
		}
	}
	return want
}

func TestBreakdownMatchesIndependentOracleOnEveryDimension(t *testing.T) {
	now := bdNow()
	cases := bdCases(now)
	for _, legacy := range []bool{false, true} {
		for _, dim := range bdDims {
			t.Run(fmt.Sprintf("%s/legacy=%v", dim, legacy), func(t *testing.T) {
				h := bdStore(t, legacy, bdDocs(cases))
				got := bdBreakdown(t, h, bdQuery(now), dim, 50)
				want := bdExpect(cases, dim)
				if got.Other != nil || len(got.Rows) != len(want) || got.Total != 17 {
					t.Fatalf("rows=%d want %d, other=%v, total=%d", len(got.Rows), len(want), got.Other, got.Total)
				}
				for _, row := range got.Rows {
					key := row.Value
					if row.Missing {
						key = ""
						if row.Value != "(not yet written)" {
							t.Fatalf("missing row labelled %q", row.Value)
						}
					}
					w := want[key]
					if w == nil {
						t.Fatalf("unexpected row %q", row.Value)
					}
					if row.Count != w.Count || row.Complete != w.Complete || row.Incomplete != w.Incomplete || row.Pending != w.Pending || row.Count4xx != w.Count4xx || row.Count5xx != w.Count5xx {
						t.Errorf("%q counts = %d/%d/%d/%d/%d/%d, want %d/%d/%d/%d/%d/%d", key,
							row.Count, row.Complete, row.Incomplete, row.Pending, row.Count4xx, row.Count5xx,
							w.Count, w.Complete, w.Incomplete, w.Pending, w.Count4xx, w.Count5xx)
					}
					if row.Stats.Count != w.Stats.Count {
						t.Errorf("%q latency samples = %d, want %d valid rows only", key, row.Stats.Count, w.Stats.Count)
					}
					if w.Stats.Count > 0 {
						if row.Stats.MinMs == nil || *row.Stats.MinMs != *w.Stats.MinMs || *row.Stats.MaxMs != *w.Stats.MaxMs || row.Stats.P50 == nil || row.Stats.P95 == nil {
							t.Errorf("%q stats = %#v, want min %v max %v with P50 and P95", key, row.Stats, *w.Stats.MinMs, *w.Stats.MaxMs)
						}
					} else if row.Stats.P50 != nil || row.Stats.P95 != nil {
						t.Errorf("%q has percentiles without a valid row", key)
					}
					if len(row.Stats.Histogram) != 0 {
						t.Errorf("%q row must not carry a histogram", key)
					}
				}
			})
		}
	}
}

func TestBreakdownPollingTypeMissingLabel(t *testing.T) {
	now := bdNow()
	var docs []map[string]any
	for i, polling := range []any{bdAbsent, "", "   ", "Synchronous ", "Synchronous"} {
		doc := bdCases(now)[i].doc
		doc["RequestPollingType"] = polling
		docs = append(docs, doc)
	}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			h := bdStore(t, legacy, docs)
			got := bdBreakdown(t, h, bdQuery(now), audit.BreakdownPollingType, 20)
			if len(got.Rows) != 2 || got.Total != 5 {
				t.Fatalf("rows = %#v, want a missing row and one Synchronous row", got.Rows)
			}
			missing, sync := got.Rows[0], got.Rows[1]
			if missing.Value != "(not yet written)" || !missing.Missing || missing.Count != 3 {
				t.Fatalf("missing row = %#v", missing)
			}
			if missing.Filter == nil || !reflect.DeepEqual(missing.Filter.PollingTypes, []string{""}) {
				t.Fatalf("missing row filter = %#v, want the empty polling type", missing.Filter)
			}
			if sync.Value != "Synchronous" || sync.Missing || sync.Count != 2 || sync.Filter == nil || !reflect.DeepEqual(sync.Filter.PollingTypes, []string{"Synchronous"}) {
				t.Fatalf("trimmed row = %#v", sync)
			}
		})
	}
}

func TestLegacyBreakdownMatchesModern(t *testing.T) {
	now := bdNow()
	docs := bdDocs(bdCases(now))
	// A row that is pending under the modern rules but has no status at all, and
	// an ApiTime that is absent, so the api metric has an incomplete row too.
	delete(docs[5], "ApiTime")
	for _, metric := range []string{"total", "api"} {
		for _, outcome := range []string{"all", "success", "failure", "client_error", "server_error"} {
			for _, kind := range []string{"all", "synchronous"} {
				for _, dim := range bdDims {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", metric, outcome, kind, dim), func(t *testing.T) {
						q := bdQuery(now)
						q.Metric, q.Outcome, q.Kind = metric, outcome, kind
						modern := bdBreakdown(t, bdStore(t, false, docs), q, dim, 3)
						old := bdBreakdown(t, bdStore(t, true, docs), q, dim, 3)
						modern.Sources, old.Sources = nil, nil
						if !reflect.DeepEqual(modern, old) {
							t.Fatalf("legacy differs from modern\nmodern: %#v\nlegacy: %#v", modern, old)
						}
					})
				}
			}
		}
	}
}

// bdRowFilterDocs counts the documents a RecordFilter keeps under the request
// drill-down pipeline, using the same first-stage skip the other pipeline tests use.
func bdDrillCount(t *testing.T, q audit.Query, filter audit.RecordFilter, docs []map[string]any) int {
	t.Helper()
	q.RecordFilter = filter
	cohort := [5]string{"", q.Controller, q.Action, q.Kind, q.Outcome}
	pipeline := requestPipeline(q, cohort, "source", nil, 1)
	n := 0
	for _, doc := range docs {
		if _, ok := evalRowSurvives(t, pipeline, doc); ok {
			n++
		}
	}
	return n
}

func TestBreakdownRowFilterDrillCountMatchesPipelines(t *testing.T) {
	now := bdNow()
	docs := bdDocs(bdCases(now))
	for _, outcome := range []string{"all", "success"} {
		for _, dim := range bdDims {
			q := bdQuery(now)
			q.Outcome = outcome
			modern := bdBreakdown(t, bdStore(t, false, docs), q, dim, 50)
			legacy := bdStore(t, true, docs)
			for _, row := range modern.Rows {
				if row.Filter == nil {
					t.Fatalf("%s/%s: row %q has no filter", outcome, dim, row.Value)
				}
				if got := bdDrillCount(t, q, *row.Filter, docs); int64(got) != row.Count {
					t.Errorf("%s/%s %q: server drill-down returns %d rows, breakdown says %d", outcome, dim, row.Value, got, row.Count)
				}
				dq := q
				dq.SourceIDs = legacy.ids()
				dq.RecordFilter = *row.Filter
				page, err := legacy.store.Requests(context.Background(), audit.PageInput{Query: dq, Limit: audit.MaxPageSize})
				if err != nil {
					t.Fatal(err)
				}
				if int64(len(page.Rows)) != row.Count {
					t.Errorf("%s/%s %q: legacy drill-down returns %d rows, breakdown says %d", outcome, dim, row.Value, len(page.Rows), row.Count)
				}
			}
		}
	}
}

func TestBreakdownRowFilterOnlyKeepsExactValues(t *testing.T) {
	now := bdNow()
	docs := bdDocs(bdCases(now))
	q := bdQuery(now)
	// "OK" and " OK " are one status value once trimmed, so one filter keeps both:
	// 17 rows minus 3 NotFound, 3 InternalServerError, the Accepted one and the blank one.
	if got := bdDrillCount(t, q, audit.RecordFilter{StatusCodes: []string{"OK"}}, docs); got != 9 {
		t.Fatalf("status OK keeps %d rows, want 9", got)
	}
	// An empty entry means "missing", and it is not a wildcard.
	if got := bdDrillCount(t, q, audit.RecordFilter{UserAgents: []string{""}}, docs); got != 1 {
		t.Fatalf("missing user agent keeps %d rows, want 1", got)
	}
	if got := bdDrillCount(t, q, audit.RecordFilter{UserIDMissing: true}, docs); got != 5 {
		t.Fatalf("missing user keeps %d rows, want 5", got)
	}
	if got := bdDrillCount(t, q, audit.RecordFilter{UserIDMissing: true, UserIDs: []int{100}}, docs); got <= 5 {
		t.Fatalf("missing-or-100 keeps %d rows, want more than the 5 missing", got)
	}
	// Lists combine with AND across fields and OR inside one.
	if got := bdDrillCount(t, q, audit.RecordFilter{UserAgents: []string{"UA-A", "UA-B"}, UIVersions: []string{"1.0"}}, docs); got != 5 {
		t.Fatalf("UA-A or UA-B with ui 1.0 keeps %d rows, want 5", got)
	}
	// A value that looks like a field path is only ever a literal.
	if got := bdDrillCount(t, q, audit.RecordFilter{UserAgents: []string{"$pathLike"}}, docs); got != 1 {
		t.Fatalf("$pathLike keeps %d rows, want 1", got)
	}
}

func TestLegacyBreakdownAvoidsServerSideOperators(t *testing.T) {
	now := bdNow()
	h := bdStore(t, true, bdDocs(bdCases(now)))
	for _, dim := range bdDims {
		q := bdQuery(now)
		q.RecordFilter = audit.RecordFilter{UserAgents: []string{"UA-A"}}
		bdBreakdown(t, h, q, dim, 5)
	}
	if len(h.pipelines) != len(bdDims) {
		t.Fatalf("legacy breakdown ran %d pipelines for %d dimensions; it must scan once", len(h.pipelines), len(bdDims))
	}
	for _, pipeline := range h.pipelines {
		text, err := bson.MarshalExtJSON(bson.D{{Key: "p", Value: pipeline}}, false, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"$convert", "$toString", "$regexMatch", "$dateDiff", "$group", "$facet", "$expr"} {
			if strings.Contains(string(text), banned) {
				t.Errorf("legacy pipeline uses %s: %s", banned, text)
			}
		}
		if !strings.Contains(string(text), legacyMetadataMarker) {
			t.Errorf("legacy breakdown must read the bounded metadata projection: %s", text)
		}
	}
}

func TestLegacyBreakdownFailsClosedOnOversizedMetadata(t *testing.T) {
	now := bdNow()
	h := bdStore(t, true, bdDocs(bdCases(now)))
	oversized, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "_auditMetadataOversized", Value: true}})
	h.handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: []any{bson.Raw(oversized)}}, nil
	}
	_, err := h.store.Breakdown(context.Background(), audit.BreakdownInput{Query: bdQuery(now, h.sources[0].ID), Dim: audit.BreakdownUserAgent})
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrLegacyScanLimit) {
		t.Fatalf("oversized metadata must fail the request with the legacy scan limit, got %v", err)
	}
}

func TestBreakdownGroupLimitFailsClosed(t *testing.T) {
	now := bdNow()
	s, source, handle := recordStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		docs := make([]any, audit.MaxBreakdownGroups+1)
		for i := range docs {
			raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.D{{Key: "v", Value: fmt.Sprint("ua-", i)}}}, {Key: "count", Value: int64(1)}})
			docs[i] = bson.Raw(raw)
		}
		return &fakeCursor{docs: docs}, nil
	}
	_, err := s.Breakdown(context.Background(), audit.BreakdownInput{Query: bdQuery(now, source.ID), Dim: audit.BreakdownUserAgent})
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrBreakdownGroupLimit) {
		t.Fatalf("too many distinct values must fail closed, got %v", err)
	}
}

func TestBreakdownValidatesInputAndKeepsLimits(t *testing.T) {
	now := bdNow()
	h := bdStore(t, false, nil)
	q := bdQuery(now, h.sources[0].ID)
	for name, in := range map[string]audit.BreakdownInput{
		"unknown dimension": {Query: q, Dim: "controller"},
		"top over 50":       {Query: q, Dim: audit.BreakdownUserAgent, Top: 51},
		"negative top":      {Query: q, Dim: audit.BreakdownUserAgent, Top: -1},
		"range over 7 days": {Query: func() audit.Query { w := q; w.Start = w.End.Add(-8 * 24 * time.Hour); return w }(), Dim: audit.BreakdownUserAgent},
		"filter too long":   {Query: func() audit.Query { w := q; w.UserIPs = []string{strings.Repeat("x", 65)}; return w }(), Dim: audit.BreakdownUserAgent},
	} {
		if _, err := h.store.Breakdown(context.Background(), in); !errors.Is(err, audit.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
	if _, err := h.store.Breakdown(context.Background(), audit.BreakdownInput{Query: q, Dim: audit.BreakdownUserAgent, Top: 0}); err != nil {
		t.Fatalf("top 0 means the default: %v", err)
	}
}

func TestBreakdownReportsPartialSourceFailureWithoutItsRows(t *testing.T) {
	now := bdNow()
	docs := bdDocs(bdCases(now))
	h := bdStore(t, false, docs, "broken")
	inner := h.handle.aggregateFn
	h.handle.aggregateFn = func(ctx context.Context, db, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		if collection == "broken" {
			return nil, errors.New("mongodb://reader:secret@host: network error")
		}
		return inner(ctx, db, collection, pipeline)
	}
	got := bdBreakdown(t, h, bdQuery(now), audit.BreakdownUserAgent, 50)
	if got.Total != 17 {
		t.Fatalf("total = %d, want only the healthy source's 17 rows", got.Total)
	}
	if len(got.Sources) != 2 || !got.Sources[0].OK || got.Sources[1].OK || got.Sources[1].Error == "" || strings.Contains(got.Sources[1].Error, "secret") {
		t.Fatalf("sources = %#v", got.Sources)
	}
}

func TestBreakdownMergesSourcesAndKeepsTopRowsConsistent(t *testing.T) {
	now := bdNow()
	docs := bdDocs(bdCases(now))
	h := bdStore(t, false, docs, "second")
	got := bdBreakdown(t, h, bdQuery(now), audit.BreakdownUserAgent, 2)
	if got.Total != 34 || len(got.Rows) != 2 || got.Rows[0].Count != 10 || got.Rows[1].Count != 8 || got.Other == nil || got.Other.Count != 16 {
		t.Fatalf("two identical sources must double every count: %#v", got)
	}
	// UA-A has five rows per source; two of them are incomplete, so three are valid.
	if got.Rows[0].Value != "UA-A" || got.Rows[0].Stats.Count != 6 || got.Rows[0].Incomplete != 4 {
		t.Fatalf("top row = %#v, want UA-A with 3 valid rows and 2 incomplete per source", got.Rows[0])
	}
}

func TestBreakdownRowFilterDrillCountMatches(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	repo := audit.NewDemo(now)
	reader, ok := repo.(audit.BreakdownReader)
	if !ok {
		t.Fatal("demo repository must implement BreakdownReader")
	}
	q := audit.Query{
		SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-15 * time.Minute), End: now,
		BaselineStart: now.Add(-30 * time.Minute), BaselineEnd: now.Add(-15 * time.Minute),
		Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 10,
	}
	for _, dim := range bdDims {
		got, err := reader.Breakdown(context.Background(), audit.BreakdownInput{Query: q, Dim: dim, Top: 50})
		if err != nil {
			t.Fatalf("%s: %v", dim, err)
		}
		var sum int64
		if len(got.Rows) < 2 && dim != audit.BreakdownStatusCode && dim != audit.BreakdownPollingType {
			t.Errorf("%s: the demo should show more than one value, got %d", dim, len(got.Rows))
		}
		for _, row := range got.Rows {
			sum += row.Count
			dq := q
			dq.RecordFilter = *row.Filter
			var seen int64
			cursor := ""
			for {
				page, err := repo.Requests(context.Background(), audit.PageInput{Query: dq, Limit: audit.MaxPageSize, Cursor: cursor})
				if err != nil {
					t.Fatalf("%s %q: %v", dim, row.Value, err)
				}
				seen += int64(len(page.Rows))
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if seen != row.Count {
				t.Errorf("%s %q: drill-down pages %d records, breakdown says %d", dim, row.Value, seen, row.Count)
			}
		}
		if got.Other != nil {
			sum += got.Other.Count
		}
		if sum != got.Total {
			t.Errorf("%s: rows plus other = %d, total %d", dim, sum, got.Total)
		}
	}
}
