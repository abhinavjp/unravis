package setup

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// quitHarness is a fake PC with an install, a counting process list and a counting fake sleep.
type quitHarness struct {
	e *testEnv
	// sleeps counts every call of the fake sleep; sleepsAtFirstEnd is its value when the first TerminateIf call came.
	sleeps           int
	sleepsAtFirstEnd int
	onSleep          func(n int)

	mu          sync.Mutex
	terminates  int
	terminateIf []Process
}

type spyProcesses struct {
	Processes
	h *quitHarness
}

func (s spyProcesses) Terminate(pid uint32) error {
	s.h.mu.Lock()
	s.h.terminates++
	s.h.mu.Unlock()
	return s.Processes.Terminate(pid)
}

func (s spyProcesses) TerminateIf(pid uint32, exe string) error {
	s.h.mu.Lock()
	if len(s.h.terminateIf) == 0 {
		s.h.sleepsAtFirstEnd = s.h.sleeps
	}
	s.h.terminateIf = append(s.h.terminateIf, Process{PID: pid, Exe: exe})
	s.h.mu.Unlock()
	return s.Processes.TerminateIf(pid, exe)
}

func newQuitHarness(t *testing.T) *quitHarness {
	t.Helper()
	e := newEnv(t, "1.2.0")
	priorInstall(func(e *testEnv) Options { return e.machineOpts() })(e)
	h := &quitHarness{e: e}
	e.sys.Proc = spyProcesses{Processes: e.f.Proc, h: h}
	e.sys.Sleep = func(time.Duration) {
		h.mu.Lock()
		h.sleeps++
		n := h.sleeps
		hook := h.onSleep
		h.mu.Unlock()
		if hook != nil {
			hook(n)
		}
	}
	return h
}

// upgradeWithSleep is upgrade with a function that runs on every fake sleep (n counts from 1).
func (h *quitHarness) upgradeWithSleep(consent bool, onSleep func(n int)) Result {
	h.mu.Lock()
	h.onSleep = onSleep
	h.mu.Unlock()
	return h.upgrade(consent)
}

func (h *quitHarness) upgrade(consent bool) Result {
	h.e.t.Helper()
	o := h.e.machineOpts()
	o.CloseRunningApp = consent
	return h.e.run(OpUpgrade, o, UninstallOptions{})
}

func (h *quitHarness) list() []Process {
	l, _ := h.e.f.Proc.List()
	return l
}

func (h *quitHarness) has(pid uint32) bool {
	for _, q := range h.list() {
		if q.PID == pid {
			return true
		}
	}
	return false
}

func (h *quitHarness) removeFromList(pid uint32) {
	h.e.f.Proc.mu.Lock()
	defer h.e.f.Proc.mu.Unlock()
	for i, q := range h.e.f.Proc.List_ {
		if q.PID == pid {
			h.e.f.Proc.List_ = append(h.e.f.Proc.List_[:i], h.e.f.Proc.List_[i+1:]...)
			return
		}
	}
}

func (h *quitHarness) addToList(q Process) {
	h.e.f.Proc.mu.Lock()
	defer h.e.f.Proc.mu.Unlock()
	h.e.f.Proc.List_ = append(h.e.f.Proc.List_, q)
}

func (h *quitHarness) terminatedPIDs() []uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []uint32
	for _, q := range h.terminateIf {
		out = append(out, q.PID)
	}
	return out
}

func TestUpgradeAsksTheAppToQuitFirst(t *testing.T) {
	h := newQuitHarness(t)
	h.e.f.UseAskQuit(h.e.sys)
	h.e.f.QuitRemovesApps = true
	pid := addApp(h.e, h.e.r.UserSID)
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if h.e.f.AskQuitCalls != 1 {
		t.Errorf("AskQuitCalls = %d, want 1", h.e.f.AskQuitCalls)
	}
	if h.terminates != 0 || len(h.terminateIf) != 0 {
		t.Errorf("terminate calls %d / %d, want none: the app quit by itself", h.terminates, len(h.terminateIf))
	}
	if h.has(pid) || !res.RestartApp {
		t.Errorf("process still listed %v, RestartApp %v", h.has(pid), res.RestartApp)
	}
}

func TestUpgradeTerminatesWhatIgnoredTheQuitRequest(t *testing.T) {
	h := newQuitHarness(t)
	h.e.f.UseAskQuit(h.e.sys) // QuitRemovesApps stays false: the app ignores the request
	pid := addApp(h.e, h.e.r.UserSID)
	exe := appPath(h.e)
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if h.e.f.AskQuitCalls != 1 {
		t.Errorf("AskQuitCalls = %d, want 1", h.e.f.AskQuitCalls)
	}
	if h.sleepsAtFirstEnd != askQuitRounds {
		t.Errorf("polled %d rounds before ending it, want %d", h.sleepsAtFirstEnd, askQuitRounds)
	}
	if got := h.terminateIf; len(got) != 1 || got[0].PID != pid || !samePath(got[0].Exe, exe) {
		t.Errorf("TerminateIf calls %+v, want one for %d %s", got, pid, exe)
	}
	if h.terminates != 0 {
		t.Errorf("plain Terminate was called %d times", h.terminates)
	}
	if h.has(pid) || !res.RestartApp {
		t.Errorf("process still listed %v, RestartApp %v", h.has(pid), res.RestartApp)
	}
}

func TestUpgradeDoesNotTerminateAReusedPID(t *testing.T) {
	h := newQuitHarness(t)
	pid := addApp(h.e, h.e.r.UserSID)
	other := filepath.Join(NewLayout(h.e.machineOpts(), h.e.dirs).InstallDir, "other", AppExeName)
	asked := 0
	h.e.sys.AskQuit = func() error {
		asked++
		// The app quits and Windows gives its PID to another program in the same folder tree.
		h.removeFromList(pid)
		h.addToList(Process{PID: pid, Exe: other, SID: h.e.r.UserSID})
		return nil
	}
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if asked != 1 {
		t.Errorf("asked %d times", asked)
	}
	if !h.has(pid) {
		t.Error("the program that got the PID was ended")
	}
	if len(h.terminateIf) != 0 || h.terminates != 0 {
		t.Errorf("terminate calls %+v / %d, want none", h.terminateIf, h.terminates)
	}
}

func TestStillRunningMatchesPIDAndPath(t *testing.T) {
	orig := []Process{{PID: 1, Exe: `C:\a\unravis.exe`}, {PID: 2, Exe: `C:\a\unravis.exe`}}
	latest := []Process{
		{PID: 1, Exe: `C:\A\Unravis.exe`}, // same program
		{PID: 2, Exe: `C:\b\unravis.exe`}, // PID reused by another path
		{PID: 3, Exe: `C:\a\unravis.exe`}, // a new process
	}
	got := stillRunning(orig, latest)
	if len(got) != 1 || got[0].PID != 1 {
		t.Errorf("stillRunning = %+v, want only PID 1", got)
	}
	if got := stillRunning(nil, latest); len(got) != 0 {
		t.Errorf("stillRunning(nil, ...) = %+v", got)
	}
}

func TestUpgradeTerminatesOnlyWhatIsLeftInTheLastRound(t *testing.T) {
	h := newQuitHarness(t)
	first := addApp(h.e, h.e.r.UserSID)
	second := addApp(h.e, "")
	h.e.sys.AskQuit = func() error { return nil }
	res := h.upgradeWithSleep(true, func(n int) {
		if n == 3 {
			h.removeFromList(first) // one of the two quits while setup is waiting
		}
	})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	got := h.terminatedPIDs()
	if len(got) != 1 || got[0] != second {
		t.Errorf("terminated %v, want only %d", got, second)
	}
	if h.has(first) || h.has(second) {
		t.Errorf("list %+v", h.list())
	}
}

func TestUpgradeFallsBackToTerminateWhenAskQuitFails(t *testing.T) {
	h := newQuitHarness(t)
	h.e.f.UseAskQuit(h.e.sys)
	h.e.f.AskQuitErr = errors.New("no instance")
	h.e.f.QuitRemovesApps = true
	pid := addApp(h.e, h.e.r.UserSID)
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if h.e.f.AskQuitCalls != 1 {
		t.Errorf("AskQuitCalls = %d", h.e.f.AskQuitCalls)
	}
	if got := h.terminatedPIDs(); len(got) != 1 || got[0] != pid {
		t.Errorf("terminated %v, want %d", got, pid)
	}
	if h.sleepsAtFirstEnd != 0 {
		t.Errorf("polled %d rounds after a failed ask", h.sleepsAtFirstEnd)
	}
	if h.has(pid) {
		t.Error("the app is still running")
	}
	if log := strings.Join(h.e.log, "\n"); !strings.Contains(log, "asking Unravis to quit failed") {
		t.Errorf("the failure was not logged:\n%s", log)
	}
}

func TestUpgradeWithoutAskQuitSeamBehavesAsBefore(t *testing.T) {
	h := newQuitHarness(t)
	if h.e.sys.AskQuit != nil {
		t.Fatal("AskQuit is set")
	}
	pid := addApp(h.e, h.e.r.UserSID)
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone || !res.RestartApp {
		t.Fatalf("%+v", res)
	}
	if got := h.terminatedPIDs(); len(got) != 1 || got[0] != pid {
		t.Errorf("terminated %v, want %d", got, pid)
	}
	if h.sleepsAtFirstEnd != 0 {
		t.Errorf("polled %d rounds without a seam", h.sleepsAtFirstEnd)
	}
	if h.has(pid) {
		t.Error("the app is still running")
	}
}

func TestUpgradeDoesNotAskWithoutConsent(t *testing.T) {
	h := newQuitHarness(t)
	h.e.f.UseAskQuit(h.e.sys)
	h.e.f.QuitRemovesApps = true
	pid := addApp(h.e, h.e.r.UserSID)
	res := h.upgrade(false)
	mustFail(t, res, "Unravis is open")
	if h.e.f.AskQuitCalls != 0 {
		t.Errorf("AskQuitCalls = %d, want 0 without the person's yes", h.e.f.AskQuitCalls)
	}
	if !h.has(pid) || len(h.terminateIf) != 0 || h.terminates != 0 {
		t.Error("the app was touched without consent")
	}
}

func TestUpgradeNeverTouchesOtherUsersProcesses(t *testing.T) {
	t.Run("one is open before setup starts", func(t *testing.T) {
		h := newQuitHarness(t)
		h.e.f.UseAskQuit(h.e.sys)
		h.e.f.QuitRemovesApps = true
		mine := addApp(h.e, h.e.r.UserSID)
		theirs := addApp(h.e, "S-1-5-21-2000")
		unknown := addApp(h.e, UnknownOwner)
		res := h.upgrade(true)
		mustFail(t, res, "Unravis is open for another person on this PC")
		if h.e.f.AskQuitCalls != 0 {
			t.Errorf("AskQuitCalls = %d, want 0", h.e.f.AskQuitCalls)
		}
		if !h.has(mine) || !h.has(theirs) || !h.has(unknown) || len(h.terminateIf) != 0 || h.terminates != 0 {
			t.Errorf("a program was touched: %+v", h.list())
		}
	})
	t.Run("one starts while setup is waiting", func(t *testing.T) {
		h := newQuitHarness(t)
		mine := addApp(h.e, h.e.r.UserSID)
		h.e.sys.AskQuit = func() error { return nil }
		var theirs uint32 = 9000
		res := h.upgradeWithSleep(true, func(n int) {
			if n == 2 {
				h.addToList(Process{PID: theirs, Exe: appPath(h.e), SID: "S-1-5-21-2000"})
			}
		})
		if res.Outcome != OutcomeDone {
			t.Fatalf("%+v", res)
		}
		if got := h.terminatedPIDs(); len(got) != 1 || got[0] != mine {
			t.Errorf("terminated %v, want only %d", got, mine)
		}
		if !h.has(theirs) {
			t.Error("another user's program was ended")
		}
	})
}

func TestPollingIsBounded(t *testing.T) {
	h := newQuitHarness(t)
	h.e.f.UseAskQuit(h.e.sys) // the app never quits
	addApp(h.e, h.e.r.UserSID)
	res := h.upgrade(true)
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if h.sleepsAtFirstEnd > askQuitRounds || h.sleepsAtFirstEnd == 0 {
		t.Errorf("sleeps before the terminate = %d, want 1..%d", h.sleepsAtFirstEnd, askQuitRounds)
	}
	if askQuitRounds != 20 {
		t.Errorf("askQuitRounds = %d, want 20", askQuitRounds)
	}
}
