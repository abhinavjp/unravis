// extras_mem.go: bb:mem (process and operating system memory) and
// bb:memclerks (the ten biggest memory clerks).
//
// Sources, checked on Microsoft Learn:
//   - sys.dm_os_process_memory (one row; kilobyte gauges, page_fault_count
//     cumulative, memory_utilization_percentage int, the two *_low flags bit)
//   - sys.dm_os_sys_memory (one row; kilobyte gauges, the two signal states bit)
//   - sys.dm_os_memory_clerks (type nvarchar(60) is an engine clerk name such as
//     MEMORYCLERK_SQLBUFFERPOOL, never user data; pages_kb and
//     virtual_memory_committed_kb bigint)
//
// Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server
// 2022 and later), inside the C8 ceiling. bb:mem cross joins the two one-row
// DMVs; that exact source text is the only join the extras approve besides the
// Standard ones. Everything is a gauge except page faults, which is a delta
// computed in Go.
package bbsql

import (
	"errors"
	"reflect"
	"regexp"
	"time"
)

const (
	CapMem       = CapSingleRow
	CapMemClerks = 10 // bb:memclerks fetches TOP 11, keeps 10
)

const memSource = "sys.dm_os_process_memory CROSS JOIN sys.dm_os_sys_memory"

const stmtMem = setPrefix + `SELECT physical_memory_in_use_kb AS mem_proc_kb,
  memory_utilization_percentage AS mem_util_pct,
  locked_page_allocations_kb AS mem_locked_kb,
  large_page_allocations_kb AS mem_large_kb,
  available_commit_limit_kb AS mem_commit_avail_kb,
  page_fault_count AS mem_faults_cum,
  CASE WHEN process_physical_memory_low = 1 THEN 1 ELSE 0 END AS mem_proc_low,
  CASE WHEN process_virtual_memory_low = 1 THEN 1 ELSE 0 END AS mem_virt_low,
  total_physical_memory_kb AS mem_sys_total_kb,
  available_physical_memory_kb AS mem_sys_avail_kb,
  total_page_file_kb AS mem_pf_total_kb,
  available_page_file_kb AS mem_pf_avail_kb,
  system_cache_kb AS mem_cache_kb,
  kernel_paged_pool_kb AS mem_paged_kb,
  kernel_nonpaged_pool_kb AS mem_nonpaged_kb,
  CASE WHEN system_high_memory_signal_state = 1 THEN 1 ELSE 0 END AS mem_sys_high,
  CASE WHEN system_low_memory_signal_state = 1 THEN 1 ELSE 0 END AS mem_sys_low
FROM sys.dm_os_process_memory CROSS JOIN sys.dm_os_sys_memory /* unravis:bb:mem */`

const stmtMemClerks = setPrefix + `SELECT TOP 11 type AS clerk_type, SUM(pages_kb) AS clerk_pages_kb,
  SUM(virtual_memory_committed_kb) AS clerk_vm_kb
FROM sys.dm_os_memory_clerks
GROUP BY type
ORDER BY SUM(pages_kb) DESC /* unravis:bb:memclerks */`

// memRaw is the decoded row; Mem is the payload with the page-fault delta.
type memRaw struct {
	ProcKb       uint64 `json:"memProcKb"`
	UtilPct      uint32 `json:"memUtilPct"`
	LockedKb     uint64 `json:"memLockedKb"`
	LargeKb      uint64 `json:"memLargeKb"`
	CommitAvail  uint64 `json:"memCommitAvailKb"`
	FaultsCum    uint64 `json:"memFaultsCum"`
	ProcLow      uint8  `json:"memProcLow"`
	VirtLow      uint8  `json:"memVirtLow"`
	SysTotalKb   uint64 `json:"memSysTotalKb"`
	SysAvailKb   uint64 `json:"memSysAvailKb"`
	PfTotalKb    uint64 `json:"memPfTotalKb"`
	PfAvailKb    uint64 `json:"memPfAvailKb"`
	CacheKb      uint64 `json:"memCacheKb"`
	PagedKb      uint64 `json:"memPagedKb"`
	NonpagedKb   uint64 `json:"memNonpagedKb"`
	SysHighState uint8  `json:"memSysHigh"`
	SysLowState  uint8  `json:"memSysLow"`
}

// Mem is the bb:mem payload. PageFaults is the number of faults since the last
// reading, absent on a baseline or reset cycle.
type Mem struct {
	ProcKb       uint64  `json:"memProcKb"`
	UtilPct      uint32  `json:"memUtilPct"`
	LockedKb     uint64  `json:"memLockedKb"`
	LargeKb      uint64  `json:"memLargeKb"`
	CommitAvail  uint64  `json:"memCommitAvailKb"`
	PageFaults   *uint64 `json:"memPageFaults,omitempty"`
	ProcLow      uint8   `json:"memProcLow"`
	VirtLow      uint8   `json:"memVirtLow"`
	SysTotalKb   uint64  `json:"memSysTotalKb"`
	SysAvailKb   uint64  `json:"memSysAvailKb"`
	PfTotalKb    uint64  `json:"memPfTotalKb"`
	PfAvailKb    uint64  `json:"memPfAvailKb"`
	CacheKb      uint64  `json:"memCacheKb"`
	PagedKb      uint64  `json:"memPagedKb"`
	NonpagedKb   uint64  `json:"memNonpagedKb"`
	SysHighState uint8   `json:"memSysHigh"`
	SysLowState  uint8   `json:"memSysLow"`
}

type memSampler struct {
	prev          *uint64
	obs           Observation
	baselineEpoch uint64
}

func (memSampler) ID() string              { return "bb:mem" }
func (memSampler) Kind() string            { return "mem" }
func (memSampler) Interval() time.Duration { return DemotedInterval }

func (s *memSampler) Observe(o Observation) { s.obs = o }

func (s *memSampler) Decode(rows []Row) (any, bool, error) {
	var out []memRaw
	truncated, err := decodeRows(rows, &out, CapMem)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		s.prev = nil
		return nil, truncated, nil
	}
	r := out[0]
	if r.ProcLow > 1 || r.VirtLow > 1 || r.SysHighState > 1 || r.SysLowState > 1 || r.UtilPct > 100 {
		return nil, false, errEnumDomain
	}
	m := Mem{
		ProcKb: r.ProcKb, UtilPct: r.UtilPct, LockedKb: r.LockedKb, LargeKb: r.LargeKb,
		CommitAvail: r.CommitAvail, ProcLow: r.ProcLow, VirtLow: r.VirtLow,
		SysTotalKb: r.SysTotalKb, SysAvailKb: r.SysAvailKb, PfTotalKb: r.PfTotalKb, PfAvailKb: r.PfAvailKb,
		CacheKb: r.CacheKb, PagedKb: r.PagedKb, NonpagedKb: r.NonpagedKb,
		SysHighState: r.SysHighState, SysLowState: r.SysLowState,
	}
	if s.prev != nil && s.baselineEpoch == s.obs.Epoch && r.FaultsCum >= *s.prev {
		d := r.FaultsCum - *s.prev
		m.PageFaults = &d
	}
	cum := r.FaultsCum
	s.prev = &cum
	s.baselineEpoch = s.obs.Epoch
	return m, truncated, nil
}

// MemClerk is one row of bb:memclerks.
type MemClerk struct {
	Type    string `json:"clerkType"`
	PagesKb uint64 `json:"clerkPagesKb"`
	VmKb    uint64 `json:"clerkVmKb"`
}

var (
	clerkTypeRe   = regexp.MustCompile("^[A-Za-z0-9_]{1,60}$")
	errClerkShape = errors.New("bbsql: invalid memory clerk type")
)

type memClerksSampler struct{}

func (memClerksSampler) ID() string              { return "bb:memclerks" }
func (memClerksSampler) Kind() string            { return "memclerks" }
func (memClerksSampler) Interval() time.Duration { return DemotedInterval }

func (memClerksSampler) Decode(rows []Row) (any, bool, error) {
	var out []MemClerk
	truncated, err := decodeRows(rows, &out, CapMemClerks)
	if err != nil {
		return nil, false, err
	}
	for _, c := range out {
		if !clerkTypeRe.MatchString(c.Type) {
			return nil, false, errClerkShape
		}
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:mem", stmtMem, CapMem, memSource)
	registerExtraColumns(map[string]string{
		"mem_proc_kb": "memProcKb", "mem_util_pct": "memUtilPct", "mem_locked_kb": "memLockedKb",
		"mem_large_kb": "memLargeKb", "mem_commit_avail_kb": "memCommitAvailKb", "mem_faults_cum": "memFaultsCum",
		"mem_proc_low": "memProcLow", "mem_virt_low": "memVirtLow", "mem_sys_total_kb": "memSysTotalKb",
		"mem_sys_avail_kb": "memSysAvailKb", "mem_pf_total_kb": "memPfTotalKb", "mem_pf_avail_kb": "memPfAvailKb",
		"mem_cache_kb": "memCacheKb", "mem_paged_kb": "memPagedKb", "mem_nonpaged_kb": "memNonpagedKb",
		"mem_sys_high": "memSysHigh", "mem_sys_low": "memSysLow",
	})
	registerExtraRowSpec(reflect.TypeOf(memRaw{}), specAll(
		"memProcKb", "memUtilPct", "memLockedKb", "memLargeKb", "memCommitAvailKb", "memFaultsCum",
		"memProcLow", "memVirtLow", "memSysTotalKb", "memSysAvailKb", "memPfTotalKb", "memPfAvailKb",
		"memCacheKb", "memPagedKb", "memNonpagedKb", "memSysHigh", "memSysLow"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &memSampler{} })

	registerExtraStatement("bb:memclerks", stmtMemClerks, CapMemClerks, "sys.dm_os_memory_clerks")
	registerExtraColumns(map[string]string{
		"clerk_type": "clerkType", "clerk_pages_kb": "clerkPagesKb", "clerk_vm_kb": "clerkVmKb",
	})
	registerExtraRowSpec(reflect.TypeOf(MemClerk{}), specAll("clerkType", "clerkPagesKb", "clerkVmKb"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &memClerksSampler{} })
}
