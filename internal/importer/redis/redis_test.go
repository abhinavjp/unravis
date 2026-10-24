package redis

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func slowOpts() importer.Options {
	o := hostOpts()
	o.Supplied = map[string]string{slowThresholdK: "10000", "exported-at": "2001-10-25T01:03:00Z"}
	return o
}

func TestSlowlogTreeEvents(t *testing.T) {
	evs, rep := mustCollect(t, NewSlowlog(), slowGood, slowOpts())
	if len(evs) != 2 || rep.Rows != 2 || rep.Rejected != 0 {
		t.Fatalf("events %d report %+v", len(evs), rep)
	}
	e := evs[0]
	end := time.Unix(1003971751, 0).UTC()
	if e.Kind != KindSlowCommand || e.Host != "redis-a" || !e.End.Equal(end) || !e.Start.Equal(end.Add(-15230*time.Microsecond)) {
		t.Fatalf("event: %+v", e)
	}
	if e.DurationMs == nil || math.Abs(*e.DurationMs-15.23) > 1e-9 || e.Unit != "ms" {
		t.Fatalf("duration: %+v", e)
	}
	if e.Attrs["command"] != "KEYS" || e.Attrs["argc"] != "2" || e.Attrs["id"] != "14" || e.Attrs["client"] != pseudo("client", "web-a-conn") {
		t.Fatalf("attrs: %v", e.Attrs)
	}
	e = evs[1]
	if e.Attrs["command"] != "SORT" || e.Attrs["argc"] != "4" || e.Attrs["id"] != "12" {
		t.Fatalf("attrs: %v", e.Attrs)
	}
	if _, ok := e.Attrs["client"]; ok {
		t.Fatalf("empty client name produced a client attribute: %v", e.Attrs)
	}
	if rep.Quality.Threshold != "commands slower than 10000 microseconds" || rep.Quality.TimezoneRule == "" {
		t.Fatalf("quality: %+v", rep.Quality)
	}
	if rep.Quality.CoverageLast == nil || !rep.Quality.CoverageLast.Equal(time.Date(2001, 10, 25, 1, 3, 0, 0, time.UTC)) {
		t.Fatalf("coverage ends at the export time: %+v", rep.Quality)
	}
	if !hasGap(rep, "ring buffer") || !hasGap(rep, "command execution only") {
		t.Fatalf("gaps: %q", rep.KnownGaps)
	}
}

func hasGap(rep importer.Report, sub string) bool {
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, sub) {
			return true
		}
	}
	return false
}

// The same reply as tree text, redis-cli --json and a RESP2 dump gives the same events.
func TestSlowlogFormsAgree(t *testing.T) {
	const tree = `1) 1) (integer) 14
   2) (integer) 1003971751
   3) (integer) 15230
   4) 1) "KEYS"
      2) "session:alice"
   5) "192.0.2.10:52144"
   6) "web-a-conn"
`
	const js = `[[14,1003971751,15230,["KEYS","session:alice"],"192.0.2.10:52144","web-a-conn"]]` + "\n"
	resp := "*1\r\n*6\r\n:14\r\n:1003971751\r\n:15230\r\n*2\r\n$4\r\nKEYS\r\n$13\r\nsession:alice\r\n$16\r\n192.0.2.10:52144\r\n$10\r\nweb-a-conn\r\n"
	var want *evidence.Event
	for name, in := range map[string]string{"tree": tree, "json": js, "resp": resp} {
		evs, rep := mustCollect(t, NewSlowlog(), in, slowOpts())
		if len(evs) != 1 || rep.Rejected != 0 {
			t.Fatalf("%s: %d events, %+v", name, len(evs), rep)
		}
		e := evs[0]
		if want == nil {
			want = &e
			continue
		}
		if !e.Start.Equal(want.Start) || !e.End.Equal(want.End) || *e.DurationMs != *want.DurationMs || fmt.Sprint(e.Attrs) != fmt.Sprint(want.Attrs) {
			t.Fatalf("%s differs: %+v vs %+v", name, e, *want)
		}
	}
}

func TestSlowlogMoreArgumentsMarker(t *testing.T) {
	in := `1) 1) (integer) 1
   2) (integer) 1003971751
   3) (integer) 100
   4) 1) "mset"
      2) "k1"
      3) "v1"
      4) "... (7 more arguments)"
   5) "192.0.2.10:1"
   6) ""
`
	evs, _ := mustCollect(t, NewSlowlog(), in, slowOpts())
	if len(evs) != 1 || evs[0].Attrs["argc"] != "10" || evs[0].Attrs["command"] != "MSET" {
		t.Fatalf("%+v", evs)
	}
}

func TestSlowlogUnknownCommandIsOther(t *testing.T) {
	in := `1) 1) (integer) 1
   2) (integer) 1003971751
   3) (integer) 100
   4) 1) "customcmd"
   5) "192.0.2.10:1"
   6) ""
`
	evs, _ := mustCollect(t, NewSlowlog(), in, slowOpts())
	if len(evs) != 1 || evs[0].Attrs["command"] != "other" {
		t.Fatalf("%+v", evs)
	}
}

func TestSlowlogRowRejects(t *testing.T) {
	bad := map[string]struct {
		body   string
		reason importer.Reason
	}{
		"time":     {"1) 1) (integer) 1\n   2) (integer) 5\n   3) (integer) 100\n   4) 1) \"GET\"\n", importer.ReasonBadTime},
		"duration": {"1) 1) (integer) 1\n   2) (integer) 1003971751\n   3) (integer) -4\n   4) 1) \"GET\"\n", importer.ReasonBadNumber},
		"id":       {"1) 1) (integer) -1\n   2) (integer) 1003971751\n   3) (integer) 4\n   4) 1) \"GET\"\n", importer.ReasonBadNumber},
		"shape":    {"1) 1) (integer) 1\n   2) (integer) 1003971751\n", importer.ReasonFieldCount},
		"noargs":   {"1) 1) (integer) 1\n   2) (integer) 1003971751\n   3) (integer) 4\n   4) (empty array)\n", importer.ReasonFieldCount},
	}
	good := treeEntry(1, 2, 3, 1003971760, 50, "GET", "k", "c")
	for name, c := range bad {
		o := slowOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := collect(t, NewSlowlog(), c.body+good, o)
		if err != nil || len(evs) != 1 || rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != c.reason {
			t.Errorf("%s: %v %d %+v", name, err, len(evs), rep)
		}
	}
}

func TestSlowlogDuplicateSnapshotsAreOneEvent(t *testing.T) {
	e := treeEntry(1, 1, 5, 1003971751, 2000, "GET", "k", "c")
	evs, rep := mustCollect(t, NewSlowlog(), e+"1) 1) (integer) 5\n   2) (integer) 1003971751\n   3) (integer) 2000\n   4) 1) \"GET\"\n   5) \"x\"\n   6) \"c\"\n", slowOpts())
	if len(evs) != 1 || rep.Rows != 2 {
		t.Fatalf("%d events, rows %d", len(evs), rep.Rows)
	}
}

func TestRawOutputIsRefused(t *testing.T) {
	raw := "14\n1003971751\n15230\nKEYS\nsession:alice\n192.0.2.10:52144\nweb-a-conn\n"
	for _, imp := range []importer.Importer{NewSlowlog(), NewLatency()} {
		evs, rep, err := collect(t, imp, raw, slowOpts())
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat || len(evs) != 0 {
			t.Fatalf("%s: %v", imp.Name(), err)
		}
		if !hasGap(rep, "--no-raw") {
			t.Fatalf("%s gaps: %q", imp.Name(), rep.KnownGaps)
		}
	}
}

func TestEmptyReplyIsAcceptedWithNoEvents(t *testing.T) {
	for _, in := range []string{"(empty array)\n", "(empty list or set)\n", "*0\r\n", "[]\n"} {
		evs, rep, err := collect(t, NewSlowlog(), in, slowOpts())
		if err != nil || len(evs) != 0 || rep.Rows != 1 || rep.Rejected != 0 {
			t.Errorf("%q: %v %d %+v", in, err, len(evs), rep)
		}
	}
}

func TestPrettyJSONIsRejectedNotMisread(t *testing.T) {
	in := "[\n  [\n    14,\n    1003971751\n  ]\n]\n"
	o := slowOpts()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewSlowlog(), in, o)
	if err != nil || len(evs) != 0 || rep.Rejected == 0 {
		t.Fatalf("%v %d %+v", err, len(evs), rep)
	}
}

func TestSlowlogPseudonymDroppedWhenUnusable(t *testing.T) {
	for name, f := range map[string]func(string, string) string{
		"nil":   nil,
		"empty": func(string, string) string { return "" },
		"long":  func(string, string) string { return strings.Repeat("a", 65) },
	} {
		o := slowOpts()
		o.Pseudonymize = f
		evs, _ := mustCollect(t, NewSlowlog(), slowGood, o)
		if _, ok := evs[0].Attrs["client"]; ok || len(evs) != 2 {
			t.Errorf("%s: %v", name, evs[0].Attrs)
		}
	}
}

func TestSlowlogHostRequired(t *testing.T) {
	o := slowOpts()
	o.Host = nil
	_, _, err := collect(t, NewSlowlog(), slowGood, o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("%v", err)
	}
}

func TestSlowlogWindow(t *testing.T) {
	o := slowOpts()
	o.Window = importer.Window{From: time.Unix(1003971745, 0).UTC()}
	evs, rep := mustCollect(t, NewSlowlog(), slowGood, o)
	if len(evs) != 1 || evs[0].Attrs["id"] != "14" || rep.Rows != 2 {
		t.Fatalf("%d events", len(evs))
	}
}

func TestSlowlogThresholdUnknownIsAGap(t *testing.T) {
	o := hostOpts()
	_, rep := mustCollect(t, NewSlowlog(), slowGood, o)
	if rep.Quality.Threshold != "unknown" || !hasGap(rep, "slowlog-log-slower-than unknown") || !hasGap(rep, "Export time unknown") {
		t.Fatalf("%+v %q", rep.Quality, rep.KnownGaps)
	}
}

func TestLatencyEvents(t *testing.T) {
	evs, rep := mustCollect(t, NewLatency(), latGood, func() importer.Options { o := hostOpts(); return o }())
	if len(evs) != 3 || rep.Rows != 3 {
		t.Fatalf("%d events, %+v", len(evs), rep)
	}
	want := []struct {
		ev  string
		ms  float64
		sec int64
	}{{"fork", 230, 1003971745}, {"fork", 180, 1003971800}, {"aof-fsync-always", 1180, 1003971750}}
	for i, w := range want {
		e := evs[i]
		if e.Kind != KindLatencySpike || e.Attrs["event"] != w.ev || e.Value == nil || *e.Value != w.ms || e.Unit != "ms" ||
			!e.Start.Equal(time.Unix(w.sec, 0).UTC()) || !e.End.Equal(e.Start) {
			t.Fatalf("event %d: %+v", i, e)
		}
	}
	if rep.Quality.Threshold != "unknown" || !hasGap(rep, "160 samples") {
		t.Fatalf("%+v %q", rep.Quality, rep.KnownGaps)
	}
}

func TestLatencyUnknownLabelIsOther(t *testing.T) {
	in := "# latency-event: not-a-real-event\n1) 1) (integer) 1003971745\n   2) (integer) 5\n"
	evs, rep := mustCollect(t, NewLatency(), in, hostOpts())
	if len(evs) != 1 || evs[0].Attrs["event"] != "other" || !hasGap(rep, "shown as other") {
		t.Fatalf("%+v", evs)
	}
	evs, _ = mustCollect(t, NewLatency(), "1) 1) (integer) 1003971745\n   2) (integer) 5\n", hostOpts())
	if len(evs) != 1 || evs[0].Attrs["event"] != "other" {
		t.Fatalf("%+v", evs)
	}
}

func TestLatencyJSONAndRESP(t *testing.T) {
	for name, in := range map[string]string{
		"json": "# latency-event: fork\n[[1003971745,230],[1003971800,180]]\n",
		"resp": "# latency-event: fork\r\n*2\r\n*2\r\n:1003971745\r\n:230\r\n*2\r\n:1003971800\r\n:180\r\n",
	} {
		evs, _ := mustCollect(t, NewLatency(), in, hostOpts())
		if len(evs) != 2 || evs[1].Attrs["event"] != "fork" || *evs[1].Value != 180 {
			t.Errorf("%s: %+v", name, evs)
		}
	}
}

func TestInfoEvents(t *testing.T) {
	evs, rep := mustCollect(t, NewInfo(), infoGood, hostOpts())
	if rep.Quality.SourceVersion != "7.2.4" || rep.Rejected != 0 {
		t.Fatalf("%+v", rep)
	}
	type key struct {
		metric string
		sec    int
	}
	got := map[key]evidence.Event{}
	var starts []evidence.Event
	for _, e := range evs {
		if e.Kind == KindRedisStart {
			starts = append(starts, e)
			continue
		}
		if e.Kind != KindMetric {
			t.Fatalf("kind %q", e.Kind)
		}
		got[key{e.Attrs["metric"], e.Start.Second() + 60*e.Start.Minute()}] = e
	}
	val := func(m string, sec int) float64 {
		e, ok := got[key{m, sec}]
		if !ok || e.Value == nil {
			t.Fatalf("no %s at %d", m, sec)
		}
		return *e.Value
	}
	if val("used_memory", 3600%3600) != 1048576 || val("latest_fork_usec", 0) != 2.3 || val("mem_fragmentation_ratio", 0) != 1.5 {
		t.Fatal("first sample values")
	}
	if got[key{"latest_fork_usec", 0}].Unit != "ms" || got[key{"used_memory", 0}].Unit != "bytes" || got[key{"instantaneous_ops_per_sec", 0}].Unit != "per-sec" {
		t.Fatal("units")
	}
	if _, ok := got[key{"used_cpu_sys", 0}]; ok {
		t.Fatal("a CPU rate needs two samples")
	}
	// 6 s of CPU in 60 s of wall time on one core: 10 percent; 12 s: 20 percent.
	if math.Abs(val("used_cpu_sys", 60)-10) > 1e-9 || math.Abs(val("used_cpu_user", 60)-20) > 1e-9 {
		t.Fatal("cpu rates")
	}
	if got[key{"used_cpu_sys", 60}].Unit != "percent-one-core" {
		t.Fatal("cpu unit")
	}
	if got[key{"used_memory", 0}].Attrs["role"] != "master" || got[key{"used_memory", 60}].Attrs["role"] != "replica" {
		t.Fatalf("roles: %v / %v", got[key{"used_memory", 0}].Attrs, got[key{"used_memory", 60}].Attrs)
	}
	if len(starts) != 1 || !starts[0].Start.Equal(time.Date(2001, 9, 9, 1, 0, 30, 0, time.UTC)) || starts[0].Attrs["role"] != "replica" {
		t.Fatalf("restart: %+v", starts)
	}
	if !hasGap(rep, "point-in-time snapshot") {
		t.Fatalf("%q", rep.KnownGaps)
	}
}

func TestInfoOffsetSampleTime(t *testing.T) {
	in := "# unravis-sample 2001-09-09T03:00:00+02:00\nused_memory:5\n"
	evs, rep := mustCollect(t, NewInfo(), in, hostOpts())
	if len(evs) != 1 || !evs[0].Start.Equal(time.Date(2001, 9, 9, 1, 0, 0, 0, time.UTC)) || evs[0].TZRule != "offset-in-field" || rep.FieldRules["ts"] != "offset-in-field" {
		t.Fatalf("%+v %+v", evs, rep.FieldRules)
	}
}

func TestInfoSentinelRoleAndErrors(t *testing.T) {
	in := "# unravis-sample 2001-09-09T01:00:00Z\n# Sentinel\nsentinel_masters:1\nused_memory:5\n"
	evs, _ := mustCollect(t, NewInfo(), in, hostOpts())
	if len(evs) != 1 || evs[0].Attrs["role"] != "sentinel" {
		t.Fatalf("%+v", evs)
	}
}

func TestInfoWithoutSampleLineIsRefused(t *testing.T) {
	plain := "# Server\nredis_version:7.2.4\nuptime_in_seconds:5\nused_memory:5\n"
	_, _, err := collect(t, NewInfo(), plain, hostOpts())
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("%v", err)
	}
}

func TestInfoBadNumbersAreRejectedRows(t *testing.T) {
	in := "# unravis-sample 2001-09-09T01:00:00Z\nused_memory:abc\nused_memory_rss:-3\nconnected_clients:1e3\nblocked_clients:4\nused_memory_peak:1.\n"
	o := hostOpts()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewInfo(), in, o)
	if err != nil || len(evs) != 1 || rep.Rejected != 4 || rep.Rows != 5 {
		t.Fatalf("%v %d %+v", err, len(evs), rep)
	}
}

func TestInfoDropsEverythingElse(t *testing.T) {
	in := "# unravis-sample 2001-09-09T01:00:00Z\nrun_id:" + sentinel + "\nconfig_file:/etc/redis.conf\nmaster_host:db.example.test\nos:Linux\ndb0:keys=1\nused_memory:5\n"
	evs, rep := mustCollect(t, NewInfo(), in, hostOpts())
	if len(evs) != 1 || rep.Rejected != 0 {
		t.Fatalf("%d events %+v", len(evs), rep)
	}
	if strings.Contains(fmt.Sprintf("%#v %#v", evs, rep), sentinel) {
		t.Fatal("dropped field reached the output")
	}
}

func TestInfoEventCeiling(t *testing.T) {
	o := hostOpts()
	o.Limits.MaxEvents = 5
	_, _, err := collect(t, NewInfo(), infoGood, o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "events" {
		t.Fatalf("%v", err)
	}
}

// Keys, arguments and addresses never reach an event, a report or an error, in any importer
// and any of the three reply forms.
func TestPrivacySentinelNeverOutput(t *testing.T) {
	forms := map[string]string{
		"tree": slowBad,
		"json": `[[14,1003971751,15230,["KEYS","session:` + sentinel + `"],"192.0.2.10:52144","` + sentinel + `"]]` + "\n",
		"resp": "*1\r\n*6\r\n:14\r\n:1003971751\r\n:15230\r\n*2\r\n$4\r\nKEYS\r\n$21\r\nsession:" + sentinel + "\r\n$16\r\n192.0.2.10:52144\r\n$13\r\n" + sentinel + "\r\n",
	}
	check := func(name string, evs []evidence.Event, rep importer.Report, err error) {
		out := fmt.Sprintf("%#v|%#v|%#v|%v", evs, rep, err, err)
		if strings.Contains(out, sentinel) || strings.Contains(out, "192.0.2") || strings.Contains(out, "session:") {
			t.Errorf("%s leaked: %s", name, out)
		}
	}
	for name, in := range forms {
		o := slowOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := collect(t, NewSlowlog(), in, o)
		check("slowlog/"+name, evs, rep, err)
		if name != "tree" && len(evs) != 1 {
			t.Errorf("%s: %d events", name, len(evs))
		}
		evs, rep, err = collect(t, NewLatency(), in, o)
		check("latency/"+name, evs, rep, err)
	}
	o := hostOpts()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewInfo(), infoBad, o)
	check("info", evs, rep, err)
	evs, rep, err = collect(t, NewLatency(), latBad, o)
	check("latency-bad", evs, rep, err)
	// With the default limits the file is quarantined; the error is still free of content.
	_, rep, err = collect(t, NewSlowlog(), slowBad, hostOpts())
	check("slowlog-quarantine", nil, rep, err)
	if err == nil {
		t.Error("slowBad should be quarantined by the default reject fraction")
	}
}

func TestDetectScores(t *testing.T) {
	cases := []struct {
		name string
		data string
		file string
		slow int
		lat  int
		info int
	}{
		{"slowlog", slowGood, "slowlog.txt", 90, 0, 0},
		{"latency labelled", latGood, "x.txt", 0, 95, 0},
		{"latency unlabelled", "1) 1) (integer) 1003971745\n   2) (integer) 5\n", "latency.txt", 0, 90, 0},
		{"info", infoGood, "redis-info.txt", 0, 0, 100},
		{"plain info", "# Server\nredis_version:7.2.4\n", "x.txt", 0, 0, 30},
		{"mongo", `{"t":{"$date":"2001-09-09T01:00:00Z"}}`, "mongod.log", 0, 0, 0},
		{"csv", "a,b,c\n1,2,3\n", "x.csv", 0, 0, 0},
	}
	for _, c := range cases {
		got := [3]int{NewSlowlog().Detect([]byte(c.data), c.file), NewLatency().Detect([]byte(c.data), c.file), NewInfo().Detect([]byte(c.data), c.file)}
		if got != [3]int{c.slow, c.lat, c.info} {
			t.Errorf("%s: %v want %v", c.name, got, [3]int{c.slow, c.lat, c.info})
		}
	}
	// A UTF-16 file is detected as the text Parse will read.
	if NewSlowlog().Detect(utf16le(slowGood), "") == 0 {
		t.Error("UTF-16 slowlog not detected")
	}
}

func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

// The caps are ceilings: a caller limit can lower them and never raise them.
func TestCapsAreCeilings(t *testing.T) {
	huge := importer.Limits{MaxBytes: 1 << 60, MaxRows: 1 << 60, MaxLineBytes: 1 << 40, MaxFields: 1 << 40, MaxEvents: 1 << 60}
	for _, imp := range []importer.Importer{NewSlowlog(), NewLatency(), NewInfo()} {
		d := imp.DefaultLimits()
		got := capLimits(huge, d)
		if got.MaxBytes != d.MaxBytes || got.MaxRows != d.MaxRows || got.MaxLineBytes != d.MaxLineBytes || got.MaxFields != d.MaxFields || (d.MaxEvents > 0 && got.MaxEvents != d.MaxEvents) {
			t.Errorf("%s: caps raised: %+v", imp.Name(), got)
		}
		low := capLimits(importer.Limits{MaxBytes: 10, MaxRows: 3, MaxLineBytes: 5, MaxFields: 2, MaxEvents: 7, MaxRejectFraction: 0.5}, d)
		if low.MaxBytes != 10 || low.MaxRows != 3 || low.MaxLineBytes != 5 || low.MaxFields != 2 || low.MaxEvents != 7 || low.MaxRejectFraction != 0.5 {
			t.Errorf("%s: caps not lowered: %+v", imp.Name(), low)
		}
	}
	if d := NewInfo().DefaultLimits(); d.MaxEvents <= 0 {
		t.Error("INFO has no event ceiling")
	}
}

// A tree line with a right-aligned index (a reply of 10 or more entries) stays in one reply.
func TestWideIndexReply(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		b.WriteString(treeEntry(2, i, int64(100-i), 1003971000+int64(i), 10, "GET", "k", ""))
	}
	evs, rep := mustCollect(t, NewSlowlog(), b.String(), slowOpts())
	if len(evs) != 12 || rep.Rejected != 0 {
		t.Fatalf("%d events %+v", len(evs), rep)
	}
}

// The command table, the latency events, the INFO metrics and the roles are the export
// policy's enums (internal/evidence/caseexport_enums.go): a name here that the export lacks
// would be shown as "[removed]".
func TestTablesMatchExportEnums(t *testing.T) {
	raw, err := os.ReadFile("../../evidence/caseexport_enums.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(raw), "\r", "")
	list := func(name string) []string {
		re := regexp.MustCompile(`(?s)\b` + name + `\s*=\s*fold\((.*?)\)\n`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s not found in the export enums", name)
		}
		var out []string
		for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
			out = append(out, q[1])
		}
		return out
	}
	same := func(what string, got, want []string) {
		g := append([]string(nil), got...)
		w := append([]string(nil), want...)
		sort.Strings(g)
		sort.Strings(w)
		if strings.Join(g, ",") != strings.Join(w, ",") {
			t.Errorf("%s differ:\n importer %v\n export   %v", what, g, w)
		}
	}
	same("commands", append([]string{Other}, CommandNames...), list("redisCommands"))
	same("latency events", append([]string{Other}, LatencyEvents...), list("redisLatencyEvents"))
	var metrics []string
	for _, s := range metricSpecs {
		metrics = append(metrics, s.key)
	}
	same("info metrics", metrics, list("redisInfoMetrics"))
	same("roles", []string{"master", "replica", "sentinel", "child"}, list("redisRoles"))
	for _, r := range []string{"master", "slave", "replica", "sentinel"} {
		if got := roleOf(r); got == "" || got == "slave" {
			t.Errorf("roleOf(%q) = %q", r, got)
		}
	}
	if roleOf("SENTINEL-7f3a") != "" || roleOf("") != "" {
		t.Error("unknown role accepted")
	}
}

func TestCanonicalCommand(t *testing.T) {
	for in, want := range map[string]string{
		"get": "GET", "Keys": "KEYS", "SORT_RO": "SORT_RO", "": Other, "nosuch": Other,
		strings.Repeat("a", 33): Other, sentinel: Other,
	} {
		if got := CanonicalCommand(in); got != want {
			t.Errorf("CanonicalCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// Hostile nesting and sizes are bounded and never panic.
func TestScannerBounds(t *testing.T) {
	o := slowOpts()
	o.Limits.MaxRejectFraction = 1
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + "\n"
	tree := strings.Repeat("1) ", 5000) + "(integer) 1\n"
	resp := strings.Repeat("*1\r\n", 5000)
	huge := "*9999999999\r\n"
	bulk := "*1\r\n$99999999999\r\n"
	long := "[[1," + strings.Repeat("9", 100000) + "]]\n"
	for name, in := range map[string]string{"json": deep, "tree": tree, "resp": resp, "huge": huge, "bulk": bulk, "long": long} {
		if _, _, err := collect(t, NewSlowlog(), in, o); err != nil {
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}
