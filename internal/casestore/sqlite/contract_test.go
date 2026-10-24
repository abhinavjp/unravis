package sqlite

import (
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

// F0's conformance suite, the same list the in-memory fake passes.
func TestSQLiteContract(t *testing.T) {
	storetest.Run(t, func(t testing.TB, caps casestore.Caps, now func() time.Time) casestore.Store {
		s, err := New(Options{
			Root: t.TempDir(), Protector: storetest.RandomProtector(), MinFreeBytes: -1, Now: now,
			MaxFileBytes: caps.MaxFileBytes, MaxFileRows: caps.MaxFileRows, MaxCaseBytes: caps.MaxCaseBytes,
		})
		if err != nil {
			t.Fatal(err)
		}
		// The suite leaves some handles open; close them before TempDir is removed.
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
