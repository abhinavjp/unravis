package blackbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"
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
	// Ready is called once the status server listens, with its address.
	Ready func(addr string)

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
	host, _ := os.Hostname()
	runID := newRunID()
	store, err := OpenStore(cfg.Folder, StoreOptions{Name: cfg.Name, Version: o.Version, Host: host,
		IntervalSeconds: cfg.IntervalSeconds, RunID: runID, Now: o.Now})
	if err != nil {
		return fmt.Errorf("the recordings cannot be opened: %w", err)
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
	budget := NewBudget(cfg.IntervalSeconds)
	// Resolver wiring (BBX-1-11): the gate resolves ONE immutable inventory and
	// target result per collection cycle and shares it — with its monotonic
	// generation — among the host collector and every group, so a tick takes one
	// process snapshot, not one per consumer (W3-05), and a skipped group never
	// loses a change (W3-03). With no selected targets, Targets stays nil and
	// machine-wide groups work exactly as before.
	resolver := NewResolver()
	gate := &processChangeGate{resolver: resolver, targets: cfg.Targets}
	gate.view.Store(resolver.resolveTick(cfg.Targets, time.Now()))
	deps := GroupDeps{Config: cfg, Cores: CoreCount(),
		Generation: func() uint64 { return gate.currentView().gen }}
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
	rec := NewRecorder(RecorderOptions{Config: cfg, Store: store, Collector: col, Disk: disk, RunID: runID,
		Now: o.Now, Ticks: o.Ticks, Budget: budget, SelfUsage: SelfUsage})
	srv := NewServer(ServerOptions{Store: store, Recorder: rec, Config: cfg, Keys: keys, Now: o.Now})

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
}

// currentView is the shared per-tick result, never nil once RunHost seeds it.
func (g *processChangeGate) currentView() *tickView {
	if v := g.view.Load(); v != nil {
		return v
	}
	return &tickView{}
}

func (g *processChangeGate) Collect(now time.Time) (Sample, error) {
	g.view.Store(g.resolver.resolveTick(g.targets, now))
	return g.inner.Collect(now)
}

func (g *processChangeGate) Close() error { return g.inner.Close() }

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
