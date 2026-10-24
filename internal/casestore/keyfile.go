package casestore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"api-audit-explorer/internal/sealedfile"
)

// keyFileVersion is the only case.key layout this build reads or writes.
const keyFileVersion = 1

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
	DB     string `json:"dbKey"`
	Pseudo string `json:"pseudoKey"`
	CaseID string `json:"caseId,omitempty"`
}

// WriteKeyFile seals the keys with p and writes them atomically to path.
func WriteKeyFile(path string, p sealedfile.Protector, k Keys) error {
	if len(k.DB) != 32 || len(k.Pseudo) != 32 {
		return ErrKeyFile
	}
	doc := keyFileDoc{V: keyFileVersion, DB: hex.EncodeToString(k.DB), Pseudo: hex.EncodeToString(k.Pseudo), CaseID: k.CaseID}
	plain, err := json.Marshal(doc)
	if err != nil {
		return ErrKeyFile
	}
	defer clear(plain)
	sealed, err := p.Protect(plain)
	if err != nil {
		return fmt.Errorf("sealing case key: %w", sealedfile.ErrUnavailable)
	}
	return sealedfile.WriteAtomic(path, "case.key.*.tmp", sealed)
}

// ReadKeyFile opens a blob written by WriteKeyFile. A missing file is
// os.ErrNotExist; every other failure is ErrKeyFile.
func ReadKeyFile(path string, p sealedfile.Protector) (Keys, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Keys{}, err
	}
	if info.Size() == 0 || info.Size() > keyFileMax {
		return Keys{}, ErrKeyFile
	}
	sealed, err := os.ReadFile(path)
	if err != nil {
		return Keys{}, err
	}
	plain, err := p.Unprotect(sealed)
	if err != nil {
		return Keys{}, ErrKeyFile
	}
	defer clear(plain)
	var doc keyFileDoc
	if err := json.Unmarshal(plain, &doc); err != nil || doc.V != keyFileVersion {
		return Keys{}, ErrKeyFile
	}
	db, err1 := hex.DecodeString(doc.DB)
	pseudo, err2 := hex.DecodeString(doc.Pseudo)
	if err1 != nil || err2 != nil || len(db) != 32 || len(pseudo) != 32 {
		return Keys{}, ErrKeyFile
	}
	return Keys{DB: db, Pseudo: pseudo, CaseID: doc.CaseID}, nil
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
