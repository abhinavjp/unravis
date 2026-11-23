// extras_waiting.go: bb:waiting, the tasks waiting right now, by wait type.
//
// Source: sys.dm_os_waiting_tasks. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-waiting-tasks-transact-sql
// (wait_type nvarchar(60), wait_duration_ms bigint, blocking_session_id
// smallint NULL when not blocked, and a task's own id or a negative sentinel
// (-2, -3, -4) for waits that are not a blocker, which are not counted, session_id smallint NULL for system tasks).
// Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server
// 2022 and later), inside the C8 ceiling.
//
// Grouped in SQL: the resource description (which can carry object names and
// keys) is never selected, and no session id leaves the instance. User tasks
// only (session_id above 50, the engine's own reserved range is below it).
// The plan asks for the blocking chain depth: a depth needs a recursive walk
// that one read-only SELECT cannot do, so this sample gives the blocked count,
// the number of distinct blockers and the longest blocked wait instead.
package bbsql

import (
	"reflect"
	"time"
)

const CapWaiting = 64 // bb:waiting fetches TOP 65, keeps 64

const stmtWaiting = setPrefix + `SELECT TOP 65 wait_type,
  COUNT(*) AS wt_tasks,
  MAX(wait_duration_ms) AS wt_max_ms,
  SUM(CASE WHEN blocking_session_id > 0 AND blocking_session_id <> session_id THEN 1 ELSE 0 END) AS wt_blocked,
  COUNT(DISTINCT CASE WHEN blocking_session_id > 0 AND blocking_session_id <> session_id THEN blocking_session_id END) AS wt_blockers,
  ISNULL(MAX(CASE WHEN blocking_session_id > 0 AND blocking_session_id <> session_id THEN wait_duration_ms END), 0) AS wt_max_block_ms
FROM sys.dm_os_waiting_tasks
WHERE wait_type IS NOT NULL AND session_id > 50 AND session_id <> @@SPID
GROUP BY wait_type
ORDER BY COUNT(*) DESC /* unravis:bb:waiting */`

// Waiting is the bb:waiting payload: one row per wait type.
type Waiting struct {
	WaitType   string `json:"waitType"`
	Tasks      uint32 `json:"wtTasks"`
	MaxMs      uint64 `json:"wtMaxMs"`
	Blocked    uint32 `json:"wtBlocked"`
	Blockers   uint32 `json:"wtBlockers"`
	MaxBlockMs uint64 `json:"wtMaxBlockMs"`
}

type waitingSampler struct{}

func (waitingSampler) ID() string              { return "bb:waiting" }
func (waitingSampler) Kind() string            { return "waiting" }
func (waitingSampler) Interval() time.Duration { return BaseInterval }

func (waitingSampler) Decode(rows []Row) (any, bool, error) {
	var out []Waiting
	truncated, err := decodeRows(rows, &out, CapWaiting)
	if err != nil {
		return nil, false, err
	}
	for _, w := range out {
		if !waitTypeRe.MatchString(w.WaitType) {
			return nil, false, errWaitType
		}
	}
	if len(out) == 0 {
		return nil, false, nil // nothing waiting is a real reading, not a sample
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:waiting", stmtWaiting, CapWaiting, "sys.dm_os_waiting_tasks")
	registerExtraColumns(map[string]string{
		"wt_tasks": "wtTasks", "wt_max_ms": "wtMaxMs", "wt_blocked": "wtBlocked",
		"wt_blockers": "wtBlockers", "wt_max_block_ms": "wtMaxBlockMs",
	})
	registerExtraRowSpec(reflect.TypeOf(Waiting{}), specAll(
		"waitType", "wtTasks", "wtMaxMs", "wtBlocked", "wtBlockers", "wtMaxBlockMs"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &waitingSampler{} })
}
