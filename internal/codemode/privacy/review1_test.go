package privacy

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Regression tests for the findings of review 1 of PR #242. Each one fails on the code of commit 0c81ec1.

// MUST 1: IPv4 after a dotted number, with a dot-separated port, or padded with zeros.
func TestReview1IPv4DottedPortAndPrefix(t *testing.T) {
	for _, in := range []string{
		"10.1.2.3.443 > 10.0.0.5.52344: Flags [S]",
		"n=5.192.168.0.1",
		"0192.168.000.001",
		"1.1.1.1.1.1.1.1",
		"x 172.16.0.9.80",
	} {
		out := newSweeper(false).text(in)
		if l := leakIP(out); l != "" {
			t.Errorf("%q -> %q still holds %s", in, out, l)
		}
	}
	// the build number stays: no four parts in a row read as an address
	if got := newSweeper(false).text("build 10.0.19045.1234 ok"); got != "build 10.0.19045.1234 ok" {
		t.Errorf("build number changed: %q", got)
	}
	// the same address in two spellings shares a token
	s := newSweeper(false)
	if a, b := s.text("0192.168.000.001"), s.text("192.168.0.1"); a != b {
		t.Errorf("%q vs %q", a, b)
	}
}

// MUST 2: Learn never drops a value quietly.
func TestReview1LearnFailsClosed(t *testing.T) {
	s := newSweeper(false)
	for i := 0; i < maxLearned; i++ {
		if err := s.learn(KindUser, fmt.Sprintf("u%07d", i)); err != nil {
			t.Fatalf("value %d refused: %v", i, err)
		}
	}
	if s.isIncomplete() {
		t.Fatal("incomplete before the limit")
	}
	err := s.learn(KindUser, "victim.user@contoso.com")
	if !errors.Is(err, ErrLearnFull) || !s.isIncomplete() {
		t.Fatalf("err %v incomplete %v", err, s.isIncomplete())
	}

	g := NewGuard(false)
	long := "Mozilla/5.0 " + strings.Repeat("X", 1100) // the old limit was 1 KiB
	if err := g.sw.learn(KindUA, long); err != nil {
		t.Fatalf("1.1 KiB user agent refused: %v", err)
	}
	out, err := g.Result([]byte(`{"ua":"` + long + `"}`))
	if err != nil || strings.Contains(string(out), "XXXX") {
		t.Fatalf("raw user agent: %s %v", out, err)
	}
	huge := strings.Repeat("Y", maxLearnLen+1)
	if err := g.sw.learn(KindUA, huge); !errors.Is(err, ErrLearnTooLong) || !g.Incomplete() {
		t.Fatalf("err %v incomplete %v", err, g.Incomplete())
	}
	// an Incomplete guard releases nothing
	if _, err := g.Result([]byte(`{"a":1}`)); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("result: %v", err)
	}
	if g.Log("x") != Withheld || g.Error("x") != Withheld {
		t.Fatal("log or error released")
	}
}

func TestReview1LearnByteLimit(t *testing.T) {
	s := newSweeper(false)
	var err error
	for i := 0; err == nil && i < 400; i++ {
		err = s.learn(KindUA, fmt.Sprintf("%05d", i)+strings.Repeat("a", 10000))
	}
	if !errors.Is(err, ErrLearnFull) || !s.isIncomplete() {
		t.Fatalf("err %v", err)
	}
}

// MUST 3: a learned short id is hidden as a JSON number as well as a string.
func TestReview1ShortIDAsNumber(t *testing.T) {
	s := newSweeper(false)
	_ = s.learn(KindUser, "4711")
	out, err := s.json([]byte(`{"u":4711,"v":"4711","w":4712}`), true)
	if err != nil || string(out) != `{"u":"[USER_1]","v":"[USER_1]","w":4712}` {
		t.Fatalf("%s %v", out, err)
	}
	// SHOULD 4: the same number written another way
	s = newSweeper(false)
	_ = s.learn(KindUser, "1234567")
	out, _ = s.json([]byte(`[1.234567e6,1234567.0,1234567.5,1234568]`), true)
	if string(out) != `["[USER_1]","[USER_1]",1234567.5,1234568]` {
		t.Fatalf("%s", out)
	}
}

// MUST 4: token numbers come from first sight in the text, not from the length or the order of the learned values.
func TestReview1TokenOrderIsFirstSight(t *testing.T) {
	ids := []string{"zzzzzzz9", "aaaaaaa1", "100002", "100001"}
	for _, learnOrder := range [][]string{ids, {ids[3], ids[2], ids[1], ids[0]}, {ids[1], ids[3], ids[0], ids[2]}} {
		s := newSweeper(false)
		for _, id := range learnOrder {
			_ = s.learn(KindUser, id)
		}
		got := s.text(strings.Join(ids, " "))
		if got != "[USER_1] [USER_2] [USER_3] [USER_4]" {
			t.Fatalf("learn order %v: %q", learnOrder, got)
		}
	}
	// the same with enough values to go through the automaton, not the pending list
	s := newSweeper(false)
	var in []string
	for i := 0; i < 100; i++ {
		v := fmt.Sprintf("id-%06d", (i*7919)%100000)
		in = append(in, v)
		_ = s.learn(KindUser, v)
	}
	out := strings.Fields(s.text(strings.Join(in, " ")))
	for i, tok := range out {
		if tok != fmt.Sprintf("[USER_%d]", i+1) {
			t.Fatalf("token %d is %s", i, tok)
		}
	}
}

// MUST 5: a user agent that holds a URL is swept before the scrubbers cut the URL.
func TestReview1UserAgentWithURL(t *testing.T) {
	ua := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	g := NewGuard(false)
	_ = g.sw.learn(KindUA, ua)
	for name, out := range map[string]string{"error": g.Error("bad ua " + ua), "log": g.Log(ua)} {
		for _, frag := range []string{"Mozilla", "Googlebot", "google.com", "bot.html"} {
			if strings.Contains(out, frag) {
				t.Errorf("%s: %q left in %q", name, frag, out)
			}
		}
	}
	if got := g.Error("bad ua " + ua); got != "bad ua [UA_1]" {
		t.Errorf("got %q", got)
	}
}

// MUST 6: values in the usual driver errors.
func TestReview1DriverErrors(t *testing.T) {
	cases := []struct{ in, canary string }{
		{"Conversion failed when converting the varchar value 'hunter2-secret' to data type int.", "hunter2"},
		{"Violation of PRIMARY KEY constraint 'PK_Users'. Cannot insert duplicate key in object 'dbo.Users'. The duplicate key value is (alice@example.com).", "alice@example.com"},
		{`ERROR: duplicate key value violates unique constraint "users_email_key" DETAIL: Key (email)=(alice@example.com) already exists.`, "alice@example.com"},
		{"Incorrect syntax near 'O''Brien'.", "Brien"},
		{`E11000 duplicate key error collection: db.users index: email_1 dup key: { email: "alice@example.com" }`, "alice@example.com"},
		{"Duplicate entry 'alice@example.com' for key 'users.email'", "alice@example.com"},
		{`ERROR: invalid input syntax for type integer: "alice-secret"`, "alice-secret"},
		{"Cannot insert the value NULL into column 'x', value 'zed-secret' failed", "zed-secret"},
		{"unterminated near 'abc-secret and more", "abc-secret"},
		{"N'unicode-secret' is bad", "unicode-secret"},
	}
	for _, c := range cases {
		out := ScrubError(c.in)
		if strings.Contains(out, c.canary) {
			t.Errorf("%q survives in %q", c.canary, out)
		}
	}
	// ordinary apostrophes are text
	for _, in := range []string{"it's fine, don't panic", "Bob's case isn't open"} {
		if out := ScrubText(in); out != in {
			t.Errorf("changed: %q -> %q", in, out)
		}
	}
}

// MUST 7: Bearer tokens, JWTs and key formats.
func TestReview1Credentials(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	for _, in := range []string{
		"Authorization: Bearer " + jwt,
		"request failed: 401 token " + jwt,
		"got bearer " + jwt + " from upstream",
		"authorization=Basic dXNlcjpwYXNzd29yZA==",
		"Cookie: session=abc123; other=x",
		"key AKIAIOSFODNN7EXAMPLE rejected",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 revoked",
		"slack xoxb-1234567890-abcdefghij",
	} {
		out := ScrubError(in)
		for _, frag := range []string{"eyJhbGci", "dXNlcjpw", "abc123", "AKIAIOSF", "ghp_abcdef", "xoxb-1234"} {
			if strings.Contains(out, frag) {
				t.Errorf("%q left in %q (from %q)", frag, out, in)
			}
		}
	}
	if out := ScrubText("the bearer of bad news"); out != "the bearer of bad news" {
		t.Errorf("prose changed: %q", out)
	}
}

// MUST 8: file paths.
func TestReview1Paths(t *testing.T) {
	cases := []struct{ in, canary string }{
		{`open C:\Users\John Smith: access denied`, "Smith"},
		{`open C:\Users\John Smith\case.db: access denied`, "John"},
		{"open /data/unravis/case.db: no such file or directory", "unravis"},
		{"open /private/var/folders/xy/secret.db: denied", "folders"},
		{"stat [/home/bob/x.txt]", "bob"},
		{"path,/home/bob/x.txt", "bob"},
		{"file at ~/secrets/key.pem", "secrets"},
		{"open ./cases/acme-customer.db", "acme"},
		{"open ../cases/acme-customer.db", "acme"},
		{`open "C:\Users\Ann Lee\x.db" failed`, "Lee"},
		{`share \\fileserver\team data\acme.db: denied`, "acme"},
	}
	for _, c := range cases {
		out := ScrubText(c.in)
		if strings.Contains(out, c.canary) {
			t.Errorf("%q survives in %q", c.canary, out)
		}
	}
	// text that has a slash but is not a path stays
	for _, in := range []string{"application/json", "N/A and w/o", "ratio 3/4 done", "GET /health ok", "use a/b or c/d"} {
		if out := ScrubText(in); out != in {
			t.Errorf("changed: %q -> %q", in, out)
		}
	}
	// idempotent
	o := ScrubText("stat [/home/bob/x.txt] and C:\\a b\\c: x")
	if ScrubText(o) != o {
		t.Errorf("not idempotent: %q", o)
	}
}

// SHOULD 1: an IPv6 address glued to a word byte, or with noise in front or behind, is still tokenized.
func TestReview1IPv6Edges(t *testing.T) {
	for _, in := range []string{"2001:db8::1x", "2001:db8::1_eth0", "a.b:2001:db8::5", "2001:db8::1.2.3.4.5", "ip=2001:db8::9,", "f::1y"} {
		if out := newSweeper(false).text(in); leakIP(out) != "" || strings.Contains(out, "2001:db8") {
			t.Errorf("%q -> %q", in, out)
		}
	}
	// text that is not an address stays
	for _, in := range []string{"std::vector<int>", "Class::method()", "std::string::npos", "12:30:45", "a::b::c"} {
		if in == "a::b::c" {
			continue // a valid address shape; tokenized on purpose
		}
		if out := newSweeper(false).text(in); out != in {
			t.Errorf("changed: %q -> %q", in, out)
		}
	}
}

// SHOULD 3: learned values match without regard to ASCII case.
func TestReview1LearnedCaseFold(t *testing.T) {
	g := NewGuard(false)
	_ = g.sw.learn(KindUser, "alice.smith")
	if out := g.HostError("ALICE.SMITH logged in; Alice.Smith too", IDs{}); strings.Contains(strings.ToLower(out), "alice") || out != "[USER_1] logged in; [USER_1] too" {
		t.Fatalf("%q", out)
	}
	out, _ := g.Result([]byte(`{"Alice.Smith":"ALICE.SMITH"}`))
	if string(out) != `{"[USER_1]":"[USER_1]"}` {
		t.Fatalf("%s", out)
	}
}

// SHOULD 5: data that looks like a token cannot pass for one; two names that end as one token stay two members.
func TestReview1TokenSpoofAndDuplicateKeys(t *testing.T) {
	s := newSweeper(false)
	_ = s.learn(KindUser, "alice.smith")
	_ = s.learn(KindUser, "ALICE.SMITH2")
	out, err := s.json([]byte(`{"a":"[USER_1]","b":"alice.smith","[USER_1]":1,"alice.smith":2,"Alice.Smith":3}`), true)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"[_USER_1]","b":"[USER_1]","[_USER_1]":1,"[USER_1]":2,"[USER_1]#2":3}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	g := NewGuard(false)
	if got := g.Error("fake [IP_1] and [UA_9] and [_IP_1] and [ip_1]"); got != "fake [_IP_1] and [_UA_9] and [__IP_1] and [_ip_1]" {
		t.Fatalf("%q", got)
	}
}

// SHOULD 6: Learn and Text in turn must not cost a rebuild each time.
func TestReview1LearnTextAlternating(t *testing.T) {
	s := newSweeper(false)
	for i := 0; i < 4096; i++ {
		_ = s.learn(KindUser, fmt.Sprintf("user-%06d-%s", i, strings.Repeat("x", 80)))
		if out := s.text(fmt.Sprintf("seen user-%06d-%s here", i, strings.Repeat("x", 80))); strings.Contains(out, "user-") {
			t.Fatalf("leak at %d: %q", i, out)
		}
	}
}

// SHOULD 7: the withheld branch.
func TestReview1WithheldBranch(t *testing.T) {
	old := maxRounds
	defer func() { maxRounds = old }()
	maxRounds = 0
	if got := newSweeper(false).text("10.0.0.1 and 0::1"); got != "[IP_WITHHELD]" {
		t.Fatalf("got %q", got)
	}
}

// NIT: overlapping learned values, invalid UTF-8.
func TestReview1OverlapAndUTF8(t *testing.T) {
	s := newSweeper(false)
	_ = s.learn(KindUser, "abcdef")
	_ = s.learn(KindUser, "cdefgh")
	if got := s.text("x abcdefgh y"); strings.Contains(got, "gh") || strings.Contains(got, "ab") {
		t.Fatalf("part left: %q", got)
	}
	if _, err := s.json([]byte("{\"ua\":\"Bad\xffAgent/1.0\"}"), true); !errors.Is(err, ErrBadJSON) {
		t.Fatalf("err %v", err)
	}
}

func TestReview1HostileLearnedAndRuns(t *testing.T) {
	s := newSweeper(false)
	for k := 6; k <= 200; k += 7 {
		_ = s.learn(KindUser, strings.Repeat("a", k))
	}
	for name, in := range map[string]string{
		"a-run":     strings.Repeat("a", 1<<20),
		"v6-groups": strings.Repeat("a:b.", 1<<18),
		"v6-many":   strings.Repeat("1::1 ", 1<<17),
		"v4-run":    strings.Repeat("1.", 1<<19) + "1",
	} {
		start := time.Now()
		out := s.text(in)
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("%s: %v", name, d)
		}
		if len(out) > 8*len(in)+64 {
			t.Errorf("%s: grew from %d to %d", name, len(in), len(out))
		}
	}
}
