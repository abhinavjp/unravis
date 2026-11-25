package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/bbsql"
)

// SQLLoginFileDoc is the document set-sql-login and setup-sql read (internal/cmd/unravis-blackbox: loginInput). Its field names are the
// helper's contract; the secret is in it, so it only ever lives in a short-lived file.
type sqlLoginFileDoc struct {
	Server                 string `json:"server"`
	User                   string `json:"user"`
	Password               string `json:"password"`
	TrustServerCertificate bool   `json:"trustServerCertificate"`
}

// SQLLoginFile is a login file made for one helper call.
type SQLLoginFile struct {
	Dir  string `json:"dir"`  // the private folder made for it; remove it when the call ends
	Path string `json:"path"` // the file
	SHA  string `json:"sha"`  // SHA-256 of its bytes, hex
}

// WriteSQLLoginFile writes the login to a new private folder under root ("" is the temp folder), 0700 and 0600, and returns where it is and
// the hash of what was written (taken from the bytes in memory, never from a second read). The caller removes Dir when done.
func WriteSQLLoginFile(root string, s bbsql.StoredLogin) (SQLLoginFile, error) {
	if s.Validate() != nil {
		return SQLLoginFile{}, errors.New("the SQL login is not valid")
	}
	data, err := json.Marshal(sqlLoginFileDoc{Server: s.Server, User: s.Login.User, Password: s.Login.Password, TrustServerCertificate: s.TrustServerCertificate}) //nolint:gosec // G117: the file is private to the writer, short-lived, and read back by the helper only
	if err != nil {
		return SQLLoginFile{}, errors.New("the SQL login could not be written")
	}
	defer func() {
		for i := range data {
			data[i] = 0
		}
	}()
	dir, err := os.MkdirTemp(root, "unravis-sqllogin-")
	if err != nil {
		return SQLLoginFile{}, errors.New("a private folder for the SQL login could not be made")
	}
	path := filepath.Join(dir, "login.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return SQLLoginFile{}, errors.New("the SQL login could not be written")
	}
	sum := sha256.Sum256(data)
	return SQLLoginFile{Dir: dir, Path: path, SHA: hex.EncodeToString(sum[:])}, nil
}

// SweepSQLLoginFiles removes login folders left in the temp folder by an earlier run that ended before it could remove them (a restart, an
// update or a crash within the file's short life). It touches only folders named like the ones WriteSQLLoginFile makes, and never follows a link.
func SweepSQLLoginFiles(root string) {
	if root == "" {
		root = os.TempDir()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "unravis-sqllogin-") || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		dir := filepath.Join(root, e.Name())
		RemoveSQLLoginFile(SQLLoginFile{Dir: dir, Path: filepath.Join(dir, "login.json")})
	}
}

// RemoveSQLLoginFile wipes and removes a login file made by WriteSQLLoginFile, and its folder.
func RemoveSQLLoginFile(f SQLLoginFile) {
	if f.Path != "" {
		if fi, err := os.Lstat(f.Path); err == nil && fi.Mode().IsRegular() && fi.Size() <= 8<<10 {
			_ = os.WriteFile(f.Path, make([]byte, fi.Size()), 0o600)
		}
	}
	if f.Dir != "" {
		_ = os.RemoveAll(f.Dir)
	}
}
