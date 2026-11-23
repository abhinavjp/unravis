package privacy

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
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
	// minLearnLen is the shortest value that is replaced anywhere inside longer text. A value of minWordLen to minLearnLen-1 bytes
	// is replaced where it is a whole word (not touching a letter, digit or "_"); a shorter one only where it is the whole string,
	// a key, or a whole JSON number.
	minLearnLen = 6
	minWordLen  = 3
	// The limits of Learn. A value or a count over them is refused with an error and the Guard is then Incomplete: nothing is
	// released from a run whose identifiers were not all taken in. They also bound the cost: the automaton for the largest allowed
	// set takes well under a second to build, in steps (see buildDue).
	maxLearnLen   = 16 << 10
	maxLearned    = 8192
	maxLearnBytes = 512 << 10
	// pendMin and pendMax: learned values wait outside the automaton and are looked for one by one until there are pendMin of them
	// and at least half as many as the automaton holds (or pendMax); then all go into a new automaton. The builds follow a
	// geometric schedule, so their total cost is a small multiple of the cost of the last one.
	pendMin = 32
	pendMax = 2048
	// scanBudget bounds the work of the address scan of one text: this many steps per byte plus a floor. A text that needs more is
	// withheld whole (fail closed), so no input costs more than a constant per byte.
	scanBudgetPerByte = 16
	scanBudgetFloor   = 8192
)

// maxRounds bounds the repeat of the two address passes over one text. A variable so a test can reach the withheld branch.
var maxRounds = 4

// Learn errors. A Learn that fails leaves the Guard Incomplete.
var (
	ErrLearnTooLong = errors.New("privacy: identifier too long to track")
	ErrLearnFull    = errors.New("privacy: too many identifiers to track")
)

// sweeper finds identifiers in text and replaces each distinct one with a token that is the same for the life of the sweeper (one
// run). It is not exported: the Guard is the only way in, so no text leaves a run before the identifiers of the answer it belongs
// to were learned, and no output is made after a refused Learn.
//
// Tokens are numbered by first sight in the texts the sweeper is given, in the order the calls reach it. They do not depend on the
// value, on the order Learn was called in, or on its length.
//
// A sweeper in hidden mode replaces IP addresses it finds by itself and the values it was told with Learn (without regard to ASCII
// case). In allow mode it changes nothing.
type sweeper struct {
	mu         sync.Mutex
	allow      bool
	incomplete bool
	tokens     map[Kind]map[string]string // kind -> key -> token
	counts     map[Kind]int
	exact      map[string]Kind // every learned value, lower case: replaced when it is a whole string, a key or a JSON number
	short      bool            // some learned value is of minWordLen to minLearnLen-1 bytes
	base       *matcher        // learned values (at least minLearnLen bytes) inside longer text
	basePats   []pat
	pend       []pat
	nBytes     int
}

func newSweeper(allow bool) *sweeper {
	return &sweeper{
		allow:  allow,
		tokens: map[Kind]map[string]string{},
		counts: map[Kind]int{},
		exact:  map[string]Kind{},
	}
}

func (s *sweeper) isAllow() bool { return s.allow }

func (s *sweeper) isIncomplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.incomplete
}

// learn tells the sweeper a raw value that must not appear. The value is replaced by a token wherever it occurs in later text (case
// is ignored for ASCII letters); see minLearnLen for the short ones. A value that holds text that looks like a token is also learned
// in its escaped form, because the text it is looked for in has been escaped. An empty value is ignored. A value over maxLearnLen
// bytes, or one that would pass maxLearned values or maxLearnBytes bytes, is refused with an error and the sweeper becomes
// incomplete; it is never dropped quietly.
func (s *sweeper) learn(kind Kind, raw string) error {
	if s.allow || raw == "" {
		return nil
	}
	err := s.learnOne(kind, raw)
	if esc := escapeTokens(raw); esc != raw {
		if e2 := s.learnOne(kind, esc); err == nil {
			err = e2
		}
	}
	return err
}

func (s *sweeper) learnOne(kind Kind, raw string) error {
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
	switch {
	case len(low) >= minLearnLen:
		s.pend = append(s.pend, pat{low, kind})
		if len(s.pend) >= pendMax || (len(s.pend) >= pendMin && len(s.pend)*2 >= len(s.basePats)) {
			s.basePats = append(s.basePats, s.pend...)
			s.pend = s.pend[:0]
			s.base = buildMatcher(s.basePats)
		}
	case len(low) >= minWordLen:
		s.short = true
	}
	return nil
}

func (s *sweeper) token(kind Kind, value string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenLocked(kind, value)
}

func (s *sweeper) tokenLocked(kind Kind, value string) string {
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

// tokenKinds are the names a token can carry, in lower case, each with the "_" after it.
var tokenKinds = []string{"ip_", "user_", "ua_"}

func startsKind(s string) bool {
	for _, k := range tokenKinds {
		if len(s) >= len(k) && strings.EqualFold(s[:len(k)], k) {
			return true
		}
	}
	return false
}

// escapeTokens writes one more "_" after the "[" of every text that looks like a token: "[IP_1]" becomes "[_IP_1]", "[_IP_1]"
// becomes "[__IP_1]", also in any letter case. It is injective (no two inputs give one output) and nothing the sweeper makes looks
// like its output, so a real token can always be told from data. It is applied once, to raw host data on its way in (the Guard's
// host calls), never to text a sweeper has made and never on the way out.
func escapeTokens(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] == '_' {
			j++
		}
		if startsKind(s[j:]) {
			b.WriteString(s[last : i+1])
			b.WriteByte('_')
			last = i + 1
		}
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// tokenSpans returns the stretches of s that are tokens of this sweeper: "[IP_12]", "[USER_3]", "[UA_1]", "[IP_OVER]",
// "[IP_WITHHELD]". A learned value never matches inside one.
func tokenSpans(s string) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		for _, k := range []string{"IP_", "USER_", "UA_"} {
			if !strings.HasPrefix(s[i+1:], k) {
				continue
			}
			j := i + 1 + len(k)
			e := j
			for e < len(s) && (isDigit(s[e]) || (s[e] >= 'A' && s[e] <= 'Z')) {
				e++
			}
			if e > j && e < len(s) && s[e] == ']' {
				out = append(out, [2]int{i, e + 1})
				i = e
			}
			break
		}
	}
	return out
}

// text returns in with every IP address and every learned value replaced by its token, and invisible format characters removed.
// text(text(x)) == text(x): a token is not an address and no learned value matches inside one.
func (s *sweeper) text(in string) string {
	if s.allow || in == "" {
		return in
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textLocked(in)
}

// learned is text without the address pass: only the learned values. The Guard runs it first, before the scrubbers cut anything out
// of a learned value (a user agent holds a URL), and runs text last.
func (s *sweeper) learned(in string) string {
	if s.allow || in == "" {
		return in
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.learnedLocked(stripInvisible(in))
}

func (s *sweeper) textLocked(in string) string {
	return s.replaceIPs(s.learnedLocked(stripInvisible(in)))
}

// stripInvisible removes the characters of category Cf (zero width space, joiners, soft hyphen, byte order mark): they split an
// address or an identifier for the scanner and not for a reader.
func stripInvisible(s string) string {
	// removing a character can join the bytes around it into a new one ("\xd8" + U+0604 + "\x84" is U+0604 once the middle is
	// gone), so it repeats until nothing changes; each round makes the text shorter
	for {
		t := stripInvisibleOnce(s)
		if len(t) == len(s) {
			return t
		}
		s = t
	}
}

func stripInvisibleOnce(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r != utf8.RuneError || n != 1) && unicode.Is(unicode.Cf, r) {
			i += n
			continue
		}
		b.WriteString(s[i : i+n]) // invalid bytes are copied as they are
		i += n
	}
	return b.String()
}

// learnedLocked replaces the learned values. Overlapping finds are joined into one span, so no part of a learned value is left
// ("abcdef" and "cdefgh" learned: "abcdefgh" is one token).
func (s *sweeper) learnedLocked(in string) string {
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
	if s.base == nil && len(s.pend) == 0 && !s.short {
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
	if s.short {
		sp = s.shortWordSpans(low, sp)
	}
	if len(sp) == 0 {
		return in
	}
	sp = mergeSpans(sp)
	var toks [][2]int
	if strings.Contains(in, "[") {
		toks = tokenSpans(in)
	}
	var b strings.Builder
	b.Grow(len(in))
	last := 0
	for _, x := range sp {
		if insideAny(toks, x.from, x.to) {
			continue
		}
		var key string
		var kind Kind
		switch {
		case x.p >= 0:
			key, kind = s.basePats[x.p].lower, s.basePats[x.p].kind
		case x.p == shortP:
			key = low[x.from:x.to]
			if k, ok := s.exact[key]; ok {
				kind = k
			} else {
				kind = KindUser // a joined span: the text is no learned value of its own
			}
		default:
			key, kind = s.pend[-1-x.p].lower, s.pend[-1-x.p].kind
		}
		b.WriteString(in[last:x.from])
		b.WriteString(s.tokenLocked(kind, key))
		last = x.to
	}
	b.WriteString(in[last:])
	return b.String()
}

func insideAny(toks [][2]int, from, to int) bool {
	for _, t := range toks {
		if from >= t[0] && to <= t[1] {
			return true
		}
	}
	return false
}

// shortWordSpans adds a span for every whole word of low that is a learned value of minWordLen to minLearnLen-1 bytes. A word is
// a run of letters, digits and "_". Short spans carry p = shortP so the caller reads the key from the text.
func (s *sweeper) shortWordSpans(low string, sp []span) []span {
	for i := 0; i < len(low); {
		if !isWord(low[i]) {
			i++
			continue
		}
		j := i
		for j < len(low) && isWord(low[j]) {
			j++
		}
		if n := j - i; n >= minWordLen && n < minLearnLen {
			if _, ok := s.exact[low[i:j]]; ok {
				sp = append(sp, span{from: i, to: j, p: shortP})
			}
		}
		i = j
	}
	return sp
}

// shortP marks a span made by shortWordSpans.
const shortP = -1 << 30

// replaceIPs replaces every IPv4 then every IPv6 address. Addresses in different spellings of one address share a token
// (::1 and 0:0:0:0:0:0:0:1; 001.002.003.004 and 1.2.3.4). The v4 pass runs first so an address with an embedded v4 tail
// ("::ffff:192.168.001.050", "x::150.151.152.153A") is found by the lenient v4 reader and the "::ffff:" left in front is read on
// its own; no octet is left behind.
func (s *sweeper) replaceIPs(in string) string {
	if !strings.ContainsAny(in, ".:") {
		return in
	}
	tok := func(canon string) string { return s.tokenLocked(KindIP, canon) }
	budget := scanBudgetFloor + scanBudgetPerByte*len(in)
	// A replacement can leave a fragment that reads as an address ("0::00.0.0.0" holds the v4 part 0.0.0.0, and "0::" is left), so the
	// two passes repeat until nothing is found. Each round removes at least one address, and a text that still holds one after
	// maxRounds is withheld whole (fail closed), as is a text that costs more than the budget.
	for round := 0; round < maxRounds; round++ {
		next, ok1 := scanIPs(in, false, tok, &budget)
		next, ok2 := scanIPs(next, true, tok, &budget)
		if !ok1 || !ok2 {
			return "[IP_WITHHELD]"
		}
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

// ContainsIP reports whether text holds an IP address the sweeper would replace. A text that costs more than the scan budget counts
// as holding one.
func ContainsIP(in string) bool {
	if !strings.ContainsAny(in, ".:") {
		return false
	}
	found := false
	probe := func(string) string { found = true; return "" }
	budget := scanBudgetFloor + scanBudgetPerByte*len(in)
	_, ok1 := scanIPs(in, false, probe, &budget)
	_, ok2 := scanIPs(in, true, probe, &budget)
	return found || !ok1 || !ok2
}

// numberKey finds the learned value a JSON number stands for: the number as written, or, for a whole number written with a point or
// an exponent ("1.234567e6", "1234567.0"), as an integer.
func (s *sweeper) numberKey(num string) (string, bool) {
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
