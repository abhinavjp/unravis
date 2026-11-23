package main

// sqllogin.go: set-sql-login and clear-sql-login.
//
// These are the only commands that touch the stored SQL login (see
// internal/bbsql/login.go). They run as an administrator, never run SQL and
// never print, log or answer with any part of the login: every failure is a
// fixed reason and a fixed sentence. The login arrives either on standard
// input (an administrator at a console) or in a short-lived file that Unravis
// made and hashed before the permission prompt (--login-file with
// --login-sha256). The file is wiped and removed as soon as it is read.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sealedfile"
)

const maxLoginInput = 8 << 10

// Fixed reasons of the login commands.
const (
	reasonLoginInvalid = "login-invalid"
	reasonLoginSave    = "login-save"
	reasonLoginNoSQL   = "login-no-sql"
)

func init() {
	sqlReasonSentences[reasonLoginInvalid] = "The SQL login settings are not valid. Check the server name, login and password."
	sqlReasonSentences[reasonLoginSave] = "The SQL login could not be saved or removed."
	sqlReasonSentences[reasonLoginNoSQL] = "Install the SQL service first (install-sql). The login is saved inside its locked folder."
}

// loginInput is the document the command reads. Unknown fields are refused.
type loginInput struct {
	Server                 string `json:"server"`
	User                   string `json:"user"`
	Password               string `json:"password"`
	TrustServerCertificate bool   `json:"trustServerCertificate"`
}

type loginErr string

func (e loginErr) Error() string { return string(e) }

func (a answer) failLogin(reason string) int {
	msg := sqlReasonSentences[reason]
	if a.asJSON {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": false, "reason": reason, "message": msg})
	} else {
		fmt.Fprintln(a.err, msg)
	}
	return exitFail
}

// sealer is the machine-wide protector for the saved login.
func (d deps) loginSealer() (sealedfile.Protector, error) {
	if d.sealer != nil {
		return d.sealer()
	}
	return sealedfile.LocalMachine()
}

// readLoginInput reads the login document from the hash-bound file or stdin.
func (d deps) readLoginInput(file, sha string, stdin bool) ([]byte, error) {
	switch {
	case file != "" && !stdin:
		if sha == "" {
			return nil, loginErr(reasonLoginInvalid)
		}
		fi, err := os.Lstat(file)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxLoginInput {
			return nil, loginErr(reasonLoginInvalid)
		}
		b, err := os.ReadFile(file) //nolint:gosec // the path is the one Unravis made; the hash below binds the content
		// Wipe, then remove: the file existed only for this call.
		_ = os.WriteFile(file, make([]byte, len(b)), 0o600)
		_ = os.Remove(file)
		if err != nil {
			return nil, loginErr(reasonLoginInvalid)
		}
		sum := sha256.Sum256(b)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(sha)) {
			wipe(b)
			return nil, loginErr(reasonLoginInvalid)
		}
		return b, nil
	case stdin && file == "":
		in := d.stdin
		if in == nil {
			in = os.Stdin
		}
		b, err := io.ReadAll(io.LimitReader(in, maxLoginInput+1))
		if err != nil || len(b) > maxLoginInput {
			wipe(b)
			return nil, loginErr(reasonLoginInvalid)
		}
		return b, nil
	}
	return nil, loginErr(reasonLoginInvalid)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func (d deps) sqlLoginDir(configPath string) (string, error) {
	if !d.sqlMgr.Env.IsAdmin() {
		return "", blackbox.ErrNotAdmin
	}
	cfg, err := loadInstalledConfig(configPath)
	if err != nil {
		return "", err
	}
	return blackbox.SQLDataDir(cfg.Folder), nil
}

func runSetSQLLogin(a answer, d deps, configPath, file, sha string, stdin bool) int {
	dir, err := d.sqlLoginDir(configPath)
	if err != nil {
		return a.failSQL(err)
	}
	if fi, serr := os.Stat(dir); serr != nil || !fi.IsDir() {
		return a.failLogin(reasonLoginNoSQL)
	}
	raw, err := d.readLoginInput(file, sha, stdin)
	if err != nil {
		return a.failLogin(reasonLoginInvalid)
	}
	defer wipe(raw)
	var in loginInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		return a.failLogin(reasonLoginInvalid)
	}
	s := bbsql.StoredLogin{Server: in.Server, Login: bbsql.Login{User: in.User, Password: in.Password}, TrustServerCertificate: in.TrustServerCertificate}
	if s.Validate() != nil {
		return a.failLogin(reasonLoginInvalid)
	}
	p, err := d.loginSealer()
	if err != nil {
		return a.failLogin(reasonLoginSave)
	}
	if err := bbsql.SaveLogin(dir, s, p); err != nil {
		return a.failLogin(reasonLoginSave)
	}
	return a.ok("The SQL login is saved on this PC. Restart the SQL service (enable-sql) to use it. It is not in the settings file, the logs or any pack.",
		map[string]any{"saved": true})
}

func runClearSQLLogin(a answer, d deps, configPath string) int {
	dir, err := d.sqlLoginDir(configPath)
	if err != nil {
		return a.failSQL(err)
	}
	if err := bbsql.RemoveLogin(dir); err != nil {
		return a.failLogin(reasonLoginSave)
	}
	return a.ok("The saved SQL login is removed. The SQL service goes back to Windows sign-in on the next start.", map[string]any{"saved": false})
}
