package log4net

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/wincsv"
)

// The log4j fragment has no root element and uses the log4j prefix without
// declaring it, so the decoder reads the file between a synthetic root that
// declares both prefixes. The prefix text holds no newline, so the decoder's line
// numbers are the file's. verify: the two namespace URIs were taken from the
// log4j and log4net documentation as remembered; elements are matched by local
// name, so a different URI does not matter.
const (
	xmlOpen = `<root xmlns:log4j="http://jakarta.apache.org/log4j/" xmlns:log4net="http://logging.apache.org/log4net/">`
	xmlShut = `</root>`

	maxXMLDepth     = 64
	maxHostNameLen  = 255
	capSlack        = 8192 // the decoder reads ahead at most 4 KiB
	tokenCheckEvery = 4096
)

// Fixed "why" words for whole-file refusals of the XML importer.
const (
	whyDoctype = "xml-doctype"
	whyDepth   = "xml-depth"
	whyNoEvent = "xml-no-event"
)

// NewXML returns the log4net XmlLayoutSchemaLog4j and XmlLayout importer.
func NewXML() importer.Importer { return &xmlImp{} }

type xmlImp struct{}

func (*xmlImp) Name() string { return NameXML }

var xmlEventRe = regexp.MustCompile(`<(?:log4j:|log4net:)?event\s[^>]*\btimestamp\s*=`)

// Detect scores 90 when the head starts with "<" and holds an event start tag
// that has a timestamp attribute.
func (*xmlImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] != '<' {
		return 0
	}
	if xmlEventRe.MatchString(s) {
		return 90
	}
	return 0
}

func (*xmlImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*xmlImp) DefaultLimits() importer.Limits { return (&textImp{}).DefaultLimits() }

// capReader fails once more than max+capSlack bytes passed since base, so one
// element, comment or text node cannot grow without bound inside the decoder.
type capReader struct {
	r         io.Reader
	pos, base int64
	max       int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.pos += int64(n)
	if c.pos-c.base > c.max+capSlack {
		return n, &importer.LimitError{Limit: "line-bytes", Max: c.max, At: c.pos - c.base}
	}
	return n, err
}

// utf8Reader drops a UTF-8 BOM and transcodes UTF-16 to UTF-8.
func utf8Reader(r io.Reader) (io.Reader, error) {
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

var errDirective = &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: whyDoctype}

// skipElem reads to the end of the element whose start tag was just returned.
func skipElem(d *xml.Decoder) error {
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errDirective
		}
	}
	return nil
}

// elemText returns the text of the element whose start tag was just returned,
// nested elements skipped. capped is true when text beyond maxExcBytes was dropped.
func elemText(d *xml.Decoder) (s string, capped bool, err error) {
	var buf []byte
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return "", false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.Directive:
			return "", false, errDirective
		case xml.CharData:
			if depth != 1 {
				continue
			}
			if len(buf)+len(t) > maxExcBytes {
				capped = true
				t = t[:maxExcBytes-len(buf)]
			}
			buf = append(buf, t...)
		}
	}
	return string(buf), capped, nil
}

// xevent is what is kept of one event element before it becomes a Record.
type xevent struct {
	logger, level, stamp, host string
	msg, exc                   string
	capped                     bool
}

func attr(se xml.StartElement, name string) (string, bool) {
	for _, a := range se.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value, true
		}
	}
	return "", false
}

// isHostProp reports a property name that holds the machine name. verify: the
// two names come from the plan; the log4net HostName and log4j machine name
// property names were not checked against a release.
func isHostProp(name string) bool {
	return strings.EqualFold(name, "log4jmachinename") || strings.EqualFold(name, "log4net:HostName")
}

// readEvent reads one event element whose start tag is se. Thread, NDC, location
// info, user and identity properties are read past and dropped.
func readEvent(d *xml.Decoder, se xml.StartElement) (xevent, error) {
	var ev xevent
	ev.logger, _ = attr(se, "logger")
	ev.level, _ = attr(se, "level")
	ev.stamp, _ = attr(se, "timestamp")
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return ev, err
		}
		switch t := tok.(type) {
		case xml.Directive:
			return ev, errDirective
		case xml.EndElement:
			if depth == 0 {
				return ev, nil
			}
			depth--
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			var capped bool
			switch {
			case depth == 0 && name == "message":
				ev.msg, capped, err = elemText(d)
			case depth == 0 && (name == "throwable" || name == "exception"):
				ev.exc, capped, err = elemText(d)
			case depth == 0 && name == "properties":
				depth++
			case depth == 1 && name == "data":
				if n, _ := attr(t, "name"); isHostProp(n) && ev.host == "" {
					ev.host, _ = attr(t, "value")
				}
				err = skipElem(d)
			default:
				err = skipElem(d)
			}
			if err != nil {
				return ev, err
			}
			ev.capped = ev.capped || capped
		}
	}
}

// parseStamp reads the timestamp attribute: epoch milliseconds (log4j schema, UTC)
// or ISO 8601 with an offset (native layout). verify: both forms were taken from
// the log4j and log4net documentation as remembered.
func parseStamp(s string) (time.Time, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 40 {
		return time.Time{}, "", false
	}
	digits := true
	for i := 0; i < len(s); i++ {
		digits = digits && s[i] >= '0' && s[i] <= '9'
	}
	if digits {
		if len(s) > 15 {
			return time.Time{}, "", false
		}
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, "", false
		}
		t := time.UnixMilli(ms).UTC()
		return t, clock.RuleUTCField, wincsv.SaneTime(t)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, "", false
	}
	rule := clock.RuleOffsetInField
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		rule = clock.RuleUTCField
	}
	return t.UTC(), rule, wincsv.SaneTime(t)
}

func (m *xmlImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	opts.Limits = lim
	acc := wincsv.NewAcc(NameXML, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	ur, err := utf8Reader(pr)
	if err != nil {
		return acc.Fail(err)
	}
	cr := &capReader{r: io.MultiReader(strings.NewReader(xmlOpen), ur, strings.NewReader(xmlShut)), max: int64(lim.MaxLineBytes)}
	d := xml.NewDecoder(cr)
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil } // already UTF-8
	max := int64(lim.MaxLineBytes)

	var total int64
	send := func(ev evidence.Event) error {
		total++
		if err := lim.CheckEvents(total); err != nil {
			return err
		}
		return emit(ev)
	}
	sinks := map[string]*applog.Sink{}
	sinkFor := func(h importer.HostHint) *applog.Sink {
		if s, ok := sinks[h.Label]; ok {
			return s
		}
		o := opts
		o.Host = &h
		s := applog.NewSink(NameXML, o, send)
		sinks[h.Label] = s
		return s
	}
	byName := map[string]*applog.Sink{} // observed machine name -> sink
	machines := map[string]struct{}{}   // lower-cased names seen without a host map
	// resolve picks the sink for the machine name of one event. A non-empty
	// reason rejects the event; a non-nil error ends the file.
	resolve := func(name string) (*applog.Sink, importer.Reason, error) {
		if name == "" {
			if opts.Host != nil && opts.Host.Label != "" {
				return sinkFor(*opts.Host), "", nil
			}
			return nil, "", importer.ErrUnknownHost
		}
		if len(name) > maxHostNameLen {
			return nil, importer.ReasonUnknownHost, nil
		}
		if s, ok := byName[name]; ok {
			return s, "", nil
		}
		if err := acc.Observe(name); err != nil {
			return nil, "", err
		}
		var s *applog.Sink
		if opts.Hosts != nil {
			h, ok := opts.Hosts(name)
			if !ok || h.Label == "" {
				return nil, "", importer.ErrUnknownHost
			}
			s = sinkFor(h)
		} else {
			if opts.Host == nil || opts.Host.Label == "" {
				return nil, "", importer.ErrUnknownHost
			}
			key := strings.ToLower(name)
			if _, seen := machines[key]; !seen {
				if len(machines) > 0 { // a second machine would share one label
					return nil, "", importer.ErrUnknownHost
				}
				machines[key] = struct{}{}
			}
			s = sinkFor(*opts.Host)
		}
		byName[name] = s
		return s, "", nil
	}

	var (
		prevLine int64
		cut      bool
		ruleSet  bool
		depth    int
	)
loop:
	for n := 0; ; n++ {
		if n%tokenCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return acc.Fail(err)
			}
		}
		off0 := d.InputOffset()
		cr.base = off0
		tok, err := d.Token()
		if err != nil {
			var se *xml.SyntaxError
			switch {
			case errors.Is(err, io.EOF):
				break loop
			case errors.As(err, &se):
				cut = true
				break loop
			}
			return acc.Fail(err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return acc.Fail(errDirective)
		case xml.EndElement:
			depth--
			continue
		case xml.StartElement:
			if t.Name.Local != "event" {
				if depth++; depth > maxXMLDepth {
					return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: whyDepth})
				}
				if sz := d.InputOffset() - off0; sz > max {
					return acc.Fail(&importer.LimitError{Limit: "line-bytes", Max: max, At: sz})
				}
				continue
			}
			line64, _ := d.InputPos()
			line := int64(line64)
			if line <= prevLine {
				line = prevLine + 1
			}
			prevLine = line
			ev, err := readEvent(d, t)
			if err != nil {
				var se *xml.SyntaxError
				if errors.As(err, &se) {
					cut = true
					break loop
				}
				return acc.Fail(err)
			}
			if sz := d.InputOffset() - off0; sz > max {
				return acc.Fail(&importer.LimitError{Limit: "line-bytes", Max: max, At: sz})
			}
			if e := acc.Row(line); e != nil {
				return acc.Fail(e)
			}
			if ev.capped {
				acc.Gap(GapExcCap)
			}
			ts, rule, ok := parseStamp(ev.stamp)
			if !ok {
				acc.Reject(line, importer.ReasonBadTime)
				continue
			}
			sink, why, err := resolve(strings.TrimSpace(ev.host))
			if err != nil {
				return acc.Fail(err)
			}
			if why != "" {
				acc.Reject(line, why)
				continue
			}
			if !ruleSet {
				ruleSet = true
				acc.Rep.Quality.TimezoneRule = rule
				acc.Rep.FieldRules["timestamp"] = rule
				if opts.Host != nil {
					acc.Rep.Quality.SourceVersion = opts.Host.Runtime
				}
			}
			rec := applog.Record{Time: ts, Rule: rule, Level: strings.TrimSpace(ev.level),
				Category: ev.logger, Message: ev.msg, Exception: ev.exc}
			if e := sink.Add(rec, line); e != nil {
				return acc.Fail(e)
			}
		}
	}
	if cut {
		acc.Gap(GapXMLCut)
	}
	if acc.Rep.Rows == 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: whyNoEvent})
	}
	labels := make([]string, 0, len(sinks))
	for l := range sinks {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		if e := sinks[l].Finish(&acc.Rep); e != nil {
			return acc.Fail(e)
		}
	}
	return acc.Finish()
}
