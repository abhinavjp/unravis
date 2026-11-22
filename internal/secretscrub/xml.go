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
func maskXML(s, mask string) string {
	if strings.IndexByte(s, '<') < 0 {
		return s
	}
	var edits []edit
	pos := 0
	for pos < len(s) {
		i := strings.IndexByte(s[pos:], '<')
		if i < 0 {
			break
		}
		i += pos
		if i+1 >= len(s) || !isNameStart(s[i+1]) {
			pos = i + 1
			continue
		}
		n := i + 1
		for n < len(s) && n-i < maxIdent && isNameChar(s[n]) {
			n++
		}
		name := s[i+1 : n]
		attrs, end, selfClose, plain, ok := parseTag(s, n)
		if !ok {
			pos = n
			continue
		}
		pos = end
		if !plain {
			continue // "<token hidden>" or "<private key hidden>": a marker left by an earlier redaction, not a tag
		}
		local := name
		if c := strings.LastIndexByte(local, ':'); c >= 0 {
			local = local[c+1:]
		}
		var spans []edit
		keyHit := false
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
		elementHit := keyClass(local) != classNone
		if keyHit || elementHit {
			for _, a := range attrs {
				if a.hasValue && valueAttrNames[strings.ToLower(a.name)] {
					spans = append(spans, edit{a.vFrom, a.vTo})
				}
			}
		}
		if (keyHit || elementHit) && !selfClose {
			if from, to, found := textNode(s, end); found {
				spans = append(spans, edit{from, to})
				if to > pos {
					pos = to
				}
			}
		}
		for _, sp := range sortEdits(spans) {
			if sp.to > sp.from {
				edits = append(edits, sp)
			}
		}
	}
	return apply(s, mask, sortEdits(edits))
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
// whether it is self-closing, and whether it is plain XML (every attribute has a value). ok is false when the tag does not end
// within maxTag characters.
func parseTag(s string, n int) (attrs []attr, end int, selfClose, plain, ok bool) {
	limit := n + maxTag
	if limit > len(s) {
		limit = len(s)
	}
	plain = true
	i := n
	for i < limit {
		for i < limit && isSpace(s[i]) {
			i++
		}
		if i >= limit {
			return nil, 0, false, false, false
		}
		switch {
		case s[i] == '>':
			return attrs, i + 1, false, plain, true
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '>':
			return attrs, i + 2, true, plain, true
		case s[i] == '<':
			return nil, 0, false, false, false // a new tag starts: this one never closed
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
					return nil, 0, false, false, false
				}
				a.hasValue, a.vFrom, a.vTo = true, j+1, j+1+c
				i = j + 2 + c
			} else {
				vs := j
				for j < limit && !isSpace(s[j]) && s[j] != '>' && (s[j] != '/' || j+1 >= len(s) || s[j+1] != '>') {
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
	return nil, 0, false, false, false
}

// textNode finds the text that follows a tag that ends at p: the text up to the next "<", a CDATA section, or, when another tag
// opens first (<Password><Value>x</Value></Password>), the first text inside it. A close tag or the end of the text means no value.
func textNode(s string, p int) (from, to int, ok bool) {
	for depth := 0; depth < 4; depth++ {
		q := p
		for q < len(s) && isSpace(s[q]) {
			q++
		}
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
			return q, e, e > q
		}
		rest := s[q:]
		switch {
		case strings.HasPrefix(rest, "<![CDATA["):
			if r := strings.Index(rest[9:], "]]>"); r >= 0 {
				return q + 9, q + 9 + r, r > 0
			}
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
				return q, len(s), true
			}
			p = q + 4 + r + 3
		default:
			lim := len(rest)
			if lim > maxTag {
				lim = maxTag
			}
			r := strings.IndexByte(rest[:lim], '>')
			if r < 0 {
				return 0, 0, false
			}
			p = q + r + 1
		}
	}
	return 0, 0, false
}
