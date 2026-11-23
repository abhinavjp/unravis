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
	if !strings.ContainsFunc(s, unsafeRune) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeRune(r) && r != utf8.RuneError {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
}

// unsafeRune is a control character, a character that changes the direction of the text (a right-to-left override can make a file name
// read backwards on screen), an invisible character (zero width, line separator, tag characters), or an invalid byte.
func unsafeRune(r rune) bool {
	switch {
	case unicode.IsControl(r), r == utf8.RuneError:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
		return true
	case r >= 0x200B && r <= 0x200D, r >= 0x2028 && r <= 0x2029, r >= 0x2060 && r <= 0x2064, r == 0xFEFF, r == 0x00AD, r == 0x180E,
		r >= 0xE0000 && r <= 0xE007F, r >= 0xFE00 && r <= 0xFE0F:
		return true // invisible characters: they can hide text, or carry text that only a program reads
	}
	return false
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
