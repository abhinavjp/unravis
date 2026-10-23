package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

type testProtector struct{}

func (testProtector) Protect(value []byte) ([]byte, error) {
	out := append([]byte("fixture-encrypted:"), value...)
	for i := len("fixture-encrypted:"); i < len(out); i++ {
		out[i] ^= 0xa5
	}
	return out, nil
}
func (testProtector) Unprotect(value []byte) ([]byte, error) {
	prefix := []byte("fixture-encrypted:")
	if !bytes.HasPrefix(value, prefix) {
		return nil, errors.New("invalid test ciphertext")
	}
	out := append([]byte(nil), bytes.TrimPrefix(value, prefix)...)
	for i := range out {
		out[i] ^= 0xa5
	}
	return out, nil
}

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "connections.dat")
	service, err := New(path, testProtector{})
	if err != nil {
		t.Fatal(err)
	}
	return service, path
}

func testConnection(uri string) audit.ConnectionInput {
	return audit.ConnectionInput{URI: uri, Username: "reader", AuthSource: "admin", TLSMode: "system", CACertificate: ""}
}

func TestPasswordIsEncryptedOnDiskAndNeverInPublicRecent(t *testing.T) {
	service, path := testService(t)
	input := testConnection("mongodb://db.example")
	input.Password = "secret-that-must-not-leak"
	saved, err := service.Remember("production", input, "audit", true)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.HasCredentials {
		t.Fatal("saved password was not reflected in public metadata")
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(input.Password)) {
		t.Fatalf("password leaked in public recent: %s", encoded)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(input.Password)) || !bytes.HasPrefix(stored, []byte("fixture-encrypted:")) {
		t.Fatalf("vault did not protect the serialized payload: %q", stored[:min(len(stored), 80)])
	}

	restarted, _ := New(path, testProtector{})
	items, err := restarted.List()
	if err != nil || len(items) != 1 || items[0].ID != saved.ID {
		t.Fatalf("restart list=%#v err=%v", items, err)
	}
	resolved, err := restarted.Resolve(audit.ConnectionInput{URI: saved.URI, Username: saved.Username, AuthSource: saved.AuthSource, TLSMode: saved.TLSMode, CACertificate: saved.CACertificate, SavedConnectionID: saved.ID})
	if err != nil || resolved.Password != input.Password || resolved.SavedConnectionID != "" {
		t.Fatalf("resolve=%#v err=%v", resolved, err)
	}
	resolved, err = restarted.Resolve(audit.ConnectionInput{URI: saved.URI, Username: saved.Username, Password: "entered", AuthSource: saved.AuthSource, TLSMode: saved.TLSMode, CACertificate: saved.CACertificate, SavedConnectionID: saved.ID})
	if err != nil || resolved.Password != "entered" {
		t.Fatalf("explicit password did not take precedence: %#v, %v", resolved, err)
	}
}

func TestResolveRejectsChangedAddressAndNeverPersistsURIUserInfo(t *testing.T) {
	service, _ := testService(t)
	input := testConnection("mongodb://db.example")
	saved, err := service.Remember("db", input, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	changed := audit.ConnectionInput{URI: "mongodb://other.example", Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, SavedConnectionID: saved.ID}
	if _, err := service.Resolve(changed); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("changed address resolve error=%v", err)
	}
	input.URI = "mongodb://reader:password@db.example"
	if _, err := service.Remember("db", input, "audit", true); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("URI userinfo accepted: %v", err)
	}
}

func TestRememberRejectsSecretBearingURIOptions(t *testing.T) {
	service, path := testService(t)
	for _, uri := range []string{
		"mongodb://db.example/?tlsCertificateKeyFilePassword=private",
		"mongodb://db.example/?sslClientCertificateKeyPassword=private",
		"mongodb://db.example/?proxyPassword=private",
		"mongodb://db.example/?password=private",
		"mongodb://db.example/?authMechanismProperties=AWS_SESSION_TOKEN:private",
		"mongodb://db.example/?authMechanismProperties=CUSTOM_SECRET:private",
		"mongodb://db.example/?authMechanismProperties=service_password:private",
	} {
		input := testConnection(uri)
		if _, err := service.Remember("db", input, "audit", false); !errors.Is(err, audit.ErrInvalidInput) {
			t.Errorf("secret-bearing URI was accepted: %s (err=%v)", uri, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected secrets caused a vault file to be written: %v", err)
	}
}

func TestExistingSecretBearingURIRecordFailsClosed(t *testing.T) {
	service, path := testService(t)
	malicious := vault{Version: 1, Records: []storedRecord{{Recent: Recent{
		ID: "bad-record", Label: "db", URI: "mongodb://db.example/?authMechanismProperties=AWS_SESSION_TOKEN:private",
		Collection: "audit", LastUsedAt: time.Now().UTC(),
	}}}}
	plain, err := json.Marshal(malicious)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := (testProtector{}).Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("secret-bearing saved URI was returned: %v", err)
	}
}

func TestBareMongoHostIsPreservedAndCanResolveSavedPassword(t *testing.T) {
	service, _ := testService(t)
	input := testConnection("db.example:27017")
	input.Password = "secret"
	saved, err := service.Remember("db", input, "audit", true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.URI != input.URI {
		t.Fatalf("bare host changed during save: got %q want %q", saved.URI, input.URI)
	}
	resolved, err := service.Resolve(audit.ConnectionInput{URI: input.URI, Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, SavedConnectionID: saved.ID})
	if err != nil || resolved.Password != input.Password {
		t.Fatalf("bare-host resolve=%#v err=%v", resolved, err)
	}
}

func TestUncheckingPasswordReplacesSavedSecret(t *testing.T) {
	service, _ := testService(t)
	input := testConnection("mongodb://db.example")
	input.Password = "old-secret"
	first, err := service.Remember("db", input, "audit", true)
	if err != nil {
		t.Fatal(err)
	}
	input.Password = ""
	input.SavedConnectionID = first.ID
	resolved, err := service.Resolve(input)
	if err != nil || resolved.Password != "old-secret" {
		t.Fatalf("pre-uncheck resolve=%#v err=%v", resolved, err)
	}
	second, err := service.Remember("db renamed", resolved, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.HasCredentials {
		t.Fatalf("resave did not replace prior metadata: %#v %#v", first, second)
	}
	public, err := service.List()
	if err != nil || len(public) != 1 || public[0].HasCredentials {
		t.Fatalf("public history still reports a disk credential: %#v %v", public, err)
	}
	v, err := service.load()
	if err != nil || len(v.Records) != 1 || v.Records[0].Password != "" {
		t.Fatalf("password remained in the encrypted vault: %#v %v", v, err)
	}
	resolved, err = service.Resolve(audit.ConnectionInput{URI: input.URI, Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, CACertificate: input.CACertificate, SavedConnectionID: second.ID})
	if err != nil || resolved.Password != "old-secret" {
		t.Fatalf("same-session credential fallback failed: %#v %v", resolved, err)
	}
	sameSessionResolved := resolved
	restarted, _ := New(service.path, testProtector{})
	resolved, err = restarted.Resolve(audit.ConnectionInput{URI: input.URI, Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, CACertificate: input.CACertificate, SavedConnectionID: second.ID})
	if err != nil || resolved.Password != "" {
		t.Fatalf("restart unexpectedly restored a forgotten password: %#v %v", resolved, err)
	}
	changed := audit.ConnectionInput{URI: "mongodb://other.example", Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, SavedConnectionID: second.ID}
	if _, err := service.Resolve(changed); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("session cache ignored changed address: %v", err)
	}
	if err := service.Delete(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.sessionSecrets[second.ID]; ok {
		t.Fatal("forget left the session credential in memory")
	}
	if _, err := service.Resolve(audit.ConnectionInput{URI: input.URI, Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, SavedConnectionID: second.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted recent still resolved: %v", err)
	}
	staleRemember := sameSessionResolved
	staleRemember.SavedConnectionID = second.ID
	if _, err := service.Remember("db", staleRemember, "audit", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remember recreated a forgotten connection after a test race: %v", err)
	}
	items, err := service.List()
	if err != nil || len(items) != 0 {
		t.Fatalf("forgotten connection was recreated: %#v %v", items, err)
	}
}

func TestHistoryIsBoundedAndSupportsDelete(t *testing.T) {
	service, path := testService(t)
	var latest Recent
	for i := 0; i < MaxRecords+3; i++ {
		input := testConnection("mongodb://db" + string(rune('a'+i)) + ".example")
		input.Password = "session-secret"
		var err error
		latest, err = service.Remember("database", input, "audit", false)
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := service.List()
	if err != nil || len(items) != MaxRecords {
		t.Fatalf("records=%d err=%v", len(items), err)
	}
	if len(service.sessionSecrets) != MaxRecords {
		t.Fatalf("session credential cache has %d records, want cap %d", len(service.sessionSecrets), MaxRecords)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxFileSize {
		t.Fatalf("vault size=%v err=%v", info, err)
	}
	if err := service.Delete(latest.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(latest.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error=%v", err)
	}
}

func TestCorruptHistoryFailsClosedAndIsNeverOverwritten(t *testing.T) {
	service, path := testService(t)
	corrupt := []byte("ciphertext-from-different-or-corrupt-vault")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("list error=%v", err)
	}
	input := testConnection("mongodb://db.example")
	if _, err := service.Remember("db", input, "audit", false); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("remember error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, corrupt) {
		t.Fatalf("corrupt vault changed: %q err=%v", after, err)
	}
}

func TestOversizedVaultIsRejectedWithoutReadingPastCapOrOverwriting(t *testing.T) {
	service, path := testService(t)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxFileSize+32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversized vault error=%v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != maxFileSize+32 {
		t.Fatalf("oversized vault changed: size=%v err=%v", info, err)
	}
}

func TestDeduplicationUsesAddressAndNotLabel(t *testing.T) {
	service, _ := testService(t)
	input := testConnection("mongodb://db.example")
	first, err := service.Remember("old name", input, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Remember("new name", input, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.List()
	if err != nil || len(items) != 1 || first.ID != second.ID || items[0].Label != "new name" {
		t.Fatalf("dedupe result=%#v err=%v", items, err)
	}
	if strings.Contains(items[0].URI, "@") {
		t.Fatal("URI unexpectedly contains credentials")
	}
}
