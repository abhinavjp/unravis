package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/setup"
)

// syncBuf is a bytes.Buffer safe for the window goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type testRig struct {
	t      *testing.T
	root   string
	dirs   setup.Dirs
	sys    *setup.System
	f      *setup.Fakes
	out    *syncBuf
	errOut *syncBuf
	env    env
	// elevated counts the times the fake "run as administrator" was started.
	elevated int
	uacNo    bool
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	root := t.TempDir()
	prof := filepath.Join(root, "Users", "Zoë Ana") // a space and a non-ASCII letter in the profile path
	d := setup.Dirs{
		Windows: filepath.Join(root, "Windows"), ProgramFiles: filepath.Join(root, "Program Files"), ProgramData: filepath.Join(root, "ProgramData"),
		CommonPrograms: filepath.Join(root, "ProgramData", "Microsoft", "Windows", "Start Menu", "Programs"), Temp: filepath.Join(prof, "AppData", "Local", "Temp"),
		UserProfile: prof, LocalAppData: filepath.Join(prof, "AppData", "Local"), AppData: filepath.Join(prof, "AppData", "Roaming"),
		Programs: filepath.Join(prof, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs"),
		Startup:  filepath.Join(prof, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup"),
	}
	for _, p := range []string{filepath.Join(root, "D"), d.Windows, d.ProgramFiles, d.ProgramData, d.CommonPrograms, d.Temp, d.LocalAppData, d.AppData, d.Programs, d.Startup} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(prof, "Downloads", setup.SetupExeName)
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("setup program"), 0o644); err != nil {
		t.Fatal(err)
	}
	sys, f := setup.NewFakeSystem("1.0.0")
	rg := &testRig{t: t, root: root, dirs: d, sys: sys, f: f, out: &syncBuf{}, errOut: &syncBuf{}}
	payload := testPayload(t, "1.0.0")
	rg.env = env{
		stdout: rg.out, stderr: rg.errOut, now: func() time.Time { return time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC) },
		system:   func() (*setup.System, error) { return sys, nil },
		dirs:     func() (setup.Dirs, error) { return d, nil },
		userSID:  func() (string, error) { return "S-1-5-21-1000", nil },
		setupExe: func() (string, error) { return exe, nil },
		elevator: func(string) setup.Elevator { return rg },
		payload:  func() (*setup.Payload, error) { return payload, nil },
		static: func() (fs.FS, error) {
			return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Unravis Setup</title>")}}, nil
		},
		openWindow: func(string, string) error { return errors.New("no browser in tests") },
		openFolder: func(string) error { return nil },
		startApp:   func(string) error { return nil },
		dialWorker: func(string, uint32) (*setup.WorkerPipe, error) {
			t.Error("the worker dialled the window although it should have stopped first")
			return nil, errors.New("not in tests")
		},
		listen: setup.Listen,
	}
	return rg
}

// Start is the fake "run as administrator": an administrator runner on the same fake PC serves the request on a net.Pipe.
func (rg *testRig) Start(ctx context.Context) (io.ReadWriteCloser, error) {
	rg.elevated++
	if rg.uacNo {
		return nil, setup.ErrUACCancelled
	}
	a, b := net.Pipe()
	wr := &setup.Runner{Sys: rg.sys, Dirs: rg.dirs, Payload: testPayload(rg.t, "1.0.0")}
	go func() {
		defer b.Close()
		setup.ServeWorker(context.Background(), b, wr, nil)
	}()
	return a, nil
}

func testPayload(t *testing.T, version string) *setup.Payload {
	t.Helper()
	m := fstest.MapFS{}
	var man setup.Manifest
	man.Version = version
	for _, n := range []string{setup.AppExeName, setup.KeeperExeName, setup.BlackboxExeName} {
		data := []byte(n + " " + version)
		sum := sha256.Sum256(data)
		m[n] = &fstest.MapFile{Data: data, Mode: 0o755}
		man.Files = append(man.Files, setup.PayloadFile{Name: n, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	}
	raw, _ := json.Marshal(man)
	m[setup.ManifestName] = &fstest.MapFile{Data: raw}
	p, err := setup.LoadPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (rg *testRig) config(body string) string {
	rg.t.Helper()
	p := filepath.Join(rg.root, "conf ïg", "setup.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		rg.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		rg.t.Fatal(err)
	}
	return p
}

func TestFlagErrorsExitTwoAndChangeNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--uninstall", "--repair"},
		{"--config", "x.json"},
		{"--silent"},
		{"--worker"},
		{"--worker", "--pipe", `\\.\pipe\x`},
		{"--pipe", `\\.\pipe\x`, "--parent", "4"},
		{"--capture"}, // only in the screenshot build
		{"install"},
		{"--no-such-flag"},
	} {
		rg := newRig(t)
		before := rg.f.Snapshot()
		if got := run(args, rg.env); got != exitInvalid {
			t.Errorf("%q: exit %d, want %d (stderr %q)", args, got, exitInvalid, rg.errOut)
		}
		if rg.f.Snapshot() != before {
			t.Errorf("%q changed the PC", args)
		}
	}
}

func TestVersionAndHelp(t *testing.T) {
	rg := newRig(t)
	if got := run([]string{"--version"}, rg.env); got != 0 || !strings.Contains(rg.out.String(), "Unravis Setup") {
		t.Fatalf("--version: exit %d, out %q", got, rg.out)
	}
	rg = newRig(t)
	if got := run([]string{"--help"}, rg.env); got != 0 {
		t.Fatalf("--help: exit %d", got)
	}
	if strings.Contains(rg.errOut.String(), "-worker") {
		t.Fatalf("the help shows the worker flags:\n%s", rg.errOut)
	}
}

func TestBrokenConfigSaysWhatAndChangesNothing(t *testing.T) {
	cases := map[string]string{
		"cut short":   `{"scope": "user"`,
		"unknown key": `{"scope":"user","blackBox":null}`,
		"no scope":    `{}`,
		"empty":       ``,
	}
	for name, body := range cases {
		rg := newRig(t)
		before := rg.f.Snapshot()
		got := run([]string{"--silent", "--config", rg.config(body)}, rg.env)
		if got != exitInvalid {
			t.Errorf("%s: exit %d, want %d", name, got, exitInvalid)
		}
		if !strings.Contains(rg.out.String(), "Setup cannot use the config") || !strings.Contains(rg.out.String(), "Nothing was changed.") {
			t.Errorf("%s: output %q", name, rg.out)
		}
		if rg.f.Snapshot() != before || exists(filepath.Join(rg.dirs.LocalAppData, "Programs", "Unravis")) {
			t.Errorf("%s: the PC changed", name)
		}
	}
	rg := newRig(t)
	if got := run([]string{"--silent", "--config", filepath.Join(rg.root, "missing.json")}, rg.env); got != exitInvalid {
		t.Errorf("missing config: exit %d", got)
	}
}

func TestSilentUserInstallThenSecondRunRepairs(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	cfg := rg.config(`{"scope":"user","startAtSignIn":false}`)
	if got := run([]string{"--silent", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("exit %d\n%s", got, rg.out)
	}
	if rg.elevated != 0 {
		t.Fatal("a just-for-me install asked for administrator approval")
	}
	exe := filepath.Join(setup.DefaultLocation(setup.ScopeUser, rg.dirs), setup.AppExeName)
	if !exists(exe) {
		t.Fatalf("%s was not installed\n%s", exe, rg.out)
	}
	if !strings.Contains(rg.out.String(), "Log: ") {
		t.Fatalf("no log path in %q", rg.out)
	}
	logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	// Double run: the same command again repairs instead of failing or installing twice.
	rg.out.b.Reset()
	if got := run([]string{"--silent", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("second run: exit %d\n%s", got, rg.out)
	}
}

func TestSilentMachineInstallAsksWindowsAndCancelAtUACChangesNothing(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	rg.uacNo = true
	cfg := rg.config(`{"scope":"machine"}`)
	before := rg.f.Snapshot()
	got := run([]string{"--silent", "--config", cfg}, rg.env)
	if got != setup.OutcomeCancelledUAC.ExitCode() || rg.elevated != 1 {
		t.Fatalf("exit %d, elevated %d\n%s", got, rg.elevated, rg.out)
	}
	if rg.f.Snapshot() != before || exists(filepath.Join(rg.dirs.ProgramFiles, "Unravis")) {
		t.Fatal("No at the Windows prompt changed the PC")
	}

	rg.uacNo = false
	rg.out.b.Reset()
	if got := run([]string{"--silent", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("approved: exit %d\n%s", got, rg.out)
	}
	if rg.elevated != 2 || !exists(filepath.Join(setup.DefaultLocation(setup.ScopeMachine, rg.dirs), setup.AppExeName)) {
		t.Fatalf("the machine install did not go through the worker\n%s", rg.out)
	}
}

func TestSilentNeverPrintsTheReadKey(t *testing.T) {
	rg := newRig(t)
	keyFile := filepath.Join(rg.root, "keys", "read key.txt")
	cfg := rg.config(`{"scope":"machine","blackbox":{"name":"web-01","folder":` + jsonString(filepath.Join(rg.root, "D", "UnravisBlackbox")) +
		`,"keepDays":14,"maxGB":20,"minFreePercent":10,"port":7788},"readKeyFile":` + jsonString(keyFile) + `}`)
	if got := run([]string{"--silent", "--config", cfg}, rg.env); got != 0 {
		t.Fatalf("exit %d\n%s", got, rg.out)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil || len(bytes.TrimSpace(key)) == 0 {
		t.Fatalf("read key file: %v %q", err, key)
	}
	k := string(bytes.TrimSpace(key))
	logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*.log"))
	var logText []byte
	for _, l := range logs {
		b, _ := os.ReadFile(l)
		logText = append(logText, b...)
	}
	if strings.Contains(rg.out.String(), k) || strings.Contains(rg.errOut.String(), k) || bytes.Contains(logText, []byte(k)) {
		t.Fatal("the read key reached the output or the log")
	}
}

func TestSilentUninstallWithoutConfigKeepsData(t *testing.T) {
	rg := newRig(t)
	if got := run([]string{"--silent", "--config", rg.config(`{"scope":"user"}`)}, rg.env); got != 0 {
		t.Fatalf("install: exit %d\n%s", got, rg.out)
	}
	cases := filepath.Join(rg.dirs.LocalAppData, "Unravis")
	marker := filepath.Join(cases, "cases", "case one.db")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("case"), 0o644); err != nil {
		t.Fatal(err)
	}
	rg.out.b.Reset()
	if got := run([]string{"--silent", "--uninstall"}, rg.env); got != 0 {
		t.Fatalf("uninstall: exit %d\n%s", got, rg.out)
	}
	if exists(filepath.Join(setup.DefaultLocation(setup.ScopeUser, rg.dirs), setup.AppExeName)) {
		t.Fatal("the program is still there")
	}
	if !exists(marker) {
		t.Fatal("a silent uninstall without a config deleted case data")
	}
}

// The silent uninstall's config, end to end through run(): null, false and an empty object keep the cases and saved connections;
// only an explicit true removes them (D-C-14; the owner ruled that null counts as keep).
func TestSilentUninstallConfigKeepsOrRemovesData(t *testing.T) {
	for _, c := range []struct {
		name, body string
		gone       bool
	}{
		{"whole object null", `{"uninstall":null}`, false},
		{"deleteCases null", `{"uninstall":{"deleteCases":null}}`, false},
		{"deleteCases false", `{"uninstall":{"deleteCases":false}}`, false},
		{"empty object", `{"uninstall":{}}`, false},
		{"deleteCases true", `{"uninstall":{"deleteCases":true}}`, true},
	} {
		rg := newRig(t)
		if got := run([]string{"--silent", "--config", rg.config(`{"scope":"user"}`)}, rg.env); got != 0 {
			t.Fatalf("%s: install: exit %d\n%s", c.name, got, rg.out)
		}
		// For a just-for-me install the person's data folders are the two APIAuditExplorer folders (layout.go UserData).
		marker := filepath.Join(rg.dirs.LocalAppData, "APIAuditExplorer", "cases", "case one.db")
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("case"), 0o644); err != nil {
			t.Fatal(err)
		}
		rg.out.b.Reset()
		if got := run([]string{"--silent", "--uninstall", "--config", rg.config(c.body)}, rg.env); got != 0 {
			t.Fatalf("%s: uninstall: exit %d\n%s", c.name, got, rg.out)
		}
		if exists(filepath.Join(setup.DefaultLocation(setup.ScopeUser, rg.dirs), setup.AppExeName)) {
			t.Errorf("%s: the program is still there", c.name)
		}
		if c.gone == exists(marker) {
			t.Errorf("%s: case data present = %v, want %v\n%s", c.name, exists(marker), !c.gone, rg.out)
		}
	}
}

// A silent uninstall config that cannot be used stops with exit 2, says which key, and changes nothing (INST-14 to INST-16, INST-20).
func TestSilentUninstallBadConfigExitsTwoAndChangesNothing(t *testing.T) {
	for _, c := range []struct {
		name, body string
		args       []string
		want       string
	}{
		{"quoted true", `{"uninstall":{"deleteCases":"true"}}`, []string{"--silent", "--uninstall"}, "uninstall.deleteCases must be true or false (without quotes)"},
		{"unknown key inside uninstall", `{"uninstall":{"deleteCase":true}}`, []string{"--silent", "--uninstall"}, `"deleteCase" inside "uninstall"`},
		{"uninstall object in an install", `{"scope":"user","uninstall":{"deleteCases":true}}`, []string{"--silent"}, "--uninstall"},
	} {
		rg := newRig(t)
		if got := run([]string{"--silent", "--config", rg.config(`{"scope":"user"}`)}, rg.env); got != 0 {
			t.Fatalf("%s: install: exit %d\n%s", c.name, got, rg.out)
		}
		marker := filepath.Join(rg.dirs.LocalAppData, "APIAuditExplorer", "cases", "case one.db")
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte("case"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := rg.f.Snapshot()
		rg.out.b.Reset()
		args := append(append([]string{}, c.args...), "--config", rg.config(c.body))
		if got := run(args, rg.env); got != exitInvalid {
			t.Errorf("%s: exit %d, want %d\n%s", c.name, got, exitInvalid, rg.out)
		}
		out := rg.out.String()
		if !strings.Contains(out, "Setup cannot use the config") || !strings.Contains(out, c.want) || !strings.Contains(out, "Nothing was changed.") {
			t.Errorf("%s: output %q", c.name, out)
		}
		if rg.f.Snapshot() != before || !exists(marker) || !exists(filepath.Join(setup.DefaultLocation(setup.ScopeUser, rg.dirs), setup.AppExeName)) {
			t.Errorf("%s: the PC changed", c.name)
		}
	}
}

func TestSilentWithoutProgramFilesStops(t *testing.T) {
	rg := newRig(t)
	rg.env.payload = func() (*setup.Payload, error) { return nil, errors.New("this setup program has no program files inside.") }
	before := rg.f.Snapshot()
	if got := run([]string{"--silent", "--config", rg.config(`{"scope":"user"}`)}, rg.env); got != setup.OutcomePreflightFailed.ExitCode() {
		t.Fatalf("exit %d", got)
	}
	if rg.f.Snapshot() != before {
		t.Fatal("the PC changed")
	}
}

func TestWorkerRefusesWithoutAdminOrWithoutItsWindow(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	if got := run([]string{"--worker", "--pipe", `\\.\pipe\unravis-setup-x`, "--parent", "4242"}, rg.env); got != exitInvalid {
		t.Fatalf("not admin: exit %d", got)
	}
	rg = newRig(t)
	dialled := false
	rg.env.dialWorker = func(name string, parent uint32) (*setup.WorkerPipe, error) {
		dialled = true
		if name != `\\.\pipe\unravis-setup-x` || parent != 4242 {
			t.Errorf("dialled %q %d", name, parent)
		}
		return nil, errors.New("the pipe is not served by the setup window that started this worker")
	}
	if got := run([]string{"--worker", "--pipe", `\\.\pipe\unravis-setup-x`, "--parent", "4242"}, rg.env); got != exitInvalid || !dialled {
		t.Fatalf("bad pipe: exit %d dialled %v", got, dialled)
	}
}

func TestWorkerServesOneRequestOverItsPipe(t *testing.T) {
	rg := newRig(t)
	a, b := net.Pipe()
	rg.env.dialWorker = func(string, uint32) (*setup.WorkerPipe, error) {
		return &setup.WorkerPipe{Conn: b, SID: "S-1-5-21-1000", Profile: rg.dirs.UserProfile}, nil
	}
	done := make(chan int, 1)
	go func() { done <- run([]string{"--worker", "--pipe", `\\.\pipe\unravis-setup-x`, "--parent", "4242"}, rg.env) }()
	u := rg.dirs.User()
	req := setup.WorkRequest{Op: setup.OpInstall, User: &u, Options: setup.Options{Scope: setup.ScopeMachine,
		Location: setup.DefaultLocation(setup.ScopeMachine, rg.dirs), StartAtSignIn: true}}
	res := setup.RunWorker(context.Background(), connElevator{a}, req, nil, nil)
	if res.Outcome != setup.OutcomeDone {
		t.Fatalf("outcome %s: %s %s", res.Outcome, res.Message, res.Fix)
	}
	if got := <-done; got != 0 {
		t.Fatalf("worker exit %d", got)
	}
	logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*-admin.log"))
	if len(logs) != 1 {
		t.Fatalf("worker logs %v", logs)
	}
}

type connElevator struct{ c net.Conn }

func (e connElevator) Start(context.Context) (io.ReadWriteCloser, error) { return e.c, nil }

var listening = regexp.MustCompile(`listening at (http://127\.0\.0\.1:\d+)/#k=([A-Za-z0-9_-]+)`)

func TestWindowServesThePageWithItsKeyAndEndsOnClose(t *testing.T) {
	rg := newRig(t)
	done := make(chan int, 1)
	go func() { done <- run([]string{"--no-browser"}, rg.env) }()
	var m []string
	for i := 0; i < 200 && m == nil; i++ {
		m = listening.FindStringSubmatch(rg.out.String())
		if m == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if m == nil {
		t.Fatalf("no address in %q", rg.out)
	}
	base, key := m[1], m[2]
	get := func(path, k string) int {
		req, _ := http.NewRequest("GET", base+path, nil)
		if k != "" {
			req.Header.Set("X-Setup-Key", k)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("/api/state", ""); got == http.StatusOK {
		t.Fatal("the state was served without the key")
	}
	if got := get("/api/state", key); got != http.StatusOK {
		t.Fatalf("state with key: %d", got)
	}
	req, _ := http.NewRequest("POST", base+"/api/close", strings.NewReader("{}"))
	req.Header.Set("X-Setup-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("exit %d", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window did not end after close")
	}
	// The key is never written to the log.
	logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*.log"))
	for _, l := range logs {
		f, _ := os.Open(l)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.Contains(sc.Text(), key) {
				t.Errorf("the page key is in the log: %s", sc.Text())
			}
		}
		f.Close()
	}
}

func TestWindowEndsWhenThePageGoesQuiet(t *testing.T) {
	rg := newRig(t)
	rg.env.idleAfter = time.Nanosecond
	done := make(chan int, 1)
	go func() { done <- run([]string{"--no-browser"}, rg.env) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the window did not end on its own")
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }
