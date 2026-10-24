package applog

import (
	"testing"

	"api-audit-explorer/internal/evidence"
)

// The core's attribute set is the export policy of every app-log source.
func TestAllowedAttrsMatchExportPolicy(t *testing.T) {
	got := AllowedAttrs()
	if len(got) != len(allowedAttrs) || len(got) > evidence.MaxEventAttrs {
		t.Fatalf("AllowedAttrs has %d names", len(got))
	}
	for _, src := range []string{"log4net-text", "log4net-xml", "serilog-clef", "serilog-text",
		"mel-console", "mel-json", "ancm-stdout", "node-jsonl"} {
		pol := evidence.ExportAttrNames(src)
		if len(pol) != len(got) {
			t.Errorf("%s: export policy has %d attrs, core has %d", src, len(pol), len(got))
		}
		for a := range got {
			if !pol[a] {
				t.Errorf("%s: %q not in the export policy", src, a)
			}
		}
	}
}
