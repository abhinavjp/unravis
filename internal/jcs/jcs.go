// Package jcs writes JSON in the canonical form of RFC 8785 (JSON
// Canonicalization Scheme): no white space, object members sorted by the UTF-16
// code units of their names, strings escaped the way JSON.stringify escapes them
// and numbers written the way ECMAScript writes a Number. Two values that mean
// the same JSON give the same bytes, so a hash of the bytes is a hash of the
// meaning. The audit store hashes records and call arguments this way.
//
// Input is strict: invalid UTF-8, a duplicate member name, NaN or Infinity (which
// JSON cannot say anyway), a number outside the float64 range, trailing data and
// nesting deeper than MaxDepth are all refused. Numbers are read as float64, as
// the RFC says, so an integer above 2^53 is written as the nearest double.
package jcs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxDepth bounds the nesting of arrays and objects, so a hostile document
// cannot exhaust the stack.
const MaxDepth = 64

// ErrInvalid is returned for every input the canonicalizer refuses.
var ErrInvalid = errors.New("jcs: invalid JSON")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Marshal encodes v with encoding/json and returns the canonical form of the result.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return Canonicalize(raw)
}

// Canonicalize returns the canonical form of one JSON document.
func Canonicalize(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, invalid("not valid UTF-8")
	}
	if err := checkSurrogates(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	if err := writeValue(dec, &out, 0); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid("data after the value")
	}
	return out.Bytes(), nil
}

// checkSurrogates refuses a \uD800-\uDFFF escape that is not half of a valid
// pair. encoding/json folds such an escape into U+FFFD, so two different
// documents would get one canonical form and one hash; RFC 8785 requires
// well-formed (I-JSON) strings, which have no lone surrogates.
func checkSurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			inString = c == '"'
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(raw) {
				return nil // the syntax check reports it
			}
			if raw[i+1] != 'u' {
				i++
				continue
			}
			u, ok := hex4(raw, i+2)
			if !ok {
				return nil // the syntax check reports it
			}
			i += 5
			switch {
			case u >= 0xDC00 && u <= 0xDFFF:
				return invalid("lone low surrogate escape")
			case u >= 0xD800 && u <= 0xDBFF:
				if i+2 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u' {
					if low, ok := hex4(raw, i+3); ok && low >= 0xDC00 && low <= 0xDFFF {
						i += 6
						continue
					}
				}
				return invalid("lone high surrogate escape")
			}
		}
	}
	return nil
}

func hex4(raw []byte, at int) (uint16, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	var v uint16
	for _, c := range raw[at : at+4] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint16(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}

func writeValue(dec *json.Decoder, out *bytes.Buffer, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return invalid("%v", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth >= MaxDepth {
			return invalid("nesting deeper than %d", MaxDepth)
		}
		if t == '[' {
			return writeArray(dec, out, depth+1)
		}
		return writeObject(dec, out, depth+1)
	case string:
		writeString(out, t)
	case json.Number:
		s, err := Number(string(t))
		if err != nil {
			return err
		}
		out.WriteString(s)
	case bool:
		out.WriteString(strconv.FormatBool(t))
	case nil:
		out.WriteString("null")
	default:
		return invalid("unexpected token")
	}
	return nil
}

func writeArray(dec *json.Decoder, out *bytes.Buffer, depth int) error {
	out.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			out.WriteByte(',')
		}
		first = false
		if err := writeValue(dec, out, depth); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing bracket
		return invalid("%v", err)
	}
	out.WriteByte(']')
	return nil
}

type member struct {
	name  string
	units []uint16
	value []byte
}

func writeObject(dec *json.Decoder, out *bytes.Buffer, depth int) error {
	var members []member
	seen := map[string]struct{}{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return invalid("%v", err)
		}
		name, ok := tok.(string)
		if !ok {
			return invalid("object member name is not a string")
		}
		if _, dup := seen[name]; dup {
			return invalid("duplicate member name")
		}
		seen[name] = struct{}{}
		var v bytes.Buffer
		if err := writeValue(dec, &v, depth); err != nil {
			return err
		}
		members = append(members, member{name: name, units: utf16.Encode([]rune(name)), value: v.Bytes()})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return invalid("%v", err)
	}
	sort.Slice(members, func(i, j int) bool { return lessUnits(members[i].units, members[j].units) })
	out.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			out.WriteByte(',')
		}
		writeString(out, m.name)
		out.WriteByte(':')
		out.Write(m.value)
	}
	out.WriteByte('}')
	return nil
}

func lessUnits(a, b []uint16) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

const hexDigits = "0123456789abcdef"

// writeString writes s as JSON.stringify does: only the quotation mark, the
// backslash and the control characters below U+0020 are escaped.
func writeString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hexDigits[c>>4])
				out.WriteByte(hexDigits[c&15])
			} else {
				out.WriteByte(c)
			}
		}
	}
	out.WriteByte('"')
}

// Number writes the JSON number text lit the way ECMAScript's Number-to-String
// does (RFC 8785 section 3.2.2.3): the shortest digits that read back as the same
// double, no exponent between 1e-6 and 1e21, "0" for both zeros.
func Number(lit string) (string, error) {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return "", invalid("number %q is outside the double range", lit)
	}
	return FormatFloat(f)
}

// FormatFloat is Number for a float64. NaN and the infinities are refused.
func FormatFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", invalid("NaN and Infinity have no JSON form")
	}
	if f == 0 {
		return "0", nil
	}
	var sign string
	if f < 0 {
		sign = "-"
		f = -f
	}
	// 'e' with precision -1 gives the shortest digits that round-trip: d.ddde±XX.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expText, _ := strings.Cut(e, "e")
	exp, err := strconv.Atoi(expText)
	if err != nil {
		return "", invalid("number %q", e)
	}
	digits := strings.Replace(mant, ".", "", 1)
	k := len(digits)
	n := exp + 1 // the value is 0.<digits> * 10^n
	var body string
	switch {
	case k <= n && n <= 21:
		body = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		body = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		body = "0." + strings.Repeat("0", -n) + digits
	default:
		x := n - 1
		xs := "+"
		if x < 0 {
			xs, x = "-", -x
		}
		if k == 1 {
			body = digits + "e" + xs + strconv.Itoa(x)
		} else {
			body = digits[:1] + "." + digits[1:] + "e" + xs + strconv.Itoa(x)
		}
	}
	return sign + body, nil
}
