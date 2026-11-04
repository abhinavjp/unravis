package log4net

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/importertest"
)

func blockFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, "good-blocks.txt"),
		Bad:        fixture(t, "bad-blocks.txt"),
		Options:    baseOpts(t),
		NoFreeText: true,
	}
}

func TestBlocksConformance(t *testing.T) {
	importertest.Run(t, NewBlocks(), blockFixtures(t))
}

func FuzzBlocksParse(f *testing.F) {
	importertest.FuzzParse(f, NewBlocks(), blockFixtures(f))
}

// block builds one labelled entry with CRLF-free lines; fields are label, value pairs.
func blockText(pairs ...string) string {
	var sb strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "" {
			sb.WriteString(pairs[i+1] + "\n")
			continue
		}
		sb.WriteString(pairs[i] + ": " + pairs[i+1] + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func TestBlocksGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, NewBlocks(), fixture(t, "good-blocks.txt"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d, want 4 and 0", rep.Rows, rep.Rejected)
	}
	// block 1 carries both times; its UTC Time is preferred.
	if rep.Quality.TimezoneRule != clock.RuleUTCField || rep.FieldRules["timestamp"] != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 30, 950_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 35, 500_000_000, time.UTC); !rep.Last.Equal(want) {
		t.Errorf("last %v, want %v", rep.Last, want)
	}
	rules := map[string]int{}
	var sawExc bool
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Errorf("host %q", e.Host)
		}
		rules[e.TZRule]++
		if e.Attrs["exception"] != "" {
			sawExc = true
		}
		for k, v := range e.Attrs {
			for _, leak := range []string{"192.0.2.", "app.example.test", "order-1", "order-2"} {
				if strings.Contains(v, leak) || strings.Contains(k, leak) {
					t.Errorf("attribute %s carries %q", k, leak)
				}
			}
		}
	}
	if !sawExc {
		t.Error("no event carries an exception type from the body")
	}
	if rules[clock.RuleHostLocal] == 0 || rules[clock.RuleUTCField] == 0 {
		t.Errorf("event rules %v: block 3 has only a Server Time and must be host-local", rules)
	}
	k := kinds(evs)
	if k[applog.KindLogEntry] < 3 { // the warning, the error and the fatal entry
		t.Errorf("kinds %v", k)
	}
	if !hasGap(rep, GapLocalTime) {
		t.Errorf("gaps %v lack the local-time sentence", rep.KnownGaps)
	}
	if hasGap(rep, GapPreamble) {
		t.Errorf("unexpected preamble gap")
	}
}

func TestBlocksGapsPassLintCausal(t *testing.T) {
	for _, g := range []string{GapLocalTime, GapUTCStated, GapPreamble, GapExcCap} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestBlocksUTCPreferredOverServer(t *testing.T) {
	// the two stamps disagree on purpose: the UTC one must win.
	data := blockText("Logger name", "Synthetic.Api.A", "Type", "ERROR",
		"Server Time", "2001-10-25 20:00:00", "UTC Time", "2001-10-25 01:02:03", "", "boom")
	evs, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 3, 0, time.UTC); !rep.First.Equal(want) || len(evs) == 0 {
		t.Fatalf("first %v events %d, want %v", rep.First, len(evs), want)
	}
	if hasGap(rep, GapLocalTime) {
		t.Error("local-time gap although the UTC Time was used")
	}
}

func TestBlocksFallsBackWhenUTCUnreadable(t *testing.T) {
	data := blockText("Logger name", "Synthetic.Api.A", "Type", "ERROR",
		"Server Time", "2001-10-25 14:02:30", "UTC Time", "not a time", "", "boom")
	_, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 30, 0, time.UTC); !rep.First.Equal(want) || rep.Rejected != 0 {
		t.Fatalf("first %v rejected %d", rep.First, rep.Rejected)
	}
}

func TestBlocksNoZoneRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	_, _, err := parse(t, NewBlocks(), fixture(t, "good-blocks.txt"), o) // block 3 has only a Server Time
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
		t.Fatalf("err %v, want tz-unlabelled quarantine", err)
	}
	// every block with a UTC Time needs no zone
	data := blockText("Logger name", "A", "Type", "WARN", "UTC Time", "2001-10-25 01:02:03", "", "x") +
		blockText("Logger name", "A", "Type", "WARN", "UTC Time", "2001-10-25 01:02:04", "", "y")
	if _, _, err := parse(t, NewBlocks(), []byte(data), o); err != nil {
		t.Fatalf("UTC-only file refused without a zone: %v", err)
	}
}

func TestBlocksNoHostRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	_, _, err := parse(t, NewBlocks(), fixture(t, "good-blocks.txt"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestBlocksLogTimeUTC(t *testing.T) {
	o := baseOpts(t)
	o.Host.TZ = nil
	o.Supplied = map[string]string{SuppliedLogTime: "utc"}
	data := blockText("Logger name", "A", "Type", "WARN", "Server Time", "2001-10-25 14:02:30", "", "x")
	_, rep, err := parse(t, NewBlocks(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	if want := time.Date(2001, 10, 25, 14, 2, 30, 0, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if !hasGap(rep, GapUTCStated) || hasGap(rep, GapLocalTime) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
}

func TestBlocksTimeShapes(t *testing.T) {
	utc := func(y, mo, d, h, mi, s, ns int) time.Time { return time.Date(y, time.Month(mo), d, h, mi, s, ns, time.UTC) }
	cases := []struct {
		name, label, val string
		order            string
		want             time.Time
		rule             string
	}{
		{"iso comma fraction", "UTC Time", "2001-10-25 01:02:03,456", "", utc(2001, 10, 25, 1, 2, 3, 456_000_000), clock.RuleUTCField},
		{"iso T and Z", "UTC Time", "2001-10-25T01:02:03.5Z", "", utc(2001, 10, 25, 1, 2, 3, 500_000_000), clock.RuleOffsetInField},
		{"offset", "Server Time", "2001-10-25 14:02:03+13:00", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleOffsetInField},
		{"offset no colon", "Server Time", "2001-10-25 14:02:03 +1300", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleOffsetInField},
		{"offset hours only", "Server Time", "2001-10-25T14:02:03+13", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleOffsetInField},
		{"year first slashes", "UTC Time", "2001/10/25 01:02:03", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleUTCField},
		{"month first", "UTC Time", "10/25/2001 1:02:03 AM", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleUTCField},
		{"month first pm lower", "UTC Time", "10/25/2001 1:02:03 pm", "", utc(2001, 10, 25, 13, 2, 3, 0), clock.RuleUTCField},
		{"day first by value", "UTC Time", "25/10/2001 01:02:03", "", utc(2001, 10, 25, 1, 2, 3, 0), clock.RuleUTCField},
		{"day first stated", "UTC Time", "05/10/2001 01:02:03", applog.DateOrderDMY, utc(2001, 10, 5, 1, 2, 3, 0), clock.RuleUTCField},
		{"month first stated", "UTC Time", "05/10/2001 01:02:03", applog.DateOrderMDY, utc(2001, 5, 10, 1, 2, 3, 0), clock.RuleUTCField},
		{"minutes only", "UTC Time", "2001-10-25 01:02", "", utc(2001, 10, 25, 1, 2, 0, 0), clock.RuleUTCField},
		{"local", "Server Time", "2001-10-25 14:02:03.250", "", utc(2001, 10, 25, 1, 2, 3, 250_000_000), clock.RuleHostLocal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := baseOpts(t)
			if c.order != "" {
				o.Supplied = map[string]string{applog.SuppliedDateOrder: c.order}
			}
			data := blockText("Logger name", "A", "Type", "ERROR", c.label, c.val, "", "x")
			_, rep, err := parse(t, NewBlocks(), []byte(data), o)
			if err != nil {
				t.Fatal(err)
			}
			if !rep.First.Equal(c.want) {
				t.Errorf("first %v, want %v", rep.First, c.want)
			}
			if rep.FieldRules["timestamp"] != c.rule {
				t.Errorf("rule %q, want %q", rep.FieldRules["timestamp"], c.rule)
			}
		})
	}
}

func TestBlocksUnreadableTimesRejected(t *testing.T) {
	for _, v := range []string{"", "yesterday", "2001-13-45 01:02:03", "1980-01-01 00:00:00", "2001-10-25", "99/99/2001 01:02:03"} {
		t.Run(v, func(t *testing.T) {
			o := baseOpts(t)
			o.Limits.MaxRejectFraction = 1
			data := blockText("Logger name", "A", "Type", "ERROR", "Server Time", v, "", "x") +
				blockText("Logger name", "A", "Type", "ERROR", "UTC Time", "2001-10-25 01:02:03", "", "y")
			_, rep, err := parse(t, NewBlocks(), []byte(data), o)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Rows != 2 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || rep.Errors[0].Line != 1 {
				t.Fatalf("rows %d rejected %d errors %v", rep.Rows, rep.Rejected, rep.Errors)
			}
		})
	}
}

func TestBlocksDSTFold(t *testing.T) {
	data := blockText("Logger name", "A", "Type", "INFO", "Server Time", "2002-03-17 02:30:00", "", "one") +
		blockText("Logger name", "A", "Type", "INFO", "Server Time", "2002-03-17 02:30:05", "", "two")
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, NewBlocks(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 2 || rep.Errors[0].Reason != importer.ReasonDSTAmbiguous {
		t.Fatalf("rejected %d errors %v, want 2 dst-ambiguous", rep.Rejected, rep.Errors)
	}
}

func TestBlocksStandardAliasesAndOrder(t *testing.T) {
	// aliases, mixed case, extra spaces, labels in another order and no blank line before the stack
	data := "  level : error\r\nCATEGORY: Synthetic.Api.B\r\ntimestamp: 2001-10-25 01:02:03\r\nutc: 2001-10-25 01:02:03\r\n" +
		"client ip: 192.0.2.9\r\nurl: https://x.example.test/a\r\nMessage: failed\r\nException: System.InvalidOperationException: bad\r\n   at A.B()\r\n"
	o := baseOpts(t)
	evs, rep, err := parse(t, NewBlocks(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 1 || len(evs) == 0 {
		t.Fatalf("rows %d events %d", rep.Rows, len(evs))
	}
	e := evs[0]
	if e.Attrs[applog.AttrLevel] != "error" || e.Attrs[applog.AttrException] != "System.InvalidOperationException" {
		t.Errorf("attrs %v", e.Attrs)
	}
}

func TestBlocksBlankLinesAreSoft(t *testing.T) {
	// blank lines inside the header and inside the stack must not cut the entry.
	data := "Logger name: A\n\nType: ERROR\n\nUTC Time: 2001-10-25 01:02:03\nSystem.InvalidOperationException: x\n\n   at A.B()\n\n   at A.C()\n"
	_, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 1 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d, want 1 and 0", rep.Rows, rep.Rejected)
	}
}

func TestBlocksLabelInsideBodyIsBodyText(t *testing.T) {
	data := "Logger name: A\nType: ERROR\nUTC Time: 2001-10-25 01:02:03\nSystem.InvalidOperationException: x\nType: not a new entry\nUser IP: 192.0.2.1\n   at A.B()\n"
	evs, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 1 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d", rep.Rows, rep.Rejected)
	}
	if len(evs) == 0 || evs[0].Attrs[applog.AttrLevel] != "ERROR" {
		t.Errorf("events %v", evs)
	}
}

func TestBlocksRepeatedLoggerStartsEntryWithoutBlankLine(t *testing.T) {
	data := "Logger name: A\nType: ERROR\nUTC Time: 2001-10-25 01:02:03\nboom\nLogger name: B\nType: WARN\nUTC Time: 2001-10-25 01:02:04\nhmm\n"
	_, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 {
		t.Fatalf("rows %d, want 2", rep.Rows)
	}
}

func TestBlocksPreambleGap(t *testing.T) {
	data := append([]byte("log started\n\n"), fixture(t, "good-blocks.txt")...)
	_, rep, err := parse(t, NewBlocks(), data, baseOpts(t))
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

func TestBlocksGarbledInput(t *testing.T) {
	// a block with no time, a block with a time but no other label, noise between,
	// a label with no value, and a truncated last block.
	data := "Logger name: A\nType: ERROR\nboom\n\n" +
		"?? garbage ??\n\n" +
		"UTC Time: 2001-10-25 01:02:03\n\n" +
		"Logger name:\nType:\nServer Time:\n\n" +
		"Logger name: C\nType: ERROR\nUTC Time: 2001-10-25 01:"
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	_, rep, err := parse(t, NewBlocks(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Rejected != 3 {
		t.Fatalf("rows %d rejected %d, want 4 and 3 (%v)", rep.Rows, rep.Rejected, rep.Errors)
	}
	if rep.First.IsZero() {
		t.Errorf("the one good block was lost")
	}
}

func TestBlocksRejectFractionRefusesMostlyBadFile(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(blockText("Logger name", "A", "Type", "ERROR", "Server Time", "nope", "", "x"))
	}
	sb.WriteString(blockText("Logger name", "A", "Type", "ERROR", "UTC Time", "2001-10-25 01:02:03", "", "x"))
	_, _, err := parse(t, NewBlocks(), []byte(sb.String()), baseOpts(t))
	if !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("err %v, want a quarantine for a mostly rejected file", err)
	}
}

func TestBlocksNoLabelsRefusedAndEmpty(t *testing.T) {
	_, _, err := parse(t, NewBlocks(), []byte("hello\nworld\n"), baseOpts(t))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("err %v, want unsupported-format", err)
	}
	if _, _, err := parse(t, NewBlocks(), nil, baseOpts(t)); err == nil {
		t.Fatal("an empty file was accepted")
	}
	if _, _, err := parse(t, NewBlocks(), []byte("\n\n \n"), baseOpts(t)); err == nil {
		t.Fatal("a blank file was accepted")
	}
}

func TestBlocksHugeLineRefusesFile(t *testing.T) {
	data := fixture(t, "good-blocks.txt")
	data = append(data, []byte("Logger name: A\nType: ERROR\nUTC Time: 2001-10-25 01:02:03\n"+strings.Repeat("x", 70<<10)+"\n")...)
	evs, _, err := parse(t, NewBlocks(), data, baseOpts(t))
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Fatalf("err %v, want a line-bytes limit", err)
	}
	_ = evs
}

func TestBlocksLongLineWithinLimitAndBoundedBody(t *testing.T) {
	long := strings.Repeat("y", 60<<10)
	var sb strings.Builder
	sb.WriteString("Logger name: Synthetic.Api.A\nType: ERROR\nUTC Time: 2001-10-25 01:02:03\n")
	sb.WriteString("System.InvalidOperationException: " + long + "\n")
	for i := 0; i < 400; i++ { // 400 x 60 KiB of stack: far past the 64 KiB cap
		sb.WriteString("   at " + long + "\n")
	}
	sb.WriteString("\n")
	evs, rep, err := parse(t, NewBlocks(), []byte(sb.String()), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || !hasGap(rep, GapExcCap) {
		t.Fatalf("events %d gaps %v", len(evs), rep.KnownGaps)
	}
	if evs[0].Attrs[applog.AttrException] != "System.InvalidOperationException" {
		t.Errorf("exception %q", evs[0].Attrs[applog.AttrException])
	}
}

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestBlocksEncodingsGiveTheSameEvents(t *testing.T) {
	text := string(fixture(t, "good-blocks.txt"))
	want, wrep, err := parse(t, NewBlocks(), []byte(text), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	variants := map[string][]byte{
		"utf-8 bom": append([]byte{0xEF, 0xBB, 0xBF}, text...),
		"utf-16le":  utf16le(strings.ReplaceAll(text, "\r\n", "\n"), true),
		"lf":        []byte(strings.ReplaceAll(text, "\r\n", "\n")),
		"crlf":      []byte(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")),
	}
	for name, data := range variants {
		t.Run(name, func(t *testing.T) {
			got, rep, err := parse(t, NewBlocks(), data, baseOpts(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) || rep.Rows != wrep.Rows || !rep.First.Equal(wrep.First) || !rep.Last.Equal(wrep.Last) {
				t.Fatalf("%d events, rows %d, %v-%v; want %d, %d, %v-%v", len(got), rep.Rows, rep.First, rep.Last,
					len(want), wrep.Rows, wrep.First, wrep.Last)
			}
		})
	}
}

func TestBlocksMixedEncodingLineIsOneRejectedRow(t *testing.T) {
	good := string(fixture(t, "good-blocks.txt"))
	// a Latin-1 byte and a NUL inside the body of the third block; the rest must still import.
	data := strings.Replace(good, "Unhandled error for order-2", "Unhandled error caf\xe9 for order-2", 1)
	data = strings.Replace(data, "Application_End", "Application\x00_End", 1)
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewBlocks(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	var enc int
	for _, e := range rep.Errors {
		if e.Reason == importer.ReasonEncoding {
			enc++
		}
	}
	if enc != 2 || rep.Rows != 6 || len(evs) == 0 {
		t.Fatalf("encoding rejects %d rows %d events %d", enc, rep.Rows, len(evs))
	}
}

func TestBlocksRollingFilesKeepTheirOwnOrder(t *testing.T) {
	// log.txt.2 is the oldest, log.txt the newest; each file is imported on its own
	// and the events of the three, merged by time, run in the order of the entries.
	mk := func(day int, n int) []byte {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(blockText("Logger name", "Synthetic.Api.A", "Type", "ERROR",
				"UTC Time", fmt.Sprintf("2001-10-%02d 01:%02d:00", day, i), "", fmt.Sprintf("boom %d", i)))
		}
		return []byte(sb.String())
	}
	files := []struct {
		name string
		data []byte
	}{{"log.txt.2", mk(23, 5)}, {"log.txt.1", mk(24, 5)}, {"log.txt", mk(25, 5)}}
	var all []evidence.Event
	entries := 0
	var prevLast time.Time
	for _, f := range files {
		o := baseOpts(t)
		o.FileName = f.name
		o.ImportNonce = "nonce-" + f.name
		evs, rep, err := parse(t, NewBlocks(), f.data, o)
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if !prevLast.IsZero() && !rep.First.After(prevLast) {
			t.Errorf("%s starts at %v, not after the older file's end %v", f.name, rep.First, prevLast)
		}
		prevLast = rep.Last
		for _, e := range evs {
			if e.Kind == applog.KindLogEntry {
				all = append(all, e)
				entries++
			}
		}
	}
	ids := map[string]bool{}
	for _, e := range all {
		if ids[e.EvidenceID] {
			t.Fatalf("duplicate evidence id %s across files", e.EvidenceID)
		}
		ids[e.EvidenceID] = true
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start.Before(all[j].Start) })
	if len(all) != 15 || !all[0].Start.Equal(time.Date(2001, 10, 23, 1, 0, 0, 0, time.UTC)) ||
		!all[14].Start.Equal(time.Date(2001, 10, 25, 1, 4, 0, 0, time.UTC)) {
		t.Fatalf("%d merged events, first %v", len(all), all[0].Start)
	}
}

func TestBlocksNothingLeaks(t *testing.T) {
	data := blockText("Logger name", "Synthetic.Api.A", "Type", "ERROR", "UTC Time", "2001-10-25 01:02:03",
		"Application URL", "https://leak-url.example.test/secret", "User IP", "203.0.113.77",
		"", "leak-message alice@example.test", "", "System.InvalidOperationException: leak-exc-text")
	evs, rep, err := parse(t, NewBlocks(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	blob := fmt.Sprintf("%+v %+v", evs, rep)
	for _, leak := range []string{"leak-url", "203.0.113.77", "leak-message", "alice@example.test", "leak-exc-text"} {
		if strings.Contains(blob, leak) {
			t.Errorf("events or report contain %q", leak)
		}
	}
}

func TestBlocksCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewBlocks().Parse(ctx, bytes.NewReader(fixture(t, "good-blocks.txt")), baseOpts(t), func(evidence.Event) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestBlocksDetect(t *testing.T) {
	imp := NewBlocks()
	if got := imp.Detect(fixture(t, "good-blocks.txt"), "log.txt"); got < 85 {
		t.Errorf("good fixture scores %d", got)
	}
	cases := []struct {
		name string
		head string
		min  int
		max  int
	}{
		{"two labels", "Type: ERROR\nServer Time: 2001-10-25 14:02:30\n", 60, 80},
		{"time only", "Server Time: 2001-10-25 14:02:30\n", 0, 0},
		{"no time label", "Logger name: A\nType: ERROR\nUser IP: 192.0.2.1\n", 0, 0},
		{"time label without a time", "Logger name: A\nType: ERROR\nServer Time: soon\n", 0, 0},
		{"xml", "<log4net-event Logger Type Server Time: 2001-10-25 14:02:30>", 0, 0},
		{"log4net default layout", "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - Type: x\n", 0, 0},
		{"prose", "Dear team,\nthe time: 10:30 works.\n", 0, 0},
		{"empty", "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := imp.Detect([]byte(c.head), "x.txt"); got < c.min || got > c.max {
				t.Errorf("score %d, want %d..%d", got, c.min, c.max)
			}
		})
	}
	// UTF-16 head
	if got := imp.Detect(utf16le(string(fixture(t, "good-blocks.txt")), true), "log.txt"); got < 85 {
		t.Errorf("utf-16 head scores %d", got)
	}
}

func TestBlocksDoesNotClaimDefaultLayout(t *testing.T) {
	if got := NewBlocks().Detect(fixture(t, "good.log"), "log.txt"); got != 0 {
		t.Errorf("blocks claims a log4net default-layout file: %d", got)
	}
	if got := NewText().Detect(fixture(t, "good-blocks.txt"), "log.txt"); got != 0 {
		t.Errorf("log4net-text claims a block file: %d", got)
	}
}

func TestBlocksProbeTime(t *testing.T) {
	p := NewBlocks().(importer.TimeProber)
	o := baseOpts(t)
	cases := []struct {
		name string
		line string
		opts importer.Options
		want string
	}{
		{"utc", "UTC Time: 2001-10-25 01:02:30.950", o, "2001-10-25T01:02:30.95Z"},
		{"server local", "Server Time: 2001-10-25 14:02:30.950", o, "2001-10-25T01:02:30.95Z"},
		{"server no tz", "Server Time: 2001-10-25 14:02:30", importer.Options{Host: &importer.HostHint{}}, ""},
		{"server offset no tz", "Server Time: 2001-10-25 14:02:30+13:00", importer.Options{}, "2001-10-25T01:02:30Z"},
		{"server stated utc", "Server Time: 2001-10-25 14:02:30", importer.Options{Supplied: map[string]string{SuppliedLogTime: "utc"}}, "2001-10-25T14:02:30Z"},
		{"bad", "UTC Time: soon", o, ""},
		{"other label", "Logger name: A", o, ""},
		{"body", "   at A.B()", o, ""},
		{"empty", "", o, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := p.ProbeTime([]byte(c.line), c.opts)
			if c.want == "" {
				if ok {
					t.Fatalf("probe read %v from a line with no usable time", got)
				}
				return
			}
			want, _ := time.Parse(time.RFC3339Nano, c.want)
			if !ok || !got.Equal(want) {
				t.Fatalf("got %v %v, want %v", got, ok, want)
			}
		})
	}
}

func TestBlocksSuppliedKeys(t *testing.T) {
	keys := NewBlocks().(interface {
		SuppliedKeys() map[string]importer.SuppliedSpec
	}).SuppliedKeys()
	for _, k := range []string{SuppliedLogTime, applog.SuppliedDateOrder, applog.SuppliedRequestMinMs} {
		if _, ok := keys[k]; !ok {
			t.Errorf("supplied key %q missing", k)
		}
	}
}

func TestBlocksRowLimitEmitsNothing(t *testing.T) {
	o := baseOpts(t)
	o.Limits.MaxRows = 2
	evs, _, err := parse(t, NewBlocks(), fixture(t, "good-blocks.txt"), o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "rows" {
		t.Fatalf("err %v, want a rows limit", err)
	}
	_ = evs
}

// ProbeTime must read an ambiguous slash date the way Parse does for the same
// supplied date order.
func TestBlocksProbeTimeHonoursSuppliedDateOrder(t *testing.T) {
	prober, ok := NewBlocks().(importer.TimeProber)
	if !ok {
		t.Fatal("blocks importer is not a TimeProber")
	}
	utc := func(mo, d int) time.Time { return time.Date(2001, time.Month(mo), d, 1, 2, 3, 0, time.UTC) }
	cases := []struct {
		name  string
		order string
		want  time.Time
	}{
		{"day first stated", applog.DateOrderDMY, utc(10, 5)},
		{"month first stated", applog.DateOrderMDY, utc(5, 10)},
		{"not stated reads month first", "", utc(5, 10)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := baseOpts(t)
			if c.order != "" {
				o.Supplied = map[string]string{applog.SuppliedDateOrder: c.order}
			}
			line := "UTC Time: 05/10/2001 01:02:03"
			got, ok := prober.ProbeTime([]byte(line), o)
			if !ok || !got.Equal(c.want) {
				t.Fatalf("probe = %v %v, want %v", got, ok, c.want)
			}
			_, rep, err := parse(t, NewBlocks(), []byte(blockText("Logger name", "A", "Type", "ERROR", "UTC Time", "05/10/2001 01:02:03", "", "x")), o)
			if err != nil {
				t.Fatal(err)
			}
			if !rep.First.Equal(got) {
				t.Fatalf("probe %v disagrees with parse %v", got, rep.First)
			}
		})
	}
}
