// extras_rg.go: bb:rg, Resource Governor pool pressure.
//
// Source: sys.dm_resource_governor_resource_pools. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-resource-governor-resource-pools-transact-sql
// (all columns used here are bigint/int NOT NULL; total_cpu_usage_ms,
// total_memgrant_timeout_count and out_of_memory_count are cumulative since the
// last start or ALTER RESOURCE GOVERNOR RESET STATISTICS; the memory and waiter
// figures are instantaneous). Permission: VIEW SERVER STATE (VIEW SERVER
// PERFORMANCE STATE on SQL Server 2022 and later), inside the C8 ceiling.
//
// Pool NAMES are user-chosen text and are never selected: a pool is identified
// by its numeric id only (1 internal, 2 default, 256 and up user pools).
package bbsql

import (
	"reflect"
	"time"
)

const CapRG = 32 // bb:rg fetches TOP 33, keeps 32

const stmtRG = setPrefix + `SELECT TOP 33 pool_id, total_cpu_usage_ms, total_memgrant_timeout_count, out_of_memory_count,
  used_memory_kb, target_memory_kb, memgrant_waiter_count, active_memgrant_count
FROM sys.dm_resource_governor_resource_pools
ORDER BY pool_id /* unravis:bb:rg */`

// rgRaw is the cumulative+gauge input row.
type rgRaw struct {
	PoolID      uint32 `json:"poolId"`
	CPUMsCum    uint64 `json:"rgCpuMsCum"`
	TimeoutsCum uint64 `json:"rgTimeoutsCum"`
	OOMCum      uint64 `json:"rgOomCum"`
	UsedKb      uint64 `json:"rgUsedKb"`
	TargetKb    uint64 `json:"rgTargetKb"`
	Waiters     uint32 `json:"rgWaiters"`
	Active      uint32 `json:"rgActive"`
}

// ResourcePool is the bb:rg payload. The gauges are always present; the three
// deltas are nil (absent) on a baseline or reset cycle, never a false zero.
type ResourcePool struct {
	PoolID        uint32  `json:"poolId"`
	CPUMs         *uint64 `json:"cpuMs,omitempty"`
	GrantTimeouts *uint64 `json:"grantTimeouts,omitempty"`
	OOMCount      *uint64 `json:"oomCount,omitempty"`
	UsedKb        uint64  `json:"usedKb"`
	TargetKb      uint64  `json:"targetKb"`
	Waiters       uint32  `json:"waiters"`
	Active        uint32  `json:"activeGrants"`
}

type rgSampler struct {
	prev          map[uint32]rgRaw
	obs           Observation
	baselineEpoch uint64
}

func (rgSampler) ID() string              { return "bb:rg" }
func (rgSampler) Kind() string            { return "rg" }
func (rgSampler) Interval() time.Duration { return DemotedInterval }

func (s *rgSampler) Observe(o Observation) { s.obs = o }

// Decode emits one row per pool every cycle (the gauges are always valid). The
// deltas appear only when this pool was seen last cycle under the same epoch and
// none of its counters went backwards (a governor stats reset or restart).
func (s *rgSampler) Decode(rows []Row) (any, bool, error) {
	var cur []rgRaw
	truncated, err := decodeRows(rows, &cur, CapRG)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		s.prev = map[uint32]rgRaw{}
		s.baselineEpoch = s.obs.Epoch
		return nil, false, nil
	}
	next := make(map[uint32]rgRaw, len(cur))
	first := s.prev == nil || s.baselineEpoch != s.obs.Epoch
	out := make([]ResourcePool, 0, len(cur))
	for _, r := range cur {
		next[r.PoolID] = r
		row := ResourcePool{PoolID: r.PoolID, UsedKb: r.UsedKb, TargetKb: r.TargetKb, Waiters: r.Waiters, Active: r.Active}
		if !first {
			if p, ok := s.prev[r.PoolID]; ok &&
				r.CPUMsCum >= p.CPUMsCum && r.TimeoutsCum >= p.TimeoutsCum && r.OOMCum >= p.OOMCum {
				c, t, o := r.CPUMsCum-p.CPUMsCum, r.TimeoutsCum-p.TimeoutsCum, r.OOMCum-p.OOMCum
				row.CPUMs, row.GrantTimeouts, row.OOMCount = &c, &t, &o
			}
		}
		out = append(out, row)
	}
	s.prev = next
	s.baselineEpoch = s.obs.Epoch
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:rg", stmtRG, CapRG, "sys.dm_resource_governor_resource_pools")
	registerExtraColumns(map[string]string{
		"pool_id": "poolId", "total_cpu_usage_ms": "rgCpuMsCum", "total_memgrant_timeout_count": "rgTimeoutsCum",
		"out_of_memory_count": "rgOomCum", "used_memory_kb": "rgUsedKb", "target_memory_kb": "rgTargetKb",
		"memgrant_waiter_count": "rgWaiters", "active_memgrant_count": "rgActive",
	})
	registerExtraRowSpec(reflect.TypeOf(rgRaw{}), rowSpec{cols: map[string]colRule{
		"poolId": req(), "rgCpuMsCum": req(), "rgTimeoutsCum": req(), "rgOomCum": req(),
		"rgUsedKb": req(), "rgTargetKb": req(), "rgWaiters": req(), "rgActive": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &rgSampler{} })
}
