package localci

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExclusiveTakesTurns(t *testing.T) {
	var inside, worst int32
	var waited int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireExclusive(context.Background(), "test-turns", "", func() { atomic.AddInt32(&waited, 1) })
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for {
				w := atomic.LoadInt32(&worst)
				if n <= w || atomic.CompareAndSwapInt32(&worst, w, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			release()
		}()
	}
	wg.Wait()
	if worst != 1 {
		t.Fatalf("%d holders at once, want 1", worst)
	}
	if waited == 0 {
		t.Fatal("nobody waited, so the test did not overlap")
	}
}

func TestExclusiveNamesAreIndependent(t *testing.T) {
	a, err := acquireExclusive(context.Background(), "test-a", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := acquireExclusive(context.Background(), "test-b", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b()
}

func TestExclusiveWaitEndsWithTheRun(t *testing.T) {
	held, err := acquireExclusive(context.Background(), "test-cancel", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := acquireExclusive(ctx, "test-cancel", "", nil); err == nil {
		t.Fatal("a cancelled wait must give up")
	}
}

func TestExclusiveLockFileHoldsAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/res.lock"
	first, err := lockFile(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := lockFile(ctx, path, nil); err == nil {
		t.Fatal("a second holder got the lock while the first still had it")
	}
	first()
	second, err := lockFile(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestExclusiveLockFileOfADeadProcessIsTaken(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/res.lock"
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d %d", 0x7FFFFFF0, time.Now().Unix())), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := lockFile(ctx, path, nil)
	if err != nil {
		t.Fatalf("a lock left by a dead process was not taken: %v", err)
	}
	unlock()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unlock left the file")
	}
}

func TestExclusiveLockFileOfALiveButOldHolderIsTaken(t *testing.T) {
	old := time.Now().Add(-exclusiveMaxHold - time.Minute).Unix()
	if !lockIsStale(fmt.Sprintf("%d %d", os.Getpid(), old), time.Now()) {
		t.Fatal("a hold older than the cap should be stale")
	}
	if lockIsStale(fmt.Sprintf("%d %d", os.Getpid(), time.Now().Unix()), time.Now()) {
		t.Fatal("a fresh hold by a live process is not stale")
	}
	if lockIsStale("", time.Now()) || lockIsStale("garbage", time.Now()) {
		t.Fatal("unreadable content is not stale yet")
	}
}

func TestExclusiveAcrossTwoAcquiresWithLockDir(t *testing.T) {
	dir := t.TempDir()
	a, err := acquireExclusive(context.Background(), "res x", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := acquireExclusive(ctx, "res x", dir, nil); err == nil {
		t.Fatal("second acquire succeeded while held")
	}
	a()
	b, err := acquireExclusive(context.Background(), "res x", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	b()
}

// A step named in step-exclusive waits for the resource and the other steps of the job do not.
func TestStepExclusiveWaitsOnlyThatStep(t *testing.T) {
	needBash(t)
	h := newHarness(t, `
name: m
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - name: free
        run: echo free-ran
      - name: guarded
        run: echo guarded-ran
`)
	h.opts.LockDir = t.TempDir()
	cfg, err := ParseLocalConfig([]byte("jobs:\n  wf.yml:\n    j:\n      step-exclusive:\n        guarded: a-name\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.opts.Config = cfg
	held, err := acquireExclusive(context.Background(), "a-name", h.opts.LockDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *RunResult, 1)
	go func() { done <- h.run() }()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(h.logText(), "waiting for the exclusive resource") {
		if time.Now().After(deadline) {
			t.Fatalf("the guarded step never waited:\n%s", h.logText())
		}
		time.Sleep(50 * time.Millisecond) // winok: polls a log condition under a 20 s deadline
	}
	if !strings.Contains(h.logText(), "step 1/2 free ... ok") {
		t.Fatalf("the free step should not wait:\n%s", h.logText())
	}
	select {
	case <-done:
		t.Fatal("the run finished while the resource was held")
	default:
	}
	held()
	res := <-done
	if got := job(t, res, "j").Status; got != "success" {
		t.Fatalf("job %s:\n%s", got, h.logText())
	}
}
