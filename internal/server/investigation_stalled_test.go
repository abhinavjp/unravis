package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/investigate"
)

// A source that sends rows, then an empty page that still has a cursor, has not been read to the end. The run must say so
// (Truncated) and use none of the rows, with its own sentence: it is not "too many calls" (RV-SRV-11).
func TestInvestigationEmptyPageWithACursorMarksTheReadCut(t *testing.T) {
	first := okPage(invRows())
	first.NextCursor = "more"
	stalled := okPage(nil)
	stalled.NextCursor = "still-more"
	repo := &fakeRepository{requestPages: []audit.Page{first, stalled}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if !out.Read.Truncated || out.Read.Requests != 0 {
		t.Fatalf("read = %+v, want a cut read with no calls used", out.Read)
	}
	if len(out.Read.Warnings) != 1 || out.Read.Warnings[0] != investigationStalledText {
		t.Errorf("warnings = %q, want the stopped-part-way sentence", out.Read.Warnings)
	}
	if out.Result.Steps[0].Readiness != investigate.CannotTestYet {
		t.Errorf("step 0 = %q after a cut read, want %q", out.Result.Steps[0].Readiness, investigate.CannotTestYet)
	}
	if repo.requestCalls != 2 {
		t.Errorf("source asked %d times, want 2 (the stall page ends the read)", repo.requestCalls)
	}
}

// A page the source itself marked cut is a real limit. If an empty page with a cursor follows it, the warning still names the
// limit ("too many calls"), not the stall: the earlier cause is the one the person can act on (shorten the windows).
func TestInvestigationRealLimitCauseSurvivesALaterEmptyPage(t *testing.T) {
	first := okPage(invRows())
	first.NextCursor = "more"
	first.Truncated = true
	stalled := okPage(nil)
	stalled.NextCursor = "still-more"
	repo := &fakeRepository{requestPages: []audit.Page{first, stalled}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if !out.Read.Truncated || out.Read.Requests != 0 {
		t.Fatalf("read = %+v, want a cut read with no calls used", out.Read)
	}
	if len(out.Read.Warnings) != 1 || out.Read.Warnings[0] == investigationStalledText || !strings.Contains(out.Read.Warnings[0], "too many audit calls") {
		t.Errorf("warnings = %q, want the too-many-calls sentence, not the stalled one", out.Read.Warnings)
	}
	if repo.requestCalls != 2 {
		t.Errorf("source asked %d times, want 2 (the stall page ends the read)", repo.requestCalls)
	}
}

// A source that stalls on its very first page (nothing read yet) is the same cut read: one question, no calls used, the
// stopped-part-way sentence, and the first step cannot be tested.
func TestInvestigationStallOnTheFirstPageMarksTheReadCut(t *testing.T) {
	stalled := okPage(nil)
	stalled.NextCursor = "more"
	repo := &fakeRepository{requestPages: []audit.Page{stalled}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(t, res, &out)
	if !out.Read.Truncated || out.Read.Requests != 0 {
		t.Fatalf("read = %+v, want a cut read with no calls used", out.Read)
	}
	if len(out.Read.Warnings) != 1 || out.Read.Warnings[0] != investigationStalledText {
		t.Errorf("warnings = %q, want the stopped-part-way sentence", out.Read.Warnings)
	}
	if out.Result.Steps[0].Readiness != investigate.CannotTestYet {
		t.Errorf("step 0 = %q, want %q", out.Result.Steps[0].Readiness, investigate.CannotTestYet)
	}
	if repo.requestCalls != 1 {
		t.Errorf("source asked %d times, want 1", repo.requestCalls)
	}
}

// The CSV export loop had the same hole: a source that keeps answering "more" with no rows made it ask without end.
// It now stops, and the file says it is not whole (X-Export-Truncated).
func TestExportStopsOnAnEmptyPageWithACursor(t *testing.T) {
	stalled := okPage(nil)
	stalled.NextCursor = "more"
	repo := &fakeRepository{requestPages: []audit.Page{stalled}}
	h := New(repo, nil, testToken, false)
	input, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: audit.MaxPageSize})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(h, http.MethodPost, "/api/export", string(input), true) }()
	select {
	case res := <-done:
		if res.Code != http.StatusOK || res.Header().Get("X-Export-Truncated") != "true" {
			t.Fatalf("status=%d headers=%#v, want 200 marked truncated", res.Code, res.Header())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the export kept asking a source that sends empty pages")
	}
}

// An empty last page with no cursor is a normal end: nothing is cut.
func TestInvestigationEmptyLastPageIsNotCut(t *testing.T) {
	first := okPage(invRows())
	first.NextCursor = "more"
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{first, okPage(nil)}})
	id := e.newCase("inv")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	var out investigationAnswer
	decodeBody(t, res, &out)
	if res.Code != http.StatusOK || out.Read.Truncated || out.Read.Requests == 0 {
		t.Fatalf("code %d read = %+v, want a whole read", res.Code, out.Read)
	}
}
