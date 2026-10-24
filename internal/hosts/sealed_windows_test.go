//go:build windows

package hosts

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/sealedfile"
)

func TestSealedWithDPAPI(t *testing.T) {
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hosts.dat")
	s, err := OpenSealed(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(loadFile(t, "good.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("web-a")) || bytes.Contains(data, []byte("example.test")) {
		t.Fatal("DPAPI hosts.dat holds plaintext")
	}
	again, err := OpenSealed(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	if h, ok := again.Map().Lookup("web-a"); !ok || h.LogicalCores != 8 {
		t.Fatalf("reopened hint = %+v ok=%v", h, ok)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSealed(path, protector); err == nil {
		t.Fatal("a damaged hosts.dat opened")
	}
}
