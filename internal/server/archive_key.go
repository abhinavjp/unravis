package server

import (
	"context"
	"crypto/rand"
	"errors"

	"api-audit-explorer/internal/casekey"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
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

// ownKeyer is the store's case keys, when it has them.
func (h *handler) ownKeyer() (casestore.CaseKeyer, bool) {
	ck, ok := h.cases.(casestore.CaseKeyer)
	return ck, ok
}

// hasOwnKey reports whether the case has a key of its own. A store without case
// keys, or a case it cannot tell, has none.
func (h *handler) hasOwnKey(ctx context.Context, id casestore.CaseID) bool {
	ck, ok := h.ownKeyer()
	if !ok {
		return false
	}
	info, err := ck.CaseKey(id)
	return err == nil && info.Has
}

// caseFileLookupFor is caseFileLookup for one case: its own key resolves too, so the
// key file of a case with its own key can be read while that key is unlocked.
func (h *handler) caseFileLookupFor(id casestore.CaseID) casestore.ProtectorLookup {
	base := h.caseFileLookup()
	ck, ok := h.ownKeyer()
	if !ok {
		return base
	}
	own := ck.CaseProtectors(id)
	return func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindCaseKey {
			return own(kind)
		}
		return base(kind)
	}
}

// newArchiveKey builds the key of a new case file. A case with a key of its own
// seals it with that key (KeyKindCase; the case passphrase opens the file on any PC);
// else with a master key it is that key (KeyKindMaster); without one it derives a
// fresh key from the typed passphrase (KeyKindTyped). check is the key check value
// kept in the record. A case whose own key is locked is casestore.ErrCaseLocked.
func (h *handler) newArchiveKey(ctx context.Context, id casestore.CaseID, pass []byte) (fk casestore.FileKey, kind, check string, err error) {
	if h.hasOwnKey(ctx, id) {
		ck, _ := h.ownKeyer()
		fk, check, err = ck.CaseArchiveKey(id)
		if err != nil {
			return casestore.FileKey{}, "", "", err
		}
		return fk, casestore.KeyKindCase, check, nil
	}
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
// file was made with. A file sealed under the case's own key opens with that key
// while it is unlocked and still the one the file was made with; else the person
// types the case passphrase, and a passphrase whose key check differs from the
// file's is refused before any of the file is read. oldKey is true when only a
// typed passphrase can work and none was given (the answer is M13).
func (h *handler) openDeriver(id casestore.CaseID, f *casestore.ArchiveFile, pass []byte) (derive casestore.KeyDeriver, clean func(), oldKey bool) {
	switch f.KeyKind {
	case casestore.KeyKindMaster:
		if h.mk.State() == MasterKeyUnlocked {
			key, _, _, err := h.mk.archiveKey()
			if err == nil {
				if f.KeyCheck == "" || masterkey.KeyCheckValue(key) == f.KeyCheck {
					return casestore.FixedKey(key), func() { clear(key) }, false
				}
				clear(key)
			}
		}
	case casestore.KeyKindCase:
		if ck, ok := h.ownKeyer(); ok {
			if fk, check, err := ck.CaseArchiveKey(id); err == nil {
				if f.KeyCheck == "" || casekey.Equal(check, f.KeyCheck) {
					return casestore.FixedKey(fk.Key), func() { clear(fk.Key) }, false
				}
				clear(fk.Key)
			}
		}
		if len(pass) > 0 {
			return checkedKey(casestore.PassphraseKey(pass), f.KeyCheck), func() {}, false
		}
		return nil, func() {}, true
	}
	if len(pass) == 0 {
		return nil, func() {}, true
	}
	return casestore.PassphraseKey(pass), func() {}, false
}

// checkedKey is derive with the key check value of what it derives compared with
// want first: a key that is not the file's is refused (the file reads as one that
// does not open) without trying it on the file. An empty want checks nothing.
func checkedKey(derive casestore.KeyDeriver, want string) casestore.KeyDeriver {
	return func(salt []byte, p masterkey.Params) ([]byte, error) {
		key, err := derive(salt, p)
		if err != nil {
			return nil, err
		}
		if want != "" && !casekey.Equal(casekey.Check(key), want) {
			clear(key)
			return nil, errWrongCaseKey
		}
		return key, nil
	}
}

var errWrongCaseKey = errors.New("server: the key is not the one the file was made with")

// archiveKeyStale tells whether the key that sealed the kept file is no longer the
// one in force, so the file could only be opened with the passphrase it was made
// with (M13). A master-sealed file is stale after a reset; a case-sealed file after
// the case key was changed or removed. A locked own key cannot be compared: that is
// casestore.ErrCaseLocked.
func (h *handler) archiveKeyStale(id casestore.CaseID, f *casestore.ArchiveFile) (bool, error) {
	if f.KeyCheck == "" {
		return false, nil
	}
	switch f.KeyKind {
	case casestore.KeyKindMaster:
		return h.mk.masterCheck() != f.KeyCheck, nil
	case casestore.KeyKindCase:
		ck, ok := h.ownKeyer()
		if !ok {
			return true, nil
		}
		check, err := ck.CaseKeyCheck(id)
		switch {
		case errors.Is(err, casestore.ErrNoCaseKey):
			return true, nil
		case err != nil:
			return false, err
		}
		return !casekey.Equal(check, f.KeyCheck), nil
	}
	return false, nil
}
