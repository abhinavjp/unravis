package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"crypto/tls"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActivityQueriesAllSourcesOnceWithOverlapAndNoPayload(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	one := addFakeSource(t, s, "one")
	two := addFakeSource(t, s, "two")
	calls := 0
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		calls++
		raw, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: p}}, false, false)
		text := string(raw)
		for _, want := range []string{"RequestCompletedAt", "$gt", "$expr", "$limit"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing overlap constraint %s", want)
			}
		}
		for _, bad := range []string{"RequestPayload", "ResponsePayload"} {
			if strings.Contains(text, bad) {
				t.Fatal("payload selected")
			}
		}
		return &fakeCursor{}, nil
	}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p, err := s.Activity(context.Background(), audit.ActivityInput{SourceIDs: []string{one.ID, two.ID}, Start: at, End: at.Add(time.Minute), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(p.Sources) != 2 {
		t.Fatalf("calls=%d sources=%d", calls, len(p.Sources))
	}
}

func TestLegacyActivityIncludesEarlierKnownSpan(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "one")
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 100})
	var docs []any
	for i, started := range []time.Time{at.Add(-time.Hour), at.Add(time.Second)} {
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "_auditMetadataOversized", Value: false}, {Key: "ControllerName", Value: "Payroll"}, {Key: "ActionName", Value: "Run"}, {Key: "RequestInitiatedAt", Value: started}, {Key: "RequestCompletedAt", Value: at.Add(time.Duration(i+2) * time.Second)}})
		docs = append(docs, bson.Raw(raw))
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	var records, bytes atomic.Int64
	got, err := readLegacyRequestRowsMode(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 100, &records, &bytes, true)
	if err != nil || len(got) != 2 {
		t.Fatalf("overlaps=%d err=%v", len(got), err)
	}
	records.Store(0)
	bytes.Store(0)
	normal, err := readLegacyRequestRows(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 100, &records, &bytes)
	if err != nil || len(normal) != 1 {
		t.Fatalf("ordinary request semantics changed count=%d err=%v", len(normal), err)
	}
}

func TestActivityPipelinePreservesLegacyMetadataAdapterShape(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	raw := legacyMetadataPipeline(activityPipeline(q, "s", nil, 101))
	if len(raw) == 0 || !isLegacyMetadataPipeline(raw) {
		t.Fatal("activity bypassed or broke legacy bounded metadata projection")
	}
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: raw}}, false, false)
	if !strings.Contains(string(encoded), "RequestCompletedAt") || strings.Contains(string(encoded), "RequestPayload") {
		t.Fatal("legacy overlap match or projection incorrect")
	}
}

// activityJSON is a pipeline as text, to compare stages byte for byte.
func activityJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func activityStageKeys(t *testing.T, stage bson.D) []string {
	t.Helper()
	if len(stage) != 1 || stage[0].Key != "$match" {
		t.Fatalf("stage is not one $match: %v", stage)
	}
	doc, ok := stage[0].Value.(bson.D)
	if !ok {
		t.Fatalf("$match is %T, want bson.D", stage[0].Value)
	}
	keys := make([]string, 0, len(doc))
	for _, e := range doc {
		keys = append(keys, e.Key)
	}
	return keys
}

func TestActivityPipelineFilterStages(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := queryForTest(at)
	plain := activityPipeline(base, "s", nil, 101)
	// Today's shape: the request pipeline with the overlap $or in place of its
	// first $match and the window $match after its first $set.
	if want := len(requestPipeline(base, [5]string{"", "", "", "all", "all"}, "s", nil, 101)) + 1; len(plain) != want {
		t.Fatalf("no filter: %d stages, want %d", len(plain), want)
	}
	withUser, withOp, withBoth := base, base, base
	withUser.UserIDs = []int{42}
	withOp.Operation = "Payroll.Run"
	withBoth.UserIDs, withBoth.Operation = []int{42}, "Payroll.Run"

	for name, tc := range map[string]struct {
		q    audit.Query
		keys []string
	}{
		"user only":      {withUser, []string{"UserId"}},
		"operation only": {withOp, []string{"ControllerName", "ActionName"}},
		"both":           {withBoth, []string{"UserId", "ControllerName", "ActionName"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := activityPipeline(tc.q, "s", nil, 101)
			if len(got) != len(plain)+1 {
				t.Fatalf("%d stages, want %d", len(got), len(plain)+1)
			}
			// The new stage is exactly one after the overlap stage; every other
			// stage is the unfiltered one.
			if activityJSON(t, got[0]) != activityJSON(t, plain[0]) {
				t.Fatal("the first $match changed")
			}
			keys := activityStageKeys(t, got[1])
			if strings.Join(keys, ",") != strings.Join(tc.keys, ",") {
				t.Fatalf("filter keys %v, want %v", keys, tc.keys)
			}
			if activityJSON(t, got[2:]) != activityJSON(t, plain[1:]) {
				t.Fatal("stages after the filter differ from the unfiltered pipeline")
			}
			doc := got[1][0].Value.(bson.D)
			for _, e := range doc {
				switch e.Key {
				case "UserId":
					in, ok := e.Value.(bson.D)
					if !ok || len(in) != 1 || in[0].Key != "$in" {
						t.Fatalf("UserId condition is %v", e.Value)
					}
					list, ok := in[0].Value.(bson.A)
					if !ok || len(list) != 2 {
						t.Fatalf("UserId list is %v", in[0].Value)
					}
					if n, ok := list[0].(int); !ok || n != 42 {
						t.Fatalf("UserId number is %T %v", list[0], list[0])
					}
					if s, ok := list[1].(string); !ok || s != "42" {
						t.Fatalf("UserId text is %T %v", list[1], list[1])
					}
				case "ControllerName":
					if v, ok := e.Value.(string); !ok || v != "Payroll" {
						t.Fatalf("ControllerName is %T %v", e.Value, e.Value)
					}
				case "ActionName":
					if v, ok := e.Value.(string); !ok || v != "Run" {
						t.Fatalf("ActionName is %T %v", e.Value, e.Value)
					}
				}
			}
			text := activityJSON(t, got[1])
			for _, bad := range []string{"$expr", "$where", "$regex", "$function"} {
				if strings.Contains(text, bad) {
					t.Fatalf("filter stage uses %s", bad)
				}
			}
		})
	}
}

func TestActivityFilterStaysOutsideOverlapOr(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	plain := queryForTest(at)
	filtered := plain
	filtered.UserIDs, filtered.Operation = []int{42}, "Payroll.Run"
	want := activityJSON(t, activityPipeline(plain, "s", nil, 101)[0])
	got := activityPipeline(filtered, "s", nil, 101)
	if activityJSON(t, got[0]) != want {
		t.Fatal("the first $match changed when a filter was set")
	}
	first := got[0][0].Value.(bson.D)
	if len(first) != 1 || first[0].Key != "$or" {
		t.Fatalf("first $match is not the lone $or: %v", first)
	}
	for _, name := range []string{"UserId", "ControllerName", "ActionName"} {
		if strings.Contains(want, name) {
			t.Fatalf("first $match holds %s", name)
		}
	}
}

func TestActivityFilterBadOperationMatchesNothing(t *testing.T) {
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	q.Operation = "NoDot"
	got := activityPipeline(q, "s", nil, 101)
	text := activityJSON(t, got[1])
	if !strings.Contains(text, `"_id"`) || !strings.Contains(text, `"$in":[]`) {
		t.Fatalf("an operation that does not split must match nothing, got %s", text)
	}
}

func TestActivityAppliesUserAndOperationFilters(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "one")
	var seen mongo.Pipeline
	calls := 0
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		calls++
		seen = p
		return &fakeCursor{}, nil
	}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	user := 42
	input := audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 100, UserID: &user, Operation: "Payroll.Run"}
	if _, err := s.Activity(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(seen) < 2 {
		t.Fatalf("calls=%d stages=%d", calls, len(seen))
	}
	if keys := activityStageKeys(t, seen[1]); strings.Join(keys, ",") != "UserId,ControllerName,ActionName" {
		t.Fatalf("filter stage keys %v", keys)
	}
	if strings.Contains(activityJSON(t, seen[0]), "ControllerName") {
		t.Fatal("filter leaked into the first $match")
	}
	// No filter: no extra stage, and the cursor of one query does not fit the other.
	calls = 0
	input.UserID, input.Operation = nil, ""
	if _, err := s.Activity(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(activityJSON(t, seen[1]), "UserId") || strings.Contains(activityJSON(t, seen[1]), "ControllerName") {
		t.Fatal("an unfiltered read got a filter stage")
	}
}

func TestActivityRefusesBadOperationBeforeAnyQuery(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "one")
	calls := 0
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		calls++
		return &fakeCursor{}, nil
	}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, op := range []string{"NoDot", ".Run", "Payroll.", "A.B.C", "a b.c"} {
		_, err := s.Activity(context.Background(), audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 100, Operation: op})
		if !errors.Is(err, audit.ErrInvalidInput) {
			t.Fatalf("operation %q: err=%v", op, err)
		}
		if strings.Contains(err.Error(), op) {
			t.Fatalf("the error echoes the operation: %v", err)
		}
	}
	if calls != 0 {
		t.Fatalf("%d queries ran for a bad operation", calls)
	}
}
