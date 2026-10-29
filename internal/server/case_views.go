package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/timeline"
)

// Views over one case that need the audit repository or the evidence engine:
// the cross-source timeline and the redacted case export.

/* ------------------------------------------------------------ case view */

// getCase answers GET /api/cases/{id}: the case as the list shows it plus the
// number of environments and source tags. The counts come from the opened case
// (the list reads the manifest only and cannot know them). A malformed id
// answers the same 404 as a missing case.
func (h *handler) getCase(w http.ResponseWriter, r *http.Request, raw string) {
	if _, err := casestore.ParseID(raw); err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return
	}
	if h.recordCaseView(w, r, raw) {
		return
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	h.event(r, "case_opened")
	writeJSON(w, http.StatusOK, h.viewOf(r.Context(), c), maxResponseBytes)
}

// recordCaseView answers the case view of an archived or missing case from its
// record, since it has no working copy to open. It returns false for any other
// case, which the caller opens as usual. A mark still there after recovery answers
// M19 or M5.
func (h *handler) recordCaseView(w http.ResponseWriter, r *http.Request, raw string) bool {
	ar, ok := h.cases.(casestore.Archiver)
	if !ok {
		return false
	}
	id, err := casestore.ParseID(raw)
	if err != nil {
		return false
	}
	info, err := ar.Entry(r.Context(), id)
	if err != nil || !(info.Missing || (info.Archive != nil && info.Archive.Active)) {
		return false
	}
	info = h.settle(r.Context(), info)
	if err := casestore.CheckMark(info.Mark); err != nil {
		writeStoreError(w, err)
		return true
	}
	writeJSON(w, http.StatusOK, recordView(info), maxResponseBytes)
	return true
}

/* ------------------------------------------------------------- timeline */

// timelineRequest picks the audit window and the sources whose slow audit
// creates become the intervals to explain. It is a body, not a query string,
// because an audit query is the same structure the other audit routes take.
type timelineRequest struct {
	Query audit.Query `json:"query"`
	// AuditStallMs is the smallest audit create time that makes an interval.
	// Omitted: rule B6's default.
	AuditStallMs *float64 `json:"auditStallMs,omitempty"`
}

// timelineAnswer is the timeline plus how the intervals were found.
type timelineAnswer struct {
	timeline.View
	Intervals          int  `json:"intervals"`
	IntervalsTruncated bool `json:"intervalsTruncated"` // more slow audit creates exist than one page holds
}

// caseTimeline answers POST /api/cases/{id}/timeline. The intervals are the
// audit creates of the window at or above the stall threshold, newest first,
// at most one page; each runs from the request start for as long as the audit
// create took, as the client measured it. The timeline builder decides
// whether the case may be shown at all (two sources, a checked time zone rule
// for each) and says why when it may not.
func (h *handler) caseTimeline(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body timelineRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	q := body.Query
	applyQueryDefaults(&q)
	if err := audit.ValidateQuery(q); err != nil {
		writeQueryProblem(w, err)
		return
	}
	min := float64(audit.DefaultAuditStallMs)
	if body.AuditStallMs != nil {
		min = *body.AuditStallMs
	}
	if math.IsNaN(min) || math.IsInf(min, 0) || min <= 0 || min > 7*24*3600*1000 {
		writeRepoError(w, fmt.Errorf("%w: invalid threshold", audit.ErrInvalidInput))
		return
	}
	q.AuditCreateTimeMs = &audit.Range{Min: &min}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	page, err := h.repo.Requests(ctx, audit.PageInput{Query: q, Limit: audit.MaxPageSize, Order: audit.OrderNewest})
	if err != nil {
		writeRepoError(w, err)
		return
	}
	ivs := make([]join.Interval, 0, len(page.Rows))
	for _, row := range page.Rows {
		if iv, ok := auditInterval(row); ok {
			ivs = append(ivs, iv)
		}
	}
	buildCtx, stop := context.WithTimeout(r.Context(), requestTimeout)
	defer stop()
	view, err := timeline.Build(buildCtx, c, importer.Window{From: q.Start.UTC(), To: q.End.UTC()}, ivs)
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "timeline-failed", "The timeline could not be built.")
		return
	}
	writeJSON(w, http.StatusOK, timelineAnswer{View: view, Intervals: len(ivs), IntervalsTruncated: page.NextCursor != ""}, maxResponseBytes)
}

// auditInterval is the stretch of one slow audit create: from the request
// start, for the audit create time. A row without a start or a usable time
// makes no interval.
func auditInterval(r audit.RequestRecord) (join.Interval, bool) {
	if r.InitiatedAt == nil || r.AuditCreateTimeMs == nil {
		return join.Interval{}, false
	}
	ms := *r.AuditCreateTimeMs
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms <= 0 {
		return join.Interval{}, false
	}
	start := r.InitiatedAt.UTC()
	return join.Interval{ID: r.ID, Start: start, End: start.Add(time.Duration(ms * float64(time.Millisecond)))}, true
}

/* ---------------------------------------------------------- case export */

// caseExportPreview builds the redacted export of one case and keeps it for
// /api/export/confirm, exactly as the findings export does. The pseudonym key
// is fresh for each preview, handed to the builder and zeroed; it is never
// stored, so a re-export shares no pseudonym with this one. The file holds no
// file names (BuildCase never reads them).
func (h *handler) caseExportPreview(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	// Held so an erase cannot finish between the read and the stored preview,
	// which would leave an export of an erased case behind. The case is opened
	// after the gate is taken, so an erase that ran first answers 404.
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	_, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	imports, err := c.Imports(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	events, err := c.Events(r.Context(), casestore.EventQuery{Limit: casestore.MaxEventQuery})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	in := evidence.CaseInput{
		Events:          events,
		EventsTruncated: len(events) >= casestore.MaxEventQuery,
		ToolVersion:     exportToolVersion,
		Now:             time.Now().UTC(),
	}
	for _, rec := range imports {
		in.Imports = append(in.Imports, evidence.CaseImport{
			Importer: rec.Importer, FileName: rec.FileName, SHA256: rec.SHA256, HostLabel: rec.HostLabel,
			TZRule: rec.TZRule, Status: rec.Status, Rows: rec.Rows, Rejected: rec.Rejected,
			Quality: rec.Quality, KnownGaps: rec.KnownGaps, AckText: rec.AckText,
		})
	}
	key, err := newExportKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	in.Key = key
	export, err := evidence.BuildCase(in)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		writeExportBuildError(w, err)
		return
	}
	canonical, err := canonicalExport(export)
	if err != nil {
		writeExportBuildError(w, err)
		return
	}
	csv, err := evidence.EventsCSV(export)
	if err != nil {
		writeExportBuildError(w, err)
		return
	}
	raw16 := make([]byte, 16)
	if _, err := rand.Read(raw16); err != nil {
		writeError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	previewID := hex.EncodeToString(raw16)
	sum := sha256.Sum256(canonical)
	h.exports().put(previewID, storedExport{canonical: canonical, sha256: hex.EncodeToString(sum[:]), csv: csv, caseID: string(id)})
	writeJSON(w, http.StatusOK, exportPreviewResponse{PreviewID: previewID, Export: export}, maxResponseBytes)
}
