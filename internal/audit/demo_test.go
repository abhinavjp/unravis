package audit

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDemoRecordIsIllustrativeAndComplete(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemo(now)
	page, err := d.Requests(context.Background(), PageInput{Query: demoQuery(now), APIKey: CohortKey("demo-west", "Payroll", "Run", "synchronous", "success"), Limit: 1})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(page.Rows), err)
	}
	row := page.Rows[0]
	detail, err := d.Record(context.Background(), RecordInput{SourceID: row.SourceID, ID: row.ID})
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != row.ID || detail.State != "valid" || detail.UserID == nil || detail.UserIP == "" || detail.UIVersion == "" || detail.CreatedOn == nil || detail.UpdatedOn == nil || detail.PollingTypeRaw == "" {
		t.Fatalf("demo detail lacks audit evidence: %#v", detail)
	}
	if !strings.Contains(detail.Payload.Request, "Illustrative demo") || !strings.Contains(detail.Payload.Response, "Illustrative demo") {
		t.Fatalf("demo payload isn't labeled illustrative: %#v", detail.Payload)
	}
	if row.UserID == nil || row.UIVersion == "" {
		t.Fatalf("demo table rows lack the new evidence columns: %#v", row)
	}
	if _, err := d.Record(context.Background(), RecordInput{SourceID: row.SourceID, ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing record error = %v", err)
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

func TestDemoAnalysisCarriesAdditivePhases(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	analysis, err := NewDemo(now).Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range analysis.APIs {
		p := c.Current.Phases
		if p == nil || p.AuditMs == nil || p.ApiMs == nil || p.PayloadMs == nil || c.Current.MeanMs == nil {
			t.Fatalf("%s.%s has no phases: %+v", c.Controller, c.Action, p)
		}
		if got := *p.AuditMs + *p.ApiMs; got < *c.Current.MeanMs-1e-6 || got > *c.Current.MeanMs+1e-6 {
			t.Fatalf("%s.%s audit+api = %v, want the total mean %v", c.Controller, c.Action, got, *c.Current.MeanMs)
		}
		if c.PhaseDeltaMs == nil || c.PhaseDeltaMs.ApiMs == nil {
			t.Fatalf("%s.%s has no phase delta", c.Controller, c.Action)
		}
	}
}

// demoDrillRepo is a demo repository plus hand-made rows in every quality
// state (the generated demo rows are all complete): an unfinished row, an
// unusable timing, pending and unclassified rows, an unknown-kind row and a
// Hangfire row, each inside the current window.
func demoDrillRepo(now time.Time) *demoRepository {
	d := NewDemo(now).(*demoRepository)
	at := now.Add(-5 * time.Minute)
	negative := -50.0
	add := func(id, kind, state, status, outcome string, duration *float64) {
		initiated := at
		d.records = append(d.records, RequestRecord{ID: id, SourceID: "demo-west", Controller: "Payroll", Action: "Run", Kind: kind, State: state, Status: status, Outcome: outcome, InitiatedAt: &initiated, DurationMs: duration, ApiTimeMs: duration})
	}
	add("x-incomplete", "synchronous", "incomplete", "", "unknown", nil)
	add("x-incomplete-500", "synchronous", "incomplete", "500 Internal Server Error", "failure", nil)
	add("x-invalid", "synchronous", "invalid", "200 OK", "success", &negative)
	add("x-pending", "asynchronous", "incomplete", "Accepted", "unknown", nil)
	add("x-unknown-kind", "unknown", "incomplete", "", "unknown", nil)
	add("x-unclassified", "synchronous", "valid", "", "unknown", new(float64))
	add("x-hangfire-incomplete", "hangfire", "incomplete", "", "unknown", nil)
	return d
}

func demoDrillCount(t *testing.T, d *demoRepository, q Query, state string) (int, map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := d.Requests(context.Background(), PageInput{Query: q, State: state, Cursor: cursor, Limit: 100})
		if err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatalf("duplicate row %s", row.ID)
			}
			seen[row.ID] = true
			if row.State == "" {
				t.Fatalf("row %s has no state", row.ID)
			}
		}
		if page.NextCursor == "" {
			return len(seen), seen
		}
		cursor = page.NextCursor
	}
}

func TestDemoRequestsStateFilterIncomplete(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := demoDrillRepo(now)
	q := demoQuery(now)
	page, err := d.Requests(context.Background(), PageInput{Query: q, State: "incomplete", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) == 0 {
		t.Fatal("no incomplete rows returned")
	}
	for _, row := range page.Rows {
		if row.State != "incomplete" {
			t.Errorf("row %s has state %q, want incomplete", row.ID, row.State)
		}
	}
	if _, err := d.Requests(context.Background(), PageInput{Query: q, State: "bogus", Limit: 100}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown state error = %v, want ErrInvalidInput", err)
	}
}

func TestDemoQualityCountEqualsDrillCount(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := demoDrillRepo(now)
	for _, kind := range []string{"all", "http", "synchronous", "asynchronous", "hangfire"} {
		for _, outcome := range []string{"all", "success", "failure", "client_error", "server_error"} {
			q := demoQuery(now)
			q.Kind, q.Outcome = kind, outcome
			analysis, err := d.Analyze(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			quality := analysis.Quality
			for state, want := range map[string]int64{"incomplete": quality.Incomplete, "invalid": quality.Invalid, "pending": quality.PendingAsync, "unclassified": quality.UnknownOutcome} {
				got, _ := demoDrillCount(t, d, q, state)
				if int64(got) != want {
					t.Errorf("%s/%s: quality count for %s = %d, drill-down rows = %d", kind, outcome, state, want, got)
				}
			}
			all, _ := demoDrillCount(t, d, q, "")
			complete, _ := demoDrillCount(t, d, q, "complete")
			incomplete, _ := demoDrillCount(t, d, q, "incomplete")
			invalid, _ := demoDrillCount(t, d, q, "invalid")
			if all != complete+incomplete+invalid {
				t.Errorf("%s/%s: %d rows, but complete %d + incomplete %d + invalid %d", kind, outcome, all, complete, incomplete, invalid)
			}
		}
	}
	// The hand-made rows are really counted: the default view sees each of them.
	analysis, err := d.Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	if q := analysis.Quality; q.Incomplete != 5 || q.Invalid != 1 || q.PendingAsync != 1 || q.UnknownOutcome != 5 {
		t.Fatalf("quality = %+v, want 5 incomplete, 1 invalid, 1 pending, 5 unclassified", q)
	}
}

func TestDemoCohortDrillMatchesSummaryUnderKindFilter(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := demoDrillRepo(now)
	q := demoQuery(now)
	q.Kind, q.Outcome = "synchronous", "success"
	// The unknown-kind cohort exists under a synchronous filter and opens.
	page, err := d.Requests(context.Background(), PageInput{Query: q, APIKey: CohortKey("demo-west", "Payroll", "Run", "unknown", "unknown"), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ID != "x-unknown-kind" || page.Rows[0].State != "incomplete" {
		t.Fatalf("unknown-kind cohort rows = %+v", page.Rows)
	}
	if _, err := d.Requests(context.Background(), PageInput{Query: q, APIKey: CohortKey("demo-west", "Payroll", "Run", "hangfire", "unknown"), Limit: 100}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("hangfire cohort under synchronous = %v, want ErrInvalidInput", err)
	}
}

// The demo's timeline carries the failure counters and percentiles of every
// point: the hand-made unfinished rows reach the points as Incomplete counts
// only (an unfinished 500 is never a failure), and the timed rows keep P90 but
// no P99 below 100 samples.
func TestDemoTimelinePointsCarryFailureCountsAndPercentiles(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	analysis, err := demoDrillRepo(now).Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	var incomplete, server, client, timed int64
	for _, s := range analysis.Timeline {
		for _, p := range s.Points {
			incomplete, server, client = incomplete+p.Incomplete, server+p.Count5xx, client+p.Count4xx
			if p.Count > 0 {
				timed++
				if p.P90 == nil || p.P99 != nil {
					t.Fatalf("timed demo point has p90=%v p99=%v with %d samples", p.P90, p.P99, p.Count)
				}
			} else if p.P50 != nil || p.P90 != nil || p.P95 != nil || p.P99 != nil {
				t.Fatalf("point with no timings has percentiles: %#v", p)
			}
		}
	}
	if incomplete != analysis.Quality.Incomplete || server != 0 || client != 0 || timed == 0 {
		t.Fatalf("timeline incomplete=%d (quality %d) 5xx=%d 4xx=%d timed=%d, want no 5xx and no 4xx: unfinished rows are never failures", incomplete, analysis.Quality.Incomplete, server, client, timed)
	}
}

func TestDemoOldestFirstListsUnfinishedWorkAndBindsCursorToOrder(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	d := NewDemoWithStaleWork(now)
	ctx := context.Background()
	q := demoQuery(now)
	q.Completion = "incomplete"
	all := func(order string, completion string) []RequestRecord {
		t.Helper()
		qq := q
		qq.Completion = completion
		var rows []RequestRecord
		cursor := ""
		for pages := 0; pages < 20; pages++ {
			page, err := d.Requests(ctx, PageInput{Query: qq, Order: order, Cursor: cursor, Limit: 5})
			if err != nil {
				t.Fatalf("order %q completion %q: %v", order, completion, err)
			}
			rows = append(rows, page.Rows...)
			if page.NextCursor == "" {
				return rows
			}
			cursor = page.NextCursor
		}
		t.Fatal("paging did not finish")
		return nil
	}
	oldest, newest := all("oldest", "incomplete"), all("newest", "incomplete")
	if len(oldest) != 12 || len(newest) != 12 {
		t.Fatalf("the demo should hold 12 unfinished requests (6 per source), got %d oldest and %d newest", len(oldest), len(newest))
	}
	seen := map[string]bool{}
	for i, row := range oldest {
		if seen[row.ID] {
			t.Fatalf("duplicate row %s while paging oldest first", row.ID)
		}
		seen[row.ID] = true
		if row.State != "incomplete" || row.Outcome != "unknown" {
			t.Errorf("row %s: state %q outcome %q, want an unfinished unknown-outcome row", row.ID, row.State, row.Outcome)
		}
		if i == 0 {
			continue
		}
		prev := oldest[i-1]
		if row.InitiatedAt.Before(*prev.InitiatedAt) || (row.InitiatedAt.Equal(*prev.InitiatedAt) && row.ID <= prev.ID) {
			t.Fatalf("oldest-first order broken between %s and %s", prev.ID, row.ID)
		}
		// Newest is exactly the reverse of oldest.
		if newest[len(newest)-1-i].ID != row.ID {
			t.Fatalf("newest is not the reverse of oldest at %d: %s vs %s", i, newest[len(newest)-1-i].ID, row.ID)
		}
	}
	if pending := all("oldest", "pending"); len(pending) != 6 {
		t.Fatalf("pending list = %d rows, want 6 (3 per source)", len(pending))
	}
	// Only an incomplete completion filter (or an age range) may ask for oldest first.
	plain := demoQuery(now)
	if _, err := d.Requests(ctx, PageInput{Query: plain, Order: "oldest", Limit: 5}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oldest without an incomplete filter = %v, want invalid input", err)
	}
	if _, err := d.Requests(ctx, PageInput{Query: q, Order: "sideways", Limit: 5}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an unknown order = %v, want invalid input", err)
	}
	aged := demoQuery(now)
	ten := float64(10 * time.Minute / time.Millisecond)
	aged.IncompleteAgeMs = &Range{Min: &ten}
	page, err := d.Requests(ctx, PageInput{Query: aged, Order: "oldest", Limit: 5})
	if err != nil || len(page.Rows) == 0 {
		t.Fatalf("an age range allows oldest first: rows=%d err=%v", len(page.Rows), err)
	}
	for _, row := range page.Rows {
		if age := now.Sub(*row.InitiatedAt); age < 10*time.Minute {
			t.Errorf("row %s is %s old, under the 10 minute threshold", row.ID, age)
		}
	}
	// A cursor belongs to the order that made it.
	first, err := d.Requests(ctx, PageInput{Query: q, Order: "oldest", Limit: 5})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first oldest page: cursor=%q err=%v", first.NextCursor, err)
	}
	if _, err := d.Requests(ctx, PageInput{Query: q, Order: "newest", Cursor: first.NextCursor, Limit: 5}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an oldest cursor under newest = %v, want invalid input", err)
	}
	if _, err := d.Requests(ctx, PageInput{Query: q, Order: "oldest", Cursor: first.NextCursor, Limit: 5}); err != nil {
		t.Fatalf("an oldest cursor under oldest: %v", err)
	}
}

func TestDemoWithStaleWorkReportsCompletionTalliesAndKeepsTheBaseDemoClean(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	// The plain demo has nothing unfinished, which other tests rely on.
	base, err := NewDemo(now).Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range base.APIs {
		if c.Current.Incomplete != 0 || c.Current.Pending != 0 || c.Current.Invalid != 0 {
			t.Fatalf("the base demo must stay all complete: %#v", c.Current)
		}
	}
	analysis, err := NewDemoWithStaleWork(now).Analyze(context.Background(), demoQuery(now))
	if err != nil {
		t.Fatal(err)
	}
	var run, generate, list *Rollup
	for i := range analysis.Rollups {
		switch analysis.Rollups[i].Action {
		case "Run":
			run = &analysis.Rollups[i]
		case "Generate":
			generate = &analysis.Rollups[i]
		case "List":
			list = &analysis.Rollups[i]
		}
	}
	if run == nil || generate == nil || list == nil {
		t.Fatalf("rollups = %#v", analysis.Rollups)
	}
	if run.Incomplete != 6 || run.Pending != 0 || run.CompletionShare == nil || *run.CompletionShare >= 1 {
		t.Fatalf("Payroll Run should have unfinished requests: %#v share=%v", run, run.CompletionShare)
	}
	if generate.Pending != 6 || generate.Incomplete != 0 {
		t.Fatalf("Reports Generate has 6 pending asynchronous requests and no other unfinished ones: %#v", generate)
	}
	if list.Invalid != 2 || list.Incomplete != 0 {
		t.Fatalf("Employees List should show invalid rows apart from incomplete: %#v", list)
	}
	// Invalid rows never enter the denominator: List has only complete + invalid, so its share is 1.
	if list.CompletionShare == nil || *list.CompletionShare != 1 {
		t.Fatalf("List completion share = %v, want 1 (invalid excluded)", list.CompletionShare)
	}
}
