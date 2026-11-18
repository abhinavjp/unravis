package blackbox

import (
	"sync"
	"testing"
	"time"
)

// Readings used by the budget tests. Each is one tick at a 15 s cadence.
var (
	rdCalm = SelfReading{CPUPct: 0.1, WallSeconds: 15, HeapInUse: 10 << 20, Private: 50 << 20}
	rdBand = SelfReading{CPUPct: 0.8, WallSeconds: 15, HeapInUse: 28 << 20, Private: 100 << 20}
	rdOver = SelfReading{CPUPct: 2.0, WallSeconds: 15, HeapInUse: 10 << 20, Private: 50 << 20}
)

// budgetRig drives a Budget with a fake clock. A window at a 15 s step is exactly
// four ticks (4 ticks and 60 s).
type budgetRig struct {
	t   *testing.T
	b   *Budget
	now time.Time
}

func newBudgetRig(t *testing.T, base int) *budgetRig {
	t.Helper()
	r := &budgetRig{t: t, b: NewBudget(base), now: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
	// The warm-up cycles take no part in a window.
	for i := 0; i < budgetWarmupCycles; i++ {
		r.tick(rdOver)
	}
	return r
}

func (r *budgetRig) tick(rd SelfReading) {
	r.now = r.now.Add(15 * time.Second)
	r.b.Cycle(r.now, rd, true)
}

// window runs one full window (four ticks) of the reading.
func (r *budgetRig) window(rd SelfReading) {
	for i := 0; i < budgetWindowTicks; i++ {
		r.tick(rd)
	}
}

func (r *budgetRig) windows(n int, rd SelfReading) {
	for i := 0; i < n; i++ {
		r.window(rd)
	}
}

func (r *budgetRig) want(interval int, why string) {
	r.t.Helper()
	if got := r.b.EffectiveInterval(); got != interval {
		r.t.Fatalf("%s: effective = %d, want %d", why, got, interval)
	}
}

// widenBudget feeds b over-budget readings from start until it has widened by
// the given number of rungs, and returns the time of the last cycle.
func widenBudget(b *Budget, start time.Time, rungs int) time.Time {
	now := start
	for i := 0; i < budgetWarmupCycles+budgetWindowTicks*budgetOverWindows*rungs; i++ {
		now = now.Add(15 * time.Second)
		b.Cycle(now, rdOver, true)
	}
	return now
}

func TestBudgetStartsAtBase(t *testing.T) {
	for _, base := range []int{5, 15, 30, 60} {
		b := NewBudget(base)
		if got := b.EffectiveInterval(); got != base {
			t.Fatalf("base %d: effective = %d, want %d", base, got, base)
		}
		if len(b.SlowGroups()) != 0 {
			t.Fatalf("base %d: slow groups at start: %v", base, b.SlowGroups())
		}
		if b.GroupSkipped("http", true) || b.GroupSkipped("http", false) {
			t.Fatalf("base %d: a group is skipped at start", base)
		}
	}
}

// The first readings carry the cost of the first tick (opening every counter);
// they must not count against the budget.
func TestBudgetWarmupCyclesAreIgnored(t *testing.T) {
	r := newBudgetRig(t, 15) // two over-budget warm-up ticks inside
	r.windows(1, rdCalm)
	if k := r.b.LastWindow().Kind; k != WindowCalm {
		t.Fatalf("first window after warm-up = %v, want calm", k)
	}
	r.want(15, "warm-up ignored")
}

// One over window is not enough: it takes two in a row.
func TestBudgetWidensAfterTwoOverWindows(t *testing.T) {
	r := newBudgetRig(t, 15)
	r.window(rdOver)
	r.want(15, "after one over window")
	r.window(rdOver)
	r.want(30, "after two over windows")
	r.windows(2, rdOver)
	r.want(60, "after four over windows")
	r.windows(6, rdOver)
	r.want(60, "capped at 60")
}

// With a base of 5 the ladder is still 15/30/60.
func TestBudgetLadderStartsAt15FromSmallBase(t *testing.T) {
	r := newBudgetRig(t, 5)
	r.windows(2, rdOver)
	r.want(15, "5 widens to the first rung")
	r.windows(2, rdOver)
	r.want(30, "then 30")
}

func TestBudgetStepsBackAfterFiveCalmWindows(t *testing.T) {
	r := newBudgetRig(t, 15)
	r.windows(4, rdOver)
	r.want(60, "widened")
	r.windows(4, rdCalm)
	r.want(60, "four calm windows are not enough")
	r.windows(1, rdCalm)
	r.want(30, "five calm windows step back one rung")
	r.windows(5, rdCalm)
	r.want(15, "five more step back to the base")
	r.windows(10, rdCalm)
	r.want(15, "never below base")
}

func TestBudgetStepBackNeverBelowBase(t *testing.T) {
	r := newBudgetRig(t, 30)
	r.windows(2, rdOver)
	r.want(60, "30 widens to 60")
	r.windows(5, rdCalm)
	r.want(30, "back to base")
	r.windows(10, rdCalm)
	r.want(30, "stays at base 30, not 15")
}

// A window between the calm and over limits is the hysteresis band: it resets
// both counts and changes nothing.
func TestBudgetHysteresisBand(t *testing.T) {
	r := newBudgetRig(t, 15)
	r.window(rdOver)
	r.window(rdBand)
	r.window(rdOver)
	r.want(15, "over, band, over is not two in a row")
	if k := r.b.LastWindow().Kind; k != WindowOver {
		t.Fatalf("last window kind = %v", k)
	}

	r = newBudgetRig(t, 15)
	r.windows(4, rdOver)
	r.want(60, "widened")
	r.windows(4, rdCalm)
	r.window(rdBand)
	r.windows(1, rdCalm)
	r.want(60, "a band window resets the calm count")
	r.windows(4, rdCalm)
	r.want(30, "five calm windows after the band window step back")
}

// A spike on one tick does not widen: the window mean is judged.
func TestBudgetCPUIsAWindowMean(t *testing.T) {
	r := newBudgetRig(t, 15)
	spike := rdCalm
	spike.CPUPct = 3.0 // one tick at 3 percent, three at 0.1: mean 0.8
	for w := 0; w < 4; w++ {
		r.tick(spike)
		r.tick(rdCalm)
		r.tick(rdCalm)
		r.tick(rdCalm)
	}
	r.want(15, "a spike averaged over the window stays in the band")
	if k := r.b.LastWindow().Kind; k != WindowBand {
		t.Fatalf("kind = %v, want band", k)
	}
}

func TestBudgetHeapAndPrivateLimits(t *testing.T) {
	heap := rdCalm
	heap.HeapInUse = 40 << 20 // over 32 MiB
	r := newBudgetRig(t, 15)
	r.windows(2, heap)
	r.want(30, "heap over 32 MiB widens")

	priv := rdCalm
	priv.Private = 140 << 20 // over 128 MiB
	r = newBudgetRig(t, 15)
	r.windows(2, priv)
	r.want(30, "private bytes over 128 MiB widen")

	// 28 MiB heap is under the limit and over the calm value: band, not over.
	r = newBudgetRig(t, 15)
	r.windows(4, rdBand)
	r.want(15, "heap in the band does not widen")
}

// A missing reading counts as calm: it never widens and it helps step back.
func TestBudgetMissingReadingCountsAsCalm(t *testing.T) {
	r := newBudgetRig(t, 15)
	for i := 0; i < budgetWindowTicks*4; i++ {
		r.now = r.now.Add(15 * time.Second)
		r.b.Cycle(r.now, SelfReading{}, false)
	}
	r.want(15, "missing readings never widen")

	r = newBudgetRig(t, 15)
	r.windows(4, rdOver)
	r.want(60, "widened")
	for i := 0; i < budgetWindowTicks*budgetCalmWindows; i++ {
		r.now = r.now.Add(15 * time.Second)
		r.b.Cycle(r.now, SelfReading{}, false)
	}
	r.want(30, "five windows of missing readings step back")
}

// An optional group over 1 s twice in a row sits out 10 minutes, alone; then it
// is tried once more.
func TestBudgetOptionalGroupProbation(t *testing.T) {
	r := newBudgetRig(t, 15)
	b := r.b
	b.GroupTiming("tcp", 2*time.Second, true)
	if b.GroupSkipped("tcp", true) {
		t.Fatal("one slow run must not skip the group")
	}
	b.GroupTiming("tcp", 2*time.Second, true)
	if !b.GroupSkipped("tcp", true) {
		t.Fatal("two slow runs in a row must put the group on probation")
	}
	if got := b.SlowGroups(); len(got) != 1 || got[0] != "tcp" {
		t.Fatalf("SlowGroups = %v, want [tcp]", got)
	}
	if b.GroupSkipped("http", true) {
		t.Fatal("one slow group must not switch other groups off")
	}
	if b.GroupSkipped("tcp", false) {
		t.Fatal("a mandatory group is never skipped")
	}

	// Still on probation just before the time box ends...
	r.now = r.now.Add(budgetProbation - time.Second)
	b.Cycle(r.now, rdCalm, true)
	if !b.GroupSkipped("tcp", true) {
		t.Fatal("probation ended early")
	}
	// ...and over once it has passed.
	r.now = r.now.Add(2 * time.Second)
	b.Cycle(r.now, rdCalm, true)
	if b.GroupSkipped("tcp", true) {
		t.Fatal("the group must be tried again after the probation")
	}
	// Slow again on that one try: a new probation starts at once.
	b.GroupTiming("tcp", 2*time.Second, true)
	if !b.GroupSkipped("tcp", true) {
		t.Fatal("a slow retry must start a new probation")
	}
	// A fast run clears everything.
	r.now = r.now.Add(budgetProbation + time.Second)
	b.Cycle(r.now, rdCalm, true)
	b.GroupTiming("tcp", 50*time.Millisecond, true)
	b.GroupTiming("tcp", 2*time.Second, true)
	if b.GroupSkipped("tcp", true) {
		t.Fatal("a fast run must clear the strikes: one slow run after it is not two in a row")
	}
}

// A slow mandatory group is never skipped; it makes the window over budget, so
// the interval widens (after two such windows).
func TestBudgetMandatorySlowCountsAsOverWindow(t *testing.T) {
	r := newBudgetRig(t, 15)
	for w := 0; w < 2; w++ {
		r.tick(rdCalm)
		r.b.GroupTiming("diskCpu", 2*time.Second, false)
		r.b.GroupTiming("diskCpu", 2*time.Second, false)
		r.tick(rdCalm)
		r.tick(rdCalm)
		r.tick(rdCalm)
		if r.b.GroupSkipped("diskCpu", false) {
			t.Fatal("a mandatory group must never be skipped")
		}
	}
	r.want(30, "two windows with a slow mandatory group widen")
	if len(r.b.SlowGroups()) != 0 {
		t.Fatalf("a mandatory group is not on probation: %v", r.b.SlowGroups())
	}
}

func TestBudgetPersistedStateRestores(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	r := newBudgetRig(t, 15)
	r.windows(4, rdOver)
	r.want(60, "widened")
	r.b.GroupTiming("tcp", 2*time.Second, true)
	r.b.GroupTiming("tcp", 2*time.Second, true)
	st := r.b.Persisted()
	if st.Base != 15 || st.Effective != 60 || len(st.Probation) != 1 || st.Probation[0].Group != "tcp" {
		t.Fatalf("persisted = %+v", st)
	}

	// Same base: effective and probation come back.
	nb := NewBudget(15)
	nb.Restore(r.now, st)
	if nb.EffectiveInterval() != 60 {
		t.Fatalf("restored effective = %d, want 60", nb.EffectiveInterval())
	}
	if !nb.GroupSkipped("tcp", true) {
		t.Fatal("restored probation must still skip the group")
	}

	// A different base (the owner changed the interval): ignored.
	nb = NewBudget(30)
	nb.Restore(r.now, st)
	if nb.EffectiveInterval() != 30 {
		t.Fatalf("a state learned at another base must be ignored, got %d", nb.EffectiveInterval())
	}

	// Probation that already ended is dropped; an effective outside the ladder is ignored.
	nb = NewBudget(15)
	nb.Restore(r.now.Add(budgetProbation+time.Minute), st)
	if nb.GroupSkipped("tcp", true) {
		t.Fatal("expired probation must not come back")
	}
	nb = NewBudget(15)
	nb.Restore(now, BudgetState{Base: 15, Effective: 600})
	if nb.EffectiveInterval() != 15 {
		t.Fatalf("an out-of-range interval must be ignored, got %d", nb.EffectiveInterval())
	}
	nb.Restore(now, BudgetState{Base: 15, Effective: 3})
	if nb.EffectiveInterval() != 15 {
		t.Fatalf("an interval below base must be ignored, got %d", nb.EffectiveInterval())
	}
}

// Cycle is on the tick path: it must not allocate.
func TestBudgetCycleAllocatesNothing(t *testing.T) {
	r := newBudgetRig(t, 15)
	now := r.now
	n := testing.AllocsPerRun(200, func() {
		now = now.Add(15 * time.Second)
		r.b.Cycle(now, rdOver, true)
	})
	if n != 0 {
		t.Fatalf("Cycle allocated %v per call, want 0", n)
	}
}

// TestBudgetEffectiveIntervalConcurrentReads models the real split: one tick
// goroutine advances the budget (Cycle -> widen/stepBack write effective) while
// many status-server goroutines read EffectiveInterval, as Recorder.Status does
// from handleStatus. effective is an atomic.Int64, so this must not race. Run
// with -race to detect a regression; without it (CGO off) the bounds assertion
// still checks correctness under concurrent goroutines.
func TestBudgetEffectiveIntervalConcurrentReads(t *testing.T) {
	b := NewBudget(15)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if v := b.EffectiveInterval(); v < 15 || v > 60 {
					t.Errorf("effective interval %d outside the ladder bounds [15,60]", v)
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
		for round := 0; round < 200; round++ {
			for i := 0; i < budgetWindowTicks*budgetOverWindows+budgetWarmupCycles; i++ {
				now = now.Add(15 * time.Second)
				b.Cycle(now, rdOver, true)
			}
			for i := 0; i < budgetWindowTicks*budgetCalmWindows; i++ {
				now = now.Add(15 * time.Second)
				b.Cycle(now, rdCalm, true)
			}
		}
	}()

	<-done
	close(stop)
	wg.Wait()

	if v := b.EffectiveInterval(); v < 15 || v > 60 {
		t.Fatalf("final effective interval %d outside [15,60]", v)
	}
}
