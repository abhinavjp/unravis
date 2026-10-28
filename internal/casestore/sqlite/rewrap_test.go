package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

func rewrapKeyring(t *testing.T, pass string) *masterkey.Keyring {
	t.Helper()
	dir := t.TempDir()
	if err := masterkey.Setup(dir, []byte(pass)); err != nil {
		t.Fatal(err)
	}
	kr, err := masterkey.Unlock(dir, []byte(pass))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Lock)
	return kr
}

func keyringLookup(kr *masterkey.Keyring) casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindMasterKey {
			return kr, nil
		}
		return nil, casestore.ErrLocked
	}
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	return sha256.Sum256(readFile(t, path))
}

// dpapiCase makes a store with the test (DPAPI stand-in) protector and one
// imported, closed-handle case. It returns the store, root, protector and case.
func dpapiCase(t *testing.T) (*Store, string, *storetest.Protector, casestore.CaseInfo) {
	t.Helper()
	p := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, p)
	info := mustCreate(t, s, "Rewrap me")
	c, err := s.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	mustImport(t, c.(*Case), "one", 5)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return s, root, p, info
}

func masterStore(t *testing.T, root string, kr *masterkey.Keyring, mut ...func(*Options)) *Store {
	t.Helper()
	// A different DPAPI protector: proves nothing here depends on the old one.
	return newStoreAt(t, root, storetest.RandomProtector(), append([]func(*Options){func(o *Options) { o.Protectors = keyringLookup(kr) }}, mut...)...)
}

func TestRewrapCaseOpensWithTheKeyringAlone(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, p, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	ctx := context.Background()
	dbPath := filepath.Join(root, string(info.ID), dbName)
	before := fileHash(t, dbPath)

	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindDPAPI {
		t.Fatalf("CaseKind before = %q, %v", k, err)
	}
	if err := s.Rewrap(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("CaseKind after = %q, %v", k, err)
	}
	if fileHash(t, dbPath) != before {
		t.Fatal("case.db bytes changed by Rewrap")
	}
	// A second Rewrap is a no-op.
	keyBytes := readFile(t, filepath.Join(root, string(info.ID), keyName))
	if err := s.Rewrap(ctx, info.ID); err != nil {
		t.Fatalf("second Rewrap: %v", err)
	}
	if !bytes.Equal(keyBytes, readFile(t, filepath.Join(root, string(info.ID), keyName))) {
		t.Fatal("second Rewrap rewrote case.key")
	}

	// A fresh Store with another DPAPI protector and the Keyring opens it and sees
	// the data.
	s2 := masterStore(t, root, kr)
	c := mustOpen(t, s2, info.ID)
	if imp, ev, _ := counts(t, c); imp != 1 || ev != 5 {
		t.Fatalf("after re-wrap: %d imports, %d events", imp, ev)
	}
	mustImport(t, c, "two", 3) // writes through the master-key sealed manifest
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	list, err := s2.List(ctx)
	if err != nil || len(list) != 1 || list[0].Title != "Rewrap me" || list[0].Imports != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if k := caseKind(filepath.Join(root, string(info.ID))); k != casestore.KindMasterKey {
		t.Fatalf("kind after an import = %q", k)
	}

	// The old DPAPI-only store can no longer open it, and says locked.
	s3 := newStoreAt(t, root, p)
	if _, err := s3.Open(ctx, info.ID); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("DPAPI-only open of a master-key case = %v, want ErrLocked", err)
	}
}

func TestMasterKeyCaseLockedRefusesAndRevealsNothing(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	if err := s.Rewrap(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := filepath.Join(root, string(info.ID))

	// Neither the case key nor the manifest holds the title in the clear.
	for _, n := range []string{keyName, manifestName} {
		if bytes.Contains(readFile(t, filepath.Join(dir, n)), []byte("Rewrap me")) {
			t.Fatalf("%s holds the title in plain", n)
		}
	}

	check := func(name string, st *Store) {
		t.Helper()
		if _, err := st.Open(ctx, info.ID); !errors.Is(err, casestore.ErrLocked) {
			t.Errorf("%s: Open = %v, want ErrLocked", name, err)
		}
		if err := st.CloseCase(ctx, info.ID); !errors.Is(err, casestore.ErrLocked) {
			t.Errorf("%s: CloseCase = %v, want ErrLocked", name, err)
		}
		if err := st.Rewrap(ctx, info.ID); !errors.Is(err, casestore.ErrLocked) {
			t.Errorf("%s: Rewrap = %v, want ErrLocked", name, err)
		}
		list, err := st.List(ctx)
		if err != nil || len(list) != 1 || list[0].Title != "(locked)" || list[0].Imports != 0 {
			t.Errorf("%s: List = %+v, %v", name, list, err)
		}
		if k, err := st.CaseKind(info.ID); err != nil || k != casestore.KindMasterKey {
			t.Errorf("%s: CaseKind = %q, %v", name, k, err)
		}
	}
	// No lookup at all.
	check("no Protectors", newStoreAt(t, root, storetest.RandomProtector()))
	// A lookup that reports the key as not unlocked.
	check("lookup error", newStoreAt(t, root, storetest.RandomProtector(), func(o *Options) {
		o.Protectors = func(string) (sealedfile.Protector, error) { return nil, errors.New("not unlocked") }
	}))
	// A Keyring that was locked after use.
	locked := rewrapKeyring(t, "correct horse battery")
	locked.Lock()
	check("locked keyring", masterStore(t, root, locked))
}

func TestMasterKeyCaseWrongKeyIsNotLockedAndRevealsNothing(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	other := rewrapKeyring(t, "an entirely different one")
	s, root, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	if err := s.Rewrap(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	wrong := masterStore(t, root, other)
	_, err := wrong.Open(context.Background(), info.ID)
	if err == nil || errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("wrong key Open = %v, want a non-locked refusal", err)
	}
	for _, bad := range []string{"Rewrap me", string(info.ID)} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("error text reveals %q: %v", bad, err)
		}
	}
	list, _ := wrong.List(context.Background())
	if len(list) != 1 || list[0].Title == "Rewrap me" {
		t.Fatalf("List with the wrong key = %+v", list)
	}
}

func TestRewrapWhileTheCaseIsOpen(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	c := mustOpen(t, s, info.ID)
	if err := s.Rewrap(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	// The open handle keeps working and its next manifest write is master-key sealed.
	mustImport(t, c, "late", 2)
	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("CaseKind = %q, %v", k, err)
	}
	if k, err := casestore.FileKind(filepath.Join(root, string(info.ID), manifestName)); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("manifest kind = %q, %v", k, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, masterStore(t, root, kr), info.ID)
	if imp, ev, _ := counts(t, c2); imp != 2 || ev != 7 {
		t.Fatalf("%d imports, %d events", imp, ev)
	}
}

func TestRewrapFailureKeepsTheCaseOpenable(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, p, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	ctx := context.Background()
	dir := filepath.Join(root, string(info.ID))
	keyBefore, manBefore := readFile(t, filepath.Join(dir, keyName)), readFile(t, filepath.Join(dir, manifestName))
	boom := errors.New("simulated crash")

	s.opt.rewrapStep = func(stage string) error {
		if stage == "staged" {
			return boom
		}
		return nil
	}
	if err := s.Rewrap(ctx, info.ID); !errors.Is(err, boom) {
		t.Fatalf("Rewrap = %v", err)
	}
	if !bytes.Equal(keyBefore, readFile(t, filepath.Join(dir, keyName))) || !bytes.Equal(manBefore, readFile(t, filepath.Join(dir, manifestName))) {
		t.Fatal("a failed Rewrap changed the old files")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	c := mustOpen(t, newStoreAt(t, root, p), info.ID) // the plain DPAPI path still works
	if imp, _, _ := counts(t, c); imp != 1 {
		t.Fatalf("imports = %d", imp)
	}
	_ = c.Close()

	// A crash between the two replaces: open refuses as damaged (not locked, no
	// detail), and a second Rewrap finishes it.
	s.opt.rewrapStep = func(stage string) error {
		if stage == "manifest-replaced" {
			return boom
		}
		return nil
	}
	if err := s.Rewrap(ctx, info.ID); !errors.Is(err, boom) {
		t.Fatalf("Rewrap = %v", err)
	}
	if _, err := masterStore(t, root, kr).Open(ctx, info.ID); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("open of a half-moved case = %v, want ErrKeyFile", err)
	}
	s.opt.rewrapStep = nil
	if err := s.Rewrap(ctx, info.ID); err != nil {
		t.Fatalf("finishing: %v", err)
	}
	c = mustOpen(t, masterStore(t, root, kr), info.ID)
	if imp, ev, _ := counts(t, c); imp != 1 || ev != 5 {
		t.Fatalf("%d imports, %d events", imp, ev)
	}
}

func TestRewrapWhileLockedWritesNothing(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	dir := filepath.Join(root, string(info.ID))
	keyBefore, manBefore := readFile(t, filepath.Join(dir, keyName)), readFile(t, filepath.Join(dir, manifestName))
	kr.Lock()
	if err := s.Rewrap(context.Background(), info.ID); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Rewrap with a locked keyring = %v, want ErrLocked", err)
	}
	if !bytes.Equal(keyBefore, readFile(t, filepath.Join(dir, keyName))) || !bytes.Equal(manBefore, readFile(t, filepath.Join(dir, manifestName))) {
		t.Fatal("files changed")
	}
	// A missing case is not found, not locked.
	if err := s.Rewrap(context.Background(), casestore.CaseID("0123456789abcdefghjkmnpqrs")); err == nil || errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Rewrap of a missing case = %v", err)
	}
}

func TestNewCaseKindCreatesMasterKeyCases(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	root := filepath.Join(t.TempDir(), "cases")
	ctx := context.Background()
	useMaster := true
	s := masterStore(t, root, kr, func(o *Options) {
		o.NewCaseKind = func() string {
			if useMaster {
				return casestore.KindMasterKey
			}
			return casestore.KindDPAPI
		}
	})
	info := mustCreate(t, s, "Born sealed")
	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("CaseKind = %q, %v", k, err)
	}
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "x", 4)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(readFile(t, filepath.Join(root, string(info.ID), manifestName)), []byte("Born sealed")) {
		t.Fatal("title in the manifest in plain")
	}
	// Rewrap of a case that is already master-key is a no-op.
	if err := s.Rewrap(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	// Creating while the keyring is locked refuses and leaves no folder behind.
	kr.Lock()
	if _, err := s.Create(ctx, "Nope"); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Create while locked = %v, want ErrLocked", err)
	}
	entries := caseFolders(root)
	if len(entries) != 1 {
		t.Fatalf("%d entries in the root after a refused Create", len(entries))
	}
	// A DPAPI default is unchanged.
	useMaster = false
	s2 := newStoreAt(t, root, storetest.RandomProtector())
	d := mustCreate(t, s2, "Plain")
	if k, _ := s2.CaseKind(d.ID); k != casestore.KindDPAPI {
		t.Fatalf("default kind = %q", k)
	}
}
