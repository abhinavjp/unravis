package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSQLEnv is fakeEnv plus the locked SQL data folder and the launch-access
// grant. Embedding keeps every other answer the same, so the host install path
// is not changed by these tests.
type fakeSQLEnv struct {
	*fakeEnv
	sqlLocked  []string // "dir sid" of every SecureSQLDir call, in order
	sqlErr     error
	sqlGranted []string // "exe config sid" of every GrantSQLLaunchAccess call
	grantErr   error
	// SEC-1: the fake ACL reader. verifyCalls records every admin-lock proof
	// asked of a settings file; verify, when set, answers instead of the
	// default "admin-locked" answer, so a test can drive a user-writable,
	// unreadable or swapped config without Windows.
	verifyCalls []string
	verify      func(path string) error
}

func (e *fakeSQLEnv) VerifyAdminOnlyFile(path string) error {
	e.verifyCalls = append(e.verifyCalls, path)
	if e.verify != nil {
		return e.verify(path)
	}
	return nil
}

func (e *fakeSQLEnv) SecureSQLDir(dir, sid string) error {
	e.sqlLocked = append(e.sqlLocked, dir+" "+sid)
	return e.sqlErr
}

func (e *fakeSQLEnv) GrantSQLLaunchAccess(exe, config, sid string) error {
	e.sqlGranted = append(e.sqlGranted, exe+" "+config+" "+sid)
	return e.grantErr
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
	// W7-01: install-sql now requires a real host installation — the installed
	// program and the settings file it binds must exist.
	if err := os.WriteFile(filepath.Join(g.prog, InstalledExeName), []byte("MZ program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(filepath.Join(g.prog, ConfigFileName), g.cfg, false); err != nil {
		t.Fatal(err)
	}
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
// carries no secret on its command line. W8-03: install creates it STOPPED —
// the owner runs the grant and then enable-sql; a first connection before the
// grant must not latch a denial. W7-01: the service SID gets least-privilege
// launch access to the installed program and settings.
func TestInstallSQLUsesAccount(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	spec := g.svc.spec
	if spec.Name != SQLServiceName || g.svc.state != SvcStopped {
		t.Fatalf("%+v %s (install must leave the service stopped)", spec, g.svc.state)
	}
	if g.svc.did("start") != 0 {
		t.Fatalf("install started the service before the owner grant: %v", g.svc.calls)
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
	// W7-01: launch access was granted for the installed exe and the bound
	// config, to the service SID — and nothing else.
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	wantGrant := filepath.Join(g.env.prog, InstalledExeName) + " " + g.configPath() + " " + sid
	if len(g.env.sqlGranted) != 1 || g.env.sqlGranted[0] != wantGrant {
		t.Fatalf("launch access grant = %v, want [%s]", g.env.sqlGranted, wantGrant)
	}
}

// The service is registered with the EXACT config path it was given — never a
// hardcoded program-folder copy — and the locked folder is the SQL folder of
// that same config (BBX-5-02 fix: the service must not read an unlocked folder).
func TestInstallSQLBindsGivenConfigPath(t *testing.T) {
	g := newSQLRig(t)
	// A non-default config path, different from progDir/blackbox.json. It must
	// be a real file: install-sql verifies the settings it binds (W7-01).
	other := filepath.Join(t.TempDir(), "other-blackbox.json")
	if err := SaveConfig(other, g.cfg, false); err != nil {
		t.Fatal(err)
	}
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
	t.Run("the launch grant fails", func(t *testing.T) {
		g := newSQLRig(t)
		g.env.grantErr = errors.New("access denied")
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		if g.svc.did("create") != 0 {
			t.Fatal("a service was registered although launch access could not be granted")
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
	if len(g.svc.calls) != calls || g.svc.state != SvcStopped {
		t.Fatalf("the service was touched before the name was confirmed: %v", g.svc.calls)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("the sql data was touched before the name was confirmed")
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	n := len(g.svc.calls)
	if g.svc.calls[n-1] != "delete" || g.svc.state != SvcNotInstalled {
		t.Fatalf("%v %s", g.svc.calls, g.svc.state)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatal("the sql data was deleted without being asked")
	}
	// W8-04: a clean remove publishes the off status, never an old enabled:true.
	statusRaw, err := os.ReadFile(filepath.Join(g.sqlDir(), "status.json"))
	if err != nil {
		t.Fatalf("remove did not publish a status: %v", err)
	}
	if !strings.Contains(string(statusRaw), `"enabled":false`) || !strings.Contains(string(statusRaw), `"state":"off"`) {
		t.Fatalf("remove published a non-off status: %s", statusRaw)
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
	if g.svc.did("create") != 1 || g.svc.did("delete") != 0 || g.svc.state != SvcStopped {
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

// W8-09: a relative config path is refused before anything changes — the
// service must be bound to an absolute path that resolves the same way under
// the SCM's working directory as under the installer's.
func TestInstallSQLRefusesRelativeConfigPath(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, "relative-blackbox.json"); installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	if len(g.svc.calls) != 0 || len(g.env.sqlLocked) != 0 || len(g.env.sqlGranted) != 0 {
		t.Fatalf("something was changed on a relative config path: %v %v", g.svc.calls, g.env.sqlLocked)
	}
}

// W7-01: a supplied config alone does not prove a host installation — the
// installed program and the bound settings file must both exist.
func TestInstallSQLRequiresInstalledProgramAndConfig(t *testing.T) {
	t.Run("no installed program", func(t *testing.T) {
		g := newSQLRig(t)
		if err := os.Remove(filepath.Join(g.env.prog, InstalledExeName)); err != nil {
			t.Fatal(err)
		}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if len(g.svc.calls) != 0 {
			t.Fatalf("a service was created without an installed program: %v", g.svc.calls)
		}
	})
	t.Run("no settings file", func(t *testing.T) {
		g := newSQLRig(t)
		missing := filepath.Join(t.TempDir(), "missing.json")
		if err := g.m.InstallSQL(g.cfg, missing); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if len(g.svc.calls) != 0 {
			t.Fatalf("a service was created without a settings file: %v", g.svc.calls)
		}
	})
}

// W8-03: enable-sql is the explicit owner activation. It confirms the installed
// program/config/folder, durably clears ONLY the persisted latch, and starts
// the service. A healthy state is left untouched; a routine restart never
// clears a genuine denial (only this owner action does).
func TestEnableSQLActivatesAfterTheOwnerGrant(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	if g.svc.state != SvcStopped {
		t.Fatalf("install left the service %s", g.svc.state)
	}
	// Simulate the first-connection denial the owner is re-arming after: a
	// persisted denied latch in the SQL data folder.
	if err := os.MkdirAll(g.sqlDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(g.sqlDir(), "state.json")
	denied := `{"interval":30000000000,"failures":1,"autoOff":true,"slowStrikes":0,"state":"denied"}`
	if err := os.WriteFile(statePath, []byte(denied), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.m.EnableSQL(g.cfg, g.configPath()); err != nil {
		t.Fatalf("enable-sql: %v", err)
	}
	if g.svc.state != SvcRunning || g.svc.did("start") != 1 {
		t.Fatalf("enable-sql did not start the service: %v %s", g.svc.calls, g.svc.state)
	}
	// The latch is durably cleared: a fresh on state, no auto-off.
	cleared, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cleared), `"autoOff":true`) || !strings.Contains(string(cleared), `"state":"on"`) {
		t.Fatalf("the latch was not cleared: %s", cleared)
	}
	// The folder lock was re-confirmed at activation.
	if len(g.env.sqlLocked) != 2 {
		t.Fatalf("enable-sql did not re-confirm the folder lock: %v", g.env.sqlLocked)
	}
	// A second enable with a healthy state starts nothing new and touches no
	// state: the service already runs.
	calls := len(g.svc.calls)
	if err := g.m.EnableSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	if len(g.svc.calls) != calls {
		t.Fatalf("enable-sql disturbed a running service: %v", g.svc.calls)
	}
}

// enable-sql refuses without admin, without an installed service, and when the
// settings no longer match the installed folder — and it never starts the
// service when the latch cannot be cleared durably.
func TestEnableSQLRefusals(t *testing.T) {
	t.Run("no admin", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		g.env.admin = false
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonNoAdmin {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatal("enable-sql started without admin rights")
		}
	})
	t.Run("not installed", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.EnableSQL(g.cfg, g.configPath()); !errors.Is(err, ErrNotSQLInstalled) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("config folder mismatch", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		other := g.cfg
		other.Folder = filepath.Join(t.TempDir(), "elsewhere")
		otherPath := filepath.Join(t.TempDir(), "other.json")
		if err := SaveConfig(otherPath, other, false); err != nil {
			t.Fatal(err)
		}
		if err := g.m.EnableSQL(g.cfg, otherPath); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatal("enable-sql started with a mismatched config")
		}
	})
}

// SEC-1: a --config an ordinary user can change must never be bound to the SQL
// service. The admin-lock proof is asked BEFORE anything is mutated, and an
// unreadable or unverifiable ACL is a refusal, never a pass (fail closed).
func TestInstallSQLRefusesUserWritableConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"user-writable DACL", errors.New("blackbox: the settings file allows a non-administrator to change it")},
		{"untrusted owner", errors.New("blackbox: the settings file has an untrusted owner")},
		{"unreadable ACL", errors.New("access denied")},
		{"reparse ancestor", errors.New("blackbox: refusing a reparse point in the settings file chain")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newSQLRig(t)
			g.env.verify = func(string) error { return tc.err }
			err := g.m.InstallSQL(g.cfg, g.configPath())
			if installReason(t, err) != ReasonOther {
				t.Fatalf("%v", err)
			}
			if len(g.svc.calls) != 0 || len(g.env.sqlLocked) != 0 || len(g.env.sqlGranted) != 0 {
				t.Fatalf("something was changed on a refused config: %v %v %v", g.svc.calls, g.env.sqlLocked, g.env.sqlGranted)
			}
			if len(g.env.verifyCalls) != 1 || g.env.verifyCalls[0] != g.configPath() {
				t.Fatalf("the bound config was not proven admin-locked: %v", g.env.verifyCalls)
			}
			// W8-07: the public sentence never names the path.
			if strings.Contains(err.Error(), g.configPath()) {
				t.Fatalf("the refusal names the path: %v", err)
			}
		})
	}
}

// SEC-1: an admin-locked config (the installed Program Files copy) installs,
// and the proof was actually asked of the bound path.
func TestInstallSQLAdminLockedConfigInstalls(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	if len(g.env.verifyCalls) == 0 || g.env.verifyCalls[0] != g.configPath() {
		t.Fatalf("install did not prove the config admin-locked: %v", g.env.verifyCalls)
	}
	if g.svc.state != SvcStopped || g.svc.did("create") != 1 {
		t.Fatalf("the admin-locked install did not register the stopped service: %v %s", g.svc.calls, g.svc.state)
	}
}

// SEC-1: enable-sql repeats the proof. A config that became user-writable since
// install is refused and the service is NOT started.
func TestEnableSQLRefusesUserWritableConfig(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	g.env.verify = func(string) error {
		return errors.New("blackbox: the settings file allows a non-administrator to change it")
	}
	if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	if g.svc.did("start") != 0 {
		t.Fatalf("enable-sql started with a user-writable config: %v", g.svc.calls)
	}
}

// SEC-1: the proof is repeated immediately before the service is started, so a
// config swapped after the entry check still cannot ride into a running
// service. A healthy config is proven at install, at enable entry and again at
// the start itself.
func TestEnableSQLRechecksConfigBeforeStart(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	g.env.verify = func(string) error {
		calls++
		if calls == 1 {
			return nil // the entry check still sees the locked config
		}
		return errors.New("blackbox: the settings file allows a non-administrator to change it")
	}
	if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	if g.svc.did("start") != 0 {
		t.Fatalf("the service started although the start-time proof failed: %v", g.svc.calls)
	}
	// Healthy path: install proves once, enable proves at entry AND at start.
	g2 := newSQLRig(t)
	if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	if err := g2.m.EnableSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	if len(g2.env.verifyCalls) != 3 {
		t.Fatalf("want proofs at install, enable entry and start, got %v", g2.env.verifyCalls)
	}
	for _, p := range g2.env.verifyCalls {
		if p != g2.configPath() {
			t.Fatalf("a proof was asked of the wrong path: %v", g2.env.verifyCalls)
		}
	}
}

// SEC-1: the DACL rule in pure Go, driven by described ACEs so every adversarial
// case runs on any platform. Fail closed: a NULL DACL, an unknown entry type or
// any change right for a non-administrator refuses; denies never cancel allows.
func TestAdminOnlyDACLRule(t *testing.T) {
	const (
		sy       = "S-1-5-18"
		ba       = "S-1-5-32-544"
		bu       = "S-1-5-32-545" // BUILTIN\Users
		everyone = "S-1-1-0"
		user     = "S-1-5-21-111-222-333-1001"
		full     = uint32(0x1F01FF) // FILE_ALL_ACCESS
		read     = uint32(0x120089) // FILE_GENERIC_READ
	)
	allow := func(mask uint32, trustee string) fileACE {
		return fileACE{Type: 0x0, Mask: mask, Trustee: trustee}
	}
	for _, tc := range []struct {
		name    string
		present bool
		aces    []fileACE
		wantErr bool
	}{
		{"NULL DACL is everyone-writable", false, nil, true},
		{"empty DACL grants nobody anything", true, nil, false},
		{"admin-locked Program Files copy", true, []fileACE{allow(full, sy), allow(full, ba)}, false},
		{"admin-locked with users read", true, []fileACE{allow(full, sy), allow(full, ba), allow(read, bu)}, false},
		{"service SID read grant (W7-01)", true, []fileACE{allow(full, sy), allow(full, ba), allow(read, "S-1-5-80-1")}, false},
		{"users write data", true, []fileACE{allow(full, sy), allow(0x2, bu)}, true},
		{"users append data", true, []fileACE{allow(0x4, bu)}, true},
		{"users write attributes", true, []fileACE{allow(0x100, bu)}, true},
		{"everyone WRITE_DAC", true, []fileACE{allow(0x40000, everyone)}, true},
		{"a user WRITE_OWNER", true, []fileACE{allow(0x80000, user)}, true},
		{"a user DELETE (replace the file)", true, []fileACE{allow(0x10000, user)}, true},
		{"a user GENERIC_ALL", true, []fileACE{allow(0x10000000, user)}, true},
		{"a user GENERIC_WRITE", true, []fileACE{allow(0x40000000, user)}, true},
		{"a user MAXIMUM_ALLOWED", true, []fileACE{allow(0x02000000, user)}, true},
		{"a user full control", true, []fileACE{allow(full, user)}, true},
		{"deny before allow does not cancel the allow", true, []fileACE{
			{Type: 0x1, Mask: 0x2, Trustee: bu}, allow(0x2, bu)}, true},
		{"object allow ACE with users write", true, []fileACE{{Type: 0x5, Mask: 0x2, Trustee: bu}}, true},
		{"callback allow ACE with users write", true, []fileACE{{Type: 0x9, Mask: 0x2, Trustee: bu}}, true},
		{"callback object allow ACE with users write", true, []fileACE{{Type: 0xB, Mask: 0x2, Trustee: bu}}, true},
		{"unknown ACE type fails closed", true, []fileACE{{Type: 0x4, Mask: 0, Trustee: sy}}, true},
		{"malformed trustee (empty SID string) with write", true, []fileACE{allow(0x2, "")}, true},
		{"audit entries grant nothing", true, []fileACE{allow(full, sy), {Type: 0x2, Mask: full, Trustee: bu}}, false},
		{"mandatory label grants nothing", true, []fileACE{allow(full, sy), {Type: 0x11, Mask: 0x3, Trustee: "S-1-16-12288"}}, false},
		{"object deny grants nothing", true, []fileACE{allow(full, sy), {Type: 0x6, Mask: full, Trustee: bu}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := adminOnlyDACL(tc.present, tc.aces)
			if tc.wantErr && err == nil {
				t.Fatal("a changeable settings file was accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("an admin-locked settings file was refused: %v", err)
			}
		})
	}
}
