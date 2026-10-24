package log4net

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/importertest"
	"api-audit-explorer/internal/importer/textio"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func auckland(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	return loc
}

func baseOpts(t testing.TB) importer.Options {
	return importer.Options{
		CaseID:      "abcdefgh234567abcdefgh2345",
		ImportNonce: "nonce-1",
		FileName:    "app.log",
		Host:        &importer.HostHint{Label: "web-a", Role: "app", TZ: auckland(t), Runtime: "dotnet-4.8"},
		Pseudonymize: func(kind, v string) string {
			return kind + "-0001"
		},
	}
}

func parse(t testing.TB, imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return importer.Collect(ctx, imp, bytes.NewReader(data), opts)
}

func textFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, "good.log"),
		Bad:        fixture(t, "bad.log"),
		Options:    baseOpts(t),
		NoFreeText: true,
	}
}

func TestTextConformance(t *testing.T) {
	importertest.Run(t, NewText(), textFixtures(t))
}

func FuzzTextParse(f *testing.F) {
	importertest.FuzzParse(f, NewText(), textFixtures(f))
}

func kinds(evs []evidence.Event) map[string]int {
	m := map[string]int{}
	for _, e := range evs {
		m[e.Kind]++
	}
	return m
}

func TestTextGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, NewText(), fixture(t, "good.log"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d, want 4 and 0", rep.Rows, rep.Rejected)
	}
	if rep.Quality.TimezoneRule != clock.RuleHostLocal || rep.FieldRules["timestamp"] != clock.RuleHostLocal {
		t.Errorf("timezone rule %q", rep.Quality.TimezoneRule)
	}
	// the host time is 14:02:30.950 NZDT (+13:00)
	if want := time.Date(2001, 10, 25, 1, 2, 30, 950_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 35, 500_000_000, time.UTC); !rep.Last.Equal(want) {
		t.Errorf("last %v, want %v", rep.Last, want)
	}
	var sawExc bool
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Errorf("host %q", e.Host)
		}
		if e.TZRule != clock.RuleHostLocal {
			t.Errorf("event rule %q", e.TZRule)
		}
		if e.Attrs["exception"] != "" {
			sawExc = true
		}
	}
	if !sawExc {
		t.Error("no event carries an exception type from the continuation lines")
	}
	k := kinds(evs)
	if k[applog.KindLogEntry] == 0 {
		t.Errorf("no log-entry events: %v", k)
	}
	if !hasGap(rep, GapLocalTime) {
		t.Errorf("gaps %v lack the local-time sentence", rep.KnownGaps)
	}
	if hasGap(rep, GapPreamble) {
		t.Errorf("unexpected preamble gap")
	}
}

func hasGap(rep importer.Report, g string) bool {
	for _, k := range rep.KnownGaps {
		if k == g {
			return true
		}
	}
	return false
}

func TestTextGapsPassLintCausal(t *testing.T) {
	for _, g := range []string{GapLocalTime, GapUTCStated, GapPreamble, GapExcCap, GapXMLCut} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestTextOffsetVariant(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil // the offset is in the field: no host zone is needed
	evs, rep, err := parse(t, NewText(), fixture(t, "offset.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 31, 123_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if hasGap(rep, GapLocalTime) {
		t.Error("local-time gap on an offset file")
	}
	for _, e := range evs {
		if e.TZRule != clock.RuleOffsetInField {
			t.Errorf("event rule %q", e.TZRule)
		}
	}
}

func TestTextMissingTZRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	_, _, err := parse(t, NewText(), fixture(t, "good.log"), o)
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
		t.Fatalf("err %v, want tz-unlabelled quarantine", err)
	}
}

func TestTextNoHostRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	_, _, err := parse(t, NewText(), fixture(t, "good.log"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestTextLogTimeUTC(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	o.Supplied = map[string]string{SuppliedLogTime: "utc"}
	_, rep, err := parse(t, NewText(), fixture(t, "good.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	if want := time.Date(2001, 10, 25, 14, 2, 30, 950_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if !hasGap(rep, GapUTCStated) || hasGap(rep, GapLocalTime) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
}

func TestTextUTCDatePattern(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	o.Supplied = map[string]string{SuppliedPattern: "%utcdate [%thread] %-5level %logger - %message%newline"}
	_, rep, err := parse(t, NewText(), fixture(t, "good.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
}

func TestTextRefusedPatterns(t *testing.T) {
	cases := []struct{ name, pattern, why string }{
		{"timestamp", "%timestamp [%thread] %-5level %logger - %message%newline", whyTimestamp},
		{"r", "%r [%thread] %-5level %logger - %message%newline", whyTimestamp},
		{"message twice", "%date %-5level %logger - %message %message%newline", whyDuplicate},
		{"level twice", "%date %level %level - %message%newline", whyDuplicate},
		{"no date", "[%thread] %-5level %logger - %message%newline", whyNoDate},
		{"unknown token", "%date %bogus - %message%newline", whyToken},
		{"long", strings.Repeat("x", maxPatternBytes+1), whyLength},
		{"control char", "%date\t%message", whyChars},
		{"dangling percent", "%date %", whySyntax},
		{"date names", "%date{dddd MMMM yyyy HH:mm:ss} %message", whyDateFmt},
		{"literal after newline", "%date %message%newline-end", whyNewline},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := baseOpts(t)
			o.Supplied = map[string]string{SuppliedPattern: c.pattern}
			evs, _, err := parse(t, NewText(), fixture(t, "good.log"), o)
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
				t.Fatalf("err %v, want unsupported-format", err)
			}
			if qe.Column != c.why {
				t.Errorf("why %q, want %q", qe.Column, c.why)
			}
			if len(evs) != 0 {
				t.Errorf("%d events with a refused pattern", len(evs))
			}
			if strings.Contains(err.Error(), c.pattern) && len(c.pattern) > 6 {
				t.Errorf("error repeats the pattern text")
			}
		})
	}
}

func TestTextStatedPattern(t *testing.T) {
	data := []byte("2001-10-25 14:02:30,950|INFO|Synthetic.Api.A|first\n" +
		"2001-10-25 14:02:31,950|ERROR|Synthetic.Api.A|second\n" +
		"System.TimeoutException: timed out\n")
	o := baseOpts(t)
	o.Supplied = map[string]string{SuppliedPattern: "%date|%level|%logger|%message%newline"}
	evs, rep, err := parse(t, NewText(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 || len(evs) == 0 {
		t.Fatalf("rows %d events %d", rep.Rows, len(evs))
	}
}

func TestTextPreambleGap(t *testing.T) {
	data := append([]byte("log started\n"), fixture(t, "good.log")...)
	_, rep, err := parse(t, NewText(), data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if !hasGap(rep, GapPreamble) {
		t.Errorf("gaps %v lack the preamble sentence", rep.KnownGaps)
	}
	if rep.Rows != 4 {
		t.Errorf("rows %d", rep.Rows)
	}
}

func TestTextNoHeaderRefused(t *testing.T) {
	_, _, err := parse(t, NewText(), []byte("hello\nworld\n"), baseOpts(t))
	if err == nil {
		t.Fatal("a file with no header line was accepted")
	}
}

func TestTextEmpty(t *testing.T) {
	_, _, err := parse(t, NewText(), []byte{}, baseOpts(t))
	if err == nil {
		t.Fatal("an empty file was accepted")
	}
	_ = textio.ErrEmpty
}

func TestTextBadDateRejectedNotLeaked(t *testing.T) {
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewText(), fixture(t, "bad.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || rep.Errors[0].Line != 2 {
		t.Fatalf("rejected %d errors %v", rep.Rejected, rep.Errors)
	}
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, importertest.Sentinel) || strings.Contains(k, importertest.Sentinel) {
				t.Errorf("sentinel in attribute %s", k)
			}
		}
	}
}

func TestTextDSTFold(t *testing.T) {
	// 2002-03-17 02:30 occurs twice in Auckland (NZDT ends at 03:00 local, third Sunday of March 2002).
	data := []byte("2002-03-17 02:30:00,000 [1] INFO  Synthetic.Api.A - one\n" +
		"2002-03-17 02:30:05,000 [1] INFO  Synthetic.Api.A - two\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, NewText(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 2 {
		t.Fatalf("rejected %d, want 2 (dst-ambiguous)", rep.Rejected)
	}
	for _, e := range rep.Errors {
		if e.Reason != importer.ReasonDSTAmbiguous {
			t.Errorf("reason %q", e.Reason)
		}
	}
}

func TestTextDSTGap(t *testing.T) {
	// 2001-10-07 02:30 does not exist in Auckland (NZST to NZDT at 02:00).
	data := []byte("2001-10-07 02:30:00,000 [1] INFO  Synthetic.Api.A - one\n" +
		"2001-10-25 14:02:30,950 [1] INFO  Synthetic.Api.A - two\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, NewText(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonDSTNonexistent {
		t.Fatalf("rejected %d errors %v", rep.Rejected, rep.Errors)
	}
}

func TestTextContinuationNeverRejected(t *testing.T) {
	data := []byte("2001-10-25 14:02:30,950 [1] ERROR Synthetic.Api.A - boom\n" +
		"System.InvalidOperationException: x\n" +
		"\x01 odd line\n" +
		"   at A.B()\n")
	evs, rep, err := parse(t, NewText(), data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 1 || rep.Rejected != 0 || len(evs) == 0 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
}

func TestTextLongContinuationCapped(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("2001-10-25 14:02:30,950 [1] ERROR Synthetic.Api.A - boom\n")
	line := "   at Synthetic.Api.A.Frame()" + strings.Repeat(" ", 1000) + "\n"
	for b.Len() < 3*maxExcBytes {
		b.WriteString(line)
	}
	_, rep, err := parse(t, NewText(), b.Bytes(), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if !hasGap(rep, GapExcCap) {
		t.Errorf("gaps %v lack the exception-cap sentence", rep.KnownGaps)
	}
}

func TestTextLineLimit(t *testing.T) {
	long := "2001-10-25 14:02:30,950 [1] INFO  Synthetic.Api.A - " + strings.Repeat("x", 300<<10) + "\n"
	o := baseOpts(t)
	_, _, err := parse(t, NewText(), []byte(long+long), o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Fatalf("err %v, want line-bytes limit", err)
	}
}

func TestTextDetect(t *testing.T) {
	m := NewText()
	if s := m.Detect(fixture(t, "good.log"), "app.log"); s < 50 {
		t.Errorf("good.log scored %d", s)
	}
	if s := m.Detect(fixture(t, "offset.log"), "app.log"); s < 50 {
		t.Errorf("offset.log scored %d", s)
	}
	for _, name := range []string{"good.xml", "native.xml"} {
		if s := m.Detect(fixture(t, name), "app.log"); s != 0 {
			t.Errorf("%s scored %d as text", name, s)
		}
	}
	if s := m.Detect([]byte("a,b,c\n1,2,3\n"), "x.csv"); s != 0 {
		t.Errorf("csv scored %d", s)
	}
	if s := m.Detect(nil, ""); s != 0 {
		t.Errorf("empty scored %d", s)
	}
}

func TestTextAllowedAttrs(t *testing.T) {
	for _, a := range []string{"level", "logger", "exception", "family"} {
		if !NewText().AllowedAttrs()[a] {
			t.Errorf("attribute %s is not allowed", a)
		}
	}
}

func TestTextLoggerLabels(t *testing.T) {
	evs, _, err := parse(t, NewText(), fixture(t, "good.log"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if l := e.Attrs["logger"]; strings.Contains(l, "Synthetic") {
			t.Errorf("application logger name stored as %q", l)
		}
	}
}
