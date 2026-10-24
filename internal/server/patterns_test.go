package server

import (
	"api-audit-explorer/internal/audit"
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
