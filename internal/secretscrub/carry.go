package secretscrub

import "strings"

// Carry lets a reader that sees one line at a time mask the forms that go on to the next line: a YAML block scalar ("password: |"
// and the indented lines under it) and a key with nothing after it whose value is on the next line ("DB_PASSWORD=" then the value,
// "<Password>" then the value, "client_secret:" then an indented value). Use one Carry for one stream of lines and call Line for
// each line in order. A reader that has the whole text calls Mask instead.
//
// Not carried: a quoted value or a JSON array that runs over several lines, and a tag split over lines.
type Carry struct {
	pend   int
	indent int // the indent of the line that opened a block
}

// Line returns line with its secrets masked, including the value it carries over from the lines before.
func (c *Carry) Line(line, mask string) string {
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
		c.pend = pendNone
		switch {
		case pend == pendBlock && ind > keyIndent:
			c.pend, c.indent = pendBlock, keyIndent
			return line[:ind] + mask
		case pend == pendNext, pend == pendNextIndented && ind > 0:
			return line[:ind] + mask
		}
	}
	out, pend := maskPend(line, mask)
	if pend != pendNone {
		c.pend, c.indent = pend, indentOf(line)
	}
	return out
}

func indentOf(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}
