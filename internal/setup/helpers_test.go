package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// testEnv is a fake PC under a temporary folder: Windows-shaped folders, a fake System and a payload.
type testEnv struct {
	t     *testing.T
	root  string
	dirs  Dirs
	sys   *System
	f     *Fakes
	r     *Runner
	logMu sync.Mutex
	log   []string
	evs   []StepEvent
}

func newEnv(t *testing.T, version string) *testEnv {
	t.Helper()
	root := t.TempDir()
	prof := filepath.Join(root, "Users", "Ana")
	d := Dirs{
		Windows: filepath.Join(root, "Windows"), ProgramFiles: filepath.Join(root, "Program Files"), ProgramData: filepath.Join(root, "ProgramData"),
		CommonPrograms: filepath.Join(root, "ProgramData", "Microsoft", "Windows", "Start Menu", "Programs"), Temp: filepath.Join(prof, "AppData", "Local", "Temp"),
		UserProfile: prof, LocalAppData: filepath.Join(prof, "AppData", "Local"), AppData: filepath.Join(prof, "AppData", "Roaming"),
		Programs: filepath.Join(prof, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs"),
		Startup:  filepath.Join(prof, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup"),
	}
	// root\D stands for a second drive's root (the Blackbox recordings go to root\D\UnravisBlackbox), which setup never makes.
	for _, p := range []string{filepath.Join(root, "D"), d.Windows, d.ProgramFiles, d.ProgramData, d.CommonPrograms, d.Temp, d.LocalAppData, d.AppData, d.Programs, d.Startup} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sys, f := NewFakeSystem(version)
	e := &testEnv{t: t, root: root, dirs: d, sys: sys, f: f}
	e.r = &Runner{Sys: sys, Dirs: d, Payload: testPayload(t, version, true), UserSID: "S-1-5-21-1000",
		SetupExe: filepath.Join(prof, "Downloads", SetupExeName)}
	writeFile(t, e.r.SetupExe, "setup program "+version)
	e.r.Log = func(s string) { e.logMu.Lock(); e.log = append(e.log, s); e.logMu.Unlock() }
	e.r.Events = func(ev StepEvent) { e.logMu.Lock(); e.evs = append(e.evs, ev); e.logMu.Unlock() }
	// The journal folders exist from the start (a real PC keeps them between runs), so snapshots compare the rest.
	for _, el := range []bool{true, false} {
		if _, err := e.r.ensureJournalDir(el); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// testPayload is a payload whose "programs" are small text files.
func testPayload(t *testing.T, version string, withBlackbox bool) *Payload {
	t.Helper()
	m := fstest.MapFS{}
	var man Manifest
	man.Version = version
	names := []string{AppExeName, KeeperExeName}
	if withBlackbox {
		names = append(names, BlackboxExeName)
	}
	for _, n := range names {
		data := []byte(n + " " + version + " " + strings.Repeat("x", 300<<10)) // over one copy chunk, for the cancel test
		sum := sha256.Sum256(data)
		m[n] = &fstest.MapFile{Data: data, Mode: 0o755}
		man.Files = append(man.Files, PayloadFile{Name: n, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	}
	raw, _ := json.Marshal(man)
	m[ManifestName] = &fstest.MapFile{Data: raw}
	p, err := LoadPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *testEnv) machineOpts() Options {
	return Options{Scope: ScopeMachine, Location: DefaultLocation(ScopeMachine, e.dirs), StartAtSignIn: true, OpenWhenDone: true}
}

func (e *testEnv) withBlackbox(o Options, firewall bool) Options {
	o.Blackbox = &BlackboxOptions{Name: "web-b", Folder: filepath.Join(e.root, "D", "UnravisBlackbox"), KeepDays: 14, MaxGB: 20, MinFreePercent: 10, Port: 7788}
	if firewall {
		o.Firewall = &FirewallOptions{RemoteAddresses: []string{"10.0.0.5", "10.0.1.0/24"}}
	}
	return o
}

func (e *testEnv) userOpts() Options {
	return Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, e.dirs), StartAtSignIn: true}
}

// run plans and executes one operation.
func (e *testEnv) run(op Op, o Options, un UninstallOptions) Result {
	e.t.Helper()
	old, err := Detect(e.sys.Reg)
	if err != nil {
		e.t.Fatal(err)
	}
	p, err := e.r.NewPlan(op, o, un, old)
	if err != nil {
		e.t.Fatalf("plan %s: %v", op, err)
	}
	return e.r.Execute(t0ctx(), p)
}

func (e *testEnv) mustRun(op Op, o Options, un UninstallOptions) Result {
	e.t.Helper()
	res := e.run(op, o, un)
	if res.Outcome != OutcomeDone {
		e.t.Fatalf("%s: outcome %s: %s (left %v)\nlog:\n%s", op, res.Outcome, res.Message, res.Left, strings.Join(e.log, "\n"))
	}
	return res
}

// snapshot is the whole fake PC: the fake machine, and every file and folder under the temporary root with its size and content hash.
func (e *testEnv) snapshot() string {
	e.t.Helper()
	var lines []string
	_ = filepath.WalkDir(e.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(e.root, p)
		if d.IsDir() {
			lines = append(lines, "D "+rel)
			return nil
		}
		data, _ := os.ReadFile(p)
		sum := sha256.Sum256(data)
		lines = append(lines, "F "+rel+" "+hex.EncodeToString(sum[:8]))
		return nil
	})
	sort.Strings(lines)
	return e.f.Snapshot() + "\nFILES\n" + strings.Join(lines, "\n")
}

func diffSnap(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	in := map[string]int{}
	for _, l := range al {
		in[l]++
	}
	for _, l := range bl {
		in[l]--
	}
	var out []string
	for l, n := range in {
		switch {
		case n > 0:
			out = append(out, "- "+l)
		case n < 0:
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }
