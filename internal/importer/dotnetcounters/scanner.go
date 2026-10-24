package dotnetcounters

import (
	"bufio"

	"api-audit-explorer/internal/importer"
)

// scanner is the small pull parser the JSON path uses. A dotnet-counters JSON
// file is one line, however long, so the line limit cannot be enforced per line:
// it is enforced per value instead (an event object, a string, a skipped member),
// and a value over MaxLineBytes refuses the file with the same LimitError a long
// CSV line gets. Memory is bounded by one value, never by the file.
type scanner struct {
	br   *bufio.Reader
	line int64 // newlines consumed so far
	max  int
	buf  []byte
}

func (s *scanner) readByte() (byte, error) {
	b, err := s.br.ReadByte()
	if err == nil && b == '\n' {
		s.line++
	}
	return b, err
}

func (s *scanner) unread(b byte) {
	if s.br.UnreadByte() == nil && b == '\n' {
		s.line--
	}
}

func (s *scanner) limit() error {
	return &importer.LimitError{Limit: "line-bytes", Max: int64(s.max), At: s.line + 1}
}

func (s *scanner) skipSpace() (byte, error) {
	for {
		b, err := s.readByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return b, nil
	}
}

// readString reads the rest of a string whose opening quote is consumed. It
// returns at most keep bytes of it, escapes left as written.
func (s *scanner) readString(keep int) (string, error) {
	var out []byte
	n, esc := 0, false
	for {
		b, err := s.readByte()
		if err != nil {
			return "", err
		}
		if n++; n > s.max {
			return "", s.limit()
		}
		switch {
		case esc:
			esc = false
		case b == '\\':
			esc = true
		case b == '"':
			return string(out), nil
		}
		if len(out) < keep {
			out = append(out, b)
		}
	}
}

// value reads one JSON value whose first byte is consumed. With keep it returns
// the raw bytes (valid until the next call); without, it only consumes them.
// Bracket matching is by depth alone: malformed text is the decoder's to reject.
func (s *scanner) value(first byte, keep bool) ([]byte, error) {
	s.buf = s.buf[:0]
	n := 1
	if n > s.max {
		return nil, s.limit()
	}
	if keep {
		s.buf = append(s.buf, first)
	}
	add := func(b byte) error {
		if n++; n > s.max {
			return s.limit()
		}
		if keep {
			s.buf = append(s.buf, b)
		}
		return nil
	}
	switch first {
	case '"', '{', '[':
		depth, inStr, esc := 1, first == '"', false
		if first == '"' {
			depth = 0
		}
		for inStr || depth > 0 {
			b, err := s.readByte()
			if err != nil {
				return nil, err
			}
			if err := add(b); err != nil {
				return nil, err
			}
			if inStr {
				switch {
				case esc:
					esc = false
				case b == '\\':
					esc = true
				case b == '"':
					inStr = false
				}
				continue
			}
			switch b {
			case '"':
				inStr = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	case ',', ']', '}', ':':
		return nil, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	default: // number, true, false, null
		for {
			b, err := s.readByte()
			if err != nil {
				return s.buf, err
			}
			switch b {
			case ' ', '\t', '\r', '\n', ',', ']', '}':
				s.unread(b)
				return s.buf, nil
			}
			if err := add(b); err != nil {
				return nil, err
			}
		}
	}
	return s.buf, nil
}
