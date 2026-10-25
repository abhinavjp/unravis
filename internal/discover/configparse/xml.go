// Package configparse reads application config files for log file paths and
// nothing else (spec D3, section 2.2 "Secrets"). The parsers stream: they walk
// the document token by token and keep only the elements and attributes the
// caller names in an allowlist. Each kept value goes through a caller-supplied
// validator that returns a derived, checked value (a clean path, a fixed flag),
// so a raw config value never reaches a result. Everything else in the file
// (connection strings, keys, passwords, tokens) is read once, dropped, and
// never stored, logged or put in an error.
//
// Limits, shared by both parsers: 4 MiB of input, 64 levels of nesting, no DTD.
package configparse

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

// Limits shared by the XML and JSON parsers.
const (
	// MaxBytes is the most input a parser accepts. It equals discover.MaxConfigBytes.
	MaxBytes = 4 << 20
	// MaxDepth is the deepest nesting a parser accepts.
	MaxDepth = 64
	// maxNodes bounds the retained tree (XML) and kept values (JSON), so a file
	// full of allowlisted names cannot grow a result without limit.
	maxNodes = 20000
)

// Errors. None carries input text: the parsers drop the underlying syntax error,
// which can quote an element name or a value.
var (
	ErrTooLarge = errors.New("configparse: input is larger than the limit")
	ErrDepth    = errors.New("configparse: nesting is deeper than the limit")
	ErrDTD      = errors.New("configparse: a document type declaration is not allowed")
	ErrSyntax   = errors.New("configparse: the file is not well-formed")
	ErrTooMany  = errors.New("configparse: too many entries")
)

// Reject records a value that was dropped because the caller's validator refused
// it. It holds names from the allowlist and a short code, never the value.
type Reject struct {
	Element string // XML element name (lower case), empty for JSON
	Attr    string // attribute name (XML) or last key segment (JSON), from the allowlist
	Key     string // JSON: the flattened key, lower case, empty for XML
	Code    string // set by the validator, for example "config-unparsed" or "unsafe-path"
}

// Node is one retained element. Name is lower case with no namespace. Attrs holds
// what the validator returned, not what the file said.
type Node struct {
	Name     string
	Attrs    map[string]string
	Children []*Node
}

// Walk calls fn for n and every retained descendant, parents first. fn returns
// false to skip the subtree.
func (n *Node) Walk(fn func(*Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// XMLFilter says what an XML parse keeps.
type XMLFilter struct {
	// Attrs maps a lower-case element name to the lower-case attribute names
	// handed to Keep. An element not in the map is traversed and forgotten.
	Attrs map[string][]string
	// Keep decides what to retain for one allowlisted element. anc lists the
	// lower-case names of every ancestor element, outermost first. raw holds only
	// the allowlisted attributes. It returns the attributes to keep (derived or
	// validated values; nil drops the element) and the rejections to record.
	// Keep must not retain raw or any value in it.
	Keep func(elem string, anc []string, raw map[string]string) (kept map[string]string, rejects []Reject)
}

// XMLResult is the filtered tree. Root is a virtual node named "" whose children
// are the outermost retained elements.
type XMLResult struct {
	Root    *Node
	Rejects []Reject
}

// ParseXML walks data and returns the allowlisted elements. A DOCTYPE or any
// other directive is refused (no entity expansion or external fetch can happen),
// nesting beyond MaxDepth is refused, and so is input over MaxBytes.
func ParseXML(data []byte, f XMLFilter) (*XMLResult, error) {
	data, err := toUTF8(data)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.Entity = nil
	dec.CharsetReader = charsetReader

	res := &XMLResult{Root: &Node{}}
	var (
		names    []string // every open element, lower case
		retained = []*Node{res.Root}
		isRet    []bool // parallel to names: did this element push onto retained
		count    int
	)
	for {
		tok, terr := dec.Token()
		if terr != nil {
			if terr == io.EOF {
				break
			}
			return nil, ErrSyntax
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, ErrDTD
		case xml.StartElement:
			if len(names) >= MaxDepth {
				return nil, ErrDepth
			}
			name := strings.ToLower(t.Name.Local)
			var node *Node
			if attrNames, ok := f.Attrs[name]; ok && f.Keep != nil {
				raw := map[string]string{}
				for _, a := range t.Attr {
					an := strings.ToLower(a.Name.Local)
					for _, want := range attrNames {
						if an == want {
							raw[an] = a.Value
						}
					}
				}
				kept, rejects := f.Keep(name, names, raw)
				for _, r := range rejects {
					r.Element = name
					r.Key = ""
					res.Rejects = append(res.Rejects, r)
				}
				if len(res.Rejects) > maxNodes {
					return nil, ErrTooMany
				}
				if kept != nil {
					count++
					if count > maxNodes {
						return nil, ErrTooMany
					}
					node = &Node{Name: name, Attrs: kept}
					parent := retained[len(retained)-1]
					parent.Children = append(parent.Children, node)
				}
			}
			names = append(names, name)
			if node != nil {
				retained = append(retained, node)
				isRet = append(isRet, true)
			} else {
				isRet = append(isRet, false)
			}
		case xml.EndElement:
			if len(names) == 0 {
				return nil, ErrSyntax
			}
			if isRet[len(isRet)-1] {
				retained = retained[:len(retained)-1]
			}
			names = names[:len(names)-1]
			isRet = isRet[:len(isRet)-1]
		}
	}
	if len(names) != 0 {
		return nil, ErrSyntax
	}
	return res, nil
}

// toUTF8 checks the size cap and converts a UTF-16 document (BOM present) or a
// UTF-8 document with a BOM to plain UTF-8.
func toUTF8(data []byte) ([]byte, error) {
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:], nil
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		le := data[0] == 0xFF
		body := data[2:]
		u := make([]uint16, 0, len(body)/2)
		for i := 0; i+1 < len(body); i += 2 {
			if le {
				u = append(u, uint16(body[i])|uint16(body[i+1])<<8)
			} else {
				u = append(u, uint16(body[i])<<8|uint16(body[i+1]))
			}
		}
		return []byte(string(utf16.Decode(u))), nil
	}
	return data, nil
}

// charsetReader accepts the encodings a Windows config file declares. A UTF-16
// document was already converted by toUTF8, so its declaration passes through.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii", "utf-16", "utf-16le", "utf-16be", "unicode":
		return in, nil
	case "iso-8859-1", "latin1", "windows-1252":
		b, err := io.ReadAll(io.LimitReader(in, MaxBytes+1))
		if err != nil {
			return nil, ErrSyntax
		}
		out := make([]rune, len(b))
		for i, c := range b {
			out[i] = rune(c)
		}
		return strings.NewReader(string(out)), nil
	}
	return nil, ErrSyntax
}
