package privacy

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// rawIdent is the regex sweep of the spec (BL-365 test TestHiddenModeNoRawIdentifiers): an IPv4 or IPv6 address, or the planted user
// id and user agent, anywhere in a text.
var rawIdent = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|\busr_[0-9a-f]{8}\b|CANARY-UA|[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{0,4}){2,}`)

type fixtureRow struct {
	User string `json:"user"`
	IP   string `json:"ip"`
	UA   string `json:"ua"`
	N    int    `json:"n"`
}

func fixture(rng *rand.Rand) []fixtureRow {
	users := make([]string, 12)
	for i := range users {
		users[i] = fmt.Sprintf("usr_%08x", rng.Uint32())
	}
	rows := make([]fixtureRow, 300)
	for i := range rows {
		u := users[rng.IntN(len(users))]
		rows[i] = fixtureRow{User: u, IP: fmt.Sprintf("10.%d.%d.%d", rng.IntN(4), rng.IntN(4), rng.IntN(4)), UA: "CANARY-UA/1." + fmt.Sprint(rng.IntN(3)), N: i % 7}
	}
	return rows
}

func TestHiddenModeNoRawIdentifiers(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // G404: seeded, reproducible test data
	rows := fixture(rng)
	g := NewGuard(false)
	for _, r := range rows {
		_ = g.Learn(KindUser, r.User)
		_ = g.Learn(KindUA, r.UA)
	}
	raw, _ := json.Marshal(map[string]any{"rows": rows, "note": "peer 10.9.9.9 / ::1 saw " + rows[0].User})
	out, err := g.Result(raw)
	if err != nil {
		t.Fatal(err)
	}
	logLine := g.Log("user " + rows[1].User + " from " + rows[1].IP + " agent " + rows[1].UA)
	errText := g.Error("tool failed for " + rows[2].User + " at " + rows[2].IP + " Server=db1;Password=CANARYpw")
	for name, text := range map[string]string{"result": string(out), "log": logLine, "error": errText} {
		if m := rawIdent.FindString(text); m != "" {
			t.Errorf("%s holds raw identifier %q: %s", name, m, text)
		}
	}
	if strings.Contains(errText, "CANARYpw") {
		t.Error("secret in error")
	}
}

func TestAllowModeShowsIdentifiers(t *testing.T) {
	g := NewGuard(true)
	_ = g.Learn(KindUser, "usr_deadbeef")
	if !g.AllowIdentifiers() {
		t.Fatal("mode")
	}
	raw := []byte(`{"user":"usr_deadbeef","ip":"10.1.2.3"}`)
	out, err := g.Result(raw)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("%s %v", out, err)
	}
	if got := g.Log("usr_deadbeef 10.1.2.3"); got != "usr_deadbeef 10.1.2.3" {
		t.Fatalf("log %q", got)
	}
	// secrets are scrubbed in every mode
	if e := g.Error("Server=h;Password=CANARYpw;"); strings.Contains(e, "CANARYpw") {
		t.Fatalf("error %q", e)
	}
}

// Acceptance 2: grouping by the token gives the same counts as grouping by the raw value, because one value has one token for the
// whole run, in any call, log or result.
func TestGroupByTokenEqualsGroupByRaw(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // G404: seeded, reproducible test data
	rows := fixture(rng)
	rawCounts := map[string]int{}
	for _, r := range rows {
		rawCounts[r.User]++
	}
	g := NewGuard(false)
	for _, r := range rows {
		_ = g.Learn(KindUser, r.User)
	}
	// the rows arrive in several host calls
	tokCounts := map[string]int{}
	for i := 0; i < len(rows); i += 50 {
		raw, _ := json.Marshal(rows[i:min(i+50, len(rows))])
		out, err := g.Result(raw)
		if err != nil {
			t.Fatal(err)
		}
		var got []fixtureRow
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		for _, r := range got {
			tokCounts[r.User]++
		}
	}
	if len(tokCounts) != len(rawCounts) {
		t.Fatalf("%d tokens for %d users", len(tokCounts), len(rawCounts))
	}
	a, b := countsSorted(rawCounts), countsSorted(tokCounts)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("raw %v tokens %v", a, b)
	}
}

func countsSorted(m map[string]int) []int {
	var v []int
	for _, n := range m {
		v = append(v, n)
	}
	for i := range v { // small insertion sort, no import needed
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	return v
}

func TestTokensStablePerRunDifferAcrossRuns(t *testing.T) {
	run := func(order []string) map[string]string {
		g := NewGuard(false)
		m := map[string]string{}
		for _, ip := range order {
			m[ip] = g.Log(ip)
		}
		return m
	}
	a := run([]string{"10.0.0.1", "10.0.0.2"})
	b := run([]string{"10.0.0.2", "10.0.0.1"})
	if a["10.0.0.1"] == b["10.0.0.1"] {
		t.Fatal("the token of a value is the same in two runs with different data: it must come from first sight, not the value")
	}
	g := NewGuard(false)
	if g.Log("10.0.0.1") != g.Log("x 10.0.0.1")[2:] {
		t.Fatal("not stable inside a run")
	}
}

func TestErrorScrubThenSweepAndCap(t *testing.T) {
	g := NewGuard(false)
	e := g.Error(`failed C:\Users\bob\case.db from 10.1.2.3 ` + strings.Repeat("x", 5000))
	if len(e) > MaxErrorBytes || !utf8.ValidString(e) {
		t.Fatalf("len %d", len(e))
	}
	if strings.Contains(e, "bob") || strings.Contains(e, "10.1.2.3") {
		t.Fatalf("leak: %q", e)
	}
}

func TestResultRefusesBadJSON(t *testing.T) {
	g := NewGuard(false)
	if out, err := g.Result([]byte(`{"ip":"10.1.2.3"`)); err == nil || out != nil {
		t.Fatalf("passed on %q", out)
	}
}

// FuzzGuardNoLeak joins learned identifiers, addresses and a secret to arbitrary text with nothing in between and checks every
// output with the independent oracle.
func FuzzGuardNoLeak(f *testing.F) {
	f.Add("a", "b", uint8(0))
	f.Add("\"", "\\", uint8(1))
	f.Add("1.", ".2", uint8(2))
	f.Add("'", "'", uint8(3))
	f.Add("C:\\", ":", uint8(4))
	f.Fuzz(func(t *testing.T, pre, post string, pick uint8) {
		learned := []string{"usr_cafe0001", "Alice.Smith@example.com", "Mozilla/5.0 (compatible; Bot/2.1; +http://bot.example.com/about)", "4711"}
		g := NewGuard(false)
		for _, l := range learned {
			if err := g.Learn(KindUser, l); err != nil {
				t.Fatal(err)
			}
		}
		planted := []string{"usr_cafe0001", "ALICE.SMITH@EXAMPLE.COM", learned[2], "10.20.30.40", "2001:db8::7", "10.1.2.3.443"}
		p := planted[int(pick)%len(planted)]
		in := pre + p + post
		outs := map[string]string{"log": g.Log(in), "error": g.Error(in)}
		raw, _ := json.Marshal(map[string]string{"k": in, in: "v"})
		res, err := g.Result(raw)
		if err != nil {
			t.Fatalf("own JSON refused: %v", err)
		}
		// the strings of the JSON, decoded: the oracle must not read the digits of a \u0026 escape as an address
		var dec any
		if err := json.Unmarshal(res, &dec); err != nil {
			t.Fatalf("output does not decode: %v", err)
		}
		var sb strings.Builder
		walk(dec, func(str string) { sb.WriteString(str); sb.WriteByte('\n') })
		outs["json"] = sb.String()
		for name, out := range outs {
			if l := leakIP(out); l != "" {
				t.Fatalf("%s: address (%s): %q -> %q", name, l, in, out)
			}
			if l := leakLearned(out, learned[:3]); l != "" && !strings.Contains(strings.ToLower(pre+post), strings.ToLower(l)) {
				t.Fatalf("%s: learned %q: %q -> %q", name, l, in, out)
			}
			if !utf8.ValidString(out) {
				t.Fatalf("%s: invalid UTF-8", name)
			}
		}
	})
}
