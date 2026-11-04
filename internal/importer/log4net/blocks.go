package log4net

// The block importer ("log4net-blocks") reads application logs that a log4net
// layout wrote as several labelled lines per entry, entries separated by a blank
// line:
//
//	Logger name: Synthetic.Api.Orders
//	Type: ERROR
//	Server Time: 2001-10-25 14:02:30.950
//	UTC Time: 2001-10-25 01:02:30.950
//	Application URL: https://app.example.test/orders
//	User IP: 192.0.2.10
//	System.InvalidOperationException: text of the message
//	   at Synthetic.Api.Orders.Save()
//
// The label spellings are matched case-insensitively and a few aliases are
// accepted (see blockLabels). Free lines after the labels are the message and the
// exception text. The reader is tolerant: blank lines are soft, a block may lack
// any label, and the order of the labels is free. A header label starts a new
// block after a blank line, or when the label is repeated before the body began.
//
// Time: the UTC Time label when it reads; else the Server Time label, which is
// host-local unless it carries its own offset or the owner's "log-time" statement
// is "utc" (a missing host zone then refuses the file, as in log4net-text). A
// block with no readable time is one rejected row.
//
// URL and user IP are recognised so that they delimit the block, but the values
// are dropped on purpose: the shared sink stores neither (plan sections 2 and 3).
// Message and exception text feed the classifier and are never stored.

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// NameBlocks is the importer name of the labelled-block layout.
const NameBlocks = "log4net-blocks"

const (
	maxLabelName  = 20      // longest label spelling looked up, in bytes
	maxLabelValue = 1 << 10 // header value kept, in bytes
	maxMsgBytes   = 1 << 10 // message kept for the classifier, per entry
	maxSniffLine  = 8192
	sniffLines    = 80
)

// blockLabel is the meaning of one label.
type blockLabel int

const (
	lbNone blockLabel = iota
	lbLogger
	lbLevel
	lbServer
	lbUTC
	lbURL
	lbIP
	lbMessage   // body label: never starts a block
	lbException // body label: never starts a block
	lbCount
)

// header reports whether the label is one that can start a block.
func (l blockLabel) header() bool { return l >= lbLogger && l <= lbIP }

var blockLabels = map[string]blockLabel{
	"logger name": lbLogger, "logger": lbLogger, "category": lbLogger,
	"type": lbLevel, "level": lbLevel, "severity": lbLevel, "log level": lbLevel,
	"server time": lbServer, "local time": lbServer, "time": lbServer, "date": lbServer,
	"timestamp": lbServer, "log time": lbServer, "date time": lbServer,
	"utc time": lbUTC, "time utc": lbUTC, "utc": lbUTC, "utc date": lbUTC,
	"application url": lbURL, "url": lbURL, "request url": lbURL,
	"user ip": lbIP, "client ip": lbIP, "ip": lbIP, "ip address": lbIP, "user ip address": lbIP,
	"message": lbMessage, "details": lbMessage,
	"exception": lbException, "stack trace": lbException, "stacktrace": lbException,
}

// splitLabel reads "Label: value". The label is the text before the first colon,
// at most maxLabelName bytes; anything else is not a label line.
func splitLabel(line string) (blockLabel, string) {
	s := strings.TrimLeft(line, " \t")
	i := strings.IndexByte(s, ':')
	if i < 1 || i > maxLabelName {
		return lbNone, ""
	}
	name := strings.Join(strings.Fields(strings.ToLower(s[:i])), " ")
	lb, ok := blockLabels[name]
	if !ok {
		return lbNone, ""
	}
	return lb, strings.TrimSpace(s[i+1:])
}

// clipText bounds s to n bytes on a rune boundary.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// looksTime is the cheap shape test Detect uses on a time label: it starts with a
// digit, holds a clock colon and is not absurdly long.
func looksTime(v string) bool {
	return len(v) >= 8 && len(v) <= 64 && v[0] >= '0' && v[0] <= '9' && strings.IndexByte(v, ':') > 0
}

// Time layouts. The value is normalised first: ISO "T" becomes a space, yyyy/MM/dd
// becomes yyyy-MM-dd, a day/month-first slash date becomes month first, and the
// text is upper-cased so that "pm" reads. Go reads a fraction after the seconds
// whatever the layout says.
var (
	wallLayouts = []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04",
		"01/02/2006 15:04:05", "01/02/2006 15:04",
		"01/02/2006 3:04:05 PM", "01/02/2006 3:04 PM",
	}
	zonedLayouts = []string{
		"2006-01-02 15:04:05Z07:00", "2006-01-02 15:04Z07:00",
		"01/02/2006 15:04:05Z07:00", "01/02/2006 15:04Z07:00",
	}
	zoneTailRe = regexp.MustCompile(`^(.+?\d:\d{2}(?::\d{2}(?:[.,]\d+)?)?)\s*(Z|UTC|GMT|[+-]\d{2}(?::?\d{2})?)$`)
	ymdSlashRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}`)
)

// normStamp returns the value ready for the layouts and whether it carries its
// own zone (then it ends in "Z" or "+hh:mm"). order tracks slash dates; nil reads
// each on its own.
func normStamp(v string, order *applog.DateOrder) (string, bool, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" || len(v) > 64 {
		return "", false, false
	}
	if len(v) > 10 && v[10] == 'T' {
		v = v[:10] + " " + v[11:]
	}
	if ymdSlashRe.MatchString(v) {
		v = v[:4] + "-" + v[5:7] + "-" + v[8:]
	}
	v, ok := applog.NormalizeSlashDate(v, order)
	if !ok {
		return "", false, false
	}
	m := zoneTailRe.FindStringSubmatch(v)
	if m == nil {
		return v, false, true
	}
	z := m[2]
	switch z {
	case "Z", "UTC", "GMT":
		z = "Z"
	default:
		d := strings.ReplaceAll(z[1:], ":", "")
		if len(d) == 2 {
			d += "00"
		}
		z = z[:1] + d[:2] + ":" + d[2:]
	}
	return m[1] + z, true, true
}

func parseZoned(v string) (time.Time, bool) {
	for _, l := range zonedLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func wallLayout(v string) (string, bool) {
	for _, l := range wallLayouts {
		if _, err := time.Parse(l, v); err == nil {
			return l, true
		}
	}
	return "", false
}

// stampKind says how a block's time was read, for the field rule and the gaps.
type stampKind int

const (
	stampNone   stampKind = iota
	stampUTC              // the UTC Time label, or a Server Time read as UTC by the owner's statement
	stampOffset           // the value carries its own offset
	stampLocal            // host-local, resolved with the host zone
)

// readStamp reads one time value. wantLocal is the owner's statement ("utc") turned
// round: when false and the value has no zone and utcLabel is false, the value is
// host-local. A nil error with kind stampNone means the value is unreadable; the
// reason says why. A fatal error refuses the file.
func readStamp(v string, utcLabel, utcStated bool, loc *time.Location, order *applog.DateOrder) (time.Time, stampKind, importer.Reason, error) {
	nv, zoned, ok := normStamp(v, order)
	if !ok {
		return time.Time{}, stampNone, importer.ReasonBadTime, nil
	}
	if zoned {
		t, ok := parseZoned(nv)
		if !ok || !wincsv.SaneTime(t) {
			return time.Time{}, stampNone, importer.ReasonBadTime, nil
		}
		return t, stampOffset, "", nil
	}
	layout, ok := wallLayout(nv)
	if !ok {
		return time.Time{}, stampNone, importer.ReasonBadTime, nil
	}
	if utcLabel || utcStated {
		t, _ := time.Parse(layout, nv)
		t = t.UTC()
		if !wincsv.SaneTime(t) {
			return time.Time{}, stampNone, importer.ReasonBadTime, nil
		}
		return t, stampUTC, "", nil
	}
	if loc == nil {
		return time.Time{}, stampNone, importer.ReasonTZUnlabelled, wincsv.TZUnlabelled()
	}
	t, why := applog.LocalTime(layout, nv, loc)
	if why != "" {
		return time.Time{}, stampNone, why, nil
	}
	if !wincsv.SaneTime(t) {
		return time.Time{}, stampNone, importer.ReasonBadTime, nil
	}
	return t, stampLocal, "", nil
}

// NewBlocks returns the importer for labelled multi-line log4net entries.
func NewBlocks() importer.Importer { return &blocksImp{} }

type blocksImp struct{}

func (*blocksImp) Name() string { return NameBlocks }

// Detect scores 90 when the first lines hold three different header labels, one
// of them a time that looks like a time, and 70 for two. A file that starts with
// "<" is never text.
func (*blocksImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] == '<' {
		return 0
	}
	var seen [lbCount]bool
	timeSeen := false
	for i, line := range strings.SplitN(s, "\n", sniffLines) {
		if i >= sniffLines-1 {
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > maxSniffLine {
			continue
		}
		lb, val := splitLabel(line)
		if !lb.header() {
			continue
		}
		if lb == lbServer || lb == lbUTC {
			if !looksTime(val) {
				continue
			}
			timeSeen = true
		}
		seen[lb] = true
	}
	if !timeSeen {
		return 0
	}
	n := 0
	for _, ok := range seen {
		if ok {
			n++
		}
	}
	switch {
	case n >= 3:
		return 90
	case n == 2:
		return 70
	}
	return 0
}

func (*blocksImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*blocksImp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 64 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// ProbeTime implements importer.TimeProber for a "UTC Time:" or "Server Time:"
// line. Any other line has no time of its own.
func (*blocksImp) ProbeTime(line []byte, opts importer.Options) (time.Time, bool) {
	if len(line) > maxSniffLine {
		return time.Time{}, false
	}
	lb, val := splitLabel(string(line))
	if lb != lbUTC && lb != lbServer {
		return time.Time{}, false
	}
	var loc *time.Location
	if opts.Host != nil {
		loc = opts.Host.TZ
	}
	// A fresh tracker per line: Parse honours the supplied date order, so the probe
	// must read an ambiguous slash date the same way.
	order := applog.NewDateOrder(opts.Supplied[applog.SuppliedDateOrder])
	t, kind, _, err := readStamp(val, lb == lbUTC, logTimeUTC(opts), loc, order)
	if err != nil || kind == stampNone {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = (*blocksImp)(nil)

// block is the entry being read.
type block struct {
	on        bool
	line      int64
	has       [lbCount]bool
	body      bool // a free line or a body label was read
	blank     bool // a blank line followed the last line of the block
	level     string
	logger    string
	server    string
	utc       string
	msg       string
	hasMsg    bool
	exc       []byte
	excCapped bool
}

func (b *block) reset(line int64) {
	*b = block{on: true, line: line, exc: b.exc[:0]}
}

func (b *block) addBody(s string, acc *wincsv.Acc) {
	b.body = true
	if s == "" {
		return
	}
	if !b.hasMsg {
		b.msg, b.hasMsg = clipText(s, maxMsgBytes), true
	}
	if b.excCapped {
		return
	}
	if len(b.exc)+len(s)+1 > maxExcBytes {
		b.excCapped = true
		acc.Gap(GapExcCap)
		return
	}
	if len(b.exc) > 0 {
		b.exc = append(b.exc, '\n')
	}
	b.exc = append(b.exc, s...)
}

// startsBlock decides whether a header label begins a new block. A repeated
// label always does before the body; a repeated logger or time label does after
// it too, since those name an entry. A blank line ends an entry that has a body
// or a time; before that it is soft, so a blank line inside the labels is
// tolerated.
func (b *block) startsBlock(lb blockLabel) bool {
	switch {
	case !b.on:
		return true
	case b.has[lb] && (!b.body || lb == lbLogger || lb == lbServer || lb == lbUTC):
		return true
	case b.blank:
		return b.body || b.has[lbServer] || b.has[lbUTC]
	}
	return false
}

func (m *blocksImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	opts.Limits = lim
	acc := wincsv.NewAcc(NameBlocks, lim)
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
	acc.Rep.Quality.SourceVersion = opts.Host.Runtime
	sink := applog.NewSink(NameBlocks, opts, emit)
	order := applog.NewDateOrder(opts.Supplied[applog.SuppliedDateOrder])
	utcStated := logTimeUTC(opts)

	var (
		b       block
		seen    bool // any non-blank line
		blocks  int
		ruleSet bool
	)
	flush := func() error {
		if !b.on {
			return nil
		}
		defer func() { b.on = false }()
		var (
			t    time.Time
			kind stampKind
			why  importer.Reason
			err  error
		)
		if b.utc != "" {
			t, kind, why, err = readStamp(b.utc, true, utcStated, opts.Host.TZ, order)
			if err != nil {
				return err
			}
		}
		if kind == stampNone && b.server != "" {
			t, kind, why, err = readStamp(b.server, false, utcStated, opts.Host.TZ, order)
			if err != nil {
				return err
			}
		}
		if kind == stampNone {
			if why == "" {
				why = importer.ReasonBadTime
			}
			acc.Reject(b.line, why)
			return nil
		}
		var rule string
		switch kind {
		case stampUTC:
			rule = clock.RuleUTCField
			if utcStated && b.utc == "" {
				acc.Gap(GapUTCStated)
			}
		case stampOffset:
			rule = clock.RuleOffsetInField
		default:
			rule = clock.RuleHostLocal
			acc.Gap(GapLocalTime)
		}
		if !ruleSet {
			ruleSet = true
			acc.Rep.Quality.TimezoneRule = rule
			acc.Rep.FieldRules["timestamp"] = rule
		}
		return sink.Add(applog.Record{
			Time: t, Rule: rule, Level: b.level, Category: b.logger,
			Message: b.msg, Exception: string(b.exc),
		}, b.line)
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
		text := string(line)
		if strings.TrimSpace(text) == "" {
			if b.on {
				b.blank = true
			}
			continue
		}
		seen = true
		lb, val := splitLabel(text)
		if lb.header() && b.startsBlock(lb) {
			if e := acc.Row(no); e != nil { // before the flush, so a row limit emits nothing of this block
				return acc.Fail(e)
			}
			if e := flush(); e != nil {
				return acc.Fail(e)
			}
			b.reset(no)
			blocks++
		}
		if !b.on { // text before the first entry
			acc.Gap(GapPreamble)
			continue
		}
		b.blank = false
		if lb.header() && b.body && b.has[lb] {
			lb = lbNone // a label spelling inside the body is body text
		}
		switch lb {
		case lbLogger:
			b.logger = clipText(val, maxLabelValue)
		case lbLevel:
			b.level = clipText(val, 64)
		case lbServer:
			b.server = clipText(val, 64)
		case lbUTC:
			b.utc = clipText(val, 64)
		case lbURL, lbIP:
			// recognised so that it delimits the block; never kept
		case lbMessage, lbException:
			b.addBody(val, acc)
		default:
			b.addBody(strings.TrimSpace(text), acc)
		}
		if lb.header() {
			b.has[lb] = true
		}
	}
	if !seen {
		return acc.Fail(textio.ErrEmpty)
	}
	if blocks == 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if e := flush(); e != nil {
		return acc.Fail(e)
	}
	order.Gaps(acc.Gap)
	if e := sink.Finish(&acc.Rep); e != nil {
		return acc.Fail(e)
	}
	return acc.Finish()
}
