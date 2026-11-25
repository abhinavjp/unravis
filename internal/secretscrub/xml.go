package secretscrub

import (
	"strings"
)

type attr struct {
	name       string
	hasValue   bool
	vFrom, vTo int // the text inside the quotes (or the bare value)
}

// keyAttrNames name the attribute that holds the name of a setting (<add key="password" value="x"/>).
var keyAttrNames = map[string]bool{"key": true, "name": true, "id": true, "property": true, "setting": true, "param": true, "variable": true, "field": true, "keyname": true}

// valueAttrNames name the attribute that holds the value of a setting.
var valueAttrNames = map[string]bool{"value": true, "val": true, "data": true, "content": true, "text": true, "connectionstring": true}

func isNameStart(c byte) bool { return isLower(c) || isUpper(c) || c == '_' }
func isNameChar(c byte) bool  { return isIdent(c) || c == ':' }

// maskXML masks XML forms: the text of a secret element, the value attribute next to a secret key attribute, the value of a secret
// attribute, and a connectionString attribute that holds a secret.
func maskXML(s, mask string, w *work) (string, int) {
	if strings.IndexByte(s, '<') < 0 {
		return s, pendNone
	}
	pending := pendNone
	var edits []edit
	pos := 0
	for pos < len(s) {
		i := strings.IndexByte(s[pos:], '<')
		if i < 0 {
			break
		}
		i += pos
		if w.out() {
			// Over budget: fail closed, the rest of the text goes.
			edits = append(edits, edit{i, len(s)})
			break
		}
		w.spend(16)
		if i+1 >= len(s) || !isNameStart(s[i+1]) {
			pos = i + 1
			continue
		}
		n := i + 1
		for n < len(s) && n-i < maxIdent && isNameChar(s[n]) {
			n++
		}
		name := s[i+1 : n]
		attrs, end, selfClose, plain, ok := parseTag(s, n, w)
		if !ok {
			to := len(s)
			if k := strings.IndexByte(s[n:], '<'); k >= 0 {
				to = n + k
			}
			w.spend(to - i)
			spans, _, _ := tagSpans(s, name, attrs)
			// Fail closed: a secret attribute in a tag that is not finished hides everything up to the end of what is read of the tag,
			// not only its value. The value can itself hold a secret key (value="password=" then the secret); masking only the value
			// would take that key away from the key=value pass that runs next.
			first := -1
			for _, sp := range spans {
				if sp.to > sp.from && (first < 0 || sp.from < first) {
					first = sp.from
				}
			}
			if first >= 0 && first < to {
				edits = append(edits, edit{first, to})
			}
			if possibleSecretTag(s[i:to]) {
				// A line reader can see only an unfinished tag. Mask secret attributes already present and carry its name
				// to the next line.
				pending = pendTag
				w.carry = carryHint{tag: s[i:to]}
			}
			pos = n
			continue
		}
		pos = end
		resume := w.rescan // a "<" inside a quoted value of this tag, or 0
		trunc := w.truncated
		if trunc {
			// The tag is longer than maxTag: fail closed. What is left of it, up to the next "<", is masked like the text of a secret
			// element, so a secret after a long attribute does not survive. The search moves pos past what it read (consumed).
			to := len(s)
			if k := strings.IndexByte(s[end:], '<'); k >= 0 {
				to = end + k
			}
			w.spend(to - end + 1)
			if to > end {
				edits = append(edits, edit{end, to})
			}
			pos = to
		}
		if !plain && (len(attrs) != 1 || attrs[0].name != "hidden") {
			// An attribute without a value ("<Password x>") is still a tag. Only "<token hidden>", which an earlier redaction leaves
			// ("<private key hidden>" and the like have no secret name), is a marker and not a tag.
			plain = true
		}
		if !plain {
			if resume > 0 && resume < pos {
				pos = resume
			}
			continue
		}
		spans, keyHit, elementHit := tagSpans(s, name, attrs)
		if (keyHit || elementHit) && !selfClose && !trunc {
			if from, to, found := textNode(s, end, w); found {
				spans = append(spans, edit{from, to})
				if to > pos {
					pos = to
				}
			} else if onlySpace(s, end) {
				pending = pendXMLText
				w.carry = carryHint{tag: name}
			}
		}
		for _, sp := range sortEdits(spans) {
			if sp.to > sp.from {
				edits = append(edits, sp)
			}
		}
		if resume > 0 && resume < pos {
			pos = resume
		}
	}
	return apply(s, mask, sortEdits(edits)), pending
}

func tagSpans(s, name string, attrs []attr) (spans []edit, keyHit, elementHit bool) {
	local := name
	if c := strings.LastIndexByte(local, ':'); c >= 0 {
		local = local[c+1:]
	}
	for _, a := range attrs {
		if !a.hasValue {
			continue
		}
		lname := strings.ToLower(a.name)
		switch {
		case keyClass(a.name) != classNone:
			spans = append(spans, edit{a.vFrom, a.vTo})
		case isConnStringName(lname) && secretfulConnString(s[a.vFrom:a.vTo]):
			spans = append(spans, edit{a.vFrom, a.vTo})
		case keyAttrNames[lname] && plainName(s[a.vFrom:a.vTo]) && keyClass(s[a.vFrom:a.vTo]) != classNone:
			keyHit = true
		}
	}
	elementHit = keyClass(local) != classNone
	if keyHit || elementHit {
		for _, a := range attrs {
			if a.hasValue && valueAttrNames[strings.ToLower(a.name)] {
				spans = append(spans, edit{a.vFrom, a.vTo})
			}
		}
	}
	return spans, keyHit, elementHit
}

func possibleSecretTag(s string) bool {
	if len(s) < 2 || s[0] != '<' {
		return false
	}
	name := s[1:]
	if i := strings.IndexAny(name, " \t\r\n/>"); i >= 0 {
		name = name[:i]
	}
	if name == "" || IsSecretKey(name) {
		return name != ""
	}
	low := lowerASCII(name)
	for start := 0; start < len(low); start++ {
		if !isNameStart(low[start]) {
			continue
		}
		for _, word := range secretWords {
			if strings.HasPrefix(word, low[start:]) {
				return true
			}
		}
	}
	return false
}

// plainName says whether v reads as the name of a setting ("db.password", "Admin Password"), not as a value.
func plainName(v string) bool {
	if v == "" || len(v) > 4*maxIdent {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !isIdent(v[i]) && v[i] != ' ' && v[i] != ':' {
			return false
		}
	}
	return true
}

func isConnStringName(lname string) bool {
	return strings.Contains(lname, "connectionstring") || strings.Contains(lname, "connection-string") || strings.Contains(lname, "connection_string") ||
		strings.Contains(lname, "connstr") || lname == "conn" || lname == "dsn"
}

// secretfulConnString says whether the value of a connectionString attribute holds a secret: a password or token key, or a login
// before an "@". A connection string without one (only a host and a database) stays readable.
func secretfulConnString(v string) bool {
	if keyClass(v) != classNone {
		return true
	}
	if strings.Contains(v, "://") && strings.Contains(v, "@") {
		return true
	}
	return false
}

// parseTag reads the attributes of a tag whose name ends at n. It returns the attributes, where the tag ends (after the ">"),
// whether it is self-closing, and whether it is plain XML (every attribute has a value). The attributes live in a buffer that the next
// call reuses. ok is false only when a new tag starts inside this one (it never closed). A tag that is longer than maxTag, or whose quote
// does not close within reach, counts as closed at that point (fail closed): the attributes read so far and the text after it are masked.
func parseTag(s string, n int, w *work) (attrs []attr, end int, selfClose, plain, ok bool) {
	limit := n + maxTag
	if limit > len(s) {
		limit = len(s)
	}
	plain = true
	w.truncated = false
	w.rescan = 0
	full := n+maxTag <= len(s) // false when the text ends inside the tag: that is not a tag
	i := n
	defer func() { w.spend(i - n + 1) }()
	attrs = w.attrs[:0] // reused; the caller reads it before the next parseTag call
	defer func() { w.attrs = attrs[:0] }()
	for i < limit {
		for i < limit && isSpace(s[i]) {
			i++
		}
		if i >= limit {
			return w.cut(attrs, i, plain, full)
		}
		switch {
		case s[i] == '>':
			return attrs, i + 1, false, plain, true
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '>':
			return attrs, i + 2, true, plain, true
		case s[i] == '<':
			return attrs[:0], 0, false, false, false // a new tag starts: this one never closed
		}
		a := attr{}
		st := i
		for i < limit && !isSpace(s[i]) && s[i] != '=' && s[i] != '>' && s[i] != '/' && s[i] != '"' && s[i] != '\'' && s[i] != '<' {
			i++
		}
		if i == st {
			i++ // a stray character: skip it
			plain = false
			continue
		}
		a.name = s[st:i]
		j := i
		for j < limit && isSpace(s[j]) {
			j++
		}
		if j < limit && s[j] == '=' {
			j++
			for j < limit && isSpace(s[j]) {
				j++
			}
			if j < limit && (s[j] == '"' || s[j] == '\'') {
				q := s[j]
				c := strings.IndexByte(s[j+1:limit], q)
				if c < 0 {
					// The quote does not close within reach (a long certificate or description, or text that ends): the tag ends
					// where this attribute starts, and what follows is read as the text after the tag.
					end = st
					i = limit // the search read up to the limit: charged
					if !full {
						a.hasValue, a.vFrom, a.vTo = true, j+1, limit
						attrs = append(attrs, a)
						return attrs, 0, false, plain, false
					}
					return w.cut(attrs, end, plain, full)
				}
				if lt := strings.IndexByte(s[j+1:j+1+c], '<'); lt >= 0 && w.rescan == 0 {
					// A "<" inside a quoted value is usually text ("a<b", a template). It can also mean the quote opened by mistake (a stray
					// quote) and the closing quote belongs to a later tag. Both are read: the value stays an attribute of this tag, and the
					// scan resumes at the "<", so the later tag is read on its own.
					w.rescan = j + 1 + lt
				}
				a.hasValue, a.vFrom, a.vTo = true, j+1, j+1+c
				i = j + 2 + c
			} else {
				vs := j
				for j < limit && !isSpace(s[j]) && s[j] != '>' && s[j] != '<' && (s[j] != '/' || j+1 >= len(s) || s[j+1] != '>') {
					j++
				}
				a.hasValue, a.vFrom, a.vTo = true, vs, j
				i = j
			}
		} else {
			plain = false // an attribute without a value
		}
		attrs = append(attrs, a)
	}
	return w.cut(attrs, i, plain, full) // the limit was reached
}

// cut ends a tag that ran past the limit. With a full limit the tag is longer than maxTag: it counts as closed at end (fail closed), so
// the attributes read so far are kept and the rest of it is masked. Without one the text ends inside the tag, which is not a tag.
func (w *work) cut(attrs []attr, end int, plain, full bool) ([]attr, int, bool, bool, bool) {
	if !full {
		return attrs, 0, false, false, false
	}
	w.truncated = true
	return attrs, end, false, plain, true
}

// textNode finds the text that follows a tag that ends at p: the text up to the next "<", a CDATA section, or, when another tag
// opens first (<Password><Value>x</Value></Password>), the first text inside it. A close tag or the end of the text means no value.
func textNode(s string, p int, w *work) (from, to int, ok bool) {
	start, far := p, p
	defer func() { w.spend(far - start + 1) }()
	for depth := 0; depth < 4; depth++ {
		q := p
		for q < len(s) && isSpace(s[q]) {
			q++
		}
		far = q
		if q >= len(s) {
			return 0, 0, false
		}
		if s[q] != '<' {
			e := len(s)
			if r := strings.IndexByte(s[q:], '<'); r >= 0 {
				e = q + r
			}
			for e > q && isSpace(s[e-1]) {
				e--
			}
			far = e
			return q, e, e > q
		}
		rest := s[q:]
		switch {
		case strings.HasPrefix(rest, "<![CDATA["):
			if r := strings.Index(rest[9:], "]]>"); r >= 0 {
				far = q + 9 + r
				return q + 9, q + 9 + r, r > 0
			}
			far = len(s)
			return q + 9, len(s), len(s) > q+9 // never closed: the rest of the text
		case strings.HasPrefix(rest, "</"):
			return 0, 0, false
		case strings.HasPrefix(rest, "<!--"):
			lim := len(rest)
			if lim > maxTag {
				lim = maxTag
			}
			r := strings.Index(rest[4:lim], "-->")
			if r < 0 {
				// No end within reach: fail closed and take the rest of the text, which also keeps the reading linear.
				far = q + lim
				return q, len(s), true
			}
			p = q + 4 + r + 3
			far = p
		default:
			lim := len(rest)
			if lim > maxTag {
				lim = maxTag
			}
			r := strings.IndexByte(rest[:lim], '>')
			if r < 0 {
				far = q + lim // the search read up to the limit: charged
				return 0, 0, false
			}
			p = q + r + 1
			far = p
		}
	}
	return 0, 0, false
}

// onlySpace says whether nothing but white space follows position p. It reads forward and stops at the first other character.
func onlySpace(s string, p int) bool {
	for p < len(s) && isSpace(s[p]) {
		p++
	}
	return p >= len(s)
}
