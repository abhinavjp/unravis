package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
)

// These pin the steps run() is made of. They use temporary folders and an in-memory demo; nothing here touches the real data folders.

func TestLaunchOutcome(t *testing.T) {
	for _, c := range []struct {
		opened, printed bool
		want            string
	}{
		{true, false, "browser_opened"},
		{true, true, "browser_opened"},
		{false, true, "link_printed"},
		{false, false, "none"},
	} {
		if got := launchOutcome(c.opened, c.printed); got != c.want {
			t.Errorf("launchOutcome(%v,%v) = %q, want %q", c.opened, c.printed, got, c.want)
		}
	}
}

func TestNewHTTPServerKeepsItsLimits(t *testing.T) {
	h := http.NewServeMux()
	s := newHTTPServer(h)
	if s.Handler != h || s.ReadTimeout != httpReadTimeout || s.WriteTimeout != httpWriteTimeout ||
		s.ReadHeaderTimeout != 5*time.Second || s.IdleTimeout != 60*time.Second || s.MaxHeaderBytes != 16<<10 {
		t.Fatalf("server limits changed: %+v", s)
	}
}

func listenOnce(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestWriteSessionFileWritesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	l := listenOnce(t)
	token := strings.Repeat("t", 40)
	remove := writeSessionFile(getenv, l, token)
	path := filepath.Join(dir, mcp.SessionFileName)
	port, gotToken, err := mcp.ReadSessionFile(path)
	if err != nil || port != l.Addr().(*net.TCPAddr).Port || gotToken != token {
		t.Fatalf("session file: port %d token ok %v err %v", port, gotToken == token, err)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file still there: %v", err)
	}
}

func TestWriteSessionFileFailureGivesANoOp(t *testing.T) {
	// The config "folder" is a file, so the session file cannot be made.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return filepath.Join(blocker, "sub")
		}
		return ""
	}
	remove := writeSessionFile(getenv, listenOnce(t), strings.Repeat("t", 40))
	remove() // must not panic or touch anything
}

func TestAwaitShutdownWhenTheServerStopsItself(t *testing.T) {
	done := make(chan error, 1)
	done <- http.ErrServerClosed
	if err := awaitShutdown(context.Background(), done, func() { t.Error("removeSession called") }, &fakeShutdown{}); err != nil {
		t.Fatalf("closed server: %v", err)
	}
	boom := errors.New("serve failed")
	done <- boom
	if err := awaitShutdown(context.Background(), done, func() {}, &fakeShutdown{}); !errors.Is(err, boom) {
		t.Fatalf("serve error: %v", err)
	}
}

func TestAwaitShutdownOnSignalRemovesTheSessionThenWaitsForServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Serve returns only once Shutdown has been called, as http.Server does.
	done := make(chan error, 1)
	removed := false
	srv := &serveStopper{done: done, onShutdown: func() { _ = removed }}
	if err := awaitShutdown(ctx, done, func() { removed = true }, srv); err != nil || !removed || !srv.shutdown {
		t.Fatalf("err %v removed %v shutdown %v", err, removed, srv.shutdown)
	}
	// A failed shutdown is reported and the server is closed hard.
	failing := &fakeShutdown{err: errors.New("busy")}
	if err := awaitShutdown(ctx, make(chan error), func() {}, failing); err == nil || !failing.closed {
		t.Fatalf("failed shutdown: err %v closed %v", err, failing.closed)
	}
}

// serveStopper makes the serve channel report a closed server once Shutdown is called.
type serveStopper struct {
	done       chan error
	onShutdown func()
	shutdown   bool
}

func (s *serveStopper) Shutdown(context.Context) error {
	s.shutdown = true
	s.onShutdown()
	s.done <- http.ErrServerClosed
	return nil
}

func (s *serveStopper) Close() error { return nil }

func TestWireDataInDemoModeUsesMemoryOnly(t *testing.T) {
	getenv := func(string) string { return "" }
	cfg, _, err := demoEnv(true, getenv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w, err := wireData(runFlags{demo: true}, cfg, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if w.repo == nil || w.closeCases == nil || w.closeRepo == nil || len(w.options) == 0 {
		t.Fatalf("demo wiring incomplete: %+v", w)
	}
	w.closeCases()
	w.closeRepo()
}

func TestWindowsFileJoinsPartsUnderTheWindowsFolder(t *testing.T) {
	t.Setenv("SystemRoot", `C:\X`)
	if got, want := windowsFile("System32", "a.exe"), filepath.Join(`C:\X`, "System32", "a.exe"); got != want {
		t.Fatalf("windowsFile = %q, want %q", got, want)
	}
	if got, want := system32File("a.exe"), filepath.Join(`C:\X`, "System32", "a.exe"); got != want {
		t.Fatalf("system32File = %q, want %q", got, want)
	}
	t.Setenv("SystemRoot", "")
	if got, want := system32File("a.exe"), filepath.Join(`C:\Windows`, "System32", "a.exe"); got != want {
		t.Fatalf("fallback system32File = %q, want %q", got, want)
	}
}
