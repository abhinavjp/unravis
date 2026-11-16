package blackbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/bbsql"
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
	// SEC-3: the install-record lock. markerLocks records every lock call;
	// markerLockErr, when set, makes the lock fail so the undo can be watched.
	markerLocks   []string
	markerLockErr error
	// SEC-6: the fake access lists. dacls holds the entries of every object the
	// launch grant touches, so a test can count them without Windows. An object
	// named in unreadable has an access list that cannot be read at all: both
	// the grant and the revoke must then refuse, never assume an empty list.
	dacls      map[string][]fileACE
	unreadable map[string]bool
	sqlRevoked [][]sqlLaunchGrant // what each remove-sql handed the revoke seam
	// REG-1: the fake owner model. owners records who owns each object, and an
	// object nobody pinned is owned by the account that ran the install — the
	// shape a real settings file has, because SaveConfig's atomic write does
	// os.Chmod only and a rename keeps the descriptor of the temp file.
	// VerifyAdminOnlyFile below refuses a path whose recorded owner
	// trustedFileOwner does not accept, so no test can pass by pretending the
	// settings file is admin-locked when nothing in the run ever locked it.
	owners map[string]string
	// REG-1: the settings-file owner lock. settingsLocks records every
	// LockSQLSettingsFile call; settingsLockErr makes the lock fail, so a write
	// that will not land must still refuse the install, never re-allow the owner.
	settingsLocks   []string
	settingsLockErr error
}

// The rights of the least-privilege launch grant, named as the production plan
// names them so the fake can never drift from what it is meant to model.
const (
	aceFlagsNone = aceFlagsExplicit
	maskExe      = maskSQLProgram
	maskCfg      = maskSQLSettings
	maskTraverse = maskSQLTraverse
)

// fullControl is the Windows FILE_ALL_ACCESS value, as the fake writes it for the
// entries a real Program Files object already carries.
const fullControl = uint32(0x001F01FF)

// sidInstallingUser stands for the account that ran the install: a machine
// user SID, which is what Windows really leaves as the owner of a settings file
// nobody pinned. It is deliberately not one of the two owners trustedFileOwner
// accepts, and TestInstallSQLLocksTheSettingsFileItBinds fails the fixture if
// that ever stops being true.
const sidInstallingUser = "S-1-5-21-1004-1005-1006-1001"

// ownerOf answers the fake's owner for path, seeding an unpinned object with the
// installing account, as the real filesystem does.
func (e *fakeSQLEnv) ownerOf(path string) string {
	if o, ok := e.owners[path]; ok {
		return o
	}
	if e.owners == nil {
		e.owners = map[string]string{}
	}
	e.owners[path] = sidInstallingUser
	return sidInstallingUser
}

// LockSQLSettingsFile is the fake's owner-and-DACL write for a settings file
// (REG-1): like the Windows lock it assigns bbsql.FolderOwnerSID in the same
// single write as the protected DACL, so the object it returns is exactly what
// VerifyAdminOnlyFile below demands. A failure it reports leaves the owner
// alone, as a refused SetNamedSecurityInfo does.
func (e *fakeSQLEnv) LockSQLSettingsFile(path string) error {
	e.settingsLocks = append(e.settingsLocks, path)
	if e.settingsLockErr != nil {
		return e.settingsLockErr
	}
	if e.owners == nil {
		e.owners = map[string]string{}
	}
	e.owners[path] = bbsql.FolderOwnerSID()
	return nil
}

// acl returns the fake access list of path, seeded the way the host install
// leaves a Program Files object: explicit full control for SYSTEM and
// Administrators and nothing for anybody else.
func (e *fakeSQLEnv) acl(path string) []fileACE {
	if e.dacls == nil {
		e.dacls = map[string][]fileACE{}
	}
	if aces, ok := e.dacls[path]; ok {
		return aces
	}
	seed := []fileACE{
		{Type: 0x0, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-18"},
		{Type: 0x0, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-32-544"},
	}
	e.dacls[path] = seed
	return seed
}

func (e *fakeSQLEnv) setACL(path string, aces []fileACE) {
	if e.dacls == nil {
		e.dacls = map[string][]fileACE{}
	}
	e.dacls[path] = aces
}

func (e *fakeSQLEnv) LockSQLInstallMarker(path string) error {
	e.markerLocks = append(e.markerLocks, path)
	if e.markerLockErr != nil {
		return e.markerLockErr
	}
	// The real marker lock assigns the owner in the same write (R1-s2), so the
	// record is trusted after it, and only after it.
	if e.owners == nil {
		e.owners = map[string]string{}
	}
	e.owners[path] = bbsql.FolderOwnerSID()
	return nil
}

func (e *fakeSQLEnv) VerifyAdminOnlyFile(path string) error {
	e.verifyCalls = append(e.verifyCalls, path)
	if e.verify != nil {
		return e.verify(path)
	}
	// REG-1: the owner half of the proof is answered by the fake's own owner
	// model, so an object nobody pinned is refused exactly as the real
	// verifyAdminOnlyFile refuses it. A test that wants to drive some other
	// answer sets verify above.
	if owner := e.ownerOf(path); !trustedFileOwner(owner) {
		return fmt.Errorf("blackbox: the settings file has an untrusted owner (%s)", owner)
	}
	return nil
}

func (e *fakeSQLEnv) SecureSQLDir(dir, sid string) error {
	e.sqlLocked = append(e.sqlLocked, dir+" "+sid)
	return e.sqlErr
}

// GrantSQLLaunchAccess applies the real launch plan (sqlLaunchGrants) to the fake
// access lists with the real idempotency rule (aceGrantNeeded), so a test sees the
// entries the Windows code would write (SEC-6). Like the Windows code it proves
// every target readable before writing any of them: an access list that cannot be
// read stops the whole grant with nothing changed.
func (e *fakeSQLEnv) GrantSQLLaunchAccess(exe, config, sid string) error {
	e.sqlGranted = append(e.sqlGranted, exe+" "+config+" "+sid)
	if e.grantErr != nil {
		return e.grantErr
	}
	grants, err := sqlLaunchGrants(exe, config, sid)
	if err != nil {
		return err
	}
	for _, gr := range grants {
		if e.unreadable[gr.Path] {
			return errors.New("access denied")
		}
	}
	for _, gr := range grants {
		e.addACE(gr.Path, gr.ACE)
	}
	return nil
}

// RevokeSQLLaunchGrants takes the same plan back with the rule the Windows revoke
// uses (aceGrantKept): only entries exactly equal to a planned one go. As in the
// Windows revoke, every target is read and decided before any list is rewritten.
func (e *fakeSQLEnv) RevokeSQLLaunchGrants(grants []sqlLaunchGrant) error {
	e.sqlRevoked = append(e.sqlRevoked, grants)
	for _, gr := range grants {
		if e.unreadable[gr.Path] {
			return errors.New("access denied")
		}
	}
	for _, gr := range grants {
		kept, _ := aceGrantKept(e.acl(gr.Path), gr.ACE)
		e.setACL(gr.Path, kept)
	}
	return nil
}

// addACE appends one entry to a fake object only when the object does not already
// hold exactly that entry, as the Windows append does.
func (e *fakeSQLEnv) addACE(path string, want fileACE) {
	aces := e.acl(path)
	if !aceGrantNeeded(aces, want) {
		return
	}
	e.setACL(path, append(aces, want))
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

// R1-s1: the folder the install hands the SQL service must carry the owner the
// service's own sample-folder rule demands. When that owner cannot be assigned,
// install-sql must refuse with its own sentence and roll back: a service
// registered over an un-owned folder reads nothing at any later start, and the
// operator has no way to fix it but a re-install. The Windows half (the write and
// the read-back) is in sqlservice_windows_test.go; this is the fail-closed order,
// which is pure and so runs on every build.
func TestInstallSQLRollsBackWhenTheFolderOwnerIsNotSet(t *testing.T) {
	g := newSQLRig(t)
	g.env.sqlErr = errSQLFolderOwner
	err := g.m.InstallSQL(g.cfg, g.configPath())
	if err == nil {
		t.Fatal("install-sql reported success on a folder nobody had owned")
	}
	if !errors.Is(err, errSQLFolderOwner) {
		t.Fatalf("install error = %v, want the owner sentence so the operator is told to run elevated", err)
	}
	var ie *InstallError
	if !errors.As(err, &ie) || ie.Reason != ReasonFolder {
		t.Fatalf("reason = %#v, want %q: the wizard's folder words name the remedy that works", err, ReasonFolder)
	}
	if g.svc.did("create") != 0 {
		t.Fatal("a service was registered over a folder the service cannot read")
	}
	if len(g.env.markerLocks) != 0 {
		t.Fatalf("an install record was written although the folder was refused: %v", g.env.markerLocks)
	}
	for _, p := range []string{g.sqlDir(), g.cfg.Folder, sqlInstallMarkerPath(g.env.prog)} {
		if _, serr := os.Stat(p); serr == nil {
			t.Errorf("the undo left %s behind", p)
		}
	}
}

// R1-s1/R1-s2: the owner rules on both sides of the install are the same set, and
// the one constant is the shared one. The folder rule in internal/bbsql refuses a
// folder whose owner is outside its trustees (an owner holds implicit WRITE_DAC),
// so the installer must write an owner that rule accepts - and the file rule must
// not accept the account that merely happened to run the install.
func TestOwnerTrustRulesMatchTheFolderRule(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	installer := bbsql.FolderOwnerSID()
	if installer != "S-1-5-32-544" {
		t.Fatalf("the install writes owner %q, want BUILTIN\\Administrators: internal/bbsql names that trustee in its own install DACL", installer)
	}
	for _, owner := range []string{installer, sidSystemSID, sid} {
		if !trustedSQLFolderOwner(owner, sid) {
			t.Errorf("the install refused its own folder owner %q after writing it", owner)
		}
	}
	for _, owner := range []string{"S-1-5-32-545", "S-1-1-0", "S-1-5-21-1-1-1-1001", "", "S-1-5-80-3-142520817-3865290043-129737760-3809223833", strings.ToLower(installer) + "-"} {
		if trustedSQLFolderOwner(owner, sid) {
			t.Errorf("the install accepted folder owner %q, which internal/bbsql refuses", owner)
		}
	}
	if !trustedSQLFolderOwner(strings.ToLower(installer), sid) {
		t.Error("the folder owner test is case-sensitive: a SID read back in the other case is the same account")
	}
	// R1-s2: the file rule is the folder rule minus the installing token, because
	// the installer assigns a file's owner itself and must not leave it to chance.
	if trustedFileOwner("S-1-5-21-1-1-1-1001") {
		t.Error("an install record owned by the account that ran the install was accepted: that owner keeps implicit WRITE_DAC and can re-open the record")
	}
	if !trustedFileOwner(installer) || !trustedFileOwner(sidSystemSID) || trustedFileOwner(strings.ToLower(installer)+"x") {
		t.Error("trustedFileOwner no longer accepts exactly SYSTEM and Administrators")
	}
	// R1-s2, the half that keeps the folder rule and the file rule apart: a
	// Windows owner test cannot see this split drift without it, and the
	// installing token still counts for the objects the installer only ADOPTS
	// (trustedOwner, in sqlservice_windows.go, asserted there).
	self := "S-1-5-21-1-1-1-1001"
	if trustedFileOwner(self) {
		t.Error("the file rule accepted the installing token: the split from the folder rule is what an ordinary owner with implicit WRITE_DAC must not be able to cross")
	}
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
	g.env.verify = func(path string) error {
		if path != g.configPath() {
			return nil // the install record stays locked
		}
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
// the start itself; the SEC-3 install record is proven at install and at
// enable entry, and its proofs never mask the config's.
func TestEnableSQLRechecksConfigBeforeStart(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	configCalls := 0
	g.env.verify = func(path string) error {
		if path != g.configPath() {
			return nil // the install record stays locked
		}
		configCalls++
		if configCalls == 1 {
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
	// Healthy path: install proves the config then the record; enable proves
	// the record and the config at entry, and the config again at the start.
	g2 := newSQLRig(t)
	if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	if err := g2.m.EnableSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	marker := sqlInstallMarkerPath(g2.env.prog)
	want := []string{g2.configPath(), marker, marker, g2.configPath(), g2.configPath()}
	if len(g2.env.verifyCalls) != len(want) {
		t.Fatalf("want proofs %v, got %v", want, g2.env.verifyCalls)
	}
	for i, p := range want {
		if g2.env.verifyCalls[i] != p {
			t.Fatalf("proof %d was asked of %q, want %q (%v)", i, g2.env.verifyCalls[i], p, g2.env.verifyCalls)
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

// SEC-3: the activation folder check must compare the loaded settings against
// the folder persisted at INSTALL time — not against the same config file it
// just loaded (a file compared with itself can never fail). Editing Folder in
// the bound config and enabling with that same edited config must be REFUSED.
func TestEnableSQLRefusesEditedConfigFolder(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	// Edit Folder in the bound settings file (a mistake, or tampering by
	// whoever can write it) and enable with the SAME edited config: the old
	// loaded.Folder != cfg.Folder check compares the file with itself.
	edited := g.cfg
	edited.Folder = filepath.Join(t.TempDir(), "elsewhere")
	if err := SaveConfig(g.configPath(), edited, false); err != nil {
		t.Fatal(err)
	}
	if err := g.m.EnableSQL(edited, g.configPath()); installReason(t, err) != ReasonOther {
		t.Fatalf("enable-sql accepted an edited folder: %v", err)
	}
	if g.svc.did("start") != 0 {
		t.Fatalf("enable-sql started with an edited folder: %v", g.svc.calls)
	}
	// The honest positive case: an UNCHANGED folder still activates.
	g2 := newSQLRig(t)
	if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	if err := g2.m.EnableSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatalf("enable-sql refused the unchanged install folder: %v", err)
	}
	if g2.svc.state != SvcRunning {
		t.Fatalf("enable-sql did not start the honest case: %s", g2.svc.state)
	}
}

// SEC-3: install persists the install-time folder in the install record beside
// the program, locks it to administrators and proves the lock before the
// service is registered.
func TestInstallSQLPersistsInstallFolder(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	marker := sqlInstallMarkerPath(g.env.prog)
	folder, err := readSQLInstallMarker(marker)
	if err != nil || folder != g.cfg.Folder {
		t.Fatalf("install record = %q, %v; want the install folder %q", folder, err, g.cfg.Folder)
	}
	if len(g.env.markerLocks) != 1 || g.env.markerLocks[0] != marker {
		t.Fatalf("the install record was not locked: %v", g.env.markerLocks)
	}
	// The record's admin lock was proven at install, after the config's.
	if len(g.env.verifyCalls) != 2 || g.env.verifyCalls[0] != g.configPath() || g.env.verifyCalls[1] != marker {
		t.Fatalf("install proofs = %v, want [config, record]", g.env.verifyCalls)
	}
}

// SEC-3: an install record that cannot be locked or proven refuses the whole
// install and leaves no record, no folder and no service behind.
func TestInstallSQLUndoUnprovenInstallRecord(t *testing.T) {
	t.Run("the lock fails", func(t *testing.T) {
		g := newSQLRig(t)
		g.env.markerLockErr = errors.New("access denied")
		if err := g.m.InstallSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("create") != 0 {
			t.Fatalf("a service was registered although the record could not be locked: %v", g.svc.calls)
		}
		if _, err := os.Stat(sqlInstallMarkerPath(g.env.prog)); err == nil {
			t.Fatal("the unlocked install record was left behind")
		}
		if _, err := os.Stat(g.sqlDir()); err == nil {
			t.Fatal("the sql folder was left behind")
		}
	})
	t.Run("the proof fails", func(t *testing.T) {
		g := newSQLRig(t)
		marker := sqlInstallMarkerPath(g.env.prog)
		g.env.verify = func(path string) error {
			if path == marker {
				return errors.New("access denied") // unreadable ACL: fail closed
			}
			return nil
		}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("create") != 0 {
			t.Fatalf("a service was registered although the record was not proven: %v", g.svc.calls)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the unproven install record was left behind")
		}
	})
}

// SEC-3: enable-sql fails closed on the install record. Missing, malformed,
// tampered or unverifiable means REFUSED — never a zero value that looks like
// a matching folder. The tampered case also proves the record (not the config
// itself) is the comparison source.
func TestEnableSQLInstallRecordFailClosed(t *testing.T) {
	t.Run("record missing", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(sqlInstallMarkerPath(g.env.prog)); err != nil {
			t.Fatal(err)
		}
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatalf("enable-sql started without an install record: %v", g.svc.calls)
		}
	})
	t.Run("record malformed", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sqlInstallMarkerPath(g.env.prog), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatalf("enable-sql started on a malformed record: %v", g.svc.calls)
		}
	})
	t.Run("record names no folder", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sqlInstallMarkerPath(g.env.prog), []byte(`{"folder":""}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatalf("enable-sql started on an empty record folder: %v", g.svc.calls)
		}
	})
	t.Run("record tampered to another folder", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		// The config is UNCHANGED; the record now names a different folder.
		// The refusal proves the record is actually consulted.
		if err := writeSQLInstallMarker(sqlInstallMarkerPath(g.env.prog), filepath.Join(t.TempDir(), "elsewhere"), g.configPath()); err != nil {
			t.Fatal(err)
		}
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatalf("enable-sql started on a tampered record: %v", g.svc.calls)
		}
	})
	t.Run("record ACL unverifiable", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		marker := sqlInstallMarkerPath(g.env.prog)
		g.env.verify = func(path string) error {
			if path == marker {
				return errors.New("access denied") // unreadable ACL: fail closed
			}
			return nil
		}
		if err := g.m.EnableSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("start") != 0 {
			t.Fatalf("enable-sql started on an unverifiable record: %v", g.svc.calls)
		}
	})
}

// SEC-3: a clean remove takes the install record with the service; a re-install
// writes a fresh one.
func TestRemoveSQLClearsInstallRecord(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatal(err)
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sqlInstallMarkerPath(g.env.prog)); err == nil {
		t.Fatal("the install record survived the removal")
	}
}

// countTrustee names how many entries of one fake object belong to a trustee.
func countTrustee(aces []fileACE, trustee string) int {
	n := 0
	for _, a := range aces {
		if strings.EqualFold(a.Trustee, trustee) {
			n++
		}
	}
	return n
}

// countGrantedTrustee names how many ACCESS-ALLOWED entries of one fake object
// belong to a trustee. A trustee can hold a deny or an entry this code never
// added, so a test that counts what the grant wrote must not include them.
func countGrantedTrustee(aces []fileACE, trustee string) int {
	n := 0
	for _, a := range aces {
		if a.Type == aceTypeAllow && strings.EqualFold(a.Trustee, trustee) {
			n++
		}
	}
	return n
}

// hasACE reports whether an object carries this exact entry.
func hasACE(aces []fileACE, want fileACE) bool {
	for _, a := range aces {
		if a.Type == want.Type && a.Flags == want.Flags && a.Mask == want.Mask && strings.EqualFold(a.Trustee, want.Trustee) {
			return true
		}
	}
	return false
}

// sqlGrantObjectsLeft lists the fake objects that still carry an ACCESS-ALLOWED
// entry for the SQL service SID. A deny is not a grant, so it does not count.
func sqlGrantObjectsLeft(e *fakeSQLEnv, sid string) []string {
	var out []string
	for path, aces := range e.dacls {
		for _, a := range aces {
			if a.Type == 0x0 && strings.EqualFold(a.Trustee, sid) {
				out = append(out, path)
				break
			}
		}
	}
	return out
}

// SEC-6: the launch grant is applied to the same objects on every install, so
// it must be idempotent: an equal entry (same trustee, rights, type and
// inheritance flags) is never added twice — not by a re-install after a failed
// one, and not by a cycle of install/remove/install. Entries that differ in
// any of those are a different grant and must still be added.
func TestSQLLaunchGrantIsIdempotent(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	const unrelated = "S-1-5-32-545" // BUILTIN\Users, an operator-owned entry
	exeOf := func(g *sqlRig) string { return filepath.Join(g.env.prog, InstalledExeName) }

	t.Run("a second grant adds nothing new", func(t *testing.T) {
		g := newSQLRig(t)
		// An entry the operator owns, and one that happens to name the SQL SID
		// with the same rights already: the grant must not duplicate it either.
		g.env.setACL(exeOf(g), append(g.env.acl(exeOf(g)),
			fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskCfg, Trustee: unrelated},
			fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskExe, Trustee: sid}))
		// The first install fails AFTER the grant was applied, so its rollback
		// leaves the entries in place; the second install grants again.
		g.m.Svc = &createFailSvc{fakeSvc: g.svc, err: errors.New("access denied")}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err == nil {
			t.Fatal("install should fail")
		}
		g.m.Svc = g.svc
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		if len(g.env.sqlGranted) != 2 {
			t.Fatalf("the grant ran %d times, want 2", len(g.env.sqlGranted))
		}
		exe, config := exeOf(g), g.configPath()
		for _, want := range []struct {
			path string
			mask uint32
		}{{exe, maskExe}, {config, maskCfg}, {g.env.prog, maskTraverse}} {
			aces := g.env.acl(want.path)
			if !hasACE(aces, fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: want.mask, Trustee: sid}) {
				t.Errorf("%s lost its %08x grant: %v", want.path, want.mask, aces)
			}
			if n := countTrustee(aces, sid); n != 1 {
				t.Errorf("%s carries %d entries for the SQL SID, want exactly 1: %v", want.path, n, aces)
			}
			if !hasACE(aces, fileACE{Type: 0x0, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-18"}) {
				t.Errorf("%s lost its SYSTEM entry: %v", want.path, aces)
			}
		}
		if !hasACE(g.env.acl(exe), fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskCfg, Trustee: unrelated}) {
			t.Errorf("the operator's own entry was disturbed: %v", g.env.acl(exe))
		}
		// The ancestors above the two folders must not be granted at all: the
		// service holds bypass traverse checking, so piling traverse entries on
		// every folder up to the drive root buys nothing and enlarges the
		// surface a bug in this code could touch.
		for p := filepath.Dir(g.env.prog); p != filepath.Dir(p); p = filepath.Dir(p) {
			if n := countTrustee(g.env.dacls[p], sid); n != 0 {
				t.Errorf("the ancestor %s was granted (%d entries)", p, n)
			}
		}
	})

	t.Run("a different entry for the same SID is not skipped", func(t *testing.T) {
		g := newSQLRig(t)
		// The SQL SID already holds the folder-traverse rights on the program.
		// The read/execute grant it needs is a DIFFERENT entry: comparing only
		// the trustee would skip it and the service could not start.
		g.env.setACL(exeOf(g), append(g.env.acl(exeOf(g)),
			fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskTraverse, Trustee: sid}))
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		if !hasACE(g.env.acl(exeOf(g)), fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskExe, Trustee: sid}) {
			t.Fatalf("the read/execute grant was skipped because the SID was already there: %v", g.env.acl(exeOf(g)))
		}
		// An inherited entry with the same rights is not the explicit one either
		// (it disappears when the parent changes), so it must not mask the grant.
		g2 := newSQLRig(t)
		g2.env.setACL(g2.configPath(), append(g2.env.acl(g2.configPath()),
			fileACE{Type: 0x0, Flags: 0x10, Mask: maskCfg, Trustee: sid})) // INHERITED_ACE
		if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
			t.Fatal(err)
		}
		if !hasACE(g2.env.acl(g2.configPath()), fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskCfg, Trustee: sid}) {
			t.Fatalf("the explicit grant was masked by an inherited entry: %v", g2.env.acl(g2.configPath()))
		}
	})
}

// SEC-6: remove-sql revokes exactly what install-sql added. Nothing else on the
// object changes — not the operator's entries, not SYSTEM or Administrators,
// not an inherited entry that merely looks like ours — and no grant survives
// the service it was made for.
func TestRemoveSQLRevokesLaunchGrants(t *testing.T) {
	g := newSQLRig(t)
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	exe, config := exeOfInstall(g), g.configPath()
	const unrelated = "S-1-5-32-545"
	g.env.setACL(exe, append(g.env.acl(exe),
		fileACE{Type: 0x0, Flags: aceFlagsNone, Mask: maskCfg, Trustee: unrelated},
		fileACE{Type: 0x1, Flags: aceFlagsNone, Mask: maskExe, Trustee: sid})) // a DENY, not ours to remove
	if err := g.m.InstallSQL(g.cfg, config); err != nil {
		t.Fatal(err)
	}
	if len(sqlGrantObjectsLeft(g.env, sid)) == 0 {
		t.Fatal("the install granted nothing, so this test proves nothing")
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if left := sqlGrantObjectsLeft(g.env, sid); len(left) != 0 {
		for _, p := range left {
			t.Errorf("%s still carries the SQL SID after remove-sql: %v", p, g.env.acl(p))
		}
	}
	for _, path := range []string{exe, config, g.env.prog} {
		aces := g.env.acl(path)
		for _, must := range []fileACE{
			{Type: 0x0, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-18"},
			{Type: 0x0, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-32-544"},
		} {
			if !hasACE(aces, must) {
				t.Errorf("%s lost %s's entry during the revoke: %v", path, must.Trustee, aces)
			}
		}
	}
	// The operator's own read entry sits on the program only and is untouched.
	if n := countTrustee(g.env.acl(exe), unrelated); n != 1 {
		t.Errorf("the operator's entry on the program was disturbed (%d copies): %v", n, g.env.acl(exe))
	}
	if !hasACE(g.env.acl(exe), fileACE{Type: 0x1, Flags: aceFlagsNone, Mask: maskExe, Trustee: sid}) {
		t.Errorf("the revoke removed a DENY entry for the SQL SID, which install never added: %v", g.env.acl(exe))
	}
	// A re-install after the revoke grants again, cleanly.
	if err := g.m.InstallSQL(g.cfg, config); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{exe, config, g.env.prog} {
		if n := countGrantedTrustee(g.env.acl(path), sid); n != 1 {
			t.Errorf("%s carries %d SQL SID grants after install/remove/install, want 1: %v", path, n, g.env.acl(path))
		}
	}
}

func exeOfInstall(g *sqlRig) string { return filepath.Join(g.env.prog, InstalledExeName) }

// SEC-6: the grant plan is one list, decided in pure Go, that the install applies
// and the removal takes back. It names the two files and the two folders holding
// them — and nothing above them, because a service account holds bypass traverse
// checking and never needs a traverse entry on a folder it only passes through.
func TestSQLLaunchGrantPlan(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	other := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "unravis-blackbox.exe")
	plan, err := sqlLaunchGrants(exe, filepath.Join(dir, ConfigFileName), sid)
	if err != nil {
		t.Fatal(err)
	}
	// The same folder for both files is listed once, not twice.
	if len(plan) != 3 {
		t.Fatalf("plan = %+v, want 3 entries: program, settings, one folder", plan)
	}
	want := map[string]uint32{
		exe:                                maskSQLProgram,
		filepath.Join(dir, ConfigFileName): maskSQLSettings,
		dir:                                maskSQLTraverse,
	}
	for _, gr := range plan {
		mask, ok := want[gr.Path]
		if !ok {
			t.Fatalf("the plan grants %s, which is not part of the launch access", gr.Path)
		}
		if gr.ACE.Mask != mask {
			t.Errorf("%s gets %08x, want %08x", gr.Path, gr.ACE.Mask, mask)
		}
		if gr.ACE.Type != aceTypeAllow || gr.ACE.Flags != aceFlagsExplicit {
			t.Errorf("%s gets an entry of type %d flags %d, want an explicit allow", gr.Path, gr.ACE.Type, gr.ACE.Flags)
		}
		if gr.ACE.Trustee != sid {
			t.Errorf("%s is granted to %s, want the SQL service SID", gr.Path, gr.ACE.Trustee)
		}
		delete(want, gr.Path)
	}
	for p := range want {
		t.Errorf("the plan never grants %s", p)
	}
	// A settings file in another folder adds that folder and still nothing above
	// it: the parent of dir (the temp root) must not appear.
	plan2, err := sqlLaunchGrants(exe, filepath.Join(other, "blackbox.json"), sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2) != 4 {
		t.Fatalf("plan = %+v, want 4 entries", plan2)
	}
	for _, gr := range plan2 {
		if gr.Path == filepath.Dir(dir) || strings.HasPrefix(gr.Path, filepath.Dir(dir)+string(filepath.Separator)+filepath.Base(dir)+string(filepath.Separator)+"..") {
			t.Fatalf("an ancestor above the folder holding the file was granted: %s", gr.Path)
		}
	}
	for _, tc := range []struct{ name, exe, config, sid string }{
		{"relative program", "unravis-blackbox.exe", exe, sid},
		{"relative settings", exe, "blackbox.json", sid},
		{"no service account", exe, exe, ""},
	} {
		if _, err := sqlLaunchGrants(tc.exe, tc.config, tc.sid); err == nil {
			t.Errorf("%s: the plan was accepted, so the grant would land somewhere nobody named", tc.name)
		}
	}
	// The two halves are what remove-sql uses, so a settings path the install
	// record cannot name still leaves the program's own entries revocable.
	prog, err := sqlProgramLaunchGrants(exe, sid)
	if err != nil || len(prog) != 2 || prog[0].ACE.Mask != maskSQLProgram {
		t.Fatalf("program half = %+v, %v", prog, err)
	}
	settings, err := sqlSettingsLaunchGrants(exe, sid)
	if err != nil || len(settings) != 2 || settings[0].ACE.Mask != maskSQLSettings {
		t.Fatalf("settings half = %+v, %v", settings, err)
	}
}

// SEC-6: the two halves of the plan can name the same object (the settings file
// sitting next to the program, the folder holding both). One object is granted
// and revoked once, so a merge must drop the exact repeat and keep every entry
// that differs in any part of the equality.
func TestMergeLaunchGrants(t *testing.T) {
	const sid = "S-1-5-80-1-2-3-4-5"
	gr := func(path string, ace fileACE) sqlLaunchGrant { return sqlLaunchGrant{Path: path, ACE: ace} }
	explicit := func(mask uint32) fileACE {
		return fileACE{Type: aceTypeAllow, Flags: aceFlagsExplicit, Mask: mask, Trustee: sid}
	}
	prog := []sqlLaunchGrant{gr(`C:\p\unravis-blackbox.exe`, explicit(maskSQLProgram)), gr(`C:\p`, explicit(maskSQLTraverse))}
	settings := []sqlLaunchGrant{
		gr(`C:\p\blackbox.json`, explicit(maskSQLSettings)),
		gr(`C:\p\unravis-blackbox.exe`, explicit(maskSQLProgram)),                                 // the same file as the program: once
		gr(`C:\p`, explicit(maskSQLTraverse)),                                                     // the same folder, the same rights: once
		gr(`C:\p`, explicit(maskSQLSettings)),                                                     // same folder, other rights: a different grant
		gr(`C:\p`, fileACE{Type: aceTypeAllow, Flags: 0x03, Mask: maskSQLTraverse, Trustee: sid}), // inherited: different
	}
	merged := mergeLaunchGrants(prog, settings)
	want := []sqlLaunchGrant{
		prog[0], prog[1],
		gr(`C:\p\blackbox.json`, explicit(maskSQLSettings)),
		gr(`C:\p`, explicit(maskSQLSettings)),
		gr(`C:\p`, fileACE{Type: aceTypeAllow, Flags: 0x03, Mask: maskSQLTraverse, Trustee: sid}),
	}
	if len(merged) != len(want) {
		t.Fatalf("merged %d entries, want %d: %+v", len(merged), len(want), merged)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v (order must survive)", i, merged[i], want[i])
		}
	}
	for i, a := range merged {
		for j, b := range merged {
			if i != j && a.Path == b.Path && aceEqual(a.ACE, b.ACE) {
				t.Errorf("entries %d and %d repeat the same grant on %s", i, j, a.Path)
			}
		}
	}
	if got := mergeLaunchGrants(nil, nil, prog); len(got) != len(prog) {
		t.Errorf("merging with empty lists changed the plan: %+v", got)
	}
}

// SEC-6: what counts as the SAME entry. Too loose and a right the service needs
// is skipped; too strict and a repeated install piles up copies. The revoke is
// held to the same equality, so an entry this code did not add can never match it
// and be stripped with it.
func TestACEGrantRules(t *testing.T) {
	const sid = "S-1-5-80-1-2-3-4-5"
	explicit := func(mask uint32, trustee string) fileACE {
		return fileACE{Type: aceTypeAllow, Flags: aceFlagsExplicit, Mask: mask, Trustee: trustee}
	}
	want := explicit(maskSQLProgram, sid)
	lookAlikes := []struct {
		name   string
		have   fileACE
		needed bool
		remove int
	}{
		{"the exact entry is already there", want, false, 1},
		{"the same rights for another trustee", explicit(maskSQLProgram, "S-1-5-32-544"), true, 0},
		{"other rights for this trustee", explicit(maskSQLSettings, sid), true, 0},
		{"the same rights with an inheritance flag", fileACE{Type: aceTypeAllow, Flags: 0x03, Mask: maskSQLProgram, Trustee: sid}, true, 0},
		{"the same rights marked inherited", fileACE{Type: aceTypeAllow, Flags: 0x10, Mask: maskSQLProgram, Trustee: sid}, true, 0},
		{"a deny of the same rights for this trustee", fileACE{Type: 0x1, Flags: aceFlagsExplicit, Mask: maskSQLProgram, Trustee: sid}, true, 0},
		{"an object entry of the same rights", fileACE{Type: 0x5, Flags: aceFlagsExplicit, Mask: maskSQLProgram, Trustee: sid}, true, 0},
	}
	admins := []fileACE{
		{Type: aceTypeAllow, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-18"},
		{Type: aceTypeAllow, Flags: 0x03, Mask: fullControl, Trustee: "S-1-5-32-544"},
	}
	for _, tc := range lookAlikes {
		existing := append(append([]fileACE{}, admins...), tc.have)
		if got := aceGrantNeeded(existing, want); got != tc.needed {
			t.Errorf("%s: aceGrantNeeded = %v, want %v", tc.name, got, tc.needed)
		}
		kept, removed := aceGrantKept(existing, want)
		if removed != tc.remove {
			t.Errorf("%s: aceGrantKept removed %d, want %d", tc.name, removed, tc.remove)
		}
		if len(kept) != len(existing)-tc.remove {
			t.Errorf("%s: kept %d of %d, want %d", tc.name, len(kept), len(existing), len(existing)-tc.remove)
		}
		for _, a := range kept {
			if aceEqual(a, want) {
				t.Errorf("%s: the entry that was counted as removed survived: %+v", tc.name, a)
			}
		}
		for _, a := range existing {
			if aceEqual(a, want) {
				continue
			}
			found := false
			for _, k := range kept {
				if k == a {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: an entry this rule may not touch disappeared: %+v", tc.name, a)
			}
		}
	}
	// Every look-alike at once, plus exactly one entry that IS ours (the first
	// row of the table is that entry, so it is not added a second time): the
	// revoke takes only our own entry, in order, and touches nothing else.
	existing := append(append([]fileACE{}, admins...), want)
	for _, tc := range lookAlikes[1:] {
		existing = append(existing, tc.have)
	}
	kept, removed := aceGrantKept(existing, want)
	if removed != 1 || len(kept) != len(existing)-1 {
		t.Fatalf("removed %d of a list holding one exact entry (%d entries kept of %d)", removed, len(kept), len(existing))
	}
	at := 0
	for i, a := range existing {
		if aceEqual(a, want) {
			continue // the one entry the revoke was entitled to drop
		}
		if kept[at] != a {
			t.Fatalf("entry %d was reordered or replaced: %+v vs %+v", i, kept[at], a)
		}
		at++
	}
	if !aceEqual(fileACE{Trustee: "S-1-5-18"}, fileACE{Trustee: "s-1-5-18"}) {
		t.Error("a trustee SID comparison must not depend on how it was spelled")
	}
}

// SEC-6: an access list that cannot be read is never treated as an empty one —
// not when granting (nothing is written, no service is registered) and not when
// revoking (nothing is rewritten, and the failure is reported instead of a
// silently "clean" removal).
func TestSQLLaunchACLUnreadableFailsClosed(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("install refuses", func(t *testing.T) {
		g := newSQLRig(t)
		g.env.unreadable = map[string]bool{g.env.prog: true}
		if err := g.m.InstallSQL(g.cfg, g.configPath()); installReason(t, err) != ReasonOther {
			t.Fatalf("%v", err)
		}
		if g.svc.did("create") != 0 {
			t.Fatalf("a service was registered although an ACL could not be read: %v", g.svc.calls)
		}
		for _, p := range []string{g.sqlDir(), g.cfg.Folder} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("left behind: %s", p)
			}
		}
		for path, aces := range g.env.dacls {
			if countTrustee(aces, sid) != 0 {
				t.Errorf("%s was granted from an ACL that could not be read: %v", path, aces)
			}
		}
	})
	t.Run("remove reports", func(t *testing.T) {
		g := newSQLRig(t)
		if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
			t.Fatal(err)
		}
		g.env.unreadable = map[string]bool{exeOfInstall(g): true}
		err := g.m.RemoveSQL(g.cfg, g.cfg.Name)
		if installReason(t, err) != ReasonOther {
			t.Fatalf("remove-sql reported success although it could not read an ACL: %v", err)
		}
		if g.svc.did("delete") != 1 || g.svc.state != SvcNotInstalled {
			t.Fatalf("the service itself was not removed: %v %s", g.svc.calls, g.svc.state)
		}
		// The record belonged to the removed service and went with it; the object
		// that could not be read keeps whatever it had, untouched.
		if _, err := os.Stat(sqlInstallMarkerPath(g.env.prog)); err == nil {
			t.Fatal("the install record survived the removal")
		}
		if !hasACE(g.env.acl(exeOfInstall(g)), fileACE{Type: aceTypeAllow, Flags: aceFlagsExplicit, Mask: maskSQLProgram, Trustee: sid}) {
			t.Fatalf("an unreadable object was rewritten anyway: %v", g.env.acl(exeOfInstall(g)))
		}
		if _, err := os.Stat(filepath.Join(g.sqlDir(), "status.json")); err != nil {
			t.Error("a clean remove must still publish the off status")
		}
	})
}

// SEC-6: the install record names the settings file the grant was applied to, so
// remove-sql revokes THAT file. A record that does not name one — written before
// this packet — leaves the settings file exactly as it is: nothing is guessed at,
// and the program's own entries are still taken back.
func TestRemoveSQLRevokesTheRecordedConfig(t *testing.T) {
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		t.Fatal(err)
	}
	g := newSQLRig(t)
	other := filepath.Join(t.TempDir(), "other-blackbox.json")
	if err := SaveConfig(other, g.cfg, false); err != nil {
		t.Fatal(err)
	}
	if err := g.m.InstallSQL(g.cfg, other); err != nil {
		t.Fatal(err)
	}
	rec, err := readSQLInstallRecord(sqlInstallMarkerPath(g.env.prog))
	if err != nil || rec.Config != other {
		t.Fatalf("install record = %+v, %v; want the bound settings file %q", rec, err, other)
	}
	if err := g.m.RemoveSQL(g.cfg, g.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if len(g.env.sqlRevoked) != 1 {
		t.Fatalf("the revoke seam ran %d times, want 1", len(g.env.sqlRevoked))
	}
	var sawSettings, sawSettingsDir bool
	for _, gr := range g.env.sqlRevoked[0] {
		if gr.Path == other {
			sawSettings = true
		}
		if gr.Path == filepath.Dir(other) {
			sawSettingsDir = true
		}
	}
	if !sawSettings || !sawSettingsDir {
		t.Fatalf("the revoke did not name the bound settings file and its folder: %+v", g.env.sqlRevoked[0])
	}
	if countTrustee(g.env.acl(other), sid) != 0 || countTrustee(g.env.acl(exeOfInstall(g)), sid) != 0 {
		t.Fatalf("a grant survived the removal: %v / %v", g.env.acl(other), g.env.acl(exeOfInstall(g)))
	}
	// An older record: no settings path, so only the program half may be revoked.
	g2 := newSQLRig(t)
	if err := g2.m.InstallSQL(g2.cfg, g2.configPath()); err != nil {
		t.Fatal(err)
	}
	if err := writeSQLInstallMarker(sqlInstallMarkerPath(g2.env.prog), g2.cfg.Folder, ""); err != nil {
		t.Fatal(err)
	}
	if err := g2.m.RemoveSQL(g2.cfg, g2.cfg.Name); err != nil {
		t.Fatal(err)
	}
	if countTrustee(g2.env.acl(exeOfInstall(g2)), sid) != 0 {
		t.Errorf("the program's entries were not revoked: %v", g2.env.acl(exeOfInstall(g2)))
	}
	if countTrustee(g2.env.acl(g2.configPath()), sid) != 1 {
		t.Errorf("a settings file the record cannot name was touched anyway: %v", g2.env.acl(g2.configPath()))
	}
	// A record naming a relative settings path is unusable for the revoke, so the
	// path is dropped rather than trusted.
	rec2 := filepath.Join(t.TempDir(), "sql-install.json")
	if err := writeSQLInstallMarker(rec2, g2.cfg.Folder, "blackbox.json"); err != nil {
		t.Fatal(err)
	}
	got, err := readSQLInstallRecord(rec2)
	if err != nil || got.Config != "" || got.Folder != g2.cfg.Folder {
		t.Fatalf("record = %+v, %v; want the folder and no usable settings path", got, err)
	}
}

// REG-1: install-sql must bind a settings file it has ADMIN-LOCKED, and the
// owner half of that lock is the installer's own job. Nothing in this tree ever
// assigned an owner to blackbox.json — the only owner writer is the descriptor
// write the install record and the SQL data folder use — so on a real host the
// settings file is owned by the account that ran the install, exactly the owner
// trustedFileOwner refuses on purpose. Proving that file before anything pins it
// therefore refused a perfectly healthy host with advice the operator could not
// act on. The fake now models the owner, so the code has to do the assigning.
func TestInstallSQLLocksTheSettingsFileItBinds(t *testing.T) {
	if trustedFileOwner(sidInstallingUser) {
		t.Fatal("the fake's default owner is trusted: the owner half of the proof can no longer fail")
	}
	g := newSQLRig(t)
	if got := g.env.ownerOf(g.configPath()); got != sidInstallingUser {
		t.Fatalf("the settings file the installer wrote is owned by %q, want the installing account", got)
	}
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatalf("install-sql refused a healthy host: %v", err)
	}
	// The lock step actually ran, on the settings file, exactly once.
	if len(g.env.settingsLocks) != 1 || g.env.settingsLocks[0] != g.configPath() {
		t.Fatalf("settings-file locks = %v, want exactly [%s]", g.env.settingsLocks, g.configPath())
	}
	// The prove step actually ran, after the lock: the bound settings file, then
	// the install record. A no-op seam could not satisfy this.
	marker := sqlInstallMarkerPath(g.env.prog)
	want := []string{g.configPath(), marker}
	if len(g.env.verifyCalls) != len(want) {
		t.Fatalf("install proofs = %v, want %v", g.env.verifyCalls, want)
	}
	for i, p := range want {
		if g.env.verifyCalls[i] != p {
			t.Fatalf("proof %d was asked of %q, want %q (%v)", i, g.env.verifyCalls[i], p, g.env.verifyCalls)
		}
	}
	// The file the service is bound to now carries the owner the rule demands,
	// so a later enable-sql proves the same file the install locked.
	if owner := g.env.ownerOf(g.configPath()); !trustedFileOwner(owner) {
		t.Fatalf("the settings file is still owned by %q after install-sql", owner)
	}
}

// REG-1 must repair, not re-allow: a settings file whose owner lock will not
// land is still a refusal, and it still refuses before anything is registered.
func TestInstallSQLRefusesWhenTheSettingsFileCannotBeLocked(t *testing.T) {
	g := newSQLRig(t)
	g.env.settingsLockErr = errors.New("SetNamedSecurityInfo failed")
	err := g.m.InstallSQL(g.cfg, g.configPath())
	if installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	// The repair was attempted on the settings file the service would bind — the
	// refusal below is a failed lock, not a step that was never taken.
	if len(g.env.settingsLocks) != 1 || g.env.settingsLocks[0] != g.configPath() {
		t.Fatalf("settings-file locks = %v, want exactly [%s]", g.env.settingsLocks, g.configPath())
	}
	var ie *InstallError
	if !errors.As(err, &ie) || ie.Cause == nil || !strings.Contains(ie.Cause.Error(), "SetNamedSecurityInfo") {
		t.Fatalf("the refused install must name the lock that failed, not swallow it: %v", err)
	}
	// Nothing downstream was bound on a config that could not be locked, and the
	// install never reaches the point where it trusts the settings file.
	if len(g.svc.calls) != 0 || len(g.env.sqlLocked) != 0 || len(g.env.sqlGranted) != 0 || len(g.env.markerLocks) != 0 {
		t.Fatalf("something was changed on a config that could not be locked: %v %v %v %v",
			g.svc.calls, g.env.sqlLocked, g.env.sqlGranted, g.env.markerLocks)
	}
}

// REG-1: enable-sql on a host install-sql completed must start the service. The
// settings owner is pinned ONCE, at install: activation deliberately proves
// without repairing, so a settings file swapped or widened after install-sql is
// still refused instead of silently fixed.
func TestEnableSQLSucceedsOnAHealthyHost(t *testing.T) {
	g := newSQLRig(t)
	if err := g.m.InstallSQL(g.cfg, g.configPath()); err != nil {
		t.Fatalf("install-sql refused a healthy host: %v", err)
	}
	if err := g.m.EnableSQL(g.cfg, g.configPath()); err != nil {
		t.Fatalf("enable-sql refused a healthy host: %v", err)
	}
	if g.svc.state != SvcRunning || g.svc.did("start") != 1 {
		t.Fatalf("enable-sql did not start the service: %v %s", g.svc.calls, g.svc.state)
	}
	marker := sqlInstallMarkerPath(g.env.prog)
	if len(g.env.settingsLocks) != 1 || g.env.settingsLocks[0] != g.configPath() {
		t.Fatalf("settings-file locks = %v, want the install's single lock on %s", g.env.settingsLocks, g.configPath())
	}
	// install proves the config then the record; enable proves the record, the
	// config at entry and the config again immediately before the start.
	want := []string{g.configPath(), marker, marker, g.configPath(), g.configPath()}
	if len(g.env.verifyCalls) != len(want) {
		t.Fatalf("proofs = %v, want %v", g.env.verifyCalls, want)
	}
	for i, p := range want {
		if g.env.verifyCalls[i] != p {
			t.Fatalf("proof %d was asked of %q, want %q (%v)", i, g.env.verifyCalls[i], p, g.env.verifyCalls)
		}
	}
}
