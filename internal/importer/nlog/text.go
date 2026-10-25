// Package nlog imports NLog text logs written with the default pipe-delimited
// layout "${longdate}|${level:uppercase=true}|${logger}|${message}" (importer
// "nlog-text"). It is a framer for the shared application-log core
// (internal/importer/applog): it reads one line, builds an in-memory
// applog.Record and hands it to the core, which classifies it, emits the
// events and discards the record.
//
// Format, from the public NLog documentation (https://nlog-project.org/config/
// and the layout renderer pages for longdate and level; verify against the
// NLog version in use, no NLog was run for this card):
//
//   - ${longdate} is "yyyy-MM-dd HH:mm:ss.ffff" (four fraction digits) in the
//     machine's local time, with no zone or offset. Layouts that use
//     ${date:format=...} with one to seven fraction digits are read as well, as
//     long as the line keeps the pipe-delimited shape.
//   - ${level} is Trace, Debug, Info, Warn, Error or Fatal, upper-cased when the
//     layout says uppercase=true; either spelling is read.
//   - ${logger} is the logger name, ${message} the rendered message. The
//     message may hold "|"; only the first three delimiters count.
//   - A multi-line message or an exception rendered below the header (for
//     example by ${exception:format=tostring} after a newline) is a run of
//     lines that do not start with a time stamp.
//
// The space-delimited variant ("2001-10-24 13:00:00.1234 INFO Logger message")
// is not read.
//
// Time: the line carries no zone, so the user picks the host and the time is
// read in that host's zone (RuleHostLocal). No host zone refuses the file
// (tz-unlabelled); a time inside a clock change is rejected, never guessed
// (dst-ambiguous, dst-nonexistent). The owner may state that the times are UTC
// (supplied "log-time"="utc"; a layout that uses universalTime=true cannot be
// told apart from the default), which gives RuleUTCField and a gap sentence.
// The format has no offset, so there is no time-zone mismatch to check.
//
// Rows: the first header starts the data. A line that does not start with a time
// stamp is a continuation line of the entry above and is never rejected; lines
// before the first header are skipped with a gap. A line that starts with a time
// stamp but is not a whole header (cut, a level outside the NLog set, a missing
// field) is rejected: field-count, or bad-time when its time does not read.
//
// Privacy: message text, exception text and stack frames are never stored. What
// leaves a line is its time, its level, its logger name (only as the core's
// label), the curated exception types the core finds in the continuation lines,
// the family the core derives and the numbers of a request-finished line.
// Nothing from the file reaches an error, a report or a known gap.
package nlog

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// NameText is the importer name and the key of the export policy.
const NameText = "nlog-text"

// SuppliedLogTime is "utc" when the owner states that the log times are UTC.
const SuppliedLogTime = "log-time"

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapLocalTime = "Log times are host-local with no zone in the file; the host time zone chosen by the user is applied."
	GapUTCStated = "Log times are read as UTC; the owner stated this and the file does not."
	GapPreamble  = "Lines before the first entry header were skipped."
	GapExcCap    = "Message or exception text beyond 64 KiB for one entry was ignored."
)

const (
	maxExcBytes   = 64 << 10 // continuation text kept for classification, per entry
	ctxCheckEvery = 256
	maxHeadLine   = 8192 // longest line Detect looks at
)

// timeLayout reads the time of a header. Go accepts a fraction after the
// seconds field when the layout has none, so one to nine digits all parse.
const timeLayout = "2006-01-02 15:04:05"

var (
	// headerRe is a whole header: time, level, logger, message.
	headerRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d{1,7})?)\|((?i:trace|debug|info|warn|error|fatal))\|([^|]*)\|(.*)$`)
	// datedRe is a line that starts like an entry, whatever follows the time.
	datedRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
)

// NewText returns the NLog text importer.
func NewText() importer.Importer { return &textImp{} }

type textImp struct{}

func (*textImp) Name() string { return NameText }

// AllowedAttrs is the attribute set of the application-log export policy.
func (*textImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*textImp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 64 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// Detect scores 85 when one of the first lines is a whole header of the default
// layout and 90 when a second one is.
func (*textImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] == '<' {
		return 0
	}
	seen := 0
	for i, line := range strings.SplitN(s, "\n", 80) {
		if i >= 79 {
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > maxHeadLine || line == "" || line[0] < '0' || line[0] > '9' {
			continue
		}
		if headerRe.MatchString(line) {
			if seen++; seen == 2 {
				return 90
			}
		}
	}
	if seen == 1 {
		return 85
	}
	return 0
}

// pend is the entry being read: its header is known, its continuation lines are
// still arriving.
type pend struct {
	on     bool
	rec    applog.Record
	line   int64
	exc    []byte
	capped bool
}

func (p *pend) addExc(s string, acc *wincsv.Acc) {
	if p.capped {
		return
	}
	if len(p.exc)+len(s)+1 > maxExcBytes {
		p.capped = true
		acc.Gap(GapExcCap)
		return
	}
	if len(p.exc) > 0 {
		p.exc = append(p.exc, '\n')
	}
	p.exc = append(p.exc, s...)
}

// logTimeUTC reports the owner's statement that the log times are UTC.
func logTimeUTC(opts importer.Options) bool {
	return strings.EqualFold(strings.TrimSpace(opts.Supplied[SuppliedLogTime]), "utc")
}

// when reads a header time as UTC under rule.
func when(val, rule string, loc *time.Location) (time.Time, importer.Reason) {
	var t time.Time
	if rule == clock.RuleHostLocal {
		var why importer.Reason
		if t, why = applog.LocalTime(timeLayout, val, loc); why != "" {
			return time.Time{}, why
		}
	} else {
		var err error
		if t, err = time.Parse(timeLayout, val); err != nil {
			return time.Time{}, importer.ReasonBadTime
		}
		t = t.UTC()
	}
	if !wincsv.SaneTime(t) {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
}

// notHeaderReason says why a line that starts with a time stamp is not a header.
func notHeaderReason(text string) importer.Reason {
	if len(text) >= len(timeLayout) {
		if _, err := time.Parse(timeLayout, text[:len(timeLayout)]); err != nil {
			return importer.ReasonBadTime
		}
	}
	return importer.ReasonFieldCount
}

func (m *textImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	opts.Limits = lim
	acc := wincsv.NewAcc(NameText, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	// The lines carry no host name: they belong to the host the user picked.
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}

	var (
		sink    *applog.Sink
		rule    string
		pd      pend
		seen    bool
		started bool
	)
	// start fixes the zone rule at the first header and opens the sink.
	start := func() error {
		rule = clock.RuleHostLocal
		if logTimeUTC(opts) {
			rule = clock.RuleUTCField
			acc.Gap(GapUTCStated)
		} else {
			if opts.Host.TZ == nil {
				return wincsv.TZUnlabelled()
			}
			acc.Gap(GapLocalTime)
		}
		acc.Rep.Quality.TimezoneRule = rule
		acc.Rep.Quality.SourceVersion = opts.Host.Runtime
		acc.Rep.FieldRules["timestamp"] = rule
		sink = applog.NewSink(NameText, opts, emit)
		started = true
		return nil
	}
	flush := func() error {
		if !pd.on {
			return nil
		}
		rec := pd.rec
		rec.Exception = string(pd.exc)
		line := pd.line
		pd = pend{exc: pd.exc[:0]}
		return sink.Add(rec, line)
	}

	for n := 0; ; n++ {
		if n%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return acc.Fail(err)
			}
		}
		line, no, err := lr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return acc.Fail(err)
			}
			if e := acc.Row(no); e != nil {
				return acc.Fail(e)
			}
			acc.Reject(no, reason)
			continue
		}
		seen = true
		text := string(line)
		var sm []string
		if text != "" && text[0] >= '0' && text[0] <= '9' {
			sm = headerRe.FindStringSubmatch(text)
		}
		dated := sm != nil || (text != "" && datedRe.MatchString(text))
		if !started && !dated {
			if strings.TrimSpace(text) != "" {
				acc.Gap(GapPreamble)
			}
			continue
		}
		if e := acc.Row(no); e != nil { // every line from the first header on is a row, so MaxRows and the reject fraction see it
			return acc.Fail(e)
		}
		if !dated { // continuation line
			if pd.on {
				pd.addExc(text, acc)
			}
			continue
		}
		if e := flush(); e != nil {
			return acc.Fail(e)
		}
		if sm == nil { // starts like an entry but is not a whole header
			acc.Reject(no, notHeaderReason(text))
			continue
		}
		if !started {
			if e := start(); e != nil {
				return acc.Fail(e)
			}
		}
		t, why := when(sm[1], rule, opts.Host.TZ)
		if why != "" {
			acc.Reject(no, why)
			continue
		}
		pd = pend{on: true, line: no, exc: pd.exc[:0]}
		pd.rec = applog.Record{Time: t, Rule: rule, Level: sm[2], Category: sm[3], Message: sm[4]}
	}
	if !started {
		if !seen {
			return acc.Fail(textio.ErrEmpty)
		}
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if e := flush(); e != nil {
		return acc.Fail(e)
	}
	if e := sink.Finish(&acc.Rep); e != nil {
		return acc.Fail(e)
	}
	return acc.Finish()
}
