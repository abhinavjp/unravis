package sqlaccess

import (
	"errors"
	"strings"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/dbhealth"
)

// Fixed sentences for a connection that cannot be saved for the Blackbox. They name no server, login or password.
var (
	ErrNotSQLLogin     = errors.New("Only a SQL login can be saved for the Blackbox. With Windows sign-in the SQL service uses its own Windows account.") //nolint:staticcheck // ST1005: shown to the person as a sentence
	ErrEncryptOptional = errors.New("The Blackbox always encrypts the SQL connection. Set Encrypt to mandatory, then try again.")                         //nolint:staticcheck // ST1005: shown to the person as a sentence
	ErrLoginShape      = errors.New("This SQL login or server name cannot be used by the Blackbox. Check the server name and the login.")                 //nolint:staticcheck // ST1005: shown to the person as a sentence
)

// StoredLoginFor turns a SQL connection into the login the Blackbox's SQL service keeps. It never reads the network. Windows sign-in has no
// login to keep, and a connection that allows an unencrypted link is refused (the service always encrypts).
func StoredLoginFor(t dbhealth.SQLTarget) (bbsql.StoredLogin, error) {
	if t.Auth != "sql" {
		return bbsql.StoredLogin{}, ErrNotSQLLogin
	}
	if t.Encrypt == "optional" {
		return bbsql.StoredLogin{}, ErrEncryptOptional
	}
	server := strings.TrimSpace(t.Server)
	for _, p := range []string{"tcp:", "TCP:"} {
		server = strings.TrimPrefix(server, p)
	}
	s := bbsql.StoredLogin{Server: server, Login: bbsql.Login{User: t.Username, Password: t.Password}, TrustServerCertificate: t.TrustServerCertificate}
	if s.Validate() != nil {
		return bbsql.StoredLogin{}, ErrLoginShape
	}
	return s, nil
}
