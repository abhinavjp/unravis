package store

import (
	"context"
	"crypto/tls"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestAnalyzeExecutesCurrentAndReferenceOncePerSelectedSource(t *testing.T) {
	var client *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		if client == nil {
			client = &fakeClient{}
		}
		return client, nil
	})

	const sourceCount = 5
	q := queryForTest(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	q.SourceIDs = make([]string, 0, sourceCount)
	collections := make(map[string]struct{}, sourceCount)
	for i := 0; i < sourceCount; i++ {
		collection := "audit_" + string(rune('a'+i))
		source := addFakeSource(t, s, collection)
		q.SourceIDs = append(q.SourceIDs, source.ID)
		collections[collection] = struct{}{}
	}

	var mu sync.Mutex
	calls := make(map[string]int, sourceCount)
	referenceCalls := make(map[string]int, sourceCount)
	client.aggregateFn = func(_ context.Context, _, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[collection]++
		for _, stage := range pipeline {
			if stage[0].Key != "$set" {
				continue
			}
			for _, field := range stage[0].Value.(bson.D) {
				if field.Key == "_auditBucketAt" {
					if expression, ok := field.Value.(bson.D); ok && expression[0].Key == "$literal" {
						referenceCalls[collection]++
					}
				}
			}
		}
		return &fakeCursor{}, nil
	}

	if _, err := s.Analyze(context.Background(), q); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := len(calls), sourceCount; got != want {
		t.Fatalf("aggregated collections = %d, want %d; calls=%v", got, want, calls)
	}
	total := 0
	for collection := range collections {
		if referenceCalls[collection] != 1 {
			t.Errorf("compacted reference calls for %s = %d, want 1", collection, referenceCalls[collection])
		}
		if got := calls[collection]; got != 2 {
			t.Errorf("Aggregate calls for %s = %d, want exactly one current and one reference call", collection, got)
		}
		total += calls[collection]
	}
	if want := sourceCount * 2; total != want {
		t.Errorf("total Aggregate calls = %d, want %d", total, want)
	}
}
