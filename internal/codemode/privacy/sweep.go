package privacy

import (
	"errors"
	"math"
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
	// in the text, so it is replaced only when it is the whole string (a JSON string value, a key, or a JSON number).
	minLearnLen = 6
	// The limits of Learn. A value or a count over them is refused with an error and the Sweeper is then Incomplete: the broker must
	// not release anything from a run whose identifiers were not all taken in (see Guard).
	maxLearnLen   = 16 << 10
	maxLearned    = 16384
	maxLearnBytes = 1 << 20
	// pendMin and pendMax: learned values wait outside the automaton and are looked for one by one until there are pendMin of them
	// and at least a quarter as many as the automaton holds (or pendMax), then all go into a new automaton. The build cost is paid
	// on a geometric schedule, so N Learn calls cost O(N) builds of growing size, not N builds.
	pendMin = 32
	pendMax = 1024
)

// maxRounds bounds the repeat of the two address passes over one text. A variable so a test can reach the withheld branch.
var maxRounds = 4

// Learn errors. A Learn that fails leaves the Sweeper Incomplete.
var (
	ErrLearnTooLong = errors.New("privacy: identifier too long to track")
	ErrLearnFull    = errors.New("privacy: too many identifiers to track")
)

// Sweeper finds identifiers in text and replaces each distinct one with a token that is the same for the life of the Sweeper (one
// run). It is safe for concurrent use: four host calls may finish at once.
//
// Tokens are numbered by first sight in the texts the Sweeper is given, in the order the calls reach it. They do not depend on the
// value, on the order Learn was called in, or on its length.
//
// A Sweeper in Hidden mode replaces IP addresses it finds by itself and the values it was told with Learn (without regard to ASCII
// case). In Allow mode it changes nothing.
type Sweeper struct {
	mu         sync.Mutex
	allow      bool
	incomplete bool
	tokens     map[Kind]map[string]string // kind -> key -> token
	counts     map[Kind]int
	exact      map[string]Kind // every learned value, lower case: replaced when it is a whole string, a key or a JSON number
	base       *matcher        // learned values (at least minLearnLen bytes) inside longer text
	basePats   []pat
	pend       []pat
	nBytes     int
}

// NewSweeper returns a Sweeper. allow=true means the operator allowed identifiers: it then returns every text unchanged.
func NewSweeper(allow bool) *Sweeper {
	return &Sweeper{
		allow:  allow,
		tokens: map[Kind]map[string]string{},
		counts: map[Kind]int{},
		exact:  map[string]Kind{},
	}
}

// Allow reports whether the sweeper passes text unchanged.
func (s *Sweeper) Allow() bool { return s.allow }

// Incomplete reports that a Learn was refused, so some identifier may pass raw. The caller must withhold the run's output.
func (s *Sweeper) Incomplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.incomplete
}

// Learn tells the sweeper a raw value that must not appear (a user id or a user agent the broker saw in a tool's raw answer). The
// value is replaced by a token wherever it occurs in later text (case is ignored for ASCII letters); a value shorter than
// minLearnLen is replaced only where it is the whole string or a whole JSON number. An empty value is ignored. A value over
// maxLearnLen bytes, or one that would pass maxLearned values or maxLearnBytes bytes, is refused with an error and the Sweeper
// becomes Incomplete; it is never dropped quietly.
func (s *Sweeper) Learn(kind Kind, raw string) error {
	if s.allow || raw == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(raw) > maxLearnLen {
		s.incomplete = true
		return ErrLearnTooLong
	}
	low := asciiLower(raw)
	if _, dup := s.exact[low]; dup {
		return nil
	}
	if len(s.exact) >= maxLearned || s.nBytes+len(low) > maxLearnBytes {
		s.incomplete = true
		return ErrLearnFull
	}
	s.nBytes += len(low)
	s.exact[low] = kind
	if len(low) >= minLearnLen {
		s.pend = append(s.pend, pat{low, kind})
		if len(s.pend) >= pendMax || (len(s.pend) >= pendMin && len(s.pend)*4 >= len(s.basePats)) {
			s.mergePendLocked()
		}
	}
	return nil
}

// LearnAll is Learn for a list. It takes in every value it can and returns the first error.
func (s *Sweeper) LearnAll(kind Kind, raws []string) error {
	var first error
	for _, r := range raws {
		if err := s.Learn(kind, r); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Sweeper) mergePendLocked() {
	s.basePats = append(s.basePats, s.pend...)
	s.pend = s.pend[:0]
	s.base = buildMatcher(s.basePats)
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

// tokenReplacer writes the text of a token as the characters that cannot be mistaken for one.
var tokenReplacer = strings.NewReplacer("[IP_", "[_IP_", "[USER_", "[_USER_", "[UA_", "[_UA_")

// escapeTokens changes text that already looks like a token ("[USER_1]") into "[_USER_1]", so data cannot pass itself off as a
// token a script then groups with a real value. Applied to raw input only (JSON, Guard.Log, Guard.Error), never to text a Sweeper
// has already made.
func escapeTokens(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	return tokenReplacer.Replace(s)
}

// Text returns in with every IP address and every learned value replaced by its token. Text(Text(x)) == Text(x): a token is not an
// address and holds no learned value. It does not escape text that looks like a token (the Guard and JSON do, on raw input).
func (s *Sweeper) Text(in string) string {
	if s.allow || in == "" {
		return in
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textLocked(in)
}

// Learned is Text without the address pass: only the learned values. The Guard runs it first, before the scrubbers cut anything out
// of a learned value (a user agent holds a URL), and runs Text last.
func (s *Sweeper) Learned(in string) string {
	if s.allow || in == "" {
		return in
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.learnedLocked(in)
}

func (s *Sweeper) textLocked(in string) string {
	return s.replaceIPs(s.learnedLocked(in))
}

// learnedLocked replaces the learned values. Overlapping finds are joined into one span, so no part of a learned value stays behind
// ("abcdef" and "cdefgh" learned: "abcdefgh" is one token).
func (s *Sweeper) learnedLocked(in string) string {
	if len(s.exact) == 0 {
		return in
	}
	var low string
	if len(in) <= maxLearnLen {
		low = asciiLower(in)
		if k, ok := s.exact[low]; ok {
			return s.tokenLocked(k, low)
		}
	}
	if s.base == nil && len(s.pend) == 0 {
		return in
	}
	if low == "" {
		low = asciiLower(in)
	}
	var sp []span
	if s.base != nil {
		sp = s.base.scan(low, nil)
	}
	for pi, p := range s.pend {
		for from := 0; ; {
			j := strings.Index(low[from:], p.lower)
			if j < 0 {
				break
			}
			at := from + j
			sp = addSpan(sp, span{from: at, to: at + len(p.lower), p: -1 - pi})
			from = at + 1
		}
	}
	if len(sp) == 0 {
		return in
	}
	sp = mergeSpans(sp)
	var b strings.Builder
	b.Grow(len(in))
	last := 0
	for _, x := range sp {
		var pt pat
		if x.p >= 0 {
			pt = s.basePats[x.p]
		} else {
			pt = s.pend[-1-x.p]
		}
		b.WriteString(in[last:x.from])
		b.WriteString(s.tokenLocked(pt.kind, pt.lower))
		last = x.to
	}
	b.WriteString(in[last:])
	return b.String()
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

// ContainsIP reports whether text holds an IP address the sweeper would replace. The broker uses it as a last check after a sweep.
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

// numberKey finds the learned value a JSON number stands for: the number as written, or, for a whole number written with a point or
// an exponent ("1.234567e6", "1234567.0"), as an integer.
func (s *Sweeper) numberKey(num string) (string, bool) {
	if _, ok := s.exact[num]; ok {
		return num, true
	}
	if len(num) > 40 || !strings.ContainsAny(num, ".eE") {
		return "", false
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) >= 1<<53 {
		return "", false
	}
	n := strconv.FormatInt(int64(f), 10)
	if _, ok := s.exact[n]; ok {
		return n, true
	}
	return "", false
}
