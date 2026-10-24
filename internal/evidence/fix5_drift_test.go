package evidence_test

import (
	"sort"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/all"
)

// w2Pending names the wave-2 sources whose export policy W2-F lands before
// the importer exists, with the card that registers the importer. A pending
// source that is not yet in the registry is skipped by name; once its card
// registers it, the full check applies and its entry here should go.
var w2Pending = map[string]string{
	"log4net-text": "W2-b", "log4net-xml": "W2-b",
	"serilog-clef": "W2-c", "serilog-text": "W2-c",
	"mel-console": "W2-d", "mel-json": "W2-d", "ancm-stdout": "W2-d",
	"dotnet-counters": "W2-e", "win-procsnap": "W2-f",
	"redis-slowlog": "W2-h", "redis-latency": "W2-h", "redis-info": "W2-h", "redis-log": "W2-i",
	"node-jsonl":    "W2-j",
	"mongo-profile": "W2-k", "mongo-currentop": "W2-k", "mongo-serverstatus": "W2-k", "mongostat-json": "W2-k",
	"linux-sadf": "W2-m", "linux-pidstat": "W2-m",
}

// w2PendingAttrs are policy attributes of registered importers that a wave-2
// card adds to the importer. Until the importer allows one, the "policy lists
// an attribute the importer does not allow" check skips it.
var w2PendingAttrs = map[string]map[string]string{
	"iis-w3c":    {"bytes-out": "W2-g", "bytes-in": "W2-g"},
	"win-events": {"faulting-app": "W2-g"},
}

// Every attribute an importer may write must be either listed in the case
// export policy or left out on purpose (exportDropped). A new importer
// attribute therefore fails the build until someone decides which it is, and
// a policy entry for an attribute no importer writes fails too.
func TestFix5ExportPolicyCoversEveryImporter(t *testing.T) {
	policy, dropped := evidence.ExportPolicyKeys(), evidence.ExportDroppedKeys()
	seen := map[string]bool{}
	for _, imp := range all.Importers() {
		name := imp.Name()
		seen[name] = true
		pol, ok := policy[name]
		if !ok {
			t.Errorf("importer %s has no export policy", name)
			continue
		}
		allowed := imp.AllowedAttrs()
		for k := range allowed {
			if !pol[k] && !dropped[name][k] {
				t.Errorf("%s: attribute %q is in neither the export policy nor exportDropped", name, k)
			}
		}
		for k := range pol {
			if !allowed[k] && w2PendingAttrs[name][k] == "" {
				t.Errorf("%s: export policy lists %q, which the importer does not allow", name, k)
			}
			if dropped[name][k] {
				t.Errorf("%s: %q is both in the export policy and in exportDropped", name, k)
			}
		}
		for k := range dropped[name] {
			if !allowed[k] {
				t.Errorf("%s: exportDropped lists %q, which the importer does not allow", name, k)
			}
		}
	}
	var stray []string
	for name := range policy {
		if !seen[name] {
			stray = append(stray, name)
		}
	}
	sort.Strings(stray)
	for _, name := range stray {
		card, pending := w2Pending[name]
		if !pending {
			t.Errorf("export policy for a source no importer has: %s", name)
			continue
		}
		t.Run(name, func(t *testing.T) { t.Skipf("%s is not registered yet (%s)", name, card) })
	}
	for name := range dropped {
		if !seen[name] {
			t.Errorf("exportDropped for a source no importer has: %s", name)
		}
	}
	for name := range w2Pending {
		if _, ok := policy[name]; !ok {
			t.Errorf("w2Pending names %s, which has no export policy", name)
		}
	}
}
