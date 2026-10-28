package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// Review 2 of the scenario fixes.

// reviewReportOutputs builds a case whose title, a checklist note and a saved view
// name carry the text, and returns the html and csv reports.
func reviewReportOutputs(t *testing.T, text string) map[string]string {
	t.Helper()
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("Outage " + text)
	body, _ := json.Marshal(map[string]string{"list": "todo", "text": "note " + text})
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", string(body)); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	view, _ := json.Marshal(map[string]any{"name": "view " + text, "tool": "slowest-apis", "settings": map[string]any{"minMs": 500}})
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/views", string(view)); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	out := map[string]string{}
	for _, format := range []string{"html", "csv"} {
		out[format] = reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", reportBody(format, ""))).Content
	}
	return out
}

// Review2-2: a secret in JSON form is taken out of a title, a note and a view name.
func TestScn1Review2JSONSecretsInReport(t *testing.T) {
	for text, leaks := range map[string][]string{
		`{"password":"jsonhunter"}`:  {"jsonhunter"},
		`"apiKey":"jsonkey1"`:        {"jsonkey1"},
		`{'pwd': 'single quoted'}`:   {"single quoted"},
		`{"token":"jsontoken"}`:      {"jsontoken"},
		`api_key: colonkey1 was set`: {"colonkey1"},
	} {
		for format, content := range reviewReportOutputs(t, text) {
			for _, leak := range leaks {
				if strings.Contains(content, leak) {
					t.Errorf("%s report for %q still carries %q", format, text, leak)
				}
			}
		}
	}
	// The word alone, or "token: expired", is not a secret.
	if got := scrubTypedNote("token: expired"); got != "token: expired" {
		t.Errorf("scrubTypedNote left %q", got)
	}
}

// Review2-3: an Authorization Basic credential and user:pass@host are taken out;
// a plain email address is not.
func TestScn1Review2BasicAndSchemelessCredentials(t *testing.T) {
	for in, leak := range map[string]string{
		"sent Authorization: Basic dXNlcjpwdw== to the api": "dXNlcjpwdw",
		"connect as admin:hunter2@db01 failed":              "hunter2",
	} {
		if got := scrubTypedNote(in); strings.Contains(got, leak) || !strings.Contains(got, "[removed]") {
			t.Errorf("scrubTypedNote(%q) = %q", in, got)
		}
	}
	for _, kept := range []string{"mail a@b.com about it", "contact first.last@corp.example today"} {
		if got := scrubTypedNote(kept); got != kept {
			t.Errorf("scrubTypedNote(%q) = %q, want it unchanged", kept, got)
		}
	}
}

// Review2-5: a findings preview built from a case ledger is dropped when the case
// is erased, so Confirm cannot save the erased case's text.
func TestScn1Review2ErasedCaseDropsLedgerPreview(t *testing.T) {
	fb := installExportFakes(t)
	store := casestore.NewMemory()
	exportNow = time.Now().UTC().Truncate(time.Second)
	h := New(audit.NewDemoWithPatterns(exportNow), nil, testToken, true,
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithCaptureCases(), WithLedger(&fakeLedger{}))
	e := &caseEnv{t: t, store: store, h: h}
	body := exportBody("demo-west")
	if r := request(h, http.MethodPost, "/api/export/preview", body, true); r.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", r.Code, r.Body.String())
	}
	related := fb.inputs[0].Report.Findings[0].EvidenceID

	id := e.newCase("erase me")
	th := createCaseTheory(t, e, id, "Slow calls follow the nightly job")
	upd := `{"text":"Slow calls follow the nightly job","state":"supported_by_correlation","evidenceIds":["` + related + `"],"notes":"seen twice","settlingObservation":"Run with the job paused","facts":{"presentK":2,"totalN":3}}`
	if res := e.json(http.MethodPut, caseLedgerPath(id)+"/"+th.ID, upd); res.Code != http.StatusOK {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}
	var withCase map[string]any
	if err := json.Unmarshal([]byte(body), &withCase); err != nil {
		t.Fatal(err)
	}
	withCase["ledgerCaseId"] = id
	raw, _ := json.Marshal(withCase)
	r := request(h, http.MethodPost, "/api/export/preview", string(raw), true)
	if r.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", r.Code, r.Body.String())
	}
	var p struct {
		PreviewID string `json:"previewId"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil || p.PreviewID == "" {
		t.Fatalf("no preview id: %v %s", err, r.Body.String())
	}
	if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+q(id)+`}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	c := request(h, http.MethodPost, "/api/export/confirm", `{"previewId":"`+p.PreviewID+`"}`, true)
	if c.Code != http.StatusNotFound {
		t.Errorf("confirm after erase = %d %s, want 404", c.Code, c.Body.String())
	}
}

// Review2-6: a saved view with a null for a checked key is refused like any other bad value.
func TestScn1Review2SavedViewRefusesNull(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/views"
		for _, settings := range []string{
			`{"incidentStart":null,"minCalls":null,"route":null}`,
			`{"incidentStart":null}`, `{"incidentEnd":null}`, `{"referenceStart":null}`, `{"referenceEnd":null}`,
			`{"minCalls":null}`, `{"route":null}`, `{"match":null}`,
		} {
			res := e.json(http.MethodPost, base, `{"name":"v","tool":"slowest-apis","settings":`+settings+`}`)
			if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "plan-invalid" {
				t.Errorf("settings %s = %d %s, want 400 plan-invalid", settings, res.Code, res.Body)
			}
		}
	})
}
