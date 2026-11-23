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
	`<add key="password" value="CANARYj1" note="a<b"/>` + "\n",
	"[\"--password\",\n  \"CANARYk2==\"]\n",
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
		// The fuzzer may write the word into the text itself: count it, so only a planted one that survives fails.
		if out := Mask(in, m); strings.Count(out, "CANARY") > strings.Count(s, "CANARY") {
			t.Fatalf("a planted secret survived:\n in: %q\nout: %q", in, out)
		}
	})
}

// FuzzMaskBounds: no panic, output never more than a few times the input, and no input takes many seconds.
func FuzzMaskBounds(f *testing.F) {
	for _, s := range []string{`["--password","x"]`, "sqlcmd -P x -S y", `{"name":"DB_PASSWORD","value":"a\"b"}`, "- name: K_SECRET\n  value: v\n", "<key>Password</key><string>x</string>", "a: b\n\tc"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		start := time.Now()
		out := Mask(s, m)
		if d := time.Since(start); d > 10*time.Second {
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
	if out := Mask(in, m); strings.Contains(out, "CANARY") {
		t.Fatalf("got %q", out)
	}
}

// Review of the first fix: a "<" inside a quoted value is text, not a reason to drop the tag.
func TestMaskAngleBracketInAttributeValue(t *testing.T) {
	for _, in := range []string{
		`<add key="ApiToken" value="CANARY1" note="a<b"/>`,
		`<add key="password" value="CANARY2" desc="<b>bold</b>"/>`,
		`<add desc="<b>bold</b>" key="password" value="CANARY3"/>`,
		`<Token attr="<">CANARY4</Token>`,
		`<add key="password" value="CANARY5<ss"/>`,
		`<param name="token" value="CANARY6<17"/>`,
		`<add key="password" value="<%= CANARY7 %>"/>`,
		`<A!="` + "\n\n" + `<add key="password" value="CANARY8"/>`,
	} {
		for _, ctx := range []string{"%s", "%s\n" + strings.Repeat("x ", 3000), "<a>%s</a>"} {
			text := strings.Replace(ctx, "%s", in, 1)
			if out := Mask(text, m); strings.Contains(out, "CANARY") {
				t.Errorf("leaked:\n in: %.120q\nout: %.120q", text, out)
			}
		}
	}
}

// Review of the first fix: a comma at the end of an unquoted option does not hide the option on the next line, and a value on the next
// line is still masked.
func TestMaskCommaThenNewline(t *testing.T) {
	for _, in := range []string{
		"args: [--password,\n  CANARY1]",
		"(--password,\n CANARY2)",
		"`--password`,\n`CANARY3`",
		"sqlcmd -P,\nCANARY4",
		`["--password",` + "\n  " + `"CANARY5"]`,
		// Review round 2: a value with "=" or ":" on the next line, and a value after a blank line.
		"\"args\": [\n  \"--password\",\n  \"CANARY6==\"\n]",
		"\"args\": [\n  \"--token\",\n  \"CANARY7=def\"\n]",
		"\"args\": [\"sqlcmd\", \"-P\",\n  \"CANARY8=ss:w0rd\"]",
		"\"args\": [\n  \"--password\",\n  \"CANARY9:\"\n]",
		"args: [--client-secret,\n  CANARY10=]",
		"--password,\n,\nCANARY11",
		"sqlcmd -P,\n\nCANARY12",
		`["--password",` + "\n\n" + `"CANARY13"]`,
		// Review round 3: a quoted element that starts like a tag or a setting is still a value.
		"\"args\": [\"--password\",\n  \"secret:CANARY16\"]",
		"\"args\": [\"--password\",\n  \"<CANARY17\"]",
		// A key on the next line is a line of its own.
		"-pW,\n\npassword: CANARY14",
		"--password,\nDB_PASSWORD=CANARY15",
		`["--password",` + "\n" + `"password": "CANARY18"]`,
	} {
		if out := Mask(in, m); strings.Contains(out, "CANARY") {
			t.Errorf("leaked: %q -> %q", in, out)
		}
	}
}
