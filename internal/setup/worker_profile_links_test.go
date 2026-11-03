package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// WP-74j: the elevated worker never writes a link inside the user's profile; the window does it after the worker commits. All tests
// use the fake PC of newEnv: temporary folders and a fake system, never the real Desktop, Start menu or Startup folder.

const testSID = "S-1-5-21-1000"

// workerSeen records what the worker was asked: the sign-in value of the request it received.
type workerSeen struct {
	mu     sync.Mutex
	signIn bool
	n      int
}

func (w *workerSeen) get() (bool, int) { w.mu.Lock(); defer w.mu.Unlock(); return w.signIn, w.n }

// workerElevator is the fake "run as administrator" with the real WorkerPrepare, and a record of the request it received.
func workerElevator(e *testEnv, seen *workerSeen) *pipeElevator {
	inner := WorkerPrepare(testSID, e.dirs.UserProfile)
	return &pipeElevator{r: e.r, prepare: func(req *WorkRequest, r *Runner) error {
		if seen != nil {
			seen.mu.Lock()
			seen.signIn = req.Options.StartAtSignIn
			seen.n++
			seen.mu.Unlock()
		}
		return inner(req, r)
	}}
}

func windowBackend(e *testEnv, el Elevator) *LocalBackend {
	return &LocalBackend{R: e.r, Elev: el, Mode: OpInstall}
}

func runWindow(t *testing.T, be *LocalBackend, req WorkRequest) Result {
	t.Helper()
	return be.Run(context.Background(), req, func(StepEvent) {}, func(string) {})
}

// workerOnly sends one request to the worker directly (no window), so only the worker's own writes are in the picture.
func workerOnly(e *testEnv, req WorkRequest) Result {
	u := e.dirs.User()
	req.User = &u
	return RunWorker(context.Background(), workerElevator(e, nil), req, nil, nil)
}

func listNames(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, en := range ents {
		out = append(out, en.Name())
	}
	return out
}

// afterShortcuts runs f in the worker right after its "shortcuts" step is done (an uninstall has no commit hook, so the step event
// is the place to look). The worker's copy of the runner calls this Events first.
func afterShortcuts(e *testEnv, f func()) {
	prev := e.r.Events
	e.r.Events = func(ev StepEvent) {
		if prev != nil {
			prev(ev)
		}
		if ev.ID == "shortcuts" && ev.State == StepDone {
			f()
		}
	}
}

func TestWorkerNeverWritesIntoTheUserProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		user bool
	}{{"machine scope", false}, {"user scope with Blackbox", true}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "1.0.0")
			if err := os.Remove(e.dirs.Desktop); err != nil { // a folder that does not exist must still not exist afterwards
				t.Fatal(err)
			}
			o := e.machineOpts()
			if tc.user {
				o = e.withBlackbox(e.userOpts(), false)
			}
			l := NewLayout(o, e.dirs)
			var desktop, startup []string
			var startMenu, quit bool
			e.r.BeforeCommit = func(*Journal) { // after the "shortcuts" step
				desktop, startup = listNames(e.dirs.Desktop), listNames(e.dirs.Startup)
				startMenu, quit = exists(l.StartMenuLink), exists(l.QuitLink)
			}
			res := workerOnly(e, WorkRequest{Op: OpInstall, Options: o})
			if res.Outcome != OutcomeDone {
				t.Fatalf("outcome %s: %s %s", res.Outcome, res.Message, res.Fix)
			}
			if len(desktop) != 0 || len(startup) != 0 {
				t.Fatalf("the worker wrote into the profile: desktop %v startup %v", desktop, startup)
			}
			if exists(e.dirs.Desktop) {
				t.Fatal("the worker made the Desktop folder")
			}
			if names := listNames(e.dirs.Startup); len(names) != 0 {
				t.Fatalf("Startup folder: %v", names)
			}
			if tc.user {
				if startMenu || quit || exists(l.StartMenuLink) || exists(l.QuitLink) {
					t.Fatal("the worker wrote the profile Start menu or Quit link")
				}
			} else if !startMenu || !quit || !exists(l.StartMenuLink) || !exists(l.QuitLink) {
				t.Fatal("the machine-scope Start menu and Quit links are part of the install")
			}
		})
	}
}

func TestWorkerLeavesAPlantedDesktopLinkAlone(t *testing.T) {
	plant := func(e *testEnv) (Layout, []byte) {
		l := NewLayout(e.machineOpts(), e.dirs)
		putLink(t, l.DesktopLink, filepath.Join(e.root, "Other", "other.exe"), "")
		data, err := os.ReadFile(l.DesktopLink)
		if err != nil {
			t.Fatal(err)
		}
		return l, data
	}
	// Worker half.
	e := newEnv(t, "1.0.0")
	l, want := plant(e)
	var during []byte
	e.r.BeforeCommit = func(*Journal) { during, _ = os.ReadFile(l.DesktopLink) }
	if res := workerOnly(e, WorkRequest{Op: OpInstall, Options: e.machineOpts()}); res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	got, _ := os.ReadFile(l.DesktopLink)
	if !bytes.Equal(during, want) || !bytes.Equal(got, want) {
		t.Fatal("the worker changed a foreign Desktop link")
	}
	// Window half: the whole run through LocalBackend.Run.
	e2 := newEnv(t, "1.0.0")
	l2, want2 := plant(e2)
	if res := runWindow(t, windowBackend(e2, workerElevator(e2, nil)), WorkRequest{Op: OpInstall, Options: e2.machineOpts()}); res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if got, _ := os.ReadFile(l2.DesktopLink); !bytes.Equal(got, want2) {
		t.Fatal("the window changed a foreign Desktop link")
	}
}

func TestWindowWritesTheDesktopAndStartupLinksAfterTheWorker(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	l := NewLayout(o, e.dirs)
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeDone || res.AppExe == "" {
		t.Fatalf("result %+v", res)
	}
	if target, args := readLink(t, l.DesktopLink); !samePath(target, res.AppExe) || args != "" {
		t.Fatalf("Desktop link = %q %q (exe %q)", target, args, res.AppExe)
	}
	if target, args := readLink(t, l.StartupLink); !samePath(target, res.AppExe) || args != startupLinkArg {
		t.Fatalf("Startup link = %q %q", target, args)
	}
}

func TestWindowWritesStartMenuAndQuitLinksForUserScopeAfterTheWorker(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.withBlackbox(e.userOpts(), false)
	l := NewLayout(o, e.dirs)
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s %s", res.Outcome, res.Message, res.Fix)
	}
	if target, args := readLink(t, l.StartMenuLink); !samePath(target, res.AppExe) || args != "" {
		t.Fatalf("Start menu link = %q %q", target, args)
	}
	if target, args := readLink(t, l.QuitLink); !samePath(target, res.AppExe) || args != quitLinkArg {
		t.Fatalf("Quit link = %q %q", target, args)
	}
}

func TestWindowWritesNoStartupLinkWhenTheOptionIsOff(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	o.StartAtSignIn = false
	l := NewLayout(o, e.dirs)
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if exists(l.StartupLink) {
		t.Fatal("a Startup link was written with the option off")
	}
	if !exists(l.DesktopLink) {
		t.Fatal("the Desktop link is missing")
	}
}

func TestWorkerUninstallLeavesTheLinksAndTheWindowRemovesOnlyOurs(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	e.mustRun(OpInstall, o, UninstallOptions{}) // in-process: the links are in its transaction
	l := NewLayout(o, e.dirs)
	foreign := filepath.Join(e.root, "Other", "other.exe")
	putLink(t, l.DesktopLink, foreign, "")
	var desktop, startup bool
	afterShortcuts(e, func() { desktop, startup = exists(l.DesktopLink), exists(l.StartupLink) })
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpUninstall})
	if res.Outcome != OutcomeLeftovers {
		t.Fatalf("outcome %s: %s (left %v)", res.Outcome, res.Message, res.Left)
	}
	if !desktop || !startup {
		t.Fatalf("the worker removed a profile link: desktop %v startup %v | log: %s", desktop, startup, strings.Join(e.log, " | "))
	}
	if exists(l.StartupLink) {
		t.Fatal("our Startup link is still there")
	}
	if target, _ := readLink(t, l.DesktopLink); !samePath(target, foreign) {
		t.Fatalf("the foreign Desktop link changed: %q", target)
	}
	found := false
	for _, s := range res.Left {
		if strings.Contains(s, l.DesktopLink) && strings.Contains(s, "kept") {
			found = true
		}
	}
	if !found {
		t.Fatalf("res.Left = %v", res.Left)
	}
}

func TestWorkerUninstallLeavesUserScopeStartMenuAndQuitLinks(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.withBlackbox(e.userOpts(), false)
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if !exists(l.StartMenuLink) || !exists(l.QuitLink) {
		t.Fatal("the install did not write the profile links")
	}
	var startMenu, quit bool
	afterShortcuts(e, func() { startMenu, quit = exists(l.StartMenuLink), exists(l.QuitLink) })
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpUninstall})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s (left %v)", res.Outcome, res.Message, res.Left)
	}
	if !startMenu || !quit {
		t.Fatalf("the worker removed a profile link: start menu %v quit %v", startMenu, quit)
	}
	if exists(l.StartMenuLink) || exists(l.QuitLink) || exists(l.DesktopLink) || exists(l.StartupLink) {
		t.Fatal("the window left one of our links behind")
	}
}

func TestFailedWorkerRunWritesNoLinks(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	l := NewLayout(o, e.dirs)
	e.r.FailAfter = func(step string) error {
		if step == "shortcuts" {
			return errors.New("injected")
		}
		return nil
	}
	res := runWindow(t, windowBackend(e, workerElevator(e, nil)), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	for _, p := range []string{l.DesktopLink, l.StartupLink, l.StartMenuLink, l.QuitLink} {
		if exists(p) {
			t.Fatalf("%s exists after a rolled-back run", p)
		}
	}
	if len(res.Left) != 0 {
		t.Fatalf("left %v", res.Left)
	}
}

func TestLinkWriteFailureIsListedAndDoesNotFailTheInstall(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	// The Desktop folder cannot be written: a file stands where the folder should be.
	if err := os.Remove(e.dirs.Desktop); err != nil {
		t.Fatal(err)
	}
	writeFile(t, e.dirs.Desktop, "not a folder")
	var logs []string
	be := windowBackend(e, workerElevator(e, nil))
	res := be.Run(context.Background(), WorkRequest{Op: OpInstall, Options: o}, func(StepEvent) {}, func(s string) { logs = append(logs, s) })
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if len(res.Left) != 1 {
		t.Fatalf("res.Left = %v (logs %q)", res.Left, logs)
	}
}

// installedWithSignIn installs machine scope in this process with the option on, then removes the Startup link (the tray toggle).
func installedWithSignIn(t *testing.T, e *testEnv) Layout {
	t.Helper()
	o := e.machineOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if !savedStartAtSignIn(t, e) {
		t.Fatal("the install did not save the option as on")
	}
	return l
}

func TestUpgradeSendsTheSignInValueReadFromTheStartupLink(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := installedWithSignIn(t, e)
	if err := os.Remove(l.StartupLink); err != nil {
		t.Fatal(err)
	}
	var seen workerSeen
	res := runWindow(t, windowBackend(e, workerElevator(e, &seen)), WorkRequest{Op: OpUpgrade, Options: Options{StartAtSignIn: true}})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if signIn, n := seen.get(); n != 1 || signIn {
		t.Fatalf("worker got StartAtSignIn=%v (requests %d)", signIn, n)
	}
	if exists(l.StartupLink) {
		t.Fatal("the window wrote the Startup link back")
	}
}

func TestUpgradeKeepsTheSavedSignInValueWhenTheLinkCannotBeRead(t *testing.T) {
	e := newEnv(t, "1.0.0")
	installedWithSignIn(t, e)
	old := readLinkFile
	readLinkFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	t.Cleanup(func() { readLinkFile = old })
	var seen workerSeen
	res := runWindow(t, windowBackend(e, workerElevator(e, &seen)), WorkRequest{Op: OpUpgrade, Options: Options{StartAtSignIn: false}})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if signIn, n := seen.get(); n != 1 || !signIn {
		t.Fatalf("worker got StartAtSignIn=%v (requests %d); want the saved true", signIn, n)
	}
}

func TestInstallRequestOverAnInstallIsTreatedAsUpgrade(t *testing.T) {
	e := newEnv(t, "1.0.0")
	l := installedWithSignIn(t, e)
	if err := os.Remove(l.StartupLink); err != nil {
		t.Fatal(err)
	}
	var seen workerSeen
	o := e.machineOpts() // the page says: start at sign-in on
	res := runWindow(t, windowBackend(e, workerElevator(e, &seen)), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if signIn, n := seen.get(); n != 1 || signIn {
		t.Fatalf("worker got StartAtSignIn=%v (requests %d); want false, from the missing link", signIn, n)
	}
}

func TestWorkerPrepareSetsNoProfileLinks(t *testing.T) {
	e := newEnv(t, "1.0.0")
	u := e.dirs.User()
	// A request that tries to switch the flag off (an unknown field) cannot: the wire type has no such field.
	var req WorkRequest
	raw := `{"op":"install","noProfileLinks":false,"options":{"scope":"machine","noProfileLinks":false}}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	req.User = &u
	r := &Runner{Sys: e.sys, Dirs: e.dirs}
	if err := WorkerPrepare(testSID, e.dirs.UserProfile)(&req, r); err != nil {
		t.Fatal(err)
	}
	if !r.NoProfileLinks {
		t.Fatal("WorkerPrepare did not set NoProfileLinks")
	}
}

func TestServeWorkerSetsNoProfileLinksEvenWithNilPrepare(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.machineOpts()
	l := NewLayout(o, e.dirs)
	if e.r.NoProfileLinks {
		t.Fatal("the caller's runner must start without the flag")
	}
	a, b := net.Pipe()
	done := make(chan Result, 1)
	go func() { done <- ServeWorker(context.Background(), b, e.r, nil); b.Close() }()
	res := RunWorker(context.Background(), connOnly{a}, WorkRequest{Op: OpInstall, Options: o}, nil, nil)
	<-done
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if exists(l.DesktopLink) || exists(l.StartupLink) {
		t.Fatal("a worker with a nil prepare wrote a profile link")
	}
	if !exists(l.StartMenuLink) {
		t.Fatal("the machine Start menu link is part of the install")
	}
	if e.r.NoProfileLinks {
		t.Fatal("the worker changed the caller's runner")
	}
}

// connOnly hands RunWorker an already open pipe end.
type connOnly struct{ c net.Conn }

func (c connOnly) Start(context.Context) (io.ReadWriteCloser, error) { return c.c, nil }

func TestInProcessRunKeepsTheLinksInItsTransaction(t *testing.T) {
	e := newEnv(t, "1.0.0")
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	be := windowBackend(e, &pipeElevator{err: ErrUACCancelled})
	res := runWindow(t, be, WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	for _, p := range []string{l.StartMenuLink, l.QuitLink, l.DesktopLink, l.StartupLink} {
		if !exists(p) {
			t.Fatalf("%s was not written", p)
		}
	}
	// A fault after the "shortcuts" step takes every link back out.
	e2 := newEnv(t, "1.0.0")
	l2 := NewLayout(o, e2.dirs)
	e2.r.FailAfter = func(step string) error {
		if step == "shortcuts" {
			return errors.New("injected")
		}
		return nil
	}
	res = runWindow(t, windowBackend(e2, &pipeElevator{err: ErrUACCancelled}), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	for _, p := range []string{l2.StartMenuLink, l2.QuitLink, l2.DesktopLink, l2.StartupLink} {
		if exists(p) {
			t.Fatalf("%s survived the rollback", p)
		}
	}
}
