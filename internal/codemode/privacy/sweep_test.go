package privacy

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSweepIPv4Table(t *testing.T) {
	cases := []struct{ in, want string }{
		{"from 10.1.2.3 to 10.1.2.4", "from [IP_1] to [IP_2]"},
		{"again 10.1.2.3", "again [IP_1]"},
		{"10.1.2.3:443", "[IP_1]:443"},
		{"ip=10.1.2.3,", "ip=[IP_1],"},
		{"(10.1.2.3).", "([IP_1])."},
		{"ip10.1.2.3", "ip[IP_1]"},
		{"010.001.002.003", "[IP_1]"}, // leading zeros: same address as 10.1.2.3
		{"999.1.1.1", "999.1.1.1"},
		{"10.0.19045.1234", "10.0.19045.1234"}, // a build number
		{"1.2.3.4.5", "[IP_1]"},                // a window of four reads as an address: fail closed
		{"1.2.3", "1.2.3"},
		{"v6.1.7601.65", "v6.1.7601.65"},
		{"192.168.0.1, 192.168.0.2", "[IP_1], [IP_2]"},
		{"no address here", "no address here"},
		{"", ""},
	}
	for _, c := range cases {
		s := NewSweeper(false)
		if c.in == "010.001.002.003" {
			s.Text("10.1.2.3") // first sight with the plain spelling
		}
		if got := s.Text(c.in); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestSweepIPv6Table(t *testing.T) {
	cases := []struct{ in, want string }{
		{"peer ::1 closed", "peer [IP_1] closed"},
		{"peer 0:0:0:0:0:0:0:1 closed", "peer [IP_1] closed"},
		{"peer [fe80::1%eth0]:443", "peer [[IP_1]]:443"},
		{"peer 2001:db8::ff00:42:8329.", "peer [IP_1]."},
		{"label:2001:db8::1", "label:[IP_1]"},
		{"mapped ::ffff:10.1.2.3", "mapped [IP_1]"},
		{"time 12:30:45 only", "time 12:30:45 only"},
		{"std::vector<int>", "std::vector<int>"},
		{"a::b", "[IP_1]"}, // a valid v6 address: a hex-looking pair is tokenized, which is the safe side
		{"Class::method", "Class::method"},
		{"2001:db8:0:0:0:0:0:1", "[IP_1]"},
	}
	for _, c := range cases {
		s := NewSweeper(false)
		if strings.HasPrefix(c.in, "peer 0:0") {
			s.Text("::1")
		}
		if strings.HasPrefix(c.in, "mapped") {
			s.Text("10.1.2.3")
		}
		got := s.Text(c.in)
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestSweepFragmentLeftByReplacement(t *testing.T) {
	s := NewSweeper(false)
	out := s.Text("0::00.0.0.0")
	if ContainsIP(out) || strings.Contains(out, "0::") {
		t.Fatalf("got %q", out)
	}
	if s.Text(out) != out {
		t.Fatal("not idempotent")
	}
}

func TestSweepIPv6Port(t *testing.T) {
	s := NewSweeper(false)
	out := s.Text("peer 2d46:c670:d3e1:5cea:b30e:f6a1:ce38:af1d:8080 gone")
	if ContainsIP(out) || strings.Contains(out, "2d46") {
		t.Fatalf("leak: %q", out)
	}
	if out != "peer [IP_1]:8080 gone" {
		t.Fatalf("got %q", out)
	}
}

func TestSweepAllowModeChangesNothing(t *testing.T) {
	s := NewSweeper(true)
	s.Learn(KindUser, "alice@example.com")
	in := "10.1.2.3 ::1 alice@example.com"
	if got := s.Text(in); got != in {
		t.Fatal(got)
	}
	if !s.Allow() {
		t.Fatal("not allow")
	}
}

func TestLearnedValues(t *testing.T) {
	s := NewSweeper(false)
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"
	s.Learn(KindUA, ua)
	s.Learn(KindUA, "Mozilla/5.0")
	s.Learn(KindUser, "u-4821")
	s.Learn(KindUser, "7") // short: whole-string only
	got := s.Text("agent " + ua + " user u-4821 and Mozilla/5.0 and 7 of 70")
	for _, raw := range []string{ua, "u-4821", "Mozilla"} {
		if strings.Contains(got, raw) {
			t.Fatalf("raw %q left in %q", raw, got)
		}
	}
	if !strings.Contains(got, "7 of 70") {
		t.Fatalf("short learned value damaged ordinary text: %q", got)
	}
	first := s.Text("7")
	if first != s.Text("7") || first == "7" {
		t.Fatal("whole-string short value not tokenized stably")
	}
	// the longest value wins over its prefix
	if !strings.Contains(got, s.Token(KindUA, asciiLower(ua))) {
		t.Fatalf("long user agent has no token: %q", got)
	}
}

func TestLearnBounds(t *testing.T) {
	s := NewSweeper(false)
	s.Learn(KindUser, "")
	s.Learn(KindUser, strings.Repeat("x", maxLearnLen+1))
	for i := 0; i < maxLearned+50; i++ {
		s.Learn(KindUser, fmt.Sprintf("user-%06d", i))
	}
	if len(s.exact) != maxLearned {
		t.Fatalf("kept %d", len(s.exact))
	}
	if strings.Contains(s.Text("user-000001"), "user-000001") {
		t.Fatal("learned value not replaced")
	}
}

func TestTokenOverflowStaysBounded(t *testing.T) {
	s := NewSweeper(false)
	for i := 0; i < maxTokens+10; i++ {
		s.Token(KindUser, fmt.Sprintf("raw-%d", i))
	}
	if got := s.Token(KindUser, "raw-new"); got != "[USER_OVER]" {
		t.Fatalf("got %q", got)
	}
	if got := s.Token(KindUser, "raw-0"); got != "[USER_1]" {
		t.Fatalf("an earlier value changed token: %q", got)
	}
	if len(s.tokens[KindUser]) != maxTokens {
		t.Fatalf("map grew to %d", len(s.tokens[KindUser]))
	}
}

// Property: for random addresses written in random spellings and put into random text, (1) the raw address never survives,
// (2) the same address gets the same token in every spelling and place, (3) different addresses never share a token, (4) a second
// sweep changes nothing, (5) text around the address is kept.
func TestPropertyIPTokens(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a seeded generator for reproducible test data
	pre := []string{"", "from ", "ip=", "peer: ", "(", "[", "\"", "host ", "x -> "}
	post := []string{"", " ok", ",", ".", ")", "]", ":8080", "\"", "; next"}
	for round := 0; round < 40; round++ {
		s := NewSweeper(false)
		byCanon := map[string]string{}
		byToken := map[string]string{}
		for n := 0; n < 300; n++ {
			var addr netip.Addr
			if rng.IntN(2) == 0 {
				var b [4]byte
				for i := range b {
					b[i] = byte(rng.IntN(256)) //nolint:gosec // G115: the value is below 256
				}
				addr = netip.AddrFrom4(b)
			} else {
				var b [16]byte
				for i := range b {
					b[i] = byte(rng.IntN(256)) //nolint:gosec // G115: the value is below 256
				}
				if rng.IntN(3) == 0 { // sparse, so "::" forms occur
					for i := 2; i < 14; i++ {
						b[i] = 0
					}
				}
				addr = netip.AddrFrom16(b)
			}
			canon := addr.Unmap().String()
			spell := canon
			if addr.Is6() && rng.IntN(2) == 0 {
				spell = fullV6(addr)
			}
			if addr.Is6() && rng.IntN(4) == 0 {
				spell = strings.ToUpper(spell)
			}
			p, q := pre[rng.IntN(len(pre))], post[rng.IntN(len(post))]
			if q == ":8080" && addr.Is6() {
				q = "" // "v6:port" without brackets is ambiguous; the sweeper takes the port into the token (see TestSweepIPv6Port)
			}
			in := p + spell + q
			out := s.Text(in)
			if l := leakIP(out); l != "" {
				t.Fatalf("sweep left an address (%s): %q -> %q", l, in, out)
			}
			if !strings.HasPrefix(out, p) || !strings.HasSuffix(out, q) {
				t.Fatalf("surrounding text changed: %q -> %q", in, out)
			}
			tok := out[len(p) : len(out)-len(q)]
			if !strings.HasPrefix(tok, "[IP_") || !strings.HasSuffix(tok, "]") {
				t.Fatalf("not a token: %q from %q", tok, in)
			}
			if prev, ok := byCanon[canon]; ok && prev != tok {
				t.Fatalf("%s got two tokens %s and %s", canon, prev, tok)
			}
			byCanon[canon] = tok
			if other, ok := byToken[tok]; ok && other != canon {
				t.Fatalf("collision: %s and %s share %s", other, canon, tok)
			}
			byToken[tok] = canon
			if s.Text(out) != out {
				t.Fatalf("second sweep changed %q", out)
			}
		}
	}
}

func fullV6(a netip.Addr) string {
	b := a.As16()
	parts := make([]string, 8)
	for i := range parts {
		parts[i] = fmt.Sprintf("%x", uint16(b[2*i])<<8|uint16(b[2*i+1]))
	}
	return strings.Join(parts, ":")
}

// Property: tokens are numbered by first sight in the run, so two runs that see different data number differently, and the token
// says nothing about the value.
func TestTokensAreNotDerivedFromTheValue(t *testing.T) {
	a, b := NewSweeper(false), NewSweeper(false)
	if a.Text("10.0.0.1 10.0.0.2") != "[IP_1] [IP_2]" || b.Text("10.0.0.2 10.0.0.1") != "[IP_1] [IP_2]" {
		t.Fatal("numbering is not by first sight")
	}
}

func TestPropertyLearnedNoLeakNoCollision(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9)) //nolint:gosec // G404: seeded, reproducible test data
	s := NewSweeper(false)
	raws := make([]string, 200)
	seen := map[string]bool{}
	for i := range raws {
		for {
			r := fmt.Sprintf("usr_%08x", rng.Uint32())
			if !seen[r] {
				seen[r], raws[i] = true, r
				break
			}
		}
		s.Learn(KindUser, raws[i])
	}
	toks := map[string]string{}
	for n := 0; n < 2000; n++ {
		r := raws[rng.IntN(len(raws))]
		in := "a " + r + " b " + r + "."
		out := s.Text(in)
		if strings.Contains(out, "usr_") {
			t.Fatalf("raw left: %q", out)
		}
		tok := s.Token(KindUser, r)
		if out != "a "+tok+" b "+tok+"." {
			t.Fatalf("got %q", out)
		}
		if o, ok := toks[tok]; ok && o != r {
			t.Fatalf("collision %s %s", o, r)
		}
		toks[tok] = r
	}
}

func TestSweeperConcurrent(t *testing.T) {
	s := NewSweeper(false)
	var wg sync.WaitGroup
	res := make([]string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				res[g] = s.Text(fmt.Sprintf("a 10.0.%d.%d b", g, i%50))
				s.Learn(KindUser, fmt.Sprintf("user-%d-%d", g, i))
				_ = s.Text("user-1-3 10.0.0.1")
			}
		}()
	}
	wg.Wait()
	if ContainsIP(strings.Join(res, " ")) {
		t.Fatal("leak under concurrency")
	}
}

func TestSweepLinearOnHostileInput(t *testing.T) {
	for name, in := range map[string]string{
		"colons":   strings.Repeat(":", 1<<20),
		"colhex":   strings.Repeat("a:", 1<<19),
		"dots":     strings.Repeat("1.", 1<<19),
		"dotsdig":  strings.Repeat("1.2.3.4.", 1<<17),
		"run":      strings.Repeat("::1", 1<<18),
		"zones":    "::1%" + strings.Repeat("z", 1<<20),
		"hexdots":  strings.Repeat("f.", 1<<19),
		"trailing": strings.Repeat("1:", 1<<19) + strings.Repeat(".", 1<<19),
	} {
		start := time.Now()
		s := NewSweeper(false)
		out := s.Text(in)
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("%s: %v", name, d)
		}
		if len(out) > 4*len(in)+64 {
			t.Errorf("%s: output grew from %d to %d", name, len(in), len(out))
		}
	}
}

// Fuzz targets: the committed seeds run in every `go test`; nightly-fuzz.yml runs the real fuzzer.

func FuzzSweepText(f *testing.F) {
	for _, s := range []string{"10.1.2.3", "::1", "fe80::1%eth0", "a::b:1.2.3.4", "1.2.3.4.5", ":::", "[::ffff:10.0.0.1]:80", "x 255.255.255.255.", "0:0:0:0:0:0:0:0", "12:30:45", "\xff10.0.0.1\xfe"} {
		f.Add(s, uint8(1))
	}
	f.Fuzz(func(t *testing.T, in string, seed uint8) {
		s := NewSweeper(false)
		if seed%2 == 0 && len(in) >= minLearnLen && len(in) < 40 {
			s.Learn(KindUser, in)
		}
		out := s.Text(in)
		if l := leakIP(out); l != "" {
			t.Fatalf("address survives (%s): %q -> %q", l, in, out)
		}
		if ContainsIP(out) {
			t.Fatalf("scanner still sees an address: %q -> %q", in, out)
		}
		if again := s.Text(out); again != out {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, out, again)
		}
		if len(out) > 4*len(in)+64 {
			t.Fatalf("grew: %d -> %d", len(in), len(out))
		}
	})
}

// FuzzSweepPlanted joins a real address to arbitrary text on both sides with nothing in between (no space, punctuation, digits,
// letters, dots and colons all touch it). The independent oracle must find nothing in the output.
func FuzzSweepPlanted(f *testing.F) {
	f.Add("x", "y", uint8(0))
	f.Add("a:", ":b", uint8(1))
	f.Add("1.", ".2", uint8(2))
	f.Add("n=5.", "", uint8(4))
	f.Add("", ".443", uint8(0))
	f.Add("0", "", uint8(4))
	f.Fuzz(func(t *testing.T, pre, post string, pick uint8) {
		addrs := []string{"10.20.30.40", "2001:db8::7", "::1", "fe80::abcd:1%eth0", "192.168.1.1", "192.168.001.001", "2001:db8:0:0:0:0:0:1"}
		a := addrs[int(pick)%len(addrs)]
		in := pre + a + post
		out := NewSweeper(false).Text(in)
		if l := leakIP(out); l != "" {
			t.Fatalf("leak (%s): %q -> %q", l, in, out)
		}
	})
}
