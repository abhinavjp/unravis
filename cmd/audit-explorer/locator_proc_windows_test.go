//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/locator"
)

// buildExe builds the real unravis.exe once per test into dir.
func buildExe(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "unravis.exe")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return exe
}

// sidecar is one started copy of the exe. done is closed, with err set, when the process has exited and been reaped.
type sidecar struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startSidecar starts one copy. Its cleanup kills the process and waits for the exit, so the per-user mutex of this copy is gone before
// the next test starts (all tests share the per-user demo mutex).
func startSidecar(t *testing.T, exe, data string) *sidecar {
	t.Helper()
	cmd := exec.Command(exe, "--pipe-server", "--demo")
	cmd.Env = append(os.Environ(), "AUDIT_CONFIG_DIR="+data)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &sidecar{cmd: cmd, done: make(chan struct{})}
	go func() { s.err = cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(15 * time.Second):
			t.Errorf("pid %d did not exit after Kill", cmd.Process.Pid)
		}
	})
	return s
}

func waitLive(t *testing.T, path string) locator.Record {
	t.Helper()
	r, ok := locator.Wait(path, locator.SystemProber(), 120, 250*time.Millisecond, time.Sleep)
	if !ok {
		t.Fatalf("no live locator at %s", path)
	}
	return r
}

// TestTenConcurrentStartsOneOwner runs 10 copies of the real exe in pipe mode at once. Exactly one stays running, the other nine
// exit 12 (attached), and the locator names the one owner.
func TestTenConcurrentStartsOneOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the exe")
	}
	dir := t.TempDir()
	exe := buildExe(t, dir)
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	var procs []*sidecar
	for range 10 {
		procs = append(procs, startSidecar(t, exe, data))
	}
	rec := waitLive(t, locator.FilePath(filepath.Join(data, "run"), true))
	running, attached := 0, 0
	deadline := time.After(30 * time.Second)
	for _, p := range procs {
		select {
		case <-p.done:
			if ee, ok := p.err.(*exec.ExitError); ok && ee.ExitCode() == exitAttached {
				attached++
			} else {
				t.Errorf("pid %d exited with %v, want exit %d", p.cmd.Process.Pid, p.err, exitAttached)
			}
		case <-time.After(2 * time.Second):
			running++
			if p.cmd.Process.Pid != rec.PID {
				t.Errorf("running pid %d is not the locator's owner %d", p.cmd.Process.Pid, rec.PID)
			}
		case <-deadline:
			t.Fatal("copies did not settle")
		}
	}
	if running != 1 || attached != 9 {
		t.Fatalf("running=%d attached=%d, want 1 and 9", running, attached)
	}
}

// TestStaleLocatorReplacedAfterKill kills the owner hard (as taskkill /f does), checks the old locator is stale, starts again and
// expects a new pid in a live locator.
func TestStaleLocatorReplacedAfterKill(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the exe")
	}
	dir := t.TempDir()
	exe := buildExe(t, dir)
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := locator.FilePath(filepath.Join(data, "run"), true)
	first := startSidecar(t, exe, data)
	r1 := waitLive(t, path)
	if r1.PID != first.cmd.Process.Pid {
		t.Fatalf("locator pid %d, started %d", r1.PID, first.cmd.Process.Pid)
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-first.done
	stale, err := locator.Read(path)
	if err != nil || stale.PID != r1.PID {
		t.Fatalf("the killed owner's file should still be there: %+v, %v", stale, err)
	}
	if locator.Alive(stale, locator.SystemProber()) {
		t.Fatal("the locator of a killed owner is reported alive")
	}
	second := startSidecar(t, exe, data)
	deadline := time.Now().Add(30 * time.Second)
	for {
		r2, err := locator.Read(path)
		if err == nil && r2.PID == second.cmd.Process.Pid && locator.Alive(r2, locator.SystemProber()) {
			if r2.PID == r1.PID {
				t.Fatal("pid did not change")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("locator never replaced: %+v, %v", r2, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
