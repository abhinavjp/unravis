package server

import (
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func importedPartialLane(t *testing.T, e *caseEnv, id, key string) tracingLane {
	t.Helper()
	res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d %s", res.Code, res.Body)
	}
	var out tracingResponse
	decodeBody(t, res, &out)
	return tracingLaneOf(t, out, key)
}

// DATA-13: every imported file has no recorded time range, so no range is known. The
// lane must still say its files are not fully described, not stay silent.
func TestTracingLaneIsPartialWhenNoImportedFileHasARange(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingEvents(2))
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		l := importedPartialLane(t, e, id, "iis")
		if l.ImportedFrom != nil || l.ImportedTo != nil {
			t.Fatalf("a range was sent for files with none: %v..%v", l.ImportedFrom, l.ImportedTo)
		}
		if !l.ImportedPartial {
			t.Error("lane with only range-less files is not marked partial")
		}
	})
}

// DATA-13: one file is imported with a range, another listed file was never imported.
// The range shown is not the whole of what the owner listed, so the lane is partial.
func TestTracingLaneIsPartialWhenAListedFileWasNeverImported(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		first := time.Date(2001, 2, 3, 4, 10, 0, 0, time.UTC)
		last := time.Date(2001, 2, 3, 4, 20, 0, 0, time.UTC)
		imp := seedSpannedImport(t, e.store, id, "iis-w3c", "a.log", tracingEventsAt(first, 2), &first, &last)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "b.log"}) // listed, never imported
		l := importedPartialLane(t, e, id, "iis")
		if l.ImportedFrom == nil || !l.ImportedFrom.Equal(first) || l.ImportedTo == nil || !l.ImportedTo.Equal(last) {
			t.Fatalf("imported span = %v..%v, want %v..%v", l.ImportedFrom, l.ImportedTo, first, last)
		}
		if !l.ImportedPartial {
			t.Error("lane with a never-imported listed file is not marked partial")
		}
	})
}

// Control: every listed file is imported and has a range, so the lane is not partial.
func TestTracingLaneIsNotPartialWhenEveryFileHasARange(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		first := time.Date(2001, 2, 3, 4, 10, 0, 0, time.UTC)
		last := time.Date(2001, 2, 3, 4, 20, 0, 0, time.UTC)
		imp := seedSpannedImport(t, e.store, id, "iis-w3c", "a.log", tracingEventsAt(first, 2), &first, &last)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		if l := importedPartialLane(t, e, id, "iis"); l.ImportedPartial {
			t.Error("fully described lane is marked partial")
		}
	})
}
