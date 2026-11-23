package blackbox

import (
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/bbsql"
)

// A saved SQL login belongs to the SQL service: removing the service deletes
// it, even when the samples are kept. Platform-neutral on purpose (its own
// settings, no installed program).
func TestRemoveSQLDeletesTheSavedLogin(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(root, "rec")
	cfg.LogPaths = []string{filepath.Join(root, "logs")}
	sqlDir := SQLDataDir(cfg.Folder)
	if err := os.MkdirAll(sqlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	login := filepath.Join(sqlDir, bbsql.LoginFile)
	sample := filepath.Join(sqlDir, "bb-sql-2026112312.ndjson")
	for _, p := range []string{login, sample} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prog := filepath.Join(root, "prog")
	if err := os.MkdirAll(prog, 0o700); err != nil {
		t.Fatal(err)
	}
	env := &fakeSQLEnv{fakeEnv: &fakeEnv{admin: true, exe: filepath.Join(root, "x.exe"), prog: prog}}
	m := &SQLManager{Svc: &fakeSvc{state: SvcStopped}, Env: env}
	// The launch-permission step is Windows-only and may report on other
	// platforms; what this test pins is the credential, so the error is not checked.
	_ = m.RemoveSQL(cfg, cfg.Name)
	if _, err := os.Stat(login); err == nil {
		t.Error("the saved SQL login survived removing the SQL service")
	}
	if _, err := os.Stat(sample); err != nil {
		t.Error("removing the service must keep the samples")
	}
}
