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
	tracingTooLongMsg   = "A time window can be at most 30 days long."
	tracingUnknownMsg   = "That source is not part of this case."

	tracingNoFileWarn   = "A selected source has no file yet, so it shows nothing."
	tracingNotLaneWarn  = "A selected source is not an SQL or IIS source, so it is not shown."
	tracingClockMsg     = "The time zone rule of a file is not confirmed, so its events are not placed on the timeline."
	tracingRowCapMsg    = "Only the first 50,000 saved events were listed; the bucket counts cover every saved event."
	tracingScanBudget   = "The scan of saved events stopped early, so the counts may be too low."
	tracingConsStored   = "stored-as-of"
	tracingOriginCase   = "case"
	tracingOriginLive   = "live"
	tracingReasonRowCap = "rowcap"
	tracingReasonBudget = "scan-budget"
	tracingDisabledTZ   = "tz-rule"
)

// tracingCountBuckets is the bucket aggregate behind the SQL and IIS lanes. It is
// a package variable only so a test can watch the handle it is given.
var tracingCountBuckets = casestore.CountBuckets

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
// is empty, and imports only when an imp_ id is present or ids is empty.
func resolveTracingSources(ctx context.Context, c casestore.Case, ids []string) (tracingSelection, *tracingFailure) {
	sel := tracingSelection{
		Refs:     []tracingRef{},
		ImportOf: map[string]int64{},
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
			}
		}
		// A case that cannot hold tags has none: a cs_ id then falls through to
		// the unknown-source answer below.
	}
	importByID := map[int64]casestore.ImportRecord{}
	var imports []casestore.ImportRecord
	if needImports {
		var err error
		imports, err = c.Imports(ctx)
		if err != nil {
			return sel, tracingStoreFailure()
		}
		for _, im := range imports {
			importByID[im.ID] = im
		}
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
				sel.ImportOf[id] = t.ImportID
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
		return sel, nil
	}

	// Default call: every tag (oldest first), then every usable import no tag
	// references (in id order), cut to the first 50.
	linked := map[int64]bool{}
	refs := make([]tracingRef, 0, len(tags)+len(imports))
	for _, t := range tags {
		sid := tracingTagID(t.ID)
		refs = append(refs, tracingRef{SourceID: sid, Label: tracingLabel(t.Label, t.Kind)})
		sel.ImportOf[sid] = t.ImportID
		if t.ImportID > 0 {
			linked[t.ImportID] = true
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
	return sel, nil
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
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Count    int64     `json:"count"`
	Errors   *int64    `json:"errors,omitempty"`
	Clipped  bool      `json:"clipped"`
	Complete bool      `json:"complete"`
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
	warnings := []string{}
	refByImport := map[int64]tracingRef{}
	noFile := false
	for _, ref := range sel.Refs {
		id := sel.ImportOf[ref.SourceID]
		if id == 0 {
			noFile = true
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
			return nil, nil, err
		}
	}

	var infos []calltrace.SourceInfo
	nameValid := map[string]bool{}  // importer name -> every selected file of it has a confirmed rule
	nameLane := map[string]string{} // importer name -> lane key
	nameIDs := map[string]map[string]bool{}
	asOf := map[string]time.Time{} // lane key -> latest Imported
	selectedIDs := map[int64]bool{}
	notLane := false
	for _, rec := range recs {
		ref, picked := refByImport[rec.ID]
		if !picked || (rec.Status != casestore.StatusOK && rec.Status != "") {
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
		infos = append(infos, info)
		selectedIDs[rec.ID] = true
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
		if t := rec.Imported.UTC(); t.After(asOf[lane]) {
			asOf[lane] = t
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
	unchecked := map[string]bool{}
	laneNames := map[string][]string{}
	for name, lane := range nameLane {
		if !nameValid[name] {
			unchecked[lane] = true
		}
	}
	for name, lane := range nameLane {
		if !unchecked[lane] {
			laneNames[lane] = append(laneNames[lane], name)
		}
	}
	var readNames []string
	for _, lane := range []string{calltrace.LaneSQL, calltrace.LaneIIS} {
		sort.Strings(laneNames[lane])
		readNames = append(readNames, laneNames[lane]...)
	}

	var evs []evidence.Event
	capped := false
	if len(readNames) > 0 {
		got, err := c.Events(ctx, casestore.EventQuery{
			Window:  importer.Window{From: axis.Start, To: axis.End},
			Sources: readNames,
			Limit:   casestore.MaxEventQuery,
		})
		if err != nil {
			return nil, nil, err
		}
		capped = len(got) >= casestore.MaxEventQuery
		// The store window is inclusive at the end; tracing is half open.
		evs = make([]evidence.Event, 0, len(got))
		for _, e := range got {
			if e.Start.Before(axis.End) {
				evs = append(evs, e)
			}
		}
	}
	built := calltrace.BuildLanes(calltrace.Window{From: axis.Start, To: axis.End}, true, infos, evs)
	if capped {
		built = calltrace.MarkIncomplete(built)
	}

	// reasonByID carries only the reasons the importer itself produced
	// (iis.KnownHTTPERRReason), keyed by EvidenceID so the lookup below
	// reaches the right event even when several HTTP.sys error rows share a
	// timestamp. No other attribute of an iis-httperr event is read here.
	reasonByID := map[string]string{}
	for _, e := range evs {
		if e.Source != "iis-httperr" {
			continue
		}
		if r := e.Attrs["reason"]; iis.KnownHTTPERRReason(r) {
			reasonByID[e.EvidenceID] = r
		}
	}

	lanes := make([]tracingLane, 0, 2)
	for _, key := range []string{calltrace.LaneSQL, calltrace.LaneIIS} {
		var bl calltrace.Lane
		for _, l := range built {
			if l.Key == key {
				bl = l
			}
		}
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
		if unchecked[key] {
			lane.State = calltrace.StateClockUnchecked
			lane.Disabled = tracingDisabledTZ
			lane.Reason = tracingClockMsg
			lane.Hidden = 0
			lanes = append(lanes, lane)
			continue
		}
		for _, e := range bl.Events {
			te := tracingEvent{
				ID: e.ID, SourceID: e.SourceID, Label: e.Label, Start: e.Start, End: e.End,
				DurationMs: e.DurationMs, Severity: e.Severity, StatusClass: e.StatusClass, Win32: e.Win32,
			}
			if key == calltrace.LaneIIS && e.Source == "iis-httperr" {
				te.Kind = "iis-httperr"
				te.End = te.Start
				te.Label = httperrLabel(reasonByID[e.ID])
			}
			lane.Events = append(lane.Events, te)
		}
		if names := laneNames[key]; len(names) > 0 {
			// c must be the unwrapped handle: a *sharedCase hides BucketCounter and
			// CountBuckets would fall back to the 50 000 row scan.
			res, err := tracingCountBuckets(ctx, c, casestore.BucketQuery{
				AxisStart: axis.AxisStart, From: axis.Start, To: axis.End, WidthMs: axis.WidthMs, Sources: names,
			})
			if err != nil {
				return nil, nil, err
			}
			perCell := map[int]int64{}
			var total int64
			for _, bc := range res.Counts {
				if bc.ImportID != 0 && !selectedIDs[bc.ImportID] {
					continue
				}
				perCell[bc.Index] += bc.Count
				total += bc.Count
			}
			lane.Scanned = total
			lane.Buckets = tracingBuckets(axis, perCell)
			if capped {
				lane.Truncated = true
				lane.ReasonCode = tracingReasonRowCap
				lane.Reason = tracingRowCapMsg
			}
			if res.Partial {
				lane.Complete = false
				lane.Truncated = true
				lane.ReasonCode = tracingReasonBudget
				lane.Reason = tracingScanBudget
			}
		}
		lanes = append(lanes, lane)
	}
	return lanes, warnings, nil
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

// tracingBuckets is one bucket per usable cell of the axis, zero-filled.
func tracingBuckets(axis tracingAxis, perCell map[int]int64) []tracingBucket {
	out := make([]tracingBucket, 0, axis.Buckets)
	for k := 0; k < axis.Buckets; k++ {
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		out = append(out, tracingBucket{Start: from, End: to, Count: perCell[k], Clipped: clipped, Complete: true})
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
	// unlisted explicit id is a 404; a listing failure only drops the audit lane
	// (a warning below). No id or error text is kept.
	auditRefs, auditWarn, auditErr := selectAuditSources(ctx, h.repo, sel.Explicit, sel.AuditIDs)
	if errors.Is(auditErr, errUnknownAuditSource) {
		writeCaseFailure(w, http.StatusNotFound, "unknown-source", tracingUnknownMsg)
		return
	}
	start, end := req.Incident.Start.UTC(), req.Incident.End.UTC()
	axis := newTracingAxis(start, end)
	win := tracingWindow{Start: start, End: end, AxisStart: axis.AxisStart, AxisEnd: axis.AxisEnd,
		BucketSeconds: int(axis.WidthMs / 1000), Buckets: axis.Buckets}
	resp := tracingResponse{
		CaseID:     string(caseID),
		RequestSeq: req.RequestSeq,
		AsOf:       time.Now().UTC(),
		Window:     win,
		Warnings:   sel.Warnings,
		Sources:    sel.Refs,
		Lanes:      []tracingLane{},
	}
	lanes, warns, err := tracingSQLIISLanes(ctx, unwrapCase(c), axis, sel, resp.AsOf)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, lane := range lanes {
		appendLane(&resp, lane)
	}
	resp.Warnings = append(append([]string{}, sel.Warnings...), warns...)
	switch {
	case auditErr != nil:
		resp.Warnings = append(resp.Warnings, tracingAuditListWarn)
	case len(auditRefs) > 0:
		if auditWarn != "" {
			resp.Warnings = append(resp.Warnings, auditWarn)
		}
		auditIDs := make([]string, len(auditRefs))
		for i, ref := range auditRefs {
			auditIDs[i] = ref.SourceID
		}
		reader, _ := h.repo.(audit.RollupReader)
		read := readAuditLane(ctx, reader, auditIDs, axis, time.Now)
		if r.Context().Err() != nil {
			return // the client went away; nothing to write
		}
		appendLane(&resp, auditLaneToWire(read, auditRefs))
	}
	finishTracingMeta(&resp)
	writeJSON(w, http.StatusOK, resp, maxResponseBytes)
}
