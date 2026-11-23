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
// secret name masks every "value" member. A value that is a bracket (a list, a nested record) is not read here. A bare value with white
// space inside (a Go print of a string with spaces) is masked up to the first space. Every byte read is charged; over budget, the rest of
// the text is masked.

func pairRecords(s string, edits []edit, w *work) ([]edit, int) {
	var stack []jframe
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
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, jframe{})
			i++
		case c == '}' || c == ')' || c == ']':
			if len(stack) > 0 {
				edits = stack[len(stack)-1].flush(edits)
				stack = stack[:len(stack)-1]
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
			if !isName && !isValue {
				i = e
				continue
			}
			v := k + 1
			for v < len(s) && (s[v] == ' ' || s[v] == '\t') {
				v++
			}
			from, to, next := pairValue(s, v)
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
		return k+1 >= len(s) || (s[k+1] != ':' && s[k+1] != '=' && !(s[k+1] == '/' && k+2 < len(s) && s[k+2] == '/'))
	case '=':
		return k+1 >= len(s) || (s[k+1] != '=' && s[k+1] != '>')
	}
	return false
}

// pairValue reads the value that starts at v: a quoted string (inside its quotes) or a word. It returns the span and where to go on. A
// quote that never closes on its line is read to the end of the line, since the record may still be a secret. A bracket or an empty
// value gives an empty span.
func pairValue(s string, v int) (from, to, next int) {
	if v >= len(s) {
		return v, v, v
	}
	switch s[v] {
	case '"', '\'':
		f, t, n, _, ok := recordString(s, v, false)
		if ok {
			return f, t, n
		}
		e := v + 1
		for e < len(s) && s[e] != '\n' && s[e] != '\r' {
			e++
		}
		return v + 1, e, e
	case '{', '(', '[', '}', ')', ']', ',', ';', '\n', '\r':
		return v, v, v
	}
	e := v
	for e < len(s) && !strings.ContainsRune(" \t\r\n,;)}]\"'\\", rune(s[e])) {
		e++
	}
	return v, e, e
}
