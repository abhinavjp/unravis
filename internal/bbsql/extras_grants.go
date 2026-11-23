// extras_grants.go: bb:grants, the query memory grants waiting right now.
//
// Source: sys.dm_exec_query_memory_grants. Columns and nullability checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-exec-query-memory-grants-transact-sql
// (grant_time and wait_time_ms are NULL until the grant is given; the DMV has no
// row for a query that needs no grant). Permission: VIEW SERVER STATE (VIEW
// SERVER PERFORMANCE STATE on SQL Server 2022 and later), inside the C8 ceiling.
//
// Everything is summed in SQL, so no session id, plan or SQL handle, or query
// text ever leaves the instance. Every figure is an instantaneous gauge, never
// differenced.
package bbsql

import (
	"reflect"
	"time"
)

const CapGrants = CapSingleRow

const stmtGrants = setPrefix + `SELECT COUNT(*) AS grants_total,
  ISNULL(SUM(CASE WHEN grant_time IS NULL THEN 1 ELSE 0 END), 0) AS grants_waiting,
  ISNULL(SUM(requested_memory_kb), 0) AS grants_requested_kb,
  ISNULL(SUM(granted_memory_kb), 0) AS grants_granted_kb,
  ISNULL(SUM(used_memory_kb), 0) AS grants_used_kb,
  ISNULL(MAX(CASE WHEN grant_time IS NULL THEN wait_time_ms ELSE 0 END), 0) AS grants_max_wait_ms
FROM sys.dm_exec_query_memory_grants /* unravis:bb:grants */`

// Grants is the bb:grants payload: counts and kilobytes, nothing else.
type Grants struct {
	Total       uint32 `json:"grantsTotal"`
	Waiting     uint32 `json:"grantsWaiting"`
	RequestedKb uint64 `json:"grantsRequestedKb"`
	GrantedKb   uint64 `json:"grantsGrantedKb"`
	UsedKb      uint64 `json:"grantsUsedKb"`
	MaxWaitMs   uint64 `json:"grantsMaxWaitMs"`
}

type grantsSampler struct{}

func (grantsSampler) ID() string              { return "bb:grants" }
func (grantsSampler) Kind() string            { return "grants" }
func (grantsSampler) Interval() time.Duration { return BaseInterval }

func (grantsSampler) Decode(rows []Row) (any, bool, error) {
	return extraSingleRow[Grants](rows, CapGrants)
}

func init() {
	registerExtraStatement("bb:grants", stmtGrants, CapGrants, "sys.dm_exec_query_memory_grants")
	registerExtraColumns(map[string]string{
		"grants_total": "grantsTotal", "grants_waiting": "grantsWaiting",
		"grants_requested_kb": "grantsRequestedKb", "grants_granted_kb": "grantsGrantedKb",
		"grants_used_kb": "grantsUsedKb", "grants_max_wait_ms": "grantsMaxWaitMs",
	})
	registerExtraRowSpec(reflect.TypeOf(Grants{}), rowSpec{cols: map[string]colRule{
		"grantsTotal": req(), "grantsWaiting": req(), "grantsRequestedKb": req(),
		"grantsGrantedKb": req(), "grantsUsedKb": req(), "grantsMaxWaitMs": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &grantsSampler{} })
}
