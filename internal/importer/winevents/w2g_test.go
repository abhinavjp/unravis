package winevents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/importertest"
)

func w2gImp() importer.Importer { return New(iis.ClassifyWAS, ClassifyApp, ClassifyTaskScheduler) }

func TestClassifyAppTable(t *testing.T) {
	cases := []struct {
		provider string
		id       int
		kind     string
	}{
		{"Application Error", 1000, "app-crash"},
		{".NET Runtime", 1026, "app-crash"},
		{"Windows Error Reporting", 1001, "app-crash"},
		{"Application Hang", 1002, "app-hang"},
		{"Service Control Manager", 7031, "service-crash"},
		{"Service Control Manager", 7034, "service-crash"},
		{"EventLog", 6008, "host-unexpected-shutdown"},
		{"Microsoft-Windows-Kernel-Power", 41, "host-unexpected-shutdown"},
		{"Microsoft-Windows-Resource-Exhaustion-Detector", 2004, "low-memory"},
		{"ASP.NET 4.0.30319.0", 1309, "aspnet-unhandled"},
		{"IIS AspNetCore Module V2", 1010, "ancm-start-failure"},
		{"application error", 1000, "app-crash"}, // provider case does not matter
	}
	for _, c := range cases {
		k, attrs, ok := ClassifyApp(c.provider, c.id)
		if !ok || k != c.kind || len(attrs) != 0 {
			t.Errorf("%s %d = %q %v %v, want %q", c.provider, c.id, k, attrs, ok, c.kind)
		}
	}
	// An uncited id, or a cited id under another provider, is not classified.
	for _, c := range []struct {
		provider string
		id       int
	}{
		{"Application Error", 1003}, {"Application Hang", 1000}, {"Service Control Manager", 7036},
		{"Microsoft-Windows-Kernel-Power", 42}, {"IIS AspNetCore Module V2", 1000},
		{"SyntheticApp", 1000}, {"", 0}, {"EventLog", 6005},
	} {
		if k, _, ok := ClassifyApp(c.provider, c.id); ok {
			t.Errorf("%s %d classified as %q", c.provider, c.id, k)
		}
	}
}

func TestFaultingAppLabel(t *testing.T) {
	for in, want := range map[string]string{
		"w3wp.exe": "w3wp.exe", "W3WP.EXE": "w3wp.exe", " dotnet.exe ": "dotnet.exe", "node.exe": "node.exe",
		"redis-server.exe": "redis-server.exe", "memurai.exe": "memurai.exe", "mongod.exe": "mongod.exe",
		"sqlservr.exe":      "sqlservr.exe",
		"SENTINEL-7f3a.exe": "other", `C:\inetsrv\w3wp.exe`: "other", "w3wp.exe --arg": "other",
		"w3wp": "other", "w3wp.exe\nx": "other", "other": "other", "job-alpha.exe": "other",
	} {
		if got := FaultingAppLabel(in); got != want {
			t.Errorf("FaultingAppLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestW2GFixtureKinds(t *testing.T) {
	imp := w2gImp()
	evs, rep, err := parse(t, imp, fixture(t, "events_app.csv"), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 || len(evs) != 10 {
		t.Fatalf("events=%d rejected=%d", len(evs), rep.Rejected)
	}
	want := []struct{ kind, app string }{
		{"worker-process-failure", ""}, {"worker-process-failure", ""}, {"apppool-disabled", ""},
		{"app-crash", "w3wp.exe"}, {"app-hang", "dotnet.exe"}, {"app-crash", "other"},
		{"service-crash", ""}, {"host-unexpected-shutdown", ""}, {"ancm-start-failure", ""}, {"win-event", ""},
	}
	for i, w := range want {
		e := evs[i]
		if e.Kind != w.kind || e.Attrs["faulting-app"] != w.app {
			t.Errorf("row %d: kind %q faulting-app %q, want %q %q", i, e.Kind, e.Attrs["faulting-app"], w.kind, w.app)
		}
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Errorf("row %d: %v", i, err)
		}
	}
	if evs[0].Attrs["app-pool"] != "pool-alpha" || evs[0].Attrs["id"] != "5011" {
		t.Errorf("columns lost: %v", evs[0].Attrs)
	}
	// The 2001-10-25T01:02:40Z recipe row from the plan: a WAS 5011 event.
	if !evs[1].Start.Equal(time.Date(2001, 10, 25, 1, 2, 40, 0, time.UTC)) {
		t.Errorf("time %v", evs[1].Start)
	}
}

// Without the classifiers the same rows stay generic, and a FaultingApp column
// is still reduced.
func TestW2GGenericWithoutClassifiers(t *testing.T) {
	evs, _, err := parse(t, New(), fixture(t, "events_app.csv"), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind != "win-event" {
			t.Errorf("kind %q without classifiers", e.Kind)
		}
	}
	if evs[3].Attrs["faulting-app"] != "w3wp.exe" {
		t.Errorf("faulting-app %q", evs[3].Attrs["faulting-app"])
	}
}

func TestW2GFaultingAppColumnIsOptionalAndSingle(t *testing.T) {
	// Absent column: no faulting-app attribute.
	evs, _, err := parse(t, w2gImp(), fixture(t, "events_utc.csv"), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if _, has := e.Attrs["faulting-app"]; has {
			t.Error("faulting-app without a column")
		}
	}
	// A repeated FaultingApp column could hold other data: the file is refused.
	dup := "\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\",\"FaultingApp\",\"FaultingApp\"\n" +
		"\"2001-10-25T01:02:42Z\",\"web-a\",\"Application\",\"Application Error\",\"1000\",\"2\",\"w3wp.exe\",\"" + importertest.Sentinel + "\"\n"
	_, rep, err := parse(t, w2gImp(), []byte(dup), opts(nil))
	if err == nil {
		t.Fatal("duplicate FaultingApp column accepted")
	}
	assertNoSentinel(t, rep, nil, err)
}

func assertNoSentinel(t *testing.T, rep importer.Report, evs any, err error) {
	t.Helper()
	b, _ := json.Marshal(struct {
		R importer.Report
		E any
	}{rep, evs})
	s := string(b)
	if err != nil {
		s += err.Error()
	}
	if strings.Contains(s, importertest.Sentinel) {
		t.Fatalf("sentinel leaked: %s", s)
	}
}

// Privacy: the sentinel in every dropped or reduced column never reaches an
// event, report, gap or error.
func TestW2GPrivacySentinel(t *testing.T) {
	var b strings.Builder
	b.WriteString("\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\",\"AppPool\",\"FaultingApp\",\"Message\",\"Properties\"\n")
	rows := [][]string{
		{"Application", "Application Error", "1000", importertest.Sentinel + ".exe"},
		{"Application", "Application Error", "1000", `C:\` + importertest.Sentinel + `\w3wp.exe`},
		{"Application", "Application Hang", "1002", importertest.Sentinel},
		{"Application", "Application Error", "1000", "w3wp.exe"},
		{"System", "Microsoft-Windows-WAS", "5011", importertest.Sentinel},
	}
	for i, r := range rows {
		fmt.Fprintf(&b, "\"2001-10-25T01:02:%02dZ\",\"web-a\",\"%s\",\"%s\",\"%s\",\"2\",\"\",\"%s\",\"%s message\",\"%s props\"\n",
			i, r[0], r[1], r[2], r[3], importertest.Sentinel, importertest.Sentinel)
	}
	imp := w2gImp()
	evs, rep, err := parse(t, imp, []byte(b.String()), opts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != len(rows) {
		t.Fatalf("events %d", len(evs))
	}
	assertNoSentinel(t, rep, evs, nil)
	for i, e := range evs[:3] {
		if e.Attrs["faulting-app"] != "other" {
			t.Errorf("row %d faulting-app %q", i, e.Attrs["faulting-app"])
		}
	}
	if evs[3].Attrs["faulting-app"] != "w3wp.exe" {
		t.Errorf("listed image lost: %q", evs[3].Attrs["faulting-app"])
	}
}

func TestW2GNoNetwork(t *testing.T) {
	var hit bool
	old := http.DefaultTransport
	http.DefaultTransport = failTransport{&hit}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(t, w2gImp(), fixture(t, "events_app.csv"), opts(nil)); err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("importer used the network")
	}
}

func w2gFixtures(t testing.TB) importertest.Fixtures {
	good := fixture(t, "events_app.csv")
	return importertest.Fixtures{
		Good:    good,
		Bad:     fixture(t, "bad_sentinel.csv"),
		Options: opts(nil),
		// LogName, ProviderName and AppPool are kept whole; faulting-app is a
		// fixed label, so a derived formula cell reads "other".
		KeepsCellValues: true,
	}
}

func TestW2GConformance(t *testing.T) {
	fx := w2gFixtures(t)
	fx.Good = bytes.ReplaceAll(fx.Good, []byte("w3wp.exe"), []byte("other"))
	fx.Good = bytes.ReplaceAll(fx.Good, []byte("dotnet.exe"), []byte("other"))
	fx.Good = bytes.ReplaceAll(fx.Good, []byte("job-alpha.exe"), []byte("other"))
	importertest.Run(t, w2gImp(), fx)
}

func FuzzW2GParse(f *testing.F) {
	f.Add(fixture(f, "events_app.csv"))
	fx := w2gFixtures(f)
	importertest.FuzzParse(f, w2gImp(), fx)
}
