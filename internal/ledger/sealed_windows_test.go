//go:build windows

package ledger

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
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.dat")
	s, err := OpenSealed(path, filepath.Join(dir, "ledger.json"), protector)
	if err != nil {
		t.Fatal(err)
	}
	h := seedLedger(t, s)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(sealedNote)) || bytes.Contains(data, []byte(demoText)) {
		t.Fatal("DPAPI ledger.dat holds plaintext")
	}
	again, err := OpenSealed(path, "", protector)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := again.List(); len(l) != 1 || l[0].ID != h.ID || l[0].Notes != sealedNote {
		t.Fatalf("reopened = %+v", l)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenSealed(path, "", protector); err == nil || s != nil {
		t.Fatal("DPAPI accepted a tampered blob")
	}
}
