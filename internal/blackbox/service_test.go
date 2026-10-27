package blackbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSvc is a service manager that lives in memory.
type fakeSvc struct {
	state    SvcState
	spec     ServiceSpec
	calls    []string
	startErr error
	stuck    bool // Start leaves the service pending
	onStart  func()
}

func (f *fakeSvc) State(string) (SvcState, error) { return f.state, nil }
func (f *fakeSvc) Create(spec ServiceSpec) error {
	f.calls = append(f.calls, "create")
	f.spec, f.state = spec, SvcStopped
	return nil
}
func (f *fakeSvc) Start(string) error {
	f.calls = append(f.calls, "start")
	if f.onStart != nil {
		f.onStart()
	}
	if f.startErr != nil {
		return f.startErr
	}
	if f.stuck {
		f.state = SvcStartPending
	} else {
		f.state = SvcRunning
	}
	return nil
}
func (f *fakeSvc) Stop(string) error {
	f.calls = append(f.calls, "stop")
	f.state = SvcStopped
	return nil
}
func (f *fakeSvc) Delete(string) error {
	f.calls = append(f.calls, "delete")
	f.state = SvcNotInstalled
	return nil
}
func (f *fakeSvc) did(c string) int {
	n := 0
	for _, x := range f.calls {
		if x == c {
			n++
		}
	}
	return n
}

// fakeEnv answers for a machine we control. The "program" is a real file so its
// hash can change or the file can go.
type fakeEnv struct {
	admin     bool
	exe       string
	portErr   error
	folder    error
	probeErr  error
	listensOn string   // when set, only this host answers a probe
	probed    []string // host:port of each ProbeStatus call
	real      OSEnv
	slept     time.Duration
	prog      string // the program folder (stands for %ProgramFiles%\Unravis Blackbox)
	secureErr error
	secured   []string // folders SecureDir was asked about, in order, with the state of the program folder at the time
	log       []string
}

func (e *fakeEnv) IsAdmin() bool               { return e.admin }
func (e *fakeEnv) ExePath() (string, error)    { return e.exe, nil }
func (e *fakeEnv) ProgramDir() (string, error) { return e.prog, nil }
func (e *fakeEnv) SecureDir(dir string, usersRead bool) error {
	_, haveExe := os.Stat(filepath.Join(e.prog, InstalledExeName))
	e.log = append(e.log, fmt.Sprintf("secure %s read=%v exeAlreadyThere=%v", dir, usersRead, haveExe == nil))
	e.secured = append(e.secured, dir)
	return e.secureErr
}
func (e *fakeEnv) PortFree(string, int) error          { return e.portErr }
func (e *fakeEnv) FolderWritable(string) error         { return e.folder }
func (e *fakeEnv) FileSHA256(p string) (string, error) { return e.real.FileSHA256(p) }
func (e *fakeEnv) ProbeStatus(host string, port int, _ string) error {
	e.probed = append(e.probed, net.JoinHostPort(host, strconv.Itoa(port)))
	if e.listensOn != "" && host != e.listensOn {
		return errors.New("connection refused") // nothing listens on that address
	}
	return e.probeErr
}
func (e *fakeEnv) Sleep(d time.Duration) { e.slept += d }

type fixedDisk struct{ free uint64 }

func (d fixedDisk) Free(string) (DiskInfo, error) { return DiskInfo{Free: d.free, Total: 1 << 40}, nil }

type svcRig struct {
	m    *Manager
	svc  *fakeSvc
	env  *fakeEnv
	cfg  Config
	p    Paths // the settings files Install writes, in the program folder
	root string
	prog string
}

func newSvcRig(t *testing.T) *svcRig {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "unravis-blackbox.exe")
	if err := os.WriteFile(exe, []byte("MZ program"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Name = "web-b"
	cfg.Folder = filepath.Join(root, "data", "UnravisBlackbox")
	cfg.Port = 7788
	// The zip is unpacked in root; the service is installed into prog.
	prog := filepath.Join(root, "Program Files", "Unravis Blackbox")
	g := &svcRig{svc: &fakeSvc{}, env: &fakeEnv{admin: true, exe: exe, prog: prog}, cfg: cfg, root: root, prog: prog,
		p: Paths{ConfigPath: filepath.Join(prog, ConfigFileName), KeyPath: filepath.Join(prog, KeyFileName)}}
	if err := os.MkdirAll(prog, 0o755); err != nil {
		t.Fatal(err)
	}
	g.m = &Manager{Svc: g.svc, Env: g.env, Disk: fixedDisk{free: 50 << 30}}
	return g
}

// nothingLeft checks that a failed install left no service, folder, config or key.
func (g *svcRig) nothingLeft(t *testing.T) {
	t.Helper()
	if g.svc.state != SvcNotInstalled {
		t.Fatalf("service left behind: %s", g.svc.state)
	}
	for _, p := range []string{g.cfg.Folder, filepath.Dir(g.cfg.Folder), g.p.ConfigPath, g.p.KeyPath, g.p.ConfigPath + ".bak", filepath.Join(g.prog, InstalledExeName)} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("left behind: %s", p)
		}
	}
}

func installReason(t *testing.T, err error) Reason {
	t.Helper()
	var ie *InstallError
	if !errors.As(err, &ie) {
		t.Fatalf("not an InstallError: %v", err)
	}
	return ie.Reason
}

func TestInstallHappyPath(t *testing.T) {
	g := newSvcRig(t)
	res, err := g.m.Install(g.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g.svc.state != SvcRunning || g.svc.spec.Name != ServiceName || g.svc.spec.ExePath != filepath.Join(g.prog, InstalledExeName) {
		t.Fatalf("%+v %s", g.svc.spec, g.svc.state)
	}
	// The key is shown once and only its hash is kept.
	kc, err := LoadKeyCheck(g.p.KeyPath)
	if err != nil || !kc.Check(res.ReadKey) {
		t.Fatalf("key hash: %v", err)
	}
	cfgText, _ := os.ReadFile(g.p.ConfigPath)
	if strings.Contains(string(cfgText), res.ReadKey) {
		t.Fatal("the key is in the config")
	}
	for _, a := range g.svc.spec.Args {
		if strings.Contains(a, res.ReadKey) {
			t.Fatal("the key is on the service command line")
		}
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || got.Name != "web-b" || got.Port != 7788 {
		t.Fatalf("%+v %v", got, err)
	}
	if st, _ := os.Stat(g.cfg.Folder); st == nil || !st.IsDir() {
		t.Fatal("folder not made")
	}
}

func TestInstallFiveFailureReasonsLeaveNothingBehind(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(g *svcRig)
		reason Reason
		text   string
	}{
		{"no admin", func(g *svcRig) { g.env.admin = false }, ReasonNoAdmin, "administrator"},
		{"port busy", func(g *svcRig) { g.env.portErr = errors.New("in use") }, ReasonPortBusy, "Port 7788 is busy"},
		{"read-only folder", func(g *svcRig) { g.env.folder = errors.New("access denied") }, ReasonFolder, "read-only or has under 1 GB free"},
		{"disk nearly full", func(g *svcRig) { g.m.Disk = fixedDisk{free: 100 << 20} }, ReasonFolder, "under 1 GB free"},
		{"antivirus ate the program", func(g *svcRig) {
			g.svc.onStart = func() { _ = os.Remove(filepath.Join(g.prog, InstalledExeName)) }
			g.svc.startErr = errors.New("the file cannot be found")
		}, ReasonAntivirus, "Antivirus blocked it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newSvcRig(t)
			c.setup(g)
			_, err := g.m.Install(g.cfg)
			if err == nil {
				t.Fatal("install should fail")
			}
			if r := installReason(t, err); r != c.reason {
				t.Fatalf("reason %s: %v", r, err)
			}
			if !strings.Contains(err.Error(), c.text) {
				t.Fatalf("message %q lacks %q", err.Error(), c.text)
			}
			g.nothingLeft(t)
		})
	}
}

// F2: the service runs as LocalSystem, so it must not run a program from where
// the zip was unpacked (any user may be able to write there).
func TestInstallRunsTheServiceFromTheLockedProgramFolderNotTheUnpackedZip(t *testing.T) {
	g := newSvcRig(t)
	res, err := g.m.Install(g.cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(g.prog, InstalledExeName)
	if g.svc.spec.ExePath != want || res.ExePath != want {
		t.Fatalf("service runs %q, want the copy in %q", g.svc.spec.ExePath, g.prog)
	}
	if samePath(g.svc.spec.ExePath, g.env.exe) {
		t.Fatal("the service runs the unpacked zip's program")
	}
	copied, err := os.ReadFile(want)
	orig, _ := os.ReadFile(g.env.exe)
	if err != nil || string(copied) != string(orig) {
		t.Fatalf("the installed program is not a copy of the original: %v", err)
	}
	// Settings and key hash are read from the locked folder too.
	args := strings.Join(g.svc.spec.Args, " ")
	if !strings.Contains(args, filepath.Join(g.prog, ConfigFileName)) || !strings.Contains(args, filepath.Join(g.prog, KeyFileName)) {
		t.Fatalf("service reads its settings from elsewhere: %s", args)
	}
	// The program folder is locked before the program goes in, with no read
	// access for users; the recordings folder is locked too, readable by users.
	if len(g.env.log) != 2 {
		t.Fatalf("%v", g.env.log)
	}
	if !strings.Contains(g.env.log[0], g.cfg.Folder) || !strings.Contains(g.env.log[0], "read=true") {
		t.Fatalf("recordings folder not locked: %v", g.env.log)
	}
	if !strings.Contains(g.env.log[1], g.prog) || !strings.Contains(g.env.log[1], "read=false") || !strings.Contains(g.env.log[1], "exeAlreadyThere=false") {
		t.Fatalf("program folder must be locked before the program is copied in: %v", g.env.log)
	}
}

func TestInstallFromTheProgramFolderItselfDoesNotCopyOntoItself(t *testing.T) {
	g := newSvcRig(t)
	exe := filepath.Join(g.prog, InstalledExeName)
	if err := os.WriteFile(exe, []byte("MZ program"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.env.exe = exe
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZ program" || g.svc.spec.ExePath != exe {
		t.Fatalf("%q %s", b, g.svc.spec.ExePath)
	}
}

func TestInstallThatCannotLockTheProgramFolderStopsAndLeavesNothing(t *testing.T) {
	g := newSvcRig(t)
	g.env.secureErr = errors.New("access denied")
	_, err := g.m.Install(g.cfg)
	if err == nil || !strings.Contains(err.Error(), "only administrators may change it") && !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("%v", err)
	}
	if g.svc.did("create") != 0 {
		t.Fatal("a service was registered although the folders could not be locked")
	}
	g.nothingLeft(t)
}

func TestInstallUndoRemovesTheCopiedProgram(t *testing.T) {
	g := newSvcRig(t)
	g.svc.startErr = errors.New("boom")
	if _, err := g.m.Install(g.cfg); err == nil {
		t.Fatal("should fail")
	}
	if _, err := os.Stat(filepath.Join(g.prog, InstalledExeName)); err == nil {
		t.Fatal("the copied program was left behind")
	}
	if _, err := os.Stat(g.env.exe); err != nil {
		t.Fatal("the user's own program file was removed")
	}
}

func TestRemoveDeletesTheInstalledProgramCopy(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := g.m.Remove(g.cfg, g.p.KeyPath, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.prog, InstalledExeName)); err == nil {
		t.Fatal("the installed program copy was left in the program folder")
	}
}

func TestInstallTwiceSaysAlreadyInstalledAndKeepsTheFirst(t *testing.T) {
	g := newSvcRig(t)
	first, err := g.m.Install(g.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The first install is running and holds the port: the second must still say
	// "already installed", not "port busy".
	g.env.portErr = errors.New("in use")
	_, err = g.m.Install(g.cfg)
	if installReason(t, err) != ReasonExists {
		t.Fatalf("%v", err)
	}
	if g.svc.state != SvcRunning || g.svc.did("create") != 1 || g.svc.did("delete") != 0 {
		t.Fatalf("the first install was disturbed: %v", g.svc.calls)
	}
	kc, _ := LoadKeyCheck(g.p.KeyPath)
	if !kc.Check(first.ReadKey) {
		t.Fatal("the first key was replaced")
	}
	if _, err := os.Stat(g.cfg.Folder); err != nil {
		t.Fatal("the first folder was removed")
	}
}

func TestInstallUndoKeepsPreviousConfigAndKey(t *testing.T) {
	g := newSvcRig(t)
	if err := os.WriteFile(g.p.ConfigPath, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.p.KeyPath, []byte("oldhash"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.svc.startErr = errors.New("boom")
	if _, err := g.m.Install(g.cfg); err == nil {
		t.Fatal("should fail")
	}
	b, _ := os.ReadFile(g.p.ConfigPath)
	k, _ := os.ReadFile(g.p.KeyPath)
	if string(b) != `{"old":true}` || string(k) != "oldhash" {
		t.Fatalf("not restored: %q %q", b, k)
	}
}

func TestInstallServiceThatNeverRunsOrAnswersIsUndone(t *testing.T) {
	g := newSvcRig(t)
	g.svc.stuck = true
	_, err := g.m.Install(g.cfg)
	if installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	g.svc.stuck = false
	g.nothingLeft(t)

	g2 := newSvcRig(t)
	g2.env.probeErr = errors.New("connection refused")
	_, err = g2.m.Install(g2.cfg)
	if installReason(t, err) != ReasonOther {
		t.Fatalf("%v", err)
	}
	g2.nothingLeft(t)
}

func TestInstallRefusesBadConfigBeforeTouchingAnything(t *testing.T) {
	g := newSvcRig(t)
	g.cfg.Port = 0
	if _, err := g.m.Install(g.cfg); err == nil {
		t.Fatal("accepted")
	}
	if len(g.svc.calls) != 0 {
		t.Fatalf("%v", g.svc.calls)
	}
}

func TestStartStopApply(t *testing.T) {
	g := newSvcRig(t)
	if err := g.m.Start(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if err := g.m.Stop(); err != nil || g.svc.state != SvcStopped {
		t.Fatalf("%v %s", err, g.svc.state)
	}
	if err := g.m.Stop(); err != nil {
		t.Fatal("stopping a stopped service must not fail")
	}
	// Apply on a stopped service changes nothing running.
	res, err := g.m.Apply(g.p.ConfigPath)
	if err != nil || res.Restarted {
		t.Fatalf("%+v %v", res, err)
	}
	if err := g.m.Start(); err != nil || g.svc.state != SvcRunning {
		t.Fatalf("%v", err)
	}
	if err := g.m.Start(); err != nil {
		t.Fatal("starting a running service must not fail")
	}
	before := g.svc.did("start")
	res, err = g.m.Apply(g.p.ConfigPath)
	if err != nil || !res.Restarted || g.svc.did("start") != before+1 || g.svc.state != SvcRunning {
		t.Fatalf("%+v %v %v", res, err, g.svc.calls)
	}
}

func TestApplyWithBrokenConfigStopsNothing(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.p.ConfigPath, []byte(`{"port": 0, "keepDays": `), 0o644); err != nil {
		t.Fatal(err)
	}
	stops := g.svc.did("stop")
	if _, err := g.m.Apply(g.p.ConfigPath); err == nil {
		t.Fatal("accepted a broken file")
	}
	if g.svc.did("stop") != stops || g.svc.state != SvcRunning {
		t.Fatal("the recorder was stopped by a typo")
	}
}

func TestServiceCommandsNeedAdmin(t *testing.T) {
	g := newSvcRig(t)
	g.env.admin = false
	if err := g.m.Start(); !errors.Is(err, ErrNotAdmin) {
		t.Fatal(err)
	}
	if err := g.m.Stop(); !errors.Is(err, ErrNotAdmin) {
		t.Fatal(err)
	}
	if _, err := g.m.Apply(g.p.ConfigPath); !errors.Is(err, ErrNotAdmin) {
		t.Fatal(err)
	}
	if _, err := g.m.Remove(g.cfg, g.p.KeyPath, false, ""); !errors.Is(err, ErrNotAdmin) {
		t.Fatal(err)
	}
}

func TestRemoveKeepsRecordingsUnlessAskedAndNamed(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(g.cfg.Folder, FileNameFor(t0))
	stranger := filepath.Join(g.cfg.Folder, "my-notes.txt")
	for _, f := range []string{rec, filepath.Join(g.cfg.Folder, ManifestName), stranger} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Wrong name: nothing at all is changed.
	if _, err := g.m.Remove(g.cfg, g.p.KeyPath, true, "web-c"); !errors.Is(err, ErrConfirmName) {
		t.Fatalf("%v", err)
	}
	if g.svc.state != SvcRunning {
		t.Fatal("service touched before the name was confirmed")
	}
	// Plain remove: service and key go, recordings stay.
	res, err := g.m.Remove(g.cfg, g.p.KeyPath, false, "")
	if err != nil || !res.ServiceRemoved || res.RecordingsDeleted {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Fatal("recordings were deleted")
	}
	if _, err := os.Stat(g.p.KeyPath); err == nil {
		t.Fatal("key hash left behind")
	}
	// Remove again, with recordings and the right name.
	res, err = g.m.Remove(g.cfg, g.p.KeyPath, true, "web-b")
	if err != nil || !res.RecordingsDeleted || res.FilesDeleted != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("recording left")
	}
	if _, err := os.Stat(stranger); err != nil {
		t.Fatal("a file that is not the Blackbox's was deleted")
	}
	if _, err := os.Stat(g.cfg.Folder); err != nil {
		t.Fatal("a folder with someone else's file in it was removed")
	}
}

func TestRemoveWhenNothingIsInstalled(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Remove(g.cfg, g.p.KeyPath, false, ""); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
}

func TestRemoveStopsARunningServiceFirst(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := g.m.Remove(g.cfg, g.p.KeyPath, false, ""); err != nil {
		t.Fatal(err)
	}
	n := len(g.svc.calls)
	if g.svc.calls[n-2] != "stop" || g.svc.calls[n-1] != "delete" {
		t.Fatalf("%v", g.svc.calls)
	}
}

func TestRemoveRecordingsRemovesFolderWhenEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec")
	_ = os.MkdirAll(filepath.Join(dir, SnapshotsDir, SnapshotName("web-b", t0)), 0o755)
	_ = os.WriteFile(filepath.Join(dir, FileNameFor(t0)), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ManifestName+".bad-20261027"), []byte("a"), 0o644)
	n, err := RemoveRecordings(dir)
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("empty folder kept")
	}
	if n, err := RemoveRecordings(dir); err != nil || n != 0 {
		t.Fatalf("missing folder: %d %v", n, err)
	}
}

// ---- snapshot ----

func snapRig(t *testing.T) (Config, string) {
	t.Helper()
	cfg := Default()
	cfg.Name = "web-b"
	cfg.Folder = t.TempDir()
	logs := t.TempDir()
	cfg.LogPaths = []string{logs}
	for i, h := range []time.Time{t0, t0.Add(time.Hour)} {
		if err := os.WriteFile(filepath.Join(cfg.Folder, FileNameFor(h)), []byte(strings.Repeat("{}\n", 10+i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(cfg.Folder, ManifestName), []byte(`{"format":1}`), 0o644)
	_ = os.WriteFile(filepath.Join(cfg.Folder, "readkey.sha256"), []byte("secret-hash"), 0o644)
	return cfg, logs
}

func TestSnapshotCopiesRecordingsNotLogsByDefault(t *testing.T) {
	cfg, logs := snapRig(t)
	writeLog(t, logs, "u_ex.log")
	now := time.Date(2026, 10, 27, 11, 5, 0, 0, time.Local)
	res, err := Snapshot(cfg, now, SnapshotOptions{Disk: fixedDisk{free: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Dir) != "web-b-2026-10-27-1105" || filepath.Base(filepath.Dir(res.Dir)) != SnapshotsDir {
		t.Fatalf("%s", res.Dir)
	}
	if res.Recordings != 2 || res.Logs != 0 {
		t.Fatalf("%+v", res)
	}
	ents, _ := os.ReadDir(res.Dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, ManifestName) || strings.Contains(joined, "readkey") || strings.Contains(joined, "logs") {
		t.Fatalf("%v", names)
	}
	// Recordings are byte for byte.
	a, _ := os.ReadFile(filepath.Join(cfg.Folder, FileNameFor(t0)))
	b, _ := os.ReadFile(filepath.Join(res.Dir, FileNameFor(t0)))
	if string(a) != string(b) {
		t.Fatal("not copied as they are")
	}
}

func TestSnapshotCopiesLogsOnlyWhenSettingIsTicked(t *testing.T) {
	cfg, logs := snapRig(t)
	cfg.Record.CopyLogsOnSnapshot = true
	writeLog(t, logs, "u_ex.log")
	old := filepath.Join(logs, "old.log")
	writeLog(t, logs, "old.log")
	_ = os.Chtimes(old, t0.Add(-48*time.Hour), t0.Add(-48*time.Hour))
	_ = os.Chtimes(filepath.Join(logs, "u_ex.log"), t0.Add(30*time.Minute), t0.Add(30*time.Minute))
	res, err := Snapshot(cfg, time.Now(), SnapshotOptions{Disk: fixedDisk{free: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Logs != 1 {
		t.Fatalf("%+v", res)
	}
	ents, _ := os.ReadDir(filepath.Join(res.Dir, "logs"))
	if len(ents) != 1 || !strings.HasSuffix(ents[0].Name(), "u_ex.log") {
		t.Fatalf("%v", ents)
	}
}

func TestSnapshotTwiceInOneMinuteKeepsBoth(t *testing.T) {
	cfg, _ := snapRig(t)
	now := time.Date(2026, 10, 27, 11, 5, 0, 0, time.Local)
	a, err1 := Snapshot(cfg, now, SnapshotOptions{Disk: fixedDisk{free: 1 << 30}})
	b, err2 := Snapshot(cfg, now, SnapshotOptions{Disk: fixedDisk{free: 1 << 30}})
	if err1 != nil || err2 != nil || a.Dir == b.Dir {
		t.Fatalf("%v %v %s %s", err1, err2, a.Dir, b.Dir)
	}
}

func TestSnapshotDriveFullIsRefusedUpFrontAndLeavesNothing(t *testing.T) {
	cfg, _ := snapRig(t)
	_, err := Snapshot(cfg, time.Now(), SnapshotOptions{Disk: fixedDisk{free: 5}})
	var se *SnapshotError
	if !errors.As(err, &se) || se.Reason != SnapDriveFull || !strings.Contains(se.Message, "The drive is full") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Folder, SnapshotsDir)); err == nil {
		t.Fatal("snapshots folder left")
	}
}

func TestSnapshotFailureMidwayRemovesHalfSnapshot(t *testing.T) {
	cfg, _ := snapRig(t)
	calls := 0
	opt := SnapshotOptions{Disk: fixedDisk{free: 1 << 30}, CopyFile: func(src, dst string) (int64, error) {
		calls++
		if calls == 2 {
			return 0, &openErr{errors.New("The process cannot access the file because it is being used by another process.")}
		}
		return defaultCopy(src, dst)
	}}
	_, err := Snapshot(cfg, time.Now(), opt)
	var se *SnapshotError
	if !errors.As(err, &se) || se.Reason != SnapFileOpen || !strings.Contains(se.Message, "open in another program") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Folder, SnapshotsDir)); err == nil {
		t.Fatal("half snapshot left")
	}

	// Disk fills during the copy.
	calls = 0
	opt.CopyFile = func(src, dst string) (int64, error) {
		calls++
		if calls == 2 {
			return 0, errors.New("There is not enough space on the disk.")
		}
		return defaultCopy(src, dst)
	}
	_, err = Snapshot(cfg, time.Now(), opt)
	if !errors.As(err, &se) || se.Reason != SnapDriveFull {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Folder, SnapshotsDir)); err == nil {
		t.Fatal("half snapshot left")
	}
}

func TestSnapshotKeepsEarlierSnapshotsOnFailure(t *testing.T) {
	cfg, _ := snapRig(t)
	first, err := Snapshot(cfg, time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local), SnapshotOptions{Disk: fixedDisk{free: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Snapshot(cfg, time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local), SnapshotOptions{Disk: fixedDisk{free: 1 << 30},
		CopyFile: func(string, string) (int64, error) { return 0, &openErr{errors.New("locked")} }})
	if err == nil {
		t.Fatal("should fail")
	}
	if _, err := os.Stat(first.Dir); err != nil {
		t.Fatal("an earlier snapshot was removed")
	}
}

func TestSnapshotWithNoRecordings(t *testing.T) {
	cfg := Default()
	cfg.Name = "box"
	cfg.Folder = t.TempDir()
	if _, err := Snapshot(cfg, time.Now(), SnapshotOptions{Disk: fixedDisk{free: 1 << 30}}); err == nil {
		t.Fatal("empty snapshot made")
	}
}

func TestOSEnvPortFreeSeesARealListener(t *testing.T) {
	ln, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	n := ln.Addr().(*net.TCPAddr).Port
	if (OSEnv{}).PortFree("127.0.0.1", n) == nil {
		t.Fatal("a busy port was reported free")
	}
	ln.Close()
	if err := (OSEnv{}).PortFree("127.0.0.1", n); err != nil {
		t.Fatalf("a free port was reported busy: %v", err)
	}
}

func TestOSEnvFolderWritableAndHash(t *testing.T) {
	dir := t.TempDir()
	if err := (OSEnv{}).FolderWritable(dir); err != nil {
		t.Fatal(err)
	}
	if err := (OSEnv{}).FolderWritable(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing folder is not writable")
	}
	f := filepath.Join(dir, "a")
	_ = os.WriteFile(f, []byte("abc"), 0o644)
	h, err := (OSEnv{}).FileSHA256(f)
	if err != nil || h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("%s %v", h, err)
	}
}

func TestProbeHost(t *testing.T) {
	for bind, want := range map[string]string{
		"": "127.0.0.1", "0.0.0.0": "127.0.0.1", "::": "127.0.0.1", "[::]": "127.0.0.1",
		"127.0.0.1": "127.0.0.1", "127.0.0.2": "127.0.0.2", "::1": "::1",
		"10.0.0.5": "10.0.0.5", "192.168.1.20": "192.168.1.20", "fe80::1": "fe80::1",
	} {
		if got := ProbeHost(bind); got != want {
			t.Errorf("ProbeHost(%q) = %q, want %q", bind, got, want)
		}
	}
}

// Review F5: install --bind <LAN address> failed and was undone, because the
// probe only ever asked 127.0.0.1, where nothing listens.
func TestInstallWithASpecificBindProbesThatAddress(t *testing.T) {
	for _, tc := range []struct{ bind, listensOn, want string }{
		{"10.0.0.5", "10.0.0.5", "10.0.0.5:7788"},
		{"::1", "::1", "[::1]:7788"},
		{"0.0.0.0", "127.0.0.1", "127.0.0.1:7788"},
		{"127.0.0.1", "127.0.0.1", "127.0.0.1:7788"},
	} {
		g := newSvcRig(t)
		g.env.listensOn = tc.listensOn
		cfg := g.cfg
		cfg.Bind = tc.bind
		if _, err := g.m.Install(cfg); err != nil {
			t.Fatalf("bind %s: %v", tc.bind, err)
		}
		if len(g.env.probed) == 0 || g.env.probed[0] != tc.want {
			t.Fatalf("bind %s probed %v, want %s", tc.bind, g.env.probed, tc.want)
		}
		if g.svc.state != SvcRunning {
			t.Fatalf("bind %s: service not left running", tc.bind)
		}
	}
}

// Review F6.2: Remove deletes only what the recorder made, even when the folder
// is shared with other things.
func TestRemoveRecordingsSparesEverythingTheRecorderDidNotMake(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	snap := filepath.Join(dir, SnapshotsDir)
	mine := []string{
		filepath.Join(snap, SnapshotName("web-b", t0)),
		filepath.Join(snap, SnapshotName("web-b", t0)+"-2"),
	}
	theirs := []string{
		filepath.Join(snap, "my-old-reports"), // someone else's folder under snapshots
		filepath.Join(dir, "reports"),         // a folder next to the recordings
		filepath.Join(dir, "reports", "q3"),
	}
	for _, d := range append(append([]string{}, mine...), theirs...) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(d, "f.txt"), []byte("x"), 0o644)
	}
	files := map[string]bool{ // name -> must survive
		FileNameFor(t0): false, ManifestName: false, ManifestName + ".tmp-123": false,
		"backup.tmp-2026.sql": true, "notes.txt": true, "bb-notes.ndjson": true, "snapshots.txt": true,
	}
	for n := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RemoveRecordings(dir); err != nil {
		t.Fatal(err)
	}
	for n, keep := range files {
		_, err := os.Stat(filepath.Join(dir, n))
		if keep != (err == nil) {
			t.Errorf("%s: kept=%v want %v", n, err == nil, keep)
		}
	}
	for _, d := range mine {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("snapshot %s should be gone", d)
		}
	}
	for _, d := range theirs {
		if _, err := os.Stat(filepath.Join(d, "f.txt")); err != nil {
			t.Errorf("%s was deleted", d)
		}
	}
}

func TestRemoveRecordingsRefusesDriveRootsAndSystemFolders(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	for _, p := range unsafeFolderCases {
		if _, err := RemoveRecordings(p); err == nil {
			t.Errorf("RemoveRecordings(%q) went ahead", p)
		}
	}
}

// Review F6.1: a service that started and wrote samples before the status
// probe failed leaves nothing behind when the install is undone.
func TestInstallUndoRemovesRecordingsTheServiceWrote(t *testing.T) {
	write := func(dir string) {
		_ = os.WriteFile(filepath.Join(dir, FileNameFor(t0)), []byte("{}"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, ManifestName), []byte("{}"), 0o644)
		_ = os.MkdirAll(filepath.Join(dir, SnapshotsDir, SnapshotName("web-b", t0)), 0o755)
	}
	t.Run("folder made by the install is gone", func(t *testing.T) {
		g := newSvcRig(t)
		g.svc.onStart = func() { write(g.cfg.Folder) }
		g.env.probeErr = errors.New("connection refused")
		if _, err := g.m.Install(g.cfg); err == nil {
			t.Fatal("install should fail")
		}
		g.nothingLeft(t)
	})
	t.Run("a folder that was already there keeps what it had", func(t *testing.T) {
		g := newSvcRig(t)
		_ = os.MkdirAll(g.cfg.Folder, 0o755)
		old := filepath.Join(g.cfg.Folder, FileNameFor(t0.Add(-48*time.Hour)))
		notes := filepath.Join(g.cfg.Folder, "notes.txt")
		_ = os.WriteFile(old, []byte("old"), 0o644)
		_ = os.WriteFile(notes, []byte("mine"), 0o644)
		g.svc.onStart = func() { write(g.cfg.Folder) }
		g.env.probeErr = errors.New("connection refused")
		if _, err := g.m.Install(g.cfg); err == nil {
			t.Fatal("install should fail")
		}
		for _, p := range []string{old, notes} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s was removed", p)
			}
		}
		for _, p := range []string{filepath.Join(g.cfg.Folder, FileNameFor(t0)), filepath.Join(g.cfg.Folder, ManifestName), filepath.Join(g.cfg.Folder, SnapshotsDir)} {
			if _, err := os.Stat(p); err == nil {
				t.Fatalf("%s left behind", p)
			}
		}
		if _, err := os.Stat(g.cfg.Folder); err != nil {
			t.Fatal("pre-existing folder removed")
		}
	})
}
