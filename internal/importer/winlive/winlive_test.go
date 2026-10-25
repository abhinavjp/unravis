package winlive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const hdr = "# unravis-win-live v1 interval=15 cores=8\ntime,scope,process,pid,start,metric,value\n"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts() importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host: &importer.HostHint{Label: "web-a", Role: "iis", LogicalCores: 8},
	}
}

func parse(t testing.TB, data string) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), New(), strings.NewReader(data), opts())
}

func largeFile() []byte {
	var b strings.Builder
	b.WriteString(hdr)
	base := time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)
	start := base.Add(-8 * time.Hour).Format(TimeFormat)
	for i := 1; i <= 1200; i++ {
		ts := base.Add(time.Duration(i) * 15 * time.Second).Format(TimeFormat)
		fmt.Fprintf(&b, "%s,host,,,,host-cpu,%d\n", ts, i%100)
		fmt.Fprintf(&b, "%s,host,,,,mem-available,%d\n", ts, 8000000000+i)
		for j, name := range []string{"w3wp", "sqlservr", "node"} {
			fmt.Fprintf(&b, "%s,proc,%s,%d,%s,cpu-seconds,%d.5\n", ts, name, 1000+j, start, i*3+j)
			fmt.Fprintf(&b, "%s,proc,%s,%d,%s,private-bytes,%d\n", ts, name, 1000+j, start, 900000000+i)
		}
	}
	return []byte(b.String())
}

func fixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "good.csv"), Bad: fixture(t, "bad_sentinel.csv"), Large: largeFile(),
		Options: opts(), NoFreeText: true, NeverDetected: true,
	}
}

func TestConformance(t *testing.T) { importertest.Run(t, New(), fixtures(t)) }

func FuzzParse(f *testing.F) {
	f.Add(fixture(f, "good.csv"))
	f.Add(fixture(f, "bad_sentinel.csv"))
	f.Add([]byte(hdr + "2001-10-25T13:00:00Z,proc,w3wp,1,,cpu-seconds,1\n"))
	fx := fixtures(f)
	fx.Large = nil
	importertest.FuzzParse(f, New(), fx)
}

func at(h, m, s int) time.Time { return time.Date(2001, 10, 25, h, m, s, 0, time.UTC) }

func find(evs []evidence.Event, kind, metric, process string, t time.Time) *evidence.Event {
	for i, e := range evs {
		if e.Kind == kind && e.Attrs["metric"] == metric && e.Attrs["process"] == process && e.Start.Equal(t) {
			return &evs[i]
		}
	}
	return nil
}

func TestDetectIsAlwaysZero(t *testing.T) {
	for _, h := range [][]byte{fixture(t, "good.csv"), nil, []byte(hdr)} {
		if s := New().Detect(h, "win-live.csv"); s != 0 {
			t.Errorf("Detect = %d", s)
		}
	}
}

func TestEvents(t *testing.T) {
	evs, rep, err := parse(t, string(fixture(t, "good.csv")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 19 || rep.Rejected != 0 || len(evs) != 17 || rep.Emitted != 17 {
		t.Fatalf("rows %d rejected %d events %d emitted %d", rep.Rows, rep.Rejected, len(evs), rep.Emitted)
	}
	if !rep.First.Equal(at(13, 0, 15)) || !rep.Last.Equal(at(13, 0, 30)) {
		t.Errorf("coverage %v .. %v", rep.First, rep.Last)
	}
	for _, e := range evs {
		if e.Host != "web-a" || e.Source != Name {
			t.Errorf("event host %q source %q", e.Host, e.Source)
		}
	}
	// CPU is the change in cumulative seconds over the interval, in percent of
	// the host's 8 cores: (106-100)/15/8 = 5 percent.
	cpu := find(evs, "metric", "cpu", "w3wp", at(13, 0, 15))
	if cpu == nil || cpu.Value == nil || math.Abs(*cpu.Value-5) > 1e-9 || cpu.Unit != "percent-host" || cpu.Attrs["pid"] != "1200" {
		t.Fatalf("w3wp cpu %+v", cpu)
	}
	if c := find(evs, "metric", "cpu", "instance-1a2b3c4d", at(13, 0, 15)); c == nil || math.Abs(*c.Value-2.5) > 1e-9 {
		t.Errorf("instance cpu %+v", c)
	}
	if c := find(evs, "metric", "cpu", "w3wp", at(13, 0, 30)); c == nil || math.Abs(*c.Value-6.5/15*100/8) > 1e-9 {
		t.Errorf("second w3wp cpu %+v", c)
	}
	if e := find(evs, "metric", "private-bytes", "w3wp", at(13, 0, 15)); e == nil || *e.Value != 900000000 || e.Unit != "bytes" {
		t.Errorf("private bytes %+v", e)
	}
	if e := find(evs, "metric", "threads", "w3wp", at(13, 0, 15)); e == nil || *e.Value != 42 || e.Unit != "count" {
		t.Errorf("threads %+v", e)
	}
	if e := find(evs, "metric", "mem-available", "", at(13, 0, 15)); e == nil || *e.Value != 8589934592 || e.Unit != "bytes" {
		t.Errorf("mem-available %+v", e)
	}
	if e := find(evs, "metric", "host-cpu", "", at(13, 0, 30)); e == nil || *e.Value != 28 || e.Unit != "percent-host" {
		t.Errorf("host-cpu %+v", e)
	}
	if e := find(evs, "process-start", "", "sqlservr", at(13, 0, 10)); e == nil || !e.End.Equal(at(13, 0, 10)) {
		t.Errorf("process-start %+v", e)
	}
	gone := find(evs, "process-gone", "", "instance-1a2b3c4d", at(13, 0, 15))
	if gone == nil || !gone.End.Equal(at(13, 0, 30)) {
		t.Errorf("process-gone %+v", gone)
	}
	// The start event (13:00:10) is before the first sample: coverage does not
	// move back to it.
	if rep.First.Before(at(13, 0, 15)) {
		t.Errorf("coverage widened to %v", rep.First)
	}
	if !hasGap(rep, GapSampled) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	// An event of a series has the same attributes procsnap writes.
	for _, e := range evs {
		for k := range e.Attrs {
			if !New().AllowedAttrs()[k] {
				t.Errorf("attribute %q is not declared", k)
			}
		}
	}
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

// A CPU delta crosses a batch boundary through the carry rows: the first batch
// ends with a reading, the next one starts with it as a carry row, and the
// events from the two batches together equal the events of the one long batch.
func TestCarryOverCPUAcrossBatches(t *testing.T) {
	const start = "2001-10-25T08:00:00Z"
	row := func(ts string, sec int) string {
		return fmt.Sprintf("%s,proc,w3wp,1200,%s,cpu-seconds,%d\n", ts, start, sec)
	}
	one := hdr + row("2001-10-25T13:00:00Z", 100) + row("2001-10-25T13:00:15Z", 106) +
		row("2001-10-25T13:00:30Z", 112) + row("2001-10-25T13:00:45Z", 130)
	first := hdr + row("2001-10-25T13:00:00Z", 100) + row("2001-10-25T13:00:15Z", 106)
	second := hdr + fmt.Sprintf("2001-10-25T13:00:15Z,carry,w3wp,1200,%s,cpu-seconds,106\n", start) +
		row("2001-10-25T13:00:30Z", 112) + row("2001-10-25T13:00:45Z", 130)

	all, _, err := parse(t, one)
	if err != nil {
		t.Fatal(err)
	}
	e1, _, err := parse(t, first)
	if err != nil {
		t.Fatal(err)
	}
	e2, rep2, err := parse(t, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || len(e1) != 1 || len(e2) != 2 {
		t.Fatalf("events %d / %d + %d", len(all), len(e1), len(e2))
	}
	if rep2.Rows != 3 || rep2.Emitted != 2 {
		t.Errorf("second batch rows %d emitted %d: the carry row must emit nothing", rep2.Rows, rep2.Emitted)
	}
	if !rep2.First.Equal(at(13, 0, 30)) {
		t.Errorf("the carry row's time counts as coverage: first %v", rep2.First)
	}
	joined := append(append([]evidence.Event{}, e1...), e2...)
	for i := range all {
		a, b := all[i], joined[i]
		if !a.Start.Equal(b.Start) || *a.Value != *b.Value {
			t.Errorf("event %d: %v %v vs %v %v", i, a.Start, *a.Value, b.Start, *b.Value)
		}
	}
	// Without the carry row the first reading of the second batch has nothing to
	// subtract from, so its event is not made up.
	bare := hdr + row("2001-10-25T13:00:30Z", 112) + row("2001-10-25T13:00:45Z", 130)
	if e, _, err := parse(t, bare); err != nil || len(e) != 1 {
		t.Errorf("without carry: %d events, %v", len(e), err)
	}
}

func TestNegativeCPUDeltaIsSkipped(t *testing.T) {
	s := "2001-10-25T08:00:00Z"
	body := hdr +
		"2001-10-25T13:00:00Z,proc,node,5,\"" + s + "\",cpu-seconds,500\n" +
		"2001-10-25T13:00:15Z,proc,node,5,\"" + s + "\",cpu-seconds,10\n" +
		"2001-10-25T13:00:30Z,proc,node,5,\"" + s + "\",cpu-seconds,16\n"
	evs, rep, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !hasGap(rep, GapCPUSkipped) {
		t.Fatalf("%d events, gaps %v", len(evs), rep.KnownGaps)
	}
	if math.Abs(*evs[0].Value-6.0/15*100/8) > 1e-9 {
		t.Errorf("value %v", *evs[0].Value)
	}
}

func TestPidReuseIsANewSeries(t *testing.T) {
	body := hdr +
		"2001-10-25T13:00:00Z,proc,node,5,2001-10-25T08:00:00Z,cpu-seconds,500\n" +
		"2001-10-25T13:00:15Z,proc,node,5,2001-10-25T13:00:10Z,cpu-seconds,1\n" +
		"2001-10-25T13:00:30Z,proc,node,5,2001-10-25T13:00:10Z,cpu-seconds,4\n"
	evs, rep, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || hasGap(rep, GapCPUSkipped) {
		t.Errorf("%d events, gaps %v", len(evs), rep.KnownGaps)
	}
}

func TestCPUPassThrough(t *testing.T) {
	body := hdr + "2001-10-25T13:00:15Z,proc,w3wp,1200,2001-10-25T08:00:00Z,cpu,12.5\n"
	evs, _, err := parse(t, body)
	if err != nil || len(evs) != 1 || *evs[0].Value != 12.5 || evs[0].Attrs["metric"] != "cpu" {
		t.Fatalf("%v %+v", err, evs)
	}
}

func quarantine(err error, r importer.Reason) bool {
	var q *importer.QuarantineError
	return errors.As(err, &q) && q.Reason == r
}

// A process value that is neither a server image nor "instance-<hash>" refuses
// the file: a raw name never reaches the store, and the error does not echo it.
func TestParserRejectsRawProcessNames(t *testing.T) {
	for _, name := range []string{
		"chrome", "Chrome.exe", "w3wp.exe", "W3WP", "alice-laptop-agent", `C:\Users\alice\app.exe`,
		"instance-", "instance-XYZ", "instance-12", "instance-1a2b3c4d5e6f708192a", "other", "SENTINEL-7f3a",
	} {
		for _, scope := range []string{"proc", "carry", "mark"} {
			metric, value := "cpu-seconds", "1"
			if scope == "mark" {
				metric, value = "start", "0"
			}
			body := hdr + fmt.Sprintf("2001-10-25T13:00:15Z,%s,%s,7,2001-10-25T08:00:00Z,%s,%s\n", scope, name, metric, value)
			evs, _, err := parse(t, body)
			if !quarantine(err, importer.ReasonUnscrubbedColumn) {
				t.Errorf("%s/%q: err %v", scope, name, err)
			}
			if len(evs) != 0 {
				t.Errorf("%s/%q: %d events stored", scope, name, len(evs))
			}
			if err != nil && strings.Contains(err.Error(), "SENTINEL") {
				t.Errorf("error echoes the value: %v", err)
			}
		}
	}
	// A process name on a host row is refused too.
	if _, _, err := parse(t, hdr+"2001-10-25T13:00:15Z,host,chrome,,,host-cpu,5\n"); !quarantine(err, importer.ReasonUnscrubbedColumn) {
		t.Errorf("host row with a name: %v", err)
	}
	// The accepted forms.
	for _, name := range []string{"w3wp", "dotnet", "node", "redis-server", "memurai", "mongod", "sqlservr", "instance-1a2b3c4d", "instance-0123456789abcdef"} {
		if !ValidProcess(name) {
			t.Errorf("%q refused", name)
		}
	}
}

func TestUnknownColumnRefusesTheFile(t *testing.T) {
	for _, h := range []string{
		"time,scope,process,pid,start,metric,value,commandline\n",
		"time,scope,process,pid,start,metric,value,user\n",
		"time,scope,image,pid,start,metric,value\n",
	} {
		_, _, err := parse(t, "# unravis-win-live v1 interval=15 cores=8\n"+h+"2001-10-25T13:00:15Z,host,,,,host-cpu,5\n")
		if !quarantine(err, importer.ReasonUnscrubbedColumn) {
			t.Errorf("%q: %v", h, err)
		}
	}
	// Known columns in the wrong order or a missing one are refused as well.
	for _, h := range []string{
		"time,scope,process,pid,start,value,metric\n",
		"time,scope,process,pid,start,metric\n",
	} {
		_, _, err := parse(t, "# unravis-win-live v1 interval=15 cores=8\n"+h+"2001-10-25T13:00:15Z,host,,,,host-cpu,5\n")
		if err == nil {
			t.Errorf("%q accepted", h)
		}
	}
}

func TestHeaderErrors(t *testing.T) {
	cols := "time,scope,process,pid,start,metric,value\n"
	row := "2001-10-25T13:00:15Z,host,,,,host-cpu,5\n"
	for name, body := range map[string]string{
		"no comment line":   cols + row,
		"other version":     "# unravis-win-live v2 interval=15 cores=8\n" + cols + row,
		"interval 7":        "# unravis-win-live v1 interval=7 cores=8\n" + cols + row,
		"interval 0":        "# unravis-win-live v1 interval=0 cores=8\n" + cols + row,
		"zero cores":        "# unravis-win-live v1 interval=15 cores=0\n" + cols + row,
		"huge cores":        "# unravis-win-live v1 interval=15 cores=99999\n" + cols + row,
		"extra header text": "# unravis-win-live v1 interval=15 cores=8 user=alice\n" + cols + row,
		"no interval":       "# unravis-win-live v1 cores=8\n" + cols + row,
		"empty":             "",
		"comment only":      "# unravis-win-live v1 interval=15 cores=8",
	} {
		if _, _, err := parse(t, body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, iv := range []int{5, 15, 30, 60} {
		body := fmt.Sprintf("# unravis-win-live v1 interval=%d cores=8\n", iv) + cols + row
		if _, _, err := parse(t, body); err != nil {
			t.Errorf("interval %d: %v", iv, err)
		}
	}
}

func TestHeaderBuilderMatchesParser(t *testing.T) {
	body := Header(30, 4) + "\n" + ColumnLine() + "\n2001-10-25T13:00:15Z,host,,,,host-cpu,5\n"
	if _, _, err := parse(t, body); err != nil {
		t.Fatal(err)
	}
}

func TestHostIsRequired(t *testing.T) {
	o := opts()
	o.Host = nil
	_, _, err := importer.Collect(context.Background(), New(), strings.NewReader(hdr), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("err %v", err)
	}
}

// padding is n valid host rows in hour h, one every second from minute m.
func padding(n, h, m int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2001-10-25T%02d:%02d:%02dZ,host,,,,host-cpu,%d\n", h, m+i/60, i%60, i%100)
	}
	return b.String()
}

func TestRowRejects(t *testing.T) {
	const s = "2001-10-25T08:00:00Z"
	for name, row := range map[string]string{
		"local time":         "2001-10-25 13:00:15,host,,,,host-cpu,5",
		"no Z":               "2001-10-25T13:00:15,host,,,,host-cpu,5",
		"offset":             "2001-10-25T13:00:15+01:00,host,,,,host-cpu,5",
		"year 1970":          "1970-01-01T00:00:00Z,host,,,,host-cpu,5",
		"bad scope":          "2001-10-25T13:00:15Z,thread,,,,host-cpu,5",
		"host metric":        "2001-10-25T13:00:15Z,host,,,,swap,5",
		"proc metric":        "2001-10-25T13:00:15Z,proc,w3wp,1,2001-10-25T08:00:00Z,swap,5",
		"negative":           "2001-10-25T13:00:15Z,host,,,,host-cpu,-5",
		"NaN":                "2001-10-25T13:00:15Z,host,,,,host-cpu,NaN",
		"Inf":                "2001-10-25T13:00:15Z,host,,,,mem-available,Inf",
		"fraction bytes":     "2001-10-25T13:00:15Z,host,,,,mem-available,1.5",
		"text value":         "2001-10-25T13:00:15Z,host,,,,host-cpu,SENTINEL-7f3a",
		"host with pid":      "2001-10-25T13:00:15Z,host,,12,,host-cpu,5",
		"proc without pid":   "2001-10-25T13:00:15Z,proc,w3wp,,,working-set,5",
		"proc pid zero":      "2001-10-25T13:00:15Z,proc,w3wp,0,,working-set,5",
		"proc pid text":      "2001-10-25T13:00:15Z,proc,w3wp,abc,,working-set,5",
		"proc no name":       "2001-10-25T13:00:15Z,proc,,12,,working-set,5",
		"proc bad start":     "2001-10-25T13:00:15Z,proc,w3wp,12,yesterday,working-set,5",
		"mark without start": "2001-10-25T13:00:15Z,mark,w3wp,12,,start,0",
		"mark bad metric":    "2001-10-25T13:00:15Z,mark,w3wp,12," + s + ",stop,0",
		"carry bad metric":   "2001-10-25T13:00:15Z,carry,w3wp,12," + s + ",working-set,5",
		"carry no start":     "2001-10-25T13:00:15Z,carry,w3wp,12,,cpu-seconds,5",
	} {
		// 40 good rows before and after, so one reject stays under the limit.
		body := hdr + padding(40, 12, 0) + row + "\n" + padding(40, 14, 0)
		evs, rep, err := parse(t, body)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if rep.Rejected != 1 || len(evs) != 80 {
			t.Errorf("%s: rejected %d events %d (%v)", name, rep.Rejected, len(evs), rep.Errors)
		}
	}
}

func TestRowsOutOfOrderAndLateCarryAreRejected(t *testing.T) {
	body := hdr + padding(60, 13, 1) +
		"2001-10-25T13:00:15Z,host,,,,host-cpu,2\n" +
		"2001-10-25T13:02:45Z,carry,w3wp,1,2001-10-25T08:00:00Z,cpu-seconds,5\n"
	evs, rep, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 60 || rep.Rejected != 2 {
		t.Errorf("events %d rejected %d", len(evs), rep.Rejected)
	}
}

func TestProcessStartMayBeBlankForProcRows(t *testing.T) {
	evs, _, err := parse(t, hdr+"2001-10-25T13:00:15Z,proc,w3wp,1200,,working-set,5\n")
	if err != nil || len(evs) != 1 {
		t.Fatalf("%v %d", err, len(evs))
	}
}

func TestEventIDsAreUniqueAndOutputIsDeterministic(t *testing.T) {
	a, _, err := parse(t, string(fixture(t, "good.csv")))
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := parse(t, string(fixture(t, "good.csv")))
	seen := map[string]bool{}
	for i := range a {
		if seen[a[i].EvidenceID] {
			t.Errorf("duplicate id %s", a[i].EvidenceID)
		}
		seen[a[i].EvidenceID] = true
		if a[i].EvidenceID != b[i].EvidenceID {
			t.Errorf("event %d id differs between runs", i)
		}
		if err := a[i].Validate(New().AllowedAttrs()); err != nil {
			t.Errorf("event %d invalid: %v", i, err)
		}
	}
}

func TestEventsDoNotShareAttrMaps(t *testing.T) {
	evs, _, _ := parse(t, string(fixture(t, "good.csv")))
	evs[0].Attrs["x"] = "y"
	for i := 1; i < len(evs); i++ {
		if _, bad := evs[i].Attrs["x"]; bad {
			t.Fatalf("event %d shares an attribute map", i)
		}
	}
}

func TestGapSentencesPassLintCausal(t *testing.T) {
	for _, g := range []string{GapSampled, GapCPUSkipped} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestTrackedProcessCap(t *testing.T) {
	old := maxTracked
	maxTracked = 2
	defer func() { maxTracked = old }()
	var b bytes.Buffer
	b.WriteString(hdr)
	for _, ts := range []string{"2001-10-25T13:00:00Z", "2001-10-25T13:00:15Z"} {
		for pid := 1; pid <= 5; pid++ {
			fmt.Fprintf(&b, "%s,proc,node,%d,2001-10-25T08:00:00Z,cpu-seconds,%d\n", ts, pid, pid*10)
		}
	}
	evs, _, err := parse(t, b.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Errorf("%d events with 2 tracked processes", len(evs))
	}
}
