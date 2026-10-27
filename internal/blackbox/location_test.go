package blackbox

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLocationKinds(t *testing.T) {
	cases := []struct {
		in    string
		kind  LocationKind
		canon string
	}{
		{`D:\UnravisBlackbox`, LocFolder, `d:\unravisblackbox`},
		{`d:/UnravisBlackbox/`, LocFolder, `d:\unravisblackbox`},
		{`  D:\UnravisBlackbox\  `, LocFolder, `d:\unravisblackbox`},
		{`\\web-b\UnravisBlackbox`, LocShare, `\\web-b\unravisblackbox`},
		{`//web-b/UnravisBlackbox/`, LocShare, `\\web-b\unravisblackbox`},
		{`http://web-b:7788`, LocURL, `http://web-b:7788`},
		{`HTTP://WEB-B:7788/`, LocURL, `http://web-b:7788`},
		{`web-b:7788`, LocURL, `http://web-b:7788`},
		{`http://web-b`, LocURL, `http://web-b:7788`},
		{`https://10.0.0.5:9000`, LocURL, `https://10.0.0.5:9000`},
	}
	for _, c := range cases {
		l, err := ParseLocation(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if l.Kind != c.kind || l.Canonical() != c.canon {
			t.Errorf("%q: kind %s canon %q, want %s %q", c.in, l.Kind, l.Canonical(), c.kind, c.canon)
		}
	}
}

// The same Blackbox typed two ways must give one canonical key, so adding it twice
// is caught (clumsy-user case 1).
func TestSameBlackboxTypedTwoWaysHasOneCanonicalKey(t *testing.T) {
	pairs := [][2]string{
		{`\\WEB-B\UnravisBlackbox`, `//web-b/unravisblackbox/`},
		{`http://web-b:7788`, `WEB-B:7788`},
		{`http://web-b`, `http://web-b:7788/`},
		{`D:\Bb`, `d:/bb/`},
	}
	for _, p := range pairs {
		a, err1 := ParseLocation(p[0])
		b, err2 := ParseLocation(p[1])
		if err1 != nil || err2 != nil {
			t.Fatalf("%v %v", err1, err2)
		}
		if a.Canonical() != b.Canonical() {
			t.Errorf("%q and %q differ: %q vs %q", p[0], p[1], a.Canonical(), b.Canonical())
		}
	}
	a, _ := ParseLocation("http://web-b:7788")
	b, _ := ParseLocation("https://web-b:7788")
	if a.Canonical() == b.Canonical() {
		t.Error("http and https are different places")
	}
}

func TestParseLocationRefusals(t *testing.T) {
	cases := []struct {
		in  string
		err error
	}{
		{"", ErrLocationEmpty},
		{"   ", ErrLocationEmpty},
		{"http://user:pw@web-b:7788", ErrLocationKeyInURL},
		{"http://web-b:7788/?key=ubk-AAAA", ErrLocationKeyInURL},
		{"http://web-b:7788#ubk-AAAA", ErrLocationKeyInURL},
		{"user@web-b:7788", ErrLocationKeyInURL},
		{"ftp://web-b:7788", ErrLocationBadURL},
		{"http://web-b:7788/files/bb-20261027-10.ndjson", ErrLocationBadURL},
		{"http://web-b:0", ErrLocationBadPort},
		{"http://web-b:70000", ErrLocationBadPort},
		{`\\web-b`, ErrLocationBadShare},
		{`\\web-b\`, ErrLocationBadShare},
		{`\\web b\share`, ErrLocationBadShare},
		{`D:\bb\..\x`, ErrLocationUnsafePath},
		{`D:\b|b`, ErrLocationUnsafePath},
		{"relative\\folder", ErrLocationBadPath},
		{"just words", ErrLocationBadPath},
		{"D:\\a\x00b", ErrLocationUnsafePath},
	}
	for _, c := range cases {
		_, err := ParseLocation(c.in)
		if !errors.Is(err, c.err) {
			t.Errorf("%q: err %v, want %v", c.in, err, c.err)
		}
	}
}

func TestLocationErrorsNeverEchoWhatWasTyped(t *testing.T) {
	_, err := ParseLocation("http://u:ubk-SECRETKEY@web-b:7788")
	if err == nil || strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("err = %v", err)
	}
	_, err = ParseLocation("http://web-b:7788/?key=ubk-SECRETKEY")
	if err == nil || strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocationDisplayAndNeedsKey(t *testing.T) {
	u, _ := ParseLocation("web-b:7788")
	if u.Display() != "http://web-b:7788" || u.BaseURL() != "http://web-b:7788" || !u.NeedsKey() {
		t.Fatalf("%+v", u)
	}
	f, _ := ParseLocation(`D:\Bb`)
	if f.Display() != `D:\Bb` || f.NeedsKey() {
		t.Fatalf("%+v", f)
	}
	s, _ := ParseLocation(`\\web-b\Bb`)
	if s.Display() != `\\web-b\Bb` || s.NeedsKey() {
		t.Fatalf("%+v", s)
	}
}
