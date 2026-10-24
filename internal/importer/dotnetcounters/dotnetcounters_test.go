package dotnetcounters

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

func zone(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func opts(t testing.TB) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host: &importer.HostHint{Label: "web-a", Role: "app", TZ: zone(t), Runtime: "dotnet-8"},
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

func parse(t testing.TB, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), New(), bytes.NewReader(data), o)
}

func must(t testing.TB, data []byte, o importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := parse(t, data, o)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return evs, rep
}

// fixtures: every attribute is an allowlisted word or a number, so NoFreeText
// applies.
func fixtures(t testing.TB, good, bad string) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, good),
		Bad:        fixture(t, bad),
		Options:    opts(t),
		NoFreeText: true,
	}
}

func TestConformanceCSV(t *testing.T) {
	importertest.Run(t, New(), fixtures(t, "counters_basic.csv", "bad_sentinel.csv"))
}

// The harness is run on the CSV only: its line-limit, derived large file and
// endless-input checks assume a line-per-row file, and a JSON export is one
// document on one line (the limit is per value instead; see
// TestJSONTruncatedAndLimits). The JSON path has its own tests below and is a seed
// of the fuzz target.

func FuzzParse(f *testing.F) {
	fx := fixtures(f, "counters_basic.csv", "bad_sentinel.csv")
	f.Add(fixture(f, "counters_basic.json"))
	f.Add(fixture(f, "bad_sentinel.json"))
	importertest.FuzzParse(f, New(), fx)
}

// find returns the first event whose attribute k has value v.
func find(evs []evidence.Event, k, v string) (evidence.Event, bool) {
	for _, e := range evs {
		if e.Attrs[k] == v {
			return e, true
		}
	}
	return evidence.Event{}, false
}

func TestCSVUnits(t *testing.T) {
	evs, rep := must(t, fixture(t, "counters_basic.csv"), opts(t))
	if rep.Rows != 12 || rep.Rejected != 0 || len(evs) != 12 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
	want := time.Date(2001, 10, 25, 1, 0, 0, 0, time.UTC) // 14:00 NZDT, UTC+13
	if !evs[0].Start.Equal(want) || !evs[0].End.Equal(want) || evs[0].Start.Location() != time.UTC {
		t.Errorf("first start %v", evs[0].Start)
	}
	for _, e := range evs {
		if e.Kind != "metric" || e.Host != "web-a" || e.Source != Name || e.TZRule != clock.RuleHostLocal || e.Value == nil {
			t.Fatalf("event %+v", e)
		}
	}
	check := func(counter string, unit string, value float64) {
		t.Helper()
		e, ok := find(evs, "counter", counter)
		if !ok {
			t.Fatalf("no event for %q", counter)
		}
		if e.Unit != unit || *e.Value != value {
			t.Errorf("%s: %g %s, want %g %s", counter, *e.Value, e.Unit, value, unit)
		}
	}
	check("CPU Usage", "percent-host", 12.5)
	check("Working Set", "bytes", 210.5e6)
	check("ThreadPool Queue Length", "count", 3)
	check("Allocation Rate", "per-sec", 1048576)
	check("% Time in GC since last GC", "percent", 0.4)
	check("Gen 0 GC Count", "per-sec", 0.1)
	check("Request Rate", "per-sec", 42)
	check("dotnet.thread_pool.queue.length", "count", 2)
	check("http.server.request.duration", "ms", 45)
	e, _ := find(evs, "counter", "http.server.request.duration")
	if e.Attrs["method"] != "GET" || e.Attrs["status"] != "200" || e.Attrs["provider"] != "Microsoft.AspNetCore.Hosting" {
		t.Errorf("tags %v", e.Attrs)
	}
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, "orders") {
				t.Errorf("route text kept in %s=%q", k, v)
			}
		}
	}
	if _, ok := rep.FieldRules["timestamp"]; !ok || rep.Quality.TimezoneRule != clock.RuleHostLocal || rep.Quality.SourceVersion != "dotnet-8" {
		t.Errorf("quality %+v rules %v", rep.Quality, rep.FieldRules)
	}
}

func TestJSON(t *testing.T) {
	evs, rep := must(t, fixture(t, "counters_basic.json"), opts(t))
	if rep.Rows != 5 || len(evs) != 5 || rep.Rejected != 0 {
		t.Fatalf("rows %d events %d rejected %d", rep.Rows, len(evs), rep.Rejected)
	}
	want := time.Date(2001, 10, 25, 1, 0, 5, 123456700, time.UTC)
	if !evs[0].Start.Equal(want) || evs[0].TZRule != clock.RuleOffsetInField {
		t.Errorf("first %v rule %s", evs[0].Start, evs[0].TZRule)
	}
	h, ok := find(evs, "counter", "http.server.request.duration")
	if !ok || *h.Value != 250 || h.Unit != "ms" || h.Attrs["method"] != "POST" || h.Attrs["status"] != "500" || h.Attrs["percentile"] != "95" {
		t.Errorf("histogram %+v", h)
	}
	a, _ := find(evs, "counter", "Allocation Rate")
	if *a.Value != 2097152 || a.Unit != "per-sec" {
		t.Errorf("rate %+v", a)
	}
	// The same file with a timestamp in Z reads as UTC and needs no zone.
	z := strings.ReplaceAll(string(fixture(t, "counters_basic.json")), "+13:00", "Z")
	o := opts(t)
	o.Host.TZ = nil
	evs, _ = must(t, []byte(z), o)
	if evs[0].TZRule != clock.RuleUTCField || !evs[0].Start.Equal(time.Date(2001, 10, 25, 14, 0, 5, 123456700, time.UTC)) {
		t.Errorf("Z timestamp: %v %s", evs[0].Start, evs[0].TZRule)
	}
}

func TestDetect(t *testing.T) {
	d := New()
	if n := d.Detect(fixture(t, "counters_basic.csv"), "a.csv"); n != 95 {
		t.Errorf("csv %d", n)
	}
	if n := d.Detect(fixture(t, "counters_basic.json"), "a.json"); n != 90 {
		t.Errorf("json %d", n)
	}
	for _, s := range []string{"", "a,b,c\n1,2,3\n", `{"foo":1}`, "Timestamp,Provider\n", "Timestamp,Provider,Counter Name,Counter Type\n"} {
		if n := d.Detect([]byte(s), "x"); n != 0 {
			t.Errorf("%q scored %d", s, n)
		}
	}
}

func TestAllowedAttrsMatchExportPolicy(t *testing.T) {
	got, want := New().AllowedAttrs(), evidence.ExportAttrNames(Name)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("allowed %v, export policy %v", got, want)
	}
}

func TestAttrValuesStayOnTheLists(t *testing.T) {
	csv := `Timestamp,Provider,Counter Name,Counter Type,Mean/Increment
10/25/2001 2:00:00 PM,Microsoft.AspNetCore.Hosting,http.server.request.duration (s)[http.request.method=SENTINEL;http.response.status_code=2000;error.type=Acme.Billing.CustomException;Percentile=abc],Histogram,1
10/25/2001 2:00:00 PM,System.Runtime,dotnet.gc.collections ({collection})[gc.heap.generation=gen9],Metric,1
10/25/2001 2:00:00 PM,System.Runtime,dotnet.gc.collections ({collection})[gc.heap.generation=GEN1],Metric,2
10/25/2001 2:00:00 PM,System.Runtime,dotnet.exceptions ({exception})[error.type=system.timeoutexception],Metric,2
`
	evs, _ := must(t, []byte(csv), opts(t))
	if len(evs) != 4 {
		t.Fatalf("%d events", len(evs))
	}
	e := evs[0]
	if e.Attrs["method"] != "" || e.Attrs["status"] != "" || e.Attrs["percentile"] != "" || e.Attrs["error-type"] != "app-exception" {
		t.Errorf("off-list tag values kept: %v", e.Attrs)
	}
	if evs[1].Attrs["generation"] != "" || evs[2].Attrs["generation"] != "gen1" {
		t.Errorf("generation %v %v", evs[1].Attrs, evs[2].Attrs)
	}
	if evs[3].Attrs["error-type"] != "System.TimeoutException" {
		t.Errorf("error type %v", evs[3].Attrs)
	}
	for _, e := range evs {
		if err := e.ValidateFor(opts(t).CaseID, Name, New().AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
}

func TestPID(t *testing.T) {
	o := opts(t)
	evs, rep := must(t, fixture(t, "counters_basic.csv"), o)
	if _, has := evs[0].Attrs["pid"]; has || !hasGap(rep, GapNoPID) {
		t.Errorf("pid without supply: %v gaps %v", evs[0].Attrs, rep.KnownGaps)
	}
	o.Supplied = map[string]string{"pid": "4242"}
	evs, rep = must(t, fixture(t, "counters_basic.csv"), o)
	if evs[0].Attrs["pid"] != "4242" || hasGap(rep, GapNoPID) {
		t.Errorf("pid %v gaps %v", evs[0].Attrs, rep.KnownGaps)
	}
	o.Supplied = map[string]string{"pid": "web-a.example.test"}
	evs, _ = must(t, fixture(t, "counters_basic.csv"), o)
	if _, has := evs[0].Attrs["pid"]; has {
		t.Errorf("non-numeric pid kept: %v", evs[0].Attrs)
	}
}

func hasGap(rep importer.Report, g string) bool {
	for _, s := range rep.KnownGaps {
		if s == g {
			return true
		}
	}
	return false
}

func TestHostRequired(t *testing.T) {
	o := opts(t)
	o.Host = nil
	if _, _, err := parse(t, fixture(t, "counters_basic.csv"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("nil host: %v", err)
	}
	// A CSV with no zone refuses; a JSON with offsets does not need one.
	o = opts(t)
	o.Host.TZ = nil
	if _, _, err := parse(t, fixture(t, "counters_basic.csv"), o); err == nil {
		t.Error("csv without a zone accepted")
	}
	if _, _, err := parse(t, fixture(t, "counters_basic.json"), o); err != nil {
		t.Errorf("json with offsets: %v", err)
	}
	local := `{"TargetProcess":"job-alpha","Events":[{"timestamp":"2001-10-25T14:00:05","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":1}]}`
	if _, _, err := parse(t, []byte(local), o); err == nil {
		t.Error("json local time without a zone accepted")
	}
	evs, _ := must(t, []byte(local), opts(t))
	if len(evs) != 1 || evs[0].TZRule != clock.RuleHostLocal || !evs[0].Start.Equal(time.Date(2001, 10, 25, 1, 0, 5, 0, time.UTC)) {
		t.Errorf("json local %+v", evs)
	}
}

func TestUnknownReadingsAreDroppedNotRejected(t *testing.T) {
	csv := `Timestamp,Provider,Counter Name,Counter Type,Mean/Increment
10/25/2001 2:00:00 PM,Acme.Internal,Order Backlog (Count),Metric,5
10/25/2001 2:00:00 PM,System.Runtime,Order Backlog (Count),Metric,5
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (furlongs),Metric,5
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,NaN
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,
10/25/2001 2:00:00 PM,System.Runtime,Allocation Rate (B),Rate,5
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,1
`
	evs, rep := must(t, []byte(csv), opts(t))
	if len(evs) != 1 || rep.Rejected != 0 || rep.Rows != 7 {
		t.Fatalf("events %d rejected %d rows %d", len(evs), rep.Rejected, rep.Rows)
	}
	for _, g := range []string{GapCounters, GapUnits, GapNoValue, GapCPU} {
		if !hasGap(rep, g) {
			t.Errorf("missing gap %q", g)
		}
	}
	// A file of nothing but unknown counters is complete, not quarantined.
	all := "Timestamp,Provider,Counter Name,Counter Type,Mean/Increment\n" + strings.Repeat("10/25/2001 2:00:00 PM,Acme.Internal,X (Count),Metric,5\n", 50)
	if evs, _, err := parse(t, []byte(all), opts(t)); err != nil || len(evs) != 0 {
		t.Errorf("unknown-only file: %d events, %v", len(evs), err)
	}
}

func TestBadRowsAreRejected(t *testing.T) {
	csv := `Timestamp,Provider,Counter Name,Counter Type,Mean/Increment
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,abc
not a time,System.Runtime,CPU Usage (%),Metric,1
10/32/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,1
10/25/2001 14:00:00 PM,System.Runtime,CPU Usage (%),Metric,1
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,0x10
10/25/2001 2:00:00 PM,System.Runtime,CPU Usage (%),Metric,2
`
	o := opts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep := must(t, []byte(csv), o)
	if len(evs) != 1 || rep.Rejected != 5 {
		t.Errorf("events %d rejected %d", len(evs), rep.Rejected)
	}
	// With the default tolerance the file is quarantined.
	if _, _, err := parse(t, []byte(csv), opts(t)); err == nil {
		t.Error("mostly bad file accepted")
	}
}

func TestDSTRowsRejected(t *testing.T) {
	// 2001-10-07 02:30 does not exist in Auckland; 2002-03-17 02:30 happens twice.
	csv := `Timestamp,Provider,Counter Name,Counter Type,Mean/Increment
10/07/2001 2:30:00 AM,System.Runtime,CPU Usage (%),Metric,1
03/17/2002 2:30:00 AM,System.Runtime,CPU Usage (%),Metric,1
03/17/2002 4:30:00 AM,System.Runtime,CPU Usage (%),Metric,1
`
	o := opts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep := must(t, []byte(csv), o)
	if len(evs) != 1 || rep.Rejected != 2 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	got := map[importer.Reason]bool{}
	for _, e := range rep.Errors {
		got[e.Reason] = true
	}
	if !got[importer.ReasonDSTNonexistent] || !got[importer.ReasonDSTAmbiguous] {
		t.Errorf("reasons %v", rep.Errors)
	}
}

func TestDateOrder(t *testing.T) {
	hdr := "Timestamp,Provider,Counter Name,Counter Type,Mean/Increment\n"
	row := func(d string) string { return d + " 10:00:00,System.Runtime,CPU Usage (%),Metric,1\n" }
	day := func(csv string) (int, int, importer.Report) {
		evs, rep := must(t, []byte(hdr+csv), opts(t))
		l := evs[0].Start.In(zone(t))
		return int(l.Month()), l.Day(), rep
	}
	// Slashes default to month first; a day above 12 confirms the order.
	if m, d, rep := day(row("03/04/2001")); m != 3 || d != 4 || !hasGap(rep, GapDateOrder) {
		t.Errorf("slash default %d/%d", m, d)
	}
	// Dots and hyphens default to day first.
	if m, d, _ := day(row("03.04.2001")); m != 4 || d != 3 {
		t.Errorf("dot default %d/%d", m, d)
	}
	if m, d, _ := day(row("03-04-2001")); m != 4 || d != 3 {
		t.Errorf("hyphen default %d/%d", m, d)
	}
	// Year first is unambiguous.
	if m, d, _ := day(row("2001-03-04")); m != 3 || d != 4 {
		t.Errorf("year first %d/%d", m, d)
	}
	// A day above 12 settles a slash file as day first, and a gap says the
	// earlier rows may be swapped.
	evs, rep := must(t, []byte(hdr+row("03/04/2001")+row("25/04/2001")+row("05/04/2001")), opts(t))
	if len(evs) != 3 || !hasGap(rep, GapDateMixed) {
		t.Errorf("events %d gaps %v", len(evs), rep.KnownGaps)
	}
	if l := evs[2].Start.In(zone(t)); l.Month() != 4 || l.Day() != 5 {
		t.Errorf("settled order: %v", l)
	}
	// Conflicting evidence rejects the row.
	o := opts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep = must(t, []byte(hdr+row("04/25/2001")+row("25/04/2001")), o)
	if len(evs) != 1 || rep.Rejected != 1 {
		t.Errorf("conflict: %d events %d rejected", len(evs), rep.Rejected)
	}
}

func TestQuotedAndCommaDates(t *testing.T) {
	csv := "Timestamp,Provider,Counter Name,Counter Type,Mean/Increment\n" +
		"\"10/25/2001, 2:00:00 PM\",System.Runtime,CPU Usage (%),Metric,1\n" +
		"10/25/2001, 2:00:10 PM,System.Runtime,CPU Usage (%),Metric,2\n" +
		"10/25/2001 14:00:20,System.Runtime,CPU Usage (%),Metric,3\n"
	evs, rep := must(t, []byte(csv), opts(t))
	if len(evs) != 3 || rep.Rejected != 0 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	if !evs[1].Start.Equal(evs[0].Start.Add(10*time.Second)) || !evs[2].Start.Equal(evs[0].Start.Add(20*time.Second)) {
		t.Errorf("times %v %v %v", evs[0].Start, evs[1].Start, evs[2].Start)
	}
}

func TestWindow(t *testing.T) {
	o := opts(t)
	from := time.Date(2001, 10, 25, 1, 0, 5, 0, time.UTC)
	o.Window = importer.Window{From: from}
	evs, rep := must(t, fixture(t, "counters_basic.csv"), o)
	if len(evs) != 3 || !hasGap(rep, GapWindow) { // the three readings at 2:00:10 PM
		t.Errorf("events %d gaps %v", len(evs), rep.KnownGaps)
	}
}

func TestJSONTruncatedAndLimits(t *testing.T) {
	full := fixture(t, "counters_basic.json")
	evs, rep, err := parse(t, full[:len(full)-60], opts(t))
	if err != nil || !hasGap(rep, GapTruncated) || len(evs) == 0 || len(evs) >= 5 {
		t.Errorf("truncated: %d events, gaps %v, %v", len(evs), rep.KnownGaps, err)
	}
	// One value over the line limit is a refusal, not a silent skip.
	o := opts(t)
	o.Limits.MaxLineBytes = 200
	_, _, err = parse(t, full, o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Errorf("long value: %v", err)
	}
	// An ordinary-length member skipped past does not count against the limit as a
	// whole file: a long TargetProcess within the limit passes.
	long := strings.Replace(string(full), `"job-alpha"`, `"`+strings.Repeat("a", 150)+`"`, 1)
	o.Limits.MaxLineBytes = 400
	if _, _, err := parse(t, []byte(long), o); err != nil {
		t.Errorf("long member within limit: %v", err)
	}
	for _, s := range []string{`[1,2]`, `{"Events":[1,"x",[2],null]}`, `{"A":1}`, `{`, `{"Events":`} {
		if _, _, err := parse(t, []byte(s), opts(t)); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	// Ending inside the Events array is a gap, not a refusal.
	if _, rep, err := parse(t, []byte(`{"TargetProcess":"job-alpha","Events":[`), opts(t)); err != nil || !hasGap(rep, GapTruncated) {
		t.Errorf("empty array cut short: %v %v", err, rep.KnownGaps)
	}
}

func TestJSONValues(t *testing.T) {
	doc := `{"TargetProcess":"job-alpha","Events":[
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":null},
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":"NaN"},
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":"7"},
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":1e400},
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":true},
{"timestamp":"2001-10-25T14:00:05+13:00","provider":"System.Runtime","name":"CPU Usage (%)","counterType":"Metric","value":8}
]}`
	o := opts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep := must(t, []byte(doc), o)
	if len(evs) != 2 || *evs[0].Value != 7 || *evs[1].Value != 8 || rep.Rejected != 1 {
		t.Errorf("events %d rejected %d", len(evs), rep.Rejected)
	}
}

func utf16le(s string, bom bool) []byte {
	u := utf16.Encode([]rune(s))
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func TestJSONEncodings(t *testing.T) {
	full := string(fixture(t, "counters_basic.json"))
	base, _ := must(t, []byte(full), opts(t))
	for name, data := range map[string][]byte{
		"utf8-bom": append([]byte{0xEF, 0xBB, 0xBF}, full...),
		"utf16-le": utf16le(full, true),
		"crlf":     []byte(strings.ReplaceAll(full, ",\"Events\"", ",\r\n\"Events\"")),
	} {
		evs, _, err := parse(t, data, opts(t))
		if err != nil || len(evs) != len(base) {
			t.Errorf("%s: %d events, %v", name, len(evs), err)
		}
	}
}

// TestSentinelBothFormats checks that nothing from a file's free text, including
// the process name and route tag, reaches an event, report, gap or error.
func TestSentinelBothFormats(t *testing.T) {
	for _, name := range []string{"bad_sentinel.csv", "bad_sentinel.json"} {
		data := fixture(t, name)
		if !bytes.Contains(data, []byte(sentinel)) {
			t.Fatalf("%s has no sentinel", name)
		}
		for _, frac := range []float64{0, 1} {
			o := opts(t)
			o.Limits.MaxRejectFraction = frac
			o.Supplied = map[string]string{"pid": "42"}
			evs, rep, err := parse(t, data, o)
			text := []string{}
			if err != nil {
				text = append(text, err.Error())
			}
			text = append(text, rep.KnownGaps...)
			for _, e := range rep.Errors {
				text = append(text, e.Error())
			}
			for _, r := range rep.FieldRules {
				text = append(text, r)
			}
			for _, e := range evs {
				text = append(text, e.Host, e.Kind, e.Unit, e.TZRule, e.Source)
				for k, v := range e.Attrs {
					text = append(text, k, v)
				}
			}
			for _, s := range text {
				if strings.Contains(s, sentinel) {
					t.Errorf("%s (reject fraction %g): sentinel in %q", name, frac, s)
				}
			}
			if frac == 1 && err == nil && len(evs) == 0 {
				t.Errorf("%s: nothing parsed", name)
			}
		}
	}
	// TargetProcess never becomes an attribute, even when it looks harmless.
	evs, _ := must(t, fixture(t, "counters_basic.json"), opts(t))
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, "job-alpha") || k == "process" {
				t.Errorf("process name kept: %s=%q", k, v)
			}
		}
	}
}

func TestGapsAreFactual(t *testing.T) {
	gaps := []string{GapLocalTime, GapDateOrder, GapDateMixed, GapCPU, GapCounters, GapUnits, GapTags, GapRates, GapNoValue, GapNoPID, GapTruncated, GapWindow}
	for _, g := range gaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("causal wording in gap %q: %v", g, err)
		}
	}
}
