package winevents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts(tz *time.Location) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Hosts: func(o string) (importer.HostHint, bool) {
			if strings.EqualFold(o, "WEB-A") || strings.EqualFold(o, "WEB-A.example.test") {
				return importer.HostHint{Label: "web-a", Role: "iis", TZ: tz}, true
			}
			return importer.HostHint{}, false
		},
	}
}

func parse(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), o)
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

// fixtures: LogName, ProviderName, Id and AppPool are whole cells that reach
// attributes, so the harness's generated formula cells apply.
func fixtures(t testing.TB) importertest.Fixtures {
	good := fixture(t, "events_utc.csv")
	// The file has a "#TYPE" preamble line before the header, so the harness's
	// derived large file would repeat the header as data. Repeat the body only.
	lines := bytes.SplitAfterN(good, []byte("\n"), 3)
	large := append([]byte{}, lines[0]...)
	large = append(large, lines[1]...)
	for len(large) < 2*importer.HeadSize+1024 {
		large = append(large, lines[2]...)
	}
	return importertest.Fixtures{
		Good:    good,
		Bad:     fixture(t, "bad_sentinel.csv"),
		Options: opts(nil),
		Large:   large,
	}
}

func TestConformance(t *testing.T) {
	importertest.Run(t, New(), fixtures(t))
}

func TestUTCFields(t *testing.T) {
	evs, rep, err := parse(t, New(), fixture(t, "events_utc.csv"), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 || rep.Rows != 5 || rep.Rejected != 0 || rep.Emitted != 5 {
		t.Fatalf("events %d rows %d rejected %d", len(evs), rep.Rows, rep.Rejected)
	}
	e := evs[0]
	want := time.Date(2001, 10, 24, 13, 0, 0, 0, time.UTC)
	if !e.Start.Equal(want) || !e.End.Equal(want) || e.Start.Location() != time.UTC {
		t.Errorf("time %v", e.Start)
	}
	if e.Kind != "win-event" || e.Host != "web-a" || e.Source != Name || e.TZRule != clock.RuleUTCField || e.Severity != "error" {
		t.Errorf("fields %+v", e)
	}
	if e.Attrs["log"] != "System" || e.Attrs["provider"] != "Microsoft-Windows-WAS" || e.Attrs["id"] != "5002" || e.Attrs["app-pool"] != "DefaultAppPool" {
		t.Errorf("attrs %v", e.Attrs)
	}
	if !evs[1].Start.Equal(want.Add(5*time.Second+123*time.Millisecond)) || evs[1].Severity != "warning" {
		t.Errorf("fraction/severity: %v %q", evs[1].Start, evs[1].Severity)
	}
	if evs[2].Severity != "error" || evs[3].Severity != "info" || evs[4].Severity != "info" {
		t.Errorf("severity names/numbers: %q %q %q", evs[2].Severity, evs[3].Severity, evs[4].Severity)
	}
	if _, has := evs[2].Attrs["app-pool"]; has {
		t.Error("empty AppPool kept")
	}
	// Message text is dropped at parse: nothing from it may reach an event.
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, "SYNTHETIC") {
				t.Errorf("message text in attr %s=%q", k, v)
			}
		}
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField || rep.FieldRules["TimeCreatedUtc"] != clock.RuleUTCField {
		t.Errorf("rule not reported: %+v %+v", rep.Quality, rep.FieldRules)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-A.example.test" {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
	if !hasGap(rep, GapNoTaskEvents) {
		t.Errorf("task-scheduler gap missing: %v", rep.KnownGaps)
	}
}

func TestLocalTimeDSTFoldAndGap(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata")
	}
	o := opts(la)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	evs, rep, err := parse(t, New(), fixture(t, "events_local.csv"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 6 || rep.Rejected != 2 || len(evs) != 4 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
	reasons := map[importer.Reason]int64{}
	for _, re := range rep.Errors {
		reasons[re.Reason] = re.Line
	}
	if reasons[importer.ReasonDSTNonexistent] != 3 || reasons[importer.ReasonDSTAmbiguous] != 6 {
		t.Errorf("reject reasons/lines %v", rep.Errors)
	}
	got := map[string]time.Time{}
	for _, e := range evs {
		got[e.Attrs["id"]] = e.Start
		if e.TZRule != clock.RuleHostLocal {
			t.Errorf("rule %q", e.TZRule)
		}
	}
	wants := map[string]time.Time{
		"10": time.Date(2001, 4, 1, 9, 30, 0, 0, time.UTC),    // 01:30 PST (-8)
		"12": time.Date(2001, 4, 1, 10, 30, 0, 0, time.UTC),   // 03:30 PDT (-7)
		"13": time.Date(2001, 10, 28, 7, 30, 0, 0, time.UTC),  // 00:30 PDT
		"15": time.Date(2001, 10, 28, 10, 30, 0, 0, time.UTC), // 02:30 PST
	}
	for id, w := range wants {
		if !got[id].Equal(w) {
			t.Errorf("id %s: %v want %v", id, got[id], w)
		}
	}
	if _, bad := got["11"]; bad {
		t.Error("gap time emitted")
	}
	if _, bad := got["14"]; bad {
		t.Error("fold time emitted")
	}
	if rep.Quality.TimezoneRule != clock.RuleHostLocal || rep.FieldRules["TimeCreated"] != clock.RuleHostLocal {
		t.Errorf("rule not reported %+v %+v", rep.Quality, rep.FieldRules)
	}
	// With the default reject fraction the two rejects quarantine this tiny file.
	_, _, err = parse(t, New(), fixture(t, "events_local.csv"), opts(la))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction {
		t.Errorf("default fraction: %v", err)
	}
}

func TestLocalTimeNeedsHostZone(t *testing.T) {
	var n int
	rep, err := New().Parse(context.Background(), bytes.NewReader(fixture(t, "events_local.csv")), opts(nil),
		func(evidence.Event) error { n++; return nil })
	var qe *importer.QuarantineError
	if !errors.Is(err, importer.ErrTimezoneUnlabelled) || !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
		t.Fatalf("got %v", err)
	}
	if n != 0 || rep.Emitted != 0 {
		t.Errorf("emitted %d before refusing", n)
	}
}

func TestMissingColumn(t *testing.T) {
	_, _, err := parse(t, New(), fixture(t, "bad_missing_id.csv"), opts(nil))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Column != "Id" {
		t.Fatalf("got %v", err)
	}
	noTime := []byte("\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\"\n\"web-a\",\"System\",\"P\",\"1\",\"4\"\n")
	if _, _, err := parse(t, New(), noTime, opts(nil)); !errors.As(err, &qe) || qe.Column != "TimeCreatedUtc" {
		t.Fatalf("no time column: %v", err)
	}
}

func TestUnknownHostAndPickedHost(t *testing.T) {
	o := opts(nil)
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	_, rep, err := parse(t, New(), fixture(t, "events_utc.csv"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("got %v", err)
	}
	if len(rep.ObservedHosts) != 1 {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
	o.Host = &importer.HostHint{Label: "picked"}
	evs, _, err := parse(t, New(), fixture(t, "events_utc.csv"), o)
	if err != nil || len(evs) != 5 || evs[0].Host != "picked" {
		t.Fatalf("picked host: %d events, %v", len(evs), err)
	}
}

func TestBinaryAndNonEventInput(t *testing.T) {
	for name, data := range map[string][]byte{
		"evtx": append([]byte("ElfFile\x00"), make([]byte, 64)...),
		"blg":  append([]byte("PERF\x01\x00\x00\x00"), make([]byte, 64)...),
	} {
		_, _, err := parse(t, New(), data, opts(nil))
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
			t.Errorf("%s: got %v", name, err)
		}
	}
	_, _, err := parse(t, New(), []byte("a,b\n1,2\n"), opts(nil))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) {
		t.Errorf("not an events csv: %v", err)
	}
}

func TestRejectsAreReasonsOnly(t *testing.T) {
	_, rep, err := parse(t, New(), fixture(t, "bad_sentinel.csv"), opts(nil))
	if err == nil {
		t.Fatal("expected quarantine: 4 of 5 rows rejected")
	}
	if strings.Contains(fmt.Sprintf("%v %+v", err, rep), "SENTINEL") {
		t.Error("content in error or report")
	}
	reasons := map[importer.Reason]bool{}
	for _, re := range rep.Errors {
		reasons[re.Reason] = true
	}
	for _, r := range []importer.Reason{importer.ReasonBadNumber, importer.ReasonBadTime, importer.ReasonFieldCount} {
		if !reasons[r] {
			t.Errorf("missing reason %s in %v", r, rep.Errors)
		}
	}
}

func TestUTCRequiresZ(t *testing.T) {
	for _, cell := range []string{"2001-10-24T13:00:00+02:00", "2001-10-24T13:00:00", "10/24/2001 13:00:00"} {
		data := "\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\"\n" +
			"\"" + cell + "\",\"web-a\",\"System\",\"P\",\"1\",\"4\"\n"
		o := opts(nil)
		o.Limits = importer.Limits{MaxRejectFraction: 1}
		evs, rep, err := parse(t, New(), []byte(data), o)
		if err != nil || len(evs) != 0 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime {
			t.Errorf("%q: %d events, %+v, %v", cell, len(evs), rep, err)
		}
	}
}

func TestSpecialCellsKeptVerbatim(t *testing.T) {
	pools := []string{"=cmd+1", "+sum", "-neg", "@at", "\tTab"}
	var b strings.Builder
	b.WriteString("\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\",\"AppPool\"\n")
	for _, p := range pools {
		fmt.Fprintf(&b, "\"2001-10-24T13:00:00Z\",\"web-a\",\"System\",\"P\",\"1\",\"4\",\"%s\"\n", p)
	}
	evs, _, err := parse(t, New(), []byte(b.String()), opts(nil))
	if err != nil || len(evs) != len(pools) {
		t.Fatalf("%d events, err %v", len(evs), err)
	}
	for i, e := range evs {
		if e.Attrs["app-pool"] != pools[i] {
			t.Errorf("pool %d: %q != %q", i, e.Attrs["app-pool"], pools[i])
		}
		if err := e.Validate(New().AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
}

func TestTaskSchedulerClassifier(t *testing.T) {
	imp := New(ClassifyTaskScheduler)
	evs, rep, err := parse(t, imp, fixture(t, "events_tasks.csv"), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
	if got := strings.Join(kinds, ","); got != "task-start,task-complete,task-skipped,win-event,win-event" {
		t.Errorf("kinds %s", got)
	}
	if evs[2].Attrs["reason"] != "instance-already-running" {
		t.Errorf("classifier attrs %v", evs[2].Attrs)
	}
	if hasGap(rep, GapNoTaskEvents) {
		t.Error("gap reported although task events are present")
	}
	// Without a classifier everything stays a plain win-event.
	plain, _, err := parse(t, New(), fixture(t, "events_tasks.csv"), opts(nil))
	if err != nil || plain[0].Kind != "win-event" {
		t.Errorf("no classifier: %v", err)
	}
}

func TestClassifierAttrsFilteredByAllowlist(t *testing.T) {
	cls := func(p string, id int) (string, map[string]string, bool) {
		return "custom", map[string]string{"reason": "r", "message": "leak", "id": "forged"}, true
	}
	imp := New(cls)
	evs, _, err := parse(t, imp, fixture(t, "events_utc.csv"), opts(nil))
	if err != nil || len(evs) == 0 {
		t.Fatal(err)
	}
	for _, e := range evs {
		if _, has := e.Attrs["message"]; has {
			t.Error("non-allowlisted classifier attr kept")
		}
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
}

func TestDetect(t *testing.T) {
	imp := New()
	if imp.Detect(fixture(t, "events_utc.csv"), "e.csv") != 90 || imp.Detect(fixture(t, "events_local.csv"), "") != 90 {
		t.Error("event exports not detected")
	}
	if imp.Detect([]byte("a,b\n"), "") != 0 || imp.Detect(nil, "") != 0 {
		t.Error("false positive")
	}
	good := fixture(t, "events_tasks.csv")
	u16 := []byte{0xFF, 0xFE}
	for _, r := range string(good[:160]) {
		u16 = append(u16, byte(r), 0)
	}
	if imp.Detect(u16, "") != 90 {
		t.Error("utf-16le head not detected")
	}
}

type failTransport struct{ hit *bool }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*f.hit = true
	return nil, errors.New("network is off in importer tests")
}

func TestNoNetwork(t *testing.T) {
	var hit bool
	old := http.DefaultTransport
	http.DefaultTransport = failTransport{&hit}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(t, New(ClassifyTaskScheduler), fixture(t, "events_utc.csv"), opts(nil)); err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("importer used the network")
	}
}

// sentinelScrub hides two things from importertest.FuzzParse's sentinel check,
// because a mutated file can legitimately put the sentinel there: a name in
// Report.ObservedHosts (raw names by design; Run's own host check exempts it),
// and an attribute whose value is a whole cell (LogName, ProviderName, AppPool).
// F0's FuzzParse flags both. Errors, gaps, field rules and the rest of every
// event are still checked, and Run checks Fixtures.Bad for attribute leaks.
type sentinelScrub struct{ importer.Importer }

func (s sentinelScrub) Parse(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rep, err := s.Importer.Parse(ctx, r, o, func(e evidence.Event) error {
		clean := make(map[string]string, len(e.Attrs))
		for k, v := range e.Attrs {
			if !strings.Contains(v, importertest.Sentinel) {
				clean[k] = v
			}
		}
		e.Attrs = clean
		return emit(e)
	})
	kept := rep.ObservedHosts[:0:0]
	for _, h := range rep.ObservedHosts {
		if !strings.Contains(h, importertest.Sentinel) {
			kept = append(kept, h)
		}
	}
	rep.ObservedHosts = kept
	return rep, err
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"events_utc.csv", "events_tasks.csv", "events_local.csv", "bad_sentinel.csv", "bad_missing_id.csv"} {
		f.Add(fixture(f, n))
	}
	f.Add([]byte("\xff\xfeT\x00i\x00"))
	fx := fixtures(f)
	la, _ := time.LoadLocation("America/Los_Angeles")
	fx.Options = opts(la) // the same seeds also reach the host-local time path
	importertest.FuzzParse(f, sentinelScrub{New(ClassifyTaskScheduler)}, fx)
}
