//go:build windows

package locator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/pipesec"
)

// TestLocatorFileACLOwnerOnly writes the locator through the production ACL step and reads the DACL with icacls: no inherited entry,
// and nothing for Everyone, Users or Authenticated Users.
func TestLocatorFileACLOwnerOnly(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	if err := Write(path, rec(), Options{SecureDir: casestore.SecureDir, SecureFile: casestore.SecureFile}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls: %v\n%s", err, out)
	}
	text := string(out)
	for _, bad := range []string{"Everyone", "BUILTIN\\Users", "Authenticated Users", "(I)"} {
		if strings.Contains(text, bad) {
			t.Errorf("DACL has %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, os.Getenv("USERNAME")) {
		t.Errorf("DACL does not name the current user:\n%s", text)
	}
}

func TestSystemProberSelfAndPipes(t *testing.T) {
	p := SystemProber()
	start, err := SelfStart()
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.ProcessStart(os.Getpid())
	if err != nil || !sameStart(got, start) {
		t.Fatalf("ProcessStart = %v, %v; want %v", got, err, start)
	}
	if d := time.Since(start); d < 0 || d > 24*time.Hour {
		t.Fatalf("creation time %v is not recent", start)
	}
	exe, _ := os.Executable()
	policy := pipesec.PeerPolicy{InstallDir: filepath.Dir(exe), AllowedBase: []string{filepath.Base(exe)}, RequireSameLogon: true}
	ui, _ := pipesec.NewName(pipesec.KindUI)
	ag, _ := pipesec.NewName(pipesec.KindAG)
	if p.PipeExists(ui) {
		t.Fatal("a pipe that was never created exists")
	}
	l1, err := pipesec.Listen(pipesec.ListenConfig{Name: ui, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := pipesec.Listen(pipesec.ListenConfig{Name: ag, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	r := Record{Version: Schema, PID: os.Getpid(), StartTime: start, Exe: exe, AppVersion: "t", UI: ui, AG: ag}
	if !Alive(r, p) {
		t.Fatal("a live owner with real pipes is not alive")
	}
	_ = l1.Close()
	_ = l2.Close()
	deadline := time.Now().Add(5 * time.Second)
	for Alive(r, p) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Alive(r, p) {
		t.Fatal("closed pipes still count as alive")
	}
}

// TestProcessStartDeadPid starts a process, waits for it to exit and expects ProcessStart to fail for its pid.
func TestProcessStartDeadPid(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := SystemProber().ProcessStart(cmd.ProcessState.Pid()); err == nil {
		t.Fatal("an exited process has a start time")
	}
}
