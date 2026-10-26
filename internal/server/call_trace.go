package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Call trace: one to three audited API calls, each on a timeline with lanes
// for the SQL, Mongo, Redis and IIS events of the open case, plus automatic
// checks. The server reads the audit record and the case events and hands them
// to the pure functions in internal/calltrace; it states no cause.

const (
	callTraceMaxCalls     = 3
	callTraceDefaultMin   = 2
	callTraceMaxMin       = 30
	callTraceMaxIDLen     = 512
	callTraceSharedWindow = 2 * time.Hour
	// callTraceLookback widens the event read before the window. The store
	// selects events by their start time, so an event that began before the
	// window and still reaches it would be missed. Long SQL waits and slow IIS
	// requests can start many minutes early. Events that do not reach the window
	// are dropped again before the lanes are built.
	callTraceLookback = 30 * time.Minute
)

// callTraceRef names one audited call.
type callTraceRef struct {
	SourceID string `json:"sourceId"`
	ID       string `json:"id"`
}

// callTraceRequest is the body of both call-trace routes. Margins are minutes
// around the call; 0 or absent means the default, above the maximum is clamped,
// negative is refused. Naming the same call twice is refused.
type callTraceRequest struct {
	Calls     []callTraceRef `json:"calls"`
	BeforeMin int            `json:"beforeMin"`
	AfterMin  int            `json:"afterMin"`
}

// validate checks the body and returns the margins to use.
func (b callTraceRequest) validate() (before, after time.Duration, ok bool) {
	if len(b.Calls) < 1 || len(b.Calls) > callTraceMaxCalls {
		return 0, 0, false
	}
	if b.BeforeMin < 0 || b.AfterMin < 0 {
		return 0, 0, false
	}
	seen := make(map[callTraceRef]struct{}, len(b.Calls))
	for _, c := range b.Calls {
		if c.SourceID == "" || c.ID == "" || len(c.SourceID) > callTraceMaxIDLen || len(c.ID) > callTraceMaxIDLen {
			return 0, 0, false
		}
		if _, dup := seen[c]; dup {
			return 0, 0, false
		}
		seen[c] = struct{}{}
	}
	return clampMinutes(b.BeforeMin), clampMinutes(b.AfterMin), true
}

func clampMinutes(m int) time.Duration {
	switch {
	case m == 0:
		m = callTraceDefaultMin
	case m > callTraceMaxMin:
		m = callTraceMaxMin
	}
	return time.Duration(m) * time.Minute
}

// callTrace answers POST /api/call-trace: the calls with no case open, so every
// lane says there are no files.
func (h *handler) callTrace(w http.ResponseWriter, r *http.Request) {
	var body callTraceRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	before, after, ok := body.validate()
	if !ok {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	plans, ok := h.planTraces(ctx, w, body, before, after)
	if !ok {
		return
	}
	h.runCallTrace(ctx, w, plans, nil)
}

// caseCallTrace answers POST /api/cases/{id}/call-trace: the same, with the
// lanes read from the events the case holds. The case opens the way the
// timeline opens it, so a case that may not be read answers the same failure.
// The audit records are read first, so a slow audit database does not hold the
// case read gate.
func (h *handler) caseCallTrace(w http.ResponseWriter, r *http.Request, raw string) {
	var body callTraceRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	before, after, ok := body.validate()
	if !ok {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	if _, ok := caseIDOf(w, raw); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	plans, ok := h.planTraces(ctx, w, body, before, after)
	if !ok {
		return
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	h.runCallTrace(ctx, w, plans, c)
}

// tracePlan is one call with the window its lanes cover.
type tracePlan struct {
	call calltrace.Call
	win  calltrace.Window
}

// planTraces reads the audit record of each call and builds its window. It
// writes the failure and returns false when a record cannot be read or placed.
func (h *handler) planTraces(ctx context.Context, w http.ResponseWriter, body callTraceRequest, before, after time.Duration) ([]tracePlan, bool) {
	plans := make([]tracePlan, 0, len(body.Calls))
	for _, ref := range body.Calls {
		detail, err := h.repo.Record(ctx, audit.RecordInput{SourceID: ref.SourceID, ID: ref.ID})
		if err != nil {
			writeTraceRepoError(w, err)
			return nil, false
		}
		normalizeRowTimes(&detail.RequestRecord)
		call, ok := calltrace.CallFromRecord(detail.RequestRecord)
		win := callWindow(call, before, after)
		if !ok || win.From.Year() < 1 || win.To.Year() > 9999 {
			writeError(w, http.StatusUnprocessableEntity, "That call has no start time, so it cannot be placed on a timeline.")
			return nil, false
		}
		plans = append(plans, tracePlan{call: call, win: win})
	}
	return plans, true
}

// writeTraceRepoError is writeRepoError, except that a canceled request answers
// the way writeStoreError answers it (400) instead of 500.
func writeTraceRepoError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		writeCaseFailure(w, http.StatusBadRequest, "canceled", "The request was canceled.")
		return
	}
	writeRepoError(w, err)
}

func (h *handler) runCallTrace(ctx context.Context, w http.ResponseWriter, plans []tracePlan, c casestore.Case) {
	res := calltrace.Result{Traces: make([]calltrace.Trace, 0, len(plans)), Compare: []calltrace.Check{}}
	union := plans[0].win
	for _, p := range plans[1:] {
		if p.win.From.Before(union.From) {
			union.From = p.win.From
		}
		if p.win.To.After(union.To) {
			union.To = p.win.To
		}
	}
	if union.To.Sub(union.From) <= callTraceSharedWindow {
		u := union
		res.SharedWindow = &u
	}

	var sources []calltrace.SourceInfo
	var readable []string // importers whose time zone rule is validated; the others plot nothing
	if c != nil {
		var err error
		sources, err = caseSources(ctx, c)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for _, s := range sources {
			if s.Validated {
				readable = append(readable, s.Name)
			}
		}
	}

	// Each call reads its own window, so a dense case cannot push the events of
	// one call out of the read made for another.
	for _, p := range plans {
		var evs []evidence.Event
		capped := false
		if c != nil && len(readable) > 0 {
			var err error
			evs, capped, err = readTraceEvents(ctx, c, p.win, readable)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			evs = overlapping(evs, p.win)
		}
		lanes := calltrace.BuildLanes(p.win, c != nil, sources, evs)
		if capped {
			// The read stopped early, so "no event seen" proves nothing.
			lanes = calltrace.MarkIncomplete(lanes)
			res.Truncated = true
		}
		checks := calltrace.RunChecks(p.call, lanes)
		res.Traces = append(res.Traces, calltrace.Trace{Call: p.call, Window: p.win, Lanes: lanes, Checks: checks})
	}
	if len(res.Traces) > 1 {
		res.Compare = calltrace.CompareChecks(res.Traces)
	}
	if err := ctx.Err(); err != nil {
		writeTraceRepoError(w, err)
		return
	}
	normalizeResult(&res)
	writeJSON(w, http.StatusOK, res, maxResponseBytes)
}

// callWindow is the call's span, from its start to its end (or its start when
// it has no usable end), widened by the margins. All times are UTC.
func callWindow(call calltrace.Call, before, after time.Duration) calltrace.Window {
	start := call.Start.UTC()
	end := start
	if call.End != nil && call.End.After(start) {
		end = call.End.UTC()
	}
	return calltrace.Window{From: start.Add(-before), To: end.Add(after)}
}

// caseSources lists the importers the case holds files for and whether every
// file of each has a time zone rule that can be trusted. The file list and the
// rule test follow internal/timeline (okImports, validatedRule).
func caseSources(ctx context.Context, c casestore.Case) ([]calltrace.SourceInfo, error) {
	imports, err := c.Imports(ctx)
	if err != nil {
		return nil, err
	}
	idx := map[string]*calltrace.SourceInfo{}
	spanKnown := map[string]bool{} // importer -> every file states the span it covers
	var names []string
	for _, f := range imports {
		if f.Status != casestore.StatusOK && f.Status != "" {
			continue
		}
		s := idx[f.Importer]
		if s == nil {
			s = &calltrace.SourceInfo{Name: f.Importer, Validated: true}
			idx[f.Importer] = s
			spanKnown[f.Importer] = true
			names = append(names, f.Importer)
		}
		if !traceRuleValidated(f) {
			s.Validated = false
		}
		first, last := f.Quality.CoverageFirst, f.Quality.CoverageLast
		if first == nil || last == nil || first.IsZero() || last.IsZero() || last.Before(*first) {
			spanKnown[f.Importer] = false
			continue
		}
		if s.From.IsZero() || first.Before(s.From) {
			s.From = first.UTC()
		}
		if s.To.IsZero() || last.After(s.To) {
			s.To = last.UTC()
		}
	}
	sort.Strings(names)
	out := make([]calltrace.SourceInfo, 0, len(names))
	for _, n := range names {
		si := *idx[n]
		if !spanKnown[n] {
			si.From, si.To = time.Time{}, time.Time{} // one file without a span: nothing is claimed
		}
		out = append(out, si)
	}
	return out, nil
}

// traceRuleValidated reports whether a file's time zone rule is one of the four
// known rules and, for host-local, names a host.
func traceRuleValidated(f casestore.ImportRecord) bool {
	switch f.TZRule {
	case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias:
		return true
	case clock.RuleHostLocal:
		return f.HostLabel != ""
	}
	return false
}

// readTraceEvents reads the events of the named importers that start in the
// window (plus the lookback) and reports whether the read hit the cap, so events
// may be missing. sources must not be empty: an empty list would match every
// importer.
func readTraceEvents(ctx context.Context, c casestore.Case, win calltrace.Window, sources []string) ([]evidence.Event, bool, error) {
	evs, err := c.Events(ctx, casestore.EventQuery{
		Window:  importer.Window{From: win.From.Add(-callTraceLookback), To: win.To},
		Sources: sources,
		Limit:   casestore.MaxEventQuery,
	})
	if err != nil {
		return nil, false, err
	}
	return evs, len(evs) >= casestore.MaxEventQuery, nil
}

// overlapping keeps the events that touch the window.
func overlapping(evs []evidence.Event, win calltrace.Window) []evidence.Event {
	out := make([]evidence.Event, 0, len(evs))
	for _, e := range evs {
		end := e.End
		if end.Before(e.Start) {
			end = e.Start
		}
		if e.Start.After(win.To) || end.Before(win.From) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// normalizeResult makes every JSON array an array, never null.
func normalizeResult(res *calltrace.Result) {
	if res.Traces == nil {
		res.Traces = []calltrace.Trace{}
	}
	if res.Compare == nil {
		res.Compare = []calltrace.Check{}
	}
	normalizeChecks(res.Compare)
	for i := range res.Traces {
		t := &res.Traces[i]
		if t.Call.Parts == nil {
			t.Call.Parts = []calltrace.Part{}
		}
		if t.Lanes == nil {
			t.Lanes = []calltrace.Lane{}
		}
		for j := range t.Lanes {
			if t.Lanes[j].Sources == nil {
				t.Lanes[j].Sources = []string{}
			}
			if t.Lanes[j].Events == nil {
				t.Lanes[j].Events = []calltrace.LaneEvent{}
			}
		}
		if t.Checks == nil {
			t.Checks = []calltrace.Check{}
		}
		normalizeChecks(t.Checks)
	}
}

func normalizeChecks(cs []calltrace.Check) {
	for i := range cs {
		if cs[i].CallIDs == nil {
			cs[i].CallIDs = []string{}
		}
	}
}
