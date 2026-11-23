// extras_qhash.go: bb:qhash, per-query-fingerprint cost (deep profile).
//
// Source: sys.dm_exec_query_stats. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-exec-query-stats-transact-sql
// (query_hash binary(8); execution_count, total_worker_time and
// total_elapsed_time (microseconds) and total_logical_reads are cumulative
// bigint while the plan stays cached). Permission: VIEW SERVER STATE (VIEW
// SERVER PERFORMANCE STATE on SQL Server 2022 and later), inside the C8 ceiling.
//
// The payload is a query hash (an 8-byte fingerprint of the normalised
// statement, sent as 0x plus 16 hex digits, never the statement) and four
// per-interval deltas computed here in Go. No text, no plan, no handle leaves
// the server: the statement groups the cached statements by hash in SQL and
// reads nothing else.
//
// Known limits, stated on purpose (live proof owed, BACKLOG BL-550):
//   - It keeps the TOP 50 hashes by CUMULATIVE CPU, not by CPU this interval. A
//     hash that has only just become busy is not seen until it climbs into the
//     top 50, and it needs one more cycle to get a baseline.
//   - A plan that leaves the cache takes its counters with it, so a hash total
//     can go DOWN. That hash is re-baselined, never reported as a negative.
package bbsql

import (
	"errors"
	"reflect"
	"regexp"
	"sort"
	"time"
)

const CapQHash = 50 // bb:qhash fetches TOP 51, keeps 50

const stmtQHash = setPrefix + `SELECT TOP 51 CONVERT(varchar(18), query_hash, 1) AS qhash,
  SUM(execution_count) AS q_execs,
  SUM(total_worker_time) AS q_cpu_us,
  SUM(total_elapsed_time) AS q_elapsed_us,
  SUM(total_logical_reads) AS q_reads
FROM sys.dm_exec_query_stats
WHERE query_hash IS NOT NULL
GROUP BY query_hash
ORDER BY SUM(total_worker_time) DESC /* unravis:bb:qhash */`

// qHashRe is the accepted shape of a query hash: 0x and 16 hex digits.
var qHashRe = regexp.MustCompile(`^0x[0-9A-Fa-f]{16}$`)

var errQHash = errors.New("bbsql: invalid query hash")

// qhashRaw is the cumulative input row, kept apart from the delta payload.
type qhashRaw struct {
	Hash     string `json:"qHash"`
	ExecsCum uint64 `json:"qExecsCum"`
	CPUCum   uint64 `json:"qCpuUsCum"`
	ElapCum  uint64 `json:"qElapsedUsCum"`
	ReadsCum uint64 `json:"qReadsCum"`
}

// QHash is the bb:qhash payload: per-interval deltas for one query hash.
type QHash struct {
	Hash      string `json:"qHash"`
	Execs     uint64 `json:"qExecs"`
	CPUUs     uint64 `json:"qCpuUs"`
	ElapsedUs uint64 `json:"qElapsedUs"`
	Reads     uint64 `json:"qReads"`
}

type qhashSampler struct {
	prev          map[string]qhashRaw
	obs           Observation
	baselineEpoch uint64
}

func (qhashSampler) ID() string              { return "bb:qhash" }
func (qhashSampler) Kind() string            { return "qhash" }
func (qhashSampler) Interval() time.Duration { return ExtraEvery5m }

func (s *qhashSampler) Observe(o Observation) { s.obs = o }

// Decode follows the bb:latches rules: the first cycle after startup or an
// epoch change only baselines; a hash with no baseline, or whose counters went
// backwards, baselines without emitting; an idle hash is dropped; an empty
// reply clears every baseline. The map is rebuilt from this cycle's cap-limited
// rows, so it cannot grow. Output is ordered by CPU this interval, largest first.
func (s *qhashSampler) Decode(rows []Row) (any, bool, error) {
	var cur []qhashRaw
	truncated, err := decodeRows(rows, &cur, CapQHash)
	if err != nil {
		return nil, false, err
	}
	for _, r := range cur {
		if !qHashRe.MatchString(r.Hash) {
			return nil, false, errQHash
		}
	}
	if len(cur) == 0 {
		s.prev = map[string]qhashRaw{}
		s.baselineEpoch = s.obs.Epoch
		return nil, false, nil
	}
	next := make(map[string]qhashRaw, len(cur))
	first := s.prev == nil || s.baselineEpoch != s.obs.Epoch
	out := make([]QHash, 0, len(cur))
	for _, r := range cur {
		next[r.Hash] = r
		if first {
			continue
		}
		p, ok := s.prev[r.Hash]
		if !ok || r.ExecsCum < p.ExecsCum || r.CPUCum < p.CPUCum || r.ElapCum < p.ElapCum || r.ReadsCum < p.ReadsCum {
			continue
		}
		d := QHash{
			Hash: r.Hash, Execs: r.ExecsCum - p.ExecsCum, CPUUs: r.CPUCum - p.CPUCum,
			ElapsedUs: r.ElapCum - p.ElapCum, Reads: r.ReadsCum - p.ReadsCum,
		}
		if d.Execs == 0 && d.CPUUs == 0 && d.ElapsedUs == 0 && d.Reads == 0 {
			continue
		}
		out = append(out, d)
	}
	s.prev = next
	s.baselineEpoch = s.obs.Epoch
	if len(out) == 0 {
		return nil, truncated, nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CPUUs > out[j].CPUUs })
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:qhash", stmtQHash, CapQHash, "sys.dm_exec_query_stats")
	registerExtraColumns(map[string]string{
		"qhash": "qHash", "q_execs": "qExecsCum", "q_cpu_us": "qCpuUsCum",
		"q_elapsed_us": "qElapsedUsCum", "q_reads": "qReadsCum",
	})
	registerExtraRowSpec(reflect.TypeOf(qhashRaw{}), rowSpec{cols: map[string]colRule{
		"qHash": req(), "qExecsCum": req(), "qCpuUsCum": req(), "qElapsedUsCum": req(), "qReadsCum": req(),
	}})
	RegisterExtraFactory(TierDeep, func() Sampler { return &qhashSampler{} })
}
