//go:build windows

package history

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A saved file held open by another program (antivirus, a backup tool) cannot be moved on Windows. The set-aside says it is unavailable, the
// unreadable file stays exactly where it was, and no copy is left behind. Once the other program lets go, the same call works.
func TestSetAsideBlockedByAnOpenHandleLeavesTheFileUntouched(t *testing.T) {
	service, path := testService(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	damaged := []byte("a file that is not a saved connections file")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	holder, err := os.Open(path) // Go opens without delete sharing, so a rename of this file is refused while it is open
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetAside()
	if !errors.Is(err, ErrUnavailable) {
		holder.Close()
		t.Fatalf("set-aside of a held file = %v, want ErrUnavailable", err)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, damaged) {
		holder.Close()
		t.Fatalf("the held file changed: %v", readErr)
	}
	if left, _ := filepath.Glob(path + ".unreadable-*"); len(left) != 0 {
		holder.Close()
		t.Fatalf("a copy was left behind: %v", left)
	}
	holder.Close()
	if _, err := service.SetAside(); err != nil {
		t.Fatalf("set-aside after the holder let go: %v", err)
	}
}
