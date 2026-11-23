package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
	obj bool
	n   int  // members written so far
	key bool // an object frame: the next string is a member name
}

// JSON returns raw with every string value and every member name swept (see Text). Numbers, booleans and null are kept, except a
// number of minLearnLen digits or more that equals a learned value, which becomes the token as a string. When nothing changed it
// returns raw itself, byte for byte (so the hash of an unchanged result does not move). When something changed the result is
// compact JSON with the members in their original order. Invalid JSON, text after the value, nesting over maxDepth or a text over
// MaxJSONBytes is an error and the caller must not pass the text on.
func (s *Sweeper) JSON(raw []byte) ([]byte, error) {
	if len(raw) > MaxJSONBytes {
		return nil, ErrTooLarge
	}
	if s.allow {
		if !json.Valid(raw) {
			return nil, ErrBadJSON
		}
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	out.Grow(len(raw))
	enc := json.NewEncoder(&out)
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
		case json.Delim:
			switch v {
			case '{', '[':
				before()
				if len(st) >= maxDepth {
					return nil, ErrTooDeep
				}
				out.WriteByte(byte(v))
				st = append(st, frame{obj: v == '{', key: v == '{'})
			default: // '}' or ']'
				if len(st) == 0 {
					return nil, ErrBadJSON
				}
				st = st[:len(st)-1]
				out.WriteByte(byte(v))
				after()
			}
		case string:
			sw := s.textSwept(v)
			if sw != v {
				changed = true
			}
			if len(st) > 0 && st[len(st)-1].obj && st[len(st)-1].key {
				f := &st[len(st)-1]
				if f.n > 0 {
					out.WriteByte(',')
				}
				writeString(sw)
				out.WriteByte(':')
				f.key = false
				continue
			}
			before()
			writeString(sw)
			after()
		case json.Number:
			before()
			if t, ok := s.numberToken(string(v)); ok {
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

func (s *Sweeper) textSwept(v string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textLocked(v)
}

func (s *Sweeper) numberToken(num string) (string, bool) {
	if len(num) < minLearnLen {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.exact[num]
	if !ok {
		return "", false
	}
	return s.tokenLocked(k, num), true
}
