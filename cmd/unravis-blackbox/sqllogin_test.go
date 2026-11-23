package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sealedfile"
)

const (
	loginText = "Pa55;w0rd'\"x"
	loginDoc  = `{"server":"sql01\\PROD","user":"bb_reader","password":"Pa55;w0rd'\"x"}`
)

// loginRig is a small, platform-neutral rig: its own settings file under a
// temp folder, an administrator environment and a fake sealer. It needs no
// installed service, so it runs the same on every platform.
type loginRigT struct {
	d      deps
	admin  *memEnv
	cfg    string
	folder string
	dir    string
}

func loginRig(t *testing.T) (*loginRigT, string) {
	t.Helper()
	root := t.TempDir()
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(root, "rec")
	cfg.LogPaths = []string{filepath.Join(root, "logs")}
	cfgPath := filepath.Join(root, "blackbox.json")
	if err := blackbox.SaveConfig(cfgPath, cfg, false); err != nil {
		t.Fatalf("settings: %v", err)
	}
	env := &memEnv{admin: true}
	g := &loginRigT{
		admin: env, cfg: cfgPath, folder: cfg.Folder,
		d: deps{
			mgr:    &blackbox.Manager{Svc: &memSvc{}, Env: env, Disk: bigDisk{}},
			exeDir: func() string { return root },
			sqlMgr: &blackbox.SQLManager{Env: env},
			sealer: func() (sealedfile.Protector, error) { return xorSeal{}, nil },
		},
	}
	g.dir = blackbox.SQLDataDir(cfg.Folder)
	if err := os.MkdirAll(g.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return g, g.dir
}

func (g *loginRigT) do(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append(args, "--config", g.cfg), &out, &errb, g.d)
	return code, out.String(), errb.String()
}

func (g *loginRigT) doIn(stdin string, args ...string) (int, string, string) {
	g.d.stdin = strings.NewReader(stdin)
	return g.do(args...)
}

func noLoginLeak(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		for _, bad := range []string{loginText, "bb_reader", "sql01"} {
			if strings.Contains(o, bad) {
				t.Errorf("output leaked %q: %q", bad, o)
			}
		}
	}
}

func TestSetSQLLoginFromStdinSealsItAndPrintsNothing(t *testing.T) {
	g, dir := loginRig(t)
	c, out, errs := g.doIn(loginDoc, "set-sql-login", "--login-stdin", "--json")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	noLoginLeak(t, out, errs)
	raw, _ := os.ReadFile(filepath.Join(dir, bbsql.LoginFile)) //nolint:gosec // test folder
	if len(raw) == 0 || bytes.Contains(raw, []byte(loginText)) {
		t.Fatal("login missing or stored as plain text")
	}
	s, found, err := bbsql.LoadLogin(dir, xorSeal{})
	if err != nil || !found || s.Login.User != "bb_reader" || s.Server != "sql01\\PROD" {
		t.Fatalf("not readable back: %v", err)
	}
	// It is not in the settings file.
	cfgBytes, _ := os.ReadFile(g.cfg) //nolint:gosec // test file
	if bytes.Contains(cfgBytes, []byte("bb_reader")) || bytes.Contains(cfgBytes, []byte(loginText)) {
		t.Fatal("login reached blackbox.json")
	}
	// clear
	if c, out, errs := g.do("clear-sql-login", "--json"); c != 0 || strings.Contains(out+errs, "bb_reader") {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if bbsql.HasLogin(dir) {
		t.Fatal("login still there")
	}
}

func TestSetSQLLoginFromFileNeedsTheHashAndWipesTheFile(t *testing.T) {
	g, dir := loginRig(t)
	f := filepath.Join(t.TempDir(), "login.tmp")
	sum := sha256.Sum256([]byte(loginDoc))
	good := hex.EncodeToString(sum[:])

	// No hash: refused, nothing saved.
	if err := os.WriteFile(f, []byte(loginDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, out, errs := g.do("set-sql-login", "--login-file", f, "--json"); c == 0 || bbsql.HasLogin(dir) {
		t.Fatalf("saved without a hash: %d %s %s", c, out, errs)
	}
	// Wrong hash: refused, file gone, nothing saved.
	if err := os.WriteFile(f, []byte(loginDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	c, out, errs := g.do("set-sql-login", "--login-file", f, "--login-sha256", strings.Repeat("0", 64), "--json")
	if c == 0 || bbsql.HasLogin(dir) {
		t.Fatalf("saved with a wrong hash: %d %s", c, out)
	}
	noLoginLeak(t, out, errs)
	// Good hash.
	if err := os.WriteFile(f, []byte(loginDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, out, errs := g.do("set-sql-login", "--login-file", f, "--login-sha256", good, "--json"); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	} else {
		noLoginLeak(t, out, errs)
	}
	if _, err := os.Stat(f); err == nil {
		t.Fatal("the login file was not removed")
	}
	if !bbsql.HasLogin(dir) {
		t.Fatal("not saved")
	}
}

func TestSetSQLLoginRefusals(t *testing.T) {
	g, dir := loginRig(t)
	cases := map[string]string{
		"unknown field": `{"server":"sql01","user":"u","password":"p","extra":1}`,
		"bad server":    `{"server":"sql01;x=1","user":"u","password":"p"}`,
		"no password":   `{"server":"sql01","user":"u"}`,
		"not json":      `Pa55;w0rd`,
		"too big":       `{"server":"sql01","user":"u","password":"` + strings.Repeat("a", maxLoginInput) + `"}`,
	}
	for name, doc := range cases {
		c, out, errs := g.doIn(doc, "set-sql-login", "--login-stdin", "--json")
		if c == 0 || bbsql.HasLogin(dir) {
			t.Errorf("%s: saved (%d)", name, c)
		}
		if strings.Contains(out+errs, "Pa55") {
			t.Errorf("%s: echoed the input", name)
		}
	}
	// Needs admin.
	g.admin.admin = false
	if c, _, _ := g.doIn(loginDoc, "set-sql-login", "--login-stdin", "--json"); c == 0 || bbsql.HasLogin(dir) {
		t.Error("saved without administrator rights")
	}
	if c, _, _ := g.do("clear-sql-login", "--json"); c == 0 {
		t.Error("cleared without administrator rights")
	}
	g.admin.admin = true
	// Neither source, or both.
	if c, _, _ := g.do("set-sql-login", "--json"); c == 0 {
		t.Error("ran with no source")
	}
	// No SQL folder yet.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	c, out, _ := g.doIn(loginDoc, "set-sql-login", "--login-stdin", "--json")
	if c == 0 || !strings.Contains(out, reasonLoginNoSQL) {
		t.Errorf("no SQL folder: %d %s", c, out)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the command created the folder")
	}
}
