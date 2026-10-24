package log4net

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
)

func mustCompile(t *testing.T, p string) *header {
	t.Helper()
	h, why := compile(p)
	if why != "" {
		t.Fatalf("pattern %q refused: %s", p, why)
	}
	return h
}

func TestCompileFields(t *testing.T) {
	h := mustCompile(t, "%date [%thread] %-5level %logger{2} - %message%newline")
	sm := h.re.FindStringSubmatch("2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - hello there")
	if sm == nil {
		t.Fatal("header line did not match")
	}
	if sm[h.date] != "2001-10-25 14:02:30,950" || sm[h.level] != "INFO" || sm[h.logger] != "Synthetic.Api.A" || sm[h.message] != "hello there" {
		t.Errorf("groups %q", sm)
	}
	if h.rule != clock.RuleHostLocal || !h.local {
		t.Errorf("rule %q local %v", h.rule, h.local)
	}
}

func TestCompilePadding(t *testing.T) {
	h := mustCompile(t, "%date %5level %-10logger|%message")
	for _, line := range []string{
		"2001-10-25 14:02:30,950  INFO Synthetic.Api|m",
		"2001-10-25 14:02:30,950 ERROR A         |m",
	} {
		if !h.re.MatchString(line) {
			t.Errorf("no match: %q", line)
		}
	}
}

func TestCompileDroppedTokens(t *testing.T) {
	h := mustCompile(t, "%date %-5level %logger (%file:%line) %username - %message%newline")
	if !h.dropsFields {
		t.Error("dropped tokens not noted")
	}
	sm := h.re.FindStringSubmatch("2001-10-25 14:02:30,950 WARN  A (Foo.cs:12) EXAMPLE\\alice - text")
	if sm == nil || sm[h.message] != "text" {
		t.Errorf("match %q", sm)
	}
}

func TestCompileExceptionToken(t *testing.T) {
	h := mustCompile(t, "%date %level %logger - %message %exception%newline")
	if h.excGrp == 0 {
		t.Error("no exception group")
	}
	// after %newline, %exception adds no group of its own
	h2 := mustCompile(t, "%date %level %logger - %message%newline%exception")
	if h2.excGrp != 0 {
		t.Errorf("exception group %d after newline", h2.excGrp)
	}
}

func TestCompileCaseRules(t *testing.T) {
	mustCompile(t, "%DATE %LEVEL %Logger - %Message")
	mustCompile(t, "%d %p %c - %m%n")
	if _, why := compile("%D %p %c - %m"); why != whyToken {
		t.Errorf("%%D: %q", why)
	}
}

func TestCompileUTCDate(t *testing.T) {
	h := mustCompile(t, "%utcdate %level %logger - %message")
	if h.rule != clock.RuleUTCField || h.local {
		t.Errorf("rule %q local %v", h.rule, h.local)
	}
}

func TestCompileOffsetDate(t *testing.T) {
	h := mustCompile(t, "%date{yyyy-MM-ddTHH:mm:ss.fffzzz} %level %logger - %message")
	if h.rule != clock.RuleOffsetInField || h.local {
		t.Errorf("rule %q local %v", h.rule, h.local)
	}
	h2 := mustCompile(t, "%date{yyyy-MM-dd'T'HH:mm:ss.fffK} %level %logger - %message")
	if h2.rule != clock.RuleOffsetInField {
		t.Errorf("K rule %q", h2.rule)
	}
}

func TestCompileRefusals(t *testing.T) {
	cases := map[string]string{
		"":                                     whyLength,
		"%date":                                "", // date alone is fine
		"%timestamp %message":                  whyTimestamp,
		"%date %date %message":                 whyDuplicate,
		"%date %utcdate %message":              whyDuplicate,
		"%date %logger %logger":                whyDuplicate,
		"%date %exception %exception":          whyDuplicate,
		"%level %message":                      whyNoDate,
		"%date %9999level":                     whySyntax,
		"%date %.level":                        whySyntax,
		"%date %nope":                          whyToken,
		"%date{unclosed %message":              whySyntax,
		"%date{MMM dd yyyy HH:mm:ss} %message": whyDateFmt,
		"%date{yyyy-MM-dd} %message":           whyDateFmt,
		"%date{hh:mm:ss yyyy-MM-dd} %message":  whyDateFmt,
		"%date{yyyy yyyy-MM-dd HH:mm:ss}":      whyDateFmt,
		"%date{yyyy-MM-dd HH:mm:ss,ffffffff}":  whyDateFmt,
		"%date{yyyy-MM-dd HH:mm:ss zzz zzz}":   whyDateFmt,
		"%date{yyyy-MM-dd HH:mm:ss%}":          whyDateFmt,
		"%date %message%newline[x]":            whyNewline,
		"%date %message%newline%level":         whyNewline,
	}
	for p, want := range cases {
		_, why := compile(p)
		if why != want {
			t.Errorf("%q: why %q, want %q", p, why, want)
		}
	}
}

func TestCompileBounds(t *testing.T) {
	if _, why := compile("%date " + strings.Repeat("[%thread]", maxPatternPieces)); why == "" {
		t.Error("a pattern with too many pieces was accepted")
	}
	if _, why := compile("%date " + strings.Repeat("x", maxPatternBytes)); why != whyLength {
		t.Errorf("long pattern: %q", why)
	}
	if _, why := compile("%date \x00"); why != whyChars {
		t.Errorf("control char: %q", why)
	}
}

func TestDateFormatSelfTest(t *testing.T) {
	for _, f := range []string{"", "ISO8601", "iso8601", iso8601, offsetDate,
		"yyyy/MM/dd HH:mm:ss", "yyyy-MM-dd HH:mm:ss.fffffff", "[yyyy-MM-dd HH:mm:ss,f]"} {
		sp, ok := dateFormat(f)
		if !ok {
			t.Errorf("%q refused", f)
			continue
		}
		if _, err := time.Parse(sp.layout, time.Date(2001, 10, 25, 14, 2, 31, 0, time.UTC).Format(sp.layout)); err != nil {
			t.Errorf("%q layout %q does not round-trip: %v", f, sp.layout, err)
		}
	}
}

func TestDefaultHeaders(t *testing.T) {
	if len(defaultHeaders) != 6 {
		t.Fatalf("%d default headers, want 6", len(defaultHeaders))
	}
	lines := map[string]bool{
		"2001-10-25 14:02:30,950 [12] INFO  Synthetic.A - m":       true,
		"2001-10-25 14:02:30,950 [12] INFO  Synthetic.A [ctx] - m": true,
		"2001-10-25 14:02:30,950 INFO  Synthetic.A - m":            true,
		"2001-10-25T14:02:31.123+13:00 [17] WARN  Synthetic.A - m": true,
		"System.TimeoutException: boom":                            false,
		"   at A.B() in C:\\x.cs:line 4":                           false,
		"":                                                         false,
	}
	for line, want := range lines {
		got := false
		for _, h := range defaultHeaders {
			if h.re.MatchString(line) {
				got = true
			}
		}
		if got != want {
			t.Errorf("%q: matched %v, want %v", line, got, want)
		}
	}
}

func TestCompilePatternTextNotInWhy(t *testing.T) {
	_, why := compile("%date %SENTINEL-7f3a")
	if strings.Contains(why, "SENTINEL") {
		t.Errorf("why %q carries pattern text", why)
	}
}
