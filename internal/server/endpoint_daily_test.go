package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
)

// liveAnalyze reports every Analyze read as live: no documents-examined count.
type liveAnalyze struct{ audit.Repository }

func (l liveAnalyze) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	out, err := l.Repository.Analyze(ctx, q)
	out.Provenance = append([]audit.Provenance(nil), out.Provenance...)
	for i := range out.Provenance {
		out.Provenance[i].DocsExamined = nil
	}
	return out, err
}

// liveAll is liveDemo (live page reads) with live Analyze reads too.
type liveAll struct{ liveDemo }

func (l liveAll) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	return liveAnalyze{l.liveDemo.Repository}.Analyze(ctx, q)
}

func dailyPath(op, sources, tz string, start, end time.Time) string {
	v := url.Values{}
	v.Set("op", op)
	v.Set("sources", sources)
	v.Set("start", start.Format(time.RFC3339))
	v.Set("end", end.Format(time.RFC3339))
	if tz != "" {
		v.Set("tz", tz)
	}
	return "/api/endpoint-daily?" + v.Encode()
}

func TestEndpointDailyOnDemo(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true)
	midnight := now.Truncate(24 * time.Hour)
	start, end := midnight.AddDate(0, 0, -30), midnight.AddDate(0, 0, -10)
	r := request(h, http.MethodGet, dailyPath("Invoices.Search", "demo-west", "", start, end), "", true)
	if r.Code != http.StatusOK {
		t.Fatalf("status %d %s", r.Code, r.Body.String())
	}
	var out audit.EndpointDaily
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Operation != "Invoices.Search" || out.Zone != "UTC" || len(out.Days) != 20 {
		t.Fatalf("%+v", out)
	}
	var before, after float64
	for _, d := range out.Days {
		if d.Count != 22 || d.P95Ms == nil {
			t.Fatalf("day %+v", d)
		}
		if d.Date == midnight.AddDate(0, 0, -21).Format("2006-01-02") {
			before = *d.P95Ms
		}
		if d.Date == midnight.AddDate(0, 0, -20).Format("2006-01-02") {
			after = *d.P95Ms
		}
	}
	if before == 0 || after < 2*before {
		t.Fatalf("the step shows in the series: before %v after %v", before, after)
	}
	// The raw JSON keeps nulls as null, not zero.
	var raw map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil || raw["warnings"] == nil {
		t.Fatalf("warnings must be an array: %s", r.Body.String())
	}
}

func TestEndpointDailyRejectsBadInput(t *testing.T) {
	h := New(audit.NewDemoWithPatterns(time.Now()), nil, testToken, true)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	good := dailyPath("Invoices.Search", "demo-west", "", start, start.AddDate(0, 0, 5))
	if r := request(h, http.MethodGet, good, "", true); r.Code != http.StatusOK {
		t.Fatalf("baseline %d %s", r.Code, r.Body.String())
	}
	for name, path := range map[string]string{
		"no params":      "/api/endpoint-daily",
		"bad start":      "/api/endpoint-daily?op=A.B&sources=demo-west&start=yesterday&end=2026-03-05T00:00:00Z",
		"no op":          dailyPath("", "demo-west", "", start, start.AddDate(0, 0, 5)),
		"op without dot": dailyPath("Invoices", "demo-west", "", start, start.AddDate(0, 0, 5)),
		"no sources":     dailyPath("Invoices.Search", "", "", start, start.AddDate(0, 0, 5)),
		"bad zone":       dailyPath("Invoices.Search", "demo-west", "Nowhere/Land", start, start.AddDate(0, 0, 5)),
		"too long":       dailyPath("Invoices.Search", "demo-west", "", start, start.AddDate(0, 0, 130)),
		"backward":       dailyPath("Invoices.Search", "demo-west", "", start.AddDate(0, 0, 5), start),
	} {
		r := request(h, http.MethodGet, path, "", true)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, r.Code, r.Body.String())
		}
	}
	if r := request(h, http.MethodGet, good, "", false); r.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", r.Code)
	}
	if r := request(h, http.MethodPost, good, "", true); r.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", r.Code)
	}
}

func TestEndpointDailyLiveSourceIsRefusedBeyondFourteenDays(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	h := New(liveAnalyze{audit.NewDemoWithPatterns(now)}, nil, testToken, true)
	midnight := now.Truncate(24 * time.Hour)
	r := request(h, http.MethodGet, dailyPath("Invoices.Search", "demo-west", "", midnight.AddDate(0, 0, -15), midnight), "", true)
	if r.Code != http.StatusConflict {
		t.Fatalf("status %d %s", r.Code, r.Body.String())
	}
	var body struct{ Error, Code string }
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || body.Code != "broad_scan_refused" || body.Error != endpointDailyRefused {
		t.Fatalf("body %s (%v)", r.Body.String(), err)
	}
	r = request(h, http.MethodGet, dailyPath("Invoices.Search", "demo-west", "", midnight.AddDate(0, 0, -14), midnight), "", true)
	if r.Code != http.StatusOK {
		t.Fatalf("14 days = %d %s", r.Code, r.Body.String())
	}
}

// B9 and the audit series ride on /api/patterns: the report carries the fields
// the page reads, the split is a suggestion in the UTC zone, and the evidence ids
// are accepted by the ledger.
func TestPatternsStepChangeAndAuditSeriesContract(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	store, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true, WithLedger(store))
	midnight := now.Truncate(24 * time.Hour)
	in := audit.PatternInput{
		SourceIDs: []string{"demo-west"}, Start: now.Add(-7 * 24 * time.Hour), End: now, TimeZone: "UTC",
		StepChange:  &audit.StepChangeInput{Start: midnight.AddDate(0, 0, -40), End: midnight.AddDate(0, 0, -8)},
		AuditSeries: true,
	}
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("status %d %s", r.Code, r.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	var step map[string]json.RawMessage
	if err := json.Unmarshal(raw["stepChange"], &step); err != nil {
		t.Fatalf("stepChange %s (%v)", raw["stepChange"], err)
	}
	for _, k := range []string{"zone", "start", "end", "daysRead", "daysLeftOut", "operations", "date", "sharing", "split", "partial"} {
		if _, ok := step[k]; !ok {
			t.Fatalf("stepChange lacks %q: %s", k, raw["stepChange"])
		}
	}
	var series map[string]json.RawMessage
	if err := json.Unmarshal(raw["auditSeries"], &series); err != nil {
		t.Fatalf("auditSeries %s (%v)", raw["auditSeries"], err)
	}
	for _, k := range []string{"zone", "days", "band", "excludedHangfire", "withoutAudit", "partial"} {
		if _, ok := series[k]; !ok {
			t.Fatalf("auditSeries lacks %q: %s", k, raw["auditSeries"])
		}
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.StepChange == nil || report.StepChange.Split == nil || report.StepChange.Split.Zone != "UTC" {
		t.Fatalf("%+v", report.StepChange)
	}
	var ids []string
	for _, f := range report.Findings {
		if f.Rule == "B9" || f.Rule == "B6f" {
			ids = append(ids, f.EvidenceID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("findings %+v", report.Findings)
	}
	created, err := store.Create("Slowness steps up on one date")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ledger.Input{Text: created.Text, State: ledger.StateOpen, EvidenceIDs: ids})
	if r := request(h, http.MethodPut, "/api/ledger/"+created.ID, string(body), true); r.Code != http.StatusOK {
		t.Fatalf("ledger update with %v = %d %s", ids, r.Code, r.Body.String())
	}
}

func TestPatternsStepChangeInvalidInputIs400AndLiveIsAWarning(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	midnight := now.Truncate(24 * time.Hour)
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true)
	in := audit.PatternInput{
		SourceIDs: []string{"demo-west"}, Start: now.Add(-4 * time.Hour), End: now,
		StepChange: &audit.StepChangeInput{Start: midnight.AddDate(0, 0, -200), End: midnight},
	}
	if r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true); r.Code != http.StatusBadRequest {
		t.Fatalf("200 days = %d %s", r.Code, r.Body.String())
	}
	in.StepChange = &audit.StepChangeInput{Start: midnight.AddDate(0, 0, -40), End: midnight.AddDate(0, 0, -8)}
	in.TimeZone = "Nowhere/Land"
	if r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true); r.Code != http.StatusBadRequest {
		t.Fatalf("bad zone = %d %s", r.Code, r.Body.String())
	}
	// A live source answers 200 with a warning and no step, so the other rules stay.
	in.TimeZone = ""
	live := New(liveAll{liveDemo{audit.NewDemoWithPatterns(now)}}, nil, testToken, true)
	r := request(live, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("live = %d %s", r.Code, r.Body.String())
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.StepChange != nil {
		t.Fatalf("a live source must not run B9: %+v", report.StepChange)
	}
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "Step dating") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings %q", report.Warnings)
	}
}
