// extras_trans.go: bb:trans, the open transactions right now.
//
// Source: sys.dm_tran_active_transactions. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-tran-active-transactions-transact-sql
// (transaction_begin_time datetime, transaction_type 1 read/write, 2 read-only,
// 3 system, 4 distributed; transaction_state 7 = rolling back). Permission:
// VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and
// later), inside the C8 ceiling.
//
// Only read/write and distributed transactions are counted. The transaction
// name column is never selected. The version store size and the log used come
// from the perfctr extra (Transactions and Databases objects): the per
// database DMVs need a database-level right, which this sampler does not ask
// for.
package bbsql

import (
	"reflect"
	"time"
)

const CapTrans = CapSingleRow

const stmtTrans = setPrefix + `SELECT COUNT(*) AS trans_open,
  ISNULL(MAX(CASE WHEN transaction_begin_time <= GETDATE() THEN DATEDIFF(second, transaction_begin_time, GETDATE()) END), 0) AS trans_oldest_sec,
  ISNULL(SUM(CASE WHEN transaction_state = 7 THEN 1 ELSE 0 END), 0) AS trans_rolling_back
FROM sys.dm_tran_active_transactions
WHERE transaction_type IN (1, 4) /* unravis:bb:trans */`

// Trans is the bb:trans payload.
type Trans struct {
	Open        uint32 `json:"transOpen"`
	OldestSec   uint64 `json:"transOldestSec"`
	RollingBack uint32 `json:"transRollingBack"`
}

type transSampler struct{}

func (transSampler) ID() string              { return "bb:trans" }
func (transSampler) Kind() string            { return "trans" }
func (transSampler) Interval() time.Duration { return DemotedInterval }

func (transSampler) Decode(rows []Row) (any, bool, error) {
	return extraSingleRow[Trans](rows, CapTrans)
}

func init() {
	registerExtraStatement("bb:trans", stmtTrans, CapTrans, "sys.dm_tran_active_transactions")
	registerExtraColumns(map[string]string{
		"trans_open": "transOpen", "trans_oldest_sec": "transOldestSec", "trans_rolling_back": "transRollingBack",
	})
	registerExtraRowSpec(reflect.TypeOf(Trans{}), specAll("transOpen", "transOldestSec", "transRollingBack"))
	RegisterExtraFactory(TierEverything, func() Sampler { return &transSampler{} })
}
