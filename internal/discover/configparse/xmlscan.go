// Package configparse holds streaming extractors that read a config file for
// log paths and nothing else. Each extractor names the few attributes it keeps
// and drops every other value as it reads: connection strings, passwords, user
// names, bindings and host names never reach a struct field, a log line or an
// error. Every extractor refuses a DOCTYPE or any other directive (so no entity
// can be declared), nesting deeper than MaxDepth and input larger than MaxBytes.
package configparse

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

// Overlong stands in for a kept attribute whose value was longer than the
// extractor will hold. It carries a control character, so a path check refuses
// it.
const Overlong = "\x00overlong"

// maxValue is the longest kept attribute value in bytes.
const maxValue = 1024

// capReader fails with ErrTooLarge once more than MaxBytes have been read.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	room := MaxBytes + 1 - c.n
	if room <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > MaxBytes {
		return 0, ErrTooLarge
	}
	return n, err
}

// scan walks the elements of an XML document and calls visit for each start
// element with the names of its ancestors and itself (local names, namespaces
// ignored) and its attributes. visit must copy only the values it keeps. The
// path slice is reused: visit must not keep it.
func scan(r io.Reader, visit func(path []string, attrs []xml.Attr)) error {
	br := bufio.NewReader(&capReader{r: r})
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}
	dec := xml.NewDecoder(br)
	dec.Strict = true
	var path []string
	seen := false
	for {
		tok, err := dec.Token()
		if err != nil {
			switch {
			case err == io.EOF:
				if !seen {
					return ErrSyntax
				}
				return nil
			case errors.Is(err, ErrTooLarge):
				return ErrTooLarge
			default:
				return ErrSyntax
			}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			seen = true
			if len(path) >= MaxDepth {
				return ErrDepth
			}
			path = append(path, t.Name.Local)
			visit(path, t.Attr)
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.Directive:
			return ErrDTD
		}
	}
}

// pathIs reports whether path equals want exactly.
func pathIs(path []string, want ...string) bool {
	if len(path) != len(want) {
		return false
	}
	for i := range want {
		if path[i] != want[i] {
			return false
		}
	}
	return true
}

// clip returns v, or Overlong when it is longer than maxValue.
func clip(v string) string {
	if len(v) > maxValue {
		return Overlong
	}
	return v
}
