package sqlserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// The Query Store importer ("sql-querystore") reads a CSV or JSON export of
// sys.query_store_runtime_stats joined to sys.query_store_runtime_stats_interval
// (and optionally query_store_plan, query_store_query_text and
// query_store_wait_stats). The file comes from SSMS "Save Results As", from
// sqlcmd -s, -W, or from a query ending in FOR JSON (the export steps are in
// docs/superpowers/plans/2026-10-25-log-support-gap-check.md).
//
// Field doc, per column (matched by name, case-insensitive; every other column
// is dropped unread):
//
//	start_time, end_time   the interval, runtime_stats_interval.start_time and
//	                       end_time. They are datetimeoffset values that Query
//	                       Store always stores in UTC (+00:00); the importer reads
//	                       them as UTC and refuses a cell with another offset.
//	                       One of the two is enough; a file with neither is refused.
//	query_id, plan_id      numbers; query_id is required.
//	count_executions       executions in the interval.
//	avg_duration           MICROSECONDS in Query Store; emitted as the event
//	                       duration in milliseconds (value / 1000).
//	max_duration           MICROSECONDS; attribute max_duration_ms (value / 1000).
//	avg_cpu_time           MICROSECONDS; attribute avg_cpu_ms (value / 1000).
//	avg_logical_io_reads   8 KB pages, a number; attribute avg_logical_reads.
//	avg_rowcount           rows, a number; attribute avg_rows.
//	last_execution_time    datetimeoffset, UTC; attribute last_execution.
//	execution_type[_desc]  Regular, Aborted or Exception (or 0, 3, 4).
//	is_forced_plan         0/1 or true/false; attribute forced.
//	wait_category_desc,    from query_store_wait_stats. avg_query_wait_time is in
//	avg_query_wait_time    MILLISECONDS there (unlike the runtime columns) and
//	                       becomes the duration of a separate "wait" event.
//	query_sql_text,        never read: neither the text nor the plan is stored,
//	query_text, query_plan truncated or hashed, so no literal or parameter value
//	                       can reach an event, a report or an error. This is
//	                       stricter than the xe importer, which refuses a file
//	                       with such a column; a Query Store export routinely
//	                       carries the text, so it is skipped instead.
//
// Each row is one (plan, interval, execution type) aggregate, so it becomes one
// "query-interval" event spanning the interval; a row that names a wait
// category also gives one "wait" event of the same kind the xe importer emits.
// Rows repeated by the join (the same runtime row once per wait category) are
// emitted once.
const (
	QSName            = "sql-querystore"
	KindQueryInterval = "query-interval"
)

const (
	gapQSAverages = "each Query Store row is an average over its interval; it does not say when inside the interval the time was spent"
	gapQSCapture  = "Query Store capture mode and retention are not in the export; queries it did not capture, or has purged, are absent"
	gapQSText     = "query text and plan columns in the export were not read"
	gapQSDup      = "rows repeated by the join were counted once"
	gapQSCols     = "the export is a flat file: only the columns named in the importer help were read"
)

// Allowed attributes. Every value is a number, a fixed word or a canonical
// Query Store wait category; nothing is free text.
var qsAttrs = map[string]bool{
	"query_id": true, "plan_id": true, "executions": true, "max_duration_ms": true, "avg_cpu_ms": true,
	"avg_logical_reads": true, "avg_rows": true, "forced": true, "execution_type": true, "last_execution": true,
	"wait_class": true, "wait_category": true,
}

// qsRoles maps a normalised header to the role it fills. Role "drop" is a
// column that is known and never read (text and plan columns and the plain ids
// that sit beside them in the catalog views).
var qsRoles = map[string]string{
	"start_time": "start", "interval_start_time": "start", "interval_start": "start",
	"runtime_stats_interval_start_time": "start",
	"end_time":                          "end", "interval_end_time": "end", "interval_end": "end",
	"runtime_stats_interval_end_time": "end",
	"query_id":                        "query_id", "plan_id": "plan_id",
	"count_executions": "executions",
	"avg_duration":     "avg_duration", "max_duration": "max_duration", "avg_cpu_time": "avg_cpu",
	"avg_logical_io_reads": "avg_reads", "avg_rowcount": "avg_rows",
	"last_execution_time": "last_exec",
	"execution_type":      "exec_type", "execution_type_desc": "exec_type",
	"is_forced_plan":      "forced",
	"wait_category_desc":  "wait_cat",
	"avg_query_wait_time": "wait_ms",
	"query_sql_text":      "drop", "query_text": "drop", "query_plan": "drop", "query_text_id": "drop", "context_settings_id": "drop",
}

// qsRoleNames maps a role back to the canonical column name reported when a
// file is refused for it (never a cell, never the user's header).
var qsRoleNames = map[string]string{"start": "start_time", "end": "end_time", "query_id": "query_id"}

// qsDetectExtra are the columns that mark a file as a Query Store export when
// it also has query_id.
var qsDetectExtra = map[string]bool{
	"plan_id": true, "executions": true, "avg_duration": true, "max_duration": true, "avg_cpu": true,
	"avg_reads": true, "avg_rows": true, "wait_cat": true, "forced": true, "start": true, "end": true,
}

// qsWaitCategories are the wait_category_desc values of query_store_wait_stats.
var qsWaitCategories = map[string]string{}

func init() {
	for _, c := range []string{"Unknown", "CPU", "Worker Thread", "Lock", "Latch", "Buffer Latch", "Buffer IO", "Compilation",
		"SQL CLR", "Mirroring", "Transaction", "Idle", "Preemptive", "Service Broker", "Tran Log IO", "Network IO",
		"Parallelism", "Memory", "User Wait", "Tracing", "Full Text Search", "Other Disk IO", "Replication",
		"Log Rate Governor"} {
		qsWaitCategories[strings.ToLower(c)] = c
	}
}

func qsWaitClass(category string) string {
	switch category {
	case "Lock":
		return "lock"
	case "Latch", "Buffer Latch":
		return "latch"
	case "Preemptive":
		return "preemptive"
	}
	return "other"
}

var (
	qsNumber = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	// qsWord is a wait category we do not know: letters, digits and single spaces.
	qsWord = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?: [A-Za-z0-9]+){0,3}$`)
)

const (
	qsMaxMicros = 7 * 24 * 3600 * 1e6 // one week, in microseconds
	qsMaxMillis = 7 * 24 * 3600 * 1e3
	qsMaxCount  = 1e12
	qsMaxID     = 1e15 - 1 // the largest 15-digit id the case export keeps
)

var (
	qsMinTime = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	qsMaxTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// qsTimeLayouts read datetime2 and datetimeoffset as SSMS, sqlcmd and FOR JSON
// write them. Go accepts fractional seconds after the seconds field in all of them.
var qsTimeLayouts = []string{
	"2006-01-02 15:04:05", "2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05 Z07:00",
}

// qsParseTime reads an interval time as UTC. A cell with an offset other than
// zero is not trusted, as in the xe importer, and a time far outside any real
// Query Store is refused.
func qsParseTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, l := range qsTimeLayouts {
		t, err := time.Parse(l, v)
		if err != nil {
			continue
		}
		if _, off := t.Zone(); off != 0 {
			return time.Time{}, false
		}
		t = t.UTC()
		if t.Before(qsMinTime) || !t.Before(qsMaxTime) {
			return time.Time{}, false
		}
		return t, true
	}
	return time.Time{}, false
}

// qsNum reads an optional non-negative number. ok is false for a cell that is
// present but not a finite number in [0, max]. An empty cell and the NULL that
// sqlcmd writes are absent (present false).
func qsNum(v string, max float64) (f float64, present, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "null") {
		return 0, false, true
	}
	if !qsNumber.MatchString(v) {
		return 0, true, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > max {
		return 0, true, false
	}
	if f == 0 {
		f = 0 // "-0" is zero, not negative zero
	}
	return f, true, true
}

func qsInt(v string, max float64) (n float64, present, ok bool) {
	f, present, ok := qsNum(v, max)
	if !ok || !present {
		return 0, present, ok
	}
	if f != math.Trunc(f) {
		return 0, true, false
	}
	return f, true, true
}

// qsFmt writes a number with at most three decimals, inside the case-export
// number shape.
func qsFmt(f float64) string {
	return strconv.FormatFloat(math.Round(f*1000)/1000, 'f', -1, 64)
}

func qsEmpty(v string) bool {
	v = strings.TrimSpace(v)
	return v == "" || strings.EqualFold(v, "null")
}

type qsImporter struct{}

// NewQuerystore returns the Query Store export importer ("sql-querystore").
func NewQuerystore() importer.Importer { return qsImporter{} }

func (qsImporter) Name() string { return QSName }

func (qsImporter) AllowedAttrs() map[string]bool {
	out := make(map[string]bool, len(qsAttrs))
	for k := range qsAttrs {
		out[k] = true
	}
	return out
}

func (qsImporter) DefaultLimits() importer.Limits {
	// A query text can be large: one cell may be a megabyte, so the line cap is
	// 2 MiB (the text is never kept). MaxRecord is 4 lines.
	return importer.Limits{MaxBytes: 512 << 20, MaxRows: 1_000_000, MaxLineBytes: 2 << 20, MaxFields: 4096, MaxRejectFraction: 0.05}
}

// Detect scores a CSV whose header, or a JSON export whose first object, has
// query_id and at least one other Query Store column. A file that names
// query_id but no interval time is still detected, so that Parse can refuse it
// with its reason.
func (qsImporter) Detect(head []byte, fileName string) int {
	text := headText(head)
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return 0
	}
	roles := map[string]bool{}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		for _, k := range jsonKeys(trimmed) {
			if r, ok := qsRoles[normHeader(k)]; ok {
				roles[r] = true
			}
		}
	} else {
		lines := firstLines(text, 1)
		if len(lines) == 0 {
			return 0
		}
		for _, h := range splitHeader(lines[0]) {
			if r, ok := qsRoles[normHeader(h)]; ok {
				roles[r] = true
			}
		}
	}
	if !roles["query_id"] {
		return 0
	}
	extra := 0
	for r := range roles {
		if qsDetectExtra[r] {
			extra++
		}
	}
	if extra == 0 {
		return 0
	}
	score := 55
	if roles["start"] || roles["end"] {
		score += 15
	}
	if roles["executions"] || roles["avg_duration"] {
		score += 15
	}
	if roles["plan_id"] {
		score += 5
	}
	name := strings.ToLower(fileName)
	if strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".json") {
		score += 5
	}
	if score > 95 {
		score = 95
	}
	return score
}

// jsonKeys returns the object keys found in a JSON head (the rows, or a ROOT
// wrapper and its rows), a best-effort scan that stops at the first error (the
// head may be cut).
func jsonKeys(s string) []string {
	dec := json.NewDecoder(strings.NewReader(s))
	var keys []string
	// stack holds, per open container, whether it is an object whose next
	// string token is a key.
	type frame struct{ object, wantKey bool }
	var stack []frame
	for len(keys) < 200 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				stack = append(stack, frame{object: d == '{', wantKey: d == '{'})
			default:
				stack = stack[:len(stack)-1]
				if n := len(stack); n > 0 && stack[n-1].object {
					stack[n-1].wantKey = true
				}
			}
			continue
		}
		n := len(stack)
		if n == 0 || !stack[n-1].object {
			continue
		}
		if stack[n-1].wantKey {
			if k, ok := tok.(string); ok && n <= 4 {
				keys = append(keys, k)
			}
			stack[n-1].wantKey = false
		} else {
			stack[n-1].wantKey = true
		}
	}
	return keys
}

// qsProc holds the per-import state shared by the CSV and JSON readers.
type qsProc struct {
	s      *sink
	host   string
	runKey map[qsKey]struct{}
	// first-row check for JSON, header check for CSV
	checked bool

	dup, hadText bool
}

type qsKey struct {
	q, p, s, e int64
	x, w       string
}

// row turns one set of role cells into events. It returns a reject reason for
// the row ("" for none) or an error that ends the import.
func (p *qsProc) row(cells map[string]string, line int64) (importer.Reason, error) {
	startCell, endCell := cells["start"], cells["end"]
	var start, end time.Time
	var okS, okE bool
	if !qsEmpty(startCell) {
		if start, okS = qsParseTime(startCell); !okS {
			return importer.ReasonBadTime, nil
		}
	}
	if !qsEmpty(endCell) {
		if end, okE = qsParseTime(endCell); !okE {
			return importer.ReasonBadTime, nil
		}
	}
	switch {
	case okS && !okE:
		end = start
	case okE && !okS:
		start = end
	case !okS && !okE:
		return importer.ReasonBadTime, nil
	}
	if end.Before(start) || end.Sub(start) > 7*24*time.Hour {
		return importer.ReasonBadTime, nil
	}

	qid, present, ok := qsInt(cells["query_id"], qsMaxID)
	if !ok || !present {
		return importer.ReasonBadNumber, nil
	}
	attrs := map[string]string{"query_id": strconv.FormatInt(int64(qid), 10)}
	var planID int64
	if pid, present, ok := qsInt(cells["plan_id"], qsMaxID); !ok {
		return importer.ReasonBadNumber, nil
	} else if present {
		planID = int64(pid)
		attrs["plan_id"] = strconv.FormatInt(planID, 10)
	}

	// Runtime numbers: Query Store keeps durations and CPU in microseconds.
	var durMs *float64
	hasRuntime := false
	if n, present, ok := qsInt(cells["executions"], qsMaxCount); !ok {
		return importer.ReasonBadNumber, nil
	} else if present {
		attrs["executions"] = strconv.FormatInt(int64(n), 10)
		hasRuntime = true
	}
	if v, present, ok := qsNum(cells["avg_duration"], qsMaxMicros); !ok {
		return importer.ReasonBadNumber, nil
	} else if present {
		d := math.Round(v/1000*1000) / 1000
		durMs = &d
		hasRuntime = true
	}
	for _, c := range []struct {
		role, attr string
		max        float64
		div        float64
	}{
		{"max_duration", "max_duration_ms", qsMaxMicros, 1000},
		{"avg_cpu", "avg_cpu_ms", qsMaxMicros, 1000},
		{"avg_reads", "avg_logical_reads", qsMaxCount, 1},
		{"avg_rows", "avg_rows", qsMaxCount, 1},
	} {
		v, present, ok := qsNum(cells[c.role], c.max)
		if !ok {
			return importer.ReasonBadNumber, nil
		}
		if present {
			attrs[c.attr] = qsFmt(v / c.div)
			hasRuntime = true
		}
	}
	if !qsEmpty(cells["last_exec"]) {
		t, ok := qsParseTime(cells["last_exec"])
		if !ok {
			return importer.ReasonBadTime, nil
		}
		attrs["last_execution"] = t.Format("2006-01-02T15:04:05Z")
	}
	if v := strings.TrimSpace(cells["forced"]); !qsEmpty(v) {
		switch strings.ToLower(v) {
		case "1", "true":
			attrs["forced"] = "true"
		case "0", "false":
			attrs["forced"] = "false"
		default:
			return importer.ReasonUnsupportedFormat, nil
		}
		hasRuntime = true
	}
	execType := ""
	if v := strings.TrimSpace(cells["exec_type"]); !qsEmpty(v) {
		switch strings.ToLower(v) {
		case "0", "regular":
			execType = "regular"
		case "3", "aborted":
			execType = "aborted"
		case "4", "exception":
			execType = "exception"
		default:
			return importer.ReasonUnsupportedFormat, nil
		}
		attrs["execution_type"] = execType
	}

	// Wait stats: wait_category_desc names the category, avg_query_wait_time is in ms.
	category := ""
	var waitMs *float64
	if v := strings.TrimSpace(cells["wait_cat"]); !qsEmpty(v) {
		if c, known := qsWaitCategories[strings.ToLower(v)]; known {
			category = c
		} else if qsWord.MatchString(v) {
			category = "Unknown" // a category this importer does not know, never stored
		} else {
			return importer.ReasonUnsupportedFormat, nil
		}
	}
	if w, present, ok := qsNum(cells["wait_ms"], qsMaxMillis); !ok {
		return importer.ReasonBadNumber, nil
	} else if present {
		d := math.Round(w*1000) / 1000
		waitMs = &d
	}

	loc := qsKey{q: int64(qid), p: planID, s: start.UnixNano(), e: end.UnixNano(), x: execType}
	emitRuntime := hasRuntime || category == ""
	if emitRuntime {
		if _, dup := p.runKey[loc]; dup {
			emitRuntime = false
			p.dup = true
		}
	}
	emitWait := category != ""
	if emitWait {
		wk := loc
		wk.w = category
		if _, dup := p.runKey[wk]; dup {
			emitWait = false
			p.dup = true
		} else {
			p.runKey[wk] = struct{}{}
		}
	}
	if !emitRuntime && !emitWait {
		return "", nil
	}
	p.s.seen(start)
	p.s.seen(end)
	if emitRuntime {
		p.runKey[loc] = struct{}{}
		e := evidence.Event{Kind: KindQueryInterval, Host: p.host, Start: start, End: end, TZRule: clock.RuleUTCField,
			Severity: "info", Attrs: attrs}
		if durMs != nil {
			e.DurationMs, e.Unit = durMs, "ms"
		}
		if err := p.s.put(e, line, 0); err != nil {
			return "", err
		}
	}
	if emitWait {
		wa := map[string]string{"query_id": attrs["query_id"], "wait_category": category, "wait_class": qsWaitClass(category)}
		if v, ok := attrs["plan_id"]; ok {
			wa["plan_id"] = v
		}
		e := evidence.Event{Kind: KindWait, Host: p.host, Start: start, End: end, TZRule: clock.RuleUTCField,
			Severity: "info", Attrs: wa}
		if waitMs != nil {
			e.DurationMs, e.Unit = waitMs, "ms"
		}
		if err := p.s.put(e, line, 1); err != nil {
			return "", err
		}
	}
	return "", nil
}

// qsFurniture reports the lines sqlcmd writes that are not data: the row of
// dashes under the header and the "(N rows affected)" footer.
func qsFurniture(rec []string) bool {
	if len(rec) == 1 {
		s := strings.TrimSpace(rec[0])
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && strings.Contains(s, " row") && strings.Contains(s, "affected") {
			return true
		}
	}
	for _, c := range rec {
		c = strings.TrimSpace(c)
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return len(rec) > 0
}

// Parse reads a Query Store export. A file with no interval time, or without
// query_id, is refused before any row is read.
func (x qsImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := effectiveLimits(opts.Limits, x.DefaultLimits())
	s := newSink(ctx, QSName, opts, lim, emit)
	s.rep.Quality = evidence.SourceQuality{TimezoneRule: clock.RuleUTCField}
	s.rep.FieldRules = map[string]string{"start_time": clock.RuleUTCField, "end_time": clock.RuleUTCField}
	if err := ctx.Err(); err != nil {
		return s.rep, err
	}
	// The export carries no server name, so the owner must have picked the host.
	if opts.Host == nil || opts.Host.Label == "" {
		return s.rep, importer.ErrUnknownHost
	}
	br, head, err := sniffed(importer.LimitReader(r, lim.MaxBytes))
	if err != nil {
		return s.rep, err
	}
	p := &qsProc{s: s, host: opts.Host.Label, runKey: map[qsKey]struct{}{}}
	maxRec := 4 * lim.MaxLineBytes
	text := strings.TrimLeft(headText(head), " \t\r\n")
	if text != "" && (text[0] == '[' || text[0] == '{') {
		err = p.parseJSON(br, lim, maxRec)
	} else {
		err = p.parseCSV(br, lim, maxRec)
	}
	if err != nil {
		return s.rep, err
	}
	s.rep.KnownGaps = append(s.rep.KnownGaps, gapQSAverages, gapQSCapture, gapQSCols)
	if p.hadText {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapQSText)
	}
	if p.dup {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapQSDup)
	}
	return s.finish()
}

func (p *qsProc) parseCSV(br *bufio.Reader, lim importer.Limits, maxRec int) error {
	csvr, _, err := textio.NewCSV(br, textio.CSVOptions{MaxFields: lim.MaxFields, MaxLine: lim.MaxLineBytes,
		MaxRecord: maxRec, Skip: qsFurniture})
	if err != nil {
		return err
	}
	header := csvr.Header()
	col := map[string]int{}
	for i, h := range header {
		role, known := qsRoles[normHeader(h)]
		if !known {
			if f := unscrubbed(h); f != "" {
				return &importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: f}
			}
			continue
		}
		if role == "drop" {
			p.hadText = true
			continue
		}
		if _, dup := col[role]; dup {
			// Two columns that fill one role (SELECT * over a join repeats plan_id):
			// the second could hold other data, so the file is refused. The name
			// reported is our own canonical one.
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: roleColumn(role)}
		}
		col[role] = i
	}
	if _, ok := col["query_id"]; !ok {
		return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "query_id"}
	}
	_, hs := col["start"]
	_, he := col["end"]
	if !hs && !he {
		return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "start_time"}
	}
	cells := map[string]string{}
	badRun := 0
	for {
		rec, line, err := csvr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			var re importer.RowError
			if !errors.As(err, &re) {
				return err
			}
			if rerr := p.s.row(); rerr != nil {
				return rerr
			}
			if re.Line < 1 {
				re.Line = line
			}
			p.s.reject(re.Line, re.Reason)
			if badRun++; badRun > 1000 {
				return &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
			}
			continue
		}
		badRun = 0
		if err := p.s.row(); err != nil {
			return err
		}
		for k := range cells {
			delete(cells, k)
		}
		for role, i := range col {
			if i < len(rec) {
				cells[role] = rec[i]
			}
		}
		reason, err := p.row(cells, line)
		if err != nil {
			return err
		}
		if reason != "" {
			p.s.reject(line, reason)
		}
	}
}

func roleColumn(role string) string {
	if n, ok := qsRoleNames[role]; ok {
		return n
	}
	for h, r := range qsRoles {
		if r == role && h == role {
			return h
		}
	}
	for h, r := range qsRoles {
		if r == role {
			return h
		}
	}
	return "query_id"
}

// qsCountReader fails a JSON import whose single element grows past a bound: the
// decoder buffers a whole value, so one huge string would otherwise fill memory.
type qsCountReader struct {
	r        io.Reader
	max      int64
	total    int64
	recStart int64
	err      error
}

func (c *qsCountReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.r.Read(p)
	c.total += int64(n)
	if pending := c.total - c.recStart; pending > 2*c.max {
		c.err = &importer.LimitError{Limit: "record-bytes", Max: c.max, At: pending}
		return 0, c.err
	}
	return n, err
}

// qsRaw is one JSON object reduced to its role cells.
type qsRaw struct {
	cells map[string]string
	bad   bool
}

func (q *qsRaw) set(role, v string) {
	if role == "" || role == "drop" {
		return
	}
	if _, dup := q.cells[role]; dup {
		q.bad = true
		return
	}
	q.cells[role] = v
}

// value consumes the value whose first token is tok and records it under role.
func (q *qsRaw) value(dec *json.Decoder, tok json.Token, role string) error {
	switch v := tok.(type) {
	case json.Delim:
		if err := skipContainer(dec, v); err != nil {
			return err
		}
		if role != "" && role != "drop" {
			q.bad = true // a role column holding an object or an array
		}
	case string:
		q.set(role, v)
	case json.Number:
		q.set(role, v.String())
	case bool:
		q.set(role, strconv.FormatBool(v))
	case nil:
		// FOR JSON leaves out NULL; an explicit null is the same: absent.
	}
	return nil
}

func skipContainer(dec *json.Decoder, open json.Delim) error {
	if open != '{' && open != '[' {
		return nil
	}
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

func roleOfKey(tok json.Token) (string, bool) {
	k, ok := tok.(string)
	if !ok {
		return "", false
	}
	return qsRoles[normHeader(k)], true
}

// readObject reads the members of an object whose '{' is consumed.
func (p *qsProc) readObject(dec *json.Decoder, q *qsRaw) error {
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		role, _ := roleOfKey(kt)
		if role == "drop" {
			p.hadText = true
		}
		vt, err := dec.Token()
		if err != nil {
			return err
		}
		if err := q.value(dec, vt, role); err != nil {
			return err
		}
	}
	_, err := dec.Token() // '}'
	return err
}

func (p *qsProc) parseJSON(br *bufio.Reader, lim importer.Limits, maxRec int) error {
	dr, _, err := textio.Decode(br)
	if err != nil {
		return err
	}
	cr := &qsCountReader{r: dr, max: int64(maxRec)}
	dec := json.NewDecoder(cr)
	dec.UseNumber()
	var elem int64
	badRun := 0

	handle := func(q *qsRaw) error {
		elem++
		cr.recStart = dec.InputOffset()
		if err := p.s.row(); err != nil {
			return err
		}
		if !p.checked {
			p.checked = true
			if _, ok := q.cells["query_id"]; !ok {
				return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "query_id"}
			}
			_, hs := q.cells["start"]
			_, he := q.cells["end"]
			if !hs && !he {
				return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "start_time"}
			}
		}
		if q.bad {
			p.s.reject(elem, importer.ReasonUnsupportedFormat)
			if badRun++; badRun > 1000 {
				return &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
			}
			return nil
		}
		reason, err := p.row(q.cells, elem)
		if err != nil {
			return err
		}
		if reason != "" {
			p.s.reject(elem, reason)
			if badRun++; badRun > 1000 {
				return &importer.QuarantineError{Reason: importer.ReasonRejectFraction}
			}
			return nil
		}
		badRun = 0
		return nil
	}
	// rows reads the elements of an array whose '[' is consumed.
	rows := func() error {
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			q := &qsRaw{cells: map[string]string{}}
			if d, ok := tok.(json.Delim); ok && d == '{' {
				if err := p.readObject(dec, q); err != nil {
					return err
				}
			} else {
				q.bad = true
				if err := q.value(dec, tok, ""); err != nil {
					return err
				}
			}
			if err := handle(q); err != nil {
				return err
			}
		}
		_, err := dec.Token() // ']'
		return err
	}

	err = func() error {
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			d, ok := tok.(json.Delim)
			if !ok {
				return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
			}
			switch d {
			case '[':
				if err := rows(); err != nil {
					return err
				}
			case '{':
				// One object per line, or a ROOT('name') wrapper around the array.
				q := &qsRaw{cells: map[string]string{}}
				wrapped := false
				for dec.More() {
					kt, err := dec.Token()
					if err != nil {
						return err
					}
					role, _ := roleOfKey(kt)
					if role == "drop" {
						p.hadText = true
					}
					vt, err := dec.Token()
					if err != nil {
						return err
					}
					if vd, isDelim := vt.(json.Delim); isDelim && vd == '[' && role == "" && len(q.cells) == 0 && !q.bad {
						wrapped = true
						if err := rows(); err != nil {
							return err
						}
						continue
					}
					if err := q.value(dec, vt, role); err != nil {
						return err
					}
				}
				if _, err := dec.Token(); err != nil {
					return err
				}
				if !wrapped {
					if err := handle(q); err != nil {
						return err
					}
				}
			default:
				return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
			}
		}
	}()
	if err == nil {
		if elem == 0 {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "query_id"}
		}
		return nil
	}
	var le *importer.LimitError
	var qe *importer.QuarantineError
	switch {
	case errors.As(err, &le), errors.As(err, &qe), errors.Is(err, importer.ErrLimit), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return err
	}
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	if errors.As(err, &se) || errors.As(err, &te) || errors.Is(err, io.ErrUnexpectedEOF) {
		// Broken or cut JSON cannot be resynchronised: keep the rows read so far,
		// count the break as one rejected row, and stop.
		if elem == 0 {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		if rerr := p.s.row(); rerr != nil {
			return rerr
		}
		p.s.reject(elem+1, importer.ReasonUnsupportedFormat)
		return nil
	}
	return err
}
