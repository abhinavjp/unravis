// Package querytext makes the text of a SQL statement safe to keep in a recording, for the opt-in query text level of the Blackbox SQL
// collection. It is a pure function with no I/O. The rules, in order:
//
//   - the input is cut to maxInput bytes first, so one huge statement costs a bounded amount of work;
//   - a statement that can carry a credential (a login, user or credential being made or changed, a password or key phrase, a linked
//     data source, a master key) is withheld whole: its text is never kept;
//   - comments are dropped, because people put notes and secrets in them;
//   - every literal (a string, a number, a hex value, a quoted "identifier" that may really be a string) becomes ?, so the shape of the query
//     stays and the values do not;
//   - what is left goes through secretscrub.Mask as a second net, white space is folded to single spaces and the text is cut to the size cap.
//
// Fail closed: an unclosed string or comment runs to the end of the text, so nothing after it is kept.
package querytext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/secretscrub"
)

const (
	// DefaultMaxChars is the size cap used when none is set; MinMaxChars and MaxMaxChars bound what an owner may choose.
	DefaultMaxChars = 1000
	MinMaxChars     = 100
	MaxMaxChars     = 4000
	// Withheld replaces the text of a statement that may carry a credential.
	Withheld = "[query text withheld: it may hold a credential]"

	maxInput = 64 << 10
	mask     = "***"
)

// withholdWords are kept in lower case. A statement holding one of them (as a word or a part of a name) is withheld whole.
var withholdWords = []string{
	"password", "passwd", "pwd", "passphrase", "identified by", "secret", "credential", "create login", "alter login", "create user",
	"alter user", "sp_addlogin", "sp_password", "sp_addlinkedsrvlogin", "sp_addsrvrolemember", "openrowset", "opendatasource",
	"master key", "certificate", "private key", "encryptbypass", "decryptbypass", "execute as", "exec as", "sas token", "access key",
	"shared access", "bearer", "authorization", "api_key", "apikey", "token",
}

// Scrub returns the text to keep for a statement, at most maxChars characters (DefaultMaxChars when maxChars is 0; values outside
// MinMaxChars..MaxMaxChars are moved inside). An empty or blank statement gives "".
func Scrub(text string, maxChars int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	limit := capFor(maxChars)
	if len(text) > maxInput {
		text = text[:maxInput]
	}
	text = strings.ToValidUTF8(text, " ")
	if withheld(text) {
		return Withheld
	}
	shaped := shape(text)
	shaped = strings.Join(strings.Fields(secretscrub.Mask(shaped, mask)), " ")
	return cut(shaped, limit)
}

func capFor(n int) int {
	switch {
	case n == 0:
		return DefaultMaxChars
	case n < MinMaxChars:
		return MinMaxChars
	case n > MaxMaxChars:
		return MaxMaxChars
	}
	return n
}

func withheld(text string) bool {
	l := strings.ToLower(text)
	for _, w := range withholdWords {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func isIdent(r rune) bool {
	return r == '_' || r == '@' || r == '#' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// shape drops comments and replaces literals with ?.
func shape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	prevIdent := false
	for i < len(s) {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
			prevIdent = false
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s) // unclosed: nothing after it is kept
			} else {
				i += 2 + end + 2
			}
			b.WriteByte(' ')
			prevIdent = false
		case !prevIdent && (c == 'N' || c == 'n') && i+1 < len(s) && s[i+1] == '\'':
			i = skipQuoted(s, i+1, '\'') // N'...' is a string too
			b.WriteByte('?')
			prevIdent = false
		case c == '\'' || c == '"':
			i = skipQuoted(s, i, c)
			b.WriteByte('?')
			prevIdent = false
		case c == '[':
			// a bracketed identifier is kept; ]] is a ] inside it
			j := i + 1
			for j < len(s) {
				if s[j] == ']' {
					if j+1 < len(s) && s[j+1] == ']' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			b.WriteString(s[i:j])
			i = j
			prevIdent = true
		case !prevIdent && c == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X'):
			i += 2
			for i < len(s) && isHex(s[i]) {
				i++
			}
			b.WriteByte('?')
		case !prevIdent && (isDigit(c) || (c == '.' && i+1 < len(s) && isDigit(s[i+1]))):
			i = skipNumber(s, i)
			b.WriteByte('?')
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				b.WriteByte(' ')
			} else {
				b.WriteString(s[i : i+n])
			}
			prevIdent = isIdent(r)
			i += n
		}
	}
	return b.String()
}

// skipQuoted returns the index after the quote that closes the one at i; a doubled quote is a quote inside. Unclosed runs to the end.
func skipQuoted(s string, i int, q byte) int {
	j := i + 1
	for j < len(s) {
		if s[j] == q {
			if j+1 < len(s) && s[j+1] == q {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(s)
}

func skipNumber(s string, i int) int {
	for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
		i++
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			return j
		}
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool   { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }

// cut keeps at most n characters, whole runes, and marks a cut with an ellipsis.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}
