package history

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/sealedfile"
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
	if got, _ := os.ReadFile(older); string(got) != "an older damaged copy" { //nolint:gosec // test reads its own temp file
		t.Fatalf("the older copy was replaced: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(filepath.Dir(path), kept)); !bytes.Equal(got, damaged) { //nolint:gosec // test reads its own temp file
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
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, damaged) { //nolint:gosec // test reads its own temp file
		t.Fatalf("the file moved: %v", err)
	}
}

func TestFolderNamesTheFolderOfTheSavedFile(t *testing.T) {
	service, path := testService(t)
	if dir, shared := service.Folder(); dir != filepath.Dir(path) || shared {
		t.Fatalf("Folder = %q %v, want %q false", dir, shared, filepath.Dir(path))
	}
}

// The sealer can fail for reasons that say nothing about the file: a service that did not answer, a domain key that cannot be reached, a
// profile still loading. Those must never read as "unreadable", so set-aside is refused and the file is left exactly as it was.
type flakySealer struct {
	testProtector
	err error
}

func (f flakySealer) Unprotect([]byte) ([]byte, error) { return nil, f.err }

func TestSetAsideRefusedWhenTheSealerFailsForAnotherReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "connections.dat")
	healthy, err := New(path, testProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := healthy.Remember("one", testConnection("mongodb://one.example:27017"), "orders", false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path) //nolint:gosec // test reads its own temp file
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(path, flakySealer{err: errors.New("the key service did not answer")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrCorrupt) {
		t.Fatalf("List with a failing sealer = %v, want ErrUnavailable and not ErrCorrupt", err)
	}
	if _, err := service.SetAside(); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotDamaged) {
		t.Fatalf("SetAside with a failing sealer = %v, want ErrUnavailable", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // test reads its own temp file
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the file changed: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), setAsideInfix) {
			t.Fatalf("a set-aside copy was made: %s", entry.Name())
		}
	}
}

// A sealer that says the bytes cannot be opened is the one real "unreadable" case, and still gets the way out.
func TestSealerDataErrorStillReadsAsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "connections.dat")
	service, err := New(path, flakySealer{err: fmt.Errorf("%w: wrong account", sealedfile.ErrBadData)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("sealed for someone else"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("List = %v, want ErrCorrupt", err)
	}
	kept, err := service.SetAside()
	if err != nil || kept == "" {
		t.Fatalf("SetAside = %q, %v", kept, err)
	}
}
