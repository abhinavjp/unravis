// Package masterkey is the passphrase protector behind the optional master key
// (decision D-1): one passphrase the user chooses for this PC is stretched with
// Argon2id into a 32-byte wrapping key. The key lives only in a Keyring, in
// memory, for the running session. Only a salt, the Argon2id parameters and a
// verifier (a constant sealed under the key) are written to disk, so a wrong
// passphrase is detected without touching any case. Invariant I5: the passphrase
// and the key are never written, logged, returned or put in an error.
package masterkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"api-audit-explorer/internal/sealedfile"
)

// FileName is the master key file, kept next to connections.dat.
const FileName = "masterkey.dat"

// MinPassphraseLen is the shortest accepted passphrase, in characters (runes,
// counted after NFC normalisation).
const MinPassphraseLen = 12

// KeyLen is the length of the wrapping key and of every key this package derives.
const KeyLen = 32

const (
	fileVersion = 1
	// fileMax bounds what Unlock will read. A real file is under 400 bytes.
	fileMax = 4 << 10
	saltLen = 16

	// Sane bounds for stored Argon2id parameters. A damaged file must not make
	// the process allocate gigabytes or spin for minutes.
	minTime, maxTime           = 1, 10
	minMemoryKiB, maxMemoryKiB = 16 << 10, 256 << 10 // 16 MiB .. 256 MiB
	minThreads, maxThreads     = 1, 16

	blobVersion byte = 1
	nonceLen         = 12

	// keyringLabel is the AAD label Keyring.Protect/Unprotect use.
	keyringLabel  = "unravis/masterkey/protect/v1"
	verifierLabel = "unravis/masterkey/verifier/v1"
	verifierConst = "unravis master key verifier v1"
)

// MinVisibleLen is the fewest visible characters a passphrase must hold: runes
// that are not white space, control or format characters (zero-width spaces,
// bidi marks, variation selectors) and not one of the blank filler letters. A
// passphrase of twelve blanks is 12 characters long but nobody can see or
// recall it.
const MinVisibleLen = 8

var (
	// ErrWrongKey is the failure Unlock reports for a passphrase that does not
	// match a well-formed masterkey.dat.
	ErrWrongKey = errors.New("master key is wrong")
	// ErrDamaged means masterkey.dat exists but is not a usable key file: empty,
	// oversized, not JSON, unknown version, out-of-bounds parameters, bad
	// salt or verifier, or not a regular file at all. No passphrase can open it.
	ErrDamaged = errors.New("master key file is damaged")
	// ErrBlank means the passphrase has too few visible characters.
	ErrBlank = errors.New("passphrase has too few visible characters")
	// ErrInvalidChars means the passphrase is not valid text (invalid UTF-8 or
	// the replacement character U+FFFD that a lone surrogate turns into).
	ErrInvalidChars = errors.New("passphrase has invalid characters")
	// ErrNotSet means no master key has been set up in this folder.
	ErrNotSet = errors.New("master key is not set")
	// ErrAlreadySet means Setup was asked to overwrite an existing master key.
	ErrAlreadySet = errors.New("master key is already set")
	// ErrTooShort means the passphrase has fewer than MinPassphraseLen characters.
	ErrTooShort = errors.New("passphrase is too short")
	// ErrLocked means the Keyring has been locked (its key zeroed).
	ErrLocked = errors.New("master key is locked")
	// ErrCiphertext is the single failure for any ciphertext that does not open:
	// tampered, truncated, wrong key, wrong label or unknown version.
	ErrCiphertext = errors.New("sealed data cannot be opened")
)

// Params are the Argon2id cost parameters. They are stored in masterkey.dat so
// they can be raised later. Memory is in KiB.
type Params struct {
	Time    uint32 `json:"t"`
	Memory  uint32 `json:"m"`
	Threads uint8  `json:"p"`
}

// DefaultParams are the parameters Setup uses: time 3, 64 MiB, parallelism 4.
func DefaultParams() Params { return Params{Time: 3, Memory: 64 << 10, Threads: 4} }

// Validate reports whether p is inside the accepted bounds.
func (p Params) Validate() bool {
	return p.Time >= minTime && p.Time <= maxTime &&
		p.Memory >= minMemoryKiB && p.Memory <= maxMemoryKiB &&
		p.Threads >= minThreads && p.Threads <= maxThreads
}

type fileDoc struct {
	V        int    `json:"v"`
	KDF      string `json:"kdf"`
	Params          // t, m, p
	Salt     string `json:"salt"`
	Verifier string `json:"verifier"`
}

// Path is the location of the master key file inside dir.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Exists reports whether a master key file is present in dir.
func Exists(dir string) bool {
	fi, err := os.Stat(Path(dir))
	return err == nil && fi.Mode().IsRegular()
}

// FileState is what is in the master key slot of a folder.
type FileState int

const (
	// FileAbsent: nothing at masterkey.dat.
	FileAbsent FileState = iota
	// FileUsable: a well-formed key file (the passphrase is not checked).
	FileUsable
	// FileDamaged: something is there that no passphrase can open.
	FileDamaged
)

// Inspect classifies masterkey.dat in dir without any passphrase. A read error
// that is not proof of damage (a sharing violation, say) counts as usable, so a
// passing glitch never offers the user a reset.
func Inspect(dir string) FileState {
	_, _, _, st, err := readDoc(dir)
	if err != nil {
		return FileUsable
	}
	return st
}

// readDoc reads and structurally validates masterkey.dat. It reports FileAbsent
// or FileDamaged without an error; a non-nil error is an I/O failure that proves
// nothing either way.
func readDoc(dir string) (doc fileDoc, salt, ver []byte, st FileState, err error) {
	// Look at the name first (Lstat does not follow a link or reparse point), open
	// it once, then require that the open handle is the very file Lstat described.
	// A swap between the two calls is caught by SameFile, and the read goes through
	// the handle with a hard cap, so no size check by path can be outrun.
	fi, err := os.Lstat(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil, nil, FileAbsent, nil
	}
	if err != nil {
		return doc, nil, nil, FileUsable, err
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > fileMax {
		return doc, nil, nil, FileDamaged, nil
	}
	f, err := os.Open(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil, nil, FileAbsent, nil
	}
	if err != nil {
		return doc, nil, nil, FileUsable, err
	}
	defer f.Close()
	hi, err := f.Stat()
	if err != nil {
		return doc, nil, nil, FileUsable, err
	}
	if !hi.Mode().IsRegular() || !os.SameFile(fi, hi) {
		return doc, nil, nil, FileDamaged, nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, fileMax+1))
	if err != nil {
		return doc, nil, nil, FileUsable, err
	}
	if len(raw) == 0 || len(raw) > fileMax {
		return doc, nil, nil, FileDamaged, nil
	}
	if json.Unmarshal(raw, &doc) != nil || doc.V != fileVersion || doc.KDF != "argon2id" {
		return doc, nil, nil, FileDamaged, nil
	}
	salt, err1 := hex.DecodeString(doc.Salt)
	ver, err2 := hex.DecodeString(doc.Verifier)
	if err1 != nil || err2 != nil || !doc.Params.Validate() ||
		len(salt) < 8 || len(salt) > 64 ||
		len(ver) != 1+nonceLen+len(verifierConst)+gcmTag {
		return doc, nil, nil, FileDamaged, nil
	}
	return doc, salt, ver, FileUsable, nil
}

// gcmTag is the AES-GCM tag length.
const gcmTag = 16

// Visible counts the characters of pass (read as UTF-8) a person can see: not
// white space, control, format, surrogate or private-use characters, not a
// variation selector, and not a blank filler letter.
func Visible(pass []byte) int {
	n := 0
	for len(pass) > 0 {
		r, size := utf8.DecodeRune(pass)
		pass = pass[size:]
		if isVisible(r) {
			n++
		}
	}
	return n
}

func isVisible(r rune) bool {
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0, 0x2800, 0x180E, 0x034F, 0x17B4, 0x17B5:
		return false // Hangul/Khmer fillers, braille blank, Mongolian vowel separator, grapheme joiner
	}
	if r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF {
		return false // variation selectors
	}
	if unicode.IsSpace(r) || unicode.IsControl(r) {
		return false
	}
	return !unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Cn)
}

// CheckPassphrase applies the rules for choosing a new passphrase to the NFC
// form n: valid text, at least MinPassphraseLen characters, at least
// MinVisibleLen of them visible.
func CheckPassphrase(n []byte) error {
	if !utf8.Valid(n) || bytes.ContainsRune(n, utf8.RuneError) {
		return ErrInvalidChars
	}
	if utf8.RuneCount(n) < MinPassphraseLen {
		return ErrTooShort
	}
	if Visible(n) < MinVisibleLen {
		return ErrBlank
	}
	return nil
}

// Normalize returns pass in Unicode NFC form as a new slice. The caller owns it
// and should clear it after use.
func Normalize(pass []byte) []byte {
	cp := append([]byte(nil), pass...)
	out := norm.NFC.Bytes(cp)
	if len(out) > 0 && len(cp) > 0 && &out[0] != &cp[0] {
		clear(cp)
	}
	return out
}

// DeriveKey stretches pass (NFC-normalised first) with Argon2id into a 32-byte
// key. It does not modify or zero pass. The caller must clear the result.
// Parameters outside the accepted bounds are refused before any allocation.
func DeriveKey(pass, salt []byte, p Params) ([]byte, error) {
	if !p.Validate() || len(salt) < 8 || len(salt) > 64 {
		return nil, ErrWrongKey
	}
	n := Normalize(pass)
	defer clear(n)
	return argon2.IDKey(n, salt, p.Time, p.Memory, p.Threads, KeyLen), nil
}

// SealWith encrypts plain under key with AES-256-GCM: version byte, random
// 12-byte nonce, ciphertext and tag. The version byte and label are bound as AAD
// so a blob made for one purpose does not open for another.
func SealWith(key []byte, label string, plain []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+nonceLen, 1+nonceLen+len(plain)+aead.Overhead())
	out[0] = blobVersion
	if _, err := rand.Read(out[1 : 1+nonceLen]); err != nil {
		return nil, err
	}
	return aead.Seal(out, out[1:1+nonceLen], plain, aad(label)), nil
}

// OpenWith reverses SealWith. Every failure returns ErrCiphertext.
func OpenWith(key []byte, label string, blob []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, ErrCiphertext
	}
	if len(blob) < 1+nonceLen+aead.Overhead() || blob[0] != blobVersion {
		return nil, ErrCiphertext
	}
	plain, err := aead.Open(nil, blob[1:1+nonceLen], blob[1+nonceLen:], aad(label))
	if err != nil {
		return nil, ErrCiphertext
	}
	return plain, nil
}

func aad(label string) []byte { return append([]byte{blobVersion}, label...) }

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyLen {
		return nil, ErrLocked
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Setup creates masterkey.dat in dir. It refuses a passphrase under 12
// characters and refuses to overwrite an existing master key (that would orphan
// every case it protects). pass is zeroed before Setup returns.
func Setup(dir string, pass []byte) error {
	defer clear(pass)
	n := Normalize(pass)
	defer clear(n)
	if err := CheckPassphrase(n); err != nil {
		return err
	}
	if _, err := os.Lstat(Path(dir)); err == nil {
		if Inspect(dir) == FileDamaged {
			return ErrDamaged
		}
		return ErrAlreadySet
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	p := DefaultParams()
	key, err := DeriveKey(n, salt, p)
	if err != nil {
		return err
	}
	defer clear(key)
	ver, err := SealWith(key, verifierLabel, []byte(verifierConst))
	if err != nil {
		return err
	}
	data, err := json.Marshal(fileDoc{
		V: fileVersion, KDF: "argon2id", Params: p,
		Salt: hex.EncodeToString(salt), Verifier: hex.EncodeToString(ver),
	})
	if err != nil {
		return err
	}
	return sealedfile.WriteAtomic(Path(dir), "masterkey.*.tmp", data)
}

// Unlock derives the key from pass and checks it against the verifier. A
// passphrase that does not match gives ErrWrongKey; a masterkey.dat that no
// passphrase could open (empty, garbage, out-of-bounds parameters, not a file)
// gives ErrDamaged, before any key derivation; a missing file gives ErrNotSet.
// pass is zeroed before Unlock returns.
func Unlock(dir string, pass []byte) (*Keyring, error) {
	defer clear(pass)
	doc, salt, ver, st, err := readDoc(dir)
	if err != nil {
		return nil, err
	}
	switch st {
	case FileAbsent:
		return nil, ErrNotSet
	case FileDamaged:
		return nil, ErrDamaged
	}
	key, err := DeriveKey(pass, salt, doc.Params)
	if err != nil {
		return nil, ErrDamaged
	}
	plain, err := OpenWith(key, verifierLabel, ver)
	if err != nil || string(plain) != verifierConst {
		clear(key)
		return nil, ErrWrongKey
	}
	return &Keyring{key: key}, nil
}
