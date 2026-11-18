//go:build windows

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Perf harness (owner finding 2026-11-18: the recorder spends ~0.8 CPU-s per
// 15 s tick). It builds the same chain the service builds (RunHost: OSCollector,
// resolver, change gate, BuildGroups, group collector, store, recorder) against
// the REAL Windows APIs and times every stage with wall time AND process CPU
// time (kernel+user, so GC and runtime helper threads count).
//
// It is off by default (it samples the real machine for ~30 s):
//
//	UNRAVIS_PERF=1 go test ./internal/blackbox/ -run TestPerfHarness -v -count=1
//
// UNRAVIS_PERF_TICKS (default 20) and UNRAVIS_PERF_GAP_MS (default 1500) tune it.
// Nothing is installed, elevated or written outside a temp folder.

func perfEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

var procQueryProcessCycleTime = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryProcessCycleTime")

// cyclesPerSec is calibrated once: process cycles burned by a busy loop divided
// by its wall time. GetProcessTimes only moves in ~15.6 ms steps, which is far
// too coarse to attribute a few milliseconds to a stage; the cycle counter is
// exact, and counts every thread of the process (GC and runtime helpers too).
var cyclesPerSec float64

func rawCycles() uint64 {
	var c uint64
	r, _, _ := procQueryProcessCycleTime.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		return 0
	}
	return c
}

func calibrateCycles() {
	if cyclesPerSec != 0 {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c0, t0 := rawCycles(), time.Now()
	x := 1.0
	for time.Since(t0) < 150*time.Millisecond {
		for i := 0; i < 10000; i++ {
			x = x*1.0000001 + 1e-9
		}
	}
	_ = x
	cyclesPerSec = float64(rawCycles()-c0) / time.Since(t0).Seconds()
}

// selfCPU is this process's CPU time so far, from the cycle counter when it is
// available (precise), else from kernel+user time (15.6 ms steps).
func selfCPU() time.Duration {
	if cyclesPerSec > 0 {
		return time.Duration(float64(rawCycles()) / cyclesPerSec * float64(time.Second))
	}
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return 0
	}
	return time.Duration(filetimeTicks(k)+filetimeTicks(u)) * 100
}

type stageStat struct {
	wall, cpu time.Duration
	n         int
	maxWall   time.Duration
}

type stageClock struct {
	order []string
	stats map[string]*stageStat
}

func newStageClock() *stageClock { return &stageClock{stats: map[string]*stageStat{}} }

// time runs fn and charges its wall and CPU time to the named stage.
func (c *stageClock) time(name string, fn func()) {
	w0, c0 := time.Now(), selfCPU()
	fn()
	w, cp := time.Since(w0), selfCPU()-c0
	st := c.stats[name]
	if st == nil {
		st = &stageStat{}
		c.stats[name] = st
		c.order = append(c.order, name)
	}
	st.wall += w
	st.cpu += cp
	st.n++
	if w > st.maxWall {
		st.maxWall = w
	}
}

func (c *stageClock) report(t *testing.T, title string) {
	t.Helper()
	var total time.Duration
	rows := ""
	names := append([]string(nil), c.order...)
	sort.SliceStable(names, func(i, j int) bool { return c.stats[names[i]].cpu > c.stats[names[j]].cpu })
	for _, n := range names {
		s := c.stats[n]
		total += s.cpu
		rows += fmt.Sprintf("  %-28s cpu %8.2f ms/tick  wall %8.2f ms/tick  max wall %7.1f ms  (n=%d)\n",
			n, msOf(s.cpu)/float64(s.n), msOf(s.wall)/float64(s.n), msOf(s.maxWall), s.n)
	}
	t.Logf("%s\n%s  sum of stage cpu: %.2f ms per tick (target 150)", title, rows, msOf(total)/float64(c.stats[c.order[0]].n))
}

func msOf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// perfChain is the production chain without the status server.
type perfChain struct {
	cfg    Config
	hc     *HostCollector
	gate   *processChangeGate
	groups []Group
	gc     Collector
	rec    *Recorder
	store  *Store
	budget *Budget
}

func buildPerfChain(t *testing.T) *perfChain {
	t.Helper()
	cfg := Default()
	cfg.Name = "perf"
	cfg.Folder = filepath.Join(t.TempDir(), "rec")
	if err := os.MkdirAll(cfg.Folder, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(cfg.Folder, StoreOptions{Name: cfg.Name, Version: "perf", Host: "perf",
		IntervalSeconds: cfg.IntervalSeconds, RunID: "perf"})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := OSCollector(cfg.Record.Processes)
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver()
	wireResolverSnapshot(resolver, hc)
	gate := &processChangeGate{resolver: resolver, targets: cfg.Targets}
	gate.view.Store(resolver.resolveTick(cfg.Targets, time.Now()))
	deps := GroupDeps{Config: cfg, Cores: CoreCount(), Generation: func() uint64 { return gate.currentView().gen },
		SQLProcessSig: func() uint64 { return gate.currentView().sqlSig }}
	hc.SetSelectedTargets(deps.Targets)
	groups := BuildGroups(deps)
	budget := NewBudget(cfg.IntervalSeconds)
	gate.inner = NewGroupCollector(hc, groups, budget)
	rec := NewRecorder(RecorderOptions{Config: cfg, Store: store, Collector: gate, Disk: OSDisk{}, RunID: "perf",
		Budget: budget, SelfUsage: SelfUsage})
	return &perfChain{cfg: cfg, hc: hc, gate: gate, groups: groups, gc: gate, rec: rec, store: store, budget: budget}
}

func TestPerfHarnessTicks(t *testing.T) {
	if os.Getenv("UNRAVIS_PERF") == "" {
		t.Skip("set UNRAVIS_PERF=1 to profile the real machine")
	}
	ticks := perfEnvInt("UNRAVIS_PERF_TICKS", 20)
	gap := time.Duration(perfEnvInt("UNRAVIS_PERF_GAP_MS", 1500)) * time.Millisecond
	// The same process-wide settings RunHost applies.
	if os.Getenv("UNRAVIS_PERF_NO_RUNTIME_TUNING") == "" {
		_ = SetBelowNormalPriority()
		prevLimit := debug.SetMemoryLimit(hostMemoryLimit)
		prevGC := debug.SetGCPercent(hostGCPercent)
		defer func() { debug.SetMemoryLimit(prevLimit); debug.SetGCPercent(prevGC) }()
	}
	calibrateCycles()
	ch := buildPerfChain(t)
	defer ch.gc.Close()
	t.Logf("cores=%d groups=%d  cpu clock: process cycle counter at %.2f GHz", runtime.NumCPU(), len(ch.groups), cyclesPerSec/1e9)
	gc0 := gcCPU()

	// Per-stage pass: the stages the recorder runs, one by one.
	clk := newStageClock()
	hc := ch.hc
	var prev time.Time
	for i := 0; i < ticks; i++ {
		now := time.Now().UTC()
		var dt float64
		if !prev.IsZero() {
			dt = now.Sub(prev).Seconds()
		}
		prev = now
		clk.time("self.SelfUsage", func() { SelfUsage() })
		clk.time("resolveTick", func() { ch.gate.view.Store(ch.gate.resolver.resolveTick(ch.gate.targets, now)) })
		clk.time("host.Host(cpu,mem)", func() { hc.src.Host() })
		clk.time("host.DiskQueue+Latency", func() { hc.extras.DiskQueue(); hc.extras.DiskLatencyMs() })
		clk.time("host.Network", func() { hc.extras.Network() })
		clk.time("host.src.Processes", func() { hc.procs, _ = hc.src.Processes(hc.procs[:0]) })
		clk.time("host.topProcesses+remember", func() {
			if i == 0 {
				hc.remember()
				hc.prevProcAt, hc.havePrev = now, true
				return
			}
			hc.topProcesses(dt)
			hc.remember()
		})
		for _, g := range ch.groups {
			g := g
			clk.time("group."+g.Name(), func() { g.Collect(now, dt) })
		}
		time.Sleep(gap)
	}
	clk.report(t, "per-stage (direct calls, serial, includes GC/runtime CPU of the process)")

	// End-to-end pass: Recorder.Tick, exactly the service path (goroutine per
	// group, budget, store append, manifest rewrite, fsync).
	e2e := newStageClock()
	for i := 0; i < ticks; i++ {
		e2e.time("Recorder.Tick (total)", func() { ch.rec.Tick(time.Now()) })
		time.Sleep(gap)
	}
	e2e.report(t, "end-to-end Recorder.Tick")
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("runtime: gc cpu total %.1f ms over %d ticks (%.2f ms/tick), NumGC=%d, HeapInuse=%.1f MiB, Sys=%.1f MiB",
		(gcCPU()-gc0)*1000, 2*ticks, (gcCPU()-gc0)*1000/float64(2*ticks), ms.NumGC, float64(ms.HeapInuse)/(1<<20), float64(ms.Sys)/(1<<20))
	if p := len(ch.hc.procs); p > 0 {
		withTimes := 0
		for i := range ch.hc.procs {
			if ch.hc.procs[i].Have != 0 {
				withTimes++
			}
		}
		t.Logf("processes listed %d, readable %d", p, withTimes)
	}
	ch.store.Close("stopped")
}

// gcCPU is the Go runtime GC CPU seconds so far (all GC worker threads).
func gcCPU() float64 {
	s := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindFloat64 {
		return s[0].Value.Float64()
	}
	return 0
}
