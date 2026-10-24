package textio

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/importer"
)

// CSVOptions configure NewCSV.
type CSVOptions struct {
	Comma, Comment rune     // Comma defaults to ','; Comment 0 means none
	MaxFields      int      // default importer.DefaultMaxFields; more fields: *importer.LimitError
	RequireHeader  []string // each must be a header name, else QuarantineError (Column = the missing name)
	MaxLine        int      // longest physical line in bytes; default DefaultMaxLine; longer: *importer.LimitError
	// MaxRecord is the most bytes one logical record may span: a quoted field
	// can run over many lines, and an unterminated quote would otherwise run to
	// the end of the file. Default 16 * MaxLine; more: *importer.LimitError
	// with Limit "record-bytes".
	MaxRecord int
}

// recordSlack covers what encoding/csv has read ahead of the record it is
// still assembling (its bufio buffer is 4096 bytes).
const recordSlack = 2 * 4096

// boundReader limits the length of every physical line and of every logical
// record passing through, and remembers the first error it saw, so csv's own
// error wrapping cannot hide it.
type boundReader struct {
	r      io.Reader
	max    int
	cur    int
	prev   byte
	lines  int64
	err    error
	failed bool

	maxRec   int64
	total    int64 // bytes delivered so far
	recStart int64 // csv.Reader.InputOffset after the last record that csv finished
}

// breach records the line-length error. start is the index in p where the
// offending line begins; bytes before it are delivered first, so records that
// precede the long line are not lost to csv's read-ahead, and the error is
// returned (and marked failed) on the next call.
func (b *boundReader) breach(p []byte, start int) (int, error) {
	b.err = &importer.LimitError{Limit: "line-bytes", Max: int64(b.max), At: b.lines + 1}
	if start <= 0 {
		b.failed = true
		return 0, b.err
	}
	return start, nil
}

func (b *boundReader) Read(p []byte) (int, error) {
	if b.err != nil {
		b.failed = true
		return 0, b.err
	}
	n, err := b.r.Read(p)
	b.total += int64(n)
	// csv only asks for more bytes while it is inside a record (or reading
	// ahead), so what has been delivered beyond the end of the last finished
	// record is the record being assembled plus a bounded read-ahead.
	if pending := b.total - b.recStart; pending > b.maxRec+recordSlack {
		b.err = &importer.LimitError{Limit: "record-bytes", Max: b.maxRec, At: pending}
		b.failed = true
		return 0, b.err
	}
	for i := 0; i < n; i++ {
		if p[i] == '\n' {
			eff := b.cur
			if b.prev == '\r' {
				eff-- // the \r of a \r\n terminator is not part of the line
			}
			if eff > b.max {
				return b.breach(p, i-b.cur)
			}
			b.cur, b.prev = 0, 0
			b.lines++
			continue
		}
		b.cur++
		b.prev = p[i]
		if b.cur > b.max+1 { // cannot fit even with a trailing \r
			return b.breach(p, i+1-b.cur)
		}
	}
	if errors.Is(err, io.EOF) && b.cur > b.max { // last line has no terminator
		eff := b.cur
		if b.prev == '\r' {
			eff--
		}
		if eff > b.max {
			return b.breach(p, n-b.cur)
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
		b.failed = true
	}
	return n, err
}

// CSVReader reads strict CSV: encoding/csv with LazyQuotes off, a header,
// per-row field-count checks and bounded line and field sizes.
type CSVReader struct {
	r         *csv.Reader
	src       *boundReader
	header    []string
	enc       Encoding
	maxFields int
	err       error // sticky terminal error
}

// NewCSV detects the encoding, reads the header and checks RequireHeader.
// An empty or header-less file is a *QuarantineError (ReasonUnsupportedFormat),
// as is a missing required column; an overlong line or too many fields is a
// *LimitError.
func NewCSV(r io.Reader, o CSVOptions) (*CSVReader, Encoding, error) {
	dec, enc, err := decode(r)
	if err != nil {
		return nil, enc, err
	}
	if o.MaxLine <= 0 {
		o.MaxLine = DefaultMaxLine
	}
	if o.MaxFields <= 0 {
		o.MaxFields = importer.DefaultMaxFields
	}
	if o.MaxRecord <= 0 {
		o.MaxRecord = 16 * o.MaxLine
	}
	src := &boundReader{r: dec, max: o.MaxLine, maxRec: int64(o.MaxRecord)}
	cr := csv.NewReader(src)
	if o.Comma != 0 {
		cr.Comma = o.Comma
	}
	cr.Comment = o.Comment
	cr.LazyQuotes = false
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = false
	c := &CSVReader{r: cr, src: src, enc: enc, maxFields: o.MaxFields}

	rec, _, err := c.readRecord()
	switch {
	case errors.Is(err, io.EOF):
		return nil, enc, ErrEmpty
	case err != nil:
		var re importer.RowError
		if errors.As(err, &re) {
			return nil, enc, &importer.QuarantineError{Reason: re.Reason}
		}
		return nil, enc, err
	}
	c.header = rec
	for _, want := range o.RequireHeader {
		if c.Index(want) < 0 {
			return nil, enc, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: want}
		}
		// Index binds to the first match; a second column of the same name
		// could hold different (even unmasked) data, so the file is refused.
		n := 0
		for _, h := range c.header {
			if h == want {
				n++
			}
		}
		if n > 1 {
			return nil, enc, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: want}
		}
	}
	return c, enc, nil
}

// readRecord reads one record and applies the checks that do not depend on the header.
func (c *CSVReader) readRecord() ([]string, int64, error) {
	if c.err != nil {
		return nil, 0, c.err
	}
	rec, err := c.r.Read()
	// InputOffset moves line by line, so it is only a record boundary here,
	// after Read has returned.
	c.src.recStart = c.r.InputOffset()
	if c.src.failed {
		c.err = c.src.err
		return nil, 0, c.err
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			c.err = io.EOF
			return nil, 0, io.EOF
		}
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			line := int64(pe.StartLine)
			if line <= 0 {
				line = int64(pe.Line)
			}
			return nil, line, importer.RowError{Line: line, Reason: importer.ReasonFieldCount}
		}
		c.err = err
		return nil, 0, err
	}
	line, _ := c.r.FieldPos(0)
	if len(rec) > c.maxFields {
		c.err = &importer.LimitError{Limit: "fields", Max: int64(c.maxFields), At: int64(len(rec))}
		return nil, int64(line), c.err
	}
	for _, f := range rec {
		if !utf8.ValidString(f) || strings.IndexByte(f, 0) >= 0 || (c.enc != UTF8 && strings.Contains(f, "�")) {
			return nil, int64(line), importer.RowError{Line: int64(line), Reason: importer.ReasonEncoding}
		}
	}
	return rec, int64(line), nil
}

// Header returns the header names, exactly as written.
func (c *CSVReader) Header() []string { return append([]string(nil), c.header...) }

// Index returns the position of a header name, or -1.
func (c *CSVReader) Index(name string) int {
	for i, h := range c.header {
		if h == name {
			return i
		}
	}
	return -1
}

// Next returns the next data record and the 1-based physical line it starts on.
// Errors:
//   - io.EOF at the end;
//   - importer.RowError (ReasonFieldCount for a wrong field count or broken
//     quoting, ReasonEncoding for bad text): reject the row and call Next again;
//   - *importer.LimitError for an overlong line, too many fields, or a byte
//     limit from the underlying reader: the stream ends, refuse the file.
func (c *CSVReader) Next() (rec []string, lineNo int64, err error) {
	rec, lineNo, err = c.readRecord()
	if err != nil {
		return nil, lineNo, err
	}
	if len(rec) != len(c.header) {
		return nil, lineNo, importer.RowError{Line: lineNo, Reason: importer.ReasonFieldCount}
	}
	return rec, lineNo, nil
}
