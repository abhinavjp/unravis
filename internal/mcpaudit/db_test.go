package mcpaudit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"api-audit-explorer/internal/mcpaudit"
)

type prot struct{}

func (prot) Protect(b []byte) ([]byte, error) { return append([]byte("S:"), b...), nil }
func (prot) Unprotect(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("S:")) {
		return nil, errors.New("no")
	}
	return b[2:], nil
}

var ctx = context.Background()

func TestOpenRefusesRelativeAndUNCFolders(t *testing.T) {
	for _, dir := range []string{"mcp", "./mcp", "", `\\srv\share\mcp`, "//srv/share/mcp"} {
		if _, _, err := mcpaudit.Open(ctx, dir, prot{}, nil); !errors.Is(err, mcpaudit.ErrBadFolder) {
			t.Errorf("%q: %v", dir, err)
		}
	}
	if _, err := os.Stat("mcp"); err == nil {
		t.Fatal("a relative folder was created")
	}
}

// A store written by a later version is not set aside (a downgrade must not lose the grants).
func TestNewerSchemaIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	db, _, err := mcpaudit.Open(ctx, dir, prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, mcpaudit.FileName)
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	before, _ := os.ReadFile(path) //nolint:gosec // the test's own file
	if _, rec, err := mcpaudit.Open(ctx, dir, prot{}, nil); !errors.Is(err, mcpaudit.ErrNewerVersion) || rec != "" {
		t.Fatalf("%q %v", rec, err)
	}
	if m, _ := filepath.Glob(path + ".bad-*"); len(m) != 0 {
		t.Fatalf("set aside: %v", m)
	}
	after, _ := os.ReadFile(path) //nolint:gosec // the test's own file
	if !bytes.Equal(before, after) {
		t.Fatal("the newer store was modified")
	}
}

// A version 1 store (no contested column) is upgraded in place and keeps its profiles.
func TestVersionOneStoreIsUpgraded(t *testing.T) {
	dir := t.TempDir()
	db, _, err := mcpaudit.Open(ctx, dir, prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(ctx, mcpaudit.Profile{ID: "h1", State: "pending", Rev: 3, Tools: []string{"a"}, Sources: []string{"s"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, mcpaudit.FileName)))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE profiles DROP COLUMN contested", "PRAGMA user_version=1"} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	db, rec, err := mcpaudit.Open(ctx, dir, prot{}, nil)
	if err != nil || rec != "" {
		t.Fatalf("%q %v", rec, err)
	}
	defer func() { _ = db.Close() }()
	p, err := db.Get(ctx, "h1")
	if err != nil || p.Rev != 3 || p.Contested != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := db.Update(ctx, "h1", func(p *mcpaudit.Profile) error { p.Contested = 2; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestMACDomainsDiffer(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if bytes.Equal(db.MAC("claim", []byte("x")), db.MAC("grant", []byte("x"))) {
		t.Fatal("two domains give one digest")
	}
	if !bytes.Equal(db.MAC("claim", []byte("x")), db.MAC("claim", []byte("x"))) {
		t.Fatal("not deterministic")
	}
}

func TestDeleteLiftsTheRevisionFloorAtomically(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if f, _ := db.RevFloor(ctx); f != 0 {
		t.Fatalf("floor %d", f)
	}
	_ = db.Insert(ctx, mcpaudit.Profile{ID: "a", State: "pending", Rev: 7})
	_ = db.Insert(ctx, mcpaudit.Profile{ID: "b", State: "pending", Rev: 4})
	if err := db.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "b"); err != nil { // a lower revision never lowers the floor
		t.Fatal(err)
	}
	if f, _ := db.RevFloor(ctx); f != 7 {
		t.Fatalf("floor %d", f)
	}
	if err := db.Delete(ctx, "a"); !errors.Is(err, mcpaudit.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := db.SetMeta(ctx, "revFloor", []byte("0")); err == nil {
		t.Fatal("the floor can be set through SetMeta")
	}
	if _, _, err := db.Meta(ctx, "revFloor"); err == nil {
		t.Fatal("the floor can be read through Meta")
	}
}

// Close wipes the key while a MAC may be running: with -race this fails if they are not ordered.
func TestCloseAndMACDoNotRace(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				db.MAC("grant", []byte("x"))
			}
		}()
	}
	_ = db.Close()
	wg.Wait()
}
