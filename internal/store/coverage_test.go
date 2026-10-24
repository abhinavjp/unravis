package store

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// coverageCursor serves bson documents; Decode round-trips them through bson so
// the production decode target is exercised.
type coverageCursor struct {
	docs    []bson.D
	idx     int
	current bson.D
	err     error
}

func (c *coverageCursor) Next(context.Context) bool {
	if c.idx >= len(c.docs) {
		return false
	}
	c.current = c.docs[c.idx]
	c.idx++
	return true
}
func (c *coverageCursor) Decode(dst any) error {
	raw, err := bson.Marshal(c.current)
	if err != nil {
		return err
	}
	return bson.Unmarshal(raw, dst)
}
func (c *coverageCursor) Err() error                  { return c.err }
func (c *coverageCursor) Close(context.Context) error { return nil }

func coverageGroup(kind, day string, docs, ria, created, api, account int64) bson.D {
	return bson.D{
		{Key: "_id", Value: bson.D{{Key: "kind", Value: kind}, {Key: "day", Value: day}}},
		{Key: "docs", Value: docs}, {Key: "requestInitiatedAt", Value: ria}, {Key: "createdOn", Value: created},
		{Key: "apiTime", Value: api}, {Key: "accountId", Value: account},
	}
}

func coverageStore(t *testing.T, legacy bool, opts ...Option) (*Store, audit.Source, *fakeClient) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		if legacy {
			return legacyFlagClient{handle}, nil
		}
		return handle, nil
	})
	for _, opt := range opts {
		opt(s)
	}
	return s, addFakeSource(t, s, "audit"), handle
}

func coverageInput(id string) audit.CoverageInput {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return audit.CoverageInput{SourceID: id, Start: start, End: start.Add(48 * time.Hour)}
}

func isCollStats(p mongo.Pipeline) bool {
	return len(p) == 1 && len(p[0]) == 1 && p[0][0].Key == "$collStats"
}

func TestCoverageRefusedByDefaultWithoutQuery(t *testing.T) {
	s, src, handle := coverageStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		t.Fatal("a refused census must not query the source")
		return nil, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); !errors.Is(err, audit.ErrBroadScanRefused) {
		t.Fatalf("err = %v, want ErrBroadScanRefused", err)
	}
	// The gate comes before the lease: an unknown source is refused, not "not found".
	if _, err := s.Coverage(context.Background(), coverageInput("missing")); !errors.Is(err, audit.ErrBroadScanRefused) {
		t.Fatalf("unknown source err = %v, want ErrBroadScanRefused", err)
	}
	// Input validation still comes first.
	bad := coverageInput(src.ID)
	bad.End = bad.Start
	if _, err := s.Coverage(context.Background(), bad); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("invalid input err = %v, want ErrInvalidInput", err)
	}
}

func TestCoverageRunsWhenAllowedAndUsesCensusBudget(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	calls := 0
	handle.aggregateFn = func(ctx context.Context, db, coll string, p mongo.Pipeline) (mongoCursor, error) {
		calls++
		if got := queryBudget(ctx); got != 60*time.Second {
			t.Fatalf("budget = %v, want 60s", got)
		}
		if db != "db" || coll != "audit" {
			t.Fatalf("queried %s.%s", db, coll)
		}
		return &coverageCursor{docs: []bson.D{coverageGroup("synchronous", "2026-10-01", 10, 10, 10, 10, 8)}}, nil
	}
	report, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("aggregate calls = %d, want 1", calls)
	}
	if len(report.Provenance) != 1 {
		t.Fatalf("provenance = %#v", report.Provenance)
	}
	p := report.Provenance[0]
	if p.Window != "coverage" || p.ScanClass != audit.ScanBroad || p.DocsExamined != nil || p.Completeness != audit.RanToEnd ||
		p.SourceID != src.ID || p.MaxTimeMs != 60000 || p.DocsReturned != 1 || p.PartialReason != "" {
		t.Fatalf("provenance = %#v", p)
	}
	if report.SourceID != src.ID || report.TenantAttribution != "available" {
		t.Fatalf("report = %#v", report)
	}
}

func TestCoverageTimeoutIsPartial(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, context.DeadlineExceeded
	}
	report, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if err != nil {
		t.Fatal(err)
	}
	p := report.Provenance[0]
	if p.Completeness != audit.Partial || p.PartialReason != "timeout" || p.DocsExamined != nil {
		t.Fatalf("provenance = %#v", p)
	}
	if !strings.HasPrefix(report.TenantAttribution, "tenant attribution unavailable") {
		t.Fatalf("a partial census must not claim attribution: %q", report.TenantAttribution)
	}
}

func TestCoverageOtherErrorIsSanitized(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("mongodb://reader:secret@host:27017/db: boom")
	}
	_, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "mongodb://") {
		t.Fatalf("err = %v", err)
	}
}

func TestCoveragePipelineNeverProjectsValues(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	pipeline := coveragePipeline(start, start.Add(24*time.Hour))
	groups := 0
	for _, stage := range pipeline {
		switch stage[0].Key {
		case "$match", "$sort", "$limit":
		case "$group":
			groups++
			doc := stage[0].Value.(bson.D)
			for _, e := range doc {
				if e.Key == "_id" {
					continue
				}
				acc, ok := e.Value.(bson.D)
				if !ok || len(acc) != 1 || acc[0].Key != "$sum" {
					t.Fatalf("accumulator %s = %v, want only $sum", e.Key, e.Value)
				}
			}
		default:
			t.Fatalf("stage %s must not appear in a census (no $project/$addFields of raw fields)", stage[0].Key)
		}
	}
	if groups != 1 {
		t.Fatalf("groups = %d, want 1", groups)
	}
	// No raw value is ever a group key other than kind and the day string.
	group := pipeline[1][0].Value.(bson.D)
	id := group[0].Value.(bson.D)
	if group[0].Key != "_id" || len(id) != 2 || id[0].Key != "kind" || id[1].Key != "day" {
		t.Fatalf("group key = %v", group[0])
	}
	for _, field := range []string{"RequestInitiatedAt", "CreatedOn", "ApiTime", "AccountId"} {
		want := bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$gt", Value: bson.A{"$" + field, nil}}}, 1, 0}}}}}
		found := false
		for _, e := range group {
			if reflect.DeepEqual(e.Value, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no non-null counter for %s", field)
		}
	}
}

func TestCoveragePipelineMatchEqualsSummaryMatch(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(72 * time.Hour)
	want := buildSummaryPipeline(audit.Query{}, start, end, false)[0]
	got := coveragePipeline(start, end)[0]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first stage differs:\n got %v\nwant %v", got, want)
	}
	if got[0].Key != "$match" || len(got[0].Value.(bson.D)) != 1 || got[0].Value.(bson.D)[0].Key != "$or" {
		t.Fatalf("first stage is not the bare time $or: %v", got)
	}
}

func TestCoverageKindSwitchAndDayMatchSummaryPipeline(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	wantKind, wantAt := summarySetField(t, start, "_auditKind"), summarySetField(t, start, "_auditAt")
	group := coveragePipeline(start, start.Add(time.Hour))[1][0].Value.(bson.D)
	id := group[0].Value.(bson.D)
	if !reflect.DeepEqual(id[0].Value, wantKind) {
		t.Fatalf("kind switch differs from pipeline.go:\n got %v\nwant %v", id[0].Value, wantKind)
	}
	wantDay := bson.D{{Key: "$dateToString", Value: bson.D{{Key: "format", Value: "%Y-%m-%d"}, {Key: "date", Value: wantAt}, {Key: "onNull", Value: ""}}}}
	if !reflect.DeepEqual(id[1].Value, wantDay) {
		t.Fatalf("day expression differs:\n got %v\nwant %v", id[1].Value, wantDay)
	}
}

// summarySetField extracts a $set field expression from the summary pipeline.
func summarySetField(t *testing.T, start time.Time, field string) any {
	t.Helper()
	q := audit.Query{Metric: "total", Kind: "all", Outcome: "all", BucketSeconds: 10}.Normalized()
	for _, stage := range buildSummaryPipeline(q, start, start.Add(time.Hour), false) {
		if stage[0].Key != "$set" {
			continue
		}
		for _, e := range stage[0].Value.(bson.D) {
			if e.Key == field {
				return e.Value
			}
		}
	}
	t.Fatalf("summary pipeline has no %s", field)
	return nil
}

func TestCoverageDocsExaminedBudgetRefuses(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true), WithDocsExaminedBudget(10))
	censusRuns := 0
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		if isCollStats(p) {
			stats := p[0][0].Value.(bson.D)
			if !reflect.DeepEqual(stats, bson.D{{Key: "count", Value: bson.D{}}}) {
				t.Fatalf("$collStats = %v", stats)
			}
			return &coverageCursor{docs: []bson.D{{{Key: "count", Value: int64(11)}}}}, nil
		}
		censusRuns++
		return &coverageCursor{}, nil
	}
	_, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	if censusRuns != 0 {
		t.Fatal("census aggregate ran after the budget refusal")
	}
	// At the budget the census runs.
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		if isCollStats(p) {
			return &coverageCursor{docs: []bson.D{{{Key: "count", Value: int64(10)}}}}, nil
		}
		censusRuns++
		return &coverageCursor{}, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); err != nil || censusRuns != 1 {
		t.Fatalf("err = %v runs = %d", err, censusRuns)
	}
}

func TestCoverageBudgetUnsetSkipsCollStats(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		if isCollStats(p) {
			t.Fatal("$collStats ran without a docs-examined budget")
		}
		return &coverageCursor{}, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageRowCapAndPercentages(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &coverageCursor{docs: []bson.D{
			coverageGroup("synchronous", "2026-10-01", 3, 3, 3, 2, 1),
			coverageGroup("asynchronous", "2026-10-01", 3, 3, 2, 3, 2),
			coverageGroup("hangfire", "2026-10-02", 6, 6, 6, 6, 6),
		}}, nil
	}
	report, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 3 {
		t.Fatalf("rows = %d", len(report.Rows))
	}
	first := report.Rows[0]
	if first.Kind != "synchronous" || first.Day != "2026-10-01" || first.Docs != 3 || first.ApiTime.NonNull != 2 || first.ApiTime.Pct != 66.7 || first.AccountID.Pct != 33.3 {
		t.Fatalf("first row = %#v", first)
	}
	total := report.Total
	if total.Kind != "all" || total.Day != "" || total.Docs != 12 || total.AccountID.NonNull != 9 || total.AccountID.Pct != 75 ||
		total.ApiTime.NonNull != 11 || total.CreatedOn.NonNull != 11 || total.RequestInitiatedAt.Pct != 100 {
		t.Fatalf("total = %#v", total)
	}
	if want := "tenant attribution unavailable (75.0 % populated)"; report.TenantAttribution != want {
		t.Fatalf("tenant attribution = %q, want %q", report.TenantAttribution, want)
	}
	if report.Provenance[0].SampleSize != 12 || report.Provenance[0].DocsReturned != 3 {
		t.Fatalf("provenance = %#v", report.Provenance[0])
	}

	docs := make([]bson.D, audit.MaxCoverageRows+1)
	for i := range docs {
		docs[i] = coverageGroup("synchronous", fmt.Sprintf("d%04d", i), 1, 1, 1, 1, 1)
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &coverageCursor{docs: docs}, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("1001 groups err = %v, want ErrLimit", err)
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &coverageCursor{docs: docs[:audit.MaxCoverageRows]}, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); err != nil {
		t.Fatalf("1000 groups err = %v", err)
	}
}

func TestCoverageLegacyRefused(t *testing.T) {
	s, src, handle := coverageStore(t, true, WithBroadScans(true))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		t.Fatal("legacy source must not be queried")
		return nil, nil
	}
	_, err := s.Coverage(context.Background(), coverageInput(src.ID))
	if !errors.Is(err, audit.ErrInvalidInput) || !strings.Contains(err.Error(), "coverage census is not available for MongoDB 3.4 sources") {
		t.Fatalf("err = %v", err)
	}
}

func TestCoverageSemaphoreReleased(t *testing.T) {
	s, src, handle := coverageStore(t, false, WithBroadScans(true))
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &coverageCursor{}, nil
	}
	if _, err := s.Coverage(context.Background(), coverageInput(src.ID)); err != nil {
		t.Fatal(err)
	}
	if len(s.global) != 0 || len(s.perSource[src.ID]) != 0 {
		t.Fatalf("admission slots leaked: global=%d per=%d", len(s.global), len(s.perSource[src.ID]))
	}
}
