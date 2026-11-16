// Registry coverage: what the shipped runner will actually do, checked from the
// registry instead of one sampler file at a time. The per-sampler tests prove
// their own decode; this file proves the set - that every cadence is the one the
// security review allowed, that nothing reaches the tick that is not
// allowlisted, and that the C13 budget fixture measures the same schedule the
// service runs. That last one is not pedantry: the fixture once disagreed with
// the registry on three of seven kinds, and the daily storage figure was wrong
// because of it.
//
// Only the reviewed kinds are looked at. Other tests register throwaway samplers
// in this process to exercise the registry itself, and those are deliberately
// ignored here rather than cleaned up over there.
package bbsql

import (
	"testing"
	"time"
)

// documentedCadence is the cadence the review and BACKLOG.md allowed per
// sample, keyed by kind. Anything here that a reviewer disputes is a document
// change plus a code change, never a silent interval edit.
var documentedCadence = map[string]time.Duration{
	"requests":   BaseInterval,    // BL-101: every 15 s
	"waits":      DemotedInterval, // BL-102: allowed at 60 s, top 20
	"schedulers": BaseInterval,    // BL-118: 15 s
	"semaphores": BaseInterval,    // BL-118: 15 s
	"tempdb":     DemotedInterval, // BL-118: page totals every 60 s
	"filestats":  DemotedInterval, // BL-119: 60 s
	"ag":         DemotedInterval, // BL-119: 60 s, only while HADR is enabled
}

// reviewedSamplers returns the shipped samplers keyed by kind, and says so when
// one is missing or registered twice - two streams for one kind would double
// the bytes and give the analyst two answers to the same question.
func reviewedSamplers(t *testing.T) map[string]Sampler {
	t.Helper()
	byKind := map[string]Sampler{}
	for _, s := range Samplers() {
		if _, ok := documentedCadence[s.Kind()]; !ok {
			continue
		}
		if dup, seen := byKind[s.Kind()]; seen {
			t.Errorf("kind %q registered twice (%q and %q): one kind must have one sample stream",
				s.Kind(), dup.ID(), s.ID())
		}
		byKind[s.Kind()] = s
	}
	for kind := range documentedCadence {
		if _, ok := byKind[kind]; !ok {
			t.Errorf("kind %q is not registered: the sample never runs", kind)
		}
	}
	return byKind
}

func TestRegistryCadenceIsTheReviewedCadence(t *testing.T) {
	for kind, s := range reviewedSamplers(t) {
		if s.Interval() != documentedCadence[kind] {
			t.Errorf("%s cadence = %v, want %v (the documented cadence)", kind, s.Interval(), documentedCadence[kind])
		}
	}
}

// Every statement the tick may send must come from the allowlist, at a cadence
// the runner accepts, with a row cap that is not left to chance. Checked here
// because a sampler can decode perfectly and still be unreachable (wrong id) or
// unbounded (no cap).
func TestRegistrySendsOnlyAllowlistedStatements(t *testing.T) {
	for kind, s := range reviewedSamplers(t) {
		text, ok := Statement(s.ID())
		if !ok {
			t.Errorf("%s: statement id %q is not in the registry", kind, s.ID())
			continue
		}
		if !IsAllowed(text) {
			t.Errorf("%s: statement %q fails the allowlist lint", kind, s.ID())
		}
		if iv := s.Interval(); iv != BaseInterval && iv != DemotedInterval {
			t.Errorf("%s: cadence %v is neither of the two the runner accepts", kind, iv)
		}
		if rowCap := statementCap(text); rowCap <= 0 {
			t.Errorf("%s: statement %q has no row cap", kind, s.ID())
		}
	}
}

// The gated sample must say so, and the ungated ones must not: bb:ag only runs
// while the instance reports HADR enabled, while a gate on any other sample
// would let it silently never run.
func TestRegistryGatesOnlyWhatIsGated(t *testing.T) {
	byKind := reviewedSamplers(t)
	if s, ok := byKind["ag"]; ok {
		g, isGate := s.(Gate)
		if !isGate {
			t.Errorf("ag is not a Gate, so it would run on a standalone instance")
		} else if g.GateID() != "bb:hadr" {
			t.Errorf("ag gate id = %q, want bb:hadr", g.GateID())
		}
	}
	for _, kind := range []string{"requests", "waits", "schedulers", "semaphores", "tempdb", "filestats"} {
		s, ok := byKind[kind]
		if !ok {
			continue
		}
		if _, isGate := s.(Gate); isGate {
			t.Errorf("%s carries a gate but BL-101/102/118/119 allow none: the sample would silently never run", kind)
		}
	}
}

// TestBudgetFixtureMatchesTheRegistry is the anti-drift gate: the C13 footprint
// numbers are only worth anything if the fixture runs the schedule the service
// runs, with the caps the service ships.
func TestBudgetFixtureMatchesTheRegistry(t *testing.T) {
	intervals := map[string]time.Duration{}
	for _, s := range reviewedSamplers(t) {
		intervals[s.ID()] = s.Interval()
	}
	caps := map[string]int{
		"requests": CapRequests, "waits": CapWaits, "schedulers": CapSchedulers,
		"semaphores": CapSemaphores, "tempdb": CapTempdb, "filestats": CapFileStats,
		"ag": CapAG,
	}
	fixtures := append(newBudgetSamplers(realisticCounts()), newBudgetSamplers(atCapCounts())...)
	seen := map[string]bool{}
	for _, sp := range fixtures {
		want, ok := intervals[sp.id]
		if !ok {
			t.Errorf("budget fixture samples %q (%s), which no longer registers: the measurement excludes a live sampler", sp.id, sp.kind)
			continue
		}
		if sp.interval != want {
			t.Errorf("budget fixture %q runs at %v, the registry runs it at %v - the daily figures are then wrong", sp.id, sp.interval, want)
		}
		if sp.limit != caps[sp.kind] {
			t.Errorf("budget fixture %q uses cap %d, the shipped cap is %d", sp.id, sp.limit, caps[sp.kind])
		}
		seen[sp.id] = true
	}
	for id := range intervals {
		if !seen[id] {
			t.Errorf("registered sampler %q is missing from the C13 budget fixture: its bytes are unplanned", id)
		}
	}
}
