package procsnap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

const getProcessHeader = `"SampleUtc","ProcessName","Id","CPU","WorkingSet64","PrivateMemorySize64","HandleCount","Threads","StartTimeUtc"` + "\n"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts(cores int) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host: &importer.HostHint{Label: "web-a", Role: "iis", LogicalCores: cores},
		Pseudonymize: func(kind, value string) string {
			sum := sha256.Sum256([]byte(kind + "\x00" + value))
			return kind + "-" + hex.EncodeToString(sum[:4])
		},
	}
}

func parse(t testing.TB, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), New(), bytes.NewReader(data), o)
}

// largeFile is a Get-Process file of more than 2*importer.HeadSize bytes whose
// snapshots are in time order (the default Large repeats Good, which is not).
func largeFile() []byte {
	var b strings.Builder
	b.WriteString(getProcessHeader)
	base := time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)
	start := base.Add(-8 * time.Hour).Format("2006-01-02T15:04:05.0000000Z")
	for i := 0; i < 700; i++ {
		ts := base.Add(time.Duration(i) * 15 * time.Second).Format("2006-01-02T15:04:05.0000000Z")
		for j, name := range []string{"w3wp", "sqlservr", "node", "clientapp"} {
			fmt.Fprintf(&b, "%q,%q,%q,%q,%q,%q,%q,%q,%q\n", ts, name, fmt.Sprint(1000+j),
				fmt.Sprintf("%d.5", i*3+j), fmt.Sprint(1000000000+i*1000+j), fmt.Sprint(900000000+i), fmt.Sprint(900+j), fmt.Sprint(40+j), start)
		}
	}
	return []byte(b.String())
}

func fixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, "getprocess_basic.csv"),
		Bad:        fixture(t, "bad_sentinel.csv"),
		Large:      largeFile(),
		Options:    opts(8),
		NoFreeText: true, // process is a server image or a derived label, never a cell
	}
}

func TestConformance(t *testing.T) {
	importertest.Run(t, New(), fixtures(t))
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"getprocess_basic.csv", "tasklist_basic.csv", "bad_sentinel.csv"} {
		f.Add(fixture(f, n))
	}
	f.Add([]byte("\xff\xfeS\x00a\x00"))
	f.Add([]byte(getProcessHeader + `"2001-10-25T13:00:00Z","x","1","","","","","",""` + "\n"))
	fx := fixtures(f)
	fx.Large = nil
	importertest.FuzzParse(f, New(), fx)
}

func byKind(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

func at(h, m, s int) time.Time { return time.Date(2001, 10, 25, h, m, s, 0, time.UTC) }

func TestGetProcessEvents(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 15 || rep.Rejected != 0 || rep.Emitted != int64(len(evs)) || len(evs) != 76 {
		t.Fatalf("rows %d rejected %d emitted %d events %d", rep.Rows, rep.Rejected, rep.Emitted, len(evs))
	}
	if n := len(byKind(evs, "metric")); n != 68 {
		t.Errorf("%d metric events", n)
	}
	for _, e := range evs {
		if e.Source != Name || e.Host != "web-a" || e.TZRule != clock.RuleUTCField || e.Severity != "" {
			t.Fatalf("event fields %+v", e)
		}
		if _, has := e.Attrs["pid"]; !has || e.Attrs["process"] == "" {
			t.Fatalf("attrs %v", e.Attrs)
		}
		for k := range e.Attrs {
			if !New().AllowedAttrs()[k] {
				t.Errorf("attr %q is not allowlisted", k)
			}
		}
	}
	// cpu: 7.5 CPU seconds over 15 s is 50 percent of one core, 6.25 of eight.
	var w3, sql []float64
	for _, e := range byKind(evs, "metric") {
		if e.Attrs["metric"] != "cpu" {
			continue
		}
		if e.Unit != "percent-host" {
			t.Errorf("cpu unit %q", e.Unit)
		}
		switch e.Attrs["process"] {
		case "w3wp":
			w3 = append(w3, *e.Value)
		case "sqlservr":
			sql = append(sql, *e.Value)
		}
	}
	if fmt.Sprint(w3) != "[6.25 6.25]" || fmt.Sprint(sql) != "[5 5 5]" {
		t.Errorf("cpu w3wp %v sqlservr %v", w3, sql)
	}
	cpuEvents := 0
	for _, e := range byKind(evs, "metric") {
		if e.Attrs["metric"] == "cpu" {
			cpuEvents++
		}
	}
	if cpuEvents != 8 {
		t.Errorf("%d cpu events, want 8 (no baseline for a first snapshot, none for a blank CPU cell)", cpuEvents)
	}
	// Units of the other metrics.
	units := map[string]string{}
	for _, e := range byKind(evs, "metric") {
		units[e.Attrs["metric"]] = e.Unit
	}
	for m, u := range map[string]string{"working-set": "bytes", "private-bytes": "bytes", "handles": "count", "threads": "count"} {
		if units[m] != u {
			t.Errorf("unit of %s is %q, want %q", m, units[m], u)
		}
	}
	// A blank CPU cell is missing, not zero: w3wp has no cpu event at 13:00:45.
	for _, e := range byKind(evs, "metric") {
		if e.Attrs["process"] == "w3wp" && e.Attrs["metric"] == "cpu" && e.Start.Equal(at(13, 0, 45)) {
			t.Errorf("blank cpu cell produced %v", *e.Value)
		}
	}
	// Names: server images stay, a client image is an instance label, a name that
	// is not an image name is "other".
	names := map[string]bool{}
	for _, e := range evs {
		names[e.Attrs["process"]] = true
	}
	if !names["w3wp"] || !names["sqlservr"] || !names["node"] || !names["other"] {
		t.Errorf("names %v", names)
	}
	var inst string
	for n := range names {
		if strings.HasPrefix(n, "instance-") {
			inst = n
		}
		if strings.Contains(strings.ToLower(n), "payroll") || strings.Contains(strings.ToLower(n), "acme") {
			t.Errorf("client name %q stored", n)
		}
	}
	if inst == "" {
		t.Errorf("no instance label: %v", names)
	}
	for _, g := range []string{GapSnapshotsOnly, GapGoneWindow} {
		if !hasGap(rep, g) {
			t.Errorf("missing gap %q", g)
		}
	}
	if hasGap(rep, GapNoCores) || hasGap(rep, GapNoStartTime) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField || rep.FieldRules["SampleUtc"] != clock.RuleUTCField {
		t.Errorf("rule %+v %+v", rep.Quality, rep.FieldRules)
	}
}

func TestProcessStartDoesNotWidenCoverage(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	starts := byKind(evs, "process-start")
	if len(starts) != 6 {
		t.Fatalf("%d process-start events", len(starts))
	}
	early := false
	for _, e := range starts {
		if !e.End.Equal(e.Start) || e.Value != nil || e.Unit != "" {
			t.Errorf("start event %+v", e)
		}
		if e.Start.Before(at(13, 0, 0)) {
			early = true
		}
		if _, has := e.Attrs["metric"]; has {
			t.Errorf("process-start carries a metric attr")
		}
	}
	if !early {
		t.Error("no start time before the first snapshot in the fixture")
	}
	if !rep.First.Equal(at(13, 0, 0)) || !rep.Last.Equal(at(13, 0, 45)) {
		t.Errorf("coverage %v to %v", rep.First, rep.Last)
	}
	// A process is announced once, not at every snapshot.
	seen := map[string]int{}
	for _, e := range starts {
		seen[e.Attrs["process"]+"/"+e.Attrs["pid"]+"/"+e.Start.String()]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s announced %d times", k, n)
		}
	}
}

func TestProcessGoneIsAnIntervalBetweenSnapshots(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	gone := byKind(evs, "process-gone")
	if len(gone) != 2 {
		t.Fatalf("%d process-gone events", len(gone))
	}
	for _, e := range gone {
		// Last listed at 13:00:15; the first snapshot without it is 13:00:30.
		if !e.Start.Equal(at(13, 0, 15)) || !e.End.Equal(at(13, 0, 30)) {
			t.Errorf("gone window %v to %v", e.Start, e.End)
		}
		if e.Value != nil || e.DurationMs != nil {
			t.Errorf("gone event holds a value: %+v", e)
		}
	}
	// w3wp, sqlservr and node are in the last snapshot; none of them is gone.
	for _, e := range gone {
		if p := e.Attrs["process"]; p == "w3wp" || p == "sqlservr" || p == "node" {
			t.Errorf("%s reported gone", p)
		}
	}
	if !hasGap(rep, GapGoneWindow) {
		t.Error("no gap for the exit window")
	}
}

func TestGoneAtEndOfFile(t *testing.T) {
	data := getProcessHeader +
		`"2001-10-25T13:00:00Z","w3wp","1240","1","10","10","1","1",""` + "\n" +
		`"2001-10-25T13:00:00Z","node","1241","1","10","10","1","1",""` + "\n" +
		`"2001-10-25T13:00:15Z","w3wp","1240","2","10","10","1","1",""` + "\n"
	evs, _, err := parse(t, []byte(data), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	gone := byKind(evs, "process-gone")
	if len(gone) != 1 || gone[0].Attrs["process"] != "node" || !gone[0].Start.Equal(at(13, 0, 0)) || !gone[0].End.Equal(at(13, 0, 15)) {
		t.Fatalf("gone %+v", gone)
	}
	if len(byKind(evs, "process-start")) != 0 {
		t.Error("a blank StartTimeUtc produced a process-start")
	}
}

// A process id that comes back with another start time is a new process: no
// CPU is computed across the two, and the new one is announced.
func TestPidReuseIsANewSeries(t *testing.T) {
	data := getProcessHeader +
		`"2001-10-25T13:00:00Z","app","200","100.0","10","10","1","1","2001-10-25T08:00:00Z"` + "\n" +
		`"2001-10-25T13:00:15Z","app","200","0.5","10","10","1","1","2001-10-25T13:00:10Z"` + "\n"
	evs, _, err := parse(t, []byte(data), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Attrs["metric"] == "cpu" {
			t.Errorf("cpu across a reused process id: %v", *e.Value)
		}
	}
	if n := len(byKind(evs, "process-start")); n != 2 {
		t.Errorf("%d process-start events", n)
	}
	gone := byKind(evs, "process-gone")
	if len(gone) != 1 || !gone[0].Start.Equal(at(13, 0, 0)) || !gone[0].End.Equal(at(13, 0, 15)) {
		t.Errorf("gone %+v", gone)
	}
}

func TestNegativeCPUDeltaIsSkipped(t *testing.T) {
	data := getProcessHeader +
		`"2001-10-25T13:00:00Z","w3wp","1","100.0","10","10","1","1",""` + "\n" +
		`"2001-10-25T13:00:15Z","w3wp","1","3.0","10","10","1","1",""` + "\n" +
		`"2001-10-25T13:00:30Z","w3wp","1","10.5","10","10","1","1",""` + "\n"
	evs, rep, err := parse(t, []byte(data), opts(1))
	if err != nil {
		t.Fatal(err)
	}
	var cpu []float64
	for _, e := range evs {
		if e.Attrs["metric"] == "cpu" {
			cpu = append(cpu, *e.Value)
		}
	}
	// The pair that went down is skipped and rebaselines: 7.5 s over 15 s is 50 percent.
	if fmt.Sprint(cpu) != "[50]" {
		t.Errorf("cpu %v", cpu)
	}
	if !hasGap(rep, GapCPUSkipped) || rep.Rejected != 0 {
		t.Errorf("gaps %v rejected %d", rep.KnownGaps, rep.Rejected)
	}
}

func TestUnknownCoresGivesPercentOfOneCore(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(0))
	if err != nil {
		t.Fatal(err)
	}
	var w3 []float64
	for _, e := range evs {
		if e.Attrs["metric"] != "cpu" {
			continue
		}
		if e.Unit != "percent-one-core" {
			t.Errorf("unit %q", e.Unit)
		}
		if e.Attrs["process"] == "w3wp" {
			w3 = append(w3, *e.Value)
		}
	}
	if fmt.Sprint(w3) != "[50 50]" || !hasGap(rep, GapNoCores) {
		t.Errorf("w3wp %v gaps %v", w3, rep.KnownGaps)
	}
}

func TestTasklist(t *testing.T) {
	evs, rep, err := parse(t, fixture(t, "tasklist_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 7 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d", rep.Rows, rep.Rejected)
	}
	// Only working-set events (bytes, KiB times 1024); "N/A" is missing; no start.
	var ws []float64
	for _, e := range byKind(evs, "metric") {
		if e.Attrs["metric"] != "working-set" || e.Unit != "bytes" {
			t.Errorf("metric %q unit %q", e.Attrs["metric"], e.Unit)
		}
		if e.Attrs["process"] == "w3wp" {
			ws = append(ws, *e.Value)
		}
	}
	if fmt.Sprint(ws) != fmt.Sprint([]float64{1887436 * 1024, 1887500 * 1024}) {
		t.Errorf("w3wp working set %v", ws)
	}
	if n := len(byKind(evs, "process-start")); n != 0 {
		t.Errorf("%d process-start events from tasklist", n)
	}
	gone := byKind(evs, "process-gone")
	if len(gone) != 1 || !strings.HasPrefix(gone[0].Attrs["process"], "instance-") ||
		!gone[0].Start.Equal(at(13, 0, 0)) || !gone[0].End.Equal(at(13, 0, 15)) {
		t.Errorf("gone %+v", gone)
	}
	if !hasGap(rep, GapNoStartTime) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	for _, e := range evs {
		for _, v := range e.Attrs {
			if v == "Services" || v == "Console" {
				t.Errorf("session column leaked: %q", v)
			}
		}
	}
}

func TestMemUsageBytes(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    float64
		present bool
		ok      bool
	}{
		{"1,887,436 K", 1887436 * 1024, true, true},
		{"1.887.436 K", 1887436 * 1024, true, true},
		{"1\u00a0887\u00a0436 K", 1887436 * 1024, true, true},
		{"12 K", 12 * 1024, true, true},
		{"N/A", 0, false, true},
		{"", 0, false, true},
		{"12", 0, false, false},
		{"K", 0, false, false},
		{"1e5 K", 0, false, false},
		{"-5 K", 0, false, false},
		{"1,2x3 K", 0, false, false},
		{"99999999999999999999 K", 0, false, false},
	} {
		v, present, ok := memUsageBytes(c.in)
		if present != c.present || ok != c.ok || (present && v != c.want) {
			t.Errorf("memUsageBytes(%q) = %v, %v, %v", c.in, v, present, ok)
		}
	}
}

func TestImageName(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"w3wp", "w3wp", true},
		{"W3WP.EXE", "w3wp", true},
		{"redis-server.exe", "redis-server", true},
		{"Acme.Billing#1", "acme.billing#1", true},
		{"Acme Billing", "", false},
		{`C:\Program Files\x.exe`, "", false},
		{"", "", false},
		{".exe", "", false},
		{strings.Repeat("a", 65), "", false},
		{strings.Repeat("a", 64), strings.Repeat("a", 64), true},
		{"naïve", "", false},
	} {
		got, ok := imageName(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("imageName(%q) = %q, %v", c.in, got, ok)
		}
	}
}

func TestPseudonymizeIsUsedForClientImages(t *testing.T) {
	data := getProcessHeader +
		`"2001-10-25T13:00:00Z","PayrollSvc.exe","1","","10","","","",""` + "\n" +
		`"2001-10-25T13:00:00Z","w3wp.exe","2","","10","","","",""` + "\n" +
		`"2001-10-25T13:00:00Z","C:\x\payroll.exe","3","","10","","","",""` + "\n"
	o := opts(8)
	var calls []string
	o.Pseudonymize = func(kind, value string) string {
		calls = append(calls, kind+":"+value)
		return "instance-abcd1234"
	}
	evs, _, err := parse(t, []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range evs {
		got[e.Attrs["pid"]] = e.Attrs["process"]
	}
	if got["1"] != "instance-abcd1234" || got["2"] != "w3wp" || got["3"] != "other" {
		t.Errorf("names %v", got)
	}
	// Only the lower-cased image name of the client process reaches Pseudonymize.
	if fmt.Sprint(calls) != "[instance:payrollsvc]" {
		t.Errorf("Pseudonymize calls %v", calls)
	}
	// Without a pseudonymiser, or when it returns nothing, the name is "other".
	for _, f := range []func(string, string) string{nil, func(string, string) string { return "" }} {
		o.Pseudonymize = f
		evs, _, err = parse(t, []byte(data), o)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Attrs["pid"] == "1" && e.Attrs["process"] != "other" {
				t.Errorf("process %q", e.Attrs["process"])
			}
		}
	}
}

func TestQuarantinedColumns(t *testing.T) {
	for _, c := range []struct{ col, want string }{
		{"UserName", "UserName"}, {"User Name", "UserName"}, {"Window Title", "WindowTitle"},
		{"MainWindowTitle", "MainWindowTitle"}, {"CommandLine", "CommandLine"}, {"Path", "Path"},
		{"Company", "Company"}, {"Description", "Description"}, {"Services", "Services"}, {"Modules", "Modules"},
	} {
		data := fmt.Sprintf("\"SampleUtc\",\"ProcessName\",\"Id\",\"WorkingSet64\",%q\n", c.col) +
			`"2001-10-25T13:00:00Z","w3wp","1","10","` + sentinel + `"` + "\n"
		evs, rep, err := parse(t, []byte(data), opts(8))
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnscrubbedColumn || qe.Column != c.want || len(evs) != 0 {
			t.Errorf("%s: %d events, %v", c.col, len(evs), err)
			continue
		}
		if strings.Contains(fmt.Sprint(err, rep), sentinel) {
			t.Errorf("%s: cell text in the error or report", c.col)
		}
	}
	// tasklist /v adds User Name and Window Title.
	data := `"SampleUtc","Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"` + "\n" +
		`"2001-10-25T13:00:00Z","w3wp.exe","1","Services","0","10 K","Running","` + sentinel + `","0:00:01","` + sentinel + `"` + "\n"
	if _, _, err := parse(t, []byte(data), opts(8)); !isQuarantine(err, importer.ReasonUnscrubbedColumn) {
		t.Errorf("tasklist /v: %v", err)
	}
}

func isQuarantine(err error, r importer.Reason) bool {
	var qe *importer.QuarantineError
	return errors.As(err, &qe) && qe.Reason == r
}

func TestHeaderErrors(t *testing.T) {
	row := `"2001-10-25T13:00:00Z","w3wp","1","10"` + "\n"
	for name, hdr := range map[string]string{
		"missing Id":          `"SampleUtc","ProcessName","WorkingSet64","X"`,
		"missing ProcessName": `"SampleUtc","Id","WorkingSet64","X"`,
		"missing SampleUtc":   `"Time","ProcessName","Id","WorkingSet64"`,
		"duplicate Id":        `"SampleUtc","ProcessName","Id","Id"`,
		"duplicate CPU":       `"SampleUtc","ProcessName","Id","CPU","cpu"`,
		"tasklist no PID":     `"SampleUtc","Image Name","Mem Usage","X"`,
	} {
		_, _, err := parse(t, []byte(hdr+"\n"+row), opts(8))
		if !isQuarantine(err, importer.ReasonUnsupportedFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHostIsRequired(t *testing.T) {
	o := opts(8)
	o.Host = nil
	if evs, _, err := parse(t, fixture(t, "getprocess_basic.csv"), o); !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Errorf("%d events, %v", len(evs), err)
	}
	o.Host = &importer.HostHint{}
	if _, _, err := parse(t, fixture(t, "getprocess_basic.csv"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("empty label: %v", err)
	}
}

func TestCultureTimeRefusesTheFile(t *testing.T) {
	data := getProcessHeader + `"10/25/2001 1:00:00 PM","w3wp","1","1","10","10","1","1",""` + "\n"
	evs, rep, err := parse(t, []byte(data), opts(8))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonBadTime || qe.Column != "SampleUtc" || len(evs) != 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if !hasGap(rep, GapTimeFormat) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
}

func TestRowRejects(t *testing.T) {
	// 20 good rows, the row under test (line 22), then 20 good rows of the next snapshot.
	lines := func(bad string) string {
		return getProcessHeader + filler("13:00:00", 100, 0) + bad + filler("13:00:15", 100, 0)
	}
	for name, c := range map[string]struct {
		row    string
		reason importer.Reason
	}{
		"local time":      {`"2001-10-25T13:00:00+02:00","w3wp","2","1","10","10","1","1",""` + "\n", importer.ReasonBadTime},
		"no zone":         {`"2001-10-25T13:00:00","w3wp","2","1","10","10","1","1",""` + "\n", importer.ReasonBadTime},
		"year 1601":       {`"1601-01-01T00:00:00Z","w3wp","2","1","10","10","1","1",""` + "\n", importer.ReasonBadTime},
		"earlier time":    {`"2001-10-25T12:59:59Z","w3wp","2","1","10","10","1","1",""` + "\n", importer.ReasonBadTime},
		"bad start":       {`"2001-10-25T13:00:00Z","w3wp","2","1","10","10","1","1","yesterday"` + "\n", importer.ReasonBadTime},
		"bad id":          {`"2001-10-25T13:00:00Z","w3wp","x","1","10","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"negative id":     {`"2001-10-25T13:00:00Z","w3wp","-4","1","10","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"bad cpu":         {`"2001-10-25T13:00:00Z","w3wp","2","NaN","10","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"negative cpu":    {`"2001-10-25T13:00:00Z","w3wp","2","-1","10","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"culture decimal": {`"2001-10-25T13:00:00Z","w3wp","2","1,5","10","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"bad working set": {`"2001-10-25T13:00:00Z","w3wp","2","1","1 MB","10","1","1",""` + "\n", importer.ReasonBadNumber},
		"field count":     {`"2001-10-25T13:00:00Z","w3wp","2"` + "\n", importer.ReasonFieldCount},
		"same twice":      {`"2001-10-25T13:00:00Z","w3wp","100","1","10","10","1","1",""` + "\n", importer.ReasonUnsupportedFormat},
	} {
		evs, rep, err := parse(t, []byte(lines(c.row)), opts(8))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != c.reason || rep.Errors[0].Line != 22 {
			t.Errorf("%s: rejected %d %v", name, rep.Rejected, rep.Errors)
		}
		// 40 good rows give four metrics each, and 20 cpu values in the second
		// snapshot; the rejected row gives nothing.
		if n := len(byKind(evs, "metric")); n != 180 {
			t.Errorf("%s: %d metric events", name, n)
		}
	}
}

// filler returns 20 good rows (process ids first to first+19) of one sample time,
// with cumulative CPU of cpu0 plus the process index.
func filler(ts string, first int, cpu0 int) string {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "\"2001-10-25T%sZ\",\"w3wp\",\"%d\",\"%d\",\"10\",\"10\",\"1\",\"1\",\"\"\n", ts, first+i, cpu0+i)
	}
	return b.String()
}

// A row whose numbers do not parse still shows that the process is listed, so
// it is not reported gone.
func TestRejectedRowStillCountsAsListed(t *testing.T) {
	data := getProcessHeader + filler("13:00:00", 100, 0) +
		`"2001-10-25T13:00:00Z","app","1","1","10","10","1","1",""` + "\n" +
		filler("13:00:15", 100, 0) +
		`"2001-10-25T13:00:15Z","app","1","oops","10","10","1","1",""` + "\n" +
		filler("13:00:30", 100, 0) +
		`"2001-10-25T13:00:30Z","app","1","3","10","10","1","1",""` + "\n"
	evs, rep, err := parse(t, []byte(data), opts(1))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(byKind(evs, "process-gone")); n != 0 || rep.Rejected != 1 {
		t.Errorf("%d gone, %d rejected", n, rep.Rejected)
	}
	var cpu []float64
	for _, e := range evs {
		if e.Attrs["metric"] == "cpu" && e.Attrs["pid"] == "1" {
			cpu = append(cpu, *e.Value)
		}
	}
	if fmt.Sprint(cpu) != "[6.666666666666667]" { // 2 s over 30 s, from the first snapshot
		t.Errorf("cpu %v", cpu)
	}
}

func TestEventIDsAreUniqueAndOutputIsDeterministic(t *testing.T) {
	a, _, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range a {
		if seen[e.EvidenceID] {
			t.Fatalf("duplicate id %s", e.EvidenceID)
		}
		seen[e.EvidenceID] = true
		if err := e.Validate(New().AllowedAttrs()); err != nil {
			t.Fatalf("event invalid: %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		b, _, _ := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
		if !reflect.DeepEqual(a, b) {
			t.Fatal("two parses of one file differ")
		}
	}
	// Events of one snapshot's exits come in source order.
	var lines []int64
	for _, e := range byKind(a, "process-gone") {
		lines = append(lines, e.Line)
	}
	if !sort.SliceIsSorted(lines, func(i, j int) bool { return lines[i] < lines[j] }) {
		t.Errorf("gone events out of order: %v", lines)
	}
}

func TestEventsDoNotShareAttrMaps(t *testing.T) {
	evs, _, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8))
	if err != nil {
		t.Fatal(err)
	}
	evs[0].Attrs["metric"] = "changed"
	for _, e := range evs[1:] {
		if e.Attrs["metric"] == "changed" {
			t.Fatal("attr maps are shared")
		}
	}
}

func TestGapSentencesPassLintCausal(t *testing.T) {
	for _, g := range []string{GapNoCores, GapSnapshotsOnly, GapGoneWindow, GapNoStartTime, GapNotTracked, GapCPUSkipped, GapTimeFormat} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestDetect(t *testing.T) {
	for in, want := range map[string]int{
		`"SampleUtc","ProcessName","Id","CPU"` + "\n":                                       90,
		"#TYPE Selected.System.Diagnostics.Process\n\"SampleUtc\",\"ProcessName\",\"Id\"\n": 90,
		`"SampleUtc","Image Name","PID","Session Name","Session#","Mem Usage"` + "\n":       90,
		"\xef\xbb\xbf\"sampleutc\",\"processname\",\"id\"\n":                                90,
		`"ProcessName","Id","CPU"` + "\n":                                                   0,
		`"SampleUtc","Id"` + "\n":                                                           0,
		`"TimeCreated","MachineName","LogName","ProviderName"` + "\n":                       0,
		"": 0,
	} {
		if got := New().Detect([]byte(in), "x.csv"); got != want {
			t.Errorf("Detect(%q) = %d, want %d", in, got, want)
		}
	}
}

// Nothing from a cell the importer does not keep may reach an event, the report
// or an error, whichever column holds it.
func TestSentinelNeverStored(t *testing.T) {
	cases := map[string]string{
		"name, image form": getProcessHeader + `"2001-10-25T13:00:00Z","` + sentinel + `.exe","1","1","10","10","1","1","2001-10-25T08:00:00Z"` + "\n",
		"name, with space": getProcessHeader + `"2001-10-25T13:00:00Z","` + sentinel + ` Agent","1","1","10","10","1","1",""` + "\n",
		"name, a path":     getProcessHeader + `"2001-10-25T13:00:00Z","C:\\` + sentinel + `\\x.exe","1","1","10","10","1","1",""` + "\n",
		"id":               getProcessHeader + `"2001-10-25T13:00:00Z","w3wp","` + sentinel + `","1","10","10","1","1",""` + "\n",
		"cpu":              getProcessHeader + `"2001-10-25T13:00:00Z","w3wp","1","` + sentinel + `","10","10","1","1",""` + "\n",
		"working set":      getProcessHeader + `"2001-10-25T13:00:00Z","w3wp","1","1","` + sentinel + `","10","1","1",""` + "\n",
		"start time":       getProcessHeader + `"2001-10-25T13:00:00Z","w3wp","1","1","10","10","1","1","` + sentinel + `"` + "\n",
		"sample time":      getProcessHeader + `"` + sentinel + `","w3wp","1","1","10","10","1","1",""` + "\n",
		"extra column":     `"SampleUtc","ProcessName","Id","Notes"` + "\n" + `"2001-10-25T13:00:00Z","w3wp","1","` + sentinel + `"` + "\n",
		"command line":     `"SampleUtc","ProcessName","Id","CommandLine"` + "\n" + `"2001-10-25T13:00:00Z","w3wp","1","` + sentinel + `"` + "\n",
		"path":             `"SampleUtc","ProcessName","Id","Path"` + "\n" + `"2001-10-25T13:00:00Z","w3wp","1","` + sentinel + `"` + "\n",
		"window title":     `"SampleUtc","ProcessName","Id","MainWindowTitle"` + "\n" + `"2001-10-25T13:00:00Z","w3wp","1","` + sentinel + `"` + "\n",
		"tasklist /v": `"SampleUtc","Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"` + "\n" +
			`"2001-10-25T13:00:00Z","w3wp.exe","1","` + sentinel + `","0","10 K","Running","` + sentinel + `","0:00:01","` + sentinel + `"` + "\n",
		"tasklist session": `"SampleUtc","Image Name","PID","Session Name","Session#","Mem Usage"` + "\n" +
			`"2001-10-25T13:00:00Z","w3wp.exe","1","` + sentinel + `","` + sentinel + `","10 K"` + "\n",
	}
	for name, data := range cases {
		evs, rep, err := parse(t, []byte(data), opts(8))
		var all strings.Builder
		fmt.Fprintf(&all, "%v|%+v", err, rep)
		for _, e := range evs {
			fmt.Fprintf(&all, "|%+v", e)
		}
		if strings.Contains(all.String(), sentinel) {
			t.Errorf("%s: sentinel stored: %.300s", name, all.String())
		}
	}
	// The process name of a client image is a hash, not the name.
	data := getProcessHeader + `"2001-10-25T13:00:00Z","` + sentinel + `","1","1","10","10","1","1",""` + "\n"
	evs, _, err := parse(t, []byte(data), opts(8))
	if err != nil || len(evs) == 0 || !strings.HasPrefix(evs[0].Attrs["process"], "instance-") {
		t.Fatalf("%d events, %v", len(evs), err)
	}
}

func TestNoNetwork(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(t, fixture(t, "getprocess_basic.csv"), opts(8)); err != nil {
		t.Fatal(err)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("network call from the importer")
	return nil, errors.New("no network")
}

func TestProcessesBeyondTheTrackingCap(t *testing.T) {
	old := maxTracked
	maxTracked = 3
	defer func() { maxTracked = old }()
	var b strings.Builder
	b.WriteString(getProcessHeader)
	for _, ts := range []string{"13:00:00", "13:00:15"} {
		for i := 0; i < 5; i++ {
			fmt.Fprintf(&b, "\"2001-10-25T%sZ\",\"w3wp\",\"%d\",\"%d\",\"10\",\"10\",\"1\",\"1\",\"2001-10-25T08:00:00Z\"\n", ts, i+1, i+10)
		}
	}
	evs, rep, err := parse(t, []byte(b.String()), opts(1))
	if err != nil || !hasGap(rep, GapNotTracked) {
		t.Fatalf("%v, gaps %v", err, rep.KnownGaps)
	}
	// Followed (3): starts, cpu in the second snapshot. Not followed (2): the four
	// direct metrics only, each snapshot, with no cpu, start or exit.
	if n, s := len(byKind(evs, "process-start")), len(byKind(evs, "process-gone")); n != 3 || s != 0 {
		t.Errorf("%d starts, %d gone", n, s)
	}
	cpu := 0
	for _, e := range evs {
		if e.Attrs["metric"] == "cpu" {
			cpu++
		}
	}
	if cpu != 3 || len(byKind(evs, "metric")) != 3+4*10 {
		t.Errorf("%d cpu, %d metric events", cpu, len(byKind(evs, "metric")))
	}
}
