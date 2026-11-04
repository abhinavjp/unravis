package iis

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// testPolicy returns a policy with a 32-byte key (values 0..31).
func testPolicy(allowed ...string) *importer.CookiePolicy {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return &importer.CookiePolicy{Allowed: allowed, HMACKey: key}
}

func TestHashCookieKnownAnswer(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0x01
	}
	p := &importer.CookiePolicy{Allowed: []string{"sid"}, HMACKey: key}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("unravis-cookie-v1\x00sid\x00v"))
	want := hex.EncodeToString(m.Sum(nil))[:32]
	got := HashCookie(p, "SID", "v")
	if got != want || len(got) != 32 {
		t.Fatalf("HashCookie = %q, want %q", got, want)
	}
	if g := HashCookie(p, "other", "v"); g != "" {
		t.Fatalf("name not allowed gave %q", g)
	}
	if g := HashCookie(nil, "sid", "v"); g != "" {
		t.Fatalf("nil policy gave %q", g)
	}
	for _, n := range []int{31, 33} {
		bad := &importer.CookiePolicy{Allowed: []string{"sid"}, HMACKey: make([]byte, n)}
		if g := HashCookie(bad, "sid", "v"); g != "" {
			t.Fatalf("%d-byte key gave %q", n, g)
		}
	}
	other := &importer.CookiePolicy{Allowed: []string{"sid"}, HMACKey: make([]byte, 32)}
	if g := HashCookie(other, "sid", "v"); g == "" || g == want {
		t.Fatalf("different key gave %q (same as first: %v)", g, g == want)
	}
	if g := HashCookie(testPolicy("sid"), "sid", "v"); g == "" || g == want {
		t.Fatalf("testPolicy key gave %q", g)
	}
}

func TestW3CClientIPValidation(t *testing.T) {
	for _, s := range []string{"192.0.2.1", "2001:db8::1", "::1"} {
		if !validIPv4or6(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "1.2.3", "999.1.1.1", "1.2.3.4/32", "1.2.3.4 ", strings.Repeat("1", 65)} {
		if validIPv4or6(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestSafeReferer(t *testing.T) {
	ok := []struct{ in, want string }{
		{"http://a.test/x?q=1#f", "http://a.test/x"},
		{"https://a.test:8443/p/q", "https://a.test:8443/p/q"},
		{"https://user:pw@a.test/p", "https://a.test/p"},
		{"http://a.test", "http://a.test"},
		{"http://a.test/payroll;jsessionid=ABC123", "http://a.test/payroll"},
		{"http://a.test/a;x=1/b", "http://a.test/a"},
		{"https://example.com/x", "https://example.com/x"},
		{"https://a.b.example.co.uk/x", "https://a.b.example.co.uk/x"},
		{"http://localhost/x", "http://localhost/x"},
		{"http://localhost:8080/x", "http://localhost:8080/x"},
		{"https://1host.example.com/x", "https://1host.example.com/x"},
		{"https://xn--bcher-kva.example/x", "https://xn--bcher-kva.example/x"},
		{"https://0xample.com/x", "https://0xample.com/x"},
	}
	for _, c := range ok {
		got, valid := safeReferer(c.in)
		if !valid || got != c.want {
			t.Errorf("safeReferer(%q) = %q,%v want %q,true", c.in, got, valid, c.want)
		}
	}
	drop := []string{
		"ftp://a.test/x", "a.test/x", "http:///x", "javascript:alert(1)", "http://a.test/\xff",
		"http://a.test/" + strings.Repeat("a", 600),
		"http://a.test/" + strings.Repeat("a", 300),
		"http://10.0.0.5/payroll;jsessionid=ABC123", "https://192.0.2.1/x", "http://[2001:db8::1]:8443/x", "http://[::1]/",
		// non-canonical IPv4 forms that browsers resolve as addresses
		"http://2130706433/x", "http://0x7f.1/x", "http://0177.0.0.1/x", "http://127.1/x",
		"http://1.2.3/x", "http://0x7f000001/x", "http://0X7F.0.0.1/x", "http://127.0.0.1./x",
		"http://10.1/x", "http://a.test.0x1/x",
	}
	for _, in := range drop {
		if got, valid := safeReferer(in); valid || got != "" {
			t.Errorf("safeReferer(%.40q) = %q,%v want dropped", in, got, valid)
		}
	}
	// path of 300 'a' with a short prefix: cleaned result is over 256 bytes
	if _, valid := safeReferer("http://a.test/" + strings.Repeat("a", 300)); valid {
		t.Error("over-long cleaned referer must be dropped, not truncated")
	}
}

func TestSplitCookiePairs(t *testing.T) {
	got := splitCookiePairs("sid=a;+theme=b")
	if len(got) != 2 || got[0] != (cookiePair{"sid", "a"}) || got[1] != (cookiePair{"theme", "b"}) {
		t.Fatalf("pairs = %+v", got)
	}
	for _, in := range []string{";;", "=x", "novalue", ""} {
		if g := splitCookiePairs(in); len(g) != 0 {
			t.Errorf("splitCookiePairs(%q) = %+v, want none", in, g)
		}
	}
	if g := splitCookiePairs("SID=a"); len(g) != 1 || g[0].Name != "sid" {
		t.Errorf("name not lower-cased: %+v", g)
	}
	if g := splitCookiePairs("tok=a=b=="); len(g) != 1 || g[0].Value != "a=b==" {
		t.Errorf("second '=' lost: %+v", g)
	}
	var many []string
	for i := 0; i < 25; i++ {
		many = append(many, "k"+string(rune('a'+i))+"=v")
	}
	if g := splitCookiePairs(strings.Join(many, ";+")); len(g) != 20 {
		t.Errorf("25 pairs gave %d, want 20", len(g))
	}
	long := "big=" + strings.Repeat("x", 600)
	if g := splitCookiePairs(long + ";+ok=1"); len(g) != 1 || g[0].Name != "ok" {
		t.Errorf("oversized pair not skipped: %+v", g)
	}
}

func TestDeclareDuplicatePrivacyColumn(t *testing.T) {
	sp := &spec{keep: []string{"date", "c-ip", "cs(referer)"}}
	col, width, err := declare("#Fields: date c-ip x c-ip cs(referer) cs(referer)", sp,
		importer.Limits{MaxFields: 4096}, &runState{declared: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if col["c-ip"] != 1 || col["cs(referer)"] != 4 || width != 6 {
		t.Fatalf("col = %v width = %d", col, width)
	}
	sp2 := &spec{keep: []string{"date", "time"}}
	_, _, err = declare("#Fields: date time time", sp2, importer.Limits{MaxFields: 4096},
		&runState{declared: map[string]bool{}})
	wantQuarantine(t, err, importer.ReasonFieldCount, "time")
}
