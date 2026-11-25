package mcpagent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// ErrNoCred is the answer of Load when this host has nothing stored yet.
var ErrNoCred = errors.New("mcpagent: no stored credential")

var (
	profileRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	b64urRe   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

const (
	nonceLen  = 22 // 16 random bytes, base64url, no padding: what mcpsrv.Enroll takes
	secretLen = 43 // 32 random bytes, base64url, no padding: what mcpsrv.Claim hands out
	maxCred   = 4 << 10
)

// Cred is what one host installation keeps between runs: the profile id, then either the nonce it enrolled with (waiting for the
// person's approval) or the secret it claimed. Both are secrets and are only ever on disk sealed.
type Cred struct {
	V       int    `json:"v"`
	Profile string `json:"profile"`
	Nonce   string `json:"nonce,omitempty"`
	Secret  string `json:"secret,omitempty"`
}

func (c Cred) valid() bool {
	if c.V != 1 || !profileRe.MatchString(c.Profile) {
		return false
	}
	nonceOK := len(c.Nonce) == nonceLen && b64urRe.MatchString(c.Nonce)
	secretOK := len(c.Secret) == secretLen && b64urRe.MatchString(c.Secret)
	return nonceOK != secretOK // exactly one of the two
}

// Store keeps one sealed file per profile in Dir (bridge-<profile>.cred). Prot seals it for the current user (DPAPI on Windows); with
// no protector nothing is stored, and the host stays waiting: a credential is never written in the clear.
type Store struct {
	Dir  string
	Prot sealedfile.Protector
}

func (s Store) path(profile string) (string, error) {
	if s.Dir == "" || !filepath.IsAbs(s.Dir) || s.Prot == nil || !profileRe.MatchString(profile) {
		return "", sealedfile.ErrUnavailable
	}
	return filepath.Join(s.Dir, "bridge-"+profile+".cred"), nil
}

// Load reads and unseals the credential of profile. A file that does not unseal (copied from another account), is too large or has the
// wrong shape is removed and reported as ErrNoCred: the host enrolls again and the person approves again.
func (s Store) Load(profile string) (Cred, error) {
	p, err := s.path(profile)
	if err != nil {
		return Cred{}, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return Cred{}, ErrNoCred
	}
	if err != nil {
		return Cred{}, sealedfile.ErrUnavailable
	}
	// The file is closed before it can be removed: Windows does not delete an open file. One Read can return less than the file holds.
	buf, rerr := io.ReadAll(io.LimitReader(f, maxCred+1))
	_ = f.Close()
	if rerr != nil {
		return Cred{}, sealedfile.ErrUnavailable
	}
	var c Cred
	if len(buf) > maxCred || len(buf) == 0 {
		_ = os.Remove(p)
		return Cred{}, ErrNoCred
	}
	plain, err := s.Prot.Unprotect(buf)
	if err == nil {
		err = json.Unmarshal(plain, &c)
	}
	if err != nil || !c.valid() || c.Profile != profile {
		_ = os.Remove(p)
		return Cred{}, ErrNoCred
	}
	return c, nil
}

// Save seals and writes the credential (atomic, 0600, the folder restricted to the current user).
func (s Store) Save(c Cred) error {
	if !c.valid() {
		return errors.New("mcpagent: credential not valid")
	}
	p, err := s.path(c.Profile)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	sealed, err := s.Prot.Protect(plain)
	if err != nil {
		return sealedfile.ErrUnavailable
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return sealedfile.ErrUnavailable
	}
	if err := casestore.SecureDir(s.Dir); err != nil {
		return sealedfile.ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(p, "bridge-*.tmp", sealed); err != nil {
		return sealedfile.ErrUnavailable
	}
	return nil
}

// Delete removes the stored credential of profile (nothing to remove is not an error).
func (s Store) Delete(profile string) error {
	p, err := s.path(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// newNonce makes the claim nonce from r (crypto/rand in production).
func newNonce(r io.Reader) (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
