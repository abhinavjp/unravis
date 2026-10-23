//go:build windows

package history

import (
	"bytes"
	"testing"
)

func TestDPAPIUsesCurrentUserRoundTrip(t *testing.T) {
	protector, err := currentUserProtector()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("current-user DPAPI round trip")
	ciphertext, err := protector.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ciphertext, plain) {
		t.Fatal("DPAPI returned plaintext")
	}
	decoded, err := protector.Unprotect(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("unprotected value=%q", decoded)
	}
}
