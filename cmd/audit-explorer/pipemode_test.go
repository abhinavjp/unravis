package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/instance"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/pipesrv"
)

type memListener struct {
	ch   chan net.Conn
	done chan struct{}
}

func newMem() *memListener { return &memListener{ch: make(chan net.Conn), done: make(chan struct{})} }

type memAddr struct{}

func (memAddr) Network() string { return "pipe" }
func (memAddr) String() string  { return "mem" }

func (m *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-m.ch:
		return c, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}
func (m *memListener) Close() error {
	select {
	case <-m.done:
	default:
		close(m.done)
	}
	return nil
}
func (m *memListener) Addr() net.Addr { return memAddr{} }

// get calls path through the listener with an optional session token. It reports failures as an error, never through t, because it
// runs on the goroutine of run() (afterServing), where t.Fatal would end the wrong goroutine.
func (m *memListener) get(path, token string) (int, string, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		cl, sv := net.Pipe()
		m.ch <- sv
		return cl, nil
	}}}
	req, err := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
	if err != nil {
		return 0, "", err
	}
	if token != "" {
		req.Header.Set("X-Audit-Token", token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

const (
	testUIPipe = `\\.\pipe\unravis.ui.0123456789abcdef0123456789abcdef`
	testAGPipe = `\\.\pipe\unravis.ag.0123456789abcdef0123456789abcdef`
)

const testToken = "pipe-test-token-0123456789abcdef0123456789"

// swapPipeSeams installs fake pipes and returns them; tcp reports whether the TCP listener was asked for.
func swapPipeSeams(t *testing.T, onServing func(cancel context.CancelFunc)) (ui, ag *memListener, tcpAsked *bool) {
	t.Helper()
	ui, ag = newMem(), newMem()
	asked := false
	oldTCP, oldPipes, oldAfter, oldOS, oldStart := listenTCP, openPipes, afterServing, hostOS, selfStart
	selfStart = func() (time.Time, error) { return time.Now(), nil }
	listenTCP = func(port int) (net.Listener, error) { asked = true; return oldTCP(port) }
	openPipes = func() (*pipesrv.Pipes, error) {
		return &pipesrv.Pipes{UI: ui, AG: ag, UIName: testUIPipe, AGName: testAGPipe}, nil
	}
	afterServing = onServing
	hostOS = "windows"
	t.Cleanup(func() {
		listenTCP, openPipes, afterServing, hostOS, selfStart = oldTCP, oldPipes, oldAfter, oldOS, oldStart
	})
	return ui, ag, &asked
}

func sessionPath(t *testing.T, getenv func(string) string) string {
	t.Helper()
	p, err := mcp.SessionFilePath(getenv)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPipeOnlyNoListenerNoSessionFile is spec 02 section 4: --pipe-only opens no TCP listener and writes no session file, yet serves
// the dashboard over the ui pipe and keeps the GUI routes off the ag pipe.
func TestPipeOnlyNoListenerNoSessionFile(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	path := sessionPath(t, getenv)
	var uiCode, uiNoTokenCode, agCode, agInfoCode int
	var callErr error
	var ui, ag *memListener
	var tcp *bool
	ui, ag, tcp = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("session file exists while serving: %v", err)
		}
		var err1, err2, err3, err4 error
		uiCode, _, err1 = ui.get("/api/info", testToken)
		uiNoTokenCode, _, err2 = ui.get("/api/info", "")
		agCode, _, err3 = ag.get("/api/cases", testToken)
		agInfoCode, _, err4 = ag.get("/api/info", testToken)
		callErr = errors.Join(err1, err2, err3, err4)
	})
	done := make(chan error, 1)
	go func() { done <- run([]string{"--pipe-only", "--demo"}, getenv) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not stop")
	}
	if *tcp {
		t.Fatal("--pipe-only asked for a TCP listener")
	}
	if callErr != nil {
		t.Fatalf("pipe call: %v", callErr)
	}
	if uiCode != 200 {
		t.Errorf("ui pipe GET /api/info with the token = %d, want 200", uiCode)
	}
	if uiNoTokenCode != 401 && uiNoTokenCode != 403 {
		t.Errorf("ui pipe GET /api/info without a token = %d, want 401 or 403 (the token is still checked)", uiNoTokenCode)
	}
	if agCode != 404 {
		t.Errorf("ag pipe GET /api/cases = %d, want 404", agCode)
	}
	if agInfoCode != 200 {
		t.Errorf("ag pipe GET /api/info with the token = %d, want 200 (bridge read route)", agInfoCode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file after exit: %v", err)
	}
}

func TestPipeServerLoopbackKeepsBrowserModeListener(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir})
	path := sessionPath(t, getenv)
	var sessionSeen bool
	_, _, tcp := swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		_, err := os.Stat(path)
		sessionSeen = err == nil
	})
	if err := run([]string{"--pipe-server", "--loopback", "--demo"}, getenv); err != nil {
		t.Fatal(err)
	}
	if !*tcp || !sessionSeen {
		t.Fatalf("loopback asked=%v session file seen=%v, want both", *tcp, sessionSeen)
	}
}

func TestPipeFlagRules(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "windows"
	for _, c := range []struct {
		args []string
		bad  string // substring of the error; empty means valid
	}{
		{[]string{"--pipe-only"}, ""},
		{[]string{"--pipe-server"}, ""},
		{[]string{"--pipe-server", "--loopback", "--port", "0"}, ""},
		{[]string{"--pipe-only", "--loopback"}, "never opens a TCP port"},
		{[]string{"--loopback"}, "only valid with --pipe-server"},
		{[]string{"--pipe-only", "--mcp"}, "--mcp"},
		{[]string{"--pipe-only", "--background"}, "--background"},
		{[]string{"--pipe-only", "--port", "8080"}, "needs --loopback"},
		{[]string{"--pipe-only", "--port", "0"}, "needs --loopback"},
		{[]string{"--pipe-only", "--no-browser"}, "--no-browser"},
		{[]string{"--pipe-server", "--no-browser"}, "--no-browser"},
		{[]string{"--pipe-only", "--quit"}, "--quit"},
		{[]string{"--pipe-server", "--check-update"}, "--check-update"},
	} {
		_, err := parseFlags(c.args)
		switch {
		case c.bad == "" && err != nil:
			t.Errorf("%v: %v", c.args, err)
		case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
			t.Errorf("%v: err = %v, want %q", c.args, err, c.bad)
		}
	}
	hostOS = "linux"
	if _, err := parseFlags([]string{"--pipe-only"}); err == nil {
		t.Error("pipe mode must be refused off Windows")
	}
}

func TestListenerPortNilIsZero(t *testing.T) {
	if listenerPort(nil) != 0 {
		t.Fatal("nil listener must report port 0")
	}
}

// TestPipeRejectsRemoteFlag: pipe mode has no flag that could expose it off this PC. Any remote, host or bind option is an unknown
// flag and stops the start. (The PIPE_REJECT_REMOTE_CLIENTS bit itself is tested in internal/pipesec on Windows.)
func TestPipeRejectsRemoteFlag(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	hostOS = "windows"
	for _, a := range []string{"--remote", "--accept-remote", "--host=0.0.0.0", "--bind=0.0.0.0", "--listen=0.0.0.0:80"} {
		for _, mode := range []string{"--pipe-only", "--pipe-server"} {
			if _, err := parseFlags([]string{mode, a}); err == nil {
				t.Errorf("%s %s was accepted", mode, a)
			}
		}
	}
}

// TestServeOverPipeSameJSONAsLoopback: with --pipe-server --loopback the ui pipe and the TCP listener serve one handler, so the same
// read routes return the same JSON (demo data).
func TestServeOverPipeSameJSONAsLoopback(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	var tcpL net.Listener
	var ui *memListener
	type pair struct {
		pipe, tcp string
		err       error
	}
	results := map[string]pair{}
	ui, _, _ = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		for _, route := range []string{"/api/info", "/api/sources"} {
			var r pair
			var pc, tc int
			pc, r.pipe, r.err = ui.get(route, testToken)
			if r.err == nil {
				req, _ := http.NewRequest("GET", "http://"+tcpL.Addr().String()+route, nil)
				req.Header.Set("X-Audit-Token", testToken)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					r.err = err
				} else {
					b, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					tc, r.tcp = resp.StatusCode, string(b)
				}
			}
			if r.err == nil && (pc != 200 || tc != 200) {
				r.err = errors.New(route + ": status pipe/tcp not 200")
			}
			results[route] = r
		}
	})
	oldTCP := listenTCP
	listenTCP = func(port int) (net.Listener, error) {
		l, err := oldTCP(port)
		tcpL = l
		return l, err
	}
	t.Cleanup(func() { listenTCP = oldTCP })
	if err := run([]string{"--pipe-server", "--loopback", "--demo"}, getenv); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("routes checked: %d", len(results))
	}
	for route, r := range results {
		if r.err != nil {
			t.Errorf("%s: %v", route, r.err)
		} else if r.pipe != r.tcp || r.pipe == "" {
			t.Errorf("%s differs:\npipe: %.200s\ntcp:  %.200s", route, r.pipe, r.tcp)
		}
	}
}

// pipeGateWith makes run use a fake singleton gate in pipe mode.
func pipeGateWith(t *testing.T, f *fakeGate, live func() bool) {
	t.Helper()
	old := pipeGateDepsFn
	pipeGateDepsFn = func(string, bool) gateDeps {
		d := f.deps()
		d.liveLocator = live
		return d
	}
	t.Cleanup(func() { pipeGateDepsFn = old })
}

// TestPipeModeSecondCopyExits12: a pipe-mode copy that is not the owner never signals Open, waits for the live locator and exits 12
// without a message box.
func TestPipeModeSecondCopyExits12(t *testing.T) {
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
	f := &fakeGate{first: false}
	polls := 0
	pipeGateWith(t, f, func() bool { polls++; return polls >= 3 }) // the owner's locator goes live on the third look
	opened := false
	swapPipeSeams(t, func(cancel context.CancelFunc) { opened = true; cancel() })
	err := run([]string{"--pipe-only", "--demo"}, getenv)
	var ee *exitCodeError
	if !errors.As(err, &ee) || ee.code != 12 {
		t.Fatalf("err = %v, want exit code 12", err)
	}
	if opened {
		t.Error("the second copy started serving")
	}
	if f.signalCalls != 0 {
		t.Errorf("signalled Open %d times; pipe mode has no tab to open", f.signalCalls)
	}
	if f.sleepCalls != 2 {
		t.Errorf("sleeps = %d, want 2 (polled 250 ms apart until live)", f.sleepCalls)
	}
	// The exit goes through reportFatal: code 12, no message box.
	var code int
	shown := false
	oldExit, oldShow, oldUnusable := exitProcess, showMessage, stderrUnusable
	exitProcess, showMessage, stderrUnusable = func(c int) { code = c }, func(string, string) { shown = true }, func() bool { return true }
	t.Cleanup(func() { exitProcess, showMessage, stderrUnusable = oldExit, oldShow, oldUnusable })
	reportFatal(err)
	if code != 12 || shown {
		t.Errorf("exit code %d, message box shown %v; want 12 and none", code, shown)
	}
}

// TestPipeModeSecondCopyNoLocator: no live locator within 40 x 250 ms (the owner is browser mode, or still starting) is a quiet exit 13:
// no message box, because a pipe-mode copy runs detached with no window.
func TestPipeModeSecondCopyNoLocator(t *testing.T) {
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
	f := &fakeGate{first: false}
	pipeGateWith(t, f, func() bool { return false })
	swapPipeSeams(t, func(cancel context.CancelFunc) { cancel() })
	err := run([]string{"--pipe-only", "--demo"}, getenv)
	assertQuietExit(t, err, exitNoOwner)
	if f.sleepCalls != 39 {
		t.Errorf("sleeps = %d, want 39 (40 looks)", f.sleepCalls)
	}
}

// TestPipeModeElevatedExits11: an elevated pipe-mode start is refused with exit 11 and no message box, before any name is touched.
func TestPipeModeElevatedExits11(t *testing.T) {
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
	f := &fakeGate{first: true, isElevated: true}
	pipeGateWith(t, f, func() bool { return false })
	swapPipeSeams(t, func(cancel context.CancelFunc) { cancel() })
	err := run([]string{"--pipe-only", "--demo"}, getenv)
	assertQuietExit(t, err, exitElevated)
	if f.acquireCalls != 0 {
		t.Errorf("acquired the mutex %d times while elevated", f.acquireCalls)
	}
}

// assertQuietExit checks that err ends the process with code and, through reportFatal, shows no message box even when there is no console.
func assertQuietExit(t *testing.T, err error, want int) {
	t.Helper()
	var ee *exitCodeError
	if !errors.As(err, &ee) || ee.code != want {
		t.Fatalf("err = %v, want exit code %d", err, want)
	}
	var code int
	shown := false
	oldExit, oldShow, oldUnusable := exitProcess, showMessage, stderrUnusable
	exitProcess, showMessage, stderrUnusable = func(c int) { code = c }, func(string, string) { shown = true }, func() bool { return true }
	t.Cleanup(func() { exitProcess, showMessage, stderrUnusable = oldExit, oldShow, oldUnusable })
	reportFatal(err)
	if code != want || shown {
		t.Errorf("exit code %d, message box shown %v; want %d and none", code, shown, want)
	}
}

// TestPipeModeOwnerListensForInstanceEvents: the owner registers for Open and Quit on the per-user instance names, so --quit and a
// browser-mode second start reach it. Quit runs the normal shutdown; Open is harmless.
func TestPipeModeOwnerListensForInstanceEvents(t *testing.T) {
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": t.TempDir(), "AUDIT_SESSION_TOKEN": testToken})
	f := &fakeGate{first: true}
	pipeGateWith(t, f, func() bool { return false })
	var got instance.Names
	stopped := false
	old := instanceListen
	instanceListen = func(ctx context.Context, n instance.Names, onOpen, onQuit func()) (func(), error) {
		got = n
		onOpen() // must not panic or block
		go onQuit()
		return func() { stopped = true }, nil
	}
	t.Cleanup(func() { instanceListen = old })
	// Without a Quit the owner would serve forever: the only thing that ends this run is the Quit the fake listener sends.
	swapPipeSeams(t, func(context.CancelFunc) {})
	done := make(chan error, 1)
	go func() { done <- run([]string{"--pipe-only", "--demo"}, getenv) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Quit did not stop the pipe-mode owner")
	}
	if want, _ := instance.NewNames(instance.DemoApp, "S-1-5-21-1-2-3-1001"); got != want {
		t.Errorf("listened on %+v, want %+v", got, want)
	}
	if !stopped {
		t.Error("the instance listener was not stopped on exit")
	}
}

// TestPipeModeWritesAndRemovesLocator: the owner publishes backend.json (no token) while serving, replaces a stale one, and removes its
// own on a clean stop. The legacy session file is not written in pipe-only mode.
func TestPipeModeWritesAndRemovesLocator(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "AUDIT_SESSION_TOKEN": testToken})
	path := locator.FilePath(filepath.Join(dir, "run"), true)
	stale := locator.Record{Version: locator.Schema, PID: 999999, StartTime: time.Unix(1, 0).UTC(), Exe: "old.exe", UI: testUIPipe, AG: testAGPipe}
	if err := locator.Write(path, stale, locator.Options{}); err != nil {
		t.Fatal(err)
	}
	var seen locator.Record
	var seenErr error
	var raw []byte
	swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		seen, seenErr = locator.Read(path)
		raw, _ = os.ReadFile(path) //nolint:gosec // test path
	})
	if err := run([]string{"--pipe-only", "--demo"}, getenv); err != nil {
		t.Fatal(err)
	}
	if seenErr != nil || seen.PID != os.Getpid() || seen.UI != testUIPipe || seen.AG != testAGPipe || seen.Exe == "" {
		t.Fatalf("locator while serving: %+v, %v", seen, seenErr)
	}
	if strings.Contains(strings.ToLower(string(raw)), testToken) || strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Errorf("locator carries a token: %s", raw)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("locator after a clean stop: %v", err)
	}
}

// TestPipeModeLocatorFailureIsFatal: an owner nobody can find must not keep running.
func TestPipeModeLocatorFailureIsFatal(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, "UNRAVIS_RUN_DIR": filepath.Join(blocker, "run"), "AUDIT_SESSION_TOKEN": testToken})
	swapPipeSeams(t, func(cancel context.CancelFunc) { cancel() })
	if err := run([]string{"--pipe-only", "--demo"}, getenv); err == nil {
		t.Fatal("run succeeded without a locator")
	}
}
