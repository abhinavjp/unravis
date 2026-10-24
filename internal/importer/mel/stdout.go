package mel

import (
	"context"
	"io"
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

// KindAppSession is the point event placed at the start time in the file name.
const KindAppSession = "app-session"

// sessionOrdinal keeps the session point's event id apart from the sink's.
const sessionOrdinal = 9

// GapStdoutNameTime is added when the file name time falls in a DST fold or gap.
const GapStdoutNameTime = "The start time in the file name falls in a repeated or skipped hour of the host time zone; " +
	"the session point was not placed."

const stdoutTimeLayout = "20060102150405"

type stdoutImp struct{}

// NewStdout returns the importer for the ASP.NET Core Module stdout log.
func NewStdout() importer.Importer { return &stdoutImp{} }

func (*stdoutImp) Name() string { return NameStdout }

func (*stdoutImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*stdoutImp) DefaultLimits() importer.Limits { return defaultLimits(wincsv.DefaultMaxLineBytes) }

// Detect scores a file named stdout_<time>_<pid>.log with console text at 90
// (50 when the lines carry a time); the same text under another name at 15.
func (*stdoutImp) Detect(head []byte, fileName string) int {
	hi := readHead(head)
	named := stdoutNameRe.MatchString(baseName(fileName))
	switch {
	case named && (hi.untimed || hi.crash):
		return 90
	case named && hi.dated:
		return 50
	case hi.untimed || hi.crash:
		return 15
	}
	return 0
}

func (m *stdoutImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(NameStdout, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	// The file name is the only source of time and of the process id.
	nm := stdoutNameRe.FindStringSubmatch(baseName(opts.FileName))
	if nm == nil {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if _, err := strconv.ParseUint(nm[2], 10, 32); err != nil {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	if opts.Host.TZ == nil {
		return acc.Fail(wincsv.TZUnlabelled())
	}
	start, why := applog.LocalTime(stdoutTimeLayout, nm[1], opts.Host.TZ)
	switch why {
	case importer.ReasonDSTAmbiguous, importer.ReasonDSTNonexistent:
		start = start.UTC() // zero; no point is placed
	case "":
		if !wincsv.SaneTime(start) {
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
		}
	default:
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	opts.Limits = lim
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	sink := applog.NewSink(NameStdout, opts, emit)
	s := newScan(ctx, opts, acc, sink, true)
	if err := s.run(lr); err != nil {
		return acc.Fail(err)
	}
	if err := s.finish(); err != nil {
		return acc.Fail(err)
	}
	if s.headers == 0 && !s.crashSeen {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if err := sink.Finish(&acc.Rep); err != nil {
		return acc.Fail(err)
	}
	if !start.IsZero() {
		if err := s.session(start, emit); err != nil {
			return acc.Fail(err)
		}
		acc.Gap(GapStdoutTime)
	} else {
		acc.Gap(GapStdoutNameTime)
	}
	setRule(acc, clock.RuleHostLocal, opts)
	return acc.Finish()
}

// session emits the app-session point at the file-name time.
func (s *scan) session(start time.Time, emit func(evidence.Event) error) error {
	if !s.opts.Window.Contains(start) {
		return nil
	}
	if err := s.acc.CheckEmit(1); err != nil {
		return err
	}
	line := int64(1)
	if s.crashSeen {
		line = s.crashLine
	}
	ev := wincsv.NewEvent(s.opts, NameStdout, line, sessionOrdinal)
	ev.Kind = KindAppSession
	ev.Start, ev.End = start, start
	ev.Host = s.opts.Host.Label
	ev.TZRule = clock.RuleHostLocal
	ev.Severity = "info"
	if s.crashSeen {
		ev.Severity = "critical"
		attrs := map[string]string{applog.AttrLevel: "crit"}
		if types := applog.ExceptionTypes(string(s.crashText)); len(types) > 0 {
			attrs[applog.AttrException] = strings.Join(types, ",")
		}
		ev.Attrs = attrs
	}
	if err := emit(ev); err != nil {
		return err
	}
	s.acc.Emitted(start)
	return nil
}
