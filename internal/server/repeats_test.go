package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

func TestIdenticalAnalysesCountAsOneRepeat(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	c.observe(testQuery())
	c.observe(testQuery())
	if c.repeats != 1 || c.total != 2 {
		t.Fatalf("repeats=%d total=%d, want 1 of 2", c.repeats, c.total)
	}
}

func TestRepeatCountingIgnoresSourceOrderAndOperationSpelling(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	first := testQuery()
	first.SourceIDs = []string{"a", "b"}
	first.Controller, first.Action = "Orders", "Create"
	second := first
	second.SourceIDs = []string{"b", "a"}
	second.Controller, second.Action = "", ""
	second.Operation = "Orders.Create"
	c.observe(first)
	c.observe(second)
	if c.repeats != 1 {
		t.Fatalf("repeats=%d, want 1", c.repeats)
	}
}

func TestDifferentWindowsAreNotRepeats(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	c.observe(testQuery())
	moved := testQuery()
	moved.Start = moved.Start.Add(time.Minute)
	moved.End = moved.End.Add(time.Minute)
	c.observe(moved)
	if c.repeats != 0 || c.total != 2 {
		t.Fatalf("repeats=%d total=%d, want 0 of 2", c.repeats, c.total)
	}
}

// Age-range queries measure against the server clock, so they are never counted,
// not even when they repeat, and they do not change what later queries match.
func TestAgeRangeQueriesAreNeverCounted(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	aged := testQuery()
	minimum := 1000.0
	aged.IncompleteAgeMs = &audit.Range{Min: &minimum}
	c.observe(aged)
	c.observe(aged)
	if c.repeats != 0 || c.total != 0 {
		t.Fatalf("age-range repeats=%d total=%d, want 0 of 0", c.repeats, c.total)
	}
	c.observe(testQuery())
	c.observe(aged)
	c.observe(testQuery())
	if c.repeats != 1 || c.total != 2 {
		t.Fatalf("after mixing repeats=%d total=%d, want 1 of 2", c.repeats, c.total)
	}
}

func TestRepeatExpiresAfterTenMinutes(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	c.observe(testQuery())
	clock = clock.Add(repeatWindow - time.Second)
	c.observe(testQuery())
	if c.repeats != 1 {
		t.Fatalf("inside the window repeats=%d, want 1", c.repeats)
	}
	clock = clock.Add(repeatWindow)
	c.observe(testQuery())
	if c.repeats != 1 || c.total != 3 {
		t.Fatalf("after the window repeats=%d total=%d, want 1 of 3", c.repeats, c.total)
	}
}

func TestOnlyTheLastEightKeysAreRemembered(t *testing.T) {
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	c := &analyzeRepeats{now: func() time.Time { return clock }}
	query := func(i int) audit.Query {
		q := testQuery()
		q.Start = q.Start.Add(time.Duration(i) * time.Minute)
		q.End = q.Start.Add(time.Minute)
		return q
	}
	for i := 0; i < repeatMemory+1; i++ {
		c.observe(query(i))
	}
	if len(c.recent) != repeatMemory {
		t.Fatalf("remembered %d keys, want %d", len(c.recent), repeatMemory)
	}
	c.observe(query(repeatMemory)) // the newest key is still remembered
	if c.repeats != 1 {
		t.Fatalf("newest key repeats=%d, want 1", c.repeats)
	}
	c.observe(query(0)) // the oldest key was evicted
	if c.repeats != 1 {
		t.Fatalf("evicted key counted as a repeat: repeats=%d", c.repeats)
	}
}

// The summary is the only output and carries counts, never source names.
func TestShutdownLogIsContentFree(t *testing.T) {
	var logs bytes.Buffer
	prevOutput, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() { log.SetOutput(prevOutput); log.SetFlags(prevFlags) }()

	h := New(&fakeRepository{}, nil, testToken, false)
	body, _ := json.Marshal(testQuery())
	for i := 0; i < 2; i++ {
		if res := request(h, http.MethodPost, "/api/analyze", string(body), true); res.Code != http.StatusOK {
			t.Fatalf("analyze status=%d body=%s", res.Code, res.Body.String())
		}
	}
	LogAnalyzeRepeats(h)
	got := strings.TrimSpace(logs.String())
	if got != "analyze repeats: 1 of 2" {
		t.Fatalf("shutdown log=%q", got)
	}
	if strings.Contains(got, "demo-west") {
		t.Fatalf("shutdown log leaked a source name: %q", got)
	}
}
