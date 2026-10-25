package masterkey

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

var p06Fast = Params{Time: 1, Memory: 16 << 10, Threads: 1}

func p06Key(t *testing.T, pass string) []byte {
	t.Helper()
	k, err := DeriveKey([]byte(pass), []byte("0123456789abcdef"), p06Fast)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Typed on a Mac or pasted from a PDF, the same words arrive composed (NFC) or
// decomposed (NFD). They must be one key. Compatibility lookalikes (a ligature,
// full-width letters) are NOT folded: they are different keys, on purpose.
func TestP06_KeyDerivationFoldsCompositionOnly(t *testing.T) {
	nfc := "café crème brûlée"
	nfd := "café crème brûlée"
	if !bytes.Equal(p06Key(t, nfc), p06Key(t, nfd)) {
		t.Fatal("NFC and NFD spellings give different keys")
	}
	if bytes.Equal(p06Key(t, "office plan 2024"), p06Key(t, "oﬃce plan 2024")) {
		t.Fatal("a ligature folded into its letters (NFKC) when only NFC was promised")
	}
	if bytes.Equal(p06Key(t, "alpha bravo charlie"), p06Key(t, "ａlpha bravo charlie")) {
		t.Fatal("a full-width letter folded into its ASCII twin")
	}
}

// A trailing space, newline or tab is part of the passphrase and is never
// trimmed: a trailing space is a different key and the screens must say so.
func TestP06_WhitespaceAtTheEndsIsNeverTrimmed(t *testing.T) {
	base := "alpha bravo charlie delta"
	if bytes.Equal(p06Key(t, base), p06Key(t, base+" ")) || bytes.Equal(p06Key(t, base), p06Key(t, " "+base)) {
		t.Fatal("leading or trailing space was trimmed")
	}
	if err := CheckPassphrase([]byte(base + " ")); err != nil {
		t.Fatalf("a trailing space is refused: %v", err)
	}
	// Characterisation: the rule check does not refuse control characters (a
	// browser strips line breaks from a single-line field, so only a direct API
	// call can send one). They are kept as typed, like a space.
	for _, tail := range []string{"\n", "\r\n", "\t"} {
		if CheckPassphrase([]byte(base+tail)) == nil && bytes.Equal(p06Key(t, base), p06Key(t, base+tail)) {
			t.Errorf("a trailing %q was trimmed", tail)
		}
	}
}

// A masterkey.dat cut short, or flipped in a byte, is damaged or a wrong key:
// never a panic, never success, and never an unbounded derivation.
func TestP06_DamagedKeyFileNeverUnlocksOrHangs(t *testing.T) {
	pass := "alpha bravo charlie delta"
	src := t.TempDir()
	if err := Setup(src, []byte(pass)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(src))
	if err != nil {
		t.Fatal(err)
	}
	try := func(name string, b []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(Path(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		k, err := Unlock(dir, []byte(pass))
		if err == nil {
			k.Lock()
			t.Errorf("%s: unlocked", name)
		} else if !errors.Is(err, ErrDamaged) && !errors.Is(err, ErrWrongKey) {
			t.Errorf("%s: error %v", name, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s took %s", name, d)
		}
	}
	for _, n := range []int{0, 1, len(data) / 2, len(data) - 1} {
		try("cut@"+strconv.Itoa(n), data[:n])
	}
	step := len(data) / 10
	for o := 0; o < len(data); o += step {
		b := append([]byte(nil), data...)
		b[o] ^= 0x01
		try("flip@"+strconv.Itoa(o), b)
	}
	try("not a file at all", []byte("hello"))
}
