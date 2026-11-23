package sqlaccess

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/dbhealth"
)

func TestStoredLoginFor(t *testing.T) {
	ok, err := StoredLoginFor(dbhealth.SQLTarget{Server: " tcp:db01\\PROD ", Auth: "sql", Username: "u", Password: "p", Encrypt: "mandatory", TrustServerCertificate: true})
	if err != nil || ok.Server != "db01\\PROD" || ok.Login.User != "u" || !ok.TrustServerCertificate {
		t.Fatalf("%v", err)
	}
	cases := []struct {
		name string
		t    dbhealth.SQLTarget
		want error
	}{
		{"windows", dbhealth.SQLTarget{Server: "db01", Auth: "windows"}, ErrNotSQLLogin},
		{"optional encrypt", dbhealth.SQLTarget{Server: "db01", Auth: "sql", Username: "u", Password: "p", Encrypt: "optional"}, ErrEncryptOptional},
		{"bad server", dbhealth.SQLTarget{Server: "db01;x=1", Auth: "sql", Username: "u", Password: "p", Encrypt: "mandatory"}, ErrLoginShape},
		{"no password", dbhealth.SQLTarget{Server: "db01", Auth: "sql", Username: "u", Encrypt: "mandatory"}, ErrLoginShape},
		{"domain user", dbhealth.SQLTarget{Server: "db01", Auth: "sql", Username: "CORP\\u", Password: "p", Encrypt: "mandatory"}, ErrLoginShape},
	}
	for _, c := range cases {
		_, err := StoredLoginFor(c.t)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "db01") || strings.Contains(err.Error(), "CORP")) {
			t.Errorf("%s: the error names the server or login: %v", c.name, err)
		}
	}
}
