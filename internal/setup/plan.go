package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/keeper"
)

// Plan is the list of steps one run takes, worked out before anything changes.
type Plan struct {
	Op      Op
	Version string
	Opts    Options
	Uninst  UninstallOptions
	Layout  Layout
	Old     *Installed
	// Elevated says the run needs (and has) administrator rights.
	Elevated bool
	Steps    []Step

	r             *Runner
	stamp         string
	staged        map[string]string
	readKey       string
	appWasRunning bool
	unState       *uninstallState
}

func (p *Plan) backupDir() string {
	return filepath.Join(p.Layout.InstallDir, ".setup-backup-"+p.stamp)
}

// KeeperAccount is the keeper service's own virtual account. Its saved passwords are sealed for this account (DPAPI), so setup never
// gives an existing keeper service a different one.
var KeeperAccount = `NT SERVICE\` + keeper.ServiceName

// KeeperSpec is the keeper service setup registers.
func KeeperSpec(exe string) ServiceSpec {
	return ServiceSpec{Name: keeper.ServiceName, DisplayName: "Unravis Key Keeper",
		Description: "Keeps the passwords and read keys Unravis saves, and does the checks that need them, for everyone on this PC. It never hands a secret back.",
		ExePath:     exe, Account: KeeperAccount, SIDUnrestricted: true, DelayedStart: false}
}

// FirewallRuleName is the name of the Blackbox firewall rule for a port.
func FirewallRuleName(port int) string { return fmt.Sprintf("Unravis Blackbox (TCP %d)", port) }

// NewPlan works out the steps of op. For an upgrade or a repair the choices come from the install found (old); only the answer about
// closing a running Unravis comes from opts.
func (r *Runner) NewPlan(op Op, opts Options, un UninstallOptions, old *Installed) (*Plan, error) {
	if r.Payload == nil && op != OpUninstall {
		return nil, errors.New("this setup program has no program files inside. Download it again")
	}
	p := &Plan{Op: op, Uninst: un, Old: old, r: r, stamp: r.Sys.now().UTC().Format("20060102-150405"), staged: map[string]string{}}
	if r.Payload != nil {
		p.Version = r.Payload.Manifest.Version
	}
	switch op {
	case OpInstall:
		canon, err := opts.Validated(r.Dirs, r.Sys.paths())
		if err != nil {
			return nil, err
		}
		p.Opts = canon
	case OpUpgrade, OpRepair, OpUninstall:
		if old == nil {
			return nil, errors.New("Unravis is not installed on this PC")
		}
		p.Opts = old.State.Options()
		p.Opts.CloseRunningApp = opts.CloseRunningApp
		if op == OpUninstall {
			p.Opts.CloseRunningApp = un.CloseRunningApp
			p.Version = old.Version
		}
		if p.Opts.Location == "" {
			p.Opts.Location = old.Location
		}
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
	if opts.ReadKeyFile != "" {
		p.Opts.ReadKeyFile = opts.ReadKeyFile
		if op != OpInstall {
			if c, err := r.Sys.paths().Canonical(opts.ReadKeyFile); err == nil {
				p.Opts.ReadKeyFile = c
			}
		}
	}
	p.Layout = NewLayout(p.Opts, r.Dirs)
	p.Elevated = NeedsAdmin(p.Opts)
	if op == OpUninstall {
		p.Steps = uninstallSteps(p)
	} else {
		p.Steps = installSteps(p)
	}
	return p, nil
}

func installSteps(p *Plan) []Step {
	steps := []Step{{ID: "close-app", Title: "Close Unravis", Do: p.closeApp}}
	if p.Op != OpInstall {
		steps = append(steps, Step{ID: "stop-services", Title: "Stop the Unravis services", Do: p.stopServices})
	}
	steps = append(steps,
		Step{ID: "program-folder", Title: "Make the program folder", Do: p.programFolder},
		Step{ID: "copy-files", Title: "Copy the program files", Do: p.copyFiles},
	)
	if p.Opts.Scope == ScopeMachine {
		steps = append(steps,
			Step{ID: "shared-folder", Title: "Make the shared data folder", Do: p.sharedFolder},
			Step{ID: "anchor", Title: "Record the shared folder for Unravis", Do: p.anchor},
			Step{ID: "keeper-service", Title: "Set up the key keeper service", Do: p.keeperService},
		)
	}
	if p.Opts.Blackbox != nil {
		steps = append(steps, Step{ID: "blackbox", Title: "Set up the Blackbox recorder", Do: p.blackbox})
		if p.Opts.Firewall != nil {
			steps = append(steps, Step{ID: "firewall", Title: "Open the Blackbox port to the listed PCs", Do: p.firewall})
		}
	}
	steps = append(steps,
		Step{ID: "shortcuts", Title: "Add the Start menu shortcut", Do: p.shortcuts},
		Step{ID: "arp", Title: "Add Unravis to Installed apps", Do: p.arp},
		Step{ID: "state", Title: "Save the setup choices", Do: p.saveState},
		Step{ID: "verify", Title: "Check that Unravis starts", Do: p.verify},
	)
	return steps
}

// ---- install steps ----

// runningApps lists Unravis programs running from the program folder.
func (p *Plan) runningApps() ([]Process, error) {
	list, err := p.r.Sys.Proc.List()
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, q := range list {
		if strings.EqualFold(filepath.Base(q.Exe), AppExeName) && (insideFolder(q.Exe, p.Layout.InstallDir) || samePath(q.Exe, p.Layout.AppExe)) {
			out = append(out, q)
		}
	}
	return out, nil
}

// ErrAppRunning is Unravis open while setup needs to replace or remove it, without the person's yes to closing it.
var ErrAppRunning = &StepError{Msg: "Unravis is open.", Fix: "Close Unravis (or tick “Close Unravis for me”), then try again."}

func (p *Plan) closeApp(ctx context.Context, t *Txn) error {
	running, err := p.runningApps()
	if err != nil {
		return err
	}
	if len(running) == 0 {
		return nil
	}
	mine, others := p.r.splitApps(running)
	if len(others) > 0 {
		return ErrAppOpenForOther
	}
	if !p.Opts.CloseRunningApp {
		return ErrAppRunning
	}
	// Unravis has no way to be asked to close from outside, so the person's own copies are ended, after their yes.
	for _, q := range mine {
		t.Logf("close-app: closing Unravis (process %d)", q.PID)
		if err := p.r.Sys.Proc.Terminate(q.PID); err != nil && !errors.Is(err, ErrNotFound) {
			return &StepError{Msg: "Unravis could not be closed: " + err.Error() + ".", Fix: "Close Unravis yourself, then try again.", Err: err}
		}
	}
	p.appWasRunning = true
	return nil
}

func (p *Plan) stopServices(ctx context.Context, t *Txn) error {
	for _, name := range []string{keeper.ServiceName, blackbox.ServiceName} {
		if _, err := t.StopService(name); err != nil {
			return &StepError{Msg: "The service " + name + " did not stop: " + err.Error() + ".", Fix: "Restart the PC, then run setup again.", Err: err}
		}
	}
	return nil
}

func (p *Plan) programFolder(ctx context.Context, t *Txn) error {
	dir := p.Layout.InstallDir
	if err := p.r.refuseLink(dir); err != nil {
		return err
	}
	if p.Opts.Scope == ScopeMachine {
		why, err := p.r.programFolderProblem(dir, p.Old)
		if err != nil {
			return err
		}
		if why != "" {
			return &StepError{Msg: why, Fix: "Choose a new or empty folder, then run setup again."}
		}
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := t.MkdirAll(filepath.Dir(dir)); err != nil {
			return err
		}
		if p.Opts.Scope == ScopeMachine {
			// Made with its access list in one go: there is no moment when someone else could put a file in it.
			return t.MkdirSDDL(dir, ProgramFolderSDDL)
		}
		return t.MkdirAll(dir)
	} else if err != nil {
		return err
	}
	if p.Opts.Scope == ScopeMachine {
		return t.SetSDDL(dir, ProgramFolderSDDL)
	}
	return nil
}

// stageFile copies a payload file into the staging folder and checks its SHA-256 there, so what is placed is exactly what was built.
func (p *Plan) stageFile(ctx context.Context, staging, name string) (string, error) {
	pf, ok := p.r.Payload.file(name)
	if !ok {
		return "", &StepError{Msg: "This setup program does not carry " + name + ".", Fix: "Download the setup program again."}
	}
	dst := filepath.Join(staging, name)
	src, err := p.r.Payload.FS.Open(path.Clean(name))
	if err != nil {
		return "", err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if err := copyCtx(ctx, out, src.(fs.File)); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	got, err := fileSHA256(dst)
	if err != nil || !strings.EqualFold(got, pf.SHA256) {
		return "", &StepError{Msg: name + " changed or vanished while it was copied.",
			Fix: "Security software may have quarantined it. Allow the setup program in your antivirus, then run setup again.", Err: err}
	}
	return dst, nil
}

func (p *Plan) copyFiles(ctx context.Context, t *Txn) error {
	staging := filepath.Join(p.Layout.InstallDir, ".setup-staging-"+p.stamp)
	if err := t.WorkDir(staging); err != nil {
		return err
	}
	names := []string{AppExeName}
	if p.Opts.Scope == ScopeMachine {
		names = append(names, KeeperExeName)
	}
	if p.Opts.Blackbox != nil {
		names = append(names, BlackboxExeName)
	}
	for _, n := range names {
		dst, err := p.stageFile(ctx, staging, n)
		if err != nil {
			return err
		}
		p.staged[n] = dst
	}
	if p.r.SetupExe != "" && !samePath(p.r.SetupExe, p.Layout.SetupExe) {
		dst := filepath.Join(staging, SetupExeName)
		if err := copyFile(ctx, p.r.SetupExe, dst); err != nil {
			return err
		}
		p.staged[SetupExeName] = dst
	}
	place := []struct{ name, to string }{{AppExeName, p.Layout.AppExe}, {SetupExeName, p.Layout.SetupExe}}
	if p.Opts.Scope == ScopeMachine {
		place = append(place, struct{ name, to string }{KeeperExeName, p.Layout.KeeperExe})
	}
	for _, f := range place {
		if err := ctx.Err(); err != nil {
			return err
		}
		staged, ok := p.staged[f.name]
		if !ok {
			continue
		}
		if err := t.PlaceFile(staged, f.to); err != nil {
			return &StepError{Msg: "Could not put " + f.to + " in place: " + err.Error() + ".", Fix: "Close any program using the Unravis folder, then run setup again.", Err: err}
		}
	}
	return nil
}

// anchorMatches reports whether the record names the shared folder and the IDs of the folder and its cases folder as they are now:
// the folder is the one an earlier setup made, even if its permissions were loosened since.
func (p *Plan) anchorMatches() bool {
	a, err := AnchorReader(p.r.Sys.Reg)()
	if err != nil || !samePath(a.Root, p.Layout.SharedRoot) {
		return false
	}
	root, err := p.r.Sys.Sec.Read(p.Layout.SharedRoot)
	if err != nil || root.ID != a.RootID {
		return false
	}
	cases, err := p.r.Sys.Sec.Read(p.Layout.SharedCases)
	return err == nil && cases.ID == a.CasesID
}

func (p *Plan) sharedFolder(ctx context.Context, t *Txn) error {
	l := p.Layout
	shared := SharedFolderSDDL()
	for _, d := range []string{l.SharedRoot, l.SharedCases} {
		if err := p.r.refuseLink(d); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(l.SharedRoot); err == nil {
		if terr := p.r.Sys.Trust(l.SharedRoot); terr == nil {
			t.Logf("shared-folder: %s is the one setup made; kept as it is", l.SharedRoot)
			return p.ensureKeeperDir(t)
		} else if !p.anchorMatches() {
			return &StepError{Msg: l.SharedRoot + " was not made by the Unravis installer (" + terr.Error() + ").",
				Fix: "Move that folder somewhere else (it may hold someone's files), then run setup again.", Err: terr}
		}
		// The folder is the recorded one but its permissions were changed: put the list from security.md back.
		t.Logf("shared-folder: %s has looser permissions than setup gave it; putting them back", l.SharedRoot)
		if err := t.SetSDDL(l.SharedRoot, shared); err != nil {
			return err
		}
		if err := t.SetSDDL(l.SharedCases, shared); err != nil {
			return err
		}
		return p.ensureKeeperDir(t)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := t.MkdirSDDL(l.SharedRoot, shared); err != nil {
		return err
	}
	if err := t.MkdirSDDL(l.SharedCases, shared); err != nil {
		return err
	}
	return p.ensureKeeperDir(t)
}

// ensureKeeperDir makes the keeper's folder, or puts its permissions back. Its files are never touched: they are sealed for the keeper's
// account and setup has no business opening them.
func (p *Plan) ensureKeeperDir(t *Txn) error {
	dir := p.Layout.KeeperDir
	if err := p.r.refuseLink(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return t.MkdirSDDL(dir, KeeperFolderSDDL())
	} else if err != nil {
		return err
	}
	// Exactly the list of security.md (SY, BA and the keeper, nothing inherited); anything else, such as an extra reader or a missing
	// keeper entry, is put back.
	fs, err := p.r.Sys.Sec.Read(dir)
	if err == nil && sameAccess(fs, KeeperFolderSDDL()) {
		return nil
	}
	return t.SetSDDL(dir, KeeperFolderSDDL())
}

// withoutKeeper drops the keeper's own entry before the administrators-only check of its folder.
func withoutKeeper(fs datadir.FolderSecurity) datadir.FolderSecurity {
	k := keeper.ServiceSID(keeper.ServiceName)
	out := fs
	out.ACEs = nil
	for _, a := range fs.ACEs {
		if a.SID != k {
			out.ACEs = append(out.ACEs, a)
		}
	}
	return out
}

func (p *Plan) anchor(ctx context.Context, t *Txn) error {
	l := p.Layout
	root, err := p.r.Sys.Sec.Read(l.SharedRoot)
	if err != nil {
		return err
	}
	cases, err := p.r.Sys.Sec.Read(l.SharedCases)
	if err != nil {
		return err
	}
	if err := t.CreateKey(HKLM, AnchorKey, AnchorKeySDDL); err != nil {
		return err
	}
	for _, v := range []struct{ name, value string }{{AnchorRootValue, l.SharedRoot}, {AnchorRootIDValue, root.ID}, {AnchorCasesIDValue, cases.ID}} {
		if err := t.SetString(HKLM, AnchorKey, v.name, v.value); err != nil {
			return err
		}
	}
	// Read it back the way Unravis and the keeper will.
	if err := p.r.Sys.Trust(l.SharedRoot); err != nil {
		return &StepError{Msg: "The shared folder record does not check out: " + err.Error() + ".", Fix: "Run setup again; if it fails the same way, send the setup log to support.", Err: err}
	}
	return nil
}

func (p *Plan) keeperService(ctx context.Context, t *Txn) error {
	spec := KeeperSpec(p.Layout.KeeperExe)
	st, err := p.r.Sys.Svc.State(spec.Name)
	if err != nil {
		return err
	}
	if st == blackbox.SvcNotInstalled {
		if err := t.CreateService(spec); err != nil {
			return &StepError{Msg: "Windows would not create the key keeper service: " + err.Error() + ".", Fix: "Restart the PC, then run setup again.", Err: err}
		}
	} else {
		old, err := p.r.Sys.Svc.Config(spec.Name)
		if err != nil {
			return err
		}
		if !strings.EqualFold(old.Account, KeeperAccount) {
			// Its saved passwords are sealed for the account it runs as. Changing the account would lock them away for good.
			return &StepError{Msg: "The key keeper service runs as " + old.Account + ", not as " + KeeperAccount + ".",
				Fix: "Uninstall Unravis keeping your data, then install again."}
		}
		if !samePath(old.ExePath, spec.ExePath) || len(old.Args) != 0 {
			if err := t.SetServiceConfig(spec); err != nil {
				return err
			}
		}
	}
	return t.StartService(spec.Name)
}

// bbEnv lets blackbox.Manager install through the System.
type bbEnv struct {
	p   *Plan
	t   *Txn
	exe string
}

func (e bbEnv) IsAdmin() bool               { return e.p.r.Sys.Mach.IsAdmin() }
func (e bbEnv) ExePath() (string, error)    { return e.exe, nil }
func (e bbEnv) ProgramDir() (string, error) { return e.p.Layout.BlackboxDir, nil }
func (e bbEnv) SecureDir(dir string, usersRead bool) error {
	// Journalled: a recordings folder that was there before gets its old permissions back on a rollback.
	if usersRead {
		return e.t.SetSDDL(dir, BlackboxRecordingsSDDL)
	}
	return e.t.SetSDDL(dir, BlackboxProgramSDDL)
}
func (e bbEnv) PortFree(bind string, port int) error { return e.p.r.Sys.Mach.PortFree(bind, port) }
func (e bbEnv) FolderWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".unravis-setup-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
func (e bbEnv) FileSHA256(p string) (string, error) { return fileSHA256(p) }
func (e bbEnv) ProbeStatus(host string, port int, key string) error {
	if e.p.r.Sys.Probe == nil {
		return nil
	}
	return e.p.r.Sys.Probe(host, port, key)
}
func (e bbEnv) Sleep(d time.Duration) { e.p.r.Sys.sleep(d) }

// bbSvc lets blackbox.Manager use the System's services.
type bbSvc struct{ s Services }

func (b bbSvc) State(name string) (blackbox.SvcState, error) { return b.s.State(name) }
func (b bbSvc) Create(spec blackbox.ServiceSpec) error {
	return b.s.Create(ServiceSpec{Name: spec.Name, DisplayName: spec.DisplayName, Description: spec.Description, ExePath: spec.ExePath, Args: spec.Args})
}
func (b bbSvc) Start(name string) error  { return b.s.Start(name) }
func (b bbSvc) Stop(name string) error   { return b.s.Stop(name) }
func (b bbSvc) Delete(name string) error { return b.s.Delete(name) }

func (p *Plan) blackbox(ctx context.Context, t *Txn) error {
	cfg, _ := p.Opts.BlackboxConfig()
	exe, cfgPath, keyPath := p.Layout.BlackboxPaths()
	st, err := p.r.Sys.Svc.State(blackbox.ServiceName)
	if err != nil {
		return err
	}
	_, cfgErr := os.Stat(cfgPath)
	if p.Op != OpInstall && cfgErr == nil {
		return p.blackboxKeep(t, exe, cfgPath, keyPath, st)
	}
	if st != blackbox.SvcNotInstalled {
		return &StepError{Msg: "A service named " + blackbox.ServiceName + " already exists on this PC.",
			Fix: "Remove that Blackbox first (unravis-blackbox remove), or install Unravis without the Blackbox."}
	}
	_, dirErr := os.Lstat(p.Layout.BlackboxDir)
	_, recErr := os.Lstat(cfg.Folder)
	u := Undo{Kind: UndoBlackbox, Path: cfg.Folder, Existed: recErr == nil, Dir: p.Layout.BlackboxDir, DirCreated: errors.Is(dirErr, os.ErrNotExist),
		Note: "Blackbox installed: remove its service, settings and new recordings"}
	if err := t.record(u); err != nil {
		return err
	}
	m := &blackbox.Manager{Svc: bbSvc{p.r.Sys.Svc}, Env: bbEnv{p: p, t: t, exe: p.staged[BlackboxExeName]}, Disk: p.r.Sys.Disk}
	res, err := m.Install(cfg)
	if err != nil {
		var ie *blackbox.InstallError
		if errors.As(err, &ie) {
			return &StepError{Msg: ie.Message, Fix: "Change the Blackbox choices, or untick the Blackbox, then run setup again.", Err: err}
		}
		return err
	}
	p.readKey = res.ReadKey
	if p.Opts.ReadKeyFile != "" {
		return p.writeReadKeyFile(t, res.ReadKey)
	}
	return nil
}

// blackboxKeep is the Blackbox on an upgrade or a repair: its program is replaced, its settings kept (with a backup next to them), its
// service registered again if it went missing, and it is started.
func (p *Plan) blackboxKeep(t *Txn, exe, cfgPath, keyPath string, st blackbox.SvcState) error {
	cfg, err := blackbox.LoadConfig(cfgPath)
	if err != nil {
		return &StepError{Msg: "The Blackbox settings cannot be read: " + blackbox.PlainReason(err), Fix: "Fix blackbox.json (its backup is blackbox.json.bak), then run setup again.", Err: err}
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	if err := t.WriteFile(cfgPath+".bak", data, "Blackbox settings backup"); err != nil {
		return err
	}
	if staged, ok := p.staged[BlackboxExeName]; ok {
		if err := t.PlaceFile(staged, exe); err != nil {
			return &StepError{Msg: "Could not replace the Blackbox program: " + err.Error() + ".", Fix: "Restart the PC, then run setup again.", Err: err}
		}
	}
	if st == blackbox.SvcNotInstalled {
		if _, err := os.Stat(keyPath); err != nil {
			return &StepError{Msg: "The Blackbox service is missing and so is its read key.", Fix: "Uninstall the Blackbox (keep the recordings), then add it again."}
		}
		spec := ServiceSpec{Name: blackbox.ServiceName, DisplayName: "Unravis Blackbox",
			Description: "Records CPU, memory, disk, network and process load every few seconds so the evidence exists when something goes wrong. Read only.",
			ExePath:     exe, Args: []string{"service", "--config", cfgPath, "--key-file", keyPath}}
		if err := t.CreateService(spec); err != nil {
			return err
		}
	}
	_ = cfg
	return t.StartService(blackbox.ServiceName)
}

// writeReadKeyFile writes the silent-mode read key to the file the config names, readable by the person and the system only. The key
// itself is never logged.
func (p *Plan) writeReadKeyFile(t *Txn, key string) error {
	f := p.Opts.ReadKeyFile
	if err := t.MkdirAll(filepath.Dir(f)); err != nil {
		return err
	}
	if p.r.UserSID == "" {
		return errors.New("the person's account is not known, so the read key file cannot be locked to them")
	}
	if _, err := os.Lstat(f); err == nil {
		return readKeyFileExists(f)
	}
	// Made new, with its list in the create call: there is no moment when the key sits under the folder's inherited permissions.
	if err := t.CreateFileSDDL(f, ReadKeyFileSDDL(p.r.UserSID), []byte(key+"\r\n")); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readKeyFileExists(f)
		}
		return err
	}
	return nil
}

func readKeyFileExists(f string) error {
	return &StepError{Msg: "The read key file " + f + " is already there. Setup does not replace a file.",
		Fix: "Choose a file name that does not exist yet, then run setup again."}
}

func (p *Plan) firewall(ctx context.Context, t *Txn) error {
	cfg, _ := p.Opts.BlackboxConfig()
	exe, _, _ := p.Layout.BlackboxPaths()
	rule := FirewallRule{Name: FirewallRuleName(cfg.Port), Port: cfg.Port, RemoteAddresses: p.Opts.Firewall.RemoteAddresses, Program: exe}
	if old, err := p.r.Sys.FW.Get(rule.Name); err == nil && sameRule(old, rule) {
		return nil
	}
	return t.AddFirewallRule(rule)
}

func sameRule(a, b FirewallRule) bool {
	if a.Port != b.Port || !samePath(a.Program, b.Program) || len(a.RemoteAddresses) != len(b.RemoteAddresses) {
		return false
	}
	for i := range a.RemoteAddresses {
		if a.RemoteAddresses[i] != b.RemoteAddresses[i] {
			return false
		}
	}
	return true
}

func (p *Plan) shortcuts(ctx context.Context, t *Txn) error {
	link := Shortcut{Target: p.Layout.AppExe, WorkDir: p.Layout.InstallDir, Description: "Unravis: read-only audit investigations", Icon: p.Layout.AppExe}
	data, err := link.Bytes()
	if err != nil {
		return err
	}
	links := []string{p.Layout.StartMenuLink}
	if p.Opts.StartAtSignIn {
		links = append(links, p.Layout.StartupLink)
	}
	for _, l := range links {
		if err := t.MkdirAll(filepath.Dir(l)); err != nil {
			return err
		}
		if err := t.WriteFile(l, data, "shortcut"); err != nil {
			return err
		}
	}
	return nil
}

func (p *Plan) arp(ctx context.Context, t *Txn) error {
	root, key := p.Layout.ARPRoot, ARPKey
	if err := t.CreateKey(root, key, ""); err != nil {
		return err
	}
	setup := `"` + p.Layout.SetupExe + `"`
	values := []struct{ name, value string }{
		{"DisplayName", ProductName},
		{"DisplayVersion", p.Version},
		{"Publisher", ProductName},
		{"InstallLocation", p.Layout.InstallDir},
		{"DisplayIcon", p.Layout.AppExe},
		{"UninstallString", setup + " --uninstall"},
		{"QuietUninstallString", setup + " --uninstall --silent"},
		{"ModifyPath", setup + " --repair"},
	}
	for _, v := range values {
		if err := t.SetString(root, key, v.name, v.value); err != nil {
			return err
		}
	}
	size := uint32((p.r.Payload.Size() + 1023) / 1024)
	if err := t.SetDWORD(root, key, "EstimatedSize", size); err != nil {
		return err
	}
	return t.SetDWORD(root, key, "NoModify", 0)
}

func (p *Plan) saveState(ctx context.Context, t *Txn) error {
	s := State{Version: p.Version, Scope: p.Opts.Scope, Location: p.Layout.InstallDir, StartAtSignIn: p.Opts.StartAtSignIn,
		Blackbox: p.Opts.Blackbox, Firewall: p.Opts.Firewall, SharedRoot: p.Layout.SharedRoot, Installed: p.r.Sys.now().UTC()}
	if p.Old != nil && p.Op != OpInstall && !p.Old.State.Installed.IsZero() {
		s.Installed = p.Old.State.Installed
	}
	if p.Opts.Firewall != nil && p.Opts.Blackbox != nil {
		s.FirewallRule = FirewallRuleName(p.Opts.Blackbox.Port)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return t.WriteFile(p.Layout.StateFile, data, "setup choices")
}

// verify runs the placed program with --version: proof it starts before the old files (kept in the backup folder) are let go.
func (p *Plan) verify(ctx context.Context, t *Txn) error {
	if p.r.Sys.RunVersion == nil {
		return nil
	}
	out, err := p.r.Sys.RunVersion(p.Layout.AppExe)
	if err != nil {
		return &StepError{Msg: "The new unravis.exe did not start: " + err.Error() + ".",
			Fix: "Security software may be blocking it. Allow the Unravis folder in your antivirus, then run setup again.", Err: err}
	}
	if !strings.Contains(out, strings.TrimPrefix(p.Version, "v")) {
		return &StepError{Msg: "The program in place reports “" + strings.TrimSpace(out) + "”, not version " + p.Version + ".",
			Fix: "Run setup again; if it fails the same way, download the setup program again."}
	}
	return nil
}

// checkAdminOnly refuses a folder that someone other than administrators, the system or a service may change.
func checkAdminOnly(name string, fs datadir.FolderSecurity) error {
	const write = 0x2 | 0x4 | 0x10 | 0x40 | 0x100 | 0x10000 | 0x40000 | 0x80000 | 0x10000000 | 0x40000000
	switch fs.Owner {
	case "S-1-5-18", "S-1-5-32-544":
	default:
		return fmt.Errorf("%s is owned by %s", name, fs.Owner)
	}
	if fs.NullDACL {
		return fmt.Errorf("%s has no access list", name)
	}
	for _, a := range fs.ACEs {
		if a.Type != 0 {
			continue
		}
		switch a.SID {
		case "S-1-5-18", "S-1-5-32-544":
			continue
		}
		if a.Mask&write != 0 {
			return fmt.Errorf("%s lets %s change it", name, a.SID)
		}
	}
	return nil
}
