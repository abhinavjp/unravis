package cmdtree

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
)

func fastApp(t *testing.T) {
	t.Helper()
	ow, os2, op := startWait, stopWait, appPoll
	startWait, stopWait, appPoll = 400*time.Millisecond, 400*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { startWait, stopWait, appPoll = ow, os2, op })
}

// down makes the owner not run: no locator file.
func (o *owner) down() { _ = os.Remove(locator.FilePath(o.runDir, false)) }

// up is what a started owner does: it writes its locator and answers.
func (o *owner) up() {
	o.set(func(o *owner) { o.alive = true })
	_ = locator.Write(locator.FilePath(o.runDir, false), o.rec, locator.Options{})
}

type starter struct {
	calls int
	do    func()
	pid   int
	exit  chan int
	err   *clicore.Error
}

func (s *starter) start(context.Context) (clicore.Child, *clicore.Error) {
	s.calls++
	if s.err != nil {
		return clicore.Child{}, s.err
	}
	if s.do != nil {
		s.do()
	}
	if s.exit == nil {
		s.exit = make(chan int, 1)
	}
	return clicore.Child{PID: s.pid, Exit: s.exit}, nil
}

func runStart(t *testing.T, o *owner, s *starter, args ...string) result {
	t.Helper()
	var st clicore.Starter
	if s != nil {
		st = s.start
	}
	return runWith2(t, o.attacher(), st, "", nil, args...)
}

func runWith2(t *testing.T, a clicore.Attacher, st clicore.Starter, stdin string, env map[string]string, args ...string) result {
	t.Helper()
	var out, errb strings.Builder
	code := Run(context.Background(), args, IO{In: strings.NewReader(stdin), Out: &out, Err: &errb,
		Getenv:        func(n string) string { return env[n] },
		Exe:           func() (string, error) { return "/opt/unravis/bin/unravis.exe", nil },
		Attach:        a.Attach,
		StartHeadless: st})
	return result{code, out.String(), errb.String()}
}

func TestAppStartAlreadyRunningStartsNothing(t *testing.T) {
	fastApp(t)
	o := newOwner(t, nil)
	s := &starter{pid: 4242}
	d := data(t, runStart(t, o, s, "app", "start", "--headless", "--json"))
	if d["started"] != false || d["alreadyRunning"] != true || d["pid"] != float64(4242) || d["owner"] != "app" || s.calls != 0 {
		t.Errorf("%v calls=%d", d, s.calls)
	}
	tb := runStart(t, o, s, "app", "start", "--headless")
	if tb.code != 0 || !strings.Contains(tb.out, "already running") || !strings.Contains(tb.out, "4242") {
		t.Errorf("table: %+v", tb)
	}
}

func TestAppStartStartsAndWaitsForHello(t *testing.T) {
	fastApp(t)
	for name, prep := range map[string]func(*owner){
		"no locator": func(o *owner) { o.down() },
		"stale":      func(o *owner) { o.set(func(o *owner) { o.alive = false }) },
	} {
		o := newOwner(t, nil)
		prep(o)
		s := &starter{pid: 4242, do: o.up}
		d := data(t, runStart(t, o, s, "app", "start", "--headless", "--json"))
		if d["started"] != true || d["alreadyRunning"] != false || d["pid"] != float64(4242) || s.calls != 1 {
			t.Errorf("%s: %v calls=%d", name, d, s.calls)
		}
	}
	// the owner answers a little later: the command polls
	o := newOwner(t, nil)
	o.down()
	s := &starter{pid: 4242, do: func() { go func() { time.Sleep(60 * time.Millisecond); o.up() }() }}
	if d := data(t, runStart(t, o, s, "app", "start", "--headless", "--json")); d["started"] != true {
		t.Errorf("late: %v", d)
	}
	// another start won: the owner that answers is not the child
	o = newOwner(t, nil)
	o.down()
	s = &starter{pid: 9999, do: o.up}
	if d := data(t, runStart(t, o, s, "app", "start", "--headless", "--json")); d["started"] != false || d["alreadyRunning"] != true || d["pid"] != float64(4242) {
		t.Errorf("another owner: %v", d)
	}
}

func TestAppStartMapsTheExitOfTheChild(t *testing.T) {
	fastApp(t)
	for _, c := range []struct {
		code int
		want string
		exit int
	}{{11, "ELEVATION_REFUSED", 4}, {12, "OWNER_BUSY", 3}, {13, "UPSTREAM_UNAVAILABLE", 3}, {0, "INTERNAL", 1}, {7, "INTERNAL", 1}} {
		o := newOwner(t, nil)
		o.down()
		s := &starter{pid: 1, exit: make(chan int, 1)}
		s.exit <- c.code
		r := runStart(t, o, s, "app", "start", "--headless", "--json")
		if r.code != c.exit || errCode(t, r) != c.want {
			t.Errorf("exit %d: %+v", c.code, r)
		}
	}
}

func TestAppStartGivesUpAndRefuses(t *testing.T) {
	fastApp(t)
	o := newOwner(t, nil)
	o.down()
	if r := runStart(t, o, &starter{pid: 1}, "app", "start", "--headless", "--json"); r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("never answers: %+v", r)
	}
	// an owner that cannot be trusted is not started over
	o = newOwner(t, nil)
	o.set(func(o *owner) { o.helloPID = 7 })
	s := &starter{pid: 1}
	if r := runStart(t, o, s, "app", "start", "--headless", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNTRUSTED" || s.calls != 0 {
		t.Errorf("untrusted: %+v calls=%d", r, s.calls)
	}
	// an owner that comes up and does not prove who it is stops the wait at once: it is not waited for until the time runs out
	o = newOwner(t, nil)
	o.down()
	if r := runStart(t, o, &starter{pid: 4242, do: func() { o.set(func(o *owner) { o.helloPID = 7 }); o.up() }}, "app", "start", "--headless", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNTRUSTED" {
		t.Errorf("untrusted after start: %+v", r)
	}
	// a start that cannot be made, and a build with no way to start
	o = newOwner(t, nil)
	o.down()
	if r := runStart(t, o, &starter{err: clicore.NewError(clicore.CodeEndpointUnavailable, "no app file", "")}, "app", "start", "--headless", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("starter error: %+v", r)
	}
	if r := runStart(t, o, nil, "app", "start", "--headless", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("no starter: %+v", r)
	}
	// usage
	s = &starter{pid: 1, do: o.up}
	for name, args := range map[string][]string{
		"no headless": {"app", "start", "--json"},
		"extra":       {"app", "start", "--headless", "now", "--json"},
		"request":     {"app", "start", "--headless", "--request", "-", "--json"},
		"profile":     {"app", "start", "--headless", "--profile", "cli-x", "--json"},
	} {
		if r := runStart(t, o, s, args...); r.code != 2 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if s.calls != 0 {
		t.Error("a refused command started the app")
	}
	if r := runWith2(t, o.attacher(), s.start, "", map[string]string{"UNRAVIS_PROFILE": "cli-x"}, "app", "start", "--headless", "--json"); r.code != 2 || s.calls != 0 {
		t.Errorf("profile variable: %+v", r)
	}
	if r := runWith2(t, o.attacher(), s.start, `{"x":1}`, nil, "app", "start", "--headless", "--request", "-", "--json"); r.code != 2 || s.calls != 0 {
		t.Errorf("request: %+v", r)
	}
}

func TestAppStopStopsAHeadlessOwner(t *testing.T) {
	fastApp(t)
	o := newOwner(t, nil)
	o.set(func(o *owner) {
		o.ownerName = "headless"
		o.onStop = func() { o.set(func(o *owner) { o.alive = false }) }
	})
	d := data(t, o.run("app", "stop", "--json"))
	if d["stopped"] != true || d["alreadyStopped"] != false || d["pid"] != float64(4242) {
		t.Errorf("%v", d)
	}
	// it is gone: the second stop is already done, and exits 0
	d = data(t, o.run("app", "stop", "--json"))
	if d["stopped"] != false || d["alreadyStopped"] != true {
		t.Errorf("second: %v", d)
	}
	o.down()
	if d := data(t, o.run("app", "stop", "--json")); d["alreadyStopped"] != true {
		t.Errorf("no locator: %v", d)
	}
	// a locked app can still be stopped
	o = newOwner(t, nil)
	o.set(func(o *owner) {
		o.ownerName, o.state = "headless", "locked"
		o.onStop = func() { o.set(func(o *owner) { o.alive = false }) }
	})
	if d := data(t, o.run("app", "stop", "--json")); d["stopped"] != true {
		t.Errorf("locked: %v", d)
	}
	if tb := newOwnerHeadless(t).run("app", "stop"); tb.code != 0 || !strings.Contains(tb.out, "stopped") {
		t.Errorf("table: %+v", tb)
	}
}

func newOwnerHeadless(t *testing.T) *owner {
	o := newOwner(t, nil)
	o.set(func(o *owner) {
		o.ownerName = "headless"
		o.onStop = func() { o.set(func(o *owner) { o.alive = false }) }
	})
	return o
}

func TestAppStopLeavesTheShellOwnerAlone(t *testing.T) {
	fastApp(t)
	for _, name := range []string{"app", "shell"} {
		o := newOwner(t, nil)
		stopped := false
		o.set(func(o *owner) { o.ownerName, o.onStop = name, func() { stopped = true } })
		r := o.run("app", "stop", "--json")
		if r.code != 4 || errCode(t, r) != "PERMISSION_DENIED" || !strings.Contains(r.err, "tray") {
			t.Errorf("%s: %+v", name, r)
		}
		time.Sleep(250 * time.Millisecond)
		if stopped || len(o.opsSeen()) != 0 {
			t.Errorf("%s: the stop was sent (ops %v) or ran", name, o.opsSeen())
		}
	}
}

func TestAppStopWaitsAndGivesUp(t *testing.T) {
	fastApp(t)
	o := newOwner(t, nil)
	o.set(func(o *owner) { o.ownerName, o.onStop = "headless", func() {} }) // it says stopping and does not go
	if r := o.run("app", "stop", "--json"); r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("never stops: %+v", r)
	}
	// it goes a little later: the command waits for it
	o = newOwner(t, nil)
	o.set(func(o *owner) {
		o.ownerName = "headless"
		o.onStop = func() { time.AfterFunc(120*time.Millisecond, func() { o.set(func(o *owner) { o.alive = false }) }) }
	})
	if d := data(t, o.run("app", "stop", "--json")); d["stopped"] != true {
		t.Errorf("late: %v", d)
	}
	// a new owner under the same name has another pid: the old one is gone
	o = newOwner(t, nil)
	o.set(func(o *owner) {
		o.ownerName, o.onStop = "headless", func() { o.set(func(o *owner) { o.helloPID = 7 }) }
	})
	// (its hello then fails to match the locator, which is an untrusted answer, not a stop: the command says so)
	if r := o.run("app", "stop", "--json"); r.code == 0 {
		t.Errorf("an owner that no longer proves itself is not a stopped one: %+v", r)
	}
	for name, args := range map[string][]string{
		"extra":   {"app", "stop", "now", "--json"},
		"profile": {"app", "stop", "--profile", "cli-x", "--json"},
	} {
		if r := newOwnerHeadless(t).run(args...); r.code != 2 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := runWith2(t, newOwnerHeadless(t).attacher(), nil, `{"x":1}`, nil, "app", "stop", "--request", "-", "--json"); r.code != 2 {
		t.Errorf("request: %+v", r)
	}
}

// Stopping an owner that is not headless is refused by the owner too, whatever this command line checks first.
func TestOwnerRefusesStopAndStartOps(t *testing.T) {
	o := newOwner(t, nil)
	for _, op := range []string{"app.start", "app.stop", "doctor"} {
		a := o.attacher()
		sess, e := a.Attach(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		id, _ := newCallID()
		_, e = sess.Do(context.Background(), clicore.Call{Op: op, RequestID: "req_0123456789abcdef", CallID: id, Deadline: time.Second})
		if e == nil || e.Code != clicore.CodePermissionDenied {
			t.Errorf("%s: %v", op, e)
		}
	}
}
