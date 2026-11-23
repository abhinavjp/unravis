package secretscrub

import (
	"strings"
	"testing"
	"time"
)

// Fuzz targets (BL-450). Every pull request replays the committed seeds and crashers under testdata/fuzz (a plain `go test` does);
// the nightly workflow (.github/workflows/nightly-fuzz.yml) runs the real fuzzer. A failing input found there is committed to
// testdata/fuzz/<Target>/ and becomes a permanent test.

// canaries are secrets in the forms Mask reads (not "Bearer x" and URL credentials: the callers have their own rules for those). Each one carries the word CANARY, which must never survive.
var canaries = []string{
	"DB_PASSWORD=CANARYa1\n",
	"password: CANARYb2\n",
	`{"name":"DB_PASSWORD","value":"CANARYc3"}` + "\n",
	"- name: DB_PASSWORD\n  value: CANARYd4\n",
	"mysql -u root --password CANARYe5 -h db\n",
	"sqlcmd -S x -P CANARYf6\n",
	"<Password>CANARYg7</Password>\n",
	`<add key="password" value="CANARYh8"/>` + "\n",
	`{"CommandLine":"sqlcmd -S x -P \"CANARYi9\""}` + "\n",
}

// FuzzMaskCanary puts a planted secret after any text, on its own line: whatever came before (an open quote, a half tag, a comment, a
// cut record) must not hide it.
func FuzzMaskCanary(f *testing.F) {
	f.Add("x", uint8(0))
	f.Add("{", uint8(2))
	f.Add("<A!=\"", uint8(7))
	f.Add("-pW,", uint8(5))
	f.Fuzz(func(t *testing.T, s string, k uint8) {
		in := s + "\n\n" + canaries[int(k)%len(canaries)]
		if out := Mask(in, m); strings.Contains(out, "CANARY") {
			t.Fatalf("a planted secret survived:\n in: %q\nout: %q", in, out)
		}
	})
}

// FuzzMaskBounds: no panic, output never more than a few times the input, and no input takes seconds.
func FuzzMaskBounds(f *testing.F) {
	for _, s := range []string{`["--password","x"]`, "sqlcmd -P x -S y", `{"name":"DB_PASSWORD","value":"a\"b"}`, "- name: K_SECRET\n  value: v\n", "<key>Password</key><string>x</string>", "a: b\n\tc"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		start := time.Now()
		out := Mask(s, m)
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("%s for %d bytes: %q", d, len(s), s)
		}
		if len(out) > 4*len(s)+64 {
			t.Fatalf("output grew from %d to %d", len(s), len(out))
		}
		var c Carry
		for _, line := range strings.SplitAfter(s, "\n") {
			_ = c.Line(line, m)
		}
	})
}

// Found by FuzzMaskCanary on its first run.
func TestMaskStrayQuoteDoesNotHideTheNextTag(t *testing.T) {
	in := "<A!=\"\n\n" + `<add key="password" value="CANARYh8"/>` + "\n"
	if out := Mask(in, m); strings.Contains(out, "CANARY") {
		t.Fatalf("leaked: %q", out)
	}
}

func TestMaskShortOptionWithCommaDoesNotTakeTheNextLine(t *testing.T) {
	in := "-pW,\n\nsqlcmd -S x -P CANARYf6\n"
	out := Mask(in, m)
	if strings.Contains(out, "CANARY") || !strings.Contains(out, "sqlcmd") {
		t.Fatalf("got %q", out)
	}
}
