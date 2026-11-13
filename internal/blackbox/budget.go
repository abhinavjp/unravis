package blackbox

import (
	"sort"
	"time"
)

// Light-budget limits (handoff §3). A counter group over budgetMaxGroup is
// skipped; the recorder itself over budgetMaxCPUPct of one core or
// budgetMaxWS of working set triggers skipping optional groups, then widening.
const (
	budgetMaxGroup   = time.Second
	budgetMaxCPUPct  = 0.5
	budgetMaxWS      = 50 << 20
	budgetCalmCycles = 10
)

// budgetLadder is the widen/step-back ladder in seconds. It is fixed at
// 15/30/60 even when the configured base is 5; the base is only the floor.
var budgetLadder = [3]int{15, 30, 60}

// Budget decides, each tick, whether to skip optional counter groups and how
// wide the sampling interval is. It is tick-loop-only: every method must be
// called from the single recorder goroutine, so no mutex is needed. Durations
// and usage are passed in; it reads no clock and starts no goroutine.
type Budget struct {
	base         int
	effective    int
	calm         int
	overBudget   bool
	skipOptional bool
	slow         map[string]bool // name -> optional flag of the slow group
}

// NewBudget starts effective at base. The widen ladder stays 15/30/60; the
// effective interval never drops below base nor rises above 60.
func NewBudget(baseInterval int) *Budget {
	return &Budget{
		base:      baseInterval,
		effective: baseInterval,
		slow:      make(map[string]bool),
	}
}

// GroupTiming records a group's last duration. Over budgetMaxGroup marks it
// slow (skipped next cycle until the mark is consumed); a fast timing clears
// the mark. optional records the group's optionality so Cycle can widen
// immediately for a slow mandatory group. The map is reused.
func (b *Budget) GroupTiming(name string, d time.Duration, optional bool) {
	if d > budgetMaxGroup {
		b.slow[name] = optional
		return
	}
	delete(b.slow, name)
}

// NoteSkipped consumes a group's slow mark once the collector has skipped it,
// making the mark a one-cycle skip: the group is re-probed next cycle instead
// of being pinned slow forever. A genuinely slow group is re-marked by
// GroupTiming each time it runs slow.
func (b *Budget) NoteSkipped(name string) {
	delete(b.slow, name)
}

// SelfUsage sets overBudget from the recorder's own cost this cycle.
func (b *Budget) SelfUsage(cpuPctOfOneCore float64, workingSetBytes uint64) {
	b.overBudget = cpuPctOfOneCore > budgetMaxCPUPct || workingSetBytes > budgetMaxWS
}

// Cycle advances the machine one tick. Over budget or any slow group first
// skips optional groups and widens one ladder step when optional groups are
// already skipped OR a slow mandatory group exists (immediate widen); with
// only optional groups slow, widening happens on the next such cycle.
// Otherwise it counts calm cycles and, after budgetCalmCycles of them, steps
// back one interval and re-enables optional groups.
func (b *Budget) Cycle() {
	if b.overBudget || len(b.slow) > 0 {
		if b.skipOptional || b.mandatorySlow() {
			b.widen()
		}
		b.skipOptional = true
		b.calm = 0
		return
	}
	b.calm++
	if b.calm >= budgetCalmCycles {
		b.stepBack()
		b.skipOptional = false
		b.calm = 0
	}
}

// mandatorySlow reports whether any group currently marked slow is
// mandatory. It only reads the reused slow map, so it allocates nothing.
func (b *Budget) mandatorySlow() bool {
	for _, optional := range b.slow {
		if !optional {
			return true
		}
	}
	return false
}

// widen moves up to the next ladder rung above effective, capped at 60.
func (b *Budget) widen() {
	for _, rung := range budgetLadder {
		if rung > b.effective {
			b.effective = rung
			return
		}
	}
}

// stepBack moves down to the highest rung below effective, never below base.
// When effective is already at the lowest rung it falls back to base.
func (b *Budget) stepBack() {
	next := b.base
	for i := len(budgetLadder) - 1; i >= 0; i-- {
		if rung := budgetLadder[i]; rung < b.effective && rung >= b.base {
			next = rung
			break
		}
	}
	if next < b.effective {
		b.effective = next
	}
}

// EffectiveInterval is the sampling interval in seconds for this cycle.
func (b *Budget) EffectiveInterval() int { return b.effective }

// SkipOptional reports whether optional counter groups are being skipped.
func (b *Budget) SkipOptional() bool { return b.skipOptional }

// SlowGroups returns the sorted names of groups currently marked slow.
func (b *Budget) SlowGroups() []string {
	if len(b.slow) == 0 {
		return nil
	}
	names := make([]string, 0, len(b.slow))
	for name := range b.slow {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GroupSkipped reports whether a group is skipped this cycle: optional groups
// while over budget, or any group marked slow.
func (b *Budget) GroupSkipped(name string, optional bool) bool {
	if optional && b.skipOptional {
		return true
	}
	_, slow := b.slow[name]
	return slow
}