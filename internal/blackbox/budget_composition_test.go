package blackbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

// W3-07 composition footprint gate.
//
// The micro-benchmark in budget_bench_test.go drives six substitute groups over
// a fixed ~200-reading fixture with the host process list disabled, in
// collect-then-cycle order. It is NOT a measurement of the production path. This
// file drives the REAL composition instead:
//
//   - the real Recorder.Tick (which feeds/cycles the budget BEFORE collect, the
//     production ordering) against a real Store in a temp dir, with real
//     housekeeping and the real log catalogue over fake log files;
//   - the real BuildGroups registry over an injected fake CounterSource and a
//     fake resolver snapshot (one immutable per-tick view, monotonic generation,
//     lossless StartTicks identity);
//   - the real host process list (HostCollector with processes=true) over a fake
//     sampler.Source, including the selected-target rows;
//   - the real workerSlot deadlines, trackedGroup generation acks, Engine
//     Reconcile/Volatile baselines, and Store.Append/Housekeep.
//
// It asserts bounded RETAINED heap and allocated BYTES per tick (not allocation
// count alone), the documented 1 s per-group bound (no group marked slow), and
// that goroutines do not grow across many ticks. On Windows it also measures the
// real process working set and CPU via the production SelfUsage twin and asserts
// the documented 50 MiB / 0.5%-of-one-core limits; off Windows those two
// assertions are skipped honestly (no Go-heap substitute).
//
// Honest scope limits (also in the report):
//   - newDiskCPUGroup and newEventsGroup have no GroupDeps.Source seam and would
//     open a live PDH query / wevtapi handle, so their registry entries are
//     substituted with factories that build the SAME production group structs
//     (diskCPUGroup / eventsGroup) over injected fakes. Their Collect/Reconcile/
//     cursor logic is exercised; the factory's native-open call is not. Every
//     other factory is the real production factory.
//   - tcpGroup.counts (native TCP table) and dumpsGroup.scan (real dump roots)
//     are injected post-build so no live table/root is read.
//   - sqlHost discovery/source seams are faked (no PDH object enumeration).
//   - This still cannot prove live CPU/working-set/handles on a real server under
//     representative load; that needs owner-authorized live evidence.

const (
	compInterval = 15  // base sampling interval, seconds (matches the budget ladder floor)
	compWarmup   = 20  // ticks to prime rate baselines and fill caches before measuring
	compMeasured = 220 // measured ticks after warmup (240 total ~ one simulated hour)

	// compMaxBytesPerTick bounds average heap BYTES allocated per tick. This is a
	// test bound (not a documented budget number); the measured figure is ~68 KiB
	// per tick, and the limit keeps ~3.8x headroom so a cardinality/scan
	// regression that balloons allocations fails the gate. See the report.
	compMaxBytesPerTick = 256 << 10
	// compMaxRetainedGrowth bounds retained (post-GC) heap growth between the end
	// of warmup and the end of the run: engines reuse history, the resolver prunes
	// retired identities, the manifest is capped, so retained state must be flat.
	// Measured growth is ~24 KiB; the limit keeps generous headroom.
	compMaxRetainedGrowth = 1 << 20
	// compMaxGoroutineGrowth bounds goroutine growth across the run. Each tick's
	// workers deliver within the tick, so the count must return to baseline.
	compMaxGoroutineGrowth = 4
	// compMaxTickWall is a generous wall-clock ceiling for one calm tick (the
	// documented CPU budget is 75 ms of CPU per 15 s tick; wall is a loose proxy
	// that still catches a tick that blocks on I/O or a stalled provider).
	compMaxTickWall = 75 * time.Millisecond
)

// --- injected fakes ---------------------------------------------------------

// compCounterSource is a deterministic CounterSource with the Paths/Refresh
// surface the wildcard groups use. Cumulative KindRate paths advance by a fixed
// step each Collect; "elapsed" paths track the fake clock so procIO identity stays
// confirmed; "invalid" paths report OK=false (missing, never 0).
type compCounterSource struct {
	mu        sync.Mutex
	paths     []string
	values    map[string]float64
	rateStep  map[string]float64
	invalid   map[string]bool
	elapsed   map[string]int64 // path -> creation unix seconds; value = now - start
	nowFn     func() time.Time
	refreshes int
	collectN  int
	closed    bool
}

func newCompCounterSource(nowFn func() time.Time) *compCounterSource {
	return &compCounterSource{
		values:   map[string]float64{},
		rateStep: map[string]float64{},
		invalid:  map[string]bool{},
		elapsed:  map[string]int64{},
		nowFn:    nowFn,
	}
}

func (s *compCounterSource) add(path string, v float64) {
	s.paths = append(s.paths, path)
	s.values[path] = v
}

func (s *compCounterSource) addRate(path string, start, step float64) {
	s.add(path, start)
	s.rateStep[path] = step
}

func (s *compCounterSource) addElapsed(path string, startUnix int64) {
	s.paths = append(s.paths, path)
	s.elapsed[path] = startUnix
}

func (s *compCounterSource) Collect() ([]CounterReading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collectN++
	out := make([]CounterReading, 0, len(s.paths))
	for _, p := range s.paths {
		v := s.values[p]
		if step := s.rateStep[p]; step != 0 {
			s.values[p] += step
			v = s.values[p]
		}
		if start, ok := s.elapsed[p]; ok {
			v = float64(s.nowFn().Unix() - start)
		}
		if s.invalid[p] {
			out = append(out, CounterReading{Path: p, Value: v, Status: 1, OK: false})
			continue
		}
		out = append(out, CounterReading{Path: p, Value: v, Status: 0, OK: true})
	}
	return out, nil
}

func (s *compCounterSource) Close() error            { s.closed = true; return nil }
func (s *compCounterSource) Paths() []string         { return s.paths }
func (s *compCounterSource) RefreshInstances() error { s.refreshes++; return nil }

// compExtras is a deterministic Extras (disk queue/latency, network totals).
type compExtras struct{ closed bool }

func (compExtras) DiskQueue() (float64, error)      { return 1.5, nil }
func (compExtras) DiskLatencyMs() (float64, error)  { return 4.5, nil }
func (compExtras) Network() (uint64, uint64, error) { return 100, 200, nil }
func (e *compExtras) Close() error                  { e.closed = true; return nil }

// compHostSource is a fake sampler.Source for the host process list. Host times
// advance so CPU% computes; process CPU seconds advance per read so rows carry a
// real rate. extra procs can be churned between ticks.
type compHostSource struct {
	mu     sync.Mutex
	cores  int
	base   []sampler.ProcSample
	extra  []sampler.ProcSample
	reads  uint64
	closed bool
}

func (s *compHostSource) Cores() int { return s.cores }

func (s *compHostSource) Host() (sampler.HostSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	n := s.reads
	return sampler.HostSample{
		Times:          sampler.HostTimes{Idle: 100 * n, Kernel: 150 * n, User: 50 * n},
		AvailableBytes: 8 << 30,
		CommittedBytes: 12 << 30,
	}, nil
}

func (s *compHostSource) Processes(dst []sampler.ProcSample) ([]sampler.ProcSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := float64(s.reads) * 0.5
	for i := range s.base {
		p := s.base[i]
		p.CPUSeconds = float64(p.PID) + step
		dst = append(dst, p)
	}
	for _, p := range s.extra {
		p.CPUSeconds = float64(p.PID) + step
		dst = append(dst, p)
	}
	return dst, nil
}

func (s *compHostSource) Close() error { s.closed = true; return nil }

func compProcSample(pid uint32, name string) sampler.ProcSample {
	return sampler.ProcSample{
		PID: pid, Image: name, Threads: 4,
		Have:  sampler.HaveTimes | sampler.HaveMemory | sampler.HaveHandles,
		Start: time.Unix(1000, 0), CPUSeconds: float64(pid),
		WorkingSet: 1 << 20, PrivateBytes: 1 << 19, Handles: 10,
	}
}

// compDisk is a fake DiskSpace with a fixed free percentage.
type compDisk struct{ pct float64 }

func (d compDisk) Free(string) (DiskInfo, error) {
	return DiskInfo{Free: uint64(d.pct), Total: 100}, nil
}

// compSelf is an injectable SelfUsage reading. ok=false models a missing reading.
type compSelf struct {
	mu    sync.Mutex
	rd    SelfReading
	ok    bool
	calls int
}

func (s *compSelf) set(rd SelfReading, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rd, s.ok = rd, ok
}

func (s *compSelf) read() (SelfReading, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.rd, s.ok
}

// compClock is a fake wall+steady clock advanced explicitly by the test.
type compClock struct {
	mu   sync.Mutex
	t    time.Time
	mono time.Duration
}

func (c *compClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *compClock) steady() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

func (c *compClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	c.mono += d
}

// compProcFixture is the fake process inventory the resolver snapshots. The test
// churns procs between ticks; snaps/loads count resolver work (W3-05 bounds).
type compProcFixture struct {
	mu    sync.Mutex
	procs []ProcInfo
	snaps int64
	loads int64
}

func (f *compProcFixture) snapshot() ([]ProcInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps++
	out := make([]ProcInfo, len(f.procs))
	copy(out, f.procs)
	return out, nil
}

func (f *compProcFixture) modules(pid uint32) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	switch pid {
	case 100:
		return []string{"clr.dll"}, nil // .NET Framework
	case 200:
		return []string{"coreclr.dll"}, nil // .NET 10
	default:
		return []string{"kernel32.dll", "ntdll.dll"}, nil
	}
}

func (f *compProcFixture) setProcs(p []ProcInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs = p
}

func compProcInfo(pid uint32, name string) ProcInfo {
	return ProcInfo{PID: pid, ParentPID: 4, Start: 1000, StartTicks: 1000*filetimeTicksPerSecond + filetimeUnixEpochDiff, Name: name}
}

// compStallGroup blocks until released: a stalled provider for the adverse case.
type compStallGroup struct {
	release chan struct{}
	entered chan struct{}
	calls   int64
	closed  bool
}

func (g *compStallGroup) Name() string   { return "compStall" }
func (g *compStallGroup) Optional() bool { return false }
func (g *compStallGroup) Collect(time.Time, float64) (any, error) {
	atomic.AddInt64(&g.calls, 1)
	if g.entered != nil {
		select {
		case g.entered <- struct{}{}:
		default:
		}
	}
	<-g.release
	return &TCPGroup{}, nil
}
func (g *compStallGroup) Close() error { g.closed = true; return nil }

// compLateGroup is the payload a recovered factory serves.
type compLateGroup struct{ closed bool }

func (g *compLateGroup) Name() string   { return "compLate" }
func (g *compLateGroup) Optional() bool { return true }
func (g *compLateGroup) Collect(time.Time, float64) (any, error) {
	return &TCPGroup{RetransmitsPerSec: fp64(1)}, nil
}
func (g *compLateGroup) Close() error { g.closed = true; return nil }

// --- rig --------------------------------------------------------------------

type compOpts struct {
	stall bool // register a permanently blocked optional group
	late  bool // register a factory that fails twice then recovers
}

type compRig struct {
	t        *testing.T
	cfg      Config
	clk      *compClock
	store    *Store
	rec      *Recorder
	budget   *Budget
	gate     *processChangeGate
	resolver *Resolver
	fixture  *compProcFixture
	src      *compCounterSource
	hostSrc  *compHostSource
	self     *compSelf
	stall    *compStallGroup
	late     *compLateGroup
	lateTry  int
	logDir   string
}

// compInner unwraps the trackedGroup wrapper BuildGroups adds when a Generation
// source is wired, so a test can reach the production group struct.
func compInner(g Group) Group {
	if tg, ok := g.(*trackedGroup); ok {
		return tg.inner
	}
	return g
}

// newCompRig wires the production composition exactly as RunHost does, over
// fakes. It does NOT call HostCollector.Prime: RunHost does not either, so the
// first sample establishes baselines just like production.
func newCompRig(t *testing.T, opts compOpts) *compRig {
	t.Helper()
	clk := &compClock{t: time.Unix(100000, 0)}
	r := &compRig{t: t, clk: clk, self: &compSelf{rd: rdCalm, ok: true}}

	storeDir := t.TempDir()
	r.logDir = t.TempDir()
	for _, n := range []string{"app.log", "iis.log"} {
		if err := os.WriteFile(filepath.Join(r.logDir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Default()
	cfg.Name = "comp"
	cfg.Folder = storeDir
	cfg.IntervalSeconds = compInterval
	cfg.KeepDays = 7
	cfg.MaxGB = 1
	cfg.MinFreePercent = 10
	cfg.Record.Processes = true
	cfg.Record.LogsCatalogue = true
	cfg.LogPaths = []string{r.logDir}
	cfg.Targets = []Target{{Kind: "appPool", Name: "PayrollPool"}, {Kind: "service", Name: "EmailSvc"}}
	r.cfg = cfg

	r.src = newCompCounterSource(clk.now)
	buildCompReadings(r.src)

	r.hostSrc = &compHostSource{cores: 4}
	r.hostSrc.base = append(r.hostSrc.base, compProcSample(100, "w3wp.exe"), compProcSample(200, "email.exe"))
	for i := 0; i < 30; i++ {
		r.hostSrc.base = append(r.hostSrc.base, compProcSample(uint32(1000+i), "bg.exe"))
	}

	r.fixture = &compProcFixture{}
	base := []ProcInfo{compProcInfo(100, "w3wp.exe"), compProcInfo(200, "email.exe")}
	for i := 0; i < 30; i++ {
		base = append(base, compProcInfo(uint32(1000+i), "bg.exe"))
	}
	r.fixture.setProcs(base)

	r.resolver = NewResolver()
	r.resolver.snapshotFn = r.fixture.snapshot
	r.resolver.loadModulesFn = r.fixture.modules
	r.resolver.instancesFn = func() ([]string, error) { return []string{"100_PayrollPool"}, nil }
	r.resolver.servicesFn = func() ([]svcProc, error) { return []svcProc{{name: "EmailSvc", pid: 200}}, nil }
	r.resolver.nowFn = clk.now

	// Substitute only the two factories with no source seam (would open live
	// PDH/wevtapi). They build the SAME production structs over injected fakes.
	registryMu.Lock()
	saved := registry
	registry = make(map[string]registration, len(saved))
	for k, v := range saved {
		registry[k] = v
	}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
	RegisterGroup("diskCpu", false, func(d GroupDeps) (Group, error) {
		return &diskCPUGroup{
			src:   r.src,
			eng:   NewEngine(diskCPUSpecs(r.src.Paths()), d.Cores),
			cores: d.Cores,
		}, nil
	})
	evSrc := &compEventSource{}
	RegisterGroup("events", true, func(GroupDeps) (Group, error) {
		return &eventsGroup{query: evSrc.query}, nil
	})

	if opts.stall {
		r.stall = &compStallGroup{release: make(chan struct{}), entered: make(chan struct{}, 1)}
		stall := r.stall
		RegisterGroup("compStall", true, func(GroupDeps) (Group, error) { return stall, nil })
	}
	if opts.late {
		r.late = &compLateGroup{}
		late := r.late
		r.lateTry = 0
		RegisterGroup("compLate", true, func(GroupDeps) (Group, error) {
			r.lateTry++
			if r.lateTry < 3 {
				return nil, errors.New("provider not ready")
			}
			return late, nil
		})
	}

	// sqlHost seams: fake discovery + source (no PDH object enumeration).
	sqlSrc := newCompCounterSource(clk.now)
	sqlPaths, _, _ := buildSQLHostTargets([]string{"SQLServer:"})
	for _, p := range sqlPaths {
		sqlSrc.add(p, 5)
	}
	savedDiscover := discoverSQLInstances
	savedNewSource := sqlHostNewSource
	discoverSQLInstances = func() ([]string, error) { return []string{"SQLServer:"}, nil }
	sqlHostNewSource = func([]string) (CounterSource, error) { return sqlSrc, nil }
	t.Cleanup(func() {
		discoverSQLInstances = savedDiscover
		sqlHostNewSource = savedNewSource
	})

	r.gate = &processChangeGate{resolver: r.resolver, targets: cfg.Targets}
	r.gate.view.Store(r.resolver.resolveTick(cfg.Targets, clk.now()))
	deps := GroupDeps{
		Source:     r.src,
		Config:     cfg,
		Cores:      4,
		Generation: func() uint64 { return r.gate.currentView().gen },
		Targets:    func() []ResolvedTarget { return r.gate.currentView().targets },
	}
	host := NewHostCollector(r.hostSrc, &compExtras{}, true)
	host.SetSelectedTargets(deps.Targets)
	groups := BuildGroups(deps)

	// Inject the native-side seams the factories cannot take via GroupDeps.
	for _, g := range groups {
		switch ig := compInner(g).(type) {
		case *tcpGroup:
			ig.counts = func() (tcpStates, error) {
				return tcpStates{counts: []TCPStateCount{{State: 5, Count: 7}, {State: 2, Count: 3}}}, nil
			}
		case *dumpsGroup:
			ig.scan = func([]string) []DumpEntry {
				return []DumpEntry{{Name: "w3wp.exe.1234.dmp", Size: 1024, MTime: clk.now().UTC(), Target: "w3wp"}}
			}
		}
	}

	r.budget = NewBudget(compInterval)
	r.gate.inner = NewGroupCollector(host, groups, r.budget)

	store, err := OpenStore(storeDir, StoreOptions{
		Name: cfg.Name, Version: "test", Host: "comp", IntervalSeconds: compInterval,
		RunID: "comp", Now: clk.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.store = store
	r.rec = NewRecorder(RecorderOptions{
		Config: cfg, Store: store, Collector: r.gate, Disk: compDisk{pct: 80}, RunID: "comp",
		Now: clk.now, Mono: clk.steady, Budget: r.budget, SelfUsage: r.self.read,
	})
	t.Cleanup(func() {
		_ = r.store.Close("test-end")
	})
	return r
}

// tick advances the fake clock one interval and runs the real Recorder.Tick.
func (r *compRig) tick() {
	r.clk.advance(compInterval * time.Second)
	r.rec.Tick(r.clk.now())
}

// tickBy advances the fake clock by d and runs the real Recorder.Tick.
func (r *compRig) tickBy(d time.Duration) {
	r.clk.advance(d)
	r.rec.Tick(r.clk.now())
}

// readLastSample reads the newest sample line back from the real Store, proving
// the write path produced it.
func (r *compRig) readLastSample() Sample {
	r.t.Helper()
	name := r.store.CurrentFile()
	if name == "" {
		r.t.Fatal("no current recording file")
	}
	data, err := os.ReadFile(filepath.Join(r.store.Dir(), name))
	if err != nil {
		r.t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var s Sample
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &s); err != nil {
		r.t.Fatalf("last line not a sample: %v", err)
	}
	return s
}

// buildCompReadings fills the shared fake source with expanded paths for every
// production group that takes GroupDeps.Source. Values are fixed English-path
// readings; cumulative rates climb; one memPressure path is invalid.
func buildCompReadings(s *compCounterSource) {
	// http: two app-pool queues, four counters each.
	for _, pool := range []string{"PayrollPool", "OtherPool"} {
		pre := `\HTTP Service Request Queues(` + pool + `)\`
		s.add(pre+"CurrentQueueSize", 3)
		s.add(pre+"MaxQueueItemAge", 2)
		s.addRate(pre+"RejectedRequests", 0, 0)
		s.addRate(pre+"ArrivalRate", 120, 0)
	}
	// iis: the selected pool's worker (gauges).
	s.add(`\W3SVC_W3WP(100_PayrollPool)\Active Requests`, 7)
	s.add(`\W3SVC_W3WP(100_PayrollPool)\Active Threads`, 9)
	s.add(`\WAS_W3WP(100_PayrollPool)\Ping Response Latency`, 2)
	// clr: selected Framework instance "w3wp" (PID 100) plus an unselected one.
	for _, inst := range []string{"w3wp", "w3wp#1"} {
		pid := 100.0
		if inst == "w3wp#1" {
			pid = 300
		}
		pre := `\.NET CLR Memory(` + inst + `)\`
		s.add(pre+"Process ID", pid)
		s.add(pre+"Gen 0 heap size", 1<<20)
		s.add(pre+"Gen 1 heap size", 2<<20)
		s.add(pre+"Gen 2 heap size", 3<<20)
		s.add(pre+"Large Object Heap size", 4<<20)
		s.add(pre+"% Time in GC", 4)
		s.addRate(pre+"Allocated Bytes/sec", 500000, 0)
		lp := `\.NET CLR LocksAndThreads(` + inst + `)\`
		s.addRate(lp+"Contention Rate/sec", 1, 0)
		s.add(lp+"# of current physical threads", 12)
		ep := `\.NET CLR Exceptions(` + inst + `)\`
		s.addRate(ep+"# of Exceps Thrown/sec", 2, 0)
	}
	// Process-object identity counters (ID Process + Elapsed Time) for BOTH
	// selected PIDs. The per-process groups (clr/iis/proc10/procIO) confirm
	// (PID, creation FILETIME) against the shared view's unix-second Start
	// (W5-01/W5-04); the start is derived from Elapsed Time (tick clock minus it).
	s.add(`\Process(w3wp)\ID Process`, 100)
	s.addElapsed(`\Process(w3wp)\Elapsed Time`, 1000)
	s.add(`\Process(email)\ID Process`, 200)
	// proc10 + procIO detail for the selected .NET 10 instance "email" (PID 200).
	s.addRate(`\Process(email)\% Processor Time`, 10, 0)
	s.add(`\Process(email)\Working Set`, 50<<20)
	s.add(`\Process(email)\Private Bytes`, 60<<20)
	s.addRate(`\Process(email)\IO Read Bytes/sec`, 1000, 0)
	s.addRate(`\Process(email)\IO Write Bytes/sec`, 2000, 0)
	s.add(`\Process(email)\Page File Bytes`, 70<<20)
	s.add(`\Process(email)\Virtual Bytes`, 80<<20)
	s.addElapsed(`\Process(email)\Elapsed Time`, 1000)
	// tcp: both families plus one interface queue. Connections Reset is cumulative.
	s.addRate(`\TCPv4\Segments Retransmitted/sec`, 1.5, 0)
	s.addRate(`\TCPv6\Segments Retransmitted/sec`, 2.5, 0)
	s.addRate(`\TCPv4\Connections Reset`, 100, 7.5)
	s.addRate(`\TCPv6\Connections Reset`, 200, 7.5)
	s.add(`\Network Interface(eth0)\Output Queue Length`, 2)
	// memPressure: Pool Nonpaged Bytes is INVALID (missing, never 0).
	s.addRate(`\Memory\Pages Input/sec`, 6, 0)
	s.add(`\Memory\Pool Paged Bytes`, 300<<20)
	s.add(`\Memory\Pool Nonpaged Bytes`, 4096)
	s.invalid[`\Memory\Pool Nonpaged Bytes`] = true
	s.add(`\Paging File(_Total)\% Usage`, 12)
	// vm: VMware balloon, 512 MB. Without this seed the shared source has no
	// reading and the vm group would sit in Missing.
	s.add(`\VM Memory\Memory Ballooned in MB`, 512)
	// aspnet: fixed paths.
	s.add(`\ASP.NET\Requests Queued`, 1)
	s.addRate(`\ASP.NET\Requests Rejected`, 0, 0)
	s.add(`\ASP.NET\Request Wait Time`, 3)
	s.add(`\ASP.NET\Application Restarts`, 0)
	s.add(`\ASP.NET Applications(__Total__)\Requests Current`, 5)
	// diskCpu: one physical disk plus the processor aggregates.
	dpre := `\PhysicalDisk(0 C:)\`
	s.add(dpre+"Avg. Disk sec/Read", 0.001)
	s.add(dpre+"Avg. Disk sec/Write", 0.002)
	s.addRate(dpre+"Disk Reads/sec", 10, 0)
	s.addRate(dpre+"Disk Writes/sec", 20, 0)
	s.addRate(dpre+"Split IO/Sec", 1, 0)
	s.add(`\System\Processor Queue Length`, 1)
	s.addRate(`\Processor(_Total)\% Privileged Time`, 8, 0)
	s.addRate(`\Processor(_Total)\% DPC Time`, 1, 0)
	s.addRate(`\Processor(_Total)\% Interrupt Time`, 0.5, 0)
}

// compEventSource is a fake per-channel event query: one new record per call so
// the forward cursor and the Store.Append ack are exercised without wevtapi.
type compEventSource struct {
	mu sync.Mutex
	n  uint64
}

func (f *compEventSource) query(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if budget <= 0 {
		return eventChannelRead{lastID: afterID}, nil
	}
	f.n++
	id := f.n
	if id <= afterID {
		id = afterID + 1
	}
	return eventChannelRead{
		rows:      []rawEvent{{Channel: channel, EventID: 51, Level: 3, T: floor, RecordID: id}},
		lastID:    id,
		attempted: 1,
	}, nil
}

// --- the composition gate ---------------------------------------------------

func TestCompositionFootprintGate(t *testing.T) {
	r := newCompRig(t, compOpts{})

	for i := 0; i < compWarmup; i++ {
		r.tick()
	}
	runtime.GC()
	var base, before runtime.MemStats
	runtime.ReadMemStats(&base)
	before = base
	baseGoroutines := runtime.NumGoroutine()

	// Real process self-stat baseline (Windows only; ok=false elsewhere). The
	// production SelfUsage twin reads the current process; the first call primes
	// its delta baseline.
	wallStart := time.Now()
	_, selfOK0 := SelfUsage()

	start := time.Now()
	var maxTick time.Duration
	for i := 0; i < compMeasured; i++ {
		t0 := time.Now()
		r.tick()
		if d := time.Since(t0); d > maxTick {
			maxTick = d
		}
	}
	elapsed := time.Since(start)
	selfRd, selfOK1 := SelfUsage()
	wallWindow := time.Since(wallStart)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	endGoroutines := runtime.NumGoroutine()

	bytesPerTick := (after.TotalAlloc - before.TotalAlloc) / compMeasured
	allocsPerTick := (after.Mallocs - before.Mallocs) / compMeasured
	retainedGrowth := int64(after.HeapAlloc) - int64(base.HeapAlloc)
	avgTick := elapsed / compMeasured

	t.Logf("composition: %d ticks, avg %v/tick, max %v/tick, %d allocs/tick, %d bytes/tick",
		compMeasured, avgTick, maxTick, allocsPerTick, bytesPerTick)
	t.Logf("retained heap growth %d bytes (base %d, after %d); goroutines %d -> %d",
		retainedGrowth, base.HeapAlloc, after.HeapAlloc, baseGoroutines, endGoroutines)

	// Allocated BYTES per tick (not just count): a cardinality/scan regression
	// that balloons allocations must fail even if the count looks similar.
	if bytesPerTick > compMaxBytesPerTick {
		t.Errorf("%d bytes/tick exceeds the gate bound %d", bytesPerTick, compMaxBytesPerTick)
	}
	// Retained state must be flat across the run.
	if retainedGrowth > compMaxRetainedGrowth {
		t.Errorf("retained heap grew %d bytes over the run; bound %d", retainedGrowth, compMaxRetainedGrowth)
	}
	// Goroutines must not grow across many ticks.
	if endGoroutines > baseGoroutines+compMaxGoroutineGrowth {
		t.Errorf("goroutines grew %d -> %d over %d ticks", baseGoroutines, endGoroutines, compMeasured)
	}
	// Per-group duration bound: the real groupCollector marks any group over
	// budgetMaxGroup (1 s) slow. A calm composition must mark none.
	if slow := r.budget.SlowGroups(); len(slow) != 0 {
		t.Errorf("groups over the 1 s per-group budget: %v", slow)
	}
	// Loose wall proxy for the 75 ms/tick CPU budget (real CPU asserted below on
	// Windows). A calm tick must not block.
	if avgTick > compMaxTickWall {
		t.Errorf("average tick wall %v exceeds %v", avgTick, compMaxTickWall)
	}

	// The whole run wrote one sample per tick through the real Store.
	m := r.store.Manifest()
	if got := m.SampleCount(); got != compWarmup+compMeasured {
		t.Errorf("store wrote %d samples, want %d", got, compWarmup+compMeasured)
	}

	// Read back the final sample: every production group field landed, the host
	// process list is present, and the invalid reading is absent (never 0).
	last := r.readLastSample()
	if last.HTTP == nil || last.IIS == nil || last.DiskCPU == nil || last.CLR == nil ||
		last.Proc10 == nil || last.ProcIO == nil || last.TCP == nil || last.MemPressure == nil ||
		last.ASPNET == nil || last.SQLHost == nil {
		t.Fatalf("a production group field is absent: missing=%v", last.Missing)
	}
	if len(last.Procs) == 0 {
		t.Error("host process list absent (Record.Processes=true)")
	}
	if last.VM == nil || last.VM.BalloonBytes == nil || *last.VM.BalloonBytes != 536870912 || last.VM.StealPct != nil {
		t.Errorf("vm group did not record and read back balloonBytes 536870912: %+v (missing=%v)", last.VM, last.Missing)
	}
	if missingHas(last, "vm") {
		t.Errorf("vm is in Missing although it was seeded: %v", last.Missing)
	}
	if last.MemPressure.PoolNonPagedBytes != nil {
		t.Error("invalid reading emitted poolNonPagedBytes; missing must never be 0")
	}
	if last.MemPressure.PoolPagedBytes == nil {
		t.Error("valid gauge poolPagedBytes absent")
	}
	// The selected targets resolved and produced per-process rows.
	if len(last.CLR.Targets) == 0 || last.CLR.Targets[0].Target != "pool:PayrollPool" {
		t.Errorf("CLR target rows = %+v, want pool:PayrollPool", last.CLR.Targets)
	}
	if len(last.Proc10.Targets) == 0 || last.Proc10.Targets[0].Target != "svc:EmailSvc" {
		t.Errorf("proc10 target rows = %+v, want svc:EmailSvc", last.Proc10.Targets)
	}

	// The log catalogue path ran (first tick catalogues; fake log files exist).
	if !compAnySampleHasLogs(r) {
		t.Error("log catalogue never produced entries (Record.LogsCatalogue=true)")
	}

	// Resolver work is bounded: one snapshot per tick (W3-05), and the runtime
	// cache never exceeds the live process set (retired identities are pruned).
	if got := r.fixture.snaps; got != int64(compWarmup+compMeasured)+1 {
		t.Errorf("resolver took %d snapshots, want one per tick plus the seed (%d)", got, compWarmup+compMeasured+1)
	}
	r.fixture.mu.Lock()
	live := len(r.fixture.procs)
	r.fixture.mu.Unlock()
	r.resolver.mu.Lock()
	cached := len(r.resolver.cached)
	r.resolver.mu.Unlock()
	if cached > live {
		t.Errorf("resolver cached %d identities for %d live processes (cache must be pruned)", cached, live)
	}

	// Windows-only: real process working set and CPU via the production self-stat
	// twin. The first call primes the delta baseline (ok=false is expected); the
	// second call is the measurement. Off Windows these are skipped honestly — no
	// Go-heap substitute.
	if runtime.GOOS == "windows" && selfOK1 {
		if selfRd.HeapInUse > budgetMaxHeap {
			t.Errorf("Go heap in use %d bytes exceeds the budget %d", selfRd.HeapInUse, uint64(budgetMaxHeap))
		}
		if selfRd.Private > budgetMaxPrivate {
			t.Errorf("private bytes %d exceed the budget %d", selfRd.Private, uint64(budgetMaxPrivate))
		}
		// CPU per tick = average percent of one core over the window * window /
		// ticks. The documented budget is 0.5% of one core averaged over 15 s,
		// i.e. 75 ms of CPU per tick.
		cpuPct := selfRd.CPUPct
		cpuSeconds := cpuPct / 100 * wallWindow.Seconds()
		cpuPerTick := time.Duration(cpuSeconds / float64(compMeasured) * float64(time.Second))
		t.Logf("windows self-stat: heap %d bytes, private %d bytes, cpu %.3f%% of one core over %v => %v CPU/tick",
			selfRd.HeapInUse, selfRd.Private, cpuPct, wallWindow, cpuPerTick)
		if cpuPerTick > 75*time.Millisecond {
			t.Errorf("CPU per tick %v exceeds the 75 ms budget (0.5%% of one core at 15 s)", cpuPerTick)
		}
	} else {
		t.Logf("working-set/CPU self-measurement unavailable on %s (ok0=%v ok1=%v); skipping those assertions honestly",
			runtime.GOOS, selfOK0, selfOK1)
	}
}

// compAnySampleHasLogs reports whether any written sample carries log catalogue
// entries (the catalogue runs on the first tick and every 5 min).
func compAnySampleHasLogs(r *compRig) bool {
	name := r.store.CurrentFile()
	data, err := os.ReadFile(filepath.Join(r.store.Dir(), name))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var s Sample
		if json.Unmarshal([]byte(line), &s) == nil && len(s.Logs) > 0 {
			return true
		}
	}
	return false
}

// --- adverse cases ----------------------------------------------------------

// TestCompositionStalledSourceBounded proves a blocked provider costs at most one
// group budget per tick through the REAL recorder, the other groups still run,
// goroutines do not grow, and Close skips a source whose worker is still inside.
func TestCompositionStalledSourceBounded(t *testing.T) {
	r := newCompRig(t, compOpts{stall: true})
	defer close(r.stall.release)

	// First tick: the stalled worker hits the 1 s deadline; the tick is bounded
	// and the healthy groups still land.
	t0 := time.Now()
	r.tick()
	first := time.Since(t0)
	if first > 3*budgetMaxGroup {
		t.Fatalf("first tick took %v with a stalled source, want about one group budget", first)
	}
	s := r.readLastSample()
	if !missingHas(s, "compStall: slow") {
		t.Fatalf("Missing = %v, want compStall: slow", s.Missing)
	}
	if s.HTTP == nil || s.MemPressure == nil {
		t.Fatalf("healthy groups stopped while another stalled: missing=%v", s.Missing)
	}

	base := runtime.NumGoroutine()
	for i := 0; i < 40; i++ {
		t0 := time.Now()
		r.tick()
		if d := time.Since(t0); d > 3*budgetMaxGroup {
			t.Fatalf("tick %d took %v; a busy slot must not re-block", i, d)
		}
		s := r.readLastSample()
		// From the tick after the timeout the still-outstanding worker is reported
		// as busy (it is not timed again, so it is never put on probation).
		if !missingHas(s, "compStall: busy") {
			t.Fatalf("tick %d Missing = %v, want compStall: busy", i, s.Missing)
		}
		// The mandatory siblings keep running while the stalled provider is busy.
		// (Optional groups are correctly skipped by the budget after a slow group.)
		if s.MemPressure == nil || s.DiskCPU == nil {
			t.Fatalf("tick %d: mandatory groups stopped: missing=%v", i, s.Missing)
		}
	}
	if got := runtime.NumGoroutine(); got > base+compMaxGoroutineGrowth {
		t.Errorf("goroutines grew %d -> %d over 40 stalled ticks", base, got)
	}
	if got := atomic.LoadInt64(&r.stall.calls); got != 1 {
		t.Errorf("stalled source entered %d times, want exactly 1 (one outstanding worker)", got)
	}

	// Close while the worker is still inside the source: it must NOT close it
	// (use-after-free guard) and must stay bounded by the grace period.
	cstart := time.Now()
	if err := r.gate.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(cstart); d > groupCloseGrace+2*time.Second {
		t.Fatalf("Close took %v, want bounded by the grace period", d)
	}
	if r.stall.closed {
		t.Error("Close closed a source whose worker is still inside it")
	}
}

// TestCompositionProcessChurnStableRates proves continuous process churn keeps
// machine-wide rates alive (Reconcile retains non-volatile baselines), advances
// the generation, prunes the resolver cache, and does not grow goroutines.
func TestCompositionProcessChurnStableRates(t *testing.T) {
	r := newCompRig(t, compOpts{})
	for i := 0; i < 5; i++ {
		r.tick() // prime rate baselines
	}
	base := runtime.NumGoroutine()
	gen0 := r.gate.currentView().gen

	nextPID := uint32(2000)
	for i := 0; i < 60; i++ {
		// Churn the background process set every tick; keep the selected targets.
		procs := []ProcInfo{compProcInfo(100, "w3wp.exe"), compProcInfo(200, "email.exe")}
		for j := 0; j < 30; j++ {
			procs = append(procs, compProcInfo(uint32(1000+j), "bg.exe"))
		}
		procs = append(procs, compProcInfo(nextPID, "transient.exe"))
		nextPID++
		r.fixture.setProcs(procs)
		// Churn the host list too, so topProcesses re-ranks.
		r.hostSrc.mu.Lock()
		r.hostSrc.extra = []sampler.ProcSample{compProcSample(nextPID, "transient.exe")}
		r.hostSrc.mu.Unlock()

		r.tick()
		s := r.readLastSample()
		// Non-volatile machine-wide rates survive unrelated churn (W1-02).
		if s.TCP == nil || s.TCP.RetransmitsPerSec == nil {
			t.Fatalf("tick %d: tcp retransmit rate starved by churn: missing=%v", i, s.Missing)
		}
		if s.MemPressure == nil || s.MemPressure.HardFaultsPerSec == nil {
			t.Fatalf("tick %d: memPressure rate starved by churn: missing=%v", i, s.Missing)
		}
		if s.DiskCPU == nil || s.DiskCPU.CPU == nil || s.DiskCPU.CPU.PrivilegedPct == nil {
			t.Fatalf("tick %d: diskCpu rate starved by churn: missing=%v", i, s.Missing)
		}
		// The selected targets keep resolving.
		if s.IIS == nil || len(s.IIS.Workers) == 0 {
			t.Fatalf("tick %d: selected pool worker lost under churn: missing=%v", i, s.Missing)
		}
	}
	if got := runtime.NumGoroutine(); got > base+compMaxGoroutineGrowth {
		t.Errorf("goroutines grew %d -> %d over 60 churn ticks", base, got)
	}
	if r.gate.currentView().gen <= gen0 {
		t.Errorf("generation did not advance under churn: %d -> %d", gen0, r.gate.currentView().gen)
	}
	r.fixture.mu.Lock()
	live := len(r.fixture.procs)
	r.fixture.mu.Unlock()
	r.resolver.mu.Lock()
	cached := len(r.resolver.cached)
	r.resolver.mu.Unlock()
	if cached > live {
		t.Errorf("resolver cache %d exceeds live set %d under churn (must prune)", cached, live)
	}
	if slow := r.budget.SlowGroups(); len(slow) != 0 {
		t.Errorf("groups marked slow under churn: %v", slow)
	}
}

// TestCompositionFailedFactoryRecovers proves a factory that fails at startup is
// retried on the bounded backoff and recovers without restarting the recorder,
// through the real BuildGroups lazyGroup slot and Recorder.Tick.
func TestCompositionFailedFactoryRecovers(t *testing.T) {
	r := newCompRig(t, compOpts{late: true})

	// While unavailable the group is named in Missing, never zero-filled.
	// BuildGroups calls the factory once eagerly (attempt 1, fails -> lazyGroup
	// retry slot); the first tick's Collect is attempt 2.
	r.tick()
	if s := r.readLastSample(); !missingHas(s, "compLate") {
		t.Fatalf("first tick Missing = %v, want compLate named", s.Missing)
	}
	if r.lateTry != 2 {
		t.Fatalf("factory attempts = %d after first tick, want 2 (build + first collect)", r.lateTry)
	}

	// Drive past the bounded backoff windows; the factory recovers on attempt 3.
	for i := 0; i < 12; i++ {
		r.tick()
	}
	if r.lateTry != 3 {
		t.Fatalf("factory attempts = %d, want 3 (bounded retry then recovery)", r.lateTry)
	}
	if s := r.readLastSample(); missingHas(s, "compLate") {
		t.Fatalf("recovered group still named missing: %v", s.Missing)
	}
	// Once built, the real group serves every later tick without re-running the
	// factory.
	for i := 0; i < 3; i++ {
		r.tick()
	}
	if r.lateTry != 3 {
		t.Errorf("factory re-ran after recovery: attempts = %d", r.lateTry)
	}
	if s := r.readLastSample(); missingHas(s, "compLate") {
		t.Errorf("recovered group went missing again: %v", s.Missing)
	}
}

// TestCompositionProbationAndMissingReadings drives the real Recorder.Tick: an
// optional group that was slow twice is named "tcp: slow" and sits out for the
// time box; a missing self reading is calm (it never widens the interval); when
// the probation has passed the group is tried again and a fast run clears it.
func TestCompositionProbationAndMissingReadings(t *testing.T) {
	r := newCompRig(t, compOpts{})
	r.tick()
	r.tick()
	r.budget.GroupTiming("tcp", 2*time.Second, true)
	r.budget.GroupTiming("tcp", 2*time.Second, true)

	r.self.set(SelfReading{}, false)
	for i := 0; i < 12; i++ {
		r.tick()
		if s := r.readLastSample(); !missingHas(s, "tcp: slow") {
			t.Fatalf("tick %d: optional group ran during its probation: missing=%v", i, s.Missing)
		}
	}
	if got := r.budget.EffectiveInterval(); got != compInterval {
		t.Fatalf("missing readings moved the interval to %d", got)
	}

	// The probation is 10 minutes; run past it.
	for i := 0; i < int(budgetProbation/(compInterval*time.Second))+2; i++ {
		r.tick()
	}
	if s := r.readLastSample(); missingHas(s, "tcp: slow") || missingHas(s, "tcp") {
		t.Fatalf("group still skipped after the probation: missing=%v", s.Missing)
	}
	if slow := r.budget.SlowGroups(); len(slow) != 0 {
		t.Fatalf("a fast run must clear the probation: %v", slow)
	}
}

// TestCompositionOverReadingWidensThenSteps: an over-budget self reading widens
// the real recorder's interval after two windows; calm readings step it back
// after five.
func TestCompositionOverReadingWidensThenSteps(t *testing.T) {
	r := newCompRig(t, compOpts{})
	r.self.set(rdOver, true)
	for i := 0; i < budgetWarmupCycles+budgetWindowTicks*budgetOverWindows; i++ {
		r.tick()
	}
	if got := r.budget.EffectiveInterval(); got != 2*compInterval {
		t.Fatalf("effective interval = %d, want %d after two over windows", got, 2*compInterval)
	}
	r.self.set(rdCalm, true)
	for i := 0; i < budgetWindowTicks*budgetCalmWindows+1; i++ {
		r.tickBy(30 * time.Second)
	}
	if got := r.budget.EffectiveInterval(); got != compInterval {
		t.Fatalf("effective interval = %d, want back at %d after calm windows", got, compInterval)
	}
}
