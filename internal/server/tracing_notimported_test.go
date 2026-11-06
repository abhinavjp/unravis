package server

import (
	"net/http"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// The owner's case listed eight IIS files and an SQL file that were only
// previewed, never imported. The lanes then said only "no file", as if the list
// were wrong. They must say the files were not imported, and the sources must be
// flagged so the panel can show it.
func TestTracingLaneSaysFilesWereNotImported(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "sql-xel", Label: "File · 192.168.4.2:1655"})
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex261021.log"})
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex261022.log"})

		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != http.StatusOK {
			t.Fatalf("status %d %s", res.Code, res.Body)
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		for _, key := range []string{"sql", "iis"} {
			l := tracingLaneOf(t, out, key)
			if l.State != "no-data" || l.ReasonCode != tracingReasonNotImported || l.Reason != tracingNotImportedMsg {
				t.Errorf("%s lane = %q %q %q, want no-data %q", key, l.State, l.ReasonCode, l.Reason, tracingReasonNotImported)
			}
		}
		if len(out.Sources) != 3 {
			t.Fatalf("sources = %+v", out.Sources)
		}
		for _, s := range out.Sources {
			if !s.NoFile {
				t.Errorf("%s is listed as traced but has no import", s.Label)
			}
		}
	})
}

// A lane that holds an imported file keeps no reason, and only the sources
// without an import are flagged.
func TestTracingImportedLaneHasNoNotImportedReason(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingEvents(2))
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "sql-xel", Label: "never imported"})

		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		var out tracingResponse
		decodeBody(t, res, &out)
		if l := tracingLaneOf(t, out, "iis"); l.State == "no-data" || l.ReasonCode != "" || l.Reason != "" {
			t.Errorf("iis lane = %q %q %q, want a read lane with no reason", l.State, l.ReasonCode, l.Reason)
		}
		if l := tracingLaneOf(t, out, "sql"); l.State != "no-data" || l.ReasonCode != tracingReasonNotImported {
			t.Errorf("sql lane = %q %q, want no-data not-imported", l.State, l.ReasonCode)
		}
		flagged := 0
		for _, s := range out.Sources {
			if s.NoFile {
				flagged++
				if s.Label != "never imported" {
					t.Errorf("%s flagged but is imported", s.Label)
				}
			}
		}
		if flagged != 1 {
			t.Errorf("flagged %d sources, want 1: %+v", flagged, out.Sources)
		}
	})
}
