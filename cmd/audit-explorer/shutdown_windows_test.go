//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/pipesec"
	"api-audit-explorer/internal/pipesrv"
)

// These tests run the real unravis.exe as --pipe-only with the demo's real case databases and drive it over its real ui pipe. The pipe
// peer check only lets an Unravis image from the exe's own folder in, so the client is a copy of this test binary named
// unravis-desktop.exe (an allowed name) placed beside the exe; it runs TestSidecarPipeClientHelper below.

const (
	helperEnvMode   = "UNRAVIS_TEST_PIPE_HELPER"
	helperEnvPipe   = "UNRAVIS_TEST_PIPE_NAME"
	helperEnvExe    = "UNRAVIS_TEST_SERVER_EXE"
	helperEnvCaseID = "UNRAVIS_TEST_CASE_ID"
)

// TestSidecarPipeClientHelper is not a test: when helperEnvMode is set, this binary acts as the shell. "load" writes to-do lines to the
// case from 4 goroutines and prints "ACKED <n>" as writes are saved; "shutdown" does the same and, after 20 saved writes, calls the
// shutdown RPC and prints "SHUTDOWN <status>". It ends when the server's pipe is gone.
func TestSidecarPipeClientHelper(t *testing.T) {
	mode := os.Getenv(helperEnvMode)
	if mode == "" {
		t.Skip("helper for the sidecar tests")
	}
	name, serverExe, caseID := os.Getenv(helperEnvPipe), os.Getenv(helperEnvExe), os.Getenv(helperEnvCaseID)
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		t.Fatal(err)
	}
	sp := pipesec.ServerPolicy{ExpectedImage: serverExe, InstallDir: filepath.Dir(serverExe), ExpectedSession: session}
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		c, _, err := pipesec.DialVerified(ctx, name, sp)
		return c, err
	}}}
	call := func(method, path string, body []byte) (int, error) {
		req, err := http.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-Audit-Token", testToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	var acked atomic.Int64
	var down atomic.Bool
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !down.Load() {
				body, _ := json.Marshal(map[string]any{"list": casestore.ListToDo, "text": fmt.Sprintf("line %d %s", w, time.Now().Format(time.RFC3339Nano))})
				code, err := call("POST", "/api/cases/"+caseID+"/checklist", body)
				switch {
				case err != nil:
					down.Store(true)
				case code == http.StatusOK:
					if n := acked.Add(1); n%5 == 0 {
						fmt.Println("ACKED", n)
					}
				}
			}
		}()
	}
	for acked.Load() < 20 && !down.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if mode == "shutdown" {
		code, err := call("POST", pipesrv.ShutdownPath, nil)
		fmt.Println("SHUTDOWN", code, err)
	}
	wg.Wait()
	fmt.Println("FINAL", acked.Load())
}

type sidecar struct {
	cmd      *exec.Cmd
	getenv   func(string) string
	dataDir  string
	exePath  string
	pipeName string
	exited   chan error
}

// startSidecar builds unravis.exe (once per test), starts it as --pipe-only --demo with real demo cases and waits for its ui pipe.
func startSidecar(t *testing.T) *sidecar {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the exe")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "unravis.exe")
	if out, err := exec.Command("go", "build", "-o", exePath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"AUDIT_CONFIG_DIR": data, "AUDIT_SESSION_TOKEN": testToken,
		envDemoCases: "1", envDemoNow: demoTestNow.Format(time.RFC3339),
	}
	before := uiPipes()
	cmd := exec.Command(exePath, "--pipe-only", "--demo")
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &sidecar{cmd: cmd, getenv: envOf(env), dataDir: data, exePath: exePath, exited: make(chan error, 1)}
	go func() { s.exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(30 * time.Second)
	for s.pipeName == "" {
		select {
		case err := <-s.exited:
			t.Fatalf("exe exited early: %v", err)
		default:
		}
		for n := range uiPipes() {
			if !before[n] {
				s.pipeName = `\\.\pipe\` + n
			}
		}
		if s.pipeName == "" {
			if time.Now().After(deadline) {
				t.Fatal("the ui pipe did not appear")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return s
}

var uiPipeName = regexp.MustCompile(`^unravis\.ui\.[0-9a-f]{32}$`)

func uiPipes() map[string]bool {
	out := map[string]bool{}
	entries, _ := os.ReadDir(`\\.\pipe\`)
	for _, e := range entries {
		if uiPipeName.MatchString(e.Name()) {
			out[e.Name()] = true
		}
	}
	return out
}

// runClient copies this test binary beside the sidecar exe as unravis-desktop.exe and runs the helper in mode. onLine sees each output
// line (ACKED, SHUTDOWN, FINAL). It returns when the helper has ended.
func (s *sidecar) runClient(t *testing.T, mode string, onLine func(line string)) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(filepath.Dir(s.exePath), "unravis-desktop.exe")
	if err := os.WriteFile(client, b, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(client, "-test.run=^TestSidecarPipeClientHelper$")
	cmd.Env = append(os.Environ(), helperEnvMode+"="+mode, helperEnvPipe+"="+s.pipeName, helperEnvExe+"="+s.exePath, helperEnvCaseID+"="+string(demoCaseOpenID))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(90 * time.Second)
		_ = cmd.Process.Kill()
	}()
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		onLine(strings.TrimSpace(sc.Text()))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("pipe client: %v\n%s", err, stderr.String())
	}
}

func lastCount(line, prefix string, into *int64) {
	if rest, ok := strings.CutPrefix(line, prefix+" "); ok {
		if n, err := strconv.ParseInt(strings.Fields(rest)[0], 10, 64); err == nil {
			*into = n
		}
	}
}

// TestShutdownCleanRealExe is spec 01 BL-303 acceptance "ShutdownClean" on app-created data: the real exe, writers busy, the shutdown RPC
// over the real ui pipe; the exe exits 0 within 5 s, leaves no -wal or -shm, the case passes integrity_check and keeps every saved line.
func TestShutdownCleanRealExe(t *testing.T) {
	s := startSidecar(t)
	var called time.Time
	var final int64
	s.runClient(t, "shutdown", func(line string) {
		if strings.HasPrefix(line, "SHUTDOWN") {
			called = time.Now()
			if !strings.HasPrefix(line, "SHUTDOWN 202") {
				t.Errorf("shutdown call answered %q, want 202", line)
			}
		}
		lastCount(line, "FINAL", &final)
	})
	select {
	case err := <-s.exited:
		if err != nil {
			t.Fatalf("exe did not exit 0: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exe still running 5 s after the shutdown call")
	}
	if called.IsZero() {
		t.Fatal("the helper never called shutdown")
	}
	t.Logf("exit 0, %v after the call; %d lines saved", time.Since(called).Round(10*time.Millisecond), final)
	if left := walFiles(t, s.dataDir); len(left) != 0 {
		t.Fatalf("left after shutdown: %v", left)
	}
	verifyCaseAfterStop(t, s.getenv, string(demoCaseOpenID), final)
}

// TestKillDuringWriteReopens is spec 01 BL-303 acceptance "KillDuringWrite": the exe is killed (TerminateProcess, like taskkill /f) while
// writers are busy. The database must reopen, pass integrity_check and still hold every line the app said it saved.
func TestKillDuringWriteReopens(t *testing.T) {
	s := startSidecar(t)
	var acked int64
	killed := false
	s.runClient(t, "load", func(line string) {
		lastCount(line, "ACKED", &acked)
		if acked >= 20 && !killed {
			killed = true
			_ = s.cmd.Process.Kill()
		}
	})
	<-s.exited
	if !killed {
		t.Fatal("the load never reached 20 saved writes")
	}
	t.Logf("killed after %d reported saves; -wal left behind: %v", acked, len(walFiles(t, s.dataDir)) > 0)
	verifyCaseAfterStop(t, s.getenv, string(demoCaseOpenID), acked)
}
