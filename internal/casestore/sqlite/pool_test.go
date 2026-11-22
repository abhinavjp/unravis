package sqlite

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// poolOf returns the database/sql pool of an open case.
func poolStats(c *Case) (open, idle, max int) {
	c.mu.Lock()
	db := c.db
	c.mu.Unlock()
	st := db.Stats()
	return st.OpenConnections, st.Idle, st.MaxOpenConnections
}

// TestPoolNeverExceedsFour (BL-423): twenty readers at once on a real encrypted
// SQLite file never open more than four connections, and every read answers.
func TestPoolNeverExceedsFour(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "pool").ID)
	mustImport(t, c, "first", 50)

	var peak atomic.Int64
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if open, _, _ := poolStats(c); int64(open) > peak.Load() {
				peak.Store(int64(open))
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 20*10)
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := c.Events(ctx, casestore.EventQuery{}); err != nil {
					errs <- err
					return
				}
				if _, err := c.Imports(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-watched
	close(errs)
	for err := range errs {
		t.Errorf("read under load: %v", err)
	}
	if open, _, max := poolStats(c); max != maxOpenConns || open > maxOpenConns {
		t.Errorf("pool: max %d open %d, want max %d", max, open, maxOpenConns)
	}
	if peak.Load() > maxOpenConns {
		t.Errorf("peak open connections %d, cap %d", peak.Load(), maxOpenConns)
	}
}

// TestIdleConnectionsClose (BL-423): an unused connection is closed after the idle
// time, and the next read reconnects (key and hardening applied again) and works.
func TestIdleConnectionsClose(t *testing.T) {
	old := poolIdleTime
	poolIdleTime = 200 * time.Millisecond
	t.Cleanup(func() { poolIdleTime = old })
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "idle").ID)
	mustImport(t, c, "first", 10)
	if _, err := c.Events(ctx, casestore.EventQuery{}); err != nil {
		t.Fatal(err)
	}
	if open, _, _ := poolStats(c); open == 0 {
		t.Fatal("no connection open right after a read")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if open, _, _ := poolStats(c); open == 0 {
			break
		}
		if time.Now().After(deadline) {
			open, idle, _ := poolStats(c)
			t.Fatalf("idle connections stayed open: open %d idle %d", open, idle)
		}
		time.Sleep(50 * time.Millisecond)
	}
	evs, err := c.Events(ctx, casestore.EventQuery{})
	if err != nil {
		t.Fatalf("read after the pool emptied: %v", err)
	}
	if len(evs) != 10 {
		t.Errorf("read after the pool emptied: %d events, want 10", len(evs))
	}
	mustImport(t, c, "second", 5) // a writer reconnects as well
}

// TestImportConnectionIsNotKept (BL-423): the import connection (large page cache)
// is discarded when the import ends; the pool is back to at most one idle
// connection, so the large cache does not stay resident.
func TestImportConnectionIsNotKept(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "importconn").ID)
	mustImport(t, c, "first", 20)
	if _, idle, _ := poolStats(c); idle > maxIdleConns {
		t.Errorf("idle connections after an import: %d, cap %d", idle, maxIdleConns)
	}
	// The pool connections keep the read-sized cache, the import's big one is gone.
	var kib int
	if err := c.db.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&kib); err != nil {
		t.Fatal(err)
	}
	if kib != -cacheKiB {
		t.Errorf("pool connection cache_size %d, want %d", kib, -cacheKiB)
	}
}
