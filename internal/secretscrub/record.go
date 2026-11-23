package secretscrub

import "strings"

// Record forms (SWP-1122-13): a secret whose name and value are two fields of one record, so no "KEY=value" or "key: value" pair holds
// both. Three readers find the record around the name and mask the value member.
//
//   - JSON (and the same shape in a log line): {"name":"DB_PASSWORD","value":"x"} in either order, also {"Key":..,"Value":..}, inside
//     an array, with other members around. One pass with a stack of objects; a member that never closes is judged at the end of text.
//   - YAML list (Kubernetes and Helm env, Azure and ECS settings in YAML): "- name: DB_PASSWORD" with "value: x" on a line of the same
//     record, before or after it. The record is the lines at the indent of the name key, read at most maxRecordLines each way.
//   - Property list (plist): <key>Password</key> followed by the next element, <string>, <data> or <integer>.
//
// Every reader charges the work budget for what it reads; over budget, the rest of the text is masked (fail closed), as in the other
// readers. They run on the whole text only: Carry sees one line at a time and does not carry a record from one line to the next.

// maxRecordDepth bounds the stack of open objects. A deeper object is read as part of its parent: its members are not judged.
const maxRecordDepth = 32

// maxRecordString bounds a string read as a member name or value. A longer string is not a member of a record.
const maxRecordString = 4096

// maxRecordLines bounds how many lines of a YAML record are read on each side of the name key.
const maxRecordLines = 12

// nameKeys are the member names that hold the name of a setting in a name and value record.
var nameKeys = map[string]bool{"name": true, "key": true, "setting": true, "variable": true, "settingname": true, "varname": true}

func maskRecords(s, mask string, w *work) string {
	if len(s) < 12 {
		return s
	}
	var edits []edit
	if strings.IndexByte(s, '{') >= 0 && strings.ContainsAny(s, "\"'") {
		edits = jsonRecords(s, edits, w)
	}
	if strings.Contains(s, "</key>") {
		edits = plistRecords(s, edits, w)
	}
	if strings.IndexByte(s, ':') >= 0 && strings.IndexByte(s, '\n') >= 0 {
		edits = yamlRecords(s, edits, w)
	}
	if w.out() {
		edits = append(edits, edit{lastEnd(edits), len(s)})
	}
	if len(edits) == 0 {
		return s
	}
	return apply(s, mask, sortEdits(edits))
}

// lastEnd is where the fail-closed edit of an over-budget scan starts: after the last span already found (spans may be unsorted).
func lastEnd(edits []edit) int {
	m := 0
	for _, e := range edits {
		if e.to > m {
			m = e.to
		}
	}
	return m
}

type jframe struct {
	key        string // the member whose value is read next, lower case; "" between members
	secret     bool   // a name member holds the name of a secret
	vFrom, vTo int    // the span of the value member to mask; vTo <= vFrom: none yet
}

func (f *jframe) flush(edits []edit) []edit {
	if f.secret && f.vTo > f.vFrom {
		edits = append(edits, edit{f.vFrom, f.vTo})
	}
	return edits
}

// jsonStringEnd returns the index of the closing quote of the string that opens at q, or -1 when there is none within
// maxRecordString bytes (the quote is then an ordinary character).
func jsonStringEnd(s string, q int) int {
	return quotedEnd(s, q, s[q])
}

// quotedEnd is jsonStringEnd for either quote character.
func quotedEnd(s string, q int, quote byte) int {
	limit := q + 1 + maxRecordString
	if limit > len(s) {
		limit = len(s)
	}
	for k := q + 1; k < limit; k++ {
		switch s[k] {
		case '\\':
			k++
		case quote:
			return k
		}
	}
	return -1
}

// opensSingleQuoted says whether the apostrophe at i opens a string in a Python-style record ({'name': 'X', 'value': 'y'}): the
// last mark before it is one of "{[,:". An apostrophe inside a word or a sentence ("it's") does not.
func opensSingleQuoted(s string, i int) bool {
	for k := i - 1; k >= 0 && i-k <= 8; k-- {
		switch s[k] {
		case ' ', '\t':
			continue
		case '{', '[', ',', ':':
			return true
		}
		return false
	}
	return false
}

func skipSpace(s string, k int) int {
	for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\r' || s[k] == '\n') {
		k++
	}
	return k
}

func jsonRecords(s string, edits []edit, w *work) []edit {
	var stack []jframe
	over := 0 // objects opened beyond maxRecordDepth
	i := 0
	for i < len(s) {
		if w.out() {
			return edits
		}
		c := s[i]
		w.spend(1)
		switch c {
		case '{':
			if len(stack) < maxRecordDepth {
				stack = append(stack, jframe{})
			} else {
				over++
			}
			i++
		case '}':
			if over > 0 {
				over--
			} else if len(stack) > 0 {
				edits = stack[len(stack)-1].flush(edits)
				stack = stack[:len(stack)-1]
			}
			i++
		case '"', '\'':
			if c == '\'' && !opensSingleQuoted(s, i) {
				i++
				continue
			}
			j := quotedEnd(s, i, c)
			if j < 0 {
				i++
				continue
			}
			w.spend(j - i)
			if over > 0 || len(stack) == 0 {
				i = j + 1
				continue
			}
			top := &stack[len(stack)-1]
			content := s[i+1 : j]
			k := skipSpace(s, j+1)
			if k < len(s) && s[k] == ':' {
				// A member name. Read the first byte of its value: a literal value of a "value" member is a span to mask as well.
				top.key = strings.ToLower(content)
				if len(top.key) > maxIdent {
					top.key = top.key[:maxIdent]
				}
				v := skipSpace(s, k+1)
				if top.key == "value" && v < len(s) && s[v] != '"' && s[v] != '\'' && s[v] != '{' && s[v] != '[' {
					e := v
					for e < len(s) && e-v < maxRecordString && s[e] != ',' && s[e] != '}' && s[e] != ']' && s[e] != ' ' && s[e] != '\n' && s[e] != '\r' && s[e] != '\t' {
						e++
					}
					w.spend(e - v)
					top.vFrom, top.vTo = v, e
					top.key = ""
				}
				i = k + 1
				continue
			}
			// A string value.
			if nameKeys[top.key] && IsSecretKey(content) {
				top.secret = true
			}
			if top.key == "value" {
				top.vFrom, top.vTo = i+1, j
			}
			top.key = ""
			i = j + 1
		default:
			i++
		}
	}
	// Objects that never closed: the record is read as far as it goes.
	for n := len(stack) - 1; n >= 0; n-- {
		edits = stack[n].flush(edits)
	}
	return edits
}

// plistRecords masks the element after <key>Name</key> when Name is a secret key name.
func plistRecords(s string, edits []edit, w *work) []edit {
	pos := 0
	for pos < len(s) {
		if w.out() {
			return edits
		}
		i := strings.Index(s[pos:], "<key>")
		if i < 0 {
			break
		}
		i += pos
		w.spend(i - pos + 8)
		nameFrom := i + len("<key>")
		end := strings.Index(s[nameFrom:min(len(s), nameFrom+2*maxIdent+8)], "</key>")
		if end < 0 {
			pos = nameFrom
			continue
		}
		name := strings.TrimSpace(s[nameFrom : nameFrom+end])
		p := nameFrom + end + len("</key>")
		pos = p
		if name == "" || !IsSecretKey(name) {
			continue
		}
		p = skipSpace(s, p)
		w.spend(8)
		for _, tag := range []string{"string", "data", "integer", "real"} {
			open := "<" + tag + ">"
			if !strings.HasPrefix(s[p:], open) {
				continue
			}
			from := p + len(open)
			stop := strings.Index(s[from:], "</"+tag+">")
			to := len(s) // no close tag: fail closed, the value runs to the end of the text
			if stop >= 0 {
				to = from + stop
			}
			w.spend(to - from)
			if to > from {
				edits = append(edits, edit{from, to})
			}
			pos = to
			break
		}
	}
	return edits
}

type yline struct {
	start, end int // the line without its line break
	indent     int
}

// yamlName reads "name:" (or "- name:") with a secret key name as its value from the line. dash says the line opens a list item.
func yamlName(s string, l yline) (secret, dash bool) {
	t := s[l.start+l.indent : l.end]
	if strings.HasPrefix(t, "- ") {
		dash = true
		t = strings.TrimLeft(t[2:], " \t")
	}
	if len(t) > 0 && (t[0] == '"' || t[0] == '\'') {
		t = t[1:]
	}
	colon := strings.IndexByte(t, ':')
	if colon <= 0 || colon > 16 {
		return false, dash
	}
	key := strings.ToLower(strings.TrimRight(t[:colon], "\"' "))
	if !nameKeys[key] {
		return false, dash
	}
	v := strings.TrimSpace(t[colon+1:])
	if h := strings.Index(v, " #"); h >= 0 {
		v = strings.TrimSpace(v[:h])
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v != "" && len(v) <= 4*maxIdent && IsSecretKey(v), dash
}

// yamlValueLine reports whether the line is the "value:" member of a record, and where the text after the colon starts.
func yamlValueLine(s string, l yline) (rest int, ok bool) {
	t := s[l.start+l.indent : l.end]
	off := l.start + l.indent
	if strings.HasPrefix(t, "- ") {
		skip := 2
		for skip < len(t) && (t[skip] == ' ' || t[skip] == '\t') {
			skip++
		}
		t, off = t[skip:], off+skip
	}
	if len(t) > 0 && (t[0] == '"' || t[0] == '\'') {
		t, off = t[1:], off+1
	}
	if len(t) < 6 || !strings.EqualFold(t[:5], "value") {
		return 0, false
	}
	k := 5
	if k < len(t) && (t[k] == '"' || t[k] == '\'') {
		k++
	}
	if k >= len(t) || t[k] != ':' {
		return 0, false
	}
	return off + k + 1, true
}

func yamlRecords(s string, edits []edit, w *work) []edit {
	// Lines, read once.
	var lines []yline
	for start := 0; start <= len(s); {
		e := strings.IndexByte(s[start:], '\n')
		end := len(s)
		next := len(s) + 1
		if e >= 0 {
			end = start + e
			next = end + 1
		}
		ee := end
		if ee > start && s[ee-1] == '\r' {
			ee--
		}
		ind := 0
		for start+ind < ee && (s[start+ind] == ' ' || s[start+ind] == '\t') {
			ind++
		}
		lines = append(lines, yline{start, ee, ind})
		start = next
	}
	w.spend(len(s))
	blank := func(l yline) bool { return l.start+l.indent >= l.end }
	for n, l := range lines {
		if w.out() {
			return edits
		}
		if blank(l) {
			continue
		}
		secret, dash := yamlName(s, l)
		if !secret {
			continue
		}
		rec := l.indent
		if dash {
			rec += 2
		}
		w.spend(maxRecordLines * 4)
		// After the name key.
		for m := n + 1; m < len(lines) && m <= n+maxRecordLines; m++ {
			x := lines[m]
			if blank(x) {
				continue
			}
			if x.indent < rec || (x.indent == rec && strings.HasPrefix(s[x.start+x.indent:x.end], "- ")) {
				break
			}
			if x.indent == rec {
				if rest, ok := yamlValueLine(s, x); ok {
					edits = yamlMaskValue(s, lines, m, rest, x.indent, edits, w)
				}
			}
		}
		// Before it, unless the name key opens the item.
		if dash {
			continue
		}
		for m := n - 1; m >= 0 && m >= n-maxRecordLines; m-- {
			x := lines[m]
			if blank(x) {
				continue
			}
			xs := s[x.start+x.indent : x.end]
			if x.indent < rec {
				if x.indent == rec-2 && strings.HasPrefix(xs, "- ") {
					if rest, ok := yamlValueLine(s, x); ok {
						edits = yamlMaskValue(s, lines, m, rest, rec, edits, w)
					}
				}
				break
			}
			if x.indent == rec {
				if rest, ok := yamlValueLine(s, x); ok {
					edits = yamlMaskValue(s, lines, m, rest, x.indent, edits, w)
				}
			}
		}
	}
	return edits
}

// yamlMaskValue masks the value of a "value:" line: a quoted scalar inside its quotes, a block scalar and its lines, a value on the
// next indented line, or the plain rest of the line. keyIndent is the indent of the member (the indent its continuation lines exceed).
func yamlMaskValue(s string, lines []yline, m, rest, keyIndent int, edits []edit, w *work) []edit {
	l := lines[m]
	k := rest
	for k < l.end && (s[k] == ' ' || s[k] == '\t') {
		k++
	}
	w.spend(l.end - rest + 1)
	if k >= l.end || s[k] == '#' {
		// Nothing on the line: the value is on the next indented line.
		for n := m + 1; n < len(lines) && n <= m+maxCommentLines+1; n++ {
			x := lines[n]
			if x.start+x.indent >= x.end || s[x.start+x.indent] == '#' {
				continue
			}
			if x.indent > keyIndent {
				edits = append(edits, edit{x.start + x.indent, x.end})
			}
			break
		}
		return edits
	}
	switch s[k] {
	case '|', '>':
		for n := m + 1; n < len(lines) && n <= m+maxBlockLines; n++ {
			x := lines[n]
			if x.start+x.indent >= x.end {
				continue
			}
			if x.indent <= keyIndent {
				break
			}
			edits = append(edits, edit{x.start + x.indent, x.end})
		}
		return edits
	case '"', '\'':
		q := s[k]
		for e := k + 1; e < l.end; e++ {
			if q == '"' && s[e] == '\\' {
				e++
				continue
			}
			if s[e] == q {
				if q == '\'' && e+1 < l.end && s[e+1] == '\'' {
					e++
					continue
				}
				if e > k+1 {
					edits = append(edits, edit{k + 1, e})
				}
				return edits
			}
		}
		return append(edits, edit{k + 1, l.end}) // unclosed: to the end of the line
	}
	e := l.end
	if h := strings.Index(s[k:l.end], " #"); h >= 0 {
		e = k + h
	}
	for e > k && (s[e-1] == ' ' || s[e-1] == '\t') {
		e--
	}
	if e > k {
		edits = append(edits, edit{k, e})
	}
	return edits
}
