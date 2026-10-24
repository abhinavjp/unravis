// Package mongoexports imports owner-run MongoDB exports as evidence events (wave 2,
// card W2-k): the profiler collection (mongoexport, one document per line), a
// $currentOp loop, a serverStatus loop and mongostat --json.
//
// Field names come from MongoDB's public documentation for system.profile,
// $currentOp, serverStatus and mongostat. Three details are not confirmed against a
// real export and are read leniently: the profiler "ts" is taken as the operation end,
// "currentOpTime" is taken as the snapshot time, and mongostat "time" is host-local.
// Each is stated as a known gap on the report.
//
// Privacy (wave plan section 2.2): the query text never survives the parse. Filters,
// command bodies, namespaces, user names, client addresses and application names are
// read into a throwaway buffer and dropped. An event carries a fixed category (an
// allowlisted command or operation name, plan stage names) and counts, never a name from
// the file. FTDC binary files are refused. The package makes no network calls.
package mongoexports

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

const (
	// maxDurationMs is longer than any plausible operation: bad-number.
	maxDurationMs = 400 * 24 * 3600 * 1000.0
	maxSlowms     = 3_600_000
	maxCount      = 1e15
	rejectFrac    = 0.05
	maxFieldsDflt = 4096
)

// Allowlists. Each value that reaches an event is looked up here first, so the parse
// itself is fail-closed: a name from the file that is not listed is dropped. The lists
// match the case-export enums in internal/evidence (the export keeps the same set).
var (
	commandNames = set("find", "aggregate", "count", "distinct", "getMore", "insert", "update", "delete",
		"findAndModify", "mapReduce", "createIndexes", "dropIndexes", "drop", "create", "collStats", "dbStats",
		"listCollections", "listIndexes", "killCursors", "bulkWrite", "commitTransaction", "abortTransaction",
		"explain", "validate", "compact", "reIndex", "renameCollection", "collMod")
	profileTypes = set("command", "query", "getmore", "update", "insert", "remove", "none", "write", "transaction")
	currentOps   = set("none", "update", "insert", "query", "getmore", "remove", "killcursors", "command")
	planStages   = set("COLLSCAN", "IXSCAN", "FETCH", "SORT", "SORT_KEY_GENERATOR", "PROJECTION_SIMPLE",
		"PROJECTION_COVERED", "PROJECTION_DEFAULT", "LIMIT", "SKIP", "COUNT_SCAN", "DISTINCT_SCAN", "EOF", "IDHACK",
		"SHARDING_FILTER", "TEXT", "TEXT_MATCH", "SUBPLAN", "OR", "AND_HASH", "AND_SORTED", "MULTI_PLAN",
		"CACHED_PLAN", "GROUP", "LOOKUP", "UNWIND", "UPDATE", "DELETE", "COUNT", "RECORD_STORE_FAST_COUNT",
		"QUEUED_DATA", "TRIAL", "SHARD_MERGE", "SINGLE_SHARD", "COLUMN_SCAN")
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

var (
	reVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$`)
	reCmdName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	reQHash   = regexp.MustCompile(`^[0-9A-Fa-f]{1,16}$`)
	reStage   = regexp.MustCompile(`^[A-Z_]{2,32}`)
)

// ---- line engine -----------------------------------------------------------------

// liner is the per-format part of a parse. The engine owns reading, limits, comments,
// the window and emitting; a liner turns one JSON line into events.
type liner interface {
	// begin runs before the first line, with the effective limits. A non-nil error refuses
	// the file.
	begin(lim importer.Limits, opts importer.Options, rep *importer.Report) error
	// line parses one line. A reason rejects the row; an error refuses the file.
	// sample is the time of the last "# unravis-sample" comment line, or zero.
	line(line []byte, no int64, sample time.Time, opts importer.Options, rep *importer.Report) (t time.Time, evs []evidence.Event, reason importer.Reason, err error)
	// end adds gaps and source quality once the file is read.
	end(rep *importer.Report, opts importer.Options)
}

// base holds what every liner needs: the key cap of a line, the host resolver and the
// version the file states.
type base struct {
	maxKeys int
	h       hostResolver
	version string
}

func (b *base) setup(lim importer.Limits) {
	b.maxKeys = lim.MaxFields
	if b.maxKeys <= 0 {
		b.maxKeys = maxFieldsDflt
	}
}

// host returns the label of the named host, or the picked host's label. A file that
// leaves the host unknown is refused.
func (b *base) host(name string, opts importer.Options, rep *importer.Report) (string, error) {
	label, err := b.h.resolve(name, opts, rep)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", importer.ErrUnknownHost
	}
	return label, nil
}

// noteVersion keeps the first version string that has the shape of one.
func (b *base) noteVersion(raw json.RawMessage) {
	if b.version != "" {
		return
	}
	if s, ok := str(raw); ok && len(s) <= 40 && reVersion.MatchString(s) {
		b.version = s
	}
}

// spec is what differs between the four importers besides the liner.
type spec struct {
	name      string
	def       importer.Limits // the card's caps
	maxEvents int64
}

func (s spec) effective(in importer.Limits) importer.Limits {
	out := s.def
	if in.MaxBytes > 0 && in.MaxBytes < out.MaxBytes {
		out.MaxBytes = in.MaxBytes
	}
	if in.MaxRows > 0 && in.MaxRows < out.MaxRows {
		out.MaxRows = in.MaxRows
	}
	if in.MaxLineBytes > 0 && in.MaxLineBytes < out.MaxLineBytes {
		out.MaxLineBytes = in.MaxLineBytes
	}
	if in.MaxFields > 0 && in.MaxFields < out.MaxFields {
		out.MaxFields = in.MaxFields
	}
	if in.MaxEvents > 0 && in.MaxEvents < out.MaxEvents {
		out.MaxEvents = in.MaxEvents
	}
	// The reject fraction is a policy knob, not one of the section 2.4 caps.
	if in.MaxRejectFraction > 0 {
		out.MaxRejectFraction = in.MaxRejectFraction
	}
	return out
}

func (s spec) limits() importer.Limits {
	l := s.def
	l.MaxEvents = s.maxEvents
	return l
}

// run is the shared Parse.
func run(ctx context.Context, s spec, ln liner, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	s.def.MaxEvents = s.maxEvents
	lim := s.effective(opts.Limits)
	rep := importer.Report{Importer: s.name}
	if err := ctx.Err(); err != nil {
		return rep, err
	}

	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 64*1024)
	head, perr := br.Peek(16)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		return rep, perr
	}
	if err := importer.Sniff(head); err != nil {
		return rep, err
	}
	if isFTDC(head) {
		// FTDC diagnostic.data is binary BSON: refused, never read (use the serverStatus recipe).
		return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return rep, err
	}
	if err := ln.begin(lim, opts, &rep); err != nil {
		return rep, err
	}

	var sample time.Time
	for n := 0; ; n++ {
		if n&255 == 0 {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
		}
		line, no, err := lr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		var re importer.RowError
		if errors.As(err, &re) {
			// A line that is not strict UTF-8: one rejected row, the stream goes on.
			rep.Rows++
			if err := lim.CheckRows(rep.Rows); err != nil {
				return rep, err
			}
			rep.Reject(no, re.Reason)
			continue
		}
		if err != nil {
			// A read error or a limit breach ends the file; it is never taken for its end.
			return rep, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			// A recipe comment. Only the sample marker is read; any other comment is skipped.
			if t, ok := sampleMarker(line); ok {
				sample = t
			}
			continue
		}
		rep.Rows++
		if err := lim.CheckRows(rep.Rows); err != nil {
			return rep, err
		}
		if rep.Rows == 1 && line[0] != '{' {
			return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		t, evs, reason, lerr := ln.line(line, no, sample, opts, &rep)
		if lerr != nil {
			return rep, lerr
		}
		if reason != "" {
			rep.Reject(no, reason)
			continue
		}
		rep.Cover(t)
		for _, ev := range evs {
			if !inWindow(ev, opts.Window) {
				continue
			}
			if err := lim.CheckEvents(rep.Emitted + 1); err != nil {
				return rep, err
			}
			if err := emit(ev); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
	}
	if rep.Rows == 0 {
		return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	if err := lim.CheckRejects(rep.Rows, rep.Rejected); err != nil {
		return rep, err
	}
	ln.end(&rep, opts)
	return rep, nil
}

// isFTDC reports the head of an FTDC metrics file: a BSON document whose first element
// is the date field "_id".
func isFTDC(head []byte) bool {
	return len(head) >= 9 && head[4] == 0x09 && bytes.Equal(head[5:9], []byte("_id\x00"))
}

func inWindow(e evidence.Event, w importer.Window) bool {
	if !w.From.IsZero() && e.End.Before(w.From) {
		return false
	}
	if !w.To.IsZero() && e.Start.After(w.To) {
		return false
	}
	return true
}

// sampleMarker reads "# unravis-sample <ISO time with Z or an offset>".
func sampleMarker(line []byte) (time.Time, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(string(line), "#")), "unravis-sample")
	if !ok {
		return time.Time{}, false
	}
	t, rule := parseTimeString(strings.TrimSpace(rest))
	if rule != "" {
		return time.Time{}, false
	}
	return t, true
}

// ---- detection helpers -----------------------------------------------------------

// decodeHead strips a BOM and decodes UTF-16 so Detect sees the text Parse will.
func decodeHead(head []byte) []byte {
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		return head[3:]
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		order := unicode.LittleEndian
		if head[0] == 0xFE {
			order = unicode.BigEndian
		}
		body := head[2:]
		if len(body) > 4096 {
			body = body[:4096]
		}
		body = body[:len(body)&^1]
		out, err := unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder().Bytes(body)
		if err != nil {
			return nil
		}
		return out
	}
	return head
}

// firstLine returns the first non-blank, non-comment line of the head, or nil. An archive,
// an FTDC file or a mongod.log structured line is not one of these exports.
func firstLine(head []byte) []byte {
	if importer.Sniff(head) != nil || isFTDC(head) {
		return nil
	}
	text := decodeHead(head)
	for len(text) > 0 {
		text = bytes.TrimLeft(text, " \t\r\n")
		line := text
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, text = line[:i], line[i+1:]
		} else {
			text = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if line[0] != '{' || bytes.HasPrefix(line, []byte(`{"t":{"$date"`)) {
			return nil
		}
		return line
	}
	return nil
}

func hasAll(line []byte, keys ...string) bool {
	for _, k := range keys {
		if !bytes.Contains(line, []byte(`"`+k+`":`)) {
			return false
		}
	}
	return true
}

func hasAny(line []byte, keys ...string) bool {
	for _, k := range keys {
		if bytes.Contains(line, []byte(`"`+k+`":`)) {
			return true
		}
	}
	return false
}

func fileHint(name string, words ...string) bool {
	lower := strings.ToLower(name)
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// ---- JSON helpers -----------------------------------------------------------------

// forEachKey walks the top-level keys of one JSON object. Every value is handed over as
// raw bytes that the caller reads or drops; nothing is kept here. ok is false when the
// line is not exactly one object. tooMany is true when the object has more keys than max.
func forEachKey(line []byte, max int, fn func(key string, raw json.RawMessage)) (ok, tooMany bool, keys int) {
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false, false, 0
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return false, false, keys
		}
		key, isStr := kt.(string)
		if !isStr {
			return false, false, keys
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return false, false, keys
		}
		keys++
		if max > 0 && keys > max {
			return true, true, keys
		}
		fn(key, v)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return false, false, keys
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return false, false, keys
	}
	return true, false, keys
}

// number reads a JSON number, or an Extended JSON wrapper of one ($numberInt,
// $numberLong, $numberDouble). A string is not a number.
func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	if raw[0] == '{' {
		var w map[string]json.RawMessage
		if json.Unmarshal(raw, &w) != nil || len(w) != 1 {
			return 0, false
		}
		for k, v := range w {
			switch k {
			case "$numberInt", "$numberLong", "$numberDouble":
				var s string
				if json.Unmarshal(v, &s) != nil || len(s) > 40 {
					return 0, false
				}
				f, err := strconv.ParseFloat(s, 64)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
					return 0, false
				}
				return f, true
			}
		}
		return 0, false
	}
	if raw[0] == '"' {
		return 0, false
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

// count reads a non-negative whole number below 1e15 and writes it as text.
func count(raw json.RawMessage) (string, bool) {
	n, ok := number(raw)
	if !ok || n < 0 || n != math.Trunc(n) || n >= maxCount {
		return "", false
	}
	return strconv.FormatInt(int64(n), 10), true
}

func str(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func boolean(raw json.RawMessage) (string, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return "true", true
	case "false":
		return "false", true
	}
	return "", false
}

// firstKey returns the first key of an object, the command name, when it is on the
// command allowlist; else "". Nothing else of the object is kept.
func firstKey(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '{' {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ""
	}
	kt, err := dec.Token()
	if err != nil {
		return ""
	}
	k, _ := kt.(string)
	if !reCmdName.MatchString(k) || !commandNames[k] {
		return ""
	}
	return k
}

// stageTokens reduces planSummary ("IXSCAN { a: 1 }, COLLSCAN") to its stage names.
// Key patterns and bounds are dropped, and so is any stage name that is not a known one.
func stageTokens(s string) string {
	var toks []string
	depth, start := 0, 0
	flush := func(end int) {
		seg := strings.TrimSpace(s[start:end])
		m := reStage.FindString(seg)
		if m == "" || !planStages[m] {
			return
		}
		if rest := seg[len(m):]; rest != "" && rest[0] != ' ' && rest[0] != '{' {
			return
		}
		if len(toks) < 8 {
			toks = append(toks, m)
		}
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return strings.Join(toks, ",")
}

// ---- time ------------------------------------------------------------------------

// parseTimeString reads an ISO time. A string with an offset or Z is placed on the
// clock; a naive one is tz-unlabelled (it cannot be placed without a zone).
func parseTimeString(s string) (time.Time, importer.Reason) {
	if len(s) > 64 {
		return time.Time{}, importer.ReasonBadTime
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.999999999Z0700", s)
	}
	if err != nil {
		if _, nerr := time.Parse("2006-01-02T15:04:05.999999999", s); nerr == nil {
			return time.Time{}, importer.ReasonTZUnlabelled
		}
		return time.Time{}, importer.ReasonBadTime
	}
	return checkYear(t.UTC())
}

func checkYear(t time.Time) (time.Time, importer.Reason) {
	if y := t.Year(); y < 2000 || y > 2100 {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
}

// parseMongoTime reads a time field: an ISO string, {"$date": <ISO string>},
// {"$date": <epoch ms>} or {"$date": {"$numberLong": "<epoch ms>"}}. The rule says how
// it became UTC.
func parseMongoTime(raw json.RawMessage) (t time.Time, rule string, reason importer.Reason) {
	if len(raw) == 0 {
		return time.Time{}, "", importer.ReasonBadTime
	}
	if raw[0] == '"' {
		s, ok := str(raw)
		if !ok {
			return time.Time{}, "", importer.ReasonBadTime
		}
		t, reason = parseTimeString(s)
		return t, clock.RuleOffsetInField, reason
	}
	if raw[0] == '{' {
		var w map[string]json.RawMessage
		if json.Unmarshal(raw, &w) != nil || len(w) != 1 {
			return time.Time{}, "", importer.ReasonBadTime
		}
		inner, ok := w["$date"]
		if !ok {
			return time.Time{}, "", importer.ReasonBadTime
		}
		if len(inner) > 0 && inner[0] == '"' {
			return parseMongoTime(inner)
		}
		ms, ok := number(inner)
		if !ok {
			return time.Time{}, "", importer.ReasonBadTime
		}
		t, reason = epochMs(ms)
		return t, clock.RuleUTCField, reason
	}
	return time.Time{}, "", importer.ReasonBadTime
}

func epochMs(ms float64) (time.Time, importer.Reason) {
	if ms < 0 || ms > 5e12 {
		return time.Time{}, importer.ReasonBadTime
	}
	return checkYear(time.UnixMilli(int64(ms)).UTC())
}

// ---- hosts -----------------------------------------------------------------------

// hostResolver maps the host names a file states to labels. A raw name is reported to
// Report.Observe when the user picked no host, and is never stored or put in an error.
type hostResolver struct {
	cache map[string]string
}

// stripPort cuts ":27017" from "name:27017"; an IPv6 literal in brackets keeps its form.
func stripPort(name string) string {
	if strings.HasPrefix(name, "[") {
		if i := strings.Index(name, "]"); i > 0 {
			return name[1:i]
		}
		return name
	}
	if i := strings.LastIndexByte(name, ':'); i > 0 && strings.Count(name, ":") == 1 {
		return name[:i]
	}
	return name
}

func plainName(name string) bool {
	return name != "" && len(name) <= 253 && utf8.ValidString(name) && !strings.ContainsAny(name, "\x00\r\n\t")
}

// resolve returns the host label for a file-stated name, or the picked host's label when
// the file names none. "" with a nil error means no host is known yet; the caller then
// refuses the file with ErrUnknownHost before it emits.
func (h *hostResolver) resolve(raw string, opts importer.Options, rep *importer.Report) (string, error) {
	name := stripPort(raw)
	if !plainName(name) {
		if opts.Host != nil {
			return opts.Host.Label, nil
		}
		return "", nil
	}
	if label, ok := h.cache[name]; ok {
		return label, nil
	}
	label, err := h.lookup(name, opts, rep)
	if err != nil {
		return "", err
	}
	if h.cache == nil {
		h.cache = map[string]string{}
	}
	h.cache[name] = label
	return label, nil
}

func (h *hostResolver) lookup(name string, opts importer.Options, rep *importer.Report) (string, error) {
	if opts.Host != nil {
		if opts.Hosts != nil {
			if hh, ok := opts.Hosts(name); ok && hh.Label != "" && hh.Label != opts.Host.Label {
				return "", importer.ErrUnknownHost
			}
		}
		return opts.Host.Label, nil
	}
	if err := rep.Observe(name); err != nil {
		return "", err
	}
	if opts.Hosts == nil {
		return "", nil
	}
	hh, ok := opts.Hosts(name)
	if !ok || hh.Label == "" {
		return "", importer.ErrUnknownHost
	}
	return hh.Label, nil
}

// hint returns the host hint for a file-stated name (the picked host, else the map).
func hintFor(name string, opts importer.Options) (importer.HostHint, bool) {
	name = stripPort(name)
	if opts.Host != nil {
		return *opts.Host, true
	}
	if opts.Hosts != nil && plainName(name) {
		return opts.Hosts(name)
	}
	return importer.HostHint{}, false
}

// ---- shared report text -------------------------------------------------------------

// quality fills the coverage and time-zone fields of the source quality.
func quality(rep *importer.Report, version, rule string) evidence.SourceQuality {
	q := evidence.SourceQuality{SourceVersion: version, TimezoneRule: rule}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
	}
	return q
}

func newEvent(opts importer.Options, name, kind, host string, no int64, ord int, start, end time.Time, rule string) evidence.Event {
	return evidence.Event{
		EvidenceID: evidence.EventID(opts.CaseID, name, opts.ImportNonce, no, ord),
		Source:     name,
		Kind:       kind,
		Host:       host,
		Start:      start,
		End:        end,
		Severity:   "info",
		Line:       no,
		TZRule:     rule,
	}
}

func fptr(v float64) *float64 { return &v }

// nonNeg reads a finite, non-negative number below the count cap.
func nonNeg(raw json.RawMessage) (float64, bool) {
	n, ok := number(raw)
	if !ok || n < 0 || n >= maxCount {
		return 0, false
	}
	return n, true
}

func fmtNum(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// slowmsThreshold reads the owner-supplied slowms (whole milliseconds, 0 to an hour).
func slowmsThreshold(opts importer.Options) (int64, bool) {
	s, ok := opts.Supplied["slowms"]
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || v < 0 || v > maxSlowms {
		return 0, false
	}
	return v, true
}

// ---- importer plumbing -----------------------------------------------------------

// impl is the importer.Importer every constructor returns.
type impl struct {
	sp       spec
	attrs    []string
	detect   func(head []byte, fileName string) int
	newLiner func() liner
}

func (m impl) Name() string { return m.sp.name }

func (m impl) Detect(head []byte, fileName string) int { return m.detect(head, fileName) }

func (m impl) AllowedAttrs() map[string]bool { return set(m.attrs...) }

func (m impl) DefaultLimits() importer.Limits { return m.sp.limits() }

func (m impl) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, m.sp, m.newLiner(), r, opts, emit)
}
