package sqlite

import (
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

// The same contract the in-memory fake passes.
func TestSQLiteContract(t *testing.T) {
	storetest.Contract(t, func(t *testing.T) casestore.Store {
		s, _ := newTestStore(t)
		return s
	})
}
