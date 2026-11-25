package secretscrub

import "strings"

// maxBlockLines is how many lines under a block scalar header Carry masks. A block that long is not a password.
const maxBlockLines = 200

// maxCommentLines is how many comment lines between a key and its value Carry looks past.
const maxCommentLines = 4

// maxTagLines is how many lines a split XML tag may run over before Carry stops carrying it. Every line of the tag is parsed again with
// the lines before it, so the cost of a tag that goes on grows with the square of its lines; a real tag split over lines (a pretty
// printed <add key=... value=... />) takes a handful. A stray "<a" at the end of a log line is not a tag, and the lines after it
// would be read again and again.
const maxTagLines = 16

// Carry lets a reader that sees one line at a time mask the forms that go on to the next line: a YAML block scalar ("password: |"
// and the indented lines under it), a key with nothing after it whose value is on the next line ("DB_PASSWORD=" then the value,
// "<Password>" then the value, "client_secret:" then an indented value), a quoted value, a JSON array or object, and a split XML
// tag. Use one Carry for one stream of lines and call Line for each line in order. A reader that has the whole text calls Mask instead.
//
// A value on the next line is masked only when that line can be a value: one word, or text in quotes. A sentence or a test runner line
// after "Password:" ("sudo: 3 incorrect password attempts", "--- FAIL: TestLogin") is kept, because it is ordinary text.
//
// Every state has a limit, so one stray quote, bracket or tag does not hide the rest of a log. The limit is where Carry fails open: it
// stops carrying, and the lines after it are read as new text. A quote, a bracket and the text of an XML element are carried over
// maxBlockLines lines, a block scalar over maxBlockLines lines under its header, a comment gap over maxCommentLines lines, and a split
// XML tag over maxTagLines lines and 2*maxTag bytes. The line that crosses the byte limit of a tag is hidden whole; the line that
// crosses a line limit is read normally. A value that is longer than its limit shows from there on, so a caller that must never show a
// line of a longer value has to read the whole text and call Mask.
type Carry struct {
	pend     int
	indent   int // the indent of the line that opened a block
	lines    int // lines of the block masked so far
	comments int // comment lines passed while waiting for the value
	quote    byte
	nest     int
	literal  bool
	class    int // the class of the key that opened a quote, for the text that sticks to its closing quote
	depth    int
	tag      string
	// A split tag keeps the block or next-line value of a key that came with it on its line beside it (the state that was left over
	// in "<a x=1 <b" + "DB_PASSWORD="), so the key=value reader runs under an open tag too.
	sub         int
	subIndent   int
	subLines    int
	subComments int
}

// set takes what line left open. A new state starts with the indent of the line that opened it and no lines counted, so a block that
// opens on the tail of a line that closed a quote, bracket or tag (b" password: |) is measured from that line.
func (c *Carry) set(pend int, hint carryHint, line string) {
	c.pend, c.quote, c.nest, c.literal, c.class, c.depth, c.tag = pend, hint.quote, hint.nest, hint.literal, hint.class, hint.depth, hint.tag
	if pend != pendNone {
		c.indent, c.lines, c.comments = indentOf(line), 0, 0
	}
	c.sub = pendNone
	if pend == pendTag && hint.sub != pendNone {
		c.sub, c.subIndent, c.subLines, c.subComments = hint.sub, indentOf(line), 0, 0
	}
}

// overCap counts a line carried by a quote, bracket or XML text and drops the state once it has gone on longer than maxBlockLines. Such
// a value is not a password, so one stray quote or bracket does not hide the rest of a log.
func (c *Carry) overCap() {
	c.lines++
	if c.lines > maxBlockLines {
		c.pend = pendNone
	}
}

// Line returns line with its secrets masked, including the value it carries over from the lines before.
func (c *Carry) Line(line, mask string) string {
	switch c.pend {
	case pendQuote:
		return c.quotedLine(line, mask)
	case pendBracket:
		return c.bracketLine(line, mask)
	case pendXMLText:
		return c.xmlLine(line, mask)
	case pendTag:
		return c.tagLine(line, mask)
	}
	if c.pend != pendNone {
		if out, ok := c.stepNext(line, mask); ok {
			return out
		}
	}
	out, pend, hint := maskPend(line, mask)
	c.set(pend, hint, line)
	return out
}

// stepNext reads a line under a block scalar header or a key whose value is on the next line. It returns the line to show and true when
// the line is part of the value or a pause in the search for it; when it is neither, the state ends and the line is read as new text.
func (c *Carry) stepNext(line, mask string) (string, bool) {
	text := strings.TrimRight(line, " \t\r")
	ind := indentOf(line)
	if text == "" {
		if c.pend != pendBlock {
			c.pend = pendNone // a blank line ends the search for a value on the next line; inside a block it is part of it
		}
		return line, true
	}
	pend, keyIndent := c.pend, c.indent
	switch {
	case pend == pendBlock && ind > keyIndent && c.lines < maxBlockLines:
		c.lines++
		return line[:ind] + mask, true
	case pend != pendBlock && strings.HasPrefix(text[ind:], "#") && c.comments < maxCommentLines:
		c.comments++ // a comment line between the key and its value
		return line, true
	case c.nextLineIsValue(pend, text, ind):
		c.pend = pendNone
		return line[:ind] + mask, true
	}
	c.pend = pendNone
	return "", false
}

func (c *Carry) quotedLine(line, mask string) string {
	ind := indentOf(line)
	combined := string(c.quote) + line
	end, after := closingQuote(combined, 0, c.nest, c.literal)
	if end == len(combined) && after == len(combined) {
		c.overCap()
		return line[:ind] + mask
	}
	if c.nest == 0 && end < len(combined) && !valueSeparator(combined, after) {
		// "x"y: text sticks to the closing quote, so the quote was not the end of the value (the same rule as readValue: fail closed,
		// take the rest of it).
		end = unquotedEnd(combined, after, c.class, false, false)
	}
	out := line[:ind] + mask + line[end-1:]
	c.pend = pendNone
	out, pend, hint := maskPend(out, mask)
	c.set(pend, hint, line)
	return out
}

func (c *Carry) bracketLine(line, mask string) string {
	ind := indentOf(line)
	depth, quote := c.depth, c.quote
	closeAt := -1
	for i := 0; i < len(line); i++ {
		if quote != 0 {
			switch line[i] {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch line[i] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				closeAt = i
			}
		case '"', '\'':
			quote = line[i]
		}
		if closeAt >= 0 {
			break
		}
	}
	if closeAt < 0 {
		c.depth, c.quote = depth, quote
		c.overCap()
		return line[:ind] + mask
	}
	out := line[:ind] + mask + line[closeAt:]
	c.pend = pendNone
	out, pend, hint := maskPend(out, mask)
	c.set(pend, hint, line)
	return out
}

func (c *Carry) xmlLine(line, mask string) string {
	ind := indentOf(line)
	close := "</" + c.tag
	if at := indexFold(line, close); at >= 0 {
		if end := strings.IndexByte(line[at:], '>'); end >= 0 {
			out := line
			if strings.TrimSpace(line[:at]) != "" {
				out = line[:ind] + mask + line[at:]
			}
			c.pend = pendNone
			out, pend, hint := maskPend(out, mask)
			c.set(pend, hint, line)
			return out
		}
	}
	c.overCap()
	return line[:ind] + mask
}

// indexFold is strings.Index that does not care about the case of ASCII letters (XML names are written in any case in logs). It never
// changes the length of the text, so the index is the index in s.
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// tagLine reads a line of a split tag together with the lines before it. The tag text holds every line so far, so the line that
// closes it sees the attributes that came before. A tag that goes on over maxTagLines lines or 2*maxTag bytes is dropped: it is not a
// tag, and reading it again for every line would cost the square of its length.
func (c *Carry) tagLine(line, mask string) string {
	// The block or next-line value that came with the tag reads this line first; the tag reads it too, whatever the answer.
	handled, hok := "", false
	if c.sub != pendNone {
		s := Carry{pend: c.sub, indent: c.subIndent, lines: c.subLines, comments: c.subComments}
		handled, hok = s.stepNext(line, mask)
		c.sub, c.subLines, c.subComments = s.pend, s.lines, s.comments
	}
	sub, subIndent, subLines, subComments := c.sub, c.subIndent, c.subLines, c.subComments
	if len(c.tag)+len(line)+1 > 2*maxTag {
		c.pend, c.sub = pendNone, pendNone
		return mask
	}
	n := c.lines + 1
	prev := c.tag
	out, pend, hint := maskPend(c.tag+"\n"+line, mask)
	c.set(pend, hint, line)
	if c.pend == pendTag && strings.HasPrefix(c.tag, prev) {
		// The same tag goes on: count its lines, and keep the state that came with it unless this line opened a newer one.
		c.lines = n
		if c.sub == pendNone {
			c.sub, c.subIndent, c.subLines, c.subComments = sub, subIndent, subLines, subComments
		}
		if n >= maxTagLines {
			c.pend, c.sub = pendNone, pendNone
		}
	}
	if c.pend == pendNone && c.sub == pendNone && hok && sub != pendNone {
		// The tag ended (or was dropped) on this line and the state that came with it goes on as a state of its own.
		c.pend, c.indent, c.lines, c.comments = sub, subIndent, subLines, subComments
	}
	if hok {
		return handled
	}
	if at := strings.LastIndexByte(out, '\n'); at >= 0 {
		return out[at+1:]
	}
	return mask
}

// nextLineIsValue says whether the line after a key with no value is that key's value.
//
//   - A name that is clearly a secret ("DB_PASSWORD=", "<Password>"): an indented line, a quoted line or a list item
//     ("- x") is the value whatever it holds (a password may hold spaces); so is one word. A line that is none of those ("--- FAIL:
//     TestLogin") is ordinary text.
//   - A bare word that is also a sentence word ("Password:", "token:"): only one word or quoted text; for "token:" and "secret:" only
//     quoted text.
func (c *Carry) nextLineIsValue(pend int, text string, ind int) bool {
	if pend&pendColon != 0 {
		if ind <= c.indent && looksLikeKey(text[ind:]) {
			return false // after a colon the value is indented deeper than the key; this line is the next key
		}
		pend &^= pendColon
	}
	rest := text[ind:]
	quoted := rest[0] == '"' || rest[0] == '\''
	switch pend {
	case pendNextQuoted:
		return quoted
	case pendNextIndented:
		return ind > 0
	case pendNextText:
		return rest[0] != '<'
	case pendNextStrong:
		return ind > 0 || quoted || strings.HasPrefix(rest, "- ") || rest == "-" || len(rest) > 512 || plausibleValue(rest)
	case pendNext:
		return plausibleValue(rest)
	}
	return false
}

// plausibleValue says whether a line can be the value of the key above it: one word with no space in it, or text in quotes.
func plausibleValue(text string) bool {
	if text == "" || len(text) > 512 {
		return false
	}
	if text[0] == '"' || text[0] == '\'' {
		return true
	}
	return !strings.ContainsAny(text, " \t")
}

func indentOf(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}
