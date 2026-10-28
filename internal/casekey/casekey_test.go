package casekey

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/masterkey"
)

var fast = masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1}

const pass = "correct horse battery"

func TestCheckIsHMACUnderTheDerivedKeyNotAHashOfThePassphrase(t *testing.T) {
	data, o, err := New([]byte(pass), fast, "case1", nil, StateActive)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Zero()
	if len(o.Check) != 64 || Ending(o.Check) != o.Check[60:] || len(o.Ending) != 4 {
		t.Fatalf("check %q ending %q", o.Check, o.Ending)
	}
	w, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if w.Ending != o.Ending {
		t.Fatalf("readable ending %q != %q", w.Ending, o.Ending)
	}
	// The file shows the ending only, never the full check value, the passphrase,
	// or the case master.
	for _, secret := range []string{o.Check, pass, fmt.Sprintf("%x", o.CM)} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("secret in the file")
		}
	}
	// Same passphrase, new salt: a different check value (it is keyed, not a hash).
	_, o2, err := New([]byte(pass), fast, "case1", nil, StateActive)
	if err != nil {
		t.Fatal(err)
	}
	defer o2.Zero()
	if Equal(o.Check, o2.Check) {
		t.Fatal("check value repeats across salts")
	}
}

func TestOpenRightWrongAndDamaged(t *testing.T) {
	data, o, err := New([]byte(pass), fast, "case1", nil, StateActive)
	if err != nil {
		t.Fatal(err)
	}
	cm := append([]byte(nil), o.CM...)
	o.Zero()
	w, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.Open([]byte(pass), "case1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.CM, cm) || got.State != StateActive {
		t.Fatal("wrong content")
	}
	got.Zero()
	if _, err := w.Open([]byte("a different phrase!"), "case1"); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key: %v", err)
	}
	// Another case's wrap moved into this folder.
	if _, err := w.Open([]byte(pass), "case2"); !errors.Is(err, ErrDamaged) {
		t.Fatalf("other case: %v", err)
	}
	// NFC: the composed and decomposed forms of the same text are one key.
	d2, o3, err := New([]byte("café café café x"), fast, "c", nil, StateActive)
	if err != nil {
		t.Fatal(err)
	}
	o3.Zero()
	w2, _ := Parse(d2)
	if g, err := w2.Open([]byte("café café café x"), "c"); err != nil {
		t.Fatalf("NFC: %v", err)
	} else {
		g.Zero()
	}
	// An edited readable ending is refused after the right key opens it.
	bad := strings.Replace(string(data), `"e":"`+w.Ending+`"`, `"e":"0000"`, 1)
	wb, err := Parse([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Open([]byte(pass), "case1"); !errors.Is(err, ErrDamaged) {
		t.Fatalf("edited ending: %v", err)
	}
}

func TestParseRefusesGarbageAndBadParams(t *testing.T) {
	for _, raw := range []string{"", "{", "[]", `{"v":2}`, `{"v":1,"salt":"zz"}`,
		`{"v":1,"salt":"00112233445566778899","params":{"t":99,"m":16384,"p":1},"state":"active","blob":"AA=="}`,
		`{"v":1,"salt":"00112233445566778899","params":{"t":1,"m":16384,"p":1},"state":"bogus","blob":"AA=="}`,
		strings.Repeat("x", 5000)} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrDamaged) {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}

func TestNewRefusesShortAndBlankKeys(t *testing.T) {
	if _, _, err := New([]byte("short"), fast, "c", nil, StateActive); !errors.Is(err, masterkey.ErrTooShort) {
		t.Fatalf("short: %v", err)
	}
	if _, _, err := New([]byte("            "), fast, "c", nil, StateActive); err == nil {
		t.Fatal("blank accepted")
	}
	if _, _, err := New([]byte(pass), masterkey.Params{Time: 1, Memory: 1, Threads: 1}, "c", nil, StateActive); err == nil {
		t.Fatal("tiny params accepted")
	}
}

func TestSealAgainKeepsKeyAndChangesState(t *testing.T) {
	_, o, err := New([]byte(pass), fast, "c", nil, StateSetting)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Zero()
	data, err := o.Seal(StateActive)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := Parse(data)
	g, err := w.Open([]byte(pass), "c")
	if err != nil || g.State != StateActive || !Equal(g.Check, o.Check) {
		t.Fatalf("%v %+v", err, g)
	}
	g.Zero()
	if _, err := o.Seal("nope"); err == nil {
		t.Fatal("bad state sealed")
	}
}

func TestOpenedNeverPrintsSecrets(t *testing.T) {
	_, o, _ := New([]byte(pass), fast, "c", nil, StateActive)
	defer o.Zero()
	for _, s := range []string{fmt.Sprintf("%v", o), fmt.Sprintf("%+v", o), fmt.Sprintf("%#v", o), fmt.Sprint(o)} {
		if strings.Contains(s, o.Check) || strings.Contains(s, fmt.Sprintf("%x", o.CM)) {
			t.Fatalf("printed a secret: %s", s)
		}
	}
}

// A named salt gives the same key ending twice (the demo's keyed case); a wrong-size salt is
// refused; a nil salt is fresh every time.
func TestNewWithSaltIsRepeatableAndNilSaltIsNot(t *testing.T) {
	salt := []byte("0123456789abcdef")
	_, a, err := NewWithSalt([]byte(pass), fast, "case1", nil, StateActive, salt)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := NewWithSalt([]byte(pass), fast, "case1", nil, StateActive, salt)
	if err != nil {
		t.Fatal(err)
	}
	if a.Ending != b.Ending || a.Check != b.Check {
		t.Fatalf("same salt and key, endings %s and %s", a.Ending, b.Ending)
	}
	if _, _, err := NewWithSalt([]byte(pass), fast, "case1", nil, StateActive, []byte("short")); err == nil {
		t.Fatal("a salt of the wrong size was accepted")
	}
	_, c, _ := New([]byte(pass), fast, "case1", nil, StateActive)
	_, d, _ := New([]byte(pass), fast, "case1", nil, StateActive)
	if c.Check == d.Check {
		t.Fatal("two fresh salts gave one check value")
	}
}
