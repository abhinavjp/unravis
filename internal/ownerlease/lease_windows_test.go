//go:build windows

package ownerlease

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLeaseExclusive(t *testing.T) {
	dir := t.TempDir()
	rel, err := Acquire(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire = %v, want ErrBusy", err)
	}
	// Demo and non-demo use different files.
	relDemo, err := Acquire(dir, true)
	if err != nil {
		t.Fatalf("demo Acquire while normal held: %v", err)
	}
	relDemo()
	rel()
	rel2, err := Acquire(dir, false)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	rel2()
}

// TestLeaseHelper is the child of TestLeaseFreedOnKill; it does nothing unless the parent sets the env var.
func TestLeaseHelper(t *testing.T) {
	dir := os.Getenv("OWNERLEASE_HELPER_DIR")
	if dir == "" {
		return
	}
	if _, err := Acquire(dir, false); err != nil {
		os.Stdout.WriteString("error " + err.Error() + "\n")
		return
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(time.Minute)
}

func TestLeaseFreedOnKill(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestLeaseHelper")
	cmd.Env = append(os.Environ(), "OWNERLEASE_HELPER_DIR="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("helper said %q, err %v; want ready", line, err)
	}
	if _, err := Acquire(dir, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("Acquire while child holds = %v, want ErrBusy", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	killed = true
	deadline := time.Now().Add(2 * time.Second)
	for {
		rel, err := Acquire(dir, false)
		if err == nil {
			rel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease not freed within 2s of kill: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAcquireCreatesRunDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	rel, err := Acquire(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if _, err := os.Stat(Path(dir, false)); err != nil {
		t.Errorf("lock file missing: %v", err)
	}
}
