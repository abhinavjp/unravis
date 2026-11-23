// extras_workers.go: bb:workers, worker threads by state.
//
// Source: sys.dm_os_workers. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-workers-transact-sql
// (state is nvarchar INIT/RUNNING/RUNNABLE/SUSPENDED; is_sick and is_preemptive
// are bit). Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL
// Server 2022 and later), inside the C8 ceiling.
//
// Counted in SQL: no worker or thread address, no task, no session. The
// "maximum" half of used-vs-maximum is a configuration fact and comes with the
// sysinfo extra, not from this per-worker DMV.
package bbsql

import (
	"reflect"
	"time"
)

const CapWorkers = CapSingleRow

const stmtWorkers = setPrefix + `SELECT COUNT(*) AS workers_total,
  ISNULL(SUM(CASE WHEN state = N'RUNNING' THEN 1 ELSE 0 END), 0) AS workers_running,
  ISNULL(SUM(CASE WHEN state = N'RUNNABLE' THEN 1 ELSE 0 END), 0) AS workers_runnable,
  ISNULL(SUM(CASE WHEN state = N'SUSPENDED' THEN 1 ELSE 0 END), 0) AS workers_suspended,
  ISNULL(SUM(CASE WHEN is_sick = 1 THEN 1 ELSE 0 END), 0) AS workers_sick,
  ISNULL(SUM(CASE WHEN is_preemptive = 1 THEN 1 ELSE 0 END), 0) AS workers_preemptive
FROM sys.dm_os_workers /* unravis:bb:workers */`

// Workers is the bb:workers payload: counts of workers.
type Workers struct {
	Total      uint32 `json:"workersTotal"`
	Running    uint32 `json:"workersRunning"`
	Runnable   uint32 `json:"workersRunnable"`
	Suspended  uint32 `json:"workersSuspended"`
	Sick       uint32 `json:"workersSick"`
	Preemptive uint32 `json:"workersPreemptive"`
}

type workersSampler struct{}

func (workersSampler) ID() string              { return "bb:workers" }
func (workersSampler) Kind() string            { return "workers" }
func (workersSampler) Interval() time.Duration { return BaseInterval }

func (workersSampler) Decode(rows []Row) (any, bool, error) {
	return extraSingleRow[Workers](rows, CapWorkers)
}

func init() {
	registerExtraStatement("bb:workers", stmtWorkers, CapWorkers, "sys.dm_os_workers")
	registerExtraColumns(map[string]string{
		"workers_total": "workersTotal", "workers_running": "workersRunning",
		"workers_runnable": "workersRunnable", "workers_suspended": "workersSuspended",
		"workers_sick": "workersSick", "workers_preemptive": "workersPreemptive",
	})
	registerExtraRowSpec(reflect.TypeOf(Workers{}), rowSpec{cols: map[string]colRule{
		"workersTotal": req(), "workersRunning": req(), "workersRunnable": req(),
		"workersSuspended": req(), "workersSick": req(), "workersPreemptive": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &workersSampler{} })
}
