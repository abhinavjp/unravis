// Package iis imports IIS W3C request logs, HTTP.sys error logs (HTTPERR) and
// classifies WAS application pool events. Parsers are built and tested on
// synthetic files only (importer wave plan section 4).
//
// Drop at parse: client and server IPs, query strings, user agents, cookies and
// referers are never read into any value. Every field not named in a spec's
// keep list is skipped, so an unknown or sensitive column cannot leak.
package iis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

const (
	maxStemBytes     = 256
	ctxCheckEvery    = 256
	maxHostNameBytes = 256
)

// spec describes one fielded W3C-style log format.
type spec struct {
	name      string
	limits    importer.Limits
	keep      []string // lowercased field names the parser reads
	required  []string // subset of keep that must be declared
	zoneRule  string   // clock.Rule* for the date+time pair
	preflight func(opts importer.Options) error
	row       func(c *rowCtx) (evidence.Event, importer.Reason, error)
	gaps      func(st *runState, rep *importer.Report) []string
}

type runState struct {
	rep         *importer.Report
	hostCache   map[string]string
	machines    map[string]struct{} // lower-cased names seen when no host map is wired
	software    string
	first, last time.Time
	declared    map[string]bool // kept columns seen in any #Fields
}

type rowCtx struct {
	vals []string
	col  map[string]int
	line int64
	opts importer.Options
	st   *runState
}

// get returns the cell for a kept column. ok is false when the column is not
// declared, or the cell is empty or the W3C null marker "-".
func (c *rowCtx) get(name string) (string, bool) {
	i, found := c.col[name]
	if !found || i >= len(c.vals) {
		return "", false
	}
	v := c.vals[i]
	if v == "" || v == "-" {
		return "", false
	}
	return v, true
}

// resolveHost maps the name observed inside the file (if any) through
// opts.Hosts, else uses the host the user picked. Raw names reach only
// Report.ObservedHosts, never an Event.
//
// Without a host map the picked host may label one machine only: a second
// distinct name refuses the file (ErrUnknownHost), so several machines are
// never merged under one label. Names are compared without regard to case.
// A name longer than maxHostNameBytes is never used or recorded; its row is
// rejected with ReasonUnknownHost instead of falling back to the picked host.
// A non-empty reason means "reject this row"; a non-nil error aborts the file.
func (c *rowCtx) resolveHost(observed string) (string, importer.Reason, error) {
	if observed != "" {
		if len(observed) > maxHostNameBytes {
			return "", importer.ReasonUnknownHost, nil
		}
		if label, ok := c.st.hostCache[observed]; ok {
			return label, "", nil
		}
		if err := c.st.rep.Observe(observed); err != nil {
			return "", "", err
		}
		if c.opts.Hosts != nil {
			h, ok := c.opts.Hosts(observed)
			if !ok || h.Label == "" {
				return "", "", importer.ErrUnknownHost
			}
			c.st.hostCache[observed] = h.Label
			return h.Label, "", nil
		}
		if c.opts.Host == nil || c.opts.Host.Label == "" {
			return "", "", importer.ErrUnknownHost
		}
		key := strings.ToLower(observed)
		if _, seen := c.st.machines[key]; !seen {
			if len(c.st.machines) > 0 {
				return "", "", importer.ErrUnknownHost
			}
			c.st.machines[key] = struct{}{}
		}
		c.st.hostCache[observed] = c.opts.Host.Label
		return c.opts.Host.Label, "", nil
	}
	if c.opts.Host != nil && c.opts.Host.Label != "" {
		return c.opts.Host.Label, "", nil
	}
	return "", "", importer.ErrUnknownHost
}

// Years outside this range are corrupt dates (0001-01-01, 1601-01-01, 9999),
// not log times. A row with one is rejected as bad-time; it never aborts the
// import.
const (
	minLogYear = 1990
	maxLogYear = 2100
)

func saneLogTime(t time.Time) bool {
	y := t.UTC().Year()
	return y >= minLogYear && y <= maxLogYear
}

func mergeLimits(got, def importer.Limits) importer.Limits {
	if got.MaxBytes <= 0 {
		got.MaxBytes = def.MaxBytes
	}
	if got.MaxRows <= 0 {
		got.MaxRows = def.MaxRows
	}
	if got.MaxLineBytes <= 0 {
		got.MaxLineBytes = def.MaxLineBytes
	}
	if got.MaxFields <= 0 {
		got.MaxFields = def.MaxFields
	}
	if got.MaxRejectFraction <= 0 {
		got.MaxRejectFraction = def.MaxRejectFraction
	}
	return got
}

func quarantine(r importer.Reason, column string) error {
	return &importer.QuarantineError{Reason: r, Column: column}
}

// run is the shared reader loop: directives, #Fields (re-declarable), rows,
// limits, reject fraction and the report.
func run(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error, sp *spec) (importer.Report, error) {

	lim := mergeLimits(opts.Limits, sp.limits)
	rep := importer.Report{Importer: sp.name, FieldRules: map[string]string{"date+time": sp.zoneRule}}
	st := &runState{rep: &rep, hostCache: map[string]string{}, machines: map[string]struct{}{},
		declared: map[string]bool{}}

	fin := func(err error) (importer.Report, error) {
		rep.First, rep.Last = st.first, st.last
		if !st.first.IsZero() {
			f, l := st.first, st.last
			rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
		}
		rep.Quality.TimezoneRule = sp.zoneRule
		if v := softwareVersion(st.software); v != "" {
			rep.Quality.SourceVersion = v
		} else if opts.Host != nil {
			rep.Quality.SourceVersion = opts.Host.Runtime
		}
		if sp.gaps != nil {
			rep.KnownGaps = sp.gaps(st, &rep)
		}
		return rep, err
	}

	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 64*1024)
	head, _ := br.Peek(16)
	if err := importer.Sniff(head); err != nil {
		return fin(err)
	}
	if sp.preflight != nil {
		if err := sp.preflight(opts); err != nil {
			return fin(err)
		}
	}
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return fin(err)
	}

	var (
		col      map[string]int
		width    int
		sawHdr   bool
		sinceChk int
	)
	reject := rep.Reject

	for {
		sinceChk++
		if sinceChk >= ctxCheckEvery {
			sinceChk = 0
			if err := ctx.Err(); err != nil {
				return fin(err)
			}
		}
		line, no, err := lr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var re importer.RowError
			if errors.As(err, &re) {
				// the reader refused one line (bad UTF-8, NUL); it is a row
				rep.Rows++
				if err := lim.CheckRows(rep.Rows); err != nil {
					return fin(err)
				}
				reject(no, re.Reason)
				continue
			}
			return fin(err)
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			if !utf8.Valid(line) {
				continue
			}
			text := string(line)
			switch {
			case hasDirective(text, "#Fields:"):
				c, w, e := declare(text, sp, lim, st)
				if e != nil {
					return fin(e)
				}
				col, width, sawHdr = c, w, true
			case hasDirective(text, "#Software:"):
				if st.software == "" && len(text) <= 256 {
					st.software = strings.TrimSpace(text[len("#Software:"):])
				}
			}
			continue
		}
		rep.Rows++
		if rep.Rows > lim.MaxRows {
			return fin(&importer.LimitError{Limit: "rows", Max: lim.MaxRows, At: rep.Rows})
		}
		if !sawHdr {
			return fin(quarantine(importer.ReasonUnsupportedFormat, ""))
		}
		if !utf8.Valid(line) {
			reject(no, importer.ReasonEncoding)
			continue
		}
		vals := strings.Split(string(line), " ")
		if len(vals) != width {
			reject(no, importer.ReasonFieldCount)
			continue
		}
		c := &rowCtx{vals: vals, col: col, line: no, opts: opts, st: st}
		ev, reason, err := sp.row(c)
		if err != nil {
			return fin(err)
		}
		if reason != "" {
			reject(no, reason)
			continue
		}
		ev.EvidenceID = evidence.EventID(opts.CaseID, sp.name, opts.ImportNonce, no, 0)
		ev.Source, ev.Line, ev.TZRule = sp.name, no, sp.zoneRule
		// coverage spans request starts as well as ends
		if st.first.IsZero() || ev.Start.Before(st.first) {
			st.first = ev.Start
		}
		if ev.End.After(st.last) {
			st.last = ev.End
		}
		if err := emit(ev); err != nil {
			return fin(err)
		}
		rep.Emitted++
	}
	if !sawHdr {
		return fin(quarantine(importer.ReasonUnsupportedFormat, ""))
	}
	if rep.Rows > 0 && float64(rep.Rejected)/float64(rep.Rows) > lim.MaxRejectFraction {
		return fin(quarantine(importer.ReasonRejectFraction, ""))
	}
	return fin(nil)
}

func hasDirective(line, name string) bool {
	return len(line) >= len(name) && strings.EqualFold(line[:len(name)], name)
}

// declare parses a #Fields line. Only the spec's keep list is indexed; the
// header text itself never reaches an error or report.
func declare(text string, sp *spec, lim importer.Limits, st *runState) (map[string]int, int, error) {
	names := strings.Fields(text[len("#Fields:"):])
	if len(names) > lim.MaxFields {
		return nil, 0, quarantine(importer.ReasonFieldCount, "")
	}
	keep := make(map[string]bool, len(sp.keep))
	for _, k := range sp.keep {
		keep[k] = true
	}
	col := map[string]int{}
	for i, n := range names {
		n = strings.ToLower(n)
		if !keep[n] {
			continue
		}
		if _, dup := col[n]; dup {
			return nil, 0, quarantine(importer.ReasonFieldCount, n)
		}
		col[n] = i
		st.declared[n] = true
	}
	for _, req := range sp.required {
		if _, ok := col[req]; !ok {
			return nil, 0, quarantine(importer.ReasonUnsupportedFormat, req)
		}
	}
	return col, len(names), nil
}

// softwareVersion extracts "iis-<major>" from "Microsoft Internet Information
// Services 10.0"; anything else gives "".
func softwareVersion(s string) string {
	const p = "Microsoft Internet Information Services "
	if !strings.HasPrefix(s, p) {
		return ""
	}
	rest := s[len(p):]
	maj, _, _ := strings.Cut(rest, ".")
	if _, ok := parseUint(maj, 99); !ok {
		return ""
	}
	return "iis-" + maj
}

func parseUint(s string, max uint64) (uint64, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > max {
		return 0, false
	}
	return n, true
}

// cleanToken accepts short tokens made of [A-Za-z0-9._+-] only.
func cleanToken(s string, max int) (string, bool) {
	if s == "" || len(s) > max {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		ok := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
			b == '.' || b == '_' || b == '+' || b == '-'
		if !ok {
			return "", false
		}
	}
	return s, true
}

// normaliseStem turns a request path into a low-cardinality route. The query
// and fragment are cut and matrix parameters (";jsessionid=...") are dropped.
// A segment survives only if it looks like a route word (see routeSegment);
// anything else, such as a number, GUID, ObjectId, token, e-mail address or
// encoded text, becomes {id}. The result is at most 256 bytes. Only paths that
// start with "/" are kept.
//
// A plain word in an id position (a user name in /users/jsmith) cannot be told
// from a route word by shape and is kept.
func normaliseStem(s string) (string, bool) {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" || s[0] != '/' {
		return "", false
	}
	segs := strings.Split(s, "/")
	for i, seg := range segs {
		if j := strings.IndexByte(seg, ';'); j >= 0 {
			seg = seg[:j]
		}
		segs[i] = routeSegment(seg)
	}
	out := strings.Join(segs, "/")
	if len(out) > maxStemBytes {
		out = out[:maxStemBytes]
		for !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out, true
}

// staticExt lists the file extensions a route word may carry. Document and
// archive extensions are left out: those names are free text.
var staticExt = map[string]bool{
	"html": true, "htm": true, "aspx": true, "asmx": true, "ashx": true, "svc": true, "axd": true,
	"js": true, "css": true, "json": true, "xml": true, "txt": true, "map": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true, "svg": true, "ico": true, "webp": true,
	"woff": true, "woff2": true, "ttf": true, "eot": true,
}

// routeSegment returns seg when it is a route word and "{id}" otherwise. A
// route word is [A-Za-z0-9_-] up to 40 bytes, optionally followed by one
// allowlisted extension, or a version such as v1 or v1.0. It is not an
// identifier: not all digits, not a GUID, not a hex run of 16 or more, not 32
// bytes or longer, not 20 or more bytes with a digit in them, and without a
// run of 5 digits.
func routeSegment(seg string) string {
	if seg == "" {
		return ""
	}
	if isNumeric(seg) || isGUID(seg) {
		return "{id}"
	}
	if isVersion(seg) {
		return seg
	}
	base := seg
	if i := strings.LastIndexByte(seg, '.'); i >= 0 {
		if !staticExt[strings.ToLower(seg[i+1:])] {
			return "{id}"
		}
		base = seg[:i]
	}
	if base == "" || len(base) > 40 || looksLikeIdentifier(base) {
		return "{id}"
	}
	for i := 0; i < len(base); i++ {
		b := base[i]
		ok := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
		if !ok {
			return "{id}"
		}
	}
	return seg
}

// looksLikeIdentifier reports shapes that carry a record or session id: long
// hex (an ObjectId is 24 hex digits), long tokens, tokens that mix letters and
// digits at 20 bytes or more, and long digit runs.
func looksLikeIdentifier(s string) bool {
	if len(s) >= 32 {
		return true
	}
	hex, digit, run, maxRun := true, false, 0, 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !isHex(b) {
			hex = false
		}
		if b >= '0' && b <= '9' {
			digit = true
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	return hex && len(s) >= 16 || digit && len(s) >= 20 || maxRun >= 5
}

// isVersion matches v1, V2.1 and v1.2.3.
func isVersion(s string) bool {
	if len(s) < 2 || (s[0] != 'v' && s[0] != 'V') {
		return false
	}
	dots, prevDot := 0, true
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			prevDot = false
		case s[i] == '.' && !prevDot && dots < 2:
			dots++
			prevDot = true
		default:
			return false
		}
	}
	return !prevDot && len(s) <= 12
}

func isNumeric(s string) bool {
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

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isGUID(s string) bool {
	if len(s) == 38 && s[0] == '{' && s[37] == '}' {
		s = s[1:37]
	}
	switch len(s) {
	case 32:
		for i := 0; i < 32; i++ {
			if !isHex(s[i]) {
				return false
			}
		}
		return true
	case 36:
		for i := 0; i < 36; i++ {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if s[i] != '-' {
					return false
				}
			} else if !isHex(s[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// headText decodes the Detect head: strips a UTF-8 BOM, decodes UTF-16 BOMs.
func headText(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		return string(head[3:])
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		le := head[0] == 0xFF
		var b strings.Builder
		for i := 2; i+1 < len(head); i += 2 {
			var u uint16
			if le {
				u = uint16(head[i]) | uint16(head[i+1])<<8
			} else {
				u = uint16(head[i])<<8 | uint16(head[i+1])
			}
			if u < 0x80 {
				b.WriteByte(byte(u))
			} else {
				b.WriteByte('?')
			}
		}
		return b.String()
	}
	return string(head)
}

// headerInfo reads the leading directive lines from a Detect head.
func headerInfo(head []byte) (software string, fields map[string]bool) {
	text := headText(head)
	fields = map[string]bool{}
	lines := strings.Split(text, "\n")
	// the last element may be a partial line when the head is cut at 64 KiB
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if !strings.HasPrefix(l, "#") {
			break
		}
		switch {
		case hasDirective(l, "#Software:"):
			software = strings.TrimSpace(l[len("#Software:"):])
		case hasDirective(l, "#Fields:"):
			for _, n := range strings.Fields(l[len("#Fields:"):]) {
				fields[strings.ToLower(n)] = true
			}
		}
	}
	return software, fields
}

func isHTTPERRHeader(software string, fields map[string]bool) bool {
	return strings.Contains(software, "Microsoft HTTP API") || fields["s-reason"] || fields["s-queuename"]
}
