package history

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// damagedFixtures are files the history refuses to read: each one makes readLocked answer ErrCorrupt.
func damagedFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	protect := func(doc string) []byte {
		out, err := testProtector{}.Protect([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	rec := func(id string) string {
		return `{"id":"` + id + `","label":"db","uri":"mongodb://db.example","username":"","authSource":"","tlsMode":"system","caCertificate":"","collection":"audit","hasCredentials":false,"lastUsedAt":"2026-11-27T10:00:00Z"}`
	}
	return map[string][]byte{
		"not sealed at all": []byte("this is not a connections file"),
		"duplicate id":      protect(`{"version":1,"records":[` + rec("a") + `,` + rec("a") + `]}`),
		"unknown field":     protect(`{"version":1,"records":[],"surprise":true}`),
		"one bad record":    protect(`{"version":1,"records":[` + rec("a") + `,{"id":"b","label":"","uri":"mongodb://db.example","collection":"audit"}]}`),
	}
}

// RV-DAT-1 (W-13): an unreadable file can be set aside. The file is kept byte for byte, the list starts empty, and saving works again.
func TestSetAsideKeepsTheDamagedFileByteForByte(t *testing.T) {
	for name, damaged := range damagedFixtures(t) {
		t.Run(name, func(t *testing.T) {
			service, path := testService(t)
			service.now = func() time.Time { return time.Date(2026, 11, 27, 14, 30, 12, 0, time.Local) }
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, damaged, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("fixture must be unreadable, got %v", err)
			}
			kept, err := service.SetAside()
			if err != nil {
				t.Fatal(err)
			}
			if kept != "connections.dat.unreadable-20261127-143012" {
				t.Fatalf("kept name = %q", kept)
			}
			got, err := os.ReadFile(filepath.Join(filepath.Dir(path), kept)) //nolint:gosec // test reads its own temp file
			if err != nil || !bytes.Equal(got, damaged) {
				t.Fatalf("set-aside copy differs from the damaged file: err=%v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the live file is still there: %v", err)
			}
			items, err := service.List()
			if err != nil || len(items) != 0 {
				t.Fatalf("after set-aside List = %#v %v, want an empty list", items, err)
			}
			if _, err := service.Remember("db", testConnection("mongodb://db.example"), "audit", false); err != nil {
				t.Fatalf("saving after set-aside: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(filepath.Dir(path), kept)); err != nil || !bytes.Equal(got, damaged) { //nolint:gosec // test reads its own temp file
				t.Fatalf("the kept copy changed after a new save: %v", err)
			}
		})
	}
}

// A file that reads fine, or that is not there, is never set aside, and a healthy file is left exactly as it was.
func TestSetAsideRefusesAHealthyOrMissingFile(t *testing.T) {
	service, path := testService(t)
	if _, err := service.SetAside(); !errors.Is(err, ErrNotDamaged) {
		t.Fatalf("missing file: %v, want ErrNotDamaged", err)
	}
	input := testConnection("mongodb://db.example")
	input.Password = "secret-pw"
	if _, err := service.Remember("db", input, "audit", true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path) //nolint:gosec // test reads its own temp file
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAside(); !errors.Is(err, ErrNotDamaged) {
		t.Fatalf("healthy file: %v, want ErrNotDamaged", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // test reads its own temp file
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a refused set-aside changed the healthy file: %v", err)
	}
	if left, _ := filepath.Glob(path + ".unreadable-*"); len(left) != 0 {
		t.Fatalf("a refused set-aside left copies: %v", left)
	}
	items, err := service.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("saved list lost: %#v %v", items, err)
	}
}

// At most three copies are kept. A fourth is refused, the damaged file stays where it is, and no older copy is removed.
func TestSetAsideRefusesAFourthCopyAndRemovesNothing(t *testing.T) {
	service, path := testService(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// All set aside inside one second: the names must not collide, because a rename would replace an older copy.
	service.now = func() time.Time { return time.Date(2026, 11, 27, 14, 30, 12, 0, time.Local) }
	copies := map[string][]byte{}
	for i := 0; i < MaxSetAside; i++ {
		damaged := []byte("damaged file number " + string(rune('A'+i)))
		if err := os.WriteFile(path, damaged, 0o600); err != nil {
			t.Fatal(err)
		}
		kept, err := service.SetAside()
		if err != nil {
			t.Fatalf("set-aside %d: %v", i+1, err)
		}
		if _, dup := copies[kept]; dup {
			t.Fatalf("set-aside %d reused the name %q", i+1, kept)
		}
		copies[kept] = damaged
	}
	fourth := []byte("the fourth damaged file")
	if err := os.WriteFile(path, fourth, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAside(); !errors.Is(err, ErrSetAsideFull) {
		t.Fatalf("fourth set-aside: %v, want ErrSetAsideFull", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, fourth) { //nolint:gosec // test reads its own temp file
		t.Fatalf("the fourth damaged file was moved or changed: %v", err)
	}
	for name, want := range copies {
		if got, err := os.ReadFile(filepath.Join(filepath.Dir(path), name)); err != nil || !bytes.Equal(got, want) { //nolint:gosec // test reads its own temp file
			t.Fatalf("older copy %q was removed or changed: %v", name, err)
		}
	}
	// The list still fails closed: nothing was started over.
	if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("after a refused fourth, List = %v, want ErrCorrupt", err)
	}
}

// Passwords typed for one session are dropped with the list they belonged to.
func TestSetAsideDropsSessionOnlyPasswords(t *testing.T) {
	service, path := testService(t)
	input := testConnection("mongodb://db.example")
	input.Password = "session-only"
	if _, err := service.Remember("db", input, "audit", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAside(); err != nil {
		t.Fatal(err)
	}
	service.sessionMu.Lock()
	left := len(service.sessionSecrets)
	service.sessionMu.Unlock()
	if left != 0 {
		t.Fatalf("%d session passwords survived the set-aside", left)
	}
}
