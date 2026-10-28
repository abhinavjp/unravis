//go:build windows

package sampler

import (
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/importer/winlive"
)

// A smoke test of the real Windows source: it reads this machine, which has at
// least this test process in the list.
func TestWindowsSourceReadsThisMachine(t *testing.T) {
	src, err := OSSource()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Cores() < 1 {
		t.Fatalf("cores = %d", src.Cores())
	}
	h, err := src.Host()
	if err != nil {
		t.Fatal(err)
	}
	if h.AvailableBytes == 0 || h.CommittedBytes == 0 || h.Times.Kernel == 0 || h.Times.Idle > h.Times.Kernel {
		t.Errorf("host sample %+v", h)
	}
	procs, err := src.Processes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) < 5 {
		t.Fatalf("%d processes", len(procs))
	}
	var self *ProcSample
	for i := range procs {
		if procs[i].PID == uint32(os.Getpid()) {
			self = &procs[i]
		}
	}
	if self == nil {
		t.Fatal("this process is not in the list")
	}
	if self.Have != HaveTimes|HaveMemory|HaveHandles {
		t.Errorf("this process: Have = %b", self.Have)
	}
	if self.Image == "" || self.Threads == 0 || self.PrivateBytes == 0 || self.WorkingSet == 0 || self.Handles == 0 || self.CPUSeconds < 0 {
		t.Errorf("this process: %+v", *self)
	}
	if d := time.Since(self.Start); d < 0 || d > 24*time.Hour {
		t.Errorf("this process started %v ago", d)
	}
	for _, p := range procs {
		if p.PID == 0 {
			t.Error("the idle process is listed")
		}
	}
}

// A real run: the sampler at its shortest interval reads this machine for about
// six seconds, and the batch it writes on stop parses with the importer.
func TestWindowsSamplerRunsAndWritesABatch(t *testing.T) {
	if testing.Short() {
		t.Skip("takes six seconds")
	}
	log := &importLog{}
	s, err := Start(Config{IntervalSeconds: 5, Import: log.Import, Pseudonymize: pseudo})
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the first real tick (5 s after the start) instead of a fixed 6 s: the sampler thread runs at background priority and a
	// loaded machine can make it late. A sampler that never ticks still fails, after a long wait.
	deadline := time.Now().Add(60 * time.Second)
	for s.Status().Samples < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s.Stop()
	if st := s.Status(); st.Running || st.Stop != StopUser || st.Samples < 1 {
		t.Fatalf("status %+v", st)
	}
	if log.count() != 1 {
		t.Fatalf("%d batches, want 1", log.count())
	}
	evs, _ := parse(t, log.bodies[0])
	if len(evs) < 3 {
		t.Errorf("%d events", len(evs))
	}
}

// A short real run through the engine: two ticks at the shortest interval would
// take ten seconds, so this drives the engine against the real source instead.
func TestWindowsSourceThroughTheEngine(t *testing.T) {
	src, err := OSSource()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	e := newEngine(src, func(kind, value string) string { return pseudo(kind, value) }, 15, 1<<40, nil)
	now := time.Now()
	if err := e.prime(now); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := e.tick(now.Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, body, ok := e.cut()
	if !ok {
		t.Fatal("no batch")
	}
	evs, _ := parse(t, body)
	if len(evs) < 3 {
		t.Fatalf("%d events", len(evs))
	}
	_ = winlive.Name
}
