package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDemoStatsCountsAndFindsNewest(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	repo := NewDemo(now)
	statter, ok := repo.(SourceStatter)
	if !ok {
		t.Fatal("demo repository is not a SourceStatter")
	}
	got, err := statter.Stats(context.Background(), StatsInput{SourceID: "demo-west"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Docs <= 0 || got.NewestAt == nil || got.NewestAt.After(now.Add(time.Hour)) {
		t.Fatalf("stats = %+v", got)
	}
	if _, err := statter.Stats(context.Background(), StatsInput{SourceID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown source error = %v", err)
	}
}
