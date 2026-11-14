// schedulers.go holds the three BL-118 performance samples and nothing else:
// scheduler load (bb:schedulers), query resource semaphores (bb:semaphores) and
// tempdb page totals (bb:tempdb). The shapes are exactly the ones the BL-118
// security review allowed: schedulers summed in SQL over VISIBLE ONLINE rows
// only (no per-scheduler id ever leaves the instance), semaphore
// grantee/waiter/forced/timeout counts with every difference computed in Go,
// and tempdb page totals summed in SQL with no per-session id. No text, no
// names, no logins: a column outside the fixed structs is rejected by
// decodeRows, never filtered away here. Samplers are not safe for concurrent
// use and never are concurrent: the runner serialises every tick, so no mutex
// guards the baseline map below (C13).
package bbsql

import (
	"math"
	"time"
)

// schedRowHasNull reports whether one row carries a SQL NULL. conn.go scans
// every column into an `any`, and database/sql hands a NULL back as a nil
// value, so this is a presence test, not a driver type assertion (C13).
func schedRowHasNull(row Row) bool {
	for _, v := range row {
		if v == nil {
			return true
		}
	}
	return false
}

// schedSummedRowsMissing reports whether the rows of a statement that already
// SUMs in SQL carry no sample at all: no row, a row without a single column (a
// reply that cannot have come from the allowlisted SELECT), or a NULL. A SUM
// over an empty set does not return zero rows: it returns one row of NULLs, and
// decodeRows would turn those NULLs into struct zeros. Reporting "0 tasks" or
// "0 pages" for "nothing to add up" is the missing-is-0 defect BL-118 forbids,
// so the whole sample is dropped instead. Only the rows decodeRows keeps are
// inspected: a NULL in a row past the cap was never going to be a payload.
func schedSummedRowsMissing(rows []Row, keep int) bool {
	if len(rows) == 0 {
		return true
	}
	for i, row := range rows {
		if i >= keep {
			break
		}
		if len(row) == 0 || schedRowHasNull(row) {
			return true
		}
	}
	return false
}

// schedulersSampler samples scheduler load every BaseInterval (15 s). It is a
// point-in-time gauge: bb:schedulers has no GROUP BY, so SQL returns the one
// summed row and there is no baseline to keep.
type schedulersSampler struct{}

func (schedulersSampler) ID() string              { return "bb:schedulers" }
func (schedulersSampler) Kind() string            { return "schedulers" }
func (schedulersSampler) Interval() time.Duration { return BaseInterval }

// Decode emits the single summed row as one object, not a one-element array:
// with CapSchedulers == 1 there is never a choice of element to make, and an
// array of one load reading only invites the analyst to sum it again.
func (s *schedulersSampler) Decode(rows []Row) (any, bool, error) {
	var out []Schedulers
	truncated, err := decodeRows(rows, &out, CapSchedulers)
	if err != nil {
		return nil, false, err
	}
	if schedSummedRowsMissing(rows, CapSchedulers) || len(out) == 0 {
		return nil, truncated, nil
	}
	return out[0], truncated, nil
}

// tempdbSampler samples tempdb page totals on the 60 s cadence. BL-118 and the
// security review allow "tempdb page totals every 60 s"; the statement sums
// sys.dm_db_session_space_usage, a per-session DMV, so 15 s would multiply the
// cost of that SUM by four without new information (C13). The cadence is the
// one place this file deliberately follows the packet over the looser brief.
type tempdbSampler struct{}

func (tempdbSampler) ID() string              { return "bb:tempdb" }
func (tempdbSampler) Kind() string            { return "tempdb" }
func (tempdbSampler) Interval() time.Duration { return DemotedInterval }

// Decode emits the allocated page totals as read, NOT as a delta: the summed
// columns are per-session allocations, and sessions end, so the total moves
// downwards through the day without any counter reset. A difference of that
// series would blame or credit a tick for sessions coming and going, which is
// why BL-118 asked for page totals. No baseline is kept, so there is no state
// to bound here either.
func (s *tempdbSampler) Decode(rows []Row) (any, bool, error) {
	var out []Tempdb
	truncated, err := decodeRows(rows, &out, CapTempdb)
	if err != nil {
		return nil, false, err
	}
	if schedSummedRowsMissing(rows, CapTempdb) || len(out) == 0 {
		return nil, truncated, nil
	}
	return out[0], truncated, nil
}

// semaphoreCumulative is the last full sighting of one resource semaphore. The
// two cumulative counters are what gets subtracted; grantees and waiters are
// recorded so a baseline is a complete, replaceable picture of the row it came
// from, and are never differenced.
type semaphoreCumulative struct {
	grantees, waiters, forced, timeouts int64
}

// semaphoresSampler samples sys.dm_exec_query_resource_semaphores every
// BaseInterval (15 s) and keeps the previous sighting per resource_semaphore_id
// (numeric id only: a resource pool name is text and must never reach the
// sample, C9).
type semaphoresSampler struct {
	prev map[int32]semaphoreCumulative
}

func (semaphoresSampler) ID() string              { return "bb:semaphores" }
func (semaphoresSampler) Kind() string            { return "semaphores" }
func (semaphoresSampler) Interval() time.Duration { return BaseInterval }

// Decode turns one DMV reply, which mixes gauge and cumulative columns, into
// one payload row per semaphore. The rule per column is the point of this
// sampler and the thing a reviewer must be able to check:
//
//	grantee_count      gauge       -> emitted as read (current value)
//	waiter_count       gauge       -> emitted as read (current value)
//	forced_grant_count cumulative  -> emitted as current - previous (a delta)
//	timeout_count      cumulative  -> emitted as current - previous (a delta)
//
// Gauges are never differenced: a grant finishing makes grantee_count go down,
// and a "negative delta" or a faked zero would say something that never
// happened. Counters are always differenced because they only ever grow, and
// the raw value since instance start says nothing about this interval.
//
// Guards, in order: a row carrying NULL is not a sample, so it is skipped and
// its baseline is dropped (it re-baselines next cycle). The first cycle only
// records baselines and emits nothing (missing is never 0). An id with no
// baseline is skipped for the cycle it first appears in. A counter going
// backwards (instance restart, resource governor reconfigure) replaces that
// id's baseline and emits nothing for it. prev is rebuilt from this cycle's
// kept rows only, so it can never hold more than CapSemaphores entries and an
// id that disappeared stops costing memory; an empty reply clears the map.
func (s *semaphoresSampler) Decode(rows []Row) (any, bool, error) {
	var cur []Semaphores
	truncated, err := decodeRows(rows, &cur, CapSemaphores)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		s.prev = map[int32]semaphoreCumulative{}
		return nil, false, nil
	}

	nextPrev := make(map[int32]semaphoreCumulative, len(cur))
	firstCycle := s.prev == nil
	out := make([]Semaphores, 0, len(cur))
	for i, r := range cur {
		if schedRowHasNull(rows[i]) {
			continue // a NULL counter cannot be read and must not become a zero
		}
		k := r.ResourceSemaphoreID
		cum := semaphoreCumulative{
			grantees: int64(r.Grantees),
			waiters:  int64(r.Waiters),
			forced:   int64(r.Forced),
			timeouts: int64(r.Timeouts),
		}
		nextPrev[k] = cum // the new baseline stands even when nothing is emitted
		if firstCycle {
			continue
		}
		prev, ok := s.prev[k]
		if !ok {
			continue // unseen id: this sighting is only its baseline
		}
		if cum.forced < prev.forced || cum.timeouts < prev.timeouts {
			continue // counters rolled back: baseline replaced, nothing emitted
		}
		dForced := cum.forced - prev.forced
		dTimeouts := cum.timeouts - prev.timeouts
		if dForced > math.MaxInt32 || dTimeouts > math.MaxInt32 {
			continue // cannot fit the fixed field: never wrap into a negative delta
		}
		// Unlike the stall and wait deltas, an all-zero pair here is still
		// evidence: the gauges beside it describe the grants in flight right
		// now, so an id with a baseline is always emitted.
		out = append(out, Semaphores{
			ResourceSemaphoreID: k,
			Grantees:            r.Grantees,
			Waiters:             r.Waiters,
			Forced:              int32(dForced),
			Timeouts:            int32(dTimeouts),
		})
	}

	s.prev = nextPrev
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSampler(&schedulersSampler{})
	RegisterSampler(&semaphoresSampler{})
	RegisterSampler(&tempdbSampler{})
}
