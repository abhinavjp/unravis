package privacy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	zwsp = "\u200b"
	shy  = "\u00ad"
	zwj  = "\u200d"
	bom  = "\ufeff"
)

// M1: an invisible character between "[" and the kind must not hide a token look-alike from the escape (strip first, then escape).
func TestReview3ForgedTokenThroughInvisibleChar(t *testing.T) {
	ids := IDs{Users: []string{"1234567"}}
	out, err := NewGuard(false).HostResult([]byte(`{"a":"x [`+zwsp+`USER_1234567] y"}`), ids)
	if err != nil || strings.Contains(string(out), "1234567") || strings.Contains(string(out), "[USER_1234567") {
		t.Fatalf("%s %v", out, err)
	}
	if got := NewGuard(false).HostError("["+zwsp+"USER_1234567]", ids); strings.Contains(got, "1234567") || strings.Contains(got, "[USER_1234567") {
		t.Fatalf("%q", got)
	}
	// data that forges a token for the script: it must reach the script escaped, never as a real-looking token
	out, _ = NewGuard(false).HostResult([]byte(`{"a":"[`+zwsp+`IP_1]","b":"[`+shy+`_`+zwj+`UA_2]"}`), IDs{})
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["a"] != "[_IP_1]" || m["b"] != "[__UA_2]" {
		t.Fatalf("%s", out)
	}
	if got := NewGuard(false).HostError("["+bom+"IP_1]", IDs{}); got != "[_IP_1]" {
		t.Fatalf("%q", got)
	}
}

// M2: a learned value that holds an invisible character is normalised like the text, so it still matches.
func TestReview3LearnedWithInvisibleChars(t *testing.T) {
	for _, c := range []struct{ learn, text, leak string }{
		{"ali" + shy + "ce.smith", "user ali" + shy + "ce.smith here", "alice"},
		{"ali" + shy + "ce.smith", "user alice.smith here", "alice"},
		{"bob" + zwj + "by99", "bob" + zwj + "by99", "bobby99"},
		{bom + "Mozilla/5.0 (X11)", bom + "Mozilla/5.0 (X11)", "Mozilla"},
		{bom + "Mozilla/5.0 (X11)", "Mozilla/5.0 (X11)", "Mozilla"},
	} {
		g := NewGuard(false)
		out, err := g.HostResult([]byte(fmt.Sprintf("{%q:%q}", "k", c.text)), IDs{Users: []string{c.learn}})
		if err != nil || strings.Contains(string(out), c.leak) || strings.Contains(string(out), "alice") {
			t.Errorf("%q in %q: %s %v", c.learn, c.text, out, err)
		}
		if got := NewGuard(false).HostError(c.text, IDs{UserAgents: []string{c.learn}}); strings.Contains(got, c.leak) {
			t.Errorf("error: %q", got)
		}
	}
}

// M3: the out side makes no numbered token: a script that writes guesses learns nothing from the numbers, and a replay gives the
// same text.
func TestReview3OutSideNoOracle(t *testing.T) {
	g := NewGuard(false)
	in, err := g.HostResult([]byte(`{"err":"connect from 10.0.37.200 refused","who":4711}`), IDs{Users: []string{"4711"}})
	if err != nil || !strings.Contains(string(in), "[IP_1]") || !strings.Contains(string(in), "[USER_1]") {
		t.Fatalf("%s %v", in, err)
	}
	var guesses []string
	for x := 0; x < 256; x++ {
		for y := 0; y < 256; y++ {
			guesses = append(guesses, fmt.Sprintf("10.0.%d.%d", x, y))
		}
	}
	raw, _ := json.Marshal(guesses)
	out, err := g.Output(raw)
	if err != nil {
		t.Fatal(err)
	}
	var back []string
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	for i, v := range back {
		if v != "[IP_OUT]" {
			t.Fatalf("guess %d came back as %q: the out side must treat every address alike", i, v)
		}
	}
	nums := make([]int, 10000)
	for i := range nums {
		nums[i] = i
	}
	raw, _ = json.Marshal(nums)
	out, err = g.Output(raw)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("a short learned id must not be matched on the out side (guess oracle): %.60s %v", out, err)
	}
	if got := g.Log("guess 4711 and alice"); got != "guess 4711 and alice" {
		t.Fatalf("%q", got)
	}
	// real tokens from the in side pass through the out side unchanged
	if got := g.Log("seen [IP_1] and [USER_1]"); got != "seen [IP_1] and [USER_1]" {
		t.Fatalf("%q", got)
	}
}

func TestReview3OutSideLongLearnedValue(t *testing.T) {
	g := NewGuard(false)
	ua := "Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101"
	if _, err := g.HostResult([]byte(`{"ua":"x"}`), IDs{UserAgents: []string{ua}}); err != nil {
		t.Fatal(err)
	}
	if got := g.Log("agent " + ua); got != "agent [UA_OUT]" {
		t.Fatalf("%q", got)
	}
}

// M3: replay. The live run and a new Guard (replay) give the same bytes for the same script output, whatever is in it.
func TestReview3ReplayHashStable(t *testing.T) {
	outputs := []string{
		`{"a":"[IP_1]","b":"9.9.9.9","c":"from 1.2.3.4","d":"fe80::1"}`,
		`{"x":["[USER_2]","8.8.8.8","[IP_1]","8.8.4.4"]}`,
	}
	live := NewGuard(false)
	if _, err := live.HostResult([]byte(`{"ip":"7.7.7.7","ip2":"6.6.6.6"}`), IDs{}); err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
		a, err := live.Output([]byte(o))
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewGuard(false).Output([]byte(o))
		if err != nil || string(a) != string(b) {
			t.Fatalf("live %s, replay %s (%v)", a, b, err)
		}
		again, _ := NewGuard(false).Output(a)
		if string(again) != string(a) {
			t.Fatalf("not idempotent: %s -> %s", a, again)
		}
	}
	for _, line := range []string{"a 9.9.9.9 b 1.2.3.4 [IP_1]", "x"} {
		if live.Log(line) != NewGuard(false).Log(line) {
			t.Fatalf("log differs for %q", line)
		}
	}
}

// M4: invalid UTF-8 cannot make the invisible-character step cost more than linear time.
func TestReview3NormalizeLinear(t *testing.T) {
	k := 50 << 10
	in := strings.Repeat("\xe2\x80", k) + zwsp + strings.Repeat("\x8b", k)
	for name, f := range map[string]func(){
		"HostError":   func() { _ = NewGuard(false).HostError(in, IDs{}) },
		"Log":         func() { _ = NewGuard(false).Log(in) },
		"ScriptError": func() { _ = NewGuard(false).ScriptError(in) },
		"normalize":   func() { _ = normalize(in) },
	} {
		start := time.Now()
		f()
		if d := time.Since(start); d > 10*time.Second { // the bug cost 30 s for 150 KB; a linear pass takes milliseconds
			t.Errorf("%s: %v", name, d)
		}
	}
	if a := normalize(in); normalize(a) != a {
		t.Fatal("normalize is not idempotent")
	}
}

// M5: the duplicate-name suffix costs O(1) per member.
func TestReview3DuplicateNamesLinear(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 16384; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		// many spellings of ::1, each a member name that ends as the same token
		b.WriteString(`"` + strings.Repeat("0:", 7-i%7) + strings.Repeat("0:", i%7) + `:` + strings.Repeat("0", i%4) + `1"`)
		b.WriteString(":1")
	}
	b.WriteString("}")
	start := time.Now()
	out, err := NewGuard(false).HostResult([]byte(b.String()), IDs{})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
	checkUniqueNames(t, out)

	// 30k case variants of one learned id as names
	b.Reset()
	b.WriteString("{")
	n := 30000
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		v := []byte("abcdefghijkl")
		for j := 0; j < 12; j++ {
			if i>>uint(j)&1 == 1 {
				v[j] -= 32
			}
		}
		b.WriteString(`"` + string(v) + `":1`)
	}
	b.WriteString("}")
	start = time.Now()
	out, err = NewGuard(false).HostResult([]byte(b.String()), IDs{Users: []string{"abcdefghijkl"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
	if strings.Contains(strings.ToLower(string(out)), "abcdef") {
		t.Fatal("leak")
	}
	checkUniqueNames(t, out)
}

func checkUniqueNames(t *testing.T, out []byte) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(out)))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		if seen[k.(string)] {
			t.Fatalf("duplicate member name %q", k)
		}
		seen[k.(string)] = true
		if _, err := dec.Token(); err != nil {
			t.Fatal(err)
		}
	}
}

// NIT: an unchanged name that equals the token of a changed one is renamed too, on both sides.
func TestReview3NameEqualToToken(t *testing.T) {
	s := newSweeper(false)
	_ = s.learn(KindUser, "alice1")
	out, err := s.json([]byte(`{"alice1":2,"[USER_1]":1}`), false)
	if err != nil {
		t.Fatal(err)
	}
	checkUniqueNames(t, out)
	out, err = s.json([]byte(`{"[USER_1]":1,"alice1":2}`), false)
	if err != nil {
		t.Fatal(err)
	}
	checkUniqueNames(t, out)
}

// M6: a scrubber that stops at ":" (UNC host) must not cut the first group off an address.
func TestReview3UNCBeforeAddress(t *testing.T) {
	for _, in := range []string{
		`\\3AE8:E4A3:FC8D:F749:B3DD:A2CB:EB49:B01B/64`,
		`x \\9337:993e:5f21:0:0:0:11:22 y`,
		`\\\\n9337:993e:5f21:0:0:0:11:22`,
		`open \\10.0.0.5\share\f.txt`,
	} {
		for name, out := range map[string]string{
			"Log":       NewGuard(false).Log(in),
			"HostError": NewGuard(false).HostError(in, IDs{}),
		} {
			if l := leakIP(out); l != "" || strings.Contains(strings.ToLower(out), "e4a3") || strings.Contains(strings.ToLower(out), "993e") || strings.Contains(out, "10.0.0.5") {
				t.Errorf("%s %q -> %q (%s)", name, in, out, l)
			}
		}
	}
}

// S1/S2: a digit glued to an address, by hand or by removing an invisible character, does not hide the octets.
func TestReview3GluedDigits(t *testing.T) {
	for _, c := range []struct {
		in string
		o  [4]int
	}{
		{"port 149.239.210.191" + bom + "9", [4]int{149, 239, 210, 191}},
		{"9218.250.173.149", [4]int{218, 250, 173, 149}},
		{"aZ107.0169.196.1971", [4]int{107, 169, 196, 197}},
		{"a::ffff:183.167.137.2079", [4]int{183, 167, 137, 207}},
		{"x 1.2.3.4" + zwsp + "5 y", [4]int{1, 2, 3, 4}},
	} {
		in := c.in
		for name, out := range map[string]string{
			"HostError": NewGuard(false).HostError(in, IDs{}),
			"Log":       NewGuard(false).Log(in),
		} {
			if l := leakIP(out); l != "" || leakPartialV4(out, c.o) != "" {
				t.Errorf("%s %q -> %q (%s)", name, in, out, l)
			}
		}
		raw, _ := json.Marshal(map[string]string{"v": in})
		out, err := NewGuard(false).HostResult(raw, IDs{})
		if err != nil || leakIP(string(out)) != "" || leakPartialV4(string(out), c.o) != "" {
			t.Errorf("json %q -> %s", in, out)
		}
	}
}

// S5: a double-quoted value after "column" in a Postgres error.
func TestReview3ColumnValue(t *testing.T) {
	got := ScrubError(`ERROR: column "alice@x.com" does not exist`)
	if strings.Contains(got, "alice@x.com") {
		t.Fatalf("%q", got)
	}
}

// NIT: a learned id too big for a float, written with a point.
func TestReview3BigNumberWithPoint(t *testing.T) {
	out, err := NewGuard(false).HostResult([]byte(`{"n":12345678901234567890.0,"m":12345678901234567890}`), IDs{Users: []string{"12345678901234567890"}})
	if err != nil || strings.Contains(string(out), "1234567890123") {
		t.Fatalf("%s %v", out, err)
	}
}
