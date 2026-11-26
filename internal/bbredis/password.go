package bbredis

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/sealedfile"
)

// PasswordFile is the sealed Redis password inside the Blackbox's locked redis folder. The folder's access list (SYSTEM
// and Administrators only) is what keeps other accounts away from it; the machine-wide seal keeps it unreadable off
// this PC. The password never goes into blackbox.json, a pack, a log line or an answer.
const PasswordFile = "password.sealed"

const maxPasswordFile = 4 << 10

// ErrPassword is the one error of the store. It never carries the password, a path or the cause.
var ErrPassword = errors.New("bbredis: the Redis password could not be saved or read")

type passwordDoc struct {
	V    int    `json:"v"`
	Pass string `json:"password"`
}

func passwordPath(dir string) string { return filepath.Join(dir, PasswordFile) }

// ValidPassword says whether pw can be stored: 1 to 256 bytes and no control character.
func ValidPassword(pw string) bool {
	if pw == "" || len(pw) > 256 {
		return false
	}
	return !strings.ContainsFunc(pw, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// SavePassword seals pw into dir. The folder must already exist (the caller creates and locks it); this never creates
// one, so a password is never written into a folder that was not locked first.
func SavePassword(dir, pw string, p sealedfile.Protector) error {
	if p == nil || !ValidPassword(pw) {
		return ErrPassword
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return ErrPassword
	}
	raw, err := json.Marshal(passwordDoc{V: 1, Pass: pw}) //nolint:gosec // the document is sealed before it is stored
	if err != nil {
		return ErrPassword
	}
	sealed, err := p.Protect(raw)
	for i := range raw {
		raw[i] = 0
	}
	if err != nil {
		return ErrPassword
	}
	if err := sealedfile.WriteAtomic(passwordPath(dir), "password-*.tmp", sealed); err != nil {
		return ErrPassword
	}
	return nil
}

// LoadPassword reads the sealed password. found is false (and err nil) when none is saved. A file that is there but
// cannot be read is an error, never a silent fall back to "no password".
func LoadPassword(dir string, p sealedfile.Protector) (pw string, found bool, err error) {
	path := passwordPath(dir)
	fi, serr := os.Lstat(path)
	if errors.Is(serr, os.ErrNotExist) {
		return "", false, nil
	}
	if serr != nil || !fi.Mode().IsRegular() || fi.Size() > maxPasswordFile || p == nil {
		return "", true, ErrPassword
	}
	sealed, rerr := os.ReadFile(path) //nolint:gosec // a fixed name inside the locked redis folder
	if rerr != nil {
		return "", true, ErrPassword
	}
	raw, uerr := p.Unprotect(sealed)
	if uerr != nil {
		return "", true, ErrPassword
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	var d passwordDoc
	if json.Unmarshal(raw, &d) != nil || d.V != 1 || !ValidPassword(d.Pass) {
		return "", true, ErrPassword
	}
	return d.Pass, true, nil
}

// HasPassword reports whether a password file exists in dir (no secret is read).
func HasPassword(dir string) bool {
	_, err := os.Lstat(passwordPath(dir))
	return err == nil
}

// RemovePassword deletes the sealed password. Missing is fine.
func RemovePassword(dir string) error {
	err := os.Remove(passwordPath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrPassword
	}
	return nil
}
