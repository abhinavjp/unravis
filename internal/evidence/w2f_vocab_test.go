package evidence

import (
	"sort"
	"strings"
	"testing"
)

func w2fKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func words(s string) []string {
	out := strings.Fields(s)
	sort.Strings(out)
	return out
}

// W2-F pre-lands the export policy and kinds of every wave-2 source so that
// the importer cards touch no shared file. This pins the attribute names and
// kinds each card writes (plan §2.2, §2.3 and the cards).
func TestW2FExportVocabulary(t *testing.T) {
	appAttrs := "command error-code exception family in iocp-busy iocp-min level logger method qs qu status timeout-ms worker-busy worker-min"
	appKinds := "app-request app-start app-stop heartbeat-late log-entry log-rate"
	cases := []struct{ source, attrs, kinds string }{
		{"log4net-text", appAttrs, appKinds},
		{"log4net-xml", appAttrs, appKinds},
		{"serilog-clef", appAttrs, appKinds},
		{"serilog-text", appAttrs, appKinds},
		{"nlog-text", appAttrs, appKinds},
		{"mel-console", appAttrs, appKinds},
		{"mel-json", appAttrs, appKinds},
		{"ancm-stdout", appAttrs, appKinds + " app-session"},
		{"node-jsonl", appAttrs, appKinds + " client-event proxy-command"},
		{"dotnet-counters", "counter error-type generation method percentile pid provider status", "metric"},
		{"win-procsnap", "metric pid process", "metric process-gone process-start"},
		{"redis-slowlog", "argc client command id", "slow-command"},
		{"redis-latency", "event", "latency-spike"},
		{"redis-info", "metric role", "metric redis-start"},
		{"redis-log", "family role", "aof-fsync-slow aof-rewrite-done aof-rewrite-start bgsave-done bgsave-fail " +
			"bgsave-start client-buffer-closed config-warning redis-start redis-stop replication-event"},
		{"mongo-profile", "command docsExamined keysExamined ns nreturned numYields planSummary queryHash " +
			"responseLength type workingMs", "slow-op"},
		{"mongo-currentop", "ns numYields op planSummary waitingForLock", "inflight-op"},
		{"mongo-serverstatus", "metric", "metric mongod-start"},
		{"mongostat-json", "metric", "metric"},
		{"linux-sadf", "cpu metric pid process", "metric"},
		{"linux-pidstat", "cpu metric pid process", "metric"},
	}
	for _, c := range cases {
		pol, ok := exportPolicy[c.source]
		if !ok {
			t.Errorf("%s: no export policy", c.source)
			continue
		}
		if got, want := strings.Join(w2fKeys(pol), " "), strings.Join(words(c.attrs), " "); got != want {
			t.Errorf("%s attrs:\n got  %s\n want %s", c.source, got, want)
		}
		if len(pol) > MaxEventAttrs {
			t.Errorf("%s: %d attrs, more than MaxEventAttrs", c.source, len(pol))
		}
		if got, want := strings.Join(w2fKeys(exportKinds[c.source]), " "), strings.Join(words(c.kinds), " "); got != want {
			t.Errorf("%s kinds:\n got  %s\n want %s", c.source, got, want)
		}
		if len(exportDropped[c.source]) != 0 {
			t.Errorf("%s: unexpected exportDropped entry", c.source)
		}
	}
	for src, attrs := range map[string]string{"iis-w3c": "bytes-out bytes-in", "win-events": "faulting-app",
		"mongod-json": "responseLength"} {
		for _, a := range strings.Fields(attrs) {
			_, ok := exportPolicy[src][a]
			if want := src != "mongod-json"; ok != want {
				t.Errorf("%s %q in policy = %v, want %v", src, a, ok, want)
			}
		}
	}
	if got := ExportAttrNames("redis-latency"); len(got) != 1 || !got["event"] {
		t.Errorf("ExportAttrNames(redis-latency) = %v", got)
	}
	if ExportAttrNames("no-such-source") != nil {
		t.Error("ExportAttrNames of an unknown source is not nil")
	}
}

func TestW2FUnitsAndCounters(t *testing.T) {
	for _, u := range []string{"ms", "count", "percent-host", "percent-one-core", "bytes", "percent", "per-sec", "ratio"} {
		if !exportUnits[u] {
			t.Errorf("unit %q not in exportUnits", u)
		}
	}
	for _, c := range []string{"Process ID", "Active Requests", "Active Threads Count", "Requests Rejected", "Requests / Sec"} {
		if counterNames[strings.ToLower(c)] != c {
			t.Errorf("counter %q not in counterNames", c)
		}
	}
	for _, a := range []string{"w3wp.exe", "mongod.exe", "other"} {
		if faultingApps[a] != a {
			t.Errorf("faulting app %q missing", a)
		}
	}
	if faultingApps["sentinel-7f3a.exe"] != "" {
		t.Error("an unlisted faulting app is kept")
	}
}

// The export keeps framework names as they are and keys everything else.
func TestW2FExportValueRules(t *testing.T) {
	key := []byte("w2f-test-key-000000000000000000")
	logger := appLogPolicy["logger"]
	if got := exportAttr(key, logger, "StackExchange.Redis"); got != "StackExchange.Redis" {
		t.Errorf("framework logger = %q", got)
	}
	for _, v := range []string{"Microsoft.AspNetCore.Hosting", "StackExchange.Redis.ConnectionMultiplexer", "ioredis", "System"} {
		if got := exportAttr(key, logger, v); got != v || !attrValueOK(logger, got) {
			t.Errorf("framework logger %q = %q", v, got)
		}
	}
	for _, v := range []string{"Synthetic.Api.CacheClient", "Microsoft.AspNetCore.Hosting.Diagnostics", "logger-0a1b2c3d",
		"SystemX.Foo", "Microsoft.SENTINEL-7f3a", "redis-a"} {
		got := exportAttr(key, logger, v)
		if !strings.HasPrefix(got, "logger-") || got == v || !attrValueOK(logger, got) {
			t.Errorf("logger %q = %q, want a fresh logger- label", v, got)
		}
	}
	if got := exportAttr(key, appLogPolicy["exception"], "System.TimeoutException,app-exception"); got != "System.TimeoutException,app-exception" {
		t.Errorf("exception list = %q", got)
	}
	if got := exportAttr(key, appLogPolicy["exception"], "System.TimeoutException,SENTINEL-7f3a"); got != removedText {
		t.Errorf("exception list with an unknown part = %q", got)
	}
	if got := exportAttr(key, procName, "w3wp"); got != "w3wp" {
		t.Errorf("process w3wp = %q", got)
	}
	if got := exportAttr(key, procName, "web-a-tool"); !strings.HasPrefix(got, "instance-") {
		t.Errorf("process web-a-tool = %q", got)
	}
	if got := exportAttr(key, appLogPolicy["level"], "INF"); got != "INF" {
		t.Errorf("level INF = %q", got)
	}
	if got := exportAttr(key, appLogPolicy["command"], "keys"); got != "KEYS" {
		t.Errorf("command keys = %q", got)
	}
	if got := exportAttr(key, linuxPolicy["cpu"], "all"); got != "all" {
		t.Errorf("cpu all = %q", got)
	}
}
