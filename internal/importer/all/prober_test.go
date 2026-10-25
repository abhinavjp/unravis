package all_test

import (
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

func byName(t *testing.T, name string) importer.Importer {
	t.Helper()
	for _, imp := range all.Importers() {
		if imp.Name() == name {
			return imp
		}
	}
	t.Fatalf("no importer %q", name)
	return nil
}

// The probers are exactly the importers whose single lines carry a full date
// and time (spec 4.3). A new one must be added here on purpose.
func TestProberSet(t *testing.T) {
	var got []string
	for _, imp := range all.Importers() {
		if _, ok := imp.(importer.TimeProber); ok {
			got = append(got, imp.Name())
		}
	}
	sort.Strings(got)
	want := []string{"iis-httperr", "iis-w3c","log4net-text", "mel-json", "mongod-json", "nlog-text", "serilog-clef", "serilog-text"}
	if len(got) != len(want) {
		t.Fatalf("probers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("probers %v, want %v", got, want)
		}
	}
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestProbeTime(t *testing.T) {
	tz, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skip("no tz database")
	}
	local := importer.Options{Host: &importer.HostHint{Label: "web-a", TZ: tz}}
	noTZ := importer.Options{Host: &importer.HostHint{Label: "web-a"}}
	utcStated := importer.Options{Host: &importer.HostHint{Label: "web-a"}, Supplied: map[string]string{"log-time": "utc"}}
	none := importer.Options{}

	cases := []struct {
		name, imp, line string
		opts            importer.Options
		want            string // RFC3339 UTC, "" = no time
	}{
		{"iis row", "iis-w3c", "2001-09-11 08:30:15 GET /SENTINEL-7f3a 200 12", none, "2001-09-11T08:30:15Z"},
		{"iis row only time", "iis-w3c", "2001-09-11 08:30:15", none, "2001-09-11T08:30:15Z"},
		{"iis directive", "iis-w3c", "#Fields: date time cs-method", none, ""},
		{"iis date only", "iis-w3c", "2001-09-11 GET /x", none, ""},
		{"iis year out of range", "iis-w3c", "1850-09-11 08:30:15 GET /x 200", none, ""},
		{"iis not a date", "iis-w3c", "2001-13-45 08:30:15 GET /x 200", none, ""},
		{"iis glued", "iis-w3c", "2001-09-11 08:30:15GET /x", none, ""},
		{"iis empty", "iis-w3c", "", none, ""},
		{"httperr row", "iis-httperr", "2001-09-11 08:30:15 192.0.2.50 51000 192.0.2.10 443 HTTP/1.1 GET /x 503 1 QueueFull pool-a", none, "2001-09-11T08:30:15Z"},
		{"httperr directive", "iis-httperr", "#Fields: date time c-ip", none, ""},
		{"httperr empty", "iis-httperr", "", none, ""},

		{"serilog text", "serilog-text", "2001-10-25 14:02:30.950 +13:00 [INF] SENTINEL-7f3a", none, "2001-10-25T01:02:30.95Z"},
		{"serilog text continuation", "serilog-text", "   at Synthetic.Api.A()", none, ""},
		{"serilog text no zone", "serilog-text", "2001-10-25 14:02:30.950 [INF] x", none, ""},
		{"serilog clef", "serilog-clef", `{"@t":"2001-10-25T01:02:30.1234567Z","@mt":"SENTINEL-7f3a"}`, none, "2001-10-25T01:02:30.1234567Z"},
		{"serilog clef offset", "serilog-clef", `{"@mt":"x","@t":"2001-10-25T14:02:30+13:00"}`, none, "2001-10-25T01:02:30Z"},
		{"serilog clef no offset", "serilog-clef", `{"@t":"2001-10-25T01:02:30","@mt":"x"}`, none, ""},
		{"serilog clef no @t", "serilog-clef", `{"@mt":"x"}`, none, ""},
		{"serilog clef not json", "serilog-clef", `2001-10-25T01:02:30Z`, none, ""},

		{"nlog local", "nlog-text", "2001-10-25 14:02:30.0000|INFO|A|SENTINEL-7f3a", local, "2001-10-25T01:02:30Z"},
		{"nlog utc stated", "nlog-text", "2001-10-25 14:02:30.0000|INFO|A|x", utcStated, "2001-10-25T14:02:30Z"},
		{"nlog no tz", "nlog-text", "2001-10-25 14:02:30.0000|INFO|A|x", noTZ, ""},
		{"nlog no host", "nlog-text", "2001-10-25 14:02:30.0000|INFO|A|x", none, ""},
		{"nlog continuation", "nlog-text", "   at Foo.Bar()", local, ""},

		{"log4net local", "log4net-text", "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - SENTINEL-7f3a", local, "2001-10-25T01:02:30.95Z"},
		{"log4net utc stated", "log4net-text", "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - x", utcStated, "2001-10-25T14:02:30.95Z"},
		{"log4net no tz", "log4net-text", "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.A - x", noTZ, ""},
		{"log4net continuation", "log4net-text", "System.Exception: SENTINEL-7f3a", local, ""},

		{"mel json offset", "mel-json", `{"Timestamp":"2001-10-25T14:02:30.123+13:00","EventId":0,"LogLevel":"Information","Category":"A","Message":"SENTINEL-7f3a"}`, none, "2001-10-25T01:02:30.123Z"},
		{"mel json local", "mel-json", `{"Timestamp":"2001-10-25T14:02:30","EventId":0,"LogLevel":"Information","Category":"A"}`, local, "2001-10-25T01:02:30Z"},
		{"mel json local no tz", "mel-json", `{"Timestamp":"2001-10-25T14:02:30","EventId":0,"LogLevel":"Information","Category":"A"}`, noTZ, ""},
		{"mel json no timestamp", "mel-json", `{"EventId":0,"LogLevel":"Information","Category":"A"}`, none, ""},

		{"mongod", "mongod-json", `{"t":{"$date":"2001-10-25T01:02:30.123+00:00"},"s":"I","c":"COMMAND","id":51803,"msg":"SENTINEL-7f3a"}`, none, "2001-10-25T01:02:30.123Z"},
		{"mongod naive", "mongod-json", `{"t":{"$date":"2001-10-25T01:02:30.123"},"s":"I"}`, none, ""},
		{"mongod year", "mongod-json", `{"t":{"$date":"1995-10-25T01:02:30.123Z"},"s":"I"}`, none, ""},
		{"mongod garbage", "mongod-json", `not json SENTINEL-7f3a`, none, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok := byName(t, c.imp).(importer.TimeProber)
			if !ok {
				t.Fatalf("%s is not a TimeProber", c.imp)
			}
			got, ok := p.ProbeTime([]byte(c.line), c.opts)
			if c.want == "" {
				if ok {
					t.Fatalf("got %v, want no time", got)
				}
				return
			}
			if !ok || !got.Equal(utc(c.want)) || got.Location() != time.UTC {
				t.Fatalf("got %v %v, want %s", got, ok, c.want)
			}
		})
	}
}

func TestProbeFunc(t *testing.T) {
	iis := byName(t, "iis-w3c")
	f, ok := importer.ProbeFunc(iis, importer.Options{})
	if !ok {
		t.Fatal("iis-w3c has a prober")
	}
	if got, ok := f([]byte("2001-09-11 08:30:15 GET /x 200")); !ok || !got.Equal(utc("2001-09-11T08:30:15Z")) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := importer.ProbeFunc(byName(t, "win-events"), importer.Options{}); ok {
		t.Fatal("win-events has no prober")
	}
}
