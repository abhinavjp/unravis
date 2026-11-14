// waits.go holds the bb:waits sampler (BL-102): the per-wait-type DELTA of the
// cumulative sys.dm_os_wait_stats counters, top 20 waits, benign waits already
// filtered in SQL by the allowlisted statement. The payload is a validated wait
// type and two numbers — no query text, no resource description, no session
// data — and the deltas are computed here in Go, never in SQL. Samplers are not
// safe for concurrent use and never are concurrent: the runner serialises every
// tick.
package bbsql

import "time"

// waitsCumulative is the last cumulative sighting of one wait type, kept apart
// from Waits so the payload struct only ever carries deltas.
type waitsCumulative struct {
	ms    uint64
	tasks uint64
}

// waitsSampler turns the cumulative wait counters into per-cycle deltas. prev is
// bounded by CapWaits: it is rebuilt from the (cap-limited) rows of this cycle
// every time, so keys that left the top 20 cannot accumulate.
type waitsSampler struct {
	prev map[string]waitsCumulative
}

func (waitsSampler) ID() string   { return "bb:waits" }
func (waitsSampler) Kind() string { return "waits" }

// Interval is the 60 s the security review allowed BL-102 at (and BACKLOG.md
// BL-102 repeats): the deltas are per-interval, so a 15 s cadence would write
// four times the lines for the same information.
func (waitsSampler) Interval() time.Duration { return DemotedInterval }

// Decode emits, per wait type, the increase since the previous sighting of that
// same type. The rules that keep the series honest:
//
//   - The first cycle after startup only records the baseline and emits
//     nothing: a delta with nothing to subtract would be a lie, and "unreadable
//     means missing, never 0".
//   - bb:waits is TOP 20 ORDER BY wait_time_ms DESC, so the row SET changes
//     between ticks. A wait type with no baseline — new, or one that fell out of
//     the top 20 and has come back — is recorded as a baseline and emits
//     nothing this cycle: the gap spans an unobserved interval, so any number
//     computed over it would be a false delta.
//   - A counter that went backwards (instance restart, DBCC SQLPERF(...,RESET))
//     replaces that wait type's baseline and emits nothing for it this cycle.
//     The following cycle is measured against the reset value.
//   - A wait type whose deltas are all zero is dropped: an idle wait carries no
//     information, and the 20-row cap makes payload the scarce resource.
//   - An empty reply clears every baseline, because a stale snapshot cannot
//     later be subtracted from an unknown interval.
func (s *waitsSampler) Decode(rows []Row) (any, bool, error) {
	// decodeRows lands the CUMULATIVE wait_time_ms / waiting_tasks_count into
	// Waits.DeltaMs / DeltaTasks (the columnKeys mapping); this function is
	// what turns them into deltas. It returns its fixed error untouched.
	var cur []Waits
	truncated, err := decodeRows(rows, &cur, CapWaits)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		// Nothing seen: every baseline is stale, and an empty reply is not a
		// sample.
		s.prev = map[string]waitsCumulative{}
		return nil, false, nil
	}

	nextPrev := make(map[string]waitsCumulative, len(cur))
	firstCycle := s.prev == nil
	out := make([]Waits, 0, len(cur))
	for _, r := range cur {
		cum := waitsCumulative{ms: r.DeltaMs, tasks: r.DeltaTasks}
		// The baseline is the current sighting, whether or not anything is
		// emitted for it: rebuilding the map from these rows is what bounds it.
		nextPrev[r.WaitType] = cum
		if firstCycle {
			continue
		}
		prev, ok := s.prev[r.WaitType]
		if !ok {
			continue // unseen key (new or re-entered): baseline only, no delta
		}
		if cum.ms < prev.ms || cum.tasks < prev.tasks {
			continue // counters reset: baseline replaced, nothing emitted
		}
		deltaMs := cum.ms - prev.ms
		deltaTasks := cum.tasks - prev.tasks
		if deltaMs == 0 && deltaTasks == 0 {
			continue // idle wait type: nothing to report, no zero rows written
		}
		out = append(out, Waits{WaitType: r.WaitType, DeltaMs: deltaMs, DeltaTasks: deltaTasks})
	}

	s.prev = nextPrev
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSampler(&waitsSampler{})
}
