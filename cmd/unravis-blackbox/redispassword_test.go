package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/bbredis"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sealedfile"
)

type testSealer struct{}

func (testSealer) Protect(b []byte) ([]byte, error)   { return xorAll(b), nil }
func (testSealer) Unprotect(b []byte) ([]byte, error) { return xorAll(b), nil }
func xorAll(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x33
	}
	return out
}

func redisRig(t *testing.T, input string) (*rig, string, string) {
	t.Helper()
	r := newRig(t)
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	cfg.LogPaths = []string{r.dir}
	if err := os.MkdirAll(cfg.Folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "bb.json")
	if err := blackbox.SaveConfig(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	r.d.sealer = func() (sealedfile.Protector, error) { return testSealer{}, nil }
	r.d.stdin = strings.NewReader(input)
	r.d.redisCheck = func(int, string) error { return nil }
	return r, path, blackbox.RedisDataDir(cfg.Folder)
}

func TestSetRedisPasswordSealsItAndNeverPrintsIt(t *testing.T) {
	const pw = "hunter2-secret"
	r, path, dir := redisRig(t, pw+"\r\n")
	var checked string
	r.d.redisCheck = func(port int, p string) error { checked = p; return nil }
	for _, asJSON := range []bool{true, false} {
		r.d.stdin = strings.NewReader(pw + "\n")
		args := []string{"set-redis-password", "--config", path}
		if asJSON {
			args = append(args, "--json")
		}
		c, out, errs := r.do(args...)
		if c != 0 {
			t.Fatalf("%d %s %s", c, out, errs)
		}
		if strings.Contains(out+errs, pw) {
			t.Fatalf("the password was printed: %s %s", out, errs)
		}
	}
	if checked != pw {
		t.Errorf("the password was not tested against Redis first: %q", checked)
	}
	raw, err := os.ReadFile(filepath.Join(dir, bbredis.PasswordFile)) //nolint:gosec // test file
	if err != nil || strings.Contains(string(raw), pw) {
		t.Fatalf("stored file: %v, plain=%v", err, strings.Contains(string(raw), pw))
	}
	got, found, err := bbredis.LoadPassword(dir, testSealer{})
	if err != nil || !found || got != pw {
		t.Fatalf("load: %q %v %v", got, found, err)
	}
	// clear
	if c, _, _ := r.do("clear-redis-password", "--config", path); c != 0 || bbredis.HasPassword(dir) {
		t.Fatalf("clear: %d", c)
	}
	if c, _, _ := r.do("clear-redis-password", "--config", path); c != 0 {
		t.Fatalf("clearing twice: %d", c)
	}
}

func TestSetRedisPasswordRefusals(t *testing.T) {
	// Not an administrator: refused before anything is read.
	r, path, dir := redisRig(t, "pw\n")
	r.env.admin = false
	if c, out, _ := r.do("set-redis-password", "--config", path, "--json"); c != exitFail || !strings.Contains(out, `"ok":false`) {
		t.Fatalf("non-admin: %d %s", c, out)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a folder was made without administrator rights")
	}
	cases := map[string]struct {
		in     string
		check  error
		reason string
	}{
		"empty":       {"\n", nil, reasonRedisInvalid},
		"long":        {strings.Repeat("a", 300), nil, reasonRedisInvalid},
		"inner break": {"a\nb\n", nil, reasonRedisInvalid},
		"unreachable": {"pw\n", errRedisUnreachable, reasonRedisUnreachable},
		"refused":     {"pw\n", errRedisRefused, reasonRedisRefused},
	}
	for name, tc := range cases {
		r, path, dir := redisRig(t, tc.in)
		r.d.redisCheck = func(int, string) error { return tc.check }
		c, out, _ := r.do("set-redis-password", "--config", path, "--json")
		if c != exitFail || !strings.Contains(out, tc.reason) {
			t.Errorf("%s: %d %s", name, c, out)
		}
		if bbredis.HasPassword(dir) {
			t.Errorf("%s: a password was saved", name)
		}
		if strings.Contains(out, "pw") && !strings.Contains(out, "password") {
			t.Errorf("%s: output carries the password: %s", name, out)
		}
	}
	// A settings file that is not there is "not installed", not a crash.
	r2, _, _ := redisRig(t, "pw\n")
	if c, _, _ := r2.do("set-redis-password", "--config", filepath.Join(r2.dir, "missing.json")); c != exitFail {
		t.Error("a missing settings file did not fail")
	}
	_ = errors.New
}

func TestRedisFolderThatIsAFileIsRefused(t *testing.T) {
	r, path, dir := redisRig(t, "pw\n")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, out, _ := r.do("set-redis-password", "--config", path, "--json"); c != exitFail || !strings.Contains(out, reasonRedisSave) {
		t.Fatalf("%d %s", c, out)
	}
}

func TestRedisPasswordIsNotACommandLineArgument(t *testing.T) {
	r, path, _ := redisRig(t, "pw\n")
	if c, _, _ := r.do("set-redis-password", "--config", path, "extra-secret"); c != exitUsage {
		t.Errorf("a stray argument (a password typed on the command line) was accepted: %d", c)
	}
}

// miniRedis answers PING with the given line and records every command word it receives.
func miniRedis(t *testing.T, pingAnswer string) (port int, words func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var seen []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				authed := false
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil || !strings.HasPrefix(line, "*") {
						return
					}
					n, _ := strconv.Atoi(strings.TrimSpace(line[1 : len(line)-2]))
					var first string
					for i := 0; i < n; i++ {
						l, _ := r.ReadString('\n')
						sz, _ := strconv.Atoi(strings.TrimSpace(l[1:]))
						buf := make([]byte, sz+2)
						if _, err := io.ReadFull(r, buf); err != nil {
							return
						}
						if i == 0 {
							first = strings.ToUpper(string(buf[:sz]))
						}
					}
					mu.Lock()
					seen = append(seen, first)
					mu.Unlock()
					if first == "AUTH" {
						authed = true
					}
					if first == "PING" && !authed {
						_, _ = io.WriteString(c, pingAnswer)
					} else {
						_, _ = io.WriteString(c, "+OK\r\n")
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestRedisCheckSendsThePasswordOnlyWhereItIsAskedForAndTrusted(t *testing.T) {
	// A Redis with no password: nothing is sent, and the owner is told.
	port, words := miniRedis(t, "+PONG\r\n")
	if err := redisCheck(port, "pw"); !errors.Is(err, errRedisNoPassword) {
		t.Fatalf("no-password server: %v", err)
	}
	for _, w := range words() {
		if w == "AUTH" {
			t.Fatal("a password was sent to a Redis that did not ask for one")
		}
	}
	// A Redis that asks, behind a listener that is not trusted: nothing is sent.
	port, words = miniRedis(t, "-NOAUTH Authentication required.\r\n")
	old := bbredis.ListenerCheck
	bbredis.ListenerCheck = func(int) error { return bbredis.ErrUntrustedListener }
	err := redisCheck(port, "pw")
	bbredis.ListenerCheck = old
	if !errors.Is(err, errRedisNotTrusted) {
		t.Fatalf("untrusted: %v", err)
	}
	for _, w := range words() {
		if w == "AUTH" {
			t.Fatal("the password was sent to an untrusted listener")
		}
	}
	// Trusted: it is sent.
	port, words = miniRedis(t, "-NOAUTH x\r\n")
	if err := redisCheck(port, "pw"); err != nil {
		t.Fatal(err)
	}
	if w := words(); len(w) < 2 || w[1] != "AUTH" {
		t.Fatalf("commands %v", w)
	}
}

func TestSQLStatusSaysWhereItStandsAndPrintsTheGrantOnlyWhenNeeded(t *testing.T) {
	r, path, _ := redisRig(t, "")
	svc := &memSvc{}
	r.d.sqlMgr = &blackbox.SQLManager{Svc: svc, Env: r.env}
	c, out, errs := r.do("sql-status", "--config", path, "--json")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if !strings.Contains(out, `"state":"not-installed"`) || strings.Contains(out, "CREATE LOGIN") {
		t.Fatalf("not installed: %s", out)
	}
	svc.state = blackbox.SvcStopped
	c, out, _ = r.do("sql-status", "--config", path)
	if c != 0 || !strings.Contains(out, "enable-sql") || !strings.Contains(out, "CREATE LOGIN") {
		t.Fatalf("stopped: %d %s", c, out)
	}
	if c, _, _ := r.do("sql-status", "--config", filepath.Join(r.dir, "none.json")); c != exitFail {
		t.Error("a missing settings file did not fail")
	}
	if len(svc.calls) != 0 {
		t.Errorf("sql-status changed the service: %v", svc.calls)
	}
}

func installedRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	cfg.LogPaths = []string{r.dir}
	src := filepath.Join(r.dir, "src.json")
	if err := blackbox.SaveConfig(src, cfg, false); err != nil {
		t.Fatal(err)
	}
	if c, out, errs := r.do("install", "--config", src, "--json"); c != 0 {
		t.Fatalf("install: %d %s %s", c, out, errs)
	}
	return r
}

func TestConfigSetValidateHistoryRollbackThroughTheCLI(t *testing.T) {
	r := installedRig(t)
	if c, out, errs := r.do("config", "set", "web.iis", "off", "--json"); c != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("set: %d %s %s", c, out, errs)
	}
	if c, out, errs := r.do("config", "set", "--every", "5m", "--json"); c != exitUsage && c != exitFail {
		t.Fatalf("set with no collector: %d %s %s", c, out, errs)
	}
	if c, out, _ := r.do("config", "set", "web.iis", "--every", "5m", "--json"); c != 0 || !strings.Contains(out, "every 300") {
		t.Fatalf("set --every: %d %s", c, out)
	}
	if c, out, _ := r.do("config", "set", "counters.extra", "add", `\Memory\Available MBytes`, "--json"); c != 0 {
		t.Fatalf("counter add: %d %s", c, out)
	}
	c, out, _ := r.do("config", "history", "--json")
	if c != 0 || !strings.Contains(out, `"verified":true`) || strings.Count(out, `"seq"`) != 3 {
		t.Fatalf("history: %d %s", c, out)
	}
	if c, out, _ := r.do("config", "rollback", "--json"); c != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("rollback: %d %s", c, out)
	}
	// Refusals carry a fixed reason.
	for _, args := range [][]string{
		{"config", "set", "nope.none", "off", "--json"},
		{"config", "set", "host.diskcpu", "off", "--json"},
		{"config", "set", "web.iis", "maybe", "--json"},
		{"config", "set", "counters.extra", "add", `\\server\Process(*)\x`, "--json"},
		{"config", "set", "web.iis", "on", "--every", "7", "--json"},
		{"config", "set", "web.iis", "on", "--every", "soon", "--json"},
	} {
		c, out, _ := r.do(args...)
		if c != exitFail || !strings.Contains(out, `"ok":false`) {
			t.Errorf("%v: %d %s", args, c, out)
		}
	}
	// validate: a good file, a bad one, a missing one. None of them changes anything.
	good := filepath.Join(r.dir, "good.json")
	cfg := blackbox.Default()
	cfg.Name, cfg.Folder, cfg.LogPaths = "x", filepath.Join(r.dir, "rec2"), []string{r.dir}
	if err := blackbox.SaveConfig(good, cfg, false); err != nil {
		t.Fatal(err)
	}
	if c, out, _ := r.do("config", "validate", good, "--json"); c != 0 || !strings.Contains(out, `"valid":true`) {
		t.Errorf("validate good: %d %s", c, out)
	}
	bad := filepath.Join(r.dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"name":"x","collectors":{"nope.none":{"on":false}}}`), 0o600)
	if c, _, _ := r.do("config", "validate", bad); c != exitFail {
		t.Errorf("validate bad: %d", c)
	}
	if c, _, _ := r.do("config", "validate", filepath.Join(r.dir, "missing.json")); c != exitFail {
		t.Errorf("validate missing: %d", c)
	}
	// Not an administrator: set and rollback are refused; history and validate still work.
	r.env.admin = false
	if c, out, _ := r.do("config", "set", "web.iis", "off", "--json"); c != exitFail || !strings.Contains(out, "no-admin") {
		t.Errorf("non-admin set: %d %s", c, out)
	}
	if c, _, _ := r.do("config", "history"); c != 0 {
		t.Errorf("non-admin history: %d", c)
	}
	if c, _, _ := r.do("config"); c != exitUsage {
		t.Errorf("bare config: %d", c)
	}
	if c, _, _ := r.do("config", "run", "ls"); c != exitUsage {
		t.Errorf("unknown config verb: %d", c)
	}
}
