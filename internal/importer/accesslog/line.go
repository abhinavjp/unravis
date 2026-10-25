package accesslog

import (
	"strconv"
	"strings"
	"time"
)

// line is one access-log line cut into the parts the importers read. The
// client address, the ident token, the referrer and the user agent are walked
// over and never copied: they have no field here.
type line struct {
	user     string // %u / $remote_user; "-" when no user
	ts       string // text between [ and ]
	req      string // request line, between the quotes, as written
	status   string
	bytes    string // %b / $body_bytes_sent
	combined bool   // referrer and user agent fields were present
	trailing string // text after the last known field; "" when none
}

// cut splits s at its first space. more is false when there is no space.
func cut(s string) (tok, rest string, more bool) {
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// quoted reads a double-quoted field at the start of s. A backslash escapes
// the next byte (nginx writes \x22, Apache \"; both keep the quote inside).
func quoted(s string) (body, rest string, ok bool) {
	if s == "" || s[0] != '"' {
		return "", "", false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[1:i], s[i+1:], true
		}
	}
	return "", "", false
}

// scan cuts a line of the nginx "combined" format or the Apache "common" or
// "combined" format, with at most one trailing field group. ok is false for
// any other shape (a custom log_format, a cut line, text that is not an
// access log); nothing is guessed.
//
//	host ident user [time] "request" status bytes ["referrer" "agent"] [trailing]
func scan(s string) (l line, ok bool) {
	var more bool
	var tok string
	// client address and ident: skipped, never read into a value
	if tok, s, more = cut(s); !more || tok == "" {
		return l, false
	}
	if _, s, more = cut(s); !more {
		return l, false
	}
	if l.user, s, more = cut(s); !more || l.user == "" {
		return l, false
	}
	if s == "" || s[0] != '[' {
		return l, false
	}
	i := strings.IndexByte(s, ']')
	if i < 0 || i > 64 {
		return l, false
	}
	l.ts, s = s[1:i], s[i+1:]
	if s == "" || s[0] != ' ' {
		return l, false
	}
	if l.req, s, ok = quoted(s[1:]); !ok || s == "" || s[0] != ' ' {
		return l, false
	}
	if l.status, s, more = cut(s[1:]); !more || l.status == "" {
		return l, false
	}
	if l.bytes, s, more = cut(s); l.bytes == "" {
		return l, false
	}
	if !more {
		return l, true
	}
	if s != "" && s[0] == '"' {
		// "referrer" "agent": walked over, never stored
		if _, s, ok = quoted(s); !ok || s == "" || s[0] != ' ' {
			return l, false
		}
		if _, s, ok = quoted(s[1:]); !ok {
			return l, false
		}
		l.combined = true
		switch {
		case s == "":
		case s[0] == ' ':
			l.trailing = s[1:]
		default:
			return l, false
		}
		return l, true
	}
	l.trailing = s
	return l, true
}

// Trailing field shapes.
const (
	shapeNone    = iota // nothing after the known fields
	shapeSeconds        // nginx $request_time [$upstream_response_time]
	shapeWhole          // Apache %D or %T: one whole number
	shapeOther          // anything else: an unsupported log format
)

// decimalSeconds reports whether s is a decimal number of seconds such as
// 0.123, optionally in double quotes (nginx always writes three decimals; up
// to nine are read). It returns the value without the quotes.
func decimalSeconds(s string) (string, bool) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	whole, frac, found := strings.Cut(s, ".")
	if !found || whole == "" || frac == "" || len(whole) > 7 || len(frac) > 9 || !digits(whole) || !digits(frac) {
		return "", false
	}
	return s, true
}

// upstreamOK reports whether s is what $upstream_response_time writes: "-" or
// seconds, several joined by ", " or " : ", the whole optionally quoted. The
// value itself is not stored.
func upstreamOK(s string) bool {
	if len(s) > 128 {
		return false
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	for s != "" {
		var part string
		switch {
		case strings.Contains(s, ", "):
			part, s, _ = strings.Cut(s, ", ")
		case strings.Contains(s, " : "):
			part, s, _ = strings.Cut(s, " : ")
		default:
			part, s = s, ""
		}
		if part == "-" {
			continue
		}
		if _, ok := decimalSeconds(part); !ok {
			return false
		}
	}
	return true
}

// trailingShape classifies the trailing text of a line.
func trailingShape(t string) (shape int, requestTime string) {
	if t == "" {
		return shapeNone, ""
	}
	first, rest, more := cut(t)
	if v, ok := decimalSeconds(first); ok {
		if !more || upstreamOK(rest) {
			return shapeSeconds, v
		}
		return shapeOther, ""
	}
	if !more && len(t) <= 12 && digits(t) {
		return shapeWhole, t
	}
	return shapeOther, ""
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// uintOf parses a short plain decimal; ok is false past max or for any
// character that is not a digit.
func uintOf(s string, max uint64) (uint64, bool) {
	if s == "" || len(s) > 10 || !digits(s) {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > max {
		return 0, false
	}
	return n, true
}

const timeLayout = "02/Jan/2006:15:04:05 -0700"

// parseTime reads "10/Oct/2000:13:55:36 -0700" into UTC. The offset is
// required, signed, four digits, within -1400 to +1400 and minutes under 60;
// anything else is refused rather than read as UTC.
func parseTime(s string) (time.Time, bool) {
	if len(s) != len(timeLayout) || (s[21] != '+' && s[21] != '-') || !digits(s[22:]) {
		return time.Time{}, false
	}
	hh, _ := strconv.Atoi(s[22:24])
	mm, _ := strconv.Atoi(s[24:26])
	if hh > 14 || mm > 59 || (hh == 14 && mm > 0) {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, false
	}
	t = t.UTC()
	if y := t.Year(); y < minLogYear || y > maxLogYear {
		return time.Time{}, false
	}
	return t, true
}

// Years outside this range are corrupt dates, not log times (as in iis-w3c).
const (
	minLogYear = 1990
	maxLogYear = 2100
)

// requestParts splits a request line into method and target. A line that is
// not "METHOD target" or "METHOD target HTTP/x" (a "-" from a timed-out
// connection, TLS bytes sent to a plain port) gives ok false.
func requestParts(req string) (method, target string, ok bool) {
	f := strings.Fields(req)
	switch len(f) {
	case 2:
		return f[0], f[1], true
	case 3:
		if strings.HasPrefix(f[2], "HTTP/") {
			return f[0], f[1], true
		}
	}
	return "", "", false
}
