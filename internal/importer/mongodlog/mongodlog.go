// Package mongodlog imports mongod.log in the structured JSON format (MongoDB 4.4 and
// later) as slow-operation events (wave plan card P4).
//
// Format and log ids come from MongoDB's public "Log Messages" documentation
// (structured logging, LOGV2). Each id used below is cited where it is declared. The
// pre-4.4 plain-text format is unsupported (the first line is not JSON).
//
// Privacy (wave plan section 4): command bodies, filters, client, remote and appName are
// dropped while parsing and never stored, logged or put in an error. Only an allowlist of
// scalar attributes survives (see allowedAttrs). Errors carry a Reason and a line
// number, never row content. The package makes no network calls.
package mongodlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// Name is the stable importer name.
	Name = "mongod-json"
	// SourceLabel is how the source is named in absence wording.
	SourceLabel = "mongod.log"
	// KindSlowOp is the Event.Kind of a slow operation.
	KindSlowOp = "slow-op"
)

// Log ids, from the MongoDB docs "Log Messages" page (structured log examples).
// The ids are matched exactly; a mismatch against a real file is a reason to correct
// the constant, not to loosen the matching (R13 gate: no real file was read to author this).
const (
	// idSlowQuery: COMMAND, msg "Slow query"; attr.durationMillis is the operation time in ms.
	// The docs' log-message example for slow operations; also used on 8.0+ with workingMillis.
	idSlowQuery = 51803
	// idBuildInfo: CONTROL, msg "Build Info"; attr.buildInfo.version is the server version.
	idBuildInfo = 23403
	// idOptions: CONTROL, msg "Options set by command line"; attr.options is the normalised
	// configuration, where operationProfiling.slowOpThresholdMs is slowms.
	idOptions = 21951
	// idStarting: CONTROL, msg "MongoDB starting"; attr.host is the server host name.
	idStarting = 4615611
	// idRestarted: CONTROL, msg "***** SERVER RESTARTED *****"; written when mongod starts and
	// appends to an existing log file, so it marks a restart, not a log rotation. Also matched
	// by message text so a wrong id cannot hide it.
	idRestarted = 20698
)

const (
	msgRestarted = "***** SERVER RESTARTED *****"
	// msgRotated is the message written when the log is rotated; matched by text only.
	msgRotated = "Log rotation initiated"
	// No id is cited for the runtime profiler change: it is detected by message text only.
	msgProfilerChanged = "Profiler settings changed"
)

// Limits (wave plan section 4): 2 GiB, 10M rows, 256 KiB per line.
const (
	maxBytes   = int64(2) << 30
	maxRows    = int64(10_000_000)
	maxLineLen = 256 << 10
	maxFields  = 4096
	rejectFrac = 0.05

	maxDurationMs = 400 * 24 * 3600 * 1000.0 // longer than any plausible operation: bad-number
	maxSlowms     = 3_600_000
)

// Source-quality sentences. These are fixed text: they never hold row content and they
// avoid causal wording (evidence.LintCausal).
const (
	samplingSampled = "slow operations are sampled (slowOpSampleRate below 1)"
	samplingRotated = "log rotation marker inside the file"
	samplingRestart = "server restart marker inside the file"
	samplingChanged = "slow-operation threshold changed inside the file"
)

var allowedAttrs = map[string]bool{
	"type": true, "ns": true, "command": true, "planSummary": true,
	"keysExamined": true, "docsExamined": true, "nreturned": true, "numYields": true,
	"queryHash": true, "workingMs": true,
}

var (
	reVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$`)
	reCmdName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	reType    = regexp.MustCompile(`^[A-Za-z]{1,32}$`)
	reQHash   = regexp.MustCompile(`^[0-9A-Fa-f]{1,16}$`)
	reStage   = regexp.MustCompile(`^[A-Z_]{2,32}`)
)

// Importer implements importer.Importer for mongod.log JSON.
type Importer struct{}

// New returns the importer.
func New() importer.Importer { return Importer{} }

func (Importer) Name() string { return Name }
func (Importer) AllowedAttrs() map[string]bool {
	out := make(map[string]bool, len(allowedAttrs))
	for k, v := range allowedAttrs {
		out[k] = v
	}
	return out
}

func (Importer) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: maxBytes, MaxRows: maxRows, MaxLineBytes: maxLineLen,
		MaxFields: maxFields, MaxRejectFraction: rejectFrac,
	}
}

// Detect scores the first line: a structured log line starts with {"t":{"$date":.
func (Importer) Detect(head []byte, fileName string) int {
	if importer.Sniff(head) != nil {
		return 0
	}
	text := decodeHead(head)
	text = bytes.TrimLeft(text, " \t\r\n")
	if !bytes.HasPrefix(text, []byte(`{"t":{"$date":"`)) {
		return 0
	}
	score := 80
	first := text
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if bytes.Contains(first, []byte(`"c":"`)) && bytes.Contains(first, []byte(`"id":`)) && bytes.Contains(first, []byte(`"msg":"`)) {
		score += 10
	}
	lower := strings.ToLower(fileName)
	if strings.HasSuffix(lower, ".log") || strings.Contains(lower, "mongo") {
		score += 5
	}
	return score
}

// decodeHead strips a BOM and decodes UTF-16 so Detect sees the same text Parse will.
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

// state is what one pass learns about the file besides events.
type state struct {
	version        string
	slowms         *int64
	sampled        bool
	rotated        bool
	restarted      bool
	profilerChange bool
	startCount     int
	slowSeen       bool
	hostLabel      string
	timeSeen       bool
}

func effectiveLimits(in importer.Limits) importer.Limits {
	d := Importer{}.DefaultLimits()
	out := d
	if in.MaxBytes > 0 && in.MaxBytes < d.MaxBytes {
		out.MaxBytes = in.MaxBytes
	}
	if in.MaxRows > 0 && in.MaxRows < d.MaxRows {
		out.MaxRows = in.MaxRows
	}
	if in.MaxLineBytes > 0 && in.MaxLineBytes < d.MaxLineBytes {
		out.MaxLineBytes = in.MaxLineBytes
	}
	if in.MaxFields > 0 && in.MaxFields < d.MaxFields {
		out.MaxFields = in.MaxFields
	}
	// The reject fraction is a policy knob, not one of the section 4 caps: the caller's
	// value (for example a preview that tolerates every reject) is honoured.
	if in.MaxRejectFraction > 0 {
		out.MaxRejectFraction = in.MaxRejectFraction
	}
	return out
}

// Parse reads the log and emits one slow-op event per "Slow query" line.
func (imp Importer) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := effectiveLimits(opts.Limits)
	rep := importer.Report{Importer: Name}
	rep.Rule("t.$date", clock.RuleOffsetInField)
	if err := ctx.Err(); err != nil {
		return rep, err
	}

	// Collect/Prepare already capped the bytes and sniffed the head; this is repeated so
	// a direct caller still gets the section 4 caps (a caller limit can only lower them).
	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 64*1024)
	head, perr := br.Peek(16)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		return rep, perr
	}
	if err := importer.Sniff(head); err != nil {
		return rep, err
	}
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return rep, err
	}

	st := &state{}
	if opts.Host != nil {
		st.hostLabel = opts.Host.Label
	}

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
			// A read error or a limit breach (bytes, line-bytes) ends the file; it is
			// never taken for the end of input.
			return rep, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		rep.Rows++
		if err := lim.CheckRows(rep.Rows); err != nil {
			return rep, err
		}
		if rep.Rows == 1 && line[0] != '{' {
			// Pre-4.4 text format (or not a mongod log at all).
			return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}

		t, reason, ev, werr := handleLine(line, no, st, opts, &rep)
		if werr != nil {
			return rep, werr
		}
		if reason != "" {
			rep.Reject(no, reason)
			continue
		}
		rep.Cover(t)
		if ev == nil || !inWindow(*ev, opts.Window) {
			continue
		}
		if err := emit(*ev); err != nil {
			return rep, err
		}
		rep.Emitted++
	}

	if rep.Rows == 0 {
		return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	if err := lim.CheckRejects(rep.Rows, rep.Rejected); err != nil {
		return rep, err
	}
	finish(&rep, st, opts)
	return rep, nil
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

// head fields of a structured log line. attr stays raw: most of it is dropped unread.
type logLine struct {
	T struct {
		Date json.RawMessage `json:"$date"`
	} `json:"t"`
	S    string          `json:"s"`
	ID   int64           `json:"id"`
	Msg  string          `json:"msg"`
	Attr json.RawMessage `json:"attr"`
}

// handleLine returns the line time, a reject reason (empty when the row is good), the
// event for a slow query, and a fatal error (unknown host, emit failure is separate).
func handleLine(line []byte, no int64, st *state, opts importer.Options, rep *importer.Report) (time.Time, importer.Reason, *evidence.Event, error) {
	var ll logLine
	if err := json.Unmarshal(line, &ll); err != nil {
		return time.Time{}, importer.ReasonFieldCount, nil, nil
	}
	t, reason := parseTime(ll.T.Date)
	if reason != "" {
		return time.Time{}, reason, nil, nil
	}
	st.timeSeen = true

	if ll.Msg == msgProfilerChanged {
		st.profilerChange = true
	}
	switch {
	case ll.ID == idSlowQuery:
		return slowQuery(ll, t, no, st, opts)
	case ll.ID == idBuildInfo:
		var a struct {
			BuildInfo struct {
				Version string `json:"version"`
			} `json:"buildInfo"`
		}
		if json.Unmarshal(ll.Attr, &a) == nil && len(a.BuildInfo.Version) <= 32 && reVersion.MatchString(a.BuildInfo.Version) {
			st.version = a.BuildInfo.Version
		}
	case ll.ID == idOptions:
		var a struct {
			Options struct {
				OperationProfiling struct {
					SlowOpThresholdMs *float64 `json:"slowOpThresholdMs"`
					SlowOpSampleRate  *float64 `json:"slowOpSampleRate"`
				} `json:"operationProfiling"`
			} `json:"options"`
		}
		if json.Unmarshal(ll.Attr, &a) == nil {
			op := a.Options.OperationProfiling
			if v := op.SlowOpThresholdMs; v != nil && *v >= 0 && *v <= maxSlowms && *v == math.Trunc(*v) {
				ms := int64(*v)
				st.slowms = &ms
			}
			if v := op.SlowOpSampleRate; v != nil && *v != 1 {
				st.sampled = true
			}
		}
	case ll.ID == idStarting:
		if st.startCount > 0 || st.slowSeen {
			st.restarted = true
		}
		st.startCount++
		var a struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(ll.Attr, &a) == nil {
			if err := observeHost(a.Host, st, opts, rep); err != nil {
				return t, "", nil, err
			}
		}
	case ll.ID == idRestarted || ll.Msg == msgRestarted:
		st.restarted = true
	case ll.Msg == msgRotated:
		st.rotated = true
	}
	return t, "", nil, nil
}

// observeHost records the server's own host name. A raw name is reported only when the
// user did not pick the host; the service maps or refuses it and the name is never stored.
// When the user picked a host, a name in the file that the host map knows under another
// label refuses the file (the same rule as the SQL Server XE importer); a name the map does
// not know leaves the pick in force. A file whose start-up line is missing (a rotated
// piece) names no host: without a pick it is refused as an unknown host.
func observeHost(name string, st *state, opts importer.Options, rep *importer.Report) error {
	if name == "" || len(name) > 253 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n\t") {
		return nil
	}
	if opts.Host != nil {
		if opts.Hosts != nil {
			if hh, ok := opts.Hosts(name); ok && hh.Label != "" && hh.Label != opts.Host.Label {
				return importer.ErrUnknownHost
			}
		}
		return nil
	}
	if err := rep.Observe(name); err != nil {
		return err
	}
	if opts.Hosts == nil {
		return nil
	}
	hh, ok := opts.Hosts(name)
	if !ok || hh.Label == "" {
		return importer.ErrUnknownHost
	}
	if st.hostLabel == "" {
		st.hostLabel = hh.Label
	}
	return nil
}

func slowQuery(ll logLine, t time.Time, no int64, st *state, opts importer.Options) (time.Time, importer.Reason, *evidence.Event, error) {
	f, ok := parseSlowAttr(ll.Attr)
	if !ok {
		return time.Time{}, importer.ReasonFieldCount, nil, nil
	}
	if !f.hasDuration || f.durationMs < 0 || f.durationMs > maxDurationMs || math.IsNaN(f.durationMs) {
		return time.Time{}, importer.ReasonBadNumber, nil, nil
	}
	st.slowSeen = true
	if st.hostLabel == "" {
		return t, "", nil, importer.ErrUnknownHost
	}

	d := f.durationMs
	end := t.UTC()
	start := end.Add(-time.Duration(d * float64(time.Millisecond)))
	attrs := map[string]string{}
	if f.typ != "" {
		attrs["type"] = f.typ
	}
	if f.ns != "" {
		attrs["ns"] = f.ns
	}
	if f.typ == "command" && f.cmd != "" {
		attrs["command"] = f.cmd
	}
	if f.plan != "" {
		attrs["planSummary"] = f.plan
	}
	for k, v := range f.counts {
		attrs[k] = v
	}
	if f.qhash != "" {
		attrs["queryHash"] = f.qhash
	}
	if f.working != "" {
		attrs["workingMs"] = f.working
	}
	ev := evidence.Event{
		EvidenceID: evidence.EventID(opts.CaseID, Name, opts.ImportNonce, no, 0),
		Source:     Name,
		Kind:       KindSlowOp,
		Host:       st.hostLabel,
		Start:      start,
		End:        end,
		DurationMs: &d,
		Unit:       "ms",
		Severity:   severity(ll.S),
		Line:       no,
		TZRule:     clock.RuleOffsetInField,
		Attrs:      attrs,
	}
	if err := ev.Validate(allowedAttrs); err != nil {
		return time.Time{}, importer.ReasonFieldCount, nil, nil
	}
	return t, "", &ev, nil
}

func severity(s string) string {
	switch s {
	case "W":
		return "warning"
	case "E":
		return "error"
	case "F":
		return "critical"
	default:
		return "info"
	}
}

// parseTime reads t.$date. A string with an offset (or Z) is RuleOffsetInField; a naive
// time cannot be placed on the clock and is tz-unlabelled.
func parseTime(raw json.RawMessage) (time.Time, importer.Reason) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || len(s) > 64 {
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
	t = t.UTC()
	if y := t.Year(); y < 2000 || y > 2100 {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
}

type slowFields struct {
	durationMs  float64
	hasDuration bool
	working     string
	counts      map[string]string
	typ, ns     string
	plan, qhash string
	cmd         string
}

var countKeys = map[string]bool{"keysExamined": true, "docsExamined": true, "nreturned": true, "numYields": true}

// parseSlowAttr streams attr once. Every key outside the keep list (command body, filter,
// client, remote, appName, ...) is read into a throwaway buffer and discarded.
func parseSlowAttr(raw json.RawMessage) (slowFields, bool) {
	f := slowFields{counts: map[string]string{}}
	if len(raw) == 0 {
		return f, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return f, false
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return f, false
		}
		key, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return f, false
		}
		switch {
		case key == "durationMillis":
			if n, ok := number(v); ok {
				f.durationMs, f.hasDuration = n, true
			}
		case key == "workingMillis":
			if n, ok := number(v); ok && n >= 0 && n <= maxDurationMs {
				f.working = strconv.FormatFloat(n, 'f', -1, 64)
			}
		case countKeys[key]:
			if n, ok := number(v); ok && n >= 0 && n == math.Trunc(n) && n < 1e15 {
				f.counts[key] = strconv.FormatInt(int64(n), 10)
			}
		case key == "type":
			if s, ok := str(v); ok && reType.MatchString(s) {
				f.typ = s
			}
		case key == "ns":
			if s, ok := str(v); ok && s != "" && len(s) <= 256 {
				f.ns = s
			}
		case key == "planSummary":
			if s, ok := str(v); ok {
				f.plan = stageTokens(s)
			}
		case key == "queryHash":
			if s, ok := str(v); ok && reQHash.MatchString(s) {
				f.qhash = s
			}
		case key == "command":
			f.cmd = firstKey(v)
		}
	}
	if _, err := dec.Token(); err != nil {
		return f, false
	}
	return f, true
}

func number(raw json.RawMessage) (float64, bool) {
	var n json.Number
	if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

func str(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// firstKey returns the first key of a command object (the command name, for example
// "insert"), or "" when it is not an object or the key is not a plain name (a
// $truncated marker, for instance). Nothing else of the object is kept.
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
	if !reCmdName.MatchString(k) {
		return ""
	}
	return k
}

// stageTokens reduces planSummary ("IXSCAN { a: 1 }, COLLSCAN") to its stage names. Key
// patterns and bounds after a stage name are dropped.
func stageTokens(s string) string {
	var toks []string
	depth, start := 0, 0
	flush := func(end int) {
		seg := strings.TrimSpace(s[start:end])
		m := reStage.FindString(seg)
		if m == "" {
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

// finish assembles source quality and fixed-sentence gaps.
func finish(rep *importer.Report, st *state, opts importer.Options) {
	q := evidence.SourceQuality{SourceVersion: st.version}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
	}
	if st.timeSeen {
		q.TimezoneRule = clock.RuleOffsetInField
	}

	rep.Gap("mongod.log lists only operations slower than slowms; faster operations are not in this file")
	if st.version == "" {
		rep.Gap("mongod version unknown: no Build Info line in the file")
	}

	slowms := st.slowms
	if supplied, ok := opts.Supplied["slowms"]; ok {
		if v, err := strconv.ParseInt(strings.TrimSpace(supplied), 10, 64); err == nil && v >= 0 && v <= maxSlowms {
			if slowms == nil {
				slowms = &v
			} else if *slowms != v {
				rep.Gap("owner-supplied slowms differs from the startup options in the file; the file value is used")
			}
		}
	}
	if slowms != nil {
		q.Threshold = fmt.Sprintf("operations slower than %d ms", *slowms)
	} else {
		rep.Gap("slowms unknown: not in the startup options and not supplied by the owner")
	}

	var marks []string
	if st.sampled {
		marks = append(marks, samplingSampled)
	}
	if st.rotated {
		marks = append(marks, samplingRotated)
	}
	if st.restarted {
		marks = append(marks, samplingRestart)
	}
	if st.profilerChange {
		marks = append(marks, samplingChanged)
	}
	q.Sampling = strings.Join(marks, "; ")
	for _, m := range marks {
		rep.Gap(m)
	}
	rep.Quality = q
}
