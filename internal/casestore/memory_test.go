package casestore_test

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

func TestMemoryContract(t *testing.T) {
	storetest.Run(t, func(t testing.TB, caps casestore.Caps, now func() time.Time) casestore.Store {
		return casestore.NewMemoryWith(caps, now)
	})
}

func TestNewIDShape(t *testing.T) {
	seen := map[casestore.CaseID]bool{}
	for i := 0; i < 200; i++ {
		id, err := casestore.NewID()
		if err != nil || !id.Valid() || len(id) != 26 {
			t.Fatalf("NewID: %q %v", id, err)
		}
		if seen[id] {
			t.Fatal("duplicate id")
		}
		seen[id] = true
	}
	if _, err := casestore.ParseID(strings.Repeat("a", 26)); err != nil {
		t.Errorf("ParseID valid: %v", err)
	}
	if _, err := casestore.ParseID("../x"); err == nil {
		t.Error("ParseID accepted a path")
	}
}
