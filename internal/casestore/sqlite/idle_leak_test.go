package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/leakcheck"
)

// BL-424: opening, reading, importing into and closing a case five times (with a
// store that is itself closed each time) leaves no goroutine and no open
// connection behind.
func TestCaseOpenCloseLeavesNoGoroutines(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	id := mustCreate(t, s, "leak").ID
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(i int) {
		c, err := s.Open(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		cs := c.(*Case)
		label := fmt.Sprintf("leak%d", i+1)
		if _, err := cs.Import(ctx, tMeta(label+".csv"), tBody(label), tRun(tEvents(cs, label, 5))); err != nil {
			t.Fatal(err)
		}
		if _, err := cs.Events(ctx, casestore.EventQuery{}); err != nil {
			t.Fatal(err)
		}
		cs.mu.Lock()
		db := cs.db
		cs.mu.Unlock()
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if st := db.Stats(); st.OpenConnections != 0 {
			t.Fatalf("closing the case left %d connections open", st.OpenConnections)
		}
	})
}

// TestStoreCloseLeavesNoGoroutines is the same for the shutdown path: handles
// left open are closed by Store.Close.
func TestStoreCloseLeavesNoGoroutines(t *testing.T) {
	ctx := context.Background()
	leakcheck.Cycles(t, 5, leakcheck.Options{}, func(int) {
		st := newStoreAt(t, filepath.Join(t.TempDir(), "cases"), storetest.RandomProtector())
		info, err := st.Create(ctx, "leak")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Open(ctx, info.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
