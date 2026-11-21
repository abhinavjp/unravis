//go:build !windows

package main

import (
	"bytes"
	"testing"
)

// The demo's off-Windows sealer round-trips, refuses tampered or short blobs,
// and a second sealer (a later start on the same folder) opens the first one's blobs.
func TestDemoSealRoundTrip(t *testing.T) {
	p, err := demoSeal()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("demo case data")
	blob, err := p.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("sealed blob holds the plain text")
	}
	got, err := p.Unprotect(blob)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	blob[len(blob)-1] ^= 1
	if _, err := p.Unprotect(blob); err == nil {
		t.Fatal("tampered blob opened")
	}
	if _, err := p.Unprotect([]byte{1, 2}); err == nil {
		t.Fatal("short blob opened")
	}
	other, _ := demoSeal()
	fresh, _ := p.Protect(plain)
	if got, err := other.Unprotect(fresh); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("a second start could not open the first start's blob: %v", err)
	}
}
