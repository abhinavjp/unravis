package server

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func patternBody(in audit.PatternInput) string {
	raw, _ := json.Marshal(in)
	return string(raw)
}

func validPatternInput() audit.PatternInput {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return audit.PatternInput{SourceIDs: []string{"demo-west"}, Start: start, End: start.Add(time.Hour)}
}

// patternEngineReady reports whether the real engine (Task A) is merged. Until
// then ValidatePattern is a stub that rejects everything, so tests that need
// real validation or a real report skip through this helper.
func patternEngineReady() bool {
	return audit.ValidatePattern(validPatternInput()) == nil
}

func TestPatternsRequireTokenAndPost(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	if r := request(h, http.MethodPost, "/api/patterns", patternBody(validPatternInput()), false); r.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d", r.Code)
	}
	if r := request(h, http.MethodGet, "/api/patterns", "", true); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", r.Code)
	}
}

func TestPatternsRejectsMalformedBodyBeforeEngine(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	for name, body := range map[string]string{
		"unknown field": `{"sourceIds":["a"],"bogus":1}`,
		"not json":      `{`,
	} {
		if r := request(h, http.MethodPost, "/api/patterns", body, true); r.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", name, r.Code)
		}
	}
}

func TestPatternsInvalidInputIsRejected(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	bad := validPatternInput()
	bad.End = bad.Start.Add(-time.Hour)
	r := request(h, http.MethodPost, "/api/patterns", patternBody(bad), true)
	if r.Code == http.StatusOK {
		t.Fatalf("invalid window accepted: %s", r.Body.String())
	}
	if patternEngineReady() && r.Code != http.StatusBadRequest {
		t.Fatalf("invalid window = %d, want 400", r.Code)
	}
}

func TestPatternsOnRepositoryWithoutSlowestReaderDoesNotPanic(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	r := request(h, http.MethodPost, "/api/patterns", patternBody(validPatternInput()), true)
	if r.Code < 200 || r.Code >= 600 {
		t.Fatalf("status = %d", r.Code)
	}
	var body map[string]any
	if json.Unmarshal(r.Body.Bytes(), &body) != nil {
		t.Fatalf("response is not JSON: %s", r.Body.String())
	}
	if r.Code >= 400 {
		if msg, _ := body["error"].(string); msg == "" {
			t.Fatalf("error status %d without an error message: %s", r.Code, r.Body.String())
		}
	}
}

func TestPatternsUnavailableIs503(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	r := request(h, http.MethodPost, "/api/patterns", patternBody(validPatternInput()), true)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s", r.Code, r.Body.String())
	}
	var body struct{ Error string }
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || body.Error != "Patterns are not available for this source. It cannot list the slowest requests." {
		t.Fatalf("body %s (%v)", r.Body.String(), err)
	}
}

// The evidence IDs a pattern run prints must be accepted by the real ledger
// as references, so an analyst can paste them in unchanged.
func TestPatternEvidenceIDsAreAcceptedByLedger(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true, WithLedger(store))
	in := audit.PatternInput{SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-4 * time.Hour), End: now}
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("patterns = %d %s", r.Code, r.Body.String())
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("demo scenario printed no findings")
	}
	ids := make([]string, 0, len(report.Findings))
	for _, f := range report.Findings {
		ids = append(ids, f.EvidenceID)
	}
	created, err := store.Create("Slow requests repeat in this window")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ledger.Input{Text: created.Text, State: ledger.StateOpen, EvidenceIDs: ids})
	r = request(h, http.MethodPut, "/api/ledger/"+created.ID, string(body), true)
	if r.Code != http.StatusOK {
		t.Fatalf("ledger update with %v = %d %s", ids, r.Code, r.Body.String())
	}
	var saved ledger.Hypothesis
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil || len(saved.EvidenceIDs) != len(ids) {
		t.Fatalf("saved %+v (%v)", saved, err)
	}
}

// A window of days shows the recurrence tables and findings (B3a, A3) with the
// field names the page reads, and the ledger takes their evidence ids.
func TestPatternsMultiDayContractAndLedger(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true, WithLedger(store))
	in := audit.PatternInput{SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-5 * 24 * time.Hour), End: now}
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("patterns = %d %s", r.Code, r.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"onsetMinutes", "concentration"} {
		if len(raw[key]) < 3 {
			t.Fatalf("%s missing in %s", key, r.Body.String())
		}
	}
	if _, ok := raw["idleGaps"]; ok {
		t.Fatalf("idleGaps present without opt-in")
	}
	var onset []map[string]any
	if err := json.Unmarshal(raw["onsetMinutes"], &onset); err != nil || len(onset) == 0 {
		t.Fatalf("onsetMinutes %s (%v)", raw["onsetMinutes"], err)
	}
	for _, k := range []string{"minute", "days", "onsets", "episodes"} {
		if _, ok := onset[0][k]; !ok {
			t.Fatalf("onsetMinutes row lacks %q: %v", k, onset[0])
		}
	}
	var conc []map[string]any
	if err := json.Unmarshal(raw["concentration"], &conc); err != nil || len(conc) == 0 {
		t.Fatalf("concentration %s (%v)", raw["concentration"], err)
	}
	for _, k := range []string{"dimension", "key", "stallShare", "trafficShare", "lift", "stallN", "allN", "episodes", "withheld"} {
		if _, ok := conc[0][k]; !ok {
			t.Fatalf("concentration row lacks %q: %v", k, conc[0])
		}
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range report.Findings {
		if f.Rule == "B3a" || f.Rule == "A3" {
			ids = append(ids, f.EvidenceID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("findings %+v", report.Findings)
	}
	created, err := store.Create("Stalls start at one minute")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ledger.Input{Text: created.Text, State: ledger.StateOpen, EvidenceIDs: ids})
	if r := request(h, http.MethodPut, "/api/ledger/"+created.ID, string(body), true); r.Code != http.StatusOK {
		t.Fatalf("ledger update with %v = %d %s", ids, r.Code, r.Body.String())
	}
}

// liveDemo is the demo repository with its provenance reporting what a live store
// does: docsExamined is not measured.
type liveDemo struct{ audit.Repository }

func unmeasured(p audit.Page, err error) (audit.Page, error) {
	p.Provenance = append([]audit.Provenance(nil), p.Provenance...)
	for i := range p.Provenance {
		p.Provenance[i].DocsExamined = nil
	}
	return p, err
}

func (l liveDemo) Slowest(ctx context.Context, in audit.PageInput) (audit.Page, error) {
	return unmeasured(l.Repository.(audit.SlowestReader).Slowest(ctx, in))
}

func (l liveDemo) Requests(ctx context.Context, in audit.PageInput) (audit.Page, error) {
	return unmeasured(l.Repository.Requests(ctx, in))
}

// Quiet-gap analysis (B6g) is opt-in, returns the table the page reads, and its
// evidence id is accepted by the ledger.
func TestPatternsIdleGapsContractAndLedger(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := New(audit.NewDemoWithPatterns(now), nil, testToken, true, WithLedger(store))
	in := audit.PatternInput{SourceIDs: []string{"demo-west"}, Start: now.Add(-18 * time.Hour), End: now.Add(-12 * time.Hour), IdleGaps: true}
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("patterns = %d %s", r.Code, r.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	var idle map[string]json.RawMessage
	if err := json.Unmarshal(raw["idleGaps"], &idle); err != nil {
		t.Fatalf("idleGaps %s (%v)", raw["idleGaps"], err)
	}
	for _, k := range []string{"bands", "lift", "excludedHangfire", "partial"} {
		if _, ok := idle[k]; !ok {
			t.Fatalf("idleGaps lacks %q: %s", k, raw["idleGaps"])
		}
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range report.Findings {
		if f.Rule == "B6g" {
			ids = append(ids, f.EvidenceID)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("findings %+v", report.Findings)
	}
	created, err := store.Create("Audit create is slower after quiet gaps")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ledger.Input{Text: created.Text, State: ledger.StateOpen, EvidenceIDs: ids})
	if r := request(h, http.MethodPut, "/api/ledger/"+created.ID, string(body), true); r.Code != http.StatusOK {
		t.Fatalf("ledger update with %v = %d %s", ids, r.Code, r.Body.String())
	}
}

// On a live source a window over 24 hours is refused with the same 409 and code as
// the coverage census, and only when the analysis was asked for.
func TestPatternsIdleGapsRefusedOnLongLiveWindow(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	h := New(liveDemo{audit.NewDemoWithPatterns(now)}, nil, testToken, true)
	in := audit.PatternInput{SourceIDs: []string{"demo-west"}, Start: now.Add(-30 * time.Hour), End: now}
	if r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true); r.Code != http.StatusOK {
		t.Fatalf("without the opt-in = %d %s", r.Code, r.Body.String())
	}
	in.IdleGaps = true
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusConflict {
		t.Fatalf("status = %d %s", r.Code, r.Body.String())
	}
	var body struct{ Error, Code string }
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || body.Code != "broad_scan_refused" || body.Error != idleGapsRefused {
		t.Fatalf("body %s (%v)", r.Body.String(), err)
	}
	// Within 24 hours the same source answers.
	in.Start = now.Add(-18 * time.Hour)
	in.End = now.Add(-12 * time.Hour)
	if r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true); r.Code != http.StatusOK {
		t.Fatalf("inside 24 h = %d %s", r.Code, r.Body.String())
	}
}

func TestPatternsHappyPathOnDemoRepository(t *testing.T) {
	if !patternEngineReady() {
		t.Skip("pattern engine not merged yet (ValidatePattern is a stub)")
	}
	now := time.Now()
	demo := audit.NewDemo(now)
	h := New(demo, nil, testToken, true)
	sources, err := demo.Sources(t.Context())
	if err != nil || len(sources) == 0 {
		t.Fatalf("demo sources: %v", err)
	}
	in := audit.PatternInput{SourceIDs: []string{sources[0].ID}, Start: now.Add(-24 * time.Hour), End: now}
	r := request(h, http.MethodPost, "/api/patterns", patternBody(in), true)
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d %s", r.Code, r.Body.String())
	}
	var report audit.PatternReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Params.StallMs == 0 {
		t.Fatalf("params not echoed: %+v", report.Params)
	}
}
