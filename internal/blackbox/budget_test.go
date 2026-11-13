package blackbox

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// wsBytes is a working-set size in bytes, for fake SelfUsage inputs.
func wsBytes(n uint64) uint64 { return n << 20 }

func TestBudgetStartsAtBase(t *testing.T) {
	for _, base := range []int{5, 15, 30, 60} {
		b := NewBudget(base)
		if got := b.EffectiveInterval(); got != base {
			t.Fatalf("base %d: effective = %d, want %d", base, got, base)
		}
		if b.SkipOptional() {
			t.Fatalf("base %d: optional groups skipped at start", base)
		}
		if len(b.SlowGroups()) != 0 {
			t.Fatalf("base %d: slow groups at start: %v", base, b.SlowGroups())
		}
		if b.GroupSkipped("http", true) || b.GroupSkipped("http", false) {
			t.Fatalf("base %d: a group is skipped at start", base)
		}
	}
}

func TestBudgetSlowGroupSkipsAndMarks(t *testing.T) {
	b := NewBudget(15)
	b.GroupTiming("http", 1500*time.Millisecond, false)
	if got := b.SlowGroups(); !reflect.DeepEqual(got, []string{"http"}) {
		t.Fatalf("SlowGroups = %v, want [http]", got)
	}
	if !b.GroupSkipped("http", false) {
		t.Fatal("slow mandatory group http not skipped")
	}
	if !b.GroupSkipped("http", true) {
		t.Fatal("slow optional group http not skipped")
	}
	if b.GroupSkipped("db", false) {
		t.Fatal("fast group db reported skipped")
	}
	// Names come back sorted, not in insertion order; both optional and
	// mandatory groups are marked.
	b.GroupTiming("zeta", 2*time.Second, true)
	b.GroupTiming("alpha", 2*time.Second, true)
	if got := b.SlowGroups(); !reflect.DeepEqual(got, []string{"alpha", "http", "zeta"}) {
		t.Fatalf("SlowGroups = %v, want sorted [alpha http zeta]", got)
	}
}

func TestBudgetOverCPUSkipsOptionalFirst(t *testing.T) {
	b := NewBudget(15)
	b.SelfUsage(0.9, wsBytes(10))
	b.Cycle()
	if !b.SkipOptional() {
		t.Fatal("over CPU did not skip optional groups")
	}
	if got := b.EffectiveInterval(); got != 15 {
		t.Fatalf("first over cycle widened to %d, want base 15", got)
	}
	if !b.GroupSkipped("opt", true) {
		t.Fatal("optional group not skipped while over budget")
	}
	if b.GroupSkipped("mand", false) {
		t.Fatal("mandatory group skipped on CPU over alone")
	}
	// Only the next over cycle widens.
	b.Cycle()
	if got := b.EffectiveInterval(); got != 30 {
		t.Fatalf("second over cycle effective = %d, want 30", got)
	}
}

func TestBudgetWidensThenCaps(t *testing.T) {
	cases := []struct {
		base int
		want []int // effective interval after each over cycle
	}{
		{15, []int{15, 30, 60, 60, 60}},
		{5, []int{5, 15, 30, 60, 60}},
		{30, []int{30, 60, 60, 60}},
		{60, []int{60, 60, 60}},
	}
	for _, c := range cases {
		b := NewBudget(c.base)
		b.SelfUsage(0.9, wsBytes(10))
		for i, want := range c.want {
			b.Cycle()
			if got := b.EffectiveInterval(); got != want {
				t.Fatalf("base %d cycle %d: effective = %d, want %d", c.base, i+1, got, want)
			}
			if !b.SkipOptional() {
				t.Fatalf("base %d cycle %d: optional not skipped", c.base, i+1)
			}
		}
	}
}

func TestBudgetStepsBackAfterTenCalm(t *testing.T) {
	b := NewBudget(15)
	b.SelfUsage(0.9, wsBytes(10))
	b.Cycle() // skip optional
	b.Cycle() // 15 -> 30
	b.Cycle() // 30 -> 60
	if got := b.EffectiveInterval(); got != 60 {
		t.Fatalf("effective = %d, want 60 before calm", got)
	}
	b.SelfUsage(0.1, wsBytes(1)) // back under budget
	for i := 0; i < budgetCalmCycles-1; i++ {
		b.Cycle()
		if got := b.EffectiveInterval(); got != 60 {
			t.Fatalf("calm cycle %d stepped back early to %d", i+1, got)
		}
		if !b.SkipOptional() {
			t.Fatalf("calm cycle %d re-enabled optional early", i+1)
		}
	}
	b.Cycle() // the 10th calm cycle steps back
	if got := b.EffectiveInterval(); got != 30 {
		t.Fatalf("after 10 calm effective = %d, want 30", got)
	}
	if b.SkipOptional() {
		t.Fatal("optional groups not re-enabled after step-back")
	}
	// Calm reset: a further 9 calm cycles must not step back again.
	for i := 0; i < budgetCalmCycles-1; i++ {
		b.Cycle()
		if got := b.EffectiveInterval(); got != 30 {
			t.Fatalf("calm-reset cycle %d effective = %d, want 30", i+1, got)
		}
	}
	b.Cycle()
	if got := b.EffectiveInterval(); got != 15 {
		t.Fatalf("after second 10 calm effective = %d, want 15", got)
	}
}

func TestBudgetNeverBelowBase(t *testing.T) {
	for _, base := range []int{30, 60} {
		b := NewBudget(base)
		b.SelfUsage(0.9, wsBytes(10))
		b.Cycle()
		b.Cycle() // widen to 60 (or stay at 60 for base 60)
		b.SelfUsage(0.1, wsBytes(1))
		for i := 0; i < 2*budgetCalmCycles; i++ {
			b.Cycle()
			if got := b.EffectiveInterval(); got < base {
				t.Fatalf("base %d: effective %d dropped below base", base, got)
			}
		}
		if got := b.EffectiveInterval(); got != base {
			t.Fatalf("base %d: effective = %d, want %d", base, got, base)
		}
	}
}

func TestBudgetWSOver(t *testing.T) {
	cases := []struct {
		cpu      float64
		ws       uint64
		wantSkip bool
	}{
		{0.9, wsBytes(10), true},
		{0.1, wsBytes(60), true},
		{0.5, wsBytes(50), false}, // exactly at the limits is not over
		{0.1, wsBytes(10), false},
		{0.6, 0, true},
	}
	for _, c := range cases {
		b := NewBudget(15)
		b.SelfUsage(c.cpu, c.ws)
		b.Cycle()
		if got := b.SkipOptional(); got != c.wantSkip {
			t.Fatalf("cpu %v ws %d: SkipOptional = %v, want %v", c.cpu, c.ws, got, c.wantSkip)
		}
	}
}

func TestBudgetFastGroupClearsSlow(t *testing.T) {
	b := NewBudget(15)
	b.GroupTiming("http", 1500*time.Millisecond, false)
	b.GroupTiming("http", 500*time.Millisecond, false)
	if got := b.SlowGroups(); len(got) != 0 {
		t.Fatalf("fast timing left slow groups %v", got)
	}
	if b.GroupSkipped("http", false) {
		t.Fatal("cleared group still skipped")
	}
	// Exactly at the limit is not slow; just over is.
	b.GroupTiming("db", budgetMaxGroup, true)
	if len(b.SlowGroups()) != 0 {
		t.Fatal("timing exactly at budgetMaxGroup marked slow")
	}
	b.GroupTiming("db", budgetMaxGroup+1, true)
	if got := b.SlowGroups(); !reflect.DeepEqual(got, []string{"db"}) {
		t.Fatalf("SlowGroups = %v, want [db]", got)
	}
}

// TestSlowGroupReprobesAfterSkip checks the livelock fix: a slow mark is a
// one-cycle skip consumed by NoteSkipped, so a skipped group is re-probed and
// can never permanently pin the interval.
func TestSlowGroupReprobesAfterSkip(t *testing.T) {
	b := NewBudget(15)
	b.GroupTiming("http", 1500*time.Millisecond, false) // mandatory slow
	b.Cycle()                                           // widens immediately to 30
	if got := b.EffectiveInterval(); got != 30 {
		t.Fatalf("effective = %d, want 30 after mandatory slow cycle", got)
	}
	if !b.GroupSkipped("http", false) {
		t.Fatal("slow group not marked for skip")
	}
	// The collector skips the group this cycle and consumes the mark.
	b.NoteSkipped("http")
	if got := b.SlowGroups(); len(got) != 0 {
		t.Fatalf("NoteSkipped left slow groups %v, want empty", got)
	}
	if b.GroupSkipped("http", false) {
		t.Fatal("consumed group still skipped; it would never be re-probed")
	}
	// Under budget with no slow marks: 10 calm cycles step back to base.
	b.SelfUsage(0.1, wsBytes(1))
	for i := 0; i < budgetCalmCycles; i++ {
		b.Cycle()
	}
	if got := b.EffectiveInterval(); got != 15 {
		t.Fatalf("after 10 calm effective = %d, want base 15", got)
	}
	if got := b.SlowGroups(); len(got) != 0 {
		t.Fatalf("SlowGroups = %v, want empty", got)
	}
	if b.SkipOptional() {
		t.Fatal("optional groups not re-enabled after step-back")
	}
	// A genuinely slow group is re-marked when it runs slow again.
	b.GroupTiming("http", 2*time.Second, false)
	if got := b.SlowGroups(); !reflect.DeepEqual(got, []string{"http"}) {
		t.Fatalf("re-probed slow group not re-marked: %v", got)
	}
}

// TestMandatorySlowWidensImmediately checks that a slow MANDATORY group
// widens on the first over cycle, while an optional-only slow group skips
// optional first and widens on the next over cycle.
func TestMandatorySlowWidensImmediately(t *testing.T) {
	b := NewBudget(15)
	b.GroupTiming("core", 1500*time.Millisecond, false) // mandatory
	b.Cycle()
	if got := b.EffectiveInterval(); got != 30 {
		t.Fatalf("mandatory slow: first cycle effective = %d, want 30", got)
	}
	if !b.SkipOptional() {
		t.Fatal("mandatory slow: optional groups not skipped")
	}
	b.Cycle()
	if got := b.EffectiveInterval(); got != 60 {
		t.Fatalf("mandatory slow: second cycle effective = %d, want 60", got)
	}
	b.Cycle()
	if got := b.EffectiveInterval(); got != 60 {
		t.Fatalf("mandatory slow: effective = %d, want cap 60", got)
	}

	// Optional-only slow: skip optional first, widen on the next over cycle.
	o := NewBudget(15)
	o.GroupTiming("opt", 1500*time.Millisecond, true)
	o.Cycle()
	if !o.SkipOptional() {
		t.Fatal("optional slow: optional groups not skipped")
	}
	if got := o.EffectiveInterval(); got != 15 {
		t.Fatalf("optional slow: first cycle widened to %d, want base 15", got)
	}
	o.Cycle()
	if got := o.EffectiveInterval(); got != 30 {
		t.Fatalf("optional slow: second cycle effective = %d, want 30", got)
	}

	// A mandatory slow group widens immediately from a non-15 base too.
	m := NewBudget(30)
	m.GroupTiming("core", 2*time.Second, false)
	m.Cycle()
	if got := m.EffectiveInterval(); got != 60 {
		t.Fatalf("base 30 mandatory slow: effective = %d, want 60", got)
	}
}

// TestBudgetCycleAllocatesNothing checks the footprint rule: Cycle does only
// slow-set bookkeeping and integer math, no per-cycle slices.
func TestBudgetCycleAllocatesNothing(t *testing.T) {
	over := NewBudget(15)
	over.SelfUsage(0.9, wsBytes(10))
	if n := testing.AllocsPerRun(200, func() { over.Cycle() }); n != 0 {
		t.Fatalf("over-budget Cycle allocated %v per call, want 0", n)
	}
	calm := NewBudget(15)
	if n := testing.AllocsPerRun(200, func() { calm.Cycle() }); n != 0 {
		t.Fatalf("calm Cycle allocated %v per call, want 0", n)
	}
	slow := NewBudget(15)
	slow.GroupTiming("core", 2*time.Second, false) // exercises mandatorySlow + widen
	if n := testing.AllocsPerRun(200, func() { slow.Cycle() }); n != 0 {
		t.Fatalf("mandatory-slow Cycle allocated %v per call, want 0", n)
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

	// Readers: hammer EffectiveInterval like the HTTP status handlers.
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

	// The single tick goroutine: widen toward 60, then step back, repeatedly.
	// All non-atomic Budget fields are touched only here, as in the real loop.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := 0; round < 400; round++ {
			b.SelfUsage(0.9, wsBytes(10)) // over budget
			for i := 0; i < 4; i++ {
				b.Cycle() // widen 15 -> 30 -> 60
			}
			b.SelfUsage(0.1, wsBytes(1)) // back under budget
			for i := 0; i < budgetCalmCycles; i++ {
				b.Cycle() // step back down
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