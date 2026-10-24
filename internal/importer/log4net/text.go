// Package log4net imports log4net application logs written by PatternLayout
// (text, importer "log4net-text") and by XmlLayoutSchemaLog4j or XmlLayout (XML,
// importer "log4net-xml"). Both turn each entry into an applog.Record and hand it
// to the shared applog.Sink, which classifies it and emits the events. Message
// text, thread, NDC, properties, user names and location info are never stored
// (plan sections 2 and 3, W2-b).
//
// Text: the file has no machine name, so the user picks the host. The date is
// host-local unless the pattern says otherwise (%utcdate, an offset in the date
// format, or the owner's "log-time" statement set to "utc"); a missing host zone
// refuses the file. A line that matches no header line is a continuation line: it
// is added to the Exception of the entry above and is never rejected. Lines before
// the first header line are skipped. See pattern.go for the pattern rules.
//
// XML: one element per entry. The timestamp is epoch milliseconds for the log4j
// schema (UTC) and ISO 8601 for the native layout. The machine-name property only
// feeds Report.ObservedHosts and the host mapping.
package log4net

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Importer names. They are the keys of the export policy.
const (
	NameText = "log4net-text"
	NameXML  = "log4net-xml"
)

// Options.Supplied keys the owner may set. S2 declares them in SuppliedKeys().
const (
	// SuppliedPattern is the conversionPattern value, nothing else (at most 512 bytes).
	SuppliedPattern = "pattern"
	// SuppliedLogTime is "utc" when the owner states that zone-less log times are UTC.
	SuppliedLogTime = "log-time"
)

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapLocalTime = "Log times are host-local with no zone in the file; the host time zone chosen by the user is applied."
	GapUTCStated = "Log times are read as UTC; the owner stated this and the file does not."
	GapPreamble  = "Lines before the first entry header were skipped."
	GapExcCap    = "Message or exception text beyond 64 KiB for one entry was ignored."
	GapXMLCut    = "The file could not be read past one point; later entries are missing."
)

const (
	maxExcBytes   = 64 << 10 // exception text kept for classification, per entry
	ctxCheckEvery = 256
)

// NewText returns the log4net PatternLayout text importer.
func NewText() importer.Importer { return &textImp{} }

type textImp struct{}

func (*textImp) Name() string { return NameText }

// Detect scores 85 when one of the first lines is a header line of a default
// pattern, else 0. A file that starts with "<" is never text.
func (*textImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] == '<' {
		return 0
	}
	for i, line := range strings.SplitN(s, "\n", 80) {
		if i >= 79 {
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > 8192 {
			continue
		}
		for _, h := range defaultHeaders {
			if h.re.MatchString(line) {
				return 85
			}
		}
	}
	return 0
}

func (*textImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*textImp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 64 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
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

// logTimeUTC reports the owner's statement that zone-less times are UTC.
func logTimeUTC(opts importer.Options) bool {
	return strings.EqualFold(strings.TrimSpace(opts.Supplied[SuppliedLogTime]), "utc")
}

// when reads a header date as UTC under rule.
func (h *header) when(val, rule string, loc *time.Location) (time.Time, importer.Reason) {
	var t time.Time
	if rule == clock.RuleHostLocal {
		var why importer.Reason
		if t, why = applog.LocalTime(h.layout, val, loc); why != "" {
			return time.Time{}, why
		}
	} else {
		var err error
		if t, err = time.Parse(h.layout, val); err != nil {
			return time.Time{}, importer.ReasonBadTime
		}
		t = t.UTC()
	}
	if !wincsv.SaneTime(t) {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
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
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	var h *header
	if p := opts.Supplied[SuppliedPattern]; p != "" {
		ch, why := compile(p)
		if why != "" { // why is one of this package's fixed words, never pattern text
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: why})
		}
		h = ch
	}

	var (
		sink *applog.Sink
		rule string
		pd   pend
		seen bool
	)
	// start fixes the zone rule once the header is known and opens the sink.
	start := func(hd *header) error {
		rule = hd.rule
		if hd.local && logTimeUTC(opts) {
			rule = clock.RuleUTCField
			acc.Gap(GapUTCStated)
		}
		if rule == clock.RuleHostLocal {
			if opts.Host.TZ == nil {
				return wincsv.TZUnlabelled()
			}
			acc.Gap(GapLocalTime)
		}
		acc.Rep.Quality.TimezoneRule = rule
		acc.Rep.Quality.SourceVersion = opts.Host.Runtime
		acc.Rep.FieldRules["timestamp"] = rule
		sink = applog.NewSink(NameText, opts, emit)
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
	if h != nil {
		if err := start(h); err != nil {
			return acc.Fail(err)
		}
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
		if h == nil {
			for _, c := range defaultHeaders {
				if sm = c.re.FindStringSubmatch(text); sm != nil {
					h = c
					break
				}
			}
			if h == nil {
				if strings.TrimSpace(text) != "" {
					acc.Gap(GapPreamble)
				}
				continue
			}
			if err := start(h); err != nil {
				return acc.Fail(err)
			}
		} else {
			sm = h.re.FindStringSubmatch(text)
		}
		if e := acc.Row(no); e != nil { // every line after the first header is a row, so MaxRows and the reject fraction see it
			return acc.Fail(e)
		}
		if sm == nil { // continuation line
			if pd.on {
				pd.addExc(text, acc)
			}
			continue
		}
		if e := flush(); e != nil {
			return acc.Fail(e)
		}
		t, why := h.when(sm[h.date], rule, opts.Host.TZ)
		if why != "" {
			acc.Reject(no, why)
			continue
		}
		pd.on, pd.line = true, no
		pd.rec = applog.Record{Time: t, Rule: rule}
		pd.exc = pd.exc[:0]
		if h.level > 0 {
			pd.rec.Level = sm[h.level]
		}
		if h.logger > 0 {
			pd.rec.Category = sm[h.logger]
		}
		if h.message > 0 {
			pd.rec.Message = sm[h.message]
		}
		if h.excGrp > 0 && sm[h.excGrp] != "" {
			pd.addExc(sm[h.excGrp], acc)
		}
	}
	if h == nil {
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
