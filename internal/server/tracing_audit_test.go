package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// auditRow is one request row the fake rollup counts.
type auditRow struct {
	at    time.Time
	class string
}

// auditFake is an audit.Repository (only Sources works) and an
// audit.RollupReader. Rollup builds the slots of the plan from rows with
// AddRow, so the slot width is the one the caller asked for.
type auditFake struct {
	audit.Repository
	sources   []audit.Source
	listErr   error
	listCalls int
	rows      []auditRow
	results   []audit.SourceResult // nil: every planned source OK
	err       error
	plans     []audit.RollupPlan
	onRollup  func() // runs first in Rollup (a test cancels the request here)
}

func (f *auditFake) Sources(context.Context) ([]audit.Source, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.sources, nil
}

func (f *auditFake) Rollup(_ context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	f.plans = append(f.plans, plan)
	if f.onRollup != nil {
		f.onRollup()
	}
	if f.err != nil {
		return audit.RollupSlots{}, f.err
	}
	b := audit.NewRollupBuilder(plan.Width)
	ms := 10.0
	for _, r := range f.rows {
		if r.at.Before(plan.From) || !r.at.Before(plan.To) {
			continue
		}
		b.AddRow(r.at, "synchronous", "valid", r.class, &ms, audit.DefaultRollupSlowMs)
	}
	res := f.results
	if res == nil {
		for _, id := range plan.SourceIDs {
			res = append(res, audit.SourceResult{SourceID: id, OK: true})
		}
	}
	return audit.RollupSlots{Builder: b, Sources: res}, nil
}

func auditSources(ids ...string) []audit.Source {
	out := make([]audit.Source, len(ids))
	for i, id := range ids {
		out[i] = audit.Source{ID: id, Label: "Label " + id, Database: "secretdb", Collection: "secretcoll"}
	}
	return out
}

var auditT0 = time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)

func auditClock(at time.Time) func() time.Time { return func() time.Time { return at } }

func TestAuditLaneBucketsFromRollup(t *testing.T) {
	axis := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	if axis.WidthMs != 60000 {
		t.Fatalf("axis width %d", axis.WidthMs)
	}
	f := &auditFake{rows: []auditRow{
		{auditT0.Add(10 * time.Second), ""},
		{auditT0.Add(50 * time.Second), ""},
		{auditT0.Add(65 * time.Second), audit.StatusServerError},
	}}
	clk := auditT0.Add(2 * time.Hour)
	got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(clk))
	if !got.Complete || got.Truncated || got.ReasonCode != "" || got.Reason != "" {
		t.Fatalf("read = %+v", got)
	}
	if !got.AsOf.Equal(clk) {
		t.Errorf("asOf %v, want %v", got.AsOf, clk)
	}
	if got.Scanned != 3 || len(got.Buckets) != 2 {
		t.Fatalf("scanned %d buckets %+v", got.Scanned, got.Buckets)
	}
	b0, b1 := got.Buckets[0], got.Buckets[1]
	if !b0.Start.Equal(auditT0) || !b0.End.Equal(auditT0.Add(time.Minute)) || b0.Count != 2 || b0.Errors != 0 || b0.Clipped {
		t.Errorf("bucket 0 = %+v", b0)
	}
	if !b1.Start.Equal(auditT0.Add(time.Minute)) || b1.Count != 1 || b1.Errors != 1 {
		t.Errorf("bucket 1 = %+v", b1)
	}
}

func TestAuditLaneOneAggregateCall(t *testing.T) {
	short := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	longStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	long := newTracingAxis(longStart, longStart.Add(29*24*time.Hour))
	if long.WidthMs != 6*3600000 {
		t.Fatalf("long axis width %d", long.WidthMs)
	}
	for _, c := range []struct {
		name string
		axis tracingAxis
		want time.Duration
	}{{"60 s", short, time.Minute}, {"6 h", long, time.Hour}} {
		f := &auditFake{}
		ids := []string{"a1", "a2", "a3"}
		readAuditLane(context.Background(), f, ids, c.axis, auditClock(auditT0))
		if len(f.plans) != 1 {
			t.Fatalf("%s: %d Rollup calls, want 1", c.name, len(f.plans))
		}
		p := f.plans[0]
		if strings.Join(p.SourceIDs, ",") != "a1,a2,a3" {
			t.Errorf("%s: ids %v", c.name, p.SourceIDs)
		}
		if p.Width != c.want {
			t.Errorf("%s: width %v, want %v", c.name, p.Width, c.want)
		}
		if p.Zone != "UTC" || p.Loc != time.UTC || !p.From.Equal(c.axis.Start) || !p.To.Equal(c.axis.End) {
			t.Errorf("%s: plan %+v", c.name, p)
		}
		if p.SlowMs != audit.DefaultRollupSlowMs {
			t.Errorf("%s: slowMs %v", c.name, p.SlowMs)
		}
	}
}

func TestAuditLaneFailureReasons(t *testing.T) {
	const secret = "mongodb://user:hunter2@db-host.example/secret"
	axis := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	cases := []struct {
		name   string
		err    error
		code   string
		reason string
	}{
		{"broad scan", fmt.Errorf("%s: %w", secret, audit.ErrBroadScanRefused), "error", "The audit source reads at most 24 hours unless broad scans are allowed, so this window was not read."},
		{"group limit", fmt.Errorf("%s: %w", secret, audit.ErrRollupGroupLimit), "rowcap", "The audit read reached its row limit; nothing was shortened."},
		{"limit", fmt.Errorf("%s: %w", secret, audit.ErrLimit), "rowcap", "The audit read reached its row limit; nothing was shortened."},
		{"deadline", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), "timeout", "The audit read ran out of time; nothing was shortened."},
		{"docs examined", fmt.Errorf("%s: %w", secret, audit.ErrDocsExaminedBudget), "timeout", "The audit read ran out of time; nothing was shortened."},
		{"random", errors.New(secret), "error", "The audit read failed; nothing was shortened."},
		// A cancel the request did not cause is an ordinary failure.
		{"cancel, request alive", fmt.Errorf("%s: %w", secret, context.Canceled), "error", "The audit read failed; nothing was shortened."},
	}
	for _, c := range cases {
		f := &auditFake{err: c.err, rows: []auditRow{{auditT0.Add(time.Second), ""}}}
		got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
		if got.Complete || len(got.Buckets) != 0 || got.Truncated {
			t.Errorf("%s: read = %+v", c.name, got)
		}
		if got.ReasonCode != c.code || got.Reason != c.reason {
			t.Errorf("%s: code %q reason %q, want %q %q", c.name, got.ReasonCode, got.Reason, c.code, c.reason)
		}
		if strings.Contains(got.Reason, "hunter2") || strings.Contains(got.Reason, "db-host") {
			t.Errorf("%s: reason holds error text: %q", c.name, got.Reason)
		}
		if got.Buckets == nil {
			t.Errorf("%s: buckets nil", c.name)
		}
	}
	// The request itself was cancelled: no reason text at all.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &auditFake{err: fmt.Errorf("%s: %w", secret, context.Canceled)}
	got := readAuditLane(cctx, f, []string{"a1"}, axis, auditClock(auditT0))
	if got.Complete || got.ReasonCode != "error" || got.Reason != "" {
		t.Errorf("cancelled request: %+v", got)
	}
}

func TestAuditLanePartialSourceKeepsCounts(t *testing.T) {
	axis := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	f := &auditFake{
		rows: []auditRow{{auditT0.Add(5 * time.Second), ""}},
		results: []audit.SourceResult{
			{SourceID: "a1", OK: true},
			{SourceID: "a2", OK: false, ErrorCode: "query_timeout", Error: "driver text hunter2"},
		},
	}
	got := readAuditLane(context.Background(), f, []string{"a1", "a2"}, axis, auditClock(auditT0))
	if got.Complete || got.ReasonCode != "timeout" || got.Reason != "The audit read ran out of time; nothing was shortened." {
		t.Fatalf("read = %+v", got)
	}
	if len(got.Buckets) != 1 || got.Buckets[0].Count != 1 {
		t.Errorf("buckets %+v", got.Buckets)
	}
	if len(got.Sources) != 2 || got.Sources[1].OK || got.Sources[1].ErrorCode != "query_timeout" {
		t.Errorf("sources %+v", got.Sources)
	}
	for _, s := range got.Sources {
		if s.Error != "" {
			t.Errorf("driver text kept: %+v", s)
		}
	}
	// A memory-limit failure is a rowcap, any other failure an error.
	for code, want := range map[string]string{"query_memory_limit": "rowcap", "permission_denied": "error", "": "error"} {
		f.results = []audit.SourceResult{{SourceID: "a1", OK: false, ErrorCode: code}}
		if got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0)); got.ReasonCode != want || got.Complete {
			t.Errorf("code %q -> %q complete %v, want %q", code, got.ReasonCode, got.Complete, want)
		}
	}
	// No source results at all cannot be called complete.
	f.results = []audit.SourceResult{}
	if got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0)); got.Complete || got.ReasonCode != "error" {
		t.Errorf("no results: %+v", got)
	}
	// Every requested id needs its own OK result: a missing one (a2) leaves the lane partial.
	f.results = []audit.SourceResult{{SourceID: "a1", OK: true}}
	got = readAuditLane(context.Background(), f, []string{"a1", "a2"}, axis, auditClock(auditT0))
	if got.Complete || got.ReasonCode != "error" || got.Reason != "The audit read failed; nothing was shortened." {
		t.Errorf("missing a2 result: %+v", got)
	}
	// An OK result for some other id does not stand in for a1.
	f.results = []audit.SourceResult{{SourceID: "zz", OK: true}}
	if got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0)); got.Complete || got.ReasonCode != "error" {
		t.Errorf("wrong id result: %+v", got)
	}
	// All requested ids OK: complete.
	f.results = []audit.SourceResult{{SourceID: "a2", OK: true}, {SourceID: "a1", OK: true}}
	if got = readAuditLane(context.Background(), f, []string{"a1", "a2"}, axis, auditClock(auditT0)); !got.Complete || got.ReasonCode != "" {
		t.Errorf("all ok: %+v", got)
	}
}

func TestAuditLaneAlignsToAxis(t *testing.T) {
	start := time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(30*24*time.Hour))
	if axis.WidthMs != 6*3600000 && axis.WidthMs != 8*3600000 {
		t.Fatalf("axis width %d", axis.WidthMs)
	}
	var rows []auditRow
	for i := 0; i < 700; i++ {
		rows = append(rows, auditRow{start.Add(time.Duration(i)*time.Hour + time.Duration(i%59)*time.Minute), ""})
	}
	f := &auditFake{rows: rows}
	got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if !got.Complete || len(got.Buckets) == 0 || len(got.Buckets) > 120 {
		t.Fatalf("read complete %v buckets %d", got.Complete, len(got.Buckets))
	}
	starts := map[int64]bool{}
	for k := 0; k < axis.Buckets; k++ {
		if from, _, _, ok := axis.Bucket(k); ok {
			starts[from.UnixMilli()] = true
		}
	}
	// Expected per-cell counts, worked out from the rows alone: each row falls in
	// its hour slot, and the slot in the axis cell that holds it.
	cellMs := axis.WidthMs
	wantCounts := map[int64]int64{}
	for _, r := range rows {
		slot := r.at.Truncate(time.Hour)
		k := (slot.UnixMilli() - axis.AxisStart.UnixMilli()) / cellMs
		from, _, _, ok := axis.Bucket(int(k))
		if !ok {
			t.Fatalf("row %v has no axis cell %d", r.at, k)
		}
		wantCounts[from.UnixMilli()]++
	}
	if len(got.Buckets) != len(wantCounts) {
		t.Errorf("%d buckets, want %d", len(got.Buckets), len(wantCounts))
	}
	var sum int64
	for _, b := range got.Buckets {
		if !starts[b.Start.UnixMilli()] {
			t.Errorf("bucket start %v is not an axis cell start", b.Start)
		}
		if b.Start.Before(axis.AxisStart) {
			t.Errorf("bucket start %v before the axis", b.Start)
		}
		if want := wantCounts[b.Start.UnixMilli()]; b.Count != want || b.Errors != 0 {
			t.Errorf("bucket %v count %d errors %d, want %d and 0", b.Start, b.Count, b.Errors, want)
		}
		sum += b.Count
	}
	if sum != int64(len(rows)) || got.Scanned != sum {
		t.Errorf("sum %d scanned %d, rows %d", sum, got.Scanned, len(rows))
	}
	if f.plans[0].Width != time.Hour {
		t.Errorf("read width %v", f.plans[0].Width)
	}

	// A window that starts inside a cell: the first bucket is cut to the window.
	s2 := time.Date(2026, 3, 10, 10, 7, 30, 0, time.UTC)
	axis = newTracingAxis(s2, s2.Add(time.Hour))
	f = &auditFake{rows: []auditRow{{s2.Add(10 * time.Second), ""}, {s2.Add(90 * time.Second), ""}}}
	got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if len(got.Buckets) != 2 {
		t.Fatalf("buckets %+v", got.Buckets)
	}
	if first := got.Buckets[0]; !first.Start.Equal(s2) || !first.Clipped || !first.End.Equal(time.Date(2026, 3, 10, 10, 8, 0, 0, time.UTC)) {
		t.Errorf("first bucket %+v", first)
	}
	if second := got.Buckets[1]; second.Clipped || !second.Start.Equal(time.Date(2026, 3, 10, 10, 9, 0, 0, time.UTC)) {
		t.Errorf("second bucket %+v", second)
	}
	if got.Buckets[0].Count != 1 || got.Buckets[1].Count != 1 || got.Buckets[0].Errors != 0 || got.Buckets[1].Errors != 0 {
		t.Errorf("cut-window counts %+v", got.Buckets)
	}
	// A window that starts on a cell edge is not clipped.
	s3 := time.Date(2026, 3, 10, 10, 7, 0, 0, time.UTC)
	axis = newTracingAxis(s3, s3.Add(time.Hour))
	f = &auditFake{rows: []auditRow{{s3.Add(10 * time.Second), ""}}}
	got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if len(got.Buckets) != 1 || !got.Buckets[0].Start.Equal(s3) || got.Buckets[0].Clipped || got.Buckets[0].Count != 1 {
		t.Errorf("edge window buckets %+v", got.Buckets)
	}
}

func TestAuditLaneNilReader(t *testing.T) {
	axis := newTracingAxis(auditT0, auditT0.Add(time.Hour))
	got := readAuditLane(context.Background(), nil, []string{"a1"}, axis, auditClock(auditT0))
	if got.Complete || got.ReasonCode != "error" || got.Reason != "The audit source cannot be read here." {
		t.Errorf("read = %+v", got)
	}
}

func TestAuditLaneJSONHasNoNulls(t *testing.T) {
	lane := auditLaneToWire(auditLaneRead{Complete: true}, nil)
	raw, err := json.Marshal(lane)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{`"buckets":[]`, `"events":[]`, `"sources":[]`, `"state":"none-seen"`} {
		if !strings.Contains(s, want) {
			t.Errorf("lane lacks %s: %s", want, s)
		}
	}
	if strings.Contains(s, "null") {
		t.Errorf("lane holds a null: %s", s)
	}
}

func TestAuditLaneWireShape(t *testing.T) {
	asOf := time.Date(2026, 3, 10, 12, 0, 0, 0, time.FixedZone("x", 3600))
	read := auditLaneRead{
		Complete: true, AsOf: asOf, Scanned: 4,
		Buckets: []auditBucket{{Start: auditT0, End: auditT0.Add(time.Minute), Clipped: true, Count: 4, Errors: 0}},
	}
	lane := auditLaneToWire(read, []tracingRef{{SourceID: "a1", Label: "Label a1"}})
	raw, _ := json.Marshal(lane)
	s := string(raw)
	for _, want := range []string{
		`"key":"audit"`, `"label":"API requests (audit log)"`, `"origin":"live"`, `"consistency":"live-non-snapshot"`,
		`"asOf":"2026-03-10T11:00:00Z"`, `"state":"events"`, `"complete":true`, `"truncated":false`, `"scanned":4`,
		`"sources":[{"sourceId":"a1","label":"Label a1"}]`, `"events":[]`, `"hidden":0`,
		`"end":"2026-03-10T10:01:00Z"`, `"count":4`, `"errors":0`, `"clipped":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("lane lacks %s: %s", want, s)
		}
	}
	if strings.Contains(s, "null") || strings.Contains(s, `"disabled"`) {
		t.Errorf("lane holds a null or disabled: %s", s)
	}
	if lane.Buckets[0].Complete != true {
		t.Errorf("bucket complete = false")
	}
	// An incomplete read says so.
	inc := auditLaneToWire(auditLaneRead{Complete: false, ReasonCode: "timeout", Reason: "r"}, nil)
	if inc.State != calltrace.StateIncomplete || inc.Complete || inc.ReasonCode != "timeout" || inc.Reason != "r" {
		t.Errorf("incomplete lane %+v", inc)
	}
}

func TestAuditLaneOrderViaAppendLane(t *testing.T) {
	var resp tracingResponse
	stored := func(key string) tracingLane {
		return tracingLane{Key: key, State: calltrace.StateEvents, Origin: tracingOriginCase, Consistency: tracingConsStored, Complete: true}
	}
	appendLane(&resp, stored("sql"))
	appendLane(&resp, stored("iis"))
	appendLane(&resp, auditLaneToWire(auditLaneRead{Complete: true, Buckets: []auditBucket{{Start: auditT0, End: auditT0.Add(time.Minute), Count: 1}}}, nil))
	if len(resp.Lanes) != 3 || resp.Lanes[0].Key != "audit" || resp.Lanes[1].Key != "sql" || resp.Lanes[2].Key != "iis" {
		t.Fatalf("lanes %+v", resp.Lanes)
	}
	finishTracingMeta(&resp)
	if !resp.MixedConsistency || resp.AllLanesStored {
		t.Errorf("mixed %v allStored %v", resp.MixedConsistency, resp.AllLanesStored)
	}
}

func TestAuditSourceIDsCapsAtFifty(t *testing.T) {
	ctx := context.Background()
	ids55 := make([]string, 55)
	for i := range ids55 {
		ids55[i] = "s" + strconv.Itoa(i)
	}
	f := &auditFake{sources: auditSources(ids55...)}
	got, warn, err := auditSourceIDs(ctx, f, false, nil)
	if err != nil || len(got) != 50 || got[0] != "s0" || got[49] != "s49" || warn != "Only the first 50 audit sources are shown." {
		t.Fatalf("55 sources: %d ids, warn %q, err %v", len(got), warn, err)
	}
	f = &auditFake{sources: auditSources(ids55[:50]...)}
	if got, warn, err = auditSourceIDs(ctx, f, false, nil); err != nil || len(got) != 50 || warn != "" {
		t.Errorf("50 sources: %d ids, warn %q, err %v", len(got), warn, err)
	}
	// Duplicates and empty ids are dropped; listed order is kept.
	f = &auditFake{sources: auditSources("b", "", "a", "b", "c")}
	if got, _, _ = auditSourceIDs(ctx, f, false, nil); strings.Join(got, ",") != "b,a,c" {
		t.Errorf("dedupe: %v", got)
	}
	// Explicit with no ids never lists.
	for _, ids := range [][]string{nil, {}} {
		f = &auditFake{sources: auditSources("a")}
		if got, warn, err = auditSourceIDs(ctx, f, true, ids); err != nil || len(got) != 0 || warn != "" || f.listCalls != 0 {
			t.Errorf("explicit empty: %v %q %v, %d list calls", got, warn, err, f.listCalls)
		}
	}
	// Explicit ids are checked and kept in the order sent; a non-explicit call ignores ids.
	f = &auditFake{sources: auditSources("a", "b", "c")}
	if got, _, err = auditSourceIDs(ctx, f, true, []string{"c", "a"}); err != nil || strings.Join(got, ",") != "c,a" {
		t.Errorf("explicit: %v %v", got, err)
	}
	if _, _, err = auditSourceIDs(ctx, f, true, []string{"a", "zz9"}); !errors.Is(err, errUnknownAuditSource) {
		t.Errorf("unknown id: err %v", err)
	}
	if got, _, err = auditSourceIDs(ctx, f, false, []string{"zz9"}); err != nil || strings.Join(got, ",") != "a,b,c" {
		t.Errorf("non-explicit: %v %v", got, err)
	}
	// A listing failure is not an unknown id; a nil repository lists nothing.
	f = &auditFake{listErr: errors.New("boom")}
	if _, _, err = auditSourceIDs(ctx, f, true, []string{"a"}); err == nil || errors.Is(err, errUnknownAuditSource) {
		t.Errorf("listing failure err = %v", err)
	}
	if got, warn, err = auditSourceIDs(ctx, nil, false, nil); err != nil || len(got) != 0 || warn != "" {
		t.Errorf("nil repo default: %v %q %v", got, warn, err)
	}
	if _, _, err = auditSourceIDs(ctx, nil, true, []string{"a"}); !errors.Is(err, errUnknownAuditSource) {
		t.Errorf("nil repo explicit: %v", err)
	}
}

// auditHandlerEnv is a memory case store with repo as the audit repository.
func auditHandlerEnv(t *testing.T, repo audit.Repository) (*caseEnv, string) {
	t.Helper()
	store, _ := tagStores(t)["memory"]()
	e := tagEnv(t, store, repo)
	return e, e.newCase("tracing")
}

func laneKeys(out tracingResponse) string {
	keys := make([]string, len(out.Lanes))
	for i, l := range out.Lanes {
		keys[i] = l.Key
	}
	return strings.Join(keys, ",")
}

func TestTracingUnknownAuditSourceIs404(t *testing.T) {
	f := &auditFake{sources: auditSources("a1")}
	e, id := auditHandlerEnv(t, f)
	res := e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":["zz9"]`))
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "unknown-source" {
		t.Fatalf("zz9 = %d %s, want 404 unknown-source", res.Code, res.Body)
	}
	if got := tracingErrorText(t, res); got != "That source is not part of this case." {
		t.Errorf("says %q", got)
	}
	if strings.Contains(res.Body.String(), "zz9") {
		t.Errorf("body echoes the id: %s", res.Body)
	}
	if len(f.plans) != 0 {
		t.Errorf("%d Rollup calls on an unknown id", len(f.plans))
	}
	// One unknown id among valid ones is still a 404 with no read.
	res = e.json(http.MethodPost, tracingPath(id), tracingBody(`"sourceIds":["a1","zz9"]`))
	if res.Code != http.StatusNotFound || len(f.plans) != 0 {
		t.Errorf("a1+zz9 = %d, %d Rollup calls", res.Code, len(f.plans))
	}
	out, _ := tracingPost(t, e, id, tracingBody(`"sourceIds":["a1"]`))
	if laneKeys(out) != "audit,sql,iis" || len(f.plans) != 1 {
		t.Errorf("a1: lanes %s, %d Rollup calls", laneKeys(out), len(f.plans))
	}
}

func TestTracingDefaultCallHasAuditLane(t *testing.T) {
	rows := []auditRow{{time.Date(2001, 2, 3, 4, 0, 30, 0, time.UTC), ""}, {time.Date(2001, 2, 3, 4, 1, 5, 0, time.UTC), audit.StatusServerError}}
	for _, extra := range []string{``, `"sourceIds":null`, `"sourceIds":[]`} {
		f := &auditFake{sources: auditSources("a1", "a2"), rows: rows}
		e, id := auditHandlerEnv(t, f)
		out, raw := tracingPost(t, e, id, tracingBody(extra))
		if laneKeys(out) != "audit,sql,iis" {
			t.Errorf("%q: lanes %s", extra, laneKeys(out))
		}
		if len(f.plans) != 1 || strings.Join(f.plans[0].SourceIDs, ",") != "a1,a2" {
			t.Errorf("%q: Rollup calls %+v", extra, f.plans)
		}
		lane := tracingLaneOf(t, out, "audit")
		if !lane.Complete || lane.State != calltrace.StateEvents || lane.Scanned != 2 || len(lane.Buckets) != 2 {
			t.Errorf("%q: audit lane %+v", extra, lane)
		}
		if len(lane.Sources) != 2 || lane.Sources[0] != (tracingRef{SourceID: "a1", Label: "Label a1"}) {
			t.Errorf("%q: audit sources %+v", extra, lane.Sources)
		}
		// sql and iis are no-data here, so they say nothing about consistency.
		if out.MixedConsistency || out.AllLanesStored {
			t.Errorf("%q: mixed %v allStored %v", extra, out.MixedConsistency, out.AllLanesStored)
		}
		for _, banned := range []string{"secretdb", "secretcoll", "hunter2"} {
			if strings.Contains(raw, banned) {
				t.Errorf("%q: body holds %q", extra, banned)
			}
		}
	}

	// Only saved sources picked: no audit lane, no Rollup call, no listing.
	f := &auditFake{sources: auditSources("a1", "a2")}
	e, id := auditHandlerEnv(t, f)
	tag := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a"})
	out, _ := tracingPost(t, e, id, tracingBody(`"sourceIds":["`+tracingTagID(tag.ID)+`"]`))
	if laneKeys(out) != "sql,iis" || len(f.plans) != 0 || f.listCalls != 0 {
		t.Errorf("cs_ only: lanes %s, %d Rollup calls, %d list calls", laneKeys(out), len(f.plans), f.listCalls)
	}

	// Naming a2 reads only a2.
	f = &auditFake{sources: auditSources("a1", "a2")}
	e, id = auditHandlerEnv(t, f)
	out, _ = tracingPost(t, e, id, tracingBody(`"sourceIds":["a2"]`))
	if laneKeys(out) != "audit,sql,iis" || len(f.plans) != 1 || strings.Join(f.plans[0].SourceIDs, ",") != "a2" {
		t.Errorf("a2: lanes %s, plans %+v", laneKeys(out), f.plans)
	}
	if lane := tracingLaneOf(t, out, "audit"); len(lane.Sources) != 1 || lane.Sources[0].SourceID != "a2" {
		t.Errorf("a2 lane sources %+v", lane.Sources)
	}
	// An audit id adds no case source to the answer.
	if len(out.Sources) != 0 {
		t.Errorf("a2 response sources %+v, want none", out.Sources)
	}

	// A failed read keeps the answer 200 with an incomplete lane and no error text.
	f = &auditFake{sources: auditSources("a1"), err: errors.New("mongodb://u:hunter2@host")}
	e, id = auditHandlerEnv(t, f)
	out, raw := tracingPost(t, e, id, tracingBody(""))
	lane := tracingLaneOf(t, out, "audit")
	if lane.Complete || lane.State != calltrace.StateIncomplete || lane.ReasonCode != "error" || strings.Contains(raw, "hunter2") {
		t.Errorf("failed read lane %+v", lane)
	}
}

func TestTracingCancelledRequestLeaksNoLaneError(t *testing.T) {
	f := &auditFake{sources: auditSources("a1"), err: fmt.Errorf("mongodb://u:hunter2@host: %w", context.Canceled)}
	e, id := auditHandlerEnv(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onRollup = cancel // the client goes away during the audit read
	req := httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingBody(""))).WithContext(ctx)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	res := e.serve(req)
	if len(f.plans) != 1 {
		t.Fatalf("%d Rollup calls, want 1", len(f.plans))
	}
	if res.Code >= 500 {
		t.Errorf("cancelled request = %d %s", res.Code, res.Body)
	}
	body := res.Body.String()
	if body != "" {
		t.Errorf("cancelled request wrote %q, want nothing", body)
	}
	for _, banned := range []string{tracingAuditErrorMsg, "hunter2", `"audit"`} {
		if strings.Contains(body, banned) {
			t.Errorf("cancelled body holds %q: %s", banned, body)
		}
	}
}

func TestAuditSourceIDsListingFailure(t *testing.T) {
	const warning = "The audit sources could not be listed, so the audit lane is not shown."
	f := &auditFake{listErr: errors.New("mongodb://u:hunter2@host")}
	e, id := auditHandlerEnv(t, f)
	for _, body := range []string{tracingBody(""), tracingBody(`"sourceIds":["a1"]`)} {
		out, raw := tracingPost(t, e, id, body)
		found := false
		for _, w := range out.Warnings {
			if w == warning {
				found = true
			}
		}
		if !found || laneKeys(out) != "sql,iis" || len(f.plans) != 0 || strings.Contains(raw, "hunter2") {
			t.Errorf("%s: warnings %v lanes %s plans %d", body, out.Warnings, laneKeys(out), len(f.plans))
		}
	}
	// An empty listing: no lane, no warning.
	f = &auditFake{}
	e, id = auditHandlerEnv(t, f)
	out, _ := tracingPost(t, e, id, tracingBody(""))
	if laneKeys(out) != "sql,iis" || len(out.Warnings) != 0 || len(f.plans) != 0 {
		t.Errorf("empty listing: lanes %s warnings %v", laneKeys(out), out.Warnings)
	}
	// A nil repository is an empty listing.
	e, id = auditHandlerEnv(t, nil)
	out, _ = tracingPost(t, e, id, tracingBody(""))
	if laneKeys(out) != "sql,iis" || len(out.Warnings) != 0 {
		t.Errorf("nil repo: lanes %s warnings %v", laneKeys(out), out.Warnings)
	}
	// More than 50 listed sources: the first 50 are read, one warning.
	ids := make([]string, 52)
	for i := range ids {
		ids[i] = "s" + strconv.Itoa(i)
	}
	f = &auditFake{sources: auditSources(ids...)}
	e, id = auditHandlerEnv(t, f)
	out, _ = tracingPost(t, e, id, tracingBody(""))
	if len(f.plans) != 1 || len(f.plans[0].SourceIDs) != 50 || len(out.Warnings) != 1 || out.Warnings[0] != "Only the first 50 audit sources are shown." {
		t.Errorf("52 sources: plans %d warnings %v", len(f.plans), out.Warnings)
	}
}
