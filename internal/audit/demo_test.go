package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func demoQuery(now time.Time) Query {
	return Query{SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-15 * time.Minute), End: now, BaselineStart: now.Add(-30 * time.Minute), BaselineEnd: now.Add(-15 * time.Minute), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 10}
}

func TestDemoHasTwoPublicSourcesAndRejectsURIInput(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	sources, err := d.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("got %d sources", len(sources))
	}
	encoded, err := json.Marshal(sources)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "uri") {
		t.Fatalf("public source exposes URI: %s", encoded)
	}
	if _, err := d.AddSource(context.Background(), SourceInput{Label: "live", URI: "mongodb://user:secret@example.invalid/db", Database: "db", Collection: "requests"}); err == nil {
		t.Fatal("demo must reject real connection input")
	}
	sources, err = d.Sources(context.Background())
	if err != nil || len(sources) != 2 {
		t.Fatalf("rejected URI changed demo sources: %d, %v", len(sources), err)
	}
}

func TestDemoAnalyzesSelectedCurrentAndReferenceWindows(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	q := demoQuery(now)
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) != 6 {
		t.Fatalf("expected 3 API cohorts per source, got %d", len(analysis.APIs))
	}
	var payrollRatio, reportRatio, employeeRatio float64
	for _, c := range analysis.APIs {
		if c.State != "compared" || c.Current.Count < 20 || c.Reference.Count < 20 {
			t.Fatalf("demo cohort isn't comparable: %#v", c)
		}
		if strings.EqualFold(c.Controller, "Payroll") && c.MedianRatio != nil {
			payrollRatio = *c.MedianRatio
		}
		if strings.EqualFold(c.Controller, "Reports") && c.MedianRatio != nil {
			reportRatio = *c.MedianRatio
		}
		if strings.EqualFold(c.Controller, "Employees") && c.MedianRatio != nil {
			employeeRatio = *c.MedianRatio
		}
	}
	if payrollRatio < 2 {
		t.Fatalf("payroll should regress in current window: %v", payrollRatio)
	}
	if reportRatio < .9 || reportRatio > 1.1 {
		t.Fatalf("report should remain stable: %v", reportRatio)
	}
	if employeeRatio < .9 || employeeRatio > 1.1 {
		t.Fatalf("employee API should remain stable: %v", employeeRatio)
	}
	q.Start = now.Add(-10 * time.Minute)
	q.End = now.Add(-5 * time.Minute)
	q.BaselineStart = now.Add(-25 * time.Minute)
	q.BaselineEnd = now.Add(-20 * time.Minute)
	analysis, err = d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) == 0 {
		t.Fatal("reselected fixture window should contain observations")
	}
}

func TestDemoRequestPagingHasNoDuplicatesAndCapsAt100(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	q := demoQuery(now)
	analysis, err := d.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for _, c := range analysis.APIs {
		if c.Controller == "Payroll" {
			key = c.Key
			break
		}
	}
	if key == "" {
		t.Fatal("payroll key missing")
	}
	seen := map[string]bool{}
	cursor := ""
	pageCount := 0
	for {
		page, err := d.Requests(context.Background(), PageInput{Query: q, APIKey: key, Cursor: cursor, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) > 100 {
			t.Fatalf("returned %d rows", len(page.Rows))
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatalf("duplicate row %s", row.ID)
			}
			seen[row.ID] = true
		}
		pageCount += len(page.Rows)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if pageCount != 125 {
		t.Fatalf("expected 125 payroll rows for one source, got %d", pageCount)
	}
	if _, err := d.Requests(context.Background(), PageInput{Query: q, APIKey: key, Limit: MaxPageSize + 1}); err == nil {
		t.Fatal("expected page size cap")
	}
}

func TestDemoPayloadIsExplicitlyIllustrative(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	page, err := d.Requests(context.Background(), PageInput{Query: demoQuery(now), APIKey: CohortKey("demo-west", "Payroll", "Run", "synchronous", "success"), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("rows=%d", len(page.Rows))
	}
	payload, err := d.Payload(context.Background(), PayloadInput{SourceID: page.Rows[0].SourceID, ID: page.Rows[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Request, "Illustrative demo") || !strings.Contains(payload.Response, "Illustrative demo") {
		t.Fatalf("payload isn't labeled illustrative: %#v", payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "mongodb") {
		t.Fatalf("demo payload contains connection detail: %s", encoded)
	}
}

func TestDemoCanceledContextReturnsPromptly(t *testing.T) {
	d := NewDemo(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Analyze(ctx, demoQuery(time.Now())); err == nil {
		t.Fatal("expected context cancellation")
	}
	if _, err := d.Sources(ctx); err == nil {
		t.Fatal("expected context cancellation")
	}
}

func TestDemoResponsesMatchJSONContracts(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	analysis, err := d.Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "NaN") || strings.Contains(string(encoded), "Infinity") {
		t.Fatalf("non-finite JSON value: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"apis"`) || !strings.Contains(string(encoded), `"sourceId"`) || !strings.Contains(string(encoded), `"p95Ratio"`) {
		t.Fatalf("unexpected contract JSON: %s", encoded)
	}
}
