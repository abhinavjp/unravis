package log4net

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
)

// A conversionPattern is read into one anchored regular expression that matches a
// header line. Free-text tokens (thread, NDC, property, message, exception and the
// dropped location and user tokens) match lazily up to the literal that follows
// them, so the expression is only as exact as the literals around those tokens.
//
// Refused (the whole file, as unsupported-format): a token that is not in the
// table below, %timestamp and %r (milliseconds since start: no absolute time), a
// pattern with no date token, a repeated date, level, logger, message or exception
// token, a date format that is not made of the specifiers listed at dateFormat,
// anything but %exception after the first %newline, and a pattern longer than
// maxPatternBytes. The pattern text never reaches an error or a report.

// maxPatternBytes bounds the owner's "pattern" setting (plan section 5).
const maxPatternBytes = 512

const (
	maxPatternPieces = 64
	maxOptionBytes   = 64
	maxPadDigits     = 3
)

// Why a pattern was refused. They are fixed words for tests and never carry
// pattern text.
const (
	whyLength    = "pattern-length"
	whyChars     = "pattern-chars"
	whySyntax    = "pattern-syntax"
	whyToken     = "pattern-token"
	whyTimestamp = "pattern-timestamp"
	whyNoDate    = "pattern-no-date"
	whyDuplicate = "pattern-duplicate"
	whyDateFmt   = "pattern-date-format"
	whyNewline   = "pattern-newline"
)

type kind int

const (
	kDate kind = iota + 1
	kUTCDate
	kThread
	kLevel
	kLogger
	kNDC
	kProp
	kMessage
	kNewline
	kException
	kDrop    // matched and dropped: user, location and similar
	kRefuseT // %timestamp, %r
)

// names maps a converter name to its kind. A name of one letter is case
// sensitive (%p is the level, %P a property); longer names are compared in lower
// case. verify: the log4net names and their case rules were taken from the
// log4net PatternLayout documentation as remembered; none was checked against a
// release.
var names = map[string]kind{
	"date": kDate, "d": kDate, "utcdate": kUTCDate,
	"thread": kThread, "t": kThread,
	"level": kLevel, "p": kLevel,
	"logger": kLogger, "c": kLogger,
	"ndc": kNDC, "x": kNDC,
	"property": kProp, "properties": kProp, "P": kProp,
	"message": kMessage, "m": kMessage,
	"newline": kNewline, "n": kNewline,
	"exception": kException,
	"username":  kDrop, "identity": kDrop, "location": kDrop, "file": kDrop, "line": kDrop,
	"method": kDrop, "class": kDrop, "type": kDrop, "appdomain": kDrop, "mdc": kDrop,
	"w": kDrop, "u": kDrop, "l": kDrop, "F": kDrop, "L": kDrop, "M": kDrop, "C": kDrop, "a": kDrop, "X": kDrop,
	"timestamp": kRefuseT, "r": kRefuseT,
}

func lookup(name string) (kind, bool) {
	if len(name) == 1 {
		k, ok := names[name]
		return k, ok
	}
	k, ok := names[strings.ToLower(name)]
	return k, ok
}

type piece struct {
	lit        string // literal text, when tok is zero
	tok        kind
	opt        string
	minW, maxW int
	left       bool
}

// lex cuts the pattern into literals and tokens.
func lex(p string) ([]piece, string) {
	if len(p) == 0 || len(p) > maxPatternBytes {
		return nil, whyLength
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return nil, whyChars
		}
	}
	var out []piece
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, piece{lit: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(p); {
		c := p[i]
		if c != '%' {
			lit.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(p) {
			return nil, whySyntax
		}
		if p[i] == '%' {
			lit.WriteByte('%')
			i++
			continue
		}
		flush()
		var pc piece
		if p[i] == '-' {
			pc.left = true
			i++
		}
		j := i
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if j-i > maxPadDigits {
			return nil, whySyntax
		}
		if j > i {
			pc.minW, _ = strconv.Atoi(p[i:j])
		}
		i = j
		if i < len(p) && p[i] == '.' {
			i++
			j = i
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				j++
			}
			if j == i || j-i > maxPadDigits {
				return nil, whySyntax
			}
			pc.maxW, _ = strconv.Atoi(p[i:j])
			i = j
		}
		j = i
		for j < len(p) && (p[j] >= 'a' && p[j] <= 'z' || p[j] >= 'A' && p[j] <= 'Z') {
			j++
		}
		if j == i {
			return nil, whySyntax
		}
		k, ok := lookup(p[i:j])
		if !ok {
			return nil, whyToken
		}
		pc.tok = k
		i = j
		if i < len(p) && p[i] == '{' {
			end := strings.IndexByte(p[i:], '}')
			if end < 0 || end-1 > maxOptionBytes {
				return nil, whySyntax
			}
			pc.opt = p[i+1 : i+end]
			i += end + 1
		}
		out = append(out, pc)
		if len(out) > maxPatternPieces {
			return nil, whySyntax
		}
	}
	flush()
	if len(out) > maxPatternPieces {
		return nil, whySyntax
	}
	return out, ""
}

// header is a compiled conversionPattern.
type header struct {
	re                                   *regexp.Regexp
	date, level, logger, message, excGrp int // submatch indexes; 0 when absent
	layout                               string
	rule                                 string
	local                                bool // the date has no zone: the host zone applies
	dropsFields                          bool // thread, NDC, property or location tokens were dropped
}

// compile reads one conversionPattern. why is "" on success.
func compile(p string) (*header, string) {
	ps, why := lex(p)
	if why != "" {
		return nil, why
	}
	var b strings.Builder
	b.WriteString("^")
	h := &header{}
	groups := 0
	seen := map[kind]bool{}
	afterNL := false
	for _, pc := range ps {
		if pc.tok == 0 {
			if afterNL {
				return nil, whyNewline
			}
			b.WriteString(regexp.QuoteMeta(pc.lit))
			continue
		}
		if afterNL && pc.tok != kNewline && pc.tok != kException {
			return nil, whyNewline
		}
		switch pc.tok {
		case kRefuseT:
			return nil, whyTimestamp
		case kDate, kUTCDate, kLevel, kLogger, kMessage, kException:
			key := pc.tok
			if key == kUTCDate {
				key = kDate
			}
			if seen[key] {
				return nil, whyDuplicate
			}
			seen[key] = true
		}
		var body string
		switch pc.tok {
		case kNewline:
			afterNL = true
			continue
		case kException:
			if afterNL { // after the first %newline the exception is the continuation lines
				continue
			}
			groups++
			h.excGrp = groups
			body = `(.*?)`
		case kDate, kUTCDate:
			df, ok := dateFormat(pc.opt)
			if !ok {
				return nil, whyDateFmt
			}
			h.layout = df.layout
			h.local = !df.offset && pc.tok == kDate
			switch {
			case df.offset:
				h.rule = clock.RuleOffsetInField
			case pc.tok == kUTCDate:
				h.rule = clock.RuleUTCField
			default:
				h.rule = clock.RuleHostLocal
			}
			groups++
			h.date = groups
			body = "(" + df.re + ")"
		case kLevel:
			groups++
			h.level = groups
			body = `([A-Za-z][A-Za-z0-9_]*)`
		case kLogger:
			groups++
			h.logger = groups
			body = `(\S+)`
		case kMessage:
			groups++
			h.message = groups
			body = `(.*?)`
		case kThread, kNDC, kProp, kDrop:
			h.dropsFields = true
			body = `(?:.*?)`
		}
		if pc.minW > 0 {
			if pc.left {
				body += ` *`
			} else {
				body = ` *` + body
			}
		}
		b.WriteString(body)
	}
	if !seen[kDate] {
		return nil, whyNoDate
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, whySyntax
	}
	h.re = re
	return h, ""
}

// dateSpec is a .NET custom date format turned into a regular expression for the
// text and a Go layout for time.Parse.
type dateSpec struct {
	re, layout string
	offset     bool
}

// iso8601 is the format of %date with no option (log4net's ISO8601 name).
const iso8601 = "yyyy-MM-dd HH:mm:ss,fff"

// dateFormat reads the option of %date or %utcdate. It accepts "", "ISO8601" and
// a custom format built from these specifiers and nothing else:
//
//	yyyy MM dd HH mm ss   each exactly once
//	fff                   one to seven f, directly after ss and a "." or ","
//	zzz, K                the offset as +hh:mm (K also "Z")
//	T                     literal, bare or as 'T'
//	space - : / . , [ ]   literal
//
// Month and day names, 12-hour clocks, and short or long runs of a specifier are
// refused: the importer cannot read them without guessing the culture. verify:
// the .NET meaning of these specifiers, and ":" and "/" as the invariant
// separators, were taken from the documentation as remembered.
func dateFormat(opt string) (dateSpec, bool) {
	f := opt
	if f == "" || strings.EqualFold(f, "ISO8601") {
		f = iso8601
	}
	var re, layout strings.Builder
	parts := map[string]bool{}
	var spec dateSpec
	last := ""
	need := func(name string) bool {
		if parts[name] {
			return false
		}
		parts[name] = true
		return true
	}
	for i := 0; i < len(f); {
		c := f[i]
		run := 1
		for i+run < len(f) && f[i+run] == c {
			run++
		}
		switch c {
		case 'y':
			if run != 4 || !need("y") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{4}`)
			layout.WriteString("2006")
			last = "y"
		case 'M':
			if run != 2 || !need("M") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{2}`)
			layout.WriteString("01")
			last = "M"
		case 'd':
			if run != 2 || !need("d") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{2}`)
			layout.WriteString("02")
			last = "d"
		case 'H':
			if run != 2 || !need("H") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{2}`)
			layout.WriteString("15")
			last = "H"
		case 'm':
			if run != 2 || !need("m") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{2}`)
			layout.WriteString("04")
			last = "m"
		case 's':
			if run != 2 || !need("s") {
				return dateSpec{}, false
			}
			re.WriteString(`\d{2}`)
			layout.WriteString("05")
			last = "s"
		case 'z':
			if run != 3 || spec.offset {
				return dateSpec{}, false
			}
			spec.offset = true
			re.WriteString(`[+-]\d{2}:\d{2}`)
			layout.WriteString("-07:00")
			last = "z"
		case 'K':
			if run != 1 || spec.offset {
				return dateSpec{}, false
			}
			spec.offset = true
			re.WriteString(`(?:Z|[+-]\d{2}:\d{2})`)
			layout.WriteString("Z07:00")
			last = "K"
		case '\'':
			if i+2 < len(f) && f[i+1] == 'T' && f[i+2] == '\'' {
				re.WriteString("T")
				layout.WriteString("T")
				i += 3
				last = "T"
				continue
			}
			return dateSpec{}, false
		case '.', ',':
			if i+1 < len(f) && f[i+1] == 'f' && run == 1 {
				n := 0
				for i+1+n < len(f) && f[i+1+n] == 'f' {
					n++
				}
				if last != "s" || n > 7 || !need("f") {
					return dateSpec{}, false
				}
				re.WriteString(regexp.QuoteMeta(string(c)) + `\d{` + strconv.Itoa(n) + `}`)
				layout.WriteString(string(c) + strings.Repeat("0", n))
				i += 1 + n
				last = "f"
				continue
			}
			re.WriteString(regexp.QuoteMeta(strings.Repeat(string(c), run)))
			layout.WriteString(strings.Repeat(string(c), run))
			last = "."
		case ' ', '-', ':', '/', '[', ']', 'T':
			re.WriteString(regexp.QuoteMeta(strings.Repeat(string(c), run)))
			layout.WriteString(strings.Repeat(string(c), run))
			last = "l"
		default:
			return dateSpec{}, false
		}
		i += run
	}
	for _, p := range []string{"y", "M", "d", "H", "m", "s"} {
		if !parts[p] {
			return dateSpec{}, false
		}
	}
	spec.re, spec.layout = re.String(), layout.String()
	// The layout and the expression must agree on one sample, or the format is
	// not one this importer can read.
	ref := time.Date(2001, 10, 25, 14, 2, 31, 123456700, time.FixedZone("x", 13*3600))
	s := ref.Format(spec.layout)
	rx, err := regexp.Compile("^(?:" + spec.re + ")$")
	if err != nil || !rx.MatchString(s) {
		return dateSpec{}, false
	}
	if _, err := time.Parse(spec.layout, s); err != nil {
		return dateSpec{}, false
	}
	return spec, true
}

// defaultPatterns are tried, in this order, when the owner states none. Each is
// tried with the ISO8601 date and with the offset variant.
var defaultPatterns = []string{
	"%date [%thread] %-5level %logger - %message%newline",
	"%date [%thread] %-5level %logger [%ndc] - %message%newline",
	"%date %-5level %logger - %message%newline",
}

// offsetDate is the date option of the ISO 8601 variant with an offset.
const offsetDate = "yyyy-MM-ddTHH:mm:ss.fffzzz"

// defaults compiles the candidates once.
func defaults() []*header {
	var out []*header
	for _, p := range defaultPatterns {
		for _, rep := range []string{"%date", "%date{" + offsetDate + "}"} {
			if h, why := compile(strings.Replace(p, "%date", rep, 1)); why == "" {
				out = append(out, h)
			}
		}
	}
	return out
}

var defaultHeaders = defaults()
