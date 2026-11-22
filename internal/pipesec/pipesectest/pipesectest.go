// Package pipesectest makes pipe names for tests, so a test never touches a production pipe name (spec 08, section on test pipes).
package pipesectest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// Name returns `\\.\pipe\unravis-test-<pid>-<8 hex>`.
func Name(t testing.TB) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random pipe name: %v", err)
	}
	return fmt.Sprintf(`\\.\pipe\unravis-test-%d-%s`, os.Getpid(), hex.EncodeToString(b[:]))
}
