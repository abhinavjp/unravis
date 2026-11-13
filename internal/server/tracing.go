package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/iis"
)

// The tracing route: POST /api/cases/{id}/tracing. It is read-only: it writes
// nothing to the case. The audit lane reads the live audit repository itself
// (one aggregate Rollup per request, see tracing_audit.go); the sql and iis
// lanes read only what the case already stores.

const (
	tracingMaxSources   = 50
	tracingMaxIDLen     = 128
	tracingLabelRunes   = 64
	tracingWindowText   = "The window needs a start and an end, and the end must be after the start."
	tracingBadSourceMsg = "A source id is not valid."
	tracingTooManyMsg   = "Pick at most 50 sources."
	tracingBadSeqMsg    = "The request number is not valid."
	tracingCappedWarn   = "Only the first 50 sources are shown."
	tracingTooLongMsg   = "The longest window is 186 days. Shorten the start or end."
	tracingUnknownMsg   = "That source is not part of this case."

	tracingNoFileWarn   = "A selected source has no file yet, so it shows nothing."
	tracingNotLaneWarn  = "A selected source is not an SQL or IIS source, so it is not shown."
	tracingClockMsg     = "The time zone rule of a file is not confirmed, so its events are not placed on the timeline."
	tracingRowCapMsg    = "Only the first 50,000 saved events were listed; the bucket counts cover every saved event."
	tracingScanBudget   = "The scan of saved events stopped early, so the counts may be too low."
	tracingTimeoutMsg   = "Reading the saved events ran out of time, so the counts and events may be too low. Shorten the window to see all of it."
	tracingConsStored   = "stored-as-of"
	tracingOriginCase   = "case"
	tracingOriginLive   = "live"
	tracingReasonRowCap = "rowcap"
	tracingReasonBudget = "scan-budget"
	// tracingReasonTimeout is the lane reason code when a saved-event read passed
	// its time budget (tracingLaneBudget); the lane keeps what was read so far.
	tracingReasonTimeout = "timeout"
	tracingDisabledTZ    = "tz-rule"
	// A no-data lane says why when the case knows: the file is listed but was never
	// imported, or it was imported and then set aside.
	tracingReasonNotImported = "not-imported"
	tracingReasonSkipped     = "import-skipped"
	tracingNotImportedMsg    = "The files listed for this lane have not been imported into this case, so it holds none of their events. Import them to see this lane."
	// The database lane is also filled by a saved Query Store day, and the Timeline never opens a connection itself, so its words
	// say what is missing and where to add it.
	tracingSQLNotImportedMsg = "No database data is saved in this case for this window. The Timeline shows saved data only and never connects by itself. In the case's Sources, import the listed SQL file or use \"Read this day\" on the SQL connection."
	tracingSkippedMsg        = "The files imported for this lane were set aside, so the case holds none of their events."
)

// tracingCountBuckets is the bucket aggregate behind the SQL and IIS lanes. It is
// a package variable only so a test can watch the handle it is given.
var tracingCountBuckets = casestore.CountBuckets

// tracingLaneBudget is the time one saved-event read of a lane (the event list,
// and the bucket counts) may take before the lane is answered partial with
// reason "timeout". Each read has its own budget, so one slow lane never fails
// the others. A package variable only so a test can shorten it.
var tracingLaneBudget = casestore.MaxScanTime

// tracingRequest is the body. There is deliberately no route field: a body
// that has one is refused by decodeJSON as an unknown field. Reference is
// accepted and checked but not used (the answer has one window). RequestSeq is
// echoed so a client can drop an answer that arrives late.
type tracingRequest struct {
	SourceIDs  []string             `json:"sourceIds"`
	Incident   investigationWindow  `json:"incident"`
	Reference  *investigationWindow `json:"reference"`
	RequestSeq *int64               `json:"requestSeq"`
}

// tracingIDs returns the source ids trimmed, with repeats dropped in order of
// first appearance.
func (b tracingRequest) tracingIDs() []string {
	seen := make(map[string]bool, len(b.SourceIDs))
	out := make([]string, 0, len(b.SourceIDs))
	for _, id := range b.SourceIDs {
		id = strings.TrimSpace(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func tracingWindowOK(win investigationWindow) bool {
	if win.Start.IsZero() || win.End.IsZero() || !win.End.After(win.Start) {
		return false
	}
	// The axis rounds the window out to whole hours or days, so a year at either
	// end of the JSON range would make the answer unwritable (a 500). Keep both
	// ends inside years 1..9998 in UTC.
	return win.Start.UTC().Year() >= 1 && win.End.UTC().Year() <= 9998
}

// problem returns the first failing sentence, or "" when the request is fine.
func (b tracingRequest) problem() string {
	ids := b.tracingIDs()
	if len(ids) > tracingMaxSources {
		return tracingTooManyMsg
	}
	for _, id := range ids {
		if id == "" || len(id) > tracingMaxIDLen {
			return tracingBadSourceMsg
		}
	}
	if !tracingWindowOK(b.Incident) {
		return tracingWindowText
	}
	if b.Reference != nil && !tracingWindowOK(*b.Reference) {
		return tracingWindowText
	}
	if b.RequestSeq != nil && *b.RequestSeq < 0 {
		return tracingBadSeqMsg
	}
	if b.Incident.End.Sub(b.Incident.Start) > audit.MaxTraceWindow {
		return tracingTooLongMsg
	}
	if b.Reference != nil && b.Reference.End.Sub(b.Reference.Start) > audit.MaxTraceWindow {
		return tracingTooLongMsg
	}
	return ""
}

// parseTracingSource reads the wire form of a source id: cs_<n> is a source
// tag, imp_<n> an import record, anything else an audit source id (kept as
// sent; it is resolved later). ok is false for a cs_ or imp_ id whose number is
// not a positive whole number of at most 18 digits.
func parseTracingSource(id string) (kind string, n int64, ok bool) {
	for _, prefix := range []string{"cs", "imp"} {
		rest, found := strings.CutPrefix(id, prefix+"_")
		if !found {
			continue
		}
		if len(rest) < 1 || len(rest) > 18 {
			return prefix, 0, false
		}
		for i := 0; i < len(rest); i++ {
			if rest[i] < '0' || rest[i] > '9' {
				return prefix, 0, false
			}
		}
		v, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || v <= 0 {
			return prefix, 0, false
		}
		return prefix, v, true
	}
	return "audit", 0, true
}

// unwrapCase returns the handle inside the sharedCase that openCase hands out.
// The wrapper shows only the Case methods, so an optional interface
// (casestore.Tagger, casestore.BucketCounter) is asserted on the result of this.
// Close stays on the wrapper.
func unwrapCase(c casestore.Case) casestore.Case {
	if sc, wrapped := c.(*sharedCase); wrapped {
		return sc.Case
	}
	return c
}

type tracingRef struct {
	SourceID string `json:"sourceId"`
	Label    string `json:"label"`
	// NoFile is true for a listed source whose file was never imported into the
	// case (a tag the wizard saved, or a file that was only previewed).
	NoFile bool `json:"noFile,omitempty"`
}

// tracingSelection is the sources one request reads. Refs are the resolved
// cs_/imp_ sources in request order. ImportOf maps a source id to the import
// behind it (0 for a tag with no file yet). AuditIDs are the audit ids as sent.
// Explicit is true when the request named at least one source; false means
// sourceIds was omitted, null or [], and the audit sources of the case are
// included by the audit lane (never infer "no audit lane" from AuditIDs being
// empty).
type tracingSelection struct {
	Refs     []tracingRef
	ImportOf map[string]int64
	AuditIDs []string
	Explicit bool
	Warnings []string
	// KindOf maps a cs_ source id to the importer name its tag was saved with, so a
	// tag with no import can still be placed on its lane.
	KindOf map[string]string
	// SavedAudit are the names of the audit-class Mongo databases the case saved
	// (tags with no file behind them), in saved order. Only read when the request
	// reads tags or names an audit source; they are never refs.
	SavedAudit []string
}

type tracingFailure struct {
	Status          int
	Reason, Message string
}

func tracingUnknownSource() *tracingFailure {
	return &tracingFailure{Status: http.StatusNotFound, Reason: "unknown-source", Message: tracingUnknownMsg}
}

func tracingStoreFailure() *tracingFailure {
	return &tracingFailure{Status: http.StatusInternalServerError, Reason: "store-error", Message: "The case store could not complete the request."}
}

// tracingLabel is the first non-blank text, cut to 64 runes.
func tracingLabel(parts ...string) string {
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if utf8.RuneCountInString(p) > tracingLabelRunes {
			p = string([]rune(p)[:tracingLabelRunes])
		}
		return p
	}
	return ""
}

func tracingTagID(id int64) string    { return "cs_" + strconv.FormatInt(id, 10) }
func tracingImportID(id int64) string { return "imp_" + strconv.FormatInt(id, 10) }

// resolveTracingSources turns the (de-duplicated) ids into a selection. c is
// already unwrapped. It reads source tags only when a cs_ id is present or ids
// is empty, and imports whenever it reads tags (to pair them) or an imp_ id is
// present.
func resolveTracingSources(ctx context.Context, c casestore.Case, ids []string) (tracingSelection, *tracingFailure) {
	sel := tracingSelection{
		Refs:     []tracingRef{},
		ImportOf: map[string]int64{},
		KindOf:   map[string]string{},
		AuditIDs: []string{},
		Explicit: len(ids) > 0,
		Warnings: []string{},
	}
	needTags, needImports := len(ids) == 0, len(ids) == 0
	for _, id := range ids {
		switch kind, _, _ := parseTracingSource(id); kind {
		case "cs":
			needTags = true
		case "imp":
			needImports = true
		default:
			needTags = true // an audit id: the saved audit databases are listed too
		}
	}

	tagByID := map[int64]casestore.SourceTag{}
	var tags []casestore.SourceTag
	if needTags {
		if tagger, ok := c.(casestore.Tagger); ok {
			var err error
			tags, err = tagger.Sources(ctx)
			if err != nil {
				return sel, tracingStoreFailure()
			}
			for _, t := range tags {
				tagByID[t.ID] = t
				if t.Kind == mongoDatabaseKind && t.Classification == casestore.ClassAudit && strings.TrimSpace(t.Label) != "" {
					sel.SavedAudit = append(sel.SavedAudit, t.Label)
				}
			}
		}
		// A case that cannot hold tags has none: a cs_ id then falls through to
		// the unknown-source answer below.
	}
	importByID := map[int64]casestore.ImportRecord{}
	var imports []casestore.ImportRecord
	if needImports || needTags {
		// Tags are paired with imports (pairFileTags), so tags imply imports.
		var err error
		imports, err = c.Imports(ctx)
		if err != nil {
			return sel, tracingStoreFailure()
		}
		for _, im := range imports {
			importByID[im.ID] = im
		}
	}

	// A tag the wizard saved before its file was imported has ImportID 0; the pairing
	// gives it the import it belongs to.
	paired := pairFileTags(tags, imports)
	tagImport := func(t casestore.SourceTag) int64 {
		if t.ImportID == 0 {
			return paired[t.ID]
		}
		return t.ImportID
	}

	if len(ids) > 0 {
		for _, id := range ids {
			kind, n, _ := parseTracingSource(id)
			switch kind {
			case "cs":
				t, found := tagByID[n]
				if !found {
					return sel, tracingUnknownSource()
				}
				sel.Refs = append(sel.Refs, tracingRef{SourceID: id, Label: tracingLabel(t.Label, t.Kind)})
				sel.ImportOf[id] = tagImport(t)
				sel.KindOf[id] = t.Kind
			case "imp":
				im, found := importByID[n]
				if !found {
					return sel, tracingUnknownSource()
				}
				sel.Refs = append(sel.Refs, tracingRef{SourceID: id, Label: tracingLabel(im.Importer)})
				sel.ImportOf[id] = im.ID
			default:
				sel.AuditIDs = append(sel.AuditIDs, id)
			}
		}
		sel.markNoFile()
		return sel, nil
	}

	// Default call: every tag (oldest first), then every usable import no tag
	// references (in id order), cut to the first 50.
	linked := map[int64]bool{}
	refs := make([]tracingRef, 0, len(tags)+len(imports))
	for _, t := range tags {
		if t.Kind == mongoDatabaseKind {
			// A saved audit database is not a file: it is listed with the live audit
			// sources (SavedAudit), and is never a "source with no file yet".
			continue
		}
		sid := tracingTagID(t.ID)
		refs = append(refs, tracingRef{SourceID: sid, Label: tracingLabel(t.Label, t.Kind)})
		imp := tagImport(t)
		sel.ImportOf[sid] = imp
		sel.KindOf[sid] = t.Kind
		if imp > 0 {
			linked[imp] = true
		}
	}
	byID := make([]casestore.ImportRecord, len(imports))
	copy(byID, imports)
	sort.Slice(byID, func(i, j int) bool { return byID[i].ID < byID[j].ID })
	for _, im := range byID {
		if (im.Status != casestore.StatusOK && im.Status != "") || linked[im.ID] {
			continue
		}
		sid := tracingImportID(im.ID)
		refs = append(refs, tracingRef{SourceID: sid, Label: tracingLabel(im.Importer)})
		sel.ImportOf[sid] = im.ID
	}
	if len(refs) > tracingMaxSources {
		for _, r := range refs[tracingMaxSources:] {
			delete(sel.ImportOf, r.SourceID)
		}
		refs = refs[:tracingMaxSources]
		sel.Warnings = append(sel.Warnings, tracingCappedWarn)
	}
	sel.Refs = refs
	sel.markNoFile()
	return sel, nil
}

// markNoFile flags every listed source whose file was never imported, so the
// panel can say so instead of listing it as traced.
func (s *tracingSelection) markNoFile() {
	for i := range s.Refs {
		if s.ImportOf[s.Refs[i].SourceID] == 0 {
			s.Refs[i].NoFile = true
		}
	}
}

// The response contract (phase 04 reads it by JSON path). Arrays are never null.

type tracingWindow struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	AxisStart     time.Time `json:"axisStart"`
	AxisEnd       time.Time `json:"axisEnd"`
	BucketSeconds int       `json:"bucketSeconds"`
	Buckets       int       `json:"buckets"`
}

type tracingBucket struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Count  int64     `json:"count"`
	Errors *int64    `json:"errors,omitempty"`
	// ClientErrors is how many requests of the bucket had a 4xx status (the iis
	// lane only; omitted when there were none).
	ClientErrors *int64 `json:"clientErrors,omitempty"`
	// TimedCount is how many of the Count requests had a timing, and P50Ms,
	// P95Ms and MaxMs their response times in milliseconds (the audit and web-server
	// lanes). A bucket with no timed request omits all four.
	TimedCount int64 `json:"timedCount,omitempty"`
	// AvgMs is the average duration of the bucket's statements in milliseconds
	// (the sql lane drawn from saved Query Store figures; p50 and p95 are then absent).
	AvgMs    *float64 `json:"avgMs,omitempty"`
	P50Ms    *float64 `json:"p50Ms,omitempty"`
	P95Ms    *float64 `json:"p95Ms,omitempty"`
	MaxMs    *float64 `json:"maxMs,omitempty"`
	// BusyMs is the summed duration, in milliseconds, of the calls that started in this bucket.
	// Nil means it was not recorded; it is never a stand-in zero.
	BusyMs *float64 `json:"busyMs,omitempty"`
	Clipped  bool     `json:"clipped"`
	Complete bool     `json:"complete"`
}

type tracingEvent struct {
	ID          string    `json:"id"`
	SourceID    string    `json:"sourceId"`
	Label       string    `json:"label"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	DurationMs  *float64  `json:"durationMs"`
	Severity    string    `json:"severity"`
	StatusClass string    `json:"statusClass"`
	Win32       *int64    `json:"win32"`
	Kind        string    `json:"kind,omitempty"`
}

type tracingLane struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	State       string          `json:"state"`
	Origin      string          `json:"origin"`
	AsOf        time.Time       `json:"asOf"`
	Complete    bool            `json:"complete"`
	Truncated   bool            `json:"truncated"`
	ReasonCode  string          `json:"reasonCode"`
	Reason      string          `json:"reason"`
	Scanned     int64           `json:"scanned"`
	Consistency string          `json:"consistency"`
	Disabled    string          `json:"disabled,omitempty"`
	Sources     []tracingRef    `json:"sources"`
	Buckets     []tracingBucket `json:"buckets"`
	Events      []tracingEvent  `json:"events"`
	Hidden      int             `json:"hidden"`
	// ChunksTotal, ChunksRead and ReadThrough are set only on an audit lane whose
	// window was read in chunks of at most one day (a window over 24 hours).
	// ChunksRead of ChunksTotal chunks finished (a missing ChunksRead is 0);
	// ReadThrough is the end of the last finished chunk, set when the read stopped
	// early. Cells that end after it carry complete false.
	ChunksTotal int        `json:"chunksTotal,omitempty"`
	ChunksRead  int        `json:"chunksRead,omitempty"`
	ReadThrough *time.Time `json:"readThrough,omitempty"`
	// SourcesRead are the live audit sources this lane read to the end (the audit
	// lane only). SavedNotConnected are the audit databases the case saved that no
	// open connection carries, by saved name (the audit lane only).
	SourcesRead       []tracingRef `json:"sourcesRead,omitempty"`
	SavedNotConnected []string     `json:"savedNotConnected,omitempty"`
	ImportedFrom      *time.Time   `json:"importedFrom,omitempty"` // earliest time in the lane's imported files
	ImportedTo        *time.Time   `json:"importedTo,omitempty"`   // latest time in the lane's imported files
	ImportedPartial   bool         `json:"importedPartial,omitempty"` // some imported file has no recorded time range
	// BlockSeconds is the length of the blocks this lane's source saved its numbers in, when it is
	// longer than the bucket (Query Store). Zero means not known or same as the bucket.
	BlockSeconds int `json:"blockSeconds,omitempty"`
}

type tracingResponse struct {
	CaseID           string        `json:"caseId"`
	RequestSeq       *int64        `json:"requestSeq,omitempty"`
	AsOf             time.Time     `json:"asOf"`
	Window           tracingWindow `json:"window"`
	Truncated        bool          `json:"truncated"`
	MixedConsistency bool          `json:"mixedConsistency"`
	AllLanesStored   bool          `json:"allLanesStored"`
	Warnings         []string      `json:"warnings"`
	Sources          []tracingRef  `json:"sources"`
	Lanes            []tracingLane `json:"lanes"`
	// AuditSources are the live audit sources open in the process (the whole
	// listing, not only the ones this request read); SavedNotConnected are the
	// audit databases the case saved that no open connection carries. Both are left
	// out when the request did not list audit sources.
	AuditSources      []tracingRef `json:"auditSources,omitempty"`
	SavedNotConnected []string     `json:"savedNotConnected,omitempty"`
	// ReconnectFailed are the saved audit databases Unravis tried to open again for this request and could not: the page says why
	// and offers Connect. Left out when nothing failed or nothing was tried.
	ReconnectFailed []tracingReconnectFail `json:"reconnectFailed,omitempty"`
}

// tracingLaneRank orders lanes in the answer: audit, sql, iis, then any other.
func tracingLaneRank(key string) int {
	switch key {
	case "audit":
		return 0
	case calltrace.LaneSQL:
		return 1
	case calltrace.LaneIIS:
		return 2
	}
	return 3
}

// appendLane adds lane to resp.Lanes. A lane with the same Key is replaced, so
// calling twice never duplicates. The lanes are kept in the order audit, sql,
// iis (an unknown key after iis) whatever order the calls came in, and
// resp.Lanes is never nil afterwards. The lane's own arrays are made non-nil.
func appendLane(resp *tracingResponse, lane tracingLane) {
	if lane.Sources == nil {
		lane.Sources = []tracingRef{}
	}
	if lane.Buckets == nil {
		lane.Buckets = []tracingBucket{}
	}
	if lane.Events == nil {
		lane.Events = []tracingEvent{}
	}
	replaced := false
	for i := range resp.Lanes {
		if resp.Lanes[i].Key == lane.Key {
			resp.Lanes[i] = lane
			replaced = true
			break
		}
	}
	if !replaced {
		resp.Lanes = append(resp.Lanes, lane)
	}
	if resp.Lanes == nil {
		resp.Lanes = []tracingLane{}
	}
	sort.SliceStable(resp.Lanes, func(i, j int) bool {
		return tracingLaneRank(resp.Lanes[i].Key) < tracingLaneRank(resp.Lanes[j].Key)
	})
}

// finishTracingMeta sets the top-level truncated, allLanesStored and
// mixedConsistency from resp.Lanes only. It is idempotent and may be called
// again after another appendLane. Lanes in state no-data, clock-unchecked or
// disabled say nothing about consistency and are ignored for it.
func finishTracingMeta(resp *tracingResponse) {
	truncated, live := false, false
	seen := map[string]bool{}
	for _, l := range resp.Lanes {
		if l.Truncated {
			truncated = true
		}
		if l.Origin == tracingOriginLive {
			live = true
		}
		switch l.State {
		case calltrace.StateEvents, calltrace.StateNoneSeen, calltrace.StateIncomplete, calltrace.StateOutOfRange:
			seen[l.Consistency] = true
		}
	}
	resp.Truncated = truncated
	resp.AllLanesStored = len(resp.Lanes) > 0 && !live
	resp.MixedConsistency = len(seen) >= 2
}

// tracingSQLIISLanes builds the saved SQL and IIS lanes (in that order, both
// always returned) from the events of the selected case sources, and the
// warnings. c is the unwrapped case handle.
//
// Window rule: tracing is START-BASED and half-open, [axis.Start, axis.End),
// with NO lookback. This differs from /call-trace, which widens the read by
// callTraceLookback to catch a statement that began before the call. The tracing
// view plots an event where it starts, so a statement that began before the
// window and overlaps it is not shown.
//
// Limitation: EventQuery.Sources filters by importer NAME and an
// evidence.Event carries no import id. When an unselected import of the case has
// the same importer name as a selected one, its events can appear in events[] of
// that lane. The bucket counts stay exact because they filter by ImportID.
// Consequently scanned is the sum of the bucket counts and counts SELECTED
// imports only; in that same-importer case the lane can list more events than
// scanned.
func tracingSQLIISLanes(ctx context.Context, c casestore.Case, axis tracingAxis, sel tracingSelection, now time.Time) ([]tracingLane, []string, error) {
	src, warnings, err := resolveLaneSources(ctx, c, sel)
	if err != nil {
		return nil, nil, err
	}
	// Each lane has its own row-cap budget: a lane with many rows must not starve
	// the other one, and only a lane whose own read hit the cap is flagged.
	// Memory stays bounded: at most 2 x MaxEventQuery events are held at once.
	var evs []evidence.Event
	cappedBy := map[string]bool{}
	timedOutBy := map[string]bool{}
	for _, key := range []string{calltrace.LaneSQL, calltrace.LaneIIS} {
		got, capped, timedOut, err := readLaneEvents(ctx, c, axis, src.laneNames[key])
		if err != nil {
			return nil, nil, err
		}
		cappedBy[key] = capped
		timedOutBy[key] = timedOut
		evs = append(evs, got...)
	}
	built := calltrace.BuildLanes(calltrace.Window{From: axis.Start, To: axis.End}, true, src.infos, evs)
	reasonByID := httperrReasonsByID(evs)

	lanes := make([]tracingLane, 0, 2)
	for _, key := range []string{calltrace.LaneSQL, calltrace.LaneIIS} {
		var bl calltrace.Lane
		for _, l := range built {
			if l.Key == key {
				bl = l
			}
		}
		if cappedBy[key] || timedOutBy[key] {
			// A lane that came back with no event proves nothing when its read hit the
			// cap or ran out of time.
			bl = calltrace.MarkIncomplete([]calltrace.Lane{bl})[0]
		}
		lane := buildLane(key, bl, src.asOf, reasonByID, now)
		if src.unchecked[key] {
			lane.State = calltrace.StateClockUnchecked
			lane.Disabled = tracingDisabledTZ
			lane.Reason = tracingClockMsg
			lane.Hidden = 0
			lane.Events = []tracingEvent{} // an unconfirmed clock plots nothing
			lanes = append(lanes, lane)
			continue
		}
		if sp := src.span[key]; sp != nil && sp.known {
			from, to := sp.from, sp.to
			lane.ImportedFrom, lane.ImportedTo = &from, &to
			lane.ImportedPartial = sp.partial
		}
		if lane.State == calltrace.StateNoData {
			lane.ReasonCode, lane.Reason = emptyLaneReason(key, src)
		}
		if names := src.laneNames[key]; len(names) > 0 {
			if err := bucketLane(ctx, c, axis, &lane, names, src.selectedIDs, cappedBy[key], timedOutBy[key]); err != nil {
				return nil, nil, err
			}
		}
		lanes = append(lanes, lane)
	}
	return lanes, warnings, nil
}

// emptyLaneReason says why a lane with the state no-data is empty, when the
// selection knows: listed files that were never imported, or imports that were
// set aside. Both are empty when the selection holds nothing for the lane.
func emptyLaneReason(key string, src laneSources) (code, text string) {
	switch {
	case src.notImported[key] > 0 && key == "sql":
		return tracingReasonNotImported, tracingSQLNotImportedMsg
	case src.notImported[key] > 0:
		return tracingReasonNotImported, tracingNotImportedMsg
	case src.skipped[key] > 0:
		return tracingReasonSkipped, tracingSkippedMsg
	}
	return "", ""
}

// laneSources is what the selection says about the two lanes: the source
// infos for calltrace, the importer names to read per lane, the lanes whose
// clock is unconfirmed, the latest import time per lane and the import ids
// that count.
type laneSources struct {
	infos       []calltrace.SourceInfo
	laneNames   map[string][]string  // lane key -> importer names to read
	unchecked   map[string]bool      // lane key -> a selected file has no confirmed rule
	asOf        map[string]time.Time // lane key -> latest Imported
	selectedIDs map[int64]bool
	notImported map[string]int           // lane key -> selected sources whose file was never imported
	skipped     map[string]int           // lane key -> selected imports that were quarantined or failed
	span        map[string]*importedSpan // lane key -> time range of its imported files
}

// importedSpan is the earliest and latest time held by a lane's imported files.
// known means at least one file had a recorded range. partial is set when some
// file had none, so the shown range is never presented as the whole.
type importedSpan struct {
	from, to time.Time
	known    bool
	seen     bool
	partial  bool
}

func (s *importedSpan) add(first, last *time.Time) {
	if first == nil || last == nil {
		s.partial, s.seen = true, true
		return
	}
	f, l := first.UTC(), last.UTC()
	if !s.known {
		s.from, s.to, s.known, s.seen = f, l, true, true
		return
	}
	s.seen = true
	if f.Before(s.from) {
		s.from = f
	}
	if l.After(s.to) {
		s.to = l
	}
}

// resolveLaneSources maps the selected sources to imports and works out, per
// lane, what to read. The warnings are the no-file, not-a-lane and ambiguous
// ones, in that order; the slice is never nil.
func resolveLaneSources(ctx context.Context, c casestore.Case, sel tracingSelection) (laneSources, []string, error) {
	warnings := []string{}
	refByImport := map[int64]tracingRef{}
	noFile := false
	notImported := map[string]int{}
	for _, ref := range sel.Refs {
		id := sel.ImportOf[ref.SourceID]
		if id == 0 {
			noFile = true
			if lane := calltrace.LaneOf(sel.KindOf[ref.SourceID]); lane == calltrace.LaneSQL || lane == calltrace.LaneIIS {
				notImported[lane]++
			}
			continue
		}
		if _, dup := refByImport[id]; !dup {
			refByImport[id] = ref
		}
	}
	if noFile {
		warnings = append(warnings, tracingNoFileWarn)
	}

	var recs []casestore.ImportRecord
	if len(refByImport) > 0 {
		var err error
		if recs, err = c.Imports(ctx); err != nil {
			return laneSources{}, nil, err
		}
	}

	var out laneSources
	nameValid := map[string]bool{}  // importer name -> every selected file of it has a confirmed rule
	nameLane := map[string]string{} // importer name -> lane key
	nameIDs := map[string]map[string]bool{}
	out.asOf = map[string]time.Time{}
	out.selectedIDs = map[int64]bool{}
	out.notImported = notImported
	out.skipped = map[string]int{}
	out.span = map[string]*importedSpan{}
	notLane := false
	for _, rec := range recs {
		ref, picked := refByImport[rec.ID]
		if !picked {
			continue
		}
		if rec.Status != casestore.StatusOK && rec.Status != "" {
			if lane := calltrace.LaneOf(rec.Importer); lane == calltrace.LaneSQL || lane == calltrace.LaneIIS {
				out.skipped[lane]++
			}
			continue
		}
		lane := calltrace.LaneOf(rec.Importer)
		if lane != calltrace.LaneSQL && lane != calltrace.LaneIIS {
			notLane = true
			continue
		}
		info := calltrace.SourceInfo{Name: rec.Importer, Validated: traceRuleValidated(rec), SourceID: ref.SourceID, Label: ref.Label}
		first, last := rec.Quality.CoverageFirst, rec.Quality.CoverageLast
		if first != nil && last != nil && !first.IsZero() && !last.IsZero() && !last.Before(*first) {
			info.From, info.To = first.UTC(), last.UTC()
		}
		out.infos = append(out.infos, info)
		out.selectedIDs[rec.ID] = true
		sp := out.span[lane]
		if sp == nil {
			sp = &importedSpan{}
			out.span[lane] = sp
		}
		sp.add(rec.Quality.CoverageFirst, rec.Quality.CoverageLast)
		nameLane[rec.Importer] = lane
		if v, seen := nameValid[rec.Importer]; !seen {
			nameValid[rec.Importer] = info.Validated
		} else {
			nameValid[rec.Importer] = v && info.Validated
		}
		if nameIDs[rec.Importer] == nil {
			nameIDs[rec.Importer] = map[string]bool{}
		}
		nameIDs[rec.Importer][ref.SourceID] = true
		if t := rec.Imported.UTC(); t.After(out.asOf[lane]) {
			out.asOf[lane] = t
		}
	}
	if notLane {
		warnings = append(warnings, tracingNotLaneWarn)
	}
	ambiguous := make([]string, 0)
	for name, ids := range nameIDs {
		if len(ids) > 1 {
			ambiguous = append(ambiguous, name)
		}
	}
	sort.Strings(ambiguous)
	for _, name := range ambiguous {
		warnings = append(warnings, "Events from "+name+" cannot be tied to one source because two selected files use it.")
	}

	// Per lane: the importer names to read, and whether a clock is unconfirmed.
	// A lane with an unconfirmed clock plots nothing, so it reads nothing.
	out.unchecked = map[string]bool{}
	out.laneNames = map[string][]string{}
	for name, lane := range nameLane {
		if !nameValid[name] {
			out.unchecked[lane] = true
		}
	}
	for name, lane := range nameLane {
		if !out.unchecked[lane] {
			out.laneNames[lane] = append(out.laneNames[lane], name)
		}
	}
	for _, lane := range []string{calltrace.LaneSQL, calltrace.LaneIIS} {
		sort.Strings(out.laneNames[lane])
	}
	return out, warnings, nil
}

// readLaneEvents reads the events of the named importers of ONE lane in the axis
// window, start-based and half open. capped is true when the read hit the row
// cap (casestore.MaxEventQuery, one budget per call), so events of these names
// may be missing. timedOut is true when the read passed tracingLaneBudget: evs
// then holds the oldest events read so far, and events after them may be
// missing. No names reads nothing.
func readLaneEvents(ctx context.Context, c casestore.Case, axis tracingAxis, names []string) (evs []evidence.Event, capped, timedOut bool, err error) {
	if len(names) == 0 {
		return nil, false, false, nil
	}
	got, err := c.Events(ctx, casestore.EventQuery{
		Window:      importer.Window{From: axis.Start, To: axis.End},
		Sources:     names,
		Limit:       casestore.MaxEventQuery,
		MaxScanTime: tracingLaneBudget,
	})
	if errors.Is(err, casestore.ErrScanBudget) && ctx.Err() == nil {
		timedOut, err = true, nil // keep the events read so far
	}
	if err != nil {
		return nil, false, false, err
	}
	capped = len(got) >= casestore.MaxEventQuery
	// The store window is inclusive at the end; tracing is half open.
	evs = make([]evidence.Event, 0, len(got))
	for _, e := range got {
		if e.Start.Before(axis.End) {
			evs = append(evs, e)
		}
	}
	return evs, capped, timedOut, nil
}

// httperrReasonsByID carries only the reasons the importer itself produced
// (iis.KnownHTTPERRReason), keyed by EvidenceID so the lookup reaches the right
// event even when several HTTP.sys error rows share a timestamp. No other
// attribute of an iis-httperr event is read here.
func httperrReasonsByID(evs []evidence.Event) map[string]string {
	reasonByID := map[string]string{}
	for _, e := range evs {
		if e.Source != sourceHTTPErr {
			continue
		}
		if r := e.Attrs["reason"]; iis.KnownHTTPERRReason(r) {
			reasonByID[e.EvidenceID] = r
		}
	}
	return reasonByID
}

// buildLane makes the wire lane for key from the calltrace lane bl (the zero
// Lane when calltrace made none): label, sources and event markers. Buckets
// stay empty; bucketLane fills them. An unconfirmed clock is applied by the
// caller.
func buildLane(key string, bl calltrace.Lane, asOf map[string]time.Time, reasonByID map[string]string, now time.Time) tracingLane {
	lane := tracingLane{
		Key: key, Label: bl.Label, State: bl.State,
		Origin: tracingOriginCase, AsOf: now.UTC(), Complete: true, Consistency: tracingConsStored,
		Sources: make([]tracingRef, 0, len(bl.SourceRefs)), Buckets: []tracingBucket{}, Events: []tracingEvent{},
		Hidden: bl.Hidden,
	}
	if lane.Label == "" {
		lane.Label = key
	}
	if t, ok := asOf[key]; ok {
		lane.AsOf = t
	}
	for _, r := range bl.SourceRefs {
		lane.Sources = append(lane.Sources, tracingRef{SourceID: r.SourceID, Label: r.Label})
	}
	for _, e := range bl.Events {
		te := tracingEvent{
			ID: e.ID, SourceID: e.SourceID, Label: e.Label, Start: e.Start, End: e.End,
			DurationMs: e.DurationMs, Severity: e.Severity, StatusClass: e.StatusClass, Win32: e.Win32,
		}
		if key == calltrace.LaneIIS && e.Source == sourceHTTPErr {
			te.Kind = sourceHTTPErr
			te.End = te.Start
			te.Label = httperrLabel(reasonByID[e.ID])
		}
		lane.Events = append(lane.Events, te)
	}
	return lane
}

// bucketLane sets lane.Scanned, lane.Buckets and the truncation fields from the
// store's bucket counts for the importer names of the lane. Only imports in
// selectedIDs are counted. capped is the row-cap result of this lane's own event
// read. The iis lane also gets errors on every bucket: the rows whose status is
// 5xx (zero where there are none). HTTP.sys error markers (source iis-httperr) are
// counted as events but never as errors. The sql lane leaves errors out.
// eventsTimedOut is true when this lane's own event read ran out of time.
//
// When the count passes its time budget the lane keeps the cells counted so far:
// those buckets stay complete, the later ones are marked incomplete, and the lane
// is partial with reason "timeout".
func bucketLane(ctx context.Context, c casestore.Case, axis tracingAxis, lane *tracingLane, names []string, selectedIDs map[int64]bool, capped, eventsTimedOut bool) error {
	withErrors := lane.Key == calltrace.LaneIIS
	// c must be the unwrapped handle: a *sharedCase hides BucketCounter and
	// CountBuckets would fall back to the 50 000 row scan.
	res, err := tracingCountBuckets(ctx, c, casestore.BucketQuery{
		AxisStart: axis.AxisStart, From: axis.Start, To: axis.End, WidthMs: axis.WidthMs, Sources: names,
		CountErrors: withErrors, CountClientErrors: withErrors, CountTimings: withErrors,
		// An HTTP.sys marker is not a request, so it is never an error; the store
		// skips these rows when counting errors.
		ErrorExcludeSources: []string{sourceHTTPErr},
		MaxTime:             tracingLaneBudget,
	})
	if err != nil {
		return err
	}
	perCell := map[int]int64{}
	errCell := map[int]int64{}
	clientCell := map[int]int64{}
	var total int64
	for _, bc := range res.Counts {
		if bc.ImportID != 0 && !selectedIDs[bc.ImportID] {
			continue
		}
		perCell[bc.Index] += bc.Count
		errCell[bc.Index] += bc.Errors
		clientCell[bc.Index] += bc.ClientErrors
		total += bc.Count
	}
	lane.Scanned = total
	if !withErrors {
		errCell, clientCell = nil, nil
	}
	lane.Buckets = tracingBuckets(axis, perCell, errCell)
	if withErrors {
		timing := map[int]*latencyAcc{}
		for _, bt := range res.Timings {
			if bt.ImportID != 0 && !selectedIDs[bt.ImportID] {
				continue
			}
			acc := timing[bt.Index]
			if acc == nil {
				acc = &latencyAcc{}
				timing[bt.Index] = acc
			}
			acc.add([]audit.RollupWireBin{{Bin: bt.Bin, Count: bt.Count, Sum: bt.Sum, Min: bt.Min, Max: bt.Max}})
		}
		// Buckets are in axis order but skip unusable cells, so find each by its start.
		idx := map[time.Time]int{}
		for k := 0; k < axis.Buckets; k++ {
			if from, _, _, ok := axis.Bucket(k); ok {
				idx[from] = k
			}
		}
		for i := range lane.Buckets {
			k := idx[lane.Buckets[i].Start]
			if n := clientCell[k]; n > 0 {
				lane.Buckets[i].ClientErrors = &n
			}
			if acc := timing[k]; acc != nil {
				lane.Buckets[i].TimedCount, lane.Buckets[i].P50Ms, lane.Buckets[i].P95Ms, lane.Buckets[i].MaxMs = acc.figures()
				// The IIS log writes the time a request ENDED, so this busy time sits in the step the request ended in (the API lane counts the step it started in).
				lane.Buckets[i].BusyMs = acc.busy()
			}
		}
	}
	if capped {
		lane.Truncated = true
		lane.ReasonCode = tracingReasonRowCap
		lane.Reason = tracingRowCapMsg
	}
	if eventsTimedOut {
		lane.Complete = false
		lane.Truncated = true
		lane.ReasonCode = tracingReasonTimeout
		lane.Reason = tracingTimeoutMsg
	}
	if res.Partial {
		lane.Complete = false
		lane.Truncated = true
		lane.ReasonCode = tracingReasonBudget
		lane.Reason = tracingScanBudget
		if res.TimedOut {
			lane.ReasonCode = tracingReasonTimeout
			lane.Reason = tracingTimeoutMsg
			// Cells from DoneTo on were not counted: do not call them complete.
			for i := range lane.Buckets {
				if !lane.Buckets[i].Start.Before(res.DoneTo) {
					lane.Buckets[i].Complete = false
				}
			}
		}
	}
	return nil
}

// httperrLabel builds the label for an HTTP.sys error marker. reason is ""
// when the event carries no reason the importer recognised (reasonByID has no
// entry for it). The 40-rune cap matches EventLabel's maxLabelLen, so a
// reason that would overflow the prefixed form falls back to the reason alone,
// and one that overflows even alone falls back to the bare prefix.
func httperrLabel(reason string) string {
	const prefix = "HTTP.sys error"
	if reason == "" {
		return prefix
	}
	full := prefix + ": " + reason
	if len([]rune(full)) <= 40 {
		return full
	}
	if len([]rune(reason)) <= 40 {
		return reason
	}
	return prefix
}

// tracingBuckets is one bucket per usable cell of the axis, zero-filled. A
// non-nil errCell adds errors to every bucket (0 where the cell has none); nil
// leaves the key out.
func tracingBuckets(axis tracingAxis, perCell, errCell map[int]int64) []tracingBucket {
	out := make([]tracingBucket, 0, axis.Buckets)
	for k := 0; k < axis.Buckets; k++ {
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		b := tracingBucket{Start: from, End: to, Count: perCell[k], Clipped: clipped, Complete: true}
		if errCell != nil {
			n := errCell[k]
			b.Errors = &n
		}
		out = append(out, b)
	}
	return out
}

// caseTracing answers POST /api/cases/{id}/tracing.
func (h *handler) caseTracing(w http.ResponseWriter, r *http.Request, raw string) {
	var req tracingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := req.problem(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ids := req.tracingIDs()
	for _, id := range ids {
		if _, _, ok := parseTracingSource(id); !ok {
			writeError(w, http.StatusBadRequest, tracingBadSourceMsg)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	caseID, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	sel, fail := resolveTracingSources(ctx, unwrapCase(c), ids)
	if fail != nil {
		writeCaseFailure(w, fail.Status, fail.Reason, fail.Message)
		return
	}
	// Audit sources are checked against the listing before anything is read. An
	// unlisted explicit id is a 404; a listing failure gives an incomplete audit
	// lane plus a warning (below). No id or error text is kept.
	// Saved audit databases that were open before a restart are opened again first (bounded; a failure never fails the trace), so the
	// listing below already holds them. A request that names only case files lists no audit sources, so nothing is reopened for it.
	var reconnectFailed []tracingReconnectFail
	if (!sel.Explicit || len(sel.AuditIDs) > 0) && len(sel.SavedAudit) > 0 {
		// Only the databases this case saved are reopened; the others wait for the start-up pass or for the person.
		reconnectFailed = h.reconnectSaved(ctx, tracingReconnectBudget, sel.SavedAudit)
	}
	auditRefs, auditItems, auditWarn, auditErr := selectAuditSourcesListed(ctx, h.repo, sel.Explicit, sel.AuditIDs)
	if errors.Is(auditErr, errUnknownAuditSource) {
		writeCaseFailure(w, http.StatusNotFound, "unknown-source", tracingUnknownMsg)
		return
	}
	now := time.Now().UTC()
	start, end := req.Incident.Start.UTC(), req.Incident.End.UTC()
	axis := newTracingAxis(start, end)
	win := tracingWindow{Start: start, End: end, AxisStart: axis.AxisStart, AxisEnd: axis.AxisEnd,
		BucketSeconds: int(axis.WidthMs / 1000), Buckets: axis.Buckets}
	resp := tracingResponse{
		CaseID:     string(caseID),
		RequestSeq: req.RequestSeq,
		AsOf:       now,
		Window:     win,
		Sources:    sel.Refs,
		Lanes:      []tracingLane{},
	}
	lanes, warns, err := tracingSQLIISLanes(ctx, unwrapCase(c), axis, sel, now)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// The api-seen fallback reads the live audit repository (one SQLBusy call per
	// request, only when the SQL lane would otherwise be empty). The reader is
	// asserted once, like the audit lane's RollupReader below; a repository that
	// does not implement it leaves the lane untouched.
	sqlBusy, _ := h.repo.(audit.SQLBusyReader)
	for _, lane := range lanes {
		if lane.Key == calltrace.LaneSQL && lane.Disabled == "" && (lane.State == calltrace.StateNoData || lane.State == calltrace.StateOutOfRange || lane.State == calltrace.StateEvents) {
			if held, ok := tracingHeldSQLLane(ctx, unwrapCase(c), axis, now); ok {
				if lane.State == calltrace.StateNoData || lane.State == calltrace.StateOutOfRange {
					imported := lane
					lane = held
					// The imported files' time range stays on the lane the owner sees.
					if lane.ImportedFrom == nil {
						lane.ImportedFrom = imported.ImportedFrom
					}
					if lane.ImportedTo == nil {
						lane.ImportedTo = imported.ImportedTo
					}
					if !lane.ImportedPartial {
						lane.ImportedPartial = imported.ImportedPartial
					}
				} else if layered, did := layerSQLLane(lane, held); did {
					lane = layered
				}
			}
		}
		lane, err = tracingQSFileLane(ctx, unwrapCase(c), axis, sel, lane)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		lane, err = tracingAPISseenLane(ctx, sqlBusy, axis, lane, time.Now)
		if err != nil {
			if r.Context().Err() != nil {
				return // the client went away; nothing to write
			}
			writeRepoError(w, err)
			return
		}
		appendLane(&resp, lane)
	}
	// The open audit sources and the saved audit databases no open connection
	// carries. Both need the audit listing, so a request that made no listing call
	// (only case files named) leaves them out.
	var savedNot []string
	if auditErr == nil && (auditItems != nil || !sel.Explicit) {
		resp.AuditSources = auditListingRefs(auditItems)
		savedNot = savedNotConnectedNames(sel.SavedAudit, auditItems)
		resp.SavedNotConnected = savedNot
		resp.ReconnectFailed = reconnectFailedFor(reconnectFailed, auditItems)
	}
	// The selection warnings come first, then the lane ones, then the audit ones.
	resp.Warnings = append(append(make([]string, 0, len(sel.Warnings)+len(warns)+2), sel.Warnings...), warns...)
	switch {
	case auditErr != nil:
		resp.Warnings = append(resp.Warnings, tracingAuditListWarn)
		// The lane stays in the answer, empty and incomplete, so the window is not
		// mistaken for one that simply found nothing.
		lane := auditLaneToWire(auditFailure(time.Now().UTC(), auditCodeError, tracingAuditListWarn), nil)
		appendLane(&resp, lane)
	case len(auditRefs) > 0:
		if auditWarn != "" {
			resp.Warnings = append(resp.Warnings, auditWarn)
		}
		auditIDs := make([]string, len(auditRefs))
		for i, ref := range auditRefs {
			auditIDs[i] = ref.SourceID
		}
		reader, _ := h.repo.(audit.RollupReader)
		// The live lane stamps its own read time (time.Now, not now): it is read
		// after the case lanes, and its asOf says when the live data was fresh.
		read := readAuditLane(ctx, reader, auditIDs, axis, time.Now)
		if r.Context().Err() != nil {
			return // the client went away; nothing to write
		}
		lane := auditLaneToWire(read, auditRefs)
		lane.SavedNotConnected = savedNot
		appendLane(&resp, lane)
	case len(savedNot) > 0:
		// The case saved audit databases but none is open: the lane says so instead
		// of leaving the audit rows out as if nothing had been asked.
		lane := auditSavedOnlyLane(now, savedNot)
		if reason, said := reconnectFailedReason(resp.ReconnectFailed, savedNot); reason != "" {
			lane.Reason = reason
			// The lane says it, so the notes under the chart do not repeat it.
			rest := make([]tracingReconnectFail, 0, len(resp.ReconnectFailed))
			for _, f := range resp.ReconnectFailed {
				if f != *said {
					rest = append(rest, f)
				}
			}
			resp.ReconnectFailed = rest
			if len(rest) == 0 {
				resp.ReconnectFailed = nil
			}
		}
		appendLane(&resp, lane)
	}
	finishTracingMeta(&resp)
	writeJSON(w, http.StatusOK, resp, maxResponseBytes)
}
