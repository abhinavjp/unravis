// extras_conns.go: bb:conns, session counts.
//
// Source: sys.dm_exec_sessions. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-exec-sessions-transact-sql
// (is_user_process bit, status nvarchar, open_transaction_count int,
// last_request_end_time datetime). Permission: VIEW SERVER STATE (VIEW SERVER
// PERFORMANCE STATE on SQL Server 2022 and later), inside the C8 ceiling.
//
// Counted in SQL. The login time, login name, host and program columns are
// never selected (the lint refuses them), and no session id leaves the
// instance. The plan lists dm_exec_connections as a second source; one
// statement reads one source, and the session counts carry the same
// information the investigation needs.
package bbsql

import (
	"reflect"
	"time"
)

const CapConns = CapSingleRow

const stmtConns = setPrefix + `SELECT COUNT(*) AS conns_total,
  ISNULL(SUM(CASE WHEN is_user_process = 1 THEN 1 ELSE 0 END), 0) AS conns_user,
  ISNULL(SUM(CASE WHEN is_user_process = 1 AND status = N'running' THEN 1 ELSE 0 END), 0) AS conns_running,
  ISNULL(SUM(CASE WHEN is_user_process = 1 AND status = N'sleeping' THEN 1 ELSE 0 END), 0) AS conns_sleeping,
  ISNULL(SUM(CASE WHEN is_user_process = 1 AND open_transaction_count > 0 THEN 1 ELSE 0 END), 0) AS conns_in_tran,
  ISNULL(MAX(CASE WHEN is_user_process = 1 AND status = N'sleeping' AND open_transaction_count > 0 AND last_request_end_time <= GETDATE() THEN DATEDIFF(second, last_request_end_time, GETDATE()) ELSE 0 END), 0) AS conns_idle_tran_sec
FROM sys.dm_exec_sessions
WHERE session_id <> @@SPID /* unravis:bb:conns */`

// Conns is the bb:conns payload: counts and one age in seconds.
type Conns struct {
	Total          uint32 `json:"connsTotal"`
	User           uint32 `json:"connsUser"`
	Running        uint32 `json:"connsRunning"`
	Sleeping       uint32 `json:"connsSleeping"`
	InTran         uint32 `json:"connsInTran"`
	IdleTranMaxSec uint64 `json:"connsIdleTranSec"`
}

type connsSampler struct{}

func (connsSampler) ID() string              { return "bb:conns" }
func (connsSampler) Kind() string            { return "conns" }
func (connsSampler) Interval() time.Duration { return DemotedInterval }

func (connsSampler) Decode(rows []Row) (any, bool, error) {
	return extraSingleRow[Conns](rows, CapConns)
}

func init() {
	registerExtraStatement("bb:conns", stmtConns, CapConns, "sys.dm_exec_sessions")
	registerExtraColumns(map[string]string{
		"conns_total": "connsTotal", "conns_user": "connsUser", "conns_running": "connsRunning",
		"conns_sleeping": "connsSleeping", "conns_in_tran": "connsInTran", "conns_idle_tran_sec": "connsIdleTranSec",
	})
	registerExtraRowSpec(reflect.TypeOf(Conns{}), specAll(
		"connsTotal", "connsUser", "connsRunning", "connsSleeping", "connsInTran", "connsIdleTranSec"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &connsSampler{} })
}
