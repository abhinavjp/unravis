package secretscrub

import "strings"

// Name and value records with unquoted member names (SWP-1122-14): the same record as in record.go, written by other tools.
//
//   - Flow YAML: {name: DB_PASSWORD, value: x}
//   - Go prints: map[name:DB_PASSWORD value:x] and {Name:DB_PASSWORD Value:x} (also with quoted values, %#v)
//   - Python repr and keyword calls: EnvVar(name='DB_PASSWORD', value='x')
//   - HCL: env { name = "DB_PASSWORD" value = "x" } (members on separate lines too)
//
// One pass with a stack of open brackets, like the JSON reader. Quotes are not skipped: the same record can sit inside a JSON string. A member is a name, a ":" or "=", and a value (a quoted string or a word
// that ends at white space, a comma, a semicolon or a bracket). Members count only inside a bracket. A record whose name member is a
// secret name masks every "value" member. A quoted string, also in escaped form (\"x\" inside a JSON string), is a value as it is. A bare
// "value" runs on until the bracket that closes the group, the end of the line, a quote or a backslash, or a separator that is followed by
// the next member ("name:", so "pa, ss: x" is read as a value "pa" and a member), so a password with a comma, a space or a bracket in it is masked whole; the bracket of the group itself, and
// a backslash, still end it. A value that starts with a bracket (a list, a nested record) is not read here. An HCL heredoc
// (<<EOT) is masked up to its end line. A quoted value of any other member is skipped, so a bracket or "value:" inside it counts for
// nothing. Every byte read is charged; over budget, the rest of the text is masked.

func pairRecords(s string, edits []edit, w *work) ([]edit, int) {
	var stack []jframe
	var open []byte           // the bracket that opened each frame
	memo := map[string]bool{} // heredoc tags whose end line was searched for and not found
	i := 0
	stop := len(s)
	for i < len(s) {
		if w.out() {
			stop = i
			break
		}
		c := s[i]
		w.spend(1)
		switch {
		case c == '{' || c == '(' || c == '[':
			if len(stack) >= maxRecordDepth {
				edits = stack[0].flush(edits)
				copy(stack, stack[1:])
				copy(open, open[1:])
				stack, open = stack[:len(stack)-1], open[:len(open)-1]
			}
			stack, open = append(stack, jframe{}), append(open, c)
			i++
		case c == '}' || c == ')' || c == ']':
			if len(stack) > 0 {
				edits = stack[len(stack)-1].flush(edits)
				stack, open = stack[:len(stack)-1], open[:len(open)-1]
			}
			i++
		case isNameStart(c) && (i == 0 || !isIdent(s[i-1])):
			e := i + 1
			for e < len(s) && e-i <= maxIdent && isIdent(s[e]) {
				e++
			}
			w.spend(e - i)
			k := e
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			if len(stack) == 0 || k >= len(s) || e-i > maxIdent || !pairSep(s, k) {
				i = e
				continue
			}
			top := &stack[len(stack)-1]
			key := strings.ToLower(s[i:e])
			isName, isValue := nameKeys[key], key == "value"
			v := pairValueStart(s, k+1)
			if !isName && !isValue {
				// Another member: a quoted value is skipped whole, so a bracket or a "value:" inside it counts for nothing.
				if v < len(s) && (s[v] == '"' || s[v] == '\'' || (s[v] == '\\' && escOpens(s, v))) {
					_, _, next := pairQuoted(s, v)
					w.spend(next - v + 1)
					i = max(next, e)
				} else {
					i = e
				}
				continue
			}
			var from, to, next int
			if isValue && v < len(s) && s[v] != '"' && s[v] != '\'' && s[v] != '\\' {
				from, to, next = pairBare(s, v, open[len(open)-1], w, memo)
			} else {
				from, to, next = pairValue(s, v)
			}
			w.spend(next - v + 1)
			if to > from {
				if isName && recordName(s[from:to]) && IsSecretKey(s[from:to]) {
					top.secret = true
				}
				if isValue {
					top.addSpan(from, to)
				}
			}
			i = max(next, e)
		default:
			i++
		}
	}
	for n := len(stack) - 1; n >= 0; n-- {
		edits = stack[n].flush(edits)
	}
	return edits, stop
}

// pairSep says whether s[k] is the ":" or "=" between a member name and its value: not "==", "=>", "::", ":=" or a ":" that starts "://".
func pairSep(s string, k int) bool {
	switch s[k] {
	case ':':
		return k+1 >= len(s) || (s[k+1] != ':' && s[k+1] != '=' && (s[k+1] != '/' || k+2 >= len(s) || s[k+2] != '/'))
	case '=':
		return k+1 >= len(s) || (s[k+1] != '=' && s[k+1] != '>')
	}
	return false
}

// pairValueStart returns where the value of a member begins: after white space, also over line breaks when no member starts there
// ("value:" and the value on the next line), and after a YAML tag or anchor ("!!str x", "&a x").
func pairValueStart(s string, v int) int {
	for n := 0; n < 3; n++ {
		for v < len(s) && (s[v] == ' ' || s[v] == '\t') {
			v++
		}
		if v < len(s) && (s[v] == '\n' || s[v] == '\r') {
			k := skipSpace(s, v)
			if k >= len(s) || memberAt(s, k) {
				return v
			}
			v = k
		}
		if v < len(s) && (s[v] == '!' || s[v] == '&') {
			k := v
			for k < len(s) && s[k] != ' ' && s[k] != '\t' && s[k] != '\n' && s[k] != '\r' {
				k++
			}
			if k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				v = k
				continue
			}
		}
		break
	}
	return v
}

// memberAt says whether a member ("name:" or "name =") starts at k.
func memberAt(s string, k int) bool {
	if k >= len(s) || !isNameStart(s[k]) {
		return false
	}
	e := k + 1
	for e < len(s) && e-k <= maxIdent && isIdent(s[e]) {
		e++
	}
	for e < len(s) && (s[e] == ' ' || s[e] == '\t') {
		e++
	}
	return e < len(s) && pairSep(s, e)
}

// pairQuoted reads the quoted string that starts at v, in plain or escaped form (\"x\" inside a JSON string). A string that never
// closes on its line is read up to the next double quote or the end of the line, since the record may still be a secret.
func pairQuoted(s string, v int) (from, to, next int) {
	esc := s[v] == '\\'
	f, t, n, _, ok := recordString(s, v, esc)
	if ok && s[v] == '\'' && bareQuote(s[f:t]) {
		ok = false // an apostrophe string that holds a double quote ran across the end of a JSON string: it never closed
	}
	if !ok {
		if esc {
			return v, v, v + 1
		}
		e := v + 1
		for e < len(s) && s[e] != '\n' && s[e] != '\r' && s[e] != '"' {
			if s[e] == '\\' && e+1 < len(s) && s[e+1] != '\n' && s[e+1] != '\r' {
				e++ // an escaped character stays whole, so the text around a JSON string stays valid
			}
			e++
		}
		e = min(e, len(s))
		f, t, n = v+1, e, e
	}
	k := f
	for k < t && (s[k] == ' ' || s[k] == '\t') {
		k++
	}
	if k < t && strings.IndexByte(",:}]", s[k]) >= 0 {
		// The text starts like what follows a JSON string ("value: ","b":1}" or "value: "}"): this quote closed a JSON string, it did
		// not open a value. A secret does not start with one of these.
		return v, v, v + 1
	}
	return f, t, n
}

// bareQuote says whether t holds a double quote that is not escaped.
func bareQuote(t string) bool {
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '\\':
			i++
		case '"':
			return true
		}
	}
	return false
}

// pairValue reads the value that starts at v: a quoted string (inside its quotes) or a word that ends at white space, a comma, a
// semicolon or a bracket. A bracket or an empty value gives an empty span.
func pairValue(s string, v int) (from, to, next int) {
	if v >= len(s) {
		return v, v, v
	}
	switch s[v] {
	case '"', '\'':
		return pairQuoted(s, v)
	case '\\':
		if escOpens(s, v) {
			return pairQuoted(s, v)
		}
		return v, v, v
	case '{', '(', '[', '}', ')', ']', ',', ';', '\n', '\r':
		return v, v, v
	}
	e := v
	for e < len(s) && !strings.ContainsRune(" \t\r\n,;({[)}]\"'\\", rune(s[e])) {
		e++
	}
	return v, e, e
}

// closerOf returns the bracket that closes the group opened by c.
func closerOf(c byte) byte {
	switch c {
	case '(':
		return ')'
	case '[':
		return ']'
	}
	return '}'
}

// pairBare reads a bare value of a "value" member, see the comment at the top of the file. opener is the bracket of the group.
func pairBare(s string, v int, opener byte, w *work, memo map[string]bool) (from, to, next int) {
	if strings.HasPrefix(s[v:], "<<") {
		return pairHeredoc(s, v, w, memo)
	}
	switch s[v] {
	case '{', '(', '[', '}', ')', ']', ',', ';', '\n', '\r':
		return v, v, v
	}
	closer := closerOf(opener)
	inner := make([]byte, 0, 16) // the closers still expected for the brackets opened inside the value
	e := v
loop:
	for e < len(s) {
		switch c := s[e]; c {
		case '\n', '\r', '"', '\\':
			break loop
		case '(', '[', '{':
			if len(inner) < 16 {
				inner = append(inner, closerOf(c))
			}
		case ')', ']', '}':
			if n := len(inner); n > 0 && inner[n-1] == c {
				inner = inner[:n-1]
			} else if c == closer {
				break loop // the bracket of the group itself, also when a bracket inside the value never closed
			}
		case ' ', '\t', ',', ';':
			r := e
			for r < len(s) && (s[r] == ' ' || s[r] == '\t' || s[r] == ',' || s[r] == ';') {
				r++
			}
			w.spend(r - e)
			if len(inner) == 0 && memberAt(s, r) {
				break loop
			}
			e = r
			continue
		}
		e++
	}
	w.spend(e - v)
	to = e
	for to > v && (s[to-1] == ' ' || s[to-1] == '\t' || s[to-1] == ',' || s[to-1] == ';') {
		to--
	}
	return v, to, e
}

// pairHeredoc masks the body of an HCL heredoc (value = <<EOT, up to the line that holds EOT alone). The marker stays readable. A
// heredoc that never ends is masked to the end of the text, but the scan goes on after its first line, so a record after it is still
// read. A heredoc tag that was searched for and not found is not searched for again (memo of up to 16 tags), so repeated unfinished heredocs stay linear.
func pairHeredoc(s string, v int, w *work, memo map[string]bool) (from, to, next int) {
	k := v + 2
	if k < len(s) && s[k] == '-' {
		k++
	}
	t := k
	for t < len(s) && isIdent(s[t]) {
		t++
	}
	tag := s[k:t]
	nl := strings.IndexByte(s[t:], '\n')
	if tag == "" || nl < 0 {
		return v, v, t
	}
	from = t + nl + 1
	if memo[tag] {
		return from, len(s), from
	}
	for ls := from; ls <= len(s); {
		le := len(s)
		if n := strings.IndexByte(s[ls:], '\n'); n >= 0 {
			le = ls + n
		}
		w.spend(le - ls + 1)
		if strings.TrimSpace(s[ls:le]) == tag {
			to = ls
			for to > from && (s[to-1] == '\n' || s[to-1] == '\r') {
				to--
			}
			return from, to, le
		}
		if le >= len(s) {
			break
		}
		ls = le + 1
	}
	if len(memo) < 16 {
		memo[tag] = true
	}
	return from, len(s), from
}
