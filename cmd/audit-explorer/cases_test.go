package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/ledger"
)

func TestCasesRootFollowsTheConfigFolderWhenSet(t *testing.T) {
	dir := t.TempDir()
	got, err := casesRoot(func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	})
	if err != nil || got != filepath.Join(dir, "cases") {
		t.Fatalf("casesRoot = %q, %v", got, err)
	}
}

func TestEraseHookStripsOnlyThatCasesEvidenceAndIsIdempotent(t *testing.T) {
	led, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	const caseID = "abcdefghijklmnopqrstuvwxyz"
	mine := evidence.CasePrefixes(caseID)[0] + "iis-httperr:0123456789abcdef"
	other := "ev:zzzzzzzz:iis-httperr:0123456789abcdef"
	h, err := led.Create("A synthetic hypothesis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := led.Update(h.ID, ledger.Input{
		Text: "A synthetic hypothesis", State: ledger.StateOpen, EvidenceIDs: []string{mine, other},
		Facts: ledger.Facts{Counterexamples: []string{}, Alternatives: []ledger.Alternative{{Text: "Another cause", Status: ledger.AltOpen}}},
	}); err != nil {
		t.Fatal(err)
	}
	hook := eraseHook(led)
	for i := 0; i < 2; i++ {
		if err := hook(context.Background(), caseID); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	list, err := led.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	var kept, stripped bool
	for _, id := range list[0].EvidenceIDs {
		kept = kept || id == other
		stripped = stripped || id == "erased-case-evidence"
		if strings.HasPrefix(id, evidence.CasePrefixes(caseID)[0]) {
			t.Errorf("reference to the erased case survived: %q", id)
		}
	}
	if !kept || !stripped {
		t.Errorf("evidence ids = %v, want the other case kept and one replacement", list[0].EvidenceIDs)
	}
}
