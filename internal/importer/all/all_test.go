package all_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

// Inline samples for the redis exporters, whose fixtures live in that package's
// tests. All are synthetic (2001 dates, documentation addresses).
const (
	redisSlowSample = `1) 1) (integer) 14
   2) (integer) 1003971751
   3) (integer) 15230
   4) 1) "KEYS"
      2) "session:alice"
   5) "192.0.2.10:52144"
   6) "web-a-conn"
2) 1) (integer) 12
   2) (integer) 1003971740
   3) (integer) 1012000
   4) 1) "SORT"
      2) "list:bob"
   5) "198.51.100.7:50110"
   6) ""
`
	redisLatencySample = `# latency-event: fork
1) 1) (integer) 1003971745
   2) (integer) 230
2) 1) (integer) 1003971800
   2) (integer) 180
# latency-event: aof-fsync-always
1) 1) (integer) 1003971750
   2) (integer) 1180
`
	redisInfoSample = `# unravis-sample 2001-09-09T01:00:00Z
# Server
redis_version:7.2.4
uptime_in_seconds:1000
# Clients
connected_clients:12
blocked_clients:0
# Memory
used_memory:1048576
used_memory_rss:2097152
# Stats
instantaneous_ops_per_sec:850
evicted_keys:0
# Replication
role:master
# CPU
used_cpu_sys:10.50
used_cpu_user:20.25
# Keyspace
db0:keys=5,expires=1,avg_ttl=0
`
	redisLogSample = `1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240
1240:C 25 Oct 2001 14:02:31.900 * DB saved on disk
1234:M 25 Oct 2001 14:02:32.004 * Background saving terminated with success
1234:M 25 Oct 2001 14:02:33.500 * Asynchronous AOF fsync is taking too long (disk is busy?). Writing the AOF buffer without waiting for fsync to complete, this may slow down Redis.
1234:M 25 Oct 2001 14:05:00.000 # User requested shutdown...
1300:M 25 Oct 2001 14:05:03.100 * Ready to accept connections tcp
`
)

// sample is one importer's synthetic fixture: a testdata file of its own
// package or an inline string.
// as is the upload name when the importer detects by name too.
type sample struct{ file, inline, as string }

var samples = map[string]sample{
	"iis-w3c":           {file: "iis/testdata/w3c_good.log"},
	"iis-httperr":       {file: "iis/testdata/httperr_good.log"},
	"win-counters":      {file: "wincounters/testdata/counters_basic.csv"},
	"win-events":        {file: "winevents/testdata/events_utc.csv"},
	"schedule-hangfire": {file: "schedule/testdata/hangfire_good.csv"},
	"schedule-agent":    {file: "schedule/testdata/agent_good.csv"},
	"appinsights":       {file: "appinsights/testdata/good.csv"},
	"mongod-json":       {file: "mongodlog/testdata/good.log"},
	"sql-xe":            {file: "sqlserver/testdata/xe_good.csv"},
	"sql-errorlog":      {file: "sqlserver/testdata/errorlog_good.log"},
	"sql-pdh":           {file: "sqlserver/testdata/pdh_good.csv"},
	"win-procsnap":      {file: "procsnap/testdata/getprocess_basic.csv"},
	"dotnet-counters":   {file: "dotnetcounters/testdata/counters_basic.csv"},
	"serilog-clef":      {file: "serilog/testdata/clef_good.jsonl"},
	"serilog-text":      {file: "serilog/testdata/text_good.log"},
	"nlog-text":         {file: "nlog/testdata/good.log"},
	"nginx-access":      {file: "accesslog/testdata/nginx_good.log"},
	"apache-combined":   {file: "accesslog/testdata/apache_good.log"},
	"redis-slowlog":    {inline: redisSlowSample},
	"redis-latency":     {inline: redisLatencySample},
	"redis-info":        {inline: redisInfoSample},
	"redis-log":         {inline: redisLogSample},
	"log4net-text":      {file: "log4net/testdata/good.log"},
	"log4net-xml":       {file: "log4net/testdata/good.xml"},
	"mel-console":       {file: "mel/testdata/console_basic.log"},
	"mel-json":          {file: "mel/testdata/json_basic.log"},
	"ancm-stdout":       {file: "mel/testdata/stdout_basic.log", as: "stdout_20010304050607_1234.log"},
}

// mongo exports are named by their export kind; their testdata is one file each.
var mongoSamples = map[string]string{
	"profile": "profile-good.jsonl", "currentop": "currentop-good.jsonl",
	"serverstatus": "serverstatus-good.jsonl", "mongostat": "mongostat-good.jsonl",
}

func (s sample) bytes(t *testing.T) ([]byte, string) {
	t.Helper()
	if s.file == "" {
		return []byte(s.inline), "sample.txt"
	}
	b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(s.file)))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > importer.HeadSize {
		b = b[:importer.HeadSize]
	}
	if s.as != "" {
		return b, s.as
	}
	return b, filepath.Base(s.file)
}

func sampleFor(t *testing.T, name string) (sample, bool) {
	if s, ok := samples[name]; ok {
		return s, true
	}
	for key, f := range mongoSamples {
		if strings.Contains(name, key) {
			return sample{file: "mongoexports/testdata/" + f}, true
		}
	}
	return sample{}, false
}

// knownTies are the deliberate ambiguities: a SQL Server PDH file is also a
// PDH file, scores within the tie window of win-counters by design (the
// parser is the same) and the user picks. Nothing else may tie.
var knownTies = map[string][]string{"sql-pdh": {"sql-pdh", "win-counters"}}

// Every importer's own sample is claimed by that importer with the top score,
// and no other importer scores within the registry's ambiguity margin, so a
// user is never asked to pick for a file the app ships a fixture for.
func TestCrossDetectMatrix(t *testing.T) {
	reg := all.Registry()
	names := reg.Names()
	if len(names) < 31 {
		t.Fatalf("registry holds %d importers, want at least 31", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			s, ok := sampleFor(t, name)
			if !ok {
				t.Fatalf("no sample fixture for importer %q", name)
			}
			head, base := s.bytes(t)
			best, score, tied := reg.Detect(head, base)
			if best == nil {
				t.Fatalf("no importer claims the sample")
			}
			if best.Name() != name {
				t.Errorf("sample claimed by %q (score %d), want %q (tied: %v)", best.Name(), score, name, tied)
			}
			if want, ok := knownTies[name]; ok {
				if strings.Join(tied, ",") != strings.Join(want, ",") {
					t.Errorf("tied = %v, want the documented %v", tied, want)
				}
			} else if tied != nil {
				t.Errorf("another importer is within %d points: %v", importer.TieWindow, tied)
			}
		})
	}
}

// The web access-log importers keep their margin against every other
// importer. No other importer's sample scores above zero for nginx-access or
// apache-combined, and neither of them claims another importer's sample. The
// IIS NCSA file and a plain combined or common file are one format that nginx
// and Apache both write: the two importers tie on it by design (the user
// picks) and nothing else scores within the window of them.
func TestAccessLogDetectMargins(t *testing.T) {
	reg := all.Registry()
	own := map[string]bool{"nginx-access": true, "apache-combined": true}
	for _, name := range reg.Names() {
		if own[name] {
			continue
		}
		s, ok := sampleFor(t, name)
		if !ok {
			continue
		}
		head, base := s.bytes(t)
		for n := range own {
			imp, _ := reg.ByName(n)
			if got := imp.Detect(head, base); got != 0 {
				t.Errorf("%s scores the %s sample %d, want 0", n, name, got)
			}
		}
	}
	nginx, _ := reg.ByName("nginx-access")
	apache, _ := reg.ByName("apache-combined")
	for _, f := range []string{"iis/testdata/ncsa_format.log", "accesslog/testdata/common_good.log"} {
		head, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		best, score, tied := reg.Detect(head, filepath.Base(f))
		if best == nil || score < 55 || strings.Join(tied, ",") != "apache-combined,nginx-access" && strings.Join(tied, ",") != "nginx-access,apache-combined" {
			name := ""
			if best != nil {
				name = best.Name()
			}
			t.Errorf("%s: best %q score %d tied %v, want the nginx/apache tie only", f, name, score, tied)
		}
		if a, n := apache.Detect(head, ""), nginx.Detect(head, ""); a != n {
			t.Errorf("%s: apache %d nginx %d", f, a, n)
		}
	}
}

// The registered importers have unique names.
func TestImportersUniqueNames(t *testing.T) {
	seen := map[string]bool{}
	for _, imp := range all.Importers() {
		if seen[imp.Name()] {
			t.Errorf("duplicate importer name %q", imp.Name())
		}
		seen[imp.Name()] = true
	}
}

// Every Supplied key an importer declares has a spec that accepts a value its
// parser reads and refuses one it cannot: out of range numbers, a value past
// the size cap and empty text. The key list is pinned so a new key gets a
// review.
func TestSuppliedKeysDeclared(t *testing.T) {
	want := map[string][]string{
		"mongod-json":     {"slowms"},
		"mongo-profile":   {"slowms"},
		"mongostat-json":  {"date"},
		"redis-slowlog":   {"exported-at", "slowlog-log-slower-than", "slowlog-max-len"},
		"redis-latency":   {"latency-monitor-threshold"},
		"dotnet-counters": {"pid"},
		"redis-log":       {"year"},
		"log4net-text":    {"app-request-min-ms", "log-time", "pattern"},
		"log4net-xml":     {"app-request-min-ms"},
		"serilog-clef":    {"app-request-min-ms"},
		"serilog-text":    {"app-request-min-ms"},
		"nlog-text":       {"app-request-min-ms", "log-time"},
		"apache-combined": {"duration-unit"},
		"mel-console":     {"app-request-min-ms", "date-order"},
		"mel-json":        {"app-request-min-ms", "date-order"},
		"ancm-stdout":     {"app-request-min-ms", "date-order"},
	}
	got := map[string][]string{}
	for _, imp := range all.Importers() {
		k, ok := imp.(importer.SuppliedKeyer)
		if !ok {
			continue
		}
		var keys []string
		for key, spec := range k.SuppliedKeys() {
			keys = append(keys, key)
			checkSpec(t, imp.Name(), key, spec)
		}
		sort.Strings(keys)
		got[imp.Name()] = keys
	}
	for name, keys := range want {
		if g := strings.Join(got[name], ","); g != strings.Join(keys, ",") {
			t.Errorf("%s declares supplied keys [%s], want %v", name, g, keys)
		}
	}
	for name, keys := range got {
		if len(keys) > 0 && want[name] == nil {
			t.Errorf("%s declares supplied keys %v nobody reviewed", name, keys)
		}
	}
}

var errBad = importer.ErrBadSupplied

func checkSpec(t *testing.T, name, key string, spec importer.SuppliedSpec) {
	t.Helper()
	check := func(v string) error {
		if _, ok := spec.Canonical(v); !ok {
			return errBad
		}
		return nil
	}
	if err := check(strings.Repeat("9", 600)); err == nil {
		t.Errorf("%s/%s accepts a 600 byte value", name, key)
	}
	if err := check(""); err == nil && spec.Kind != importer.SuppliedText {
		t.Errorf("%s/%s accepts an empty value", name, key)
	}
	switch spec.Kind {
	case importer.SuppliedNumber:
		if err := check("-1"); err == nil && spec.Min >= 0 {
			t.Errorf("%s/%s accepts -1", name, key)
		}
		if err := check("not-a-number"); err == nil {
			t.Errorf("%s/%s accepts text for a number", name, key)
		}
	case importer.SuppliedEnum:
		if err := check("not-an-option"); err == nil {
			t.Errorf("%s/%s accepts an unlisted value", name, key)
		}
	}
	// An error never echoes the value.
	if err := check("SENTINEL-7f3a"); err != nil && strings.Contains(err.Error(), "SENTINEL-7f3a") {
		t.Errorf("%s/%s echoes the value in its error", name, key)
	}
}
