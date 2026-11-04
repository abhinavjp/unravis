package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"api-audit-explorer/internal/audit"
)

func TestStatsReadsEstimatedCountAndNewestFromObjectID(t *testing.T) {
	s, src, handle := coverageStore(t, false)
	when := time.Date(2026, 10, 20, 8, 30, 0, 0, time.UTC)
	handle.aggregateFn = func(_ context.Context, db, coll string, p mongo.Pipeline) (mongoCursor, error) {
		if db != "db" || coll != "audit" {
			t.Fatalf("queried %s.%s", db, coll)
		}
		if isCollStats(p) {
			return &coverageCursor{docs: []bson.D{{{Key: "count", Value: int64(218000)}}}}, nil
		}
		for _, stage := range p {
			if stage[0].Key == "$match" || stage[0].Key == "$group" {
				t.Fatalf("stats must not scan: %v", p)
			}
		}
		return &coverageCursor{docs: []bson.D{{{Key: "_id", Value: bson.NewObjectIDFromTimestamp(when)}}}}, nil
	}
	got, err := s.Stats(context.Background(), audit.StatsInput{SourceID: src.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Docs != 218000 || got.NewestAt == nil || !got.NewestAt.Equal(when) || got.SourceID != src.ID {
		t.Fatalf("stats = %#v", got)
	}
}

func TestStatsFailsSoftPerHalf(t *testing.T) {
	s, src, handle := coverageStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("boom secret-host:27017")
	}
	got, err := s.Stats(context.Background(), audit.StatsInput{SourceID: src.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Docs != -1 || got.NewestAt != nil {
		t.Fatalf("stats = %#v", got)
	}
}

func TestStatsEmptyCollectionAndLegacySkipsCount(t *testing.T) {
	s, src, handle := coverageStore(t, true)
	counted := false
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		if isCollStats(p) {
			counted = true
		}
		return &coverageCursor{}, nil
	}
	got, err := s.Stats(context.Background(), audit.StatsInput{SourceID: src.ID})
	if err != nil {
		t.Fatal(err)
	}
	if counted || got.Docs != -1 || got.NewestAt != nil {
		t.Fatalf("counted=%v stats=%#v", counted, got)
	}
}

func TestStatsUnknownSource(t *testing.T) {
	s, _, _ := coverageStore(t, false)
	if _, err := s.Stats(context.Background(), audit.StatsInput{SourceID: "nope"}); err == nil {
		t.Fatal("want an error for an unknown source")
	}
}
