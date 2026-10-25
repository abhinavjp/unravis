package casestore_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// rewrapFixture is a case folder holding a DPAPI-style (v1) case.key and
// manifest.dat.
type rewrapFixture struct {
	dir      string
	dp       *storetest.Protector
	keys     casestore.Keys
	manifest []byte
}

func newRewrapFixture(t *testing.T) *rewrapFixture {
	t.Helper()
	f := &rewrapFixture{dir: t.TempDir(), dp: storetest.RandomProtector(), manifest: []byte(`{"title":"Quarterly review of the thing"}`)}
	f.keys, _ = casestore.NewKeys()
	f.keys.CaseID = "dddddddddddddddddddddddddd"
	if err := casestore.WriteKeyFile(filepath.Join(f.dir, casestore.KeyFileName), f.dp, f.keys); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.dp.Protect(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, casestore.ManifestFileName), casestore.WrapSealed(casestore.KindDPAPI, sealed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "case.db"), []byte("pretend database bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rewrapFixture) read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *rewrapFixture) lookup(kr *masterkey.Keyring) casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		switch kind {
		case casestore.KindDPAPI:
			return f.dp, nil
		case casestore.KindMasterKey:
			if kr == nil {
				return nil, casestore.ErrLocked
			}
			return kr, nil
		}
		return nil, casestore.ErrKeyFile
	}
}

func (f *rewrapFixture) from() casestore.Sealing {
	return casestore.Sealing{Kind: casestore.KindDPAPI, P: f.dp}
}

// checkKinds asserts the recorded kind of both files.
func (f *rewrapFixture) checkKinds(t *testing.T, key, man string) {
	t.Helper()
	if k, err := casestore.FileKind(filepath.Join(f.dir, casestore.KeyFileName)); err != nil || k != key {
		t.Fatalf("case.key kind = %q, %v; want %q", k, err, key)
	}
	if k, err := casestore.FileKind(filepath.Join(f.dir, casestore.ManifestFileName)); err != nil || k != man {
		t.Fatalf("manifest.dat kind = %q, %v; want %q", k, err, man)
	}
}

// opensUnderMaster asserts both files now open with the master key only and carry
// the original contents.
func (f *rewrapFixture) opensUnderMaster(t *testing.T, kr *masterkey.Keyring) {
	t.Helper()
	got, kind, err := casestore.ReadKeyFileAny(filepath.Join(f.dir, casestore.KeyFileName), masterOnly(kr))
	if err != nil || kind != casestore.KindMasterKey || !sameKeys(got, f.keys) {
		t.Fatalf("case.key under master key: kind=%q err=%v", kind, err)
	}
	_, blob, err := casestore.UnwrapSealed(f.read(t, casestore.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := kr.Open(casestore.LabelManifest, blob)
	if err != nil || !bytes.Equal(plain, f.manifest) {
		t.Fatalf("manifest under master key: %v", err)
	}
}

func (f *rewrapFixture) noTemps(t *testing.T) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(f.dir, "*.tmp"))
	if len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestRewrapMovesBothFilesAndLeavesTheDatabase(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	db := f.read(t, "case.db")
	dbInfo, _ := os.Stat(filepath.Join(f.dir, "case.db"))

	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}
	if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{}); err != nil {
		t.Fatal(err)
	}
	f.checkKinds(t, casestore.KindMasterKey, casestore.KindMasterKey)
	f.opensUnderMaster(t, kr)
	f.noTemps(t)
	if !bytes.Equal(f.read(t, "case.db"), db) {
		t.Fatal("case.db bytes changed")
	}
	if after, _ := os.Stat(filepath.Join(f.dir, "case.db")); !after.ModTime().Equal(dbInfo.ModTime()) {
		t.Fatal("case.db was rewritten")
	}
	// The old protector alone no longer opens it, and the file holds no plain key.
	if _, err := casestore.ReadKeyFile(filepath.Join(f.dir, casestore.KeyFileName), f.dp); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("DPAPI-only read of a re-wrapped key = %v, want ErrLocked", err)
	}
	raw := f.read(t, casestore.KeyFileName)
	if bytes.Contains(raw, f.keys.DB) || bytes.Contains(raw, []byte(hex.EncodeToString(f.keys.DB))) {
		t.Fatal("key bytes in the re-wrapped file")
	}
}

func TestRewrapTwiceIsANoOp(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}
	if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{}); err != nil {
		t.Fatal(err)
	}
	keyBefore, manBefore := f.read(t, casestore.KeyFileName), f.read(t, casestore.ManifestFileName)
	called := false
	err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Step: func(string) error { called = true; return nil }})
	if err != nil {
		t.Fatalf("second re-wrap: %v", err)
	}
	if called {
		t.Fatal("a second re-wrap staged or replaced something")
	}
	if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) || !bytes.Equal(manBefore, f.read(t, casestore.ManifestFileName)) {
		t.Fatal("a second re-wrap rewrote the files")
	}
	f.opensUnderMaster(t, kr)
	f.noTemps(t)
}

func TestRewrapDirectionAndLocked(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	keyBefore, manBefore := f.read(t, casestore.KeyFileName), f.read(t, casestore.ManifestFileName)
	same := func() {
		t.Helper()
		if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) || !bytes.Equal(manBefore, f.read(t, casestore.ManifestFileName)) {
			t.Fatal("files changed")
		}
		f.noTemps(t)
	}
	master := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}

	// Only DPAPI to master key.
	for name, c := range map[string][2]casestore.Sealing{
		"back":        {master, f.from()},
		"same":        {f.from(), f.from()},
		"master same": {master, master},
		"unknown":     {f.from(), {Kind: "tpm", P: f.dp}},
		"no from":     {{Kind: casestore.KindDPAPI}, master},
	} {
		if err := casestore.Rewrap(f.dir, c[0], c[1], casestore.RewrapOptions{}); !errors.Is(err, casestore.ErrRewrapDirection) {
			t.Errorf("%s = %v, want ErrRewrapDirection", name, err)
		}
	}
	same()

	// A locked or missing target writes nothing.
	kr.Lock()
	for name, to := range map[string]casestore.Sealing{
		"locked":  master,
		"missing": {Kind: casestore.KindMasterKey},
	} {
		if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{}); !errors.Is(err, casestore.ErrLocked) {
			t.Errorf("%s = %v, want ErrLocked", name, err)
		}
	}
	same()
}

func TestRewrapInjectedFailuresLeaveACaseThatOpens(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	keyBefore, manBefore := f.read(t, casestore.KeyFileName), f.read(t, casestore.ManifestFileName)
	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}
	boom := errors.New("simulated power cut")

	// Failure after both temp files are written and verified, before any replace:
	// the old files are byte-identical, the temps are gone, the case still opens.
	err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Step: func(stage string) error {
		if stage == "staged" {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("staged failure = %v", err)
	}
	if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) || !bytes.Equal(manBefore, f.read(t, casestore.ManifestFileName)) {
		t.Fatal("a failure before any replace changed the old files")
	}
	f.noTemps(t)
	f.checkKinds(t, casestore.KindDPAPI, casestore.KindDPAPI)
	if got, kind, err := casestore.ReadKeyFileAny(filepath.Join(f.dir, casestore.KeyFileName), f.lookup(nil)); err != nil || kind != casestore.KindDPAPI || !sameKeys(got, f.keys) {
		t.Fatalf("old case.key no longer opens: %v", err)
	}

	// Failure after the manifest was replaced but before case.key: the old case.key
	// is untouched and a second run finishes the move.
	err = casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Step: func(stage string) error {
		if stage == "manifest-replaced" {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("manifest-replaced failure = %v", err)
	}
	if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) {
		t.Fatal("case.key changed although the failure was before its replace")
	}
	f.checkKinds(t, casestore.KindDPAPI, casestore.KindMasterKey)
	f.noTemps(t)
	if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{}); err != nil {
		t.Fatalf("finishing a half-moved case: %v", err)
	}
	f.checkKinds(t, casestore.KindMasterKey, casestore.KindMasterKey)
	f.opensUnderMaster(t, kr)
	f.noTemps(t)
}

func TestRewrapFailureAfterTheLastReplaceIsStillMoved(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}
	boom := errors.New("late failure")
	err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Step: func(stage string) error {
		if stage == "key-replaced" {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	f.checkKinds(t, casestore.KindMasterKey, casestore.KindMasterKey)
	f.opensUnderMaster(t, kr)
	f.noTemps(t)
}

// brokenSeal is a master-key protector whose output does not open again, so the
// read-back check must catch it before anything is replaced.
type brokenSeal struct{ *masterkey.Keyring }

func (b brokenSeal) WithLabel(label string) sealedfile.Protector {
	return brokenLabelled{b.Keyring.WithLabel(label)}
}

type brokenLabelled struct{ sealedfile.Protector }

func (b brokenLabelled) Protect(p []byte) ([]byte, error) {
	out, err := b.Protector.Protect(p)
	if err == nil && len(out) > 0 {
		out[len(out)-1] ^= 1 // damage the tag
	}
	return out, err
}

func TestRewrapVerifiesTheNewFileBeforeReplacing(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	keyBefore, manBefore := f.read(t, casestore.KeyFileName), f.read(t, casestore.ManifestFileName)
	err := casestore.Rewrap(f.dir, f.from(), casestore.Sealing{Kind: casestore.KindMasterKey, P: brokenSeal{kr}}, casestore.RewrapOptions{})
	if err == nil {
		t.Fatal("a new file that does not open was accepted")
	}
	if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) || !bytes.Equal(manBefore, f.read(t, casestore.ManifestFileName)) {
		t.Fatal("old files changed although the new ones failed verification")
	}
	f.noTemps(t)
}

func TestRewrapWrongOldProtectorChangesNothing(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	keyBefore, manBefore := f.read(t, casestore.KeyFileName), f.read(t, casestore.ManifestFileName)
	wrong := casestore.Sealing{Kind: casestore.KindDPAPI, P: storetest.RandomProtector()}
	err := casestore.Rewrap(f.dir, wrong, casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}, casestore.RewrapOptions{})
	if !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("wrong old protector = %v, want ErrKeyFile", err)
	}
	if !bytes.Equal(keyBefore, f.read(t, casestore.KeyFileName)) || !bytes.Equal(manBefore, f.read(t, casestore.ManifestFileName)) {
		t.Fatal("files changed")
	}
	f.noTemps(t)
}

func TestRewrapEnterHookAndMissingFiles(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}

	deny := errors.New("a writer is busy")
	if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Enter: func() (func(), error) { return nil, deny }}); !errors.Is(err, deny) {
		t.Fatalf("Enter error = %v", err)
	}
	f.checkKinds(t, casestore.KindDPAPI, casestore.KindDPAPI)

	left := false
	if err := casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Enter: func() (func(), error) { return func() { left = true }, nil }}); err != nil {
		t.Fatal(err)
	}
	if !left {
		t.Fatal("the leave func did not run")
	}

	// A missing manifest or key is refused and nothing is created.
	g := newRewrapFixture(t)
	if err := os.Remove(filepath.Join(g.dir, casestore.ManifestFileName)); err != nil {
		t.Fatal(err)
	}
	if err := casestore.Rewrap(g.dir, g.from(), to, casestore.RewrapOptions{}); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("missing manifest = %v, want ErrKeyFile", err)
	}
	h := newRewrapFixture(t)
	if err := os.Remove(filepath.Join(h.dir, casestore.KeyFileName)); err != nil {
		t.Fatal(err)
	}
	if err := casestore.Rewrap(h.dir, h.from(), to, casestore.RewrapOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing key = %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, casestore.KeyFileName)); err == nil {
		t.Fatal("a case.key appeared for a case that had none")
	}
}

func TestRewrapErrorsNeverCarryKeyBytes(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	f := newRewrapFixture(t)
	secrets := []string{hex.EncodeToString(f.keys.DB), hex.EncodeToString(f.keys.Pseudo), string(f.manifest)}
	to := casestore.Sealing{Kind: casestore.KindMasterKey, P: kr}
	boom := errors.New("injected")

	var errs []error
	errs = append(errs, casestore.Rewrap(f.dir, f.from(), casestore.Sealing{Kind: casestore.KindDPAPI, P: storetest.RandomProtector()}, casestore.RewrapOptions{}))
	errs = append(errs, casestore.Rewrap(f.dir, f.from(), casestore.Sealing{Kind: casestore.KindMasterKey, P: brokenSeal{kr}}, casestore.RewrapOptions{}))
	errs = append(errs, casestore.Rewrap(f.dir, to, f.from(), casestore.RewrapOptions{}))
	errs = append(errs, casestore.Rewrap(f.dir, f.from(), casestore.Sealing{Kind: casestore.KindMasterKey}, casestore.RewrapOptions{}))
	errs = append(errs, casestore.Rewrap(f.dir, f.from(), to, casestore.RewrapOptions{Step: func(string) error { return boom }}))
	for i, err := range errs {
		if err == nil {
			t.Errorf("case %d: expected an error", i)
			continue
		}
		for _, s := range secrets {
			if strings.Contains(err.Error(), s) {
				t.Errorf("case %d: error text contains key or manifest bytes: %v", i, err)
			}
		}
	}
}
