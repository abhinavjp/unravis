package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
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
