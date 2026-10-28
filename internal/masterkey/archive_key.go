package masterkey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Helpers for case files sealed under the master key itself (P3, spec 7.6). A case
// file records the salt and cost parameters its key was derived with, so a file
// sealed with this PC's master key records the master salt and opens with the
// typed master passphrase on any PC, exactly like a file made from a typed
// passphrase. Nothing here stores or logs a passphrase or a key.

// archiveCheckLabel is the fixed label of the key check value.
const archiveCheckLabel = "unravis/archive/keycheck/v1"

// KeyCheckValue is the key check value of key: HMAC-SHA256 under key with a fixed
// label, as lower-case hex. It is never a hash of the passphrase; guessing a
// passphrase from it still costs a full Argon2id run per guess. The screen shows
// only its last four characters.
func KeyCheckValue(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(archiveCheckLabel))
	return hex.EncodeToString(m.Sum(nil))
}

// ArchiveKey returns a copy of the master key together with the salt and the
// Argon2id parameters it was derived with, read from masterkey.dat in dir. It is
// ErrLocked when the key is locked and ErrWrongKey when masterkey.dat no longer
// belongs to the key held (a reset happened since it was unlocked). The caller
// clears key.
func (k *Keyring) ArchiveKey(dir string) (key, salt []byte, p Params, err error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if len(k.key) != KeyLen {
		return nil, nil, Params{}, ErrLocked
	}
	doc, salt, ver, st, err := readDoc(dir)
	if err != nil {
		return nil, nil, Params{}, err
	}
	switch st {
	case FileAbsent:
		return nil, nil, Params{}, ErrNotSet
	case FileDamaged:
		return nil, nil, Params{}, ErrDamaged
	}
	plain, err := OpenWith(k.key, verifierLabel, ver)
	if err != nil || string(plain) != verifierConst {
		return nil, nil, Params{}, ErrWrongKey
	}
	return append([]byte(nil), k.key...), salt, doc.Params, nil
}
