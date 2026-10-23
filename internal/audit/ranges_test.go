package audit

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

func rng(min, max *float64) *Range { return &Range{Min: min, Max: max} }

func marshalQuery(t *testing.T, q Query) string {
	t.Helper()
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRangeValidation(t *testing.T) {
	cases := []struct {
		name  string
		apply func(q *Query)
		ok    bool
	}{
		{"observed min only", func(q *Query) { q.ObservedMs = rng(fp(10), nil) }, true},
		{"observed max only", func(q *Query) { q.ObservedMs = rng(nil, fp(500)) }, true},
		{"observed both equal", func(q *Query) { q.ObservedMs = rng(fp(5), fp(5)) }, true},
		{"empty range is no filter", func(q *Query) { q.ObservedMs = &Range{} }, true},
		{"min above max", func(q *Query) { q.ObservedMs = rng(fp(9), fp(3)) }, false},
		{"negative min", func(q *Query) { q.ApiTimeMs = rng(fp(-1), nil) }, false},
		{"negative max", func(q *Query) { q.ApiTimeMs = rng(nil, fp(-0.5)) }, false},
		{"nan min", func(q *Query) { q.AuditCreateTimeMs = rng(fp(math.NaN()), nil) }, false},
		{"inf max", func(q *Query) { q.AuditCreateTimeMs = rng(nil, fp(math.Inf(1))) }, false},
		{"ms cap", func(q *Query) { q.ObservedMs = rng(nil, fp(1e9)) }, true},
		{"ms above cap", func(q *Query) { q.ObservedMs = rng(nil, fp(1e9+1)) }, false},
		{"kb cap", func(q *Query) { q.RequestKB = rng(nil, fp(1e7)) }, true},
		{"kb above cap", func(q *Query) { q.ResponseKB = rng(fp(1e7+1), nil) }, false},
		{"age above cap", func(q *Query) { q.IncompleteAgeMs = rng(fp(1e9+1), nil) }, false},
		{"latency with complete", func(q *Query) { q.ObservedMs = rng(fp(1), nil); q.Completion = "complete" }, true},
		{"latency with all", func(q *Query) { q.ObservedMs = rng(fp(1), nil); q.Completion = "all" }, true},
		{"latency with incomplete", func(q *Query) { q.ObservedMs = rng(fp(1), nil); q.Completion = "incomplete" }, false},
		{"latency with pending", func(q *Query) { q.ApiTimeMs = rng(fp(1), nil); q.Completion = "pending" }, false},
		{"latency with invalid", func(q *Query) { q.AuditCreateTimeMs = rng(fp(1), nil); q.Completion = "invalid" }, false},
		{"age with incomplete", func(q *Query) { q.IncompleteAgeMs = rng(fp(1), nil); q.Completion = "incomplete" }, true},
		{"age with pending", func(q *Query) { q.IncompleteAgeMs = rng(fp(1), nil); q.Completion = "pending" }, true},
		{"age with complete", func(q *Query) { q.IncompleteAgeMs = rng(fp(1), nil); q.Completion = "complete" }, false},
		{"age with invalid", func(q *Query) { q.IncompleteAgeMs = rng(fp(1), nil); q.Completion = "invalid" }, false},
		{"latency with age", func(q *Query) { q.ObservedMs = rng(fp(1), nil); q.IncompleteAgeMs = rng(fp(1), nil) }, false},
		{"payload ranges keep any completion", func(q *Query) { q.RequestKB = rng(fp(1), nil); q.Completion = "invalid" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := analysisQuery()
			tc.apply(&q)
			err := ValidateQuery(q)
			if tc.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("accepted or wrong error: %v", err)
			}
		})
	}
}

func TestEffectiveCompletion(t *testing.T) {
	q := analysisQuery()
	if got := EffectiveCompletion(q); got != "" {
		t.Fatalf("plain query: %q", got)
	}
	q.Completion = "all"
	if got := EffectiveCompletion(q); got != "" {
		t.Fatalf("all: %q", got)
	}
	q.Completion = "invalid"
	if got := EffectiveCompletion(q); got != "invalid" {
		t.Fatalf("invalid: %q", got)
	}
	for _, apply := range []func(*Query){
		func(q *Query) { q.ObservedMs = rng(nil, fp(5)) },
		func(q *Query) { q.ApiTimeMs = rng(fp(5), nil) },
		func(q *Query) { q.AuditCreateTimeMs = rng(fp(1), fp(5)) },
	} {
		q := analysisQuery()
		apply(&q)
		if got := EffectiveCompletion(q); got != StateComplete {
			t.Fatalf("latency range: %q", got)
		}
	}
	q = analysisQuery()
	q.IncompleteAgeMs = rng(fp(1), nil)
	if got := EffectiveCompletion(q); got != StateIncomplete {
		t.Fatalf("age range: %q", got)
	}
	q.Completion = "pending"
	if got := EffectiveCompletion(q); got != StatePending {
		t.Fatalf("age with pending: %q", got)
	}
	q = analysisQuery()
	q.RequestKB = rng(fp(1), nil)
	if got := EffectiveCompletion(q); got != "" {
		t.Fatalf("payload range must not imply completion: %q", got)
	}
	q = analysisQuery()
	q.ObservedMs = &Range{}
	if got := EffectiveCompletion(q); got != "" {
		t.Fatalf("an empty range must not imply completion: %q", got)
	}
}

func TestHasRecordFiltersSeesRanges(t *testing.T) {
	q := analysisQuery()
	if q.HasRecordFilters() {
		t.Fatal("plain query reports filters")
	}
	q.ResponseKB = rng(nil, fp(3))
	if !q.HasRecordFilters() {
		t.Fatal("a bounded range is a record filter")
	}
	q = analysisQuery()
	q.ObservedMs = &Range{}
	if q.HasRecordFilters() {
		t.Fatal("an empty range is not a record filter")
	}
}

func TestRangeFilterMatchesSemantics(t *testing.T) {
	q := analysisQuery()
	q.ObservedMs = rng(fp(10), fp(20))
	f := RangeFacts{ObservedMs: fp(10)}
	if !RangeFilterMatches(q, f) {
		t.Fatal("inclusive min")
	}
	f.ObservedMs = fp(20)
	if !RangeFilterMatches(q, f) {
		t.Fatal("inclusive max")
	}
	f.ObservedMs = fp(20.5)
	if RangeFilterMatches(q, f) {
		t.Fatal("above max matched")
	}
	f.ObservedMs = nil
	if RangeFilterMatches(q, f) {
		t.Fatal("a missing fact matched a bounded range")
	}
	// A max-only range never admits a missing value either.
	q = analysisQuery()
	q.RequestKB = rng(nil, fp(5))
	if RangeFilterMatches(q, RangeFacts{}) {
		t.Fatal("missing KB was treated as zero")
	}
	if !RangeFilterMatches(q, RangeFacts{RequestKB: fp(0)}) {
		t.Fatal("an explicit zero KB must match a max-only range")
	}
	if RangeFilterMatches(q, RangeFacts{RequestKB: fp(-1)}) {
		t.Fatal("a negative value matched")
	}
	// Unbounded ranges read nothing.
	q = analysisQuery()
	if !RangeFilterMatches(q, RangeFacts{}) {
		t.Fatal("no range must match everything")
	}
	// Each field reads its own fact.
	q = analysisQuery()
	q.ApiTimeMs = rng(fp(1), nil)
	q.AuditCreateTimeMs = rng(fp(1), nil)
	q.ResponseKB = rng(fp(1), nil)
	q.IncompleteAgeMs = rng(fp(1), nil)
	all := RangeFacts{ApiTimeMs: fp(1), AuditCreateTimeMs: fp(1), ResponseKB: fp(1), AgeMs: fp(1)}
	if !RangeFilterMatches(q, all) {
		t.Fatal("all facts in range")
	}
	for name, mutate := range map[string]func(*RangeFacts){
		"api":    func(f *RangeFacts) { f.ApiTimeMs = nil },
		"create": func(f *RangeFacts) { f.AuditCreateTimeMs = fp(0) },
		"resp":   func(f *RangeFacts) { f.ResponseKB = nil },
		"age":    func(f *RangeFacts) { f.AgeMs = fp(0) },
	} {
		f := all
		mutate(&f)
		if RangeFilterMatches(q, f) {
			t.Fatalf("%s: out-of-range fact matched", name)
		}
	}
}

func TestEffectiveSlowestMin(t *testing.T) {
	q := analysisQuery()
	if got := EffectiveSlowestMin(q, nil); got != nil {
		t.Fatalf("no bounds: %v", *got)
	}
	if got := EffectiveSlowestMin(q, fp(7)); got == nil || *got != 7 {
		t.Fatalf("explicit only: %v", got)
	}
	q.ObservedMs = rng(fp(30), nil)
	if got := EffectiveSlowestMin(q, fp(7)); got == nil || *got != 30 {
		t.Fatalf("observed raises the floor: %v", got)
	}
	if got := EffectiveSlowestMin(q, fp(50)); got == nil || *got != 50 {
		t.Fatalf("explicit wins when higher: %v", got)
	}
	// With the total metric the api minimum is not a floor on the ranking value.
	q = analysisQuery()
	q.ApiTimeMs = rng(fp(30), nil)
	if got := EffectiveSlowestMin(q, nil); got != nil {
		t.Fatalf("total metric must ignore the api minimum: %v", *got)
	}
	q.Metric = "api"
	if got := EffectiveSlowestMin(q, nil); got == nil || *got != 30 {
		t.Fatalf("api metric floor: %v", got)
	}
	q.ObservedMs = rng(fp(90), nil)
	if got := EffectiveSlowestMin(q, nil); got != nil && *got != 30 {
		t.Fatalf("observed minimum must not floor the api metric: %v", *got)
	}
}

func TestRangeJSONRoundTrip(t *testing.T) {
	q := analysisQuery()
	if strings.Contains(marshalQuery(t, q), "observedMs") {
		t.Fatal("an unset range must be omitted")
	}
	q.ObservedMs = rng(fp(1), nil)
	q.IncompleteAgeMs = rng(nil, fp(9))
	raw := marshalQuery(t, q)
	for _, want := range []string{`"observedMs":{"min":1}`, `"incompleteAgeMs":{"max":9}`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("%s missing from %s", want, raw)
		}
	}
	q.Now = time.Unix(5, 0)
	if strings.Contains(marshalQuery(t, q), "now") || strings.Contains(marshalQuery(t, q), "Now") {
		t.Fatal("the clock must never be serialized")
	}
}

// demoRangeCases pair a range with an independent predicate over the demo
// records (Observed is completed minus initiated; the rest read stored fields).
func demoRangeCases() []demoFilterCase {
	observed := func(r RequestRecord) float64 { return float64(r.CompletedAt.UnixMilli()-r.InitiatedAt.UnixMilli()) }
	return []demoFilterCase{
		{"duration min", func(q *Query) { q.ApiTimeMs = rng(fp(45), nil) }, func(r RequestRecord) bool { return r.ApiTimeMs != nil && *r.ApiTimeMs >= 45 }},
		{"duration max", func(q *Query) { q.AuditCreateTimeMs = rng(nil, fp(8)) }, func(r RequestRecord) bool { return r.AuditCreateTimeMs != nil && *r.AuditCreateTimeMs <= 8 }},
		{"observed band", func(q *Query) { q.ObservedMs = rng(fp(30), fp(400)) }, func(r RequestRecord) bool { v := observed(r); return v >= 30 && v <= 400 }},
		{"request kb", func(q *Query) { q.RequestKB = rng(fp(1), fp(50)) }, func(r RequestRecord) bool { return r.RequestKB >= 1 && r.RequestKB <= 50 }},
		{"response kb", func(q *Query) { q.ResponseKB = rng(nil, fp(100)) }, func(r RequestRecord) bool { return r.ResponseKB <= 100 }},
		{"impossible range", func(q *Query) { q.ObservedMs = rng(fp(1e8), nil) }, func(r RequestRecord) bool { return false }},
		{"range with filter", func(q *Query) { q.ApiTimeMs = rng(fp(45), nil); q.UserIDs = []int{1001} }, func(r RequestRecord) bool { return *r.UserID == 1001 && r.ApiTimeMs != nil && *r.ApiTimeMs >= 45 }},
	}
}

func TestDemoRangeFiltersAgreeAcrossSummaryRequestsAndSlowest(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	base := demoSlowQuery()
	selected := map[string]bool{"demo-west": true, "demo-east": true}
	nonEmpty := 0
	for _, tc := range demoRangeCases() {
		t.Run(tc.name, func(t *testing.T) {
			var want int64
			for _, r := range d.records {
				if r.InitiatedAt.Before(base.Start) || !r.InitiatedAt.Before(base.End) {
					continue
				}
				if tc.expect(r) {
					want++
				}
			}
			if want > 0 {
				nonEmpty++
			}
			q := base
			tc.apply(&q)
			if err := ValidateQuery(q); err != nil {
				t.Fatal(err)
			}
			var summary int64
			for _, b := range aggregateDemo(d.records, q, selected, true) {
				summary += b.Count
			}
			if summary != want {
				t.Fatalf("summary counted %d rows, want %d", summary, want)
			}
			if got := int64(len(pageAllDemoRequests(t, d, q))); got != want {
				t.Fatalf("requests returned %d rows, want %d", got, want)
			}
			// The slowest ranking drops rows with no usable metric; none of the
			// demo rows lack one, so it must agree as well.
			if got := int64(len(walkDemoSlowest(t, d, q, 50, nil))); got != want {
				t.Fatalf("slowest returned %d rows, want %d", got, want)
			}
		})
	}
	if nonEmpty < 5 {
		t.Fatalf("only %d range cases select any row; the fixture is vacuous", nonEmpty)
	}
}

func TestDemoCursorIsBoundToTheRanges(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	q.ApiTimeMs = rng(fp(1), nil)
	p, err := d.Requests(context.Background(), PageInput{Query: q, Limit: 5})
	if err != nil || p.NextCursor == "" {
		t.Fatalf("%v %+v", err, p)
	}
	other := q
	other.ApiTimeMs = rng(fp(2), nil)
	if _, err := d.Requests(context.Background(), PageInput{Query: other, Limit: 5, Cursor: p.NextCursor}); err == nil {
		t.Fatal("a cursor from another range was accepted")
	}
}

func TestDemoSlowestAppliesTheRangeFloorAndKeepsItsCursor(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	q.ObservedMs = rng(fp(60), nil)
	want := 0
	for _, r := range demoSlowExpected(d, q, 0) {
		if float64(r.CompletedAt.UnixMilli()-r.InitiatedAt.UnixMilli()) >= 60 {
			want++
		}
	}
	if want == 0 {
		t.Fatal("vacuous fixture")
	}
	rows := walkDemoSlowest(t, d, q, 7, nil)
	if len(rows) != want {
		t.Fatalf("got %d rows, want %d", len(rows), want)
	}
	// An explicit minimum above the range floor still applies.
	high := fp(1e9)
	if got := walkDemoSlowest(t, d, q, 50, high); len(got) != 0 {
		t.Fatalf("explicit minimum ignored: %d rows", len(got))
	}
}

// ageDemo builds a demo with incomplete rows of known age.
func ageDemo() (*demoRepository, Query) {
	now := anchor
	at := func(ago time.Duration) *time.Time { v := now.Add(-ago); return &v }
	row := func(id string, initiated *time.Time, completed *time.Time) RequestRecord {
		return RequestRecord{SourceID: "demo-west", ID: id, Controller: "Reports", Action: "Run", Kind: "synchronous", Outcome: "success", Status: "200", InitiatedAt: initiated, CompletedAt: completed, State: "incomplete"}
	}
	d := &demoRepository{now: now, sources: []Source{{ID: "demo-west", Label: "West", Database: "demo", Collection: "audit"}}}
	d.records = []RequestRecord{
		row("young", at(30*time.Second), nil),
		row("middle", at(5*time.Minute), nil),
		row("old", at(9*time.Minute), nil),
	}
	q := demoSlowQuery()
	q.SourceIDs = []string{"demo-west"}
	return d, q
}

func TestDemoIncompleteAgeUsesTheDemoClock(t *testing.T) {
	d, q := ageDemo()
	if len(d.records) != 3 {
		t.Fatal("fixture")
	}
	q.IncompleteAgeMs = rng(fp(2*60*1000), fp(6*60*1000))
	page, err := d.Requests(context.Background(), PageInput{Query: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ID != "middle" {
		t.Fatalf("rows: %+v", page.Rows)
	}
	q.Completion = "complete"
	if err := ValidateQuery(q); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("age range with a complete filter: %v", err)
	}
}
