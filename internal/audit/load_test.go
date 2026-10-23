package audit

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLoadWindowsPoolHistogramsAndIncludeMissingCounts(t *testing.T) {
	q := analysisQuery()
	var b []Bucket
	b = append(b, repeatedBuckets(19, 10, anchor)...)
	b = append(b, makeBuckets("s1", "Reports", "Run", "synchronous", "success", "valid", anchor, 1, 2000)...)
	b = append(b, makeBuckets("s1", "Reports", "Run", "synchronous", "success", "incomplete", anchor, 3, 0)...)
	a, e := BuildAnalysis(q, b, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.LoadWindows) != 1 {
		t.Fatal(a.LoadWindows)
	}
	w := a.LoadWindows[0]
	if w.Count != 23 || w.Timing.Count != 20 || *w.Timing.MaxMs != 2000 || *w.Timing.P95.ValueMs > 20 {
		t.Fatalf("pooled %+v", w)
	}
}
func TestDemoSlowestIsGlobalDurationOrderAndMetricAware(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := analysisQuery()
	q.SourceIDs = []string{"demo-west", "demo-east"}
	q.Start = anchor.Add(-15 * time.Minute)
	q.End = anchor
	q.BaselineStart = anchor.Add(-30 * time.Minute)
	q.BaselineEnd = q.Start
	q.BucketSeconds = 60
	p, e := d.Slowest(context.Background(), PageInput{Query: q, Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Rows) != 2 || *p.Rows[0].DurationMs < *p.Rows[1].DurationMs {
		t.Fatal(p)
	}
	for _, r := range p.Rows {
		if r.Controller != "Payroll" {
			t.Fatal("not globally slowest")
		}
	}
}

func demoSlowQuery() Query {
	q := analysisQuery()
	q.SourceIDs = []string{"demo-west", "demo-east"}
	q.Start = anchor.Add(-15 * time.Minute)
	q.End = anchor
	q.BaselineStart = anchor.Add(-30 * time.Minute)
	q.BaselineEnd = q.Start
	q.BucketSeconds = 60
	return q
}

// demoSlowExpected ranks every matching demo record independently of
// Slowest: metric descending, then newest start, then source, then ID.
func demoSlowExpected(d *demoRepository, q Query, min float64) []RequestRecord {
	var rows []RequestRecord
	for _, r := range d.records {
		if r.InitiatedAt.Before(q.Start) || !r.InitiatedAt.Before(q.End) {
			continue
		}
		if v := RequestMetric(r, q.Metric); v != nil && *v >= min {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		av, bv := *RequestMetric(a, q.Metric), *RequestMetric(b, q.Metric)
		if av != bv {
			return av > bv
		}
		if !a.InitiatedAt.Equal(*b.InitiatedAt) {
			return a.InitiatedAt.After(*b.InitiatedAt)
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.ID > b.ID
	})
	return rows
}

func walkDemoSlowest(t *testing.T, d *demoRepository, q Query, limit int, min *float64) []RequestRecord {
	t.Helper()
	var all []RequestRecord
	cursor := ""
	for pages := 0; pages < 500; pages++ {
		p, e := d.Slowest(context.Background(), PageInput{Query: q, Limit: limit, Cursor: cursor, MinMs: min})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Rows) > limit {
			t.Fatalf("page of %d exceeds limit %d", len(p.Rows), limit)
		}
		all = append(all, p.Rows...)
		if p.NextCursor == "" {
			if p.Truncated {
				t.Fatal("truncated page without a cursor")
			}
			return all
		}
		if !p.Truncated {
			t.Fatal("cursor on a page that is not truncated")
		}
		cursor = p.NextCursor
	}
	t.Fatal("cursor never ended")
	return nil
}

func TestSlowestCursorIsStableAcrossPages(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	for _, metric := range []string{"total", "api"} {
		q := demoSlowQuery()
		q.Metric = metric
		want := demoSlowExpected(d, q, 0)
		if len(want) < 200 {
			t.Fatalf("fixture too small: %d", len(want))
		}
		for _, limit := range []int{7, 50} {
			got := walkDemoSlowest(t, d, q, limit, nil)
			if len(got) != len(want) {
				t.Fatalf("%s limit %d: got %d rows, want %d", metric, limit, len(got), len(want))
			}
			seen := map[string]bool{}
			for i := range got {
				key := got[i].SourceID + "/" + got[i].ID
				if seen[key] {
					t.Fatalf("%s limit %d: duplicate %s", metric, limit, key)
				}
				seen[key] = true
				if got[i].SourceID != want[i].SourceID || got[i].ID != want[i].ID {
					t.Fatalf("%s limit %d: row %d is %s/%s, want %s/%s", metric, limit, i, got[i].SourceID, got[i].ID, want[i].SourceID, want[i].ID)
				}
			}
		}
	}
}

func TestSlowestCursorRejectsAChangedQueryOrThreshold(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	min := 100.0
	p, e := d.Slowest(context.Background(), PageInput{Query: q, Limit: 5, MinMs: &min})
	if e != nil || p.NextCursor == "" {
		t.Fatalf("%v %+v", e, p)
	}
	other := 200.0
	for name, in := range map[string]PageInput{
		"threshold": {Query: q, Limit: 5, MinMs: &other, Cursor: p.NextCursor},
		"no min":    {Query: q, Limit: 5, Cursor: p.NextCursor},
		"garbage":   {Query: q, Limit: 5, MinMs: &min, Cursor: "!!"},
	} {
		if _, err := d.Slowest(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	changed := q
	changed.Metric = "api"
	if _, err := d.Slowest(context.Background(), PageInput{Query: changed, Limit: 5, MinMs: &min, Cursor: p.NextCursor}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("metric change: err = %v", err)
	}
}

func TestSlowestHonoursMinObserved(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	for _, metric := range []string{"total", "api"} {
		q := demoSlowQuery()
		q.Metric = metric
		all := demoSlowExpected(d, q, 0)
		// A boundary taken from the data: the minimum is inclusive.
		boundary := *RequestMetric(all[len(all)/2], metric)
		for _, min := range []float64{0, boundary, boundary + 0.001, 1e9} {
			want := demoSlowExpected(d, q, min)
			got := walkDemoSlowest(t, d, q, 50, &min)
			if len(got) != len(want) {
				t.Fatalf("%s min %v: got %d rows, want %d", metric, min, len(got), len(want))
			}
			for _, r := range got {
				if *RequestMetric(r, metric) < min {
					t.Fatalf("%s min %v: row below minimum %v", metric, min, *RequestMetric(r, metric))
				}
			}
		}
	}
}

func TestValidateSlowestAcceptsCursorAndBoundsThreshold(t *testing.T) {
	q := demoSlowQuery()
	ok := func(in PageInput) bool { return ValidateSlowest(in) == nil }
	min := 1000.0
	neg, nan := -1.0, 0.0
	nan = nan / nan
	if !ok(PageInput{Query: q, Limit: 50, Cursor: "opaque", MinMs: &min}) {
		t.Fatal("cursor and minimum must validate")
	}
	if ok(PageInput{Query: q, Limit: 51}) || ok(PageInput{Query: q, Limit: 0}) {
		t.Fatal("slowest pages are 1 to 50 rows")
	}
	if ok(PageInput{Query: q, Limit: 50, APIKey: "x"}) || ok(PageInput{Query: q, Limit: 50, MinMs: &neg}) || ok(PageInput{Query: q, Limit: 50, MinMs: &nan}) {
		t.Fatal("api key, negative and NaN minimum must be rejected")
	}
	if ok(PageInput{Query: q, Limit: 50, Cursor: strings.Repeat("a", 4097)}) {
		t.Fatal("oversized cursor must be rejected")
	}
}
