// extras_locks.go: bb:locks, the locks held and requested right now, counted by
// database, resource type, mode and status.
//
// Source: sys.dm_tran_locks. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-tran-locks-transact-sql
// (resource_database_id int, resource_type nvarchar(60), request_mode
// nvarchar(60), request_status nvarchar(60)). Permission: VIEW SERVER STATE
// (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later), inside the C8
// ceiling.
//
// Counted in SQL. The resource description and the associated entity id (which
// name rows, keys and objects) are never selected. The type, mode and status are
// engine enums and are checked against a strict shape before they are kept.
// Medium load: the DMV has one row per lock, so a server with millions of locks
// reads them all; the 5 s query limit and the extra's own back-off bound the
// cost, and the Blackbox's own session is excluded.
package bbsql

import (
	"errors"
	"reflect"
	"regexp"
	"time"
)

const CapLocks = 256 // bb:locks fetches TOP 257, keeps 256

const stmtLocks = setPrefix + `SELECT TOP 257 resource_database_id AS lock_db, resource_type AS lock_type,
  request_mode AS lock_mode, request_status AS lock_status, COUNT(*) AS lock_count
FROM sys.dm_tran_locks
WHERE request_session_id <> @@SPID
GROUP BY resource_database_id, resource_type, request_mode, request_status
ORDER BY COUNT(*) DESC /* unravis:bb:locks */`

var (
	lockTypeRe   = regexp.MustCompile("^[A-Z_]{1,30}$")
	lockModeRe   = regexp.MustCompile("^[A-Za-z_-]{1,20}$")
	errLockShape = errors.New("bbsql: invalid lock enum")
)

// LockCount is the bb:locks payload: how many locks of one kind exist.
type LockCount struct {
	DB     uint32 `json:"lockDb"`
	Type   string `json:"lockType"`
	Mode   string `json:"lockMode"`
	Status string `json:"lockStatus"`
	Count  uint64 `json:"lockCount"`
}

type locksSampler struct{}

func (locksSampler) ID() string              { return "bb:locks" }
func (locksSampler) Kind() string            { return "locks" }
func (locksSampler) Interval() time.Duration { return DemotedInterval }

func (locksSampler) Decode(rows []Row) (any, bool, error) {
	var out []LockCount
	truncated, err := decodeRows(rows, &out, CapLocks)
	if err != nil {
		return nil, false, err
	}
	for _, l := range out {
		if !lockTypeRe.MatchString(l.Type) || !lockModeRe.MatchString(l.Mode) || !lockTypeRe.MatchString(l.Status) {
			return nil, false, errLockShape
		}
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:locks", stmtLocks, CapLocks, "sys.dm_tran_locks")
	registerExtraColumns(map[string]string{
		"lock_db": "lockDb", "lock_type": "lockType", "lock_mode": "lockMode",
		"lock_status": "lockStatus", "lock_count": "lockCount",
	})
	registerExtraRowSpec(reflect.TypeOf(LockCount{}), specAll("lockDb", "lockType", "lockMode", "lockStatus", "lockCount"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &locksSampler{} })
}
