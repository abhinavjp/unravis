package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"crypto/tls"
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
