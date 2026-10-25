package nlog

import (
	"bytes"
	"context"
	"encoding/json"
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

func readFile(t testing.TB, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t testing.TB, name string) []byte { return readFile(t, "testdata", name) }

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

func parse(t testing.TB, data []byte, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return importer.Collect(ctx, NewText(), bytes.NewReader(data), opts)
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

func hasGap(rep importer.Report, g string) bool {
	for _, k := range rep.KnownGaps {
		if k == g {
			return true
		}
	}
	return false
}

func reasons(rep importer.Report) map[int64]importer.Reason {
	m := map[int64]importer.Reason{}
	for _, e := range rep.Errors {
		m[e.Line] = e.Reason
	}
	return m
}

func TestTextGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "good.log"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Importer != NameText {
		t.Errorf("importer %q", rep.Importer)
	}
	if rep.Rows != 7 || rep.Rejected != 0 { // 5 headers and 2 continuation lines
		t.Fatalf("rows %d rejected %d, want 7 and 0", rep.Rows, rep.Rejected)
	}
	if rep.Quality.TimezoneRule != clock.RuleHostLocal || rep.FieldRules["timestamp"] != clock.RuleHostLocal {
		t.Errorf("timezone rule %q", rep.Quality.TimezoneRule)
	}
	if rep.Quality.SourceVersion != "dotnet-4.8" {
		t.Errorf("source version %q", rep.Quality.SourceVersion)
	}
	// 14:02:30.9500 on web-a is NZDT (+13:00)
	if want := time.Date(2001, 10, 25, 1, 2, 30, 950_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 35, 500_000_000, time.UTC); !rep.Last.Equal(want) {
		t.Errorf("last %v, want %v", rep.Last, want)
	}
	var sawExc, sawRedis bool
	for _, e := range evs {
		if e.Host != "web-a" || e.TZRule != clock.RuleHostLocal || e.Source != NameText {
			t.Errorf("event host %q rule %q source %q", e.Host, e.TZRule, e.Source)
		}
		if e.Attrs["exception"] != "" {
			sawExc = true
		}
		if e.Attrs["family"] == applog.FamilyRedisTimeout && e.Attrs["timeout-ms"] == "5000" {
			sawRedis = true
		}
	}
	if !sawExc {
		t.Error("no event carries an exception type from the continuation lines")
	}
	if !sawRedis {
		t.Error("the Redis timeout line was not classified")
	}
	k := map[string]int{}
	for _, e := range evs {
		k[e.Kind]++
	}
	if k[applog.KindLogEntry] == 0 || k[applog.KindLogRate] == 0 {
		t.Errorf("kinds %v", k)
	}
	if !hasGap(rep, GapLocalTime) || hasGap(rep, GapPreamble) || hasGap(rep, GapUTCStated) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
}

func TestTextLevelsAreKeptAsWritten(t *testing.T) {
	data := []byte("2001-10-25 14:02:30.0000|Trace|A|x\n2001-10-25 14:02:31.0000|WARN|A|x\n" +
		"2001-10-25 14:02:32.0000|error|A|x\n2001-10-25 14:02:33.0000|Fatal|A|x\n")
	evs, rep, err := parse(t, data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 {
		t.Fatalf("rejected %d", rep.Rejected)
	}
	sev := map[string]string{}
	for _, e := range evs {
		if e.Kind == applog.KindLogEntry {
			sev[e.Attrs["level"]] = e.Severity
		}
	}
	want := map[string]string{"WARN": "warning", "error": "error", "Fatal": "critical"}
	for l, s := range want {
		if sev[l] != s {
			t.Errorf("level %s severity %q, want %q (all: %v)", l, sev[l], s, sev)
		}
	}
	if _, ok := sev["Trace"]; ok {
		t.Error("a Trace line from a plain logger became a log-entry")
	}
}

func TestTextGapsPassLintCausal(t *testing.T) {
	for _, g := range []string{GapLocalTime, GapUTCStated, GapPreamble, GapExcCap} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestTextMissingTZRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	evs, _, err := parse(t, fixture(t, "good.log"), o)
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
		t.Fatalf("err %v, want tz-unlabelled quarantine", err)
	}
	if !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Errorf("err %v is not ErrTimezoneUnlabelled", err)
	}
	if len(evs) != 0 {
		t.Errorf("%d events with a refused file", len(evs))
	}
}

func TestTextNoHostRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	_, _, err := parse(t, fixture(t, "good.log"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestTextLogTimeUTC(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil // the owner's statement replaces the host zone
	o.Supplied = map[string]string{SuppliedLogTime: "utc"}
	evs, rep, err := parse(t, fixture(t, "good.log"), o)
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
	for _, e := range evs {
		if e.TZRule != clock.RuleUTCField {
			t.Errorf("event rule %q", e.TZRule)
		}
	}
}

func TestTextBadFixtureRejectedNotLeaked(t *testing.T) {
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, fixture(t, "bad.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 6 || rep.Rejected != 4 {
		t.Fatalf("rows %d rejected %d, want 6 and 4", rep.Rows, rep.Rejected)
	}
	want := map[int64]importer.Reason{2: importer.ReasonBadTime, 3: importer.ReasonFieldCount,
		4: importer.ReasonFieldCount, 5: importer.ReasonFieldCount}
	got := reasons(rep)
	for line, r := range want {
		if got[line] != r {
			t.Errorf("line %d reason %q, want %q (all: %v)", line, got[line], r, got)
		}
	}
	assertNoSentinel(t, evs, rep, nil)
}

func TestTextRejectFractionRefuses(t *testing.T) {
	_, rep, err := parse(t, fixture(t, "bad.log"), baseOpts(t))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction {
		t.Fatalf("err %v, want reject-fraction", err)
	}
	if rep.Rejected != 4 {
		t.Errorf("rejected %d", rep.Rejected)
	}
	if strings.Contains(err.Error(), importertest.Sentinel) {
		t.Error("error repeats file text")
	}
}

func TestTextExceptionContinuation(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "exception.log"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 {
		t.Fatalf("rejected %d: a continuation line must never be rejected", rep.Rejected)
	}
	if rep.Rows != 13 { // 5 headers, 8 continuation lines (the preamble line is not a row)
		t.Errorf("rows %d, want 13", rep.Rows)
	}
	if !hasGap(rep, GapPreamble) {
		t.Errorf("gaps %v lack the preamble sentence", rep.KnownGaps)
	}
	byLine := map[int64]evidence.Event{}
	for _, e := range evs {
		if e.Kind == applog.KindLogEntry {
			byLine[e.Line] = e
		}
	}
	// header line numbers are physical: 2 start, 3 first error, 12 job failure (the WARN and ERROR below)
	first, ok := byLine[3]
	if !ok {
		t.Fatalf("no log-entry for line 3: %v", byLine)
	}
	if got := first.Attrs["exception"]; got != "System.InvalidOperationException,System.Threading.ThreadAbortException" {
		t.Errorf("exception %q", got)
	}
	var job *evidence.Event
	for _, e := range byLine {
		if strings.Contains(e.Attrs["exception"], "app-exception") {
			e := e
			job = &e
		}
	}
	if job == nil {
		t.Fatal("an exception outside the framework rule was not reduced to app-exception")
	}
	if got := job.Attrs["exception"]; got != "app-exception" {
		t.Errorf("exception %q", got)
	}
	assertNoSentinel(t, evs, rep, nil)
}

func TestTextPrivacy(t *testing.T) {
	data := fixture(t, "privacy.log")
	if !bytes.Contains(data, []byte(importertest.Sentinel)) {
		t.Fatal("privacy fixture holds no sentinel")
	}
	evs, rep, err := parse(t, data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	assertNoSentinel(t, evs, rep, []string{"alice", "192.0.2.10", "198.51.100.7", "example.test", "SENTINEL", "Synthetic", "CacheClient", "Password", "web-a\\share"})
	for _, e := range evs {
		for k := range e.Attrs {
			if !applog.AllowedAttrs()[k] {
				t.Errorf("attribute %q is outside the app-log set", k)
			}
		}
		if l := e.Attrs["logger"]; l != "" && !strings.HasPrefix(l, "logger-") {
			t.Errorf("application logger name stored as %q", l)
		}
	}
}

// assertNoSentinel fails when the sentinel, or any extra needle, appears in the
// JSON of an event or of the report.
func assertNoSentinel(t *testing.T, evs []evidence.Event, rep importer.Report, extra []string) {
	t.Helper()
	needles := append([]string{importertest.Sentinel}, extra...)
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				t.Errorf("event %s holds %q", e.Kind, n)
			}
		}
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range needles {
		if strings.Contains(string(b), n) {
			t.Errorf("report holds %q", n)
		}
	}
}

func TestTextDSTFold(t *testing.T) {
	// 2002-03-17 02:30 occurs twice in Auckland (NZDT ends at 03:00 local, third Sunday of March 2002).
	data := []byte("2002-03-17 02:30:00.0000|INFO|Synthetic.Api.A|one\n" +
		"2002-03-17 02:30:05.0000|INFO|Synthetic.Api.A|two\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, data, o)
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
	data := []byte("2001-10-07 02:30:00.0000|INFO|Synthetic.Api.A|one\n" +
		"2001-10-25 14:02:30.9500|INFO|Synthetic.Api.A|two\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonDSTNonexistent {
		t.Fatalf("rejected %d errors %v", rep.Rejected, rep.Errors)
	}
}

func TestTextTimeVariants(t *testing.T) {
	// one to seven fraction digits and none at all read; a time outside 1990-2100 is bad-time
	data := []byte("2001-10-25 14:02:30|INFO|A|x\n2001-10-25 14:02:31.5|INFO|A|x\n" +
		"2001-10-25 14:02:32.1234567|INFO|A|x\n1980-10-25 14:02:33.0000|INFO|A|x\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || reasons(rep)[4] != importer.ReasonBadTime {
		t.Fatalf("rejected %d errors %v", rep.Rejected, rep.Errors)
	}
}

func TestTextContinuationNeverRejected(t *testing.T) {
	data := []byte("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n" +
		"System.InvalidOperationException: x\n" +
		"\x01 odd line | with|pipes\n" +
		"   at A.B()\n")
	evs, rep, err := parse(t, data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Rejected != 0 || len(evs) == 0 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
}

func TestTextRejectedHeaderDoesNotFoldIntoPreviousEntry(t *testing.T) {
	data := []byte("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n" +
		"2001-10-25 14:02:31.0000|ERR\n" +
		"System.TimeoutException: belongs to the rejected line\n" +
		"2001-10-25 14:02:32.0000|INFO|Synthetic.Api.A|ok\n")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 {
		t.Fatalf("rejected %d", rep.Rejected)
	}
	for _, e := range evs {
		if e.Attrs["exception"] != "" {
			t.Errorf("exception %q folded into the entry above the rejected line", e.Attrs["exception"])
		}
	}
}

// Continuation lines are rows: a file of one header and a flood of continuation lines
// stops at MaxRows like any other.
func TestTextContinuationLinesCountAgainstMaxRows(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n")
	for i := 0; i < 200; i++ {
		b.WriteString("   at Synthetic.Api.A.Frame()\n")
	}
	o := baseOpts(t)
	o.Limits.MaxRows = 50
	_, _, err := parse(t, b.Bytes(), o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "rows" {
		t.Fatalf("err %v, want rows limit", err)
	}
}

// Continuation lines are in the denominator of the reject fraction: 1 rejected header in
// 40 lines is under the 5% cap; 1 in 10 is over it.
func TestTextContinuationLinesCountInRejectFraction(t *testing.T) {
	build := func(frames int) []byte {
		var b bytes.Buffer
		b.WriteString("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n")
		for i := 0; i < frames; i++ {
			b.WriteString("   at Synthetic.Api.A.Frame()\n")
		}
		b.WriteString("2001-13-45 14:02:30.9500|ERROR|Synthetic.Api.A|bad time\n")
		return b.Bytes()
	}
	if _, rep, err := parse(t, build(38), baseOpts(t)); err != nil || rep.Rejected != 1 {
		t.Fatalf("err %v rejected %d, want one rejected row and no refusal", err, rep.Rejected)
	}
	if _, _, err := parse(t, build(8), baseOpts(t)); err == nil {
		t.Fatal("1 rejected row in 10 should be refused")
	}
}

func TestTextEventLimit(t *testing.T) {
	var b bytes.Buffer
	for i := 0; i < 50; i++ {
		b.WriteString("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n")
	}
	o := baseOpts(t)
	o.Limits.MaxEvents = 10
	_, _, err := parse(t, b.Bytes(), o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "events" {
		t.Fatalf("err %v, want events limit", err)
	}
}

func TestTextLongContinuationCapped(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("2001-10-25 14:02:30.9500|ERROR|Synthetic.Api.A|boom\n")
	line := "   at Synthetic.Api.A.Frame()" + strings.Repeat(" ", 1000) + "\n"
	for b.Len() < 3*maxExcBytes {
		b.WriteString(line)
	}
	_, rep, err := parse(t, b.Bytes(), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if !hasGap(rep, GapExcCap) {
		t.Errorf("gaps %v lack the exception-cap sentence", rep.KnownGaps)
	}
}

func TestTextLineLimit(t *testing.T) {
	long := "2001-10-25 14:02:30.9500|INFO|Synthetic.Api.A|" + strings.Repeat("x", 300<<10) + "\n"
	_, _, err := parse(t, []byte(long+long), baseOpts(t))
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Fatalf("err %v, want line-bytes limit", err)
	}
}

func TestTextPreambleGap(t *testing.T) {
	data := append([]byte("log started\n"), fixture(t, "good.log")...)
	_, rep, err := parse(t, data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if !hasGap(rep, GapPreamble) {
		t.Errorf("gaps %v lack the preamble sentence", rep.KnownGaps)
	}
	if rep.Rows != 7 { // the preamble line is not a row
		t.Errorf("rows %d", rep.Rows)
	}
}

func TestTextNoHeaderRefused(t *testing.T) {
	for name, data := range map[string][]byte{
		"plain text":     []byte("hello\nworld\n"),
		"other layouts":  []byte("2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - x\n"),
		"space variant":  []byte("2001-10-25 14:02:30.9500 INFO Synthetic.Api.A x\n"),
		"truncated head": []byte("2001-10-25 14:02:30.9500|INF"),
	} {
		t.Run(name, func(t *testing.T) {
			evs, _, err := parse(t, data, baseOpts(t))
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
				t.Fatalf("err %v, want unsupported-format", err)
			}
			if len(evs) != 0 {
				t.Errorf("%d events", len(evs))
			}
		})
	}
}

func TestTextEmpty(t *testing.T) {
	_, _, err := parse(t, []byte{}, baseOpts(t))
	if !errors.Is(err, textio.ErrEmpty) {
		t.Fatalf("err %v, want ErrEmpty", err)
	}
}

func TestTextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewText().Parse(ctx, bytes.NewReader(fixture(t, "good.log")), baseOpts(t), func(evidence.Event) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}

func TestTextDetect(t *testing.T) {
	m := NewText()
	if s := m.Detect(fixture(t, "good.log"), "app.log"); s != 90 {
		t.Errorf("good.log scored %d", s)
	}
	if s := m.Detect(fixture(t, "exception.log"), "app.log"); s != 90 {
		t.Errorf("exception.log scored %d (a preamble must not hide the headers)", s)
	}
	if s := m.Detect([]byte("2001-10-25 14:02:30.9500|INFO|A|x\n"), "app.log"); s != 85 {
		t.Errorf("one header scored %d", s)
	}
	// the sibling text formats are not NLog
	for _, p := range [][]string{
		{"..", "log4net", "testdata", "good.log"}, {"..", "log4net", "testdata", "offset.log"},
		{"..", "serilog", "testdata", "text_good.log"}, {"..", "mel", "testdata", "console_basic.log"},
		{"..", "log4net", "testdata", "good.xml"},
	} {
		if s := m.Detect(readFile(t, p...), "x.log"); s != 0 {
			t.Errorf("%s scored %d as NLog", filepath.Join(p...), s)
		}
	}
	for name, data := range map[string][]byte{
		"csv":        []byte("a,b,c\n1,2,3\n"),
		"space":      []byte("2001-10-25 14:02:30.9500 INFO Synthetic.Api.A x\n"),
		"bad level":  []byte("2001-10-25 14:02:30.9500|NOTICE|A|x\n"),
		"cut header": []byte("2001-10-25 14:02:30.9500|INFO\n"),
		"empty":      nil,
	} {
		if s := m.Detect(data, "x.log"); s != 0 {
			t.Errorf("%s scored %d", name, s)
		}
	}
}

func TestTextAllowedAttrs(t *testing.T) {
	for _, a := range []string{"level", "logger", "exception", "family"} {
		if !NewText().AllowedAttrs()[a] {
			t.Errorf("attribute %s is not allowed", a)
		}
	}
}

func TestTextSuppliedKeys(t *testing.T) {
	k, ok := NewText().(importer.SuppliedKeyer)
	if !ok {
		t.Fatal("importer declares no supplied keys")
	}
	keys := k.SuppliedKeys()
	if len(keys) != 2 || keys[SuppliedLogTime].Kind != importer.SuppliedEnum || keys[applog.SuppliedRequestMinMs].Kind != importer.SuppliedNumber {
		t.Errorf("supplied keys %v", keys)
	}
}
