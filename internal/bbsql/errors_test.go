package bbsql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
)

// fixedCodes is the complete C10 code set (plus the fixed conn.go codes a
// pass-through error may already carry).
var fixedCodes = map[string]bool{
	CodeLoginFailed: true, CodeDatabase: true, CodePermission: true,
	CodeConnectTimeout: true, CodeNetwork: true, CodeTLS: true,
	CodeTooManyRights: true, CodeFailed: true,
	CodeRemoteTarget: true, CodeConnect: true, CodeNotAllowed: true,
	CodeTimeout: true, CodeQuery: true,
}

// TestClassifyFixedCodes: driver errors map to fixed codes only; no code or
// message ever contains the raw driver text (host, login, database, phrase).
func TestClassifyFixedCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"mssql-login", mssql.Error{Number: 18456, Message: "Login failed for user 'SECRET\\svc'."}, CodeLoginFailed},
		{"mssql-untrusted", mssql.Error{Number: 18452, Message: "login from an untrusted domain"}, CodeLoginFailed},
		{"mssql-password", mssql.Error{Number: 18488, Message: "the password must be changed"}, CodeLoginFailed},
		{"mssql-database", mssql.Error{Number: 4060, Message: "Cannot open database \"secret_db\" requested by the login."}, CodeDatabase},
		{"mssql-permission", mssql.Error{Number: 229, Message: "The SELECT permission was denied on the object 'secret_table'."}, CodePermission},
		{"mssql-timeout", mssql.Error{Number: -2, Message: "Timeout expired."}, CodeConnectTimeout},
		{"phrase-login", errors.New("Login failed for user 'sa'."), CodeLoginFailed},
		{"phrase-database", errors.New("Cannot open database \"secret_db\" requested by the login."), CodeDatabase},
		{"phrase-permission", errors.New("The SELECT permission was denied on the object 'secret_table'."), CodePermission},
		{"phrase-tls", errors.New("TLS handshake failed: x509: certificate signed by unknown authority"), CodeTLS},
		{"phrase-timeout", errors.New("read tcp 127.0.0.1:1433: i/o timeout"), CodeConnectTimeout},
		{"deadline", context.DeadlineExceeded, CodeConnectTimeout},
		{"phrase-network", errors.New("dial tcp 127.0.0.1:1433: connectex: No connection could be made."), CodeNetwork},
		{"phrase-host", errors.New("lookup SQLHOST: no such host"), CodeNetwork},
		{"unknown", errors.New("something completely unforeseen"), CodeFailed},
		{"too-many-rights", ErrTooManyRights, CodeTooManyRights},
		{"wrapped", fmt.Errorf("open: %w", mssql.Error{Number: 18456, Message: "Login failed for user 'SECRET'."}), CodeLoginFailed},
		{"already-fixed", &Error{CodeConnect, "Could not connect to the local SQL Server."}, CodeConnect},
	}
	// Distinctive raw-text fragments that must never appear in a code/message.
	markers := []string{"SECRET", "secret_db", "secret_table", "SQLHOST", "127.0.0.1", "x509", "connectex", "login failed", "permission was denied", "timeout expired", "untrusted"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got == nil {
				t.Fatal("Classify returned nil for a non-nil error")
			}
			if got.Code != tc.want {
				t.Fatalf("code = %q, want %q", got.Code, tc.want)
			}
			if !fixedCodes[got.Code] {
				t.Fatalf("code %q is outside the fixed set", got.Code)
			}
			surfaced := strings.ToLower(got.Code + " " + got.Message)
			for _, m := range markers {
				if strings.Contains(surfaced, strings.ToLower(m)) {
					t.Fatalf("raw driver text %q surfaced in %q", m, surfaced)
				}
			}
		})
	}
	if Classify(nil) != nil {
		t.Fatal("Classify(nil) must be nil")
	}
	// Same code, different raw text -> identical fixed sentence.
	a := Classify(errors.New("Login failed for user 'one'."))
	b := Classify(errors.New("Login failed for user 'two'."))
	if a.Message != b.Message {
		t.Fatalf("messages differ for the same code: %q vs %q", a.Message, b.Message)
	}
}
