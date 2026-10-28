package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"api-audit-explorer/internal/keeper"
)

// junction makes a link at link that points to target. The test is skipped where the PC does not allow one.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("cannot make a junction here: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
}

func mustFail(t *testing.T, res Result, want string) {
	t.Helper()
	if res.Outcome == OutcomeDone {
		t.Fatalf("setup went through, want a refusal containing %q", want)
	}
	var all strings.Builder
	all.WriteString(res.Message)
	for _, c := range res.Checks {
		all.WriteString(" | " + c.Detail)
	}
	if !strings.Contains(all.String(), want) {
		t.Fatalf("refusal %q does not say %q", all.String(), want)
	}
}

// ---- F2: links and owners ----

func TestJournalFolderThatIsALinkIsRefused(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(e.root, "elsewhere")
	junction(t, dir, target)
	_, err := e.r.ensureJournalDir(true)
	if err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("want a link refusal, got %v", err)
	}
	if s, _ := e.f.Sec.SDDL(target); s != DefaultUserSDDL {
		t.Fatalf("the link target was given a new list: %s", s)
	}
}

func TestJournalFolderOfAnotherAccountIsRefused(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { // a folder a person made: the fake gives it to the person
		t.Fatal(err)
	}
	if _, err := e.r.ensureJournalDir(true); err == nil || !strings.Contains(err.Error(), "another account") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if s, _ := e.f.Sec.SDDL(dir); s != DefaultUserSDDL {
		t.Fatalf("the folder's list was changed: %s", s)
	}
	// An administrator-owned folder is taken over and locked.
	if err := e.f.Sec.SetSDDL(dir, "O:BAD:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;BU)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.ensureJournalDir(true); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.f.Sec.SDDL(dir); s != AdminOnlySDDL {
		t.Fatalf("the folder was not locked: %s", s)
	}
}

func TestSharedKeeperAndProgramFoldersThatAreLinksAreRefused(t *testing.T) {
	l := func(e *testEnv) Layout { return NewLayout(e.machineOpts(), e.dirs) }
	cases := []struct {
		name string
		path func(Layout) string
		want string
	}{
		{"shared", func(l Layout) string { return l.SharedRoot }, "link"},
		{"keeper", func(l Layout) string { return l.KeeperDir }, "link"},
		{"program", func(l Layout) string { return l.InstallDir }, "link"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			target := filepath.Join(e.root, "elsewhere")
			path := c.path(l(e))
			op := OpInstall
			if c.name == "keeper" { // the keeper folder is inside the shared folder, which must be a real one first
				e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				op = OpRepair
			}
			junction(t, path, target)
			res := e.run(op, e.machineOpts(), UninstallOptions{})
			mustFail(t, res, c.want)
			if s, _ := e.f.Sec.SDDL(target); s != DefaultUserSDDL {
				t.Fatalf("the link target was given a new list: %s", s)
			}
			if strings.Contains(strings.ToLower(e.f.Sec.Snapshot()), strings.ToLower(target)) {
				t.Fatalf("a list was set on the link target:\n%s", e.f.Sec.Snapshot())
			}
		})
	}
}

// ---- F3: an existing program folder for everyone ----

func installFolderCheck(t *testing.T, e *testEnv) (Check, bool) {
	t.Helper()
	return checkByID(e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.machineOpts()}), "install-folder")
}

func TestMachineInstallRefusesAFolderWithOtherFiles(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := NewLayout(e.machineOpts(), e.dirs).InstallDir
	writeFile(t, filepath.Join(dir, "notes.txt"), "someone's notes")
	if err := e.f.Sec.SetSDDL(dir, ProgramFolderSDDL); err != nil {
		t.Fatal(err)
	}
	c, ok := installFolderCheck(t, e)
	if !ok || c.Status != CheckFailed || !strings.Contains(c.Detail, "not made by the Unravis installer") {
		t.Fatalf("check %+v", c)
	}
	res := e.run(OpInstall, e.machineOpts(), UninstallOptions{})
	mustFail(t, res, "not made by the Unravis installer")
	if s, _ := e.f.Sec.SDDL(dir); s != ProgramFolderSDDL {
		t.Fatalf("the folder's list was changed: %s", s)
	}
}

func TestMachineInstallEmptyFolderNeedsAdministratorOwner(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := NewLayout(e.machineOpts(), e.dirs).InstallDir
	if err := os.MkdirAll(dir, 0o755); err != nil { // made by a person
		t.Fatal(err)
	}
	c, ok := installFolderCheck(t, e)
	if !ok || c.Status != CheckFailed || !strings.Contains(c.Detail, "not an administrator") {
		t.Fatalf("check %+v", c)
	}
	if err := e.f.Sec.SetSDDL(dir, AdminOnlySDDL); err != nil {
		t.Fatal(err)
	}
	if c, ok := installFolderCheck(t, e); ok && c.Status == CheckFailed {
		t.Fatalf("an empty folder only administrators own was refused: %+v", c)
	}
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
}

func TestMachineInstallTakesOverItsOwnEarlierFolder(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	// Registry forgotten, folder and its setup-state.json left behind: the same installer made it.
	if c, ok := installFolderCheck(t, e); ok && c.Status == CheckFailed {
		t.Fatalf("this installer's own folder was refused: %+v", c)
	}
}

func TestMachineInstallRefusesAFolderInsideAProfile(t *testing.T) {
	e := newEnv(t, "1.2.0")
	for _, loc := range []string{
		filepath.Join(e.dirs.UserProfile, "Tools", "Unravis"),
		filepath.Join(filepath.Dir(e.dirs.UserProfile), "Bob", "Unravis"),
	} {
		o := e.machineOpts()
		o.Location = loc
		if _, err := o.Validated(e.dirs, nil); err == nil || !strings.Contains(err.Error(), "belongs to one person") {
			t.Errorf("%s was accepted (%v)", loc, err)
		}
	}
}

// ---- F4: another person's Unravis ----

func appPath(e *testEnv) string { return NewLayout(e.machineOpts(), e.dirs).AppExe }

func addApp(e *testEnv, sid string) uint32 {
	pid := e.f.Proc.Run(appPath(e))
	e.f.Proc.mu.Lock()
	defer e.f.Proc.mu.Unlock()
	for i := range e.f.Proc.List_ {
		if e.f.Proc.List_[i].PID == pid {
			e.f.Proc.List_[i].SID = sid
		}
	}
	return pid
}

func running(e *testEnv) map[uint32]bool {
	list, _ := e.f.Proc.List()
	m := map[uint32]bool{}
	for _, q := range list {
		m[q.PID] = true
	}
	return m
}

func TestAnotherPersonsUnravisIsNeverClosed(t *testing.T) {
	for _, op := range []Op{OpUpgrade, OpRepair, OpUninstall} {
		t.Run(string(op), func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			priorInstall(func(e *testEnv) Options { return e.machineOpts() })(e)
			theirs := addApp(e, "S-1-5-21-2000")
			mine := addApp(e, e.r.UserSID)
			o := e.machineOpts()
			o.CloseRunningApp = true
			un := UninstallOptions{}
			checks := e.r.Preflight(PreflightInput{Op: op, Opts: o, Old: mustDetect(t, e)})
			c, ok := checkByID(checks, "app-running")
			if !ok || c.Status != CheckFailed || c.Detail != "Unravis is open for another person on this PC." {
				t.Fatalf("check %+v", c)
			}
			res := e.run(op, o, un)
			mustFail(t, res, "Unravis is open for another person on this PC")
			if r := running(e); !r[theirs] || !r[mine] {
				t.Fatalf("a program was closed: %v", r)
			}
		})
	}
}

func TestOwnUnravisIsClosedAfterTheYes(t *testing.T) {
	e := newEnv(t, "1.2.0")
	priorInstall(func(e *testEnv) Options { return e.machineOpts() })(e)
	mine := addApp(e, strings.ToLower(e.r.UserSID))
	plain := e.f.Proc.Run(appPath(e)) // the list did not say whose: the person's
	o := e.machineOpts()
	res := e.run(OpUpgrade, o, UninstallOptions{})
	mustFail(t, res, "Unravis is open")
	if r := running(e); !r[mine] || !r[plain] {
		t.Fatalf("closed without the yes: %v", r)
	}
	o.CloseRunningApp = true
	e.mustRun(OpUpgrade, o, UninstallOptions{})
	if r := running(e); r[mine] || r[plain] {
		t.Fatalf("own programs still running: %v", r)
	}
}

func TestAnOwnerThatCannotBeReadCountsAsAnotherPerson(t *testing.T) {
	e := newEnv(t, "1.2.0")
	mine, others := e.r.splitApps([]Process{{PID: 1, SID: UnknownOwner}, {PID: 2, SID: e.r.UserSID}, {PID: 3}})
	if len(mine) != 2 || len(others) != 1 || others[0].PID != 1 {
		t.Fatalf("mine %v others %v", mine, others)
	}
}

// ---- F7: spelling and aliases ----

func TestFoldersThatWindowsWouldReadDifferentlyAreRefused(t *testing.T) {
	e := newEnv(t, "1.2.0")
	base := e.machineOpts()
	for name, loc := range map[string]string{
		"dot":   filepath.Join(e.dirs.ProgramFiles, "Unravis."),
		"space": filepath.Join(e.dirs.ProgramFiles, "Unravis "),
		"inner": filepath.Join(e.dirs.ProgramFiles, "Unravis.", "App"),
	} {
		o := base
		o.Location = loc
		if err := o.Validate(e.dirs); err == nil || !strings.Contains(err.Error(), "dot or a space") {
			t.Errorf("%s: %q was accepted (%v)", name, loc, err)
		}
	}
	o := base
	o.Blackbox = e.withBlackbox(base, false).Blackbox
	o.Blackbox.Folder += "."
	if err := o.Validate(e.dirs); err == nil {
		t.Error("a recordings folder ending in a dot was accepted")
	}
	o = e.withBlackbox(base, false)
	o.ReadKeyFile = filepath.Join(e.dirs.UserProfile, "key.txt ")
	if err := o.Validate(e.dirs); err == nil {
		t.Error("a read key file folder ending in a space was accepted")
	}
	if err := base.Validate(e.dirs); err != nil {
		t.Errorf("the normal folder was refused: %v", err)
	}
}

type aliasPaths struct{ m map[string]string }

func (a aliasPaths) Canonical(p string) (string, error) {
	c := filepath.Clean(p)
	if t, ok := a.m[strings.ToLower(c)]; ok {
		return t, nil
	}
	return c, nil
}

func TestAnAliasIntoASystemFolderIsRefused(t *testing.T) {
	e := newEnv(t, "1.2.0")
	short := filepath.Join(e.dirs.UserProfile, "WINDOW~1", "Unravis") // an 8.3 name that resolves into the Windows folder
	alias := aliasPaths{m: map[string]string{strings.ToLower(short): filepath.Join(e.dirs.Windows, "System32", "Unravis")}}
	o := e.userOpts()
	o.Location = short
	if _, err := o.Validated(e.dirs, alias); err == nil || !strings.Contains(err.Error(), "outside the Windows folder") {
		t.Fatalf("alias accepted: %v", err)
	}
	// The same spelling with no alias is an ordinary folder.
	if _, err := o.Validated(e.dirs, nil); err != nil {
		t.Fatalf("control refused: %v", err)
	}
	// The machine's own folders are canonicalised too: an alias of the profile folder is still the profile.
	m := e.machineOpts()
	m.Location = filepath.Join(e.root, "Short", "Unravis")
	profAlias := aliasPaths{m: map[string]string{strings.ToLower(m.Location): filepath.Join(e.dirs.UserProfile, "Unravis")}}
	if _, err := m.Validated(e.dirs, profAlias); err == nil || !strings.Contains(err.Error(), "belongs to one person") {
		t.Fatalf("profile alias accepted: %v", err)
	}
}

func TestPlanUsesTheCanonicalFolder(t *testing.T) {
	e := newEnv(t, "1.2.0")
	short := filepath.Join(e.dirs.UserProfile, "APPS~1", "Unravis")
	real := filepath.Join(e.dirs.UserProfile, "Apps", "Unravis")
	e.sys.Paths = aliasPaths{m: map[string]string{strings.ToLower(short): real}}
	o := e.userOpts()
	o.Location = short
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Opts.Location != real || p.Layout.InstallDir != real {
		t.Fatalf("plan works on %q (%q)", p.Opts.Location, p.Layout.InstallDir)
	}
}

// ---- F8: the pipe ----

func TestWorkerPipeIsForThePersonAndTheSystemOnly(t *testing.T) {
	const sid = "S-1-5-21-1000"
	fs, err := ParseSDDL(WorkerPipeSDDL(sid))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, a := range fs.ACEs {
		got[a.SID] = true
	}
	if len(fs.ACEs) != 2 || !got["S-1-5-18"] || !got[sid] || got["S-1-5-32-544"] {
		t.Fatalf("%s -> %+v", WorkerPipeSDDL(sid), fs.ACEs)
	}
	if strings.Contains(WorkerPipeSDDL(sid), "BA") {
		t.Fatal("the pipe list names administrators")
	}
}

// ---- F9: the read key file ----

func readKeyOpts(e *testEnv, file string) Options {
	o := e.withBlackbox(e.machineOpts(), false)
	o.ReadKeyFile = file
	return o
}

func TestReadKeyFileThatExistsIsNotReplaced(t *testing.T) {
	e := newEnv(t, "1.2.0")
	f := filepath.Join(e.dirs.UserProfile, "Desktop", "key.txt")
	writeFile(t, f, "the person's own file")
	o := readKeyOpts(e, f)
	checks := e.r.Preflight(PreflightInput{Op: OpInstall, Opts: o})
	if c, ok := checkByID(checks, "read-key-file"); !ok || c.Status != CheckFailed {
		t.Fatalf("check %+v", c)
	}
	res := e.run(OpInstall, o, UninstallOptions{})
	mustFail(t, res, "already there")
	if b, _ := os.ReadFile(f); string(b) != "the person's own file" {
		t.Fatalf("file changed: %q", b)
	}
}

func TestReadKeyFileIsMadeWithItsListAndRolledBack(t *testing.T) {
	e := newEnv(t, "1.2.0")
	f := filepath.Join(e.dirs.UserProfile, "Desktop", "key.txt")
	o := readKeyOpts(e, f)
	e.mustRun(OpInstall, o, UninstallOptions{})
	if s, _ := e.f.Sec.SDDL(f); s != ReadKeyFileSDDL(e.r.UserSID) {
		t.Fatalf("list %q", s)
	}
	// A later step fails: the file goes away with everything else.
	e2 := newEnv(t, "1.2.0")
	f2 := filepath.Join(e2.dirs.UserProfile, "Desktop", "key.txt")
	before := e2.snapshot()
	o2 := e2.withBlackbox(e2.machineOpts(), true)
	o2.ReadKeyFile = f2
	e2.f.Faults.Set("fw.Add:"+FirewallRuleName(o2.Blackbox.Port), os.ErrPermission)
	res := e2.run(OpInstall, o2, UninstallOptions{})
	if res.Outcome == OutcomeDone {
		t.Fatal("the install went through with a failing firewall")
	}
	if exists(f2) {
		t.Fatal("the key file was left behind")
	}
	if d := diffSnap(before, e2.snapshot()); d != "" {
		t.Fatalf("not put back:\n%s", d)
	}
}

func TestCreateFileSDDLNeverReplaces(t *testing.T) {
	e := newEnv(t, "1.2.0")
	f := filepath.Join(e.root, "k.txt")
	writeFile(t, f, "old")
	if err := e.f.Sec.CreateFileSDDL(f, ReadKeyFileSDDL(e.r.UserSID), []byte("new")); !os.IsExist(err) {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(f); string(b) != "old" {
		t.Fatalf("file changed: %q", b)
	}
}

// ---- F10: the keeper folder ----

func TestKeeperFolderListIsPutBackWhenDifferent(t *testing.T) {
	k := keeper.ServiceSID(keeper.ServiceName)
	base := "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + k + ")"
	cases := []struct {
		name  string
		sddl  string
		reset bool
	}{
		{"extra reader", base + "(A;OICI;GR;;;BU)", true},
		{"keeper missing", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true},
		{"weaker rights", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GR;;;" + k + ")", true},
		{"inherited entry", "O:BAD:AI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + k + ")(A;OICIID;FA;;;BU)", true},
		{"other owner", "O:S-1-5-21-1000D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + k + ")", true},
		{"same entries, other order", "O:BAD:P(A;OICI;FA;;;" + k + ")(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			priorInstall(func(e *testEnv) Options { return e.machineOpts() })(e)
			dir := NewLayout(e.machineOpts(), e.dirs).KeeperDir
			if err := e.f.Sec.SetSDDL(dir, c.sddl); err != nil {
				t.Fatal(err)
			}
			e.mustRun(OpRepair, e.machineOpts(), UninstallOptions{})
			got, _ := e.f.Sec.SDDL(dir)
			want := KeeperFolderSDDL()
			if !c.reset {
				want = c.sddl
			}
			if got != want {
				t.Fatalf("list is %q, want %q", got, want)
			}
		})
	}
}
