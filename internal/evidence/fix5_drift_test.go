package evidence_test

import (
	"sort"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/appinsights"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/mongodlog"
	"api-audit-explorer/internal/importer/schedule"
	"api-audit-explorer/internal/importer/sqlserver"
	"api-audit-explorer/internal/importer/wincounters"
	"api-audit-explorer/internal/importer/winevents"
)

// Every attribute an importer may write must be either listed in the case
// export policy or left out on purpose (exportDropped). A new importer
// attribute therefore fails the build until someone decides which it is, and
// a policy entry for an attribute no importer writes fails too.
func TestFix5ExportPolicyCoversEveryImporter(t *testing.T) {
	imps := []importer.Importer{
		iis.NewW3C(), iis.NewHTTPERR(), wincounters.New(), sqlserver.NewPDH(), winevents.New(),
		mongodlog.New(), schedule.NewHangfire(), schedule.NewAgent(), appinsights.New(),
		sqlserver.NewXE(), sqlserver.NewErrorlog(),
	}
	policy, dropped := evidence.ExportPolicyKeys(), evidence.ExportDroppedKeys()
	seen := map[string]bool{}
	for _, imp := range imps {
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
			if !allowed[k] {
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
	if len(stray) > 0 {
		t.Errorf("export policy for sources no importer has: %v", stray)
	}
	for name := range dropped {
		if !seen[name] {
			t.Errorf("exportDropped for a source no importer has: %s", name)
		}
	}
}
