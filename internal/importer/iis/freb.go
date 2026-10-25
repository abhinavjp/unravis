package iis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/wincsv"
)

// IIS Failed Request Tracing (FREB) files, fr######.xml.
//
// One <failedRequest> element is one case row: the request's time, route
// (the URL path with the query cut and ids folded), status, sub-status, win32
// error, time taken and failure reason. From the trace events the importer
// also derives, per request, up to frebTopModules "slow-module" rows: the
// modules (with the pipeline notification they ran for) that held the longest
// gap between two consecutive events.
//
// What is read and what is not. The decoder walks tokens; only these values are
// ever copied out of the file:
//
//	failedRequest   url (path only), verb, statusCode, triggerStatusCode,
//	                timeTaken, failureReason
//	event           System/TimeCreated@SystemTime, System/Computer,
//	                RenderingInfo/Opcode (compared with one constant, never kept),
//	                and the EventData/Data elements named ModuleName,
//	                Notification, HttpSubStatus and ErrorCode.
//
// Every other element, attribute and Data payload (query strings, headers,
// cookies, user names, URLs inside events, bodies, activity ids) is read past and
// dropped. Module and notification names must be plain identifier characters, so
// a value with spaces, dashes or other text is dropped rather than kept.
//
// Time. SystemTime is UTC (the trailing Z). A request whose event times do not
// parse, or are not UTC, is rejected as bad-time; the host zone plays no part.
//
// Files. Several documents back to back (a folder of traces joined with cat) are
// read as one file, since the decoder accepts a second root element and a second
// XML declaration; nothing else is needed.
//
// Safety. A DOCTYPE (or any directive) refuses the file, so no entity can be
// declared; the decoder never expands anything beyond the five built-in ones.
// Nesting beyond frebMaxDepth refuses the file, one token larger than
// MaxLineBytes is a line-bytes breach, and a request holding more than
// frebMaxEvents events is rejected.

const (
	frebName = "iis-freb"

	frebMaxDepth      = 64
	frebMaxEvents     = 20000 // events read per request; a request with more is rejected
	frebMaxKeys       = 1024  // distinct module+notification pairs tracked per request
	frebTopModules    = 3
	frebMinSlow       = time.Millisecond
	frebMaxText       = 256 // longest element text kept (module, notification, error code, computer)
	frebCapSlack      = 8192
	frebTokenCheck    = 4096
	frebOpcodeError   = "MODULE_SET_RESPONSE_ERROR_STATUS"
	frebOpcodeModDone = "NOTIFY_MODULE_COMPLETION"

	// Fixed "why" words for whole-file refusals.
	frebWhyDoctype   = "xml-doctype"
	frebWhyDepth     = "xml-depth"
	frebWhyNoRequest = "xml-no-request"

	gapFREBOnlyFailures = "Failed request tracing writes a file only for requests that matched a tracing rule, so this file shows the traced failures, not all traffic."
	gapFREBCut          = "The file ended inside a request or held text that is not valid XML; requests before that point were read and the rest was not."
	gapFREBSlowModule   = "Slow-module rows give the longest gap between two consecutive trace events and name the module running at the start of that gap; they do not say what that module was doing."
)

var errFREBDirective = &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: frebWhyDoctype}

// FREB imports IIS Failed Request Tracing XML files.
type FREB struct{}

// NewFREB returns the iis-freb importer.
func NewFREB() importer.Importer { return FREB{} }

func (FREB) Name() string { return frebName }

func (FREB) DefaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 256 << 20, MaxRows: 1_000_000, MaxEvents: 4_000_000,
		MaxLineBytes: importer.DefaultMaxLineBytes, MaxFields: importer.DefaultMaxFields,
		MaxRejectFraction: importer.DefaultMaxRejectFraction}
}

func (FREB) AllowedAttrs() map[string]bool {
	return map[string]bool{"status": true, "substatus": true, "win32": true, "trigger": true,
		"reason": true, "method": true, "stem": true, "module": true, "notification": true}
}

var frebRootRe = regexp.MustCompile(`(?i)<failedRequest\s[^>]*\burl\s*=`)

// Detect scores 95 when, after the XML prolog, the head holds a failedRequest
// start tag with a url attribute. Other XML formats (log4net) have no such tag.
func (FREB) Detect(head []byte, fileName string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] != '<' {
		return 0
	}
	if frebRootRe.MatchString(s) {
		return 95
	}
	return 0
}

// capReader fails once more than max+frebCapSlack bytes passed since base, so one
// element, comment or text node cannot grow without bound inside the decoder.
type frebCapReader struct {
	r         io.Reader
	pos, base int64
	max       int64
}

func (c *frebCapReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.pos += int64(n)
	if c.pos-c.base > c.max+frebCapSlack {
		return n, &importer.LimitError{Limit: "line-bytes", Max: c.max, At: c.pos - c.base}
	}
	return n, err
}

// frebUTF8 drops a UTF-8 BOM and transcodes UTF-16 to UTF-8.
func frebUTF8(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 4096)
	head, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		_, _ = br.Discard(3)
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}):
		_, _ = br.Discard(2)
		return transform.NewReader(br, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()), nil
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		_, _ = br.Discard(2)
		return transform.NewReader(br, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()), nil
	}
	return br, nil
}

// frebReader wraps the decoder with the guards every token passes: context
// check, per-token size cap, directive refusal and nesting depth.
type frebReader struct {
	ctx    context.Context
	d      *xml.Decoder
	cr     *frebCapReader
	max    int64
	tokens int
	depth  int
}

func (f *frebReader) next() (xml.Token, error) {
	if f.tokens++; f.tokens%frebTokenCheck == 0 {
		if err := f.ctx.Err(); err != nil {
			return nil, err
		}
	}
	off0 := f.d.InputOffset()
	f.cr.base = off0
	tok, err := f.d.Token()
	if err != nil {
		return nil, err
	}
	if sz := f.d.InputOffset() - off0; sz > f.max {
		return nil, &importer.LimitError{Limit: "line-bytes", Max: f.max, At: sz}
	}
	switch tok.(type) {
	case xml.Directive:
		return nil, errFREBDirective
	case xml.StartElement:
		if f.depth++; f.depth > frebMaxDepth {
			return nil, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: frebWhyDepth}
		}
	case xml.EndElement:
		f.depth--
	}
	return tok, nil
}

// skip reads to the end of the element whose start tag was just returned.
func (f *frebReader) skip() error {
	for depth := 1; depth > 0; {
		tok, err := f.next()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// text returns the character data directly inside the element whose start tag was
// just returned (nested elements are read past). capped is true when more than
// frebMaxText bytes were dropped.
func (f *frebReader) text() (s string, capped bool, err error) {
	var buf []byte
	for depth := 1; depth > 0; {
		tok, err := f.next()
		if err != nil {
			return "", false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth != 1 {
				continue
			}
			if len(buf)+len(t) > frebMaxText {
				capped = true
				t = t[:frebMaxText-len(buf)]
			}
			buf = append(buf, t...)
		}
	}
	return string(buf), capped, nil
}

func frebAttr(se xml.StartElement, name string) (string, bool) {
	for _, a := range se.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value, true
		}
	}
	return "", false
}

// frebEvt is what is kept of one trace event.
type frebEvt struct {
	stamp, computer           string
	module, notif             string
	subStatus, errCode        string
	opcode                    string
	modCapped, notifCapped    bool
	opcodeCapped, hostCapped  bool
}

func (f *frebReader) event() (frebEvt, error) {
	var ev frebEvt
	for {
		tok, err := f.next()
		if err != nil {
			return ev, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return ev, nil
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "system":
				err = f.system(&ev)
			case "eventdata":
				err = f.eventData(&ev)
			case "renderinginfo":
				err = f.rendering(&ev)
			default:
				err = f.skip()
			}
			if err != nil {
				return ev, err
			}
		}
	}
}

func (f *frebReader) system(ev *frebEvt) error {
	for {
		tok, err := f.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "timecreated":
				ev.stamp, _ = frebAttr(t, "SystemTime")
				err = f.skip()
			case "computer":
				ev.computer, ev.hostCapped, err = f.text()
			default:
				err = f.skip()
			}
			if err != nil {
				return err
			}
		}
	}
}

func (f *frebReader) eventData(ev *frebEvt) error {
	for {
		tok, err := f.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if !strings.EqualFold(t.Name.Local, "data") {
				if err := f.skip(); err != nil {
					return err
				}
				continue
			}
			name, _ := frebAttr(t, "Name")
			switch strings.ToLower(name) {
			case "modulename":
				ev.module, ev.modCapped, err = f.text()
			case "notification":
				ev.notif, ev.notifCapped, err = f.text()
			case "httpsubstatus":
				ev.subStatus, _, err = f.text()
			case "errorcode":
				ev.errCode, _, err = f.text()
			default:
				err = f.skip()
			}
			if err != nil {
				return err
			}
		}
	}
}

func (f *frebReader) rendering(ev *frebEvt) error {
	for {
		tok, err := f.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "opcode") {
				ev.opcode, ev.opcodeCapped, err = f.text()
			} else {
				err = f.skip()
			}
			if err != nil {
				return err
			}
		}
	}
}

// slowest is the longest gap seen for one module and notification.
type slowest struct {
	module, notif string
	gap           time.Duration
	from, to      time.Time
}

// frebReq accumulates one failedRequest.
type frebReq struct {
	line int64
	bad  importer.Reason // first reason this request cannot be a row; reading goes on

	url, verb, reason          string
	status, trigger, timeTaken string
	hasStatus                  bool

	events   int
	over     bool
	computer string
	hostBad  bool

	first, last time.Time
	hasTime     bool
	prev        time.Time
	curMod      string
	curNotif    string

	subStatus string
	win32     string
	keys      map[string]*slowest
}

func (p *frebReq) fail(r importer.Reason) {
	if p.bad == "" {
		p.bad = r
	}
}

// parseFREBTime reads SystemTime. FREB writes UTC with a trailing Z; any other
// form is refused rather than guessed.
func parseFREBTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 20 || len(s) > 40 || s[len(s)-1] != 'Z' {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !saneLogTime(t) {
		return time.Time{}, false
	}
	return t.UTC(), true
}

var frebVersionTail = regexp.MustCompile(`-[0-9]{1,3}\.[0-9]{1,3}$`)

// frebModuleName accepts an IIS module name: letters, digits, dot and underscore,
// plus the version tail IIS adds to some (ScriptModule-4.0). Any other dash is
// refused, so free text with dashes is never kept.
func frebModuleName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if loc := frebVersionTail.FindStringIndex(s); loc != nil {
		if _, ok := frebWord(s[:loc[0]], 64, true); !ok {
			return "", false
		}
		return s, len(s) <= 64
	}
	return frebWord(s, 64, true)
}

// frebWord accepts letters, digits and underscore, and the dot when dot is set.
func frebWord(s string, max int, dot bool) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > max {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		ok := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || dot && b == '.'
		if !ok {
			return "", false
		}
	}
	return s, true
}

// frebErrorCode reads the number out of an ErrorCode text such as
// "Access is denied. (0x80070005)" or a plain decimal. ok is false when there is none.
func frebErrorCode(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "(0x"); i >= 0 {
		rest := s[i+3:]
		j := strings.IndexByte(rest, ')')
		if j < 1 || j > 8 {
			return "", false
		}
		var n uint64
		for k := 0; k < j; k++ {
			b := rest[k]
			var d byte
			switch {
			case b >= '0' && b <= '9':
				d = b - '0'
			case b >= 'a' && b <= 'f':
				d = b - 'a' + 10
			case b >= 'A' && b <= 'F':
				d = b - 'A' + 10
			default:
				return "", false
			}
			n = n<<4 | uint64(d)
		}
		return itoa(n), true
	}
	if n, ok := parseUint(s, 4294967295); ok {
		return itoa(n), true
	}
	return "", false
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// add folds one event into the request.
func (p *frebReq) add(ev frebEvt) {
	p.events++
	if p.events == 1 && !ev.hostCapped {
		p.computer = strings.TrimSpace(ev.computer)
	} else if p.events == 1 {
		p.hostBad = true
	}
	t, ok := parseFREBTime(ev.stamp)
	if !ok {
		p.fail(importer.ReasonBadTime)
		return
	}
	if !p.hasTime {
		p.hasTime, p.first = true, t
	} else if d := t.Sub(p.prev); d >= frebMinSlow && p.curMod != "" {
		if p.keys == nil {
			p.keys = map[string]*slowest{}
		}
		key := p.curMod + "\x00" + p.curNotif
		s := p.keys[key]
		switch {
		case s == nil && len(p.keys) < frebMaxKeys:
			p.keys[key] = &slowest{module: p.curMod, notif: p.curNotif, gap: d, from: p.prev, to: t}
		case s != nil && d > s.gap:
			s.gap, s.from, s.to = d, p.prev, t
		}
	}
	if t.Before(p.first) {
		p.first = t
	}
	if t.After(p.last) {
		p.last = t
	}
	p.prev = t

	if !ev.modCapped {
		if m, ok := frebModuleName(ev.module); ok {
			p.curMod = m
			p.curNotif = ""
			if !ev.notifCapped {
				if n, ok := frebWord(ev.notif, 64, false); ok {
					p.curNotif = n
				}
			}
		}
	}
	switch strings.TrimSpace(ev.opcode) {
	case frebOpcodeModDone:
		p.curMod, p.curNotif = "", ""
	case frebOpcodeError:
		if ev.opcodeCapped {
			return
		}
		if v := strings.TrimSpace(ev.subStatus); v != "" {
			if _, ok := parseUint(v, 65535); !ok {
				p.fail(importer.ReasonBadNumber)
			} else {
				p.subStatus = v
			}
		}
		if v, ok := frebErrorCode(ev.errCode); ok {
			p.win32 = v
		}
	}
}

// request reads the failedRequest element whose start tag was just returned.
func (f *frebReader) request(se xml.StartElement, p *frebReq) error {
	for _, a := range se.Attr {
		switch strings.ToLower(a.Name.Local) {
		case "url":
			p.url = a.Value
		case "verb":
			p.verb = a.Value
		case "statuscode":
			p.status = a.Value
		case "triggerstatuscode":
			p.trigger = a.Value
		case "timetaken":
			p.timeTaken = a.Value
		case "failurereason":
			p.reason = a.Value
		}
	}
	for {
		tok, err := f.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if !strings.EqualFold(t.Name.Local, "event") {
				if err := f.skip(); err != nil {
					return err
				}
				continue
			}
			if p.events >= frebMaxEvents {
				p.over = true
				if err := f.skip(); err != nil {
					return err
				}
				continue
			}
			ev, err := f.event()
			if err != nil {
				return err
			}
			p.add(ev)
		}
	}
}

// frebPath returns the path of a request URL: scheme and authority are dropped
// (and so is any user info), then the query is cut and the route normalised.
func frebPath(u string) (string, bool) {
	if i := strings.Index(u, "://"); i >= 0 && i <= 10 {
		u = u[i+3:]
		j := strings.IndexAny(u, "/?#")
		if j < 0 {
			return "", false
		}
		u = u[j:]
	}
	return normaliseStem(u)
}

func isSyntaxErr(err error) bool {
	var se *xml.SyntaxError
	return errors.As(err, &se)
}

func (FREB) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {

	lim := wincsv.MergeLimits(opts.Limits, FREB{}.DefaultLimits())
	opts.Limits = lim
	acc := wincsv.NewAcc(frebName, lim)
	acc.Rep.FieldRules["SystemTime"] = clock.RuleUTCField
	acc.Rep.Quality.TimezoneRule = clock.RuleUTCField
	if opts.Host != nil {
		acc.Rep.Quality.SourceVersion = opts.Host.Runtime
	}
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	ur, err := frebUTF8(pr)
	if err != nil {
		return acc.Fail(err)
	}
	max := int64(lim.MaxLineBytes)
	cr := &frebCapReader{r: ur, max: max}
	d := xml.NewDecoder(cr)
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil } // already UTF-8
	f := &frebReader{ctx: ctx, d: d, cr: cr, max: max}

	st := &runState{rep: &acc.Rep, hostCache: map[string]string{}, machines: map[string]struct{}{},
		declared: map[string]bool{}}
	rc := &rowCtx{opts: opts, st: st}

	var (
		prevLine int64
		cut      bool
	)
loop:
	for {
		tok, err := f.next()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				break loop
			case isSyntaxErr(err):
				cut = true
				break loop
			}
			return acc.Fail(err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(se.Name.Local, "failedRequest") {
			if err := f.skip(); err != nil {
				if isSyntaxErr(err) {
					cut = true
					break loop
				}
				return acc.Fail(err)
			}
			continue
		}
		line64, _ := d.InputPos()
		line := int64(line64)
		if line <= prevLine {
			line = prevLine + 1
		}
		prevLine = line

		p := &frebReq{line: line}
		if err := f.request(se, p); err != nil {
			if isSyntaxErr(err) {
				cut = true
				break loop
			}
			return acc.Fail(err)
		}
		if err := acc.Row(line); err != nil {
			return acc.Fail(err)
		}
		evs, reason, err := p.rows(rc, opts)
		if err != nil {
			return acc.Fail(err)
		}
		if reason != "" {
			acc.Reject(line, reason)
			continue
		}
		if err := acc.CheckEmit(len(evs)); err != nil {
			return acc.Fail(err)
		}
		for _, ev := range evs {
			if err := emit(ev); err != nil {
				return acc.Fail(err)
			}
			acc.Emitted(ev.Start)
			acc.Rep.Cover(ev.End)
		}
	}
	if acc.Rep.Rows == 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: frebWhyNoRequest})
	}
	acc.Gap(gapFREBOnlyFailures)
	if acc.Rep.Emitted > 0 {
		acc.Gap(gapFREBSlowModule)
	}
	if cut {
		acc.Gap(gapFREBCut)
	}
	return acc.Finish()
}

// rows turns a finished request into its events. A non-empty reason rejects the
// request; a non-nil error ends the file.
func (p *frebReq) rows(c *rowCtx, opts importer.Options) ([]evidence.Event, importer.Reason, error) {
	if p.over {
		return nil, importer.ReasonFieldCount, nil
	}
	if p.bad != "" {
		return nil, p.bad, nil
	}
	if !p.hasTime {
		return nil, importer.ReasonBadTime, nil
	}
	attrs := make(map[string]string, 8)
	status, ok := parseUint(strings.TrimSpace(p.status), 599)
	if !ok || status < 100 {
		return nil, importer.ReasonBadNumber, nil
	}
	attrs["status"] = itoa(status)
	if v := strings.TrimSpace(p.trigger); v != "" {
		n, ok := parseUint(v, 599)
		if !ok || n < 100 {
			return nil, importer.ReasonBadNumber, nil
		}
		attrs["trigger"] = itoa(n)
	}
	end := p.last
	var dur *float64
	if v := strings.TrimSpace(p.timeTaken); v != "" {
		n, ok := parseUint(v, 4294967295)
		if !ok {
			return nil, importer.ReasonBadNumber, nil
		}
		ms := float64(n)
		dur = &ms
		end = p.first.Add(time.Duration(n) * time.Millisecond)
	}
	if p.subStatus != "" {
		attrs["substatus"] = p.subStatus
	}
	if p.win32 != "" {
		attrs["win32"] = p.win32
	}
	attrs["reason"] = "other"
	if w, ok := frebWord(p.reason, 40, false); ok {
		attrs["reason"] = strings.ToUpper(w)
	}
	if m, ok := frebWord(p.verb, 20, false); ok {
		attrs["method"] = strings.ToUpper(m)
	}
	if s, ok := frebPath(p.url); ok {
		attrs["stem"] = s
	}

	if p.hostBad {
		return nil, importer.ReasonUnknownHost, nil
	}
	host, reason, err := c.resolveHost(p.computer)
	if err != nil || reason != "" {
		return nil, reason, err
	}

	mk := func(ordinal int, kind string, start, end time.Time, a map[string]string, d *float64) evidence.Event {
		ev := wincsv.NewEvent(opts, frebName, p.line, ordinal)
		ev.Kind, ev.Host, ev.Start, ev.End, ev.Attrs, ev.DurationMs = kind, host, start, end, a, d
		ev.TZRule = clock.RuleUTCField
		return ev
	}
	out := []evidence.Event{mk(0, "failed-request", p.first, end, attrs, dur)}

	slow := make([]*slowest, 0, len(p.keys))
	for _, s := range p.keys {
		slow = append(slow, s)
	}
	sort.Slice(slow, func(i, j int) bool {
		if slow[i].gap != slow[j].gap {
			return slow[i].gap > slow[j].gap
		}
		if slow[i].module != slow[j].module {
			return slow[i].module < slow[j].module
		}
		return slow[i].notif < slow[j].notif
	})
	if len(slow) > frebTopModules {
		slow = slow[:frebTopModules]
	}
	for i, s := range slow {
		a := map[string]string{"module": s.module}
		if s.notif != "" {
			a["notification"] = s.notif
		}
		ms := float64(s.gap) / float64(time.Millisecond)
		out = append(out, mk(i+1, "slow-module", s.from, s.to, a, &ms))
	}
	return out, "", nil
}
