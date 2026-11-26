package blackbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// Go runtime settings of the service (performance review PERF-05): a soft limit
// on the Go heap and a GC that runs at 40 percent growth instead of 100.
const (
	hostMemoryLimit = 64 << 20
	hostGCPercent   = 40
)

// HostOptions wire one run of the recorder: store, sampler loop and status
// server. The service and the foreground command both call RunHost. Zero values
// are the real thing; tests replace the collector, listener, clock and ticks.
type HostOptions struct {
	Config  Config
	KeyPath string
	Version string

	Collector Collector
	Disk      DiskSpace
	Listener  net.Listener
	Now       func() time.Time
	Ticks     <-chan time.Time

	// Log receives one line per notable thing. Never a key, never a password.
	Log func(format string, a ...any)
	// Update reports the self-update state in /status (nil: not reported).
	Update func() *UpdateStatus
	// Ready is called once the status server listens, with its address.
	Ready func(addr string)

	// CrashCapture turns on the local crash record (RV-BB-10): a panic or fatal
	// error anywhere in the process leaves a crash file in the crash folder, and the
	// next start reports it in the recording. Off by default: it takes the one
	// process-wide crash output, which a test must not.
	CrashCapture bool

	// ListenRetry is how long to wait before trying a busy port again.
	ListenRetry time.Duration
}

// ErrNoReadKey is returned when the key hash file is missing: a recorder without
// a read key would answer nobody, so it refuses to start instead.
var ErrNoReadKey = errors.New("there is no read key here. Run install first")

func newRunID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RunHost records until ctx is done, then closes the run cleanly. It returns an
// error only for things that stop it starting (bad settings, no key, a folder it
// cannot write, a store it cannot open). A busy port does not stop recording:
// the recorder keeps its evidence and tries the port again.
func RunHost(ctx context.Context, o HostOptions) error {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cfg := o.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	keys, err := LoadKeyCheck(o.KeyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoReadKey
		}
		return fmt.Errorf("the read key file cannot be used: %w", err)
	}
	if err := os.MkdirAll(cfg.Folder, 0o755); err != nil {
		return fmt.Errorf("the recordings folder cannot be made: %w", err)
	}
	if o.CrashCapture {
		// Started before the store opens, so a crash while opening is kept too. Left
		// open while a panic unwinds, so the runtime can still write its dump.
		defer startCrashCapture(cfg.Folder, logf).CloseUnlessPanic()
	}
	host, _ := os.Hostname()
	runID := newRunID()
	store, err := OpenStore(cfg.Folder, StoreOptions{Name: cfg.Name, Version: o.Version, Host: host,
		IntervalSeconds: cfg.IntervalSeconds, RunID: runID, Now: o.Now})
	if err != nil {
		return fmt.Errorf("the recordings cannot be opened: %w", err)
	}
	if o.CrashCapture {
		reportPreviousCrashes(store, cfg.Folder, logf)
	}
	col := o.Collector
	if col == nil {
		hc, cerr := OSCollector(cfg.Record.Processes)
		if cerr != nil {
			_ = store.Close("failed")
			return fmt.Errorf("the measurements cannot be read: %w", cerr)
		}
		col = hc
	}
	// Below-normal priority, once, best-effort: a failure is not logged and
	// never stops recording.
	_ = SetBelowNormalPriority()
	// Keep the Go heap small: a soft memory limit and an earlier GC, so the
	// per-tick garbage does not grow the heap goal. Process-wide, set once; the
	// previous values are put back when the run ends (tests share the process).
	prevLimit := debug.SetMemoryLimit(hostMemoryLimit)
	prevGC := debug.SetGCPercent(hostGCPercent)
	defer func() {
		debug.SetMemoryLimit(prevLimit)
		debug.SetGCPercent(prevGC)
	}()
	budget := NewBudget(cfg.IntervalSeconds)
	// A restart does not start over: the interval and the groups on probation
	// that the last run settled on come back, unless the configured interval
	// changed (the owner's new interval is honoured).
	if mb := store.Manifest().Budget; mb != nil {
		restoreAt := time.Now()
		if o.Now != nil {
			restoreAt = o.Now()
		}
		budget.Restore(restoreAt, *mb)
		store.SetInterval(budget.EffectiveInterval())
	}
	// Resolver wiring (BBX-1-11): the gate resolves ONE immutable inventory and
	// target result per collection cycle and shares it — with its monotonic
	// generation — among the host collector and every group, so a tick takes one
	// process snapshot, not one per consumer (W3-05), and a skipped group never
	// loses a change (W3-03). With no selected targets, Targets stays nil and
	// machine-wide groups work exactly as before.
	resolver := NewResolver()
	wireResolverSnapshot(resolver, col)
	gate := &processChangeGate{resolver: resolver, targets: cfg.Targets}
	gate.view.Store(resolver.resolveTick(cfg.Targets, time.Now()))
	deps := GroupDeps{Config: cfg, Cores: CoreCount(),
		Generation:    func() uint64 { return gate.currentView().gen },
		SQLProcessSig: func() uint64 { return gate.currentView().sqlSig }}
	if len(cfg.Targets) > 0 {
		deps.Targets = func() []ResolvedTarget { return gate.currentView().targets }
	}
	// The busiest-process list always includes the selected targets (BBX-4-05).
	if hc, ok := col.(*HostCollector); ok {
		hc.SetSelectedTargets(deps.Targets)
	}
	groups := BuildGroups(deps)
	gate.inner = NewGroupCollector(col, groups, budget)
	col = gate
	disk := o.Disk
	if disk == nil {
		disk = OSDisk{}
	}
	diag := OpenDiagLog(store.Dir(), o.Now)
	defer diag.Close()
	diag.SetOnStuck(func() { store.Event(EventDiagStopped, "diag-log-stuck") })
	diag.SetOnResumed(func() { store.Event(EventDiagResumed, "diag-log-ok") })
	rec := NewRecorder(RecorderOptions{Config: cfg, Store: store, Collector: col, Disk: disk, RunID: runID,
		Now: o.Now, Ticks: o.Ticks, Budget: budget, SelfUsage: SelfUsage, Diag: diag})
	srv := NewServer(ServerOptions{Store: store, Recorder: rec, Config: cfg, Keys: keys, Now: o.Now, Diag: diag, Update: o.Update})

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		serveWithRetry(ctx, srv, cfg, o, logf)
	}()

	logf("recording %s to %s every %d s", cfg.Name, cfg.Folder, cfg.IntervalSeconds)
	runErr := rec.Run(ctx)

	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	<-srvDone
	return runErr
}

// wireResolverSnapshot gives the resolver the host collector's process list, so
// the resolver and the collector share one machine-wide read per tick (the
// Source serves the second caller from the first read). The list carries the
// creation identity of every process at no extra cost, so the resolver needs no
// separate "lite" enumeration. A collector that is not the host collector (a test
// fake) keeps the Toolhelp enumeration, without creation times when nothing is
// selected (PERF-04).
func wireResolverSnapshot(r *Resolver, col Collector) {
	if hc, ok := col.(*HostCollector); ok {
		r.snapshotFn = hc.SnapshotProcesses
		r.liteFn = hc.SnapshotProcesses
		return
	}
	r.liteFn = snapshotProcessesLite
}

// processChangeGate resolves ONE immutable inventory/target view per collection
// cycle and shares it with every group and the host collector: all consumers in
// a cycle read the same generation and the same targets, and the process
// snapshot cost is paid once per tick, not once per consumer (W3-05). The view
// is published with an atomic pointer because a group worker from an earlier
// tick may still read it while the tick goroutine stores the next one.
type processChangeGate struct {
	inner    Collector
	resolver *Resolver
	targets  []Target
	view     atomic.Pointer[tickView]
	// resolveNs is how long the last round spent resolving the targets, before
	// the host read starts (diagnostics only).
	resolveNs atomic.Int64
}

// currentView is the shared per-tick result, never nil once RunHost seeds it.
func (g *processChangeGate) currentView() *tickView {
	if v := g.view.Load(); v != nil {
		return v
	}
	return &tickView{}
}

func (g *processChangeGate) Collect(now time.Time) (Sample, error) {
	t := time.Now()
	g.view.Store(g.resolver.resolveTick(g.targets, now))
	g.resolveNs.Store(int64(time.Since(t)))
	return g.inner.Collect(now)
}

func (g *processChangeGate) Close() error { return g.inner.Close() }

// Timings, GroupTimings and GroupCount pass the group collector's numbers
// through. Without them the recorder, which only sees the gate, could never read
// where a round went: the self block's host and slow-group fields stayed empty.
func (g *processChangeGate) Timings() (time.Duration, string, time.Duration) {
	if t, ok := g.inner.(interface {
		Timings() (time.Duration, string, time.Duration)
	}); ok {
		return t.Timings()
	}
	return 0, "", 0
}

func (g *processChangeGate) GroupTimings() []groupTime {
	if t, ok := g.inner.(interface{ GroupTimings() []groupTime }); ok {
		return t.GroupTimings()
	}
	return nil
}

// ResolveTime is how long the last round spent resolving the targets.
func (g *processChangeGate) ResolveTime() time.Duration { return time.Duration(g.resolveNs.Load()) }

// GroupsTime is the total time the last round spent in all counter groups.
func (g *processChangeGate) GroupsTime() time.Duration {
	if t, ok := g.inner.(interface{ GroupsTime() time.Duration }); ok {
		return t.GroupsTime()
	}
	return 0
}

func (g *processChangeGate) GroupCount() int {
	if t, ok := g.inner.(interface{ GroupCount() int }); ok {
		return t.GroupCount()
	}
	return 0
}

func serveWithRetry(ctx context.Context, srv *Server, cfg Config, o HostOptions, logf func(string, ...any)) {
	retry := o.ListenRetry
	if retry <= 0 {
		retry = 30 * time.Second
	}
	ln := o.Listener
	for {
		if ln == nil {
			var err error
			ln, err = Listen(cfg.Bind, cfg.Port)
			if err != nil {
				logf("status server cannot open port %d (%v); still recording, will try again", cfg.Port, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(retry):
					continue
				}
			}
		}
		if o.Ready != nil {
			o.Ready(ln.Addr().String())
		}
		_ = srv.Serve(ln)
		return
	}
}
