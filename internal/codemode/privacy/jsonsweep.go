package privacy

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	// MaxJSONBytes is the largest JSON text SweepJSON reads: the final cap of a run is 1 MiB (spec 3.3); this leaves room for the
	// frame overhead and refuses anything bigger before it allocates.
	MaxJSONBytes = (1 << 20) + (4 << 10)
	// maxDepth is the deepest nesting SweepJSON accepts.
	maxDepth = 256
)

// Errors never carry the text.
var (
	ErrBadJSON  = errors.New("privacy: not valid JSON")
	ErrTooLarge = errors.New("privacy: JSON text too large")
	ErrTooDeep  = errors.New("privacy: JSON nested too deep")
)

type frame struct {
	obj   bool
	n     int              // members written so far
	key   bool             // an object frame: the next string is a member name
	names map[string]uint8 // names written so far: nameSeen, and nameChanged when this sweep changed it
	next  map[string]int   // per name: where the search for a free "#k" suffix goes on (so each member costs O(1) on average)
}

const (
	nameSeen    uint8 = 1
	nameChanged uint8 = 2
)

// json returns raw with every string value and every member name swept (see text); with escape, text that looks like a token
// ("[USER_1]") is first written "[_USER_1]" so data cannot pass for a token (host data on its way in; script output is not escaped). Booleans and null are kept. A number that equals a learned
// value (as written, or as an integer when it is written "1.234567e6" or "1234567.0") becomes the token as a string, however short:
// a number is always a whole value. When nothing changed it returns raw itself, byte for byte (so the hash of an unchanged result
// does not move). When something changed the result is compact JSON with the members in their original order; a changed member name
// that would repeat an earlier changed one gets "#2", "#3" ... after it, so a JSON parser that keeps the last duplicate does not
// lose a count. Invalid JSON or invalid UTF-8, text after the value, nesting over maxDepth or a text over MaxJSONBytes is an error
// and the caller must not pass the text on.
func (s *sweeper) json(raw []byte, escape bool) ([]byte, error) {
	if len(raw) > MaxJSONBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(raw) {
		return nil, ErrBadJSON
	}
	if s.allow {
		if !stdjson.Valid(raw) {
			return nil, ErrBadJSON
		}
		return raw, nil
	}
	dec := stdjson.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	out.Grow(len(raw))
	enc := stdjson.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	var st []frame
	changed := false
	topDone := false

	writeString := func(v string) {
		// Encoder adds a newline after each value; trim it after the write.
		_ = enc.Encode(v)
		out.Truncate(out.Len() - 1)
	}
	before := func() {
		if len(st) > 0 {
			if f := &st[len(st)-1]; !f.obj && f.n > 0 {
				out.WriteByte(',')
			}
		}
	}
	after := func() {
		if len(st) == 0 {
			topDone = true
			return
		}
		f := &st[len(st)-1]
		f.n++
		if f.obj {
			f.key = true
		}
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrBadJSON
		}
		if topDone {
			return nil, ErrBadJSON
		}
		switch v := tok.(type) {
		case stdjson.Delim:
			switch v {
			case '{', '[':
				before()
				if len(st) >= maxDepth {
					return nil, ErrTooDeep
				}
				out.WriteByte(delimByte(v))
				st = append(st, frame{obj: v == '{', key: v == '{'})
			default: // '}' or ']'
				if len(st) == 0 {
					return nil, ErrBadJSON
				}
				st = st[:len(st)-1]
				out.WriteByte(delimByte(v))
				after()
			}
		case string:
			// normalise first, then escape: an invisible character between "[" and "USER_1" must not hide a token look-alike from
			// the escape and then be removed
			in := normalize(v)
			if escape {
				in = escapeTokens(in)
			}
			sw := s.textSwept(in, !escape)
			if sw != v {
				changed = true
			}
			if len(st) > 0 && st[len(st)-1].obj && st[len(st)-1].key {
				f := &st[len(st)-1]
				if f.n > 0 {
					out.WriteByte(',')
				}
				if f.names == nil {
					f.names = map[string]uint8{}
					f.next = map[string]int{}
				}
				// a member name this sweep changed (or one that equals a changed one) never repeats an earlier name; unchanged
				// repeats of the input are left as they were
				was := f.names[sw]
				mine := sw != v
				if was != 0 && (mine || was&nameChanged != 0) {
					base := sw
					k := f.next[base]
					if k < 2 {
						k = 2
					}
					for f.names[base+"#"+strconv.Itoa(k)] != 0 {
						k++
					}
					f.next[base] = k + 1
					sw = base + "#" + strconv.Itoa(k)
					mine = true
					changed = true
				}
				if mine {
					f.names[sw] |= nameSeen | nameChanged
				} else {
					f.names[sw] |= nameSeen
				}
				writeString(sw)
				out.WriteByte(':')
				f.key = false
				continue
			}
			before()
			writeString(sw)
			after()
		case stdjson.Number:
			before()
			if t, ok := s.numberToken(string(v), !escape); ok {
				changed = true
				writeString(t)
			} else {
				out.WriteString(string(v))
			}
			after()
		case bool:
			before()
			if v {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
			after()
		case nil:
			before()
			out.WriteString("null")
			after()
		}
	}
	if len(st) != 0 || !topDone {
		return nil, ErrBadJSON
	}
	if !changed {
		return raw, nil
	}
	return out.Bytes(), nil
}

func (s *sweeper) textSwept(v string, out bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textLocked(v, out)
}

func (s *sweeper) numberToken(num string, out bool) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.numberKey(num)
	if !ok || (out && len(key) < minOutLen) {
		return "", false
	}
	return s.learnTokenLocked(s.exact[key], key, out), true
}

// delimByte is the byte of a JSON delimiter token.
func delimByte(d stdjson.Delim) byte {
	switch d {
	case '{':
		return '{'
	case '[':
		return '['
	case '}':
		return '}'
	}
	return ']'
}
