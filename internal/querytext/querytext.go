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
	"shared access", "bearer", "authorization", "api_key", "apikey", "token", "approle", "sp_addremotelogin", "sp_change_users_login",
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
	// Any kind of space, and invisible format characters (a zero-width space), count as a space: they cannot hide a word break.
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(text, " "))
	if withheld(strings.Join(strings.Fields(text), " ")) {
		return Withheld
	}
	shaped := strings.Join(strings.Fields(shape(text)), " ")
	// The words are checked again on what is left: a comment or extra white space cannot split a phrase (CREATE/**/LOGIN), and a procedure
	// call with a bare word for an argument (EXEC usp bob, Hunter2) is withheld, because a bare word is a string value in T-SQL.
	if withheld(shaped) || execBareArgs(shaped) {
		return Withheld
	}
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
			// T-SQL block comments nest: count the depth, and an unclosed one runs to the end (nothing after it is kept).
			depth, j := 1, i+2
			for j < len(s) && depth > 0 {
				switch {
				case s[j] == '/' && j+1 < len(s) && s[j+1] == '*':
					depth++
					j += 2
				case s[j] == '*' && j+1 < len(s) && s[j+1] == '/':
					depth--
					j += 2
				default:
					j++
				}
			}
			i = j
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
			// A bracketed identifier is kept, with ]] for a ] inside it. An unclosed one runs to the end (nothing after it is kept).
			// One that holds an @ or a backslash, or starts with a digit, is data posing as a name (a pivot column of e-mail addresses or dates, a Windows account): it is masked.
			j, closed := i+1, false
			for j < len(s) {
				if s[j] == ']' {
					if j+1 < len(s) && s[j+1] == ']' {
						j += 2
						continue
					}
					j++
					closed = true
					break
				}
				j++
			}
			if !closed {
				i = len(s)
				break
			}
			name := s[i+1 : j-1]
			if strings.ContainsAny(name, "@\\") || (name != "" && isDigit(name[0])) {
				b.WriteString("[?]")
			} else {
				b.WriteString(s[i:j])
			}
			i = j
			prevIdent = true
		case !prevIdent && c == '$' && moneyAt(s, i+1):
			i = skipNumber(s, skipSign(s, i+1))
			i = skipIdentTail(s, i)
			b.WriteByte('?')
		case !prevIdent && c == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X'):
			i += 2
			for i < len(s) && isHex(s[i]) {
				i++
			}
			b.WriteByte('?')
		case !prevIdent && (isDigit(c) || (c == '.' && i+1 < len(s) && isDigit(s[i+1]))):
			i = skipIdentTail(s, skipNumber(s, i)) // 12AB34 is one token
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

func skipSign(s string, i int) int {
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		return i + 1
	}
	return i
}

// moneyAt says whether a money literal ($12.50, $-3) starts at i, just after a $.
func moneyAt(s string, i int) bool {
	i = skipSign(s, i)
	return i < len(s) && (isDigit(s[i]) || (s[i] == '.' && i+1 < len(s) && isDigit(s[i+1])))
}

// skipIdentTail moves past letters, digits and underscores that follow a number, so 12AB34 is not left half kept.
func skipIdentTail(s string, i int) int {
	for i < len(s) && (s[i] == '_' || isDigit(s[i]) || (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
		i++
	}
	return i
}

// callKeywords are words a statement (or the piece of one a plan cache keeps) may start with. A statement that starts with anything
// else is read as a procedure call without EXEC, which T-SQL allows as the first statement of a batch.
var callKeywords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`select insert update delete merge with set declare if else while begin end create alter drop truncate use
		print raiserror waitfor grant revoke deny dbcc backup restore from where and or not join inner left right full cross outer on
		group order by having union except intersect values into top distinct option return goto try catch commit rollback save bulk open
		close fetch deallocate kill checkpoint reconfigure shutdown case when then as is in exists between like all any some for
		tran transaction distributed`) {
		callKeywords[w] = true
	}
}

// callTokens splits shaped text into tokens: white space and commas separate, = and ; are tokens of their own, and a [bracketed name] is one
// token whatever it holds (a ; or a space inside it does not split it).
func callTokens(s string) []string {
	var toks []string
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', ',', '\t':
			i++
		case '=', ';':
			toks = append(toks, s[i:i+1])
			i++
		default:
			// One token runs to the next separator, and a [bracketed part] inside it (dbo.[My Proc]) never splits it.
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != ',' && s[j] != '=' && s[j] != ';' && s[j] != '\t' {
				if s[j] == '[' {
					j++
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
					continue
				}
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks
}

// execBareArgs says whether a procedure call passes a bare word as an argument. In T-SQL an argument may be written without quotes
// (`EXEC usp bob, Hunter2`, or `usp bob` as the first statement of a batch, or `[Hunter2]`), which hands over strings that no literal rule
// can see. Allowed after the procedure name: ?, @variables, =, DEFAULT, NULL, OUTPUT, OUT, READONLY, WITH, and parts that hold only ? and
// brackets of parentheses. Anything else (a bare word or a [bracketed] one) makes the whole text withheld. The text is the output of shape
// (literals are already ?), single-spaced. The procedure name itself may be anything, including @variable and [bracketed].
func execBareArgs(s string) bool {
	toks := callTokens(s)
	start := 0
	for k := 0; k <= len(toks); k++ {
		if k < len(toks) && toks[k] != ";" {
			continue
		}
		if badCall(toks[start:k]) {
			return true
		}
		start = k + 1
	}
	return false
}

func badCall(stmt []string) bool {
	if len(stmt) == 0 {
		return false
	}
	// A statement can start after BEGIN, ELSE, THEN, TRY or CATCH: judge each such piece as a statement of its own.
	for k, t := range stmt[:len(stmt)-1] {
		switch strings.ToLower(t) {
		case "begin", "else", "then", "try", "catch":
			if badCall(stmt[k+1:]) {
				return true
			}
		}
	}
	var rest []string
	for k, t := range stmt {
		if l := strings.ToLower(t); l == "exec" || l == "execute" {
			rest = stmt[k+1:]
			break
		}
	}
	if rest == nil {
		first := strings.ToLower(stmt[0])
		if callKeywords[first] || strings.HasPrefix(first, "(") || strings.HasPrefix(first, "@") || strings.HasPrefix(first, "?") || strings.HasPrefix(first, "#") || strings.HasPrefix(first, "-") {
			return false
		}
		rest = stmt // a statement that starts with a name: a call without EXEC
	}
	// EXEC @rc = proc ...: a leading variable that is followed by = takes the return value. Without =, it holds the procedure name.
	if len(rest) >= 2 && strings.HasPrefix(rest[0], "@") && rest[1] == "=" {
		rest = rest[2:]
	}
	if len(rest) <= 1 {
		return false
	}
	for _, t := range rest[1:] { // rest[0] is the procedure name
		l := strings.ToLower(t)
		if t == "?" || t == "=" || strings.HasPrefix(t, "@") || onlyParens(t) ||
			l == "default" || l == "null" || l == "output" || l == "out" || l == "readonly" || l == "with" {
			continue
		}
		return true
	}
	return false
}

// onlyParens says whether t is made of ( ) and ? and nothing else, such as (?) or ().
func onlyParens(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if r != '(' && r != ')' && r != '?' {
			return false
		}
	}
	return true
}
