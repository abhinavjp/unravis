//go:build windows

package setup

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// startPausedChild starts "cmd.exe /c pause" held open by a stdin pipe, and kills it when the test ends.
func startPausedChild(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	exe := os.Getenv("ComSpec")
	if exe == "" {
		t.Skip("ComSpec is not set")
	}
	cmd := exec.Command(exe, "/c", "pause")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); <-done })
	return cmd, done
}

func TestTerminateIfChecksTheImagePath(t *testing.T) {
	cmd, done := startPausedChild(t)
	if err := (osProcesses{}).TerminateIf(uint32(cmd.Process.Pid), `C:\not\unravis.exe`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TerminateIf with another image = %v, want ErrNotFound", err)
	}
	select {
	case <-done:
		t.Fatal("the child was ended by a TerminateIf with the wrong image")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTerminateIfEndsTheMatchingImage(t *testing.T) {
	cmd, done := startPausedChild(t)
	if err := (osProcesses{}).TerminateIf(uint32(cmd.Process.Pid), os.Getenv("ComSpec")); err != nil {
		t.Fatalf("TerminateIf = %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the child is still running")
	}
}
