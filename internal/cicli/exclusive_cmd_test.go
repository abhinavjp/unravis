package cicli

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

func TestExclusiveRunsTheCommandAndReturnsItsExitCode(t *testing.T) {
	state := t.TempDir()
	var out, errb bytes.Buffer
	self := os.Args[0]
	if code := realMain(context.Background(), []string{"exclusive", "res", "--state", state, "--", self, "-test.run=^$"}, &out, &errb, os.Environ()); code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, errb.String())
	}
	// An unknown flag makes the test binary exit 2: the wrapper must hand that code back.
	if code := realMain(context.Background(), []string{"exclusive", "res", "--state", state, "--", self, "-test.nosuchflag"}, &out, &errb, os.Environ()); code != 2 {
		t.Fatalf("exit %d, want the command's own 2", code)
	}
}

func TestExclusiveWaitsForAHolderOfTheSameResource(t *testing.T) {
	state := t.TempDir()
	release, err := localci.HoldExclusive(context.Background(), "res", state+string(os.PathSeparator)+"locks", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	var out, errb bytes.Buffer
	go func() {
		done <- realMain(context.Background(), []string{"exclusive", "res", "--state", state, "--", os.Args[0], "-test.run=^$"}, &out, &errb, os.Environ())
	}()
	select {
	case code := <-done:
		t.Fatalf("ran while the resource was held (exit %d)", code)
	case <-time.After(1500 * time.Millisecond): // winok: waiting to show the other side stays blocked
	}
	release()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d after release: %s", code, errb.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("still waiting after the holder released")
	}
}

func TestExclusiveNeedsAResourceAndACommand(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"exclusive"}, {"exclusive", "res"}, {"exclusive", "res", "--"}, {"exclusive", "--", "x"}, {"exclusive", "a", "b", "--", "x"}} {
		if code := realMain(context.Background(), args, &out, &errb, os.Environ()); code != exitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}
