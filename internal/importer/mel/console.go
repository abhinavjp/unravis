// Package mel imports three logs written by Microsoft.Extensions.Logging and
// the ASP.NET Core Module: the simple console format ("mel-console"), the JSON
// console format ("mel-json") and the stdout file the ASP.NET Core Module writes
// for an app hosted in IIS ("ancm-stdout"). All three feed the shared applog
// core (card W2-a), which keeps the level, the logger label, the family, the
// curated exception types and a few numbers, and drops every message.
//
// Formats, as read from public documentation and source (formats marked
// "verify" were not checked against a live log):
//
//   - Simple console: a header line "<level>: <category>[<event id>]" followed by
//     the message indented six spaces. The level is one of trce, dbug, info,
//     warn, fail, crit. With TimestampFormat set, the formatted time comes first
//     on the header line; the formatter has no zone of its own, so a time with no
//     zone mark is read in the host time zone (RuleHostLocal). With SingleLine
//     the message and the exception follow the header on the same line (verify).
//   - JSON console: one object per line with Timestamp (only written when
//     TimestampFormat is set), EventId, LogLevel, Category, Message, Exception
//     and State. A file with no Timestamp on any entry (and no heartbeat line)
//     is refused.
//   - ANCM stdout: the simple console text with no time on each line, in a file
//     named stdout_<yyyyMMddHHmmss>_<pid>.log. The name gives the start of the
//     session; it is read as host-local time (verify). A line "Unhandled
//     exception." starts the text of a crash.
//
// Time: a line with no time is counted and never emitted. The one exception is
// the Kestrel heartbeat sentence, which states its own time ("As of ...", verify
// layout), so heartbeat-late points are placed from that time. A time inside a
// DST fold or gap is rejected. Host-local times with no host zone refuse the file.
//
// Privacy: message text, scopes, stack frames and every property not named above
// are dropped at parse. The process id in an ANCM file name is checked and not
// stored. Nothing from the file reaches an error, a report or a known gap.
package mel

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Importer names.
const (
	NameConsole = "mel-console"
	NameJSON    = "mel-json"
	NameStdout  = "ancm-stdout"
)

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapNoTime = "Lines with no time are counted but not placed on the timeline; " +
		"only Kestrel heartbeat lines are placed, from the time they state."
	GapHostLocal = "Timestamps with no zone are read in the host time zone chosen by the user; " +
		"a log written with UseUtcTimestamp and a format with no zone mark needs the host zone set to UTC."
	GapHeartbeat = "The Kestrel heartbeat event number and the layout of its time were not checked against a live log; " +
		"a heartbeat line whose time cannot be read is rejected."
	GapSingleLine = "SingleLine entries are read as the message followed by the exception on one line; " +
		"the layout was not checked against a live log."
	GapStdoutTime = "The start time in the file name is read in the host time zone; " +
		"that the file name uses host-local time was not verified."
	GapStdoutCrash = "Only the first Unhandled exception block of a stdout file is used; later blocks are counted as rows."
)

// Size caps for one entry: the text kept is only used to find a sentence and
// the exception types.
const (
	maxMsgBytes   = 8 << 10
	maxExcBytes   = 64 << 10
	maxCrashBytes = 64 << 10
	checkEvery    = 256
	minIndent     = 6
	kestrelPrefix = "Microsoft.AspNetCore.Server.Kestrel"
	unhandledText = "Unhandled exception. "
)

func defaultLimits(lineBytes int) importer.Limits {
	return importer.Limits{
		MaxBytes: 512 << 20, MaxRows: 10_000_000, MaxEvents: 5_000_000,
		MaxLineBytes: lineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// ---- time ------------------------------------------------------------------

var zonedLayouts = []string{
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05 Z07:00",
	"2006-01-02T15:04:05Z0700",
	"2006-01-02 15:04:05Z0700",
	"2006-01-02 15:04:05 Z0700",
	"01/02/2006 15:04:05 Z07:00",
}

var localLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"01/02/2006 15:04:05",
	"2006/01/02 15:04:05",
	"01/02/2006 3:04:05 PM",
	"1/2/2006 3:04:05 PM",
}

// parseStamp reads a timestamp the logging formatters write. A value with an
// offset (or a "Z") carries its own zone; one without is host-local and needs
// loc. The reason is "" when t is good. A value that is not a date and time is
// ReasonBadTime; local time with no zone is ReasonTZUnlabelled.
func parseStamp(p string, loc *time.Location, dates *applog.DateOrder) (t time.Time, rule string, reason importer.Reason) {
	if len(p) < 8 || len(p) > 48 {
		return time.Time{}, "", importer.ReasonBadTime
	}
	p, ok := applog.NormalizeSlashDate(p, dates)
	if !ok {
		return time.Time{}, "", importer.ReasonBadTime
	}
	for _, l := range zonedLayouts {
		if v, err := time.Parse(l, p); err == nil {
			if !wincsv.SaneTime(v) {
				return time.Time{}, "", importer.ReasonBadTime
			}
			if strings.HasSuffix(p, "Z") {
				return v.UTC(), clock.RuleUTCField, ""
			}
			return v.UTC(), clock.RuleOffsetInField, ""
		}
	}
	for _, l := range localLayouts {
		v, err := time.Parse(l, p)
		if err != nil {
			continue
		}
		if !wincsv.SaneTime(v) {
			return time.Time{}, "", importer.ReasonBadTime
		}
		u, why := applog.LocalTime(l, p, loc)
		if why != "" {
			return time.Time{}, "", why
		}
		return u, clock.RuleHostLocal, ""
	}
	return time.Time{}, "", importer.ReasonBadTime
}

// ---- header ----------------------------------------------------------------

var headerRe = regexp.MustCompile(`^(?:(.{1,80}?)[\s\]])?(trce|dbug|info|warn|fail|crit): (\S+?)\[(-?\d{1,9})\](?:\s+(.*))?$`)

var timeOfDayRe = regexp.MustCompile(`^\d{1,2}:\d{2}(?::\d{2})?(?:[.,]\d{1,7})?(?:\s?[AaPp][Mm])?$`)

// excStartRe finds an exception type that begins the exception text of a
// SingleLine entry.
var excStartRe = regexp.MustCompile(`(?:^|\s)((?:[A-Za-z_][A-Za-z0-9_]*\.)+[A-Za-z0-9_]*(?:Exception|Error))(?::|\s\()`)

// excLineRe finds a line of a multi-line entry that starts an exception.
var excLineRe = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*\.)+[A-Za-z0-9_]*(?:Exception|Error)(?::|\s\(|$)`)

type header struct {
	prefix, level, category, rest string
	id                            int
}

// parseHeader reads a header line. A line that starts with a space is never a
// header.
func parseHeader(line []byte) (header, bool) {
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
		return header{}, false
	}
	m := headerRe.FindSubmatch(line)
	if m == nil {
		return header{}, false
	}
	id, err := strconv.Atoi(string(m[4]))
	if err != nil {
		return header{}, false
	}
	return header{
		prefix: strings.Trim(strings.TrimSpace(string(m[1])), "[] \t"),
		level:  string(m[2]), category: string(m[3]), id: id,
		rest: strings.TrimSpace(string(m[5])),
	}, true
}

// prefix kinds
const (
	prefixNone = iota // nothing, or a time of day: the line has no time
	prefixDate        // a date and time (parsed or not)
	prefixText        // anything else: the line is not a header
)

func prefixKind(p string) int {
	switch {
	case p == "" || timeOfDayRe.MatchString(p):
		return prefixNone
	case p[0] >= '0' && p[0] <= '9':
		return prefixDate
	}
	return prefixText
}

// ---- scanner ---------------------------------------------------------------

// entry is the log entry being built.
type entry struct {
	open, dead, timed, excStarted bool
	line                          int64
	rec                           applog.Record
	msg, exc                      []byte
}

// scan frames the simple console text of mel-console and ancm-stdout.
type scan struct {
	ctx  context.Context
	opts importer.Options
	acc  *wincsv.Acc
	sink *applog.Sink
	loc  *time.Location
	// stdout turns on the "Unhandled exception." block.
	stdout bool

	n        int64
	cur      entry
	headers  int64 // header lines read
	dated    int64 // headers with a date and time in front, parsed or not
	untimed  int64 // headers and stray lines with no time and no heartbeat time
	beats    int64 // untimed heartbeat lines placed from their own time
	rule     string
	hostTime bool
	dates    *applog.DateOrder // the order of day and month in slash dates

	crashSeen, crashOpen bool
	crashKeep            bool
	crashLine            int64
	crashText            []byte
}

func newScan(ctx context.Context, opts importer.Options, acc *wincsv.Acc, sink *applog.Sink, stdout bool) *scan {
	return &scan{ctx: ctx, opts: opts, acc: acc, sink: sink, loc: opts.Host.TZ, stdout: stdout,
		dates: applog.NewDateOrder(opts.Supplied[applog.SuppliedDateOrder])}
}

// run reads every line of lr.
func (s *scan) run(lr *textio.LineReader) error {
	for {
		line, no, err := lr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return err
			}
			if e := s.acc.Row(no); e != nil {
				return e
			}
			s.sink.Reject(no, reason)
			continue
		}
		s.n++
		if s.n%checkEvery == 0 {
			if e := s.ctx.Err(); e != nil {
				return e
			}
		}
		if err := s.handle(line, no); err != nil {
			return err
		}
	}
}

func (s *scan) handle(line []byte, no int64) error {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	if line[0] != ' ' && line[0] != '\t' {
		if h, ok := parseHeader(line); ok && prefixKind(h.prefix) != prefixText {
			return s.header(h, no)
		}
		if s.stdout && bytes.HasPrefix(line, []byte(unhandledText)) {
			return s.crashStart(line, no)
		}
	}
	return s.continuation(line, no)
}

func (s *scan) header(h header, no int64) error {
	if err := s.acc.Row(no); err != nil {
		return err
	}
	if err := s.flush(); err != nil {
		return err
	}
	s.crashOpen = false
	s.headers++
	msg, exc := s.cur.msg[:0], s.cur.exc[:0]
	s.cur = entry{open: true, line: no, msg: msg, exc: exc,
		rec: applog.Record{Level: h.level, Category: h.category, EventID: h.id}}
	if prefixKind(h.prefix) == prefixDate {
		s.dated++
		t, rule, why := parseStamp(h.prefix, s.loc, s.dates)
		switch why {
		case "":
			s.cur.timed, s.cur.rec.Time, s.cur.rec.Rule = true, t, rule
			s.noteRule(rule)
		case importer.ReasonTZUnlabelled:
			return wincsv.TZUnlabelled()
		default:
			s.cur.dead = true
			s.sink.Reject(no, why)
		}
	}
	if h.rest != "" && !s.cur.dead {
		s.single(h.rest)
	}
	return nil
}

func (s *scan) noteRule(rule string) {
	if s.rule == "" {
		s.rule = rule
	}
	if rule == clock.RuleHostLocal {
		s.hostTime = true
	}
}

// single splits the text after a SingleLine header into the message and the
// exception.
func (s *scan) single(rest string) {
	msg := rest
	if loc := excStartRe.FindStringSubmatchIndex(rest); loc != nil {
		msg = strings.TrimSpace(rest[:loc[2]])
		s.cur.exc = appendCapped(s.cur.exc, []byte(rest[loc[2]:]), maxExcBytes)
		s.cur.excStarted = true
		s.acc.Gap(GapSingleLine)
	}
	s.cur.msg = appendCapped(s.cur.msg, []byte(msg), maxMsgBytes)
}

// appendCapped appends text and a line break, keeping the buffer under max.
func appendCapped(buf, text []byte, max int) []byte {
	if len(buf)+len(text)+1 > max {
		return buf
	}
	buf = append(buf, text...)
	return append(buf, '\n')
}

func indentOf(line []byte) int {
	n := 0
	for _, c := range line {
		switch c {
		case ' ':
			n++
		case '\t':
			n += minIndent
		default:
			return n
		}
	}
	return n
}

func (s *scan) continuation(line []byte, no int64) error {
	if s.crashOpen {
		if !s.crashKeep {
			return nil
		}
		s.crashText = appendCapped(s.crashText, bytes.TrimRight(line, " \t"), maxCrashBytes)
		return nil
	}
	if !s.cur.open {
		// A line before any header: counted, not rejected, not placed.
		if err := s.acc.Row(no); err != nil {
			return err
		}
		s.untimed++
		return nil
	}
	if s.cur.dead {
		return nil
	}
	t := bytes.TrimSpace(line)
	if bytes.HasPrefix(t, []byte("=> ")) {
		return nil // a scope line
	}
	if indentOf(line) >= minIndent {
		if s.cur.excStarted || excLineRe.Match(t) {
			s.cur.excStarted = true
			s.cur.exc = appendCapped(s.cur.exc, t, maxExcBytes)
		} else {
			s.cur.msg = appendCapped(s.cur.msg, t, maxMsgBytes)
		}
		return nil
	}
	s.cur.exc = appendCapped(s.cur.exc, t, maxExcBytes)
	return nil
}

func (s *scan) crashStart(line []byte, no int64) error {
	if err := s.acc.Row(no); err != nil {
		return err
	}
	if err := s.flush(); err != nil {
		return err
	}
	s.crashOpen = true
	s.crashKeep = !s.crashSeen
	if !s.crashSeen {
		s.crashSeen, s.crashLine = true, no
		s.crashText = appendCapped(s.crashText[:0], bytes.TrimRight(line, " \t"), maxCrashBytes)
	} else {
		s.acc.Gap(GapStdoutCrash)
	}
	return nil
}

// flush hands the open entry to the sink.
func (s *scan) flush() error {
	e := &s.cur
	if !e.open {
		return nil
	}
	if e.dead {
		e.open = false
		return nil
	}
	e.rec.Message = strings.TrimSpace(string(e.msg))
	e.rec.Exception = string(e.exc)
	return s.flushRec()
}

// finish flushes the last entry.
func (s *scan) finish() error {
	if err := s.flush(); err != nil {
		return err
	}
	if s.untimed > 0 {
		s.acc.Gap(GapNoTime)
	}
	if s.hostTime {
		s.acc.Gap(GapHostLocal)
	}
	s.dates.Gaps(s.acc.Gap)
	return nil
}

// ---- console ---------------------------------------------------------------

type consoleImp struct{}

// NewConsole returns the importer for the simple console format.
func NewConsole() importer.Importer { return &consoleImp{} }

func (*consoleImp) Name() string { return NameConsole }

func (*consoleImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*consoleImp) DefaultLimits() importer.Limits { return defaultLimits(wincsv.DefaultMaxLineBytes) }

// detectHead reads the first lines of a head. It reports the best header seen:
// dated, untimed, or none, and whether a heartbeat "As of" line was seen.
type headInfo struct {
	dated, untimed, beat, crash bool
}

func readHead(head []byte) headInfo {
	var hi headInfo
	text := wincsv.HeadText(head)
	if len(text) > 8192 {
		text = text[:8192]
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		lines = lines[:len(lines)-1] // the last line may be cut
	}
	for i, l := range lines {
		if i > 64 {
			break
		}
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, unhandledText) {
			hi.crash = true
			continue
		}
		if strings.Contains(l, `As of "`) && strings.Contains(l, "the heartbeat has been running") {
			hi.beat = true
		}
		h, ok := parseHeader([]byte(l))
		if !ok {
			continue
		}
		switch prefixKind(h.prefix) {
		case prefixDate:
			if _, _, why := parseStamp(h.prefix, time.UTC, nil); why == "" {
				hi.dated = true
			}
		case prefixNone:
			hi.untimed = true
		}
	}
	return hi
}

// Detect scores a console file with a dated header at 85 (40 when the file name
// is an ANCM stdout name), an untimed one with a heartbeat line at 20, else 0.
func (*consoleImp) Detect(head []byte, fileName string) int {
	hi := readHead(head)
	named := stdoutNameRe.MatchString(baseName(fileName))
	switch {
	case hi.dated && named:
		return 40
	case hi.dated:
		return 85
	case hi.untimed && hi.beat:
		return 20
	}
	return 0
}

func (m *consoleImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(NameConsole, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	opts.Limits = lim
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	sink := applog.NewSink(NameConsole, opts, emit)
	s := newScan(ctx, opts, acc, sink, false)
	if err := s.run(lr); err != nil {
		return acc.Fail(err)
	}
	if err := s.finish(); err != nil {
		return acc.Fail(err)
	}
	if s.dated == 0 && s.beats == 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if err := sink.Finish(&acc.Rep); err != nil {
		return acc.Fail(err)
	}
	setRule(acc, s.rule, opts)
	return acc.Finish()
}

// setRule records the time rule the file used.
func setRule(acc *wincsv.Acc, rule string, opts importer.Options) {
	acc.Rep.Quality.SourceVersion = opts.Host.Runtime
	if rule == "" {
		return
	}
	acc.Rep.Quality.TimezoneRule = rule
	acc.Rep.FieldRules["timestamp"] = rule
}

// ---- shared ----------------------------------------------------------------

var stdoutNameRe = regexp.MustCompile(`^stdout_(\d{14})_(\d{1,10})\.log$`)

// baseName is the part of a file name after the last / or \.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}
