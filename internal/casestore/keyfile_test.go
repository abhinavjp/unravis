package casestore_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

func TestKeyFileRoundTrip(t *testing.T) {
	p := storetest.RandomProtector()
	path := filepath.Join(t.TempDir(), "case.key")
	keys, err := casestore.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keys.DB, keys.Pseudo) {
		t.Fatal("the two keys are identical")
	}
	keys.CaseID = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := casestore.WriteKeyFile(path, p, keys); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, keys.DB) || bytes.Contains(raw, keys.Pseudo) {
		t.Fatal("key bytes appear in the file in the clear")
	}
	got, err := casestore.ReadKeyFile(path, p)
	if err != nil {
		t.Fatal(err)
	}
	if got.CaseID != keys.CaseID {
		t.Fatalf("case ID %q did not round trip", got.CaseID)
	}
	if !bytes.Equal(got.DB, keys.DB) || !bytes.Equal(got.Pseudo, keys.Pseudo) {
		t.Fatal("keys changed in the round trip")
	}
	keys.Zero()
	for _, b := range append(append([]byte{}, keys.DB...), keys.Pseudo...) {
		if b != 0 {
			t.Fatal("Zero left key bytes behind")
		}
	}
}

func TestKeyFileRefusals(t *testing.T) {
	p := storetest.RandomProtector()
	other := storetest.RandomProtector()
	dir := t.TempDir()
	path := filepath.Join(dir, "case.key")
	keys, _ := casestore.NewKeys()
	if err := casestore.WriteKeyFile(path, p, keys); err != nil {
		t.Fatal(err)
	}

	if _, err := casestore.ReadKeyFile(path, other); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("wrong protector = %v, want ErrKeyFile", err)
	}
	if _, err := casestore.ReadKeyFile(filepath.Join(dir, "absent.key"), p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing = %v, want os.ErrNotExist", err)
	}
	for name, content := range map[string][]byte{
		"empty":    {},
		"garbage":  []byte("not a sealed blob at all"),
		"oversize": bytes.Repeat([]byte{1}, 5<<10),
	} {
		f := filepath.Join(dir, name+".key")
		if err := os.WriteFile(f, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := casestore.ReadKeyFile(f, p); !errors.Is(err, casestore.ErrKeyFile) {
			t.Errorf("%s = %v, want ErrKeyFile", name, err)
		}
	}
	// A validly sealed blob that is not a key document.
	sealed, _ := p.Protect([]byte(`{"v":1,"dbKey":"00","pseudoKey":"00"}`))
	f := filepath.Join(dir, "short.key")
	_ = os.WriteFile(f, sealed, 0o600)
	if _, err := casestore.ReadKeyFile(f, p); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("short keys = %v, want ErrKeyFile", err)
	}
	sealed, _ = p.Protect([]byte(`{"v":2}`))
	_ = os.WriteFile(f, sealed, 0o600)
	if _, err := casestore.ReadKeyFile(f, p); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("unknown version = %v, want ErrKeyFile", err)
	}
	if err := casestore.WriteKeyFile(filepath.Join(dir, "bad.key"), p, casestore.Keys{DB: []byte{1}}); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("WriteKeyFile with short keys = %v, want ErrKeyFile", err)
	}
	// Errors never carry key material.
	_, err := casestore.ReadKeyFile(path, other)
	if err != nil && bytes.Contains([]byte(err.Error()), []byte(string(keys.DB))) {
		t.Error("error text contains key bytes")
	}
}

func TestKeyFileOverwriteAndErase(t *testing.T) {
	p := storetest.RandomProtector()
	path := filepath.Join(t.TempDir(), "case.key")
	keys, _ := casestore.NewKeys()
	if err := casestore.WriteKeyFile(path, p, keys); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := casestore.OverwriteKeyFile(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || bytes.Equal(before, after) {
		t.Fatalf("overwrite: len %d -> %d, same content = %v", len(before), len(after), bytes.Equal(before, after))
	}
	if _, err := casestore.ReadKeyFile(path, p); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("overwritten key still readable: %v", err)
	}
	if err := casestore.EraseKeyFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file survived: %v", err)
	}
	if err := casestore.EraseKeyFile(path); err != nil {
		t.Fatalf("second erase: %v", err)
	}
	if err := casestore.OverwriteKeyFile(path); err != nil {
		t.Fatalf("overwrite of a missing file: %v", err)
	}
}
