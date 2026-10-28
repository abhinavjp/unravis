package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/sqlconn"
)

type xorProtector struct{}

func (xorProtector) Protect(v []byte) ([]byte, error) {
	out := append([]byte("sealed:"), v...)
	for i := len("sealed:"); i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}

func (xorProtector) Unprotect(v []byte) ([]byte, error) {
	if !bytes.HasPrefix(v, []byte("sealed:")) {
		return nil, errors.New("bad")
	}
	out := append([]byte(nil), v[len("sealed:"):]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

func testDeps(l *keeper.MemoryListener) deps {
	return deps{
		getenv:    func(string) string { return "" },
		protector: func() (sealedfile.Protector, error) { return xorProtector{}, nil },
		listen:    func(string) (keeper.Listener, error) { return l, nil },
		runner:    func() keeper.Runner { return keeper.NewRealRunner() },
		trust:     func(string) error { return nil },
	}
}

// The keeper makes the same installer-record check as Unravis before it opens anything in the shared folder: a folder moved in,
// copied or replaced, or one with no installer record, stops it; a folder named by the operator is not checked.
func TestKeeperChecksTheSharedFolder(t *testing.T) {
	dir := filepath.Join(`C:\ProgramData`, "Unravis", "keeper")
	var asked []string
	d := testDeps(nil)
	d.trust = func(root string) error {
		asked = append(asked, root)
		return errors.New(root + ` is not the folder the installer made (it was moved, copied or replaced)`)
	}
	if err := checkRoot(d, dir); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted folder: %v", err)
	}
	if len(asked) != 1 || asked[0] != filepath.Join(`C:\ProgramData`, "Unravis") {
		t.Fatalf("checked %q", asked)
	}
	d.trust = func(string) error { return datadir.ErrNotShared }
	if err := checkRoot(d, dir); err == nil || !strings.Contains(err.Error(), "installer makes it") {
		t.Fatalf("missing folder: %v", err)
	}
	d.trust = nil
	if err := checkRoot(d, dir); err == nil {
		t.Fatal("no check must not mean trusted")
	}
	d.trust = func(string) error { return errors.New("refused") }
	d.listen = func(string) (keeper.Listener, error) { t.Fatal("listened on an untrusted folder"); return nil, nil }
	var out, errb bytes.Buffer
	if c := run([]string{"console"}, &out, &errb, d); c != exitFail || !strings.Contains(errb.String(), "did not start") {
		t.Fatalf("console: %d %q", c, errb.String())
	}
	for _, v := range []string{datadir.EnvKeeperDir, datadir.EnvDataDir} {
		v := v
		d.getenv = func(k string) string {
			if k == v {
				return `D:\dev\keeper`
			}
			return ""
		}
		if err := checkRoot(d, dir); err != nil {
			t.Fatalf("%s set: %v", v, err)
		}
	}
	d = testDeps(nil)
	if err := checkRoot(d, dir); err != nil {
		t.Fatalf("trusted: %v", err)
	}
}

func TestServeKeepsSecretsSealedInItsOwnFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keeper")
	l := keeper.NewMemoryListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, testDeps(l), dir, `\\.\pipe\x`, nil) }()

	c := keeper.NewClient(l.Dialer(keeper.Caller{SID: "S-1-5-21-test"}))
	deadline := time.Now().Add(3 * time.Second)
	for c.Ping(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the keeper did not answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	const secret = "never-in-clear-SECRET"
	rec, err := keeper.NewSQLStore(c).Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "u", Password: secret, RememberPassword: true})
	if err != nil || !rec.HasPassword {
		t.Fatalf("save: %+v %v", rec, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	f := filesIn(dir)
	raw, err := os.ReadFile(f.SQL)
	if err != nil || bytes.Contains(raw, []byte(secret)) || !bytes.HasPrefix(raw, []byte("sealed:")) {
		t.Fatalf("the saved file must be sealed (%v)", err)
	}
	log, err := os.ReadFile(f.Log)
	if err != nil || !strings.Contains(string(log), `"op":"sql.save"`) || strings.Contains(string(log), secret) {
		t.Fatalf("audit log: %q %v", log, err)
	}
}

func TestServeStopsWhenSealingIsMissing(t *testing.T) {
	d := testDeps(keeper.NewMemoryListener())
	d.protector = func() (sealedfile.Protector, error) { return nil, sealedfile.ErrUnavailable }
	if err := serve(context.Background(), d, t.TempDir(), "p", nil); err == nil || !strings.Contains(err.Error(), "sealing") {
		t.Fatalf("got %v", err)
	}
	if err := serve(context.Background(), d, "", "p", nil); err == nil {
		t.Fatal("no folder must fail")
	}
}

func TestCommandLine(t *testing.T) {
	var out, errb bytes.Buffer
	if c := run(nil, &out, &errb, testDeps(nil)); c != exitUsage {
		t.Fatalf("no args: %d", c)
	}
	if c := run([]string{"install"}, &out, &errb, testDeps(nil)); c != exitUsage || !strings.Contains(errb.String(), "never installs") {
		t.Fatalf("install is not a command: %d %q", c, errb.String())
	}
	out.Reset()
	if c := run([]string{"version"}, &out, &errb, testDeps(nil)); c != exitOK || strings.TrimSpace(out.String()) != version {
		t.Fatalf("version: %d %q", c, out.String())
	}
}
