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

// sqlLoginFilePrefix starts the name of every folder WriteSQLLoginFile makes (and that older keepers made for the same purpose).
const sqlLoginFilePrefix = "unravis-sqllogin-"

// SQLLoginFileDoc is the document set-sql-login and setup-sql read (internal/cmd/unravis-blackbox: loginInput). Its field names are the
// helper's contract; the secret is in it, so it only ever lives in a short-lived file or in the keeper's memory.
type sqlLoginFileDoc struct {
	Server                 string `json:"server"`
	User                   string `json:"user"`
	Password               string `json:"password"`
	TrustServerCertificate bool   `json:"trustServerCertificate"`
}

// MarshalSQLLoginDoc encodes the login as the document the Blackbox helper reads. The caller zeroes the bytes when done with them.
func MarshalSQLLoginDoc(s bbsql.StoredLogin) ([]byte, error) {
	if s.Validate() != nil {
		return nil, errors.New("the SQL login is not valid")
	}
	data, err := json.Marshal(sqlLoginFileDoc{Server: s.Server, User: s.Login.User, Password: s.Login.Password, TrustServerCertificate: s.TrustServerCertificate}) //nolint:gosec // G117: the document is private to the caller, short-lived, and read back by the helper only
	if err != nil {
		return nil, errors.New("the SQL login could not be encoded")
	}
	return data, nil
}

// SQLLoginFile is a login file made for one helper call.
type SQLLoginFile struct {
	Dir  string `json:"dir"`  // the private folder made for it; remove it when the call ends
	Path string `json:"path"` // the file
	SHA  string `json:"sha"`  // SHA-256 of its bytes, hex
}

// WriteSQLLoginFile writes the login to a new private folder under root ("" is the temp folder), 0700 and 0600, and returns where it is and
// the hash of what was written (taken from the bytes in memory, never from a second read). The caller removes Dir when done.
//
// The key keeper does not use this (RV-SEC-2: it hands the login over its pipe and writes no file). It is for a login the person typed, or
// one held by their own per-user Unravis, where the file is made in that person's own temp folder.
func WriteSQLLoginFile(root string, s bbsql.StoredLogin) (SQLLoginFile, error) {
	data, err := MarshalSQLLoginDoc(s)
	if err != nil {
		return SQLLoginFile{}, errors.New("the SQL login could not be written")
	}
	defer func() {
		for i := range data {
			data[i] = 0
		}
	}()
	dir, err := os.MkdirTemp(root, sqlLoginFilePrefix)
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

// SweepSQLLoginFiles removes login folders left in root ("" is the temp folder) by an earlier run that ended before it could remove them (a
// restart, an update or a crash within the file's short life; older keepers also made them for every Blackbox SQL set-up). It touches only
// folders named like the ones WriteSQLLoginFile makes, never follows a link, and returns how many it removed. It reports no contents.
func SweepSQLLoginFiles(root string) int {
	if root == "" {
		root = os.TempDir()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		// A symbolic link shows as ModeSymlink; a Windows junction or other reparse point shows as ModeIrregular (with ModeDir). Neither is
		// a folder this package made, and removing through one could reach a folder somewhere else, so both are left alone.
		if !e.IsDir() || !strings.HasPrefix(e.Name(), sqlLoginFilePrefix) || e.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			continue
		}
		dir := filepath.Join(root, e.Name())
		// Look again at the folder itself, not what it points to, in case it changed since the listing.
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			continue
		}
		RemoveSQLLoginFile(SQLLoginFile{Dir: dir, Path: filepath.Join(dir, "login.json")})
		if _, err := os.Lstat(dir); err != nil {
			removed++
		}
	}
	return removed
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
