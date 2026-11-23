package blackbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sqlPC(f *fakeElev) *ThisPC {
	return &ThisPC{Elev: f, Program: filepath.Join(os.TempDir(), "unravis-blackbox.exe")}
}

// The login travels in a file; the command line holds only the file's path and hash, never the login.
func TestSetupSQLArgsCarryNoLogin(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"installed":true,"loginSaved":true,"started":true}`}
	file := filepath.Join(t.TempDir(), "login.tmp")
	if err := os.WriteFile(file, []byte(`{"user":"bb_reader","password":"Hunter2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := sqlPC(f).SetupSQL(t.Context(), file, strings.Repeat("a", 64), false, true)
	if err != nil || !out.Installed || !out.LoginSaved || !out.Started {
		t.Fatalf("%+v %v", out, err)
	}
	if len(f.calls) != 1 || !f.calls[0].elevated {
		t.Fatalf("calls %+v", f.calls)
	}
	line := strings.Join(f.calls[0].args, " ")
	for _, bad := range []string{"bb_reader", "Hunter2"} {
		if strings.Contains(line, bad) {
			t.Fatalf("the command line holds the login: %s", line)
		}
	}
	for _, want := range []string{"setup-sql", "--login-file " + file, "--login-sha256 " + strings.Repeat("a", 64), "--enable"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
	// The helper never read the file in this fake: the call still removes it.
	if _, err := os.Stat(file); err == nil {
		t.Error("the login file outlived the call")
	}
}

func TestSetupSQLWindowsSigninAndDeclinedPromptRemoveTheFile(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"loginSaved":false,"grantScript":"GRANT x"}`}
	out, err := sqlPC(f).SetupSQL(t.Context(), "", "", true, false)
	if err != nil || out.LoginSaved || out.GrantScript != "GRANT x" || out.Started {
		t.Fatalf("%+v %v", out, err)
	}
	line := strings.Join(f.calls[0].args, " ")
	if !strings.Contains(line, "--windows-signin") || strings.Contains(line, "--login-file") || strings.Contains(line, "--enable") {
		t.Fatalf("args %s", line)
	}
	// Declined prompt: the file is still gone.
	file := filepath.Join(t.TempDir(), "login.tmp")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &fakeElev{err: ErrDeclined}
	if _, err := sqlPC(d).SetupSQL(t.Context(), file, "h", false, false); err == nil {
		t.Fatal("declined prompt answered ok")
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the login file survived a declined prompt")
	}
}

func TestClearSQLLoginAndTheGuard(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true}`}
	if err := sqlPC(f).ClearSQLLogin(t.Context()); err != nil || len(f.calls) != 1 || f.calls[0].args[0] != "clear-sql-login" || !f.calls[0].elevated {
		t.Fatalf("%v %+v", err, f.calls)
	}
	if !OpSQLSetup.NeedsGuard() || OpClearSQLLogin.NeedsGuard() {
		t.Error("setup-sql must be guarded (it carries a secret), clear need not be")
	}
}
