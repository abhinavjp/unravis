// extras_agdetail.go: bb:agdetail, availability group queues and rates.
//
// Source: sys.dm_hadr_database_replica_states. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-hadr-database-replica-states-transact-sql
// (is_local bit, synchronization_health tinyint 0 NOT_HEALTHY, 1
// PARTIALLY_HEALTHY, 2 HEALTHY; is_suspended bit; log_send_queue_size and
// redo_queue_size bigint KB, log_send_rate and redo_rate bigint KB/s, all NULL
// when the figure does not apply to that row). Permission: VIEW SERVER STATE
// (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later), inside the C8
// ceiling.
//
// Gated by bb:hadr exactly like bb:ag: an instance without availability groups
// never has the DMV queried. Rows are grouped by database and by local/remote,
// so a primary with several secondaries gets the biggest queue and the slowest
// rate of its remotes in one row (two rows per database: 64 databases fit the
// cap); no replica id, server or availability group name is ever
// selected (the lint refuses availability_replicas, and the replica id is not
// in the select list).
package bbsql

import (
	"reflect"
	"time"
)

const CapAGDetail = 128 // bb:agdetail fetches TOP 129, keeps 128

const stmtAGDetail = setPrefix + `SELECT TOP 129 database_id,
  CASE WHEN is_local = 1 THEN 1 ELSE 0 END AS ag_local,
  MIN(synchronization_health) AS ag_health,
  MAX(CASE WHEN is_suspended = 1 THEN 1 ELSE 0 END) AS ag_suspended,
  MAX(log_send_queue_size) AS ag_send_q_kb,
  MIN(log_send_rate) AS ag_send_rate_kbs,
  MAX(redo_queue_size) AS ag_redo_q_kb,
  MIN(redo_rate) AS ag_redo_rate_kbs
FROM sys.dm_hadr_database_replica_states
GROUP BY database_id, is_local
ORDER BY database_id, is_local /* unravis:bb:agdetail */`

// AGDetail is the bb:agdetail payload. The four queue and rate figures are nil
// (absent) where SQL Server reports none for that row, never a false zero.
type AGDetail struct {
	DatabaseID uint32  `json:"databaseId"`
	Local      uint8   `json:"agLocal"`
	Health     *uint8  `json:"agHealth,omitempty"`
	Suspended  uint8   `json:"agSuspended"`
	SendQKb    *uint64 `json:"agSendQKb,omitempty"`
	SendRate   *uint64 `json:"agSendRateKbs,omitempty"`
	RedoQKb    *uint64 `json:"agRedoQKb,omitempty"`
	RedoRate   *uint64 `json:"agRedoRateKbs,omitempty"`
}

type agDetailSampler struct{}

func (agDetailSampler) ID() string              { return "bb:agdetail" }
func (agDetailSampler) Kind() string            { return "agdetail" }
func (agDetailSampler) Interval() time.Duration { return DemotedInterval }
func (agDetailSampler) GateID() string          { return "bb:hadr" }

// GateOpen is the same fail-closed answer as bb:ag.
func (agDetailSampler) GateOpen(rows []Row) bool { return agSampler{}.GateOpen(rows) }

func (agDetailSampler) Decode(rows []Row) (any, bool, error) {
	var out []AGDetail
	truncated, err := decodeRows(rows, &out, CapAGDetail)
	if err != nil {
		return nil, false, err
	}
	for _, r := range out {
		if r.Local > 1 || r.Suspended > 1 || (r.Health != nil && *r.Health > 2) {
			return nil, false, errEnumDomain
		}
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:agdetail", stmtAGDetail, CapAGDetail, "sys.dm_hadr_database_replica_states")
	registerExtraColumns(map[string]string{
		"ag_local": "agLocal", "ag_health": "agHealth", "ag_suspended": "agSuspended",
		"ag_send_q_kb": "agSendQKb", "ag_send_rate_kbs": "agSendRateKbs",
		"ag_redo_q_kb": "agRedoQKb", "ag_redo_rate_kbs": "agRedoRateKbs",
	})
	registerExtraRowSpec(reflect.TypeOf(AGDetail{}), rowSpec{cols: map[string]colRule{
		"databaseId": req(), "agLocal": req(), "agHealth": reqNull(), "agSuspended": req(),
		"agSendQKb": reqNull(), "agSendRateKbs": reqNull(), "agRedoQKb": reqNull(), "agRedoRateKbs": reqNull(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &agDetailSampler{} })
}
