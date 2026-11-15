package store

import (
	"context"
	"crypto/tls"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func sqlBusyTestInput() audit.SQLBusyInput {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return audit.SQLBusyInput{Start: start, End: start.Add(time.Hour), BucketSeconds: 300}
}

// sqlBusyJSON renders a value as extended JSON so operator names can be asserted
// as text, the same way the activity tests inspect a pipeline.
func sqlBusyJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// sqlBusyDoc is one group document of the aggregation: a step start in Unix
// milliseconds, the summed database time and the call count.
func sqlBusyDoc(t *testing.T, slot time.Time, busy any, calls int64) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(bson.D{
		{Key: "_id", Value: slot.UnixMilli()},
		{Key: "busy", Value: busy},
		{Key: "calls", Value: calls},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bson.Raw(raw)
}

// sqlBusyStore wires a store whose fake server answers per collection. The
// counter records how many aggregations each collection ran.
func sqlBusyStore(t *testing.T, answer func(collection string) ([]any, error), collections ...string) (*Store, *fakeClient, map[string]int, *sync.Mutex) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	if len(collections) == 0 {
		collections = []string{"one"}
	}
	for _, c := range collections {
		addFakeSource(t, s, c)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	handle.aggregateFn = func(_ context.Context, _, collection string, _ mongo.Pipeline) (mongoCursor, error) {
		mu.Lock()
		calls[collection]++
		mu.Unlock()
		docs, err := answer(collection)
		if err != nil {
			return nil, err
		}
		return &fakeCursor{docs: docs}, nil
	}
	return s, handle, calls, &mu
}

func TestSQLBusyPipelineShape(t *testing.T) {
	in := sqlBusyTestInput()
	p := sqlBusyPipeline(in, "SqlMilliseconds", "$_auditAt")
	wantStages := []string{"$match", "$addFields", "$match", "$group", "$sort", "$limit"}
	if len(p) != len(wantStages) {
		t.Fatalf("stage count = %d, want %d", len(p), len(wantStages))
	}
	for i, stage := range p {
		if len(stage) != 1 || stage[0].Key != wantStages[i] {
			t.Fatalf("stage %d = %v, want %s", i, stage, wantStages[i])
		}
	}

	// The first $match carries the numeric guard on the stored field.
	first := p[0][0].Value.(bson.D)
	var guard any
	for _, e := range first {
		if e.Key == "SqlMilliseconds" {
			guard = e.Value
		}
	}
	guardDoc, ok := guard.(bson.D)
	if !ok || len(guardDoc) != 1 || guardDoc[0].Key != "$type" || guardDoc[0].Value != "number" {
		t.Fatalf("numeric guard = %v, want {SqlMilliseconds: {$type: \"number\"}}", guard)
	}

	// The group sums the stored number and counts calls; the step is epoch-aligned.
	group := p[3][0].Value.(bson.D)
	fields := map[string]any{}
	for _, e := range group {
		fields[e.Key] = e.Value
	}
	if sqlBusyJSON(t, fields["busy"]) != sqlBusyJSON(t, bson.D{{Key: "$sum", Value: "$SqlMilliseconds"}}) {
		t.Fatalf("busy accumulator = %v", fields["busy"])
	}
	if sqlBusyJSON(t, fields["calls"]) != sqlBusyJSON(t, bson.D{{Key: "$sum", Value: 1}}) {
		t.Fatalf("calls accumulator = %v", fields["calls"])
	}
	idJSON := sqlBusyJSON(t, fields["_id"])
	for _, want := range []string{"$subtract", "$mod", "$_auditAt", "1970-01-01T00:00:00Z"} {
		if !strings.Contains(idJSON, want) {
			t.Fatalf("step expression missing %s: %s", want, idJSON)
		}
	}

	// Sort ascending by step, then the one-extra-document cap.
	if sqlBusyJSON(t, p[4][0].Value) != sqlBusyJSON(t, bson.D{{Key: "_id", Value: 1}}) {
		t.Fatalf("sort = %v", p[4][0].Value)
	}
	if p[5][0].Value != 121 {
		t.Fatalf("$limit = %v, want 121", p[5][0].Value)
	}

	// No operator outside MongoDB 3.4 and no write stage anywhere in the pipeline.
	text := sqlBusyJSON(t, p)
	for _, banned := range []string{"$toDouble", "$convert", "$expr", "$dateTrunc", "$out", "$where", "$function", "InsertOne", "UpdateOne", "DeleteOne"} {
		if strings.Contains(text, banned) {
			t.Fatalf("pipeline uses banned operator %s", banned)
		}
	}
}

func TestSQLBusyPipelinePassesLegacyAdapterUnchanged(t *testing.T) {
	p := sqlBusyPipeline(sqlBusyTestInput(), "SqlMilliseconds", "$_auditAt")
	rewritten, err := rewriteLegacyPipeline(p)
	if err != nil {
		t.Fatal(err)
	}
	if isLegacyMetadataPipeline(rewritten) {
		t.Fatal("the adapter replaced the aggregation with a metadata stream")
	}
	if sqlBusyJSON(t, rewritten) != sqlBusyJSON(t, p) {
		t.Fatal("the 3.4-safe pipeline must pass through the legacy adapter unchanged")
	}
}

func TestSQLBusyUnwiredPathReturnsNotRecordedWithoutQuery(t *testing.T) {
	idx := -1
	for i := range recordPerfFields {
		if recordPerfFields[i].key == sqlBusyKey {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("the %s role is not in recordPerfFields", sqlBusyKey)
	}
	saved := recordPerfFields[idx].mongoPath
	recordPerfFields[idx].mongoPath = ""
	defer func() { recordPerfFields[idx].mongoPath = saved }()

	s, _, calls, mu := sqlBusyStore(t, func(string) ([]any, error) { return nil, nil })
	res, err := s.SQLBusy(context.Background(), sqlBusyTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if res.Recorded || len(res.Bins) != 0 {
		t.Fatalf("an unwired path must answer not recorded: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("an unwired path must run no query, got %v", calls)
	}
}

func TestSQLBusyInvalidInputFailsBeforeAnyQuery(t *testing.T) {
	s, _, calls, mu := sqlBusyStore(t, func(string) ([]any, error) { return nil, nil })
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	bad := []audit.SQLBusyInput{
		{Start: start, End: start, BucketSeconds: 300},
		{Start: start, End: start.Add(-time.Hour), BucketSeconds: 300},
		{Start: start, End: start.Add(time.Hour), BucketSeconds: 120},
		{Start: start, End: start.Add(32 * 24 * time.Hour), BucketSeconds: 900},
		{Start: start, End: start.Add(121 * time.Minute), BucketSeconds: 60},
	}
	for _, in := range bad {
		if _, err := s.SQLBusy(context.Background(), in); !errors.Is(err, audit.ErrInvalidInput) {
			t.Fatalf("input %+v: err = %v, want ErrInvalidInput", in, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("invalid input must run no query, got %v", calls)
	}
}

func TestSQLBusySumsAcrossSourcesAndDropsBadBins(t *testing.T) {
	in := sqlBusyTestInput()
	t0 := in.Start
	t1 := in.Start.Add(5 * time.Minute)
	t2 := in.Start.Add(10 * time.Minute)
	t3 := in.Start.Add(15 * time.Minute)
	s, _, calls, mu := sqlBusyStore(t, func(collection string) ([]any, error) {
		if collection == "two" {
			return []any{sqlBusyDoc(t, t0, 50.0, 3)}, nil
		}
		return []any{
			sqlBusyDoc(t, t0, 100.0, 2),
			sqlBusyDoc(t, t1, 200.0, 1),
			sqlBusyDoc(t, t2, -5.0, 4),        // negative: dropped
			sqlBusyDoc(t, t3, math.NaN(), 9),  // NaN: dropped
			sqlBusyDoc(t, t3, math.Inf(1), 9), // infinite: dropped
		}, nil
	}, "one", "two")

	res, err := s.SQLBusy(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded {
		t.Fatal("a wired path must answer recorded")
	}
	if len(res.Bins) != 2 {
		t.Fatalf("bins = %+v, want 2 (bad bins dropped)", res.Bins)
	}
	if !res.Bins[0].Start.Equal(t0) || res.Bins[0].BusyMs != 150 || res.Bins[0].Calls != 5 {
		t.Fatalf("step 0 = %+v, want busy 150 calls 5", res.Bins[0])
	}
	if !res.Bins[1].Start.Equal(t1) || res.Bins[1].BusyMs != 200 || res.Bins[1].Calls != 1 {
		t.Fatalf("step 1 = %+v, want busy 200 calls 1", res.Bins[1])
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["one"] != 1 || calls["two"] != 1 {
		t.Fatalf("each source must run one aggregation, got %v", calls)
	}
}

func TestSQLBusySourceErrorFailsTheCall(t *testing.T) {
	sentinel := errors.New("source read failed")
	s, _, _, _ := sqlBusyStore(t, func(collection string) ([]any, error) {
		if collection == "two" {
			return nil, sentinel
		}
		return []any{sqlBusyDoc(t, sqlBusyTestInput().Start, 10.0, 1)}, nil
	}, "one", "two")
	if _, err := s.SQLBusy(context.Background(), sqlBusyTestInput()); !errors.Is(err, sentinel) {
		t.Fatalf("a failed source must fail the call unchanged, err = %v", err)
	}
}

func TestSQLBusyTimeoutReturnsErrorUnchanged(t *testing.T) {
	s, _, _, _ := sqlBusyStore(t, func(string) ([]any, error) {
		return nil, context.DeadlineExceeded
	})
	if _, err := s.SQLBusy(context.Background(), sqlBusyTestInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a timeout must return unchanged, err = %v", err)
	}
}

func TestSQLBusyAdmissionDeniedFailsTheCall(t *testing.T) {
	s, _, calls, mu := sqlBusyStore(t, func(string) ([]any, error) { return nil, nil })
	ids, err := s.Sources(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatalf("sources = %v err %v", ids, err)
	}
	// Hold the source's only query slot so admission cannot take it.
	s.perSource[ids[0].ID] <- struct{}{}
	defer func() { <-s.perSource[ids[0].ID] }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.SQLBusy(ctx, sqlBusyTestInput()); !errors.Is(err, audit.ErrBusy) {
		t.Fatalf("a source that cannot be admitted must fail the call, err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("a denied admission must run no query, got %v", calls)
	}
}

func TestSQLBusyFailsClosedPastTheBinCap(t *testing.T) {
	in := sqlBusyTestInput()
	step := time.Duration(in.BucketSeconds) * time.Second
	docs := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = sqlBusyDoc(t, in.Start.Add(time.Duration(i)*step), 1.0, 1)
		}
		return out
	}
	s, _, _, _ := sqlBusyStore(t, func(string) ([]any, error) { return docs(sqlBusyMaxBins + 1), nil })
	if _, err := s.SQLBusy(context.Background(), in); !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("more than %d bins must fail closed, err = %v", sqlBusyMaxBins, err)
	}
	s2, _, _, _ := sqlBusyStore(t, func(string) ([]any, error) { return docs(sqlBusyMaxBins), nil })
	res, err := s2.SQLBusy(context.Background(), in)
	if err != nil || len(res.Bins) != sqlBusyMaxBins {
		t.Fatalf("exactly %d bins must pass: bins %d err %v", sqlBusyMaxBins, len(res.Bins), err)
	}
}

func TestSQLBusyNoSourcesAnswersRecordedWithoutQuery(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		return &fakeClient{}, nil
	})
	res, err := s.SQLBusy(context.Background(), sqlBusyTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded || len(res.Bins) != 0 {
		t.Fatalf("no sources: %+v, want recorded with no bins", res)
	}
}

// TestSQLBusyFiltersBySelectedSourceIDs is the DATA-02 proof. Two synthetic audit
// sources hold records that OVERLAP in time (both have a step at t0), so an
// unfiltered read sums them. Selecting one source must return only that source's
// busy time and must not even read the other; selecting both sums them; an empty
// selection keeps the historical read-every-connected-source behaviour.
func TestSQLBusyFiltersBySelectedSourceIDs(t *testing.T) {
	base := sqlBusyTestInput()
	t0 := base.Start
	s, _, calls, mu := sqlBusyStore(t, func(collection string) ([]any, error) {
		switch collection {
		case "src-a":
			return []any{sqlBusyDoc(t, t0, 100.0, 2)}, nil
		case "src-b":
			return []any{sqlBusyDoc(t, t0, 50.0, 3)}, nil
		}
		return nil, nil
	}, "src-a", "src-b")

	srcs, err := s.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	idOf := make(map[string]string, len(srcs))
	for _, src := range srcs {
		idOf[src.Label] = src.ID
	}
	idA, idB := idOf["src-a"], idOf["src-b"]
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("two distinct synthetic sources required: %+v", srcs)
	}

	for _, tc := range []struct {
		name      string
		ids       []string
		wantBusy  float64
		wantCalls int64
		wantRead  map[string]int
	}{
		{"one selected counts only that source", []string{idA}, 100, 2, map[string]int{"src-a": 1}},
		{"the other selected counts only it", []string{idB}, 50, 3, map[string]int{"src-b": 1}},
		{"both selected sum", []string{idA, idB}, 150, 5, map[string]int{"src-a": 1, "src-b": 1}},
		{"empty selection reads every connected source", nil, 150, 5, map[string]int{"src-a": 1, "src-b": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			for k := range calls {
				delete(calls, k)
			}
			mu.Unlock()
			in := base
			in.SourceIDs = tc.ids
			res, err := s.SQLBusy(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Recorded || len(res.Bins) != 1 {
				t.Fatalf("recorded %v bins %+v, want one overlapping step", res.Recorded, res.Bins)
			}
			b := res.Bins[0]
			if !b.Start.Equal(t0) || b.BusyMs != tc.wantBusy || b.Calls != tc.wantCalls {
				t.Fatalf("step = %+v, want busy %v calls %v (not the sum of both sources)", b, tc.wantBusy, tc.wantCalls)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(calls, tc.wantRead) {
				t.Fatalf("aggregations run = %v, want %v (an unselected source must not be read)", calls, tc.wantRead)
			}
		})
	}
}

// TestSQLBusyUnknownSelectedSourceFailsClosed: a selected id that is not connected
// fails the whole call (as Rollup does), rather than silently dropping it and
// presenting an undercounted sum as complete.
func TestSQLBusyUnknownSelectedSourceFailsClosed(t *testing.T) {
	s, _, calls, mu := sqlBusyStore(t, func(string) ([]any, error) {
		return []any{sqlBusyDoc(t, sqlBusyTestInput().Start, 10.0, 1)}, nil
	})
	in := sqlBusyTestInput()
	in.SourceIDs = []string{"src-missing"}
	if _, err := s.SQLBusy(context.Background(), in); !errors.Is(err, errSourceNotFound) {
		t.Fatalf("an unknown selected source must fail closed, err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("an unknown selected source must run no query, got %v", calls)
	}
}
