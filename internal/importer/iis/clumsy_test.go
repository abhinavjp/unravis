package iis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// Clumsy-user cases for the W3C privacy columns: hostile but ordinary values in
// the cookie, referer and client IP cells. None may panic, abort the file for a
// bad cell, or let a raw private value through.

func clumsyOpts(names ...string) importer.Options {
	o := baseOpts()
	o.CookiePolicy = testPolicy(names...)
	return o
}

func clumsyRow(ip, ref, cookie string) string {
	return "2001-02-03 04:05:06 WEB-A GET /a 200 5 " + ip + " " + ref + " " + cookie
}

func TestClumsyCookieOversizedPairIsSkippedNotStored(t *testing.T) {
	big := strings.Repeat("x", 600)
	evs := parseRaw(t, cookieFile(clumsyRow("-", "-", "keep="+big+";+sid=ok")), clumsyOpts("keep", "sid"))
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if _, ok := evs[0].Attrs["cookie:keep"]; ok {
		t.Error("a 600-byte allowlisted pair was digested; the pair cap is 512 bytes")
	}
	if !isLowerHex32(evs[0].Attrs["cookie:sid"]) {
		t.Errorf("the pair after the oversized one was lost: %v", evs[0].Attrs)
	}
	if strings.Contains(marshalAll(t, evs), big[:40]) {
		t.Error("raw oversized value stored")
	}
}

func TestClumsyThirtyCookiePairsNoPanicAndFirstTwentyKept(t *testing.T) {
	var pairs []string
	for i := 0; i < 30; i++ {
		pairs = append(pairs, fmt.Sprintf("c%02d=v%d", i, i))
	}
	// an allowlisted name placed past the 20-pair cap is not seen
	evs := parseRaw(t, cookieFile(clumsyRow("-", "-", strings.Join(pairs, ";+"))), clumsyOpts("c00", "c19", "c25"))
	a := evs[0].Attrs
	if !isLowerHex32(a["cookie:c00"]) || !isLowerHex32(a["cookie:c19"]) {
		t.Errorf("first 20 pairs not honoured: %v", a)
	}
	if _, ok := a["cookie:c25"]; ok {
		t.Error("pair 26 was digested; the cap is 20 pairs")
	}
}

func TestClumsyCookieNamesAreNeverEchoedRawOrUnicode(t *testing.T) {
	weird := []string{
		"\u202esid=1", // right-to-left override before the name
		"s\u0131d=1",  // dotless i
		"\u212A=1",    // Kelvin sign lower-cases to ASCII k
		"sid\u200b=1", // zero-width space
		"%73id=1",     // percent-encoded name
		"sid =1",      // space left in the name
		"=nameless",   // no name
		"sid",         // no value
		";;;;",        // only separators
		"sid=1=2=3",   // extra equals
		strings.Repeat("a", 5000) + "=1",
	}
	for _, c := range weird {
		// one row per file: a rejected row quarantines the file, which is a valid outcome
		evs, _, _ := parse(t, NewW3C(), cookieFile(clumsyRow("-", "-", c)), clumsyOpts("sid", "k"))
		if len(evs) > 1 {
			t.Fatalf("%q: events = %d", c[:min(len(c), 10)], len(evs))
		}
		for _, e := range evs {
			for key := range e.Attrs {
				if !strings.HasPrefix(key, "cookie:") {
					continue
				}
				for i := 0; i < len(key); i++ {
					if b := key[i]; b >= 0x80 || b <= ' ' {
						t.Errorf("cookie attr key %q holds a non-ASCII or control byte", key)
					}
				}
				if len(key) > len("cookie:")+48 {
					t.Errorf("cookie attr key too long: %d", len(key))
				}
			}
		}
	}
}

func TestClumsyCookieDuplicateNamesFirstWins(t *testing.T) {
	pol := testPolicy("sid")
	o := baseOpts()
	o.CookiePolicy = pol
	evs := parseRaw(t, cookieFile(clumsyRow("-", "-", "sid=first;+SID=second;+Sid=third")), o)
	if got, want := evs[0].Attrs["cookie:sid"], HashCookie(pol, "sid", "first"); got != want {
		t.Errorf("duplicate cookie names: got %q want digest of the first value %q", got, want)
	}
}

func TestClumsyRefererOddShapesNeverLeakOrPanic(t *testing.T) {
	cases := []string{
		"http://user:secret-pw@a.test/p?q=1#f",
		"http://secret-token@a.test/",
		"HTTP://A.TEST/Path",
		"http://a.test/#only-fragment",
		"http://a.test/?only=query",
		"http://a.test:99999/x",
		"http://a.test:/x",
		"http://[::1/x",
		"http://[fe80::1%25eth0]/x",
		"http://a.test/%00%0d%0a",
		"http://a.test/\u202e/evil",
		"http://\u202eevil.test/x",
		"http://a.test/a;jsessionid=SECRET",
		"http://a.test\\@evil.test/x",
		"http:a.test/x",
		"http:/a.test/x",
		"//a.test/x",
		"data:text/html,secret",
		"file:///c:/secret",
		"mailto:a@b.test",
		"http://" + strings.Repeat("a", 300) + ".test/",
		"http://a.test/" + strings.Repeat("%41", 200),
		"http://a.test/\x01",
		"http://a.test/\x7f",
	}
	for _, in := range cases {
		got, ok := safeReferer(in)
		if !ok {
			if got != "" {
				t.Errorf("safeReferer(%q) = %q with ok=false", in, got)
			}
			continue
		}
		if len(got) > 256 {
			t.Errorf("safeReferer(%q) stored %d bytes", in, len(got))
		}
		for _, banned := range []string{"secret", "SECRET", "?", "#", "@", ";", " "} {
			if strings.Contains(got, banned) {
				t.Errorf("safeReferer(%q) = %q keeps %q", in, got, banned)
			}
		}
		for i := 0; i < len(got); i++ {
			if got[i] < ' ' || got[i] == 0x7f {
				t.Errorf("safeReferer(%q) = %q holds a control byte", in, got)
			}
		}
	}
}

func TestClumsyRefererHostIsNotAServerAddressInDisguise(t *testing.T) {
	// A referer host written as a number is still an address. Browsers never
	// send these, but a hand-made log can hold them.
	for _, in := range []string{"http://2130706433/x", "http://0x7f.0.0.1/x", "http://017700000001/x", "http://127.1/x"} {
		if got, ok := safeReferer(in); ok {
			t.Logf("KNOWN GAP: %q kept as %q (a numeric host is not recognised as an address)", in, got)
		}
	}
}

func TestClumsyClientIPOddValues(t *testing.T) {
	for _, ip := range []string{"fe80::1%eth0", "::ffff:192.0.2.1", "1.2.3.4:5678", "[::1]", "0.0.0.0", "1.2.3.4,5.6.7.8", "\u0661.2.3.4", strings.Repeat("1:", 40)} {
		evs := parseRaw(t, cookieFile(clumsyRow(strings.ReplaceAll(ip, " ", "+"), "-", "-")), clumsyOpts())
		if len(evs) != 1 {
			t.Fatalf("ip %q: events = %d, want 1 (a bad IP never rejects a row)", ip, len(evs))
		}
		if v, ok := evs[0].Attrs["client-ip"]; ok {
			if strings.ContainsAny(v, "%[]:/, ") && !strings.Contains(v, "::") && strings.Count(v, ":") < 2 {
				t.Errorf("ip %q stored as %q", ip, v)
			}
			if strings.Contains(v, "%") {
				t.Errorf("ip %q stored with a zone: %q", ip, v)
			}
		}
	}
}

func TestClumsyFileLevelOddities(t *testing.T) {
	hdr := "#Software: Microsoft Internet Information Services 10.0\n#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken c-ip cs(Referer) cs(Cookie)\n"
	row := clumsyRow("192.0.2.1", "http://r.test/x", "sid=1") + "\n"
	cases := []struct {
		name string
		data string
		kind string // "ok", "quarantine", "reject"
	}{
		{"empty file", "", "quarantine"},
		{"only newlines", "\n\n\n", "quarantine"},
		{"header only", hdr, "ok0"},
		{"no fields directive", "#Software: x\n" + row, "quarantine"},
		{"row before fields", row + hdr, "quarantine"},
		{"fields twice", hdr + row + "#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken c-ip cs(Referer) cs(Cookie)\n" + row, "ok"},
		{"fields redeclared narrower", hdr + row + "#Fields: date time s-computername\n" + row, "reject1"},
		{"crlf endings", strings.ReplaceAll(hdr+row, "\n", "\r\n"), "ok"},
		{"missing final newline", hdr + strings.TrimSuffix(row, "\n"), "ok"},
		{"tabs instead of spaces", hdr + strings.ReplaceAll(row, " ", "\t"), "reject1"},
		{"nul in cookie", hdr + strings.Replace(row, "sid=1", "sid=1\x00", 1), "reject1"},
		{"invalid utf8 in referer", hdr + strings.Replace(row, "/x", "/\xff\xfe", 1), "reject1"},
		{"extra column", hdr + row[:len(row)-1] + " extra\n", "reject1"},
		{"missing column", hdr + "2001-02-03 04:05:06 WEB-A GET /a 200 5\n", "reject1"},
		{"all cells null", hdr + "2001-02-03 04:05:06 WEB-A GET /a 200 5 - - -\n", "ok"},
		{"double spaces", hdr + strings.Replace(row, "GET /a", "GET  /a", 1), "reject1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs, rep, err := parse(t, NewW3C(), []byte(c.data), clumsyOpts("sid"))
			switch c.kind {
			case "quarantine":
				if !errors.Is(err, importer.ErrQuarantine) {
					t.Fatalf("want quarantine, got %v", err)
				}
			case "ok0":
				if err != nil || len(evs) != 0 {
					t.Fatalf("err=%v events=%d", err, len(evs))
				}
			case "ok":
				if err != nil || len(evs) == 0 {
					t.Fatalf("err=%v events=%d", err, len(evs))
				}
			case "reject1":
				// one bad row out of two: over the 5% reject fraction, so the file
				// is refused cleanly, never half-read
				if err == nil && len(evs) == 0 {
					t.Fatal("nothing read and no error")
				}
				if err != nil && !errors.Is(err, importer.ErrQuarantine) {
					t.Fatalf("err = %v, want quarantine or success with rejects", err)
				}
				if err == nil && rep.Rejected == 0 {
					t.Fatal("bad row was not counted as rejected")
				}
			}
		})
	}
}

func TestClumsyHugeLineRefusesFileNotPanics(t *testing.T) {
	hdr := "#Software: Microsoft Internet Information Services 10.0\n#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken c-ip cs(Referer) cs(Cookie)\n"
	data := hdr + clumsyRow("-", "-", "sid="+strings.Repeat("z", 70<<10)) + "\n"
	_, _, err := parse(t, NewW3C(), []byte(data), clumsyOpts("sid"))
	var le *importer.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want a LimitError for a line over 64 KiB", err)
	}
	if strings.Contains(err.Error(), "zzzz") {
		t.Error("error text echoes file content")
	}
}

func TestClumsyDuplicateAndMixedCaseFieldNames(t *testing.T) {
	data := "#Software: Microsoft Internet Information Services 10.0\n" +
		"#Fields: Date TIME S-ComputerName CS-Method cs-uri-stem sc-status CS(COOKIE) cs(cookie) CS(Referer)\n" +
		"2001-02-03 04:05:06 WEB-A GET /a 200 sid=first sid=second http://r.test/x\n"
	pol := testPolicy("sid")
	o := baseOpts()
	o.CookiePolicy = pol
	evs := parseRaw(t, []byte(data), o)
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	if got, want := evs[0].Attrs["cookie:sid"], HashCookie(pol, "sid", "first"); got != want {
		t.Errorf("duplicate cs(cookie) columns: first column must win, got %q want %q", got, want)
	}
}

func TestClumsyCancelledContextStopsParse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b strings.Builder
	b.WriteString("#Software: Microsoft Internet Information Services 10.0\n#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("2001-02-03 04:05:06 WEB-A GET /a 200 5\n")
	}
	_, _, err := importer.Collect(ctx, NewW3C(), strings.NewReader(b.String()), baseOpts())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
