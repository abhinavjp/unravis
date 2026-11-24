package mcpaudit_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/mcpaudit"
)

// BL-349 store tests: real SQLite file, real server key (sealed by the test protector).

func aliasStore(t *testing.T) (*mcpaudit.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, _, err := mcpaudit.Open(ctx, dir, prot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Insert(ctx, mcpaudit.Profile{ID: "h1", State: mcpaudit.StateActive, Rev: 3, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

func recID(t testing.TB) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

var t0 = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

func TestAliasFormat(t *testing.T) {
	db, _ := aliasStore(t)
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		var keys []mcpaudit.EvidenceKey
		for j := 0; j < 100; j++ {
			keys = append(keys, mcpaudit.EvidenceKey{SourceID: "s1", RecordID: recID(t)})
		}
		got, err := db.MintAliases(ctx, "h1", 3, keys, t0)
		if err != nil || len(got) != 100 {
			t.Fatal(err, len(got))
		}
		for _, a := range got {
			if !mcpaudit.AliasRE.MatchString(a) || len(a) != 30 || seen[a] {
				t.Fatalf("bad or repeated alias %q", a)
			}
			seen[a] = true
		}
	}
}

// The alias is random: it shares no run of 6 characters with the record id it stands for, over 10,000 records.
func TestAliasNoRawID(t *testing.T) {
	db, _ := aliasStore(t)
	for i := 0; i < 100; i++ {
		keys := make([]mcpaudit.EvidenceKey, 100)
		for j := range keys {
			keys[j] = mcpaudit.EvidenceKey{SourceID: "s1", RecordID: recID(t)}
		}
		got, err := db.MintAliases(ctx, "h1", 3, keys, t0)
		if err != nil {
			t.Fatal(err)
		}
		for j, a := range got {
			id := keys[j].RecordID
			for k := 0; k+6 <= len(id); k++ {
				if strings.Contains(a, id[k:k+6]) {
					t.Fatalf("alias %q contains %q of record id %q", a, id[k:k+6], id)
				}
			}
		}
	}
}

func FuzzAlias(f *testing.F) {
	for _, s := range []string{"", "ev1_", "ev1_aaaaaaaaaaaaaaaaaaaaaaaaaa", "EV1_AAAAAAAAAAAAAAAAAAAAAAAAAA", "ev1_aaaaaaaaaaaaaaaaaaaaaaaaa1", "ev1_aaaaaaaaaaaaaaaaaaaaaaaaaa\x00", "' OR 1=1 --", strings.Repeat("a", 5000)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		db, _ := aliasStore(t)
		_, err := db.GetAlias(ctx, s, t0)
		if err == nil {
			t.Fatalf("an alias that was never made resolved: %q", s)
		}
	})
}

// The record id is in the file only sealed, and the lookup of an existing alias leaves no digest anyone can test a guess against
// without the server key. The alias and the id are different in every byte of the file.
func TestAliasRecordIDNotInFile(t *testing.T) {
	db, dir := aliasStore(t)
	rec := recID(t)
	got, err := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: rec}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	e, err := db.GetAlias(ctx, got[0], t0)
	if err != nil || e.RecordID != rec || e.SourceID != "s1" || e.ProfileID != "h1" || e.PolicyRev != 3 {
		t.Fatalf("%+v %v", e, err)
	}
	for name, b := range map[string][]byte{"open": readAll(t, dir)} {
		if bytes.Contains(b, []byte(rec)) {
			t.Errorf("the record id is in the store (%s)", name)
		}
		if raw, _ := hex.DecodeString(rec); bytes.Contains(b, raw) {
			t.Errorf("the raw record id bytes are in the store (%s)", name)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(readAll(t, dir), []byte(rec)) {
		t.Error("the record id is in the store after close")
	}
}

func TestAliasSameRowSameAliasAndExpiry(t *testing.T) {
	db, _ := aliasStore(t)
	k := mcpaudit.EvidenceKey{SourceID: "s1", RecordID: recID(t)}
	a1, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{k}, t0)
	a2, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{k}, t0.Add(time.Hour))
	if a1[0] != a2[0] {
		t.Error("the same row got two aliases")
	}
	if n, _ := db.CountAliases(ctx, "h1"); n != 1 {
		t.Errorf("%d rows", n)
	}
	// 7 days idle: used at +1h, so alive at +7d, gone at +7d1h.
	if _, err := db.GetAlias(ctx, a1[0], t0.Add(time.Hour+mcpaudit.AliasIdle-time.Second)); err != nil {
		t.Errorf("alive alias: %v", err)
	}
	if _, err := db.GetAlias(ctx, a1[0], t0.Add(time.Hour+mcpaudit.AliasIdle)); err != mcpaudit.ErrAliasNotFound {
		t.Errorf("expired alias: %v", err)
	}
	// Sliding use never goes past 30 days from the day it was made.
	at := t0
	for i := 0; i < 8; i++ {
		at = at.Add(5 * 24 * time.Hour)
		if _, err := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{k}, at); err != nil {
			t.Fatal(err)
		}
	}
	// at = t0+40d: the first alias is past its cap; the sixth touch (t0+30d) already met it, so a new alias was made then.
	got, err := db.GetAlias(ctx, a1[0], t0.Add(mcpaudit.AliasCap))
	if err != mcpaudit.ErrAliasNotFound {
		t.Errorf("an alias lived past its 30-day cap: %+v %v", got, err)
	}
	cur, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{k}, at)
	if cur[0] == a1[0] {
		t.Error("the alias past its cap was reused")
	}
}

// A mint for a profile that is not active at that revision writes nothing.
func TestAliasMintRefusedWhenProfileChanged(t *testing.T) {
	db, _ := aliasStore(t)
	k := []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: recID(t)}}
	for name, tc := range map[string]struct {
		id  string
		rev int
	}{"older revision": {"h1", 2}, "newer revision": {"h1", 4}, "no such profile": {"zz", 3}} {
		if _, err := db.MintAliases(ctx, tc.id, tc.rev, k, t0); err != mcpaudit.ErrAliasRefused {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := db.Update(ctx, "h1", func(p *mcpaudit.Profile) error { p.State = mcpaudit.StateRevoked; p.Rev = 4; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MintAliases(ctx, "h1", 4, k, t0); err != mcpaudit.ErrAliasRefused {
		t.Errorf("revoked profile: %v", err)
	}
	if n, _ := db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("%d aliases were written by refused mints", n)
	}
	bad := [][]mcpaudit.EvidenceKey{{{SourceID: "", RecordID: "x"}}, {{SourceID: "s", RecordID: ""}}, {{SourceID: "s", RecordID: strings.Repeat("x", 129)}}}
	for _, b := range bad {
		if _, err := db.MintAliases(ctx, "h1", 4, b, t0); err != mcpaudit.ErrAliasRefused {
			t.Errorf("%v: %v", b, err)
		}
	}
}

// A profile keeps at most MaxAliasesPerProfile; the least recently used go, and the ones just made are never among them.
func TestAliasTableIsBounded(t *testing.T) {
	db, _ := aliasStore(t)
	var first, last []string
	for i := 0; i < mcpaudit.MaxAliasesPerProfile/mcpaudit.MaxAliasBatch+3; i++ {
		keys := make([]mcpaudit.EvidenceKey, mcpaudit.MaxAliasBatch)
		for j := range keys {
			keys[j] = mcpaudit.EvidenceKey{SourceID: "s1", RecordID: recID(t)}
		}
		got, err := db.MintAliases(ctx, "h1", 3, keys, t0.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = got
		}
		last = got
	}
	if n, _ := db.CountAliases(ctx, "h1"); n != mcpaudit.MaxAliasesPerProfile {
		t.Fatalf("%d aliases, want %d", n, mcpaudit.MaxAliasesPerProfile)
	}
	if _, err := db.GetAlias(ctx, first[0], t0.Add(time.Minute)); err != mcpaudit.ErrAliasNotFound {
		t.Error("the oldest alias survived the cap")
	}
	for _, a := range last {
		if _, err := db.GetAlias(ctx, a, t0.Add(time.Minute)); err != nil {
			t.Fatalf("a fresh alias was trimmed: %v", err)
		}
	}
	if _, err := db.MintAliases(ctx, "h1", 3, make([]mcpaudit.EvidenceKey, mcpaudit.MaxAliasBatch+1), t0); err != mcpaudit.ErrAliasRefused {
		t.Errorf("batch over the bound: %v", err)
	}
}

// A row changed or copied in the file does not open: the record id is bound to its alias, profile and source.
func TestAliasRowTamper(t *testing.T) {
	db, _ := aliasStore(t)
	a, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: recID(t)}, {SourceID: "s1", RecordID: recID(t)}}, t0)
	for name, q := range map[string]string{
		"source changed":  fmt.Sprintf("UPDATE aliases SET source_id='s2' WHERE alias='%s'", a[0]),
		"profile changed": fmt.Sprintf("UPDATE aliases SET profile_id='h2' WHERE alias='%s'", a[0]),
		"record swapped":  fmt.Sprintf("UPDATE aliases SET record=(SELECT record FROM aliases WHERE alias='%s') WHERE alias='%s'", a[1], a[0]),
	} {
		if _, err := mcpaudit.ExecForTest(db, q); err != nil {
			t.Fatal(name, err)
		}
		if _, err := db.GetAlias(ctx, a[0], t0); err != mcpaudit.ErrAliasNotFound {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Prune removes aliases of a profile that is gone or changed, and ended ones.
func TestAliasPrune(t *testing.T) {
	db, _ := aliasStore(t)
	a, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: recID(t)}}, t0)
	if _, err := db.Prune(ctx, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAliases(ctx, ""); n != 1 {
		t.Fatalf("a live alias was pruned: %d", n)
	}
	_, _ = db.Update(ctx, "h1", func(p *mcpaudit.Profile) error { p.Rev = 4; return nil })
	if _, err := db.Prune(ctx, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("an alias of an older revision survived the prune (%s)", a[0])
	}
}

func TestAliasRemoveProfileDropsAliases(t *testing.T) {
	db, _ := aliasStore(t)
	_, _ = db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: recID(t)}}, t0)
	if _, err := db.Update(ctx, "h1", func(p *mcpaudit.Profile) error { p.State = mcpaudit.StateRevoked; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("%d aliases outlived their profile", n)
	}
}

func readAll(t *testing.T, dir string) []byte {
	t.Helper()
	var all []byte
	for _, n := range []string{mcpaudit.FileName, mcpaudit.FileName + "-wal"} {
		if b, err := readFile(dir + "/" + n); err == nil {
			all = append(all, b...)
		}
	}
	return all
}

func readFile(p string) ([]byte, error) { return os.ReadFile(p) } //nolint:gosec // a file of this test's own temp folder

// The 30-day cap is checked on read too, whatever the idle expiry column says.
func TestAliasCapChecked(t *testing.T) {
	db, _ := aliasStore(t)
	a, _ := db.MintAliases(ctx, "h1", 3, []mcpaudit.EvidenceKey{{SourceID: "s1", RecordID: recID(t)}}, t0)
	if _, err := mcpaudit.ExecForTest(db, "UPDATE aliases SET expires_at=?", t0.Add(100*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAlias(ctx, a[0], t0.Add(mcpaudit.AliasCap-time.Hour)); err != nil {
		t.Fatalf("inside the cap: %v", err)
	}
	if _, err := db.GetAlias(ctx, a[0], t0.Add(mcpaudit.AliasCap)); err != mcpaudit.ErrAliasNotFound {
		t.Errorf("at the cap: %v", err)
	}
}
