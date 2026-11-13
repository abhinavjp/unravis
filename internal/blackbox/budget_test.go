package blackbox

import (
	"reflect"
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
	b.GroupTiming("http", 1500*time.Millisecond)
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
	// Names come back sorted, not in insertion order.
	b.GroupTiming("zeta", 2*time.Second)
	b.GroupTiming("alpha", 2*time.Second)
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
	b.GroupTiming("http", 1500*time.Millisecond)
	b.GroupTiming("http", 500*time.Millisecond)
	if got := b.SlowGroups(); len(got) != 0 {
		t.Fatalf("fast timing left slow groups %v", got)
	}
	if b.GroupSkipped("http", false) {
		t.Fatal("cleared group still skipped")
	}
	// Exactly at the limit is not slow; just over is.
	b.GroupTiming("db", budgetMaxGroup)
	if len(b.SlowGroups()) != 0 {
		t.Fatal("timing exactly at budgetMaxGroup marked slow")
	}
	b.GroupTiming("db", budgetMaxGroup+1)
	if got := b.SlowGroups(); !reflect.DeepEqual(got, []string{"db"}) {
		t.Fatalf("SlowGroups = %v, want [db]", got)
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
}