package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

const testToken = "pipe-test-token-0123456789abcdef0123456789"

// swapPipeSeams installs fake pipes and returns them; tcp reports whether the TCP listener was asked for.
func swapPipeSeams(t *testing.T, onServing func(cancel context.CancelFunc)) (ui, ag *memListener, tcpAsked *bool) {
	t.Helper()
	ui, ag = newMem(), newMem()
	asked := false
	oldTCP, oldPipes, oldAfter, oldOS := listenTCP, openPipes, afterServing, hostOS
	listenTCP = func(port int) (net.Listener, error) { asked = true; return oldTCP(port) }
	openPipes = func() (*pipesrv.Pipes, error) {
		return &pipesrv.Pipes{UI: ui, AG: ag, UIName: `\\.\pipe\unravis.ui.test`, AGName: `\\.\pipe\unravis.ag.test`}, nil
	}
	afterServing = onServing
	hostOS = "windows"
	t.Cleanup(func() { listenTCP, openPipes, afterServing, hostOS = oldTCP, oldPipes, oldAfter, oldOS })
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
