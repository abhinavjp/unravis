package bbsaved

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

type xorProtector struct{}

func (xorProtector) Protect(v []byte) ([]byte, error) {
	out := append([]byte("sealed:"), v...)
	for i := len("sealed:"); i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}
func (xorProtector) Unprotect(v []byte) ([]byte, error) {
	if !bytes.HasPrefix(v, []byte("sealed:")) {
		return nil, errors.New("bad")
	}
	out := append([]byte(nil), v[len("sealed:"):]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg", "blackboxes.dat")
	s, err := New(p, xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) })
	return s, p
}

func loc(t *testing.T, in string) blackbox.Location {
	t.Helper()
	l, err := blackbox.ParseLocation(in)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const secret = "ubk-SECRET-READ-KEY-0123456789"

func TestAddListGetAndTheKeyStaysSealed(t *testing.T) {
	s, path := open(t)
	e, err := s.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret})
	if err != nil || !e.HasKey || e.Kind != blackbox.LocURL || e.Location != "http://web-b:7788" {
		t.Fatalf("%+v %v", e, err)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("%v", list)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "SECRET") {
		t.Fatalf("a key is in the list the page sees: %s", b)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the key is in the file in the clear")
	}
	k, err := s.Key(e.ID)
	if err != nil || k != secret {
		t.Fatalf("%q %v", k, err)
	}
	got, err := s.Get(e.ID)
	if err != nil || got.ID != e.ID {
		t.Fatal(err)
	}
}

func TestAFolderOrShareKeepsNoKey(t *testing.T) {
	s, _ := open(t)
	e, err := s.Add(AddInput{Name: "web-b", Location: loc(t, `\\web-b\UnravisBlackbox`), Key: secret})
	if err != nil || e.HasKey {
		t.Fatalf("%+v %v", e, err)
	}
	if k, _ := s.Key(e.ID); k != "" {
		t.Fatal("a key was kept for a share")
	}
}

func TestAURLNeedsAKey(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Add(AddInput{Name: "x", Location: loc(t, "web-b:7788")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Add(AddInput{Name: "", Location: loc(t, `D:\Bb`)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Add(AddInput{Name: "x", Location: loc(t, "web-b:7788"), Key: "a\nb"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

// Clumsy-user case 1: the same Blackbox added twice.
func TestTheSameBlackboxTwoWaysIsOneEntry(t *testing.T) {
	s, _ := open(t)
	first, err := s.Add(AddInput{Name: "web-b", Location: loc(t, `\\WEB-B\UnravisBlackbox`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{`//web-b/unravisblackbox/`, `\\web-b\UnravisBlackbox`} {
		e, err := s.Add(AddInput{Name: "web-b again", Location: loc(t, in)})
		if !errors.Is(err, ErrDuplicate) || e.ID != first.ID {
			t.Fatalf("%q: %+v %v", in, e, err)
		}
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatalf("%d entries", len(l))
	}
}

func TestTheSameRecorderByFolderAndByURLIsOneEntry(t *testing.T) {
	s, _ := open(t)
	first, err := s.Add(AddInput{Name: "web-b", Location: loc(t, `\\web-b\Bb`), RecorderHost: "WEB-B", RecorderName: "web-b"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret, RecorderHost: "web-b", RecorderName: "WEB-B"})
	if !errors.Is(err, ErrDuplicate) || e.ID != first.ID {
		t.Fatalf("%+v %v", e, err)
	}
	// A duplicate must not leave its key behind.
	if k, _ := s.Key(first.ID); k != "" {
		t.Fatal("a duplicate stored its key")
	}
	// A different recorder on the same host is a different Blackbox.
	if _, err := s.Add(AddInput{Name: "web-b2", Location: loc(t, "web-b:7790"), Key: secret, RecorderHost: "web-b", RecorderName: "web-b2"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Find("", "web-b", "web-b"); !ok || got.ID != first.ID {
		t.Fatalf("Find: %+v %v", got, ok)
	}
}

func TestRemoveDeletesTheEntryAndItsKey(t *testing.T) {
	s, path := open(t)
	e, _ := s.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret})
	if err := s.Remove(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Key(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := s.Remove(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	raw, _ := os.ReadFile(path)
	plain, _ := xorProtector{}.Unprotect(raw)
	if bytes.Contains(plain, []byte("SECRET")) {
		t.Fatal("the key stayed in the file after removal")
	}
}

func TestSetKeyAndSetThisPC(t *testing.T) {
	s, _ := open(t)
	e, _ := s.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret})
	if err := s.SetKey(e.ID, "ubk-NEW"); err != nil {
		t.Fatal(err)
	}
	if k, _ := s.Key(e.ID); k != "ubk-NEW" {
		t.Fatal(k)
	}
	if err := s.SetKey(e.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.SetKey("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SetThisPC(e.ID, true); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Get(e.ID); !g.ThisPC {
		t.Fatal("not marked")
	}
}

func TestADamagedFileIsNeverOverwritten(t *testing.T) {
	s, path := open(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not sealed at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Add(AddInput{Name: "x", Location: loc(t, `D:\Bb`)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("%v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "not sealed at all" {
		t.Fatal("the damaged file was overwritten")
	}
}

func TestTheListIsCapped(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < MaxEntries; i++ {
		if _, err := s.Add(AddInput{Name: "n", Location: loc(t, `D:\Bb`+strings.Repeat("x", i+1))}); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := s.Add(AddInput{Name: "n", Location: loc(t, `D:\Overflow`)}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%v", err)
	}
}

func TestNewNeedsAPathAndAProtector(t *testing.T) {
	if _, err := New("", xorProtector{}); err == nil {
		t.Fatal()
	}
	if _, err := New("x", nil); err == nil {
		t.Fatal()
	}
}

func TestSetRecorderFillsOnlyWhatIsEmpty(t *testing.T) {
	s, _ := open(t)
	e, _ := s.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret})
	if err := s.SetRecorder(e.ID, "WEB-B", "web-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRecorder(e.ID, "other", "other"); err != nil {
		t.Fatal(err)
	}
	g, _ := s.Get(e.ID)
	if g.RecorderHost != "WEB-B" || g.RecorderName != "web-b" {
		t.Fatalf("%+v", g)
	}
	if err := s.SetRecorder(e.ID, "", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.SetRecorder("nope", "a", "b"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Now the same recorder reached by a folder is a duplicate.
	if _, err := s.Add(AddInput{Name: "web-b", Location: loc(t, `\\web-b\Bb`), RecorderHost: "web-b", RecorderName: "WEB-B"}); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
}
