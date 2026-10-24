package redis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// The redis-log importer reads redis.log (wave 2 card W2-i). A line is
//
//	<pid>:<role> DD Mon YYYY HH:MM:SS.mmm <level> <message>     (Redis 3 and later)
//	[<pid>] DD Mon HH:MM:SS.mmm <level> <message>               (Redis 2.x and the Windows port; no year)
//
// where <level> is one of . - * #. The time is the host's local time with no zone, so the
// host the user picked must carry a time zone (RuleHostLocal). The year-less form needs the
// owner-stated "year" supplied setting.
//
// The message text is classified in memory against a fixed prefix table and then dropped.
// What is kept is the kind, the process role (from the header letter) and the family. The
// message holds client addresses, client names, file paths and keys, none of which is read
// into an event. A line that matches no table row is counted in Report.Rows (it is not
// Emitted and not Rejected) and the report carries one fixed sentence about it.
//
// The message prefixes below come from the public Redis source (server.c, rdb.c, aof.c,
// replication.c, networking.c) as remembered at the time of writing and are NOT checked
// against every Redis version: treat them as "verify". Parsing is lenient: matching is
// case-insensitive on a prefix, and a line that matches nothing is counted, never refused.

// Name of the importer, its kinds beyond KindRedisStart (commands.go), and its limits.
const (
	NameLog = "redis-log"

	KindRedisStop          = "redis-stop"
	KindBgsaveStart        = "bgsave-start"
	KindBgsaveDone         = "bgsave-done"
	KindBgsaveFail         = "bgsave-fail"
	KindAOFRewriteStart    = "aof-rewrite-start"
	KindAOFRewriteDone     = "aof-rewrite-done"
	KindAOFFsyncSlow       = "aof-fsync-slow"
	KindClientBufferClosed = "client-buffer-closed"
	KindReplicationEvent   = "replication-event"
	KindConfigWarning      = "config-warning"

	logYearKey = "year"

	logMaxBytes = 512 << 20
	logMaxRows  = 5_000_000
	logMaxLine  = 64 << 10

	logMatchBytes = 200 // how much of a message is lower-cased and compared
	logDoneWindow = 10 * time.Second
	logRollover   = 31 * 24 * time.Hour
	logDetectRows = 80
)

// ErrYearNotStated is joined to the refusal of a year-less redis.log with no owner year.
var ErrYearNotStated = errors.New("log year not stated")

const (
	gapLogLocal     = "Redis log times have no time zone; the time zone of the picked host was used"
	gapLogLevel     = "Redis writes only lines at or above its loglevel setting; lines below it are not in this file"
	gapLogKinds     = "Only start, stop, save, rewrite, replication and warning lines from a fixed list are shown; message text is not kept"
	gapLogUnmatched = "Some lines matched no known message; they were counted and not shown"
	gapLogNoYear    = "Lines have no year; the owner-stated year was used for the first line and advanced when the month wrapped"
	gapLogRolled    = "The month wrapped in a log without a year; later lines were placed in the next year"
	gapLogMerged    = "Several log lines for one start, stop, save or rewrite are shown as one event"
)

var logAllowed = map[string]bool{attrRole: true, attrFamily: true}

// attrFamily is the redis-log family attribute (export policy: redisLogFamilies).
const attrFamily = "family"

// logRoles maps the header letter to the export enum (redisRoles).
var logRoles = map[byte]string{'M': "master", 'S': "replica", 'C': "child", 'X': "sentinel"}

var logMonths = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

// logRule is one row of the prefix table. prefix is lower case and compared with the start
// of the lower-cased message; has, when set, must also occur in it.
type logRule struct {
	prefix, has  string
	kind, family string
	guard        int  // dedupe group, see logGuard*
	opens        bool // the banner line of a start: a "Ready" line from the same pid is its pair
}

const (
	logGuardNone = iota
	logGuardStart
	logGuardStop
	logGuardRDBStart
	logGuardRDBDone
	logGuardAOFStart
	logGuardAOFDone
)

// logRules is checked in order; the first match wins. Families are the export enum values
// (redisLogFamilies); "other" is a known kind with no finer family.
var logRules = []logRule{
	// Start. The banner line and the "Ready" line of one start are shown once.
	{prefix: "oo0ooo0ooo0oo redis is starting", kind: KindRedisStart, family: "other", guard: logGuardStart, opens: true},
	{prefix: "ready to accept connections", kind: KindRedisStart, family: "other", guard: logGuardStart},
	{prefix: "server started, redis version", kind: KindRedisStart, family: "other", guard: logGuardStart},
	// Stop. Several lines are written during one shutdown.
	{prefix: "user requested shutdown", kind: KindRedisStop, family: "other", guard: logGuardStop},
	{prefix: "received sigterm", kind: KindRedisStop, family: "other", guard: logGuardStop},
	{prefix: "received sigint", kind: KindRedisStop, family: "other", guard: logGuardStop},
	{prefix: "sigterm received", kind: KindRedisStop, family: "other", guard: logGuardStop},
	{prefix: "sigint received", kind: KindRedisStop, family: "other", guard: logGuardStop},
	{prefix: "redis is now ready to exit", kind: KindRedisStop, family: "other", guard: logGuardStop},
	// RDB save.
	{prefix: "background saving started", kind: KindBgsaveStart, family: "rdb", guard: logGuardRDBStart},
	{prefix: "background rdb transfer started", kind: KindBgsaveStart, family: "full-sync", guard: logGuardRDBStart},
	{prefix: "background saving terminated with success", kind: KindBgsaveDone, family: "rdb", guard: logGuardRDBDone},
	{prefix: "db saved on disk", kind: KindBgsaveDone, family: "rdb", guard: logGuardRDBDone},
	{prefix: "background rdb transfer terminated with success", kind: KindBgsaveDone, family: "full-sync", guard: logGuardRDBDone},
	{prefix: "background saving error", kind: KindBgsaveFail, family: "rdb"},
	{prefix: "background saving terminated by signal", kind: KindBgsaveFail, family: "rdb"},
	{prefix: "background rdb transfer terminated with error", kind: KindBgsaveFail, family: "full-sync"},
	{prefix: "background rdb transfer terminated by signal", kind: KindBgsaveFail, family: "full-sync"},
	{prefix: "can't save in background: fork", kind: KindBgsaveFail, family: "fork"},
	{prefix: "failed opening the rdb file", kind: KindBgsaveFail, family: "rdb"},
	{prefix: "write error saving db on disk", kind: KindBgsaveFail, family: "rdb"},
	{prefix: "error moving temp db file", kind: KindBgsaveFail, family: "rdb"},
	// AOF rewrite.
	{prefix: "background append only file rewriting started", kind: KindAOFRewriteStart, family: "aof", guard: logGuardAOFStart},
	{prefix: "background aof rewrite started", kind: KindAOFRewriteStart, family: "aof", guard: logGuardAOFStart},
	{prefix: "background aof rewrite terminated with success", kind: KindAOFRewriteDone, family: "aof", guard: logGuardAOFDone},
	{prefix: "background aof rewrite finished successfully", kind: KindAOFRewriteDone, family: "aof", guard: logGuardAOFDone},
	{prefix: "sync append only file rewrite performed", kind: KindAOFRewriteDone, family: "aof", guard: logGuardAOFDone},
	{prefix: "background aof rewrite terminated with error", kind: KindBgsaveFail, family: "aof"},
	{prefix: "background aof rewrite terminated by signal", kind: KindBgsaveFail, family: "aof"},
	{prefix: "can't rewrite append only file in background: fork", kind: KindBgsaveFail, family: "fork"},
	{prefix: "write error writing append only file on disk", kind: KindBgsaveFail, family: "aof"},
	{prefix: "asynchronous aof fsync is taking too long", kind: KindAOFFsyncSlow, family: "aof"},
	// Client output buffer. The client text after the prefix is never read.
	{prefix: "client ", has: "overcoming of output buffer limits", kind: KindClientBufferClosed, family: "output-buffer-limit"},
	{prefix: "closing client that reached max query buffer length", kind: KindClientBufferClosed, family: "other"},
	// Replication.
	{prefix: "master <-> replica sync: finished with success", kind: KindReplicationEvent, family: "master-link-up"},
	{prefix: "master <-> slave sync: finished with success", kind: KindReplicationEvent, family: "master-link-up"},
	{prefix: "master <-> replica sync started", kind: KindReplicationEvent, family: "other"},
	{prefix: "master <-> slave sync started", kind: KindReplicationEvent, family: "other"},
	{prefix: "master <-> replica sync", kind: KindReplicationEvent, family: "full-sync"},
	{prefix: "master <-> slave sync", kind: KindReplicationEvent, family: "full-sync"},
	{prefix: "connection with master lost", kind: KindReplicationEvent, family: "master-link-down"},
	{prefix: "master timeout", kind: KindReplicationEvent, family: "master-link-down"},
	{prefix: "connection with replica", has: "lost", kind: KindReplicationEvent, family: "replica-lost"},
	{prefix: "connection with slave", has: "lost", kind: KindReplicationEvent, family: "replica-lost"},
	{prefix: "disconnecting timedout replica", kind: KindReplicationEvent, family: "replica-lost"},
	{prefix: "disconnecting timedout slave", kind: KindReplicationEvent, family: "replica-lost"},
	{prefix: "synchronization with replica", has: "succeeded", kind: KindReplicationEvent, family: "replica-connected"},
	{prefix: "synchronization with slave", has: "succeeded", kind: KindReplicationEvent, family: "replica-connected"},
	{prefix: "full resync", kind: KindReplicationEvent, family: "full-sync"},
	{prefix: "starting bgsave for sync", kind: KindReplicationEvent, family: "full-sync"},
	{prefix: "partial resynchronization not possible", kind: KindReplicationEvent, family: "full-sync"},
	{prefix: "successful partial resynchronization", kind: KindReplicationEvent, family: "partial-sync"},
	{prefix: "partial resynchronization request", kind: KindReplicationEvent, family: "partial-sync"},
	{prefix: "trying a partial resynchronization", kind: KindReplicationEvent, family: "partial-sync"},
	{prefix: "connecting to master", kind: KindReplicationEvent, family: "other"},
	// Startup warnings.
	{prefix: "warning overcommit_memory", kind: KindConfigWarning, family: "overcommit-memory"},
	{prefix: "warning memory overcommit", kind: KindConfigWarning, family: "overcommit-memory"},
	{prefix: "warning you have transparent huge pages", kind: KindConfigWarning, family: "transparent-huge-pages"},
	{prefix: "warning: the tcp backlog", kind: KindConfigWarning, family: "tcp-backlog"},
	{prefix: "warning the tcp backlog", kind: KindConfigWarning, family: "tcp-backlog"},
	{prefix: "server can't set maximum open files", kind: KindConfigWarning, family: "maxclients"},
	{prefix: "you requested maxclients of", kind: KindConfigWarning, family: "maxclients"},
	{prefix: "warning: no config file specified", kind: KindConfigWarning, family: "no-config-file"},
	{prefix: "warning no config file specified", kind: KindConfigWarning, family: "no-config-file"},
}

// classifyLog returns the table row for a message, or nil. Only the first logMatchBytes of
// the message are looked at; the message is not kept.
func classifyLog(msg []byte) *logRule {
	if len(msg) > logMatchBytes {
		msg = msg[:logMatchBytes]
	}
	low := string(bytes.ToLower(bytes.TrimSpace(msg)))
	for i := range logRules {
		r := &logRules[i]
		if strings.HasPrefix(low, r.prefix) && (r.has == "" || strings.Contains(low, r.has)) {
			return r
		}
	}
	return nil
}

// logHeader is a parsed line header. The message is the rest of the line.
type logHeader struct {
	pid      int64
	role     byte // 0 when the line has none
	day      string
	mon      string
	year     int // 0 for the year-less form
	hms      string
	level    byte
	msg      []byte
	yearless bool
}

func isDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cutSpace splits b at the first space.
func cutSpace(b []byte) (head, rest []byte, ok bool) {
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		return b, nil, false
	}
	return b[:i], b[i+1:], true
}

// parseClock reads HH:MM:SS with an optional .mmm and returns it as HH:MM:SS.mmm. It checks
// the shape only; the values are checked when the time is built.
func parseClock(b []byte) (string, bool) {
	if len(b) != 8 && len(b) != 12 {
		return "", false
	}
	if b[2] != ':' || b[5] != ':' || !isDigits(b[0:2]) || !isDigits(b[3:5]) || !isDigits(b[6:8]) {
		return "", false
	}
	if len(b) == 12 {
		if b[8] != '.' || !isDigits(b[9:12]) {
			return "", false
		}
		return string(b), true
	}
	return string(b) + ".000", true
}

// parseLogHeader reads the header of one line. ok is false when the line does not have the
// shape of a redis.log line at all; a line with the shape but a bad value is ok here and
// is rejected later, when its time is built.
func parseLogHeader(line []byte) (h logHeader, ok bool) {
	line = bytes.TrimRight(line, "\r")
	if len(line) < 20 {
		return h, false
	}
	var tok, rest []byte
	var more bool
	if line[0] == '[' {
		end := bytes.IndexByte(line, ']')
		if end < 2 || end > 12 || !isDigits(line[1:end]) || end+1 >= len(line) || line[end+1] != ' ' {
			return h, false
		}
		h.pid, _ = strconv.ParseInt(string(line[1:end]), 10, 64)
		h.yearless = true
		rest = line[end+2:]
	} else {
		colon := bytes.IndexByte(line, ':')
		if colon < 1 || colon > 10 || !isDigits(line[:colon]) || colon+2 >= len(line) || line[colon+2] != ' ' {
			return h, false
		}
		h.pid, _ = strconv.ParseInt(string(line[:colon]), 10, 64)
		if r := line[colon+1]; r >= 'A' && r <= 'Z' {
			h.role = r
		} else {
			return h, false
		}
		rest = line[colon+3:]
	}
	if tok, rest, more = cutSpace(rest); !more || len(tok) < 1 || len(tok) > 2 || !isDigits(tok) {
		return h, false
	}
	h.day = string(tok)
	if tok, rest, more = cutSpace(rest); !more || len(tok) != 3 {
		return h, false
	}
	for _, c := range tok {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return h, false
		}
	}
	h.mon = string(tok)
	if !h.yearless {
		if tok, rest, more = cutSpace(rest); !more || len(tok) != 4 || !isDigits(tok) {
			return h, false
		}
		h.year, _ = strconv.Atoi(string(tok))
	}
	if tok, rest, more = cutSpace(rest); !more {
		// A header with no message: the level is the last token.
		tok = rest
		rest = nil
	}
	var okc bool
	if h.hms, okc = parseClock(tok); !okc {
		return h, false
	}
	if len(rest) == 0 {
		return h, false
	}
	switch rest[0] {
	case '.', '-', '*', '#':
		h.level = rest[0]
	default:
		return h, false
	}
	if len(rest) == 1 {
		h.msg = nil
	} else if rest[1] == ' ' {
		h.msg = rest[2:]
	} else {
		return h, false
	}
	return h, true
}

// Log implements importer.Importer for redis.log.
type Log struct{}

// NewLog returns the redis-log importer.
func NewLog() importer.Importer { return Log{} }

func (Log) Name() string                  { return NameLog }
func (Log) AllowedAttrs() map[string]bool { return copyAttrs(logAllowed) }

func (Log) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: logMaxBytes, MaxRows: logMaxRows, MaxLineBytes: logMaxLine,
		MaxFields: importer.DefaultMaxFields, MaxRejectFraction: rejectFrac,
	}
}

// SuppliedKeys declares the year the owner states for a year-less log.
func (Log) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		logYearKey: {Kind: importer.SuppliedNumber, Min: 2000, Max: 2100, Integer: true},
	}
}

// Detect recognises the header of redis.log lines in the first lines of the file. A
// recognised message raises the score; a file name with "redis" raises it a little more.
func (Log) Detect(head []byte, fileName string) int {
	if importer.Sniff(head) != nil {
		return 0
	}
	text := headText(head)
	if text == nil {
		return 0
	}
	headers, known, yearless := 0, 0, false
	for i := 0; i < logDetectRows && len(text) > 0; i++ {
		var line []byte
		if j := bytes.IndexByte(text, '\n'); j >= 0 {
			line, text = text[:j], text[j+1:]
		} else {
			line, text = text, nil
		}
		h, ok := parseLogHeader(line)
		if !ok {
			continue
		}
		headers++
		yearless = yearless || h.yearless
		if classifyLog(h.msg) != nil {
			known++
		}
	}
	if headers == 0 {
		return 0
	}
	score := 80
	if yearless {
		score = 65
	}
	if known > 0 {
		score += 10
	}
	if strings.Contains(strings.ToLower(fileName), "redis") {
		score += 5
	}
	if score > 100 {
		score = 100
	}
	return score
}

type logGuard struct {
	armed bool
	last  time.Time
}

type logState struct {
	rep  *importer.Report
	opts importer.Options
	lim  importer.Limits
	sink *sink
	host string
	loc  *time.Location
	year int // owner-stated year; 0 when none

	headers, unmatched int64
	yearlessSeen       bool
	prev               time.Time // UTC of the last year-less line, for the rollover
	curYear            int

	startOpen, stopOpen bool
	startPid, stopPid   int64
	rdbStart, rdbDone   logGuard
	aofStart, aofDone   logGuard
	merged              bool
}

// Parse reads the lines and emits one event per matched message.
func (Log) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := capLimits(opts.Limits, Log{}.DefaultLimits())
	rep := importer.Report{Importer: NameLog}
	rep.Rule("timestamp", clock.RuleHostLocal)
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	host, err := hostLabel(opts)
	if err != nil {
		return rep, err
	}
	if opts.Host.TZ == nil {
		return rep, errors.Join(&importer.QuarantineError{Reason: importer.ReasonTZUnlabelled}, importer.ErrTimezoneUnlabelled)
	}
	st := &logState{
		rep: &rep, opts: opts, lim: lim, host: host, loc: opts.Host.TZ,
		sink:    &sink{opts: opts, rep: &rep, emit: emit, allowed: logAllowed},
		rdbDone: logGuard{armed: true}, aofDone: logGuard{armed: true},
	}
	if v, ok := suppliedInt(opts.Supplied[logYearKey], 2100); ok && v >= 2000 {
		st.year = int(v)
	}
	if err := readLines(ctx, r, lim, &rep, st.line); err != nil {
		return rep, err
	}
	if err := finishRead(&rep, lim); err != nil {
		return rep, err
	}
	if st.headers == 0 {
		return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	st.finish()
	return rep, nil
}

// buildTime turns the header into UTC. year is the year to use for a year-less line.
func (st *logState) buildTime(h logHeader, year int) (clock.LocalResult, error) {
	if year < 2000 || year > 2100 {
		return clock.LocalResult{}, clock.ErrBadTime
	}
	value := h.day + " " + h.mon + " " + strconv.Itoa(year) + " " + h.hms
	if _, ok := logMonths[strings.ToLower(h.mon)]; !ok {
		return clock.LocalResult{}, clock.ErrBadTime
	}
	return clock.ParseLocal("2 Jan 2006 15:04:05.000", value, st.loc)
}

func (st *logState) line(raw []byte, no int64) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	rep := st.rep
	rep.Rows++
	if err := st.lim.CheckRows(rep.Rows); err != nil {
		return err
	}
	h, ok := parseLogHeader(raw)
	if !ok {
		st.unmatched++
		return nil
	}
	st.headers++
	year := h.year
	if h.yearless {
		if st.year == 0 {
			return errors.Join(&importer.QuarantineError{Reason: importer.ReasonBadTime}, ErrYearNotStated)
		}
		if !st.yearlessSeen {
			st.yearlessSeen = true
			st.curYear = st.year
		}
		year = st.curYear
	}
	res, err := st.buildTime(h, year)
	if err != nil {
		rep.Reject(no, importer.ReasonBadTime)
		return nil
	}
	if h.yearless {
		if !st.prev.IsZero() && res.UTC.Before(st.prev.Add(-logRollover)) && st.curYear < 2100 {
			st.curYear++
			if res2, err2 := st.buildTime(h, st.curYear); err2 == nil {
				res = res2
				rep.Gap(gapLogRolled)
			} else {
				st.curYear--
				rep.Reject(no, importer.ReasonBadTime)
				return nil
			}
		}
		st.prev = res.UTC
	}
	rule := classifyLog(h.msg)
	if rule == nil {
		st.unmatched++
		return nil
	}
	if res.Ambiguous {
		rep.Reject(no, importer.ReasonDSTAmbiguous)
		return nil
	}
	if res.Nonexistent {
		rep.Reject(no, importer.ReasonDSTNonexistent)
		return nil
	}
	at := res.UTC
	rep.Cover(at)
	if st.duplicate(rule, h, at) {
		st.merged = true
		return nil
	}
	attrs := map[string]string{attrFamily: rule.family}
	if role, ok := logRoles[h.role]; ok {
		attrs[attrRole] = role
	}
	sev := "info"
	if h.level == '#' {
		sev = "warning"
	}
	ev := evidence.Event{
		EvidenceID: evidence.EventID(st.opts.CaseID, NameLog, st.opts.ImportNonce, no, 0),
		Source:     NameLog,
		Kind:       rule.kind,
		Host:       st.host,
		Start:      at,
		End:        at,
		Severity:   sev,
		Line:       no,
		TZRule:     clock.RuleHostLocal,
		Attrs:      attrs,
	}
	bad, err := st.sink.send(ev)
	if err != nil {
		return err
	}
	if bad {
		rep.Reject(no, importer.ReasonFieldCount)
	}
	return nil
}

// duplicate reports whether the line repeats an event already shown for the same start,
// stop, save or rewrite, and updates the dedupe state. A skipped line is still a matched
// line.
func (st *logState) duplicate(r *logRule, h logHeader, at time.Time) bool {
	switch r.guard {
	case logGuardStart:
		st.stopOpen = false
		if st.startOpen && st.startPid == h.pid {
			st.startOpen = false
			return true
		}
		st.startOpen = r.opens
		st.startPid = h.pid
	case logGuardStop:
		st.startOpen = false
		if st.stopOpen && st.stopPid == h.pid {
			return true
		}
		st.stopOpen, st.stopPid = true, h.pid
	case logGuardRDBStart:
		st.rdbDone.armed = true
	case logGuardAOFStart:
		st.aofDone.armed = true
	case logGuardRDBDone:
		return st.done(&st.rdbDone, at)
	case logGuardAOFDone:
		return st.done(&st.aofDone, at)
	}
	return false
}

// done lets the first completion line after a start through and skips the ones that follow
// it within logDoneWindow. Without a start line in the file, a completion more than the
// window after the previous one is shown.
func (st *logState) done(g *logGuard, at time.Time) bool {
	if !g.armed {
		d := at.Sub(g.last)
		if d < 0 {
			d = -d
		}
		if d <= logDoneWindow {
			return true
		}
	}
	g.armed = false
	g.last = at
	return false
}

func (st *logState) finish() {
	rep := st.rep
	q := evidence.SourceQuality{TimezoneRule: clock.RuleHostLocal}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
	}
	rep.Quality = q
	rep.Gap(gapLogLocal)
	rep.Gap(gapLogLevel)
	rep.Gap(gapLogKinds)
	if st.yearlessSeen {
		rep.Gap(gapLogNoYear)
	}
	if st.merged {
		rep.Gap(gapLogMerged)
	}
	if st.unmatched > 0 {
		rep.Gap(gapLogUnmatched)
	}
}
