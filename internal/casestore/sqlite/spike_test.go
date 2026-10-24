package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
)

// TestSpikeAdiantum is the card's step-1 spike: an adiantum database opens on
// Windows with CGO off, a wrong key fails, and no plaintext reaches the disk.
func TestSpikeAdiantum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.db")
	good := bytes.Repeat([]byte{0x11}, 32)
	bad := bytes.Repeat([]byte{0x22}, 32)
	open := func(key []byte) (*sql.DB, error) {
		hexKey := hex.EncodeToString(key)
		db, err := driver.Open("file:"+filepath.ToSlash(path)+"?vfs=adiantum", func(c *sqlite3.Conn) error {
			return c.Exec("PRAGMA hexkey='" + hexKey + "'")
		})
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		return db, nil
	}
	db, err := open(good)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t(v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES ('SPIKE-SENTINEL-4c1d')"); err != nil {
		t.Fatal(err)
	}
	// Plaintext must be absent from the db and the WAL while the connection is live.
	for _, name := range []string{"case.db", "case.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		if strings.Contains(string(raw), "SPIKE-SENTINEL") || strings.Contains(string(raw), "SQLite format 3") {
			t.Fatalf("%s holds plaintext", name)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = open(good)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := db.QueryRowContext(ctx, "SELECT v FROM t").Scan(&v); err != nil || v != "SPIKE-SENTINEL-4c1d" {
		t.Fatalf("reopen: %q %v", v, err)
	}
	db.Close()
	db, err = open(bad)
	if err == nil {
		err = db.QueryRowContext(ctx, "SELECT v FROM t").Scan(&v)
		db.Close()
	}
	if err == nil {
		t.Fatal("wrong key was accepted")
	}
	t.Logf("wrong key error: %v", err)
}
