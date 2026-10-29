package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/keeper"
)

func t0ctx() context.Context { return context.Background() }

func checkByID(checks []Check, id string) (Check, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

// ---- preflight ----

func TestPreflightCleanMachineIsReady(t *testing.T) {
	e := newEnv(t, "1.2.0")
	checks := e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.withBlackbox(e.machineOpts(), true)})
	if Blocked(checks) {
		t.Fatalf("a clean PC is blocked: %+v", checks)
	}
	for _, id := range []string{"windows", "payload", "existing", "browser", "webview2", "disk-app", "disk-blackbox", "port", "recordings-folder", "admin", "blackbox-service"} {
		if _, ok := checkByID(checks, id); !ok {
			t.Errorf("preflight has no %q line", id)
		}
	}
}

func TestPreflightRules(t *testing.T) {
	vol := func(e *testEnv) string { return strings.ToUpper(filepath.VolumeName(e.root)) }
	cases := []struct {
		name   string
		id     string
		want   CheckStatus
		op     Op
		user   bool
		bb     bool
		silent bool
		setup  func(e *testEnv) PreflightInput
	}{
		{name: "Windows 10 1607 is too old", id: "windows", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Version = WindowsVersion{Major: 10, Build: 14393, Is64: true}
			return PreflightInput{}
		}},
		{name: "Windows 10 1809 is fine", id: "windows", want: CheckReady, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Version = WindowsVersion{Major: 10, Build: 17763, Is64: true}
			return PreflightInput{}
		}},
		{name: "32-bit Windows", id: "windows", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Version.Is64 = false
			return PreflightInput{}
		}},
		{name: "no approval possible for everyone", id: "admin", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Admin, e.f.Mach.Elevate = false, false
			return PreflightInput{}
		}},
		{name: "just for me needs no approval", id: "admin", want: CheckReady, user: true, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Admin, e.f.Mach.Elevate = false, false
			return PreflightInput{}
		}},
		{name: "just for me with the Blackbox needs approval", id: "admin", want: CheckFailed, user: true, bb: true, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Admin, e.f.Mach.Elevate = false, false
			return PreflightInput{}
		}},
		{name: "port in use", id: "port", want: CheckFailed, bb: true, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.BusyPorts = map[int]bool{7788: true}
			return PreflightInput{}
		}},
		{name: "too little space for the program", id: "disk-app", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Free = map[string]uint64{vol(e): 50 << 20}
			return PreflightInput{}
		}},
		{name: "too little space for recordings", id: "disk-blackbox", want: CheckFailed, bb: true, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Free = map[string]uint64{vol(e): 500 << 20}
			return PreflightInput{}
		}},
		{name: "drive not on this PC", id: "disk-app", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Drives = map[string]bool{`Q:\`: true}
			return PreflightInput{}
		}},
		{name: "another setup running", id: "other-setup", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			release, err := e.sys.Lock()
			if err != nil {
				e.t.Fatal(err)
			}
			e.t.Cleanup(release)
			return PreflightInput{}
		}},
		{name: "Blackbox already installed", id: "blackbox-service", want: CheckFailed, bb: true, setup: func(e *testEnv) PreflightInput {
			e.f.Svc.Put(ServiceSpec{Name: blackbox.ServiceName}, blackbox.SvcRunning)
			return PreflightInput{}
		}},
		{name: "recordings folder is a file", id: "recordings-folder", want: CheckFailed, bb: true, setup: func(e *testEnv) PreflightInput {
			writeFile(e.t, filepath.Join(e.root, "D", "UnravisBlackbox"), "x")
			return PreflightInput{}
		}},
		{name: "pending restart is a warning", id: "reboot", want: CheckPartly, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.Reboot = true
			return PreflightInput{}
		}},
		{name: "no browser is a warning", id: "browser", want: CheckPartly, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.BrowserPath = ""
			return PreflightInput{}
		}},
		{name: "no WebView2 is fine", id: "webview2", want: CheckReady, setup: func(e *testEnv) PreflightInput {
			e.f.Mach.HasWebView2 = false
			return PreflightInput{}
		}},
		{name: "payload changed", id: "payload", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			e.r.Payload.Manifest.Files[0].SHA256 = strings.Repeat("0", 64)
			return PreflightInput{}
		}},
		{name: "shared folder not made by setup", id: "shared-folder", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			writeFile(e.t, filepath.Join(e.dirs.ProgramData, SharedFolderName, "someone.txt"), "theirs")
			return PreflightInput{}
		}},
		{name: "program folder holds other files", id: "install-folder", want: CheckFailed, setup: func(e *testEnv) PreflightInput {
			writeFile(e.t, filepath.Join(e.dirs.ProgramFiles, "Unravis", "notes.txt"), "theirs")
			return PreflightInput{}
		}},
		{name: "upgrade with nothing installed", id: "existing", want: CheckFailed, op: OpUpgrade, setup: func(e *testEnv) PreflightInput {
			return PreflightInput{}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			o := e.machineOpts()
			if c.user {
				o = e.userOpts()
			}
			if c.bb {
				o = e.withBlackbox(o, false)
			}
			in := c.setup(e)
			in.Op, in.Opts, in.Silent = OpInstall, o, c.silent
			if c.op != "" {
				in.Op = c.op
			}
			checks := e.r.Preflight(in)
			got, ok := checkByID(checks, c.id)
			if !ok {
				t.Fatalf("no %q line in %+v", c.id, checks)
			}
			if got.Status != c.want {
				t.Fatalf("%s = %s (%s), want %s", c.id, got.Status, got.Detail, c.want)
			}
			if got.Status == CheckFailed && got.Fix == "" {
				t.Errorf("%s failed without a fix", c.id)
			}
			if c.want != CheckFailed && Blocked(checks) {
				t.Errorf("a warning blocked setup: %+v", checks)
			}
		})
	}
}

func TestPreflightExistingInstall(t *testing.T) {
	e := newEnv(t, "2.0.0")
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	old, _ := Detect(e.sys.Reg)
	e.r.Payload = testPayload(t, "1.2.0", true)

	c, _ := checkByID(e.r.Preflight(PreflightInput{Op: OpUpgrade, Old: old}), "existing")
	if c.Status != CheckFailed || !strings.Contains(c.Fix, "--allow-downgrade") {
		t.Fatalf("downgrade not refused: %+v", c)
	}
	c, _ = checkByID(e.r.Preflight(PreflightInput{Op: OpUpgrade, Old: old, AllowDowngrade: true}), "existing")
	if c.Status != CheckPartly {
		t.Fatalf("an asked-for downgrade: %+v", c)
	}
	c, _ = checkByID(e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.userOpts(), Old: old}), "existing")
	if c.Status != CheckFailed {
		t.Fatalf("installing just for me over an install for everyone: %+v", c)
	}
	c, _ = checkByID(e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.machineOpts(), Old: old}), "existing")
	if c.Status != CheckPartly || !strings.Contains(c.Detail, "upgrades it instead") {
		t.Fatalf("a second install: %+v", c)
	}
}

func TestResolveOp(t *testing.T) {
	old := &Installed{Version: "1.2.0"}
	for _, c := range []struct {
		asked Op
		old   *Installed
		v     string
		want  Op
	}{
		{OpInstall, nil, "1.2.0", OpInstall},
		{OpInstall, old, "1.3.0", OpUpgrade},
		{OpInstall, old, "1.2.0", OpRepair},
		{OpUpgrade, old, "1.2.0", OpRepair},
		{OpUpgrade, old, "1.1.0", OpUpgrade},
		{OpUninstall, old, "1.3.0", OpUninstall},
		{OpRepair, old, "1.3.0", OpRepair},
	} {
		if got := ResolveOp(c.asked, c.old, c.v); got != c.want {
			t.Errorf("ResolveOp(%s, %v, %s) = %s, want %s", c.asked, c.old != nil, c.v, got, c.want)
		}
	}
}

// ---- install and the P14 records ----

func TestMachineInstallWritesTheRecordTheReaderTrusts(t *testing.T) {
	e := newEnv(t, "1.2.0")
	res := e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	l := NewLayout(e.machineOpts(), e.dirs)

	// The record round trip: the same check Unravis and the keeper make, through the reader interface.
	if err := datadir.TrustSharedFolder(l.SharedRoot, e.f.Sec.Read, AnchorReader(e.f.Reg)); err != nil {
		t.Fatalf("the reader does not trust what setup wrote: %v", err)
	}
	a, err := AnchorReader(e.f.Reg)()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(a.Root, l.SharedRoot) || a.RootID == "" || a.CasesID == "" || a.RootID == a.CasesID {
		t.Fatalf("anchor = %+v", a)
	}
	for _, name := range []string{AnchorRootValue, AnchorRootIDValue, AnchorCasesIDValue} {
		if _, err := e.f.Reg.GetString(HKLM, AnchorKey, name); err != nil {
			t.Errorf("%s is not a string value: %v", name, err)
		}
	}
	// A folder put back by someone else (new ID) is not trusted.
	if err := os.RemoveAll(l.SharedRoot); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Sec.MkdirSDDL(l.SharedRoot, SharedFolderSDDL()); err != nil {
		t.Fatal(err)
	}
	_ = e.f.Sec.MkdirSDDL(l.SharedCases, SharedFolderSDDL())
	if err := datadir.TrustSharedFolder(l.SharedRoot, e.f.Sec.Read, AnchorReader(e.f.Reg)); err == nil {
		t.Fatal("a replaced shared folder is trusted")
	}

	// The keeper service: its own virtual account with an unrestricted service SID, started.
	spec, err := e.f.Svc.Config(keeper.ServiceName)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Account != KeeperAccount || !spec.SIDUnrestricted || !strings.EqualFold(spec.ExePath, l.KeeperExe) {
		t.Fatalf("keeper service = %+v", spec)
	}
	if st, _ := e.f.Svc.State(keeper.ServiceName); st != blackbox.SvcRunning {
		t.Fatalf("keeper service state %v", st)
	}

	// Program files, shortcuts, the Installed apps entry and the state file.
	for _, p := range []string{l.AppExe, l.KeeperExe, l.StateFile, l.StartMenuLink, l.StartupLink} {
		if !exists(p) {
			t.Errorf("%s is missing", p)
		}
	}
	if v, _ := e.f.Reg.GetString(HKLM, ARPKey, "DisplayVersion"); v != "1.2.0" {
		t.Errorf("DisplayVersion %q", v)
	}
	if v, _ := e.f.Reg.GetString(HKLM, ARPKey, "UninstallString"); !strings.Contains(v, SetupExeName) || !strings.Contains(v, "--uninstall") {
		t.Errorf("UninstallString %q", v)
	}
	if exists(filepath.Join(MachineJournalDir(e.dirs), JournalName)) {
		t.Error("the journal is left after a finished install")
	}
	for _, d := range []string{"staging", "backup"} {
		m, _ := filepath.Glob(filepath.Join(l.InstallDir, ".setup-"+d+"-*"))
		if len(m) != 0 {
			t.Errorf("working folders left: %v", m)
		}
	}
	if res.Message == "" || res.AppExe != l.AppExe {
		t.Errorf("result %+v", res)
	}
}

func TestFolderPermissionLists(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	l := NewLayout(e.machineOpts(), e.dirs)
	k := keeper.ServiceSID(keeper.ServiceName)

	want := map[string]string{l.SharedRoot: SharedFolderSDDL(), l.SharedCases: SharedFolderSDDL(), l.KeeperDir: KeeperFolderSDDL(),
		l.InstallDir: ProgramFolderSDDL, MachineJournalDir(e.dirs): AdminOnlySDDL}
	for p, sddl := range want {
		got, err := e.f.Sec.SDDL(p)
		if err != nil || got != sddl {
			t.Errorf("%s has %q (%v), want %q", p, got, err, sddl)
		}
	}

	// The shared folder list in security.md: SYSTEM, Administrators and the keeper full; Users may list and add on the folder, and
	// modify what is inside, but never change permissions, take ownership or delete the folder itself.
	fs, err := ParseSDDL(SharedFolderSDDL())
	if err != nil {
		t.Fatal(err)
	}
	if err := datadir.CheckFolder("shared", fs); err != nil {
		t.Fatalf("the shared folder list fails the reader's check: %v", err)
	}
	type ace struct {
		sid   string
		mask  uint32
		flags uint8
	}
	var got []ace
	for _, a := range fs.ACEs {
		got = append(got, ace{a.SID, a.Mask, a.Flags})
	}
	wantACEs := []ace{{"S-1-5-18", 0x1f01ff, 3}, {"S-1-5-32-544", 0x1f01ff, 3}, {k, 0x1f01ff, 3}, {"S-1-5-32-545", 0x1200af, 0}, {"S-1-5-32-545", 0x1301bf, 0x0b}}
	if len(got) != len(wantACEs) {
		t.Fatalf("shared ACEs %+v", got)
	}
	for i := range got {
		if got[i] != wantACEs[i] {
			t.Errorf("shared ACE %d = %+v, want %+v", i, got[i], wantACEs[i])
		}
	}
	// Users never get WRITE_DAC or WRITE_OWNER anywhere, nor DELETE or delete-child on the folder itself (the inherit-only entry,
	// which applies to what is inside, may carry DELETE: that is "modify what is inside").
	for _, a := range fs.ACEs {
		if a.SID != "S-1-5-32-545" {
			continue
		}
		never := uint32(0x40000 | 0x80000 | 0x40)
		if a.Flags&0x08 == 0 {
			never |= 0x10000
		}
		if a.Mask&never != 0 {
			t.Errorf("Users get %#x (flags %#x)", a.Mask, a.Flags)
		}
	}
	kf, _ := ParseSDDL(KeeperFolderSDDL())
	if err := checkAdminOnly("keeper", withoutKeeper(kf)); err != nil {
		t.Errorf("keeper folder: %v", err)
	}
	for _, a := range kf.ACEs {
		if a.SID == "S-1-5-32-545" || a.SID == "S-1-1-0" {
			t.Errorf("the keeper folder lets %s in", a.SID)
		}
	}
	pf, _ := ParseSDDL(ProgramFolderSDDL)
	if err := checkAdminOnly("program", pf); err != nil {
		t.Errorf("program folder: %v", err)
	}
	// The record key: everyone may read it (Unravis runs as the person), only SYSTEM and Administrators may change it.
	ak, _ := ParseSDDL(AnchorKeySDDL)
	const keyWrite = 0x2 | 0x4 | 0x20 | 0x10000 | 0x40000 | 0x80000 | 0x10000000 | 0x40000000 // set value, create subkey, link, DELETE, WRITE_DAC, WRITE_OWNER, GA, GW
	if ak.Owner != "S-1-5-32-544" {
		t.Errorf("anchor key owner %s", ak.Owner)
	}
	for _, a := range ak.ACEs {
		if a.SID != "S-1-5-18" && a.SID != "S-1-5-32-544" && a.Mask&keyWrite != 0 {
			t.Errorf("anchor key lets %s change it (%#x)", a.SID, a.Mask)
		}
	}
}

func TestUserInstallNeedsNoApproval(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.f.Mach.Admin, e.f.Mach.Elevate = false, false
	o := e.userOpts()
	old, _ := Detect(e.sys.Reg)
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, old)
	if err != nil {
		t.Fatal(err)
	}
	if p.Elevated {
		t.Fatal("a just-for-me install asks for approval")
	}
	if res := e.r.Execute(t0ctx(), p); res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if v, _ := e.f.Reg.GetString(HKCU, ARPKey, "DisplayVersion"); v != "1.2.0" {
		t.Fatal("no Installed apps entry for the person")
	}
	if ok, _ := e.f.Reg.KeyExists(HKLM, AnchorKey); ok {
		t.Fatal("a just-for-me install wrote the shared folder record")
	}
	if st, _ := e.f.Svc.State(keeper.ServiceName); st != blackbox.SvcNotInstalled {
		t.Fatal("a just-for-me install made the keeper service")
	}
	l := NewLayout(o, e.dirs)
	if exists(l.KeeperExe) || exists(filepath.Join(l.InstallDir, KeeperExeName)) {
		t.Fatal("a just-for-me install placed the keeper")
	}
}

func TestBlackboxInstallWithFirewallAndReadKeyFile(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.withBlackbox(e.machineOpts(), true)
	o.ReadKeyFile = filepath.Join(e.dirs.UserProfile, "Desktop", "blackbox key.txt")
	res := e.mustRun(OpInstall, o, UninstallOptions{})
	if !strings.HasPrefix(res.ReadKey, "ubk-") {
		t.Fatalf("read key %q", res.ReadKey)
	}
	data, err := os.ReadFile(o.ReadKeyFile)
	if err != nil || strings.TrimSpace(string(data)) != res.ReadKey {
		t.Fatalf("read key file %q %v", data, err)
	}
	if s, _ := e.f.Sec.SDDL(o.ReadKeyFile); s != ReadKeyFileSDDL("S-1-5-21-1000") {
		t.Fatalf("read key file SDDL %q", s)
	}
	rule, err := e.f.FW.Get(FirewallRuleName(7788))
	if err != nil || rule.Port != 7788 || strings.Join(rule.RemoteAddresses, ",") != "10.0.0.5,10.0.1.0/24" {
		t.Fatalf("rule %+v %v", rule, err)
	}
	l := NewLayout(o, e.dirs)
	_, cfgPath, _ := l.BlackboxPaths()
	cfg, err := blackbox.LoadConfig(cfgPath)
	if err != nil || cfg.Bind != "0.0.0.0" || cfg.Name != "web-b" {
		t.Fatalf("blackbox.json %+v %v", cfg, err)
	}
	if strings.Contains(strings.Join(e.log, "\n"), res.ReadKey) {
		t.Fatal("the read key reached the log")
	}
	for _, ev := range e.evs {
		if strings.Contains(ev.Detail, res.ReadKey) {
			t.Fatal("the read key reached a step event")
		}
	}
}

// ---- rollback: every step undone ----

type rollbackCase struct {
	name  string
	op    Op
	opts  func(e *testEnv) Options
	prior func(e *testEnv) // an earlier install, for upgrade and repair
}

func priorInstall(o func(e *testEnv) Options) func(e *testEnv) {
	return func(e *testEnv) {
		v := e.r.Payload
		e.r.Payload = testPayload(e.t, "1.0.0", true)
		e.f.version = "1.0.0"
		e.mustRun(OpInstall, o(e), UninstallOptions{})
		l := NewLayout(o(e), e.dirs)
		if l.SharedCases != "" {
			writeFile(e.t, filepath.Join(l.SharedCases, "case-1", "case.db"), "evidence")
			writeFile(e.t, filepath.Join(l.KeeperDir, "secrets.sealed"), "sealed-for-keeper")
		}
		e.r.Payload = v
		e.f.version = v.Manifest.Version
	}
}

func rollbackCases() []rollbackCase {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	user := func(e *testEnv) Options { return e.userOpts() }
	return []rollbackCase{
		{name: "install for everyone with Blackbox and firewall", op: OpInstall, opts: full},
		{name: "install just for me", op: OpInstall, opts: user},
		{name: "install just for me with Blackbox", op: OpInstall, opts: func(e *testEnv) Options { return e.withBlackbox(e.userOpts(), false) }},
		{name: "upgrade for everyone with Blackbox and firewall", op: OpUpgrade, opts: full, prior: priorInstall(full)},
		{name: "upgrade just for me", op: OpUpgrade, opts: user, prior: priorInstall(user)},
		{name: "repair", op: OpRepair, opts: full, prior: priorInstall(full)},
	}
}

func stepIDs(t *testing.T, c rollbackCase) []string {
	e := newEnv(t, "1.2.0")
	if c.prior != nil {
		c.prior(e)
	}
	old, _ := Detect(e.sys.Reg)
	p, err := e.r.NewPlan(c.op, c.opts(e), UninstallOptions{}, old)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range p.Steps {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestEveryStepIsUndoneOnFailure(t *testing.T) {
	for _, c := range rollbackCases() {
		for _, step := range stepIDs(t, c) {
			t.Run(c.name+"/"+step, func(t *testing.T) {
				e := newEnv(t, "1.2.0")
				if c.prior != nil {
					c.prior(e)
				}
				before := e.snapshot()
				e.r.FailAfter = func(s string) error {
					if s == step {
						return errors.New("injected failure")
					}
					return nil
				}
				res := e.run(c.op, c.opts(e), UninstallOptions{})
				if res.Outcome != OutcomeRolledBack {
					t.Fatalf("outcome %s: %s %v\n%s", res.Outcome, res.Message, res.Left, strings.Join(e.log, "\n"))
				}
				if d := diffSnap(before, e.snapshot()); d != "" {
					t.Fatalf("not everything was put back:\n%s\nlog:\n%s", d, strings.Join(e.log, "\n"))
				}
				if res.Outcome.ExitCode() != 4 {
					t.Errorf("exit code %d", res.Outcome.ExitCode())
				}
			})
		}
	}
}

func TestFaultsInsideActionsAreUndone(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	faults := []struct {
		name  string
		fault func(e *testEnv) string
		prior bool
	}{
		{"program folder permissions", func(e *testEnv) string { return "sec.MkdirSDDL:" + DefaultLocation(ScopeMachine, e.dirs) }, false},
		{"cases folder", func(e *testEnv) string {
			return "sec.MkdirSDDL:" + filepath.Join(e.dirs.ProgramData, SharedFolderName, "cases")
		}, false},
		{"keeper folder", func(e *testEnv) string {
			return "sec.MkdirSDDL:" + filepath.Join(e.dirs.ProgramData, SharedFolderName, "keeper")
		}, false},
		{"record key", func(*testEnv) string { return `reg.CreateKey:HKLM\` + AnchorKey }, false},
		{"record cases ID", func(*testEnv) string { return `reg.Set:HKLM\` + AnchorKey + `\` + AnchorCasesIDValue }, false},
		{"keeper create", func(*testEnv) string { return "svc.Create:" + keeper.ServiceName }, false},
		{"keeper start", func(*testEnv) string { return "svc.Start:" + keeper.ServiceName }, false},
		{"blackbox create", func(*testEnv) string { return "svc.Create:" + blackbox.ServiceName }, false},
		{"blackbox start", func(*testEnv) string { return "svc.Start:" + blackbox.ServiceName }, false},
		{"firewall", func(*testEnv) string { return "fw.Add:" + FirewallRuleName(7788) }, false},
		{"installed apps", func(*testEnv) string { return `reg.Set:HKLM\` + ARPKey + `\UninstallString` }, false},
		{"program does not start", func(*testEnv) string { return "run.Version" }, false},
		{"upgrade: keeper stop", func(*testEnv) string { return "svc.Stop:" + keeper.ServiceName }, true},
		{"upgrade: blackbox restart", func(*testEnv) string { return "svc.Start:" + blackbox.ServiceName }, true},
		{"upgrade: version check", func(*testEnv) string { return "run.Version" }, true},
	}
	for _, c := range faults {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			op := OpInstall
			if c.prior {
				priorInstall(full)(e)
				op = OpUpgrade
			}
			before := e.snapshot()
			e.f.Faults.Once(c.fault(e), errors.New("injected"))
			res := e.run(op, full(e), UninstallOptions{})
			if res.Outcome != OutcomeRolledBack {
				t.Fatalf("outcome %s: %s %v", res.Outcome, res.Message, res.Left)
			}
			e.f.Faults.Set(c.fault(e), nil)
			if d := diffSnap(before, e.snapshot()); d != "" {
				t.Fatalf("not put back:\n%s\nlog:\n%s", d, strings.Join(e.log, "\n"))
			}
			if res.Failed == "" || res.Fix == "" {
				t.Errorf("no failed step or fix: %+v", res)
			}
		})
	}
}

// ---- upgrade, repair ----

func TestUpgradeKeepsCasesSealedFilesAndKeeperAccount(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	l := NewLayout(full(e), e.dirs)
	caseFile, sealed := filepath.Join(l.SharedCases, "case-1", "case.db"), filepath.Join(l.KeeperDir, "secrets.sealed")
	_, cfgPath, _ := l.BlackboxPaths()
	cfgBefore, _ := os.ReadFile(cfgPath)
	keeperSDDL, _ := e.f.Sec.SDDL(l.KeeperDir)
	anchorBefore, _ := AnchorReader(e.f.Reg)()
	recording := filepath.Join(full(e).Blackbox.Folder, "bb-20261027-10.ndjson")
	writeFile(t, recording, "samples")
	pid := e.f.Proc.Run(l.AppExe)

	o := Options{CloseRunningApp: true}
	res := e.mustRun(ResolveOp(OpInstall, mustDetect(t, e), "1.2.0"), o, UninstallOptions{})
	if res.Op != OpUpgrade || !res.RestartApp {
		t.Fatalf("result %+v", res)
	}
	for p, want := range map[string]string{caseFile: "evidence", sealed: "sealed-for-keeper", recording: "samples"} {
		if got, err := os.ReadFile(p); err != nil || string(got) != want {
			t.Errorf("%s = %q %v", p, got, err)
		}
	}
	if got, _ := os.ReadFile(cfgPath); !bytes.Equal(got, cfgBefore) {
		t.Error("blackbox.json changed")
	}
	if got, err := os.ReadFile(cfgPath + ".bak"); err != nil || !bytes.Equal(got, cfgBefore) {
		t.Error("no blackbox.json.bak")
	}
	if s, _ := e.f.Sec.SDDL(l.KeeperDir); s != keeperSDDL {
		t.Errorf("keeper folder permissions changed: %s", s)
	}
	if a, _ := AnchorReader(e.f.Reg)(); a.RootID != anchorBefore.RootID || a.CasesID != anchorBefore.CasesID {
		t.Error("the shared folder record changed")
	}
	if spec, _ := e.f.Svc.Config(keeper.ServiceName); spec.Account != KeeperAccount {
		t.Errorf("keeper account %q", spec.Account)
	}
	if got, _ := os.ReadFile(l.AppExe); !bytes.HasPrefix(got, []byte(AppExeName+" 1.2.0")) {
		t.Error("the program was not replaced")
	}
	if v, _ := e.f.Reg.GetString(HKLM, ARPKey, "DisplayVersion"); v != "1.2.0" {
		t.Errorf("DisplayVersion %q", v)
	}
	if list, _ := e.f.Proc.List(); len(list) != 0 {
		t.Errorf("process %d still running", pid)
	}
	st, _ := ReadState(l.StateFile)
	if st.Version != "1.2.0" || st.Blackbox == nil || st.Firewall == nil || !st.Installed.Equal(mustDetect(t, e).State.Installed) {
		t.Errorf("state %+v", st)
	}
}

func mustDetect(t *testing.T, e *testEnv) *Installed {
	t.Helper()
	old, err := Detect(e.sys.Reg)
	if err != nil {
		t.Fatal(err)
	}
	return old
}

func TestUpgradeRefusesAKeeperWithAnotherAccount(t *testing.T) {
	full := func(e *testEnv) Options { return e.machineOpts() }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	spec, _ := e.f.Svc.Config(keeper.ServiceName)
	spec.Account = "LocalSystem"
	e.f.Svc.Put(spec, blackbox.SvcRunning)
	before := e.snapshot()
	res := e.run(OpUpgrade, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack || !strings.Contains(res.Message, "LocalSystem") {
		t.Fatalf("%+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("changed:\n%s", d)
	}
}

func TestRepairPutsBackMissingPieces(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), false) }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	e.r.Payload = testPayload(t, "1.2.0", true)
	l := NewLayout(full(e), e.dirs)
	_ = os.Remove(l.AppExe)
	_ = os.Remove(l.StartMenuLink)
	_ = e.f.Svc.Delete(keeper.ServiceName)
	_ = e.f.Sec.SetSDDL(l.SharedRoot, "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;BU)")
	if err := datadir.TrustSharedFolder(l.SharedRoot, e.f.Sec.Read, AnchorReader(e.f.Reg)); err == nil {
		t.Fatal("a loosened folder is trusted")
	}
	e.mustRun(OpRepair, Options{}, UninstallOptions{})
	for _, p := range []string{l.AppExe, l.StartMenuLink} {
		if !exists(p) {
			t.Errorf("%s not put back", p)
		}
	}
	if st, _ := e.f.Svc.State(keeper.ServiceName); st != blackbox.SvcRunning {
		t.Error("keeper service not put back")
	}
	if err := datadir.TrustSharedFolder(l.SharedRoot, e.f.Sec.Read, AnchorReader(e.f.Reg)); err != nil {
		t.Errorf("shared folder permissions not put back: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(l.SharedCases, "case-1", "case.db")); string(got) != "evidence" {
		t.Error("repair changed a case")
	}
}

// ---- uninstall ----

func TestUninstallKeepsDataByDefault(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	l := NewLayout(full(e), e.dirs)
	recording := filepath.Join(full(e).Blackbox.Folder, "bb-20261027-10.ndjson")
	writeFile(t, recording, "samples")

	res := e.run(OpUninstall, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v\n%s", res, strings.Join(e.log, "\n"))
	}
	for _, p := range []string{l.AppExe, l.KeeperExe, l.StateFile, l.StartMenuLink, l.StartupLink, l.InstallDir} {
		if exists(p) {
			t.Errorf("%s is still there", p)
		}
	}
	for _, name := range []string{keeper.ServiceName, blackbox.ServiceName} {
		if st, _ := e.f.Svc.State(name); st != blackbox.SvcNotInstalled {
			t.Errorf("service %s is still there", name)
		}
	}
	if ok, _ := e.f.Reg.KeyExists(HKLM, ARPKey); ok {
		t.Error("the Installed apps entry is still there")
	}
	if _, err := e.f.FW.Get(FirewallRuleName(7788)); err == nil {
		t.Error("the firewall rule is still there")
	}
	// Kept: the shared data, its record (so a later install trusts it again) and the recordings.
	for p, want := range map[string]string{filepath.Join(l.SharedCases, "case-1", "case.db"): "evidence", filepath.Join(l.KeeperDir, "secrets.sealed"): "sealed-for-keeper", recording: "samples"} {
		if got, err := os.ReadFile(p); err != nil || string(got) != want {
			t.Errorf("%s was not kept: %v", p, err)
		}
	}
	if err := datadir.TrustSharedFolder(l.SharedRoot, e.f.Sec.Read, AnchorReader(e.f.Reg)); err != nil {
		t.Errorf("the kept shared folder is no longer trusted: %v", err)
	}
	if len(res.Kept) == 0 {
		t.Error("the result does not say what was kept")
	}
	if exists(l.SetupExe) {
		t.Error("the setup copy in the program folder is still there")
	}

	// Installing again finds the kept folder and trusts it.
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	if got, _ := os.ReadFile(filepath.Join(l.SharedCases, "case-1", "case.db")); string(got) != "evidence" {
		t.Error("reinstall lost the kept case")
	}
}

func TestUninstallDeletesOnlyWithTheTypedConfirmation(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), false) }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	l := NewLayout(full(e), e.dirs)
	recording := filepath.Join(full(e).Blackbox.Folder, "bb-20261027-10.ndjson")
	writeFile(t, recording, "samples")

	before := e.snapshot()
	for _, un := range []UninstallOptions{{DeleteCases: true}, {DeleteCases: true, ConfirmCases: "delete"}, {DeleteRecordings: true, ConfirmRecordings: "Web-B"}} {
		res := e.run(OpUninstall, Options{}, un)
		if res.Outcome != OutcomeInvalid || !strings.Contains(res.Message, "Nothing was removed") || res.Outcome.ExitCode() != 2 {
			t.Fatalf("%+v: %+v", un, res)
		}
		if d := diffSnap(before, e.snapshot()); d != "" {
			t.Fatalf("%+v changed:\n%s", un, d)
		}
	}
	res := e.run(OpUninstall, Options{}, UninstallOptions{DeleteCases: true, ConfirmCases: ConfirmDeleteWord, DeleteRecordings: true, ConfirmRecordings: "web-b"})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if exists(l.SharedRoot) || exists(recording) {
		t.Errorf("confirmed deletions did not happen: shared %v recording %v %+v\n%s", exists(l.SharedRoot), exists(recording), res, strings.Join(e.log, "\n"))
	}
	if ok, _ := e.f.Reg.KeyExists(HKLM, AnchorKey); ok {
		t.Error("the shared folder record is left after deleting the folder")
	}
}

func TestUninstallJustForMeKeepsTheirData(t *testing.T) {
	user := func(e *testEnv) Options { return e.userOpts() }
	e := newEnv(t, "1.2.0")
	priorInstall(user)(e)
	data := filepath.Join(e.dirs.LocalAppData, "APIAuditExplorer", "cases", "c.db")
	writeFile(t, data, "mine")
	res := e.run(OpUninstall, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeDone || !exists(data) || len(res.Kept) == 0 {
		t.Fatalf("%+v exists=%v", res, exists(data))
	}
	// Uninstall started again (double run) finds nothing to remove and says so before doing anything.
	if _, err := e.r.NewPlan(OpUninstall, Options{}, UninstallOptions{}, mustDetect(t, e)); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("second uninstall: %v", err)
	}
}

// ---- clumsy-user paths ----

func TestDoubleRunWaitsForTheFirst(t *testing.T) {
	e := newEnv(t, "1.2.0")
	release, err := e.sys.Lock()
	if err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()
	res := e.run(OpInstall, e.machineOpts(), UninstallOptions{})
	if res.Outcome != OutcomePreflightFailed || !strings.Contains(res.Message, "already running") {
		t.Fatalf("%+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("the second run changed:\n%s", d)
	}
	release()
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	// Setup started again after a finished install repairs instead of installing a second copy.
	op := ResolveOp(OpInstall, mustDetect(t, e), "1.2.0")
	if op != OpRepair {
		t.Fatalf("second run op %s", op)
	}
	e.mustRun(op, e.machineOpts(), UninstallOptions{})
}

// cancelFS cancels the run while the first program file is being copied.
type cancelFS struct {
	fs.FS
	cancel func()
}

type cancelFile struct {
	fs.File
	cancel func()
}

func (c cancelFS) Open(name string) (fs.File, error) {
	f, err := c.FS.Open(name)
	if err != nil || name == ManifestName {
		return f, err
	}
	return cancelFile{f, c.cancel}, nil
}

func (c cancelFile) Read(p []byte) (int, error) {
	c.cancel()
	return c.File.Read(p)
}

var _ io.Reader = cancelFile{}

func TestCancelMidCopyPutsEverythingBack(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "upgrade"}[prior], func(t *testing.T) {
			full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
			e := newEnv(t, "1.2.0")
			op := OpInstall
			if prior {
				priorInstall(full)(e)
				op = OpUpgrade
			}
			before := e.snapshot()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e.r.Payload.FS = cancelFS{e.r.Payload.FS, cancel}
			p, err := e.r.NewPlan(op, full(e), UninstallOptions{}, mustDetect(t, e))
			if err != nil {
				t.Fatal(err)
			}
			res := e.r.Execute(ctx, p)
			if res.Outcome != OutcomeCancelled || res.Outcome.ExitCode() != 4 {
				t.Fatalf("%+v", res)
			}
			if d := diffSnap(before, e.snapshot()); d != "" {
				t.Fatalf("not put back:\n%s", d)
			}
		})
	}
}

func TestCancelBetweenSteps(t *testing.T) {
	e := newEnv(t, "1.2.0")
	before := e.snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.r.BeforeStep = func(s string) {
		if s == "shortcuts" {
			cancel()
		}
	}
	p, _ := e.r.NewPlan(OpInstall, e.withBlackbox(e.machineOpts(), false), UninstallOptions{}, nil)
	res := e.r.Execute(ctx, p)
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("%+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("not put back:\n%s", d)
	}
	var undone int
	for _, ev := range e.evs {
		if ev.State == StepUndone {
			undone++
		}
	}
	if undone == 0 {
		t.Error("no step was shown as undone")
	}
}

func TestRunningAppStopsUnlessThePersonSaysClose(t *testing.T) {
	user := func(e *testEnv) Options { return e.userOpts() }
	e := newEnv(t, "1.2.0")
	priorInstall(user)(e)
	l := NewLayout(user(e), e.dirs)
	e.f.Proc.Run(l.AppExe)
	e.f.Proc.Run(filepath.Join(e.root, "elsewhere", AppExeName)) // another copy, not ours: left alone

	c, _ := checkByID(e.r.Preflight(PreflightInput{Op: OpUpgrade, Old: mustDetect(t, e), Silent: true}), "app-running")
	if c.Status != CheckFailed || !strings.Contains(c.Fix, "closeRunningApp") {
		t.Fatalf("silent preflight %+v", c)
	}
	before := e.snapshot()
	res := e.run(OpUpgrade, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack || res.Failed != "Close Unravis" {
		t.Fatalf("%+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("changed:\n%s", d)
	}
	res = e.run(OpUninstall, Options{}, UninstallOptions{})
	if res.Outcome != OutcomePreflightFailed || !exists(l.AppExe) {
		t.Fatalf("uninstall with Unravis open: %+v", res)
	}
	res = e.mustRun(OpUpgrade, Options{CloseRunningApp: true}, UninstallOptions{})
	list, _ := e.f.Proc.List()
	if len(list) != 1 || !strings.Contains(list[0].Exe, "elsewhere") || !res.RestartApp {
		t.Fatalf("processes %+v, result %+v", list, res)
	}
	// Terminate failing (the process already ended) is fine; a real failure stops the run.
	pid := e.f.Proc.Run(l.AppExe)
	e.f.Faults.Set("proc.Terminate:"+itoa(pid), errors.New("access denied"))
	res = e.run(OpRepair, Options{CloseRunningApp: true}, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("%+v", res)
	}
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestFolderWithSpacesAndNonASCII(t *testing.T) {
	for _, scope := range []Scope{ScopeMachine, ScopeUser} {
		t.Run(string(scope), func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			o := e.machineOpts()
			if scope == ScopeUser {
				o = e.userOpts()
			}
			o.Location = filepath.Join(filepath.Dir(o.Location), "Ünravis Tëst 文件 ø")
			e.mustRun(OpInstall, o, UninstallOptions{})
			l := NewLayout(o, e.dirs)
			if !exists(l.AppExe) {
				t.Fatal("program not placed")
			}
			data, err := os.ReadFile(l.StartMenuLink)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadShortcutTarget(data)
			if err != nil || got != l.AppExe {
				t.Fatalf("shortcut target %q %v", got, err)
			}
			old := mustDetect(t, e)
			if !strings.EqualFold(old.Location, l.InstallDir) {
				t.Fatalf("detected %q", old.Location)
			}
			if res := e.run(OpUninstall, Options{}, UninstallOptions{}); res.Outcome != OutcomeDone || exists(l.InstallDir) {
				t.Fatalf("uninstall %+v", res)
			}
		})
	}
}

func TestBadLocationsAreRefusedBeforeAnythingChanges(t *testing.T) {
	e := newEnv(t, "1.2.0")
	for _, loc := range []string{"", `relative\folder`, e.dirs.Windows, filepath.Join(e.dirs.Windows, "System32", "Unravis"), filepath.VolumeName(e.root) + `\`,
		filepath.Join(e.dirs.ProgramFiles, strings.Repeat("a", 250))} {
		o := e.machineOpts()
		o.Location = loc
		if _, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil); err == nil {
			t.Errorf("location %q accepted", loc)
		}
	}
	o := e.withBlackbox(e.machineOpts(), true)
	o.Firewall.RemoteAddresses = []string{"any"}
	if _, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil); err == nil || ProblemField(err) == "" {
		t.Errorf("firewall to everyone accepted: %v", err)
	}
}

func TestCrashMidRunIsPutBackNextTime(t *testing.T) {
	e := newEnv(t, "1.2.0")
	before := e.snapshot()
	e.r.BeforeStep = func(s string) {
		if s == "shortcuts" {
			panic("power cut")
		}
	}
	func() {
		defer func() { _ = recover() }()
		e.run(OpInstall, e.withBlackbox(e.machineOpts(), true), UninstallOptions{})
	}()
	e.r.BeforeStep = nil
	j := filepath.Join(MachineJournalDir(e.dirs), JournalName)
	if !exists(j) {
		t.Fatal("no journal after a crash")
	}
	c, _ := checkByID(e.r.Preflight(PreflightInput{Op: OpInstall, Opts: e.machineOpts()}), "journal")
	if c.Status != CheckPartly {
		t.Fatalf("preflight does not mention the journal: %+v", c)
	}
	found, left, err := e.r.RecoverJournal(true)
	if !found || err != nil || len(left) != 0 {
		t.Fatalf("recover: %v %v %v", found, left, err)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("not put back:\n%s", d)
	}
}

func TestJournalInAFolderOthersCanWriteIsNotReplayed(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	writeFile(t, filepath.Join(dir, JournalName), `{"op":"install","undos":[{"kind":"file-created","path":"C:\\Windows\\win.ini"}]}`)
	_ = e.f.Sec.SetSDDL(dir, "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;BU)")
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil {
		t.Fatalf("a journal others could write was used: %v %v", found, err)
	}
	res := e.run(OpInstall, e.machineOpts(), UninstallOptions{})
	if res.Outcome != OutcomeRollbackLeft {
		t.Fatalf("%+v", res)
	}
}

// ---- log and shortcut ----

func TestRedactHidesSecrets(t *testing.T) {
	for in, bad := range map[string]string{
		"read key ubk-abcd-1234-efgh written":                  "abcd-1234",
		"open http://127.0.0.1:5000/#t=Zm9vYmFy then":          "Zm9vYmFy",
		"url http://127.0.0.1/launch?k=secretvalue&x=1":        "secretvalue",
		`config password="hunter 2" next`:                      "hunter",
		"pwd: letmein":                                         "letmein",
		"ReadKey=ubk-zzz":                                      "zzz",
		"mongodb://u:p@host/?authSource=admin&token=abc123def": "abc123def",
	} {
		if got := Redact(in); strings.Contains(got, bad) {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
	if got := Redact("copy-files: ok"); got != "copy-files: ok" {
		t.Errorf("plain line changed: %q", got)
	}
}

func TestFileLogWritesRedactedLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "setup.log")
	l, err := OpenFileLog(p)
	if err != nil {
		t.Fatal(err)
	}
	l.Line("blackbox: key ubk-1111-2222\nsecond line")
	l.Line("done")
	_ = l.Close()
	data, _ := os.ReadFile(p)
	s := string(data)
	if strings.Contains(s, "1111") || strings.Count(s, "\r\n") != 2 {
		t.Fatalf("log %q", s)
	}
}

func TestShortcutRoundTrip(t *testing.T) {
	for _, target := range []string{`C:\Program Files\Unravis\unravis.exe`, `D:\Ünravis 文件\unravis.exe`} {
		data, err := Shortcut{Target: target, WorkDir: filepath.Dir(target), Description: "Unravis", Icon: target}.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadShortcutTarget(data)
		if err != nil || got != target {
			t.Errorf("%q round trip = %q %v", target, got, err)
		}
	}
	if _, err := (Shortcut{Target: `relative\x.exe`}).Bytes(); err == nil {
		t.Error("relative target accepted")
	}
	if _, err := ReadShortcutTarget([]byte("not a shortcut")); err == nil {
		t.Error("garbage read as a shortcut")
	}
}

func TestExitCodes(t *testing.T) {
	for o, want := range map[Outcome]int{OutcomeDone: 0, OutcomePreflightFailed: 1, OutcomeInvalid: 2, OutcomeCancelledUAC: 3, OutcomeRolledBack: 4,
		OutcomeCancelled: 4, OutcomeRollbackLeft: 5, OutcomeLeftovers: 5} {
		if got := o.ExitCode(); got != want {
			t.Errorf("%s exit %d, want %d", o, got, want)
		}
	}
}

// ---- NeedsAdminFor: the UAC prompt has to come up front, even when the saved state does not ask for it ----

// newTestTxn is a Txn backed by a real journal, so a step that journals an undo (StopService does, for a service that is really
// running) does not hit the nil *Journal that a bare &Txn{sys: sys} would.
func newTestTxn(t *testing.T, sys *System) *Txn {
	t.Helper()
	j, err := NewJournal(filepath.Join(t.TempDir(), JournalName), OpUpgrade, "1.0.0", sys.now())
	if err != nil {
		t.Fatal(err)
	}
	return &Txn{sys: sys, j: j, log: func(string) {}}
}

// A service an earlier machine-scope install left behind still needs an administrator, even though this run's own options (or the
// absent/just-for-me state setup can read) say otherwise: the bug was Access denied discovered mid-run, not asked for up front.
func TestNeedsAdminForALeftoverServiceEvenWhenTheOptionsDoNot(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.userOpts()
	if NeedsAdmin(o) {
		t.Fatal("a just-for-me install should not need admin by itself")
	}
	if NeedsAdminFor(e.sys.Svc, o) {
		t.Fatal("a clean PC should not need admin for a just-for-me install")
	}
	e.f.Svc.Put(KeeperSpec(`C:\Program Files\Unravis\unravis-keeper.exe`), blackbox.SvcRunning)
	if !NeedsAdminFor(e.sys.Svc, o) {
		t.Fatal("a leftover keeper service should still ask for administrator approval")
	}
}

// recordingElevator is Elevator with nothing behind the pipe: just enough to see whether LocalBackend.Run asked to elevate at all.
type recordingElevator struct{ started bool }

func (e *recordingElevator) Start(context.Context) (io.ReadWriteCloser, error) {
	e.started = true
	return nil, errors.New("no worker in this test")
}

// (a) Unelevated, with a leftover keeper service and options that by themselves need no approval: LocalBackend.Run still asks for
// elevation up front (the worker is started) instead of running the steps unelevated and failing later at "Stop the Unravis
// services".
func TestLocalBackendElevatesForALeftoverServiceEvenUnelevated(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(KeeperSpec(`C:\Program Files\Unravis\unravis-keeper.exe`), blackbox.SvcRunning)
	o := e.userOpts()
	el := &recordingElevator{}
	be := &LocalBackend{R: e.r, Mode: OpInstall, IsAdmin: false, Elev: el}
	if !be.Changes(WorkRequest{Op: OpInstall, Options: o}).NeedsAdmin {
		t.Fatal("the approval screen should ask for admin because of the leftover service")
	}
	before := e.snapshot()
	res := be.Run(context.Background(), WorkRequest{Op: OpInstall, Options: o}, func(StepEvent) {}, func(string) {})
	if !el.started {
		t.Fatal("LocalBackend.Run should have started the elevated worker, not run the steps in this process")
	}
	if res.Outcome != OutcomeRolledBack || e.snapshot() != before {
		t.Fatalf("%+v", res)
	}
}

// (d) The approval screen and a fresh plan both say admin is needed when only a leftover service makes that true.
func TestDescribeChangesAndPlanNeedAdminForALeftoverService(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(KeeperSpec(`C:\Program Files\Unravis\unravis-keeper.exe`), blackbox.SvcRunning)
	o := e.userOpts()
	ch := DescribeChanges(WorkRequest{Op: OpInstall, Options: o}, nil, e.dirs, e.sys.Svc, false)
	if !ch.NeedsAdmin {
		t.Fatal("DescribeChanges should ask for admin because of the leftover service")
	}
	p, err := e.r.NewPlan(OpInstall, o, UninstallOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Elevated {
		t.Fatal("the plan should be marked elevated because of the leftover service")
	}
}

// (b) A service that is not installed, or already stopped, needs no rights at all to see that: the step succeeds and never calls
// Stop (which is the call that needs to be elevated).
func TestStopServicesSkipsAServiceThatIsNotThere(t *testing.T) {
	e := newEnv(t, "1.0.0")
	e.f.Svc.Put(KeeperSpec(`C:\Program Files\Unravis\unravis-keeper.exe`), blackbox.SvcStopped) // already stopped
	// blackbox.ServiceName is never installed in this env.
	p := &Plan{r: e.r, Opts: e.machineOpts()}
	if err := p.stopServices(t0ctx(), newTestTxn(t, e.sys)); err != nil {
		t.Fatalf("stopServices: %v", err)
	}
	if len(e.f.Svc.Log) != 0 {
		t.Errorf("Stop should never be called when nothing needs stopping: %v", e.f.Svc.Log)
	}
}

// (c) A real stop failure gets exactly one period and a fix that matches the cause: the UAC prompt for access-denied, a restart (or
// closing whatever else is using the service) for anything else.
func TestStopServiceErrorMessagesHaveOnePeriodAndTheRightFix(t *testing.T) {
	cases := []struct {
		name    string
		fault   error
		wantMsg string
		wantFix string
	}{
		{name: "access denied asks for the UAC prompt", fault: ErrAccessDenied,
			wantMsg: "The service " + keeper.ServiceName + " did not stop: Windows refused access.",
			wantFix: "Run setup again and choose Yes on the administrator prompt."},
		{name: "another error keeps its own text, trimmed once", fault: errors.New("the device is not ready."),
			wantMsg: "The service " + keeper.ServiceName + " did not stop: the device is not ready.",
			wantFix: "Close other programs using the service, or restart the PC, then run setup again."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "1.0.0")
			e.f.Svc.Put(KeeperSpec(`C:\Program Files\Unravis\unravis-keeper.exe`), blackbox.SvcRunning)
			e.f.Faults.Set("svc.Stop:"+keeper.ServiceName, c.fault)
			p := &Plan{r: e.r, Opts: e.machineOpts()}
			err := p.stopServices(t0ctx(), newTestTxn(t, e.sys))
			var se *StepError
			if !errors.As(err, &se) {
				t.Fatalf("not a StepError: %v", err)
			}
			if se.Msg != c.wantMsg {
				t.Errorf("msg = %q, want %q", se.Msg, c.wantMsg)
			}
			if n := strings.Count(se.Msg, "."); n != 1 {
				t.Errorf("message has %d periods, want exactly 1: %q", n, se.Msg)
			}
			if se.Fix != c.wantFix {
				t.Errorf("fix = %q, want %q", se.Fix, c.wantFix)
			}
		})
	}
}
