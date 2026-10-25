package timeline

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
)

// Every shipped and wave-2 source has a plain display name, and the wave-2
// interval kinds read as plain phrases in OverlapUnique.
func TestW2FDisplayNamesAndPhrases(t *testing.T) {
	for _, s := range []string{"schedule-hangfire", "schedule-agent",
		"log4net-text", "log4net-xml", "serilog-clef", "serilog-text", "nlog-text", "nginx-access", "apache-combined", "mel-console", "mel-json", "ancm-stdout",
		"node-jsonl", "dotnet-counters", "win-procsnap", "redis-slowlog", "redis-latency", "redis-info", "redis-log",
		"mongo-profile", "mongo-currentop", "mongo-serverstatus", "mongostat-json", "linux-sadf", "linux-pidstat", "win-live"} {
		if n := displayName(s); n == s || n == "" {
			t.Errorf("%s has no display name", s)
		}
	}
	if _, ok := displayNames["schedule"]; ok {
		t.Error(`the stale "schedule" key is back`)
	}
	want := map[string]string{"slow-command": "slow command", "inflight-op": "running operation",
		"app-request": "slow request", "heartbeat-late": "late heartbeat", "proxy-command": "slow proxy command",
		"process-gone": "process exit window", "latency-spike": "latency spike", "slow-op": "slow operation"}
	for kind, phrase := range want {
		if got := eventPhrase(evidence.Event{Kind: kind}); got != phrase {
			t.Errorf("eventPhrase(%s) = %q, want %q", kind, got, phrase)
		}
	}
	got := evidence.OverlapUnique(displayName("redis-slowlog"), eventPhrase(evidence.Event{Kind: "slow-command"}), 1.5)
	if got != "A Redis SLOWLOG session had a slow command of 1.5 s overlapping this interval (Correlation)." {
		t.Errorf("sentence = %q", got)
	}
	for _, p := range eventPhrases {
		if strings.ContainsAny(p[:1], "aeiou") {
			t.Errorf("phrase %q starts with a vowel and reads badly after \"a\"", p)
		}
	}
}
