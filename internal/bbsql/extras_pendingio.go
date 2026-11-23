// extras_pendingio.go: bb:pendingio, disk requests still waiting.
//
// Source: sys.dm_io_pending_io_requests. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-io-pending-io-requests-transact-sql
// (io_type nvarchar; io_pending 0 = pending in SQL Server, 1 = pending in the
// OS). Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL
// Server 2022 and later), inside the C8 ceiling.
//
// Counted in SQL. The DMV also carries a file handle and its path; neither is
// selected (and the lint refuses both), so no path ever leaves the instance.
package bbsql

import (
	"reflect"
	"time"
)

const CapPendingIO = CapSingleRow

const stmtPendingIO = setPrefix + `SELECT COUNT(*) AS pio_total,
  ISNULL(SUM(CASE WHEN UPPER(io_type) = N'DISK' THEN 1 ELSE 0 END), 0) AS pio_disk,
  ISNULL(SUM(CASE WHEN io_pending = 1 THEN 1 ELSE 0 END), 0) AS pio_in_os,
  ISNULL(SUM(CASE WHEN io_pending = 0 THEN 1 ELSE 0 END), 0) AS pio_in_sql
FROM sys.dm_io_pending_io_requests /* unravis:bb:pendingio */`

// PendingIO is the bb:pendingio payload: counts of pending requests.
type PendingIO struct {
	Total uint32 `json:"pioTotal"`
	Disk  uint32 `json:"pioDisk"`
	InOS  uint32 `json:"pioInOs"`
	InSQL uint32 `json:"pioInSql"`
}

type pendingIOSampler struct{}

func (pendingIOSampler) ID() string              { return "bb:pendingio" }
func (pendingIOSampler) Kind() string            { return "pendingio" }
func (pendingIOSampler) Interval() time.Duration { return BaseInterval }

func (pendingIOSampler) Decode(rows []Row) (any, bool, error) {
	return extraSingleRow[PendingIO](rows, CapPendingIO)
}

func init() {
	registerExtraStatement("bb:pendingio", stmtPendingIO, CapPendingIO, "sys.dm_io_pending_io_requests")
	registerExtraColumns(map[string]string{
		"pio_total": "pioTotal", "pio_disk": "pioDisk", "pio_in_os": "pioInOs", "pio_in_sql": "pioInSql",
	})
	registerExtraRowSpec(reflect.TypeOf(PendingIO{}), rowSpec{cols: map[string]colRule{
		"pioTotal": req(), "pioDisk": req(), "pioInOs": req(), "pioInSql": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &pendingIOSampler{} })
}
