package blackbox

import (
	"errors"
	"io"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ResolvedTarget is one resolved collection target. This packet owns the
// shape; the resolver (BBX-1-08) fills it.
type ResolvedTarget struct {
	Key   string // "pool:<name>" or "svc:<name>"
	PID   uint32
	Start int64 // unix creation SECONDS (display/wire shape), part of the identity
	// StartTicks is the LOSSLESS creation identity (raw FILETIME, 100-ns ticks
	// since 1601), mirroring ProcInfo.StartTicks. When non-zero it is THE join
	// identity for the per-process groups: two same-PID lifetimes created within
	// one unix second stay distinct, which the second-granularity Start window
	// cannot do. 0 means the view carries no lossless identity and the join
	// falls back to the Start second window. Additive: existing consumers that
	// only read Start keep working unchanged.
	StartTicks int64
	Runtime    uint8  // 0 unknown, 1 .NET Framework (clr.dll), 2 .NET 10 (coreclr/hostfxr)
	Pool       string // app pool name when Key is "pool:<name>"
}

// GroupDeps carries what a group factory needs. RunHost wires Targets (nil
// when no targets are selected) and the monotonic inventory Generation from the
// Resolver. ProcessSetChanged keeps its func() bool signature for current
// callers; when Generation is set, BuildGroups derives a per-group
// ProcessSetChanged from it (dirty until that group successfully reconciles the
// newest generation), so a skipped or failed group never loses a change
// (W3-03). A caller that supplies only ProcessSetChanged keeps the shared
// edge-triggered behaviour.
type GroupDeps struct {
	Source            CounterSource
	Engine            *Engine
	Targets           func() []ResolvedTarget
	Config            Config
	Cores             int
	ProcessSetChanged func() bool
	// Generation is the current monotonic inventory generation. Optional; when
	// set, BuildGroups tracks each group's last successfully reconciled
	// generation and acknowledges only on a successful Collect.
	Generation func() uint64
	// SQLProcessSig is a signature of the running sqlservr.exe processes (their
	// PIDs and creation times). Optional; the sqlHost group re-reads the PDH object
	// list only when it changes, not on every process change.
	SQLProcessSig func() uint64
}

// Group is one counter group, collected serially within the per-group budget.
type Group interface {
	Name() string
	Optional() bool
	Collect(now time.Time, dt float64) (any, error)
}

// GroupFactory builds a group; each group file registers one from init().
type GroupFactory func(GroupDeps) (Group, error)

type registration struct {
	optional bool
	factory  GroupFactory
}

var (
	registryMu sync.Mutex
	registry   = map[string]registration{}
)

// RegisterGroup adds a factory to the registry (called from init()).
func RegisterGroup(name string, optional bool, f GroupFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = registration{optional: optional, factory: f}
}

// BuildGroups instantiates every registered factory in name order.
//
// A factory that fails is NOT dropped for the life of the recorder: it gets a
// bounded retry slot, so a provider that was unavailable at startup (a counter
// object not registered yet, an instance still starting) is picked up later
// without a restart. A factory that reports errNoTargets is intentionally absent
// — nothing is selected, so no source is created and nothing is recorded.
func BuildGroups(d GroupDeps) []Group {
	registryMu.Lock()
	regs := make(map[string]registration, len(registry))
	for name, reg := range registry {
		regs[name] = reg
	}
	registryMu.Unlock()
	names := make([]string, 0, len(regs))
	for name := range regs {
		names = append(names, name)
	}
	sort.Strings(names)
	// With a monotonic generation source, give each group its own dirty/ack
	// state: a group is dirty until it SUCCESSFULLY reconciles the newest
	// generation, so a budget skip or a failed refresh retains the change and
	// the group catches up on resume without a second process change (W3-03).
	var tracker *changeTracker
	if d.Generation != nil {
		tracker = &changeTracker{current: d.Generation, acked: make(map[string]uint64)}
	}
	var groups []Group
	for _, name := range names {
		// "collectors" can switch a source off. A source that must run (the recorder's own health picture) never does:
		// Validate refuses to switch it off, and a Config that skipped Validate is still held to that here.
		info, known := CollectorOfGroup(name)
		if known && !info.Always && !d.Config.CollectorOn(info.ID) {
			continue
		}
		gd := d
		if tracker != nil {
			groupName := name
			gd.ProcessSetChanged = func() bool { return tracker.dirty(groupName) }
		}
		g, err := regs[name].factory(gd)
		var built Group
		switch {
		case errors.Is(err, errNoTargets):
			continue
		case errors.Is(err, errNotApplicable):
			// This host cannot have the group's counters (for example no VMware
			// Tools). The group stays silent and looks again every
			// notApplicableRecheck.
			lg := &lazyGroup{
				name: name, optional: regs[name].optional,
				factory: regs[name].factory, deps: gd,
			}
			lg.dormant.Store(true)
			built = lg
		case err != nil:
			built = &lazyGroup{
				name: name, optional: regs[name].optional,
				factory: regs[name].factory, deps: gd,
			}
		default:
			built = g
		}
		if base := time.Duration(d.Config.IntervalSeconds) * time.Second; known && !info.Always && d.Config.CollectorEvery(info.ID) > base {
			built = &everyGroup{inner: built, every: d.Config.CollectorEvery(info.ID), slack: base / 2}
		}
		if tracker != nil {
			built = &trackedGroup{name: name, tracker: tracker, inner: built}
		}
		groups = append(groups, built)
	}
	return groups
}

// changeTracker publishes the monotonic inventory generation and records each
// group's last successfully reconciled generation. A group is dirty while the
// current generation is newer than its ack; a skipped or failed group never
// acks, so it stays dirty and catches up on resume (W3-03). The mutex makes it
// safe for a late worker from an earlier tick to ack while the tick goroutine
// runs another group.
type changeTracker struct {
	mu      sync.Mutex
	current func() uint64
	acked   map[string]uint64
}

func (c *changeTracker) dirty(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current() > c.acked[name]
}

func (c *changeTracker) ack(name string, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen > c.acked[name] {
		c.acked[name] = gen
	}
}

// trackedGroup acknowledges the generation a group reconciled, but ONLY when
// its Collect succeeds. The generation is captured before Collect so a late
// worker acks the generation it actually saw, never a newer one it missed. A
// failed refresh, a panic, a skip or a busy slot leaves the ack untouched, so
// the change stays pending. Name/Optional/Close delegate, preserving the
// workerSlot deadline and the close-a-busy-source guard.
type trackedGroup struct {
	name    string
	tracker *changeTracker
	inner   Group
}

func (t *trackedGroup) Name() string   { return t.name }
func (t *trackedGroup) Optional() bool { return t.inner.Optional() }

func (t *trackedGroup) Collect(now time.Time, dt float64) (any, error) {
	gen := t.tracker.current()
	v, err := t.inner.Collect(now, dt)
	if err == nil {
		t.tracker.ack(t.name, gen)
	}
	return v, err
}

func (t *trackedGroup) Close() error {
	if cl, ok := t.inner.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// notApplicableRecheck is how often a group that is not applicable on this host
// asks its factory again (a later install, for example VMware Tools, is then
// picked up without a restart).
const notApplicableRecheck = 30 * time.Minute

// dormantGroup is implemented by a group that is "not applicable" on this host
// right now (silent, re-checking on a long interval). The tick uses it so that a
// slow or still-running re-check of such a group leaves no trace either: on a
// physical server the re-check can take longer than the one-second group budget,
// and a timeout there must not read as "vm: slow" or count a budget strike.
type dormantGroup interface{ Dormant() bool }

// dormantNow reports whether g is a not-applicable group at this moment.
func dormantNow(g Group) bool {
	d, ok := g.(dormantGroup)
	return ok && d.Dormant()
}

func (g *lazyGroup) Dormant() bool { return g.dormant.Load() }

func (t *trackedGroup) Dormant() bool { return dormantNow(t.inner) }

// groupRetryMax caps the retry backoff for an unavailable group.
const groupRetryMax = 5 * time.Minute

// groupRetryDelay doubles from 15 s up to groupRetryMax.
func groupRetryDelay(failures int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < failures && d < groupRetryMax; i++ {
		d *= 2
	}
	if d > groupRetryMax {
		return groupRetryMax
	}
	return d
}

// lazyGroup stands in for a factory that could not build at startup. It retries
// on a bounded backoff and reports a fixed unavailable reason meanwhile, so the
// group's absence is visible in Missing instead of silent.
type lazyGroup struct {
	name     string
	optional bool
	factory  GroupFactory
	deps     GroupDeps
	real     Group
	next     time.Time
	failures int
	// dormant is true while the last factory answer was errNotApplicable. A
	// dormant group reports errNotApplicable (silent), not errGroupUnavailable.
	// It is atomic because the tick goroutine reads it (dormantNow) while a worker
	// that is still inside a slow recheck may write it.
	dormant atomic.Bool
}

func (g *lazyGroup) Name() string   { return g.name }
func (g *lazyGroup) Optional() bool { return g.optional }

func (g *lazyGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.real != nil {
		v, err := g.real.Collect(now, dt)
		// The built group may itself go not applicable (VMware Tools removed). The
		// tick asks this wrapper, so mirror the answer: a slow re-check of the real
		// group must stay as quiet as one of an unbuilt group.
		g.dormant.Store(errors.Is(err, errNotApplicable))
		return v, err
	}
	if g.dormant.Load() && g.next.IsZero() {
		// BuildGroups already asked the factory and got errNotApplicable: that
		// answer counts as the first check, so wait a full interval.
		g.next = now.Add(notApplicableRecheck)
		return nil, errNotApplicable
	}
	if now.Before(g.next) {
		if g.dormant.Load() {
			return nil, errNotApplicable
		}
		return nil, errGroupUnavailable
	}
	real, err := g.factory(g.deps)
	if err != nil {
		if errors.Is(err, errNotApplicable) {
			g.dormant.Store(true)
			g.failures = 0
			g.next = now.Add(notApplicableRecheck)
			return nil, errNotApplicable
		}
		g.dormant.Store(false)
		if !errors.Is(err, errNoTargets) {
			g.failures++
		}
		g.next = now.Add(groupRetryDelay(g.failures + 1))
		return nil, errGroupUnavailable
	}
	g.dormant.Store(false) // before g.real: the tick reads dormant without a lock
	g.real, g.failures = real, 0
	v, cerr := real.Collect(now, dt)
	g.dormant.Store(errors.Is(cerr, errNotApplicable))
	return v, cerr
}

// Close releases the real group once it exists; the placeholder owns nothing.
func (g *lazyGroup) Close() error {
	if cl, ok := g.real.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// registeredNames lists the registered group names, sorted (for tests).
func registeredNames() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// assignIf sets *dst only when v holds the matching pointer type: a wrong
// type must leave the field untouched (missing, never zero-filled).
func assignIf[T any](dst **T, v any) {
	if g, ok := v.(*T); ok {
		*dst = g
	}
}

// applyGroup writes a group result to its Sample field. Unknown names and
// wrong types are ignored, never a panic.
func applyGroup(s *Sample, name string, v any) {
	switch name {
	case "http":
		assignIf(&s.HTTP, v)
	case "iis":
		assignIf(&s.IIS, v)
	case "diskCpu":
		assignIf(&s.DiskCPU, v)
	case "sqlHost":
		assignIf(&s.SQLHost, v)
	case "clr":
		assignIf(&s.CLR, v)
	case "proc10":
		assignIf(&s.Proc10, v)
	case "procIO":
		assignIf(&s.ProcIO, v)
	case "events":
		assignIf(&s.Events, v)
	case "tcp":
		assignIf(&s.TCP, v)
	case "aspnet":
		assignIf(&s.ASPNET, v)
	case "memPressure":
		assignIf(&s.MemPressure, v)
	case "vm":
		assignIf(&s.VM, v)
	case "dumps":
		assignIf(&s.Dumps, v)
	case "sqlLog":
		assignIf(&s.SQLLog, v)
	case "extraCounters":
		assignIf(&s.ExtraCounters, v)
	}
}

// Fixed failure reasons. They never carry provider text (handoff §3).
var (
	errNoTargets        = errors.New("no selected targets")
	errNotApplicable    = errors.New("not applicable on this host")
	errGroupUnavailable = errors.New("counter group unavailable")
	errGroupPanic       = errors.New("counter group panicked")
	errHostBusy         = errors.New("host collection still running")
	errHostSlow         = errors.New("host collection over budget")
)

// groupOutcome is one worker's result. The channel is buffered so a worker never
// blocks delivering, even when its tick already gave up on it.
type groupOutcome struct {
	v   any
	err error
}

// workerSlot bounds one owner's work to at most ONE outstanding goroutine.
//
// Ownership is deliberate and is what makes this safe without a mutex: the tick
// goroutine is the only receiver, the only caller of Budget/Store, and the only
// writer of Sample. A worker only calls its own group's Collect and sends on its
// own channel. A result that arrives after its deadline is discarded by idle()
// and never applied to a later sample, so a late reading cannot be attributed to
// the wrong tick.
type workerSlot struct {
	ch   chan groupOutcome
	busy bool
}

// idle reports whether the slot can accept work, freeing it if a previous result
// has arrived. That result is dropped: it belongs to an older tick.
func (w *workerSlot) idle() bool {
	if !w.busy {
		return true
	}
	select {
	case <-w.ch:
		w.busy, w.ch = false, nil
		return true
	default:
		return false
	}
}

// run starts fn and waits at most limit. timedOut means the worker is still
// outstanding and the slot stays busy; the caller must not start another one.
// A panic inside fn becomes a fixed error, never a crashed process.
func (w *workerSlot) run(fn func() (any, error), limit time.Duration) (any, error, bool) {
	ch := make(chan groupOutcome, 1)
	w.ch, w.busy = ch, true
	go func() {
		defer func() {
			if recover() != nil {
				ch <- groupOutcome{err: errGroupPanic}
			}
		}()
		v, err := fn()
		ch <- groupOutcome{v: v, err: err}
	}()
	t := time.NewTimer(limit)
	defer t.Stop()
	select {
	case out := <-ch:
		w.busy, w.ch = false, nil
		return out.v, out.err, false
	case <-t.C:
		return nil, nil, true
	}
}

// groupCollector wraps the host Collector with the budgeted group pass. Each
// owner (the host collector and every group) runs under an enforceable deadline
// in its own bounded slot, so one stalled provider delays the tick by at most one
// group budget instead of holding the recorder and shutdown indefinitely.
type groupCollector struct {
	inner  Collector
	innerW workerSlot
	groups []Group
	slots  []workerSlot
	budget *Budget
	last   time.Time

	// What the last pass cost, for the self block: the host read, and the one
	// slowest group. Written by the tick loop only; read by it right after.
	hostDur  time.Duration
	slowName string
	slowDur  time.Duration
	// The groups of the last pass that were slow or did not give a reading, for
	// the diagnostics log. Reused every pass; tick loop only.
	gt        []groupTime
	groupsDur time.Duration // all groups together, this pass

	// The adaptive host budget (RV-BB-1). hostStep is how many times the budget has
	// been doubled (0, 1 or 2); slowRun counts slow or busy host reads in a row, and
	// fastRun counts host reads in a row that took under half the current budget.
	// Tick loop only.
	hostStep int
	slowRun  int
	fastRun  int
}

// The host budget goes up one step after hostStepUpAfter slow rounds in a row and
// back down one step after hostStepDownAfter quick rounds in a row, so a PC that is
// slow for a while is read with more patience, and one that recovers goes back.
const (
	hostMaxStep       = 2 // the budget is at most 4 times hostBudget
	hostStepUpAfter   = 3
	hostStepDownAfter = 20
)

// GroupsTime is the total time all counter groups took in the last pass.
func (c *groupCollector) GroupsTime() time.Duration { return c.groupsDur }

// hostBudget is how long the host read (CPU, memory, disk, network and the
// process list) may take before the round gives up. It used to be one group
// budget; on a loaded or scanned PC a process list over a second threw away the
// whole sample, round after round, with no visible reason.
var hostBudget = 2 * time.Second

// hostLimit is the host budget of this round: hostBudget doubled once per step,
// but never more than half the sampling interval (so the round still fits), and
// never less than hostBudget itself.
func (c *groupCollector) hostLimit() time.Duration {
	base := hostBudget
	limit := base << uint(c.hostStep)
	if c.budget != nil {
		if iv := c.budget.EffectiveInterval(); iv > 0 {
			if half := time.Duration(iv) * time.Second / 2; limit > half {
				limit = half
			}
		}
	}
	if limit < base {
		limit = base
	}
	return limit
}

// noteHost feeds the adaptive budget with the outcome of one host read: slow is a
// timeout or a busy slot, quick is a read that finished in under half its budget.
func (c *groupCollector) noteHost(slow, quick bool) {
	switch {
	case slow:
		c.fastRun = 0
		c.slowRun++
		if c.slowRun >= hostStepUpAfter && c.hostStep < hostMaxStep {
			c.hostStep++
			c.slowRun = 0
		}
	case quick:
		c.slowRun = 0
		c.fastRun++
		if c.fastRun >= hostStepDownAfter && c.hostStep > 0 {
			c.hostStep--
			c.fastRun = 0
		}
	default:
		c.slowRun, c.fastRun = 0, 0
	}
}

// GroupTimings reports the slow or failed groups of the last pass. The slice is
// reused: copy it to keep it.
func (c *groupCollector) GroupTimings() []groupTime { return c.gt }

// GroupCount is how many counter groups this collector runs.
func (c *groupCollector) GroupCount() int { return len(c.groups) }

// Timings reports the last pass: the host read time and the slowest group.
func (c *groupCollector) Timings() (host time.Duration, slowGroup string, slow time.Duration) {
	return c.hostDur, c.slowName, c.slowDur
}

// NewGroupCollector wraps inner so each group runs under the budget.
func NewGroupCollector(inner Collector, groups []Group, b *Budget) Collector {
	return &groupCollector{inner: inner, groups: groups, slots: make([]workerSlot, len(groups)), budget: b}
}

func (c *groupCollector) Collect(now time.Time) (Sample, error) {
	// The host read used to sit outside every per-group timer. It is bounded now
	// too; a stalled process enumeration is a missing sample, not a frozen service.
	c.gt, c.groupsDur, c.hostDur, c.slowName, c.slowDur = c.gt[:0], 0, 0, "", 0
	// A host read that is slow or still running from an earlier round no longer
	// costs the whole sample (RV-BB-1): the groups are still read, the host fields
	// stay absent (never 0), and Missing says why. The round is dropped only when
	// no group gave a reading either.
	var s Sample
	var hostErr error
	hostStart := time.Now()
	limit := c.hostLimit()
	if !c.innerW.idle() {
		hostErr = errHostBusy
		c.noteHost(true, false)
	} else {
		hv, herr, timedOut := c.innerW.run(func() (any, error) { return c.inner.Collect(now) }, limit)
		c.hostDur = time.Since(hostStart)
		switch {
		case timedOut:
			hostErr = errHostSlow
			c.noteHost(true, false)
		case herr != nil:
			c.noteHost(false, false)
			return Sample{}, herr
		default:
			c.noteHost(false, c.hostDur < limit/2)
			s, _ = hv.(Sample)
		}
	}

	var dt float64
	if !c.last.IsZero() {
		dt = now.Sub(c.last).Seconds()
	}
	c.last = now
	groupsStart := time.Now()
	defer func() { c.groupsDur = time.Since(groupsStart) }()
	groupRead := false
	for i, g := range c.groups {
		name := g.Name()
		if c.budget.GroupSkipped(name, g.Optional()) {
			// On probation: this optional group was over budget twice in a row and
			// sits out its time box. Only this group; the others keep running.
			s.Missing = append(s.Missing, name+": slow")
			c.gt = append(c.gt, groupTime{Name: name, Outcome: "skipped"})
			continue
		}
		// Groups read their counters after the host read and after earlier groups,
		// so the tick's start is already behind the real counter sample. Hand each
		// group the tick time moved on by the real time spent, so the process start
		// derived from Elapsed Time stays inside pdhCreationSlack on a busy server.
		if c.runGroup(i, g, now.Add(time.Since(hostStart)), dt, &s) {
			groupRead = true
		}
	}
	if hostErr != nil {
		if !groupRead {
			return Sample{}, hostErr
		}
		word := MissingHostSlow
		if errors.Is(hostErr, errHostBusy) {
			word = MissingHostBusy
		}
		s.Missing = append([]string{word}, s.Missing...)
	}
	s.Missing = dedupMissing(s.Missing)
	return s, nil
}

// runGroup runs one group under its deadline and applies its result. Every
// outcome that is not a reading is named in Missing, so an absent field always
// has a reason and the group's identity is never lost in a shared token. It says
// whether the group gave a reading.
func (c *groupCollector) runGroup(i int, g Group, now time.Time, dt float64, s *Sample) bool {
	name := g.Name()
	w := &c.slots[i]
	if !w.idle() {
		if dormantNow(g) {
			// A re-check of a not-applicable group is still running: say nothing.
			return false
		}
		// The previous tick is still inside this provider. Do not start another
		// worker and do not re-mark it slow: re-marking every cycle would pin the
		// group forever (the livelock this budget already had once).
		s.Missing = append(s.Missing, name+": busy")
		c.gt = append(c.gt, groupTime{Name: name, Outcome: "busy"})
		return false
	}
	start := time.Now()
	v, err, timedOut := w.run(func() (any, error) { return g.Collect(now, dt) }, budgetMaxGroup)
	if timedOut {
		if dormantNow(g) {
			// The re-check of a not-applicable group ran long. The slot stays busy
			// until it ends; nothing is named, timed or counted against the budget.
			return false
		}
		c.budget.GroupTiming(name, budgetMaxGroup+time.Millisecond, g.Optional())
		s.Missing = append(s.Missing, name+": slow")
		// A group that ran out of time is the slowest one, whatever else ran.
		c.slowName, c.slowDur = name, budgetMaxGroup
		c.gt = append(c.gt, groupTime{Name: name, Dur: budgetMaxGroup, Outcome: "timeout"})
		return false
	}
	if errors.Is(err, errNotApplicable) {
		// The group does not apply to this host: leave no trace (no Missing, no
		// timing, no budget count). The worker slot was already freed by run.
		return false
	}
	took := time.Since(start)
	if took > c.slowDur {
		c.slowName, c.slowDur = name, took
	}
	c.budget.GroupTiming(name, took, g.Optional())
	if err != nil || isNilPayload(v) {
		s.Missing = append(s.Missing, name)
		out := "empty"
		if err != nil {
			out = "error"
		}
		c.gt = append(c.gt, groupTime{Name: name, Dur: took, Outcome: out})
		return false
	}
	if took >= diagGroupMin {
		c.gt = append(c.gt, groupTime{Name: name, Dur: took, Outcome: "ok"})
	}
	applyGroup(s, name, v)
	return true
}

// isNilPayload catches a typed nil in an interface, which != nil misses. Such a
// payload is "no reading", not a zero-filled group.
func isNilPayload(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// groupCloseGrace bounds how long Close waits for outstanding workers. A source
// whose worker is still inside it is NOT closed: closing a live native query
// under a running PdhCollectQueryData would be a use-after-free. The handle is
// left to the OS at process exit instead, which is the safe trade at shutdown.
const groupCloseGrace = 2 * time.Second

func (c *groupCollector) anyBusy() bool {
	if c.innerW.busy {
		return true
	}
	for i := range c.slots {
		if c.slots[i].busy {
			return true
		}
	}
	return false
}

func (c *groupCollector) Close() error {
	deadline := time.Now().Add(groupCloseGrace)
	for c.anyBusy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	var err error
	if !c.innerW.busy {
		err = c.inner.Close()
	}
	for i, g := range c.groups {
		if c.slots[i].busy {
			continue
		}
		if cl, ok := g.(io.Closer); ok {
			if cerr := cl.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	}
	return err
}

// dedupMissing keeps the first occurrence of each name, in order.
func dedupMissing(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]bool, len(names))
	out := names[:0]
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
