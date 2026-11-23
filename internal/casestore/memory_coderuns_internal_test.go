package casestore

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The memory store runs the same verifier; these cases need rows the sqlite
// schema refuses (a second end record), so they are made by hand here.

func memRunCase(t *testing.T) (*memCase, CodeRunStore) {
	t.Helper()
	st := NewMemoryWith(DefaultCaps, time.Now)
	info, err := st.Create(context.Background(), "Mem audit")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := c.(CodeRunStore)
	for i := 0; i < 2; i++ {
		r, err := s.BeginRun(context.Background(), RunStart{Surface: "cli", Source: "return 1", KeepResults: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordCall(context.Background(), r.RunID, CallRecord{Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndRun(context.Background(), r.RunID, RunEnd{Status: RunOK}); err != nil {
			t.Fatal(err)
		}
	}
	return c.(*memCase), s
}

func checks(rep ChainReport) map[string]bool {
	m := map[string]bool{}
	for _, p := range rep.Problems {
		m[p.Check] = true
	}
	return m
}

func TestMemReplayedEndRecordIsFound(t *testing.T) {
	c, s := memRunCase(t)
	m := c.d.code
	// Paste the first run's old end record at the tail, correctly linked and hashed.
	old := m.chain[2] // the first run: begin, call, end
	tailN, tailHash := m.tail()
	m.append(NewChainRow(tailN+1, old.RunID, old.Phase, 0, old.RecordJSON, tailHash))
	rep, err := s.VerifyCodeChain(context.Background(), nil)
	if err != nil || rep.OK {
		t.Fatalf("verify = %+v %v", rep, err)
	}
	got := checks(rep)
	if !got[CheckDuplicateEnd] || !got[CheckEndMismatch] {
		t.Errorf("checks = %v", got)
	}
}

func TestMemEditedRunRowIsFound(t *testing.T) {
	c, s := memRunCase(t)
	for _, id := range c.d.code.order[:1] {
		c.d.code.runs[id].Reason = "edited"
	}
	rep, _ := s.VerifyCodeChain(context.Background(), nil)
	if rep.OK || !checks(rep)[CheckBeginMismatch] || rep.Problems[0].N != 1 {
		t.Fatalf("verify = %+v", rep)
	}
}

func TestMemDeletedMiddleRowIsFound(t *testing.T) {
	c, s := memRunCase(t)
	m := c.d.code
	m.chain = append(m.chain[:1:1], m.chain[2:]...) // the first run's call record
	rep, _ := s.VerifyCodeChain(context.Background(), nil)
	if rep.OK || !checks(rep)[CheckChainGap] {
		t.Fatalf("verify = %+v", rep)
	}
}

func TestNewRunIDIsUUIDv7(t *testing.T) {
	a, err := NewRunID(time.UnixMilli(1700000000000))
	if err != nil || !ValidRunID(a) || a[14] != '7' {
		t.Fatalf("%q %v", a, err)
	}
	b, _ := NewRunID(time.UnixMilli(1700000000001))
	if a[:13] >= b[:13] {
		t.Errorf("ids do not sort by time: %s %s", a, b)
	}
}

func TestReplaceAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"from 10.1.2.3 to 8.8.8.8":                   "from [ip] to [ip]",
		"peer 2001:db8::1 ok":                        "peer [ip] ok",
		"[fe80::1]:8080":                             "[[ip]]:8080",
		"::1 loopback":                               "[ip] loopback",
		"v1.2.3 at 12:30:45":                         "v1.2.3 at 12:30:45",
		"std::bad Foo::Bar":                          "std::bad Foo::Bar",
		"aa:bb:cc:dd:ee:ff":                          "aa:bb:cc:dd:ee:ff",
		"999.1.1.1 and 1.2.3.4.5":                    "999.1.1.1 and [ip].5",
		"IP 192.168.1.5.443 > 10.0.0.2.51234: Flags": "IP [ip].443 > [ip].51234: Flags",
		"10.1.2.3.8080":                              "[ip].8080",
		"host_10.0.0.1 srv10.1.2.3 10.0.0.1x":        "host_[ip] srv[ip] [ip]x",
		"see 10.0.0.1. then 10.0.0.2, 10.0.0.3":      "see [ip]. then [ip], [ip]",
		"1.2.3.4 5.6.7.8":                            "[ip] [ip]",
		"x_fe80::1 and [2001:db8::1]:443":            "x_[ip] and [[ip]]:443",
		"010.000.000.001":                            "[ip]",
		"1234.1.1.1 and 1.1.1.1234":                  "1234.1.1.1 and 1.1.1.1234",
		"2001:0db8:0000:0000:0000:0000:0000:0001":    "[ip]",
	} {
		if got, _ := ReplaceAddresses(in); got != want {
			t.Errorf("ReplaceAddresses(%q) = %q, want %q", in, got, want)
		}
	}
}

// N4: "::1" becomes "[ip]", which is longer, so a text cut at its cap could grow
// past it; the cut is made again after the replacement. An address that starts
// before the cap and ends after it is replaced whole, also when earlier
// addresses shrink the text and pull the tail of the input into the kept part.
func TestHideAndCutKeepsTheCapAndLeavesNoPartialAddress(t *testing.T) {
	partial := regexp.MustCompile(`\d\.\d*$`) // a cut address ends in digits and dots
	grown := hideAndCut(strings.Repeat("::1 ", 1000), 100, true)
	if len(grown) > 100 || strings.Contains(grown, "::") {
		t.Errorf("len %d: %q", len(grown), grown)
	}
	text := strings.Repeat("a", 94) + "192.168.10.77 more"
	got := hideAndCut(text, 100, true)
	if partial.MatchString(got) || len(got) > 100 {
		t.Errorf("a cut address left a partial: %q", got[80:])
	}
	if plain := hideAndCut(text, 100, false); !strings.HasSuffix(plain, "192.16") {
		t.Errorf("allow mode must only cut: %q", plain[80:])
	}
	// Six addresses shrink by 11 bytes each; the straddling address then sits
	// where the kept part ends, however far into the input it started.
	for _, n := range []int{1, 3, 6, 12, 30} {
		shift := strings.Repeat("100.100.100.100 ", n) + strings.Repeat("a", 150-n) + "192.168.123.234 tail"
		for pad := 0; pad < 40; pad++ {
			got := hideAndCut(strings.Repeat("b", pad)+shift, 100, true)
			if partial.MatchString(got) || len(got) > 100 {
				t.Fatalf("n=%d pad=%d: a cut address left a partial: %q", n, pad, got)
			}
		}
	}
}
