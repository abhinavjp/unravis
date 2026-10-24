package audit

import (
	"context"
	"strings"
	"testing"
)

// demoFilterCase pairs a filter with an independent predicate over the demo
// records. The predicate deliberately does not call RecordFilterMatches.
type demoFilterCase struct {
	name   string
	apply  func(q *Query)
	expect func(r RequestRecord) bool
}

func demoFilterCases() []demoFilterCase {
	return []demoFilterCase{
		{"user ids", func(q *Query) { q.UserIDs = []int{1003, 1005} }, func(r RequestRecord) bool { return *r.UserID == 1003 || *r.UserID == 1005 }},
		{"ui version hit", func(q *Query) { q.UIVersions = []string{"demo-1.0"} }, func(r RequestRecord) bool { return r.UIVersion == "demo-1.0" }},
		{"ui version miss", func(q *Query) { q.UIVersions = []string{"demo-9.9"} }, func(r RequestRecord) bool { return false }},
		{"agent contains is case-insensitive", func(q *Query) { q.UserAgentContains = "BROWSER/1" }, func(r RequestRecord) bool { return strings.Contains(strings.ToLower(r.UserAgent), "browser/1") }},
		{"ip list", func(q *Query) { q.UserIPs = []string{"203.0.113.10", "203.0.113.11"} }, func(r RequestRecord) bool { return r.UserIP == "203.0.113.10" || r.UserIP == "203.0.113.11" }},
		{"url contains", func(q *Query) { q.URLContains = "/PAYROLL/" }, func(r RequestRecord) bool { return strings.Contains(r.URL, "/payroll/") }},
		{"url contains is literal", func(q *Query) { q.URLContains = "/.*" }, func(r RequestRecord) bool { return false }},
		{"status codes", func(q *Query) { q.StatusCodes = []string{"200"} }, func(r RequestRecord) bool { return strings.HasPrefix(r.Status, "200") && strings.TrimSpace(r.Status) == "200" }},
		{"status code miss", func(q *Query) { q.StatusCodes = []string{"500"} }, func(r RequestRecord) bool { return false }},
		{"operation", func(q *Query) { q.Operation = "Reports.Generate" }, func(r RequestRecord) bool { return r.Controller == "Reports" && r.Action == "Generate" }},
		{"completion complete", func(q *Query) { q.Completion = "complete" }, func(r RequestRecord) bool { return true }},
		{"completion incomplete", func(q *Query) { q.Completion = "incomplete" }, func(r RequestRecord) bool { return false }},
		{"completion pending", func(q *Query) { q.Completion = "pending" }, func(r RequestRecord) bool { return false }},
		{"combined", func(q *Query) { q.UserIDs = []int{1001}; q.Operation = "Payroll.Run"; q.URLContains = "run" }, func(r RequestRecord) bool { return *r.UserID == 1001 && r.Controller == "Payroll" && r.Action == "Run" }},
	}
}

func pageAllDemoRequests(t *testing.T, d *demoRepository, q Query) []RequestRecord {
	t.Helper()
	var rows []RequestRecord
	cursor := ""
	for pages := 0; pages < 100; pages++ {
		p, err := d.Requests(context.Background(), PageInput{Query: q, Limit: MaxPageSize, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, p.Rows...)
		if p.NextCursor == "" {
			return rows
		}
		cursor = p.NextCursor
	}
	t.Fatal("request paging did not finish")
	return nil
}

func TestDemoRecordFiltersAgreeAcrossSummaryRequestsAndSlowest(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	base := demoSlowQuery()
	selected := map[string]bool{"demo-west": true, "demo-east": true}
	for _, tc := range demoFilterCases() {
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
			q := base
			tc.apply(&q)
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
			if got := int64(len(walkDemoSlowest(t, d, q, 50, nil))); got != want {
				t.Fatalf("slowest returned %d rows, want %d", got, want)
			}
		})
	}
	// The fixture must not be vacuous: the plain query sees every current row.
	var all int64
	for _, b := range aggregateDemo(d.records, base, selected, true) {
		all += b.Count
	}
	if all != 750 {
		t.Fatalf("unfiltered demo window holds %d rows, want 750", all)
	}
}

func TestDemoOperationKeepsTheEchoedQuery(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	q.Operation = "Payroll.Run"
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Query.Operation != "Payroll.Run" {
		t.Fatalf("analysis echoed operation %q, want Payroll.Run", analysis.Query.Operation)
	}
}

func TestDemoCursorIsBoundToTheRecordFilters(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	q.UserIDs = []int{1001}
	p, err := d.Requests(context.Background(), PageInput{Query: q, Limit: 5})
	if err != nil || p.NextCursor == "" {
		t.Fatalf("%v %+v", err, p)
	}
	other := q
	other.UserIDs = []int{1002}
	if _, err := d.Requests(context.Background(), PageInput{Query: other, Limit: 5, Cursor: p.NextCursor}); err == nil {
		t.Fatal("a cursor from another filter was accepted")
	}
}
