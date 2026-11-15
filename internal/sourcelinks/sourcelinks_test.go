package sourcelinks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testProtector struct{}

func (testProtector) Protect(value []byte) ([]byte, error) {
	out := append([]byte("fixture-sealed:"), value...)
	for i := len("fixture-sealed:"); i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}

func (testProtector) Unprotect(value []byte) ([]byte, error) {
	prefix := []byte("fixture-sealed:")
	if !bytes.HasPrefix(value, prefix) {
		return nil, errors.New("invalid test ciphertext")
	}
	out := append([]byte(nil), bytes.TrimPrefix(value, prefix)...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg", "sourcelinks.dat")
	s, err := New(path, testProtector{})
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func link(id, db string) Link {
	return Link{SavedConnectionID: id, Database: db, Collection: "audit", Label: "Label " + db, ConnectionLabel: "Conn", SelectionKey: "key-" + db}
}

func TestNewNeedsPathAndProtector(t *testing.T) {
	if _, err := New("", testProtector{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("empty path: %v", err)
	}
	if _, err := New("x", nil); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil protector: %v", err)
	}
}

func TestLinksPersistAcrossReopenAndAreSealed(t *testing.T) {
	s, path := newStore(t)
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}
	if err := s.Add(link("saved-1", "orders")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(link("saved-1", "payroll")); err != nil {
		t.Fatal(err)
	}
	// The same source again replaces its link: one entry, not two.
	if err := s.Add(link("saved-1", "orders")); err != nil {
		t.Fatal(err)
	}
	reopened, _ := New(path, testProtector{})
	got, err := reopened.List()
	if err != nil || len(got) != 2 || got[0].Database != "payroll" || got[1].Database != "orders" {
		t.Fatalf("links = %+v err %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "orders") || strings.Contains(string(raw), "saved-1") {
		t.Errorf("file is not sealed: %q", raw)
	}
}

func TestAddRefusesALinkWithoutASavedConnectionOrNames(t *testing.T) {
	s, _ := newStore(t)
	for _, bad := range []Link{
		{Database: "d", Collection: "c"},
		{SavedConnectionID: "s", Collection: "c"},
		{SavedConnectionID: "s", Database: "d"},
		{SavedConnectionID: "s", Database: strings.Repeat("x", maxName+1), Collection: "c"},
	} {
		if err := s.Add(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Add(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestRemoveByKeyThenByDatabaseAndForgetBySavedConnection(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Add(link("saved-1", "a"))
	_ = s.Add(link("saved-1", "b"))
	_ = s.Add(link("saved-2", "c"))
	noKey := link("saved-2", "d")
	noKey.SelectionKey = ""
	_ = s.Add(noKey)

	if err := s.Remove("key-a", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("", "d", "audit"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("never-added", "", ""); err != nil {
		t.Fatalf("removing nothing is not an error: %v", err)
	}
	got, _ := s.List()
	if len(got) != 2 || got[0].Database != "b" || got[1].Database != "c" {
		t.Fatalf("after remove: %+v", got)
	}
	if err := s.Forget("saved-1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.List()
	if len(got) != 1 || got[0].Database != "c" {
		t.Fatalf("after forget: %+v", got)
	}
}

func TestTheOldestLinkIsDroppedAtTheCap(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxLinks+3; i++ {
		if err := s.Add(link("saved-1", "db"+string(rune('A'+i%26))+string(rune('a'+i/26)))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil || len(got) != MaxLinks {
		t.Fatalf("%d links, err %v", len(got), err)
	}
	if got[0].Database != "dbDa" {
		t.Errorf("oldest kept = %q", got[0].Database)
	}
}

func TestACorruptFileIsReportedByListAndReplacedByAdd(t *testing.T) {
	s, path := newStore(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("List = %v, want ErrCorrupt", err)
	}
	if err := s.Add(link("saved-1", "orders")); err != nil {
		t.Fatalf("Add over a corrupt file: %v", err)
	}
	if got, err := s.List(); err != nil || len(got) != 1 {
		t.Fatalf("after add: %+v %v", got, err)
	}
}

func TestSealedExtraFieldsAreRefused(t *testing.T) {
	s, path := newStore(t)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	sealed, _ := testProtector{}.Protect([]byte(`{"version":1,"links":[],"password":"x"}`))
	_ = os.WriteFile(path, sealed, 0o600)
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Errorf("List = %v, want ErrCorrupt", err)
	}
}
