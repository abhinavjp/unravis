package schedule

import (
	"testing"

	"api-audit-explorer/internal/importer"
)

// The shared conformance suite lives in harness_test.go (importertest.Run).
// This file keeps what is specific to this package: its registry and the
// section 4 caps.

func TestMetadata(t *testing.T) {
	if len(All()) != 2 {
		t.Fatalf("All() = %d importers", len(All()))
	}
	for _, imp := range []importer.Importer{NewHangfire(), NewAgent()} {
		if len(imp.AllowedAttrs()) > 16 {
			t.Errorf("%s: %d allowed attrs, max 16", imp.Name(), len(imp.AllowedAttrs()))
		}
		l := imp.DefaultLimits()
		if l.MaxBytes != 16<<20 || l.MaxRows != 100_000 || l.MaxLineBytes != 64<<10 {
			t.Errorf("%s: limits = %+v", imp.Name(), l)
		}
	}
	if NewHangfire().Name() == NewAgent().Name() {
		t.Error("importer names must differ")
	}
}
