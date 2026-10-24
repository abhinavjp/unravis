// Package textio reads importer input safely: it sniffs the byte-order mark,
// decodes UTF-8, UTF-16LE and UTF-16BE to UTF-8, bounds line length, and
// reads strict CSV. Errors carry line numbers and fixed reasons, never row
// content.
package textio

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/importer"
)

// Encoding names how the input was encoded before decoding.
type Encoding string

const (
	UTF8    Encoding = "utf-8"
	UTF16LE Encoding = "utf-16le"
	UTF16BE Encoding = "utf-16be"
)

// DefaultMaxLine is used when a caller passes a limit of zero or less.
const DefaultMaxLine = importer.DefaultMaxLineBytes

// ErrEmpty is returned when the input has no content at all.
var ErrEmpty = &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}

// decode sniffs the BOM and returns a reader of UTF-8 bytes. A UTF-8 BOM is
// dropped; UTF-16 is transcoded. Anything else is read as is and checked per
// line.
func decode(r io.Reader) (io.Reader, Encoding, error) {
	br := bufio.NewReaderSize(r, 4096)
	head, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, UTF8, err
	}
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		_, _ = br.Discard(3)
		return br, UTF8, nil
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}):
		_, _ = br.Discard(2)
		dec := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
		return transform.NewReader(br, dec), UTF16LE, nil
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		_, _ = br.Discard(2)
		dec := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
		return transform.NewReader(br, dec), UTF16BE, nil
	}
	return br, UTF8, nil
}

// validText reports whether b is strict UTF-8 without NUL or the replacement
// character a lossy decoder leaves behind for bad UTF-16.
func validText(b []byte, enc Encoding) bool {
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	return enc == UTF8 || !bytes.Contains(b, []byte("�"))
}

// LineReader yields lines of at most maxLine bytes.
type LineReader struct {
	br      *bufio.Reader
	enc     Encoding
	maxLine int
	lineNo  int64
	buf     []byte
	err     error // sticky: a limit or read error ends the stream
}

// NewLineReader detects the encoding from the BOM (UTF-8, UTF-16LE, UTF-16BE;
// otherwise strict UTF-8) and returns a reader over decoded lines.
func NewLineReader(r io.Reader, maxLine int) (*LineReader, Encoding, error) {
	if maxLine <= 0 {
		maxLine = DefaultMaxLine
	}
	dec, enc, err := decode(r)
	if err != nil {
		return nil, enc, err
	}
	return &LineReader{br: bufio.NewReaderSize(dec, 64<<10), enc: enc, maxLine: maxLine}, enc, nil
}

// Next returns the next line without its \n or \r\n terminator. The slice is
// valid until the next call. io.EOF ends the input; a final line with no
// terminator is returned first. Errors:
//   - *importer.LimitError when a line is longer than maxLine; the stream ends
//     there and the whole file is to be refused;
//   - importer.RowError{ReasonEncoding} for a line that is not strict UTF-8 or
//     contains NUL; lineNo is set and the next call continues;
//   - any read error from the underlying reader (a *LimitError for bytes).
func (l *LineReader) Next() (line []byte, lineNo int64, err error) {
	if l.err != nil {
		return nil, l.lineNo, l.err
	}
	l.buf = l.buf[:0]
	for {
		chunk, rerr := l.br.ReadSlice('\n')
		l.buf = append(l.buf, chunk...)
		if len(l.buf) > l.maxLine+2 {
			return l.stop(l.lineNo+1, &importer.LimitError{Limit: "line-bytes", Max: int64(l.maxLine), At: l.lineNo + 1})
		}
		if rerr == bufio.ErrBufferFull {
			continue
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return l.stop(l.lineNo, rerr)
		}
		if errors.Is(rerr, io.EOF) && len(l.buf) == 0 {
			return l.stop(l.lineNo, io.EOF)
		}
		break
	}
	l.lineNo++
	line = l.buf
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	if len(line) > l.maxLine {
		return l.stop(l.lineNo, &importer.LimitError{Limit: "line-bytes", Max: int64(l.maxLine), At: l.lineNo})
	}
	if !validText(line, l.enc) {
		return nil, l.lineNo, importer.RowError{Line: l.lineNo, Reason: importer.ReasonEncoding}
	}
	return line, l.lineNo, nil
}

// stop ends the stream: every later call returns the same error.
func (l *LineReader) stop(lineNo int64, err error) ([]byte, int64, error) {
	l.err = err
	return nil, lineNo, err
}

// LineNo is the number of lines returned so far.
func (l *LineReader) LineNo() int64 { return l.lineNo }
