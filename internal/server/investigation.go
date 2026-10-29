package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
)

// Investigation: the guided analysis engine (internal/investigate) run on the
// audit data of the chosen sources and the IIS rows of the open case. The route
// only reads. It builds an Input, hands it to the pure engine and returns the
// answer; it never changes /api/analyze or /api/breakdown and writes nothing to
// the case.

const (
	investigationMaxRows     = 30000 // audit calls read for one run (300 pages)
	investigationMaxSpan     = 7 * 24 * time.Hour
	investigationMaxReleases = 50
	investigationMaxLabel    = 200
	investigationMaxPool     = 100000
	investigationIISSource   = "iis-w3c"
)

// investigationAuditBudget and investigationCaseBudget split the request's
// overall timeout in two: the audit paging gets its own budget so a slow
// source cannot starve the case-side reads (openCase, the IIS rows, the
// Blackbox lane) that follow it. Tests shrink these to force the slow-audit
// path without waiting out the real budget.
var (
	investigationAuditBudget = 3 * time.Minute
	investigationCaseBudget  = 2 * time.Minute
)

type investigationWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type investigationRelease struct {
	At    time.Time `json:"at"`
	Label string    `json:"label"`
}

// investigationRequest is the body. ReleasesEntered must be true for the change
// list to count as entered (an empty list entered on purpose reads "No change
// listed"). IncludeSeries asks for the per-minute rows the charts draw.
type investigationRequest struct {
	SourceIDs       []string               `json:"sourceIds"`
	Incident        investigationWindow    `json:"incident"`
	Reference       investigationWindow    `json:"reference"`
	Route           string                 `json:"route"`
	PoolLimit       *int                   `json:"poolLimit"`
	Releases        []investigationRelease `json:"releases"`
	ReleasesEntered bool                   `json:"releasesEntered"`
	IncludeSeries   bool                   `json:"includeSeries"`
}

// investigationRead says what the run read, so a reader knows what the answer
// stands on.
type investigationRead struct {
	Requests  int      `json:"requests"`
	IISRows   int      `json:"iisRows"`
	Truncated bool     `json:"truncated"`
	Warnings  []string `json:"warnings"`
}

type investigationAnswer struct {
	Result investigate.Result `json:"result"`
	Read   investigationRead  `json:"read"`
}

// problem is the first thing wrong with the request, as a fixed sentence, or ""
// when the request is acceptable. The checks run in the order they always did.
func (b investigationRequest) problem() string {
	w := func(x investigationWindow) bool { return !x.Start.IsZero() && !x.End.IsZero() && x.End.After(x.Start) }
	if !w(b.Incident) || !w(b.Reference) {
		return "Both windows need a start and an end, and each end must be after its start."
	}
	if len(b.Route) > 256 {
		return "The route is longer than 256 characters."
	}
	if len(b.Releases) > investigationMaxReleases {
		return fmt.Sprintf("A run can list at most %d changes.", investigationMaxReleases)
	}
	if b.PoolLimit != nil && (*b.PoolLimit < 1 || *b.PoolLimit > investigationMaxPool) {
		return fmt.Sprintf("The pool limit must be a whole number from 1 to %d.", investigationMaxPool)
	}
	// Each window has its own limit; the stretch between them is never read.
	if b.Incident.End.Sub(b.Incident.Start) > investigationMaxSpan || b.Reference.End.Sub(b.Reference.Start) > investigationMaxSpan {
		return "A time window can be at most 7 days long."
	}
	for _, r := range b.Releases {
		if r.At.IsZero() {
			return "Each listed change needs a time."
		}
		if len(r.Label) > investigationMaxLabel {
			return fmt.Sprintf("A change label can be at most %d characters.", investigationMaxLabel)
		}
	}
	return ""
}

func (b investigationRequest) validate() bool { return b.problem() == "" }

// ranges are the stretches to read: the two windows and nothing between them. Two
// windows that overlap or touch are read as one stretch (when that stays within
// the 7-day limit), otherwise the later one is clipped so that no moment is read
// twice. The row cap and the span limit apply to each stretch.
func (b investigationRequest) ranges() []investigationWindow {
	a := investigationWindow{b.Reference.Start.UTC(), b.Reference.End.UTC()}
	c := investigationWindow{b.Incident.Start.UTC(), b.Incident.End.UTC()}
	if c.Start.Before(a.Start) {
		a, c = c, a
	}
	if !c.Start.After(a.End) {
		end := a.End
		if c.End.After(end) {
			end = c.End
		}
		if end.Sub(a.Start) <= investigationMaxSpan {
			return []investigationWindow{{a.Start, end}}
		}
	}
	if c.Start.Before(a.End) {
		c.Start = a.End
	}
	if !c.End.After(c.Start) {
		return []investigationWindow{a}
	}
	return []investigationWindow{a, c}
}

func (h *handler) caseInvestigation(w http.ResponseWriter, r *http.Request, raw string) {
	var body investigationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if problem := body.problem(); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	started := time.Now()
	h.event(r, "investigation_started",
		"sources", strings.Join(body.SourceIDs, ","),
		"incident_hours", body.Incident.End.Sub(body.Incident.Start).Hours(),
		"reference_hours", body.Reference.End.Sub(body.Reference.Start).Hours())
	res, read, c, ok := h.runInvestigation(w, r, raw, body)
	if !ok {
		return
	}
	c.Close()
	if !body.IncludeSeries {
		res = res.Compact()
	}
	h.event(r, "investigation_finished",
		"duration_ms", time.Since(started).Milliseconds(),
		"requests", read.Requests, "iis_rows", read.IISRows,
		"truncated", read.Truncated, "warnings", len(read.Warnings))
	writeJSON(w, http.StatusOK, investigationAnswer{Result: res, Read: read}, maxResponseBytes)
}

// runInvestigation reads the audit calls and the case's IIS rows for a validated
// request and runs the engine. On success the case is still open and the caller
// closes it (the report reads the checklist from it); on failure the answer is
// written, the case is closed and ok is false.
func (h *handler) runInvestigation(w http.ResponseWriter, r *http.Request, raw string, body investigationRequest) (investigate.Result, investigationRead, casestore.Case, bool) {
	var none investigate.Result
	if _, ok := caseIDOf(w, raw); !ok {
		return none, investigationRead{}, nil, false
	}
	var queries []audit.Query
	for _, rg := range body.ranges() {
		q := audit.Query{
			SourceIDs: body.SourceIDs,
			Start:     rg.Start, End: rg.End,
			BaselineStart: rg.Start.Add(-time.Hour), BaselineEnd: rg.Start,
			BucketSeconds: 86400,
		}
		applyQueryDefaults(&q)
		q.Kind = "all"
		if err := audit.ValidateQuery(q); err != nil {
			writeQueryProblem(w, err)
			return none, investigationRead{}, nil, false
		}
		queries = append(queries, q)
	}
	auditCtx, cancelAudit := context.WithTimeout(r.Context(), investigationAuditBudget)
	defer cancelAudit()

	// Audit first, so a failing source answers before the case is opened. The
	// audit read has its own budget: a slow source running past it must not cost
	// the case-side reads below the whole request (and so answer 504 even though
	// the client is still waiting).
	rows, read, status, err := h.readInvestigationRanges(auditCtx, queries)
	if status != "" && status != investigate.AuditPresent {
		// The audit_failure cause, in the log: this is the line that answers
		// "why could the audit source not be read" without guessing.
		h.eventWarn(r, "investigation_audit_failed", "cause", string(status), "warnings", len(read.Warnings))
	}
	if err != nil {
		if auditCtx.Err() != nil && errors.Is(auditCtx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
			h.eventWarn(r, "investigation_audit_failed", "cause", "timeout", "budget", "audit", "budget_ms", investigationAuditBudget.Milliseconds())
			// Our own audit budget ran out, not the client leaving: answer with an
			// audit-failed result instead of a timeout.
			status = investigate.AuditFailed
			rows = nil
			read.Requests = 0
			read.Warnings = append(read.Warnings, "Reading the audit calls took too long, so none were used. Shorten the windows or try again.")
		} else {
			writeTraceRepoError(w, err)
			return none, read, nil, false
		}
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return none, read, nil, false
	}

	caseCtx, cancelCase := context.WithTimeout(r.Context(), investigationCaseBudget)
	defer cancelCase()

	in, err := buildInvestigationInput(caseCtx, c, body, rows, status, &read)
	if err != nil {
		c.Close()
		writeStoreError(w, err)
		return none, read, nil, false
	}
	h.applyBlackboxLane(caseCtx, c, &in, &read)
	res := investigate.Analyze(in)
	if read.Warnings == nil {
		read.Warnings = []string{}
	}
	return res, read, c, true
}

// readInvestigationRanges reads each stretch on its own, so the row cap applies to
// a stretch and not to the sum, and a failed or capped stretch fails the whole
// read (a window missing from the data is never passed on as a whole picture).
func (h *handler) readInvestigationRanges(ctx context.Context, queries []audit.Query) ([]audit.RequestRecord, investigationRead, investigate.AuditStatus, error) {
	var all []audit.RequestRecord
	read := investigationRead{Warnings: []string{}}
	for _, q := range queries {
		rows, one, status, err := h.readInvestigationAudit(ctx, q)
		if err != nil {
			return nil, one, "", err
		}
		read.Truncated = read.Truncated || one.Truncated
		read.Warnings = append(read.Warnings, one.Warnings...)
		if status != investigate.AuditPresent {
			read.Requests = 0
			return nil, read, status, nil
		}
		all = append(all, rows...)
	}
	read.Requests = len(all)
	return all, read, investigate.AuditPresent, nil
}

// readInvestigationAudit pages the audit calls of the span, newest first. A
// source that cannot be queried, or a span with more calls than the cap, gives
// AuditFailed: a partial read is never passed on as the whole data (it would cut
// the earliest calls, which are the reference window).
func (h *handler) readInvestigationAudit(ctx context.Context, q audit.Query) ([]audit.RequestRecord, investigationRead, investigate.AuditStatus, error) {
	var rows []audit.RequestRecord
	read := investigationRead{Warnings: []string{}}
	status := investigate.AuditPresent
	cursor := ""
	// Per-source diagnostics: a start line and a finish line per source, so a
	// failed read names the source, its error code and its sanitized reason.
	start := time.Now()
	counts := map[string]int{}
	results := map[string]audit.SourceResult{}
	for _, id := range q.SourceIDs {
		h.log().Info("audit_source_start", "source_id", id, "kind", "audit",
			"window_start", q.Start.UTC().Format(time.RFC3339), "window_end", q.End.UTC().Format(time.RFC3339))
	}
	finish := func(err error) {
		for _, id := range q.SourceIDs {
			attrs := []any{"source_id", id, "kind", "audit",
				"window_hours", q.End.Sub(q.Start).Hours(),
				"rows", counts[id], "duration_ms", time.Since(start).Milliseconds()}
			if sr, seen := results[id]; seen {
				attrs = append(attrs, "ok", sr.OK)
				if !sr.OK {
					attrs = append(attrs, "error_code", sr.ErrorCode, "error", applog.Sanitize(sr.Error))
				}
			} else if err != nil {
				attrs = append(attrs, "ok", false, "error", applog.Sanitize(err.Error()))
				if errors.Is(err, context.DeadlineExceeded) {
					attrs = append(attrs, "error_code", "timeout", "budget", "audit")
				}
			} else {
				attrs = append(attrs, "ok", true)
			}
			h.log().Info("audit_source_read", attrs...)
		}
	}
	for {
		page, err := h.repo.Requests(ctx, audit.PageInput{Query: q, Cursor: cursor, Limit: audit.MaxPageSize, Order: audit.OrderNewest})
		if err != nil {
			finish(err)
			return nil, read, "", err
		}
		for _, s := range page.Sources {
			results[s.SourceID] = s
		}
		for _, row := range page.Rows {
			counts[row.SourceID]++
		}
		for _, s := range page.Sources {
			if !s.OK {
				status = investigate.AuditFailed
				read.Warnings = append(read.Warnings, "One or more audit sources could not be queried.")
				finish(nil)
				return nil, read, status, nil
			}
		}
		rows = append(rows, page.Rows...)
		if page.Truncated {
			read.Truncated = true
		}
		if page.NextCursor == "" || len(page.Rows) == 0 {
			break
		}
		if len(rows) >= investigationMaxRows {
			read.Truncated = true
			break
		}
		cursor = page.NextCursor
	}
	if read.Truncated {
		status = investigate.AuditFailed
		read.Warnings = append(read.Warnings, "There are too many audit calls in these windows to read in full, so none were used. Shorten the windows.")
		rows = nil
	}
	read.Requests = len(rows)
	finish(nil)
	return rows, read, status, nil
}

// buildInvestigationInput joins the audit calls, the case's IIS rows and the
// user's entries into one engine Input. An IIS read that hit the event cap is
// treated as no IIS: a partial list would read as missing calls.
func buildInvestigationInput(ctx context.Context, c casestore.Case, body investigationRequest, rows []audit.RequestRecord, status investigate.AuditStatus, read *investigationRead) (investigate.Input, error) {
	reqs, noStart := investigate.FromRecords(rows)
	in := investigate.Input{
		Incident:        investigate.Window{Start: body.Incident.Start.UTC(), End: body.Incident.End.UTC()},
		Reference:       investigate.Window{Start: body.Reference.Start.UTC(), End: body.Reference.End.UTC()},
		Route:           body.Route,
		Audit:           status,
		Requests:        reqs,
		NoStart:         noStart,
		PoolLimit:       body.PoolLimit,
		ReleasesEntered: body.ReleasesEntered,
	}
	for _, r := range body.Releases {
		in.Releases = append(in.Releases, investigate.Release{At: r.At.UTC(), Label: r.Label})
	}

	applyLaneRuns(ctx, c, &in, read)

	sources, err := caseSources(ctx, c)
	if err != nil {
		return in, err
	}
	readable := false
	for _, s := range sources {
		if s.Name == investigationIISSource && s.Validated {
			readable = true
		}
	}
	if !readable {
		return in, nil
	}
	// One read per stretch, each with its own cap. The store selects by start time,
	// so the lookback of the second stretch can reach events the first one holds;
	// they are counted once.
	var evs []evidence.Event
	seen := map[string]bool{}
	for _, rg := range body.ranges() {
		part, err := c.Events(ctx, casestore.EventQuery{
			Window:  importer.Window{From: rg.Start.Add(-callTraceLookback), To: rg.End},
			Sources: []string{investigationIISSource},
			Kinds:   []string{"request"},
			Limit:   casestore.MaxEventQuery,
		})
		if err != nil {
			return in, err
		}
		if len(part) >= casestore.MaxEventQuery {
			read.Warnings = append(read.Warnings, "There are too many IIS rows in these windows to read in full, so the IIS logs were not used.")
			return in, nil
		}
		for _, e := range part {
			if e.EvidenceID != "" && seen[e.EvidenceID] {
				continue
			}
			seen[e.EvidenceID] = true
			evs = append(evs, e)
		}
	}
	in.IISPresent = true
	for _, e := range evs {
		if e.DurationMs == nil {
			continue
		}
		end := e.End
		if end.Before(e.Start) {
			end = e.Start
		}
		in.IIS = append(in.IIS, investigate.IISRow{Start: e.Start.UTC(), End: end.UTC(), TakenMs: *e.DurationMs, Path: investigate.NormalizePath(e.Attrs["stem"])})
	}
	read.IISRows = len(in.IIS)
	return in, nil
}
