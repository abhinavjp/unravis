package localci

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a test: runTree tests re-run the test binary as a child with LOCALCI_HELPER set.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("LOCALCI_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "exit0":
		_, _ = os.Stdout.WriteString("helper ok\n")
		os.Exit(0)
	case "exit3":
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "background":
		// leave a child behind that holds this process's stdout and stderr, then exit 0
		c := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$") //nolint:gosec // re-running this test binary
		c.Env = append(os.Environ(), "LOCALCI_HELPER=sleep")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Start(); err != nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	os.Exit(8)
}

func helperCmd(mode string, out *bytes.Buffer) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$") //nolint:gosec // re-running this test binary
	cmd.Env = append(os.Environ(), "LOCALCI_HELPER="+mode)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd
}

func TestRunTreeWaitsAndReturnsExitStatus(t *testing.T) {
	var out bytes.Buffer
	started, err := runTree(context.Background(), helperCmd("exit0", &out), "")
	if !started || err != nil || !strings.Contains(out.String(), "helper ok") {
		t.Fatalf("started=%v err=%v out=%q", started, err, out.String())
	}
	started, err = runTree(context.Background(), helperCmd("exit3", &out), "")
	var ee *exec.ExitError
	if !started || !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("started=%v err=%v", started, err)
	}
	started, err = runTree(context.Background(), exec.Command("definitely-not-a-program-xyz"), "")
	if started || err == nil {
		t.Fatalf("a missing program is not started: %v %v", started, err)
	}
}

func TestRunTreeExitZeroWithBackgroundChildIsSuccess(t *testing.T) {
	var out bytes.Buffer
	cmd := helperCmd("background", &out)
	cmd.WaitDelay = 500 * time.Millisecond
	start := time.Now()
	started, err := runTree(context.Background(), cmd, "")
	if !started || err != nil {
		t.Fatalf("started=%v err=%v: exit 0 with a lingering child is a success", started, err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestRunTreeCancelKillsTheTree(t *testing.T) {
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := helperCmd("sleep", &out)
	cmd.WaitDelay = time.Second
	start := time.Now()
	started, err := runTree(ctx, cmd, "")
	if !started || err == nil {
		t.Fatalf("started=%v err=%v: a cancelled command must report an error", started, err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("took %v: the command was not killed", d)
	}
}
