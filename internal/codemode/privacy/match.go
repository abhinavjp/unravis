package privacy

import "sort"

// pat is one learned value, in lower case (ASCII), and what it is.
type pat struct {
	lower string
	kind  Kind
}

// span is a stretch of a text that a learned value covers.
type span struct {
	from, to int
	p        int // index of the pattern that made it
}

// matcher is an Aho-Corasick automaton over lower-case byte patterns. It finds, in one pass over a text, the longest pattern that
// ends at each position, in time linear in the text (plus the matches). Patterns are not removed; a new matcher is built from the
// whole list.
type matcher struct {
	next  map[uint64]int32 // node<<8 | byte -> child
	fail  []int32
	term  []int32 // pattern index + 1 when a pattern ends at the node
	dict  []int32 // nearest node on the fail chain where a pattern ends, 0 if none
	depth []int32
}

func edge(node int32, b byte) uint64 { return uint64(node)<<8 | uint64(b) } //nolint:gosec // G115: node is never negative

func buildMatcher(pats []pat) *matcher {
	m := &matcher{
		next:  map[uint64]int32{},
		fail:  []int32{0},
		term:  []int32{0},
		dict:  []int32{0},
		depth: []int32{0},
	}
	type kid struct {
		b  byte
		to int32
	}
	kids := [][]kid{nil}
	for pi, p := range pats {
		node := int32(0)
		for i := 0; i < len(p.lower); i++ {
			b := p.lower[i]
			to, ok := m.next[edge(node, b)]
			if !ok {
				to = int32(len(m.fail)) //nolint:gosec // G115: bounded by maxLearnBytes
				m.next[edge(node, b)] = to
				m.fail = append(m.fail, 0)
				m.term = append(m.term, 0)
				m.dict = append(m.dict, 0)
				m.depth = append(m.depth, m.depth[node]+1)
				kids = append(kids, nil)
				kids[node] = append(kids[node], kid{b, to})
			}
			node = to
		}
		m.term[node] = int32(pi + 1)
	}
	// breadth first: the fail link of a child is found from the fail link of its parent
	queue := make([]int32, 0, len(m.fail))
	for _, k := range kids[0] {
		queue = append(queue, k.to)
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		// u's own fail and dict were set when it was reached; set them for the children now
		for _, k := range kids[u] {
			f := m.fail[u]
			for {
				if to, ok := m.next[edge(f, k.b)]; ok && to != k.to {
					m.fail[k.to] = to
					break
				}
				if f == 0 {
					m.fail[k.to] = 0
					break
				}
				f = m.fail[f]
			}
			ff := m.fail[k.to]
			if m.term[ff] != 0 {
				m.dict[k.to] = ff
			} else {
				m.dict[k.to] = m.dict[ff]
			}
			queue = append(queue, k.to)
		}
	}
	return m
}

// scan appends to out the longest pattern that ends at each position of low (which must be lower case).
func (m *matcher) scan(low string, out []span) []span {
	s := int32(0)
	for i := 0; i < len(low); i++ {
		b := low[i]
		for {
			if to, ok := m.next[edge(s, b)]; ok {
				s = to
				break
			}
			if s == 0 {
				break
			}
			s = m.fail[s]
		}
		t := s
		if m.term[t] == 0 {
			t = m.dict[t]
		}
		if t != 0 {
			out = addSpan(out, span{from: i + 1 - int(m.depth[t]), to: i + 1, p: int(m.term[t]) - 1})
		}
	}
	return out
}

// addSpan appends x, or joins it to the last span when they overlap, so a text that is one long run of a repeated value does not
// make a span for every position. The joined span covers both; it keeps the pattern of the earlier one.
func addSpan(out []span, x span) []span {
	if n := len(out); n > 0 && x.from < out[n-1].to && x.to >= out[n-1].from {
		if x.from < out[n-1].from {
			out[n-1].from = x.from
		}
		if x.to > out[n-1].to {
			out[n-1].to = x.to
		}
		return out
	}
	return append(out, x)
}

// mergeSpans sorts spans by start (longest first) and joins the ones that overlap, so no part of a learned value is left when two
// of them overlap. The joined span keeps the pattern of its first span.
func mergeSpans(sp []span) []span {
	if len(sp) < 2 {
		return sp
	}
	sort.Slice(sp, func(i, j int) bool {
		if sp[i].from != sp[j].from {
			return sp[i].from < sp[j].from
		}
		return sp[i].to > sp[j].to
	})
	out := sp[:1]
	for _, s := range sp[1:] {
		cur := &out[len(out)-1]
		if s.from < cur.to {
			if s.to > cur.to {
				cur.to = s.to
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
