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
// from, and are never differenced. forcedOK/timeoutsOK record whether the
// counter was readable: a SQL NULL (or negative) cumulative counter leaves no
// valid baseline for that counter, so no delta is fabricated against it.
type semaphoreCumulative struct {
	grantees, waiters    int64
	forced, timeouts     int64
	forcedOK, timeoutsOK bool
}

// semaphoresRaw is the WIDE cumulative input type (W11-01). The DMV's
// forced_grant_count / timeout_error_count are bigint and nullable, so they are
// decoded into nullable int64 here, never into the emitted int32 field. The
// difference is computed in the int64 domain and only then range-checked into
// the fixed int32 output. Widening the INPUT does not widen the emitted contract.
//
// PoolID is the numeric resource-pool id (W11-02). It is nullable/optional in
// the input contract so a reply that carried no pool id still decodes (the
// fixed bb:semaphores statement always projects the NOT NULL pool_id, so a
// production row always carries it); a missing/NULL/out-of-range pool id is an
// unreadable identity, never a fabricated 0.
type semaphoresRaw struct {
	PoolID              *int64 `json:"poolId"`
	ResourceSemaphoreID int32  `json:"resourceSemaphoreId"`
	Grantees            int32  `json:"grantees"`
	Waiters             int32  `json:"waiters"`
	Forced              *int64 `json:"forced"`
	Timeouts            *int64 `json:"timeouts"`
}

// noPoolID is the internal baseline-key sentinel for a semaphore row whose
// reply carried no readable pool id (W11-02). The fixed statement always
// projects the NOT NULL pool_id, so this only arises for a reply that did not
// come from the allowlisted statement (legacy fixtures). It is never emitted
// and cannot collide with a real pool id, which is >= 0.
const noPoolID int32 = -1

// semaphoreKey is the composite identity of one resource semaphore (W11-02):
// resource_semaphore_id is documented as NONUNIQUE and repeats in every
// resource pool, so the numeric pool_id is part of the key. Without it, same-id
// rows from different pools overwrite each other's baselines. A numeric pool id
// is not a pool name (C9).
type semaphoreKey struct {
	poolID int32
	semID  int32
}

// semaphorePoolID reads the numeric pool id into the emitted int32 domain. ok
// is false when the reply carried no pool id, SQL NULL, or a value outside
// [0, MaxInt32]: an unreadable identity is missing, never 0. The DMV declares
// pool_id int NOT NULL, so a production row always reads.
func semaphorePoolID(p *int64) (int32, bool) {
	if p == nil || *p < 0 || *p > math.MaxInt32 {
		return 0, false
	}
	return int32(*p), true
}

// readSemaphoreCounter reads one nullable cumulative counter. ok is false when
// the counter is SQL NULL or negative (an unreadable counter is missing, never
// 0); the value is only meaningful when ok is true.
func readSemaphoreCounter(p *int64) (v int64, ok bool) {
	if p == nil || *p < 0 {
		return 0, false
	}
	return *p, true
}

// semaphoresSampler samples sys.dm_exec_query_resource_semaphores every
// BaseInterval (15 s) and keeps the previous sighting per composite
// (pool_id, resource_semaphore_id) identity (W11-02). Both are numeric ids only:
// a resource pool name is text and must never reach the sample (C9).
type semaphoresSampler struct {
	prev          map[semaphoreKey]semaphoreCumulative
	obs           Observation // last observation time + source epoch (W11-05)
	baselineEpoch uint64      // epoch the current baselines were taken under
}

func (semaphoresSampler) ID() string              { return "bb:semaphores" }
func (semaphoresSampler) Kind() string            { return "semaphores" }
func (semaphoresSampler) Interval() time.Duration { return BaseInterval }

// Observe records the runner's observation time and source generation. A change
// of epoch (reconnect, failure, pause, partial tick, clock jump) invalidates the
// baselines: the next Decode rebaselines and emits no delta (W11-05).
func (s *semaphoresSampler) Observe(o Observation) { s.obs = o }

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
// Guards, in order: the cumulative counters are decoded into the wide nullable
// int64 raw type, so a lifetime value above MaxInt32 is read, not rejected
// (W11-01). A NULL/negative counter is unreadable: it leaves no baseline and no
// delta for THAT counter, but the readable gauges beside it are still emitted
// (W11-04, schema-specific null handling, not a blanket row skip). The first
// cycle (or a new source epoch) only records baselines and emits nothing. An id
// with no baseline is skipped for the cycle it first appears in. A counter going
// backwards replaces that id's baseline and skips the row. A delta that does not
// fit int32 skips the row, never wrapped. A NULL counter emits the readable
// gauges with a nil (omitted) delta. prev is rebuilt from this cycle's kept rows
// only, so it can never hold more than CapSemaphores entries and an id that
// disappeared stops costing memory; an empty reply clears the map.
func (s *semaphoresSampler) Decode(rows []Row) (any, bool, error) {
	var cur []semaphoresRaw
	truncated, err := decodeRows(rows, &cur, CapSemaphores)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		s.prev = map[semaphoreKey]semaphoreCumulative{}
		s.baselineEpoch = s.obs.Epoch
		return nil, false, nil
	}

	nextPrev := make(map[semaphoreKey]semaphoreCumulative, len(cur))
	firstCycle := s.prev == nil || s.baselineEpoch != s.obs.Epoch
	out := make([]Semaphores, 0, len(cur))
	for _, r := range cur {
		// W11-02: the baseline key is the composite (pool_id, resource_semaphore_id).
		// A readable pool id keys the row to its pool; an unreadable one (absent,
		// NULL or out of range) falls back to the noPoolID sentinel, which cannot
		// collide with a real pool id and is never emitted.
		poolID, poolOK := semaphorePoolID(r.PoolID)
		k := semaphoreKey{poolID: noPoolID, semID: r.ResourceSemaphoreID}
		if poolOK {
			k.poolID = poolID
		}
		forced, forcedOK := readSemaphoreCounter(r.Forced)
		timeouts, timeoutsOK := readSemaphoreCounter(r.Timeouts)
		cum := semaphoreCumulative{
			grantees: int64(r.Grantees), waiters: int64(r.Waiters),
			forced: forced, timeouts: timeouts,
			forcedOK: forcedOK, timeoutsOK: timeoutsOK,
		}
		nextPrev[k] = cum // the new baseline stands even when nothing is emitted
		if firstCycle {
			continue
		}
		prev, ok := s.prev[k]
		if !ok {
			continue // unseen identity: this sighting is only its baseline
		}
		// A readable counter that went backwards is a reset (instance restart,
		// resource governor reconfigure): the baseline is replaced and nothing is
		// emitted for this id, never a negative delta.
		if (forcedOK && prev.forcedOK && forced < prev.forced) ||
			(timeoutsOK && prev.timeoutsOK && timeouts < prev.timeouts) {
			continue
		}
		// Difference in the wide domain, then range-check the emitted int32. A
		// delta that cannot fit the fixed field skips the row, never wraps.
		var dForced, dTimeouts *int32
		if forcedOK && prev.forcedOK {
			d := forced - prev.forced
			if d > math.MaxInt32 {
				continue
			}
			v := int32(d)
			dForced = &v
		}
		if timeoutsOK && prev.timeoutsOK {
			d := timeouts - prev.timeouts
			if d > math.MaxInt32 {
				continue
			}
			v := int32(d)
			dTimeouts = &v
		}
		// The gauges describe the grants in flight right now, so an id with a
		// baseline is emitted even when a nullable counter was SQL NULL: that
		// counter is a missing (nil) delta beside the readable gauges, never a
		// faked zero and never a blanked row (W11-04). The numeric pool id rides
		// along when readable; an unreadable identity is omitted, never 0.
		var outPool *int32
		if poolOK {
			p := poolID
			outPool = &p
		}
		out = append(out, Semaphores{
			PoolID:              outPool,
			ResourceSemaphoreID: r.ResourceSemaphoreID,
			Grantees:            r.Grantees,
			Waiters:             r.Waiters,
			Forced:              dForced,
			Timeouts:            dTimeouts,
		})
	}

	s.prev = nextPrev
	s.baselineEpoch = s.obs.Epoch
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSamplerFactory(func() Sampler { return &schedulersSampler{} })
	RegisterSamplerFactory(func() Sampler { return &semaphoresSampler{} })
	RegisterSamplerFactory(func() Sampler { return &tempdbSampler{} })
}
