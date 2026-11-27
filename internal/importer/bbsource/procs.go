package bbsource

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/procsnap"
	"api-audit-explorer/internal/importer/wincsv"
)

// The "procs" member of a sample lists the busiest processes of that moment. Each number becomes one metric event shaped
// like the process snapshot importer's (metric, process, pid), so the resource analysis joins a process by its id the same
// way. The name is the stored label of the snapshot importer: a server image keeps its name, any other image is a keyed
// pseudonym, never the raw name.

const (
	// KindProcMetric is the kind of a process figure, as the process snapshot importer writes it.
	KindProcMetric = "metric"

	idxProcs  = 1 << 21
	maxProcs  = 128
	procSlots = 8 // event index slots per process (5 are used)
)

// GapProcs says what the process list does not give.
const GapProcs = "A Blackbox sample lists only the busiest processes of that moment, so a process that is quiet is missing from it; " +
	"a process id that a new process takes over is not told apart from the one before it."

type procRowIn struct {
	PID     uint32   `json:"pid"`
	Name    string   `json:"name"`
	CPUPct  *float64 `json:"cpuPct"`
	WS      *uint64  `json:"ws"`
	Private *uint64  `json:"priv"`
	Handles *uint32  `json:"handles"`
	Threads uint32   `json:"threads"`
}

func procEvents(raw json.RawMessage, t time.Time, no int64, opts importer.Options, host string, nt *notes) (evs []evidence.Event, bad bool) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		nt.damaged = true
		return nil, false
	}
	rows := decodeEach[procRowIn](list, nt)
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].PID < rows[b].PID })
	if len(rows) > maxProcs {
		rows, nt.cut = rows[:maxProcs], true
	}
	nt.procsSeen = len(rows) > 0 || nt.procsSeen
	for i, r := range rows {
		if r.PID == 0 {
			nt.damaged = true // a row with no process id cannot be told from another
			continue
		}
		label := procsnap.ProcessLabel(r.Name, opts.Pseudonymize)
		pid := strconv.FormatUint(uint64(r.PID), 10)
		add := func(slot int, metric string, v float64, unit string) {
			if !validNumber(v) {
				bad = true
				return
			}
			e := wincsv.NewEvent(opts, Name, no, idxProcs+i*procSlots+slot)
			vv := v
			e.Kind, e.Host, e.Start, e.End = KindProcMetric, host, t, t
			e.Value, e.Unit, e.TZRule = &vv, unit, clock.RuleUTCField
			e.Attrs = map[string]string{"metric": metric, "process": label, "pid": pid}
			evs = append(evs, e)
		}
		if r.CPUPct != nil {
			add(0, "cpu", *r.CPUPct, "percent-host")
		}
		if r.WS != nil {
			add(1, "working-set", float64(*r.WS), "bytes")
		}
		if r.Private != nil {
			add(2, "private-bytes", float64(*r.Private), "bytes")
		}
		if r.Handles != nil {
			add(3, "handles", float64(*r.Handles), "count")
		}
		if r.Threads > 0 { // the recorder writes 0 when it did not read the count
			add(4, "threads", float64(r.Threads), "count")
		}
	}
	return evs, bad
}
