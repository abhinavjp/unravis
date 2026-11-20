package blackbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime/debug"
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
	// freeMemEvery is how often the heap is handed back to the OS, right after a
	// housekeeping pass (the Go scavenger alone returns it late).
	freeMemEvery = 10 * time.Minute
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
	// SelfUsage reads the Blackbox's own cost: CPU percent of one core since the
	// last reading, Go heap in use, private bytes and handles. Optional: nil
	// skips the measurement. ok=false is a missing reading (counted as calm).
	SelfUsage func() (SelfReading, bool)
	// Diag is the diagnostics log. Optional: nil writes nothing.
	Diag *DiagLog
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
	lastFreeMem  time.Time
	self         selfTracker // this run's own cost; tick goroutine only

	// Diagnostics (tick goroutine only): where the rounds went, kept cheap.
	sched      schedInfo // set by Run just before each Tick
	agg        diagAgg
	lastBeat   time.Time
	lastWinSeq int
	lastProb   string
	lastEff    int
	noteMinute time.Time
	noteCount  int
	lastSelf   SelfReading
}

// schedInfo is how the round was scheduled: how late it started and how many
// deadlines were skipped before it.
type schedInfo struct {
	lag    time.Duration
	missed int
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

// SQLBudgetShare is the divisor of the owner's single maxGB budget reserved for
// the SQL sample folder. The SQL service bounds its own folder to
// Config.MaxBytes()/SQLBudgetShare (cmd/unravis-blackbox/sqlrunner.go's
// sqlBudgetShare MUST equal this constant — see the seam note in the wave-10-11
// data-fix report). The host recorder retains the REMAINING share, so the two
// writers' ADMITTED (housekeeping-enforced) bytes satisfy one documented ceiling:
//
//	host MaxBytes + SQL MaxBytes <= Config.MaxBytes()
//
// Active-hour overshoot allowance: each writer protects its current hour file
// from removal, so between housekeeping passes the retained total can exceed the
// admitted ceiling by up to one active hour of writes per writer (host: one hour
// of samples at the configured interval; SQL: one hour at its sampler cadences).
// That overshoot is bounded by the per-hour write volume, is reclaimed at the
// next pass once the hour rolls, and is the explicit allowance behind the
// "combined cap" claim — not an unbounded 1.25x allocation (W10-02).
const SQLBudgetShare = 4

func (r *Recorder) limits() Limits {
	max := r.o.Config.MaxBytes()
	// Reserve SQL's share; the host keeps the rest so the combined admitted
	// bytes stay within the owner's single ceiling (W10-02).
	host := max - max/SQLBudgetShare
	return Limits{KeepDays: r.o.Config.KeepDays, MaxBytes: host, MinFreePercent: float64(r.o.Config.MinFreePercent)}
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
	r.diagStart()
	if ticks := r.o.Ticks; ticks != nil {
		// Test path: the caller drives the ticks.
		r.Tick(r.now())
		for {
			select {
			case <-ctx.Done():
				return r.stop("stopped")
			case <-ticks:
				r.Tick(r.now())
			}
		}
	}
	// Deadline scheduling: the next tick is the previous DEADLINE plus the
	// interval, not the end of the last tick plus the interval, so the cadence
	// does not drift by the tick duration and follows the budget at once. A
	// deadline that already passed (a slow tick, a sleeping machine) is skipped,
	// never run back to back.
	next := time.Now()
	r.Tick(r.now())
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		interval := time.Duration(effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds)) * time.Second
		prev := next
		next = nextDeadline(next, time.Now(), interval)
		missed := 0
		if interval > 0 {
			missed = int(next.Sub(prev)/interval) - 1
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Until(next))
		select {
		case <-ctx.Done():
			return r.stop("stopped")
		case <-timer.C:
			r.sched = schedInfo{lag: time.Since(next), missed: missed}
			r.Tick(r.now())
		}
	}
}

// nextDeadline is the first deadline after now on the grid prev + k x interval.
// A prev that is already behind now moves forward by whole intervals, so missed
// deadlines are skipped. A non-positive interval is treated as one second.
func nextDeadline(prev, now time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		interval = time.Second
	}
	next := prev.Add(interval)
	if !next.After(now) {
		missed := now.Sub(next)/interval + 1
		next = next.Add(missed * interval)
	}
	return next
}

func (r *Recorder) stop(reason string) error {
	if r.o.Diag != nil {
		r.diagBeat(r.now().UTC())
		r.o.Diag.Line("stop", "reason", reason)
	}
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
	roundStart := time.Now()
	rd := roundDiag{Seq: r.seq + 1, Lag: r.sched.lag, Missed: r.sched.missed}
	r.sched = schedInfo{}
	if rd.Lag < 0 {
		rd.Lag = 0
	}
	r.checkClock(now)

	if r.forceHK || r.lastHK.IsZero() || now.Sub(r.lastHK) >= housekeepEvery || now.Before(r.lastHK) {
		hkStart := time.Now()
		r.housekeep(now)
		rd.HK = time.Since(hkStart)
	}
	r.mu.Lock()
	paused := r.paused
	r.mu.Unlock()
	if paused {
		r.diagRound(now, rd, roundStart, true)
		return
	}

	tickStart := time.Now()
	reading, haveReading := r.feedBudget(now)
	feedDur := time.Since(tickStart)
	rd.Feed = feedDur
	r.lastSelf = reading
	var appendDur time.Duration
	// The self block of this run's record: the reading taken at the top of the
	// tick plus how long the tick took and where it went. In memory; the
	// manifest file picks it up at its next write.
	defer func() {
		st := r.self.note(now, reading, haveReading, time.Since(tickStart))
		c := &r.self.cur
		c.FeedMs = round1(float64(feedDur) / float64(time.Millisecond))
		c.AppendMs = round1(float64(appendDur) / float64(time.Millisecond))
		c.HostMs, c.SlowGroup, c.SlowGroupMs = 0, "", 0
		if t, ok := r.o.Collector.(interface {
			Timings() (time.Duration, string, time.Duration)
		}); ok {
			h, n, d := t.Timings()
			c.HostMs, c.SlowGroup = round1(float64(h)/float64(time.Millisecond)), n
			c.SlowGroupMs = round1(float64(d) / float64(time.Millisecond))
		}
		st = *c
		r.o.Store.SetSelf(st)
		rd.Append = appendDur
		if gt, ok := r.o.Collector.(interface{ GroupTimings() []groupTime }); ok {
			rd.Groups = append(rd.Groups[:0], gt.GroupTimings()...)
		}
		r.diagRound(now, rd, roundStart, false)
	}()
	if r.o.Budget != nil {
		// What the budget settled on survives a restart; SetInterval is written
		// only when the interval changes. Gaps are judged against the interval
		// this run actually ticks at, so a widened budget does not fabricate a
		// gap between on-time samples.
		r.o.Store.SetBudget(r.o.Budget.Persisted())
		r.o.Store.SetInterval(effectiveInterval(r.o.Budget, r.o.Config.IntervalSeconds))
	}

	collectStart := time.Now()
	s, err := r.o.Collector.Collect(now)
	rd.Collect = time.Since(collectStart)
	if t, ok := r.o.Collector.(interface {
		Timings() (time.Duration, string, time.Duration)
	}); ok {
		rd.Host, _, _ = t.Timings()
	}
	if err != nil {
		rd.Err = diagErrCode(err)
		if r.collectOK {
			r.event(EventCollectFailed, eventCode(err))
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
	appendStart := time.Now()
	err = r.o.Store.Append(s, hint)
	appendDur = time.Since(appendStart)
	if err != nil {
		r.mu.Lock()
		first := r.writeOK
		r.writeOK = false
		r.forceHK = true // a full disk is the usual reason: free some space now
		r.mu.Unlock()
		rd.Err = "write-failed"
		if first {
			code := eventCode(err)
			if code == codeCollectFailed {
				code = codeWriteFailed
			}
			r.event(EventWriteFailed, code)
		}
		return
	}
	// The sample is durably accepted: only now may the event read cursor
	// advance. A failure anywhere above leaves the batch pending (W6-06).
	s.Events.Ack()
	r.mu.Lock()
	r.writeOK = true
	r.resumedFlag = false
	r.lastSample = now
	r.mu.Unlock()
}

// feedBudget measures the Blackbox's own cost and advances the budget one
// cycle, before the collector runs. A missing reading (ok=false) is never
// 0-as-fact: it counts as calm and never blocks the tick. The reading is
// returned for the self block.
func (r *Recorder) feedBudget(now time.Time) (SelfReading, bool) {
	var rd SelfReading
	ok := false
	if r.o.SelfUsage != nil {
		rd, ok = r.o.SelfUsage()
	}
	if b := r.o.Budget; b != nil {
		b.Cycle(now, rd, ok)
	}
	return rd, ok
}

// event records an event in the manifest and in the diagnostics log.
func (r *Recorder) event(kind, detail string) {
	r.o.Store.Event(kind, detail)
	r.diagEvent(kind, detail)
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
			r.event(EventClockJump, fmt.Sprintf("clock moved %s by %.1f s", dir, math.Abs(d)))
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
	// Hand the heap back to the OS every freeMemEvery, right after the pass.
	defer func() {
		if r.lastFreeMem.IsZero() || now.Before(r.lastFreeMem) {
			r.lastFreeMem = now
		} else if now.Sub(r.lastFreeMem) >= freeMemEvery {
			r.lastFreeMem = now
			debug.FreeOSMemory()
		}
	}()
	res, err := housekeepFn(r.o.Store, lim, r.o.Disk, now)
	if len(res.Removed) > 0 || len(res.Skipped) > 0 || err != nil {
		r.o.Diag.Line("housekeeping", "removed", len(res.Removed), "skipped", len(res.Skipped),
			"free", round1(res.FreePercent), "diskKnown", res.DiskKnown, "low", res.DiskLow, "failed", err != nil)
	}
	if err != nil {
		code := eventCode(err)
		if code == codeCollectFailed {
			code = codeHousekeepingFailed
		}
		r.event(EventHousekeeping, code)
	}
	if res.Incomplete() {
		// A file held open by a reader cannot be removed; it is tried again
		// next round. Say so once per set of files, not every minute.
		if msg := "could not remove " + strings.Join(res.Skipped, ", ") + " (in use), will retry"; msg != r.lastSkipMsg {
			r.lastSkipMsg = msg
			r.event(EventHousekeeping, msg)
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
		r.event(EventPausedDiskLow, fmt.Sprintf("free space %.1f%% is below %d%% and nothing older can be removed", res.FreePercent, r.o.Config.MinFreePercent))
	case !res.DiskLow && r.paused:
		r.paused = false
		r.resumedFlag = true
		r.event(EventResumed, fmt.Sprintf("free space %.1f%%", res.FreePercent))
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
