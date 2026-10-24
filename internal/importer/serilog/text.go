package serilog

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

// NewText returns the importer for Serilog text written with the default file
// template.
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

// textLayout is the Go form of "yyyy-MM-dd HH:mm:ss.fff zzz".
const textLayout = "2006-01-02 15:04:05.000 -07:00"

var (
	// textHeader is the first line of a record in the default file template.
	textHeader = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2}) \[([A-Za-z]{3})\](?: (.*))?$`)
	// textHeadStart is textHeader's prefix, for Detect on a cut line.
	textHeadStart = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} [+-]\d{2}:\d{2} \[[A-Za-z]{3}\]`)
	// datedLine is a line that starts like a header but is not one.
	datedLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
	// noZoneHeader is that line with a fraction, no zone and a level.
	noZoneHeader = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)? \[`)
	// consoleHead is the console template's start: a time of day and a level, no date.
	consoleHead = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}(?:\.\d+)? [A-Za-z]{3}\]`)
)

// Detect scores a head whose first line is a default-template header at 90,
// and 95 when a later line is one as well.
func (*textImp) Detect(head []byte, _ string) int {
	s := wincsv.HeadText(head)
	score, seen := 0, 0
	for s != "" && seen < 40 {
		var l string
		l, s, _ = strings.Cut(s, "\n")
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		seen++
		if !textHeadStart.MatchString(l) {
			if score == 0 {
				return 0 // the first line must be a header
			}
			continue
		}
		if score == 0 {
			score = 90
			continue
		}
		return 95
	}
	return score
}

// record is the header line being read and the lines that follow it.
type record struct {
	rec  applog.Record
	line int64
	exc  []byte
	have bool
}

func (c *textImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, c.DefaultLimits())
	rep := importer.Report{Importer: NameText, FieldRules: map[string]string{}}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return rep, err
	}
	// The lines carry no host name: they belong to the host the user picked.
	if opts.Host == nil {
		return rep, importer.ErrUnknownHost
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return rep, err
	}
	so := opts
	so.Limits = lim
	sink := applog.NewSink(NameText, so, emit)
	var rules ruleTracker
	var cur record
	flush := func() error {
		if !cur.have {
			return nil
		}
		cur.have = false
		cur.rec.Exception = string(cur.exc)
		rules.see(cur.rec.Rule)
		return sink.Add(cur.rec, cur.line)
	}
	headers := 0
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		line, no, err := lr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return rep, err
			}
			rep.Rows++
			if err := lim.CheckRows(rep.Rows); err != nil {
				return rep, err
			}
			rep.Reject(no, reason)
			continue
		}
		rep.Rows++
		if err := lim.CheckRows(rep.Rows); err != nil {
			return rep, err
		}
		text := string(line)
		digit := text != "" && text[0] >= '0' && text[0] <= '9'
		if digit {
			if m := textHeader.FindStringSubmatch(text); m != nil {
				if err := flush(); err != nil {
					return rep, err
				}
				headers++
				t, perr := time.Parse(textLayout, m[1])
				if perr != nil || !saneTime(t) {
					rep.Reject(no, importer.ReasonBadTime)
					continue
				}
				cur = record{
					rec:  applog.Record{Time: t.UTC(), Rule: clock.RuleOffsetInField, Level: m[2], Message: m[3]},
					line: no, exc: cur.exc[:0], have: true,
				}
				continue
			}
			if datedLine.MatchString(text) {
				// A header with a zone missing, or a time that does not read.
				if err := flush(); err != nil {
					return rep, err
				}
				headers++
				reason := importer.ReasonBadTime
				if noZoneHeader.MatchString(text) {
					reason = importer.ReasonTZUnlabelled
				}
				rep.Reject(no, reason)
				continue
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if headers == 0 {
			// The first line of text is not a header of the default template.
			if consoleHead.MatchString(text) {
				return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "timestamp"}
			}
			return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		if cur.have && len(cur.exc) < maxExceptionBytes {
			if len(cur.exc) > 0 {
				cur.exc = append(cur.exc, '\n')
			}
			cur.exc = append(cur.exc, text...)
		}
	}
	if err := flush(); err != nil {
		return rep, err
	}
	if headers == 0 {
		return rep, textio.ErrEmpty
	}
	rep.Gap(GapNoLogger)
	if err := sink.Finish(&rep); err != nil {
		return rep, err
	}
	rules.finish(&rep, "Timestamp")
	return rep, finishReport(&rep, lim, opts.Host)
}
