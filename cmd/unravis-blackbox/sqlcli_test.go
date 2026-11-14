package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// fakeSQLSvc is a service manager that records the spec Create was given, so the
// tests can prove the SQL service keeps its virtual account and unrestricted
// SID. (internal/setup's bbSvc adapter drops both; the CLI must not use it.)
type fakeSQLSvc struct {
	*memSvc
	spec blackbox.ServiceSpec
}

func (f *fakeSQLSvc) Create(spec blackbox.ServiceSpec) error {
	f.spec = spec
	return f.memSvc.Create(spec)
}

// fakeSQLDirEnv records the SQL data folder lock instead of applying an ACL.
type fakeSQLDirEnv struct {
	*memEnv
	locked []string // "dir sid" of every SecureSQLDir call
}

func (e *fakeSQLDirEnv) SecureSQLDir(dir, sid string) error {
	e.locked = append(e.locked, dir+" "+sid)
	return nil
}

// fakeElev records every call of the elevate helper. The SQL commands must
// never reach it: the owner runs the grant in SSMS, not the app (C14).
type fakeElev struct{ calls [][]string }

func (f *fakeElev) Run(_ context.Context, exe string, args []string, _ bool) ([]byte, error) {
	f.calls = append(f.calls, append([]string{exe}, args...))
	return nil, nil
}

// fakeSQLRun is the fake SQL runner/executor: it records calls and never runs
// SQL. install-sql and remove-sql must not reach it at all.
type fakeSQLRun struct {
	calls int
	opts  sqlRunOptions
}

func (f *fakeSQLRun) run(_ context.Context, o sqlRunOptions) error {
	f.calls++
	f.opts = o
	return nil
}

type sqlRig struct {
	*rig
	svc      *fakeSQLSvc
	env      *fakeSQLDirEnv
	elev     *fakeElev
	runner   *fakeSQLRun
	hostRuns int // calls of hostRun, the only code that starts the HTTP status page
}

// newSQLRig installs the host Blackbox first (so the SQL commands read real
// installed settings), then swaps in the SQL fakes.
func newSQLRig(t *testing.T) *sqlRig {
	t.Helper()
	r := newRig(t)
	if c, out, errs := r.do(append([]string{"install", "--name", "box-1"}, r.args...)...); c != 0 {
		t.Fatalf("host install: %d %s %s", c, out, errs)
	}
	g := &sqlRig{rig: r, svc: &fakeSQLSvc{memSvc: &memSvc{}}, env: &fakeSQLDirEnv{memEnv: r.env}, elev: &fakeElev{}, runner: &fakeSQLRun{}}
	r.d.sqlMgr = &blackbox.SQLManager{Svc: g.svc, Env: g.env}
	r.d.elev = g.elev
	r.d.sqlRun = g.runner.run
	r.d.hostRun = func(context.Context, blackbox.HostOptions) error { g.hostRuns++; return nil }
	return g
}

// install-sql prints the owner-run grant and executes nothing: no SQL, no
// elevate helper, no host recorder. The grant never lands in blackbox.json.
func TestGrantScriptPrintedNotExecuted(t *testing.T) {
	g := newSQLRig(t)
	c, out, errs := g.do("install-sql")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	for _, must := range []string{
		`CREATE LOGIN [NT SERVICE\UnravisBlackboxSql] FROM WINDOWS WITH DEFAULT_DATABASE=master;`,
		`GRANT VIEW SERVER PERFORMANCE STATE TO [NT SERVICE\UnravisBlackboxSql];`,
		"Run in SSMS as an admin",
	} {
		if !strings.Contains(out, must) {
			t.Fatalf("install-sql output lacks %q:\n%s", must, out)
		}
	}
	if g.runner.calls != 0 {
		t.Fatalf("install-sql executed SQL (%d runner calls)", g.runner.calls)
	}
	if len(g.elev.calls) != 0 {
		t.Fatalf("install-sql used the elevate helper: %v", g.elev.calls)
	}
	if g.hostRuns != 0 {
		t.Fatal("install-sql started the host recorder")
	}
	// The grant is printed, never stored: not in the settings, not on the
	// service command line, and no password anywhere. (Statement tokens, not
	// bare words: the temp folder path embeds the test name.)
	cfg, err := os.ReadFile(filepath.Join(g.env.prog, defaultConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var cfgMap map[string]any
	if err := json.Unmarshal(cfg, &cfgMap); err != nil {
		t.Fatal(err)
	}
	for k := range cfgMap {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || strings.Contains(lk, "pwd") || strings.Contains(lk, "secret") {
			t.Fatalf("blackbox.json holds a credential field %q", k)
		}
	}
	for _, bad := range []string{"CREATE LOGIN", "GRANT VIEW SERVER"} {
		if strings.Contains(strings.ToUpper(string(cfg)), bad) {
			t.Fatalf("blackbox.json holds the grant text %q", bad)
		}
	}
	for _, a := range g.svc.spec.Args {
		la := strings.ToLower(a)
		if strings.Contains(la, "grant view") || strings.Contains(la, "create login") {
			t.Fatalf("a grant on the service command line: %q", a)
		}
	}
	// JSON mode carries the same script as a field, still nothing executed.
	g2 := newSQLRig(t)
	c2, out2, _ := g2.do("install-sql", "--json")
	got := jsonAnswer(t, out2)
	if c2 != 0 || got["ok"] != true || !strings.Contains(got["grantScript"].(string), "CREATE LOGIN") {
		t.Fatalf("%d %v", c2, got)
	}
	if g2.runner.calls != 0 || len(g2.elev.calls) != 0 {
		t.Fatal("the JSON path executed something")
	}
}

// remove-sql prints the owner-run revoke script and executes nothing.
func TestRevokeScriptPrinted(t *testing.T) {
	g := newSQLRig(t)
	if c, out, errs := g.do("install-sql"); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	c, out, errs := g.do("remove-sql", "--confirm-name", "box-1")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	for _, must := range []string{
		`REVOKE VIEW SERVER PERFORMANCE STATE FROM [NT SERVICE\UnravisBlackboxSql];`,
		`DROP LOGIN [NT SERVICE\UnravisBlackboxSql];`,
	} {
		if !strings.Contains(out, must) {
			t.Fatalf("remove-sql output lacks %q:\n%s", must, out)
		}
	}
	if g.runner.calls != 0 || len(g.elev.calls) != 0 {
		t.Fatalf("remove-sql executed something: %d %v", g.runner.calls, g.elev.calls)
	}
	if g.svc.state != blackbox.SvcNotInstalled {
		t.Fatalf("the SQL service was not removed: %s", g.svc.state)
	}
	// The SQL data stays; only the service goes.
	if _, err := os.Stat(blackbox.SQLDataDir(filepath.Join(g.dir, "rec"))); err != nil {
		t.Fatalf("the SQL data was deleted without being asked: %v", err)
	}
}

// The sql-service mode runs the SQL runner and never the host recorder: no
// HTTP server is built in this mode (the fake runner records what it got).
func TestSQLServiceModeNoHTTP(t *testing.T) {
	g := newSQLRig(t)
	c, out, errs := g.do("sql-service")
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if g.runner.calls != 1 {
		t.Fatalf("the SQL runner ran %d times, want 1", g.runner.calls)
	}
	if g.hostRuns != 0 {
		t.Fatal("sql-service started the host recorder, which builds the HTTP status page")
	}
	if len(g.elev.calls) != 0 {
		t.Fatalf("sql-service used the elevate helper: %v", g.elev.calls)
	}
	wantDir := blackbox.SQLDataDir(filepath.Join(g.dir, "rec"))
	if g.runner.opts.Dir != wantDir {
		t.Fatalf("runner dir %q, want the locked SQL folder %q", g.runner.opts.Dir, wantDir)
	}
	if g.runner.opts.Config.Name != "box-1" {
		t.Fatalf("runner config %+v", g.runner.opts.Config)
	}
}

// The elevate helper is never invoked to run SQL or a grant, and the service
// command line carries no secret and no grant text.
func TestInstallSQLNoElevateGrant(t *testing.T) {
	g := newSQLRig(t)
	if c, out, errs := g.do("install-sql"); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if c, out, errs := g.do("remove-sql", "--confirm-name", "box-1"); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if len(g.elev.calls) != 0 {
		t.Fatalf("the elevate helper was invoked: %v", g.elev.calls)
	}
	if g.runner.calls != 0 {
		t.Fatalf("SQL was executed: %d runner calls", g.runner.calls)
	}
	args := strings.ToLower(strings.Join(g.svc.spec.Args, " "))
	for _, bad := range []string{"password", "pwd", "secret", "grant view", "revoke view", "create login", "sqlcmd"} {
		if strings.Contains(args, bad) {
			t.Fatalf("a secret or a grant on the service command line: %q", args)
		}
	}
}

// The grant text holds only the allowed statements: CREATE LOGIN and GRANT of
// the performance-state right, plus comments. The revoke text holds only
// REVOKE and DROP LOGIN. Nothing else may ever be printed as a "grant".
func TestGrantScriptLint(t *testing.T) {
	for _, line := range strings.Split(grantScript, "\n") {
		l := strings.ToUpper(strings.TrimSpace(line))
		if l == "" || strings.HasPrefix(l, "--") {
			continue
		}
		if !strings.HasPrefix(l, "CREATE LOGIN [NT SERVICE\\UNRAVISBLACKBOXSQL] FROM WINDOWS") &&
			!strings.HasPrefix(l, "GRANT VIEW SERVER PERFORMANCE STATE TO ") &&
			!strings.HasPrefix(l, "GRANT VIEW SERVER STATE TO ") {
			t.Fatalf("grant script line is not an allowed statement: %q", line)
		}
	}
	for _, bad := range []string{"INSERT", "UPDATE", "DELETE", "EXEC", "ALTER", "DROP", "TRUNCATE", "MERGE", "DENY", "XP_CMDSHELL", "SP_", "PASSWORD", "WITH LOGIN"} {
		if strings.Contains(strings.ToUpper(grantScript), bad) {
			t.Fatalf("grant script contains %q", bad)
		}
	}
	if !strings.Contains(grantScript, "["+blackbox.SQLServiceAccount+"]") {
		t.Fatalf("grant script does not name the virtual account %s", blackbox.SQLServiceAccount)
	}
	for _, line := range strings.Split(revokeScript, "\n") {
		l := strings.ToUpper(strings.TrimSpace(line))
		if l == "" || strings.HasPrefix(l, "--") {
			continue
		}
		if !strings.HasPrefix(l, "REVOKE VIEW SERVER PERFORMANCE STATE FROM ") &&
			!strings.HasPrefix(l, "DROP LOGIN [NT SERVICE\\UNRAVISBLACKBOXSQL]") {
			t.Fatalf("revoke script line is not an allowed statement: %q", line)
		}
	}
}

// Review-pinned: the created SQL service spec carries the virtual account and
// the unrestricted SID, and the real CLI wires SQLManager to blackbox.WinService
// DIRECTLY — never through internal/setup's bbSvc adapter, which drops both.
func TestInstallSQLSpecAccountAndSID(t *testing.T) {
	g := newSQLRig(t)
	if c, out, errs := g.do("install-sql"); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	spec := g.svc.spec
	if spec.Name != blackbox.SQLServiceName {
		t.Fatalf("service name %q", spec.Name)
	}
	if spec.Account != `NT SERVICE\UnravisBlackboxSql` {
		t.Fatalf("the SQL service runs as %q, want the virtual account", spec.Account)
	}
	if spec.Account != blackbox.SQLServiceAccount {
		t.Fatalf("account %q drifted from blackbox.SQLServiceAccount %q", spec.Account, blackbox.SQLServiceAccount)
	}
	if !spec.SIDUnrestricted {
		t.Fatal("SIDUnrestricted is off: the service could not use the SID its data folder grants")
	}
	if len(g.env.locked) != 1 || !strings.HasPrefix(g.env.locked[0], blackbox.SQLDataDir(filepath.Join(g.dir, "rec"))+" S-1-5-80-") {
		t.Fatalf("the SQL folder was not locked to the service SID: %v", g.env.locked)
	}
	// The real deps wire the Windows service manager directly (both platforms).
	if _, ok := realDeps().sqlMgr.Svc.(blackbox.WinService); !ok {
		t.Fatalf("SQLManager is wired to %T, want blackbox.WinService (the bbSvc adapter drops Account/SIDUnrestricted)", realDeps().sqlMgr.Svc)
	}
}

// Every SQL command failure answers with a fixed reason code, never driver text.
func TestSQLCommandsFixedReasons(t *testing.T) {
	// No installed Blackbox: install-sql says not-installed.
	r := newRig(t)
	r.d.sqlMgr = &blackbox.SQLManager{Svc: &fakeSQLSvc{memSvc: &memSvc{}}, Env: &fakeSQLDirEnv{memEnv: r.env}}
	c, out, _ := r.do("install-sql", "--json")
	if got := jsonAnswer(t, out); c != exitFail || got["reason"] != "not-installed" {
		t.Fatalf("%d %v", c, got)
	}
	g := newSQLRig(t)
	// Wrong confirmation: nothing is touched.
	c, out, _ = g.do("remove-sql", "--json", "--confirm-name", "typo")
	if got := jsonAnswer(t, out); c != exitFail || got["reason"] != "confirm-name" {
		t.Fatalf("%d %v", c, got)
	}
	if g.svc.state != blackbox.SvcNotInstalled || len(g.svc.calls) != 0 {
		t.Fatalf("remove-sql touched the machine on a wrong name: %v", g.svc.calls)
	}
	// No SQL service installed: not-installed.
	c, out, _ = g.do("remove-sql", "--json", "--confirm-name", "box-1")
	if got := jsonAnswer(t, out); c != exitFail || got["reason"] != "not-installed" {
		t.Fatalf("%d %v", c, got)
	}
	// No administrator rights: no-admin, nothing changed.
	g.env.admin = false
	c, out, _ = g.do("install-sql", "--json")
	if got := jsonAnswer(t, out); c != exitFail || got["reason"] != "no-admin" {
		t.Fatalf("%d %v", c, got)
	}
	if len(g.svc.calls) != 0 {
		t.Fatalf("install-sql changed the machine without admin rights: %v", g.svc.calls)
	}
}