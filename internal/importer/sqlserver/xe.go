package sqlserver

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/join"
)

// The system_health session records wait_info only for lock waits over 30 s
// and latch waits over 15 s (and wait_info_external for preemptive waits over
// 5 s). A shorter wait never appears, so absence is worded with this
// threshold and never as "SQL was not waiting" (R8-B2).
const (
	XEName = "sql-xe"
	// XESource is the source name used in §6.6 wordings.
	XESource = "system_health"
	// XEThreshold is SourceQuality.Threshold for every XE import (card P3).
	XEThreshold = "lock waits over 30 s, latch waits over 15 s"
	// XEAbsenceThreshold is the threshold phrase of the R8-B2 absence wording.
	XEAbsenceThreshold = "lock waits over 30 s and latch waits over 15 s"
)

// Event kinds emitted by the XE importer.
const (
	KindWait        = "wait"
	KindDiagnostics = "diagnostics"
	KindError       = "error"
	KindNonYielding = "non-yielding"
	KindDeadlock    = "deadlock"
)

// xeKinds is the allowlist of system_health event names. Any other event name
// is counted (Rows) but not imported.
var xeKinds = map[string]string{
	"wait_info":                              KindWait,
	"wait_info_external":                     KindWait,
	"sp_server_diagnostics_component_result": KindDiagnostics,
	"error_reported":                         KindError,
	"scheduler_monitor_non_yielding_ring_buffer_recorded":      KindNonYielding,
	"scheduler_monitor_non_yielding_iocp_ring_buffer_recorded": KindNonYielding,
	"scheduler_monitor_non_yielding_rm_ring_buffer_recorded":   KindNonYielding,
	"xml_deadlock_report": KindDeadlock,
}

// xeScrubFragments: a header whose letters and digits contain any of these
// quarantines the file (unscrubbed-column). The card's list is sql_text,
// statement, input_buffer, xml_report, client_hostname, username,
// nt_username and database_name; the extra fragments close obvious variants
// (deadlock and blocked-process reports, raw event_data XML, query plans,
// batch text, principal and application names, error message text). The R12
// DBA dry run confirms or extends this list before any real export.
var xeScrubFragments = []string{
	"sqltext", "statement", "inputbuffer", "xmlreport", "clienthostname", "username", "ntusername", "databasename",
	"report", "xml", "eventdata", "hostname", "text", "queryplan", "showplan", "principal", "clientappname",
	"appname", "message", "login",
}

// xeRoles maps a normalised header (lower case, runs of other characters
// collapsed to "_") to the field it fills. Every other column is dropped.
var xeRoles = map[string]string{
	"timestamp_utc":        "ts",
	"name":                 "event",
	"event_name":           "event",
	"event":                "event",
	"wait_type":            "wait_type",
	"duration":             "duration",
	"duration_ms":          "duration",
	"component":            "component",
	"state":                "state",
	"state_desc":           "state",
	"error_number":         "error",
	"error":                "error",
	"severity":             "severity",
	"server_instance_name": "host",
}

var xeAttrs = map[string]bool{
	"event": true, "wait_type": true, "wait_class": true, "component": true, "state": true,
	"error_number": true, "sql_severity": true,
}

// xeWaitTimestampIsEnd: wait_info and wait_info_external fire when the wait
// completes, so the row time is the end and the start is time - duration.
const xeWaitTimestampIsEnd = true

const (
	gapXEWaitStart = "wait start is the event time minus its duration (wait_info fires when the wait ends)"
	gapXERetention = "system_health keeps a small rolling file target; an interval outside the export's coverage was not recorded"
	gapXEOther     = "system_health events of other types were counted, not imported"
)

type xeImporter struct{}

// NewXE returns the system_health Extended Events CSV importer ("sql-xe").
func NewXE() importer.Importer { return xeImporter{} }

func (xeImporter) Name() string { return XEName }

func (xeImporter) AllowedAttrs() map[string]bool {
	out := make(map[string]bool, len(xeAttrs))
	for k := range xeAttrs {
		out[k] = true
	}
	return out
}

func (xeImporter) DefaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 512 << 20, MaxRows: 2_000_000, MaxLineBytes: 64 << 10, MaxFields: 4096, MaxRejectFraction: 0.05}
}

// normHeader lower-cases a header and collapses every run of characters other
// than letters and digits into one "_" ("timestamp (UTC)" -> "timestamp_utc").
func normHeader(h string) string {
	var b strings.Builder
	us := false
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			us = false
			continue
		}
		if !us && b.Len() > 0 {
			b.WriteByte('_')
			us = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// scrubKey keeps letters and digits only, for fragment matching.
func scrubKey(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unscrubbed(header string) bool {
	k := scrubKey(header)
	for _, f := range xeScrubFragments {
		if strings.Contains(k, f) {
			return true
		}
	}
	return false
}

func splitHeader(line string) []string {
	parts := strings.Split(line, ",")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"`)
	}
	return parts
}

// Detect scores a CSV whose header has timestamp_utc and an event-name
// column. App Insights exports (itemType, operation_Id) score 0.
func (xeImporter) Detect(head []byte, fileName string) int {
	lines := firstLines(headText(head), 1)
	if len(lines) == 0 {
		return 0
	}
	roles := map[string]bool{}
	for _, h := range splitHeader(lines[0]) {
		n := normHeader(h)
		if n == "itemtype" || n == "operation_id" {
			return 0
		}
		if r, ok := xeRoles[n]; ok {
			roles[r] = true
		}
	}
	if !roles["ts"] {
		return 0
	}
	score := 30
	if roles["event"] {
		score += 40
	}
	if roles["wait_type"] || roles["component"] || roles["duration"] {
		score += 20
	}
	if strings.HasSuffix(strings.ToLower(fileName), ".csv") {
		score += 10
	}
	return score
}

// xeTimeLayouts accept datetime2(7) as fn_xe_file_target_read_file returns
// it, with a space or "T" separator and an optional "Z". Go accepts any
// fractional digits after the seconds.
var xeTimeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04:05Z", "2006-01-02T15:04:05Z", "2006-01-02T15:04:05-07:00", "2006-01-02 15:04:05-07:00"}

func parseUTCField(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, l := range xeTimeLayouts {
		t, err := time.Parse(l, v)
		if err != nil {
			continue
		}
		if _, off := t.Zone(); off != 0 {
			return time.Time{}, false // a "UTC" field carrying a non-zero offset is not trusted
		}
		return t.UTC(), true
	}
	return time.Time{}, false
}

func waitClass(waitType string) string {
	w := strings.ToUpper(waitType)
	switch {
	case strings.HasPrefix(w, "LCK_"):
		return "lock"
	case strings.Contains(w, "LATCH"):
		return "latch"
	case strings.HasPrefix(w, "PREEMPTIVE_"):
		return "preemptive"
	}
	return "other"
}

// Parse reads a system_health CSV. A scrub-list column or a missing
// timestamp_utc refuses the file before any row is read.
func (x xeImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := effectiveLimits(opts.Limits, x.DefaultLimits())
	s := newSink(ctx, XEName, opts, lim, emit)
	s.rep.Quality = evidence.SourceQuality{Threshold: XEThreshold, TimezoneRule: clock.RuleUTCField}
	s.rep.FieldRules = map[string]string{"timestamp_utc": clock.RuleUTCField}
	if err := ctx.Err(); err != nil {
		return s.rep, err
	}
	br, _, err := sniffed(importer.LimitReader(r, lim.MaxBytes))
	if err != nil {
		return s.rep, err
	}
	csvr, _, err := textio.NewCSV(&lineCap{r: br, max: lim.MaxLineBytes}, textio.CSVOptions{MaxFields: lim.MaxFields})
	if err != nil {
		if errors.Is(err, importer.ErrLimit) || errors.Is(err, importer.ErrQuarantine) {
			return s.rep, err
		}
		return s.rep, &importer.QuarantineError{Reason: importer.ReasonEncoding}
	}
	header := csvr.Header()
	for _, h := range header {
		if unscrubbed(h) {
			return s.rep, &importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: h}
		}
	}
	col := map[string]int{}
	for i, h := range header {
		role, ok := xeRoles[normHeader(h)]
		if !ok {
			continue
		}
		if _, dup := col[role]; dup {
			return s.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: h}
		}
		col[role] = i
	}
	if _, ok := col["ts"]; !ok {
		return s.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "timestamp_utc"}
	}
	if _, ok := col["event"]; !ok {
		return s.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "name"}
	}
	_, hostCol := col["host"]
	picked := ""
	if opts.Host != nil {
		picked = opts.Host.Label
	}
	if !hostCol && picked == "" {
		return s.rep, importer.ErrUnknownHost
	}
	observed := map[string]string{} // raw name -> label, within this parse only
	cell := func(rec []string, role string) string {
		i, ok := col[role]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var other, waits bool
	badRun := 0
	for {
		rec, line, err := csvr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, importer.ErrLimit) {
				return s.rep, err
			}
			if rerr := s.row(); rerr != nil {
				return s.rep, rerr
			}
			if errors.Is(err, textio.ErrEncoding) {
				s.reject(line, importer.ReasonEncoding)
			} else {
				s.reject(line, importer.ReasonFieldCount)
			}
			if badRun++; badRun > 1000 {
				return s.rep, &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
			}
			continue
		}
		badRun = 0
		if err := s.row(); err != nil {
			return s.rep, err
		}
		ts, ok := parseUTCField(cell(rec, "ts"))
		if !ok {
			s.reject(line, importer.ReasonBadTime)
			continue
		}
		host := picked
		if hostCol {
			raw := cell(rec, "host")
			label, known := observed[raw]
			if !known {
				if len(s.rep.ObservedHosts) < maxReportedErrors {
					s.rep.ObservedHosts = append(s.rep.ObservedHosts, raw)
				}
				if opts.Hosts == nil {
					return s.rep, importer.ErrUnknownHost
				}
				hint, found := opts.Hosts(raw)
				if !found || hint.Label == "" || (picked != "" && hint.Label != picked) {
					return s.rep, importer.ErrUnknownHost
				}
				label = hint.Label
				observed[raw] = label
			}
			host = label
		}
		name := strings.ToLower(cell(rec, "event"))
		kind, known := xeKinds[name]
		if !known {
			s.seen(ts)
			other = true
			continue
		}
		e := evidence.Event{Kind: kind, Host: host, Start: ts, End: ts, TZRule: clock.RuleUTCField, Attrs: map[string]string{"event": name}}
		switch kind {
		case KindWait:
			e.Severity = "warning"
			if wt := cell(rec, "wait_type"); isToken(wt, 60) {
				e.Attrs["wait_type"] = strings.ToUpper(wt)
				e.Attrs["wait_class"] = waitClass(wt)
			} else {
				e.Attrs["wait_class"] = "other"
			}
			if d := cell(rec, "duration"); d != "" {
				ms, err := strconv.ParseFloat(d, 64)
				if err != nil || ms < 0 || math.IsNaN(ms) || math.IsInf(ms, 0) || ms > 7*24*3600*1000 {
					s.reject(line, importer.ReasonBadNumber)
					continue
				}
				e.DurationMs, e.Unit = ptr(ms), "ms"
				if xeWaitTimestampIsEnd {
					e.Start = ts.Add(-time.Duration(ms * float64(time.Millisecond)))
				}
			}
			waits = true
		case KindDiagnostics:
			if c := cell(rec, "component"); isToken(c, 40) {
				e.Attrs["component"] = strings.ToLower(c)
			}
			st := strings.ToLower(cell(rec, "state"))
			switch st {
			case "clean":
				e.Severity = "info"
			case "warning":
				e.Severity = "warning"
			case "error":
				e.Severity = "error"
			default:
				st = "unknown"
			}
			e.Attrs["state"] = st
		case KindError:
			if v := cell(rec, "error"); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					s.reject(line, importer.ReasonBadNumber)
					continue
				}
				e.Attrs["error_number"] = strconv.Itoa(n)
			}
			if v := cell(rec, "severity"); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || n > 25 {
					s.reject(line, importer.ReasonBadNumber)
					continue
				}
				e.Attrs["sql_severity"] = strconv.Itoa(n)
				e.Severity = sqlSeverity(n)
			}
		case KindNonYielding:
			e.Severity = "error"
		case KindDeadlock:
			e.Severity = "warning"
		}
		s.seen(e.Start)
		s.seen(ts)
		if err := s.put(e, line, 0); err != nil {
			return s.rep, err
		}
	}
	s.rep.KnownGaps = append(s.rep.KnownGaps, gapXERetention)
	if waits {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapXEWaitStart)
	}
	if other {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapXEOther)
	}
	return s.finish()
}

// Statement is one §6.6 sentence about an audit interval and a SQL source.
// Label is Symptom or Correlation only; Correlation only for a unique join.
type Statement struct {
	Text       string
	Label      string
	Class      evidence.JoinClass
	Candidates []string
}

func qualifiesWait(e evidence.Event) bool { return e.Source == XEName && e.Kind == KindWait }

func waitPhrase(class string) string {
	switch class {
	case "lock", "latch", "preemptive":
		return class + " wait"
	}
	return "wait"
}

// XEStatement words what system_health shows for one stalled audit interval.
// A unique overlapping wait gives OverlapUnique (Correlation); several give
// OverlapAmbiguous and are never counted. With no overlapping wait it gives
// the threshold wording (R8-B2), or the gap wording when the export has
// coverage that does not include the interval. It never says that SQL was
// not waiting, nor that the API waited for SQL.
func XEStatement(iv join.Interval, events []evidence.Event, q evidence.SourceQuality, tol join.Tolerance) Statement {
	m := join.Overlap([]join.Interval{iv}, events, tol, qualifiesWait)[0]
	st := Statement{Class: m.Class, Candidates: m.Candidates, Label: audit.EvidenceSymptom}
	switch m.Class {
	case evidence.JoinUnique:
		for _, e := range events {
			if e.EvidenceID != m.Candidates[0] {
				continue
			}
			secs := e.End.Sub(e.Start).Seconds()
			if e.DurationMs != nil {
				secs = *e.DurationMs / 1000
			}
			st.Text = evidence.OverlapUnique(XESource, waitPhrase(e.Attrs["wait_class"]), secs)
			st.Label = audit.EvidenceCorrelation
		}
	case evidence.JoinAmbiguous:
		st.Text = evidence.OverlapAmbiguous(len(m.Candidates))
	default:
		if q.CoverageFirst != nil && q.CoverageLast != nil &&
			(iv.End.Before(q.CoverageFirst.Add(-tol.Total())) || iv.Start.After(q.CoverageLast.Add(tol.Total()))) {
			st.Text = evidence.NotObservedGap("the " + XESource + " export does not cover this interval")
		} else {
			st.Text = evidence.NotObservedThreshold(XESource, XEAbsenceThreshold)
		}
	}
	return st
}
