package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

type memSvc struct {
	state    blackbox.SvcState
	calls    []string
	spec     blackbox.ServiceSpec
	startErr error
}

func (s *memSvc) State(string) (blackbox.SvcState, error) { return s.state, nil }
func (s *memSvc) Create(spec blackbox.ServiceSpec) error {
	s.calls = append(s.calls, "create")
	s.spec = spec
	s.state = blackbox.SvcStopped
	return nil
}
func (s *memSvc) Start(string) error {
	s.calls = append(s.calls, "start")
	if s.startErr != nil {
		return s.startErr
	}
	s.state = blackbox.SvcRunning
	return nil
}
func (s *memSvc) Stop(string) error {
	s.calls = append(s.calls, "stop")
	s.state = blackbox.SvcStopped
	return nil
}
func (s *memSvc) Delete(string) error {
	s.calls = append(s.calls, "delete")
	s.state = blackbox.SvcNotInstalled
	return nil
}

type memEnv struct {
	admin bool
	exe   string
	port  error
	prog  string
	// listensOn, when set, makes the probe fail for any other host, as a
	// service bound to one address would.
	listensOn string
	probed    []string
}

func (e *memEnv) IsAdmin() bool                       { return e.admin }
func (e *memEnv) ExePath() (string, error)            { return e.exe, nil }
func (e *memEnv) ProgramDir() (string, error)         { return e.prog, nil }
func (e *memEnv) SecureDir(string, bool) error        { return nil }
func (e *memEnv) PortFree(string, int) error          { return e.port }
func (e *memEnv) FolderWritable(string) error         { return nil }
func (e *memEnv) FileSHA256(p string) (string, error) { return blackbox.OSEnv{}.FileSHA256(p) }
func (e *memEnv) ProbeStatus(host string, _ int, _ string) error {
	e.probed = append(e.probed, host)
	if e.listensOn != "" && host != e.listensOn {
		return errors.New("connection refused")
	}
	return nil
}
func (e *memEnv) Sleep(time.Duration) {}

type bigDisk struct{}

func (bigDisk) Free(string) (blackbox.DiskInfo, error) {
	return blackbox.DiskInfo{Free: 100 << 30, Total: 200 << 30}, nil
}

type rig struct {
	d    deps
	svc  *memSvc
	env  *memEnv
	dir  string
	args []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "unravis-blackbox.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &rig{svc: &memSvc{}, env: &memEnv{admin: true, exe: exe, prog: filepath.Join(dir, "Program Files", "Unravis Blackbox")}, dir: dir}
	r.d = deps{
		mgr:      &blackbox.Manager{Svc: r.svc, Env: r.env, Disk: bigDisk{}},
		exeDir:   func() string { return dir },
		hostname: func() string { return "WEB_B.corp" },
		now:      func() time.Time { return time.Date(2026, 10, 27, 11, 5, 0, 0, time.Local) },
		disk:     bigDisk{},
		snapOpt:  blackbox.SnapshotOptions{Disk: bigDisk{}},
		driveOK:  func(string) bool { return false },
		hostRun:  func(context.Context, blackbox.HostOptions) error { return nil },
	}
	// Every test installs into the temp dir, never onto the real C: or D:.
	r.args = []string{"--folder", filepath.Join(dir, "rec")}
	return r
}

func (r *rig) do(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, r.d)
	return code, out.String(), errb.String()
}

func TestVersionAndUsage(t *testing.T) {
	r := newRig(t)
	if c, out, _ := r.do("version"); c != 0 || !strings.HasPrefix(out, "unravis-blackbox ") {
		t.Fatalf("%d %q", c, out)
	}
	if c, _, errs := r.do(); c != exitUsage || !strings.Contains(errs, "Usage:") {
		t.Fatalf("%d %q", c, errs)
	}
	if c, _, errs := r.do("explode"); c != exitUsage || !strings.Contains(errs, "unknown command") {
		t.Fatalf("%d %q", c, errs)
	}
	if c, _, _ := r.do("start", "stray"); c != exitUsage {
		t.Fatalf("stray argument: %d", c)
	}
	if c, _, _ := r.do("start", "--nonsense"); c != exitUsage {
		t.Fatalf("unknown flag: %d", c)
	}
}

func TestInstallPrintsKeyOnceAndNamesTheBoxAfterTheComputer(t *testing.T) {
	r := newRig(t)
	c, out, errs := r.do(append([]string{"install", "--json"}, r.args...)...)
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	key, _ := got["readKey"].(string)
	if got["ok"] != true || !strings.HasPrefix(key, "ubk-") || got["name"] != "WEB-B-corp" {
		t.Fatalf("%v", got)
	}
	cfg, err := blackbox.LoadConfig(filepath.Join(r.env.prog, defaultConfigName))
	if err != nil || cfg.Name != "WEB-B-corp" || cfg.Port != 7788 {
		t.Fatalf("%+v %v", cfg, err)
	}
	// The key is nowhere on disk but as a hash.
	filepath.Walk(r.dir, func(p string, i os.FileInfo, _ error) error {
		if i != nil && !i.IsDir() && i.Size() < 1<<20 {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), key) {
				t.Errorf("the key is stored in %s", p)
			}
		}
		return nil
	})
	// Text form says it is shown once.
	r2 := newRig(t)
	_, out2, _ := r2.do(append([]string{"install"}, r2.args...)...)
	if !strings.Contains(out2, "shown once") || !strings.Contains(out2, "ubk-") {
		t.Fatalf("%s", out2)
	}
}

func TestInstallFlagsOverrideAndFailureIsJSONWithReason(t *testing.T) {
	r := newRig(t)
	r.env.port = errors.New("busy")
	c, out, _ := r.do(append([]string{"install", "--json", "--name", "box-1", "--port", "7799"}, r.args...)...)
	if c != exitFail {
		t.Fatalf("%d", c)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got["ok"] != false || got["reason"] != "port-busy" || !strings.Contains(got["message"].(string), "Port 7799 is busy") {
		t.Fatalf("%v", got)
	}
	if r.svc.state != blackbox.SvcNotInstalled {
		t.Fatal("service left behind")
	}
}

func TestInstallNotAdminAndBadFlags(t *testing.T) {
	r := newRig(t)
	r.env.admin = false
	c, _, errs := r.do(append([]string{"install"}, r.args...)...)
	if c != exitFail || !strings.Contains(errs, "administrator") {
		t.Fatalf("%d %s", c, errs)
	}
	r.env.admin = true
	if c, _, errs := r.do(append([]string{"install", "--name", "bad name!"}, r.args...)...); c != exitFail || !strings.Contains(errs, "name") {
		t.Fatalf("%d %s", c, errs)
	}
	if c, _, _ := r.do(append([]string{"install", "--port", "70000"}, r.args...)...); c != exitFail {
		t.Fatalf("%d", c)
	}
}

func TestStartStopApplyRemoveThroughTheCommandLine(t *testing.T) {
	r := newRig(t)
	if c, _, errs := r.do("start"); c != exitFail || !strings.Contains(errs, "not installed") {
		t.Fatalf("%d %s", c, errs)
	}
	if c, _, _ := r.do(append([]string{"install"}, r.args...)...); c != 0 {
		t.Fatal("install")
	}
	if c, out, _ := r.do("stop"); c != 0 || !strings.Contains(out, "Recordings are kept") {
		t.Fatalf("%d %s", c, out)
	}
	if c, out, _ := r.do("apply"); c != 0 || !strings.Contains(out, "next start") {
		t.Fatalf("%d %s", c, out)
	}
	if c, _, _ := r.do("start"); c != 0 {
		t.Fatal("start")
	}
	if c, out, _ := r.do("apply"); c != 0 || !strings.Contains(out, "restarted") {
		t.Fatalf("%d %s", c, out)
	}
	if c, out, _ := r.do("remove"); c != 0 || !strings.Contains(out, "recordings are kept") {
		t.Fatalf("%d %s", c, out)
	}
	if r.svc.state != blackbox.SvcNotInstalled {
		t.Fatal("not removed")
	}
}

// ---- apply persists the new settings (W3-01) ----

// submitFile writes cfg to a file in its own folder, standing for the
// short-lived settings file the Unravis helper passes to "apply --config".
func submitFile(t *testing.T, cfg blackbox.Config) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "blackbox.json")
	b, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// installWithTargets installs through the CLI with target A selected.
func installWithTargets(t *testing.T, r *rig) (installed string, cfgA blackbox.Config) {
	t.Helper()
	cfgA = blackbox.Default()
	cfgA.Name = "box-1"
	cfgA.Folder = filepath.Join(r.dir, "rec")
	cfgA.Targets = []blackbox.Target{{Kind: "appPool", Name: "PoolA"}}
	if c, out, errs := r.do("install", "--json", "--config", submitFile(t, cfgA)); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	return filepath.Join(r.env.prog, defaultConfigName), cfgA
}

func installedTargets(t *testing.T, path string) []blackbox.Target {
	t.Helper()
	cfg, err := blackbox.LoadConfig(path)
	if err != nil {
		t.Fatalf("installed config %s: %v", path, err)
	}
	return cfg.Targets
}

// W3-01 through the CLI: install A, apply a DIFFERENT temp file selecting B.
// The installed config must hold B and not A, the service command line must
// still name the installed copy (never the temp file), and the next run must
// load B.
func TestApplyFromATemporaryFileSavesTheInstalledCopy(t *testing.T) {
	r := newRig(t)
	installed, cfgA := installWithTargets(t, r)
	cfgB := cfgA
	cfgB.Targets = []blackbox.Target{{Kind: "service", Name: "SvcB"}}
	fileB := submitFile(t, cfgB) // a different folder than the install source
	c, out, errs := r.do("apply", "--json", "--config", fileB)
	if c != 0 || !strings.Contains(out, `"restarted":true`) {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	got := installedTargets(t, installed)
	if len(got) != 1 || got[0].Kind != "service" || got[0].Name != "SvcB" {
		t.Fatalf("installed targets: %+v", got)
	}
	raw, _ := os.ReadFile(installed)
	if strings.Contains(string(raw), "PoolA") {
		t.Fatalf("target A still in the installed config: %s", raw)
	}
	// The service reads the installed copy, not the temporary file.
	args := strings.Join(r.svc.spec.Args, " ")
	if !strings.Contains(args, installed) || strings.Contains(args, fileB) {
		t.Fatalf("service args: %s", args)
	}
	// The next run (the same loader the service uses) sees B.
	var seen blackbox.HostOptions
	r.d.hostRun = func(_ context.Context, o blackbox.HostOptions) error { seen = o; return nil }
	if c, _, _ := r.do("run"); c != 0 {
		t.Fatal("run")
	}
	if len(seen.Config.Targets) != 1 || seen.Config.Targets[0].Name != "SvcB" {
		t.Fatalf("next run targets: %+v", seen.Config.Targets)
	}
}

// W3-01: a stopped recorder still gets the new settings saved; the answer says
// restarted=false and the next start uses them.
func TestApplyWhileStoppedSavesForTheNextStart(t *testing.T) {
	r := newRig(t)
	installed, cfgA := installWithTargets(t, r)
	if c, _, _ := r.do("stop"); c != 0 {
		t.Fatal("stop")
	}
	cfgB := cfgA
	cfgB.Targets = []blackbox.Target{{Kind: "service", Name: "SvcB"}}
	c, out, _ := r.do("apply", "--json", "--config", submitFile(t, cfgB))
	if c != 0 || !strings.Contains(out, `"restarted":false`) {
		t.Fatalf("%d %s", c, out)
	}
	if r.svc.state != blackbox.SvcStopped {
		t.Fatalf("a stopped recorder was started: %s", r.svc.state)
	}
	got := installedTargets(t, installed)
	if len(got) != 1 || got[0].Name != "SvcB" {
		t.Fatalf("installed targets: %+v", got)
	}
	if c, _, _ := r.do("start"); c != 0 {
		t.Fatal("start")
	}
	var seen blackbox.HostOptions
	r.d.hostRun = func(_ context.Context, o blackbox.HostOptions) error { seen = o; return nil }
	if c, _, _ := r.do("run"); c != 0 {
		t.Fatal("run")
	}
	if len(seen.Config.Targets) != 1 || seen.Config.Targets[0].Name != "SvcB" {
		t.Fatalf("next run targets: %+v", seen.Config.Targets)
	}
}

// W3-01: a save failure is the fixed save-failed reason, nothing is stopped
// and the old settings stay. The message carries no path.
func TestApplySaveFailureIsAFixedReasonAndStopsNothing(t *testing.T) {
	r := newRig(t)
	installed, cfgA := installWithTargets(t, r)
	// A folder sits where the backup file goes, so the save cannot finish.
	if err := os.MkdirAll(installed+".bak", 0o755); err != nil {
		t.Fatal(err)
	}
	cfgB := cfgA
	cfgB.Targets = []blackbox.Target{{Kind: "service", Name: "SvcB"}}
	c, out, _ := r.do("apply", "--json", "--config", submitFile(t, cfgB))
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if c != exitFail || got["reason"] != "save-failed" {
		t.Fatalf("%d %v", c, got)
	}
	if r.svc.state != blackbox.SvcRunning {
		t.Fatalf("the recorder was stopped although the save failed: %s", r.svc.state)
	}
	if tg := installedTargets(t, installed); len(tg) != 1 || tg[0].Name != "PoolA" {
		t.Fatalf("old settings not kept: %+v", tg)
	}
	if msg, _ := got["message"].(string); strings.Contains(msg, r.dir) {
		t.Fatalf("the answer carries a path: %q", msg)
	}
}

// W3-01: a restart failure after the save is the distinct restart-failed
// reason and the previous settings are put back.
func TestApplyRestartFailurePutsThePreviousSettingsBack(t *testing.T) {
	r := newRig(t)
	installed, cfgA := installWithTargets(t, r)
	r.svc.startErr = errors.New("boom")
	cfgB := cfgA
	cfgB.Targets = []blackbox.Target{{Kind: "service", Name: "SvcB"}}
	c, out, _ := r.do("apply", "--json", "--config", submitFile(t, cfgB))
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if c != exitFail || got["reason"] != "restart-failed" {
		t.Fatalf("%d %v", c, got)
	}
	if tg := installedTargets(t, installed); len(tg) != 1 || tg[0].Name != "PoolA" {
		t.Fatalf("previous settings not restored: %+v", tg)
	}
	if msg, _ := got["message"].(string); strings.Contains(msg, r.dir) {
		t.Fatalf("the answer carries a path: %q", msg)
	}
}

func TestRemoveDeleteNeedsTheNameTyped(t *testing.T) {
	r := newRig(t)
	_, _, _ = r.do(append([]string{"install", "--name", "box-1"}, r.args...)...)
	rec := filepath.Join(r.dir, "rec", blackbox.FileNameFor(time.Now()))
	if err := os.WriteFile(rec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do("remove", "--json", "--delete-recordings")
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if c != exitFail || got["reason"] != "confirm-name" {
		t.Fatalf("%d %v", c, got)
	}
	if r.svc.state == blackbox.SvcNotInstalled {
		t.Fatal("removed without confirmation")
	}
	if c, _, _ := r.do("remove", "--delete-recordings", "--confirm-name", "box-1"); c != 0 {
		t.Fatalf("%d", c)
	}
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("recording kept")
	}
}

func TestSnapshotCommand(t *testing.T) {
	r := newRig(t)
	_, _, _ = r.do(append([]string{"install", "--name", "box-1"}, r.args...)...)
	if err := os.WriteFile(filepath.Join(r.dir, "rec", blackbox.FileNameFor(time.Now())), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, out, errs := r.do("snapshot")
	if c != 0 || !strings.Contains(out, "box-1-2026-10-27-1105") {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	// No admin needed for a snapshot.
	r.env.admin = false
	if c, _, _ := r.do("snapshot"); c != 0 {
		t.Fatal("snapshot needs admin")
	}
}

func TestConfigShowListsEveryCollector(t *testing.T) {
	r := newRig(t)
	// Written directly, so the test does not depend on the Windows install path.
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	cfg.LogPaths = []string{r.dir}
	off := false
	cfg.Collectors = map[string]blackbox.CollectorSetting{"host.tcp": {On: &off}, "web.iis": {EverySec: 60}}
	path := filepath.Join(r.dir, "bb.json")
	if err := blackbox.SaveConfig(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path) //nolint:gosec // test file
	// Read-only: no admin needed, and nothing is written.
	r.env.admin = false
	c, out, errs := r.do("config", "show", "--json", "--config", path)
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	var got struct {
		OK         bool `json:"ok"`
		Collectors []struct {
			ID       string `json:"id"`
			On       bool   `json:"on"`
			EverySec int    `json:"everySec"`
		} `json:"collectors"`
		Config struct {
			Name string `json:"name"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || !got.OK || got.Config.Name != "box-1" {
		t.Fatalf("%v %s", err, out)
	}
	if len(got.Collectors) != len(blackbox.Catalogue()) {
		t.Fatalf("collectors %d, catalogue %d", len(got.Collectors), len(blackbox.Catalogue()))
	}
	for _, col := range got.Collectors {
		wantOn, wantEvery := col.ID != "host.tcp", blackbox.DefaultIntervalSecond
		if col.ID == "web.iis" {
			wantEvery = 60
		}
		if col.On != wantOn || col.EverySec != wantEvery {
			t.Errorf("%s: %+v, want on=%v every %d s", col.ID, col, wantOn, wantEvery)
		}
	}
	// The plain text shows ids too, and a bare "config" or any other word is a usage error.
	if c, out, _ := r.do("config", "show", "--config", path); c != 0 || !strings.Contains(out, "host.tcp") || !strings.Contains(out, "on (always)") || !strings.Contains(out, "off") {
		t.Fatalf("%d %s", c, out)
	}
	if c, _, _ := r.do("config"); c != exitUsage {
		t.Fatalf("config alone: %d", c)
	}
	if c, _, _ := r.do("config", "set", "host.tcp", "off"); c != exitUsage {
		t.Fatalf("config set is not built yet and must be refused: %d", c)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) { //nolint:gosec // test file
		t.Fatal("config show changed the settings file")
	}
	if c, out, _ := r.do("config", "show", "--json", "--config", filepath.Join(r.dir, "missing.json")); c != exitFail || !strings.Contains(out, `"ok":false`) {
		t.Fatalf("missing file: %d %s", c, out)
	}
}

func TestRunPassesSettingsAndReportsRefusal(t *testing.T) {
	r := newRig(t)
	_, _, _ = r.do(append([]string{"install", "--name", "box-1"}, r.args...)...)
	var seen blackbox.HostOptions
	r.d.hostRun = func(_ context.Context, o blackbox.HostOptions) error { seen = o; return nil }
	if c, _, _ := r.do("run"); c != 0 || seen.Config.Name != "box-1" || seen.KeyPath != filepath.Join(r.env.prog, defaultKeyName) {
		t.Fatalf("%+v", seen)
	}
	r.d.hostRun = func(context.Context, blackbox.HostOptions) error { return blackbox.ErrNoReadKey }
	if c, _, errs := r.do("run"); c != exitFail || !strings.Contains(errs, "read key") {
		t.Fatalf("%d %s", c, errs)
	}
	if c, _, _ := r.do("run", "--config", filepath.Join(r.dir, "missing.json")); c != exitFail {
		t.Fatal("run without settings must fail")
	}
}

func TestPackTemplateIsTheDefaultsWithNoName(t *testing.T) {
	got, err := blackbox.LoadConfigDraft(filepath.Join("pack", "blackbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := blackbox.Default()
	want.Name = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pack\\blackbox.json drifted from the defaults:\n got %+v\nwant %+v", got, want)
	}
	// The template is not a valid running config until install names it.
	if _, err := blackbox.LoadConfig(filepath.Join("pack", "blackbox.json")); err == nil {
		t.Fatal("a nameless template must not run")
	}
}

func TestInstallFromThePackTemplateNamesAfterTheComputerAndUsesCWhenThereIsNoD(t *testing.T) {
	r := newRig(t)
	tpl, _ := os.ReadFile(filepath.Join("pack", "blackbox.json"))
	if err := os.WriteFile(filepath.Join(r.dir, defaultConfigName), tpl, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := installConfig(filepath.Join(r.dir, defaultConfigName), nil, r.d, "", "", 0, "")
	if err != nil || cfg.Name != "WEB-B-corp" || cfg.Folder != blackbox.FallbackFolder {
		t.Fatalf("%+v %v", cfg, err)
	}
	r.d.driveOK = func(root string) bool { return root == `D:\` }
	cfg, _ = installConfig(filepath.Join(r.dir, defaultConfigName), nil, r.d, "", "", 0, "")
	if cfg.Folder != blackbox.DefaultFolder {
		t.Fatalf("%s", cfg.Folder)
	}
	// A hand-edited file that is broken stops the install with a clear message.
	_ = os.WriteFile(filepath.Join(r.dir, defaultConfigName), []byte(`{"keepDays": `), 0o644)
	if _, err := installConfig(filepath.Join(r.dir, defaultConfigName), nil, r.d, "", "", 0, ""); err == nil {
		t.Fatal("accepted a broken file")
	}
}

func TestPackReadmeNamesEveryCommand(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("pack", "README.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"install", "start", "stop", "apply", "snapshot", "remove", "--delete-recordings", "--confirm-name", "run", "version", "X-Blackbox-Key", "7788"} {
		if !strings.Contains(string(b), c) {
			t.Errorf("README.txt does not mention %q", c)
		}
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"WEB_B.corp": "WEB-B-corp", "": "blackbox", "***": "blackbox", "-x-": "x", strings.Repeat("a", 80): strings.Repeat("a", 63)} {
		if got := safeName(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestPackTemplateBindsLoopbackOnly(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("pack", "blackbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"bind": "127.0.0.1"`) || strings.Contains(string(b), "0.0.0.0") {
		t.Fatalf("pack blackbox.json must bind loopback (A53):\n%s", b)
	}
}

// After install the live settings are in the program folder; commands run from
// the unpacked zip must use them, not a stale copy next to this program.
func TestCommandsAfterInstallUseTheInstalledSettingsNotTheUnpackedCopy(t *testing.T) {
	r := newRig(t)
	if c, out, _ := r.do(append([]string{"install", "--json", "--name", "web-b"}, r.args...)...); c != 0 {
		t.Fatal(out)
	}
	// The unpacked zip still has a broken blackbox.json; the installed one is fine.
	if err := os.WriteFile(filepath.Join(r.dir, defaultConfigName), []byte(`{"keepDays": `), 0o644); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do("apply", "--json")
	if c != 0 || !strings.Contains(out, `"restarted":true`) {
		t.Fatalf("apply read the wrong file: %d %s", c, out)
	}
	// The install answer says where the service runs from.
	r2 := newRig(t)
	_, out2, _ := r2.do(append([]string{"install", "--json"}, r2.args...)...)
	if !strings.Contains(out2, `"exePath":`) || !strings.Contains(out2, "Unravis Blackbox") {
		t.Fatalf("%s", out2)
	}
}

// Review F5: install --bind <specific address> must ask that address, not
// loopback, whether the service is up.
func TestInstallWithABindAddressProbesThatAddress(t *testing.T) {
	r := newRig(t)
	r.env.listensOn = "10.0.0.5"
	c, out, errOut := r.do(append([]string{"install", "--bind", "10.0.0.5"}, r.args...)...)
	if c != exitOK {
		t.Fatalf("exit %d\n%s\n%s", c, out, errOut)
	}
	if len(r.env.probed) == 0 || r.env.probed[0] != "10.0.0.5" {
		t.Fatalf("probed %v", r.env.probed)
	}
	if r.svc.state != blackbox.SvcRunning {
		t.Fatal("service not left running")
	}
}

func TestUpdateCommandReplacesTheProgramAndKeepsTheSettings(t *testing.T) {
	r := newRig(t)
	if c, _, errs := r.do("update", "--json"); c != exitFail {
		t.Fatalf("%d %s", c, errs)
	}
	if c, _, _ := r.do(append([]string{"install"}, r.args...)...); c != 0 {
		t.Fatal("install")
	}
	if c, out, _ := r.do("update", "--json"); c != 0 || !strings.Contains(out, `"updated":false`) {
		t.Fatalf("already current: %d %s", c, out)
	}
	if err := os.WriteFile(r.env.exe, []byte("MZ newer"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do("update", "--json")
	if c != 0 || !strings.Contains(out, `"updated":true`) || !strings.Contains(out, `"restarted":true`) || !strings.Contains(out, `"version"`) {
		t.Fatalf("%d %s", c, out)
	}
	got, _ := os.ReadFile(filepath.Join(r.env.prog, blackbox.InstalledExeName))
	if string(got) != "MZ newer" || r.svc.state != blackbox.SvcRunning {
		t.Fatalf("%q %s", got, r.svc.state)
	}
}

func TestUpdateWhenNotInstalledIsAFixedReason(t *testing.T) {
	r := newRig(t)
	c, out, _ := r.do("update", "--json")
	if c != exitFail || !strings.Contains(out, `"reason":"not-installed"`) {
		t.Fatalf("%d %s", c, out)
	}
}
