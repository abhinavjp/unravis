package bbsource

import "sort"

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
	ActiveRequests *float64 `json:"activeRequests"`
	ActiveThreads  *float64 `json:"activeThreads"`
	PingLatencyMs  *float64 `json:"pingLatencyMs"`
}

type tcpGroup struct {
	RetransmitsPerSec  *float64 `json:"retransmitsPerSec"`
	ResetsPerSec       *float64 `json:"resetsPerSec"`
	OutboundQueue      *float64 `json:"outboundQueue"`
	ConnFailuresPerSec *float64 `json:"connFailuresPerSec"`
	DiscardedPerSec    *float64 `json:"discardedPerSec"`
	OutErrorsPerSec    *float64 `json:"outErrorsPerSec"`
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

// groupReadings lists the numbers of the groups of a sample. ok is false when one of them is not a valid number.
func groupReadings(l *line) (out []reading, ok bool) {
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
	if l.SQLHost != nil {
		ins := l.SQLHost.Instances
		if len(ins) > maxInstances {
			ins = ins[:maxInstances]
		}
		// An instance is told apart by its place in the sorted instance names, never by the name itself: the name of a
		// named instance is a server name and stays out of the timeline.
		order := make([]int, len(ins))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return ins[order[a]].Instance < ins[order[b]].Instance })
		for rank, i := range order {
			label := ""
			if len(ins) > 1 {
				label = itoa(int64(rank + 1))
			}
			for _, sp := range sqlSpecs {
				put(sp.get(&ins[i]), sp.object, sp.counter, sp.instance, label, sp.unit)
			}
		}
	}
	if l.HTTP != nil {
		qs := l.HTTP.Queues
		if len(qs) > maxPools {
			qs = qs[:maxPools]
		}
		for _, q := range qs {
			pool := clipName(q.Pool)
			put(q.CurrentQueueSize, "HTTP Service Request Queues", "CurrentQueueSize", pool, "", "")
			put(q.MaxQueueItemAge, "HTTP Service Request Queues", "MaxQueueItemAge", pool, "", "")
			put(q.RejectedRequests, "HTTP Service Request Queues", "Rejected Requests", pool, "", "")
			put(q.ArrivalRate, "HTTP Service Request Queues", "Arrivals Rate", pool, "", "")
		}
	}
	if l.IIS != nil {
		ws := l.IIS.Workers
		if len(ws) > maxPools {
			ws = ws[:maxPools]
		}
		for _, w := range ws {
			pool := clipName(w.Pool)
			put(w.ActiveRequests, "W3SVC_W3WP", "Active Requests", pool, "", "")
			put(w.ActiveThreads, "W3SVC_W3WP", "Active Threads Count", pool, "", "")
			put(w.PingLatencyMs, "Blackbox IIS", "Worker Ping Latency (ms)", pool, "", "ms")
		}
	}
	if t := l.TCP; t != nil {
		put(t.RetransmitsPerSec, "Blackbox TCP", "Retransmits/sec", "", "", "per-sec")
		put(t.ResetsPerSec, "Blackbox TCP", "Resets/sec", "", "", "per-sec")
		put(t.OutboundQueue, "Blackbox TCP", "Outbound Queue", "", "", "count")
		put(t.ConnFailuresPerSec, "Blackbox TCP", "Connection Failures/sec", "", "", "per-sec")
		put(t.DiscardedPerSec, "Blackbox TCP", "Discarded Packets/sec", "", "", "per-sec")
		put(t.OutErrorsPerSec, "Blackbox TCP", "Outbound Errors/sec", "", "", "per-sec")
	}
	return out, ok
}

// clipName keeps a pool name short and printable; an empty name stays empty.
func clipName(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c < 0x7f {
			b = append(b, c)
		}
	}
	return string(b)
}
