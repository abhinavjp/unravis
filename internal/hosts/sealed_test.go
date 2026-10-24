package hosts

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureProtector is a reversible stand-in for DPAPI that hides the plaintext
// and rejects blobs it did not produce.
type fixtureProtector struct{ failProtect bool }

const fixturePrefix = "fixture-sealed:"

func (p fixtureProtector) Protect(value []byte) ([]byte, error) {
	if p.failProtect {
		return nil, errors.New("seal failed")
	}
	out := append([]byte(fixturePrefix), value...)
	for i := len(fixturePrefix); i < len(out); i++ {
		out[i] ^= 0xa5
	}
	return out, nil
}

func (fixtureProtector) Unprotect(value []byte) ([]byte, error) {
	if !bytes.HasPrefix(value, []byte(fixturePrefix)) {
		return nil, errors.New("not a fixture blob")
	}
	out := append([]byte(nil), value[len(fixturePrefix):]...)
	for i := range out {
		out[i] ^= 0xa5
	}
	return out, nil
}

func TestSealedMissingFileIsEmptyAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.dat")
	s, err := OpenSealed(path, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Map() == nil || s.Map().Len() != 0 {
		t.Fatalf("map = %+v", s.Map())
	}
	if _, ok := s.Map().Lookup("web-a"); ok {
		t.Fatal("empty map found a host")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("opening created files: %v", entries)
	}
}

func TestSealedSaveRoundTripAndNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.dat")
	s, err := OpenSealed(path, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	good := loadFile(t, "good.json")
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	if s.Map() != good {
		t.Fatal("Save must make the saved map current")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"web-a", "example.test", "Pacific", `"hosts"`, "version"} {
		if bytes.Contains(data, []byte(needle)) {
			t.Fatalf("hosts.dat contains plaintext %q", needle)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}

	again, err := OpenSealed(path, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	h, ok := again.Map().Lookup("SQL-A.example.test")
	if !ok || h.Label != "sql-a" || h.LogicalCores != 16 || h.TZ == nil {
		t.Fatalf("reopened hint = %+v ok=%v", h, ok)
	}

	// Saving an empty map replaces the content.
	if err := again.Save(loadFile(t, "empty.json")); err != nil {
		t.Fatal(err)
	}
	third, err := OpenSealed(path, fixtureProtector{})
	if err != nil || third.Map().Len() != 0 {
		t.Fatalf("reopen after empty save: %v", err)
	}
}

func TestSealedSaveFailureKeepsFileAndMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.dat")
	s, err := OpenSealed(path, fixtureProtector{})
	if err != nil {
		t.Fatal(err)
	}
	good := loadFile(t, "good.json")
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	s.seal = fixtureProtector{failProtect: true}
	if err := s.Save(loadFile(t, "empty.json")); err == nil {
		t.Fatal("seal failure not reported")
	}
	if err := s.Save(nil); err == nil {
		t.Fatal("nil map accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("a failed save changed hosts.dat")
	}
	if s.Map() != good {
		t.Fatal("a failed save changed the current map")
	}
}

func TestSealedBadFilesAreLeftUntouched(t *testing.T) {
	seal := func(plain string) []byte {
		b, _ := fixtureProtector{}.Protect([]byte(plain))
		return b
	}
	cases := map[string][]byte{
		"not sealed":      []byte(`{"version":1,"hosts":[]}`),
		"empty file":      {},
		"garbage":         bytes.Repeat([]byte{0xff}, 64),
		"wrong version":   seal(`{"version":2,"hosts":[]}`),
		"unknown field":   seal(`{"version":1,"hosts":[],"x":1}`),
		"duplicate label": seal(`{"version":1,"hosts":[{"label":"a"},{"label":"A"}]}`),
		"plaintext cap":   seal(`{"version":1,"hosts":[]}` + strings.Repeat(" ", MaxFileBytes)),
		"sealed cap":      bytes.Repeat([]byte("x"), maxSealedBytes+1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hosts.dat")
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := OpenSealed(path, fixtureProtector{})
			if err == nil || s != nil {
				t.Fatalf("opened a bad file: %v", err)
			}
			now, _ := os.ReadFile(path)
			if !bytes.Equal(now, content) {
				t.Fatal("a bad file was modified")
			}
		})
	}
}

func TestSealedNeedsPathAndProtector(t *testing.T) {
	if _, err := OpenSealed("", fixtureProtector{}); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := OpenSealed(filepath.Join(t.TempDir(), "hosts.dat"), nil); err == nil {
		t.Fatal("nil protector accepted")
	}
}
