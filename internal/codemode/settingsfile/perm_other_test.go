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

func TestFileOthersCanWriteIsOffNeverOn(t *testing.T) {
	s := New(t.TempDir())
	if err := os.WriteFile(s.Path(), []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(), 0o666); err != nil { //nolint:gosec // the test needs a file others can write
		t.Fatal(err)
	}
	if _, err := s.Load(); err != ErrCorrupt {
		t.Fatalf("Load = %v, want ErrCorrupt", err)
	}
	if s.Settings().Enabled {
		t.Fatal("a file others can write turned code mode on")
	}
}
