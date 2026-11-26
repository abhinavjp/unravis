package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
