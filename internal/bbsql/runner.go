// runner.go is the SQL service's own tick loop (BBX-5-10): one connection, one
// tick at a time, fixed budgets and fixed output shapes. It never runs in the
// host recorder process, never queues a tick, and never clears auto-off. The
// samplers (BBX-5-11..14) register themselves; the runner only schedules them,
// sends their allowlisted statement, decodes and appends.
package bbsql

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fixed budgets (C4): tickBudget caps a whole tick, the item limits are 3 s for
// 15 s samples and 5 s for 60 s samples.
const (
	tickBudget = 5 * time.Second
	quickLimit = 3 * time.Second
	longLimit  = 5 * time.Second
)

// hourLayout names the hourly sample file. The file name is the clock that
// rotate reads, so it is written and parsed from one constant.
const hourLayout = "20060102-15"

// defaultKeepHours bounds the SQL folder the way keepDays bounds the host
// folder. The SQL service writes its own files and host housekeeping never
// looks inside \sql, so without this the folder grows until it fills the disk.
const defaultKeepHours = 7 * 24

// errDiskLow is the fixed code for "the drive is under the owner's floor, so
// this tick wrote nothing". It is deliberately not a SQL failure: State tracks
// instance problems, not free space.
var errDiskLow = &Error{CodeDiskLow, "The drive holding the recordings folder is below its free-space floor."}

// CodeDiskUnreadable is the fixed code for a volume whose free space cannot be
// read. It is distinct from CodeDiskLow (a KNOWN-low volume): an unreadable
// volume cannot prove there is room, so sampling stays off, but the cause is
// different and must not be reported as a measured low reading (W10-03).
const CodeDiskUnreadable = "disk-unreadable"

var errDiskUnreadable = &Error{CodeDiskUnreadable, "The free space of the drive holding the recordings folder could not be read."}

// CodeWriteFailed is the fixed code for a filesystem write/repair failure on the
// sample or status files. It is tracked SEPARATELY from SQL authorization and
// instance state (W10-10): a disk fault must never be classified as a SQL
// permission denial (which would permanently deny a healthy instance) nor count
// toward the ten-failure SQL auto-off.
const CodeWriteFailed = "write-failed"

var errWriteFailed = &Error{CodeWriteFailed, "A SQL sample or status file could not be written."}

// maxAgeRemovalsPerPass bounds the age rule of one rotation pass so a clock that
// stays wrong, or a long stop, releases history in steps the owner can notice
// rather than deleting every expired hour at once (W10-09). It mirrors the host
// recorder's MaxAgeRemovalsPerPass.
const maxAgeRemovalsPerPass = 6

// maxByteRemovalsPerPass bounds the byte-cap rule of one rotation pass for the
// same reason: a saturated folder is reclaimed in bounded steps, not one sweep.
const maxByteRemovalsPerPass = 64

// clockJumpThreshold is how far the wall clock may diverge from the steady clock
// between two rotation passes before age purging is deferred (W10-09). It
// matches the host recorder's clockJumpSeconds.
const clockJumpThreshold = 2 * time.Second

// repairWindow bounds the torn-tail repair read: only the last repairWindow
// bytes of a sample file are inspected, so repair never reads a whole hour
// (W10-10). Sample lines are far smaller than this window.
const repairWindow = 64 << 10

// Sampler is one allowlisted SQL sample. Samplers register from their own
// package init, so the runner never needs to know which samples exist.
type Sampler interface {
	ID() string
	Kind() string
	Interval() time.Duration
	Decode(rows []Row) (any, bool, error) // payload, truncated, fixed error
}

// Gate is an optional Sampler interface for a sample of a feature this instance
// may not have. The runner sends the gate statement BEFORE the sampler's own
// statement; when GateOpen reports false the sampler is skipped for this tick
// (nothing is written, its due time still advances) so its DMV is never
// queried. GateOpen must fail closed.
type Gate interface {
	GateID() string
	GateOpen(rows []Row) bool
}

// Observation is the continuity metadata the runner hands a stateful sampler
// before each Decode (W11-05). Time is the observation wall time (UTC); Epoch is
// the runner's source generation, which advances on every continuity break
// (reconnect, query/decode failure, disk pause, auto-off, partial tick, clock
// jump). A sampler whose baseline was taken under a different Epoch must
// rebaseline and emit no delta: a delta may never span an unobserved interval or
// a reset that overtook the old value during a gap. Deltas are raw counter
// differences over the observed interval, never divided by the nominal cadence.
type Observation struct {
	Time  time.Time
	Epoch uint64
}

// ObservingSampler is the optional lifecycle seam. The runner calls Observe
// before Decode for any sampler that implements it; stateless samplers need not.
type ObservingSampler interface {
	Observe(Observation)
}

// samplerEntry is an immutable registry descriptor: a factory that allocates a
// FRESH sampler instance per runner, so two runners never share baseline maps
// (W11-07). The kind is cached for dedup and ordering without calling the
// factory.
type samplerEntry struct {
	kind string
	new  func() Sampler
}

// registryMu guards the registry: init order is not defined, and tests register
// samplers while other code reads the list.
var (
	registryMu sync.Mutex
	registry   []samplerEntry
)

// RegisterSamplerFactory registers an immutable factory. Every Samplers() call
// invokes it to allocate fresh sampler state, so sequential or concurrent
// runners are isolated (W11-07). A nil factory, one whose interval is neither
// cadence, or one whose Kind is already registered is ignored, so a double
// registration can never produce two sample streams for one kind.
func RegisterSamplerFactory(new func() Sampler) {
	if new == nil {
		return
	}
	probe := new()
	if probe == nil {
		return
	}
	if iv := probe.Interval(); iv != BaseInterval && iv != DemotedInterval {
		return
	}
	kind := probe.Kind()
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, e := range registry {
		if e.kind == kind {
			return
		}
	}
	registry = append(registry, samplerEntry{kind: kind, new: new})
}

// RegisterSampler registers a shared instance. It is the legacy/test seam:
// production samplers register factories so each runner gets fresh state. The
// shared instance is returned as-is by Samplers() (no fresh allocation), which
// is fine for stateless test doubles but must not be used for a stateful
// sampler that needs per-runner isolation.
func RegisterSampler(s Sampler) {
	if s == nil {
		return
	}
	RegisterSamplerFactory(func() Sampler { return s })
}

// Samplers returns a snapshot sorted by Kind, allocating a FRESH instance from
// each registered factory so no two callers share mutable sampler state
// (W11-07). Tick order is the same in every run and the lines of one hour are
// comparable with the next hour.
func Samplers() []Sampler {
	registryMu.Lock()
	entries := make([]samplerEntry, len(registry))
	copy(entries, registry)
	registryMu.Unlock()
	out := make([]Sampler, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.new())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// ConnectFunc opens the single connection and hands back the send seam with its
// closer. Tests inject a fake; nil means defaultConnect.
type ConnectFunc func(ConnConfig) (Queryer, func() error, error)

func defaultConnect(cfg ConnConfig) (Queryer, func() error, error) {
	db, err := Open(cfg)
	if err != nil {
		return nil, nil, err
	}
	return DBQueryer{DB: db}, db.Close, nil
}

// SampleDoc is one NDJSON line. Only numbers, enums and validated wait types
// reach it: Rows is whatever a Sampler decoded from a fixed struct (C9).
type SampleDoc struct {
	V         int       `json:"v"`
	T         time.Time `json:"t"`
	Kind      string    `json:"kind"`
	Truncated bool      `json:"truncated,omitempty"`
	Rows      any       `json:"rows"`
}

// StatusDoc is the only SQL thing the host publishes (C11): four fixed fields,
// no instance name, version or error text. The host reads it strictly, so an
// extra key would make it report "off".
type StatusDoc struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LastOK   *time.Time `json:"lastOK,omitempty"`
	Failures int        `json:"failures"`
}

// Runner owns one tick loop against one local instance. Everything in it is
// single-goroutine: Tick being non-re-entrant is the control, not a limitation.
type Runner struct {
	// Dir is the SQL data folder (the locked \sql subfolder). state.json,
	// status.json and bb-sql-<hour>.ndjson live directly in it.
	Dir     string
	Conn    ConnConfig
	Connect ConnectFunc
	// ConnectContext is the context-aware connect seam (W8-01). When set it
	// wins over Connect, so a stop during connect cancels the connect itself
	// rather than being noticed only after it returns. Additive: existing
	// callers and tests that assign Connect keep working.
	ConnectContext ConnectFuncContext
	Now     func() time.Time
	Log     func(format string, args ...any)

	// KeepHours bounds the sample files; 0 means defaultKeepHours.
	KeepHours int
	// MinFreePercent is the owner's free-space floor for the drive holding Dir.
	// FreePercent reads it. With no reader installed the floor cannot be checked
	// at all, so the caller must install one: the SQL service is its own process,
	// and the host recorder's housekeeping never looks inside \sql.
	MinFreePercent int
	FreePercent    func(path string) (float64, error)
	// MaxBytes is the C13 byte bound of the whole sample folder. Age retention
	// alone does not bound it: the measured worst case, every sampler at its
	// row cap on its own cadence, is ~200 MB a day (budget_test.go prints the
	// figure). 0 means no byte bound (tests); the service always sets one from
	// the owner's config.
	MaxBytes int64

	state     State
	samplers  []Sampler
	lastRun   map[string]time.Time
	lastOK    time.Time
	running   bool
	skipped   int
	q         Queryer
	closeConn func() error
	connected bool
	// connectedAt is when the live connection was opened and its ceiling
	// proved. It drives connExpired.
	connectedAt time.Time
	rotated     string // hour last swept, so rotation costs one readdir per hour
	// stateDirty is true when the authoritative state could not be persisted. It
	// is retried even while auto-off, so a failed save cannot be forgotten across
	// a restart (W10-04).
	stateDirty bool

	// gen is the source/continuity generation handed to stateful samplers as
	// Observation.Epoch. It advances on every continuity break so a delta never
	// spans an unobserved interval (W11-05).
	gen uint64
	// start anchors the default steady clock; Mono overrides it for tests.
	start time.Time
	// Mono is a steady clock (never moved by the user). Default: time since
	// start. It is compared with the wall clock to detect clock jumps (W10-09).
	Mono func() time.Duration
	// lastRotateWall/Mono/haveRotate track the previous rotation pass for the
	// wall-vs-steady clock-jump guard.
	lastRotateWall time.Time
	lastRotateMono time.Duration
	haveRotate     bool
	// repaired records sample paths whose torn tail has been checked this
	// process, so repair runs once per file, not once per append (W10-10).
	repaired map[string]bool
	// fsWriteFailures counts filesystem write/repair failures, tracked apart
	// from SQL instance failures (W10-10). It never feeds State or auto-off.
	fsWriteFailures int
}

// NewRunner loads the persisted back-off state. A corrupt or unreadable state
// file leaves the service auto-off (Load fails closed) and nothing here clears
// it: only OwnerEnable, driven by the owner, does.
func NewRunner(dir string, conn ConnConfig) *Runner {
	st, _ := Load(filepath.Join(dir, "state.json"))
	return &Runner{
		Dir:      dir,
		Conn:     conn,
		state:    st,
		samplers: Samplers(),
		lastRun:  make(map[string]time.Time),
		start:    time.Now(),
		repaired: make(map[string]bool),
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// mono reads the steady clock used for scheduling and clock-jump detection. It
// is never moved by the user, unlike the wall clock Now returns.
func (r *Runner) mono() time.Duration {
	if r.Mono != nil {
		return r.Mono()
	}
	return time.Since(r.start)
}

// breakContinuity advances the source generation so every stateful sampler
// rebaselines on its next observation (W11-05). It is called on reconnect,
// failure, disk pause, auto-off, partial ticks and clock jumps.
func (r *Runner) breakContinuity() { r.gen++ }

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

func (r *Runner) connectFn() ConnectFunc {
	if r.Connect != nil {
		return r.Connect
	}
	return defaultConnect
}

// keepHours resolves the retention default: a zero value must never mean
// "keep forever".
func (r *Runner) keepHours() int {
	if r.KeepHours > 0 {
		return r.KeepHours
	}
	return defaultKeepHours
}

// dropConn closes the one connection so the next tick reconnects and runs the
// rights ceiling again: C8 checks at every connect, not once per service start.
func (r *Runner) dropConn() {
	if r.closeConn != nil {
		_ = r.closeConn()
	}
	r.closeConn = nil
	r.q = nil
	r.connected = false
	r.connectedAt = time.Time{}
}

// StatusPath is the C11 status file inside the SQL data folder.
func StatusPath(dir string) string { return filepath.Join(dir, "status.json") }

// SamplePath is the hourly NDJSON file for t.
func SamplePath(dir string, t time.Time) string {
	return filepath.Join(dir, "bb-sql-"+t.UTC().Format(hourLayout)+".ndjson")
}

// Tick runs one sampling pass with a background parent context. Run drives
// ticks with its own context so service shutdown cancels admission, connect and
// query (W10-07); external and test drivers use Tick.
func (r *Runner) Tick(now time.Time) error {
	return r.tick(context.Background(), now)
}

// tick runs one sampling pass under parent ctx. A tick still running when the
// next is due is skipped and counted: ticks never queue and never overlap (C4).
func (r *Runner) tick(parent context.Context, now time.Time) error {
	if r.running {
		// C4: ticks never queue. Run() drives ticks in one goroutine, so this
		// only fires for an external driver - but it is the control that keeps
		// a hung tick from being followed by a pile of new ones, so it is
		// counted and logged rather than silently absorbed. The counter cannot
		// reach /status (C11 fixes those four fields), so the log is the only
		// surface it has.
		r.skipped++
		r.logf("tick: skipped, previous tick still running (%d so far)", r.skipped)
		return nil
	}
	r.running = true
	defer func() { r.running = false }()

	if r.state.AutoOff {
		// Sampling is off, but housekeeping is not: removable history must still
		// be able to restore the floor even while auto-off (W10-03). No queries
		// and no sample writes happen here.
		r.breakContinuity()
		r.housekeepOnly(now)
		// A latch that never reached disk is not a latch: keep trying to persist
		// it even though sampling is off.
		if r.stateDirty {
			if err := r.persist(); err == nil {
				return nil
			}
		}
		return r.writeStatus()
	}

	// The budget runs on the real clock, rooted in the caller's context so a
	// service stop cancels the tick instead of letting it run on (W10-07).
	ctx, cancel := context.WithTimeout(parent, tickBudget)
	defer cancel()

	if err := r.checkDisk(now); err != nil {
		r.breakContinuity()
		if e, ok := err.(*Error); ok {
			r.logf("tick: %s", e.Code)
		}
		return err
	}
	if !r.connected || r.connExpired(now) {
		if err := r.reconnect(ctx); err != nil {
			return err
		}
	}

	// One success per tick, not per sampler: OnSuccess halves the interval, so
	// calling it for every sampler would undo the back-off inside one tick.
	hadSuccess := false
	// Whole-tick slow aggregation (W10-08): the WORST used/limit ratio among the
	// samplers that ran this tick decides the strike, so a fast sibling cannot
	// erase a slow sampler's strike. OnSlow is called once per tick, not once
	// per sampler, and the demotion floor applies only at the three-strike
	// threshold (State.OnSuccess).
	ran := false
	var slowUsed, slowLimit time.Duration
	var worstRatio float64
	partial := false
	for _, s := range r.samplers {
		if !r.due(s, now) {
			continue
		}
		limit := itemLimit(s.Interval())
		timeout := remaining(ctx, limit)
		if timeout <= 0 {
			partial = true // due samplers remain: the tick budget is spent
			break
		}
		if g, ok := s.(Gate); ok {
			open, err := r.gateIsOpen(ctx, g, timeout)
			if err != nil {
				return r.fail(err)
			}
			if !open {
				r.lastRun[s.Kind()] = now
				continue
			}
		}
		text, ok := Statement(s.ID())
		if !ok {
			return r.fail(errNotAllowed)
		}
		if obs, ok := s.(ObservingSampler); ok {
			obs.Observe(Observation{Time: now.UTC(), Epoch: r.gen})
		}
		start := time.Now()
		rows, err := Query(ctx, r.q, text, timeout)
		if err != nil {
			return r.fail(err)
		}
		used := time.Since(start)
		payload, truncated, err := s.Decode(rows)
		if err != nil {
			return r.fail(err)
		}
		if err := r.appendSample(SamplePath(r.Dir, now), SampleDoc{
			V: 1, T: now.UTC(), Kind: s.Kind(), Truncated: truncated, Rows: payload,
		}); err != nil {
			// A filesystem fault is NOT a SQL failure: it never touches State,
			// never drops the healthy connection and never denies the instance
			// (W10-10). It is counted separately and retried next tick.
			r.fsWriteFailures++
			r.logf("tick: %s", CodeWriteFailed)
			return err
		}
		if ratio := float64(used) / float64(limit); ratio > worstRatio {
			worstRatio = ratio
			slowUsed, slowLimit = used, limit
		}
		ran = true
		r.lastRun[s.Kind()] = now
		r.lastOK = now
		hadSuccess = true
	}
	if partial {
		// Due samplers that never ran miss a scheduled observation: the next
		// delta must not span that unobserved gap (W11-05).
		r.breakContinuity()
	}
	if ran {
		r.state.OnSlow(slowUsed, slowLimit)
	}
	if hadSuccess {
		r.state.OnSuccess()
	}
	return r.persist()
}

// due reports whether a sampler's own interval has elapsed under the current
// back-off interval. A never-run sampler is always due. A wall clock that moved
// backwards must not suppress sampling until the stored timestamp catches up:
// a backward jump makes the sampler due at once (the clock-jump guard separately
// rebaselines deltas), so evidence is not silently stalled (W10-09).
func (r *Runner) due(s Sampler, now time.Time) bool {
	lr, ok := r.lastRun[s.Kind()]
	return !ok || now.Before(lr) || now.Sub(lr) >= max(r.state.Interval, s.Interval())
}

// connExpired reports whether the live connection has outlived C5's lifetime.
// database/sql retires the physical connection silently at that point (and
// after any transport error), so "the ceiling is checked at every connect" is
// only true if the runner notices the same moment: a login given sysadmin an
// hour later must not keep sampling on an unproven connection.
func (r *Runner) connExpired(now time.Time) bool {
	return r.connected && !r.connectedAt.IsZero() && now.Sub(r.connectedAt) >= ConnMaxLifetime
}

// reconnect opens the connection and runs the C8 ceiling check before anything
// else is sent. Rights can change while the service runs, so both happen at
// every connect and no sample is taken on an unproven connection. An existing
// connection is closed first: the service holds exactly one (C5).
func (r *Runner) reconnect(ctx context.Context) error {
	if r.connected {
		r.dropConn()
	}
	// A new physical connection is a new source generation: deltas taken before
	// it must not be subtracted from counters read after it (W11-05). The
	// ceiling is re-proven below before any sample is taken.
	r.breakContinuity()
	var q Queryer
	var closeFn func() error
	var err error
	switch {
	case r.ConnectContext != nil:
		q, closeFn, err = r.ConnectContext(ctx, r.Conn)
	case r.Connect != nil:
		q, closeFn, err = r.Connect(r.Conn)
	default:
		// Production gets the cancellable connect by default: the whole
		// connect lifecycle is bounded by ctx, not just the dial.
		q, closeFn, err = DefaultConnectContext(ctx, r.Conn)
	}
	if err != nil {
		return r.fail(err)
	}
	// A legacy ConnectFunc has no context parameter; honour cancellation here
	// so a stop during connect does not proceed to sample on a connection the
	// caller already abandoned. This is not an instance failure: State is
	// untouched.
	if ctx.Err() != nil {
		_ = closeFn()
		return Classify(ctx.Err())
	}
	r.q, r.closeConn, r.connected = q, closeFn, true
	r.connectedAt = r.now()
	if err := CheckCeiling(ctx, r.q); err != nil {
		return r.fail(err)
	}
	return nil
}

// gateIsOpen sends a gate statement and asks the sampler whether its feature
// exists here. A gate that cannot be read stays closed, so an absent feature is
// never queried instead of turning into a stream of failures. Rights errors are
// different: they are real C8/C10 failures and still fail the tick.
func (r *Runner) gateIsOpen(ctx context.Context, g Gate, timeout time.Duration) (bool, error) {
	text, ok := Statement(g.GateID())
	if !ok {
		return false, errNotAllowed
	}
	rows, err := Query(ctx, r.q, text, timeout)
	if err != nil {
		e := Classify(err)
		switch e.Code {
		case CodePermission, CodeLoginFailed, CodeTooManyRights:
			return false, e
		}
		r.logf("gate %s: %s", g.GateID(), e.Code)
		return false, nil
	}
	return g.GateOpen(rows), nil
}

// fail records one instance failure, drops the connection so the next tick
// re-checks the ceiling, persists the state, and returns the fixed code. The
// raw error is classified and then forgotten: it never reaches disk or logs.
func (r *Runner) fail(err error) error {
	e := Classify(err)
	perm := e.Code == CodePermission || e.Code == CodeLoginFailed || e.Code == CodeTooManyRights
	r.state.OnFailure(perm, e.Code == CodeTooManyRights)
	// A failed tick breaks continuity: the next successful observation must
	// rebaseline, never delta across the failure gap (W11-05).
	r.breakContinuity()
	r.dropConn()
	if err := r.persist(); err != nil {
		// The denial is in memory but not on disk. Say so with a fixed code and
		// keep the dirty flag so the next tick retries; never report a durable
		// latch that was not written.
		r.logf("tick: %s", codeStateSave)
	}
	r.logf("tick: %s", e.Code)
	return e
}

// codeStateSave is the fixed log token for a state write that did not land.
const codeStateSave = "state-save"

// persist writes the authoritative state and the status file, tracking whether
// the state actually reached disk.
func (r *Runner) persist() error {
	if err := r.saveAndStatus(); err != nil {
		r.stateDirty = true
		return err
	}
	r.stateDirty = false
	return nil
}

func (r *Runner) saveAndStatus() error {
	if err := Save(filepath.Join(r.Dir, "state.json"), r.state); err != nil {
		return err
	}
	return r.writeStatus()
}

// checkDisk separates housekeeping from sampling authorization (W10-03). The
// bounded rotation runs FIRST, even when the volume is known-low, so removable
// SQL history can restore the floor; free space is then re-read and queries and
// writes are admitted only once the floor is proven. An unreadable volume is a
// distinct fixed code from a known-low one. Neither is a SQL failure.
func (r *Runner) checkDisk(now time.Time) error {
	hour := now.UTC().Format(hourLayout)
	if hour != r.rotated {
		// The hour is only marked as swept once the sweep succeeded: a failed
		// rotation must be retried on the next tick, or one unreadable entry
		// would leave the folder unbounded for the rest of the hour with no
		// signal. The error handed back is a fixed code, because a raw os error
		// names the folder path (C10).
		if err := r.rotate(now.UTC()); err != nil {
			return errDiskSweep
		}
		r.rotated = hour
	}
	if r.FreePercent == nil || r.MinFreePercent <= 0 {
		return nil
	}
	free, err := r.FreePercent(r.Dir)
	if err != nil {
		// An unreadable volume cannot prove there is room: fail closed, with a
		// code distinct from a measured low reading (W10-03).
		return errDiskUnreadable
	}
	if free >= float64(r.MinFreePercent) {
		return nil
	}
	// Known-low: run one more bounded reclaim pass (hours may have age-expired
	// since the hourly sweep) and re-read free space after the removal. Only
	// admit sampling once the floor is proven; never write into a low volume.
	if err := r.rotate(now.UTC()); err == nil {
		if free2, err2 := r.FreePercent(r.Dir); err2 == nil && free2 >= float64(r.MinFreePercent) {
			return nil
		}
	}
	return errDiskLow
}

// housekeepOnly runs the bounded hourly rotation while sampling is paused or
// auto-off, without admitting any query or write (W10-03). A sweep failure is
// logged with a fixed code and retried next tick; it never becomes a SQL
// failure and never clears the auto-off latch.
func (r *Runner) housekeepOnly(now time.Time) {
	hour := now.UTC().Format(hourLayout)
	if hour == r.rotated {
		return
	}
	if err := r.rotate(now.UTC()); err != nil {
		r.logf("tick: %s", CodeDiskLow)
		return
	}
	r.rotated = hour
}

// errDiskSweep is the fixed code for "the sample folder could not be swept",
// which means the retention bound is not being enforced.
var errDiskSweep = &Error{CodeDiskLow, "The SQL sample folder could not be swept, so its retention bound is not enforced."}

// rotate enforces both bounds of the SQL folder: the age window (keepHours) and
// the byte cap (MaxBytes). The CURRENT writing hour (SamplePath(now)) is
// protected by name, independently of sort order, so a future-dated file left
// by a clock correction can never shield itself while real history is deleted
// (W10-09). A name that does not parse as an hour is left alone rather than
// guessed at. Age purging is bounded per pass and deferred when the wall clock
// jumps relative to the steady clock; the byte cap is bounded too and drops
// whole oldest hours first, never the current hour.
func (r *Runner) rotate(now time.Time) error {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return err
	}
	currentName := filepath.Base(SamplePath(r.Dir, now))
	cutoff := now.Add(-time.Duration(r.keepHours()) * time.Hour).Format(hourLayout)
	// A wall/monotonic discontinuity makes every file look old (forward jump) or
	// hides expiry (backward jump): defer the age rule for this pass. The byte
	// cap still applies (W10-09).
	skipAge := r.clockJumped(now)
	type sampleFile struct {
		name string
		hour string
		size int64
	}
	var kept []sampleFile
	var total int64
	ageRemoved := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "bb-sql-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		hh := strings.TrimSuffix(strings.TrimPrefix(name, "bb-sql-"), ".ndjson")
		if !validHour(hh) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		// Age purge: never the current writing hour, bounded per pass, deferred
		// on a clock jump. Future-dated files (hh > now) are not age-expired.
		if !skipAge && hh < cutoff && name != currentName && ageRemoved < maxAgeRemovalsPerPass {
			if err := os.Remove(filepath.Join(r.Dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
			ageRemoved++
			continue
		}
		kept = append(kept, sampleFile{name: name, hour: hh, size: info.Size()})
		total += info.Size()
	}
	// Retention by age does not bound the folder: the measured worst case is
	// ~200 MB a day with every sampler at its row cap, so 168 hours of it is
	// ~1.4 GB. With MaxBytes set, whole hours go from the oldest end until the
	// folder fits. The current writing hour is never a candidate; future-dated
	// files are ordinary non-current candidates (removed last, after older real
	// history), so the cap can neither empty the folder nor delete current
	// history while shielding a future file. Dropping a whole hour is honest:
	// the hour is simply missing, never zero-filled.
	byteRemoved := 0
	for r.MaxBytes > 0 && total > r.MaxBytes && byteRemoved < maxByteRemovalsPerPass {
		idx := -1
		for i, f := range kept {
			if f.name == currentName {
				continue
			}
			if idx == -1 || f.hour < kept[idx].hour {
				idx = i
			}
		}
		if idx == -1 {
			break // only the current hour remains: the cap may not empty the folder
		}
		if err := os.Remove(filepath.Join(r.Dir, kept[idx].name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= kept[idx].size
		kept = append(kept[:idx], kept[idx+1:]...)
		byteRemoved++
	}
	return nil
}

// clockJumped reports whether the wall clock diverged from the steady clock
// since the previous rotation pass, and records this pass's readings. On a jump
// the age rule is deferred and sampler continuity is broken (W10-09/W11-05).
func (r *Runner) clockJumped(now time.Time) bool {
	mono := r.mono()
	jumped := false
	if r.haveRotate {
		wall := now.Sub(r.lastRotateWall)
		steady := mono - r.lastRotateMono
		if d := wall - steady; d > clockJumpThreshold || d < -clockJumpThreshold {
			jumped = true
		}
	}
	r.lastRotateWall, r.lastRotateMono, r.haveRotate = now, mono, true
	if jumped {
		r.breakContinuity()
	}
	return jumped
}

// validHour reports whether s is exactly the file-name hour layout. The length
// gate is part of it: time.Parse accepts a short hour ("20260304-5"), and a
// name that is not exactly one hour must never be deleted.
func validHour(s string) bool {
	if len(s) != len(hourLayout) {
		return false
	}
	for i := range s {
		if i == 8 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := time.Parse(hourLayout, s)
	return err == nil
}

// writeStatus writes the C11 file atomically: a temp file in the same folder,
// then a rename, so the host never reads a half-written status. Any failure is
// the fixed filesystem code, never a raw os error (C10/W10-10).
func (r *Runner) writeStatus() error {
	var last *time.Time
	if !r.lastOK.IsZero() {
		t := r.lastOK.UTC()
		last = &t
	}
	data, err := json.Marshal(StatusDoc{
		Enabled: !r.state.AutoOff, State: r.state.State, LastOK: last, Failures: r.state.Failures,
	})
	if err != nil {
		return errWriteFailed
	}
	if err := os.MkdirAll(r.Dir, 0o700); err != nil {
		return errWriteFailed
	}
	tmp, err := os.CreateTemp(r.Dir, "status-*.tmp")
	if err != nil {
		return errWriteFailed
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return errWriteFailed
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return errWriteFailed
	}
	if err := tmp.Close(); err != nil {
		return errWriteFailed
	}
	if err := os.Rename(name, StatusPath(r.Dir)); err != nil {
		return errWriteFailed
	}
	done = true
	return nil
}

// appendSample writes one line with bounded integrity handling (W10-10): a
// pre-write offset, short-write/close/sync error propagation, rollback to that
// offset on any failure, and a one-time torn-tail repair per file so a partial
// line from a previous fault is never glued to the next record. Files are 0600
// and the folder's DACL already limits readers to SYSTEM, Administrators and the
// SQL service account. Every error is the fixed filesystem code, never a raw os
// error whose "permission" wording could be misclassified as a SQL denial.
func (r *Runner) appendSample(path string, doc SampleDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errWriteFailed
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return errWriteFailed
	}
	data = append(data, '\n')
	// Repair a torn tail once per path per process, before the first append.
	if !r.repaired[path] {
		if err := repairTornTail(path); err != nil {
			return errWriteFailed
		}
		r.repaired[path] = true
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return errWriteFailed
	}
	before, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return errWriteFailed
	}
	n, werr := f.Write(data)
	if werr != nil || n < len(data) {
		_ = f.Close()
		truncateTo(path, before)
		return errWriteFailed
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		truncateTo(path, before)
		return errWriteFailed
	}
	if err := f.Close(); err != nil {
		truncateTo(path, before)
		return errWriteFailed
	}
	return nil
}

// truncateTo cuts a sample file back to a pre-write offset. It truncates by
// name (a plain write handle, never O_APPEND): on Windows a handle opened with
// O_APPEND has no right to change the file's length, so this mirrors the host
// Store's rollback. Best effort: a failure here is logged by the caller's fixed
// code, and the next append's torn-tail repair recovers the boundary.
func truncateTo(path string, off int64) {
	_ = os.Truncate(path, off)
}

// repairTornTail cuts a partial last line off a sample file: a tail with no
// final newline is cut after the last newline, and a final line that is not
// valid JSON is cut whole. Only the last repairWindow bytes are read, so repair
// is bounded and never scans a whole hour (W10-10). A missing file is fine.
func repairTornTail(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	window := int64(repairWindow)
	if window > size {
		window = size
	}
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil && err != io.EOF {
		return err
	}
	base := size - window
	// No final newline: cut after the last newline within the window. If the
	// window holds no newline the last line is longer than the window (not a
	// shape these samples produce); leave it rather than guess a boundary.
	if buf[len(buf)-1] != '\n' {
		cut := bytes.LastIndexByte(buf, '\n') + 1
		if cut == 0 && window < size {
			return nil
		}
		return os.Truncate(path, base+int64(cut))
	}
	// Ends in a newline: the last line must still be valid JSON, or it is a
	// torn record that happens to end in a newline byte.
	end := len(buf) - 1
	start := bytes.LastIndexByte(buf[:end], '\n') + 1
	if !json.Valid(buf[start:end]) {
		return os.Truncate(path, base+int64(start))
	}
	return nil
}

// Run drives ticks until ctx is done, on monotonic scheduled deadlines (C4 /
// W10-07). The schedule is fixed-rate, not fixed-delay: a tick that overruns its
// slot does not push the next deadline back, and the slots that elapsed while it
// ran are counted as missed, never queued or run in parallel. Run's context is
// threaded through admission, connect and query so a service stop cancels the
// active tick. Exactly one tick runs at a time and Run is its only caller here,
// so the unsynchronized running flag is never touched concurrently.
func (r *Runner) Run(ctx context.Context) error {
	defer r.dropConn()
	interval := max(r.state.Interval, BaseInterval)
	next := time.Now().Add(interval)
	for {
		timer := time.NewTimer(max(time.Until(next), 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if err := r.tick(ctx, r.now()); err != nil {
			if e, ok := err.(*Error); ok {
				r.logf("tick: %s", e.Code)
			} else {
				r.logf("tick: %s", CodeFailed)
			}
		}
		// Advance the schedule on the monotonic clock, re-reading the interval so
		// a back-off change takes effect at once. Every scheduled slot that
		// elapsed during the tick is counted missed (C4: never queued/overlapped).
		interval = max(r.state.Interval, BaseInterval)
		var missed int
		next, missed = advanceSchedule(next, time.Now(), interval)
		if missed > 0 {
			r.skipped += missed
			r.logf("tick: %d scheduled slot(s) missed while busy (%d so far)", missed, r.skipped)
		}
	}
}

// advanceSchedule moves a scheduled deadline past now by whole intervals,
// returning the next deadline (>= now) and the number of slots that elapsed
// while the runner was busy (missed, never queued). A slot exactly equal to now
// is due, not missed. It is the fixed-rate arithmetic of Run, separated so it can
// be tested without real timers (W10-07).
func advanceSchedule(next, now time.Time, interval time.Duration) (time.Time, int) {
	next = next.Add(interval)
	missed := 0
	for next.Before(now) {
		missed++
		next = next.Add(interval)
	}
	return next, missed
}

// Skipped counts ticks not run because one was still running (C4).
func (r *Runner) Skipped() int { return r.skipped }

// FSWriteFailures counts filesystem write/repair failures, which are tracked
// apart from SQL instance failures and never feed State or auto-off (W10-10).
func (r *Runner) FSWriteFailures() int { return r.fsWriteFailures }

// Running reports whether a tick is executing right now.
func (r *Runner) Running() bool { return r.running }

// CurrentState copies the back-off state out for tests and diagnostics.
func (r *Runner) CurrentState() State { return r.state }

// remaining clamps an item limit to the time the tick budget has left.
func remaining(ctx context.Context, limit time.Duration) time.Duration {
	dl, ok := ctx.Deadline()
	if !ok {
		return limit
	}
	return min(limit, time.Until(dl))
}

// itemLimit is the C4 per-query budget: 15 s samples get 3 s, 60 s samples 5 s.
func itemLimit(interval time.Duration) time.Duration {
	if interval <= BaseInterval {
		return quickLimit
	}
	return longLimit
}
