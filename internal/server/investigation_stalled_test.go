package server

import (
	"net/http"
	"testing"

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
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{first, stalled}})
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
	if out.Result.Steps[0].Readiness != investigate.CannotTestYet && out.Result.Steps[0].Readiness != investigate.Failed {
		t.Errorf("step 0 = %q after a cut read", out.Result.Steps[0].Readiness)
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
