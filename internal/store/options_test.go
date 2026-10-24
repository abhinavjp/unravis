package store

import "testing"

func TestNewAppliesOptions(t *testing.T) {
	s := New()
	if s.allowBroadScans || s.docsExaminedBudget != 0 {
		t.Fatalf("New() defaults: allowBroadScans=%v docsExaminedBudget=%d, want false and 0", s.allowBroadScans, s.docsExaminedBudget)
	}
	if s.margins == nil {
		t.Fatal("New() must make the margins map")
	}

	s = New(WithBroadScans(true), WithDocsExaminedBudget(5))
	if !s.allowBroadScans || s.docsExaminedBudget != 5 {
		t.Fatalf("options not applied: allowBroadScans=%v docsExaminedBudget=%d", s.allowBroadScans, s.docsExaminedBudget)
	}
}
