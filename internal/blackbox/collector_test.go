package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/investigate"
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
	qErr, nErr, latErr error
	closed             bool
}

func (f *fakeExtras) DiskQueue() (float64, error)     { return 1.5, f.qErr }
func (f *fakeExtras) DiskLatencyMs() (float64, error) { return 4.5, f.latErr }
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
	c := NewHostCollector(src, &fakeExtras{qErr: errors.New("x"), nErr: errors.New("y"), latErr: errors.New("z")}, false)
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
	want := map[string]bool{"cpu": true, "diskQueue": true, "diskMs": true, "network": true}
	for _, m := range s.Missing {
		delete(want, m)
	}
	if len(want) != 0 {
		t.Fatalf("missing = %v", s.Missing)
	}
	line, _ := jsonLine(s)
	for _, bad := range []string{`"diskQueue":0`, `"diskMs":0`, `"netIn":0`, `"cpu":0`} {
		if contains(line, bad) {
			t.Fatalf("line has %s: %s", bad, line)
		}
	}
}

func TestCollectDiskLatency(t *testing.T) {
	newSrc := func() *fakeSource {
		return &fakeSource{cores: 1, host: []sampler.HostSample{{}, {}}}
	}
	c := NewHostCollector(newSrc(), &fakeExtras{}, false)
	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskMs == nil || *s.DiskMs != 4.5 {
		t.Fatalf("DiskMs = %v, want 4.5", s.DiskMs)
	}

	c = NewHostCollector(newSrc(), &fakeExtras{latErr: errors.New("x")}, false)
	s, err = c.Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskMs != nil {
		t.Fatal("failed latency must be absent, not zero")
	}
	if s.DiskQueue == nil {
		t.Fatal("disk queue must survive a latency failure")
	}
	found := false
	for _, m := range s.Missing {
		if m == "diskMs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing = %v, want diskMs", s.Missing)
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
	// The memory top (equal working sets, tie by PID) sits inside the CPU top
	// here, so the merged, deduped list holds TopCPU rows.
	if len(s.Procs) != TopCPU {
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

// shapeProcs builds n processes where PID i burns i CPU seconds per interval
// and holds (n+1-i) MiB: the CPU top and the memory top are disjoint.
func shapeProcs(n int) (first, second []sampler.ProcSample) {
	for i := 1; i <= n; i++ {
		first = append(first, proc(uint32(i), "p", 100))
		p := proc(uint32(i), "p", 100+float64(i))
		p.WorkingSet = uint64(n+1-i) << 20
		second = append(second, p)
	}
	return first, second
}

func TestTopProcessesShape(t *testing.T) {
	first, second := shapeProcs(20)
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
	if len(s.Procs) != TopCPU+TopMem {
		t.Fatalf("procs = %d, want %d", len(s.Procs), TopCPU+TopMem)
	}
	seen := map[uint32]bool{}
	for i, r := range s.Procs {
		if seen[r.PID] {
			t.Fatalf("duplicate PID %d", r.PID)
		}
		seen[r.PID] = true
		want := uint32(20 - i) // CPU top 10 first: 20..11
		if i >= TopCPU {
			want = uint32(i - TopCPU + 1) // then the memory top 5: 1..5
		}
		if r.PID != want {
			t.Fatalf("row %d = PID %d, want %d", i, r.PID, want)
		}
	}
	// Same reads as before: one full process read at Prime, one at Collect.
	if src.pi != 2 {
		t.Fatalf("process reads = %d, want 2", src.pi)
	}
}

func TestTopProcessesIdleExcluded(t *testing.T) {
	idle := proc(0, "idle", 9999)
	idle.WorkingSet = 1 << 30 // tops both rankings, must still be left out
	procs := []sampler.ProcSample{idle, proc(1, "a", 5), proc(2, "b", 50), proc(3, "c", 1)}
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{}}, procs: [][]sampler.ProcSample{procs}}
	c := NewHostCollector(src, nil, true)
	c.SetSelectedTargets(func() []ResolvedTarget { return []ResolvedTarget{{Key: "svc:x", PID: 0, Start: 1000}} })
	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Procs) != 3 {
		t.Fatalf("procs = %+v", s.Procs)
	}
	for _, r := range s.Procs {
		if r.PID == 0 {
			t.Fatal("Idle (PID 0) must never be listed")
		}
	}
}

func TestTopProcessesSelectedAlways(t *testing.T) {
	first, second := shapeProcs(20)
	// An idle web server outside both tops: no CPU change, tiny working set.
	first = append(first, proc(100, "w3wp.exe", 50))
	p := proc(100, "w3wp.exe", 50)
	p.WorkingSet = 1 << 10
	second = append(second, p)
	src := &fakeSource{cores: 2, host: []sampler.HostSample{{}, {}}, procs: [][]sampler.ProcSample{first, second}}
	c := NewHostCollector(src, nil, true)
	c.SetSelectedTargets(func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:web", PID: 100, Start: 1000, Pool: "web"},
			{Key: "pool:hot", PID: 20, Start: 1000, Pool: "hot"}, // already CPU top
		}
	})
	now := time.Now()
	if err := c.Prime(now); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect(now.Add(15 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Procs) != TopCPU+TopMem+1 {
		t.Fatalf("procs = %d, want the idle selected target appended", len(s.Procs))
	}
	last := s.Procs[len(s.Procs)-1]
	if last.PID != 100 || last.Name != "w3wp.exe" {
		t.Fatalf("last = %+v, want the idle selected target", last)
	}
	// A measured idle is a real reading: percent present and zero, not nil.
	if last.CPUPct == nil || *last.CPUPct != 0 {
		t.Fatalf("idle selected CPUPct = %v, want a measured 0", last.CPUPct)
	}
	n := 0
	for _, r := range s.Procs {
		if r.PID == 20 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("selected target already in the top listed %d times", n)
	}
}

func TestTopProcessesDedup(t *testing.T) {
	first, second := shapeProcs(20)
	// PID 5 leads both rankings: busiest CPU and largest working set.
	second[4].CPUSeconds += 100
	second[4].WorkingSet = 1000 << 20
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
	if len(s.Procs) != TopCPU+TopMem-1 {
		t.Fatalf("procs = %d, want the shared row deduped", len(s.Procs))
	}
	n := 0
	for _, r := range s.Procs {
		if r.PID == 5 {
			n++
		}
	}
	if n != 1 || s.Procs[0].PID != 5 {
		t.Fatalf("PID 5 listed %d times, first = %d", n, s.Procs[0].PID)
	}
}

func TestTopProcessesMissingFieldsNil(t *testing.T) {
	denied := proc(9, "denied", 0)
	denied.Have = 0 // access denied: name and threads only
	procs := []sampler.ProcSample{proc(1, "a", 10), denied, proc(2, "b", 5)}
	src := &fakeSource{cores: 1, host: []sampler.HostSample{{}}, procs: [][]sampler.ProcSample{procs}}
	s, err := NewHostCollector(src, nil, true).Collect(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var r *ProcRow
	for i := range s.Procs {
		if s.Procs[i].PID == 9 {
			r = &s.Procs[i]
		}
	}
	if r == nil {
		t.Fatalf("denied process not listed: %+v", s.Procs)
	}
	if r.CPUSec != nil || r.CPUPct != nil || r.WS != nil || r.Private != nil || r.Handles != nil {
		t.Fatalf("unreadable fields must stay nil: %+v", r)
	}
	if r.Name != "denied" || r.Start != 0 {
		t.Fatalf("identity = %+v", r)
	}
	line, _ := jsonLine(s)
	for _, bad := range []string{`"cpuSec":0`, `"cpuPct":0`, `"ws":0`, `"priv":0`, `"handles":0`} {
		if contains(line, bad) {
			t.Fatalf("line has %s: %s", bad, line)
		}
	}
}

func TestStep9StillFindsWebServer(t *testing.T) {
	// Ten minutes of a busy web server: w3wp burns 12 s per 15 s on 2 cores
	// (40 percent of the machine); 19 background processes barely move.
	const steps = 40
	host := make([]sampler.HostSample, steps+1)
	procs := make([][]sampler.ProcSample, steps+1)
	for k := 0; k <= steps; k++ {
		host[k] = sampler.HostSample{Times: sampler.HostTimes{
			Idle: uint64(100 + k), Kernel: uint64(100 + 19*k), User: uint64(100 + 19*k)}}
		var ps []sampler.ProcSample
		for i := 1; i <= 19; i++ {
			ps = append(ps, proc(uint32(i), "bg", 100+0.1*float64(k)))
		}
		ps = append(ps, proc(1200, "w3wp.exe", 100+12*float64(k)))
		procs[k] = ps
	}
	src := &fakeSource{cores: 2, host: host, procs: procs}
	c := NewHostCollector(src, nil, true)
	c.SetSelectedTargets(func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:web", PID: 1200, Start: 1000, Pool: "web"}}
	})
	t0 := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	if err := c.Prime(t0); err != nil {
		t.Fatal(err)
	}
	var hs []investigate.HostSample
	for k := 1; k <= steps; k++ {
		now := t0.Add(time.Duration(k) * 15 * time.Second)
		s, err := c.Collect(now)
		if err != nil {
			t.Fatal(err)
		}
		h := investigate.HostSample{T: now, CPU: s.CPU}
		for _, p := range s.Procs {
			h.Procs = append(h.Procs, investigate.HostProc{Name: p.Name, CPUPct: p.CPUPct})
		}
		hs = append(hs, h)
	}
	in := investigate.Input{Incident: investigate.Window{Start: t0, End: t0.Add(10 * time.Minute)}}
	res := investigate.HostLane(in, investigate.HostInput{Name: "web-b", IntervalSeconds: 15, Samples: hs})
	if res.Lanes["H1"] != investigate.Yes {
		t.Fatalf("H1 = %v (%s)", res.Lanes["H1"], res.Note)
	}
	if res.TopProcess != "w3wp.exe" {
		t.Fatalf("Step 9 top process = %q, want w3wp.exe", res.TopProcess)
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
