package privacy

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Kind says what a token stands for. The text of a token is "[" + Kind + "_" + n + "]".
type Kind string

const (
	KindIP   Kind = "IP"
	KindUser Kind = "USER"
	KindUA   Kind = "UA"
)

const (
	// maxTokens is the number of distinct values one run tokenizes per kind. The next distinct value gets the shared overflow token
	// ([IP_OVER]), so a hostile or huge result cannot grow the map without bound and still shows no raw value.
	maxTokens = 1 << 16
	// minLearnLen is the shortest value that is replaced inside longer text. A shorter learned value ("7") would damage every digit
	// in the text, so it is replaced only when it is the whole string (a JSON string value or a key).
	minLearnLen = 6
	// maxLearnLen and maxLearned bound what Learn keeps.
	maxLearnLen = 1 << 10
	maxLearned  = 4096
	// maxZone bounds the interface name after "%" in a v6 address.
	maxZone = 64
	// maxRounds bounds the repeat of the two address passes over one text.
	maxRounds = 4
)

// Sweeper finds identifiers in text and replaces each distinct one with a token that is the same for the life of the Sweeper (one
// run). It is safe for concurrent use: four host calls may finish at once.
//
// A Sweeper in Hidden mode replaces IP addresses it finds by itself and the values it was told with Learn. In Allow mode it changes
// nothing.
type Sweeper struct {
	mu      sync.Mutex
	allow   bool
	tokens  map[Kind]map[string]string // kind -> canonical value -> token
	counts  map[Kind]int
	learned map[Kind][]string // values replaced inside text (at least minLearnLen bytes)
	exact   map[string]Kind   // every learned value; a short one is replaced only as a whole string
	repl    *strings.Replacer // built from learned values, nil when there are none
	stale   bool
}

// NewSweeper returns a Sweeper. allow=true means the operator allowed identifiers: it then returns every text unchanged.
func NewSweeper(allow bool) *Sweeper {
	return &Sweeper{
		allow:   allow,
		tokens:  map[Kind]map[string]string{},
		counts:  map[Kind]int{},
		learned: map[Kind][]string{},
		exact:   map[string]Kind{},
	}
}

// Allow reports whether the sweeper passes text unchanged.
func (s *Sweeper) Allow() bool { return s.allow }

// Learn tells the sweeper a raw value that must not appear (a user id or a user agent the broker saw in a tool's raw answer). The
// value is replaced by a token wherever it occurs in later text (or, when it is shorter than minLearnLen, where it is the whole
// string). An empty or too long value is ignored; at most maxLearned values are kept.
func (s *Sweeper) Learn(kind Kind, raw string) {
	if s.allow || raw == "" || len(raw) > maxLearnLen {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.exact[raw]; dup || len(s.exact) >= maxLearned {
		return
	}
	s.exact[raw] = kind
	if len(raw) >= minLearnLen {
		s.learned[kind] = append(s.learned[kind], raw)
		s.stale = true
	}
}

// Token returns the token for a value of a kind, making one on first sight. The same value always gives the same token in one
// Sweeper; two values never share one (until the overflow token, see maxTokens). A value is compared as written.
func (s *Sweeper) Token(kind Kind, value string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenLocked(kind, value)
}

func (s *Sweeper) tokenLocked(kind Kind, value string) string {
	m := s.tokens[kind]
	if m == nil {
		m = map[string]string{}
		s.tokens[kind] = m
	}
	if t, ok := m[value]; ok {
		return t
	}
	if s.counts[kind] >= maxTokens {
		return "[" + string(kind) + "_OVER]"
	}
	s.counts[kind]++
	t := "[" + string(kind) + "_" + strconv.Itoa(s.counts[kind]) + "]"
	m[value] = t
	return t
}

// Text returns in with every IP address and every learned value replaced by its token. Text(Text(x)) == Text(x): a token is not an
// address and holds no learned value.
func (s *Sweeper) Text(in string) string {
	if s.allow || in == "" {
		return in
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textLocked(in)
}

func (s *Sweeper) textLocked(in string) string {
	if k, ok := s.exact[in]; ok {
		return s.tokenLocked(k, in)
	}
	out := in
	if r := s.replacerLocked(); r != nil {
		out = r.Replace(out)
	}
	return s.replaceIPs(out)
}

// replacerLocked rebuilds the replacer after Learn. Longest value first: strings.Replacer tries the pairs in the order given at each
// position, so "Mozilla/5.0 (X11)" wins over "Mozilla". Tokens are made here, in that fixed order, so a token number does not depend
// on where a value first shows up in the text.
func (s *Sweeper) replacerLocked() *strings.Replacer {
	if !s.stale {
		return s.repl
	}
	s.stale = false
	type pair struct {
		raw  string
		kind Kind
	}
	var ps []pair
	for k, vs := range s.learned {
		for _, v := range vs {
			ps = append(ps, pair{v, k})
		}
	}
	if len(ps) == 0 {
		s.repl = nil
		return nil
	}
	sort.Slice(ps, func(i, j int) bool {
		if len(ps[i].raw) != len(ps[j].raw) {
			return len(ps[i].raw) > len(ps[j].raw)
		}
		return ps[i].raw < ps[j].raw
	})
	args := make([]string, 0, len(ps)*2)
	for _, p := range ps {
		args = append(args, p.raw, s.tokenLocked(p.kind, p.raw))
	}
	s.repl = strings.NewReplacer(args...)
	return s.repl
}

// replaceIPs replaces every IPv6 then every IPv4 address. Addresses in different spellings of one address share a token
// (::1 and 0:0:0:0:0:0:0:1; ::ffff:1.2.3.4 and 1.2.3.4; 001.002.003.004 and 1.2.3.4).
func (s *Sweeper) replaceIPs(in string) string {
	if !strings.ContainsAny(in, ".:") {
		return in
	}
	tok := func(canon string) string { return s.tokenLocked(KindIP, canon) }
	// A replacement can leave a fragment that reads as an address ("0::00.0.0.0" holds the v4 part 0.0.0.0, and "0::" is left), so the
	// two passes repeat until nothing is found. Each round removes at least one address, and a text that still holds one after
	// maxRounds is withheld whole (fail closed).
	for round := 0; round < maxRounds; round++ {
		next := scanIPs(scanIPs(in, true, tok), false, tok)
		if next == in {
			return in
		}
		in = next
	}
	if ContainsIP(in) {
		return "[IP_WITHHELD]"
	}
	return in
}

// ContainsIP reports whether text holds an IP address the sweeper would replace. The broker uses it as a last check after a sweep,
// and the tests use it as the oracle.
func ContainsIP(in string) bool {
	if !strings.ContainsAny(in, ".:") {
		return false
	}
	found := false
	probe := func(string) string { found = true; return "" }
	scanIPs(in, true, probe)
	scanIPs(in, false, probe)
	return found
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isWord(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// scanIPs walks in once, left to right, and calls tok for every address; the result is in with those spans replaced. v6 chooses
// the IPv6 scan, otherwise the IPv4 scan. Every scanner moves forward only.
func scanIPs(in string, v6 bool, tok func(canon string) string) string {
	var b strings.Builder
	last, i := 0, 0
	for i < len(in) {
		var from, to int
		var canon string
		var ok bool
		if v6 {
			from, to, canon, ok = nextV6(in, i)
		} else {
			from, to, canon, ok = nextV4(in, i)
		}
		if !ok {
			break
		}
		if b.Cap() == 0 {
			b.Grow(len(in))
		}
		b.WriteString(in[last:from])
		b.WriteString(tok(canon))
		last, i = to, to
	}
	if last == 0 {
		return in
	}
	b.WriteString(in[last:])
	return b.String()
}

// dottedEnd returns the end of the digits-and-dots number that starts at i (a "." counts only when a digit follows it).
func dottedEnd(s string, i int) int {
	for i < len(s) && (isDigit(s[i]) || (s[i] == '.' && i+1 < len(s) && isDigit(s[i+1]))) {
		i++
	}
	return i
}

// nextV4 finds the next dotted quad at or after pos. Each of the four parts is 1 to 3 digits and at most 255 (leading zeros are
// read as decimal: "001.002.003.004" is 1.2.3.4). A quad inside a longer dotted number ("10.0.19045.1234", "1.2.3.4.5") is not an
// address. A letter next to it does not stop it ("ip1.2.3.4"): fail closed.
func nextV4(s string, pos int) (from, to int, canon string, ok bool) {
	i := pos
	for i < len(s) {
		if !isDigit(s[i]) {
			i++
			continue
		}
		start := i
		end := dottedEnd(s, start) // the whole dotted number; the scan resumes here whatever the answer is
		i = end
		// a number that starts right after a "digit." is the tail of a longer dotted number that began before pos
		if start >= 2 && s[start-1] == '.' && isDigit(s[start-2]) {
			continue
		}
		var parts [4]int
		j := start
		good := true
		for p := 0; p < 4 && good; p++ {
			k := j
			v := 0
			for k < end && isDigit(s[k]) {
				v = v*10 + int(s[k]-'0')
				k++
				if k-j > 3 {
					break
				}
			}
			if k == j || k-j > 3 || v > 255 {
				good = false
				break
			}
			parts[p] = v
			j = k
			if p < 3 {
				if j >= end || s[j] != '.' {
					good = false
					break
				}
				j++
			}
		}
		if good && j == end {
			return start, end, strconv.Itoa(parts[0]) + "." + strconv.Itoa(parts[1]) + "." + strconv.Itoa(parts[2]) + "." + strconv.Itoa(parts[3]), true
		}
	}
	return 0, 0, "", false
}

// nextV6 finds the next IPv6 address at or after pos. A candidate is a run of hex digits, colons and dots; it must parse as an
// address with net/netip, hold a "::" or at least 7 colons (so a time "12:30:45" is never one), and must not touch a word character
// on either side ("std::vector" holds no address). A zone ("%eth0") after it is part of the span. A v4-mapped address gets the
// token of its v4 form.
func nextV6(s string, pos int) (from, to int, canon string, ok bool) {
	i := pos
	for i < len(s) {
		if !(isHex(s[i]) || s[i] == ':' || s[i] == '.') {
			i++
			continue
		}
		start, colons := i, 0
		for i < len(s) && (isHex(s[i]) || s[i] == ':' || s[i] == '.') {
			if s[i] == ':' {
				colons++
			}
			i++
		}
		if colons < 2 {
			continue
		}
		// Try the run, then the run without trailing ":" or "." (a sentence end), then the same without one leading ":" (a "label:"
		// in front). At most 2 x 5 attempts per run, and runs do not overlap.
		for lead := 0; lead <= 1; lead++ {
			l := start
			if lead == 1 {
				if s[start] != ':' || (start+1 < i && s[start+1] == ':') {
					break
				}
				l++
			}
			h := i
			for t := 0; t <= 4 && h > l; t++ {
				if f, e, c, good := tryV6(s, l, h); good {
					return f, e, c, true
				}
				if s[h-1] != '.' && s[h-1] != ':' {
					break
				}
				h--
			}
			// "addr:port" without brackets: the run has one group too many, so try it without the last group.
			if k := strings.LastIndexByte(s[l:i], ':'); k > 0 && i-(l+k) <= 6 {
				if f, e, c, good := tryV6(s, l, l+k); good {
					return f, e, c, true
				}
			}
		}
	}
	return 0, 0, "", false
}

func tryV6(s string, lo, hi int) (from, to int, canon string, ok bool) {
	if hi-lo < 2 {
		return
	}
	c := s[lo:hi]
	n := strings.Count(c, ":")
	if n < 2 || (!strings.Contains(c, "::") && n < 7) {
		return
	}
	if lo > 0 && isWord(s[lo-1]) {
		return
	}
	end := hi
	if end < len(s) && s[end] == '%' {
		z := end + 1
		for z < len(s) && z-end <= maxZone && (isWord(s[z]) || s[z] == '-' || s[z] == '.' || s[z] == '~') {
			z++
		}
		end = z
	} else if end < len(s) && isWord(s[end]) {
		return
	}
	a, err := netip.ParseAddr(c)
	if err != nil {
		return
	}
	return lo, end, a.Unmap().WithZone("").String(), true
}
