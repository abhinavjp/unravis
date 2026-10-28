package masterkey

import (
	"bytes"
	"errors"
	"testing"
)

func TestArchiveKeyMatchesTypedPassphrase(t *testing.T) {
	dir := setup(t, goodPass)
	kr, err := Unlock(dir, []byte(goodPass))
	if err != nil {
		t.Fatal(err)
	}
	key, salt, p, err := kr.ArchiveKey(dir)
	if err != nil {
		t.Fatalf("ArchiveKey: %v", err)
	}
	defer clear(key)
	// The same passphrase typed on another PC derives the same key from the
	// recorded salt and parameters: that is what lets a file open anywhere.
	again, err := DeriveKey([]byte(goodPass), salt, p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("derived key differs from the keyring key")
	}
	// It is a copy: clearing it leaves the keyring working.
	clear(key)
	if _, err := kr.Protect([]byte("x")); err != nil {
		t.Fatalf("keyring damaged by clearing the copy: %v", err)
	}
}

func TestArchiveKeyLockedAndStale(t *testing.T) {
	dir := setup(t, goodPass)
	kr, err := Unlock(dir, []byte(goodPass))
	if err != nil {
		t.Fatal(err)
	}
	other := setup(t, "another pass phrase here")
	if _, _, _, err := kr.ArchiveKey(other); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("a different masterkey.dat gave %v, want ErrWrongKey", err)
	}
	if _, _, _, err := kr.ArchiveKey(t.TempDir()); !errors.Is(err, ErrNotSet) {
		t.Fatalf("no masterkey.dat gave %v, want ErrNotSet", err)
	}
	kr.Lock()
	if _, _, _, err := kr.ArchiveKey(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked keyring gave %v, want ErrLocked", err)
	}
}

func TestKeyCheckValue(t *testing.T) {
	a := KeyCheckValue(bytes.Repeat([]byte{1}, KeyLen))
	b := KeyCheckValue(bytes.Repeat([]byte{2}, KeyLen))
	if len(a) != 64 || a == b || a != KeyCheckValue(bytes.Repeat([]byte{1}, KeyLen)) {
		t.Fatalf("key check value is not a stable 64-character digest: %q %q", a, b)
	}
}
