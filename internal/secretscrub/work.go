package secretscrub

import "strings"

// workFactor is how many scanner steps Mask may take for each byte of its input, on top of workFloor. The scanners are linear by
// construction, but four rounds of review each found one more shape that read the same long stretch again for every key. The budget
// is the guard for the whole class: when a text costs more than this, scanning stops and the rest of the text is masked (fail
// closed), so no input can cost more than a fixed multiple of its length. A variable so a test can lower it.
var workFactor = 64

const workFloor = 64 << 10

// work counts the steps of one Mask, MaskURLUserinfo or Carry.Line call and remembers the line ends and line starts it has found.
type work struct {
	left int
	// The first line end at or after eolFrom is eolAt: a key asking for the end of its line inside that stretch gets the answer
	// without a new search. The keys of one text come in order, so the searches add up to one pass.
	eolFrom, eolAt int
	// The start of the line that holds position lsAt.
	lsAt, lsPos int
	// The indent of the line that starts at indLs, when indOK.
	indLs, indent int
	indOK         bool
	// The attributes of the tag parseTag read last. Reused for every tag, so a hostile text of unclosed tags allocates nothing.
	attrs []attr
	// parseTag sets it when a tag ran past maxTag without closing: the attributes read so far are kept and the rest of the tag is text.
	truncated bool
	rescan    int // the first "<" inside a quoted value of the tag parseTag read last, or 0
}

func newWork(n int) *work { return &work{left: workFactor*n + workFloor, eolFrom: -1} }

func (w *work) spend(n int) {
	if n > 0 {
		w.left -= n
	}
}

// out says whether the budget is used up.
func (w *work) out() bool { return w.left < 0 }

// lineEnd returns the index of the first "\r" or "\n" at or after k, or len(s).
func (w *work) lineEnd(s string, k int) int {
	if w.eolFrom >= 0 && k >= w.eolFrom && k <= w.eolAt {
		return w.eolAt
	}
	e := len(s)
	if i := strings.IndexAny(s[k:], "\r\n"); i >= 0 {
		e = k + i
	}
	w.spend(e - k)
	w.eolFrom, w.eolAt = k, e
	return e
}

// lineStart returns the index where the line that holds at begins.
func (w *work) lineStart(s string, at int) int {
	if at < w.lsAt {
		w.spend(at) // not expected (the keys of a text come in order); charged in full if it ever happens
		return strings.LastIndexByte(s[:at], '\n') + 1
	}
	if i := strings.LastIndexByte(s[w.lsAt:at], '\n'); i >= 0 {
		w.lsPos = w.lsAt + i + 1
	}
	w.spend(at - w.lsAt)
	w.lsAt = at
	return w.lsPos
}

// lineIndent returns how many spaces and tabs the line that starts at ls begins with. The answer is remembered, so many keys on one
// line with a long run of spaces in front of them read the run once.
func (w *work) lineIndent(s string, ls int) int {
	if w.indOK && w.indLs == ls {
		return w.indent
	}
	n := 0
	for k := ls; k < len(s) && (s[k] == ' ' || s[k] == '\t'); k++ {
		n++
	}
	w.spend(n + 1)
	w.indLs, w.indent, w.indOK = ls, n, true
	return n
}
