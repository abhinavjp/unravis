package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sealedNote = "note-marker-7f3a9c2e"

// fixtureProtector is a reversible stand-in for DPAPI that hides the plaintext
// and rejects blobs it did not produce.
type fixtureProtector struct {
	// tamper, when set, rewrites what Unprotect returns.
	tamper func([]byte) []byte
}

const fixturePrefix = "fixture-sealed:"

func (fixtureProtector) Protect(value []byte) ([]byte, error) {
	out := append([]byte(fixturePrefix), value...)
	for i := len(fixturePrefix); i < len(out); i++ {
		out[i] ^= 0xa5
	}
	return out, nil
}

func (p fixtureProtector) Unprotect(value []byte) ([]byte, error) {
	if !bytes.HasPrefix(value, []byte(fixturePrefix)) {
		return nil, errors.New("not a fixture blob")
	}
	out := append([]byte(nil), value[len(fixturePrefix):]...)
	for i := range out {
		out[i] ^= 0xa5
	}
	if p.tamper != nil {
		out = p.tamper(out)
	}
	return out, nil
}

func seedLedger(t *testing.T, s Store) Hypothesis {
	t.Helper()
	h := mustCreate(t, s, demoText)
	h, err := s.Update(h.ID, Input{Text: demoText, State: StateSupported, Notes: sealedNote})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSealedRoundTripAndNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.dat")
	s, err := OpenSealed(path, filepath.Join(dir, "ledger.json"), fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	h := seedLedger(t, s)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{sealedNote, demoText, `"hypotheses"`} {
		if bytes.Contains(data, []byte(needle)) {
			t.Fatalf("ledger.dat contains plaintext %q", needle)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a plain ledger.json must not be created")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}

	s2, err := OpenSealed(path, "", fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s2.List()
	if len(list) != 1 || list[0].ID != h.ID || list[0].Notes != sealedNote || list[0].State != StateSupported {
		t.Fatalf("reopened ledger = %+v", list)
	}
	if _, err := s2.Create("another hypothesis"); err != nil {
		t.Fatal(err)
	}
	s3, err := OpenSealed(path, "", fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s3.List(); len(l) != 2 {
		t.Fatalf("len = %d after second write", len(l))
	}
}

func TestSealedNeedsPathAndProtector(t *testing.T) {
	if _, err := OpenSealed("", "", fixtureProtector{}); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := OpenSealed(filepath.Join(t.TempDir(), "ledger.dat"), "", nil); err == nil {
		t.Fatal("nil protector accepted")
	}
}

func TestMigrationSealsAndDeletesPlainFile(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "ledger.json")
	datPath := filepath.Join(dir, "ledger.dat")
	old, err := Open(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	h := seedLedger(t, old)
	want, _ := old.List()
	if data, _ := os.ReadFile(plainPath); !bytes.Contains(data, []byte(sealedNote)) {
		t.Fatal("fixture: plain ledger should hold the note")
	}

	s, err := OpenSealed(datPath, plainPath, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plain ledger.json still present after migration: %v", err)
	}
	got, _ := s.List()
	if len(got) != 1 || got[0].ID != h.ID || !sameLedger(got, want) {
		t.Fatalf("migrated ledger differs:\n%+v\n%+v", got, want)
	}
	data, _ := os.ReadFile(datPath)
	if bytes.Contains(data, []byte(sealedNote)) {
		t.Fatal("ledger.dat holds plaintext")
	}
	again, err := OpenSealed(datPath, plainPath, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := again.List(); !sameLedger(l, want) {
		t.Fatalf("reopen after migration differs: %+v", l)
	}
}

func TestMigrationEmptyPlainFile(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "ledger.json")
	if err := os.WriteFile(plainPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSealed(filepath.Join(dir, "ledger.dat"), plainPath, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 0 {
		t.Fatalf("list = %v", l)
	}
	if _, err := os.Stat(plainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plain file should be removed")
	}
}

func TestMigrationFailedVerifyKeepsPlainFile(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"undecodable readback": func([]byte) []byte { return []byte("{not json") },
		"different ledger":     func([]byte) []byte { return []byte(`{"version":1,"hypotheses":[]}`) },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			plainPath := filepath.Join(dir, "ledger.json")
			datPath := filepath.Join(dir, "ledger.dat")
			old, err := Open(plainPath)
			if err != nil {
				t.Fatal(err)
			}
			seedLedger(t, old)
			before, _ := os.ReadFile(plainPath)

			s, err := OpenSealed(datPath, plainPath, fixtureProtector{tamper: tamper})
			if err == nil || s != nil {
				t.Fatal("migration should fail when the sealed copy does not verify")
			}
			after, rerr := os.ReadFile(plainPath)
			if rerr != nil || !bytes.Equal(before, after) {
				t.Fatalf("plain file was not kept intact: %v", rerr)
			}
			if _, err := os.Stat(datPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("the unverified ledger.dat must be removed so the next start retries")
			}
			// A later start with a working protector migrates normally.
			if _, err := OpenSealed(datPath, plainPath, fixtureProtector{}); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			if _, err := os.Stat(plainPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("plain file should be gone after the successful retry")
			}
		})
	}
}

type failingProtector struct{}

func (failingProtector) Protect([]byte) ([]byte, error)   { return nil, errors.New("no sealing") }
func (failingProtector) Unprotect([]byte) ([]byte, error) { return nil, errors.New("no sealing") }

func TestMigrationSealFailureKeepsPlainFile(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "ledger.json")
	old, _ := Open(plainPath)
	seedLedger(t, old)
	before, _ := os.ReadFile(plainPath)
	if s, err := OpenSealed(filepath.Join(dir, "ledger.dat"), plainPath, failingProtector{}); err == nil || s != nil {
		t.Fatal("expected an error")
	}
	if after, _ := os.ReadFile(plainPath); !bytes.Equal(before, after) {
		t.Fatal("plain file changed")
	}
}

func TestMigrationInvalidPlainFileLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "ledger.json")
	if err := os.WriteFile(plainPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenSealed(filepath.Join(dir, "ledger.dat"), plainPath, fixtureProtector{}); err == nil || s != nil {
		t.Fatal("opened an invalid plain ledger")
	}
	if got, _ := os.ReadFile(plainPath); string(got) != "{not json" {
		t.Fatal("invalid plain file was modified")
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ledger.dat must not be created")
	}
}

func TestLeftoverPlainFileRemovedOnlyWhenIdentical(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "ledger.json")
	datPath := filepath.Join(dir, "ledger.dat")
	old, _ := Open(plainPath)
	seedLedger(t, old)
	// Simulate a crash after the sealed file was written but before the delete.
	sealed, err := OpenSealed(datPath, "", fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := old.List()
	if err := sealed.(*store).writeSealed(items); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSealed(datPath, plainPath, fixtureProtector{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("identical leftover plain file should be removed")
	}

	// A plain file that differs from the sealed ledger is never deleted.
	if err := os.WriteFile(plainPath, []byte(`{"version":1,"hypotheses":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSealed(datPath, plainPath, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatal("sealed ledger must win over a stale plain file")
	}
	if _, err := os.Stat(plainPath); err != nil {
		t.Fatal("differing plain file must be kept")
	}
}

func TestSealedCorruptOrForeignFileFailsSafe(t *testing.T) {
	good := func(t *testing.T, dir string) (string, []byte) {
		path := filepath.Join(dir, "ledger.dat")
		s, err := OpenSealed(path, "", fixtureProtector{})
		if err != nil {
			t.Fatal(err)
		}
		seedLedger(t, s)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return path, data
	}
	cases := map[string]func(data []byte) []byte{
		"flipped byte": func(d []byte) []byte {
			out := append([]byte(nil), d...)
			out[len(out)/2] ^= 0xff
			return out
		},
		"truncated":    func(d []byte) []byte { return d[:len(fixturePrefix)+3] },
		"empty":        func([]byte) []byte { return []byte{} },
		"foreign blob": func([]byte) []byte { return []byte("this was not sealed by us") },
		"plain json":   func([]byte) []byte { return []byte(`{"version":1,"hypotheses":[]}`) },
		"oversize":     func([]byte) []byte { return bytes.Repeat([]byte("x"), maxSealedBytes+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path, data := good(t, dir)
			bad := mutate(data)
			if err := os.WriteFile(path, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			plainPath := filepath.Join(dir, "ledger.json")
			if err := os.WriteFile(plainPath, []byte(`{"version":1,"hypotheses":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := OpenSealed(path, plainPath, fixtureProtector{})
			if err == nil || s != nil {
				t.Fatal("opened a damaged ledger.dat")
			}
			if !strings.Contains(err.Error(), "left untouched") {
				t.Fatalf("error should say the file was left untouched: %v", err)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, bad) {
				t.Fatal("damaged ledger.dat was modified")
			}
			if _, err := os.Stat(plainPath); err != nil {
				t.Fatal("plain file must not be touched when ledger.dat is unreadable")
			}
		})
	}
}

func TestSealedRejectsUndecodableLedgerInsideValidSeal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.dat")
	for name, content := range map[string]string{
		"wrong version": `{"version":9,"hypotheses":[]}`,
		"unknown field": `{"version":1,"hypotheses":[],"rows":[1]}`,
		"trailing":      `{"version":1,"hypotheses":[]} {}`,
	} {
		sealed, _ := fixtureProtector{}.Protect([]byte(content))
		if err := os.WriteFile(path, sealed, 0o600); err != nil {
			t.Fatal(err)
		}
		if s, err := OpenSealed(path, "", fixtureProtector{}); err == nil || s != nil {
			t.Errorf("%s: opened an invalid ledger", name)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, sealed) {
			t.Errorf("%s: file was modified", name)
		}
	}
}

type oversizeProtector struct{}

func (oversizeProtector) Protect([]byte) ([]byte, error)     { return make([]byte, maxSealedBytes+1), nil }
func (oversizeProtector) Unprotect(v []byte) ([]byte, error) { return v, nil }

func TestSealedSizeCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.dat")
	// One byte over the cap is refused before any decryption.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxSealedBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSealed(path, "", fixtureProtector{})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversize file error = %v", err)
	}

	// Writing refuses a ledger whose sealed form is over the cap.
	s := &store{path: filepath.Join(dir, "other.dat"), seal: oversizeProtector{}, items: []Hypothesis{}}
	if err := s.save(nil); err == nil {
		t.Fatal("saved a sealed ledger over the cap")
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing should be written when over the cap")
	}
}

func TestSealedWriteFailureLeavesFileAndMemoryUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.dat")
	s, err := OpenSealed(path, "", fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	seedLedger(t, s)
	before, _ := os.ReadFile(path)
	s.(*store).seal = failingProtector{}
	if _, err := s.Create("will not persist"); err == nil {
		t.Fatal("expected a save error")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("file changed after a failed save")
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatalf("memory changed after a failed save: %d entries", len(l))
	}
}
