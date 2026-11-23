package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SWP-1021-3: the hash of a staged program is right for a large file (the code reads the open file with io.Copy; this pins the value).
func TestFileSHA256HexOfALargeFile(t *testing.T) {
	body := strings.Repeat("recorder", 1<<20) // 8 MiB
	p := filepath.Join(t.TempDir(), "prog.exe")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	got, err := fileSHA256Hex(p)
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := fileSHA256Hex(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file gave no error")
	}
}
