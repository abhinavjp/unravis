package mcpaudit_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func TestLedgerRowsAreBoundedAndValidated(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	long := string(bytes.Repeat([]byte("é"), 100)) // 200 bytes of two-byte characters
	if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", Kind: mcpaudit.KindAdmit, Tool: long, CorrID: long, Fingerprint: db.Fingerprint([]byte("{}"))}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.RecentEvents(ctx, 10)
	if err != nil || len(rows) != 1 || len(rows[0].Tool) > 64 || len(rows[0].CorrID) > 64 || !utf8.ValidString(rows[0].Tool) {
		t.Fatalf("%+v %v", rows, err)
	}
	for _, e := range []mcpaudit.Event{{ProfileID: "p", Kind: "bogus"}, {ProfileID: "p", Kind: "stop"}, {ProfileID: "p", Kind: ""}, {Kind: mcpaudit.KindEnd}, {Kind: mcpaudit.KindEnroll}} {
		if err := db.Record(ctx, e); !errors.Is(err, mcpaudit.ErrBadEvent) {
			t.Errorf("%+v: %v", e, err)
		}
	}
	// the fingerprint is keyed: not the plain hash of the arguments, and another key gives another one
	plain := sha256.Sum256([]byte("{}"))
	if fp := db.Fingerprint([]byte("{}")); bytes.Equal(fp, plain[:]) || len(fp) != 32 {
		t.Error("the fingerprint is not keyed")
	}
	db2, _, _ := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	defer func() { _ = db2.Close() }()
	if bytes.Equal(db.Fingerprint([]byte("{}")), db2.Fingerprint([]byte("{}"))) {
		t.Error("two stores give one fingerprint")
	}
}

func TestLedgerKeepsAtMostMaxEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 100k rows")
	}
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Fill the table in one statement (the cap is checked by Record), then write until the check has run.
	if _, err := mcpaudit.ExecForTest(db, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO audit_events(ts,profile_id,profile_rev,kind) SELECT CAST(strftime('%s','now') AS INTEGER)*1000,'p',1,'end' FROM n`, mcpaudit.MaxEvents+500); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", Kind: mcpaudit.KindEnd}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.CountEvents(ctx)
	if err != nil || n > mcpaudit.MaxEvents+1000 || n < mcpaudit.MaxEvents {
		t.Fatalf("rows: %d %v", n, err)
	}
	if n > mcpaudit.MaxEvents+pruneSlack {
		t.Fatalf("not pruned: %d", n)
	}
}

const pruneSlack = 1000

// BL-351: a row older than 90 days goes, a younger one stays, and the cap still holds.
func TestPruneByAgeAndCount(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{91 * 24 * time.Hour, 90*24*time.Hour + time.Minute, 89 * 24 * time.Hour, time.Hour, 0} {
		if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", Kind: mcpaudit.KindEnd, Time: now.Add(-age), Tool: age.String()}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.Prune(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	rows, _ := db.RecentEvents(ctx, 10)
	if len(rows) != 3 {
		t.Fatalf("%d rows kept", len(rows))
	}
	for _, r := range rows {
		if now.Sub(r.Time) > mcpaudit.RetainFor {
			t.Errorf("a row of %v is kept", now.Sub(r.Time))
		}
	}
	if n, err := db.Prune(ctx, now); err != nil || n != 0 {
		t.Errorf("second prune: %d %v", n, err)
	}
}

// A version 4 store (no code column) is upgraded in place: the old grant reads as not given code mode, and the column then stores a change.
func TestVersionFourStoreReadsCodeAsOff(t *testing.T) {
	dir := t.TempDir()
	db, _, err := mcpaudit.Open(ctx, dir, prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(ctx, mcpaudit.Profile{ID: "h1", State: "active", Rev: 2, Tools: []string{"a"}, Sources: []string{"s"}, Code: true}); err != nil {
		t.Fatal(err)
	}
	if p, err := db.Get(ctx, "h1"); err != nil || !p.Code {
		t.Fatalf("a new store must keep Code: %+v %v", p, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, mcpaudit.FileName)))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE profiles DROP COLUMN code", "PRAGMA user_version=4"} {
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
	if err != nil || p.Rev != 2 || p.Code {
		t.Fatalf("an old grant must read as code off: %+v %v", p, err)
	}
	p, err = db.Update(ctx, "h1", func(p *mcpaudit.Profile) error { p.Code = true; return nil })
	if err != nil || !p.Code {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err = db.Get(ctx, "h1"); err != nil || !p.Code {
		t.Fatalf("Code did not persist: %+v %v", p, err)
	}
}

func TestLifecycleKindsAreAcceptedAndOthersStillRefused(t *testing.T) {
	db, _, err := mcpaudit.Open(ctx, t.TempDir(), prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	kinds := []string{mcpaudit.KindEnroll, mcpaudit.KindApprove, mcpaudit.KindScopeChange, mcpaudit.KindRevoke}
	for _, k := range kinds {
		if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", ProfileRev: 3, Kind: k, Outcome: mcpaudit.OutcomeOK, Code: "x"}); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", Kind: "nonsense"}); !errors.Is(err, mcpaudit.ErrBadEvent) {
		t.Errorf("an unknown kind must still be refused: %v", err)
	}
	rows, err := db.RecentEvents(ctx, 10)
	if err != nil || len(rows) != len(kinds) {
		t.Fatalf("%d rows, %v", len(rows), err)
	}
	for i, r := range rows { // newest first
		if r.Kind != kinds[len(kinds)-1-i] || r.ProfileID != "p" || r.ProfileRev != 3 {
			t.Errorf("row %d: %+v", i, r)
		}
	}
}
