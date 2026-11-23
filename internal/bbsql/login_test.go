package bbsql

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/go-mssqldb/msdsn"
)

// xorSealer is a test stand-in for DPAPI: reversible, not plain text.
type xorSealer struct{ fail bool }

func (x xorSealer) Protect(b []byte) ([]byte, error) {
	if x.fail {
		return nil, errors.New("seal broke")
	}
	o := make([]byte, len(b))
	for i := range b {
		o[i] = b[i] ^ 0x5a
	}
	return o, nil
}
func (x xorSealer) Unprotect(b []byte) ([]byte, error) { return x.Protect(b) }

const (
	tUser = "bb_reader"
	tPass = "S3cret;=@'\"/ pw%"
)

func sampleLogin() StoredLogin {
	return StoredLogin{Server: "sql01.corp.example\\PROD", Login: Login{tUser, tPass}}
}

func TestLoginNeverPrints(t *testing.T) {
	l := Login{tUser, tPass}
	s := sampleLogin()
	for _, v := range []any{l, &l, s, &s} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
			out := fmt.Sprintf(f, v)
			if strings.Contains(out, tPass) || strings.Contains(out, tUser) {
				t.Fatalf("%s leaked: %q", f, out)
			}
		}
	}
}

func TestLoginValidate(t *testing.T) {
	bad := []Login{{"", "x"}, {"u", ""}, {strings.Repeat("a", 129), "x"}, {"u", strings.Repeat("a", 129)}, {"u\n", "x"}, {"u", "p\x00"}}
	for _, l := range bad {
		if l.Validate() == nil {
			t.Errorf("accepted %d/%d", len(l.User), len(l.Password))
		}
	}
	if (Login{tUser, tPass}).Validate() != nil {
		t.Error("refused a good login")
	}
}

func TestStoreRoundTripAndNoPlainText(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLogin(dir, sampleLogin(), xorSealer{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, LoginFile))
	if bytes.Contains(raw, []byte(tPass)) || bytes.Contains(raw, []byte(tUser)) {
		t.Fatal("login is in the file as plain text")
	}
	got, found, err := LoadLogin(dir, xorSealer{})
	if err != nil || !found || got != sampleLogin() {
		t.Fatalf("round trip: %v %v", found, err)
	}
	if !HasLogin(dir) {
		t.Error("HasLogin false")
	}
	if err := RemoveLogin(dir); err != nil || HasLogin(dir) {
		t.Fatal("remove failed")
	}
	if _, found, err := LoadLogin(dir, xorSealer{}); found || err != nil {
		t.Fatalf("after remove: %v %v", found, err)
	}
	if err := RemoveLogin(dir); err != nil {
		t.Fatal("removing nothing must be fine")
	}
}

func TestStoreRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLogin(filepath.Join(dir, "missing"), sampleLogin(), xorSealer{}); err == nil {
		t.Error("created a folder instead of refusing")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); err == nil {
		t.Error("folder was created")
	}
	if err := SaveLogin(dir, sampleLogin(), nil); err == nil {
		t.Error("saved without a sealer")
	}
	err := SaveLogin(dir, sampleLogin(), xorSealer{fail: true})
	if err == nil || strings.Contains(err.Error(), "seal broke") {
		t.Errorf("seal failure: %v", err)
	}
	if HasLogin(dir) {
		t.Error("a failed save left a file")
	}
	if SaveLogin(dir, StoredLogin{Server: "bad;host", Login: Login{"u", "p"}}, xorSealer{}) == nil {
		t.Error("saved a bad server")
	}
	// Damaged, wrong-version, oversized files are errors, not "none saved".
	p := filepath.Join(dir, LoginFile)
	for name, data := range map[string][]byte{"junk": []byte("junk"), "big": bytes.Repeat([]byte("a"), maxLoginFile+1), "empty-json": {0x5a ^ '{', 0x5a ^ '}'}} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, found, err := LoadLogin(dir, xorSealer{})
		var be *Error
		if !found || !errors.As(err, &be) || be.Code != CodeLoginUnreadable {
			t.Errorf("%s: found=%v err=%v", name, found, err)
		}
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadLogin(dir, nil); err != nil {
		t.Errorf("no file and no sealer: %v", err)
	}
}

func TestCheckTarget(t *testing.T) {
	l := &Login{"u", "p"}
	cases := []struct {
		server string
		login  *Login
		ok     bool
	}{
		{"(local)", nil, true},
		{"sql01", nil, false},
		{"sql01", l, true},
		{"sql01\\PROD", l, true},
		{"sql01,1433", l, true},
		{"sql01:1433", l, true},
		{"10.1.2.3", l, true},
		{"[::1]", l, false},
		{"sql01;Database=x", l, false},
		{"sql01,0", l, false},
		{"sql01,99999", l, false},
		{"sql01\\a,1433", l, false},
		{"sql01/x", l, false},
		{"user@sql01", l, false},
		{"", l, false},
		{"lpc:sql01", l, false},
	}
	for _, c := range cases {
		err := CheckTarget(ConnConfig{Server: c.server, Login: c.login})
		if (err == nil) != c.ok {
			t.Errorf("%q login=%v: err=%v want ok=%v", c.server, c.login != nil, err, c.ok)
		}
	}
}

func TestDSNWithLogin(t *testing.T) {
	cfg := sampleLogin().Conn(ConnConfig{})
	dsn, err := connDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mc, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if mc.User != tUser || mc.Password != tPass {
		t.Error("login did not survive the DSN (escaping)")
	}
	if mc.Host != "sql01.corp.example" || mc.Instance != "PROD" {
		t.Errorf("target: %q %q", mc.Host, mc.Instance)
	}
	if mc.TLSConfig == nil || mc.TLSConfig.InsecureSkipVerify {
		t.Error("TLS must be on and verified by default")
	}
	cfg.TrustServerCertificate = true
	dsn, _ = connDSN(cfg)
	mc, _ = msdsn.Parse(dsn)
	if mc.TLSConfig == nil || !mc.TLSConfig.InsecureSkipVerify {
		t.Error("trust option not applied")
	}
	// A trust anchor wins over the trust option.
	cfg.ServerCertificate = filepath.Join(os.TempDir(), "x.pem")
	dsn, _ = connDSN(cfg)
	if strings.Contains(dsn, "trustservercertificate") {
		t.Error("trust option set beside an anchor")
	}
	// Port form.
	port, _ := connDSN(StoredLogin{Server: "sql01,1444", Login: Login{"u", "p"}}.Conn(ConnConfig{}))
	if !strings.Contains(port, "sql01:1444") {
		t.Errorf("port lost: %s", strings.SplitN(port, "@", 2)[1])
	}
	// The trust option does nothing without a login.
	noLogin, _ := connDSN(ConnConfig{Server: "(local)", TrustServerCertificate: true})
	if strings.Contains(noLogin, "trustservercertificate") {
		t.Error("trust option honoured without a login")
	}
}

func TestLocalLoginKeepsLoopbackOnly(t *testing.T) {
	cfg := StoredLogin{Server: "(local)", Login: Login{"u", "p"}}.Conn(ConnConfig{})
	if remoteWithLogin(cfg) {
		t.Error("a local server with a login must keep the loopback assert")
	}
	if !remoteWithLogin(sampleLogin().Conn(ConnConfig{})) {
		t.Error("a remote server must use the remote dial rules")
	}
	if remoteWithLogin(ConnConfig{Server: "sql01"}) {
		t.Error("no login means never remote")
	}
}

func TestRemoteDialRules(t *testing.T) {
	old := lookupIP
	defer func() { lookupIP = old }()
	for _, c := range []struct {
		ip     string
		remote bool
		ok     bool
	}{
		{"10.1.2.3", true, true},
		{"0.0.0.0", true, false},
		{"224.0.0.1", true, false},
		{"10.1.2.3", false, false},
	} {
		d := loopbackDialer{remote: c.remote}
		_, err := d.DialContext(t.Context(), "tcp", c.ip+":1")
		// Allowed addresses fail to connect (nothing listens); refused ones
		// fail with errNotLoopback. That is the only distinction this test needs.
		if c.ok != (err != errNotLoopback) {
			t.Errorf("%s remote=%v: %v", c.ip, c.remote, err)
		}
	}
}

func TestOpenRefusesRemoteWithoutLoginAndBadLogin(t *testing.T) {
	if _, err := OpenContext(t.Context(), ConnConfig{Server: "sql01"}); err == nil {
		t.Fatal("remote without a login opened")
	}
	_, err := OpenContext(t.Context(), ConnConfig{Server: "sql01", Login: &Login{"", ""}})
	var be *Error
	if !errors.As(err, &be) || be.Code != CodeLoginInvalid || strings.Contains(err.Error(), "sql01") {
		t.Fatalf("bad login: %v", err)
	}
}
