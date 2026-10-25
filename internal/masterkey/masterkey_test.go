package masterkey

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/sealedfile"
)

const goodPass = "correct horse battery"

func setup(t *testing.T, pass string) string {
	t.Helper()
	dir := t.TempDir()
	if err := Setup(dir, []byte(pass)); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return dir
}

func TestSetupUnlockRoundTrip(t *testing.T) {
	dir := setup(t, goodPass)
	kr, err := Unlock(dir, []byte(goodPass))
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if kr.Locked() || len(kr.key) != KeyLen {
		t.Fatal("keyring should hold a 32-byte key")
	}
	if !Exists(dir) {
		t.Fatal("Exists should be true")
	}
	// The file holds salt, parameters and verifier only.
	raw, _ := os.ReadFile(Path(dir))
	if bytes.Contains(raw, []byte(goodPass)) || bytes.Contains(raw, []byte(hex.EncodeToString(kr.key))) {
		t.Fatal("masterkey.dat leaks the passphrase or key")
	}
}

func TestUnlockZeroesPassphrase(t *testing.T) {
	dir := setup(t, goodPass)
	pass := []byte(goodPass)
	if _, err := Unlock(dir, pass); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pass, make([]byte, len(pass))) {
		t.Fatal("passphrase slice not zeroed")
	}
}

func TestNotSet(t *testing.T) {
	if _, err := Unlock(t.TempDir(), []byte(goodPass)); !errors.Is(err, ErrNotSet) {
		t.Fatalf("want ErrNotSet, got %v", err)
	}
	if Exists(t.TempDir()) {
		t.Fatal("Exists should be false")
	}
}

func TestWrongPassphrase(t *testing.T) {
	dir := setup(t, goodPass)
	if _, err := Unlock(dir, []byte("correct horse batterz")); err != ErrWrongKey {
		t.Fatalf("want ErrWrongKey, got %v", err)
	}
}

func TestShortPassphraseRefused(t *testing.T) {
	dir := t.TempDir()
	if err := Setup(dir, []byte("12345678901")); !errors.Is(err, ErrTooShort) { // 11
		t.Fatalf("11 chars: want ErrTooShort, got %v", err)
	}
	if Exists(dir) {
		t.Fatal("nothing should be written for a refused passphrase")
	}
	// Characters, not bytes: 12 multi-byte runes pass, 11 do not.
	if err := Setup(t.TempDir(), []byte(strings.Repeat("é", 11))); !errors.Is(err, ErrTooShort) {
		t.Fatalf("11 runes: want ErrTooShort, got %v", err)
	}
	if err := Setup(t.TempDir(), []byte(strings.Repeat("é", 12))); err != nil {
		t.Fatalf("12 runes: %v", err)
	}
}

func TestSetupRefusesOverwrite(t *testing.T) {
	dir := setup(t, goodPass)
	if err := Setup(dir, []byte("another long passphrase")); !errors.Is(err, ErrAlreadySet) {
		t.Fatalf("want ErrAlreadySet, got %v", err)
	}
	if _, err := Unlock(dir, []byte(goodPass)); err != nil {
		t.Fatalf("original passphrase must still unlock: %v", err)
	}
}

func TestNFCAndNFDUnlockSameKey(t *testing.T) {
	nfc := "café au lait été"    // precomposed
	nfd := "café au lait été" // decomposed
	if nfc == nfd {
		t.Fatal("test strings must differ in bytes")
	}
	dir := setup(t, nfc)
	a, err := Unlock(dir, []byte(nfc))
	if err != nil {
		t.Fatalf("NFC unlock: %v", err)
	}
	b, err := Unlock(dir, []byte(nfd))
	if err != nil {
		t.Fatalf("NFD unlock: %v", err)
	}
	if !bytes.Equal(a.key, b.key) {
		t.Fatal("NFC and NFD forms must give the same key")
	}
	// And the other way round.
	dir2 := setup(t, nfd)
	if _, err := Unlock(dir2, []byte(nfc)); err != nil {
		t.Fatalf("set NFD, unlock NFC: %v", err)
	}
}

func TestProtectRoundTrip(t *testing.T) {
	kr, _ := Unlock(setup(t, goodPass), []byte(goodPass))
	msg := []byte("case key material")
	sealed, err := kr.Protect(msg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, msg) {
		t.Fatal("ciphertext contains plaintext")
	}
	if sealed[0] != blobVersion {
		t.Fatal("version byte missing")
	}
	again, _ := kr.Protect(msg)
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce must be random per call")
	}
	got, err := kr.Unprotect(sealed)
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	// Empty plaintext is fine.
	e, err := kr.Protect(nil)
	if err != nil {
		t.Fatal(err)
	}
	if g, err := kr.Unprotect(e); err != nil || len(g) != 0 {
		t.Fatalf("empty round trip: %v %v", g, err)
	}
	var _ sealedfile.Protector = kr
}

func TestBadCiphertextsGiveOneError(t *testing.T) {
	kr, _ := Unlock(setup(t, goodPass), []byte(goodPass))
	other, _ := Unlock(setup(t, "a completely different one"), []byte("a completely different one"))
	sealed, _ := kr.Protect([]byte("payload"))

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	badVersion := append([]byte(nil), sealed...)
	badVersion[0] = 9
	otherLabel, _ := kr.Seal("some/other/label", []byte("payload"))
	fromOther, _ := other.Protect([]byte("payload"))

	cases := map[string][]byte{
		"tampered tail":  tampered,
		"truncated":      sealed[:len(sealed)-3],
		"header only":    sealed[:5],
		"empty":          nil,
		"bad version":    badVersion,
		"other label":    otherLabel,
		"other key":      fromOther,
		"all zero bytes": make([]byte, 64),
	}
	for name, blob := range cases {
		out, err := kr.Unprotect(blob)
		if err != ErrCiphertext || out != nil {
			t.Errorf("%s: want ErrCiphertext, got %v (%d bytes)", name, err, len(out))
		}
	}
	// Labelled protectors are independent.
	pc := kr.WithLabel("case.key")
	s, _ := pc.Protect([]byte("x"))
	if _, err := kr.WithLabel("manifest").Unprotect(s); err != ErrCiphertext {
		t.Fatalf("label mismatch should fail: %v", err)
	}
	if g, err := pc.Unprotect(s); err != nil || string(g) != "x" {
		t.Fatalf("labelled round trip: %v", err)
	}
}

func TestLock(t *testing.T) {
	kr, _ := Unlock(setup(t, goodPass), []byte(goodPass))
	sealed, _ := kr.Protect([]byte("x"))
	pc := kr.WithLabel("l")
	keyAlias := kr.key // same backing array the keyring zeroes
	if bytes.Equal(keyAlias, make([]byte, KeyLen)) {
		t.Fatal("key should be non-zero before Lock")
	}
	kr.Lock()
	if !bytes.Equal(keyAlias, make([]byte, KeyLen)) {
		t.Fatal("key bytes not zeroed by Lock")
	}
	if !kr.Locked() || kr.key != nil {
		t.Fatal("keyring should report locked")
	}
	if _, err := kr.Protect([]byte("x")); err != ErrLocked {
		t.Fatalf("Protect after Lock: %v", err)
	}
	if _, err := kr.Unprotect(sealed); err != ErrLocked {
		t.Fatalf("Unprotect after Lock: %v", err)
	}
	if _, err := pc.Protect([]byte("x")); err != ErrLocked {
		t.Fatalf("labelled Protect after Lock: %v", err)
	}
	kr.Lock() // idempotent
}

func TestConcurrentUseAndLock(t *testing.T) {
	kr, _ := Unlock(setup(t, goodPass), []byte(goodPass))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s, err := kr.Protect([]byte("x"))
				if err == nil {
					if _, err := kr.Unprotect(s); err != nil && err != ErrLocked {
						t.Errorf("unexpected: %v", err)
					}
				} else if err != ErrLocked {
					t.Errorf("unexpected: %v", err)
				}
			}
		}()
	}
	kr.Lock()
	wg.Wait()
}

func rewrite(t *testing.T, dir string, edit func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(Path(dir), out, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDamagedParametersRejectedWithoutAllocating(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"huge memory":  func(m map[string]any) { m["m"] = 4294967295 },
		"huge time":    func(m map[string]any) { m["t"] = 4000000000 },
		"zero time":    func(m map[string]any) { m["t"] = 0 },
		"zero memory":  func(m map[string]any) { m["m"] = 0 },
		"zero threads": func(m map[string]any) { m["p"] = 0 },
		"many threads": func(m map[string]any) { m["p"] = 255 },
		"bad salt":     func(m map[string]any) { m["salt"] = "zz" },
		"empty salt":   func(m map[string]any) { m["salt"] = "" },
		"bad verifier": func(m map[string]any) { m["verifier"] = "00" },
		"bad version":  func(m map[string]any) { m["v"] = 7 },
		"bad kdf":      func(m map[string]any) { m["kdf"] = "scrypt" },
	} {
		t.Run(name, func(t *testing.T) {
			dir := setup(t, goodPass)
			rewrite(t, dir, edit)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := Unlock(dir, []byte(goodPass))
			runtime.ReadMemStats(&after)
			if err != ErrWrongKey {
				t.Fatalf("want ErrWrongKey, got %v", err)
			}
			// A bad verifier has valid parameters, so the key is legitimately derived.
			if d := after.TotalAlloc - before.TotalAlloc; d > 8<<20 && name != "bad verifier" {
				t.Fatalf("allocated %d bytes for damaged file", d)
			}
		})
	}
}

func TestDamagedFilesGiveWrongKey(t *testing.T) {
	dir := setup(t, goodPass)
	for name, content := range map[string][]byte{
		"empty":     {},
		"garbage":   []byte("not json"),
		"oversized": bytes.Repeat([]byte("a"), fileMax+1),
	} {
		if err := os.WriteFile(Path(dir), content, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Unlock(dir, []byte(goodPass)); err != ErrWrongKey {
			t.Errorf("%s: want ErrWrongKey, got %v", name, err)
		}
	}
}

func TestStoredParamsAreDefaults(t *testing.T) {
	dir := setup(t, goodPass)
	raw, _ := os.ReadFile(Path(dir))
	var d fileDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Params != (Params{Time: 3, Memory: 64 << 10, Threads: 4}) || len(d.Salt) != 32 {
		t.Fatalf("unexpected stored params: %+v salt=%d", d.Params, len(d.Salt))
	}
}

func TestNoSecretsInErrorsOrLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	defer func() { log.SetOutput(prev); log.SetFlags(prevFlags) }()

	secret := "sup3r-secret-pass-phrase"
	dir := t.TempDir()
	var errs []string
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err.Error(), fmt.Sprintf("%v|%+v|%#v", err, err, err))
		}
	}
	collect(Setup(dir, []byte("short")))
	collect(Setup(dir, []byte(secret)))
	collect(Setup(dir, []byte(secret)))
	_, err := Unlock(dir, []byte("wrong"+secret))
	collect(err)
	kr, err := Unlock(dir, []byte(secret))
	collect(err)
	_, err = kr.Unprotect([]byte("junk junk junk junk junk junk junk junk"))
	collect(err)
	kr.Lock()
	_, err = kr.Protect(nil)
	collect(err)
	_, err = Unlock(t.TempDir(), []byte(secret))
	collect(err)

	keyHex := ""
	if kr2, err := Unlock(dir, []byte(secret)); err == nil {
		keyHex = hex.EncodeToString(kr2.key)
		_ = keyHex
	}
	hay := strings.Join(errs, "\n") + buf.String()
	for _, needle := range []string{secret, "wrong" + secret, keyHex} {
		if needle != "" && strings.Contains(hay, needle) {
			t.Fatalf("secret %q found in error text or log", needle[:4])
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("package wrote to the log: %q", buf.String())
	}
}
