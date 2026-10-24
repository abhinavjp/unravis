// Package schedule imports Hangfire recurring-job and SQL Agent schedule exports
// (plan card P5). It emits schedule-boundary events (the instants a schedule
// says a job should start), schedule-unknown markers (schedules that could not
// be expanded) and job-run events (observed runs from history).
//
// Everything here is Symptom-level evidence about when work was scheduled. A
// boundary near a stall is a coincidence of clock times, never a cause (B3).
// No network, no file paths from content, and no cell is ever echoed into an
// error, report or gap: gaps are fixed sentences.
package schedule

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// Importer names (stable) and event kinds.
const (
	NameHangfire = "schedule-hangfire"
	NameAgent    = "schedule-agent"

	KindBoundary = "schedule-boundary"
	KindUnknown  = "schedule-unknown"
	KindJobRun   = "job-run"
)

// Caps from plan section 4: a file over the byte, row or line cap is refused
// whole. The boundary caps are different: a chatty schedule is cut and the
// rest of its window is marked unknown, so one job cannot cost the whole file.
const (
	maxBytes     = 16 << 20
	maxRows      = 100_000
	maxLineBytes = 64 << 10
	// maxBoundaries caps schedule-boundary events per file. Unknown markers are
	// not counted: they are bounded by the row cap, at most two per row.
	maxBoundaries = 100_000
	// maxPerSchedule caps the boundaries one schedule may emit, so a single
	// one-second job leaves room for the other jobs in the file.
	maxPerSchedule = maxBoundaries / 4
	maxAttrBytes   = 200
)

// Fixed attribute values for the "reason" attribute of a schedule-unknown event.
const (
	reasonCron     = "cron-unsupported"
	reasonTZ       = "tz-unknown"
	reasonFreq     = "freq-not-time-based"
	reasonDisabled = "disabled-at-export"
	reasonMonthDay = "month-day-missing"
	reasonWrap     = "window-wraps"
	reasonTooMany  = "too-many-boundaries"
)

// Fixed gap sentences. Row content is never interpolated.
const (
	gapNoWindow        = "No time window was selected; schedule boundaries were not expanded."
	gapUnknown         = "Some schedules could not be fully expanded (unsupported cron syntax, unknown time zone, not time-based, disabled, a daily window across midnight, a missing month day or too many boundaries); the affected times count as unknown, never as a miss."
	gapTruncated       = "A schedule had more boundaries than the limit allows; it was cut and the rest of the window counts as unknown."
	gapAgentStepRows   = "SQL Agent history rows for job steps (step_id above 0) are skipped; only job outcome rows become job-run events."
	gapAgentNoStep     = "The SQL Agent history has no step_id column, so a step row may be counted as a job run."
	gapAgentJobEnabled = "Whether the job itself is enabled is only known when the export has both a job and a schedule enabled column; a disabled job can appear as live."
	gapDST             = "Boundaries inside a daylight-saving gap or fold are flagged; their run time is uncertain."
	gapCronBoth        = "Cron day-of-month and day-of-week are both restricted: a match on either is treated as a boundary."
	gapHangfireSnap    = "Boundaries are expanded from the cron expressions in the export; schedule edits made before the export are not visible."
	gapHangfireLast    = "Hangfire LastExecution is the most recent enqueue only; earlier runs are not in the export."
	gapAgentSnap       = "Boundaries are expanded from the SQL Agent schedule definitions at export time; edits made before the export are not visible."
	gapAgentHostLocal  = "SQL Agent times are server-local and were converted with the host time zone chosen for this file."
	gapAgentHistory    = "SQL Agent history keeps a limited number of rows per job; missing history is not evidence that a job did not run."
	gapWeeklyFactor    = "A weekly recurrence factor above 1 assumes weeks start on Sunday."
	gapMonthDayMissing = "A monthly day beyond the month length produces no boundary for that month."
)

// attrs is the per-importer attribute allowlist (<= 16 keys).
var attrs = map[string]bool{
	"job": true, "queue": true, "tz": true, "basis": true,
	"reason": true, "dst": true, "freq": true,
}

// errEmpty is returned for an empty file, a lone byte or an unreadable header.
var errEmpty = &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}

func defaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: maxBytes, MaxRows: maxRows, MaxLineBytes: maxLineBytes,
		MaxFields: 4096, MaxRejectFraction: 0.05,
	}
}

func fillLimits(l importer.Limits) importer.Limits {
	d := defaultLimits()
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxRows <= 0 {
		l.MaxRows = d.MaxRows
	}
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = d.MaxLineBytes
	}
	if l.MaxFields <= 0 {
		l.MaxFields = d.MaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = d.MaxRejectFraction
	}
	return l
}

// All returns both schedule importers for the registry.
func All() []importer.Importer { return []importer.Importer{NewHangfire(), NewAgent()} }

// ---------------------------------------------------------------- run state

// run is the per-Parse state shared by both importers.
type run struct {
	ctx    context.Context
	name   string
	opts   importer.Options
	lim    importer.Limits
	emit   func(evidence.Event) error
	rep    importer.Report
	gaps   map[string]bool
	win    bool // a usable window was supplied
	from   time.Time
	to     time.Time
	nBound int       // boundaries emitted in the file
	jobN   int       // boundaries emitted for the schedule being expanded
	lastB  time.Time // latest boundary emitted for the schedule being expanded
	minT   time.Time
	maxT   time.Time
	host   string
}

func newRun(ctx context.Context, name string, opts importer.Options, emit func(evidence.Event) error) *run {
	r := &run{ctx: ctx, name: name, opts: opts, lim: fillLimits(opts.Limits), emit: emit, gaps: map[string]bool{}}
	r.rep.Importer = name
	r.rep.FieldRules = map[string]string{}
	if opts.Host != nil {
		r.host = opts.Host.Label
	}
	w := opts.Window
	if !w.From.IsZero() && !w.To.IsZero() && !w.To.Before(w.From) {
		r.win = true
		r.from, r.to = w.From.UTC(), w.To.UTC()
	} else {
		r.gaps[gapNoWindow] = true
	}
	return r
}

func (r *run) gap(s string) { r.gaps[s] = true }

func (r *run) reject(line int64, reason importer.Reason) {
	r.rep.Rejected++
	if len(r.rep.Errors) < 100 {
		r.rep.Errors = append(r.rep.Errors, importer.RowError{Line: line, Reason: reason})
	}
}

func (r *run) inWindow(t time.Time) bool {
	if !r.win {
		return true
	}
	return !t.Before(r.from) && !t.After(r.to)
}

// send validates and emits one event.
func (r *run) send(line int64, ordinal int, kind string, start, end time.Time, dur *float64, severity, rule string, a map[string]string) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	ev := evidence.Event{
		EvidenceID: evidence.EventID(r.opts.CaseID, r.name, r.opts.ImportNonce, line, ordinal),
		Source:     r.name,
		Kind:       kind,
		Host:       r.host,
		Start:      start.UTC(),
		End:        end.UTC(),
		DurationMs: dur,
		Severity:   severity,
		Line:       line,
		TZRule:     rule,
		Attrs:      a,
	}
	if err := ev.Validate(attrs); err != nil {
		return fmt.Errorf("schedule: invalid event: %w", err)
	}
	if err := r.emit(ev); err != nil {
		return err
	}
	r.rep.Emitted++
	if r.minT.IsZero() || ev.Start.Before(r.minT) {
		r.minT = ev.Start
	}
	if ev.End.After(r.maxT) {
		r.maxT = ev.End
	}
	return nil
}

// errBoundaryCap says a schedule reached the per-schedule or per-file boundary
// cap. It never leaves the package: the caller cuts the schedule and marks the
// rest of the window unknown.
var errBoundaryCap = errors.New("schedule: boundary cap reached")

// beginSchedule starts the per-schedule boundary count.
func (r *run) beginSchedule() {
	r.jobN = 0
	r.lastB = time.Time{}
}

// countBoundary reserves room for one more boundary, or reports the cap.
func (r *run) countBoundary() error {
	if r.nBound >= maxBoundaries || r.jobN >= maxPerSchedule {
		return errBoundaryCap
	}
	r.nBound++
	r.jobN++
	return nil
}

// sendUnknown emits one schedule-unknown marker for a job that covers the whole
// window: nothing is known about when the job fires.
func (r *run) sendUnknown(line int64, ordinal int, job, reason, rule string) error {
	return r.sendUnknownSpan(line, ordinal, job, reason, rule, r.from, r.to)
}

// sendUnknownSpan emits a schedule-unknown marker for the times between start
// and end (inclusive). B3 treats a stall start inside the span, or within its
// tolerance of it, as unknown; a stall elsewhere is judged on the boundaries.
func (r *run) sendUnknownSpan(line int64, ordinal int, job, reason, rule string, start, end time.Time) error {
	if !r.win {
		return nil
	}
	r.gap(gapUnknown)
	return r.send(line, ordinal, KindUnknown, start, end, nil, "", rule,
		map[string]string{"job": job, "reason": reason})
}

// cutSchedule turns a boundary-cap hit into a partial result: the boundaries
// already emitted stay, and the window after the last one is unknown.
func (r *run) cutSchedule(err error, line int64, ord *int, job, rule string) error {
	if !errors.Is(err, errBoundaryCap) {
		return err
	}
	start := r.from
	if r.lastB.After(start) {
		start = r.lastB
	}
	r.gap(gapTruncated)
	e := r.sendUnknownSpan(line, *ord, job, reasonTooMany, rule, start, r.to)
	*ord++
	return e
}

// finish builds the report; quarantine applies the reject-fraction rule.
func (r *run) finish(rule string, base []string) (importer.Report, error) {
	rep := r.rep
	rep.First, rep.Last = r.minT, r.maxT
	rep.Quality = evidence.SourceQuality{
		Sampling:     "export snapshot",
		TimezoneRule: rule,
	}
	if r.win {
		f, l := r.from, r.to
		rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
	}
	gaps := append([]string(nil), base...)
	// Deterministic order for the optional sentences.
	for _, g := range []string{gapNoWindow, gapUnknown, gapTruncated, gapDST, gapCronBoth, gapWeeklyFactor, gapMonthDayMissing} {
		if r.gaps[g] {
			gaps = append(gaps, g)
		}
	}
	rep.KnownGaps = gaps
	if rep.Rows > 0 && float64(rep.Rejected)/float64(rep.Rows) > r.lim.MaxRejectFraction {
		return rep, &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
	}
	return rep, nil
}

// ------------------------------------------------------------------- header

// table maps lowercase header names to column indexes.
type table struct {
	idx   map[string]int
	multi map[string][]int // every position of a header that may repeat
	n     int
}

// newTable indexes the header. A repeated name quarantines the file, except for
// the names in dupOK (a join of two tables can carry two columns of one name).
func newTable(header []string, dupOK ...string) (*table, error) {
	t := &table{idx: map[string]int{}, multi: map[string][]int{}, n: len(header)}
	for i, h := range header {
		k := strings.ToLower(strings.TrimSpace(h))
		if k == "" {
			continue
		}
		if _, dup := t.idx[k]; dup {
			allowed := false
			for _, d := range dupOK {
				allowed = allowed || d == k
			}
			if !allowed {
				return nil, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
			}
			t.multi[k] = append(t.multi[k], i)
			continue
		}
		t.idx[k] = i
		t.multi[k] = []int{i}
	}
	return t, nil
}

func (t *table) has(name string) bool { _, ok := t.idx[name]; return ok }

// at returns the trimmed cell at a column position, or "" when out of range.
func (t *table) at(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func (t *table) get(rec []string, name string) string {
	i, ok := t.idx[name]
	if !ok {
		return ""
	}
	return t.at(rec, i)
}

// getRaw returns a cell exactly as the file holds it, for the free-text id
// that becomes an attribute. A blank cell reads as "".
func (t *table) getRaw(rec []string, name string) string {
	i, ok := t.idx[name]
	if !ok || i >= len(rec) || strings.TrimSpace(rec[i]) == "" {
		return ""
	}
	return rec[i]
}

// refuseColumns quarantines the file on an unscrubbed column. Only our own
// canonical name is reported, never the header text.
func (t *table) refuseColumns(list []string) error {
	for _, c := range list {
		if t.has(c) {
			return &importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: c}
		}
	}
	return nil
}

// require quarantines when a required column is missing (our name reported).
func (t *table) require(names ...string) error {
	for _, c := range names {
		if !t.has(c) {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: c}
		}
	}
	return nil
}

// openCSV runs the archive sniff, the byte limit and the strict CSV reader.
func openCSV(r io.Reader, lim importer.Limits, dupOK ...string) (*textio.CSVReader, *table, error) {
	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 4096)
	head, _ := br.Peek(512)
	if err := importer.Sniff(head); err != nil {
		return nil, nil, err
	}
	cr, _, err := textio.NewCSV(br, textio.CSVOptions{Comma: ',', MaxFields: lim.MaxFields, MaxLine: lim.MaxLineBytes})
	if err != nil {
		var le *importer.LimitError
		if errors.As(err, &le) || errors.Is(err, importer.ErrQuarantine) {
			return nil, nil, err
		}
		// Empty file, a lone byte or a header the CSV reader cannot parse.
		return nil, nil, errEmpty
	}
	tb, err := newTable(cr.Header(), dupOK...)
	if err != nil {
		return nil, nil, err
	}
	return cr, tb, nil
}

// nextRow reads one record. It returns (nil, line, reason, nil) for a row to
// reject, and a non-nil error when the whole file must be refused.
func nextRow(cr *textio.CSVReader, wantCols int) (rec []string, line int64, reason importer.Reason, err error) {
	rec, line, err = cr.Next()
	if err == nil {
		if len(rec) != wantCols {
			return nil, line, importer.ReasonFieldCount, nil
		}
		return rec, line, "", nil
	}
	if errors.Is(err, io.EOF) {
		return nil, line, "", io.EOF
	}
	var le *importer.LimitError
	if errors.As(err, &le) {
		return nil, line, "", err
	}
	if errors.Is(err, importer.ErrQuarantine) {
		return nil, line, "", err
	}
	// A malformed record: reject it and carry on with the next line. A reader
	// that cannot make progress surfaces a non-parse error, which refuses the file.
	// F0's reader reports a bad row as an importer.RowError value.
	var re importer.RowError
	if errors.As(err, &re) {
		return nil, line, re.Reason, nil
	}
	var pe *csv.ParseError
	if errors.As(err, &pe) || errors.Is(err, csv.ErrFieldCount) {
		return nil, line, importer.ReasonFieldCount, nil
	}
	return nil, line, "", err
}

// ------------------------------------------------------------- value parsing

var tzNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-]*(/[A-Za-z0-9_+\-]+){0,2}$`)

// resolveZone maps a Hangfire TimeZoneId to a location. Windows IDs go through
// clock.WindowsZone; IANA names are loaded only when they look like a zone
// name, so no file content is ever used as a path. Unmapped gives nil.
func resolveZone(id string, cache map[string]*time.Location) *time.Location {
	if loc, ok := cache[id]; ok {
		return loc
	}
	loc := lookupZone(id)
	if len(cache) < 1024 {
		cache[id] = loc
	}
	return loc
}

func lookupZone(id string) *time.Location {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 64 || strings.EqualFold(id, "local") {
		return nil
	}
	if loc, ok := clock.WindowsZone(id); ok && loc != nil {
		return loc
	}
	if !tzNameRE.MatchString(id) {
		return nil
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil
	}
	return loc
}

// civilWall is a wall-clock instant without a zone.
type civilWall struct {
	y          int
	m          time.Month
	d          int
	hh, mm, ss int
}

func (w civilWall) naiveUTC() time.Time {
	return time.Date(w.y, w.m, w.d, w.hh, w.mm, w.ss, 0, time.UTC)
}

const wallLayout = "2006-01-02 15:04:05"

// resolveWall converts a local wall time to UTC and reports a DST flag
// ("", "ambiguous" or "nonexistent"). A fold is never guessed: the flag says
// the time is uncertain. In a gap the clock reading is advanced as the Go
// time package does, and the flag marks it.
func resolveWall(w civilWall, loc *time.Location) (time.Time, string) {
	s := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", w.y, int(w.m), w.d, w.hh, w.mm, w.ss)
	res, err := clock.ParseLocal(wallLayout, s, loc)
	if err != nil || res.UTC.IsZero() {
		return time.Date(w.y, w.m, w.d, w.hh, w.mm, w.ss, 0, loc).UTC(), ""
	}
	switch {
	case res.Nonexistent:
		return res.UTC.UTC(), "nonexistent"
	case res.Ambiguous:
		return res.UTC.UTC(), "ambiguous"
	}
	return res.UTC.UTC(), ""
}

// wallReading is one UTC instant a wall-clock time can mean, with its DST flag.
type wallReading struct {
	utc  time.Time
	flag string // "", "ambiguous" or "nonexistent"
}

// resolveWallAll lists every UTC instant a local wall time can mean. A normal
// time has one. A time in a fold happens twice, so both occurrences are listed
// and flagged ambiguous. A time in a gap never happens; it is read with the
// offset before and the offset after the change, and both readings are flagged
// nonexistent. Nothing is guessed: the caller emits every reading.
func resolveWallAll(w civilWall, loc *time.Location) []wallReading {
	naive := w.naiveUTC()
	_, offBefore := naive.Add(-24 * time.Hour).In(loc).Zone()
	_, offAfter := naive.Add(24 * time.Hour).In(loc).Zone()
	offs := []int{offBefore}
	if offAfter != offBefore {
		offs = append(offs, offAfter)
	}
	var hits []time.Time
	for _, o := range offs {
		c := naive.Add(-time.Duration(o) * time.Second)
		l := c.In(loc)
		if l.Year() == w.y && l.Month() == w.m && l.Day() == w.d &&
			l.Hour() == w.hh && l.Minute() == w.mm && l.Second() == w.ss {
			hits = append(hits, c)
		}
	}
	flag := ""
	switch len(hits) {
	case 0:
		flag = "nonexistent"
		for _, o := range offs {
			hits = append(hits, naive.Add(-time.Duration(o)*time.Second))
		}
	case 1:
	default:
		flag = "ambiguous"
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Before(hits[j]) })
	out := make([]wallReading, 0, len(hits))
	for _, h := range hits {
		out = append(out, wallReading{utc: h.UTC(), flag: flag})
	}
	return out
}

// parseInstant parses an ISO 8601 instant with Z or an offset, or epoch seconds
// or milliseconds. A zone-less ISO value is tz-unlabelled; anything else
// unreadable is bad-time. It returns the clock rule that produced the value.
func parseInstant(s string) (time.Time, string, importer.Reason) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, "", importer.ReasonBadTime
	}
	if allDigits(s) {
		if len(s) > 15 {
			return time.Time{}, "", importer.ReasonBadNumber
		}
		var v int64
		for _, c := range s {
			v = v*10 + int64(c-'0')
		}
		var t time.Time
		switch {
		case v < 100_000_000_000:
			t = time.Unix(v, 0).UTC()
		case v < 100_000_000_000_000:
			t = time.UnixMilli(v).UTC()
		default:
			return time.Time{}, "", importer.ReasonBadNumber
		}
		if !sane(t) {
			return time.Time{}, "", importer.ReasonBadTime
		}
		return t, clock.RuleUTCField, ""
	}
	norm := s
	if len(norm) > 10 && norm[10] == ' ' {
		norm = norm[:10] + "T" + norm[11:]
	}
	if t, err := time.Parse(time.RFC3339Nano, norm); err == nil {
		rule := clock.RuleOffsetInField
		if strings.HasSuffix(norm, "Z") || strings.HasSuffix(norm, "+00:00") {
			rule = clock.RuleUTCField
		}
		if !sane(t) {
			return time.Time{}, "", importer.ReasonBadTime
		}
		return t.UTC(), rule, ""
	}
	for _, l := range []string{"2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if _, err := time.Parse(l, norm); err == nil {
			return time.Time{}, "", importer.ReasonTZUnlabelled
		}
	}
	return time.Time{}, "", importer.ReasonBadTime
}

func sane(t time.Time) bool { y := t.UTC().Year(); return y >= 1990 && y <= 2100 }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// cleanAttr bounds an attribute value to maxAttrBytes on a rune boundary.
// Content is otherwise kept verbatim: cells starting with = + - @ are data.
func cleanAttr(s string) string {
	if len(s) <= maxAttrBytes {
		return s
	}
	cut := maxAttrBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

var queueRE = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// decodeHead returns the first line of a Detect head, lowercased, handling the
// UTF-8 and UTF-16 byte order marks.
func decodeHead(head []byte) string {
	switch {
	case len(head) >= 3 && head[0] == 0xef && head[1] == 0xbb && head[2] == 0xbf:
		head = head[3:]
	case len(head) >= 2 && head[0] == 0xff && head[1] == 0xfe:
		u := make([]uint16, 0, len(head)/2)
		for i := 2; i+1 < len(head); i += 2 {
			u = append(u, uint16(head[i])|uint16(head[i+1])<<8)
		}
		head = []byte(string(utf16.Decode(u)))
	case len(head) >= 2 && head[0] == 0xfe && head[1] == 0xff:
		u := make([]uint16, 0, len(head)/2)
		for i := 2; i+1 < len(head); i += 2 {
			u = append(u, uint16(head[i+1])|uint16(head[i])<<8)
		}
		head = []byte(string(utf16.Decode(u)))
	}
	if i := strings.IndexByte(string(head), '\n'); i >= 0 {
		head = head[:i]
	}
	return strings.ToLower(strings.TrimSpace(string(head)))
}

func headerCells(line string) map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Split(line, ",") {
		m[strings.Trim(strings.TrimSpace(c), `"`)] = true
	}
	return m
}
