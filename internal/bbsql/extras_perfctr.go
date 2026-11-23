// extras_perfctr.go: bb:perfctr, the SQL Server performance counters the
// investigation needs (batches, compiles, page life expectancy, log flushes,
// transactions, locks, latches, memory grants, log growths, per database).
//
// Source: sys.dm_os_performance_counters. Columns and counter types checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-performance-counters-transact-sql
// (object_name, counter_name and instance_name are nchar(128), padded with
// spaces; cntr_value bigint; cntr_type 65792 instantaneous value, 272696576
// cumulative per-second counter, 537003264 ratio with 1073939712 as its base).
// Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server
// 2022 and later), inside the C8 ceiling.
//
// Names. Nothing the DMV returns as text is stored. The statement keeps only
// counters whose NAME is in the fixed list below, trims the padding, strips the
// instance prefix from the object name (SQLServer: or MSSQL$name:, which would
// carry the instance name), and turns the instance name into either a kind
// (” none, '_Total', or 'db') or a numeric database id through DB_ID(). A
// database name, a resource pool name or a workload group name never leaves the
// instance. Go then keeps only (object, counter, instance kind) triples in
// perfDefs and writes them under a short fixed key.
//
// Values. A gauge is written as read. A cumulative counter (per-second or
// running count) is written as the difference since the last reading, computed
// here in Go: nothing on the first reading, after a gap, or when the counter
// went backwards (a restart). A ratio is written in basis points (10000 = 100%).
//
// Counters the lint refuses because their name holds a credential word (the
// logins per second counter) are not collected.
package bbsql

import (
	"math"
	"sort"
	"strings"
	"time"
)

const CapPerfctr = 1024 // bb:perfctr fetches TOP 1025, keeps 1024

// The counter names in the IN list must equal the names in perfDefs;
// TestPerfctrStatementMatchesTable enforces it both ways.
const stmtPerfctr = setPrefix + `SELECT TOP 1025 SUBSTRING(RTRIM(object_name), CHARINDEX(N':', RTRIM(object_name)) + 1, 128) AS pc_object,
  RTRIM(counter_name) AS pc_counter,
  CASE WHEN RTRIM(instance_name) = N'' THEN N'' WHEN RTRIM(instance_name) = N'_Total' THEN N'_Total' ELSE N'db' END AS pc_inst,
  DB_ID(RTRIM(instance_name)) AS pc_db,
  cntr_value AS pc_value,
  cntr_type AS pc_type
FROM sys.dm_os_performance_counters
WHERE counter_name IN (N'Page life expectancy', N'Buffer cache hit ratio', N'Buffer cache hit ratio base',
  N'Page reads/sec', N'Page writes/sec', N'Page lookups/sec', N'Lazy writes/sec', N'Checkpoint pages/sec',
  N'Free list stalls/sec', N'Readahead pages/sec', N'Database pages', N'Target pages',
  N'Total Server Memory (KB)', N'Target Server Memory (KB)', N'Memory Grants Pending', N'Memory Grants Outstanding',
  N'Granted Workspace Memory (KB)', N'Connection Memory (KB)', N'Optimizer Memory (KB)',
  N'Batch Requests/sec', N'SQL Compilations/sec', N'SQL Re-Compilations/sec', N'Auto-Param Attempts/sec',
  N'Failed Auto-Params/sec', N'Safe Auto-Params/sec',
  N'User Connections', N'Processes blocked', N'Logouts/sec', N'Temp Tables Creation Rate', N'Active Temp Tables', N'Transactions',
  N'Full Scans/sec', N'Index Searches/sec', N'Page Splits/sec', N'Forwarded Records/sec', N'Workfiles Created/sec',
  N'Worktables Created/sec', N'FreeSpace Scans/sec', N'Table Lock Escalations/sec',
  N'Lock Requests/sec', N'Lock Waits/sec', N'Lock Timeouts/sec', N'Number of Deadlocks/sec', N'Lock Wait Time (ms)',
  N'Latch Waits/sec', N'Total Latch Wait Time (ms)',
  N'Free Space in tempdb (KB)', N'Version Store Size (KB)', N'Version Generation rate (KB/s)', N'Version Cleanup rate (KB/s)',
  N'Longest Transaction Running Time', N'Snapshot Transactions', N'Errors/sec',
  N'Transactions/sec', N'Active Transactions', N'Log Flushes/sec', N'Log Bytes Flushed/sec', N'Log Growths', N'Log Shrinks',
  N'Percent Log Used', N'Data File(s) Size (KB)', N'Log File(s) Size (KB)', N'Log File(s) Used Size (KB)')
ORDER BY pc_inst, pc_object, pc_counter, pc_db /* unravis:bb:perfctr */`

// perfKind says how a counter is turned into a number.
type perfKind uint8

const (
	pcGauge perfKind = iota + 1 // type 65792, written as read
	pcCum                       // type 65792 that only grows (log growths), written as a delta
	pcRate                      // type 272696576 cumulative per-second counter, written as a delta
	pcRatio                     // type 537003264, divided by its base
	pcBase                      // type 1073939712, only used as a ratio's divisor
)

// perfInst says which instances of a counter are kept.
type perfInst uint8

const (
	instNone  perfInst = iota + 1 // the instance name is empty
	instTotal                     // '_Total' only
	instDB                        // '_Total' and one row per database (numeric id)
)

// Counter types, as documented for sys.dm_os_performance_counters.
const (
	pcTypeValue int64 = 65792
	pcTypeRate  int64 = 272696576
	pcTypeRatio int64 = 537003264
	pcTypeBase  int64 = 1073939712
)

type perfDef struct {
	object, counter string
	kind            perfKind
	inst            perfInst
	key             string // short key written to the sample
	base            string // ratio only: the base counter's name (same object)
}

// perfDefs is the fixed counter list. The key is what the charts read: keep it
// stable. The order is the output order.
var perfDefs = []perfDef{
	{"Buffer Manager", "Page life expectancy", pcGauge, instNone, "bm.ple", ""},
	{"Buffer Manager", "Buffer cache hit ratio", pcRatio, instNone, "bm.hit", "Buffer cache hit ratio base"},
	{"Buffer Manager", "Buffer cache hit ratio base", pcBase, instNone, "", ""},
	{"Buffer Manager", "Page reads/sec", pcRate, instNone, "bm.reads", ""},
	{"Buffer Manager", "Page writes/sec", pcRate, instNone, "bm.writes", ""},
	{"Buffer Manager", "Page lookups/sec", pcRate, instNone, "bm.lookups", ""},
	{"Buffer Manager", "Lazy writes/sec", pcRate, instNone, "bm.lazy", ""},
	{"Buffer Manager", "Checkpoint pages/sec", pcRate, instNone, "bm.ckpt", ""},
	{"Buffer Manager", "Free list stalls/sec", pcRate, instNone, "bm.freestall", ""},
	{"Buffer Manager", "Readahead pages/sec", pcRate, instNone, "bm.readahead", ""},
	{"Buffer Manager", "Database pages", pcGauge, instNone, "bm.dbpages", ""},
	{"Buffer Manager", "Target pages", pcGauge, instNone, "bm.target", ""},
	{"Memory Manager", "Total Server Memory (KB)", pcGauge, instNone, "mm.total", ""},
	{"Memory Manager", "Target Server Memory (KB)", pcGauge, instNone, "mm.target", ""},
	{"Memory Manager", "Memory Grants Pending", pcGauge, instNone, "mm.grantpend", ""},
	{"Memory Manager", "Memory Grants Outstanding", pcGauge, instNone, "mm.grantout", ""},
	{"Memory Manager", "Granted Workspace Memory (KB)", pcGauge, instNone, "mm.workspace", ""},
	{"Memory Manager", "Connection Memory (KB)", pcGauge, instNone, "mm.conn", ""},
	{"Memory Manager", "Optimizer Memory (KB)", pcGauge, instNone, "mm.optim", ""},
	{"SQL Statistics", "Batch Requests/sec", pcRate, instNone, "ss.batches", ""},
	{"SQL Statistics", "SQL Compilations/sec", pcRate, instNone, "ss.compiles", ""},
	{"SQL Statistics", "SQL Re-Compilations/sec", pcRate, instNone, "ss.recompiles", ""},
	{"SQL Statistics", "Auto-Param Attempts/sec", pcRate, instNone, "ss.autoparam", ""},
	{"SQL Statistics", "Failed Auto-Params/sec", pcRate, instNone, "ss.autoparamfail", ""},
	{"SQL Statistics", "Safe Auto-Params/sec", pcRate, instNone, "ss.autoparamsafe", ""},
	{"General Statistics", "User Connections", pcGauge, instNone, "gs.conns", ""},
	{"General Statistics", "Processes blocked", pcGauge, instNone, "gs.blocked", ""},
	{"General Statistics", "Logouts/sec", pcRate, instNone, "gs.logouts", ""},
	{"General Statistics", "Temp Tables Creation Rate", pcRate, instNone, "gs.tmpcreate", ""},
	{"General Statistics", "Active Temp Tables", pcGauge, instNone, "gs.tmpactive", ""},
	{"General Statistics", "Transactions", pcGauge, instNone, "gs.trans", ""},
	{"Access Methods", "Full Scans/sec", pcRate, instNone, "am.fullscans", ""},
	{"Access Methods", "Index Searches/sec", pcRate, instNone, "am.idxsearch", ""},
	{"Access Methods", "Page Splits/sec", pcRate, instNone, "am.splits", ""},
	{"Access Methods", "Forwarded Records/sec", pcRate, instNone, "am.forwarded", ""},
	{"Access Methods", "Workfiles Created/sec", pcRate, instNone, "am.workfiles", ""},
	{"Access Methods", "Worktables Created/sec", pcRate, instNone, "am.worktables", ""},
	{"Access Methods", "FreeSpace Scans/sec", pcRate, instNone, "am.fsscans", ""},
	{"Access Methods", "Table Lock Escalations/sec", pcRate, instNone, "am.escalations", ""},
	{"Locks", "Lock Requests/sec", pcRate, instTotal, "lk.requests", ""},
	{"Locks", "Lock Waits/sec", pcRate, instTotal, "lk.waits", ""},
	{"Locks", "Lock Timeouts/sec", pcRate, instTotal, "lk.timeouts", ""},
	{"Locks", "Number of Deadlocks/sec", pcRate, instTotal, "lk.deadlocks", ""},
	{"Locks", "Lock Wait Time (ms)", pcRate, instTotal, "lk.waitms", ""},
	{"Latches", "Latch Waits/sec", pcRate, instNone, "lt.waits", ""},
	{"Latches", "Total Latch Wait Time (ms)", pcRate, instNone, "lt.waitms", ""},
	{"Transactions", "Free Space in tempdb (KB)", pcGauge, instNone, "tx.tempfree", ""},
	{"Transactions", "Version Store Size (KB)", pcGauge, instNone, "tx.vstore", ""},
	{"Transactions", "Version Generation rate (KB/s)", pcRate, instNone, "tx.vgen", ""},
	{"Transactions", "Version Cleanup rate (KB/s)", pcRate, instNone, "tx.vclean", ""},
	{"Transactions", "Longest Transaction Running Time", pcGauge, instNone, "tx.longest", ""},
	{"Transactions", "Snapshot Transactions", pcGauge, instNone, "tx.snapshots", ""},
	{"SQL Errors", "Errors/sec", pcRate, instTotal, "er.errors", ""},
	{"Databases", "Transactions/sec", pcRate, instDB, "db.trans", ""},
	{"Databases", "Active Transactions", pcGauge, instDB, "db.active", ""},
	{"Databases", "Log Flushes/sec", pcRate, instDB, "db.logflush", ""},
	{"Databases", "Log Bytes Flushed/sec", pcRate, instDB, "db.logbytes", ""},
	{"Databases", "Log Growths", pcCum, instDB, "db.loggrow", ""},
	{"Databases", "Log Shrinks", pcCum, instDB, "db.logshrink", ""},
	{"Databases", "Percent Log Used", pcGauge, instDB, "db.logpct", ""},
	{"Databases", "Data File(s) Size (KB)", pcGauge, instDB, "db.datakb", ""},
	{"Databases", "Log File(s) Size (KB)", pcGauge, instDB, "db.logkb", ""},
	{"Databases", "Log File(s) Used Size (KB)", pcGauge, instDB, "db.logusedkb", ""},
}

// perfIndex finds a definition by (object, counter, case-insensitive).
var perfIndex = func() map[string]int {
	m := make(map[string]int, len(perfDefs))
	for i, d := range perfDefs {
		m[strings.ToLower(d.object+"\x00"+d.counter)] = i
	}
	return m
}()

// perfRaw is one row as selected.
type perfRaw struct {
	Object  string `json:"pcObject"`
	Counter string `json:"pcCounter"`
	Inst    string `json:"pcInst"`
	DB      *int64 `json:"pcDb"`
	Value   int64  `json:"pcValue"`
	Type    int64  `json:"pcType"`
}

// PerfCounter is the bb:perfctr payload: one number under a fixed short key. DB
// is the numeric database id for a per-database counter and absent for a
// server-wide one.
type PerfCounter struct {
	K  string  `json:"k"`
	V  int64   `json:"v"`
	DB *uint32 `json:"db,omitempty"`
}

type perfKey struct {
	def int
	db  int64 // -1 for none
}

type perfctrSampler struct {
	prev          map[perfKey]int64
	obs           Observation
	baselineEpoch uint64
}

func (perfctrSampler) ID() string              { return "bb:perfctr" }
func (perfctrSampler) Kind() string            { return "perfctr" }
func (perfctrSampler) Interval() time.Duration { return ExtraEvery30s }

func (s *perfctrSampler) Observe(o Observation) { s.obs = o }

// typeFor is the cntr_type the table expects for a kind.
func typeFor(k perfKind) int64 {
	switch k {
	case pcRate:
		return pcTypeRate
	case pcRatio:
		return pcTypeRatio
	case pcBase:
		return pcTypeBase
	default:
		return pcTypeValue
	}
}

// Decode keeps the rows whose (object, counter, instance kind, type) match the
// fixed table and ignores the rest: a counter this build does not know, or one
// whose type is not the documented one, is dropped, never guessed.
func (s *perfctrSampler) Decode(rows []Row) (any, bool, error) {
	var raw []perfRaw
	truncated, err := decodeRows(rows, &raw, CapPerfctr)
	if err != nil {
		return nil, false, err
	}
	cur := make(map[perfKey]int64, len(raw))
	for _, r := range raw {
		i, ok := perfIndex[strings.ToLower(r.Object+"\x00"+r.Counter)]
		if !ok {
			continue
		}
		d := perfDefs[i]
		if r.Type != typeFor(d.kind) {
			continue
		}
		k := perfKey{def: i, db: -1}
		switch d.inst {
		case instNone:
			if r.Inst != "" {
				continue
			}
		case instTotal:
			if r.Inst != "_Total" {
				continue
			}
		case instDB:
			switch r.Inst {
			case "_Total":
			case "db":
				if r.DB == nil || *r.DB < 1 || *r.DB > math.MaxInt32 {
					continue
				}
				k.db = *r.DB
			default:
				continue
			}
		}
		cur[k] = r.Value
	}
	if len(cur) == 0 {
		s.prev = map[perfKey]int64{}
		s.baselineEpoch = s.obs.Epoch
		return nil, false, nil
	}
	first := s.prev == nil || s.baselineEpoch != s.obs.Epoch
	keys := make([]perfKey, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].def != keys[b].def {
			return keys[a].def < keys[b].def
		}
		return keys[a].db < keys[b].db
	})
	out := make([]PerfCounter, 0, len(keys))
	for _, k := range keys {
		d := perfDefs[k.def]
		v := cur[k]
		var val int64
		switch d.kind {
		case pcGauge:
			val = v
		case pcRatio:
			b, ok := cur[perfKey{def: perfIndex[strings.ToLower(d.object+"\x00"+d.base)], db: k.db}]
			if !ok || b <= 0 || v < 0 {
				continue
			}
			val = int64(math.Round(math.Min(float64(v)/float64(b), 1) * 10000))
		case pcRate, pcCum:
			p, ok := s.prev[k]
			if first || !ok || v < p {
				continue
			}
			val = v - p
		default: // pcBase is never written
			continue
		}
		pc := PerfCounter{K: d.key, V: val}
		if k.db >= 0 {
			id := uint32(k.db) //nolint:gosec // checked against MaxInt32 above
			pc.DB = &id
		}
		out = append(out, pc)
	}
	s.prev = cur
	s.baselineEpoch = s.obs.Epoch
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:perfctr", stmtPerfctr, CapPerfctr, "sys.dm_os_performance_counters")
	registerExtraColumns(map[string]string{
		"pc_object": "pcObject", "pc_counter": "pcCounter", "pc_inst": "pcInst",
		"pc_db": "pcDb", "pc_value": "pcValue", "pc_type": "pcType",
	})
	registerExtraRowSpec(reflectTypeOf[perfRaw](), rowSpec{cols: map[string]colRule{
		"pcObject": req(), "pcCounter": req(), "pcInst": req(), "pcDb": reqNull(), "pcValue": req(), "pcType": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &perfctrSampler{} })
}
