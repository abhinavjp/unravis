// extras_latches.go: bb:latches, the latch classes that waited this minute.
//
// Source: sys.dm_os_latch_stats. Columns checked on
// https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-latch-stats-transact-sql
// (latch_class nvarchar(60); waiting_requests_count and wait_time_ms are
// cumulative bigint since start or DBCC SQLPERF CLEAR). Permission: VIEW SERVER
// STATE (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later), inside the
// C8 ceiling.
//
// The payload is a validated latch class name (an engine-defined name such as
// BUFFER, never user data; the docs list classes with lower case too, e.g.
// SSBXmitWork) and two per-interval deltas computed here in Go, never in SQL.
package bbsql

import (
	"errors"
	"reflect"
	"regexp"
	"time"
)

const CapLatches = 20 // bb:latches fetches TOP 21, keeps 20

const stmtLatches = setPrefix + `SELECT TOP 21 latch_class, waiting_requests_count, wait_time_ms AS latch_wait_ms
FROM sys.dm_os_latch_stats
WHERE waiting_requests_count > 0
ORDER BY wait_time_ms DESC /* unravis:bb:latches */`

// latchClassRe is the accepted shape of an engine latch class name.
var latchClassRe = regexp.MustCompile("^[A-Za-z0-9_]{1,60}$")

var errLatchClass = errors.New("bbsql: invalid latch class")

// latchRaw is the cumulative input row, kept apart from the delta payload.
type latchRaw struct {
	Class     string `json:"latchClass"`
	WaitsCum  uint64 `json:"latchWaitsCum"`
	WaitMsCum uint64 `json:"latchWaitMsCum"`
}

// Latches is the bb:latches payload: per-interval deltas for one latch class.
type Latches struct {
	Class      string `json:"latchClass"`
	DeltaWaits uint64 `json:"latchWaits"`
	DeltaMs    uint64 `json:"latchWaitMs"`
}

type latchesSampler struct {
	prev          map[string]latchRaw
	obs           Observation
	baselineEpoch uint64
}

func (latchesSampler) ID() string              { return "bb:latches" }
func (latchesSampler) Kind() string            { return "latches" }
func (latchesSampler) Interval() time.Duration { return DemotedInterval }

func (s *latchesSampler) Observe(o Observation) { s.obs = o }

// Decode follows the bb:waits rules exactly: the first cycle after startup or
// an epoch change only baselines; a class with no baseline, or whose counters
// went backwards, baselines without emitting; an idle class is dropped; an empty
// reply clears every baseline. The map is rebuilt from this cycle's cap-limited
// rows, so it cannot grow.
func (s *latchesSampler) Decode(rows []Row) (any, bool, error) {
	var cur []latchRaw
	truncated, err := decodeRows(rows, &cur, CapLatches)
	if err != nil {
		return nil, false, err
	}
	for _, r := range cur {
		if !latchClassRe.MatchString(r.Class) {
			return nil, false, errLatchClass
		}
	}
	if len(cur) == 0 {
		s.prev = map[string]latchRaw{}
		s.baselineEpoch = s.obs.Epoch
		return nil, false, nil
	}
	next := make(map[string]latchRaw, len(cur))
	first := s.prev == nil || s.baselineEpoch != s.obs.Epoch
	out := make([]Latches, 0, len(cur))
	for _, r := range cur {
		next[r.Class] = r
		if first {
			continue
		}
		p, ok := s.prev[r.Class]
		if !ok || r.WaitsCum < p.WaitsCum || r.WaitMsCum < p.WaitMsCum {
			continue
		}
		dw, dm := r.WaitsCum-p.WaitsCum, r.WaitMsCum-p.WaitMsCum
		if dw == 0 && dm == 0 {
			continue
		}
		out = append(out, Latches{Class: r.Class, DeltaWaits: dw, DeltaMs: dm})
	}
	s.prev = next
	s.baselineEpoch = s.obs.Epoch
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatement("bb:latches", stmtLatches, CapLatches, "sys.dm_os_latch_stats")
	registerExtraColumns(map[string]string{
		"latch_class": "latchClass", "waiting_requests_count": "latchWaitsCum", "latch_wait_ms": "latchWaitMsCum",
	})
	registerExtraRowSpec(reflect.TypeOf(latchRaw{}), rowSpec{cols: map[string]colRule{
		"latchClass": req(), "latchWaitsCum": req(), "latchWaitMsCum": req(),
	}})
	RegisterExtraFactory(TierEverything, func() Sampler { return &latchesSampler{} })
}
