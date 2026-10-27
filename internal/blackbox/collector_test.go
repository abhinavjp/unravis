package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

type fakeSource struct {
	cores    int
	host     []sampler.HostSample
	hostErr  error
	procs    [][]sampler.ProcSample
	procsErr error
	hi, pi   int
	closed   bool
}

func (f *fakeSource) Cores() int { return f.cores }
func (f *fakeSource) Host() (sampler.HostSample, error) {
	if f.hostErr != nil {
		return sampler.HostSample{}, f.hostErr
	}
	i := f.hi
	if i >= len(f.host) {
		i = len(f.host) - 1
	}
	f.hi++
	return f.host[i], nil
}
func (f *fakeSource) Processes(dst []sampler.ProcSample) ([]sampler.ProcSample, error) {
	if f.procsErr != nil {
		return dst, f.procsErr
	}
	i := f.pi
	if i >= len(f.procs) {
		i = len(f.procs) - 1
	}
	f.pi++
	return append(dst, f.procs[i]...), nil
}
func (f *fakeSource) Close() error { f.closed = true; return nil }

type fakeExtras struct {
	qErr, nErr error
	closed     bool
}

func (f *fakeExtras) DiskQueue() (float64, error) { return 1.5, f.qErr }
func (f *fakeExtras) Network() (uint64, uint64, error) {
	return 100, 200, f.nErr
}
func (f *fakeExtras) Close() error { f.closed = true; return nil }

func proc(pid uint32, name string, cpu float64) sampler.ProcSample {
	return sampler.ProcSample{PID: pid, Image: name, Threads: 4,
		Have:       sampler.HaveTimes | sampler.HaveMemory | sampler.HaveHandles,
		Start:      time.Unix(1000, 0),
		CPUSeconds: cpu, WorkingSet: 1 << 20, PrivateBytes: 1 << 19, Handles: 10}
}

func TestHostCollectorCPUPercentAndMemory(t *testing.T) {
	src := &fakeSource{cores: 2, host: []sampler.HostSample{
		{Times: sampler.HostTimes{Idle: 100, Kernel: 150, User: 50}, AvailableBytes: 5, CommittedBytes: 6},
		{Times: sampler.HostTimes{Idle: 150, Kernel: 250, User: 150}, AvailableBytes: 7, CommittedBytes: 8},
	}}
	c := NewHostCollector(src, &fakeExtras{}, false)
	now := time.Now()
	if err := c.Prime(now); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect(now.Add(15 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// total delta 200, idle delta 50: 75 percent busy.
	if s.CPU == nil || *s.CPU != 75 {
		t.Fatalf("cpu = %v", s.CPU)
	}
	if *s.MemAvail != 7 || *s.MemCommit != 8 {
		t.Fatal("memory")
	}
	if s.DiskQueue == nil || *s.NetIn != 100 || *s.NetOut != 200 {
		t.Fatal("extras")
	}
	if len(s.Missing) != 0 {
		t.Fatalf("missing = %v", s.Missing)
	}
}

func TestHostCollectorFirstSampleWithoutPrimeHasNoCPU(t *testing.T) {
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{Times: sampler.HostTimes{Idle: 1, Kernel: 2, User: 3}}}}
	c := NewHostCollector(src, nil, false)
	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU != nil {
		t.Fatal("no baseline, so no cpu value")
	}
}

func TestHostCollectorMissingCountersAreOmitted(t *testing.T) {
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{}, {}}}
	c := NewHostCollector(src, &fakeExtras{qErr: errors.New("x"), nErr: errors.New("y")}, false)
	now := time.Now()
	_ = c.Prime(now)
	s, err := c.Collect(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskQueue != nil || s.NetIn != nil || s.NetOut != nil {
		t.Fatal("failed counters must be absent, not zero")
	}
	// No CPU time passed at all: that is missing too, not 0 percent.
	if s.CPU != nil {
		t.Fatal("cpu with no time delta must be absent")
	}
	want := map[string]bool{"cpu": true, "diskQueue": true, "network": true}
	for _, m := range s.Missing {
		delete(want, m)
	}
	if len(want) != 0 {
		t.Fatalf("missing = %v", s.Missing)
	}
	line, _ := jsonLine(s)
	for _, bad := range []string{`"diskQueue":0`, `"netIn":0`, `"cpu":0`} {
		if contains(line, bad) {
			t.Fatalf("line has %s: %s", bad, line)
		}
	}
}

func TestHostCollectorHostFailureIsError(t *testing.T) {
	c := NewHostCollector(&fakeSource{cores: 1, hostErr: errors.New("boom")}, nil, false)
	if _, err := c.Collect(time.Now()); err == nil {
		t.Fatal("want error")
	}
}

func TestTopProcessesByCPUChange(t *testing.T) {
	var first, second []sampler.ProcSample
	for i := 1; i <= 30; i++ {
		first = append(first, proc(uint32(i), "p", 100))
		// process 7 burns 6 s over 15 s on a 2 core box: 20 percent of the machine.
		d := 0.1
		if i == 7 {
			d = 6
		}
		second = append(second, proc(uint32(i), "p", 100+d))
	}
	second[2].Have = 0 // access denied: still listed, no cpu fields
	src := &fakeSource{cores: 2, host: []sampler.HostSample{{}, {}}, procs: [][]sampler.ProcSample{first, second}}
	c := NewHostCollector(src, nil, true)
	now := time.Now()
	if err := c.Prime(now); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect(now.Add(15 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Procs) != TopProcesses {
		t.Fatalf("procs = %d", len(s.Procs))
	}
	if s.Procs[0].PID != 7 {
		t.Fatalf("top = %d", s.Procs[0].PID)
	}
	if got := *s.Procs[0].CPUPct; got < 19.99 || got > 20.01 {
		t.Fatalf("pct = %v", got)
	}
}

func TestTopProcessesFirstSampleOrdersByCumulativeTime(t *testing.T) {
	procs := []sampler.ProcSample{proc(1, "a", 5), proc(2, "b", 50), proc(0, "idle", 9999)}
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{}}, procs: [][]sampler.ProcSample{procs}}
	c := NewHostCollector(src, nil, true)
	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Procs) != 2 || s.Procs[0].PID != 2 {
		t.Fatalf("procs = %+v", s.Procs)
	}
	if s.Procs[0].CPUPct != nil {
		t.Fatal("no change to take on the first sample, so no percent")
	}
}

func TestProcessesOffAndProcessFailure(t *testing.T) {
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{}}, procs: [][]sampler.ProcSample{{proc(1, "a", 1)}}}
	s, _ := NewHostCollector(src, nil, false).Collect(time.Now())
	if len(s.Procs) != 0 {
		t.Fatal("processes off")
	}
	src2 := &fakeSource{cores: 1, host: []sampler.HostSample{{}}, procsErr: errors.New("x")}
	s2, err := NewHostCollector(src2, nil, true).Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range s2.Missing {
		found = found || m == "processes"
	}
	if !found {
		t.Fatalf("missing = %v", s2.Missing)
	}
}

func TestCollectorCloseClosesBoth(t *testing.T) {
	src, ex := &fakeSource{cores: 1}, &fakeExtras{}
	if err := NewHostCollector(src, ex, false).Close(); err != nil {
		t.Fatal(err)
	}
	if !src.closed || !ex.closed {
		t.Fatal("not closed")
	}
}

func TestCatalogueLogsListsNamesOnlyNewestFirst(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p string, age time.Duration) {
		if err := os.WriteFile(p, []byte("secret content"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}
	write(filepath.Join(root, "old.log"), 48*time.Hour)
	write(filepath.Join(sub, "new.LOG"), time.Hour)
	write(filepath.Join(root, "data.txt"), time.Minute)
	deep := filepath.Join(root, "1", "2", "3", "4", "5")
	_ = os.MkdirAll(deep, 0o755)
	write(filepath.Join(deep, "deep.log"), time.Minute)

	got := CatalogueLogs([]string{root, filepath.Join(root, "missing")})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if filepath.Base(got[0].Path) != "new.LOG" || filepath.Base(got[1].Path) != "old.log" {
		t.Fatalf("order %+v", got)
	}
	if got[0].Size != int64(len("secret content")) {
		t.Fatal("size")
	}
}

func TestCatalogueLogsCapsEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxLogEntries+20; i++ {
		_ = os.WriteFile(filepath.Join(root, "f"+itoa(i)+".log"), nil, 0o644)
	}
	if n := len(CatalogueLogs([]string{root})); n != MaxLogEntries {
		t.Fatalf("n = %d", n)
	}
}

func TestDiskInfoFreePercent(t *testing.T) {
	if (DiskInfo{}).FreePercent() != 0 {
		t.Fatal("zero total")
	}
	if p := (DiskInfo{Free: 25, Total: 100}).FreePercent(); p != 25 {
		t.Fatal(p)
	}
}

func TestNearestExisting(t *testing.T) {
	root := t.TempDir()
	if got := nearestExisting(filepath.Join(root, "a", "b")); got != root {
		t.Fatalf("got %s", got)
	}
}
