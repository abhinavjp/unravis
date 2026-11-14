package blackbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

// Recorder states a running recorder reports (decision A36). The other states
// (Not found, Unreachable, Clock differs, Incomplete, Not placed) are decided by
// the reader.
const (
	StateRunning     = "running"
	StateRunningLate = "running-late"
	StateDiskLow     = "disk-low"
	StateStopped     = "stopped"
)

// Timing of the work around the samples.
const (
	housekeepEvery = 60 * time.Second
	logsEvery      = 5 * time.Minute
	// clockJumpSeconds: when the wall clock moves this much more (or less) than
	// the steady clock between two ticks, it is recorded as an event.
	clockJumpSeconds = 2.0
)

// Fixed event codes (C10): events never carry err.Error(), driver text, file
// paths or instance names — the manifest is persisted and ships in snapshots.
const (
	codeCollectFailed      = "collect-failed"
	codeWriteFailed        = "write-failed"
	codeHousekeepingFailed = "housekeeping-failed"
)

// eventCode maps a failure to a fixed event code: a known cause gets a
// specific code, anything else the generic collect code (the write and
// housekeeping paths substitute their own generic code). It never returns
// err.Error().
func eventCode(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission-denied"
	case isDiskFull(err):
		return "disk-full"
	default:
		return codeCollectFailed
	}
}

// housekeepFn is the housekeeping pass, unchanged. Tests replace it to stand
// for a pass that fails outright (Store.Housekeep reports blocked removals via
// Skipped and does not itself return errors).
var housekeepFn = (*Store).Housekeep

// RecorderOptions wire the recorder to its parts. Tests replace Now, Mono and
// Ticks; the service uses the defaults.
type RecorderOptions struct {
	Config    Config
	Store     *Store
	Collector Collector
	Disk      DiskSpace
	RunID     string
	Now       func() time.Time
	// Mono is a steady clock (never moved by the user). Default: time since start.
	Mono func() time.Duration
	// Ticks drives the loop. Default: a ticker at the configured interval.
	Ticks <-chan time.Time
	// Budget throttles the loop when the Blackbox itself costs too much.
	// Optional: nil keeps the configured interval.
	Budget *Budget
	// SelfUsage reads the Blackbox's own CPU percent of one core and working
	// set. Optional: nil skips the measurement. ok=false is a missing reading.
	SelfUsage func() (float64, uint64, bool)
}

// RecorderStatus is a snapshot of the recorder for the status page.
type RecorderStatus struct {
	State       string
	RunID       string
	Seq         uint64
	LastSample  time.Time
	Started     time.Time
	FreePercent float64
	FreeKnown   bool
	WriteOK     bool
}

// Recorder is the sampling loop.
type Recorder struct {
	o   RecorderOptions
	now func() time.Time

	mu         sync.Mutex
	started    time.Time
	seq        uint64
	lastSample time.Time
	paused     bool
	stopped    bool
	freePct    float64
	freeKnown  bool
	writeOK    bool
	collectOK  bool

	lastTickWall time.Time
	lastTickMono time.Duration
	haveTick     bool
	lastHK       time.Time
	lastLogs     time.Time
	logs         []LogEntry
	resumedFlag  bool // next sample follows a disk-low pause
	forceHK      bool
	lastSkipMsg  string
	clockFwd     bool // the wall clock jumped forward since the last housekeeping pass
}

// NewRecorder makes a recorder. The caller opened the store and the collector.
func NewRecorder(o RecorderOptions) *Recorder {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Mono == nil {
		start := time.Now()
		o.Mono = func() time.Duration { return time.Since(start) }
	}
	return &Recorder{o: o, now: o.Now, started: o.Now().UTC(), writeOK: true, collectOK: true}
}

func (r *Recorder) limits() Limits {
	return Limits{KeepDays: r.o.Config.KeepDays, MaxBytes: r.o.Config.MaxBytes(), MinFreePercent: float64(r.o.Config.MinFreePercent)}
}

// effectiveInterval is the tick interval in seconds: the budget's effective
// interval when there is a budget, else the configured base.
func effectiveInterval(b *Budget, base int) int {
	if b != nil {
		return b.EffectiveInterval()
	}
	return base
}

// Run samples until ctx is done, then closes the run cleanly. The first sample
// is taken at once.
func (r *Recorder) Run(ctx context.Context) error {
	ticks := r.o.Ticks
	var t *time.Ticker
	if ticks == nil {
		t = time.NewTicker(time.Duration(r.o.Config.IntervalSeconds) * time.Second)
		defer t.Stop()
		ticks = t.C
	}
	r.Tick(r.now())
	for {
		select {
		case <-ctx.Done():
			return r.stop("stopped")
		case <-ticks:
			r.Tick(r.now())
			// Own ticker only: follow the budget by resetting the one ticker,
			// never a new one. The o.Ticks test path is unchanged.
			if t != nil && r.o.Budget != nil {
				t.Reset(time.Duration(effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds)) * time.Second)
			}
		}
	}
}

func (r *Recorder) stop(reason string) error {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	err := r.o.Store.Close(reason)
	if cerr := r.o.Collector.Close(); err == nil {
		err = cerr
	}
	return err
}

// Tick does one round: clock check, housekeeping when due, and one sample
// unless the disk is low.
func (r *Recorder) Tick(now time.Time) {
	now = now.UTC()
	r.checkClock(now)

	if r.forceHK || r.lastHK.IsZero() || now.Sub(r.lastHK) >= housekeepEvery || now.Before(r.lastHK) {
		r.housekeep(now)
	}
	r.mu.Lock()
	paused := r.paused
	r.mu.Unlock()
	if paused {
		return
	}

	r.feedBudget()
	if r.o.Budget != nil {
		// Gaps are judged against the interval this run actually ticks at, so
		// a widened budget does not fabricate a gap between on-time samples.
		r.o.Store.SetInterval(effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds))
	}

	s, err := r.o.Collector.Collect(now)
	if err != nil {
		if r.collectOK {
			r.o.Store.Event(EventCollectFailed, eventCode(err))
		}
		r.collectOK = false
		return
	}
	r.collectOK = true

	r.mu.Lock()
	r.seq++
	s.Run, s.Seq, s.T = r.o.RunID, r.seq, now
	if r.freeKnown {
		p := r.freePct
		s.DiskFreePct = &p
	}
	r.mu.Unlock()
	if r.o.Config.Record.LogsCatalogue && (r.lastLogs.IsZero() || now.Sub(r.lastLogs) >= logsEvery || now.Before(r.lastLogs)) {
		r.logs = CatalogueLogs(r.o.Config.LogPaths)
		r.lastLogs = now
		s.Logs = r.logs
	}
	hint := ""
	if r.resumedFlag {
		hint = CauseDiskLow
	}
	if err := r.o.Store.Append(s, hint); err != nil {
		r.mu.Lock()
		first := r.writeOK
		r.writeOK = false
		r.forceHK = true // a full disk is the usual reason: free some space now
		r.mu.Unlock()
		if first {
			code := eventCode(err)
			if code == codeCollectFailed {
				code = codeWriteFailed
			}
			r.o.Store.Event(EventWriteFailed, code)
		}
		return
	}
	r.mu.Lock()
	r.writeOK = true
	r.resumedFlag = false
	r.lastSample = now
	r.mu.Unlock()
}

// feedBudget measures the Blackbox's own cost and advances the budget one
// cycle, before the collector runs. A missing reading (ok=false) skips the
// update — never 0-as-fact — and never blocks the tick.
func (r *Recorder) feedBudget() {
	b := r.o.Budget
	if b == nil {
		return
	}
	if r.o.SelfUsage != nil {
		if cpu, ws, ok := r.o.SelfUsage(); ok {
			b.SelfUsage(cpu, ws)
		}
	}
	b.Cycle()
}

// checkClock compares how far the wall clock moved with how far the steady
// clock moved, and records a clock-jump event when they differ.
func (r *Recorder) checkClock(now time.Time) {
	mono := r.o.Mono()
	if r.haveTick {
		wall := now.Sub(r.lastTickWall).Seconds()
		steady := (mono - r.lastTickMono).Seconds()
		if d := wall - steady; math.Abs(d) > clockJumpSeconds {
			dir := "forward"
			if d < 0 {
				dir = "back"
			} else {
				r.clockFwd = true // housekeeping leaves the age rule out once
			}
			r.o.Store.Event(EventClockJump, fmt.Sprintf("clock moved %s by %.1f s", dir, math.Abs(d)))
		}
	}
	r.lastTickWall, r.lastTickMono, r.haveTick = now, mono, true
}

func (r *Recorder) housekeep(now time.Time) {
	r.forceHK = false
	r.lastHK = now
	lim := r.limits()
	if r.clockFwd {
		// By a clock that just jumped forward every file may look old. Leave
		// the age rule out for this pass; size and free space still apply.
		lim.SkipAge, r.clockFwd = true, false
	}
	res, err := housekeepFn(r.o.Store, lim, r.o.Disk, now)
	if err != nil {
		code := eventCode(err)
		if code == codeCollectFailed {
			code = codeHousekeepingFailed
		}
		r.o.Store.Event(EventHousekeeping, code)
	}
	if res.Incomplete() {
		// A file held open by a reader cannot be removed; it is tried again
		// next round. Say so once per set of files, not every minute.
		if msg := "could not remove " + strings.Join(res.Skipped, ", ") + " (in use), will retry"; msg != r.lastSkipMsg {
			r.lastSkipMsg = msg
			r.o.Store.Event(EventHousekeeping, msg)
		}
	} else {
		r.lastSkipMsg = ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if res.DiskKnown {
		r.freePct, r.freeKnown = res.FreePercent, true
	} else if r.o.Disk != nil {
		if info, derr := r.o.Disk.Free(r.o.Store.Dir()); derr == nil {
			r.freePct, r.freeKnown = info.FreePercent(), true
		}
	}
	// A pass that could not remove everything says nothing about whether the
	// disk is still full, unless the free space was read fresh after it. With
	// no fresh reading, or after a failed pass, the pause state stays as it is.
	if err != nil || (res.Incomplete() && !res.DiskKnown) {
		return
	}
	switch {
	case res.DiskLow && !r.paused:
		r.paused = true
		r.o.Store.Event(EventPausedDiskLow, fmt.Sprintf("free space %.1f%% is below %d%% and nothing older can be removed", res.FreePercent, r.o.Config.MinFreePercent))
	case !res.DiskLow && r.paused:
		r.paused = false
		r.resumedFlag = true
		r.o.Store.Event(EventResumed, fmt.Sprintf("free space %.1f%%", res.FreePercent))
	}
}

// Status is a snapshot for the status page. now is the time the state is judged
// at: Running late is decided from the age of the newest sample.
func (r *Recorder) Status(now time.Time) RecorderStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := RecorderStatus{RunID: r.o.RunID, Seq: r.seq, LastSample: r.lastSample, Started: r.started,
		FreePercent: r.freePct, FreeKnown: r.freeKnown, WriteOK: r.writeOK}
	ref := r.lastSample
	if ref.IsZero() {
		ref = r.started
	}
	switch {
	case r.stopped:
		st.State = StateStopped
	case r.paused:
		st.State = StateDiskLow
	case IsLate(ref, now.UTC(), effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds)):
		st.State = StateRunningLate
	default:
		st.State = StateRunning
	}
	return st
}
