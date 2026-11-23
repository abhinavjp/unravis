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

// maxRecordDepth bounds the stack of open objects. When it is full, the oldest open object is judged as read and dropped, so objects
// that never closed (cut log lines) do not switch the reader off.
const maxRecordDepth = 32

// maxRecordSpans bounds the value spans kept for one object (a cut record can repeat "value"); more are joined into one span.
const maxRecordSpans = 8

// maxRecordLines bounds how many lines of a YAML record are read on each side of the name key.
const maxRecordLines = 12

// nameKeys are the member names that hold the name of a setting in a name and value record.
var nameKeys = map[string]bool{"name": true, "key": true, "setting": true, "variable": true, "settingname": true, "varname": true}

func maskRecords(s, mask string, w *work) string {
	if len(s) < 12 {
		return s
	}
	var edits []edit
	stop := len(s) // the first position that a reader did not judge
	if strings.IndexByte(s, '{') >= 0 && strings.ContainsAny(s, "\"'") {
		var p int
		edits, p = jsonRecords(s, edits, w)
		stop = min(stop, p)
	}
	if strings.Contains(s, "</key>") {
		var p int
		edits, p = plistRecords(s, edits, w)
		stop = min(stop, p)
	}
	if strings.IndexByte(s, ':') >= 0 && strings.IndexByte(s, '\n') >= 0 {
		var p int
		edits, p = yamlRecords(s, edits, w)
		stop = min(stop, p)
	}
	if w.out() && stop < len(s) {
		// Over budget: fail closed from where the first reader stopped, so nothing a reader did not judge survives.
		edits = append(edits, edit{stop, len(s)})
	}
	if len(edits) == 0 {
		return s
	}
	return apply(s, mask, sortEdits(edits))
}

type jframe struct {
	key    string // the member whose value is read next, lower case; "" between members
	secret bool   // a name member holds the name of a secret
	spans  []edit // the value members seen so far (a duplicate or a cut record can have more than one)
}

// addSpan notes a value span. Past maxRecordSpans the spans are joined into one, so a later value never hides an earlier one.
func (f *jframe) addSpan(from, to int) {
	if to <= from {
		return
	}
	if len(f.spans) >= maxRecordSpans {
		lo, hi := from, to
		for _, e := range f.spans {
			lo, hi = min(lo, e.from), max(hi, e.to)
		}
		f.spans = append(f.spans[:0], edit{lo, hi})
		return
	}
	f.spans = append(f.spans, edit{from, to})
}

func (f *jframe) flush(edits []edit) []edit {
	if f.secret {
		edits = append(edits, f.spans...)
	}
	return edits
}

// recordString reads the string that opens at i. Plain: a quote (" or ') and the next one of the same kind, with backslash escapes.
// Escaped (JSON written inside a JSON string, \"name\":\"x\"): a backslash-quote pair opens it and the next pair closes it; in this
// mode a run of backslashes before a quote closes the string only when the run is 1 mod 4 long (a run of 3 is a quote inside the inner
// value). A string never runs over a line break and has no other cap. It returns the text inside, the index after the close, and how
// many bytes were read (charged by the caller, whether or not the string closed). A string that does not close is read to the end
// of its line (the line break included).
func recordString(s string, i int, esc bool) (from, to, next, read int, ok bool) {
	q := s[i]
	from = i + 1
	if esc {
		from = i + 2
	}
	for k := from; k < len(s); k++ {
		c := s[k]
		if c == '\n' {
			return 0, 0, 0, k - i + 1, false
		}
		if esc {
			if c == '\\' {
				j := k
				for j < len(s) && s[j] == '\\' {
					j++
				}
				if j < len(s) && s[j] == '"' {
					if (j-k)%4 == 1 {
						return from, j - 1, j + 1, j - i + 1, true
					}
					k = j
					continue
				}
				k = j - 1
			}
			continue
		}
		switch c {
		case '\\':
			if k+1 < len(s) && s[k+1] != '\n' {
				k++
			}
		case q:
			return from, k, k + 1, k - i + 1, true
		}
	}
	return 0, 0, 0, len(s) - i, false
}

// escOpens says whether the backslash at i, followed by a quote, opens a string of escaped JSON: it ends a run of backslashes that is
// 1 mod 4 long (the same rule that closes one).
func escOpens(s string, i int) bool {
	if i+1 >= len(s) || s[i] != '\\' || s[i+1] != '"' {
		return false
	}
	n := 1
	for k := i - 1; k >= 0 && s[k] == '\\' && n < 8; k-- {
		n++
	}
	return n%4 == 1
}

// opensSingleQuoted says whether the apostrophe at i opens a string in a Python-style record ({'name': 'X', 'value': 'y'}, also over
// several lines): the last mark before it is one of "{[,:". An apostrophe inside a word or a sentence ("it's") does not.
func opensSingleQuoted(s string, i int) bool {
	for k := i - 1; k >= 0 && i-k <= 64; k-- {
		switch s[k] {
		case ' ', '\t', '\r', '\n':
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

// recordName says whether a setting name can be the name of a record: no white space inside ("Reset password" is a title).
func recordName(v string) bool {
	return v != "" && len(v) <= 4*maxIdent && !strings.ContainsAny(v, " \t\r\n")
}

// arrayEnd returns the index of the "]" that closes the array opening at v, or -1 (the text ends first). Strings inside are skipped
// (a "]" inside a string does not close it; a string ends at its line). Every byte read is charged, found or not.
func arrayEnd(s string, v int, w *work) int {
	depth := 0
	inStr := false
	for k := v; k < len(s); k++ {
		c := s[k]
		if inStr {
			switch c {
			case '\\':
				if k+1 < len(s) && s[k+1] != '\n' {
					k++
				}
			case '"', '\n':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				w.spend(k - v + 1)
				return k
			}
		}
	}
	w.spend(len(s) - v + 1)
	return -1
}

// jsonRecords reads objects, and does it twice when the text has an escaped quote: once with plain quotes and once with the
// backslash-quote pair as the quote (JSON inside a JSON string, as in a last-applied-configuration annotation).
// It also returns where it stopped (len(s) when it read everything).
func jsonRecords(s string, edits []edit, w *work) ([]edit, int) {
	edits, stop := jsonPass(s, edits, w, false)
	if strings.Contains(s, "\\\"") {
		var p int
		edits, p = jsonPass(s, edits, w, true)
		stop = min(stop, p)
	}
	return edits, stop
}

func jsonPass(s string, edits []edit, w *work, esc bool) ([]edit, int) {
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
		case c == '{':
			if len(stack) >= maxRecordDepth {
				// Too deep, or objects that never closed (cut lines): the oldest one is judged as read and dropped, so the reader stays on
				// for what follows.
				edits = stack[0].flush(edits)
				copy(stack, stack[1:])
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, jframe{})
			i++
		case c == '}':
			if len(stack) > 0 {
				edits = stack[len(stack)-1].flush(edits)
				stack = stack[:len(stack)-1]
			}
			i++
		case (!esc && c == '"' && (i == 0 || s[i-1] != '\\')) || (!esc && c == '\'' && opensSingleQuoted(s, i)) || (esc && escOpens(s, i)):
			from, to, next, read, ok := recordString(s, i, esc)
			w.spend(read)
			single := !esc && c == '\''
			if single && ok && (strings.Contains(s[from:to], "{\"") || strings.HasPrefix(s[from:to], "[\"")) {
				// An apostrophe string that holds JSON ('{"name":..}' in a log line): the record inside is read, not skipped.
				i++
				continue
			}
			if single && !ok && (len(stack) == 0 || stack[len(stack)-1].key != "value") {
				i++ // an apostrophe in prose that never closes: not a string
				continue
			}
			if !esc && ok && to > from && s[to-1] == '{' && next < len(s) && !strings.ContainsRune(",:}] \t\r\n", rune(s[next])) {
				// A lone quote earlier on the line paired this string wrong: it ends on the quote that opens a member name. Read on from
				// the brace.
				i = to - 1
				continue
			}
			if !ok {
				// A string that does not close on its line (a cut log line). As the value of a "value" member it is
				// masked to the end of the line, because the record may still turn out to be a secret.
				e := i + read
				if len(stack) > 0 && stack[len(stack)-1].key == "value" {
					top := &stack[len(stack)-1]
					lineEnd := len(s)
					if k := strings.IndexByte(s[i:], '\n'); k >= 0 {
						lineEnd = i + k
					}
					top.addSpan(i+1, lineEnd)
					top.key = ""
					w.spend(lineEnd - i)
					e = lineEnd
				}
				if e <= i {
					e = i + 1
				}
				i = e
				continue
			}
			if len(stack) == 0 {
				i = next
				continue
			}
			top := &stack[len(stack)-1]
			content := s[from:to]
			k := skipSpace(s, next)
			if k < len(s) && s[k] == ':' {
				// A member name. Read the first byte of its value: a literal value of a "value" member is a span to mask as well.
				top.key = strings.ToLower(content)
				if len(top.key) > maxIdent {
					top.key = top.key[:maxIdent]
				}
				v := skipSpace(s, k+1)
				if top.key == "value" && v < len(s) {
					switch s[v] {
					case '"', '\'', '{', '\\':
					case '[':
						e := arrayEnd(s, v, w)
						if e < 0 {
							e = len(s) // the array never closes (a cut record): the rest of the text, since the record may be a secret
						}
						top.addSpan(v+1, e)
						top.key = ""
					default:
						// A bare value (number, true, null, or a mistake): a name on the next line after a cut "value": is not one.
						if strings.IndexByte(s[k+1:v], '\n') >= 0 && !(s[v] == '-' || (s[v] >= '0' && s[v] <= '9')) {
							break
						}
						e := v
						for e < len(s) && s[e] != ',' && s[e] != '}' && s[e] != ']' && s[e] != '"' && s[e] != ' ' && s[e] != '\n' && s[e] != '\r' && s[e] != '\t' {
							e++
						}
						w.spend(e - v)
						top.addSpan(v, e)
						top.key = ""
					}
				}
				i = k + 1
				continue
			}
			// A string value.
			if nameKeys[top.key] && recordName(content) && IsSecretKey(content) {
				top.secret = true
			}
			if top.key == "value" {
				top.addSpan(from, to)
			}
			top.key = ""
			i = next
		default:
			i++
		}
	}
	// Objects still open, at the end of the text or where the budget ran out: the record is judged as far as it was read.
	for n := len(stack) - 1; n >= 0; n-- {
		edits = stack[n].flush(edits)
	}
	return edits, stop
}

// plistRecords masks the element after <key>Name</key> when Name is a secret key name.
func plistRecords(s string, edits []edit, w *work) ([]edit, int) {
	pos := 0
	for pos < len(s) {
		if w.out() {
			return edits, pos
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
	return edits, len(s)
}

type yline struct {
	start, end int // the line without its line break
	next       int // where the next line starts (len(s)+1 after the last line)
	indent     int
}

// lineAt reads the line that starts at start.
func lineAt(s string, start int) yline {
	end, next := len(s), len(s)+1
	if e := strings.IndexByte(s[start:], '\n'); e >= 0 {
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
	return yline{start, ee, next, ind}
}

// prevLine reads the line before the one that starts at start; ok is false at the top of the text.
func prevLine(s string, start int) (yline, bool) {
	if start <= 0 {
		return yline{}, false
	}
	ps := strings.LastIndexByte(s[:start-1], '\n') + 1
	return lineAt(s, ps), true
}

func (l yline) blank(s string) bool { return l.start+l.indent >= l.end }

// idle says whether the line holds nothing to read: blank, or only a comment. Idle lines do not count toward the record window.
func (l yline) idle(s string) bool { return l.blank(s) || s[l.start+l.indent] == '#' }

// yamlName reads "name:" (or "- name:") with a secret key name as its value from the line. keyCol is the column of the name key
// itself: the indent of every other key of the same item.
func yamlName(s string, l yline) (secret bool, keyCol int, dash bool) {
	t := s[l.start+l.indent : l.end]
	keyCol = l.indent
	if strings.HasPrefix(t, "- ") {
		dash = true
		r := strings.TrimLeft(t[1:], " \t")
		keyCol += len(t) - len(r)
		t = r
	}
	if len(t) > 0 && (t[0] == '"' || t[0] == '\'') {
		t = t[1:]
	}
	colon := strings.IndexByte(t, ':')
	if colon <= 0 || colon > 16 {
		return false, keyCol, dash
	}
	key := strings.ToLower(strings.TrimRight(t[:colon], "\"' \t"))
	if !nameKeys[key] {
		return false, keyCol, dash
	}
	v := strings.TrimSpace(t[colon+1:])
	if h := strings.Index(v, " #"); h >= 0 {
		v = strings.TrimSpace(v[:h])
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return recordName(v) && IsSecretKey(v), keyCol, dash
}

// yamlValueLine reports whether the line is the "value:" member of a record, and where the text after the colon starts.
func yamlValueLine(s string, l yline) (rest int, ok bool) {
	t := s[l.start+l.indent : l.end]
	off := l.start + l.indent
	if strings.HasPrefix(t, "- ") {
		skip := 1
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
	for k < len(t) && (t[k] == ' ' || t[k] == '\t') {
		k++
	}
	if k >= len(t) || t[k] != ':' {
		return 0, false
	}
	return off + k + 1, true
}

// yamlRecords finds each "name:" line whose value is a secret key name and masks the "value:" member of its item. The text is read
// line by line once, and the neighbours of a name line are read from its position: no table of lines is built, so a text of a few
// million line breaks costs no memory.
func yamlRecords(s string, edits []edit, w *work) ([]edit, int) {
	w.spend(len(s))
	var done [64]int // starts of the value lines masked lately: a value line is masked once, however many name lines see it
	nDone := 0
	mask := func(x yline, rest, keyIndent int) {
		for _, d := range done[:min(nDone, len(done))] {
			if d == x.start {
				return
			}
		}
		done[nDone%len(done)] = x.start
		nDone++
		edits = yamlMaskValue(s, x, rest, keyIndent, edits, w)
	}
	for start := 0; start <= len(s); {
		if w.out() {
			return edits, start
		}
		l := lineAt(s, start)
		start = l.next
		if l.blank(s) {
			continue
		}
		secret, keyCol, dash := yamlName(s, l)
		if !secret {
			continue
		}
		w.spend(maxRecordLines * 8)
		// After the name key: the rest of the item, the lines at the item's own indent (deeper lines belong to a member above them).
		read := 0
		for x, n := lineAt(s, min(l.next, len(s))), 0; l.next <= len(s) && n < 3*maxRecordLines && read < maxRecordLines; n++ {
			if x.idle(s) {
				if x.next > len(s) {
					break
				}
				x = lineAt(s, x.next)
				continue
			}
			read++
			if x.indent < keyCol || (x.indent == keyCol && strings.HasPrefix(s[x.start+x.indent:x.end], "- ")) {
				break
			}
			if x.indent == keyCol {
				if rest, ok := yamlValueLine(s, x); ok {
					mask(x, rest, x.indent)
				}
			}
			if x.next > len(s) {
				break
			}
			x = lineAt(s, x.next)
		}
		// Before it, unless the name key opens the item.
		if dash {
			continue
		}
		read = 0
		for x, n, ok := l, 0, true; n < 3*maxRecordLines && read < maxRecordLines; n++ {
			if x, ok = prevLine(s, x.start); !ok {
				break
			}
			if x.idle(s) {
				continue
			}
			read++
			xs := s[x.start+x.indent : x.end]
			if x.indent < keyCol {
				if strings.HasPrefix(xs, "- ") && dashKeyCol(xs, x.indent) == keyCol {
					if rest, ok := yamlValueLine(s, x); ok {
						mask(x, rest, keyCol)
					}
				}
				break
			}
			if x.indent == keyCol {
				if rest, ok := yamlValueLine(s, x); ok {
					mask(x, rest, x.indent)
				}
			}
		}
	}
	return edits, len(s)
}

// dashKeyCol is the column of the key after the "- " that opens a list item on a line with the given indent.
func dashKeyCol(t string, indent int) int {
	r := strings.TrimLeft(t[1:], " \t")
	return indent + len(t) - len(r)
}

// yamlMaskValue masks the value of a "value:" line: a quoted scalar inside its quotes (also over several lines), a block scalar and
// its lines, a value on the next indented line, or the plain rest of the line. keyIndent is the column of the member (the indent that
// its continuation lines exceed).
func yamlMaskValue(s string, l yline, rest, keyIndent int, edits []edit, w *work) []edit {
	k := rest
	for k < l.end && (s[k] == ' ' || s[k] == '\t') {
		k++
	}
	w.spend(l.end - rest + 1)
	if k >= l.end || s[k] == '#' {
		// Nothing on the line: the value is on the next indented line.
		x := l
		for n := 0; n <= maxCommentLines+1 && x.next <= len(s); n++ {
			x = lineAt(s, x.next)
			if x.blank(s) || s[x.start+x.indent] == '#' {
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
		x := l
		for n := 0; n < maxBlockLines && x.next <= len(s); n++ {
			x = lineAt(s, x.next)
			if x.blank(s) {
				continue
			}
			if x.indent <= keyIndent {
				break
			}
			w.spend(x.end - x.start)
			edits = append(edits, edit{x.start + x.indent, x.end})
		}
		return edits
	case '"', '\'':
		q := s[k]
		from := k + 1
		x := l
		for n := 0; n < maxBlockLines; n++ {
			for e := from; e < x.end; e++ {
				if q == '"' && s[e] == '\\' {
					e++
					continue
				}
				if s[e] == q {
					if q == '\'' && e+1 < x.end && s[e+1] == '\'' {
						e++
						continue
					}
					if e > from {
						edits = append(edits, edit{from, e})
					}
					return edits
				}
			}
			// Not closed on this line: the line is masked and the scalar goes on over the next one.
			if x.end > from {
				edits = append(edits, edit{from, x.end})
			}
			if x.next > len(s) {
				break
			}
			x = lineAt(s, x.next)
			from = x.start + x.indent
			w.spend(x.end - x.start)
		}
		return edits
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
