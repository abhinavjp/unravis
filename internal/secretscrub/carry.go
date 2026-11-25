package secretscrub

import "strings"

// maxBlockLines is how many lines under a block scalar header Carry masks. A block that long is not a password.
const maxBlockLines = 200

// maxCommentLines is how many comment lines between a key and its value Carry looks past.
const maxCommentLines = 4

// Carry lets a reader that sees one line at a time mask the forms that go on to the next line: a YAML block scalar ("password: |"
// and the indented lines under it), a key with nothing after it whose value is on the next line ("DB_PASSWORD=" then the value,
// "<Password>" then the value, "client_secret:" then an indented value), a quoted value, a JSON array or object, and a split XML
// tag. Use one Carry for one stream of lines and call Line for each line in order. A reader that has the whole text calls Mask instead.
//
// A value on the next line is masked only when that line can be a value: one word, or text in quotes. A sentence or a test runner line
// after "Password:" ("sudo: 3 incorrect password attempts", "--- FAIL: TestLogin") is kept, because it is ordinary text.
type Carry struct {
	pend     int
	indent   int // the indent of the line that opened a block
	lines    int // lines of the block masked so far
	comments int // comment lines passed while waiting for the value
	quote    byte
	nest     int
	literal  bool
	depth    int
	tag      string
}

func (c *Carry) set(pend int, hint carryHint) {
	c.pend, c.quote, c.nest, c.literal, c.depth, c.tag = pend, hint.quote, hint.nest, hint.literal, hint.depth, hint.tag
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
		text := strings.TrimRight(line, " \t\r")
		ind := indentOf(line)
		if text == "" {
			if c.pend != pendBlock {
				c.pend = pendNone // a blank line ends the search for a value on the next line; inside a block it is part of it
			}
			return line
		}
		pend, keyIndent := c.pend, c.indent
		switch {
		case pend == pendBlock && ind > keyIndent && c.lines < maxBlockLines:
			c.lines++
			return line[:ind] + mask
		case pend != pendBlock && strings.HasPrefix(text[ind:], "#") && c.comments < maxCommentLines:
			c.comments++ // a comment line between the key and its value
			return line
		case c.nextLineIsValue(pend, text, ind):
			c.pend = pendNone
			return line[:ind] + mask
		}
		c.pend = pendNone
	}
	out, pend, hint := maskPend(line, mask)
	c.set(pend, hint)
	if pend != pendNone {
		c.indent, c.lines, c.comments = indentOf(line), 0, 0
	}
	return out
}

func (c *Carry) quotedLine(line, mask string) string {
	ind := indentOf(line)
	combined := string(c.quote) + line
	end, after := closingQuote(combined, 0, c.nest, c.literal)
	if end == len(combined) && after == len(combined) {
		return line[:ind] + mask
	}
	out := line[:ind] + mask + line[end-1:]
	c.pend = pendNone
	out, pend, hint := maskPend(out, mask)
	c.set(pend, hint)
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
		return line[:ind] + mask
	}
	out := line[:ind] + mask + line[closeAt:]
	c.pend = pendNone
	out, pend, hint := maskPend(out, mask)
	c.set(pend, hint)
	return out
}

func (c *Carry) xmlLine(line, mask string) string {
	ind := indentOf(line)
	close := "</" + c.tag
	if at := strings.Index(line, close); at >= 0 {
		if end := strings.IndexByte(line[at:], '>'); end >= 0 {
			out := line
			if strings.TrimSpace(line[:at]) != "" {
				out = line[:ind] + mask + line[at:]
			}
			c.pend = pendNone
			out, pend, hint := maskPend(out, mask)
			c.set(pend, hint)
			return out
		}
	}
	return line[:ind] + mask
}

func (c *Carry) tagLine(line, mask string) string {
	if len(c.tag)+len(line)+1 > 2*maxTag {
		c.pend = pendNone
		return mask
	}
	out, pend, hint := maskPend(c.tag+"\n"+line, mask)
	c.set(pend, hint)
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
