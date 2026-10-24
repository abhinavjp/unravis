package evidence

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// formulaTriggers are the first characters a spreadsheet may read as the
// start of a formula or a command: = + - @, tab and carriage return.
const formulaTriggers = "=+-@\t\r"

// cellLookahead bounds how much of a cell Cell normalises to find its first
// visible character, so a very long cell costs no more than a short one.
const cellLookahead = 64

// Cell makes one value safe to put in a spreadsheet cell (CSV injection). A
// value whose first byte is =, +, -, @, a tab or a carriage return gets a
// leading apostrophe, so a spreadsheet shows it as text and never runs it.
// That part is byte for byte what server.spreadsheetSafe does.
//
// It also escapes a value whose first visible character only becomes one of
// those after normalisation: a full-width or small form ("＝", "﹢"), or a
// trigger behind leading spaces, control characters or invisible characters (a
// zero-width space, a bidi mark). Such a value is escaped as a whole, unchanged otherwise.
//
// A value that already starts with an apostrophe is left alone, so Cell is
// idempotent: Cell(Cell(s)) == Cell(s).
func Cell(s string) string {
	if s == "" {
		return s
	}
	if strings.IndexByte(formulaTriggers, s[0]) >= 0 || hiddenTrigger(s) {
		return "'" + s
	}
	return s
}

// hiddenTrigger reports whether the first visible character of s, after
// NFKC and with invisible characters and leading white space removed, is a
// formula trigger. Only the first cellLookahead runes are looked at.
func hiddenTrigger(s string) bool {
	head := s
	for i, n := 0, 0; i < len(s); n++ {
		if n == cellLookahead {
			head = s[:i]
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	for _, r := range norm.NFKC.String(head) {
		switch {
		case r < utf8.RuneSelf && strings.IndexByte(formulaTriggers, byte(r)) >= 0:
			return true // checked before white space: tab and CR are triggers
		case isIgnorable(r) || unicode.IsSpace(r) || unicode.IsControl(r):
			continue // other control characters (NUL, ESC, ...) do not show either
		}
		return false
	}
	return false
}
