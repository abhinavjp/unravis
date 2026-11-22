package main

import (
	"context"
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
func (m *memListener) get(t *testing.T, path string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		cl, sv := net.Pipe()
		m.ch <- sv
		return cl, nil
	}}}
	req, _ := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

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
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir})
	path := sessionPath(t, getenv)
	var uiCode, agCode int
	var ui, ag *memListener
	var tcp *bool
	ui, ag, tcp = swapPipeSeams(t, func(cancel context.CancelFunc) {
		defer cancel()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("session file exists while serving: %v", err)
		}
		uiCode, _ = ui.get(t, "/api/info")
		agCode, _ = ag.get(t, "/api/info")
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
	if uiCode != 200 {
		t.Errorf("ui pipe GET /api/info = %d, want 200 (the pipe stands in for the token)", uiCode)
	}
	if agCode != 404 {
		t.Errorf("ag pipe GET /api/info = %d, want 404", agCode)
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
