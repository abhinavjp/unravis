package bbsql

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/sealedfile"
)

// LoginFile is the sealed login inside the locked \sql folder. The folder's
// DACL (service, SYSTEM, Administrators) is what keeps other accounts away
// from it; the machine-wide seal only keeps it unreadable off this PC.
const LoginFile = "login.sealed"

const maxLoginFile = 8 << 10

// StoredLogin is everything the service needs to reach a SQL Server with a
// login: the server, the login and whether the owner chose to trust an
// unverified certificate. It prints as hidden (the embedded Login does).
type StoredLogin struct {
	Server                 string
	Login                  Login
	TrustServerCertificate bool
}

type loginDoc struct {
	V      int    `json:"v"`
	Server string `json:"server"`
	User   string `json:"user"`
	Pass   string `json:"password"`
	Trust  bool   `json:"trust,omitempty"`
}

// Validate checks the server shape and the login.
func (s StoredLogin) Validate() error {
	if err := s.Login.Validate(); err != nil {
		return err
	}
	cfg := ConnConfig{Server: s.Server, Login: &s.Login}
	if err := CheckTarget(cfg); err != nil {
		return errLoginInvalid
	}
	return nil
}

// Conn returns base with this login applied. Trust anchors in base win over
// TrustServerCertificate (connDSN enforces that).
func (s StoredLogin) Conn(base ConnConfig) ConnConfig {
	l := s.Login
	base.Server = s.Server
	base.Login = &l
	base.TrustServerCertificate = s.TrustServerCertificate
	return base
}

func loginPath(dir string) string { return filepath.Join(dir, LoginFile) }

// SaveLogin seals s into dir. The folder must already exist (install-sql
// creates and locks it); this never creates one, so a login is never written
// into a folder that was not locked first.
func SaveLogin(dir string, s StoredLogin, p sealedfile.Protector) error {
	if p == nil {
		return errLoginUnreadable
	}
	if err := s.Validate(); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errLoginUnreadable
	}
	raw, err := json.Marshal(loginDoc{V: 1, Server: s.Server, User: s.Login.User, Pass: s.Login.Password, Trust: s.TrustServerCertificate})
	if err != nil {
		return errLoginUnreadable
	}
	sealed, err := p.Protect(raw)
	for i := range raw {
		raw[i] = 0
	}
	if err != nil {
		return errLoginUnreadable
	}
	if err := sealedfile.WriteAtomic(loginPath(dir), "login-*.tmp", sealed); err != nil {
		return errLoginUnreadable
	}
	return nil
}

// LoadLogin reads the sealed login. found is false (and err nil) when none is
// saved. A file that is there but cannot be read is an error, never a silent
// fall back to integrated auth.
func LoadLogin(dir string, p sealedfile.Protector) (s StoredLogin, found bool, err error) {
	path := loginPath(dir)
	fi, serr := os.Lstat(path)
	if errors.Is(serr, os.ErrNotExist) {
		return s, false, nil
	}
	if serr != nil || !fi.Mode().IsRegular() || fi.Size() > maxLoginFile || p == nil {
		return s, true, errLoginUnreadable
	}
	sealed, rerr := os.ReadFile(path) //nolint:gosec // a fixed name inside the locked SQL folder
	if rerr != nil {
		return s, true, errLoginUnreadable
	}
	raw, uerr := p.Unprotect(sealed)
	if uerr != nil {
		return s, true, errLoginUnreadable
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	var d loginDoc
	if json.Unmarshal(raw, &d) != nil || d.V != 1 {
		return s, true, errLoginUnreadable
	}
	s = StoredLogin{Server: d.Server, Login: Login{User: d.User, Password: d.Pass}, TrustServerCertificate: d.Trust}
	if s.Validate() != nil {
		return StoredLogin{}, true, errLoginUnreadable
	}
	return s, true, nil
}

// HasLogin reports whether a login file exists in dir (no secret is read).
func HasLogin(dir string) bool {
	_, err := os.Lstat(loginPath(dir))
	return err == nil
}

// RemoveLogin deletes the sealed login. Missing is fine.
func RemoveLogin(dir string) error {
	err := os.Remove(loginPath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errLoginUnreadable
	}
	return nil
}
