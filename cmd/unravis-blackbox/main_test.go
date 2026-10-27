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
	state blackbox.SvcState
	calls []string
}

func (s *memSvc) State(string) (blackbox.SvcState, error) { return s.state, nil }
func (s *memSvc) Create(blackbox.ServiceSpec) error {
	s.calls = append(s.calls, "create")
	s.state = blackbox.SvcStopped
	return nil
}
func (s *memSvc) Start(string) error {
	s.calls = append(s.calls, "start")
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
}

func (e *memEnv) IsAdmin() bool                       { return e.admin }
func (e *memEnv) ExePath() (string, error)            { return e.exe, nil }
func (e *memEnv) PortFree(string, int) error          { return e.port }
func (e *memEnv) FolderWritable(string) error         { return nil }
func (e *memEnv) FileSHA256(p string) (string, error) { return blackbox.OSEnv{}.FileSHA256(p) }
func (e *memEnv) ProbeStatus(int, string) error       { return nil }
func (e *memEnv) Sleep(time.Duration)                 {}

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
	r := &rig{svc: &memSvc{}, env: &memEnv{admin: true, exe: exe}, dir: dir}
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
	cfg, err := blackbox.LoadConfig(filepath.Join(r.dir, defaultConfigName))
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

func TestRunPassesSettingsAndReportsRefusal(t *testing.T) {
	r := newRig(t)
	_, _, _ = r.do(append([]string{"install", "--name", "box-1"}, r.args...)...)
	var seen blackbox.HostOptions
	r.d.hostRun = func(_ context.Context, o blackbox.HostOptions) error { seen = o; return nil }
	if c, _, _ := r.do("run"); c != 0 || seen.Config.Name != "box-1" || seen.KeyPath != filepath.Join(r.dir, defaultKeyName) {
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
	cfg, err := installConfig(filepath.Join(r.dir, defaultConfigName), r.d, "", "", 0, "")
	if err != nil || cfg.Name != "WEB-B-corp" || cfg.Folder != blackbox.FallbackFolder {
		t.Fatalf("%+v %v", cfg, err)
	}
	r.d.driveOK = func(root string) bool { return root == `D:\` }
	cfg, _ = installConfig(filepath.Join(r.dir, defaultConfigName), r.d, "", "", 0, "")
	if cfg.Folder != blackbox.DefaultFolder {
		t.Fatalf("%s", cfg.Folder)
	}
	// A hand-edited file that is broken stops the install with a clear message.
	_ = os.WriteFile(filepath.Join(r.dir, defaultConfigName), []byte(`{"keepDays": `), 0o644)
	if _, err := installConfig(filepath.Join(r.dir, defaultConfigName), r.d, "", "", 0, ""); err == nil {
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
