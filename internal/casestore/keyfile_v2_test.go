package casestore_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
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

// testKeyring returns an unlocked master-key Keyring for a throwaway folder. The
// passphrase is a test value.
func testKeyring(t *testing.T, pass string) *masterkey.Keyring {
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

func masterOnly(kr *masterkey.Keyring) casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindMasterKey {
			return kr, nil
		}
		return nil, casestore.ErrLocked
	}
}

func sameKeys(a, b casestore.Keys) bool {
	return bytes.Equal(a.DB, b.DB) && bytes.Equal(a.Pseudo, b.Pseudo) && a.CaseID == b.CaseID
}

// oldReadKeyFile is the v1-only reader as it was before key file v2. It proves a
// v2 file is refused by it rather than misread.
func oldReadKeyFile(path string, p sealedfile.Protector) (casestore.Keys, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return casestore.Keys{}, err
	}
	plain, err := p.Unprotect(raw)
	if err != nil {
		return casestore.Keys{}, casestore.ErrKeyFile
	}
	var doc struct {
		V      int    `json:"v"`
		DB     string `json:"dbKey"`
		Pseudo string `json:"pseudoKey"`
		CaseID string `json:"caseId"`
	}
	if err := json.Unmarshal(plain, &doc); err != nil || doc.V != 1 {
		return casestore.Keys{}, casestore.ErrKeyFile
	}
	db, _ := hex.DecodeString(doc.DB)
	ps, _ := hex.DecodeString(doc.Pseudo)
	return casestore.Keys{DB: db, Pseudo: ps, CaseID: doc.CaseID}, nil
}

// TestKeyFileV1StillWrittenAndRead pins the v1 layout: a file made by the old
// writer opens, and what WriteKeyFile writes now is still read by the old reader.
func TestKeyFileV1StillWrittenAndRead(t *testing.T) {
	p := storetest.RandomProtector()
	dir := t.TempDir()
	keys, _ := casestore.NewKeys()
	keys.CaseID = "bbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Written exactly as the v1 writer did: sealed JSON, no header.
	plain, _ := json.Marshal(map[string]any{"v": 1, "dbKey": hex.EncodeToString(keys.DB), "pseudoKey": hex.EncodeToString(keys.Pseudo), "caseId": keys.CaseID})
	sealed, _ := p.Protect(plain)
	legacy := filepath.Join(dir, "legacy.key")
	if err := os.WriteFile(legacy, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	got, kind, err := casestore.ReadKeyFileAny(legacy, casestore.DPAPIOnly(p))
	if err != nil || kind != casestore.KindDPAPI || !sameKeys(got, keys) {
		t.Fatalf("legacy v1 file: kind=%q err=%v", kind, err)
	}
	if k, err := casestore.FileKind(legacy); err != nil || k != casestore.KindDPAPI {
		t.Fatalf("FileKind of a v1 file = %q, %v", k, err)
	}

	// What this build writes without a master key is the same bare v1 blob.
	now := filepath.Join(dir, "now.key")
	if err := casestore.WriteKeyFile(now, p, keys); err != nil {
		t.Fatal(err)
	}
	if old, err := oldReadKeyFile(now, p); err != nil || !sameKeys(old, keys) {
		t.Fatalf("old reader on a file written now: %v", err)
	}
	if err := casestore.WriteKeyFileKind(now, casestore.KindDPAPI, p, keys); err != nil {
		t.Fatal(err)
	}
	if old, err := oldReadKeyFile(now, p); err != nil || !sameKeys(old, keys) {
		t.Fatalf("WriteKeyFileKind(dpapi) must write v1: %v", err)
	}
	if k, err := casestore.FileKind(now); err != nil || k != casestore.KindDPAPI {
		t.Fatalf("FileKind = %q, %v", k, err)
	}
}

func TestKeyFileV2RoundTrip(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	path := filepath.Join(t.TempDir(), "case.key")
	keys, _ := casestore.NewKeys()
	keys.CaseID = "cccccccccccccccccccccccccc"
	if err := casestore.WriteKeyFileKind(path, casestore.KindMasterKey, kr, keys); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.HasPrefix(raw, []byte("{\"v\":2,\"p\":\"masterkey\"}\n")) {
		t.Fatalf("v2 header missing: %q", raw[:20])
	}
	if bytes.Contains(raw, keys.DB) || bytes.Contains(raw, []byte(hex.EncodeToString(keys.DB))) || bytes.Contains(raw, keys.Pseudo) {
		t.Fatal("key bytes appear in the file in the clear")
	}
	got, kind, err := casestore.ReadKeyFileAny(path, masterOnly(kr))
	if err != nil || kind != casestore.KindMasterKey || !sameKeys(got, keys) {
		t.Fatalf("v2 round trip: kind=%q err=%v", kind, err)
	}
	if k, err := casestore.FileKind(path); err != nil || k != casestore.KindMasterKey {
		t.Fatalf("FileKind = %q, %v", k, err)
	}
	// The DPAPI-only entry point cannot open it and says so without detail.
	if _, err := casestore.ReadKeyFile(path, storetest.RandomProtector()); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("ReadKeyFile of a master-key file = %v, want ErrLocked", err)
	}
	// The old v1-only reader refuses it instead of misreading it.
	if _, err := oldReadKeyFile(path, kr); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("old reader on a v2 file = %v, want ErrKeyFile", err)
	}
	if _, err := oldReadKeyFile(path, storetest.RandomProtector()); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("old reader (other protector) on a v2 file = %v, want ErrKeyFile", err)
	}
	// A case.key blob does not open as a manifest.
	_, blob, _ := casestore.UnwrapSealed(raw)
	if _, err := kr.Open(casestore.LabelManifest, blob); err == nil {
		t.Fatal("a case.key blob opened under the manifest.dat label")
	}
	if _, err := kr.Open(casestore.LabelKey, blob); err != nil {
		t.Fatalf("a case.key blob did not open under its own label: %v", err)
	}
}

func TestKeyFileV2LockedWrongAndDamaged(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	other := testKeyring(t, "a completely different one")
	dir := t.TempDir()
	path := filepath.Join(dir, "case.key")
	keys, _ := casestore.NewKeys()
	if err := casestore.WriteKeyFileKind(path, casestore.KindMasterKey, kr, keys); err != nil {
		t.Fatal(err)
	}
	secrets := []string{hex.EncodeToString(keys.DB), hex.EncodeToString(keys.Pseudo)}
	noLeak := func(what string, err error) {
		t.Helper()
		if err == nil {
			return
		}
		for _, s := range secrets {
			if strings.Contains(err.Error(), s) {
				t.Errorf("%s: error text contains key bytes", what)
			}
		}
	}

	// Wrong master key: refused as unreadable, not as locked, nothing revealed.
	_, _, err := casestore.ReadKeyFileAny(path, masterOnly(other))
	if !errors.Is(err, casestore.ErrKeyFile) || errors.Is(err, casestore.ErrLocked) {
		t.Errorf("wrong key = %v, want ErrKeyFile", err)
	}
	noLeak("wrong key", err)

	// No master key configured.
	for name, lookup := range map[string]casestore.ProtectorLookup{
		"nil lookup":   nil,
		"dpapi only":   casestore.DPAPIOnly(storetest.RandomProtector()),
		"lookup error": func(string) (sealedfile.Protector, error) { return nil, errors.New("not unlocked") },
		"lookup nil":   func(string) (sealedfile.Protector, error) { return nil, nil },
	} {
		_, _, err := casestore.ReadKeyFileAny(path, lookup)
		if !errors.Is(err, casestore.ErrLocked) {
			t.Errorf("%s = %v, want ErrLocked", name, err)
		}
		noLeak(name, err)
	}

	// Locked after it was unlocked.
	kr.Lock()
	_, _, err = casestore.ReadKeyFileAny(path, masterOnly(kr))
	if !errors.Is(err, casestore.ErrLocked) || !errors.Is(err, masterkey.ErrLocked) {
		t.Errorf("locked = %v, want ErrLocked", err)
	}
	noLeak("locked", err)
	// Writing with a locked key refuses too, and writes nothing.
	x := filepath.Join(dir, "x.key")
	if err := casestore.WriteKeyFileKind(x, casestore.KindMasterKey, kr, keys); !errors.Is(err, casestore.ErrLocked) {
		t.Errorf("write while locked = %v, want ErrLocked", err)
	}
	if _, err := os.Stat(x); err == nil {
		t.Error("a key file was written while locked")
	}
}

func TestKeyFileV2Headers(t *testing.T) {
	kr := testKeyring(t, "correct horse battery")
	dir := t.TempDir()
	keys, _ := casestore.NewKeys()
	good := filepath.Join(dir, "good.key")
	if err := casestore.WriteKeyFileKind(good, casestore.KindMasterKey, kr, keys); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(good)
	_, blob, _ := casestore.UnwrapSealed(raw)

	write := func(name string, data []byte) string {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	for name, header := range map[string]string{
		"unknown protector": `{"v":2,"p":"tpm"}`,
		"empty protector":   `{"v":2,"p":""}`,
		"unknown version":   `{"v":3,"p":"masterkey"}`,
		"v1 header":         `{"v":1,"p":"masterkey"}`,
		"no version":        `{"p":"masterkey"}`,
	} {
		f := write(strings.ReplaceAll(name, " ", "_")+".key", append([]byte(header+"\n"), blob...))
		if _, _, err := casestore.ReadKeyFileAny(f, masterOnly(kr)); !errors.Is(err, casestore.ErrKeyFile) {
			t.Errorf("%s = %v, want ErrKeyFile", name, err)
		}
		if _, err := casestore.FileKind(f); !errors.Is(err, casestore.ErrKeyFile) {
			t.Errorf("FileKind %s = %v, want ErrKeyFile", name, err)
		}
	}

	dp := storetest.RandomProtector()
	lookup := func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindDPAPI {
			return dp, nil
		}
		return kr, nil
	}
	// A header that names a different kind than the one that sealed the blob.
	swapped := write("swapped.key", append([]byte("{\"v\":2,\"p\":\"dpapi\"}\n"), blob...))
	if _, _, err := casestore.ReadKeyFileAny(swapped, lookup); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("header says dpapi over a master-key blob = %v, want ErrKeyFile", err)
	}
	// Inner document that disagrees with its header.
	plain, _ := json.Marshal(map[string]any{"v": 2, "p": "dpapi", "dbKey": hex.EncodeToString(keys.DB), "pseudoKey": hex.EncodeToString(keys.Pseudo)})
	sealed, _ := kr.WithLabel(casestore.LabelKey).Protect(plain)
	liar := write("liar.key", append([]byte("{\"v\":2,\"p\":\"masterkey\"}\n"), sealed...))
	if _, _, err := casestore.ReadKeyFileAny(liar, masterOnly(kr)); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("inner p disagrees with header = %v, want ErrKeyFile", err)
	}
	// A v2 inner document in a headerless file is not a v1 file.
	plain2, _ := json.Marshal(map[string]any{"v": 2, "p": "masterkey", "dbKey": hex.EncodeToString(keys.DB), "pseudoKey": hex.EncodeToString(keys.Pseudo)})
	sealed2, _ := dp.Protect(plain2)
	bare := write("bare.key", sealed2)
	if _, _, err := casestore.ReadKeyFileAny(bare, lookup); !errors.Is(err, casestore.ErrKeyFile) {
		t.Errorf("v2 document in a headerless file = %v, want ErrKeyFile", err)
	}
	// Truncated, header only and flipped bytes.
	flipped := bytes.Clone(raw)
	flipped[len(flipped)-3] ^= 1
	for name, data := range map[string][]byte{
		"truncated":   raw[:len(raw)-5],
		"header_only": []byte("{\"v\":2,\"p\":\"masterkey\"}\n"),
		"flipped":     flipped,
	} {
		f := write(name+".key", data)
		if _, _, err := casestore.ReadKeyFileAny(f, masterOnly(kr)); !errors.Is(err, casestore.ErrKeyFile) {
			t.Errorf("%s = %v, want ErrKeyFile", name, err)
		}
	}
}
