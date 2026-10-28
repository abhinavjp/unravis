package server

import (
	"crypto/rand"
	"errors"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

// Keys of a case file (spec 7.6). A file is sealed with the master key of this PC
// (its salt and cost go into the file, so the typed master passphrase opens it on
// any PC), or, when no master key exists, with a passphrase typed for that file
// and never stored. Nothing here logs or returns a key except to its caller.

// archiveKey is the master key with the salt and cost it was derived with. The
// caller clears key. It is casestore.ErrLocked while the key is locked.
func (s *MasterKeySession) archiveKey() (key, salt []byte, p masterkey.Params, err error) {
	ring, err := s.Keyring()
	if err != nil {
		return nil, nil, masterkey.Params{}, err
	}
	key, salt, p, err = ring.ArchiveKey(s.dir)
	if err != nil {
		if errors.Is(err, masterkey.ErrLocked) {
			return nil, nil, masterkey.Params{}, casestore.ErrLocked
		}
		return nil, nil, masterkey.Params{}, err
	}
	return key, salt, p, nil
}

// masterCheck is the key check value of the master key in force now, or "" when it
// cannot be read (locked, none, damaged).
func (s *MasterKeySession) masterCheck() string {
	key, _, _, err := s.archiveKey()
	if err != nil {
		return ""
	}
	defer clear(key)
	return masterkey.KeyCheckValue(key)
}

// newArchiveKey builds the key of a new case file. With a master key it is that
// key (KeyKindMaster); without one it derives a fresh key from the typed
// passphrase (KeyKindTyped). check is the key check value kept in the record.
func (h *handler) newArchiveKey(pass []byte) (fk casestore.FileKey, kind, check string, err error) {
	switch h.mk.State() {
	case MasterKeyUnlocked:
		key, salt, p, kerr := h.mk.archiveKey()
		if kerr != nil {
			return casestore.FileKey{}, "", "", kerr
		}
		return casestore.FileKey{Key: key, Salt: salt, Params: p}, casestore.KeyKindMaster, masterkey.KeyCheckValue(key), nil
	case MasterKeyNone:
		if len(pass) < masterkey.MinPassphraseLen || len(pass) > maxPassphraseBytes {
			return casestore.FileKey{}, "", "", errArchivePassphrase
		}
		salt := make([]byte, 16)
		if _, rerr := rand.Read(salt); rerr != nil {
			return casestore.FileKey{}, "", "", rerr
		}
		p := caseFileParams
		if p == (masterkey.Params{}) {
			p = masterkey.DefaultParams()
		}
		key, derr := masterkey.DeriveKey(pass, salt, p)
		if derr != nil {
			return casestore.FileKey{}, "", "", derr
		}
		return casestore.FileKey{Key: key, Salt: salt, Params: p}, casestore.KeyKindTyped, masterkey.KeyCheckValue(key), nil
	}
	return casestore.FileKey{}, "", "", casestore.ErrLocked
}

// errArchivePassphrase is a typed passphrase that is missing or too short.
var errArchivePassphrase = errors.New("server: the archive passphrase is missing or too short")

// openDeriver chooses how an existing case file is opened. A file sealed under the
// master key opens with it when the key check still matches (the key in force is
// the one the file was made with), else the person must type the passphrase the
// file was made with. oldKey is true when only a typed passphrase can work and none
// was given (the answer is M13).
func (h *handler) openDeriver(f *casestore.ArchiveFile, pass []byte) (derive casestore.KeyDeriver, clean func(), oldKey bool) {
	if f.KeyKind == casestore.KeyKindMaster && h.mk.State() == MasterKeyUnlocked {
		key, _, _, err := h.mk.archiveKey()
		if err == nil {
			if f.KeyCheck == "" || masterkey.KeyCheckValue(key) == f.KeyCheck {
				return casestore.FixedKey(key), func() { clear(key) }, false
			}
			clear(key)
		}
	}
	if len(pass) == 0 {
		return nil, func() {}, true
	}
	return casestore.PassphraseKey(pass), func() {}, false
}
