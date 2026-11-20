package blackbox

import (
	"sort"
	"sync/atomic"
	"time"
)

// Light-budget limits. The numbers come from the performance review (answer 2).
//
// Memory is judged on what the Blackbox controls: the Go heap in use and the
// private (committed) bytes, never the working set, which carries the runtime,
// mapped DLLs and the PDH tables (the old 50 MB working-set rule was over budget
// on almost every tick). CPU is the mean over a window, not one tick.
const (
	// budgetMaxGroup is how long one group may take before it counts as slow.
	budgetMaxGroup = time.Second

	// Over budget above these; calm only below the (lower) calm values. Between
	// the two is the hysteresis band: it neither widens nor steps back.
	budgetMaxCPUPct   = 1.0 // percent of ONE core, mean over a window
	budgetCalmCPUPct  = 0.6
	budgetMaxHeap     = 32 << 20
	budgetCalmHeap    = 24 << 20
	budgetMaxPrivate  = 128 << 20
	budgetCalmPrivate = 96 << 20

	// A window is at least this many ticks AND at least this many seconds.
	budgetWindowTicks   = 4
	budgetWindowSeconds = 60.0
	// The first cycles take no part in a window: the second reading carries the
	// cost of the first tick (opening every counter), which is start-up, not
	// steady-state, cost.
	budgetWarmupCycles = 2

	// Widen one rung after this many consecutive over windows; step back one
	// rung after this many consecutive calm windows.
	budgetOverWindows = 2
	budgetCalmWindows = 5

	// An optional group over budgetMaxGroup this many times in a row goes on
	// probation: it is skipped for budgetProbation, then tried again once.
	budgetSlowStrikes = 2
	budgetProbation   = 10 * time.Minute
)

// budgetLadder is the widen/step-back ladder in seconds. It is fixed at
// 15/30/60 even when the configured base is 5; the base is only the floor.
var budgetLadder = [3]int{15, 30, 60}

// groupState is what the budget knows about one counter group.
type groupState struct {
	optional bool
	strikes  int       // consecutive timings over budgetMaxGroup
	until    time.Time // probation end; zero when the group is not on probation
}

// Budget decides, each tick, how wide the sampling interval is and which
// optional groups sit out. Every method except EffectiveInterval must be called
// from the single recorder goroutine, so the other fields need no mutex.
// effective is an atomic.Int64 because EffectiveInterval is also read from the
// status server's HTTP goroutines while the tick loop widens and steps back.
// It reads no clock and starts no goroutine: the tick time is passed in.
//
// The rules (performance review, answer 2):
//   - a window is at least 4 ticks and 60 s; the first two cycles are skipped;
//   - a window is over when its mean CPU is above 1.0 percent of one core, or its
//     mean heap in use is above 32 MiB, or its mean private bytes above 128 MiB,
//     or a mandatory group was slow twice in a row; calm when everything is under
//     the lower calm values; otherwise it is in the hysteresis band;
//   - two over windows in a row widen one rung (15, 30, 60); five calm windows in
//     a row step back one rung; a band window resets both counts;
//   - a missing self reading counts as calm;
//   - an optional group over 1 s twice in a row is skipped for 10 minutes (its
//     own probation), then tried again; one fast run clears it. Nothing else is
//     ever skipped, and one slow group never switches the others off.
type Budget struct {
	base      int
	effective atomic.Int64

	now     time.Time // the tick time of the latest Cycle
	lastNow time.Time
	haveNow bool
	cycles  int

	// the window being filled
	wTicks     int
	wSecs      float64
	wCoreSecs  float64 // CPU seconds used, from readings that carried a CPU delta
	wCPUWall   float64 // wall seconds those readings span
	wHeapSum   float64
	wHeapN     int
	wPrivSum   float64
	wPrivN     int
	wMandSlow  bool // a mandatory group was slow twice in a row
	overWins   int
	calmWins   int
	lastWindow WindowResult
	windowSeq  int // windows judged so far; the recorder logs each new one

	groups map[string]*groupState
}

// WindowKind is how one finished window was judged.
type WindowKind int

const (
	WindowNone WindowKind = iota
	WindowCalm
	WindowBand
	WindowOver
)

// WindowResult is the verdict of the latest finished window (for tests and the
// recorder's log; it allocates nothing).
type WindowResult struct {
	Kind    WindowKind
	CPUPct  float64
	HeapMB  float64
	Private float64
	// Reason names what made an over window over: cpu, heap, private or
	// slow-group (diagnostics only).
	Reason string
	// Action is what the window changed: "widen", "stepback" or "".
	Action string
	// From and To are the interval before and after Action.
	From, To int
}

// NewBudget starts effective at base. The widen ladder stays 15/30/60; the
// effective interval never drops below base nor rises above 60.
func NewBudget(baseInterval int) *Budget {
	b := &Budget{
		base:   baseInterval,
		groups: make(map[string]*groupState),
	}
	b.effective.Store(int64(baseInterval))
	return b
}

// BudgetState is what survives a restart: the interval the budget had settled on
// and the groups on probation. It is stored in the manifest (all optional). Base
// is the configured interval it was learned at; a state learned at another base
// is ignored, so a settings change starts fresh.
type BudgetState struct {
	Base      int              `json:"base"`
	Effective int              `json:"effective"`
	Probation []GroupProbation `json:"probation,omitempty"`
}

// GroupProbation is one optional group that sits out until Until.
type GroupProbation struct {
	Group string    `json:"group"`
	Until time.Time `json:"until"`
}

// Persisted is the state to keep across a restart. The probation list is sorted
// by group name. Recorder goroutine only.
func (b *Budget) Persisted() BudgetState {
	st := BudgetState{Base: b.base, Effective: int(b.effective.Load())}
	for name, g := range b.groups {
		if !g.until.IsZero() {
			st.Probation = append(st.Probation, GroupProbation{Group: name, Until: g.until.UTC()})
		}
	}
	sort.Slice(st.Probation, func(i, j int) bool { return st.Probation[i].Group < st.Probation[j].Group })
	return st
}

// Restore takes back the state of an earlier run. A state learned at another
// base interval is ignored; the interval is kept inside [base, 60] and probation
// that has already ended is dropped.
func (b *Budget) Restore(now time.Time, st BudgetState) {
	if st.Base != b.base {
		return
	}
	if e := st.Effective; e >= b.base && e <= budgetLadder[len(budgetLadder)-1] {
		b.effective.Store(int64(e))
	}
	for _, p := range st.Probation {
		if p.Group == "" || !p.Until.After(now) {
			continue
		}
		b.groups[p.Group] = &groupState{strikes: budgetSlowStrikes, until: p.Until.UTC()}
	}
}

// GroupTiming records a group's last duration. A timing over budgetMaxGroup is a
// strike; budgetSlowStrikes strikes in a row put an optional group on probation
// (and make a mandatory group count the window as over budget). A fast timing
// clears the strikes and the probation. optional is the group's optionality.
func (b *Budget) GroupTiming(name string, d time.Duration, optional bool) {
	g := b.groups[name]
	if d <= budgetMaxGroup {
		if g != nil {
			delete(b.groups, name)
		}
		return
	}
	if g == nil {
		g = &groupState{}
		b.groups[name] = g
	}
	g.optional = optional
	g.strikes++
	if g.strikes < budgetSlowStrikes {
		return
	}
	if optional {
		g.until = b.now.Add(budgetProbation)
	} else {
		b.wMandSlow = true
	}
}

// Cycle advances the budget one tick: it folds the self reading into the window
// being filled and, when the window is complete, widens, steps back or does
// nothing. now is the tick time. haveReading false is a missing reading and
// counts as calm. Call it once per tick, before the collector runs.
func (b *Budget) Cycle(now time.Time, r SelfReading, haveReading bool) {
	dt := 0.0
	if b.haveNow {
		if d := now.Sub(b.lastNow).Seconds(); d > 0 {
			dt = d
		}
	}
	b.lastNow, b.now, b.haveNow = now, now, true
	b.cycles++
	if b.cycles <= budgetWarmupCycles {
		return
	}
	b.wTicks++
	b.wSecs += dt
	if haveReading {
		if r.WallSeconds > 0 {
			b.wCoreSecs += r.CPUPct / 100 * r.WallSeconds
			b.wCPUWall += r.WallSeconds
		}
		if r.HeapInUse > 0 {
			b.wHeapSum += float64(r.HeapInUse)
			b.wHeapN++
		}
		if r.Private > 0 {
			b.wPrivSum += float64(r.Private)
			b.wPrivN++
		}
	}
	if b.wTicks < budgetWindowTicks || b.wSecs < budgetWindowSeconds {
		return
	}
	b.closeWindow()
}

// closeWindow judges the finished window and starts the next one.
func (b *Budget) closeWindow() {
	var cpu, heap, priv float64
	if b.wCPUWall > 0 {
		cpu = b.wCoreSecs / b.wCPUWall * 100
	}
	if b.wHeapN > 0 {
		heap = b.wHeapSum / float64(b.wHeapN)
	}
	if b.wPrivN > 0 {
		priv = b.wPrivSum / float64(b.wPrivN)
	}
	over := cpu > budgetMaxCPUPct || heap > budgetMaxHeap || priv > budgetMaxPrivate || b.wMandSlow
	calm := !over && cpu < budgetCalmCPUPct && heap < budgetCalmHeap && priv < budgetCalmPrivate
	kind := WindowBand
	reason, action := "", ""
	from := int(b.effective.Load())
	switch {
	case over:
		kind = WindowOver
		switch {
		case cpu > budgetMaxCPUPct:
			reason = "cpu"
		case heap > budgetMaxHeap:
			reason = "heap"
		case priv > budgetMaxPrivate:
			reason = "private"
		default:
			reason = "slow-group"
		}
		b.calmWins = 0
		b.overWins++
		if b.overWins >= budgetOverWindows {
			b.widen()
			b.overWins = 0
			action = "widen"
		}
	case calm:
		kind = WindowCalm
		b.overWins = 0
		b.calmWins++
		if b.calmWins >= budgetCalmWindows {
			b.stepBack()
			b.calmWins = 0
			action = "stepback"
		}
	default:
		b.overWins, b.calmWins = 0, 0
	}
	to := int(b.effective.Load())
	if to == from {
		action = ""
	}
	b.windowSeq++
	b.lastWindow = WindowResult{Kind: kind, CPUPct: cpu, HeapMB: heap / mib, Private: priv / mib,
		Reason: reason, Action: action, From: from, To: to}
	b.wTicks, b.wSecs = 0, 0
	b.wCoreSecs, b.wCPUWall = 0, 0
	b.wHeapSum, b.wHeapN, b.wPrivSum, b.wPrivN = 0, 0, 0, 0
	b.wMandSlow = false
}

// WindowSeq counts the windows judged so far.
func (b *Budget) WindowSeq() int { return b.windowSeq }

// LastWindow is the verdict of the latest finished window.
func (b *Budget) LastWindow() WindowResult { return b.lastWindow }

// widen moves up to the next ladder rung above effective, capped at 60.
func (b *Budget) widen() {
	eff := b.effective.Load()
	for _, rung := range budgetLadder {
		if int64(rung) > eff {
			b.effective.Store(int64(rung))
			return
		}
	}
}

// stepBack moves down to the highest rung below effective, never below base.
// When effective is already at the lowest rung it falls back to base.
func (b *Budget) stepBack() {
	eff := b.effective.Load()
	next := int64(b.base)
	for i := len(budgetLadder) - 1; i >= 0; i-- {
		if rung := int64(budgetLadder[i]); rung < eff && rung >= int64(b.base) {
			next = rung
			break
		}
	}
	if next < eff {
		b.effective.Store(next)
	}
}

// EffectiveInterval is the sampling interval in seconds for this cycle. It is
// safe to call from any goroutine (the status server reads it concurrently with
// the tick loop's widen/stepBack).
func (b *Budget) EffectiveInterval() int { return int(b.effective.Load()) }

// SlowGroups returns the sorted names of the groups on probation right now.
func (b *Budget) SlowGroups() []string {
	var names []string
	for name, g := range b.groups {
		if b.onProbation(g) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (b *Budget) onProbation(g *groupState) bool {
	return !g.until.IsZero() && b.now.Before(g.until)
}

// GroupSkipped reports whether a group sits out this cycle: only an optional
// group on probation does. When the probation ends the group runs once more; a
// slow run starts a new probation, a fast one clears it.
func (b *Budget) GroupSkipped(name string, optional bool) bool {
	if !optional {
		return false
	}
	g := b.groups[name]
	return g != nil && b.onProbation(g)
}
