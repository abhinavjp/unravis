package bbsource

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// The groups of a sample that are read so far: the SQL host counters, the HTTP.sys request queues, the IIS worker
// processes and the TCP figures. Each number becomes one counter event.
//
// Naming rule: a figure the recorder reads exactly as the Windows counter is filed under that counter's own object and
// name, so a recording and a counter export of the same host share a lane. A figure the recorder derives itself (a rate it
// computes, a sum over adapters, a ping it measures) is filed under an object that starts with "Blackbox", so no lane join
// can take it for a Windows counter that means something else.
//
// Units: a counter that is also a Windows counter carries the unit a counter export gives it (none, except host CPU), so
// a lane that holds both never mixes units; a "Blackbox" figure carries its own.

const (
	// maxInstances, maxPools bound what one sample may carry, so a damaged line cannot flood the timeline.
	maxInstances = 16
	maxPools     = 64
)

// reading is one number of a group, named the way the timeline names a counter.
type reading struct {
	object, counter, instance, sqlInstance, unit string
	v                                            float64
}

type sqlInstance struct {
	Instance               string   `json:"instance"`
	BatchRequestsPerSec    *float64 `json:"batchRequestsPerSec"`
	UserConnections        *float64 `json:"userConnections"`
	LockWaitsPerSec        *float64 `json:"lockWaitsPerSec"`
	ProcessesBlocked       *float64 `json:"processesBlocked"`
	MemoryGrantsPending    *float64 `json:"memoryGrantsPending"`
	PageLifeExpectancy     *float64 `json:"pageLifeExpectancy"`
	FreeListStallsPerSec   *float64 `json:"freeListStallsPerSec"`
	CompilationsPerSec     *float64 `json:"compilationsPerSec"`
	ReCompilationsPerSec   *float64 `json:"reCompilationsPerSec"`
	DeadlocksPerSec        *float64 `json:"deadlocksPerSec"`
	LockWaitTimeMs         *float64 `json:"lockWaitTimeMs"`
	LogFlushWaitTimeMs     *float64 `json:"logFlushWaitTimeMs"`
	ForwardedRecordsPerSec *float64 `json:"forwardedRecordsPerSec"`
}

type httpQueue struct {
	Pool             string   `json:"pool"`
	CurrentQueueSize *float64 `json:"currentQueueSize"`
	MaxQueueItemAge  *float64 `json:"maxQueueItemAge"`
	RejectedRequests *float64 `json:"rejectedRequests"`
	ArrivalRate      *float64 `json:"arrivalRate"`
}

type iisWorker struct {
	Pool           string   `json:"pool"`
	PID            uint32   `json:"pid"`
	ActiveRequests *float64 `json:"activeRequests"`
	ActiveThreads  *float64 `json:"activeThreads"`
	PingLatencyMs  *float64 `json:"pingLatencyMs"`
}

type tcpGroup struct {
	RetransmitsPerSec    *float64 `json:"retransmitsPerSec"`
	ResetsPerSec         *float64 `json:"resetsPerSec"`
	OutboundQueue        *float64 `json:"outboundQueue"`
	OutboundQueuePartial bool     `json:"outboundQueuePartial"`
	ConnFailuresPerSec   *float64 `json:"connFailuresPerSec"`
	DiscardedPerSec      *float64 `json:"discardedPerSec"`
	DiscardedPartial     bool     `json:"discardedPartial"`
	OutErrorsPerSec      *float64 `json:"outErrorsPerSec"`
	OutErrorsPartial     bool     `json:"outErrorsPartial"`
}

// notes collects what one file's groups could not show, so the report can say so once.
type notes struct {
	cut, damaged, partial bool
	redisSeen             bool      // a sample had a Redis group
	sqlLog, dumpsSeen     bool      // a sample had SQL log entries, a dump list
	firstT                time.Time // the time of the first sample of the file
	redis                 *redisState
	dumpSeen              map[dumpKey]struct{} // dump files already shown, by a hash of the name, time and size
}

// sqlSpec says how one SQL host figure is named. All of them are read from the same Windows counters the SQL Server
// performance-counter importer reads.
type sqlSpec struct {
	object, counter, instance, unit string
	get                             func(*sqlInstance) *float64
}

var sqlSpecs = []sqlSpec{
	{"SQLServer:SQL Statistics", "Batch Requests/sec", "", "", func(s *sqlInstance) *float64 { return s.BatchRequestsPerSec }},
	{"SQLServer:General Statistics", "User Connections", "", "", func(s *sqlInstance) *float64 { return s.UserConnections }},
	{"SQLServer:Locks", "Lock Waits/sec", "_Total", "", func(s *sqlInstance) *float64 { return s.LockWaitsPerSec }},
	{"SQLServer:General Statistics", "Processes blocked", "", "", func(s *sqlInstance) *float64 { return s.ProcessesBlocked }},
	{"SQLServer:Memory Manager", "Memory Grants Pending", "", "", func(s *sqlInstance) *float64 { return s.MemoryGrantsPending }},
	{"SQLServer:Buffer Manager", "Page life expectancy", "", "", func(s *sqlInstance) *float64 { return s.PageLifeExpectancy }},
	{"SQLServer:Buffer Manager", "Free list stalls/sec", "", "", func(s *sqlInstance) *float64 { return s.FreeListStallsPerSec }},
	{"SQLServer:SQL Statistics", "SQL Compilations/sec", "", "", func(s *sqlInstance) *float64 { return s.CompilationsPerSec }},
	{"SQLServer:SQL Statistics", "SQL Re-Compilations/sec", "", "", func(s *sqlInstance) *float64 { return s.ReCompilationsPerSec }},
	{"SQLServer:Locks", "Number of Deadlocks/sec", "_Total", "", func(s *sqlInstance) *float64 { return s.DeadlocksPerSec }},
	{"SQLServer:Locks", "Lock Wait Time (ms)", "_Total", "", func(s *sqlInstance) *float64 { return s.LockWaitTimeMs }},
	{"SQLServer:Databases", "Log Flush Wait Time", "_Total", "", func(s *sqlInstance) *float64 { return s.LogFlushWaitTimeMs }},
	{"SQLServer:Access Methods", "Forwarded Records/sec", "", "", func(s *sqlInstance) *float64 { return s.ForwardedRecordsPerSec }},
}

// isDefaultInstance says whether a SQL Server instance name is the default instance.
func isDefaultInstance(n string) bool { return n == "" || strings.EqualFold(n, "MSSQLSERVER") }

// groupReadings lists the numbers of the groups of a sample. ok is false when one of them is not a valid number.
//
// Series identity without names: a name of an app pool, a queue or a named SQL instance is a client name and stays out of
// the timeline. An IIS worker is told apart by its process id ("pid-N", as a counter export does), a SQL instance and an
// HTTP queue by their place in the sorted names of that sample (the default SQL instance always first, and the number is
// always set, so a second instance never renames the first).
func groupReadings(l *line, nt *notes) (out []reading, ok bool) {
	ok = true
	put := func(p *float64, object, counter, instance, sqlInst, unit string) {
		if p == nil {
			return // not read this time: no event, never 0
		}
		if !validNumber(*p) {
			ok = false
			return
		}
		out = append(out, reading{object: object, counter: counter, instance: instance, sqlInstance: sqlInst, unit: unit, v: *p})
	}
	if len(l.SQLHost) > 0 {
		var g struct {
			Instances []sqlInstance `json:"instances"`
		}
		if json.Unmarshal(l.SQLHost, &g) != nil {
			nt.damaged = true
		}
		ins := g.Instances
		order := make([]int, len(ins))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			x, y := ins[order[a]].Instance, ins[order[b]].Instance
			if dx, dy := isDefaultInstance(x), isDefaultInstance(y); dx != dy {
				return dx
			}
			return x < y
		})
		if len(order) > maxInstances {
			order, nt.cut = order[:maxInstances], true
		}
		for rank, i := range order {
			for _, sp := range sqlSpecs {
				put(sp.get(&ins[i]), sp.object, sp.counter, sp.instance, itoa(int64(rank+1)), sp.unit)
			}
		}
	}
	if len(l.HTTP) > 0 {
		var g struct {
			Queues []httpQueue `json:"queues"`
		}
		if json.Unmarshal(l.HTTP, &g) != nil {
			nt.damaged = true
		}
		qs := g.Queues
		sort.SliceStable(qs, func(a, b int) bool { return qs[a].Pool < qs[b].Pool })
		if len(qs) > maxPools {
			qs, nt.cut = qs[:maxPools], true
		}
		for i, q := range qs {
			inst := "queue-" + itoa(int64(i+1))
			put(q.CurrentQueueSize, "HTTP Service Request Queues", "CurrentQueueSize", inst, "", "")
			put(q.MaxQueueItemAge, "HTTP Service Request Queues", "MaxQueueItemAge", inst, "", "")
			put(q.RejectedRequests, "HTTP Service Request Queues", "RejectedRequests", inst, "", "")
			put(q.ArrivalRate, "HTTP Service Request Queues", "ArrivalRate", inst, "", "")
		}
	}
	if len(l.IIS) > 0 {
		var g struct {
			Workers []iisWorker `json:"workers"`
		}
		if json.Unmarshal(l.IIS, &g) != nil {
			nt.damaged = true
		}
		ws := g.Workers
		sort.SliceStable(ws, func(a, b int) bool { return ws[a].PID < ws[b].PID })
		if len(ws) > maxPools {
			ws, nt.cut = ws[:maxPools], true
		}
		for _, w := range ws {
			if w.PID == 0 {
				nt.damaged = true // a worker with no process id cannot be told from another
				continue
			}
			inst := "pid-" + itoa(int64(w.PID))
			put(w.ActiveRequests, "W3SVC_W3WP", "Active Requests", inst, "", "")
			put(w.ActiveThreads, "W3SVC_W3WP", "Active Threads Count", inst, "", "")
			put(w.PingLatencyMs, "Blackbox IIS", "Worker Ping Latency (ms)", inst, "", "ms")
		}
	}
	if len(l.TCP) > 0 {
		var t tcpGroup
		if json.Unmarshal(l.TCP, &t) != nil {
			nt.damaged = true
		}
		put(t.RetransmitsPerSec, "Blackbox TCP", "Retransmits/sec", "", "", "per-sec")
		put(t.ResetsPerSec, "Blackbox TCP", "Resets/sec", "", "", "per-sec")
		put(t.ConnFailuresPerSec, "Blackbox TCP", "Connection Failures/sec", "", "", "per-sec")
		for _, f := range []struct {
			v       *float64
			partial bool
			counter string
			unit    string
		}{
			{t.OutboundQueue, t.OutboundQueuePartial, "Outbound Queue", "count"},
			{t.DiscardedPerSec, t.DiscardedPartial, "Discarded Packets/sec", "per-sec"},
			{t.OutErrorsPerSec, t.OutErrorsPartial, "Outbound Errors/sec", "per-sec"},
		} {
			if f.partial {
				if f.v != nil {
					nt.partial = true
				}
				continue
			}
			put(f.v, "Blackbox TCP", f.counter, "", "", f.unit)
		}
	}
	return out, ok
}
