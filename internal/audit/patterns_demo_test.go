package audit

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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
	if rules != "B1 B2 B6 B12 B12r " {
		t.Fatalf("findings %+v", r.Findings)
	}
	if want := []Repeat{{Episode: 1, UserID: 1001, Operation: "Payroll.Run", Repeats: 3, SameSize: true, LongestChain: 4}}; !reflect.DeepEqual(r.Repeats, want) {
		t.Fatalf("repeats %+v", r.Repeats)
	}
	if b := findingByRule(r, "B12r"); b.Label != EvidenceCorrelation || b.N != 3 || !b.LowN || b.Headline != "The same user started Payroll.Run again 3 times while an earlier one was still running, during this stall (episode 1)." {
		t.Fatalf("B12r %+v", b)
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
	scenario := append(demoPatternRecords(now), demoOnsetRecords(now)...)
	scenario = append(scenario, demoIdleRecords(now, demoIdlePositiveSource, true)...)
	scenario = append(scenario, demoIdleRecords(now, demoIdleNegativeSource, false)...)
	for _, r := range scenario {
		if !r.InitiatedAt.Before(now.Add(-30*time.Minute)) || (r.SourceID != "demo-west" && r.SourceID != "demo-east") {
			t.Fatalf("scenario row %s inside the base window", r.ID)
		}
	}
}

func demoMultiDayInput(now time.Time) PatternInput {
	return PatternInput{SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-5 * 24 * time.Hour), End: now}
}

// Over five days the demo shows one recurring onset minute (B3a) and one
// operation that is a small share of traffic but most of the long stalls (A3).
func TestPatternsDemoOnsetAndConcentration(t *testing.T) {
	r, err := RunPatterns(context.Background(), NewDemoWithPatterns(patternDemoNow), demoMultiDayInput(patternDemoNow))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Episodes) != 6 || len(r.Stalls) != 6+demoOnsetDays*4 {
		t.Fatalf("episodes %d stalls %d", len(r.Episodes), len(r.Stalls))
	}
	var recurring *OnsetMinute
	for i := range r.OnsetMinutes {
		if r.OnsetMinutes[i].Minute == demoOnsetMinute {
			recurring = &r.OnsetMinutes[i]
		}
	}
	if recurring == nil || recurring.Days != demoOnsetDays || recurring.Onsets != demoOnsetDays || len(recurring.Episodes) != demoOnsetDays {
		t.Fatalf("onset minutes %+v", r.OnsetMinutes)
	}
	b3a := findingByRule(r, "B3a")
	if b3a == nil || b3a.Label != EvidenceSymptom || b3a.Headline != "Stalls started near minute :17 of the hour on 3 different days in this window." || b3a.N != 3 || !b3a.LowN {
		t.Fatalf("B3a %+v", b3a)
	}
	a3 := findingByRule(r, "A3")
	if a3 == nil || a3.Label != EvidenceCorrelation || a3.N != 12 || a3.LowN || !strings.HasPrefix(a3.Headline, "Documents.Export had 66.7% of stall requests but ") {
		t.Fatalf("A3 %+v", a3)
	}
	if len(r.Concentration) != 1 {
		t.Fatalf("concentration %+v", r.Concentration)
	}
	c := r.Concentration[0]
	if c.Dimension != ConcentrationOperation || c.Key != "Documents.Export" || c.StallN != 12 || c.AllN != 24 || c.Episodes != 3 || c.Withheld || c.Lift == nil || *c.Lift < 2 {
		t.Fatalf("row %+v", c)
	}
}

// The 4-hour window sees none of the older rows, so it has no B3a or A3.
func TestPatternsDemoOlderRowsStayOutOfFourHours(t *testing.T) {
	now := patternDemoNow
	for _, r := range demoOnsetRecords(now) {
		if r.InitiatedAt.After(now.Add(-19*time.Hour)) || r.SourceID != demoPatternSource {
			t.Fatalf("onset row %s starts too late", r.ID)
		}
	}
	rep, err := RunPatterns(context.Background(), NewDemoWithPatterns(now), demoPatternInput(now))
	if err != nil {
		t.Fatal(err)
	}
	if findingByRule(rep, "B3a") != nil || findingByRule(rep, "A3") != nil || len(rep.OnsetMinutes) != 3 || len(rep.Concentration) != 0 {
		t.Fatalf("%+v %+v", rep.Findings, rep.Concentration)
	}
}

func demoIdleInput(source string, from, to time.Duration) PatternInput {
	return PatternInput{SourceIDs: []string{source}, Start: patternDemoNow.Add(-from), End: patternDemoNow.Add(-to), IdleGaps: true}
}

// Quiet gaps (B6g): the west scenario has slow audit create after quiet gaps.
func TestPatternsDemoIdleGapsRaised(t *testing.T) {
	r, err := RunPatterns(context.Background(), NewDemoWithPatterns(patternDemoNow), demoIdleInput("demo-west", 18*time.Hour, 12*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stalls) != 0 || len(r.AuditStalls) != 0 {
		t.Fatalf("idle rows must not be stalls: %+v %+v", r.Stalls, r.AuditStalls)
	}
	g := r.IdleGaps
	if g == nil || g.Partial || g.ExcludedHangfire != 0 || len(g.Bands) != 5 || len(g.Lift) != 3 {
		t.Fatalf("idle gaps %+v", g)
	}
	if b := g.Bands[0]; b.N != 48 || b.MedianMs == nil || *b.MedianMs != 120 || b.SlowRate == nil {
		t.Fatalf("reference band %+v", b)
	}
	if b := g.Bands[4]; b.FromS != 600 || b.ToS != nil || b.N != 23 || b.MedianMs == nil || *b.MedianMs != 6200 {
		t.Fatalf("last band %+v", b)
	}
	if b := g.Bands[1]; b.N != 0 || b.MedianMs != nil || b.P95Ms != nil || b.SlowRate != nil {
		t.Fatalf("empty band must be null %+v", b)
	}
	for _, l := range g.Lift {
		if l.Withheld || l.Value == nil || *l.Value < 20 {
			t.Fatalf("lift %+v", l)
		}
	}
	f := findingByRule(r, "B6g")
	if f == nil || f.Label != EvidenceSymptom || f.N != 23 || f.LowN {
		t.Fatalf("B6g %+v", f)
	}
	if want := "Raised after quiet gaps. After quiet gaps of at least 10 min, audit create took 6.2 s at the median (n = 23). After gaps under 30 s it took 0.1 s (n = 48). Audit create is measured from the client side and covers setup work and the database write, not just the write."; f.Headline != want {
		t.Fatalf("headline %q", f.Headline)
	}
}

// The east scenario has the same traffic with slow audit create spread evenly, so
// the rule says "not observed".
func TestPatternsDemoIdleGapsNotObserved(t *testing.T) {
	r, err := RunPatterns(context.Background(), NewDemoWithPatterns(patternDemoNow), demoIdleInput("demo-east", 18*time.Hour, 6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	g := r.IdleGaps
	if g == nil || g.Partial || g.Bands[0].N != 112 || g.Bands[4].N != 55 {
		t.Fatalf("idle gaps %+v", g)
	}
	for _, l := range g.Lift {
		if l.Withheld || l.Value == nil || *l.Value > 1.5 || *l.Value < 0.5 {
			t.Fatalf("lift %+v", l)
		}
	}
	f := findingByRule(r, "B6g")
	if f == nil || f.Label != EvidenceSymptom || f.Headline != "Not observed: audit create was not slower after quiet gaps of at least 10 min in this window." {
		t.Fatalf("B6g %+v", f)
	}
}

// Without the opt-in the demo reads nothing extra and prints no table.
func TestPatternsDemoIdleGapsOptIn(t *testing.T) {
	in := demoIdleInput("demo-west", 18*time.Hour, 12*time.Hour)
	in.IdleGaps = false
	r, err := RunPatterns(context.Background(), NewDemoWithPatterns(patternDemoNow), in)
	if err != nil {
		t.Fatal(err)
	}
	if r.IdleGaps != nil || findingByRule(r, "B6g") != nil {
		t.Fatalf("%+v", r.IdleGaps)
	}
}
