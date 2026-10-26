package dbhealth

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, in SQLTarget) *url.URL {
	t.Helper()
	s, err := ConnectionURL(in)
	if err != nil {
		t.Fatalf("%+v: %v", in, err)
	}
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestConnectionURLShapes(t *testing.T) {
	u := mustURL(t, SQLTarget{Server: "db01,1444", Auth: "sql", Username: "sa", Password: "p@ss;w=rd", Database: "Audit", Encrypt: "mandatory"})
	if u.Host != "db01:1444" || u.User.Username() != "sa" {
		t.Fatalf("%s", u)
	}
	if pw, _ := u.User.Password(); pw != "p@ss;w=rd" {
		t.Fatal("password must survive escaping")
	}
	q := u.Query()
	if q.Get("database") != "Audit" || q.Get("encrypt") != "true" || q.Get("TrustServerCertificate") != "false" {
		t.Fatalf("%v", q)
	}

	u = mustURL(t, SQLTarget{Server: `HOST\SQLEXPRESS`, Auth: "windows", Encrypt: "optional", TrustServerCertificate: true})
	if u.Host != "HOST" || u.Path != "/SQLEXPRESS" || u.User != nil {
		t.Fatalf("named instance / windows: %s", u)
	}
	if u.Query().Get("encrypt") != "false" || u.Query().Get("TrustServerCertificate") != "true" {
		t.Fatalf("%v", u.Query())
	}

	u = mustURL(t, SQLTarget{Server: `HOST\INST:1433`, Auth: "windows", Encrypt: "strict"})
	if u.Host != "HOST" || u.Query().Get("encrypt") != "strict" {
		t.Fatalf("saved default port next to an instance should be dropped: %s", u)
	}
	u = mustURL(t, SQLTarget{Server: "tcp:db01:1433", Auth: "windows"})
	if u.Host != "db01:1433" {
		t.Fatalf("%s", u)
	}
}

func TestConnectionURLRefusesBadInput(t *testing.T) {
	for _, in := range []SQLTarget{
		{Server: "", Auth: "windows"},
		{Server: "a;b", Auth: "windows"},
		{Server: "a b", Auth: "windows"},
		{Server: "host,99999", Auth: "windows"},
		{Server: "host,abc", Auth: "windows"},
		{Server: "host", Auth: "sql"},
		{Server: "host", Auth: "kerberos"},
	} {
		if _, err := ConnectionURL(in); err == nil {
			t.Errorf("%+v should be refused", in)
		}
	}
}

func TestClassifyConnectNeverEchoesDriverText(t *testing.T) {
	secret := "PRODSQL01 user svc_audit"
	for msg, code := range map[string]string{
		"Login failed for user 'svc_audit'. " + secret:         "login_failed",
		"Cannot open database \"Audit\" requested by the login": "database",
		"tls: failed to verify certificate: x509: " + secret:    "certificate",
		"dial tcp: lookup PRODSQL01: no such host":              "network",
		"i/o timeout":                                           "timeout",
		"something odd " + secret:                               "failed",
	} {
		e := ClassifyConnect(errors.New(msg))
		if e.Code != code {
			t.Errorf("%q -> %s want %s", msg, e.Code, code)
		}
		if strings.Contains(e.Message, "PRODSQL01") || strings.Contains(e.Message, "svc_audit") {
			t.Errorf("leaked: %s", e.Message)
		}
	}
}
