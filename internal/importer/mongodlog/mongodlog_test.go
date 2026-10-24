package mongodlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	jn "api-audit-explorer/internal/join"
)

const sentinel = "SENTINEL-7f3a"

// ---- helpers -------------------------------------------------------------------------

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func logLineOf(ts string, id int, msg, attr string) string {
	return fmt.Sprintf(`{"t":{"$date":%q},"s":"I","c":"X","id":%d,"ctx":"c","msg":%q,"attr":%s}`, ts, id, msg, attr)
}

func slowLine(ts, ns, cmd string, durMs int) string {
	return logLineOf(ts, idSlowQuery, "Slow query", fmt.Sprintf(
		`{"type":"command","ns":%q,"command":{%q:"x","filter":{"k":%q}},"remote":"192.0.2.10:1","appName":%q,"durationMillis":%d}`,
		ns, cmd, sentinel, sentinel, durMs))
}

func opts() importer.Options {
	return importer.Options{
		CaseID: "caseabcdefgh", ImportNonce: "nonce-1", FileName: "mongod.log",
		Host: &importer.HostHint{Label: "mongo-a", Role: "mongo"},
	}
}

func hostsOpts() importer.Options {
	o := opts()
	o.Host = nil
	o.Hosts = func(observed string) (importer.HostHint, bool) {
		if observed == "mongo-a.example.test" {
			return importer.HostHint{Label: "mongo-a", Role: "mongo"}, true
		}
		return importer.HostHint{}, false
	}
	return o
}

func parse(t testing.TB, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), New(), bytes.NewReader(data), o)
}

// manyGood returns n valid slow-operation lines, one second apart from 01:00:00 on.
func manyGood(n int) []string {
	base := time.Date(2001, 9, 9, 1, 0, 0, 0, time.UTC)
	out := make([]string, n)
	for i := range out {
		out[i] = slowLine(base.Add(time.Duration(i)*time.Second).Format("2006-01-02T15:04:05.000+00:00"), "app.c", "find", 150)
	}
	return out
}

func logData(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }

// ---- good fixture ----------------------------------------------------------------------

func TestGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, readFixture(t, "good.log"), hostsOpts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Importer != Name || rep.Rows != 8 || rep.Emitted != 3 || rep.Rejected != 0 || len(evs) != 3 {
		t.Fatalf("report: %+v events %d", rep, len(evs))
	}
	if !reflect.DeepEqual(rep.ObservedHosts, []string{"mongo-a.example.test"}) {
		t.Fatalf("observed hosts: %v", rep.ObservedHosts)
	}
	if rep.FieldRules["t.$date"] != clock.RuleOffsetInField {
		t.Fatalf("field rules: %v", rep.FieldRules)
	}

	find := evs[0]
	end := time.Date(2001, 9, 9, 1, 46, 40, 123e6, time.UTC)
	if find.Kind != KindSlowOp || find.Source != Name || find.Host != "mongo-a" || find.Unit != "ms" ||
		find.TZRule != clock.RuleOffsetInField || find.Severity != "info" || find.Line != 5 {
		t.Fatalf("find event: %+v", find)
	}
	if !find.End.Equal(end) || !find.Start.Equal(end.Add(-250*time.Millisecond)) || *find.DurationMs != 250 {
		t.Fatalf("find times: %v %v %v", find.Start, find.End, *find.DurationMs)
	}
	wantAttrs := map[string]string{
		"type": "command", "ns": "app.orders", "command": "find", "planSummary": "IXSCAN",
		"keysExamined": "10", "docsExamined": "10", "nreturned": "10", "numYields": "0", "queryHash": "5C7B2E10",
	}
	if !reflect.DeepEqual(find.Attrs, wantAttrs) {
		t.Fatalf("find attrs: %v", find.Attrs)
	}

	// +13:00 offset resolves to UTC.
	ins := evs[1]
	if want := time.Date(2001, 9, 9, 1, 46, 41, 200e6, time.UTC); !ins.End.Equal(want) || ins.Attrs["command"] != "insert" {
		t.Fatalf("insert event: %+v", ins)
	}
	if evs[2].Attrs["planSummary"] != "COLLSCAN" || evs[2].Attrs["numYields"] != "5" {
		t.Fatalf("aggregate attrs: %v", evs[2].Attrs)
	}

	q := rep.Quality
	if q.SourceVersion != "7.0.2" || q.Threshold != "operations slower than 100 ms" || q.TimezoneRule != clock.RuleOffsetInField || q.Sampling != "" {
		t.Fatalf("quality: %+v", q)
	}
	if q.CoverageFirst == nil || !q.CoverageFirst.Equal(time.Date(2001, 9, 9, 1, 40, 0, 0, time.UTC)) ||
		q.CoverageLast == nil || !q.CoverageLast.Equal(time.Date(2001, 9, 9, 1, 55, 0, 0, time.UTC)) {
		t.Fatalf("coverage: %v %v", q.CoverageFirst, q.CoverageLast)
	}
	ids := map[string]bool{}
	for _, e := range evs {
		if err := e.Validate(New().AllowedAttrs()); err != nil {
			t.Fatal(err)
		}
		ids[e.EvidenceID] = true
	}
	if len(ids) != 3 {
		t.Fatal("event ids not unique")
	}
}

// Command bodies, filters, client, remote, appName and the sentinel are dropped at parse.
func TestDroppedFieldsNeverSurface(t *testing.T) {
	evs, rep, err := parse(t, readFixture(t, "good.log"), hostsOpts())
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(struct {
		E []evidence.Event
		R importer.Report
	}{evs, rep})
	for _, bad := range []string{sentinel, "192.0.2", "appName", "app-sentinel", "filter", "customer", "documents", "pipeline", "\"$db\"", "client", "remote", "locks"} {
		if strings.Contains(string(blob), bad) {
			t.Fatalf("output contains %q: %s", bad, blob)
		}
	}
}

// R8: cells starting with = + - @ tab or CR are data, kept verbatim (escaping happens at export).
func TestVerbatimCells(t *testing.T) {
	for _, ns := range []string{"db.=SUM(1)", "db.+x", "db.-x", "db.@x", "db.\tx", "db.\rx"} {
		line := logLineOf("2001-09-09T01:00:00.000+00:00", idSlowQuery, "Slow query",
			fmt.Sprintf(`{"type":"command","ns":%q,"durationMillis":5}`, ns))
		evs, _, err := parse(t, logData(line), opts())
		if err != nil || len(evs) != 1 {
			t.Fatalf("%q: %v %d", ns, err, len(evs))
		}
		if evs[0].Attrs["ns"] != ns {
			t.Fatalf("ns not verbatim: %q", evs[0].Attrs["ns"])
		}
	}
}

func TestCommandNameOnlyForCommandType(t *testing.T) {
	line := logLineOf("2001-09-09T01:00:00.000+00:00", idSlowQuery, "Slow query",
		`{"type":"update","ns":"db.c","command":{"q":{"a":1},"u":{"$set":{"b":2}}},"durationMillis":5}`)
	evs, _, err := parse(t, logData(line), opts())
	if err != nil || len(evs) != 1 {
		t.Fatal(err)
	}
	if _, ok := evs[0].Attrs["command"]; ok {
		t.Fatalf("command key kept for a legacy op: %v", evs[0].Attrs)
	}
	// A $truncated command marker is not a command name.
	line = logLineOf("2001-09-09T01:00:00.000+00:00", idSlowQuery, "Slow query",
		`{"type":"command","ns":"db.c","command":{"$truncated":"x"},"durationMillis":5}`)
	evs, _, _ = parse(t, logData(line), opts())
	if _, ok := evs[0].Attrs["command"]; ok {
		t.Fatalf("truncated marker kept: %v", evs[0].Attrs)
	}
}

func TestStageTokens(t *testing.T) {
	cases := map[string]string{
		"COLLSCAN":        "COLLSCAN",
		"IXSCAN { a: 1 }": "IXSCAN",
		"IXSCAN { a: 1, b: -1 }, IXSCAN { c: 1 }": "IXSCAN,IXSCAN",
		"IDHACK":              "IDHACK",
		"COUNT_SCAN { n: 1 }": "COUNT_SCAN",
		"collscan":            "",
		"Collscan":            "",
		"":                    "",
		"IXSCAN { a: \"" + sentinel + "\" }, COLLSCAN": "IXSCAN,COLLSCAN",
		"SENTINEL-7f3a": "",
	}
	for in, want := range cases {
		if got := stageTokens(in); got != want {
			t.Errorf("stageTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkingMillisKept(t *testing.T) {
	data := logData(
		logLineOf("2001-09-09T01:00:00.000+00:00", idBuildInfo, "Build Info", `{"buildInfo":{"version":"8.0.1"}}`),
		logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query", `{"type":"command","ns":"db.c","durationMillis":300,"workingMillis":120}`),
	)
	evs, rep, err := parse(t, data, opts())
	if err != nil || len(evs) != 1 {
		t.Fatal(err)
	}
	if evs[0].Attrs["workingMs"] != "120" || rep.Quality.SourceVersion != "8.0.1" {
		t.Fatalf("%v %+v", evs[0].Attrs, rep.Quality)
	}
}

// ---- hosts ----------------------------------------------------------------------------------

func TestHostResolution(t *testing.T) {
	// Unknown observed host refuses the file.
	o := hostsOpts()
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	if _, _, err := parse(t, readFixture(t, "good.log"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("unknown observed host: %v", err)
	}
	// No picked host and no way to map: a slow op has no label, so the file is refused.
	o = hostsOpts()
	o.Hosts = nil
	if _, _, err := parse(t, readFixture(t, "good.log"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("unmappable host: %v", err)
	}
	// A file with no "MongoDB starting" line needs a picked host.
	noStart := logData(slowLine("2001-09-09T01:00:00.000+00:00", "a.b", "find", 200))
	if _, _, err := parse(t, noStart, hostsOpts()); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("no host anywhere: %v", err)
	}
	evs, rep, err := parse(t, noStart, opts())
	if err != nil || len(evs) != 1 || evs[0].Host != "mongo-a" {
		t.Fatalf("picked host: %v %v", err, evs)
	}
	// A picked host means no raw name is reported for the service to refuse.
	_, rep, err = parse(t, readFixture(t, "good.log"), opts())
	if err != nil || len(rep.ObservedHosts) != 0 {
		t.Fatalf("observed with picked host: %v %v", err, rep.ObservedHosts)
	}
}

// ---- time ----------------------------------------------------------------------------------

func TestRowRejections(t *testing.T) {
	good := manyGood(40)
	ts := "2001-09-09T02:00:00.000+00:00"
	cases := []struct {
		name   string
		line   string
		reason importer.Reason
	}{
		{"naive time", slowLine("2001-09-09T02:00:00.000", "a.b", "find", 200), importer.ReasonTZUnlabelled},
		{"garbage time", slowLine("yesterday", "a.b", "find", 200), importer.ReasonBadTime},
		{"year out of range", slowLine("1969-09-09T02:00:00.000+00:00", "a.b", "find", 200), importer.ReasonBadTime},
		{"negative duration", slowLine(ts, "a.b", "find", -5), importer.ReasonBadNumber},
		{"huge duration", slowLine(ts, "a.b", "find", 2_000_000_000_000), importer.ReasonBadNumber},
		{"missing duration", logLineOf(ts, idSlowQuery, "Slow query", `{"type":"command","ns":"a.b"}`), importer.ReasonBadNumber},
		{"string duration", logLineOf(ts, idSlowQuery, "Slow query", `{"type":"command","ns":"a.b","durationMillis":"7"}`), importer.ReasonBadNumber},
		{"truncated json", slowLine(ts, "a.b", "find", 200)[:60], importer.ReasonFieldCount},
		{"not an object", `[1,2,3]`, importer.ReasonFieldCount},
		{"missing time", `{"s":"I","c":"X","id":51803,"msg":"Slow query","attr":{"durationMillis":4}}`, importer.ReasonBadTime},
		{"missing attr", logLineOf(ts, idSlowQuery, "Slow query", `null`), importer.ReasonFieldCount},
		{"attr not an object", logLineOf(ts, idSlowQuery, "Slow query", `"x"`), importer.ReasonFieldCount},
	}
	for _, c := range cases {
		lines := append(append([]string{}, good[:20]...), c.line)
		lines = append(lines, good[20:]...)
		evs, rep, err := parse(t, logData(lines...), opts())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Rejected != 1 || len(evs) != 40 || len(rep.Errors) != 1 ||
			rep.Errors[0].Reason != c.reason || rep.Errors[0].Line != 21 {
			t.Fatalf("%s: rejected %d events %d errors %+v", c.name, rep.Rejected, len(evs), rep.Errors)
		}
	}
}

func TestRejectFractionQuarantines(t *testing.T) {
	lines := append(manyGood(10), `{"truncated`, `{"truncated`)
	_, rep, err := parse(t, logData(lines...), opts())
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction || !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("got %v", err)
	}
	if rep.Rejected != 2 {
		t.Fatalf("rejected %d", rep.Rejected)
	}
}

func TestTextFormatUnsupported(t *testing.T) {
	_, _, err := parse(t, readFixture(t, "text-format.log"), opts())
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("got %v", err)
	}
	if got := New().Detect(readFixture(t, "text-format.log"), "mongod.log"); got != 0 {
		t.Fatalf("detect text format: %d", got)
	}
}

// ---- conformance (mirrors wave plan 1.6; swap for importertest.Run once F0 lands) ------------

func TestDetect(t *testing.T) {
	good := readFixture(t, "good.log")
	if got := New().Detect(good, "mongod.log"); got < 90 {
		t.Fatalf("good detect %d", got)
	}
	if got := New().Detect(good, "x.dat"); got < 80 || got > 100 {
		t.Fatalf("good detect, other name %d", got)
	}
	for name, head := range map[string][]byte{
		"csv":   []byte("a,b,c\n1,2,3\n"),
		"empty": nil,
		"zip":   append([]byte("PK\x03\x04"), good...),
		"gzip":  append([]byte{0x1f, 0x8b, 8}, good...),
		"json":  []byte(`{"a":1}` + "\n"),
	} {
		if got := New().Detect(head, "mongod.log"); got != 0 {
			t.Errorf("%s: detect %d", name, got)
		}
	}
	// 64 KiB head that cuts the first line short still detects.
	long := logLineOf("2001-09-09T01:00:00.000+00:00", idSlowQuery, "Slow query", `{"durationMillis":1,"x":"`+strings.Repeat("a", 100000)+`"}`)
	if got := New().Detect([]byte(long)[:65536], "mongod.log"); got < 80 {
		t.Fatalf("cut head: %d", got)
	}
}

func TestEncodingsYieldIdenticalEvents(t *testing.T) {
	good := readFixture(t, "good.log")
	want, wrep, err := parse(t, good, hostsOpts())
	if err != nil {
		t.Fatal(err)
	}
	u16, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes(good)
	if err != nil {
		t.Fatal(err)
	}
	variants := map[string][]byte{
		"bom":      append([]byte{0xEF, 0xBB, 0xBF}, good...),
		"utf16le":  u16,
		"crlf":     bytes.ReplaceAll(good, []byte("\n"), []byte("\r\n")),
		"no-final": bytes.TrimRight(good, "\n"),
	}
	for name, data := range variants {
		if name == "utf16le" && (data[0] != 0xFF || data[1] != 0xFE) {
			t.Fatalf("encoder did not write a BOM")
		}
		if got := New().Detect(data, "mongod.log"); got == 0 {
			t.Errorf("%s: detect 0", name)
		}
		evs, rep, err := parse(t, data, hostsOpts())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(evs, want) || rep.Rows != wrep.Rows || !reflect.DeepEqual(rep.Quality, wrep.Quality) {
			t.Fatalf("%s differs from utf-8", name)
		}
	}
}

func TestArchivesRefused(t *testing.T) {
	good := readFixture(t, "good.log")
	for name, head := range map[string][]byte{
		"zip":  []byte("PK\x03\x04"),
		"gzip": {0x1f, 0x8b, 8, 0},
		"7z":   {'7', 'z', 0xBC, 0xAF, 0x27, 0x1C},
		"bz2":  []byte("BZh9"),
	} {
		evs, _, err := parse(t, append(head, good...), opts())
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonArchive || !strings.Contains(err.Error(), "extract first") || len(evs) != 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLimits(t *testing.T) {
	data := logData(manyGood(10)...)

	o := opts()
	o.Limits = importer.Limits{MaxRows: 3}
	evs, _, err := parse(t, data, o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "rows" || !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("rows: %v", err)
	}
	if len(evs) > 3 {
		t.Fatalf("emitted %d past the row limit", len(evs))
	}

	o = opts()
	o.Limits = importer.Limits{MaxBytes: 500}
	if _, _, err = parse(t, data, o); !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("bytes: %v", err)
	}

	// A line over 256 KiB refuses the file; nothing is emitted after it.
	huge := logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query",
		`{"durationMillis":1,"x":"`+strings.Repeat("a", 300<<10)+`"}`)
	lines := manyGood(2)
	lines = append(lines, huge)
	lines = append(lines, manyGood(5)...)
	evs, _, err = parse(t, logData(lines...), opts())
	if !errors.As(err, &le) || le.Limit != "line" || !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("line: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("emitted %d events, want only the 2 before the long line", len(evs))
	}
	// Exactly at the limit still parses.
	pad := maxLineLen - len(logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query", `{"durationMillis":1,"x":""}`))
	ok := logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query", `{"durationMillis":1,"x":"`+strings.Repeat("a", pad)+`"}`)
	if len(ok) != maxLineLen {
		t.Fatalf("test line is %d bytes", len(ok))
	}
	if evs, _, err = parse(t, logData(ok), opts()); err != nil || len(evs) != 1 {
		t.Fatalf("at limit: %v", err)
	}
	// Caller limits above the card caps do not raise them.
	o = opts()
	o.Limits = importer.Limits{MaxBytes: 1 << 50, MaxRows: 1 << 50, MaxLineBytes: 1 << 30}
	if eff := effectiveLimits(o.Limits); eff.MaxBytes != 2<<30 || eff.MaxRows != 10_000_000 || eff.MaxLineBytes != 256<<10 {
		t.Fatalf("caps raised: %+v", eff)
	}
}

func TestDegenerateInputsNeverPanic(t *testing.T) {
	good := readFixture(t, "good.log")
	inputs := [][]byte{nil, {}, []byte("x"), []byte("{"), []byte("\n\n\n"), []byte("\xef\xbb\xbf"), []byte("\xff\xfe"), []byte("\xff\xfe{"), {0}, []byte("}{")}
	for _, n := range []int{1, 2, 17, 60, 200, 333, 701, len(good) - 1} {
		inputs = append(inputs, good[:n])
	}
	for i, in := range inputs {
		evs, _, err := parse(t, in, hostsOpts())
		if err != nil {
			if !errors.Is(err, importer.ErrQuarantine) && !errors.Is(err, importer.ErrLimit) && !errors.Is(err, importer.ErrUnknownHost) {
				t.Errorf("input %d: unexpected error type %v", i, err)
			}
			continue
		}
		for _, e := range evs {
			if e.Validate(New().AllowedAttrs()) != nil {
				t.Errorf("input %d: invalid event", i)
			}
		}
	}
	// Empty and 1-byte give a clean quarantine.
	for _, in := range [][]byte{nil, []byte("x"), []byte("{")} {
		if _, _, err := parse(t, in, opts()); !errors.Is(err, importer.ErrQuarantine) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// infiniteLog yields valid lines forever; the first Read cancels the context.
type infiniteLog struct {
	cancel  context.CancelFunc
	buf     []byte
	started bool
}

func (r *infiniteLog) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		r.cancel()
	}
	if len(r.buf) == 0 {
		r.buf = []byte(slowLine("2001-09-09T01:00:00.000+00:00", "a.b", "find", 9) + "\n")
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func TestContextCancelStopsParse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	_, _, err := importer.Collect(ctx, New(), &infiniteLog{cancel: cancel}, opts())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	cancelled, c2 := context.WithCancel(context.Background())
	c2()
	if _, _, err := importer.Collect(cancelled, New(), bytes.NewReader(logData(manyGood(3)...)), opts()); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: %v", err)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("importer made a network call")
	return nil, errors.New("network disabled")
}

func TestNoNetwork(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(t, readFixture(t, "good.log"), hostsOpts()); err != nil {
		t.Fatal(err)
	}
}

// ---- source quality and wording --------------------------------------------------------------

func TestSlowmsSources(t *testing.T) {
	noOpts := logData(
		logLineOf("2001-09-09T01:00:00.000+00:00", idBuildInfo, "Build Info", `{"buildInfo":{"version":"7.0.2"}}`),
		slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200),
	)
	// Unknown: nothing in the file, nothing supplied.
	_, rep, err := parse(t, noOpts, opts())
	if err != nil || rep.Quality.Threshold != "" || !hasGap(rep, "slowms unknown") {
		t.Fatalf("unknown slowms: %v %+v %v", err, rep.Quality, rep.KnownGaps)
	}
	// Supplied by the owner.
	o := opts()
	o.Supplied = map[string]string{"slowms": "50"}
	_, rep, _ = parse(t, noOpts, o)
	if rep.Quality.Threshold != "operations slower than 50 ms" {
		t.Fatalf("supplied: %+v", rep.Quality)
	}
	// Junk supplied values stay unknown.
	for _, v := range []string{"abc", "-1", "99999999999", ""} {
		o.Supplied = map[string]string{"slowms": v}
		if _, rep, _ = parse(t, noOpts, o); rep.Quality.Threshold != "" {
			t.Fatalf("junk %q accepted", v)
		}
	}
	// The file wins; a difference is noted.
	o.Supplied = map[string]string{"slowms": "50"}
	_, rep, _ = parse(t, readFixture(t, "good.log"), withHostsFrom(o))
	if rep.Quality.Threshold != "operations slower than 100 ms" || !hasGap(rep, "owner-supplied slowms differs") {
		t.Fatalf("conflict: %+v %v", rep.Quality, rep.KnownGaps)
	}
}

func withHostsFrom(o importer.Options) importer.Options {
	h := hostsOpts()
	o.Host, o.Hosts = nil, h.Hosts
	return o
}

func hasGap(rep importer.Report, sub string) bool {
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, sub) {
			return true
		}
	}
	return false
}

func TestMarkersAreGaps(t *testing.T) {
	base := func(extra ...string) []byte {
		lines := []string{
			logLineOf("2001-09-09T01:00:00.000+00:00", idBuildInfo, "Build Info", `{"buildInfo":{"version":"7.0.2"}}`),
			logLineOf("2001-09-09T01:00:01.000+00:00", idOptions, "Options set by command line", `{"options":{"operationProfiling":{"slowOpThresholdMs":100}}}`),
			slowLine("2001-09-09T01:00:05.000+00:00", "a.b", "find", 200),
		}
		return logData(append(lines, extra...)...)
	}
	cases := map[string]struct {
		extra string
		gap   string
	}{
		"rotation id":   {logLineOf("2001-09-09T01:10:00.000+00:00", idRotated, msgRotated, `{}`), samplingRotated},
		"rotation text": {logLineOf("2001-09-09T01:10:00.000+00:00", 1, msgRotated, `{}`), samplingRotated},
		"restart":       {logLineOf("2001-09-09T01:10:00.000+00:00", idStarting, "MongoDB starting", `{"host":"x"}`), samplingRestart},
		"profiler":      {logLineOf("2001-09-09T01:10:00.000+00:00", 2, msgProfilerChanged, `{}`), samplingChanged},
		"sampled":       {logLineOf("2001-09-09T01:10:00.000+00:00", idOptions, "Options set by command line", `{"options":{"operationProfiling":{"slowOpSampleRate":0.5}}}`), samplingSampled},
	}
	clean, rep, err := parse(t, base(), opts())
	if err != nil || len(clean) != 1 || rep.Quality.Sampling != "" {
		t.Fatalf("clean base: %v %+v", err, rep.Quality)
	}
	iv := join2("2001-09-09T01:00:02Z", "2001-09-09T01:00:04Z")
	if got := Absence(rep.Quality, iv); got != evidence.NotObservedThreshold("mongod.log", "operations slower than 100 ms") {
		t.Fatalf("clean absence: %s", got)
	}
	for name, c := range cases {
		_, rep, err := parse(t, base(c.extra), opts())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rep.Quality.Sampling != c.gap || !hasGap(rep, c.gap) {
			t.Fatalf("%s: sampling %q gaps %v", name, rep.Quality.Sampling, rep.KnownGaps)
		}
		iv := join2("2001-09-09T01:00:02Z", "2001-09-09T01:00:04Z")
		if got := Absence(rep.Quality, iv); got != evidence.NotObservedGap(c.gap) {
			t.Fatalf("%s: absence %q", name, got)
		}
	}
	// A "MongoDB starting" line at the head of a file is normal, not a restart.
	_, rep, _ = parse(t, readFixture(t, "good.log"), hostsOpts())
	if rep.Quality.Sampling != "" {
		t.Fatalf("head starting line flagged: %q", rep.Quality.Sampling)
	}
}

var causal = []string{"caused", "because", "waiting on", "blocked by", "due to", "proven", "strong correlation"}

func assertNoCausal(t *testing.T, s string) {
	t.Helper()
	low := strings.ToLower(s)
	for _, w := range causal {
		if strings.Contains(low, w) {
			t.Fatalf("causal wording %q in %q", w, s)
		}
	}
}

func TestGapSentencesAreFixedAndNotCausal(t *testing.T) {
	_, rep, err := parse(t, readFixture(t, "good.log"), hostsOpts())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rep.KnownGaps {
		assertNoCausal(t, g)
		if strings.Contains(g, sentinel) {
			t.Fatal("sentinel in gap")
		}
	}
	for _, s := range []string{samplingSampled, samplingRotated, samplingRestart, samplingChanged} {
		assertNoCausal(t, s)
	}
}

func TestWindowFilter(t *testing.T) {
	o := hostsOpts()
	o.Window = importer.Window{
		From: time.Date(2001, 9, 9, 1, 46, 41, 0, time.UTC),
		To:   time.Date(2001, 9, 9, 1, 47, 0, 0, time.UTC),
	}
	evs, rep, err := parse(t, readFixture(t, "good.log"), o)
	if err != nil || len(evs) != 1 || evs[0].Attrs["command"] != "insert" || rep.Emitted != 1 {
		t.Fatalf("window: %v %d", err, len(evs))
	}
}

// ---- joins: R8-B5 ------------------------------------------------------------------------------

func join2(start, end string) joinInterval {
	s, _ := time.Parse(time.RFC3339, start)
	e, _ := time.Parse(time.RFC3339, end)
	return joinInterval{ID: "audit-1", Start: s, End: e}
}

// R8-B5: two overlapping slow inserts on one collection are ambiguous, never counted.
func TestR8B5TwoOverlappingInsertsAreAmbiguous(t *testing.T) {
	data := logData(
		// 01:00:08.000 ending, 2.5 s long: [01:00:05.5, 01:00:08]
		slowLine("2001-09-09T01:00:08.000+00:00", "app.audit", "insert", 2500),
		// 01:00:09.000 ending, 3 s long: [01:00:06, 01:00:09]
		slowLine("2001-09-09T01:00:09.000+00:00", "app.audit", "insert", 3000),
	)
	evs, _, err := parse(t, data, opts())
	if err != nil || len(evs) != 2 {
		t.Fatal(err)
	}
	m := M1([]joinInterval{join2("2001-09-09T01:00:06Z", "2001-09-09T01:00:07Z")}, evs)
	if len(m) != 1 || m[0].Class != evidence.JoinAmbiguous || len(m[0].Candidates) != 2 || m[0].IntervalID != "audit-1" {
		t.Fatalf("match: %+v", m)
	}
	if got := evidence.OverlapAmbiguous(len(m[0].Candidates)); got != "Possible overlap (ambiguous: 2 candidates)." {
		t.Fatalf("wording: %q", got)
	}
	if n := countUnique(m); n != 0 {
		t.Fatalf("ambiguous counted: %d", n)
	}
	assertNoCausal(t, evidence.OverlapAmbiguous(2))
}

func TestR8B5OneInsertIsUnique(t *testing.T) {
	evs, _, err := parse(t, logData(slowLine("2001-09-09T01:00:08.000+00:00", "app.audit", "insert", 2500)), opts())
	if err != nil {
		t.Fatal(err)
	}
	m := M1([]joinInterval{join2("2001-09-09T01:00:06Z", "2001-09-09T01:00:07Z")}, evs)
	if len(m) != 1 || m[0].Class != evidence.JoinUnique || !reflect.DeepEqual(m[0].Candidates, []string{evs[0].EvidenceID}) {
		t.Fatalf("match: %+v", m)
	}
	if countUnique(m) != 1 {
		t.Fatal("unique not counted")
	}
}

func TestM1NoneAndTolerance(t *testing.T) {
	// Event [01:00:05, 01:00:08].
	evs, _, err := parse(t, logData(slowLine("2001-09-09T01:00:08.000+00:00", "app.audit", "insert", 3000)), opts())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, s, e string
		want       evidence.JoinClass
	}{
		{"inside", "2001-09-09T01:00:06Z", "2001-09-09T01:00:07Z", evidence.JoinUnique},
		{"ends 0.9 s before start (within 1 s)", "2001-09-09T01:00:04Z", "2001-09-09T01:00:04.1Z", evidence.JoinUnique},
		{"starts 0.9 s after end (within 1 s)", "2001-09-09T01:00:08.9Z", "2001-09-09T01:00:09Z", evidence.JoinUnique},
		{"1.5 s apart", "2001-09-09T01:00:03Z", "2001-09-09T01:00:03.5Z", evidence.JoinNone},
		{"far away", "2001-09-09T05:00:00Z", "2001-09-09T05:00:01Z", evidence.JoinNone},
	}
	for _, c := range cases {
		m := M1([]joinInterval{join2(c.s, c.e)}, evs)
		if m[0].Class != c.want {
			t.Errorf("%s: %v", c.name, m[0])
		}
	}
	// Events from elsewhere or of another kind never qualify.
	other := evs[0]
	other.Source = "iis-w3c"
	other2 := evs[0]
	other2.Kind = "request"
	if m := M1([]joinInterval{join2("2001-09-09T01:00:06Z", "2001-09-09T01:00:07Z")}, []evidence.Event{other, other2}); m[0].Class != evidence.JoinNone {
		t.Fatalf("foreign events matched: %+v", m[0])
	}
}

// R8-B5: no Build Info means the gap wording, not the threshold wording.
func TestR8B5NoBuildInfoGivesGapWording(t *testing.T) {
	noBuild := logData(
		logLineOf("2001-09-09T01:00:01.000+00:00", idOptions, "Options set by command line", `{"options":{"operationProfiling":{"slowOpThresholdMs":100}}}`),
		slowLine("2001-09-09T01:00:50.000+00:00", "a.b", "find", 200),
	)
	_, rep, err := parse(t, noBuild, opts())
	if err != nil {
		t.Fatal(err)
	}
	iv := join2("2001-09-09T01:00:10Z", "2001-09-09T01:00:20Z")
	got := Absence(rep.Quality, iv)
	if want := evidence.NotObservedGap("mongod version unknown: no Build Info line in the file"); got != want {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "only records") {
		t.Fatalf("threshold wording used without a version: %q", got)
	}
	assertNoCausal(t, got)
}

func TestAbsenceRequiresEveryQualityField(t *testing.T) {
	_, rep, err := parse(t, readFixture(t, "good.log"), hostsOpts())
	if err != nil {
		t.Fatal(err)
	}
	full := rep.Quality
	iv := join2("2001-09-09T01:41:00Z", "2001-09-09T01:42:00Z")
	want := evidence.NotObservedThreshold("mongod.log", "operations slower than 100 ms")
	if got := Absence(full, iv); got != want {
		t.Fatalf("full: %q", got)
	}
	// Exactly the coverage edges still count as covered.
	if got := Absence(full, join2("2001-09-09T01:40:00Z", "2001-09-09T01:55:00Z")); got != want {
		t.Fatalf("edges: %q", got)
	}

	mut := map[string]func(q *evidence.SourceQuality){
		"no version":   func(q *evidence.SourceQuality) { q.SourceVersion = "" },
		"no threshold": func(q *evidence.SourceQuality) { q.Threshold = "" },
		"no tz rule":   func(q *evidence.SourceQuality) { q.TimezoneRule = "" },
		"host-local":   func(q *evidence.SourceQuality) { q.TimezoneRule = clock.RuleHostLocal },
		"sampling":     func(q *evidence.SourceQuality) { q.Sampling = samplingRotated },
		"no coverage":  func(q *evidence.SourceQuality) { q.CoverageFirst, q.CoverageLast = nil, nil },
	}
	for name, f := range mut {
		q := full
		f(&q)
		got := Absence(q, iv)
		if !strings.HasPrefix(got, "Not observed (gap or sampling: ") || strings.Contains(got, "only records") {
			t.Errorf("%s: %q", name, got)
		}
		assertNoCausal(t, got)
	}
	// Interval partly outside coverage.
	for _, i := range []joinInterval{
		join2("2001-09-09T01:39:59Z", "2001-09-09T01:42:00Z"),
		join2("2001-09-09T01:50:00Z", "2001-09-09T01:55:01Z"),
		join2("2001-09-09T08:00:00Z", "2001-09-09T08:00:05Z"),
	} {
		if got := Absence(full, i); !strings.HasPrefix(got, "Not observed (gap or sampling: ") {
			t.Errorf("outside coverage: %q", got)
		}
	}
}

// ---- fuzz ----------------------------------------------------------------------------------------

func FuzzParse(f *testing.F) {
	good, err := os.ReadFile("testdata/good.log")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add(good[:300])
	f.Add([]byte(slowLine("2001-09-09T01:00:08.000+00:00", "app.audit", "insert", 2500)))
	f.Add([]byte("2001-09-09T01:40:00.000+0000 I  CONTROL  [initandlisten] text"))
	f.Add([]byte(`{"t":{"$date":"2001-09-09T01:00:00.000"},"id":51803,"attr":{"durationMillis":-1}}`))
	f.Add([]byte("\xff\xfe{\x00\"\x00"))
	valid := New().AllowedAttrs()
	f.Fuzz(func(t *testing.T, data []byte) {
		o := opts()
		o.Limits = importer.Limits{MaxBytes: 1 << 20, MaxRows: 1000, MaxLineBytes: 8 << 10}
		ids := map[string]bool{}
		_, err := New().Parse(context.Background(), bytes.NewReader(data), o, func(e evidence.Event) error {
			if err := e.Validate(valid); err != nil {
				t.Fatalf("invalid event: %v", err)
			}
			if ids[e.EvidenceID] {
				t.Fatalf("duplicate id")
			}
			ids[e.EvidenceID] = true
			return nil
		})
		if err != nil && strings.Contains(err.Error(), "{") {
			t.Fatalf("error carries content: %v", err)
		}
	})
}

type joinInterval = jn.Interval

func countUnique(m []jn.Match) int { return jn.CountUnique(m) }
