//go:build !windows

package settingsfile

import (
	"os"
	"testing"

	"api-audit-explorer/internal/codemode"
)

func TestSavedFileIsOwnerOnly(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
