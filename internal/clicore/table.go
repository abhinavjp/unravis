package clicore

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Printable makes text safe to print on a terminal: a control character or a direction override (an escape sequence that could move the cursor, change the
// title or hide text; a bell; a carriage return that overwrites a line) becomes a space (a line break too). Text from
// a log, a case title or a source name goes through it before it reaches a table or an error line. Invalid UTF-8 becomes U+FFFD.
func Printable(s string) string {
	if !strings.ContainsFunc(s, unsafeRune) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			b.WriteRune('\uFFFD')
		case unicode.IsControl(r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			b.WriteByte(' ')
		case invisible(r):
			// dropped: it shows nothing, and it can hide text or carry text that only a program reads
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || r == utf8.RuneError || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || invisible(r)
}

// invisible says whether r is a format character (bidirectional controls, zero-width space, word joiner, byte order mark, soft hyphen,
// tag characters) that shows nothing but changes how text reads. U+200C and U+200D stay: emoji sequences and several scripts need them.
func invisible(r rune) bool {
	return unicode.Is(unicode.Cf, r) && r != 0x200C && r != 0x200D
}

// WriteTable writes a plain table: a header line, then the rows, columns padded with spaces to the widest cell (width counted in
// characters), two spaces between columns, no trailing spaces, no colour, no border. The order of the rows is the order given (the
// command line never re-sorts what the owner returned). Every cell goes through Printable. A row with fewer cells than the header is
// padded with empty cells; a longer row is cut to the header.
func WriteTable(w io.Writer, header []string, rows [][]string) error {
	n := len(header)
	cells := make([][]string, 0, len(rows)+1)
	cells = append(cells, printAll(header, n))
	for _, r := range rows {
		cells = append(cells, printAll(r, n))
	}
	width := make([]int, n)
	for _, r := range cells {
		for i, c := range r {
			width[i] = max(width[i], utf8.RuneCountInString(c))
		}
	}
	var sb strings.Builder
	for _, r := range cells {
		var line strings.Builder
		for i, c := range r {
			if i > 0 {
				line.WriteString("  ")
			}
			line.WriteString(c)
			if i < n-1 {
				line.WriteString(strings.Repeat(" ", width[i]-utf8.RuneCountInString(c)))
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func printAll(r []string, n int) []string {
	out := make([]string, n)
	for i := 0; i < n && i < len(r); i++ {
		out[i] = Printable(r[i])
	}
	return out
}
