package secretscrub

import (
	"sort"
	"strings"
)

// Classes of a secret key name.
const (
	classNone     = 0
	classPassword = 1 // a password: the value may hold spaces and runs to ";" or the end of the line
	classToken    = 2 // a token or secret: the value is one word; after a colon only a quoted value counts ("token: expired")
	classPin      = 4 // "_pass" or "_pin" at the end of a name: a password, but only after "=" ("TESTS_PASS: 120 of 120" is a count)
	classKey      = 3 // an API or access key: the value is one word; a colon counts ("x-api-key: abc")
)

// maxIdent bounds how far a key name is read to each side of the word that marks it. A longer run is read as if it ended there
// (fail closed), and the bound keeps the work per candidate small.
const maxIdent = 128

// maxTag bounds the length of an XML tag read as one unit.
const maxTag = 4096

// secretWords are the words that mark a secret key (lower case). They are found with plain substring search on a lower-cased copy,
// which is several times faster than a case-insensitive pattern and cannot backtrack; findWords keeps the leftmost, longest ones.
var secretWords = []string{
	"password", "passwd", "passphrase", "pwd", "secret", "token", "credential", "pass", "pin",
	"api_key", "api-key", "apikey", "access_key", "access-key", "accesskey", "account_key", "account-key", "accountkey",
	"shared_access_key", "shared-access-key", "sharedaccesskey", "private_key", "private-key", "privatekey", "auth_key", "auth-key", "authkey",
}

// firstBytes holds the first letters of secretWords: a text with none of them is skipped without any search.
const firstBytes = "pstcaPSTCA"

func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if isUpper(s[i]) {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if isUpper(b[j]) {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// findWords returns the spans of the secret words in s, in order, without overlap; at one start the longest word wins.
func findWords(s string) [][2]int {
	if strings.IndexAny(s, firstBytes) < 0 {
		return nil
	}
	low := lowerASCII(s)
	var all [][2]int
	for _, w := range secretWords {
		for from := 0; ; {
			i := strings.Index(low[from:], w)
			if i < 0 {
				break
			}
			i += from
			all = append(all, [2]int{i, i + len(w)})
			from = i + 1
		}
	}
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a][0] != all[b][0] {
			return all[a][0] < all[b][0]
		}
		return all[a][1] > all[b][1]
	})
	out := all[:0]
	end := 0
	for _, m := range all {
		if m[0] < end {
			continue
		}
		out = append(out, m)
		end = m[1]
	}
	return out
}

// benignTail lists the words that, after a secret word, say the setting is not a secret ("token_count", "password_min_length").
var benignTail = map[string]bool{
	"count": true, "counts": true, "length": true, "len": true, "size": true, "max": true, "min": true, "limit": true,
	"expiry": true, "expires": true, "expiration": true, "ttl": true, "type": true, "name": true, "file": true, "path": true,
	"policy": true, "required": true, "enabled": true, "age": true, "status": true, "url": true, "uri": true, "endpoint": true,
	"header": true, "headers": true, "field": true, "label": true, "prompt": true, "format": true, "kind": true, "mode": true,
	"version": true, "hint": true, "rule": true, "rules": true, "regex": true, "pattern": true, "complexity": true,
}

// lowerTails are the words that may follow a secret word without a separator ("secretkey", "passwordhash").
var lowerTails = []string{"key", "value", "hash", "string", "str", "text", "data"}

func isIdent(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

// wordClass says what the word id[ws:we] makes of the name id: a password key, a token key, or no secret.
func wordClass(id string, ws, we int) int {
	w := strings.ToLower(id[ws:we])
	tail := id[we:]
	for i := 0; i < len(tail); i++ { // the name ends at the first character a name cannot hold
		if !isIdent(tail[i]) {
			tail = tail[:i]
			break
		}
	}
	class := classToken
	switch {
	case strings.Contains(w, "key"):
		class = classKey
	}
	switch w {
	case "password", "passwd", "passphrase", "pwd":
		class = classPassword
	case "pass", "pin":
		// Only the end of a longer name ("DB_PASS", "SIGN_PIN"): "pass=5" in a test report is not a secret.
		if ws == 0 || !(id[ws-1] == '_' || id[ws-1] == '.' || id[ws-1] == '-') || tail != "" {
			return classNone
		}
		return classPin
	}
	if tail == "" {
		return class
	}
	lt := strings.ToLower(tail)
	switch {
	case lt == "s" || lt == "es":
		if w == "token" {
			return classNone // "tokens=3" counts tokens
		}
		return class
	case tail[0] == '_' || tail[0] == '.' || tail[0] == '-':
		seg := lt[1:]
		for i := 0; i < len(seg); i++ {
			if !isLower(seg[i]) {
				seg = seg[:i]
				break
			}
		}
		if benignTail[seg] {
			return classNone
		}
		return class
	case tail[0] >= '0' && tail[0] <= '9':
		return class
	case isUpper(tail[0]):
		j := 1
		for j < len(tail) && isLower(tail[j]) {
			j++
		}
		if benignTail[strings.ToLower(tail[:j])] {
			return classNone
		}
		return class
	}
	for _, t := range lowerTails {
		if strings.HasPrefix(lt, t) {
			return class
		}
	}
	return classNone // "tokenizer", "secretary", "passwordless"
}

// keyClass is the class of a whole name: the first word in it that makes it a secret key.
func keyClass(name string) int {
	for _, m := range findWords(name) {
		if c := wordClass(name, m[0], m[1]); c != classNone {
			return c
		}
	}
	return classNone
}

// IsSecretKey says whether a name (a log attribute key, an XML element or attribute name, a setting name) is one whose value is a
// secret: "password", "DB_PASSWORD", "client_secret", "apiKey". "token_count" and "tokenizer" are not.
func IsSecretKey(name string) bool {
	if len(name) > 4*maxIdent {
		name = name[:4*maxIdent]
	}
	return keyClass(name) != classNone
}

type edit struct{ from, to int }

// apply replaces each span with mask. The spans are sorted and do not overlap.
func apply(s, mask string, edits []edit) string {
	if len(edits) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(edits)*len(mask))
	at := 0
	for _, e := range edits {
		if e.from < at || e.to < e.from || e.to > len(s) {
			continue
		}
		b.WriteString(s[at:e.from])
		b.WriteString(mask)
		at = e.to
	}
	b.WriteString(s[at:])
	return b.String()
}

// Mask returns s with the value of every secret setting in it replaced by mask (see the package comment for the forms). The key and
// the quotes around a quoted value stay. Text without such a setting comes back unchanged.
func Mask(s, mask string) string {
	if len(s) < 4 {
		return s
	}
	s = maskXML(s, mask)
	return maskKV(s, mask)
}

// maskKV masks the value after "key=" or "key:" for every secret key in s.
func maskKV(s, mask string) string {
	locs := findWords(s)
	if len(locs) == 0 {
		return s
	}
	var edits []edit
	pos := 0 // everything before pos is read already
	for _, m := range locs {
		if m[0] < pos {
			continue
		}
		lo := m[0]
		for lo > pos && m[0]-lo < maxIdent && isIdent(s[lo-1]) {
			lo--
		}
		hi := m[1]
		for hi < len(s) && hi-m[1] < maxIdent && isIdent(s[hi]) {
			hi++
		}
		class := wordClass(s[lo:hi], m[0]-lo, m[1]-lo)
		if class == classNone {
			continue
		}
		inQuery := lo > 0 && (s[lo-1] == '?' || s[lo-1] == '&')
		from, to, next, ok := readValue(s, hi, class, inQuery)
		if !ok {
			continue
		}
		if to > from && s[from:to] != mask {
			edits = append(edits, edit{from, to})
		}
		pos = next
	}
	return apply(s, mask, edits)
}

// readValue reads what follows a key that ends at i: an optional closing quote of the key, the "=" or ":", and the value. It
// returns the span of the value to mask, where reading went on to, and false when no value follows.
func readValue(s string, i int, class int, inQuery bool) (from, to, next int, ok bool) {
	j := i
	jsonKey := false
	k := j
	for k < len(s) && s[k] == '\\' {
		k++
	}
	if k < len(s) && (s[k] == '"' || s[k] == '\'') {
		jsonKey = true // the key was quoted: "password":..., \"password\":..., 'password' =>
		j = k + 1
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return 0, 0, 0, false
	}
	colon := false
	switch {
	case strings.HasPrefix(s[j:], "=>"):
		j += 2
	case s[j] == '=':
		j++
		if j < len(s) && s[j] == '=' {
			j++ // password == x
		}
	case s[j] == ':':
		colon = true
		j++
		if j < len(s) && s[j] == '=' {
			j++ // password := x
		}
	default:
		return 0, 0, 0, false
	}
	if class == classPin {
		if colon {
			return 0, 0, 0, false
		}
		class = classPassword
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	// A value on the next line: nothing follows the key on its line, so the value is the next line (one line break, then spaces;
	// a blank line ends the search).
	if j >= len(s) || s[j] == '\r' || s[j] == '\n' {
		if colon && class != classPassword && !jsonKey {
			return 0, 0, 0, false
		}
		if j >= len(s) {
			return 0, 0, 0, false
		}
		if s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n' {
			j += 2
		} else {
			j++
		}
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] == '\r' || s[j] == '\n' {
			return 0, 0, 0, false
		}
	}
	// A quoted value, plain ("..."), escaped (\"...\", JSON inside a JSON string) or in single quotes.
	q, nest := j, 0
	for q < len(s) && s[q] == '\\' {
		q++
		nest++
	}
	if q < len(s) && (s[q] == '"' || s[q] == '\'') {
		end, after := closingQuote(s, q, nest)
		return q + 1, end, after, true
	}
	if class == classToken && colon && !jsonKey {
		return 0, 0, 0, false // "token: expired" is a sentence; only a quoted value counts after a colon
	}
	if s[j] == '{' && class == classPassword {
		end := j + 1
		for end < len(s) {
			if s[end] == '}' {
				if end+1 < len(s) && s[end+1] == '}' {
					end += 2
					continue
				}
				return j + 1, end, end + 1, true // the braces stay, so a pattern that runs after this one reads the value as braced
			}
			end++
		}
		return j + 1, len(s), len(s), true // never closed: the rest of the text
	}
	end := unquotedEnd(s, j, class, jsonKey, inQuery)
	return j, end, end, true
}

// closingQuote finds the end of a quoted value that opens at q (a quote character preceded by nest backslashes). A quote ends the
// value unless it is escaped with a backslash, or doubled (ADO). For a value written inside a JSON string (nest > 0) the closing
// quote has the same number of backslashes. A value that never closes runs to the end of the text. It returns the end of the value
// text and where reading goes on.
func closingQuote(s string, q, nest int) (end, after int) {
	qc := s[q]
	for t := q + 1; t < len(s); t++ {
		if s[t] != qc {
			continue
		}
		r := 0
		for u := t - 1; u > q && s[u] == '\\'; u-- {
			r++
		}
		if nest == 0 {
			if r%2 == 1 {
				continue // \" is an escaped quote
			}
			if t+1 < len(s) && s[t+1] == qc {
				t++ // "" is an escaped quote
				continue
			}
		} else if r != nest {
			continue
		}
		// The closing backslashes of a nested value belong to the quote, not to the value.
		if nest == 0 {
			return t, t + 1
		}
		return t - r, t + 1
	}
	return len(s), len(s)
}

// unquotedEnd finds the end of an unquoted value that starts at j.
func unquotedEnd(s string, j, class int, jsonKey, inQuery bool) int {
	i := j
	switch {
	case class != classPassword:
		for i < len(s) {
			c := s[i]
			if isSpace(c) || c == ';' || c == ',' || c == '&' || c == '"' || c == '\'' || c == '}' || c == ')' || c == '<' {
				break
			}
			i++
		}
	case inQuery || jsonKey:
		// A URL query value ends at "&"; a JSON number or word ends at "," or a closing bracket.
		depth := 0 // a "[" inside the value (a mask such as "[removed]") closes with its own "]"
		for i < len(s) {
			c := s[i]
			if isSpace(c) || c == ';' || c == ',' || c == '&' || c == '}' || c == '"' || c == '\'' {
				break
			}
			if c == '[' {
				depth++
			} else if c == ']' {
				if depth == 0 {
					break
				}
				depth--
			}
			i++
		}
	default:
		// A password may hold spaces: the value runs to ";" or the end of the line (ADO syntax), unless the text only goes on
		// as the next setting of the same line (" NAME=value"), as a close tag, or as the end of a quoted text around it.
		for i < len(s) {
			c := s[i]
			if c == ';' && !entityEnd(s, i, j) || c == '\r' || c == '\n' {
				break
			}
			if (c == '"' || c == '\'') && quoteEndsText(s, i) {
				break
			}
			if c == '<' && i+1 < len(s) && s[i+1] == '/' {
				break
			}
			if c == ' ' && i > j && nextIsSetting(s, i+1) {
				break
			}
			i++
		}
		for i > j && (s[i-1] == ' ' || s[i-1] == '\t') {
			i-- // trailing spaces are not part of the value
		}
	}
	return i
}

// entityEnd says whether the ";" at i ends an XML entity that starts after j (&quot; &apos; &amp; &lt; &gt; &#34; &#x22;).
func entityEnd(s string, i, j int) bool {
	for k := i - 1; k >= j && i-k <= 7; k-- {
		c := s[k]
		if c == '&' {
			switch strings.ToLower(s[k+1 : i]) {
			case "quot", "apos", "amp", "lt", "gt":
				return true
			}
			e := s[k+1 : i]
			return len(e) > 1 && e[0] == '#'
		}
		if !(isLower(c) || isUpper(c) || c >= '0' && c <= '9' || c == '#') {
			return false
		}
	}
	return false
}

// quoteEndsText says whether the quote at i closes a text the value sits in: an XML attribute ("... " />, "... " next="x") or a JSON
// string ("... ", "... "} ).
func quoteEndsText(s string, i int) bool {
	j := i + 1
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return true
	}
	switch s[j] {
	case ',', '}', ']', '>', '/', '\r', '\n', ';', ')':
		return true
	}
	return j > i+1 && nextIsSetting(s, j)
}

// nextIsSetting says whether the text at i starts another "name=" setting.
func nextIsSetting(s string, i int) bool {
	j := i
	for j < len(s) && j-i < maxIdent && isIdent(s[j]) {
		j++
	}
	return j > i && j < len(s) && s[j] == '=' && (isLower(s[i]) || isUpper(s[i]) || s[i] == '_')
}

// sortEdits orders spans and drops those that overlap an earlier one.
func sortEdits(e []edit) []edit {
	sort.Slice(e, func(a, b int) bool { return e[a].from < e[b].from })
	out := e[:0]
	end := 0
	for _, x := range e {
		if x.from < end {
			continue
		}
		out = append(out, x)
		end = x.to
	}
	return out
}
