// Package casekey is the optional per-case key (decision A12, A13). A case that
// has its own key is opened with it instead of the PC master key. The passphrase
// is stretched with the same Argon2id as the master key into a wrapping key (KEK);
// the KEK wraps a random 32-byte case master that seals the case's files, so a key
// change rewrites one small file and nothing else. The check value is
// HMAC-SHA256 under the KEK with a fixed label: never a hash of the passphrase.
// The full value lives only inside the sealed wrap; files show its last 4
// characters ("Key ending xxxx"). Invariant: the passphrase, the KEK and the case
// master are never written, logged, returned or put in an error.
package casekey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"api-audit-explorer/internal/masterkey"
)

// FileName is the case's key-wrap file, next to case.key and manifest.dat.
const FileName = "case.wrap"

// MinLen is the shortest accepted case key, in characters.
const MinLen = masterkey.MinPassphraseLen

// States of a wrap. "setting" and "removing" are the two directions of a change
// of sealing that touches more than one file; the store finishes them at unlock.
const (
	StateActive   = "active"
	StateSetting  = "setting"
	StateRemoving = "removing"
)

const (
	checkLabel  = "unravis/casekey/check/v1"
	wrapLabel   = "unravis/casekey/wrap/v1"
	wrapVersion = 1
	wrapMax     = 4 << 10
	saltLen     = 16
	endingLen   = 4
)

var (
	// ErrWrongKey means the case key is not the one the case was sealed with.
	ErrWrongKey = errors.New("case key is wrong")
	// ErrDamaged means the wrap file cannot be understood.
	ErrDamaged = errors.New("case key file is damaged")
)

// Check returns the check value for a derived key: lower-case hex of
// HMAC-SHA256(key, fixed label).
func Check(kek []byte) string {
	m := hmac.New(sha256.New, kek)
	m.Write([]byte(checkLabel))
	return hex.EncodeToString(m.Sum(nil))
}

// Ending is the last 4 characters of a check value: all a file or the screen shows.
func Ending(check string) string {
	if len(check) <= endingLen {
		return check
	}
	return check[len(check)-endingLen:]
}

// Equal compares two check values in constant time.
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// CheckPassphrase applies the rules for choosing a case key to pass (any form;
// the NFC form is judged): valid text, at least MinLen characters, at least 8
// visible. It returns masterkey's errors.
func CheckPassphrase(pass []byte) error {
	n := masterkey.Normalize(pass)
	defer clear(n)
	return masterkey.CheckPassphrase(n)
}

// Wrap is the readable part of case.wrap. Everything secret is in Blob.
type Wrap struct {
	V      int              `json:"v"`
	Salt   string           `json:"salt"`
	Params masterkey.Params `json:"params"`
	Ending string           `json:"e"`
	State  string           `json:"state"`
	Blob   string           `json:"blob"`
}

// secret is what Blob holds. The readable fields are repeated here so a file with
// edited readable fields is refused.
type secret struct {
	CM     string           `json:"cm"`
	Check  string           `json:"chk"`
	CaseID string           `json:"caseId"`
	State  string           `json:"state"`
	Ending string           `json:"e"`
	Params masterkey.Params `json:"params"`
}

// Opened is a wrap opened with the right key. Zero it when done.
type Opened struct {
	// CM is the 32-byte case master that seals the case's files.
	CM []byte
	// Check is the full check value; Ending its last 4 characters.
	Check  string
	Ending string
	State  string

	kek    []byte
	salt   []byte
	params masterkey.Params
	caseID string
}

// Zero clears the secrets held.
func (o *Opened) Zero() {
	clear(o.CM)
	clear(o.kek)
}

// ArchiveKey is the key that seals a case file of this case: the key-encryption key
// with the salt and cost it was derived with. The typed passphrase derives the same
// key from the file's own copy of the salt and cost, so the file opens anywhere. The
// copies are the caller's to clear.
func (o *Opened) ArchiveKey() (key, salt []byte, params masterkey.Params) {
	return append([]byte(nil), o.kek...), append([]byte(nil), o.salt...), o.params
}

// String guards against a stray %v of an Opened writing secrets anywhere.
func (o *Opened) String() string { return fmt.Sprintf("casekey.Opened(ending %s)", o.Ending) }

// GoString does the same for %#v.
func (o *Opened) GoString() string { return o.String() }

// New seals a fresh wrap for caseID under pass with a new salt and returns the
// file bytes. cm is the case master to wrap; nil draws a new one (returned in
// Opened). state is one of the State constants.
func New(pass []byte, params masterkey.Params, caseID string, cm []byte, state string) ([]byte, *Opened, error) {
	return NewWithSalt(pass, params, caseID, cm, state, nil)
}

// NewWithSalt is New with the salt named. A nil salt draws a fresh one. A fixed salt exists
// only so the demo's capture-only case shows the same key ending on every run; the live app
// never passes one.
func NewWithSalt(pass []byte, params masterkey.Params, caseID string, cm []byte, state string, salt []byte) ([]byte, *Opened, error) {
	if err := CheckPassphrase(pass); err != nil {
		return nil, nil, err
	}
	if salt != nil && len(salt) != saltLen {
		return nil, nil, ErrDamaged
	}
	if cm == nil {
		cm = make([]byte, masterkey.KeyLen)
		if _, err := rand.Read(cm); err != nil {
			return nil, nil, err
		}
	} else {
		cm = append([]byte(nil), cm...)
	}
	if salt == nil {
		salt = make([]byte, saltLen)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, err
		}
	} else {
		salt = append([]byte(nil), salt...)
	}
	kek, err := masterkey.DeriveKey(pass, salt, params)
	if err != nil {
		clear(cm)
		return nil, nil, err
	}
	o := &Opened{CM: cm, kek: kek, salt: salt, params: params, caseID: caseID}
	o.Check = Check(kek)
	o.Ending = Ending(o.Check)
	data, err := o.Seal(state)
	if err != nil {
		o.Zero()
		return nil, nil, err
	}
	return data, o, nil
}

// Seal writes the wrap again, in the given state, under the same key and salt.
func (o *Opened) Seal(state string) ([]byte, error) {
	if !validState(state) {
		return nil, ErrDamaged
	}
	sec := secret{CM: hex.EncodeToString(o.CM), Check: o.Check, CaseID: o.caseID, State: state, Ending: o.Ending, Params: o.params}
	plain, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	blob, err := masterkey.SealWith(o.kek, wrapLabel, plain)
	if err != nil {
		return nil, err
	}
	w := Wrap{V: wrapVersion, Salt: hex.EncodeToString(o.salt), Params: o.params, Ending: o.Ending, State: state,
		Blob: base64.StdEncoding.EncodeToString(blob)}
	return json.Marshal(w)
}

func validState(s string) bool {
	return s == StateActive || s == StateSetting || s == StateRemoving
}

// Parse reads the readable part of a case.wrap. It never derives anything.
func Parse(raw []byte) (Wrap, error) {
	var w Wrap
	if len(raw) == 0 || len(raw) > wrapMax || json.Unmarshal(raw, &w) != nil {
		return Wrap{}, ErrDamaged
	}
	salt, err := hex.DecodeString(w.Salt)
	if w.V != wrapVersion || err != nil || len(salt) < 8 || len(salt) > 64 || !w.Params.Validate() || !validState(w.State) || w.Blob == "" {
		return Wrap{}, ErrDamaged
	}
	return w, nil
}

// Open derives the key from pass and opens the wrap for caseID. A key that does
// not open it is ErrWrongKey; a wrap that opens but disagrees with itself or with
// caseID is ErrDamaged.
func (w Wrap) Open(pass []byte, caseID string) (*Opened, error) {
	salt, err := hex.DecodeString(w.Salt)
	if err != nil {
		return nil, ErrDamaged
	}
	kek, err := masterkey.DeriveKey(pass, salt, w.Params)
	if err != nil {
		return nil, ErrDamaged
	}
	blob, err := base64.StdEncoding.DecodeString(w.Blob)
	if err != nil {
		clear(kek)
		return nil, ErrDamaged
	}
	plain, err := masterkey.OpenWith(kek, wrapLabel, blob)
	if err != nil {
		clear(kek)
		return nil, ErrWrongKey
	}
	defer clear(plain)
	var sec secret
	if json.Unmarshal(plain, &sec) != nil {
		clear(kek)
		return nil, ErrDamaged
	}
	cm, err := hex.DecodeString(sec.CM)
	if err != nil || len(cm) != masterkey.KeyLen || sec.CaseID != caseID || sec.State != w.State ||
		sec.Ending != w.Ending || sec.Params != w.Params || !Equal(sec.Check, Check(kek)) || Ending(sec.Check) != sec.Ending {
		clear(kek)
		clear(cm)
		return nil, ErrDamaged
	}
	return &Opened{CM: cm, Check: sec.Check, Ending: sec.Ending, State: sec.State, kek: kek, salt: salt, params: w.Params, caseID: caseID}, nil
}
