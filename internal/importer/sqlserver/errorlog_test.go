package sqlserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func errorlogBad(t testing.TB) [][]byte {
	return [][]byte{readFixture(t, "errorlog_bad.log"), readFixture(t, "errorlog_bad_tz.log")}
}

func toUTF16LE(b []byte, bom bool) []byte {
	var out []byte
	if bom {
		out = []byte{0xff, 0xfe}
	}
	for _, r := range utf16.Encode([]rune(string(b))) {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestErrorlogConformance(t *testing.T) {
	importertest.Run(t, NewErrorlog(), importertest.Fixtures{
		Good:    readFixture(t, "errorlog_good.log"),
		Bad:     errorlogBad(t),
		Options: importer.Options{Host: sqlA(t), FileName: "ERRORLOG"},
	})
}

func TestErrorlogGoodEvents(t *testing.T) {
	evs, rep, err := collect(t, NewErrorlog(), readFixture(t, "errorlog_good.log"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Source != ErrorlogName || e.Host != "sql-a" || e.Start.Location() != time.UTC {
			t.Errorf("provenance: %+v", e)
		}
	}
	want := []string{KindStart, KindError, KindLoginFailed, KindError, KindIOStall, KindNonYielding, KindStop}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if rep.Rejected != 0 || rep.Quality.SourceVersion != "SQL Server 2019" {
		t.Errorf("report: rejected %d, quality %+v", rep.Rejected, rep.Quality)
	}
	// 14:00:35 NZST (UTC+12, June) is 02:00:35Z.
	io := evs[4]
	if !io.Start.Equal(time.Date(2001, 6, 15, 2, 0, 35, 0, time.UTC)) || *io.Value != 3 || io.Unit != "count" {
		t.Errorf("io-stall = %+v", io)
	}
	if e := evs[1]; e.Attrs["error_number"] != "18456" || e.Attrs["sql_severity"] != "14" || e.Severity != "warning" {
		t.Errorf("error 18456 = %+v", e)
	}
	if e := evs[3]; e.Attrs["error_number"] != "9002" || e.Severity != "error" {
		t.Errorf("error 9002 = %+v", e)
	}
	if l := evs[2]; len(l.Attrs) != 0 || *l.Value != 1 {
		t.Errorf("login-failed keeps attributes: %+v", l)
	}
	for _, g := range []string{gapErrlogLocal, gapErrlog833, gapErrlogLogin, gapErrlogUnclassified} {
		if !contains(rep.KnownGaps, g) {
			t.Errorf("known gaps %v lack %q", rep.KnownGaps, g)
		}
	}
	if contains(rep.KnownGaps, gapErrlogNoAdjust) {
		t.Errorf("UTC adjustment line was present, but the gap says it was not")
	}
}

func TestErrorlogGapSentencesNotCausal(t *testing.T) {
	for _, g := range []string{gapErrlogLocal, gapErrlog833, gapErrlogUnclassified, gapErrlogLogin, gapErrlogNoAdjust, gapXERetention, gapXEWaitStart, gapXEOther} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestErrorlogMessageTextNeverStored(t *testing.T) {
	evs, rep, err := collect(t, NewErrorlog(), readFixture(t, "errorlog_good.log"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	blob := fmt.Sprintf("%+v %+v", evs, rep)
	for _, s := range []string{"db-alpha", "user-alpha", "192.0.2.10", "synthetic", "Password", "Login failed", "0x0000", "Scheduler"} {
		if strings.Contains(blob, s) {
			t.Errorf("message text %q stored", s)
		}
	}
}

func TestErrorlogRequiresHostZone(t *testing.T) {
	b := readFixture(t, "errorlog_good.log")
	for name, mut := range map[string]func(*importer.Options){
		"no host":  func(o *importer.Options) { o.Host = nil },
		"no zone":  func(o *importer.Options) { h := *o.Host; h.TZ = nil; o.Host = &h },
		"no label": func(o *importer.Options) { h := *o.Host; h.Label = ""; o.Host = &h },
	} {
		o := testOpts(t)
		mut(&o)
		var emitted int
		_, err := NewErrorlog().Parse(context.Background(), strings.NewReader(string(b)), o, func(evidence.Event) error { emitted++; return nil })
		if !errors.Is(err, importer.ErrTimezoneUnlabelled) || emitted != 0 {
			t.Errorf("%s: err %v, emitted %d", name, err, emitted)
		}
	}
}

func TestErrorlogUTF16(t *testing.T) {
	b := readFixture(t, "errorlog_good.log")
	want, _, err := collect(t, NewErrorlog(), b, testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	crlf := []byte(strings.ReplaceAll(string(b), "\n", "\r\n"))
	for name, in := range map[string][]byte{
		"utf16le bom":         toUTF16LE(b, true),
		"utf16le no bom":      toUTF16LE(b, false),
		"utf16le bom crlf":    toUTF16LE(crlf, true),
		"utf16le no bom crlf": toUTF16LE(crlf, false),
		"utf8 bom":            append([]byte{0xef, 0xbb, 0xbf}, b...),
		"utf8 crlf":           crlf,
	} {
		got, _, err := collect(t, NewErrorlog(), in, testOpts(t))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: events differ from utf-8", name)
		}
	}
}

func TestErrorlogTZMismatch(t *testing.T) {
	var emitted int
	_, err := NewErrorlog().Parse(context.Background(), strings.NewReader(string(readFixture(t, "errorlog_bad_tz.log"))), testOpts(t),
		func(evidence.Event) error { emitted++; return nil })
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonTZMismatch || emitted != 0 {
		t.Fatalf("got %v (emitted %d), want tz-mismatch", err, emitted)
	}
	if strings.Contains(err.Error(), importertest.Sentinel) {
		t.Error("error leaks the sentinel")
	}
}

func TestErrorlogNoAdjustLineGap(t *testing.T) {
	_, rep, err := collect(t, NewErrorlog(), []byte("2001-06-15 14:00:35.00 spid21s     Error: 9002, Severity: 17, State: 2.\n"), testOpts(t))
	if err != nil || !contains(rep.KnownGaps, gapErrlogNoAdjust) {
		t.Errorf("err %v, gaps %v", err, rep.KnownGaps)
	}
}

// New Zealand 2001: NZDT ended 2001-03-18 03:00 (02:00-03:00 occurs twice) and
// began 2001-10-07 02:00 (02:00-03:00 does not exist). Neither is guessed.
func TestErrorlogDST(t *testing.T) {
	log := "2001-03-18 01:30:00.00 spid21s     Error: 9002, Severity: 17, State: 2.\n" +
		"2001-03-18 02:30:00.00 spid21s     Error: 9002, Severity: 17, State: 2.\n" +
		"2001-10-07 02:30:00.00 spid21s     Error: 9002, Severity: 17, State: 2.\n" +
		"2001-10-07 03:30:00.00 spid21s     Error: 9002, Severity: 17, State: 2.\n"
	o := testOpts(t)
	o.Limits = NewErrorlog().DefaultLimits()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewErrorlog(), []byte(log), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || len(rep.Errors) != 2 {
		t.Fatalf("events %d, errors %+v", len(evs), rep.Errors)
	}
	if rep.Errors[0] != (importer.RowError{Line: 2, Reason: importer.ReasonDSTAmbiguous}) ||
		rep.Errors[1] != (importer.RowError{Line: 3, Reason: importer.ReasonDSTNonexistent}) {
		t.Errorf("errors = %+v", rep.Errors)
	}
	// 01:30 NZDT (+13) and 03:30 NZDT (+13).
	if !evs[0].Start.Equal(time.Date(2001, 3, 17, 12, 30, 0, 0, time.UTC)) || !evs[1].Start.Equal(time.Date(2001, 10, 6, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("times = %v, %v", evs[0].Start, evs[1].Start)
	}
}

func TestErrorlogBadLines(t *testing.T) {
	o := testOpts(t)
	o.Limits = NewErrorlog().DefaultLimits()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewErrorlog(), readFixture(t, "errorlog_bad.log"), o)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []importer.Reason
	for _, e := range rep.Errors {
		reasons = append(reasons, e.Reason)
	}
	if fmt.Sprint(reasons) != fmt.Sprint([]importer.Reason{importer.ReasonBadTime, importer.ReasonBadNumber}) {
		t.Errorf("reasons = %v", reasons)
	}
	if strings.Contains(fmt.Sprintf("%+v %+v", evs, rep), importertest.Sentinel) {
		t.Error("sentinel leaked")
	}
	// Default limits: two bad lines out of eight is over 5 %, so the file is refused.
	if _, _, err := collect(t, NewErrorlog(), readFixture(t, "errorlog_bad.log"), testOpts(t)); !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("default reject fraction: got %v", err)
	}
}

func TestErrorlogDetect(t *testing.T) {
	e := NewErrorlog()
	good := readFixture(t, "errorlog_good.log")
	cases := []struct {
		name, file string
		head       []byte
		want       int
	}{
		{"errorlog", "ERRORLOG", good, 50}, // continuation lines 2-3 are not time-prefixed
		{"errorlog.3 utf16", "ERRORLOG.3", toUTF16LE(readFixture(t, "golden/ERRORLOG"), true), 100},
		{"renamed", "server.log", readFixture(t, "golden/ERRORLOG"), 50},
		{"xe csv", "system_health.csv", readFixture(t, "xe_good.csv"), 0},
		{"name only", "errorlog", []byte("hello\n"), 50},
	}
	for _, c := range cases {
		if got := e.Detect(c.head, c.file); got != c.want {
			t.Errorf("%s: Detect = %d, want %d", c.name, got, c.want)
		}
	}
}

func FuzzParseErrorlog(f *testing.F) {
	f.Add(readFixture(f, "errorlog_good.log"))
	f.Add(toUTF16LE(readFixture(f, "errorlog_good.log"), false))
	for _, b := range errorlogBad(f) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzCheck(t, NewErrorlog(), data) })
}
