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

func TestSlowestReadsEverySourceOnceAndRanksGlobally(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	a := addFakeSource(t, s, "one")
	b := addFakeSource(t, s, "two")
	calls := 0
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.aggregateFn = func(_ context.Context, db, _ string, p mongo.Pipeline) (mongoCursor, error) {
		calls++
		raw, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: p}}, false, false)
		text := string(raw)
		if !strings.Contains(text, `"durationMs":-1`) || strings.Contains(text, "RequestPayload") {
			t.Fatal(text)
		}
		duration := 100.
		if db == b.Database {
			duration = 900
		}
		return &fakeCursor{docs: []any{requestDocument{ID: bson.NewObjectID(), InitiatedAt: &at, DurationMs: &duration}}}, nil
	}
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{a.ID, b.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	p, e := s.Slowest(context.Background(), audit.PageInput{Query: q, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if calls != 2 || len(p.Rows) != 1 || *p.Rows[0].DurationMs != 900 || !p.Truncated || p.NextCursor != "" {
		t.Fatalf("calls=%d page=%+v", calls, p)
	}
}
func TestLegacySlowestDoesNotReturnNewestInstead(t *testing.T) {
	var h *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { h = &fakeClient{}; return h, nil })
	src := addFakeSource(t, s, "one")
	p, release, _ := s.leaseSource(src.ID)
	defer release()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{src.ID}, Start: at, End: at.Add(time.Minute), Limit: 1})
	docs := []any{}
	for i, ms := range []int{9000, 100} {
		started := at.Add(time.Duration(i) * time.Second)
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "_auditMetadataOversized", Value: false}, {Key: "RequestInitiatedAt", Value: started}, {Key: "RequestCompletedAt", Value: started.Add(time.Duration(ms) * time.Millisecond)}, {Key: "ApiTime", Value: float64(10000 - ms)}})
		docs = append(docs, bson.Raw(raw))
	}
	h.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	var count, bytes atomic.Int64
	rows, e := readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 1, &count, &bytes, false, true)
	if e != nil || len(rows) != 1 || *rows[0].request.DurationMs != 9000 {
		t.Fatalf("wrong ranking %v %v", rows, e)
	}
	count.Store(0)
	bytes.Store(0)
	q.Metric = "api"
	rows, e = readLegacyRequestRowsSorted(context.Background(), p, q, [5]string{"", "", "", "all", "all"}, nil, nil, 1, &count, &bytes, false, true)
	if e != nil || *rows[0].request.ApiTimeMs != 9900 {
		t.Fatal("API metric ranking mismatch", e)
	}
}
