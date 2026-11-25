//go:build windows

package localci

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRunTreeReleasesTheJobEntry(t *testing.T) {
	var out bytes.Buffer
	cmd := helperCmd("exit0", &out)
	if started, err := runTree(context.Background(), cmd, ""); !started || err != nil {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if _, ok := jobs.Load(cmd); ok {
		t.Fatal("the job entry must be dropped once the step is waited for")
	}
}

func TestAfterStartFailureLeavesNoJobEntry(t *testing.T) {
	cmd := &exec.Cmd{Process: &os.Process{Pid: 0x7FFFFFF0}} // no such process: OpenProcess fails
	if err := afterStart(cmd); err == nil {
		t.Fatal("afterStart on a missing process must fail")
	}
	if _, ok := jobs.Load(cmd); ok {
		t.Fatal("a failed afterStart must not leave a job entry behind")
	}
	releaseTree(cmd) // and releasing it afterwards is harmless
}

func TestStepJobTerminateAndCloseAreExclusiveAndIdempotent(t *testing.T) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	j := &stepJob{h: h}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); j.terminate() }()
		go func() { defer wg.Done(); j.terminateAndClose() }()
	}
	wg.Wait()
	if !j.closed {
		t.Fatal("the handle must be closed")
	}
	j.terminate() // a no-op after the close, never touches the stale handle
	j.terminateAndClose()
}
