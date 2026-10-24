package redis

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"

	"api-audit-explorer/internal/importer"
)

// This file reads one redis-cli reply into a small bounded tree and hands each element of
// the top-level array to the importer, in the three forms the owner can have produced:
//
//	--no-raw tree text:  1) 1) (integer) 14      (nested arrays chain "N) " prefixes)
//	--json (redis-cli 7): [[14,1003971751,15230,["KEYS","k"],"192.0.2.10:52144","c"]]
//	RESP2 dump:           *1 / *6 / :14 / ... / $4 / KEYS
//
// A node keeps only what an importer needs: integers, strings cut to maxStr bytes, the
// element count of an array and its first maxKids children. Nothing deeper than the
// argument array of a SLOWLOG entry is kept, so a hostile file cannot grow the tree.
// Strings read here (keys, values, addresses) are dropped by the importers; nothing in
// this file stores, logs or returns file text except through those importers.

type kind uint8

const (
	kBad kind = iota // anything the importers do not read: errors, floats, objects, malformed values
	kInt
	kStr
	kArr
	kNil
)

const (
	maxKids      = 8   // children kept per array
	maxStr       = 256 // bytes kept per string
	maxStack     = 16  // nesting depth read; deeper is a malformed line
	maxMarkerLen = 64  // longest string kept as an array's last element
	sinkDepth    = 3   // arrays at this depth or deeper are not kept
	maxDeclared  = 1 << 30
)

// rawGap is the Report gap added when the file is redis-cli --raw output.
const rawGap = "redis-cli --raw output is not supported; export again with --no-raw"

type node struct {
	k     kind
	line  int64
	n     int64
	s     string
	total int // elements seen, kept or not
	kids  []*node
	last  string // the last element when it was a short string, else ""
}

var (
	errStruct = errors.New("redis: structure") // a malformed line; never leaves the package
	errStop   = errors.New("redis: stop")      // Detect has what it needs
)

type frame struct {
	n     *node // nil for the reply frame (depth 0): its children are delivered, not kept
	depth int
	col   int // tree text: column of the ')' of this array's index prefixes
	want  int // RESP: declared length; -1 when the end is found from the text
	got   int
	sink  bool
}

type mode uint8

const (
	mNone mode = iota
	mTree
	mJSON
	mRESP
)

type scanner struct {
	lim     importer.Limits
	rep     *importer.Report
	deliver func(*node) error
	comment func([]byte)
	stack   []*frame
	mode    mode
	bulk    int // RESP: length of the bulk string on the next line, -1 when none
	line    int64
	content bool
}

func newScanner(lim importer.Limits, rep *importer.Report, deliver func(*node) error, comment func([]byte)) *scanner {
	return &scanner{lim: lim, rep: rep, deliver: deliver, comment: comment, bulk: -1}
}

func (s *scanner) row() error {
	s.rep.Rows++
	return s.lim.CheckRows(s.rep.Rows)
}

func (s *scanner) rejectRow(r importer.Reason) error {
	if err := s.row(); err != nil {
		return err
	}
	s.rep.Reject(s.line, r)
	return nil
}

// feed reads one line. Errors other than errStop are limit or format errors that end the
// file; a malformed line is rejected inside feed.
func (s *scanner) feed(line []byte, no int64) error {
	s.line = no
	if s.bulk >= 0 {
		return s.bulkLine(line)
	}
	t := bytes.TrimSpace(line)
	if len(t) == 0 {
		return nil
	}
	if t[0] == '#' {
		if err := s.closeAll(false); err != nil {
			return err
		}
		if s.comment != nil {
			s.comment(t[1:])
		}
		return nil
	}
	if !s.content {
		s.content = true
		if isBareInt(t) {
			// --raw output: the first line is the id or timestamp, with no structure.
			s.rep.Gap(rawGap)
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
	}
	m := mNone
	switch c := t[0]; {
	case c == '[':
		m = mJSON
	case c == '*' || c == ':' || c == '$' || c == '+' || c == '_' || c == '-':
		m = mRESP
	case c >= '0' && c <= '9', c == '(':
		m = mTree
	}
	if m != s.mode {
		if err := s.closeAll(false); err != nil {
			return err
		}
		s.mode = m
	}
	switch m {
	case mTree:
		return s.treeLine(line)
	case mJSON:
		return s.jsonLine(t)
	case mRESP:
		return s.respLine(t)
	}
	return s.junk()
}

// end closes whatever is still open at the end of the input.
func (s *scanner) end() error {
	s.bulk = -1
	return s.closeAll(false)
}

func (s *scanner) junk() error {
	if err := s.closeAll(true); err != nil {
		return err
	}
	return s.rejectRow(importer.ReasonFieldCount)
}

// fail turns an errStruct into a rejected line and passes any other error on.
func (s *scanner) fail(err error) error {
	if errors.Is(err, errStruct) {
		return s.junk()
	}
	return err
}

func (s *scanner) closeAll(markBad bool) error {
	for len(s.stack) > 0 {
		if f := s.stack[len(s.stack)-1]; markBad && f.n != nil {
			f.n.k = kBad
		}
		if err := s.closeTop(); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) open(want, col int) error {
	if len(s.stack) >= maxStack {
		return errStruct
	}
	d := len(s.stack)
	f := &frame{depth: d, col: col, want: want, sink: d >= sinkDepth}
	if d > 0 {
		f.n = &node{k: kArr, line: s.line}
	}
	s.stack = append(s.stack, f)
	if want == 0 {
		return s.closeTop()
	}
	return nil
}

func (s *scanner) closeTop() error {
	f := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	incomplete := f.want >= 0 && f.got < f.want
	if f.depth == 0 {
		if f.got == 0 && !incomplete {
			return s.row() // an empty reply: one accepted row, no events
		}
		if incomplete {
			return s.rejectRow(importer.ReasonFieldCount)
		}
		return nil
	}
	if incomplete || f.sink {
		f.n.k = kBad
	}
	return s.add(f.n)
}

// add attaches a finished node to the open array on top of the stack.
func (s *scanner) add(c *node) error {
	if len(s.stack) == 0 {
		// A reply that is a bare value, not an array: nothing to import from it.
		return s.rejectRow(importer.ReasonFieldCount)
	}
	f := s.stack[len(s.stack)-1]
	f.got++
	switch {
	case f.depth == 0:
		if err := s.row(); err != nil {
			return err
		}
		if err := s.deliver(c); err != nil {
			return err
		}
	case !f.sink:
		f.n.total++
		if len(f.n.kids) < maxKids {
			f.n.kids = append(f.n.kids, c)
		}
		f.n.last = ""
		if c.k == kStr && len(c.s) <= maxMarkerLen {
			f.n.last = c.s
		}
	}
	if f.want >= 0 && f.got >= f.want {
		return s.closeTop()
	}
	return nil
}

func (s *scanner) scalar(k kind, n int64, str string) *node {
	return &node{k: k, line: s.line, n: n, s: str}
}

func isBareInt(t []byte) bool {
	if len(t) > 0 && t[0] == '-' {
		t = t[1:]
	}
	if len(t) == 0 {
		return false
	}
	for _, c := range t {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---- tree text ----

type prefix struct{ col, idx int }

// parsePrefixes reads the "N) " chain at the start of a tree line. ok is false for a
// malformed chain. The column of each ')' identifies the array the element belongs to,
// which keeps right-aligned indexes ("  9) ", " 10) ") in one array.
func parsePrefixes(line []byte) (ps []prefix, rest []byte, ok bool) {
	i := 0
	for {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		j := i
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j == i || j-i > 9 || j >= len(line) || line[j] != ')' {
			break
		}
		idx, err := strconv.Atoi(string(line[i:j]))
		if err != nil || idx < 1 || j+1 >= len(line) || line[j+1] != ' ' {
			return nil, nil, false
		}
		if len(ps) >= maxStack {
			return nil, nil, false
		}
		ps = append(ps, prefix{col: j, idx: idx})
		i = j + 1
	}
	return ps, bytes.TrimRight(bytes.TrimLeft(line[i:], " "), " "), true
}

var (
	emptyArray  = []byte("(empty array)")
	emptyList   = []byte("(empty list or set)")
	nilReply    = []byte("(nil)")
	intPrefix   = []byte("(integer) ")
	moreBytes   = []byte("... (")
	moreBytesSz = []byte(" more bytes)")
)

// treeValue reads the value after the prefixes. empty is true for an empty array.
func (s *scanner) treeValue(rest []byte) (c *node, empty bool) {
	switch {
	case len(rest) > 0 && rest[0] == '"':
		str, tail, ok := unquote(rest)
		if !ok || !(len(tail) == 0 || isMoreBytes(tail)) {
			return s.scalar(kBad, 0, ""), false
		}
		return s.scalar(kStr, 0, str), false
	case bytes.HasPrefix(rest, intPrefix):
		n, err := strconv.ParseInt(string(rest[len(intPrefix):]), 10, 64)
		if err != nil {
			return s.scalar(kBad, 0, ""), false
		}
		return s.scalar(kInt, n, ""), false
	case bytes.Equal(rest, nilReply):
		return s.scalar(kNil, 0, ""), false
	case bytes.Equal(rest, emptyArray), bytes.Equal(rest, emptyList):
		return nil, true
	}
	return s.scalar(kBad, 0, ""), false
}

// isMoreBytes matches the "... (N more bytes)" tail redis-cli can print after a string.
func isMoreBytes(tail []byte) bool {
	if !bytes.HasPrefix(tail, moreBytes) || !bytes.HasSuffix(tail, moreBytesSz) {
		return false
	}
	return isBareInt(tail[len(moreBytes) : len(tail)-len(moreBytesSz)])
}

// unquote reads a redis-cli quoted string (b[0] is '"'), keeping at most maxStr bytes.
func unquote(b []byte) (s string, tail []byte, ok bool) {
	out := make([]byte, 0, 32)
	keep := func(c byte) {
		if len(out) < maxStr {
			out = append(out, c)
		}
	}
	for i := 1; i < len(b); i++ {
		c := b[i]
		switch c {
		case '"':
			return string(out), b[i+1:], true
		case '\\':
			i++
			if i >= len(b) {
				return "", nil, false
			}
			switch e := b[i]; e {
			case 'n':
				keep('\n')
			case 'r':
				keep('\r')
			case 't':
				keep('\t')
			case 'b':
				keep(8)
			case 'a':
				keep(7)
			case 'x':
				if i+2 >= len(b) {
					return "", nil, false
				}
				h, hok := hexByte(b[i+1], b[i+2])
				if !hok {
					return "", nil, false
				}
				keep(h)
				i += 2
			default:
				keep(e)
			}
		default:
			keep(c)
		}
	}
	return "", nil, false
}

func hexByte(a, b byte) (byte, bool) {
	hv := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	x, ok1 := hv(a)
	y, ok2 := hv(b)
	return x<<4 | y, ok1 && ok2
}

func (s *scanner) treeLine(line []byte) error {
	ps, rest, ok := parsePrefixes(line)
	if !ok {
		return s.junk()
	}
	if len(ps) == 0 {
		// A value with no index: a reply that is not an array, or an empty one.
		if err := s.closeAll(false); err != nil {
			return err
		}
		c, empty := s.treeValue(rest)
		if empty {
			return s.fail(s.open(0, -1))
		}
		return s.add(c)
	}

	// Phase 1: decide where the line belongs without changing anything.
	c1 := ps[0].col
	pops := 0
	for pops < len(s.stack) && s.stack[len(s.stack)-1-pops].col > c1 {
		pops++
	}
	rem := len(s.stack) - pops
	newReply, inFrame := false, false
	switch {
	case rem > 0 && s.stack[rem-1].col == c1:
		inFrame = true
		exp := s.stack[rem-1].got + 1
		if pops > 0 {
			exp++ // the element in progress finishes when the deeper arrays close
		}
		if ps[0].idx != exp {
			if rem != 1 || ps[0].idx != 1 {
				return s.junk()
			}
			newReply, inFrame = true, false // index 1 again at the top: the next reply
		}
	case rem == 0:
		if ps[0].idx != 1 {
			return s.junk()
		}
	default:
		return s.junk()
	}
	for j := 1; j < len(ps); j++ {
		if ps[j].idx != 1 || ps[j].col <= ps[j-1].col {
			return s.junk()
		}
	}

	// Phase 2: close what ended, open what began, attach the value.
	if newReply {
		if err := s.closeAll(false); err != nil {
			return err
		}
	} else {
		for i := 0; i < pops; i++ {
			if err := s.closeTop(); err != nil {
				return err
			}
		}
	}
	if !inFrame {
		if err := s.open(-1, c1); err != nil {
			return s.fail(err)
		}
	}
	for j := 1; j < len(ps); j++ {
		if err := s.open(-1, ps[j].col); err != nil {
			return s.fail(err)
		}
	}
	c, empty := s.treeValue(rest)
	if empty {
		return s.fail(s.open(0, -1))
	}
	return s.add(c)
}

// ---- JSON ----

type jsonParser struct {
	b []byte
	i int
	s *scanner
}

func (p *jsonParser) ws() {
	for p.i < len(p.b) && (p.b[p.i] == ' ' || p.b[p.i] == '\t') {
		p.i++
	}
}

func (s *scanner) jsonLine(t []byte) error {
	p := &jsonParser{b: t, s: s}
	err := p.value()
	if err == nil {
		p.ws()
		if p.i != len(p.b) || len(s.stack) != 0 {
			err = errStruct
		}
	}
	if err != nil {
		return s.fail(err)
	}
	return nil
}

func (p *jsonParser) value() error {
	p.ws()
	if p.i >= len(p.b) {
		return errStruct
	}
	s := p.s
	switch c := p.b[p.i]; {
	case c == '[':
		p.i++
		if err := s.open(-1, -1); err != nil {
			return err
		}
		p.ws()
		if p.i < len(p.b) && p.b[p.i] == ']' {
			p.i++
			return s.closeTop()
		}
		for {
			if err := p.value(); err != nil {
				return err
			}
			p.ws()
			if p.i >= len(p.b) {
				return errStruct
			}
			switch p.b[p.i] {
			case ',':
				p.i++
			case ']':
				p.i++
				return s.closeTop()
			default:
				return errStruct
			}
		}
	case c == '"':
		j := p.i + 1
		for j < len(p.b) && p.b[j] != '"' {
			if p.b[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(p.b) {
			return errStruct
		}
		var str string
		if err := json.Unmarshal(p.b[p.i:j+1], &str); err != nil {
			return errStruct
		}
		p.i = j + 1
		if len(str) > maxStr {
			str = str[:maxStr]
		}
		return s.add(s.scalar(kStr, 0, str))
	case c == '-' || (c >= '0' && c <= '9'):
		j := p.i
		if p.b[j] == '-' {
			j++
		}
		d := j
		for j < len(p.b) && p.b[j] >= '0' && p.b[j] <= '9' {
			j++
		}
		if j == d {
			return errStruct
		}
		if j < len(p.b) && (p.b[j] == '.' || p.b[j] == 'e' || p.b[j] == 'E') {
			for j < len(p.b) && bytes.IndexByte([]byte("0123456789.eE+-"), p.b[j]) >= 0 {
				j++
			}
			p.i = j
			return s.add(s.scalar(kBad, 0, ""))
		}
		n, err := strconv.ParseInt(string(p.b[p.i:j]), 10, 64)
		p.i = j
		if err != nil {
			return s.add(s.scalar(kBad, 0, ""))
		}
		return s.add(s.scalar(kInt, n, ""))
	case bytes.HasPrefix(p.b[p.i:], []byte("null")):
		p.i += 4
		return s.add(s.scalar(kNil, 0, ""))
	case bytes.HasPrefix(p.b[p.i:], []byte("true")):
		p.i += 4
		return s.add(s.scalar(kBad, 0, ""))
	case bytes.HasPrefix(p.b[p.i:], []byte("false")):
		p.i += 5
		return s.add(s.scalar(kBad, 0, ""))
	}
	return errStruct
}

// ---- RESP2 ----

func atoiCap(b []byte) (int, bool) {
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || n < -1 || n > maxDeclared {
		return 0, false
	}
	return int(n), true
}

func (s *scanner) respLine(t []byte) error {
	arg := t[1:]
	switch t[0] {
	case '*':
		n, ok := atoiCap(arg)
		if !ok {
			return s.junk()
		}
		if n < 0 {
			return s.add(s.scalar(kNil, 0, ""))
		}
		return s.fail(s.open(n, -1))
	case ':':
		n, err := strconv.ParseInt(string(arg), 10, 64)
		if err != nil {
			return s.add(s.scalar(kBad, 0, ""))
		}
		return s.add(s.scalar(kInt, n, ""))
	case '$':
		n, ok := atoiCap(arg)
		if !ok {
			return s.junk()
		}
		if n < 0 {
			return s.add(s.scalar(kNil, 0, ""))
		}
		s.bulk = n
		return nil
	case '+':
		str := string(arg)
		if len(str) > maxStr {
			str = str[:maxStr]
		}
		return s.add(s.scalar(kStr, 0, str))
	case '_':
		return s.add(s.scalar(kNil, 0, ""))
	}
	return s.add(s.scalar(kBad, 0, "")) // '-' error replies
}

func (s *scanner) bulkLine(line []byte) error {
	want := s.bulk
	s.bulk = -1
	if len(line) != want {
		return s.add(s.scalar(kBad, 0, ""))
	}
	str := line
	if len(str) > maxStr {
		str = str[:maxStr]
	}
	return s.add(s.scalar(kStr, 0, string(str)))
}

// ---- detection helpers ----

// decodeHead strips a BOM and decodes UTF-16 so Detect sees the text Parse will.
func decodeHead(head []byte) []byte {
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		return head[3:]
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		order := unicode.LittleEndian
		if head[0] == 0xFE {
			order = unicode.BigEndian
		}
		body := head[2:]
		if len(body) > 8192 {
			body = body[:8192]
		}
		body = body[:len(body)&^1]
		out, err := unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder().Bytes(body)
		if err != nil {
			return nil
		}
		return out
	}
	return head
}

// headText decodes a Detect head and drops a partial last line when the head is full.
func headText(head []byte) []byte {
	text := decodeHead(head)
	if len(head) >= importer.HeadSize {
		if i := bytes.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i]
		}
	}
	if !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 {
		return nil
	}
	return text
}

// peek reads up to maxLines lines of text and reports the first element of the first
// reply, plus the comment lines seen before it.
type peekResult struct {
	first    *node
	label    bool // a "# latency-event:" line was seen
	sample   bool // a "# unravis-sample" line was seen
	rawStart bool
}

func peek(text []byte, maxLines int) peekResult {
	var res peekResult
	var rep importer.Report
	sc := newScanner(importer.Limits{MaxRows: 64}, &rep,
		func(n *node) error { res.first = n; return errStop },
		func(c []byte) {
			c = bytes.TrimSpace(c)
			switch {
			case bytes.HasPrefix(c, []byte(commentLabel)):
				res.label = true
			case bytes.HasPrefix(c, []byte(commentSample)):
				res.sample = true
			}
		})
	var no int64
	for len(text) > 0 && int(no) < maxLines {
		line := text
		if i := bytes.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = nil
		}
		no++
		line = bytes.TrimSuffix(line, []byte("\r"))
		if err := sc.feed(line, no); err != nil {
			var qe *importer.QuarantineError
			res.rawStart = errors.As(err, &qe)
			break
		}
	}
	if res.first == nil {
		_ = sc.end()
	}
	return res
}

// Comment line prefixes the owner's collection script writes (see the importer docs).
const (
	commentLabel  = "latency-event:"
	commentSample = "unravis-sample"
)

// unixTime reads a Unix timestamp in seconds and refuses anything outside 2000..2100.
func unixTime(sec int64) (time.Time, bool) {
	if sec < 946684800 || sec >= 4102444800 {
		return time.Time{}, false
	}
	return time.Unix(sec, 0).UTC(), true
}
