package history

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The no-clobber guarantee belongs to the move itself, not to a check made before it: a copy already at the chosen name is never replaced.
func TestSetAsideNeverReplacesAnExistingCopy(t *testing.T) {
	service, path := testService(t)
	service.now = func() time.Time { return time.Date(2026, 11, 27, 14, 30, 12, 0, time.Local) }
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	older := path + ".unreadable-20261127-143012"
	if err := os.WriteFile(older, []byte("an older damaged copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	damaged := []byte("the newer damaged file")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := service.SetAside()
	if err != nil {
		t.Fatal(err)
	}
	if kept != "connections.dat.unreadable-20261127-143012-2" {
		t.Fatalf("kept as %q, want the -2 name", kept)
	}
	if got, _ := os.ReadFile(older); string(got) != "an older damaged copy" {
		t.Fatalf("the older copy was replaced: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(filepath.Dir(path), kept)); !bytes.Equal(got, damaged) {
		t.Fatalf("the new copy is not the damaged file: %q", got)
	}
}

// Copies are counted whatever their letter case: Windows file names are not case sensitive, and a renamed copy is still a kept copy.
func TestSetAsideCountsCopiesWhateverTheirCase(t *testing.T) {
	service, path := testService(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []string{"20261121-100000", "20261122-100000", "20261123-100000"} {
		if err := os.WriteFile(filepath.Join(dir, strings.ToUpper("connections.dat.unreadable-"+stamp)), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	damaged := []byte("damaged")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAside(); !errors.Is(err, ErrSetAsideFull) {
		t.Fatalf("set-aside with three upper-case copies = %v, want ErrSetAsideFull", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the file moved: %v", err)
	}
}

func TestFolderNamesTheFolderOfTheSavedFile(t *testing.T) {
	service, path := testService(t)
	if dir, shared := service.Folder(); dir != filepath.Dir(path) || shared {
		t.Fatalf("Folder = %q %v, want %q false", dir, shared, filepath.Dir(path))
	}
}
