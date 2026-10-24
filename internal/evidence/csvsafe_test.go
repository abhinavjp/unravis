package evidence

import (
	"strings"
	"testing"
)

// Invisible characters, built from code points so the source stays readable.
var (
	zwsp = string(rune(0x200b)) // zero-width space
	rlo  = string(rune(0x202e)) // right-to-left override
	bom  = string(rune(0xfeff)) // byte order mark
	nbsp = string(rune(0x00a0)) // no-break space
	ideo = string(rune(0x3000)) // ideographic space
)

// serverRule is server.spreadsheetSafe as written today; Cell must agree with
// it on every value whose first byte decides.
func serverRule(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

func TestCellMatchesServerRule(t *testing.T) {
	for _, s := range []string{"", "=cmd|' /C calc'!A0", "+1", "-1", "@SUM(A1)", "\t", "\r", "\tx", "\r\n=1",
		"plain", "GET", "200", "'=already", "user-3fa91c2e", "a=b", " ", "\n=1", "%"} {
		if got, want := Cell(s), serverRule(s); got != want && !strings.HasPrefix(strings.TrimLeft(s, " \n"), "=") {
			t.Errorf("Cell(%q) = %q, server rule gives %q", s, got, want)
		}
	}
	for s, want := range map[string]string{"=cmd": "'=cmd", "\t": "'\t", "+1": "'+1", "-1": "'-1", "@x": "'@x", "\rx": "'\rx"} {
		if got := Cell(s); got != want {
			t.Errorf("Cell(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestCellHiddenTriggers(t *testing.T) {
	for _, s := range []string{
		"＝1+1",              // full-width equals
		"﹢1",                // small plus
		"＠SUM(A1)",          // full-width at
		zwsp + "=1+1",       // zero-width space
		rlo + "=1+1",        // bidi override
		bom + "-1",          // byte order mark
		"   =1+1",           // leading spaces
		" " + nbsp + "\t",   // spaces then a tab
		ideo + "=HYPERLINK", // ideographic space
	} {
		if got := Cell(s); got != "'"+s {
			t.Errorf("Cell(%q) = %q, want it escaped", s, got)
		}
	}
	for _, s := range []string{"plain", " plain", zwsp + "plain", "1=1", "user-3fa91c2e", "% Processor Time", "Ｇ", "   "} {
		if got := Cell(s); got != s {
			t.Errorf("Cell(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestCellIdempotentAndBounded(t *testing.T) {
	long := strings.Repeat(" ", 10000) + "=1"
	for _, s := range []string{"=1", "＝1", zwsp + "=1", "plain", "", long, strings.Repeat("=", 1<<20)} {
		once := Cell(s)
		if Cell(once) != once {
			t.Errorf("Cell is not idempotent on %.20q", s)
		}
	}
	// Only the first cellLookahead runes are looked at: a trigger behind more
	// leading spaces than that is not found, and a very long value costs the
	// same as a short one.
	if got := Cell(long); got != long {
		t.Errorf("a trigger past the lookahead should not be looked for")
	}
	if got := Cell(strings.Repeat(" ", cellLookahead-1) + "="); got[0] != '\'' {
		t.Error("a trigger inside the lookahead must be found")
	}
}
