package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// The session file tells the MCP bridge where the running dashboard listens and
// which session token it accepts. The dashboard writes it when it starts and
// removes it when it shuts down cleanly. It lives in the per-user application
// folder (AUDIT_CONFIG_DIR, or the user config folder plus APIAuditExplorer),
// the same folder as saved connections, so only the same Windows user can read
// it. The token never appears in any page the dashboard serves.

// SessionFileName is the session file's name inside the application folder.
const SessionFileName = "session.json"

// maxSessionFileBytes bounds the read: a 4096-byte token plus JSON framing.
const maxSessionFileBytes = 16 << 10

// maxTokenBytes and minTokenRunes bound a session token, as the dashboard does.
const (
	maxTokenBytes = 4096
	minTokenRunes = 32
)

type sessionRecord struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

// SessionDir is the per-user application folder: AUDIT_CONFIG_DIR when set,
// otherwise the user config folder plus APIAuditExplorer.
func SessionDir(getenv func(string) string) (string, error) {
	if getenv != nil {
		if dir := getenv("AUDIT_CONFIG_DIR"); dir != "" {
			return dir, nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "APIAuditExplorer"), nil
}

// SessionFilePath is the session file inside SessionDir.
func SessionFilePath(getenv func(string) string) (string, error) {
	dir, err := SessionDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SessionFileName), nil
}

// ValidToken reports whether token has the shape the dashboard accepts: at
// least 32 characters, at most 4096 bytes, visible ASCII only.
func ValidToken(token string) bool {
	if len(token) < minTokenRunes || len(token) > maxTokenBytes {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return false
		}
	}
	return true
}

// WriteSessionFile records port and token at path. The folder is created
// owner-only and the file is written owner-only (0600) to a temporary name,
// flushed and renamed into place, so a reader never sees a partial file.
func WriteSessionFile(path string, port int, token string) error {
	if port < 1 || port > 65535 || !ValidToken(token) {
		return errors.New("invalid session")
	}
	data, err := json.Marshal(sessionRecord{Port: port, Token: token})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp opens the file 0600. Windows ignores most mode bits; there the
	// per-user folder's ACL is what keeps other users out.
	temp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ReadSessionFile returns the port and token recorded at path.
func ReadSessionFile(path string) (int, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSessionFileBytes+1))
	if err != nil {
		return 0, "", err
	}
	if len(data) > maxSessionFileBytes {
		return 0, "", errors.New("session file too large")
	}
	var record sessionRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return 0, "", errors.New("session file is not valid")
	}
	if record.Port < 1 || record.Port > 65535 || !ValidToken(record.Token) {
		return 0, "", errors.New("session file is not valid")
	}
	return record.Port, record.Token, nil
}

// RemoveSessionFile deletes the session file at path only while it still holds
// token, so a dashboard that exits never removes the file a newer one wrote.
func RemoveSessionFile(path, token string) error {
	_, recorded, err := ReadSessionFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if recorded != token {
		return nil
	}
	return os.Remove(path)
}
