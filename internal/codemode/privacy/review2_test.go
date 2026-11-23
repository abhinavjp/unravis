package privacy

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Regression tests for the findings of review 2 of PR #242. Each one fails on commit 0a5abba.

// M1: an embedded v4 tail written with padding or touching a hex letter leaves no octet behind.
func TestReview2EmbeddedV4(t *testing.T) {
	cases := []struct {
		in   string
		octs [4]int
	}{
		{"::ffff:001.002.003.004", [4]int{1, 2, 3, 4}},
		{"::ffff:192.168.001.050", [4]int{192, 168, 1, 50}},
		{"64:ff9b::192.168.001.1", [4]int{192, 168, 1, 1}},
		{"2001:db8::10.0.0.07", [4]int{10, 0, 0, 7}},
		{"::ffff:10.20.30.040:8080", [4]int{10, 20, 30, 40}},
		{"x::150.151.152.153A", [4]int{150, 151, 152, 153}},
		{"::ffff:150.151.152.153abc", [4]int{150, 151, 152, 153}},
	}
	for _, c := range cases {
		out := newSweeper(false).text(c.in)
		if l := leakPartialV4(out, c.octs); l != "" {
			t.Errorf("%q -> %q still holds %s", c.in, out, l)
		}
		if l := leakIP(out); l != "" {
			t.Errorf("%q -> %q: %s", c.in, out, l)
		}
	}
	g := NewGuard(false)
	out, err := g.HostResult([]byte(`{"k":"::ffff:1.2.3.04"}`), IDs{})
	if err != nil || strings.Contains(string(out), "2.3") {
		t.Errorf("json: %s %v", out, err)
	}
}

// the planted address is written in several forms and the surrounding text touches it; any two octets left over fail
func FuzzSweepPartial(f *testing.F) {
	f.Add("x", "y", uint8(0), uint32(0xC8C9CACB))
	f.Add("", "A", uint8(3), uint32(0xC8C9CACB))
	f.Add("[", "]:443", uint8(5), uint32(1))
	f.Fuzz(func(t *testing.T, pre, post string, form uint8, v uint32) {
		o := [4]int{100 + int(v>>24)%150, 100 + int(v>>16&0xff)%150, 100 + int(v>>8&0xff)%150, 100 + int(v&0xff)%150}
		pad := func(n int) string { return fmt.Sprintf("%04d", n) }
		var a string
		switch form % 9 {
		case 0:
			a = fmt.Sprintf("%d.%d.%d.%d", o[0], o[1], o[2], o[3])
		case 1:
			a = fmt.Sprintf("%s.%s.%s.%s", pad(o[0]), pad(o[1]), pad(o[2]), pad(o[3]))
		case 2:
			a = fmt.Sprintf("::ffff:%d.%d.%d.%d", o[0], o[1], o[2], o[3])
		case 3:
			a = fmt.Sprintf("::ffff:%d.%d.%d.%s", o[0], o[1], o[2], pad(o[3]))
		case 4:
			a = fmt.Sprintf("64:ff9b::%s.%d.%d.%d", pad(o[0]), o[1], o[2], o[3])
		case 5:
			a = fmt.Sprintf("[::ffff:%d.%d.%d.%d]:443", o[0], o[1], o[2], o[3])
		case 6:
			a = fmt.Sprintf("%d.%d.%d.%d:%d", o[0], o[1], o[2], o[3], o[0])
		case 7:
			a = fmt.Sprintf("::%d.%d.%d.%d", o[0], o[1], o[2], o[3])
		case 8:
			a = fmt.Sprintf("2001:db8::%s.%s.%d.%d", pad(o[0]), pad(o[1]), o[2], o[3])
		}
		// text that glues digits or dots to the address changes its octets; the oracle cannot judge those (and invisible characters
		// in the text are removed, which can glue digits)
		if strings.ContainsFunc(pre+post, func(r rune) bool { return r >= 0x80 }) {
			return
		}
		if len(pre) > 0 && (isDigit(pre[len(pre)-1]) || pre[len(pre)-1] == '.') {
			return
		}
		if len(post) > 0 && (isDigit(post[0]) || post[0] == '.') {
			return
		}
		for _, n := range o {
			if strings.Contains(pre+post, fmt.Sprint(n)) {
				return
			}
		}
		in := pre + a + post
		out := newSweeper(false).text(in)
		if p := leakPartialV4(out, o); p != "" {
			t.Fatalf("partial leak %s: %q -> %q", p, in, out)
		}
		if l := leakIP(out); l != "" {
			t.Fatalf("leak %s: %q -> %q", l, in, out)
		}
	})
}

// M2: a real token and a data value that looks like one stay different, and the output side does not escape.
func TestReview2TokenInAndOut(t *testing.T) {
	g := NewGuard(false)
	host, err := g.HostResult([]byte(`{"ip":"1.2.3.4","note":"[IP_1]","n2":"[_IP_1]","n3":"[ip_1]"}`), IDs{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ip":"[IP_1]","note":"[_IP_1]","n2":"[__IP_1]","n3":"[_ip_1]"}`
	if string(host) != want {
		t.Fatalf("host: %s", host)
	}
	// what the script returns is not escaped again
	out, err := g.Output(host)
	if err != nil || string(out) != want {
		t.Fatalf("output: %s %v", out, err)
	}
	if g.Log(string(host)) != want || g.ScriptError("saw [IP_1] and [_IP_1]") != "saw [IP_1] and [_IP_1]" {
		t.Fatal("log or script error changed token text")
	}
	// a replay: a new Guard gets the stored swept answer as it is and makes the same output
	g2 := NewGuard(false)
	out2, err := g2.Output(host)
	if err != nil || string(out2) != string(out) {
		t.Fatalf("replay: %s %v", out2, err)
	}
	// injective: two raw texts never end as one
	seen := map[string]string{}
	for _, raw := range []string{"[IP_1]", "[_IP_1]", "[__IP_1]", "[ip_1]", "[USER_9]", "[ua_2]", "[x_1]", "[]", "[", "_IP_1"} {
		e := escapeTokens(raw)
		if o, dup := seen[e]; dup {
			t.Errorf("%q and %q both give %q", o, raw, e)
		}
		seen[e] = raw
	}
}

// M3: values in driver error details.
func TestReview2DriverDetails(t *testing.T) {
	cases := []struct{ in, canary string }{
		{`ERROR: null value in column "name" violates not-null constraint DETAIL: Failing row contains (42, alice@example.com, hunter2, null).`, "hunter2"},
		{`new row for relation "t" violates check constraint "c" DETAIL: Failing row contains (7, bob@corp.com).`, "bob@corp"},
		{`DETAIL: Failing row contains (7, a), b@x.com, secret99) end`, "secret99"},
		{`pq: duplicate key ... DETAIL: Key (lower(email::text))=(alice@example.com) already exists.`, "alice@"},
		{`DETAIL: Key (a, b)=(alice@x.com, (nested)) already exists.`, "alice@"},
		{`invalid input value for enum mood: "alice"`, "alice"},
		{`invalid input syntax for type uuid: "4711-alice"`, "alice"},
		{`Error 1062: Duplicate entry "alice@x.com" for key "users.email"`, "alice@x.com"},
		{`pq: password authentication failed for user "alice"`, "alice"},
		{`FATAL: role "alice" does not exist`, "alice"},
		{`mssql: Violation of PRIMARY KEY constraint. The duplicate key value is (alice@x.com).`, "alice@"},
		{`The duplicate key value is (a, (b), c@x.com).`, "c@x"},
	}
	for _, c := range cases {
		if out := ScrubError(c.in); strings.Contains(out, c.canary) {
			t.Errorf("%q survives in %q", c.canary, out)
		}
	}
}

// M4: connection logins, DSNs and headers written as JSON or Go maps.
func TestReview2ConnAndHeaders(t *testing.T) {
	cases := []struct{ in, canary string }{
		{`user=alice dbname=payroll host=db.internal`, "alice"},
		{`user=alice dbname=payroll host=db.internal`, "payroll"},
		{`connect: host=db.internal port=5432 user=alice dbname=payroll sslmode=disable`, "alice"},
		{`connect: host=db.internal port=5432 user=alice dbname=payroll sslmode=disable`, "db.internal"},
		{`sslkey=/etc/ssl/alice.key sslcert=/etc/ssl/alice.crt`, "alice"},
		{`{"Authorization":"Basic dXNlcjpwYXNzd29yZA==","Cookie":"sid=abcdef123456"}`, "dXNlcjpw"},
		{`{"Authorization":"Basic dXNlcjpwYXNzd29yZA==","Cookie":"sid=abcdef123456"}`, "abcdef123456"},
		{`headers: map[Authorization:[Basic dXNlcjpwYXNzd29yZA==] Cookie:[sid=abcdef123456]]`, "dXNlcjpw"},
		{`{"x-api-key":"k1234567890abcdef"}`, "k1234567890"},
		{`Bearer abcde`, "abcde"},
		{`Server=tcp:sql.corp,1433;Initial Catalog=hr;User ID=alice;Pwd=h`, "alice"},
	}
	for _, c := range cases {
		if out := ScrubError(c.in); strings.Contains(out, c.canary) {
			t.Errorf("%q survives in %q (from %q)", c.canary, out, c.in)
		}
	}
	// the text around a value stays when the DSN is ADO style or the message is prose
	if out := ScrubError("Server=a;Database=b;Trusted_Connection=yes timed out"); !strings.Contains(out, "Trusted_Connection=yes") {
		t.Errorf("over-redacted: %q", out)
	}
	// JSON stays JSON
	out := ScrubError(`{"Authorization":"Basic xyzxyzxyz","other":1}`)
	if !strings.Contains(out, `"other":1`) || !strings.Contains(out, `"Authorization":"[scrubbed]"`) {
		t.Errorf("JSON broken: %q", out)
	}
}

// M5: there is no output before the identifiers of the answer are learned, and none after a refused Learn.
func TestReview2LearnBeforeRelease(t *testing.T) {
	g := NewGuard(false)
	out, err := g.HostResult([]byte(`{"u":"alice@example.com","t":"hi alice@example.com"}`), IDs{Users: []string{"alice@example.com"}})
	if err != nil || strings.Contains(string(out), "alice") {
		t.Fatalf("%s %v", out, err)
	}
	// the same id in a later answer needs no second mention
	out, _ = g.HostResult([]byte(`{"x":"ALICE@example.com"}`), IDs{})
	if strings.Contains(string(out), "ALICE") {
		t.Fatalf("%s", out)
	}
	if e := g.HostError("failed for alice@example.com", IDs{}); strings.Contains(e, "alice") {
		t.Fatalf("%q", e)
	}
	// a refused identifier: no output from this answer, and none from any later call
	g2 := NewGuard(false)
	huge := strings.Repeat("A", maxLearnLen+1)
	if out, err := g2.HostResult([]byte(`{"ua":"`+huge+`"}`), IDs{UserAgents: []string{huge}}); err == nil || out != nil {
		t.Fatalf("released %d bytes, err %v", len(out), err)
	}
	if !g2.Incomplete() {
		t.Fatal("not incomplete")
	}
	if _, err := g2.Output([]byte(`{"a":1}`)); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("output: %v", err)
	}
	if _, err := g2.HostResult([]byte(`{"a":1}`), IDs{}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("host result: %v", err)
	}
	if g2.Log("x") != Withheld || g2.ScriptError("x") != Withheld || g2.HostError("x", IDs{}) != Withheld {
		t.Fatal("text released")
	}
}

// S1: IPv6 at word and hex edges, one address one token.
func TestReview2IPv6Edges(t *testing.T) {
	for _, in := range []string{"2001:db8::1abcdef", "2001:db8::1deadbeef", "02001:db8::1", "ip_dead:beef::cafe", "src:fe80::1 dst fe80::1"} {
		out := newSweeper(false).text(in)
		for _, frag := range []string{"2001", "db8", "beef", "cafe", "fe80"} {
			if strings.Contains(out, frag) {
				t.Errorf("%q: %q left in %q", in, frag, out)
			}
		}
	}
	s := newSweeper(false)
	if got := s.text("src:fe80::1 dst fe80::1"); got != "src:[IP_1] dst [IP_1]" {
		t.Errorf("one address, two tokens: %q", got)
	}
}

// S2: a text cut at the cap never leaves the front of an address.
func TestReview2CutKeepsNoAddressFront(t *testing.T) {
	g := NewGuard(false)
	for _, fill := range []int{6, 7, 8, 9, 10, 11, 12, 13} {
		in := strings.Repeat("a", maxScrubInput-fill) + " 10.20.30.40 tail"
		out := g.Log(in)
		if strings.Contains(out[len(out)-24:], "10.") || strings.Contains(out, "10.20") {
			t.Errorf("fill %d: %q", fill, out[len(out)-24:])
		}
		if len(out) > maxScrubInput+64 {
			t.Errorf("not cut: %d", len(out))
		}
	}
}

// S3: the cost of learning the largest allowed set, and of a hostile text, is bounded.
func TestReview2Costs(t *testing.T) {
	g := NewGuard(false)
	start := time.Now()
	var ids []string
	for i := 0; i < maxLearned/2; i++ {
		ids = append(ids, fmt.Sprintf("user-%06d-%s", i, strings.Repeat("x", 40)))
	}
	if _, err := g.HostResult([]byte(`"x"`), IDs{Users: ids}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("learning %d values took %v", len(ids), d)
	}
	for name, in := range map[string]string{
		"dots":   strings.Repeat("f:.", 1<<19),
		"groups": strings.Repeat("1:2:3:4:5:6:7.", 1<<17),
		"dbl":    strings.Repeat("1::2::3::", 1<<17),
	} {
		start := time.Now()
		out := newSweeper(false).text(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s: %v", name, d)
		}
		if len(out) > 4*len(in) {
			t.Errorf("%s: grew", name)
		}
	}
	// an error text is cut before any step runs on it
	big := strings.Repeat("x", 64<<20)
	start = time.Now()
	_ = g.HostError(big, IDs{})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("64 MiB error: %v", d)
	}
}

// S4: short learned values are replaced as whole words in text.
func TestReview2ShortValuesAsWords(t *testing.T) {
	g := NewGuard(false)
	ids := IDs{Users: []string{"4711", "bob"}}
	out, err := g.HostResult([]byte(`{"msg":"user 4711 failed","who":"bob logged in","x":"bobby 14711 bob."}`), ids)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"msg":"user [USER_1] failed","who":"[USER_2] logged in","x":"bobby 14711 [USER_2]."}` {
		t.Fatalf("%s", out)
	}
	if e := g.HostError("login failed for bob (id 4711)", IDs{}); e != "login failed for [USER_2] (id [USER_1])" {
		t.Fatalf("%q", e)
	}
}

// S5: a learned value that holds token text still matches, and a learned value does not match inside a token.
func TestReview2TokenTextAndLearned(t *testing.T) {
	g := NewGuard(false)
	ua := "Mozilla/5.0 [UA_1] evil"
	out, err := g.HostResult([]byte(`{"ua":"Mozilla/5.0 [UA_1] evil"}`), IDs{UserAgents: []string{ua}})
	if err != nil || strings.Contains(string(out), "Mozilla") {
		t.Fatalf("%s %v", out, err)
	}
	g = NewGuard(false)
	line := g.Log("x")
	_ = line
	_ = g.sw.learn(KindUser, "alice@example.com")
	_ = g.sw.learn(KindUser, "user_1")
	if got := g.Log("alice@example.com did it"); got != "[USER_1] did it" {
		t.Fatalf("%q", got)
	}
	s := newSweeper(false)
	_ = s.learn(KindUser, "alice@example.com")
	_ = s.learn(KindUser, "user_1")
	a := s.text("alice@example.com")
	if b := s.text(a); b != a {
		t.Fatalf("not idempotent: %q %q", a, b)
	}
}

// S7 and S8: any scheme keeps only host; invisible characters do not split an address.
func TestReview2URLsAndInvisible(t *testing.T) {
	for _, in := range []string{"smb://fileserver/share/alice", "s3://bucket/alice/key", "sftp://alice@host/home/alice", "ldap://alice@corp/dc=x"} {
		if out := ScrubText(in); strings.Contains(out, "alice") || strings.Contains(out, "dc=x") {
			t.Errorf("%q -> %q", in, out)
		}
	}
	if out := ScrubText("open smb://fileserver/x"); !strings.Contains(out, "fileserver") {
		t.Errorf("host dropped: %q", out)
	}
	zw, wj, bom := "\u200b", "\u2060", string(rune(0xFEFF))
	for _, in := range []string{"1.2" + zw + ".3.4", "1" + wj + ".2.3.4", "10.0.0." + bom + "1", "2001:db8:" + zw + ":1"} {
		if out := newSweeper(false).text(in); leakIP(out) != "" || strings.ContainsAny(out, zw+wj+bom) || strings.Contains(out, "1.2") {
			t.Errorf("%q -> %q", in, out)
		}
	}
}

// Self-check of the rules for case, adjacency and padding, and that scrubbing twice gives the same text.
func TestReview2RuleClasses(t *testing.T) {
	cases := []struct{ in, canary string }{
		{"AUTHORIZATION: BEARER abcdef123456", "abcdef123456"},
		{`{\"authorization\":\"Basic abcdefgh\"}`, "abcdefgh"},
		{`{"Cookie" : "sid=1234567890"}`, "1234567890"},
		{"Cookie:sid=123456", "123456"},
		{"x-api-key=abcdef12345", "abcdef12345"},
		{"DUPLICATE KEY VALUE IS (x@y.com)", "x@y.com"},
		{"FAILING ROW CONTAINS (1, a@b.c)", "a@b.c"},
		{"KEY (A)=(a@b.c)", "a@b.c"},
		{`INVALID INPUT VALUE FOR ENUM mood: "alice"`, "alice"},
		{"DUP KEY: {a:'x-secret'}", "x-secret"},
		{"HOST=db.corp;USER=bob", "bob"},
		{"user = alice;db=x", "alice"},
		{"UID=alice;", "alice"},
		{"url=HTTPS://alice:pw@HOST/path?x=1", "path"},
		{"SMB://srv/share/alice", "alice"},
		{"FILE:///C:/Users/alice/x", "alice"},
		{"c:/users/alice", "alice"},
		{`\\SRV\share\alice.db`, "alice"},
		{"/HOME/alice/x", "alice"},
		{"~Alice/x", "Alice"},
		{"N'abc-secret'", "abc-secret"},
		{"x'abc-secret'", "abc-secret"},
		{"AKIAIOSFODNN7EXAMPLE1", "AKIAIOSF"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig", "eyJhbGci"},
		{"Host=a  User=bob  x=1", "bob"},
	}
	for _, c := range cases {
		out := ScrubText(c.in)
		if strings.Contains(out, c.canary) {
			t.Errorf("%q survives in %q", c.canary, out)
		}
		if again := ScrubText(out); again != out {
			t.Errorf("not idempotent: %q -> %q -> %q", c.in, out, again)
		}
	}
}

// the address scanners: padding, case, zones and the bytes around an address
func TestReview2AddressClasses(t *testing.T) {
	for _, in := range []string{
		"ip=1.2.3.4x", "x1.2.3.4", "1.2.3.4,5.6.7.8", "(1.2.3.4)", "1.2.3.4/24", "1.2.3.4:80", "1.2.3.4_1.2.3.5", "0001.0002.0003.0004",
		"2001:0DB8:0000::0001", "FE80::1%ETH0", "[2001:DB8::1]:443", "2001:db8::1/64", "ADDR:2001:db8::1", "1:2:3:4:5:6:7:8",
		"::FFFF:1.2.3.4", "::ffff:0001.0002.0003.0004", "fe80::1%12", "a=2001:db8::1b",
	} {
		out := newSweeper(false).text(in)
		if l := leakIP(out); l != "" {
			t.Errorf("%q -> %q: %s", in, out, l)
		}
		if strings.ContainsAny(out, "2345678") && !strings.Contains(out, "[IP_") {
			t.Errorf("%q -> %q", in, out)
		}
	}
	// the same address in two spellings shares a token
	s := newSweeper(false)
	a, b := s.text("2001:0DB8:0:0:0:0:0:1"), s.text("2001:db8::1")
	if a != b {
		t.Errorf("%q vs %q", a, b)
	}
}
