package casestore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// Protector kinds a sealed case file can record. DPAPI is what every build
// before the master key wrote; MasterKey means the file is sealed under the
// unlocked master key (internal/masterkey).
const (
	KindDPAPI     = "dpapi"
	KindMasterKey = "masterkey"
)

// Labels bind a master-key blob to its purpose (AAD), so a sealed case.key
// never opens as a manifest.dat and the reverse.
const (
	LabelKey      = "case.key"
	LabelManifest = "manifest.dat"
)

// ValidKind reports whether k is a protector kind this build knows.
func ValidKind(k string) bool { return k == KindDPAPI || k == KindMasterKey }

// ErrLocked means a case is sealed under the master key and the master key is
// locked or not set up. It is the same value as masterkey.ErrLocked, so either
// name matches with errors.Is. It reveals nothing about the case.
var ErrLocked = masterkey.ErrLocked

// ProtectorLookup resolves the base protector for a kind. A kind that is
// unavailable (the master key is locked) is reported as ErrLocked, or any error
// the caller wants mapped to it.
type ProtectorLookup func(kind string) (sealedfile.Protector, error)

// Labeller is implemented by protectors that bind a label into what they seal
// (masterkey.Keyring). Bind uses it for the master-key kind.
type Labeller interface {
	WithLabel(label string) sealedfile.Protector
}

// Bind returns the protector to use for one file: a master-key protector that can
// label is bound to label; everything else (DPAPI, test fakes) is used as is.
func Bind(p sealedfile.Protector, kind, label string) sealedfile.Protector {
	if kind == KindMasterKey {
		if l, ok := p.(Labeller); ok {
			return l.WithLabel(label)
		}
	}
	return p
}

// Resolve returns the protector for kind bound to label, or ErrLocked.
func Resolve(lookup ProtectorLookup, kind, label string) (sealedfile.Protector, error) {
	if lookup == nil {
		return nil, ErrLocked
	}
	p, err := lookup(kind)
	if err != nil {
		if kind == KindMasterKey || errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, err
	}
	if p == nil {
		return nil, ErrLocked
	}
	return Bind(p, kind, label), nil
}

// DPAPIOnly is a lookup that serves only the DPAPI kind from p. A master-key
// file then reads as locked.
func DPAPIOnly(p sealedfile.Protector) ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		if kind == KindDPAPI && p != nil {
			return p, nil
		}
		return nil, ErrLocked
	}
}

// Key file and manifest layouts.
//
//	v1: the bare sealed blob (DPAPI). No header. What a build without a master key
//	    still writes, so rolling the binary back keeps working.
//	v2: one header line {"v":2,"p":"<kind>"}, a newline, then the sealed blob. A
//	    v1-only reader hands the whole file to its protector, which refuses it.
const (
	keyFileVersion  = 1 // inner document version of a v1 file
	keyFileVersion2 = 2 // header and inner document version of a v2 file
	headerMax       = 64
)

// envelope builds the on-disk bytes for a sealed blob of the given kind. DPAPI
// stays the bare v1 blob.
func envelope(kind string, blob []byte) []byte {
	if kind == KindDPAPI {
		return blob
	}
	h := `{"v":2,"p":"` + kind + `"}` + "\n"
	out := make([]byte, 0, len(h)+len(blob))
	return append(append(out, h...), blob...)
}

type envHeader struct {
	V int    `json:"v"`
	P string `json:"p"`
}

// parseEnvelope returns the kind recorded in raw and the sealed blob. Bytes with
// no header are a v1 DPAPI blob. A header with an unknown version or kind is
// ErrKeyFile.
func parseEnvelope(raw []byte) (kind string, blob []byte, v2 bool, err error) {
	if len(raw) == 0 || raw[0] != '{' {
		return KindDPAPI, raw, false, nil
	}
	head := raw
	if len(head) > headerMax {
		head = head[:headerMax]
	}
	nl := bytes.IndexByte(head, '\n')
	if nl < 0 {
		return KindDPAPI, raw, false, nil
	}
	var h envHeader
	if json.Unmarshal(raw[:nl], &h) != nil {
		return KindDPAPI, raw, false, nil
	}
	if h.V != keyFileVersion2 || !ValidKind(h.P) {
		return "", nil, false, ErrKeyFile
	}
	return h.P, raw[nl+1:], true, nil
}

// WrapSealed returns the on-disk bytes for a blob sealed by the protector of kind:
// the bare blob for DPAPI (v1), a header line then the blob for the master key
// (v2). Case files other than case.key (manifest.dat) use the same layout.
func WrapSealed(kind string, blob []byte) []byte { return envelope(kind, blob) }

// UnwrapSealed is the reverse: the kind a file records and its sealed blob. Bytes
// with no header are a v1 DPAPI blob; an unknown version or kind is ErrKeyFile.
func UnwrapSealed(raw []byte) (kind string, blob []byte, err error) {
	kind, blob, _, err = parseEnvelope(raw)
	return kind, blob, err
}

// FileKind reports which protector sealed the case file at path (case.key or
// manifest.dat) from its first bytes, without opening it. A missing file is
// os.ErrNotExist; an unknown version or kind is ErrKeyFile.
func FileKind(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, headerMax+1)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return "", ErrKeyFile
	}
	kind, _, _, perr := parseEnvelope(buf[:n])
	return kind, perr
}

// keyFileMax bounds what ReadKeyFile will load. A real blob is a few hundred
// bytes; anything near this size is not ours.
const keyFileMax = 4 << 10

// ErrKeyFile means a case.key could not be read, opened or understood. It never
// carries key bytes.
var ErrKeyFile = errors.New("case key file is unreadable")

// Keys are the two per-case secrets, kept in one blob so a single deletion
// destroys both (storage decision, "Pseudonym key").
type Keys struct {
	DB     []byte // 32 bytes, the adiantum key for case.db
	Pseudo []byte // 32 bytes, the HMAC key behind Case.Pseudonymize
	// CaseID binds the blob to its case. A key and database copied from another
	// case into this folder then fail the ID check on open.
	CaseID string
}

// NewKeys draws both keys from crypto/rand.
func NewKeys() (Keys, error) {
	k := Keys{DB: make([]byte, 32), Pseudo: make([]byte, 32)}
	if _, err := rand.Read(k.DB); err != nil {
		return Keys{}, err
	}
	if _, err := rand.Read(k.Pseudo); err != nil {
		return Keys{}, err
	}
	return k, nil
}

// Zero overwrites both keys in place. Go may keep other copies; this narrows
// the window and is not a guarantee.
func (k *Keys) Zero() {
	clear(k.DB)
	clear(k.Pseudo)
}

type keyFileDoc struct {
	V      int    `json:"v"`
	P      string `json:"p,omitempty"` // protector kind; v2 only
	DB     string `json:"dbKey"`
	Pseudo string `json:"pseudoKey"`
	CaseID string `json:"caseId,omitempty"`
}

// sealKeys returns the on-disk bytes of a key file: keys sealed with the protector
// for kind (already bound to its label), wrapped for that kind.
func sealKeys(kind string, p sealedfile.Protector, k Keys) ([]byte, error) {
	if len(k.DB) != 32 || len(k.Pseudo) != 32 || !ValidKind(kind) {
		return nil, ErrKeyFile
	}
	doc := keyFileDoc{V: keyFileVersion, DB: hex.EncodeToString(k.DB), Pseudo: hex.EncodeToString(k.Pseudo), CaseID: k.CaseID}
	if kind != KindDPAPI {
		doc.V, doc.P = keyFileVersion2, kind
	}
	plain, err := json.Marshal(doc)
	if err != nil {
		return nil, ErrKeyFile
	}
	defer clear(plain)
	sealed, err := p.Protect(plain)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("sealing case key: %w", sealedfile.ErrUnavailable)
	}
	return envelope(kind, sealed), nil
}

// WriteKeyFile seals the keys with p, a DPAPI protector, and writes the v1 file
// atomically to path. It is what a build without a master key writes.
func WriteKeyFile(path string, p sealedfile.Protector, k Keys) error {
	return WriteKeyFileKind(path, KindDPAPI, p, k)
}

// WriteKeyFileKind writes the key file for kind. p is the kind's base protector
// (Bind applies the case.key label for the master key). DPAPI writes v1, the
// master key writes v2.
func WriteKeyFileKind(path, kind string, p sealedfile.Protector, k Keys) error {
	data, err := sealKeys(kind, Bind(p, kind, LabelKey), k)
	if err != nil {
		return err
	}
	return sealedfile.WriteAtomic(path, "case.key.*.tmp", data)
}

// ReadKeyFile opens a key file with p as the DPAPI protector. A v2 file that
// names the master key is ErrLocked here; use ReadKeyFileAny to open it. A
// missing file is os.ErrNotExist; every other failure is ErrKeyFile.
func ReadKeyFile(path string, p sealedfile.Protector) (Keys, error) {
	k, _, err := ReadKeyFileAny(path, DPAPIOnly(p))
	return k, err
}

// ReadKeyFileAny opens a key file of either version, choosing the protector from
// the kind the file records. It returns that kind. A file sealed under the master
// key while the master key is unavailable is ErrLocked and nothing else about it
// is revealed. A missing file is os.ErrNotExist; every other failure is
// ErrKeyFile.
func ReadKeyFileAny(path string, lookup ProtectorLookup) (Keys, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Keys{}, "", err
	}
	if info.Size() == 0 || info.Size() > keyFileMax {
		return Keys{}, "", ErrKeyFile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Keys{}, "", err
	}
	return openKeys(raw, lookup)
}

func openKeys(raw []byte, lookup ProtectorLookup) (Keys, string, error) {
	kind, blob, v2, err := parseEnvelope(raw)
	if err != nil {
		return Keys{}, "", err
	}
	p, err := Resolve(lookup, kind, LabelKey)
	if err != nil {
		return Keys{}, "", err
	}
	plain, err := p.Unprotect(blob)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return Keys{}, "", ErrLocked
		}
		return Keys{}, "", ErrKeyFile
	}
	defer clear(plain)
	var doc keyFileDoc
	if err := json.Unmarshal(plain, &doc); err != nil {
		return Keys{}, "", ErrKeyFile
	}
	if v2 && (doc.V != keyFileVersion2 || doc.P != kind) || !v2 && (doc.V != keyFileVersion || doc.P != "") {
		return Keys{}, "", ErrKeyFile
	}
	db, err1 := hex.DecodeString(doc.DB)
	pseudo, err2 := hex.DecodeString(doc.Pseudo)
	if err1 != nil || err2 != nil || len(db) != 32 || len(pseudo) != 32 {
		clear(db)
		clear(pseudo)
		return Keys{}, "", ErrKeyFile
	}
	return Keys{DB: db, Pseudo: pseudo, CaseID: doc.CaseID}, kind, nil
}

// OverwriteKeyFile replaces the content of path with random bytes of the same
// length and flushes them, without deleting it. A missing file is not an error.
func OverwriteKeyFile(path string) error {
	// Never write through a link: only a plain file is overwritten.
	li, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !li.Mode().IsRegular() {
		return ErrKeyFile
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	size := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	if size == 0 || size > keyFileMax {
		size = 512
	}
	junk := make([]byte, size)
	if _, err := rand.Read(junk); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteAt(junk, 0); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// EraseKeyFile is the crypto-erase step: overwrite with random bytes, flush,
// delete. It is idempotent. On an SSD the overwrite may not reach the flash
// cells; the delete of a key that no longer exists anywhere is what matters.
func EraseKeyFile(path string) error {
	overwriteErr := OverwriteKeyFile(path)
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(overwriteErr, removeErr)
}
