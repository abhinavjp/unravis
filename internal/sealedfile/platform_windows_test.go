//go:build windows

package sealedfile

import (
	"bytes"
	"testing"
)

func TestLocalMachineRoundTripAndScopesDiffer(t *testing.T) {
	machine, err := LocalMachine()
	if err != nil {
		t.Fatal(err)
	}
	user, err := CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("shared folder document")
	sealed, err := machine.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("machine-sealed data holds the plain text")
	}
	got, err := machine.Unprotect(sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("machine round trip = %q, %v", got, err)
	}
	userSealed, err := user.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := user.Unprotect(userSealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("user round trip = %q, %v", got, err)
	}
}
