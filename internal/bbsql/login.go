// login.go is the optional stored SQL login for the Blackbox SQL service.
//
// Default stays integrated auth on the local instance (no secret anywhere).
// An owner who must reach an instance that integrated auth cannot (another
// server, or a login the service account has no right to) saves one SQL login
// on this PC. The login is sealed machine-wide in the locked \sql folder
// (loginstore.go); it is NEVER in blackbox.json, a pack, a log, an error, the
// status file or a case file. Every type here prints as redacted, so a stray
// %v cannot leak it.
package bbsql

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// Fixed codes for the stored login (C10: codes and fixed sentences only).
const (
	CodeLoginInvalid    = "login-invalid"
	CodeLoginUnreadable = "login-unreadable"
)

var (
	errLoginInvalid    = &Error{CodeLoginInvalid, "The saved SQL login settings are invalid."}
	errLoginUnreadable = &Error{CodeLoginUnreadable, "The saved SQL login could not be read. Save it again."}
)

const (
	maxLoginUser = 128 // SQL Server's own limit for a login name
	maxLoginPass = 128 // and for a password
)

// Login is a SQL Server login. It never prints its parts.
type Login struct{ User, Password string }

// Format keeps the login out of every fmt verb (%v %+v %#v %s).
func (Login) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte("[SQL login hidden]")) }

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Validate checks shape only. It never says which part is wrong in a way that
// echoes the value.
func (l Login) Validate() error {
	if l.User == "" || l.Password == "" ||
		len(l.User) > maxLoginUser || len(l.Password) > maxLoginPass ||
		hasControl(l.User) || hasControl(l.Password) {
		return errLoginInvalid
	}
	return nil
}

// remoteTarget is a parsed non-local server: host, optional instance, optional port.
type remoteTarget struct {
	host, instance string
	port           int
}

// splitRemote parses host, host\instance, host,port or host:port. IP literals
// and plain host names only; no scheme, path, credential or DSN character can
// ride along. ok is false for anything else.
func splitRemote(server string) (t remoteTarget, ok bool) {
	s := strings.TrimSpace(server)
	if s == "" || len(s) > 255 || hasControl(s) {
		return t, false
	}
	if i := strings.IndexByte(s, '\\'); i >= 0 {
		s, t.instance = s[:i], s[i+1:]
		if t.instance == "" || strings.ContainsAny(t.instance, "/\\;=@'\":,?#%& \t") {
			return t, false
		}
	}
	if i := strings.LastIndexAny(s, ",:"); i >= 0 && !strings.Contains(s[:i], ":") {
		p, err := strconv.Atoi(s[i+1:])
		if err != nil || p < 1 || p > 65535 || t.instance != "" {
			return t, false
		}
		s, t.port = s[:i], p
	}
	if net.ParseIP(s) == nil && !anchorHostRe.MatchString(s) {
		return t, false
	}
	t.host = s
	return t, true
}

// CheckTarget is the C12 gate for a configuration. Without a login only the
// local instance is allowed (unchanged). With a login a remote server is
// allowed, but its shape must be plain.
func CheckTarget(cfg ConnConfig) error {
	if CheckLocalTarget(cfg.Server) == nil {
		return nil
	}
	if cfg.Login == nil {
		return &Error{CodeRemoteTarget, "Only the local SQL Server can be monitored."}
	}
	if _, ok := splitRemote(cfg.Server); !ok {
		return &Error{CodeRemoteTarget, "The SQL server name is not valid."}
	}
	return nil
}

// remoteWithLogin reports whether this config reaches a non-local server (so
// the loopback assert in the dialer is replaced by the remote one).
func remoteWithLogin(cfg ConnConfig) bool {
	return cfg.Login != nil && CheckLocalTarget(cfg.Server) != nil
}
