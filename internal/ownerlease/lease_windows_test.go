//go:build windows

package ownerlease

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

// The lock file is owner-only and carries a Medium no-read-up label, so a Low-integrity process of the same user cannot open it to hold
// it (which would lock every later start out while no owner runs). An older file with inherited rights is replaced.
func TestLeaseFileDescriptor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir, false), nil, 0o600); err != nil { // a file of an older run: inherited rights, default label
		t.Fatal(err)
	}
	rel, err := Acquire(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	sd, err := windows.GetNamedSecurityInfo(Path(dir, false), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.HasPrefix(s, "D:P") {
		t.Errorf("DACL is not protected from inheritance: %s", s)
	}
	if !strings.Contains(s, "ML;;NWNR;;;ME") {
		t.Errorf("no Medium no-read/no-write-up label: %s", s)
	}
	for _, wide := range []string{";;;BU)", ";;;AU)", ";;;WD)", ";;;BA)"} { // users, authenticated users, everyone, administrators
		if strings.Contains(s, wide) {
			t.Errorf("the DACL grants a wider group (%s): %s", wide, s)
		}
	}
}
