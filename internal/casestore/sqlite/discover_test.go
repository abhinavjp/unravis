package sqlite

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func tableExists(t *testing.T, c *Case, name string) bool {
	t.Helper()
	var n int
	if err := c.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func schemaOf(t *testing.T, c *Case) string {
	t.Helper()
	var v string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='schema_version'").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A case written before the discovery tables existed opens, gains the tables and
// nothing else, and keeps what it held.
func TestVersionOneCaseGainsDiscoverTables(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Old")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "keep", 3)
	if !tableExists(t, c, "discover_watermarks") || !tableExists(t, c, "discover_reads") || schemaOf(t, c) != "2" {
		t.Fatal("a new case lacks the discovery tables or is not at schema 2")
	}
	for _, stmt := range []string{
		"DROP TABLE discover_watermarks", "DROP TABLE discover_reads",
		"UPDATE meta SET v='1' WHERE k='schema_version'",
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c = mustOpen(t, s, info.ID)
	if !tableExists(t, c, "discover_watermarks") || !tableExists(t, c, "discover_reads") {
		t.Fatal("the migration did not add the tables")
	}
	if v := schemaOf(t, c); v != "2" {
		t.Fatalf("schema version after migration = %s", v)
	}
	if recs, err := c.Imports(ctx); err != nil || len(recs) != 1 {
		t.Fatalf("imports after migration: %v %v", recs, err)
	}
	var k casestore.DiscoverFileKey
	k.PathHMAC[0] = 1
	if err := c.PutDiscoverMark(ctx, k, casestore.DiscoverMark{Offset: 5, LastLine: time.Unix(5, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// A second open runs no migration and keeps the mark.
	c = mustOpen(t, s, info.ID)
	if m, ok, err := c.DiscoverMark(ctx, k); err != nil || !ok || m.Offset != 5 {
		t.Fatalf("mark after reopen: %+v %v %v", m, ok, err)
	}
	_ = c.Close()
}

// The path HMAC key is derived, not the pseudonym key itself.
func TestDiscoverKeyIsNotThePseudonymKey(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "K").ID)
	defer c.Close()
	k := c.DiscoverKey()
	if len(k) != 32 || string(k) == string(c.keys.Pseudo) {
		t.Fatalf("discover key length %d, equal to pseudo key: %v", len(k), string(k) == string(c.keys.Pseudo))
	}
}
