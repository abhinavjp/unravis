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
	"sort"
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
// case from 4 goroutines and prints "SAVED <text>" for each write the app acknowledged ("ACKED <n>" every fifth, "REFUSED <code>" for a refused one); "shutdown" does the same and, after 20 saved writes, calls the
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
				text := fmt.Sprintf("line %d %s", w, time.Now().Format(time.RFC3339Nano))
				body, _ := json.Marshal(map[string]any{"list": casestore.ListToDo, "text": text})
				code, err := call("POST", "/api/cases/"+caseID+"/checklist", body)
				switch {
				case err != nil:
					down.Store(true)
				case code == http.StatusOK:
					n := acked.Add(1)
					fmt.Println("SAVED", text)
					if n%5 == 0 {
						fmt.Println("ACKED", n)
					}
				default:
					fmt.Println("REFUSED", code) // for example 423 when the case is locked by a master key
					time.Sleep(200 * time.Millisecond)
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

type shutdownSidecar struct {
	cmd      *exec.Cmd
	getenv   func(string) string
	dataDir  string
	exePath  string
	pipeName string
	demo     bool
	done     chan struct{} // closed when the process has ended
	exitErr  error         // valid after done is closed
	exitedAt atomic.Int64  // UnixNano of the moment Wait returned
}

// longTempDir is t.TempDir() in its long-name form. On the GitHub Windows runners TEMP is C:\Users\RUNNER~1\..., but the kernel reports
// process image paths in long form, and the pipe check compares images and install folders as text, so a short-name path would be
// refused as a server mismatch or a denied peer.
func longTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	in, err := windows.UTF16FromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetLongPathName(&in[0], nil, 0)
	if err != nil {
		t.Fatalf("GetLongPathName: %v", err)
	}
	buf := make([]uint16, n)
	if n, err = windows.GetLongPathName(&in[0], &buf[0], uint32(len(buf))); err != nil {
		t.Fatalf("GetLongPathName: %v", err)
	}
	long := windows.UTF16ToString(buf[:n])
	if resolved, err := filepath.EvalSymlinks(long); err == nil {
		long = resolved
	}
	return long
}

// startSidecar builds unravis.exe (once per test), starts it as --pipe-only --demo with real demo cases and waits for its ui pipe.
func startShutdownSidecar(t *testing.T) *shutdownSidecar {
	t.Helper()
	dir := longTempDir(t)
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"AUDIT_CONFIG_DIR": data, "AUDIT_SESSION_TOKEN": testToken,
		envDemoCases: "1", envDemoNow: demoTestNow.Format(time.RFC3339),
	}
	return startShutdownSidecarOn(t, buildExe(t, dir), data, env, true)
}

// startShutdownSidecarOn starts exePath as --pipe-only (with --demo when demo) on data and waits for its new ui pipe. It can be called again on
// the same exe and data to restart it. The process is killed and waited for at the end of the test, so the folder can be removed.
func startShutdownSidecarOn(t *testing.T, exePath, data string, env map[string]string, demo bool) *shutdownSidecar {
	t.Helper()
	before := uiPipes()
	args := []string{"--pipe-only"}
	if demo {
		args = append(args, "--demo")
	}
	cmd := exec.Command(exePath, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &shutdownSidecar{cmd: cmd, getenv: envOf(env), dataDir: data, exePath: exePath, demo: demo, done: make(chan struct{})}
	go func() {
		s.exitErr = cmd.Wait()
		s.exitedAt.Store(time.Now().UnixNano())
		close(s.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for s.pipeName == "" {
		select {
		case <-s.done:
			t.Fatalf("exe exited early: %v", s.exitErr)
		default:
		}
		var fresh []string
		for n := range uiPipes() {
			if !before[n] {
				fresh = append(fresh, n)
			}
		}
		sort.Strings(fresh)
		switch {
		case len(fresh) == 1:
			s.pipeName = `\\.\pipe\` + fresh[0]
		case len(fresh) > 1:
			// Another Unravis started at the same moment; a guess could drive someone else's app.
			t.Fatalf("several new ui pipes appeared (%v); stop other Unravis processes and run again", fresh)
		case time.Now().After(deadline):
			t.Fatal("the ui pipe did not appear")
		default:
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
func (s *shutdownSidecar) runClient(t *testing.T, mode, caseID string, onLine func(line string)) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if long, err := filepath.EvalSymlinks(self); err == nil {
		self = long
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
	cmd.Env = append(os.Environ(), helperEnvMode+"="+mode, helperEnvPipe+"="+s.pipeName, helperEnvExe+"="+s.exePath, helperEnvCaseID+"="+caseID)
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

// savedTexts collects the SAVED lines the helper prints.
type savedTexts struct {
	texts   []string
	refused string
	final   int64
}

func (c *savedTexts) line(line string) {
	if rest, ok := strings.CutPrefix(line, "SAVED "); ok {
		c.texts = append(c.texts, rest)
	}
	if rest, ok := strings.CutPrefix(line, "REFUSED "); ok && c.refused == "" {
		c.refused = rest
	}
	lastCount(line, "FINAL", &c.final)
}

// shutdownAndProve runs the "shutdown" helper against s and checks: the call answered 202 with {"state":"stopping"}, the exe exited 0 at
// most 5 s after the call, and no -wal/-shm is left. It returns the texts the app acknowledged.
func shutdownAndProve(t *testing.T, s *shutdownSidecar, caseID string) []string {
	t.Helper()
	var got savedTexts
	var called time.Time
	s.runClient(t, "shutdown", caseID, func(line string) {
		if strings.HasPrefix(line, "SHUTDOWN") {
			called = time.Now()
			if !strings.HasPrefix(line, "SHUTDOWN 202") {
				t.Errorf("shutdown call answered %q, want 202", line)
			}
		}
		got.line(line)
	})
	if called.IsZero() {
		if got.refused != "" {
			t.Fatalf("the app refused the writes with %s (a case locked by a master key answers 423); the proof needs an unlocked case", got.refused)
		}
		t.Fatal("the helper never called shutdown")
	}
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("exe still running 10 s after the shutdown call")
	}
	if s.exitErr != nil {
		t.Fatalf("exe did not exit 0: %v", s.exitErr)
	}
	// The time is measured from the call to the moment the process ended, not from when the helper returned: the helper outlives the
	// shutdown while its writers fail one by one.
	took := time.Unix(0, s.exitedAt.Load()).Sub(called)
	if took >= 5*time.Second {
		t.Fatalf("exe took %v from the shutdown call to exit, want under 5 s", took)
	}
	t.Logf("exit 0, %v after the call; %d lines saved", took.Round(10*time.Millisecond), len(got.texts))
	if left := walFiles(t, s.dataDir); len(left) != 0 {
		t.Fatalf("left after shutdown: %v", left)
	}
	return got.texts
}

// TestShutdownCleanRealExe is spec 01 BL-303 acceptance "ShutdownClean" on app-created data: the real exe, writers busy, the shutdown RPC
// over the real ui pipe; the exe exits 0 within 5 s of the call, leaves no -wal or -shm, the case passes integrity_check and keeps every
// saved line.
func TestShutdownCleanRealExe(t *testing.T) {
	s := startShutdownSidecar(t)
	saved := shutdownAndProve(t, s, string(demoCaseOpenID))
	verifyCaseAfterStop(t, s.getenv, string(demoCaseOpenID), saved)
}

// TestKillDuringWriteReopens is spec 01 BL-303 acceptance "KillDuringWrite": the exe is killed (TerminateProcess, like taskkill /f) while
// writers are busy, leaving a -wal behind. The exe is then started again on the same folder: it must open the case, take writes and stop
// cleanly, and the case must pass integrity_check and still hold every line the first run said it saved.
func TestKillDuringWriteReopens(t *testing.T) {
	s := startShutdownSidecar(t)
	var got savedTexts
	var acked int64
	killed := false
	s.runClient(t, "load", string(demoCaseOpenID), func(line string) {
		got.line(line)
		lastCount(line, "ACKED", &acked)
		// Kill only while a -wal exists: SQLite may merge the log between two commits, and a kill then leaves nothing to recover.
		if acked >= 20 && !killed {
			if found, err := findWALFiles(s.dataDir); err == nil && len(found) > 0 {
				killed = true
				_ = s.cmd.Process.Kill()
			}
		}
	})
	<-s.done
	if !killed {
		t.Fatal("the load never reached 20 saved writes with a -wal file present")
	}
	left := walFiles(t, s.dataDir)
	t.Logf("killed after %d reported saves; left behind: %v", acked, left)
	if len(left) == 0 {
		t.Fatal("no -wal file was left behind, so recovery from a WAL was not exercised")
	}
	// Restart the real exe on the same folder.
	env := map[string]string{
		"AUDIT_CONFIG_DIR": s.dataDir, "AUDIT_SESSION_TOKEN": testToken,
		envDemoCases: "1", envDemoNow: demoTestNow.Format(time.RFC3339),
	}
	s2 := startShutdownSidecarOn(t, s.exePath, s.dataDir, env, true)
	more := shutdownAndProve(t, s2, string(demoCaseOpenID))
	verifyCaseAfterStop(t, s2.getenv, string(demoCaseOpenID), append(got.texts, more...))
}

// TestShutdownCleanRealCase is the live-PC run of the same proof, on a COPY of a real data folder (tools/desktop/Test-Sidecar.ps1
// -Check ShutdownClean -Real makes the copy and sets these variables; the test does nothing without them). The exe runs without --demo
// on the copy, the writers add to-do lines to one real case, the shutdown RPC is called while they are busy, and the exe must exit 0
// within 5 s with no -wal/-shm left. The acknowledged texts are written to UNRAVIS_REAL_ACKED for tools/dev/checkdb, which then
// checks integrity_check and that every one of them is in the case.
func TestShutdownCleanRealCase(t *testing.T) {
	data, caseID, ackedFile := os.Getenv("UNRAVIS_REAL_DATA"), os.Getenv("UNRAVIS_REAL_CASE_ID"), os.Getenv("UNRAVIS_REAL_ACKED")
	if data == "" || caseID == "" || ackedFile == "" {
		t.Skip("set by Test-Sidecar.ps1 -Check ShutdownClean -Real")
	}
	exe := buildExe(t, longTempDir(t))
	env := map[string]string{"AUDIT_CONFIG_DIR": data, "AUDIT_SESSION_TOKEN": testToken}
	s := startShutdownSidecarOn(t, exe, data, env, false)
	saved := shutdownAndProve(t, s, caseID)
	if len(saved) < 20 {
		t.Fatalf("only %d writes were saved on the real case", len(saved))
	}
	if err := os.WriteFile(ackedFile, []byte(strings.Join(saved, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
