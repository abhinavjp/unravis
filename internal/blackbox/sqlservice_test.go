package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSQLEnv is fakeEnv plus the locked SQL data folder. Embedding keeps every
// other answer the same, so the host install path is not changed by these tests.
type fakeSQLEnv struct {
	*fakeEnv
	sqlLocked []string // "dir sid" of every SecureSQLDir call, in order
	sqlErr    error
}

func (e *fakeSQLEnv) SecureSQLDir(dir, sid string) error {
	e.sqlLocked = append(e.sqlLocked, dir+" "+sid)
	return e.sqlErr
}

// createFailSvc is fakeSvc with a Create that fails, so the undo can be watched.
type createFailSvc struct {
	*fakeSvc
	err error
}

func (f *createFailSvc) Create(ServiceSpec) error {
	f.calls = append(f.calls, "create")
	return f.err
}

type sqlRig struct {
	m   *SQLManager
	svc *fakeSvc
	env *fakeSQLEnv
	cfg Config
}

func newSQLRig(t *testing.T) *sqlRig {
	t.Helper()
	g := newSvcRig(t)
	env := &fakeSQLEnv{fakeEnv: g.env}
	return &sqlRig{m: &SQLManager{Svc: g.svc, Env: env}, svc: g.svc, env: env, cfg: g.cfg}
}

func (g *sqlRig) sqlDir() string { return filepath.Join(g.cfg.Folder, SQLDataSubdir) }

// configPath is the installed settings file the SQL service is bound to.
func (g *sqlRig) configPath() string { return filepath.Join(g.env.prog, ConfigFileName) }

// The SQL data folder is readable only by SYSTEM, Administrators and the SQL
// service SID: never BUILTIN\Users, never Everyone (BL-123).
func TestSQLSDDLNoUsers(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	text := sqlSDDL(sid)
	want := "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
	if text != want {
		t.Fatalf("sqlSDDL = %q, want %q", text, want)
	}
	for _, must := range []string{";;;SY)", ";;;BA)", ";;;" + sid + ")"} {
		if !strings.Contains(text, must) {
			t.Fatalf("%q lacks %q", text, must)
		}
	}
	for _, never := range []string{"BU", "S-1-1-0", "AU", "WD", "IU", "GR", "GW"} {
		if strings.Contains(text, never) {
			t.Fatalf("%q gives ordinary users rights (%q)", text, never)
		}
	}
	if !strings.HasPrefix(text, "D:P") {
		t.Fatalf("%q must be protected from inherited permissions", text)
	}
	if n := strings.Count(text, "(A;"); n != 3 {
		t.Fatalf("%d ACEs, want exactly 3: SYSTEM, Administrators, the SQL SID", n)
	}
}

// The per-service SID is the one Windows itself gives NT SERVICE\<name>.
func TestSQLServiceSID(t *testing.T) {
	got, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	// sc showsid UnravisBlackboxSql on a real Windows machine.
	if want := "S-1-5-80-690519117-884339739-239568777-2365834119-2194647489"; got != want {
		t.Fatalf("sqlServiceSID(%s) = %s, want %s", SQLServiceName, got, want)
	}
	// The keeper algorithm's known vector (internal/keeper/owner_test.go).
	if sid, err := sqlServiceSID("TrustedInstaller"); err != nil || sid != "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464" {
		t.Fatalf("TrustedInstaller: %s %v", sid, err)
	}
	if sid, err := sqlServiceSID(strings.ToLower(SQLServiceName)); err != nil || sid != got {
		t.Fatalf("the service SID must ignore case: %s %v", sid, err)
	}
	if _, err := sqlServiceSID(""); err == nil {
		t.Fatal("an empty service name was accepted")
	}
}

// The SQL service runs under its own virtual account, never LocalSystem, and
// carries no secret on its command line.
func TestInstallSQLUsesAccount(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	spec := g.svc.spec
	if spec.Name != SQLServiceName || g.svc.state != SvcRunning {
		t.Fatalf("%+v %s", spec, g.svc.state)
	}
	if spec.Account != SQLServiceAccount {
		t.Fatalf("the service runs as %q, want %q", spec.Account, SQLServiceAccount)
	}
	if spec.Account == "" || strings.EqualFold(spec.Account, "LocalSystem") || strings.Contains(strings.ToUpper(spec.Account), "SYSTEM") {
		t.Fatalf("the SQL service must not run as SYSTEM: %q", spec.Account)
	}
	// The folder grants the service SID, so that SID must be in its token
	// (the keeper precedent, internal/setup/system.go:72).
	if !spec.SIDUnrestricted {
		t.Fatal("the SQL service cannot use the SID its data folder grants")
	}
	if want := filepath.Join(g.env.prog, InstalledExeName); spec.ExePath != want {
		t.Fatalf("the service runs %q, want the installed copy %q", spec.ExePath, want)
	}
	args := strings.Join(spec.Args, " ")
	if !strings.Contains(args, "sql-service") || !strings.Contains(args, filepath.Join(g.env.prog, ConfigFileName)) {
		t.Fatalf("not the SQL run mode with the installed settings: %s", args)
	}
	for _, a := range spec.Args {
		for _, bad := range []string{"password", "pwd", "secret", "grant", "revoke", "create login", "--key"} {
			if strings.Contains(strings.ToLower(a), bad) {
				t.Fatalf("a secret or a SQL grant on the service command line: %q", a)
			}
		}
	}
}

// The service is registered with the EXACT config path it was given — never a
// hardcoded program-folder copy — and the locked folder is the SQL folder of
// that same config (BBX-5-02 fix: the service must not read an unlocked folder).
func TestInstallSQLBindsGivenConfigPath(t *testing.T) {
	g := newSQLRig(t)
	// A non-default config path, different from progDir/blackbox.json.
	other := filepath.Join(t.TempDir(), "other-blackbox.json")
	if err := g.m.InstallSQL(g.cfg, other); err != nil {
		t.Fatal(err)
	}
	want := []string{"sql-service", "--config", other}
	if got := g.svc.spec.Args; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("service args %v, want %v", got, want)
	}
	if strings.Contains(strings.Join(g.svc.spec.Args, " "), filepath.Join(g.env.prog, ConfigFileName)) {
		t.Fatalf("the service fell back to the hardcoded program-folder config: %v", g.svc.spec.Args)
	}
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.env.sqlLocked) != 1 || g.env.sqlLocked[0] != g.sqlDir()+" "+sid {
		t.Fatalf("the SQL folder of the given config was not locked: %v", g.env.sqlLocked)
	}
}

// An empty config path is refused before anything is changed: the service must
// always be bound to a real settings file (BBX-5-02 fix).
func TestInstallSQLRefusesEmptyConfigPath(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, ""); installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	if len(g.svc.calls) != 0 || len(g.env.sqlLocked) != 0 {
		t.Fatalf("something was changed on an empty config path: %v %v", g.svc.calls, g.env.sqlLocked)
	}
}

// Only the new sql subfolder is locked down; the host recordings folder ACL and
// the host service are left exactly as they were.
func TestInstallSQLLocksFolder(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.env.sqlLocked) != 1 || g.env.sqlLocked[0] != g.sqlDir()+" "+sid {
		t.Fatalf("the sql folder was not locked to SYSTEM/Administrators/%s: %v", sid, g.env.sqlLocked)
	}
	if st, serr := os.Stat(g.sqlDir()); serr != nil || !st.IsDir() {
		t.Fatalf("sql folder not made: %v", serr)
	}
	if strings.Contains(sqlSDDL(sid), "BU") {
		t.Fatal("the sql folder is readable by ordinary users")
	}
	// SecureDir is the host's folder lock: it must not have been called at all.
	if len(g.env.secured) != 0 || len(g.env.log) != 0 {
		t.Fatalf("the host folders were re-secured: %v %v", g.env.secured, g.env.log)
	}
	if g.svc.spec.Name == ServiceName {
		t.Fatal("the host service was changed")
	}
}

// A failed install leaves no service and no folder it made; a folder that was
// already there keeps what it had.
func TestInstallSQLUndoOnFailure(t *testing.T) {
	gone := func(t *testing.T, g *sqlRig) {
		t.Helper()
		if g.svc.state != SvcNotInstalled {
			t.Fatalf("service left behind: %s", g.svc.state)
		}
		for _, p := range []string{g.sqlDir(), g.cfg.Folder} {
			if _, err := os.Stat(p); err == nil {
				t.Fatalf("left behind: %s", p)
			}
		}
	}
	t.Run("create fails", func(t *testing.T) {
		g := newSQLRig(t)
		g.m.Svc = &createFailSvc{fakeSvc: g.svc, err: errors.New("access denied")}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		gone(t, g)
	})
	t.Run("start fails", func(t *testing.T) {
		g := newSQLRig(t)
		g.svc.startErr = errors.New("boom")
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		if g.svc.did("delete") != 1 {
			t.Fatalf("the service was not deleted: %v", g.svc.calls)
		}
		gone(t, g)
	})
	t.Run("the lock fails", func(t *testing.T) {
		g := newSQLRig(t)
		g.env.sqlErr = errors.New("access denied")
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		if g.svc.did("create") != 0 {
			t.Fatal("a service was registered although the folder could not be locked")
		}
		gone(t, g)
	})
	t.Run("a folder that was already there keeps what it had", func(t *testing.T) {
		g := newSQLRig(t)
		if err := os.MkdirAll(g.sqlDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		mine := filepath.Join(g.sqlDir(), "earlier.ndjson")
		if err := os.WriteFile(mine, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		g.m.Svc = &createFailSvc{fakeSvc: g.svc, err: errors.New("access denied")}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		if _, err := os.Stat(mine); err != nil {
			t.Fatal("earlier SQL data was removed by the undo")
		}
	})
}

// The name must be confirmed before anything at all is changed, and the data
// goes only when it was asked for.
func TestRemoveSQLConfirm(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(g.sqlDir(), "sql-data.ndjson")
	if err := os.WriteFile(data, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := len(g.svc.calls)
	if err := g.m.RemoveSQL(g.cfg, "web-c"); !errors.Is(err, ErrConfirmName) {
		t.Fatalf("%v", err)
	}
	if len(g.svc.calls) != calls || g.svc.state != SvcRunning {
		t.Fatalf("the service was touched before the name was confirmed: %v", g.svc.calls)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("the sql data was touched before the name was confirmed")
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	n := len(g.svc.calls)
	if g.svc.calls[n-2] != "stop" || g.svc.calls[n-1] != "delete" || g.svc.state != SvcNotInstalled {
		t.Fatalf("%v %s", g.svc.calls, g.svc.state)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("the sql data was deleted without being asked")
	}

	// Asked for: only <folder>\sql goes, never the recordings folder itself.
	g2 := newSQLRig(t)
	if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	g2.m.DeleteData = true
	keep := filepath.Join(g2.cfg.Folder, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g2.m.RemoveSQL(g2.cfg, "web-c"); !errors.Is(err, ErrConfirmName) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(g2.sqlDir()); err != nil {
		t.Fatal("the sql folder was deleted on a wrong name")
	}
	if err := g2.m.RemoveSQL(g2.cfg, g2.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(g2.sqlDir()); err == nil {
		t.Fatal("the sql folder was kept although its deletion was asked for")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a file in the recordings folder was deleted")
	}
}

// No administrator rights, no install; a second install says already installed
// and leaves the first service alone.
func TestSQLInstallRefusesNoAdminAndASecondService(t *testing.T) {
	g := newSQLRig(t)
	g.env.admin = false
	if err := g.m.InstallSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonNoAdmin {
		t.Fatalf("%v", err)
	}
	if len(g.svc.calls) != 0 || len(g.env.sqlLocked) != 0 {
		t.Fatalf("something was changed without administrator rights: %v %v", g.svc.calls, g.env.sqlLocked)
	}
	g.env.admin = true
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	err := g.m.InstallSQL(g.cfg, g.configPath())
	if installReason(t, err) != ReasonExists {
		t.Fatalf("%v", err)
	}
	if g.svc.did("create") != 1 || g.svc.did("delete") != 0 || g.svc.state != SvcRunning {
		t.Fatalf("the first install was disturbed: %v", g.svc.calls)
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); !errors.Is(err, ErrNotSQLInstalled) {
		t.Fatalf("%v", err)
	}
}

// The host recorder keeps LocalSystem, no service SID change and its own folder
// rights: this packet adds a second service, it does not alter the first.
func TestHostServiceAndFolderUnchangedByTheSQLWork(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if g.svc.spec.Account != "" || g.svc.spec.SIDUnrestricted {
		t.Fatalf("the host service account changed: %+v", g.svc.spec)
	}
	if len(g.env.log) != 2 || !strings.Contains(g.env.log[0], "read=true") || !strings.Contains(g.env.log[1], "read=false") {
		t.Fatalf("the host folder rights changed: %v", g.env.log)
	}
	if _, err := os.Stat(filepath.Join(g.cfg.Folder, SQLDataSubdir)); err == nil {
		t.Fatal("the host install made a sql folder")
	}
}
