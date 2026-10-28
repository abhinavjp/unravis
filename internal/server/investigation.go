package server

import (
	"context"
	"net/http"
	"time"

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

func (b investigationRequest) validate() bool {
	w := func(x investigationWindow) bool { return !x.Start.IsZero() && !x.End.IsZero() && x.End.After(x.Start) }
	if !w(b.Incident) || !w(b.Reference) || len(b.Route) > 256 || len(b.Releases) > investigationMaxReleases {
		return false
	}
	if b.PoolLimit != nil && (*b.PoolLimit < 1 || *b.PoolLimit > investigationMaxPool) {
		return false
	}
	// Each window has its own limit; the stretch between them is never read.
	if b.Incident.End.Sub(b.Incident.Start) > investigationMaxSpan || b.Reference.End.Sub(b.Reference.Start) > investigationMaxSpan {
		return false
	}
	for _, r := range b.Releases {
		if r.At.IsZero() || len(r.Label) > investigationMaxLabel {
			return false
		}
	}
	return true
}

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
	if !body.validate() {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	res, read, c, ok := h.runInvestigation(w, r, raw, body)
	if !ok {
		return
	}
	c.Close()
	if !body.IncludeSeries {
		res = res.Compact()
	}
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
			writeError(w, http.StatusBadRequest, "Request validation failed.")
			return none, investigationRead{}, nil, false
		}
		queries = append(queries, q)
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()

	// Audit first, so a failing source answers before the case is opened.
	rows, read, status, err := h.readInvestigationRanges(ctx, queries)
	if err != nil {
		writeTraceRepoError(w, err)
		return none, read, nil, false
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return none, read, nil, false
	}

	in, err := buildInvestigationInput(ctx, c, body, rows, status, &read)
	if err != nil {
		c.Close()
		writeStoreError(w, err)
		return none, read, nil, false
	}
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
	for {
		page, err := h.repo.Requests(ctx, audit.PageInput{Query: q, Cursor: cursor, Limit: audit.MaxPageSize, Order: audit.OrderNewest})
		if err != nil {
			return nil, read, "", err
		}
		for _, s := range page.Sources {
			if !s.OK {
				status = investigate.AuditFailed
				read.Warnings = append(read.Warnings, "One or more audit sources could not be queried.")
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
