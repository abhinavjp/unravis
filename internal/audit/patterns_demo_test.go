package audit

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var patternDemoNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func demoPatternInput(now time.Time) PatternInput {
	return PatternInput{SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-4 * time.Hour), End: now}
}

// The base demo has no request near 45 s, which is why the scenario exists.
func TestPatternsBaseDemoHasNoStalls(t *testing.T) {
	for name, repo := range map[string]Repository{"demo": NewDemo(patternDemoNow), "stale work": NewDemoWithStaleWork(patternDemoNow)} {
		r, err := RunPatterns(context.Background(), repo, demoPatternInput(patternDemoNow))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.Recurrence != RecurrenceNone || len(r.Stalls) != 0 || len(r.AuditStalls) != 0 || len(r.Findings) != 0 {
			t.Fatalf("%s: %+v", name, r)
		}
	}
}

func TestPatternsDemoScenario(t *testing.T) {
	r, err := RunPatterns(context.Background(), NewDemoWithPatterns(patternDemoNow), demoPatternInput(patternDemoNow))
	if err != nil {
		t.Fatal(err)
	}
	if r.Recurrence != RecurrenceRecurring || len(r.Episodes) != 3 || len(r.Stalls) != 6 || r.Truncated {
		t.Fatalf("recurrence %q episodes %d stalls %d truncated %v", r.Recurrence, len(r.Episodes), len(r.Stalls), r.Truncated)
	}
	if got := []int{r.Episodes[0].Requests, r.Episodes[1].Requests, r.Episodes[2].Requests}; fmt.Sprint(got) != "[2 1 3]" {
		t.Fatalf("episode requests %v", got)
	}
	if len(r.Volleys) != 1 {
		t.Fatalf("volleys %+v", r.Volleys)
	}
	v := r.Volleys[0]
	if v.Episode != 3 || v.UserID != 1003 || v.Requests != 3 || v.StartSpreadMs != 700 || v.EndStaggerMs != 8700 || fmt.Sprint(v.EndGapsMs) != "[4400 4300]" {
		t.Fatalf("volley %+v", v)
	}
	if len(r.AuditStalls) != 2 {
		t.Fatalf("audit stalls %+v", r.AuditStalls)
	}
	rules := ""
	for _, f := range r.Findings {
		rules += f.Rule + " "
	}
	if rules != "B1 B2 B6 B12 " {
		t.Fatalf("findings %+v", r.Findings)
	}
	if b6 := findingByRule(r, "B6"); b6.Headline != "Audit create took 6.2 s as measured from the client side. That is the longest of 2 requests at or above 5 s and 30% of observed time." {
		t.Fatalf("B6 headline %q", b6.Headline)
	}
	if r.Coverage == nil || !r.Coverage.Run || r.Coverage.RequestInitiatedAtPct == nil || *r.Coverage.RequestInitiatedAtPct != 100 {
		t.Fatalf("coverage %+v", r.Coverage)
	}
	if r.Quality != (PatternQuality{Incomplete: 6, Pending: 6, Invalid: 2}) {
		t.Fatalf("quality %+v", r.Quality)
	}
	if len(r.Provenance) == 0 {
		t.Fatal("no provenance")
	}
}

// The scenario sits outside the 30 minutes the base demo fills, so the standard
// windows read the same rows with or without it.
func TestPatternsDemoScenarioLeavesBaseWindowsAlone(t *testing.T) {
	now := patternDemoNow
	q := demoQuery(now)
	base, err := NewDemoWithStaleWork(now).Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	withPatterns, err := NewDemoWithPatterns(now).Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.APIs, withPatterns.APIs) || !reflect.DeepEqual(base.Quality, withPatterns.Quality) {
		t.Fatal("the pattern scenario changed the standard demo window")
	}
	for _, r := range demoPatternRecords(now) {
		if !r.InitiatedAt.Before(now.Add(-30*time.Minute)) || r.SourceID != demoPatternSource {
			t.Fatalf("scenario row %s inside the base window", r.ID)
		}
	}
}
