package configparse

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Decision is what a JSONFilter says about one value.
type Decision int

const (
	// Skip drops the value. A container is consumed without being walked into.
	Skip Decision = iota
	// Descend walks into a container. For a scalar it means Skip.
	Descend
	// Keep passes a string value to the validator and stores what it returns. For
	// a container or a non-string it means Skip.
	Keep
	// Mark records that the key exists, nothing more, and skips the value. It is
	// for boolean-only presence checks such as an ApplicationInsights section.
	Mark
)

// JSONFilter says what a JSON parse keeps. Keys are flattened the way .NET
// configuration flattens them: lower case, segments joined with ":", array
// elements by index ("serilog:writeto:0:args:path").
type JSONFilter struct {
	// Decide is called for every value, containers included, with the flattened
	// key. The root has the key "".
	Decide func(key string, container bool) Decision
	// Validate turns the raw string of a Keep value into the value to store (a
	// clean path, a fixed flag). ok is false to drop it; code, when set, records
	// a Reject. Validate must not retain raw.
	Validate func(key, raw string) (value, code string, ok bool)
}

// JSONResult holds the flattened allowlisted values. Later duplicates win, as in
// .NET configuration.
type JSONResult struct {
	Values  map[string]string
	Present map[string]bool
	Rejects []Reject
}

// ParseJSON streams data with json.Decoder tokens. It never builds a generic
// value: every token not selected by the filter is read and discarded. Comments
// and trailing commas, which .NET configuration accepts, are tolerated.
func ParseJSON(data []byte, f JSONFilter) (*JSONResult, error) {
	data, err := toUTF8(data)
	if err != nil {
		return nil, err
	}
	data, err = stripJSONC(data)
	if err != nil {
		return nil, err
	}
	p := &jsonParser{
		dec: json.NewDecoder(bytes.NewReader(data)),
		f:   f,
		res: &JSONResult{Values: map[string]string{}, Present: map[string]bool{}},
	}
	if err := p.value("", 0); err != nil {
		return nil, err
	}
	// Anything after the root value other than whitespace is a syntax error.
	if _, terr := p.dec.Token(); terr != io.EOF {
		return nil, ErrSyntax
	}
	return p.res, nil
}

type jsonParser struct {
	dec   *json.Decoder
	f     JSONFilter
	res   *JSONResult
	count int
}

func (p *jsonParser) token() (json.Token, error) {
	t, err := p.dec.Token()
	if err != nil {
		return nil, ErrSyntax
	}
	return t, nil
}

func (p *jsonParser) value(key string, depth int) error {
	tok, err := p.token()
	if err != nil {
		return err
	}
	d, container := json.Delim(0), false
	if dl, ok := tok.(json.Delim); ok {
		d, container = dl, dl == '{' || dl == '['
	}
	dec := Skip
	if p.f.Decide != nil {
		dec = p.f.Decide(key, container)
	}
	if container {
		if depth >= MaxDepth {
			return ErrDepth
		}
		if dec == Mark {
			p.res.Present[key] = true
		}
		if dec != Descend {
			return p.skip(d, depth)
		}
		return p.walk(key, d, depth+1)
	}
	switch dec {
	case Mark:
		p.res.Present[key] = true
	case Keep:
		s, ok := tok.(string)
		if !ok || p.f.Validate == nil {
			return nil
		}
		if p.count++; p.count > maxNodes {
			return ErrTooMany
		}
		v, code, keep := p.f.Validate(key, s)
		if code != "" {
			p.res.Rejects = append(p.res.Rejects, Reject{Attr: lastSegment(key), Key: key, Code: code})
		}
		if keep {
			p.res.Values[key] = v
		}
	}
	return nil
}

func (p *jsonParser) walk(key string, open json.Delim, depth int) error {
	if open == '[' {
		for i := 0; p.dec.More(); i++ {
			if err := p.value(joinKey(key, strconv.Itoa(i)), depth); err != nil {
				return err
			}
		}
	} else {
		for p.dec.More() {
			kt, err := p.token()
			if err != nil {
				return err
			}
			name, ok := kt.(string)
			if !ok {
				return ErrSyntax
			}
			if err := p.value(joinKey(key, strings.ToLower(name)), depth); err != nil {
				return err
			}
		}
	}
	_, err := p.token() // the closing delimiter
	return err
}

// skip consumes a container whose opening delimiter was already read.
func (p *jsonParser) skip(open json.Delim, depth int) error {
	level := 1
	for level > 0 {
		t, err := p.token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			switch d {
			case '{', '[':
				level++
				if depth+level > MaxDepth {
					return ErrDepth
				}
			case '}', ']':
				level--
			}
		}
	}
	return nil
}

func joinKey(parent, seg string) string {
	// A property name that holds colons stays one segment here and still reads
	// as a flattened key.
	if parent == "" {
		return seg
	}
	return parent + ":" + seg
}

func lastSegment(key string) string {
	if i := strings.LastIndexByte(key, ':'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// stripJSONC removes // and /* */ comments and trailing commas, outside strings,
// so encoding/json can read what .NET's configuration reader accepts. It makes
// one pass and returns ErrSyntax for an unterminated comment.
func stripJSONC(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	inStr, esc := false, false
	lastSig := -1 // index in out of the last byte that is not whitespace
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
			lastSig = len(out) - 1
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			if i < len(in) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			end := bytes.Index(in[i+2:], []byte("*/"))
			if end < 0 {
				return nil, ErrSyntax
			}
			i += 2 + end + 1
			out = append(out, ' ')
		case c == '}' || c == ']':
			if lastSig >= 0 && out[lastSig] == ',' {
				out[lastSig] = ' '
			}
			out = append(out, c)
			lastSig = len(out) - 1
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			out = append(out, c)
		default:
			out = append(out, c)
			lastSig = len(out) - 1
		}
	}
	if inStr {
		return nil, ErrSyntax
	}
	return out, nil
}
