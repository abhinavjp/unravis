package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// updateRig is an installed, running recorder and a newer program on the app side.
func updateRig(t *testing.T) *svcRig {
	t.Helper()
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.env.exe, []byte("MZ program, newer"), 0o755); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *svcRig) installedExe() string { return filepath.Join(g.prog, InstalledExeName) }

func updText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateHappyPathRestartsARunningRecorder(t *testing.T) {
	g := updateRig(t)
	cfgBefore, keyBefore := updText(t, g.p.ConfigPath), updText(t, g.p.KeyPath)
	res, err := g.m.Update()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || !res.WasRunning || !res.Restarted || res.OldHash == "" || res.NewHash == "" || res.OldHash == res.NewHash {
		t.Fatalf("%+v", res)
	}
	if updText(t, g.installedExe()) != "MZ program, newer" {
		t.Fatal("the installed program was not replaced")
	}
	if updText(t, g.installedExe()+OldProgramSuffix) != "MZ program" {
		t.Fatal("the previous program was not kept as .old")
	}
	if g.svc.state != SvcRunning || g.svc.did("stop") != 1 || g.svc.did("start") != 2 { // install start + update start
		t.Fatalf("state %s calls %v", g.svc.state, g.svc.calls)
	}
	if g.env.alive != 1 {
		t.Fatalf("status page asked %d times", g.env.alive)
	}
	if updText(t, g.p.ConfigPath) != cfgBefore || updText(t, g.p.KeyPath) != keyBefore {
		t.Fatal("settings or key changed")
	}
	left, _ := filepath.Glob(g.installedExe() + ".new-*")
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestUpdateAlreadyCurrentChangesNothing(t *testing.T) {
	g := newSvcRig(t)
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	calls := len(g.svc.calls)
	res, err := g.m.Update()
	if err != nil || res.Updated || res.Restarted || res.OldHash == "" || res.OldHash != res.NewHash {
		t.Fatalf("%+v %v", res, err)
	}
	if len(g.svc.calls) != calls || g.svc.state != SvcRunning {
		t.Fatalf("the service was touched: %v", g.svc.calls)
	}
	if _, err := os.Stat(g.installedExe() + OldProgramSuffix); err == nil {
		t.Fatal("made an .old copy for nothing")
	}
}

func TestUpdateRunFromTheInstalledProgramIsCurrent(t *testing.T) {
	g := updateRig(t)
	g.env.exe = g.installedExe()
	res, err := g.m.Update()
	if err != nil || res.Updated {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUpdateNotInstalledRefuses(t *testing.T) {
	g := newSvcRig(t)
	_, err := g.m.Update()
	if !errors.Is(err, ErrNotInstalled) || len(g.svc.calls) != 0 {
		t.Fatalf("%v %v", err, g.svc.calls)
	}
}

func TestUpdateNeedsAdmin(t *testing.T) {
	g := updateRig(t)
	g.env.admin = false
	if _, err := g.m.Update(); !errors.Is(err, ErrNotAdmin) {
		t.Fatal(err)
	}
	if updText(t, g.installedExe()) != "MZ program" {
		t.Fatal("changed the program without administrator rights")
	}
}

func TestUpdateStoppedRecorderStaysStopped(t *testing.T) {
	g := updateRig(t)
	g.svc.state = SvcStopped
	starts := g.svc.did("start")
	res, err := g.m.Update()
	if err != nil || !res.Updated || res.WasRunning || res.Restarted {
		t.Fatalf("%+v %v", res, err)
	}
	if g.svc.state != SvcStopped || g.svc.did("start") != starts || g.svc.did("stop") != 0 {
		t.Fatalf("state %s calls %v", g.svc.state, g.svc.calls)
	}
	if updText(t, g.installedExe()) != "MZ program, newer" {
		t.Fatal("not replaced")
	}
}

func TestUpdateCopyMismatchChangesNothingAndRestarts(t *testing.T) {
	g := updateRig(t)
	g.env.hashFor = func(p string) (string, bool) {
		if strings.Contains(filepath.Base(p), ".new-") {
			return strings.Repeat("0", 64), true // the staged copy is not the program
		}
		return "", false
	}
	_, err := g.m.Update()
	var ue *UpdateError
	if !errors.As(err, &ue) || ue.Reason != ReasonUpdateFailed || ue.RolledBack || !ue.Running {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(ue.Message, "did not match") || !strings.Contains(ue.Message, "running with the previous program") {
		t.Fatal(ue.Message)
	}
	if updText(t, g.installedExe()) != "MZ program" || g.svc.state != SvcRunning {
		t.Fatal("the old recorder was not left in place and running")
	}
	left, _ := filepath.Glob(g.installedExe() + ".new-*")
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestUpdateStartFailureRollsBack(t *testing.T) {
	g := updateRig(t)
	g.svc.failStarts = 1 // the new program does not start; the old one does
	_, err := g.m.Update()
	var ue *UpdateError
	if !errors.As(err, &ue) || !ue.RolledBack || !ue.Running {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(ue.Message, "did not start") || !strings.Contains(ue.Message, "running with it again") {
		t.Fatal(ue.Message)
	}
	if updText(t, g.installedExe()) != "MZ program" || g.svc.state != SvcRunning {
		t.Fatal("not rolled back")
	}
}

func TestUpdateStatusProbeFailureRollsBack(t *testing.T) {
	g := updateRig(t)
	g.env.aliveFails = 1
	_, err := g.m.Update()
	var ue *UpdateError
	if !errors.As(err, &ue) || !ue.RolledBack || !ue.Running {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(ue.Message, "status page did not answer") {
		t.Fatal(ue.Message)
	}
	if updText(t, g.installedExe()) != "MZ program" || g.svc.state != SvcRunning {
		t.Fatal("not rolled back")
	}
	// The rollback restarted the old program: one more start than the update's own.
	if g.svc.did("start") != 3 {
		t.Fatalf("calls %v", g.svc.calls)
	}
}

func TestUpdateRollbackThatCannotRestartSaysSo(t *testing.T) {
	g := updateRig(t)
	g.svc.failStarts = 2 // the new and the old program both fail to start
	_, err := g.m.Update()
	var ue *UpdateError
	if !errors.As(err, &ue) || !ue.RolledBack || ue.Running {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(ue.Message, "recorder is stopped") {
		t.Fatal(ue.Message)
	}
}

func TestUpdateRollbackHashMismatchIsReported(t *testing.T) {
	g := updateRig(t)
	g.svc.failStarts = 1
	oldSum, _ := g.env.real.FileSHA256(g.installedExe())
	// After the swap every read of the installed program looks wrong, so the
	// rollback cannot prove the old program is back.
	armed := false
	g.svc.onStart = func() { armed = true } // from the first start of the new program on
	g.env.hashFor = func(p string) (string, bool) {
		if armed && p == g.installedExe() {
			if h, _ := g.env.real.FileSHA256(p); h == oldSum {
				return strings.Repeat("1", 64), true
			}
		}
		return "", false
	}
	_, err := g.m.Update()
	var ue *UpdateError
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("%v", err)
	}
	if ue.RolledBack || !strings.Contains(ue.Message, "could not be put back") {
		t.Fatalf("%#v", ue)
	}
}

func TestUpdateWithoutAliveProberWatchesTheState(t *testing.T) {
	g := updateRig(t)
	m := *g.m
	m.Env = bareEnv{g.env}
	res, err := m.Update()
	if err != nil || !res.Restarted {
		t.Fatalf("%+v %v", res, err)
	}
	if g.env.slept == 0 {
		t.Fatal("did not wait before trusting the new recorder")
	}
}

// bareEnv hides the optional AliveProber of the fake.
type bareEnv struct{ e *fakeEnv }

func (b bareEnv) IsAdmin() bool                               { return b.e.IsAdmin() }
func (b bareEnv) ExePath() (string, error)                    { return b.e.ExePath() }
func (b bareEnv) ProgramDir() (string, error)                 { return b.e.ProgramDir() }
func (b bareEnv) SecureDir(d string, r bool) error            { return b.e.SecureDir(d, r) }
func (b bareEnv) PortFree(h string, p int) error              { return b.e.PortFree(h, p) }
func (b bareEnv) FolderWritable(d string) error               { return b.e.FolderWritable(d) }
func (b bareEnv) FileSHA256(p string) (string, error)         { return b.e.FileSHA256(p) }
func (b bareEnv) ProbeStatus(h string, p int, k string) error { return b.e.ProbeStatus(h, p, k) }
func (b bareEnv) Sleep(d time.Duration)                       { b.e.Sleep(d) }
