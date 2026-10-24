package textio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/importer"
)

func utf16(t *testing.T, s string, e unicode.Endianness, bom unicode.BOMPolicy) []byte {
	t.Helper()
	out, _, err := transform.Bytes(unicode.UTF16(e, bom).NewEncoder(), []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readAll(t *testing.T, lr *LineReader) (lines []string, nums []int64, rowErrs []importer.RowError, final error) {
	t.Helper()
	for {
		line, n, err := lr.Next()
		var re importer.RowError
		switch {
		case err == nil:
			lines = append(lines, string(line))
			nums = append(nums, n)
		case errors.As(err, &re):
			rowErrs = append(rowErrs, re)
		case errors.Is(err, io.EOF):
			return lines, nums, rowErrs, nil
		default:
			return lines, nums, rowErrs, err
		}
	}
}

func TestLineReaderEncodings(t *testing.T) {
	const text = "alpha,1\r\nbeta,2\ngamma,é\n"
	want := []string{"alpha,1", "beta,2", "gamma,é"}
	cases := []struct {
		name string
		in   []byte
		enc  Encoding
	}{
		{"utf-8", []byte(text), UTF8},
		{"utf-8 bom", append([]byte{0xEF, 0xBB, 0xBF}, text...), UTF8},
		{"utf-16le bom", utf16(t, text, unicode.LittleEndian, unicode.UseBOM), UTF16LE},
		{"utf-16be bom", utf16(t, text, unicode.BigEndian, unicode.UseBOM), UTF16BE},
	}
	for _, c := range cases {
		lr, enc, err := NewLineReader(bytes.NewReader(c.in), 0)
		if err != nil || enc != c.enc {
			t.Fatalf("%s: enc=%s err=%v", c.name, enc, err)
		}
		lines, nums, rowErrs, final := readAll(t, lr)
		if final != nil || len(rowErrs) != 0 {
			t.Fatalf("%s: final=%v rowErrs=%v", c.name, final, rowErrs)
		}
		if strings.Join(lines, "|") != strings.Join(want, "|") || nums[2] != 3 {
			t.Errorf("%s: lines=%q nums=%v", c.name, lines, nums)
		}
	}
}

func TestLineReaderEdgeCases(t *testing.T) {
	// No trailing newline, blank lines keep their numbers.
	lr, _, _ := NewLineReader(strings.NewReader("a\n\nb"), 0)
	lines, nums, _, final := readAll(t, lr)
	if final != nil || len(lines) != 3 || lines[1] != "" || lines[2] != "b" || nums[2] != 3 {
		t.Fatalf("got %q %v %v", lines, nums, final)
	}
	// Empty and one-byte inputs.
	lr, _, err := NewLineReader(strings.NewReader(""), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty: %v", err)
	}
	lr, _, _ = NewLineReader(strings.NewReader("x"), 0)
	if l, n, err := lr.Next(); err != nil || string(l) != "x" || n != 1 {
		t.Fatalf("one byte: %q %d %v", l, n, err)
	}
	// A lone BOM byte pair of a truncated UTF-16 stream must not hang or panic.
	lr, _, err = NewLineReader(bytes.NewReader([]byte{0xFF, 0xFE, 'a'}), 0)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, lr)
}

func TestLineReaderBadEncoding(t *testing.T) {
	in := "ok\nbad\xff\nnul\x00x\nafter\n"
	lr, _, _ := NewLineReader(strings.NewReader(in), 0)
	lines, nums, rowErrs, final := readAll(t, lr)
	if final != nil || len(lines) != 2 || lines[0] != "ok" || lines[1] != "after" || nums[1] != 4 {
		t.Fatalf("lines=%q nums=%v final=%v", lines, nums, final)
	}
	if len(rowErrs) != 2 || rowErrs[0] != (importer.RowError{Line: 2, Reason: importer.ReasonEncoding}) || rowErrs[1].Line != 3 {
		t.Fatalf("rowErrs=%v", rowErrs)
	}
	// Odd-length UTF-16 body yields a replacement character, which is refused.
	body := append(utf16(t, "good\n", unicode.LittleEndian, unicode.UseBOM), 0x41)
	lr, _, _ = NewLineReader(bytes.NewReader(body), 0)
	_, _, rowErrs, _ = readAll(t, lr)
	if len(rowErrs) != 1 || rowErrs[0].Reason != importer.ReasonEncoding {
		t.Fatalf("utf-16 tail: %v", rowErrs)
	}
}

func TestLineReaderLimit(t *testing.T) {
	// Exactly at the limit is fine (CRLF does not count); one over is a LimitError and ends the stream.
	ok := strings.Repeat("a", 10)
	lr, _, _ := NewLineReader(strings.NewReader(ok+"\r\n"+ok+"b\nnever\n"), 10)
	if l, _, err := lr.Next(); err != nil || len(l) != 10 {
		t.Fatalf("at limit: %q %v", l, err)
	}
	_, n, err := lr.Next()
	var le *importer.LimitError
	if !errors.As(err, &le) || !errors.Is(err, importer.ErrLimit) || le.Limit != "line-bytes" || le.Max != 10 || n != 2 {
		t.Fatalf("over limit: n=%d err=%v", n, err)
	}
	if _, _, err2 := lr.Next(); !errors.As(err2, &le) {
		t.Fatalf("stream not terminal: %v", err2)
	}
	// An overlong line without any newline must not buffer the whole input.
	big := strings.NewReader(strings.Repeat("x", 5<<20))
	lr, _, _ = NewLineReader(big, 1024)
	if _, _, err := lr.Next(); !errors.As(err, &le) {
		t.Fatalf("no-newline flood: %v", err)
	}
	if big.Len() < 4<<20 {
		t.Fatalf("read %d bytes before refusing", 5<<20-big.Len())
	}
	// Lines larger than the internal buffer but under the limit work.
	long := strings.Repeat("y", 200<<10)
	lr, _, _ = NewLineReader(strings.NewReader(long+"\nz\n"), 256<<10)
	if l, _, err := lr.Next(); err != nil || len(l) != len(long) {
		t.Fatalf("long line: %d %v", len(l), err)
	}
}

func TestLineReaderPropagatesByteLimit(t *testing.T) {
	src := importer.LimitReader(strings.NewReader("aaaa\nbbbb\ncccc\n"), 7)
	lr, _, err := NewLineReader(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, final := readAll(t, lr)
	if !errors.Is(final, importer.ErrLimit) {
		t.Fatalf("final=%v", final)
	}
}

func csvOf(t *testing.T, s string, o CSVOptions) (*CSVReader, error) {
	t.Helper()
	c, _, err := NewCSV(strings.NewReader(s), o)
	return c, err
}

func TestCSVBasics(t *testing.T) {
	in := "# a comment\n\"time\",\"host\",note\n2001-01-01,web-a,\"has, comma\"\n\n2001-01-02,web-b,\"two\nlines\"\n2001-01-03,web-c,x\n"
	c, err := csvOf(t, in, CSVOptions{Comment: '#', RequireHeader: []string{"time", "host"}})
	if err != nil {
		t.Fatal(err)
	}
	if h := c.Header(); len(h) != 3 || h[0] != "time" || c.Index("note") != 2 || c.Index("zzz") != -1 {
		t.Fatalf("header %v", h)
	}
	type row struct {
		f    string
		line int64
	}
	var got []row
	for {
		rec, n, err := c.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, row{rec[2], n})
	}
	want := []row{{"has, comma", 3}, {"two\nlines", 5}, {"x", 7}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCSVEncodings(t *testing.T) {
	const text = "a,b\n1,é\n2,x\n"
	for name, in := range map[string][]byte{
		"utf-8":        []byte(text),
		"utf-8 bom":    append([]byte{0xEF, 0xBB, 0xBF}, text...),
		"utf-16le bom": utf16(t, text, unicode.LittleEndian, unicode.UseBOM),
	} {
		c, _, err := NewCSV(bytes.NewReader(in), CSVOptions{RequireHeader: []string{"a"}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rec, n, err := c.Next()
		if err != nil || rec[1] != "é" || n != 2 {
			t.Fatalf("%s: %v %d %v", name, rec, n, err)
		}
	}
}

func TestCSVRowErrors(t *testing.T) {
	in := "a,b\n1,2\n3\n4,5,6\n7,\"unterminated\n"
	c, err := csvOf(t, in, CSVOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rec, n, err := c.Next(); err != nil || rec[0] != "1" || n != 2 {
		t.Fatalf("row 1: %v %d %v", rec, n, err)
	}
	for _, line := range []int64{3, 4} {
		_, n, err := c.Next()
		var re importer.RowError
		if !errors.As(err, &re) || re.Reason != importer.ReasonFieldCount || re.Line != line || n != line {
			t.Fatalf("line %d: n=%d err=%v", line, n, err)
		}
	}
	_, _, err = c.Next()
	var re importer.RowError
	if !errors.As(err, &re) || re.Reason != importer.ReasonFieldCount {
		t.Fatalf("unterminated quote: %v", err)
	}
	if _, _, err = c.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after: %v", err)
	}
	// Bare quote in an unquoted field is a row error (LazyQuotes off), and reading continues.
	c, _ = csvOf(t, "a,b\nx\"y,2\nok,1\n", CSVOptions{})
	if _, _, err := c.Next(); !errors.As(err, &re) {
		t.Fatalf("bare quote: %v", err)
	}
	if rec, _, err := c.Next(); err != nil || rec[0] != "ok" {
		t.Fatalf("after bare quote: %v %v", rec, err)
	}
	// Bad encoding in a field.
	c, _ = csvOf(t, "a,b\nx\xff,2\nok,1\n", CSVOptions{})
	if _, _, err := c.Next(); !errors.As(err, &re) || re.Reason != importer.ReasonEncoding {
		t.Fatalf("bad utf-8: %v", err)
	}
}

func TestCSVHeaderRefusals(t *testing.T) {
	var qe *importer.QuarantineError
	if _, err := csvOf(t, "", CSVOptions{}); !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
		t.Errorf("empty: %v", err)
	}
	if _, err := csvOf(t, "\n\n# c\n", CSVOptions{Comment: '#'}); !errors.As(err, &qe) {
		t.Errorf("blank only: %v", err)
	}
	_, err := csvOf(t, "a,b\n1,2\n", CSVOptions{RequireHeader: []string{"a", "needed"}})
	if !errors.As(err, &qe) || qe.Column != "needed" || !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("missing column: %v", err)
	}
	if _, err := csvOf(t, "a,\"b\n", CSVOptions{}); !errors.As(err, &qe) {
		t.Errorf("broken header: %v", err)
	}
	if _, err := csvOf(t, "x", CSVOptions{RequireHeader: []string{"y"}}); !errors.As(err, &qe) {
		t.Errorf("one byte: %v", err)
	}
	// Truncated UTF-16 BOM must not panic.
	if _, _, err := NewCSV(bytes.NewReader([]byte{0xFF, 0xFE, 'a'}), CSVOptions{}); err == nil {
		t.Error("truncated utf-16 accepted")
	}
}

func TestCSVLimits(t *testing.T) {
	var le *importer.LimitError
	// Fields.
	c, err := csvOf(t, "a,b\n1,2,3,4\n", CSVOptions{MaxFields: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Next(); !errors.As(err, &le) || le.Limit != "fields" {
		t.Errorf("fields: %v", err)
	}
	if _, _, err := c.Next(); !errors.As(err, &le) {
		t.Errorf("fields not terminal: %v", err)
	}
	if _, err := csvOf(t, "a,b,c,d\n", CSVOptions{MaxFields: 3}); !errors.As(err, &le) {
		t.Errorf("header fields: %v", err)
	}
	// Line length, including inside a quoted multi-line field.
	c, err = csvOf(t, "a,b\n1,"+strings.Repeat("z", 50)+"\n", CSVOptions{MaxLine: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, n, err := c.Next(); !errors.As(err, &le) || le.Limit != "line-bytes" || le.At != 2 {
		t.Errorf("line: n=%d err=%v", n, err)
	}
	c, _ = csvOf(t, "a,b\n1,\"short\n"+strings.Repeat("z", 50)+"\"\n", CSVOptions{MaxLine: 20})
	if _, _, err := c.Next(); !errors.As(err, &le) {
		t.Errorf("multi-line field: %v", err)
	}
	// Exactly MaxLine with CRLF passes.
	c, _ = csvOf(t, "a,b\r\n1,12345678\r\n", CSVOptions{MaxLine: 10})
	if rec, _, err := c.Next(); err != nil || rec[1] != "12345678" {
		t.Errorf("at limit: %v %v", rec, err)
	}
	// LF-only and unterminated last lines: exact length passes, one over fails.
	for _, tail := range []string{"\n", ""} {
		c, _ = csvOf(t, "a,b\n1,123456789"+tail, CSVOptions{MaxLine: 11})
		if rec, _, err := c.Next(); err != nil || rec[1] != "123456789" {
			t.Errorf("LF exact %q: %v %v", tail, rec, err)
		}
		c, _ = csvOf(t, "a,b\n1,123456789"+tail, CSVOptions{MaxLine: 10})
		if _, _, err := c.Next(); !errors.As(err, &le) {
			t.Errorf("LF one over %q: %v", tail, err)
		}
	}
	// A short record before a long line is still delivered.
	c, _ = csvOf(t, "a,b\n1,2\n3,"+strings.Repeat("z", 50)+"\n", CSVOptions{MaxLine: 20})
	if rec, n, err := c.Next(); err != nil || rec[0] != "1" || n != 2 {
		t.Errorf("record before breach: %v %d %v", rec, n, err)
	}
	if _, _, err := c.Next(); !errors.As(err, &le) || le.At != 3 {
		t.Errorf("breach after record: %v", err)
	}
	// Byte limit from below.
	src := importer.LimitReader(strings.NewReader("a,b\n1,2\n3,4\n5,6\n"), 10)
	c, _, err = NewCSV(src, CSVOptions{})
	if err == nil {
		for err == nil {
			_, _, err = c.Next()
		}
	}
	if !errors.Is(err, importer.ErrLimit) {
		t.Errorf("byte limit: %v", err)
	}
}

func TestErrorsNeverCarryContent(t *testing.T) {
	const s = "SENTINEL-7f3a"
	in := "a,b\n" + s + "\n" + s + ",\"x\n"
	c, _ := csvOf(t, in, CSVOptions{})
	for {
		_, _, err := c.Next()
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), s) {
			t.Fatalf("leak: %v", err)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	lr, _, _ := NewLineReader(strings.NewReader(s+"\xff\n"+strings.Repeat(s, 10)+"\n"), 20)
	for {
		_, _, err := lr.Next()
		if err != nil && strings.Contains(err.Error(), s) {
			t.Fatalf("leak: %v", err)
		}
		var re importer.RowError
		if err != nil && !errors.As(err, &re) {
			break
		}
	}
}
