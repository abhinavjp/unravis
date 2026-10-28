package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeElevator is a fake "run as administrator": the worker runs ServeWorker in a goroutine on one end of a net.Pipe.
type pipeElevator struct {
	r      *Runner
	err    error
	served chan Result
	// hook runs on the worker's end before it serves (to cut the pipe early).
	hook func(net.Conn)
	// prepare is the worker's check of the request (WorkerPrepare in the real worker).
	prepare func(*WorkRequest, *Runner) error
}

func (p *pipeElevator) Start(ctx context.Context) (io.ReadWriteCloser, error) {
	if p.err != nil {
		return nil, p.err
	}
	a, b := net.Pipe()
	p.served = make(chan Result, 1)
	go func() {
		defer b.Close()
		if p.hook != nil {
			p.hook(b)
			p.served <- Result{}
			return
		}
		p.served <- ServeWorker(context.Background(), b, p.r, p.prepare)
	}()
	return a, nil
}

type eventLog struct {
	mu    sync.Mutex
	evs   []StepEvent
	lines []string
}

func (l *eventLog) ev(e StepEvent) { l.mu.Lock(); l.evs = append(l.evs, e); l.mu.Unlock() }
func (l *eventLog) ln(s string)    { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }

func TestWorkerRunsTheRequestAndStreamsProgress(t *testing.T) {
	e := newEnv(t, "1.0.0")
	el := &pipeElevator{r: e.r}
	var got eventLog
	res := RunWorker(context.Background(), el, WorkRequest{Op: OpInstall, Options: e.withBlackbox(e.machineOpts(), false)}, got.ev, got.ln)
	if res.Outcome != OutcomeDone {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if res.ReadKey == "" {
		t.Fatal("the window gets the read key to show once")
	}
	if len(got.evs) == 0 || len(got.lines) == 0 {
		t.Fatalf("events %d lines %d", len(got.evs), len(got.lines))
	}
	for _, l := range got.lines {
		if strings.Contains(l, res.ReadKey) {
			t.Fatalf("a log line carries the read key: %q", l)
		}
	}
	if w := <-el.served; w.Outcome != OutcomeDone {
		t.Fatalf("worker side %s", w.Outcome)
	}
	if old, _ := Detect(e.sys.Reg); old == nil || old.Root != HKLM {
		t.Fatalf("installed %+v", old)
	}
}

func TestWorkerRefusesUnknownActionsAndGarbage(t *testing.T) {
	e := newEnv(t, "1.0.0")
	before := e.snapshot()
	for name, line := range map[string]string{
		"unknown op":  `{"type":"request","request":{"op":"run-command","options":{}}}`,
		"not request": `{"type":"cancel"}`,
		"garbage":     `del C:\ /s`,
	} {
		a, b := net.Pipe()
		done := make(chan Result, 1)
		go func() { done <- ServeWorker(context.Background(), b, e.r, nil); b.Close() }()
		go func() { _, _ = a.Write([]byte(line + "\n")) }()
		var m WireMsg
		dec := json.NewDecoder(a)
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		a.Close()
		if m.Type != MsgResult || m.Result.Outcome != OutcomeInvalid || !strings.Contains(m.Result.Message, "Nothing was changed") {
			t.Fatalf("%s: %+v", name, m.Result)
		}
		<-done
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("the worker changed something:\n%s", d)
	}
}

func TestCancelAtUACChangesNothing(t *testing.T) {
	e := newEnv(t, "1.0.0")
	before := e.snapshot()
	res := RunWorker(context.Background(), &pipeElevator{err: ErrUACCancelled}, WorkRequest{Op: OpInstall, Options: e.machineOpts()}, nil, nil)
	if res.Outcome != OutcomeCancelledUAC || res.Outcome.ExitCode() != 3 || !strings.Contains(res.Message, "nothing was changed") {
		t.Fatalf("%+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("changed:\n%s", d)
	}
}

func TestCancelFromTheWindowReachesTheWorker(t *testing.T) {
	e := newEnv(t, "1.0.0")
	before := e.snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	e.r.BeforeStep = func(step string) {
		if step == "shortcuts" {
			cancel()
			// The cancel travels over the pipe; wait until the worker has seen it.
			time.Sleep(200 * time.Millisecond)
		}
	}
	el := &pipeElevator{r: e.r}
	res := RunWorker(ctx, el, WorkRequest{Op: OpInstall, Options: e.machineOpts()}, nil, nil)
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	<-el.served
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("a cancel must put everything back:\n%s", d)
	}
}

func TestWorkerGoneWithoutResultSaysRunAgain(t *testing.T) {
	el := &pipeElevator{hook: func(c net.Conn) {
		buf := make([]byte, 4096)
		_, _ = c.Read(buf) // takes the request, then dies
	}}
	res := RunWorker(context.Background(), el, WorkRequest{Op: OpInstall}, nil, nil)
	if res.Outcome != OutcomeRollbackLeft || !strings.Contains(res.Fix, "Run setup again") {
		t.Fatalf("%+v", res)
	}
}

func TestWindowGoneCancelsTheWorkersRun(t *testing.T) {
	e := newEnv(t, "1.0.0")
	before := e.snapshot()
	a, b := net.Pipe()
	e.r.BeforeStep = func(step string) {
		if step == "shortcuts" {
			a.Close() // the window is closed in the middle of the run
			time.Sleep(200 * time.Millisecond)
		}
	}
	go func() {
		req, _ := json.Marshal(WireMsg{Type: MsgRequest, Request: &WorkRequest{Op: OpInstall, Options: e.machineOpts()}})
		_, _ = a.Write(append(req, '\n'))
		_, _ = io.Copy(io.Discard, a)
	}()
	res := ServeWorker(context.Background(), b, e.r, nil)
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("changed:\n%s", d)
	}
}

func TestPerformStopsAtPreflightAndAtABadPlan(t *testing.T) {
	e := newEnv(t, "1.0.0")
	before := e.snapshot()
	saved := e.f.Mach.Version
	e.f.Mach.Version = WindowsVersion{Major: 6, Minor: 1, Build: 7601, Is64: true}
	res := e.r.Perform(context.Background(), WorkRequest{Op: OpInstall, Options: e.machineOpts()})
	if res.Outcome != OutcomePreflightFailed || len(res.Checks) == 0 || res.Outcome.ExitCode() != 1 {
		t.Fatalf("%+v", res)
	}
	e.f.Mach.Version = saved
	o := e.userOpts()
	o.Location = filepath.Join(e.dirs.Windows, "Unravis")
	res = e.r.Perform(context.Background(), WorkRequest{Op: OpInstall, Options: o})
	if res.Outcome == OutcomeDone || res.Outcome.ExitCode() == 0 {
		t.Fatalf("%+v", res)
	}
	if res = e.r.Perform(context.Background(), WorkRequest{Op: OpUninstall}); res.Outcome != OutcomeInvalid {
		t.Fatalf("uninstall with nothing installed: %+v", res)
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("changed:\n%s", d)
	}
}

func TestPerformTurnsASecondInstallIntoARepair(t *testing.T) {
	e := newEnv(t, "1.0.0")
	if res := e.r.Perform(context.Background(), WorkRequest{Op: OpInstall, Options: e.userOpts()}); res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	res := e.r.Perform(context.Background(), WorkRequest{Op: OpInstall, Options: e.userOpts()})
	if res.Outcome != OutcomeDone || res.Op != OpRepair {
		t.Fatalf("%+v", res)
	}
}

func TestElevatorErrorIsNotACancel(t *testing.T) {
	res := RunWorker(context.Background(), &pipeElevator{err: errors.New("pipe busy")}, WorkRequest{Op: OpInstall}, nil, nil)
	if res.Outcome != OutcomeRolledBack || !strings.Contains(res.Message, "nothing was changed") {
		t.Fatalf("%+v", res)
	}
}

// The worker uses the person's folders only when they are the usual folders of the profile Windows names for the window's account.
func TestWorkerPrepareChecksWhoseFoldersTheyAre(t *testing.T) {
	e := newEnv(t, "1.0.0")
	prep := WorkerPrepare("S-1-5-21-2000", e.dirs.UserProfile)
	u := e.dirs.User()
	r := *e.r
	r.Dirs = r.Dirs.WithUser(UserDirs{})
	req := WorkRequest{Op: OpInstall, User: &u}
	if err := prep(&req, &r); err != nil {
		t.Fatal(err)
	}
	if r.UserSID != "S-1-5-21-2000" || r.Dirs.LocalAppData != e.dirs.LocalAppData || req.User != nil {
		t.Fatalf("%+v %+v", r.Dirs, req)
	}
	other := u
	other.UserProfile = filepath.Join(e.root, "Users", "Admin")
	other.LocalAppData = filepath.Join(other.UserProfile, "AppData", "Local")
	other.AppData = filepath.Join(other.UserProfile, "AppData", "Roaming")
	other.Programs = filepath.Join(other.AppData, "Microsoft", "Windows", "Start Menu", "Programs")
	other.Startup = filepath.Join(other.Programs, "Startup")
	bad := u
	bad.Startup = filepath.Join(e.root, "Windows", "System32")
	for name, req := range map[string]WorkRequest{
		"no folders":       {Op: OpInstall},
		"another profile":  {Op: OpInstall, User: &other},
		"not a usual path": {Op: OpInstall, User: &bad},
	} {
		if err := prep(&req, &r); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Through the pipe: a refused request changes nothing and says why.
	before := e.snapshot()
	el := &pipeElevator{r: e.r, prepare: prep}
	res := RunWorker(context.Background(), el, WorkRequest{Op: OpInstall, Options: e.machineOpts(), User: &other}, nil, nil)
	if res.Outcome != OutcomeInvalid || !strings.Contains(res.Message, "profile") || e.snapshot() != before {
		t.Fatalf("%+v", res)
	}
}
