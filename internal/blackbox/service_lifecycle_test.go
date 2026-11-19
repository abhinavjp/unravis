package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// twoSvc answers for the host service and the SQL service separately (fakeSvc
// ignores the name).
type twoSvc struct{ host, sql *fakeSvc }

func (t *twoSvc) pick(name string) *fakeSvc {
	if name == SQLServiceName {
		return t.sql
	}
	return t.host
}
func (t *twoSvc) State(n string) (SvcState, error) { return t.pick(n).State(n) }
func (t *twoSvc) Create(s ServiceSpec) error       { return t.pick(s.Name).Create(s) }
func (t *twoSvc) Start(n string) error             { return t.pick(n).Start(n) }
func (t *twoSvc) Stop(n string) error              { return t.pick(n).Stop(n) }
func (t *twoSvc) Delete(n string) error            { return t.pick(n).Delete(n) }

func installedRig(t *testing.T) *svcRig {
	t.Helper()
	g := newSvcRig(t)
	cfgA := g.cfg
	cfgA.Targets = []Target{{Kind: "appPool", Name: "PoolA"}}
	g.cfg = cfgA
	if _, err := g.m.Install(cfgA); err != nil {
		t.Fatal(err)
	}
	return g
}

// E2E-02: a restart that fails once is retried with the previous settings, and
// the answer says the previous recorder is running again.
func TestApplyRestartFailureStartsThePreviousRecorderAgain(t *testing.T) {
	g := installedRig(t)
	starts := 0
	g.svc.onStart = func() {
		starts++
		if starts == 1 {
			g.svc.startErr = errors.New("boom")
		} else {
			g.svc.startErr = nil
		}
	}
	cfgB := g.cfg
	cfgB.Targets = []Target{{Kind: "service", Name: "SvcB"}}
	res, err := g.m.Apply(applySource(t, cfgB))
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Reason != ReasonRestartFailed {
		t.Fatalf("%v", err)
	}
	if res.Restarted || !ae.Running || g.svc.state != SvcRunning {
		t.Fatalf("running=%v restarted=%v state=%s: the old recorder was not started again", ae.Running, res.Restarted, g.svc.state)
	}
	if !strings.Contains(ae.Message, "running with them again") || strings.Contains(ae.Message, g.root) {
		t.Fatalf("message: %q", ae.Message)
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || strings.Join(targetNames(got), ",") != "appPool:PoolA" {
		t.Fatalf("previous settings not restored: %+v %v", got.Targets, err)
	}
}

// E2E-02: when the previous recorder cannot be started again either, the answer
// says it is stopped and what to press, never "nothing was changed".
func TestApplyRestartFailureThatCannotRecoverSaysStopped(t *testing.T) {
	g := installedRig(t)
	g.svc.startErr = errors.New("boom")
	cfgB := g.cfg
	cfgB.Targets = []Target{{Kind: "service", Name: "SvcB"}}
	_, err := g.m.Apply(applySource(t, cfgB))
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Reason != ReasonRestartFailed {
		t.Fatalf("%v", err)
	}
	if ae.Running || !strings.Contains(ae.Message, "recorder is stopped") || !strings.Contains(ae.Message, "Press Start") {
		t.Fatalf("running=%v message: %q", ae.Running, ae.Message)
	}
}

// E2E-03: a new port that is busy is refused before the recorder is stopped.
func TestApplyRefusesABusyNewPortBeforeStopping(t *testing.T) {
	g := installedRig(t)
	g.env.portErr = errors.New("in use")
	cfgB := g.cfg
	cfgB.Port = 7799
	stops := g.svc.did("stop")
	_, err := g.m.Apply(applySource(t, cfgB))
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Reason != ReasonPortBusy || !strings.Contains(ae.Message, "Port 7799 is busy") || !strings.Contains(ae.Message, "Choose another port") {
		t.Fatalf("%v", err)
	}
	if g.svc.did("stop") != stops || g.svc.state != SvcRunning {
		t.Fatalf("the recorder was stopped: %v", g.svc.calls)
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || got.Port != g.cfg.Port {
		t.Fatalf("settings changed: %+v %v", got, err)
	}
}

// E2E-03: the port the recorder already listens on is not checked (the check
// would meet the recorder's own listener).
func TestApplySamePortSkipsThePortCheck(t *testing.T) {
	g := installedRig(t)
	g.env.portErr = errors.New("in use by the recorder itself")
	cfgB := g.cfg
	cfgB.Targets = []Target{{Kind: "service", Name: "SvcB"}}
	res, err := g.m.Apply(applySource(t, cfgB))
	if err != nil || !res.Restarted {
		t.Fatalf("%+v %v", res, err)
	}
}

// E2E-03: a new recordings folder is made, checked and locked like Install does.
func TestApplyNewFolderIsMadeCheckedAndLocked(t *testing.T) {
	g := installedRig(t)
	cfgB := g.cfg
	cfgB.Folder = filepath.Join(g.root, "elsewhere", "Recordings")
	before := len(g.env.secured)
	res, err := g.m.Apply(applySource(t, cfgB))
	if err != nil || !res.Restarted {
		t.Fatalf("%+v %v", res, err)
	}
	if st, serr := os.Stat(cfgB.Folder); serr != nil || !st.IsDir() {
		t.Fatal("the new folder was not made")
	}
	if len(g.env.secured) != before+1 || g.env.secured[before] != cfgB.Folder {
		t.Fatalf("the new folder was not locked: %v", g.env.secured[before:])
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || got.Folder != cfgB.Folder {
		t.Fatalf("%+v %v", got, err)
	}
}

// E2E-03: a read-only or full new folder is refused, nothing is stopped and the
// folder that was made for the check is taken away again.
func TestApplyNewFolderRefusedChangesNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(g *svcRig)
	}{
		{"read-only", func(g *svcRig) { g.env.folder = errors.New("access denied") }},
		{"full", func(g *svcRig) { g.m.Disk = fixedDisk{free: 100 << 20} }},
		{"cannot lock", func(g *svcRig) { g.env.secureErr = errors.New("acl") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := installedRig(t)
			c.setup(g)
			cfgB := g.cfg
			cfgB.Folder = filepath.Join(g.root, "elsewhere", "Recordings")
			stops := g.svc.did("stop")
			_, err := g.m.Apply(applySource(t, cfgB))
			var ae *ApplyError
			if !errors.As(err, &ae) || ae.Reason != ReasonFolder || !strings.Contains(ae.Message, "Choose another folder") {
				t.Fatalf("%v", err)
			}
			if g.svc.did("stop") != stops || g.svc.state != SvcRunning {
				t.Fatalf("the recorder was stopped: %v", g.svc.calls)
			}
			if _, serr := os.Stat(cfgB.Folder); serr == nil {
				t.Fatal("the refused folder was left behind")
			}
			got, lerr := LoadConfig(g.p.ConfigPath)
			if lerr != nil || got.Folder != g.cfg.Folder {
				t.Fatalf("settings changed: %+v %v", got, lerr)
			}
		})
	}
}

// E2E-09: a recording that cannot be deleted leaves the service installed and
// running again, and the answer says so. (A file held open blocks a delete on
// Windows only.)
func TestRemoveRecordingFailureKeepsTheServiceAndStartsItAgain(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("an open file blocks a delete on Windows only")
	}
	g := installedRig(t)
	rec := filepath.Join(g.cfg.Folder, FileNameFor(t0))
	if err := os.WriteFile(rec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(rec)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	res, err := g.m.Remove(g.cfg, g.p.KeyPath, true, g.cfg.Name)
	var re *RemoveError
	if !errors.As(err, &re) || re.Reason != string(ReasonRemoveFailed) {
		t.Fatalf("%v", err)
	}
	if res.ServiceRemoved || res.RecordingsDeleted {
		t.Fatalf("a failed remove claims success: %+v", res)
	}
	if g.svc.state != SvcRunning || g.svc.did("delete") != 0 {
		t.Fatalf("the service was not kept: state=%s calls=%v", g.svc.state, g.svc.calls)
	}
	if !strings.Contains(re.Message, "still installed and is running again") || strings.Contains(re.Message, g.root) {
		t.Fatalf("message: %q", re.Message)
	}
	for _, p := range []string{g.p.KeyPath, g.p.ConfigPath} {
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("a file of the still-installed Blackbox was removed: %s", p)
		}
	}
}

// E2E-10: Remove deletes the settings files with the service, and the SQL
// service with it (its data only when the recordings go too).
func TestRemoveDeletesSettingsFilesAndTheSQLService(t *testing.T) {
	g := installedRig(t)
	two := &twoSvc{host: g.svc, sql: &fakeSvc{state: SvcRunning}}
	g.m.Svc = two
	g.m.SQL = &SQLManager{Svc: two, Env: &fakeSQLEnv{fakeEnv: g.env}}
	sqlDir := SQLDataDir(g.cfg.Folder)
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "x.ndjson"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.p.ConfigPath+".bak", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := g.m.Remove(g.cfg, g.p.KeyPath, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.ServiceRemoved || !res.SQLServiceRemoved || two.sql.state != SvcNotInstalled || g.svc.state != SvcNotInstalled {
		t.Fatalf("%+v host=%s sql=%s", res, g.svc.state, two.sql.state)
	}
	for _, p := range []string{g.p.KeyPath, g.p.ConfigPath, g.p.ConfigPath + ".bak"} {
		if _, serr := os.Stat(p); serr == nil {
			t.Fatalf("left behind: %s", p)
		}
	}
	if _, serr := os.Stat(sqlDir); serr != nil {
		t.Fatal("the SQL data was deleted although the recordings were kept")
	}
}

// E2E-10: with the recordings deleted, the SQL data folder goes too and the
// recordings folder is removed.
func TestRemoveWithRecordingsAlsoDeletesTheSQLData(t *testing.T) {
	g := installedRig(t)
	two := &twoSvc{host: g.svc, sql: &fakeSvc{state: SvcStopped}}
	g.m.Svc = two
	g.m.SQL = &SQLManager{Svc: two, Env: &fakeSQLEnv{fakeEnv: g.env}}
	sqlDir := SQLDataDir(g.cfg.Folder)
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.cfg.Folder, ManifestName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := g.m.Remove(g.cfg, g.p.KeyPath, true, g.cfg.Name)
	if err != nil || !res.RecordingsDeleted || !res.SQLServiceRemoved {
		t.Fatalf("%+v %v", res, err)
	}
	if _, serr := os.Stat(sqlDir); serr == nil {
		t.Fatal("the SQL data was left behind")
	}
}

// E2E-19: removing what is not installed says what to do instead.
func TestRemoveNotInstalledSaysTakeItOffTheList(t *testing.T) {
	g := newSvcRig(t)
	_, err := g.m.Remove(g.cfg, g.p.KeyPath, false, "")
	if !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "off the list") {
		t.Fatalf("%v", err)
	}
}

// E2E-11: the failure words do not claim nothing was left half done, and a
// start failure never carries OS error text or paths.
func TestFailureWordsAreHonestAndPlain(t *testing.T) {
	_, next := FailureWords("other")
	if strings.Contains(next, "half done") {
		t.Fatalf("the default next step still says nothing was left half done: %q", next)
	}
	if what, n := FailureWords(string(ReasonRemoveFailed)); what == GenericFailure || n == "" {
		t.Fatalf("remove-failed has no words: %q %q", what, n)
	}
	raw := &os.PathError{Op: "fork/exec", Path: `C:\secret\path\unravis-blackbox.exe`, Err: os.ErrNotExist}
	got := startFailureWords(raw)
	if strings.Contains(got, `C:\`) || strings.Contains(got, "fork/exec") || !strings.Contains(got, "Reinstall Unravis") {
		t.Fatalf("%q", got)
	}
}
