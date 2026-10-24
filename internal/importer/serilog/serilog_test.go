package serilog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

func zone(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	return loc
}

// hashPS is a stand-in for the case pseudonymiser: logger-<hex of a hash>.
func hashPS(kind, v string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + v))
	return kind + "-" + hex.EncodeToString(h[:4])
}

func opts(t testing.TB) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host:         &importer.HostHint{Label: "web-a", Role: "app", TZ: zone(t), Runtime: "dotnet-8"},
		Pseudonymize: hashPS,
	}
}

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), o)
}

func must(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := parse(t, imp, data, o)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return evs, rep
}

// Every attribute is a fixed word or a number, so NoFreeText applies.
func clefFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{Good: fixture(t, "clef_good.jsonl"), Bad: fixture(t, "clef_bad.jsonl"),
		Options: opts(t), NoFreeText: true}
}

func textFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{Good: fixture(t, "text_good.log"), Bad: fixture(t, "text_bad.log"),
		Options: opts(t), NoFreeText: true}
}

func TestConformanceCLEF(t *testing.T) { importertest.Run(t, NewCLEF(), clefFixtures(t)) }
func TestConformanceText(t *testing.T) { importertest.Run(t, NewText(), textFixtures(t)) }

func FuzzCLEF(f *testing.F) { importertest.FuzzParse(f, NewCLEF(), clefFixtures(f)) }
func FuzzText(f *testing.F) { importertest.FuzzParse(f, NewText(), textFixtures(f)) }

func byKind(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func noSentinel(t *testing.T, evs []evidence.Event, rep importer.Report, err error) {
	t.Helper()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%+v %+v %v", evs, rep, err)
	for _, e := range evs {
		fmt.Fprintf(&sb, " %v", e.Attrs)
	}
	if strings.Contains(sb.String(), sentinel) || strings.Contains(strings.ToLower(sb.String()), "alice") {
		t.Errorf("file content reached the output: %s", sb.String())
	}
}

func TestCLEFEvents(t *testing.T) {
	evs, rep := must(t, NewCLEF(), fixture(t, "clef_good.jsonl"), opts(t))
	noSentinel(t, evs, rep, nil)

	// The request line, 1834.2 ms, is an app-request interval with its numbers.
	reqs := byKind(evs, applog.KindAppRequest)
	if len(reqs) != 1 {
		t.Fatalf("app-request events: %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.DurationMs == nil || *r.DurationMs != 1834.2 {
		t.Errorf("request duration %v, want 1834.2", r.DurationMs)
	}
	if r.Attrs[applog.AttrStatus] != "200" || r.Attrs[applog.AttrMethod] != "GET" {
		t.Errorf("request attrs %v", r.Attrs)
	}
	if r.TZRule != clock.RuleUTCField || r.Host != "web-a" || r.Line != 1 {
		t.Errorf("request rule/host/line: %q %q %d", r.TZRule, r.Host, r.Line)
	}
	// The interval ends at the line's time: the line is written when the request finishes.
	if got := r.End.UTC(); !got.Equal(utc("2001-10-25T01:02:30.95Z")) {
		t.Errorf("request end %v", got)
	}

	// The warning and the error are log entries, and so is the start line (a
	// classified family); the error names the curated type. A line with no @l
	// is Information.
	entries := byKind(evs, applog.KindLogEntry)
	if len(entries) != 3 {
		t.Fatalf("log-entry events: %d, want 3", len(entries))
	}
	if entries[2].Attrs[applog.AttrFamily] != applog.FamilyAppStart || entries[2].Attrs[applog.AttrLevel] != "Information" {
		t.Errorf("start entry attrs %v", entries[2].Attrs)
	}
	w, e := entries[0], entries[1]
	if w.Attrs[applog.AttrLevel] != "Warning" || w.Attrs[applog.AttrLogger] == "" || strings.Contains(w.Attrs[applog.AttrLogger], "CacheClient") {
		t.Errorf("warning attrs %v", w.Attrs)
	}
	if !strings.HasPrefix(w.Attrs[applog.AttrLogger], "logger-") {
		t.Errorf("a name outside the framework list must be a pseudonym: %v", w.Attrs)
	}
	if e.Attrs[applog.AttrException] != "System.TimeoutException" || e.Attrs[applog.AttrLevel] != "Error" {
		t.Errorf("error attrs %v", e.Attrs)
	}
	// Four parts is deeper than the framework rule keeps: a pseudonym too.
	if !strings.HasPrefix(e.Attrs[applog.AttrLogger], "logger-") {
		t.Errorf("a deep framework name is a pseudonym: %v", e.Attrs)
	}

	// The start line has an offset: +13:00 on 01:05 is 12:05 UTC the day before.
	starts := byKind(evs, applog.KindAppStart)
	if len(starts) != 1 {
		t.Fatalf("app-start events: %d", len(starts))
	}
	if !starts[0].Start.Equal(utc("2001-10-24T12:05:00Z")) || starts[0].TZRule != clock.RuleOffsetInField {
		t.Errorf("start %v rule %q", starts[0].Start, starts[0].TZRule)
	}
	if byKind(evs, applog.KindLogRate) == nil {
		t.Error("no log-rate points")
	}

	if rep.Rows != 4 || rep.Rejected != 0 || rep.Importer != NameCLEF {
		t.Errorf("report rows %d rejected %d importer %q", rep.Rows, rep.Rejected, rep.Importer)
	}
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField || rep.Quality.SourceVersion != "dotnet-8" {
		t.Errorf("quality %+v", rep.Quality)
	}
	if rep.Quality.CoverageFirst == nil || !rep.Quality.CoverageFirst.Equal(utc("2001-10-24T12:05:00Z")) {
		t.Errorf("coverage first %v", rep.Quality.CoverageFirst)
	}
	if !contains(rep.KnownGaps, GapTemplateOnly) || !contains(rep.KnownGaps, GapOffsets) || !contains(rep.KnownGaps, applog.GapDropped) {
		t.Errorf("known gaps %q", rep.KnownGaps)
	}
}

func TestTextEvents(t *testing.T) {
	evs, rep := must(t, NewText(), fixture(t, "text_good.log"), opts(t))
	noSentinel(t, evs, rep, nil)

	reqs := byKind(evs, applog.KindAppRequest)
	if len(reqs) != 1 {
		t.Fatalf("app-request events: %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.DurationMs == nil || *r.DurationMs != 1834.2 || r.Attrs[applog.AttrStatus] != "200" {
		t.Errorf("request %v %v", r.DurationMs, r.Attrs)
	}
	// The sentence is matched by its fixed shape; its method word is a listed
	// method, and its path is read by no pattern.
	if r.Attrs[applog.AttrMethod] != "GET" {
		t.Errorf("request attrs %v", r.Attrs)
	}
	// 14:02:30.950 +13:00 is 01:02:30.950 UTC.
	if !r.End.Equal(utc("2001-10-25T01:02:30.95Z")) || r.TZRule != clock.RuleOffsetInField || r.Line != 1 {
		t.Errorf("request end %v rule %q line %d", r.End, r.TZRule, r.Line)
	}
	entries := byKind(evs, applog.KindLogEntry)
	if len(entries) != 2 {
		t.Fatalf("log-entry events: %d, want 2", len(entries))
	}
	if entries[0].Attrs[applog.AttrLevel] != "WRN" || entries[1].Attrs[applog.AttrLevel] != "ERR" {
		t.Errorf("levels %v %v", entries[0].Attrs, entries[1].Attrs)
	}
	if entries[1].Attrs[applog.AttrException] != "System.TimeoutException" || entries[1].Line != 3 {
		t.Errorf("exception entry %v line %d", entries[1].Attrs, entries[1].Line)
	}
	if _, has := entries[0].Attrs[applog.AttrLogger]; has {
		t.Errorf("the default template has no logger name: %v", entries[0].Attrs)
	}
	if rep.Rows != 5 || rep.Rejected != 0 || !contains(rep.KnownGaps, GapNoLogger) {
		t.Errorf("rows %d rejected %d gaps %q", rep.Rows, rep.Rejected, rep.KnownGaps)
	}
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
}

// The plan fixtures carry the sentinel in the request path. The output must be
// identical to the same file with another path: the path is read by nothing.
func TestSentinelInRequestPath(t *testing.T) {
	for _, c := range []struct {
		imp           importer.Importer
		priv, control string
	}{
		{NewCLEF(), "clef_privacy.jsonl", "clef_good.jsonl"},
		{NewText(), "text_privacy.log", "text_good.log"},
	} {
		priv := fixture(t, c.priv)
		if !bytes.Contains(priv, []byte(sentinel)) {
			t.Fatalf("%s lost its sentinel", c.priv)
		}
		evs, rep, err := parse(t, c.imp, priv, opts(t))
		noSentinel(t, evs, rep, err)
		want, wrep := must(t, c.imp, fixture(t, c.control), opts(t))
		if j(t, evs) != j(t, want) || j(t, rep) != j(t, wrep) {
			t.Errorf("%s: the path changed the output", c.imp.Name())
		}
	}
}

func j(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestGapSentencesAreNotCausal(t *testing.T) {
	for _, g := range []string{GapTemplateOnly, GapNoLogger, GapOffsets} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestAllowedAttrsAreTheCoreSet(t *testing.T) {
	for _, imp := range []importer.Importer{NewCLEF(), NewText()} {
		got, want := imp.AllowedAttrs(), applog.AllowedAttrs()
		if len(got) != len(want) || len(got) != 16 {
			t.Errorf("%s: %d attrs, want 16", imp.Name(), len(got))
		}
		for k := range want {
			if !got[k] {
				t.Errorf("%s: missing %q", imp.Name(), k)
			}
		}
	}
}

func TestDefaultLimits(t *testing.T) {
	c, x := NewCLEF().DefaultLimits(), NewText().DefaultLimits()
	if c.MaxBytes != 2<<30 || c.MaxRows != 10_000_000 || c.MaxEvents != 2_000_000 || c.MaxLineBytes != 256<<10 {
		t.Errorf("clef limits %+v", c)
	}
	if x.MaxBytes != 2<<30 || x.MaxRows != 10_000_000 || x.MaxEvents != 2_000_000 || x.MaxLineBytes != 64<<10 {
		t.Errorf("text limits %+v", x)
	}
}

func TestDetect(t *testing.T) {
	clef, txt := NewCLEF(), NewText()
	cg, tg := fixture(t, "clef_good.jsonl"), fixture(t, "text_good.log")
	if s := clef.Detect(cg, "x.json"); s < 90 {
		t.Errorf("clef detect on clef: %d", s)
	}
	if s := txt.Detect(tg, "x.log"); s < 90 {
		t.Errorf("text detect on text: %d", s)
	}
	for name, c := range map[string]struct {
		imp  importer.Importer
		data []byte
	}{
		"clef on text":    {clef, tg},
		"text on clef":    {txt, cg},
		"clef on console": {clef, fixture(t, "text_console.log")},
		"text on console": {txt, fixture(t, "text_console.log")},
		"clef on mongod":  {clef, []byte(`{"t":{"$date":"2001-10-25T01:02:30.950+00:00"},"s":"I","c":"COMMAND"}` + "\n")},
		"text on log4net": {txt, []byte("2001-10-25 14:02:31,123 [7] INFO Synthetic.Api message\n")},
		"clef on empty":   {clef, nil},
		"text on empty":   {txt, nil},
	} {
		if s := c.imp.Detect(c.data, "f"); s != 0 {
			t.Errorf("%s: %d, want 0", name, s)
		}
	}
	// A head cut inside the first line still scores.
	if s := clef.Detect(cg[:40], ""); s < 90 {
		t.Errorf("cut clef head: %d", s)
	}
	if s := txt.Detect(tg[:45], ""); s < 90 {
		t.Errorf("cut text head: %d", s)
	}
}

func TestCLEFNegatives(t *testing.T) {
	o := opts(t)
	cases := []struct {
		name, line string
		want       importer.Reason
	}{
		{"no @t", `{"@mt":"x ` + sentinel + `","@l":"Warning"}`, importer.ReasonBadTime},
		{"@t not a string", `{"@t":12345,"@mt":"x"}`, importer.ReasonBadTime},
		{"@t unreadable", `{"@t":"2001-13-45T01:02:03Z","@mt":"x"}`, importer.ReasonBadTime},
		{"@t out of range", `{"@t":"1970-01-01T00:00:00Z","@mt":"x"}`, importer.ReasonBadTime},
		{"@t with no offset", `{"@t":"2001-10-25T01:02:30.9500000","@mt":"` + sentinel + `"}`, importer.ReasonTZUnlabelled},
		{"not json", `2001-10-25 ` + sentinel, importer.ReasonFieldCount},
		{"truncated json", `{"@t":"2001-10-25T01:02:30Z","@mt":"` + sentinel, importer.ReasonFieldCount},
		{"json array", `["` + sentinel + `"]`, importer.ReasonFieldCount},
	}
	good := string(fixture(t, "clef_good.jsonl"))
	for _, c := range cases {
		// Enough good lines around the bad one that the file is not quarantined.
		data := strings.Repeat(good, 10) + c.line + "\n"
		o.Limits.MaxRejectFraction = 0.5
		evs, rep, err := parse(t, NewCLEF(), []byte(data), o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != c.want || rep.Errors[0].Line != 41 {
			t.Errorf("%s: rejected %d errors %+v, want %s at line 41", c.name, rep.Rejected, rep.Errors, c.want)
		}
		noSentinel(t, evs, rep, nil)
	}
	// A file of nothing but such lines is refused.
	if _, _, err := parse(t, NewCLEF(), []byte(`{"@mt":"x"}`+"\n"), o); !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("file of rejected rows: %v", err)
	}
}

func TestCLEFTimeRules(t *testing.T) {
	o := opts(t)
	data := `{"@t":"2001-10-25T01:00:00Z","@l":"Error","@x":"System.Exception: a"}` + "\n" +
		`{"@t":"2001-10-25T14:00:00+13:00","@l":"Error","@x":"System.Exception: a"}` + "\n" +
		`{"@t":"2001-10-24T20:00:00-05:00","@l":"Error","@x":"System.Exception: a"}` + "\n" +
		`{"@t":"2001-10-25T01:00:00","@l":"Error"}` + "\n"
	o.Limits.MaxRejectFraction = 0.5
	evs, rep := must(t, NewCLEF(), []byte(data), o)
	ents := byKind(evs, applog.KindLogEntry)
	if len(ents) != 3 {
		t.Fatalf("entries %d", len(ents))
	}
	want := utc("2001-10-25T01:00:00Z")
	rules := []string{clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleOffsetInField}
	for i, e := range ents {
		if !e.Start.Equal(want) { // the three spellings are one instant
			t.Errorf("entry %d at %v, want %v", i, e.Start, want)
		}
		if e.TZRule != rules[i] {
			t.Errorf("entry %d rule %q, want %q", i, e.TZRule, rules[i])
		}
	}
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonTZUnlabelled {
		t.Errorf("rejects %d %+v", rep.Rejected, rep.Errors)
	}
	// Z and an offset in one file: the report names the weaker rule and says so.
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField || !contains(rep.KnownGaps, GapOffsets) {
		t.Errorf("quality %+v gaps %q", rep.Quality, rep.KnownGaps)
	}
	// Only Z: UTC rule, no mixed-marker sentence.
	_, rep = must(t, NewCLEF(), []byte(`{"@t":"2001-10-25T01:00:00Z","@l":"Error"}`+"\n"), opts(t))
	if rep.Quality.TimezoneRule != clock.RuleUTCField || contains(rep.KnownGaps, GapOffsets) {
		t.Errorf("utc-only quality %+v gaps %q", rep.Quality, rep.KnownGaps)
	}
}

// A line with no @l is Information; a level outside the known set is not kept.
func TestCLEFLevels(t *testing.T) {
	start := `"@mt":"Application started. Press Ctrl+C to shut down.","SourceContext":"Microsoft.Hosting.Lifetime"`
	data := `{"@t":"2001-10-25T01:00:00Z",` + start + `}` + "\n" +
		`{"@t":"2001-10-25T01:00:01Z","@l":"Bogus",` + start + `}` + "\n" +
		`{"@t":"2001-10-25T01:00:02Z","@l":"Fatal","@x":"System.Exception: a"}` + "\n" +
		`{"@t":"2001-10-25T01:00:03Z","@l":7,` + start + `}` + "\n" +
		`{"@t":"2001-10-25T01:00:04Z","@l":null,` + start + `}` + "\n"
	evs, rep := must(t, NewCLEF(), []byte(data), opts(t))
	var levels []string
	for _, e := range byKind(evs, applog.KindLogEntry) {
		levels = append(levels, e.Attrs[applog.AttrLevel])
	}
	// No @l and a null @l are Information; an unknown or non-text level is kept as none.
	if want := []string{"Information", "", "Fatal", "", "Information"}; fmt.Sprint(levels) != fmt.Sprint(want) {
		t.Errorf("entry levels %q, want %q", levels, want)
	}
	if !contains(rep.KnownGaps, applog.GapUnknownLevel) {
		t.Errorf("gaps %q", rep.KnownGaps)
	}
}

// Numbers are read for the request-finished family only: a line of another
// family with the same property names keeps none of them.
func TestCLEFNumbersOnlyForRequestFinished(t *testing.T) {
	data := `{"@t":"2001-10-25T01:00:00Z","@l":"Error","@mt":"x","Elapsed":9999.5,"StatusCode":503,"RequestMethod":"POST","SourceContext":"Synthetic.Api.Other"}` + "\n" +
		`{"@t":"2001-10-25T01:00:01Z","@l":"Error","@mt":"HTTP {RequestMethod} {RequestPath} responded {StatusCode} in {Elapsed:0.0000} ms","Elapsed":2500,"StatusCode":503,"RequestMethod":"POST","SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}` + "\n"
	evs, _ := must(t, NewCLEF(), []byte(data), opts(t))
	ents := byKind(evs, applog.KindLogEntry)
	if len(ents) != 2 {
		t.Fatalf("entries %d", len(ents))
	}
	for _, k := range []string{applog.AttrStatus, applog.AttrMethod} {
		if v, has := ents[0].Attrs[k]; has {
			t.Errorf("other family kept %s=%s", k, v)
		}
	}
	if ents[1].Attrs[applog.AttrStatus] != "503" || ents[1].Attrs[applog.AttrMethod] != "POST" {
		t.Errorf("request-finished attrs %v", ents[1].Attrs)
	}
	// Hostile values: strings, huge and negative numbers, an unknown method.
	data = `{"@t":"2001-10-25T01:00:01Z","@l":"Error","Elapsed":"fast","StatusCode":"503","RequestMethod":"BREW","SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}` + "\n" +
		`{"@t":"2001-10-25T01:00:02Z","@l":"Error","Elapsed":1e999,"StatusCode":-5,"RequestMethod":7,"SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}` + "\n"
	evs, _ = must(t, NewCLEF(), []byte(data), opts(t))
	for _, e := range byKind(evs, applog.KindLogEntry) {
		for _, k := range []string{applog.AttrStatus, applog.AttrMethod} {
			if v, has := e.Attrs[k]; has {
				t.Errorf("hostile value kept as %s=%s", k, v)
			}
		}
	}
}

func TestCLEFMessageTextIsNeverStored(t *testing.T) {
	line := `{"@t":"2001-10-25T01:00:00Z","@l":"Error","@mt":"` + sentinel + ` {A}","@m":"` + sentinel + ` alice","@r":["` + sentinel + `"],"@i":"` + sentinel + `","A":"` + sentinel + `","User":"` + sentinel + `","@x":"System.InvalidOperationException: ` + sentinel + `\r\n   at ` + sentinel + `.Run()","SourceContext":"Synthetic.` + sentinel + `"}` + "\n"
	evs, rep, err := parse(t, NewCLEF(), []byte(line), opts(t))
	noSentinel(t, evs, rep, err)
	if len(byKind(evs, applog.KindLogEntry)) != 1 {
		t.Fatalf("events %+v", evs)
	}
	a := byKind(evs, applog.KindLogEntry)[0].Attrs
	if a[applog.AttrException] != "System.InvalidOperationException" || !strings.HasPrefix(a[applog.AttrLogger], "logger-") {
		t.Errorf("attrs %v", a)
	}
	for k, v := range a {
		if !applog.AllowedAttrs()[k] {
			t.Errorf("attribute %q is not on the allowlist (value %q)", k, v)
		}
	}
}

func TestCLEFRequiresHost(t *testing.T) {
	o := opts(t)
	o.Host = nil
	if _, _, err := parse(t, NewCLEF(), fixture(t, "clef_good.jsonl"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("clef without a host: %v", err)
	}
	if _, _, err := parse(t, NewText(), fixture(t, "text_good.log"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("text without a host: %v", err)
	}
}

func TestCLEFLoggerNeedsPseudonym(t *testing.T) {
	o := opts(t)
	o.Pseudonymize = nil
	_, _, err := parse(t, NewCLEF(), fixture(t, "clef_good.jsonl"), o)
	if !errors.Is(err, applog.ErrPseudonym) {
		t.Errorf("no pseudonymiser: %v", err)
	}
}

func TestCLEFBlankLinesAreSkipped(t *testing.T) {
	data := "\n\n" + strings.ReplaceAll(string(fixture(t, "clef_good.jsonl")), "\n", "\n\n") + "\n"
	evs, rep := must(t, NewCLEF(), []byte(data), opts(t))
	want, wrep := must(t, NewCLEF(), fixture(t, "clef_good.jsonl"), opts(t))
	if len(evs) != len(want) || rep.Rows != wrep.Rows {
		t.Errorf("blank lines changed the result: %d/%d events, %d/%d rows", len(evs), len(want), rep.Rows, wrep.Rows)
	}
	if _, _, err := parse(t, NewCLEF(), []byte("\n  \n\t\n"), opts(t)); err == nil {
		t.Error("a file of blank lines was accepted")
	}
}

func TestTextNegatives(t *testing.T) {
	o := opts(t)
	// Console template: refused whole, with the fixed reason, without content.
	_, _, err := parse(t, NewText(), fixture(t, "text_console.log"), o)
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("console template: %v", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Errorf("error holds file content: %v", err)
	}
	// Any other text is refused too.
	for _, s := range []string{"just some text\n", "   at Foo.Bar()\n2001-10-25 14:02:30.950 +13:00 [INF] x\n", "{\"@t\":\"2001-10-25T01:00:00Z\"}\n"} {
		if _, _, err := parse(t, NewText(), []byte(s), o); !errors.Is(err, importer.ErrQuarantine) {
			t.Errorf("%q: %v", s, err)
		}
	}
	good := string(fixture(t, "text_good.log"))
	cases := []struct {
		name, line string
		want       importer.Reason
	}{
		{"no zone", `2001-10-25 14:02:31.123 [WRN] ` + sentinel, importer.ReasonTZUnlabelled},
		{"no zone no fraction", `2001-10-25 14:02:31 [WRN] ` + sentinel, importer.ReasonTZUnlabelled},
		{"bad month", `2001-13-45 14:02:31.123 +13:00 [WRN] ` + sentinel, importer.ReasonBadTime},
		{"bad offset", `2001-10-25 14:02:31.123 +99:00 [WRN] ` + sentinel, importer.ReasonBadTime},
		{"year out of range", `1970-01-01 00:00:00.000 +00:00 [WRN] ` + sentinel, importer.ReasonBadTime},
		{"date and no level", `2001-10-25 14:02:31.123 +13:00 ` + sentinel, importer.ReasonBadTime},
	}
	for _, c := range cases {
		data := strings.Repeat(good, 10) + c.line + "\nSystem.Exception: " + sentinel + "\n   at " + sentinel + "\n"
		o.Limits.MaxRejectFraction = 0.5
		evs, rep, err := parse(t, NewText(), []byte(data), o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Rejected != 1 || rep.Errors[0].Reason != c.want || rep.Errors[0].Line != 51 {
			t.Errorf("%s: rejected %d errors %+v, want %s at line 51", c.name, rep.Rejected, rep.Errors, c.want)
		}
		// The lines that follow a rejected header belong to it and add nothing.
		if n := len(byKind(evs, applog.KindLogEntry)); n != 20 {
			t.Errorf("%s: %d log entries, want 20", c.name, n)
		}
		noSentinel(t, evs, rep, nil)
	}
}

// The header's level is checked, not trusted: an unknown word is not stored.
func TestTextUnknownLevel(t *testing.T) {
	data := "2001-10-25 14:02:31.123 +13:00 [XYZ] one\n2001-10-25 14:02:32.123 +13:00 [FTL] two\n"
	evs, rep := must(t, NewText(), []byte(data), opts(t))
	for _, e := range evs {
		if v, has := e.Attrs[applog.AttrLevel]; has && v == "XYZ" {
			t.Errorf("unknown level stored: %v", e.Attrs)
		}
	}
	if !contains(rep.KnownGaps, applog.GapUnknownLevel) {
		t.Errorf("gaps %q", rep.KnownGaps)
	}
}

// Multi-line messages and long exception text: the first type and the inner
// type are read, the rest is ignored and bounded.
func TestTextExceptionBlocks(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("2001-10-25 14:02:31.123 +13:00 [ERR] Unhandled exception\n")
	sb.WriteString("System.AggregateException: One or more errors occurred. (" + sentinel + ")\n")
	sb.WriteString(" ---> System.TimeoutException: timed out\n")
	sb.WriteString("   at Synthetic.Api.Run()\n")
	for i := 0; i < 20000; i++ {
		sb.WriteString("   at Synthetic.Api.Frame" + fmt.Sprint(i) + "()\n")
	}
	sb.WriteString("2001-10-25 14:02:32.123 +13:00 [INF] after\n")
	evs, rep := must(t, NewText(), []byte(sb.String()), opts(t))
	noSentinel(t, evs, rep, nil)
	ents := byKind(evs, applog.KindLogEntry)
	if len(ents) != 1 || ents[0].Attrs[applog.AttrException] != "System.AggregateException,System.TimeoutException" {
		t.Fatalf("entries %+v", ents)
	}
}

func TestTextEncodingsAndEmpty(t *testing.T) {
	o := opts(t)
	if _, _, err := parse(t, NewText(), nil, o); err == nil {
		t.Error("empty file accepted")
	}
	if _, _, err := parse(t, NewText(), []byte("\n\n"), o); err == nil {
		t.Error("blank file accepted")
	}
	// A line that is not UTF-8 is one rejected row (encoding).
	data := append([]byte(strings.Repeat(string(fixture(t, "text_good.log")), 10)), []byte("2001-10-25 14:02:31.123 +13:00 [WRN] \xff\xfe\n")...)
	o.Limits.MaxRejectFraction = 0.5
	_, rep, err := parse(t, NewText(), data, o)
	if err != nil || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonEncoding {
		t.Errorf("bad utf-8: %v rejected %d %+v", err, rep.Rejected, rep.Errors)
	}
}

// The window the owner chose bounds the events and the log-rate counts, and
// the report still counts every row.
func TestWindow(t *testing.T) {
	o := opts(t)
	o.Window = importer.Window{From: utc("2001-10-25T01:02:31Z"), To: utc("2001-10-25T01:02:32Z")}
	evs, rep := must(t, NewCLEF(), fixture(t, "clef_good.jsonl"), o)
	if rep.Rows != 4 {
		t.Errorf("rows %d", rep.Rows)
	}
	for _, e := range evs {
		if e.Kind == applog.KindLogRate {
			continue // a rate point starts on the minute
		}
		if e.End.Before(o.Window.From) || e.Start.After(o.Window.To) {
			t.Errorf("event outside the window: %s %v-%v", e.Kind, e.Start, e.End)
		}
	}
	if len(byKind(evs, applog.KindLogEntry)) != 2 {
		t.Errorf("entries in window: %d", len(byKind(evs, applog.KindLogEntry)))
	}
}

// Every constant the report can carry is fixed text (plan 2.5).
func TestReportTextIsFixed(t *testing.T) {
	fixed := map[string]bool{GapTemplateOnly: true, GapNoLogger: true, GapOffsets: true,
		applog.GapDropped: true, applog.GapEntryCap: true, applog.GapRequestThreshold: true,
		applog.GapThresholdUnreadable: true, applog.GapUnknownLevel: true}
	for name, c := range map[string]struct {
		imp  importer.Importer
		file string
	}{"clef": {NewCLEF(), "clef_good.jsonl"}, "text": {NewText(), "text_good.log"}} {
		_, rep := must(t, c.imp, fixture(t, c.file), opts(t))
		for _, g := range rep.KnownGaps {
			if !fixed[g] {
				t.Errorf("%s: unexpected gap %q", name, g)
			}
		}
	}
}
