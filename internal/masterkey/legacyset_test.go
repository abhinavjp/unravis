package masterkey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func legacyRing(t *testing.T, dir, pass string) *Keyring {
	t.Helper()
	if err := Setup(dir, []byte(pass)); err != nil {
		t.Fatal(err)
	}
	k, err := Unlock(dir, []byte(pass))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Lock)
	return k
}

func TestP06_LegacySetRoundTripHoldsNoIDInPlain(t *testing.T) {
	dir := t.TempDir()
	k := legacyRing(t, dir, "correct horse battery")
	ids := []string{"01JCASEIDAAAAAAAAAAAAAAAAB", "01JCASEIDAAAAAAAAAAAAAAAAA"}
	if err := WriteLegacySet(dir, k, ids); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(LegacySetPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if bytes.Contains(raw, []byte(id)) {
			t.Fatalf("legacy set file holds %q in plain", id)
		}
	}
	if bytes.Contains(raw, []byte("ids")) {
		t.Fatal("legacy set file holds its JSON keys in plain")
	}
	got, st, err := ReadLegacySet(dir, k)
	if err != nil || st != LegacyOK {
		t.Fatalf("ReadLegacySet = %v, %v, %v", got, st, err)
	}
	if !reflect.DeepEqual(got, []string{ids[1], ids[0]}) {
		t.Fatalf("ids = %v", got)
	}
	// An empty set is a set, not an absent file.
	if err := WriteLegacySet(dir, k, nil); err != nil {
		t.Fatal(err)
	}
	if got, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyOK || len(got) != 0 {
		t.Fatalf("empty set = %v, %v, %v", got, st, err)
	}
	// No temp file is left behind.
	if m, _ := filepath.Glob(filepath.Join(dir, "legacyset.*.tmp")); len(m) != 0 {
		t.Fatalf("temp files left: %v", m)
	}
}

func TestP06_LegacySetAbsentDamagedAndLocked(t *testing.T) {
	dir := t.TempDir()
	k := legacyRing(t, dir, "correct horse battery")
	if _, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyAbsent {
		t.Fatalf("absent = %v, %v", st, err)
	}
	if err := WriteLegacySet(dir, k, []string{"abc"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(LegacySetPath(dir))

	for name, content := range map[string][]byte{
		"garbage":   []byte("this is not a sealed set at all, not even close"),
		"truncated": raw[:len(raw)/2],
		"flipped":   append(append([]byte{}, raw[:len(raw)-1]...), raw[len(raw)-1]^1),
	} {
		if err := os.WriteFile(LegacySetPath(dir), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if ids, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyDamaged || ids != nil {
			t.Fatalf("%s = %v, %v, %v, want damaged", name, ids, st, err)
		}
	}
	// A set sealed under another master key is damaged for this one.
	dir2 := t.TempDir()
	k2 := legacyRing(t, dir2, "another passphrase here")
	if err := WriteLegacySet(dir, k2, []string{"abc"}); err != nil {
		t.Fatal(err)
	}
	if _, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyDamaged {
		t.Fatalf("other key's set = %v, %v", st, err)
	}
	// An empty file and a directory in its place are damaged too.
	if err := os.WriteFile(LegacySetPath(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyDamaged {
		t.Fatalf("empty file = %v, %v", st, err)
	}
	_ = os.Remove(LegacySetPath(dir))
	if err := os.Mkdir(LegacySetPath(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, st, err := ReadLegacySet(dir, k); err != nil || st != LegacyDamaged {
		t.Fatalf("directory = %v, %v", st, err)
	}
	// A locked Keyring is an error, never "absent" or "damaged".
	_ = os.Remove(LegacySetPath(dir))
	if err := WriteLegacySet(dir, k, []string{"abc"}); err != nil {
		t.Fatal(err)
	}
	k.Lock()
	if _, _, err := ReadLegacySet(dir, k); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked read = %v", err)
	}
	if err := WriteLegacySet(dir, k, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked write = %v", err)
	}
	if _, _, err := ReadLegacySet(dir, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("nil ring read = %v", err)
	}
}
