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

// maxIdent bounds how far a key name is read to the left of the word that marks it, and how much of the text after the word is
// weighed for its meaning ("_count"). The name itself is read to its end whatever its length.
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
	if !strings.ContainsAny(s, firstBytes) {
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
	if len(tail) > maxIdent {
		tail = tail[:maxIdent] // only the first words of the tail matter; the cap keeps the work per word small
	}
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
		if ws == 0 || (id[ws-1] != '_' && id[ws-1] != '.' && id[ws-1] != '-') || tail != "" {
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
		return classNone // "tokens=3" counts tokens; "passwords: none" is a sentence
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
	out, _, _ := maskPend(s, mask)
	return out
}

// What a line leaves open for the next one (see Carry).
const (
	pendNone         = 0
	pendBlock        = 1  // a YAML block scalar header ("password: |") ends the line: the indented lines after it are the value
	pendNext         = 2  // a key ends the line with no value: the next line is the value
	pendNextIndented = 3  // the same for a compound name after a colon ("client_secret:"): only an indented next line is the value
	pendNextStrong   = 4  // the name is clearly a secret ("DB_PASSWORD=", "<Password>"): an indented, quoted or listed next line is the value
	pendColon        = 16 // added to pendNext, pendNextStrong and pendNextIndented when the key ended with a colon: the value is indented deeper
	pendNextText     = 6  // an XML secret element ends the line: the next line is its text unless it starts a tag
	pendNextQuoted   = 5  // a bare "token:" or "secret:" (also a sentence word): only a quoted next line is the value
	pendQuote        = 7
	pendBracket      = 8
	pendXMLText      = 9
	pendTag          = 10
)

// maskPend is Mask that also says what the text leaves open at its end.
func maskPend(s, mask string) (string, int, carryHint) {
	if len(s) < 4 {
		return s, pendNone, carryHint{}
	}
	w := newWork(len(s))
	s = maskRecords(s, mask, w)
	s = maskFlags(s, mask, w)
	s, p1 := maskXML(s, mask, w)
	xmlCarry := w.carry
	w.carry = carryHint{}
	s, p2 := maskKV(s, mask, w)
	hint := xmlCarry
	// A split XML tag or an XML element text hides the next line's value already; the key=value state must not replace it
	// ("<Password client_secret:" then ">FAKEPW</Password>"). A quote or bracket left open still wins: it swallows the lines. They are
	// told apart by kind: their numbers are below the XML ones, so comparing numbers would never let them win ("DB_PASSWORD=\"x <a").
	xmlOpen := p1 == pendTag || p1 == pendXMLText
	if p2 == pendQuote || p2 == pendBracket || (!xmlOpen && p2 > p1) {
		p1, hint = p2, w.carry
	} else if p1 == pendTag && p2 != pendNone && p2&^pendColon >= pendBlock && p2&^pendColon <= pendNextText {
		// A block or a next-line value that opens beside a split tag is kept next to it, so the key=value reader runs under the tag.
		hint.sub = p2
	}
	return s, p1, hint
}

// maskKV masks the value after "key=" or "key:" for every secret key in s.
func maskKV(s, mask string, w *work) (string, int) {
	locs := findWords(s)
	if len(locs) == 0 {
		return s, pendNone
	}
	pending := pendNone
	failHi, failClasses := -1, 0 // the classes already tried (and failed) for the name that ends at failHi
	var edits []edit
	pos := 0      // everything before pos is read already
	identEnd := 0 // where the name that the last word sits in ends
	for _, m := range locs {
		if m[0] < pos {
			continue
		}
		w.spend(32) // the name around the word and its class
		if w.out() {
			// Fail closed: the text costs more than the budget, so the rest of it goes.
			from := m[1]
			if from < pos {
				from = pos
			}
			edits = append(edits, edit{from, len(s)})
			break
		}
		lo := m[0]
		for lo > pos && m[0]-lo < maxIdent && isIdent(s[lo-1]) {
			lo--
		}
		// The name runs to its end however long it is (a name of 200 characters before "=" is still a name). The end is
		// remembered, so a run made of many secret words is read once.
		if m[0] >= identEnd {
			identEnd = m[1]
			for identEnd < len(s) && isIdent(s[identEnd]) {
				identEnd++
			}
			w.spend(identEnd - m[1])
		}
		hi := identEnd
		idEnd := hi
		if idEnd > m[1]+maxIdent {
			idEnd = m[1] + maxIdent
		}
		class := wordClass(s[lo:idEnd], m[0]-lo, m[1]-lo)
		if class == classNone {
			continue
		}
		bare := hi-lo == m[1]-m[0]
		inQuery := lo > 0 && (s[lo-1] == '?' || s[lo-1] == '&')
		// A name made of many secret words ("password_password_...") is read once per class, not once per word: the answer depends
		// on where the name ends and on the class, and reading the same long stretch again for each word would be quadratic.
		if failHi != hi {
			failHi, failClasses = hi, 0
		}
		if failClasses&(1<<class) != 0 {
			continue
		}
		pend := pendNone
		from, to, next, ok := readValue(s, hi, class, inQuery, bare, lo, &pend, w)
		if pend > pending {
			pending = pend
		}
		if !ok {
			failClasses |= 1 << class
			continue
		}
		if to > from && s[from:to] != mask {
			edits = append(edits, edit{from, to})
		}
		pos = next
	}
	return apply(s, mask, edits), pending
}

// readValue reads what follows a key that ends at i: an optional closing quote of the key, the "=" or ":", and the value. It
// returns the span of the value to mask, where reading went on to, and false when no value follows.
func readValue(s string, i int, class int, inQuery, bare bool, keyAt int, pend *int, w *work) (from, to, next int, ok bool) {
	j := i
	far := i // the furthest position read, for the work count
	defer func() { w.spend(far - i + 1) }()
	see := func(p int) {
		if p > far {
			far = p
		}
	}
	jsonKey := false
	k := j
	for k < len(s) && s[k] == '\\' {
		k++
	}
	see(k)
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
	see(j)
	// A value on the next line: nothing follows the key on its line, so the value is the next line (one line break, then spaces;
	// a blank line ends the search).
	if colon && j < len(s) {
		// A YAML block scalar ("password: |" and the indented lines under it).
		if hdrEnd, isBlock := blockHeader(s, j, w); isBlock {
			see(hdrEnd)
			if from, to, found := blockText(s, hdrEnd, keyAt, w); found {
				return from, to, to, true
			}
			if hdrEnd >= len(s) || (s[hdrEnd] == '\r' && hdrEnd+1 == len(s)) {
				*pend = pendBlock // the header ends the text: the indented lines that follow are the value
			}
			return 0, 0, 0, false
		}
	}
	if j >= len(s) || s[j] == '\r' || s[j] == '\n' {
		if j >= len(s) || (s[j] == '\r' && j+1 == len(s)) {
			switch {
			case colon && class == classToken && !jsonKey && bare:
				*pend = pendNextQuoted
			case colon && class != classPassword && !jsonKey:
				*pend = pendNextIndented
			case !bare:
				*pend = pendNextStrong
			default:
				*pend = pendNext
			}
			if colon {
				*pend |= pendColon
			}
		}
		quotedOnly := colon && class == classToken && !jsonKey && bare // "token:" is also a sentence word: only a quoted next line is a value
		if j >= len(s) {
			return 0, 0, 0, false
		}
		if s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n' {
			j += 2
		} else {
			j++
		}
		if !quotedOnly && colon && class != classPassword && !jsonKey && (j >= len(s) || (s[j] != ' ' && s[j] != '\t')) {
			return 0, 0, 0, false // a name such as client_secret: with its value on the next line counts only when that line is indented (YAML)
		}
		ls2 := j // where the line that may hold the value starts
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		see(j)
		// A comment line between the key and the value (YAML): the value is the line after it.
		for n := 0; n < 4 && j < len(s) && s[j] == '#'; n++ {
			j = w.lineEnd(s, j)
			see(j)
			if j >= len(s) {
				return 0, 0, 0, false
			}
			if s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n' {
				j += 2
			} else {
				j++
			}
			ls2 = j
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
		}
		if j >= len(s) || s[j] == '\r' || s[j] == '\n' {
			return 0, 0, 0, false
		}
		if colon && !jsonKey && j-ls2 <= w.lineIndent(s, w.lineStart(s, keyAt)) && looksLikeKey(s[j:]) {
			// After a colon (YAML) the value is indented deeper than the key: a line at the same depth is the next key ("client_secret:"
			// with no value, then "client_id: abc").
			return 0, 0, 0, false
		}
		if quotedOnly && s[j] != '"' && s[j] != '\'' {
			return 0, 0, 0, false
		}
	}
	// A quoted value, plain ("..."), escaped (\"...\", JSON inside a JSON string) or in single quotes.
	q, nest := j, 0
	for q < len(s) && s[q] == '\\' {
		q++
		nest++
	}
	see(q)
	if q < len(s) && (s[q] == '"' || s[q] == '\'') {
		end, after := closingQuote(s, q, nest, !jsonKey)
		see(after)
		if end == len(s) && after == len(s) {
			*pend = pendQuote
			w.carry = carryHint{quote: s[q], nest: nest, literal: !jsonKey, class: class}
		}
		if nest == 0 && after <= len(s) && end < len(s) && !valueSeparator(s, after) {
			// "x"y: text sticks to the closing quote, so the quote was not the end of the value (fail closed: take the rest of it).
			end = unquotedEnd(s, after, class, false, false)
			see(end)
			return q + 1, end, end, true
		}
		return q + 1, end, after, true
	}
	if jsonKey && (s[j] == '[' || s[j] == '{') {
		end, depth, quote := bracketEndState(s, j)
		see(end)
		if depth > 0 {
			*pend = pendBracket
			w.carry = carryHint{depth: depth, quote: quote}
		}
		return j, end, end, true
	}
	if class == classToken && colon && !jsonKey && bare {
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
				see(end + 1)
				return j + 1, end, end + 1, true // the braces stay, so a pattern that runs after this one reads the value as braced
			}
			end++
		}
		see(len(s))
		return j + 1, len(s), len(s), true // never closed: the rest of the text
	}
	end := unquotedEnd(s, j, class, jsonKey, inQuery)
	see(end)
	return j, end, end, true
}

// closingQuote finds the end of a quoted value that opens at q (a quote character preceded by nest backslashes). A quote ends the
// value unless it is escaped with a backslash, or doubled (ADO). For a value written inside a JSON string (nest > 0) the closing
// quote has the same number of backslashes. A value that never closes runs to the end of the text. It returns the end of the value
// text and where reading goes on.
func closingQuote(s string, q, nest int, literal bool) (end, after int) {
	qc := s[q]
	firstEscaped := -1
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
				if firstEscaped < 0 {
					firstEscaped = t
				}
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
	if literal && firstEscaped >= 0 {
		// A value that never closes with a plain quote, and whose key was not quoted (ADO, an environment variable, a path such as
		// "C:\dir\"): the backslash before the first quote is a character of the value, and that quote ends it. A quoted key (JSON) keeps
		// the escape and the value runs to the end of the text.
		return firstEscaped, firstEscaped + 1
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
		if !isLower(c) && !isUpper(c) && (c < '0' || c > '9') && c != '#' {
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

// sortEdits orders spans (longer first on a tie) and joins those that overlap.
func sortEdits(e []edit) []edit {
	sort.Slice(e, func(a, b int) bool {
		if e[a].from != e[b].from {
			return e[a].from < e[b].from
		}
		return e[a].to > e[b].to
	})
	out := e[:0]
	for _, x := range e {
		if n := len(out); n > 0 && x.from < out[n-1].to {
			// Overlapping spans are joined: the union is masked, never the shorter of the two.
			if x.to > out[n-1].to {
				out[n-1].to = x.to
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

// valueSeparator says whether the text at i can follow a finished value: the end of the text, a space, or a mark that ends a setting.
func valueSeparator(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	switch s[i] {
	case ' ', '\t', '\r', '\n', ';', ',', '}', ']', ')', '>', '<', '&', '/', ':', '|', '"', '\'':
		return true
	}
	return false
}

// looksLikeKey reports whether text starts with "name:" followed by a space, a tab or the end of the line: a YAML key.
// It reads at most maxIdent+2 bytes.
func looksLikeKey(t string) bool {
	i := 0
	if i < len(t) && (t[i] == '"' || t[i] == '\'') {
		i++
	}
	st := i
	for i < len(t) && i-st <= maxIdent && (isIdent(t[i]) || t[i] == '.' || t[i] == '-') {
		i++
	}
	if i == st || i-st > maxIdent {
		return false
	}
	if i < len(t) && (t[i] == '"' || t[i] == '\'') {
		i++
	}
	if i >= len(t) || t[i] != ':' {
		return false
	}
	i++
	return i >= len(t) || t[i] == ' ' || t[i] == '\t' || t[i] == '\r' || t[i] == '\n'
}

// bracketEnd returns where the JSON array or object that opens at i ends: after the matching close, with quoted text skipped. One that
// never closes runs to the end of the text.
func bracketEnd(s string, i int) int {
	end, _, _ := bracketEndState(s, i)
	return end
}

func bracketEndState(s string, i int) (end, depth int, quote byte) {
	for k := i; k < len(s); k++ {
		if quote != 0 {
			switch s[k] {
			case '\\':
				k++
			case quote:
				quote = 0
			}
			continue
		}
		switch s[k] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return k + 1, 0, 0
			}
		case '"', '\'':
			quote = s[k]
		}
	}
	return len(s), depth, quote
}

// blockHeader says whether the text at j is the header of a YAML block scalar ("|", ">", "|-", ">+", "|2") that ends its line. It
// returns where the header ends.
func blockHeader(s string, j int, w *work) (end int, ok bool) {
	j0, k := j, 0
	defer func() { // every exit is charged for the text this read
		if k < j {
			k = j
		}
		w.spend(k - j0 + 1)
	}()
	// A tag ("!!str", "!secret") or an anchor ("&a") may come first. One longer than maxIdent is not a tag: reading on to the next
	// space would read the same stretch again for every key that follows it.
	for n := 0; n < 2 && j < len(s) && (s[j] == '!' || s[j] == '&'); n++ {
		for t := j; j < len(s) && j-t < maxIdent && s[j] != ' ' && s[j] != '\t' && s[j] != '\r' && s[j] != '\n'; {
			j++
		}
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
	}
	if j >= len(s) || (s[j] != '|' && s[j] != '>') {
		return 0, false
	}
	k = j + 1
	for n := 0; n < 2 && k < len(s) && (s[k] == '+' || s[k] == '-' || (s[k] >= '1' && s[k] <= '9')); n++ {
		k++
	}
	for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
		k++
	}
	if k < len(s) && s[k] == '#' && k > 0 && (s[k-1] == ' ' || s[k-1] == '\t') {
		k = w.lineEnd(s, k) // a comment after the header runs to the end of the line
	}
	if k < len(s) && s[k] != '\r' && s[k] != '\n' {
		return 0, false
	}
	return k, true
}

// blockText finds the text of a block scalar whose header ends at p: the lines after it that are indented deeper than the line of the
// key (blank lines between them belong to it). It returns the span from the first text to the end of the last line of text.
func blockText(s string, p, keyAt int, w *work) (from, to int, ok bool) {
	ls := w.lineStart(s, keyAt)
	keyIndent := w.lineIndent(s, ls)
	for p < len(s) {
		// p is at a line break; the next line starts after it.
		if s[p] == '\r' && p+1 < len(s) && s[p+1] == '\n' {
			p += 2
		} else {
			p++
		}
		e := p
		if i := strings.IndexByte(s[p:], '\n'); i >= 0 {
			e = p + i
		} else {
			e = len(s)
		}
		w.spend(e - p + 1)
		if w.out() {
			// Over budget: fail closed, the rest of the text is the value.
			if !ok {
				from, ok = p, true
			}
			to = len(s)
			break
		}
		line := s[p:e]
		text := strings.TrimRight(line, "\r \t")
		indent := 0
		for indent < len(text) && (text[indent] == ' ' || text[indent] == '\t') {
			indent++
		}
		if indent < len(text) { // a line with text
			if indent <= keyIndent {
				break
			}
			if !ok {
				from, ok = p+indent, true
			}
			to = p + len(text)
		}
		p = e
	}
	return from, to, ok
}
