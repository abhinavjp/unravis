// Package resource is the resource catalogue (plan §4.2): it reads the counter
// and metric events of a case, maps them to a fixed set of metric ids, keys
// process series by a stable process identity, gives every sample a time span,
// derives start and stop markers and reports per-series coverage. The rules in
// internal/resource/rules read it; nothing here decides what a value means.
package resource

import (
	"sort"
	"strings"

	"api-audit-explorer/internal/evidence"
)

// Metric ids (plan §4.2).
const (
	HostCPU           = "host.cpu"
	HostMemAvailable  = "host.mem.available"
	HostMemCommitted  = "host.mem.committed"
	HostPages         = "host.pages"
	HostDiskSecPerTx  = "host.disk.sec_per_transfer"
	HostDiskQueue     = "host.disk.queue"
	HostNetBytes      = "host.net.bytes"
	ProcCPU           = "proc.cpu"
	ProcWS            = "proc.ws"
	ProcPrivate       = "proc.private"
	ProcHandles       = "proc.handles"
	ProcThreads       = "proc.threads"
	CLRGen2           = "clr.gen2"
	CLRGCTime         = "clr.gc_time"
	ASPNetQueued      = "aspnet.queued"
	ASPNetCurrent     = "aspnet.current"
	ASPNetWaitMs      = "aspnet.wait_ms"
	IISWPActive       = "iis.wp.active"
	NetTPQueue        = "net.tp.queue"
	NetTPThreads      = "net.tp.threads"
	NetGCPause        = "net.gc_pause"
	KestrelQueued     = "kestrel.queued"
	RedisMem          = "redis.mem"
	RedisClients      = "redis.clients"
	RedisBlocked      = "redis.blocked"
	RedisOps          = "redis.ops"
	MongoQueue        = "mongo.queue"
	MongoConns        = "mongo.conns"
	MongoWTDirty      = "mongo.wt.dirty"
	unitKeep          = "" // a rule with this unit keeps the event's own unit
	sourceWinCounters = "win-counters"
	sourceSQLPDH      = "sql-pdh"
	sourceProcSnap    = "win-procsnap"
	sourceWinLive     = "win-live"
	sourceDotnet      = "dotnet-counters"
	sourceRedisInfo   = "redis-info"
	sourceSadf        = "linux-sadf"
	sourcePidstat     = "linux-pidstat"
	sourceMongoSS     = "mongo-serverstatus"
	sourceMongostat   = "mongostat-json"
)

// Metric is what one event becomes in the catalogue: the metric id, the unit of
// the sample value, the factor applied to the event value, and whether the value
// is a counter that only grows until its process restarts (Sample.Delta holds
// the step from the previous sample).
type Metric struct {
	ID         string
	Unit       string
	Scale      float64
	Cumulative bool
}

// scope says whether a series belongs to a host or to a process on it.
type scope int

const (
	scopeHost scope = iota
	scopeProc
)

// cond is one test on an event's attributes.
type cond func(attrs map[string]string) bool

// rule maps events of some sources and one kind, whose attributes pass every
// cond, to a metric.
type rule struct {
	id         string
	sources    []string
	kind       string
	when       []cond
	unit       string  // unitKeep keeps the event unit
	scale      float64 // 0 means 1
	cumulative bool
	// rateWhenPerSec: the source reports this counter either as a running
	// total (cumulative, unit count) or, when the event unit is per-sec, as a
	// rate that is used as it is.
	rateWhenPerSec bool
	complement     bool // value becomes 100 - value (an idle percentage)
	scope          scope
	// pending marks a rule written from the plan for an importer that is not
	// merged yet: its kind and attribute values are not checked against code.
	pending bool
}

// attrIs holds when attribute key equals one of vals, ignoring case.
func attrIs(key string, vals ...string) cond {
	return func(a map[string]string) bool {
		v, ok := a[key]
		if !ok {
			return false
		}
		for _, w := range vals {
			if strings.EqualFold(v, w) {
				return true
			}
		}
		return false
	}
}

// objectASPNET holds for the ASP.NET objects that carry Requests Queued and
// friends: "ASP.NET" and the versioned "ASP.NET v4.0.30319".
func objectASPNET(a map[string]string) bool {
	o := strings.ToLower(a["object"])
	return o == "asp.net" || strings.HasPrefix(o, "asp.net v")
}

// objectCLRMemory holds for ".NET CLR Memory".
var objectCLRMemory = attrIs("object", ".NET CLR Memory")

var (
	pdh        = []string{sourceWinCounters, sourceSQLPDH}
	procsnap   = []string{sourceProcSnap, sourceWinLive} // the live sampler writes the same process metrics
	winlive    = []string{sourceWinLive}
	dotnet     = []string{sourceDotnet}
	redisInfo  = []string{sourceRedisInfo}
	sadf       = []string{sourceSadf}
	pidstat    = []string{sourcePidstat}
	mongoSS    = []string{sourceMongoSS}
	mongostat  = []string{sourceMongostat}
	objProcess = attrIs("object", "Process", "Process V2")
	objDisk    = attrIs("object", "PhysicalDisk", "LogicalDisk")
	objMemory  = attrIs("object", "Memory")
)

func counter(names ...string) cond  { return attrIs("counter", names...) }
func metricIs(names ...string) cond { return attrIs("metric", names...) }

// rules is the metric map. Order matters only for readability: the conditions
// of two rules never hold for the same event. Units are the event unit words
// (evidence.Event.Unit). Sources are importer names as merged at the time of
// writing; rules marked pending follow the wave plan text for importers that
// were not merged (redis INFO W2-h, Linux sadf and pidstat W2-m).
var rules = []rule{
	// Host CPU: Processor(_Total) % Processor Time is already percent-host.
	{id: HostCPU, sources: pdh, kind: "counter", unit: unitKeep, when: []cond{
		attrIs("object", "Processor", "Processor Information"), attrIs("instance", "_Total"), counter("% Processor Time")}},
	{id: HostCPU, sources: sadf, kind: "metric", unit: "percent-host", complement: true, pending: true, when: []cond{
		metricIs("%idle", "idle")}},

	// Host memory and paging. Available MBytes is MiB.
	{id: HostMemAvailable, sources: pdh, kind: "counter", unit: "bytes", scale: 1 << 20, when: []cond{objMemory, counter("Available MBytes")}},
	{id: HostMemAvailable, sources: pdh, kind: "counter", unit: "bytes", scale: 1 << 10, when: []cond{objMemory, counter("Available KBytes")}},
	{id: HostMemAvailable, sources: pdh, kind: "counter", unit: "bytes", when: []cond{objMemory, counter("Available Bytes")}},
	{id: HostMemAvailable, sources: sadf, kind: "metric", unit: "bytes", scale: 1 << 10, pending: true, when: []cond{metricIs("kbavail")}},
	{id: HostMemCommitted, sources: pdh, kind: "counter", unit: "bytes", when: []cond{objMemory, counter("Committed Bytes")}},
	{id: HostPages, sources: pdh, kind: "counter", unit: "per-sec", when: []cond{objMemory, counter("Pages/sec")}},

	// Disks: seconds per transfer is stored in milliseconds.
	{id: HostDiskSecPerTx, sources: pdh, kind: "counter", unit: "ms", scale: 1000, when: []cond{objDisk, counter("Avg. Disk sec/Transfer")}},
	{id: HostDiskQueue, sources: pdh, kind: "counter", unit: "count", when: []cond{objDisk, counter("Current Disk Queue Length", "Avg. Disk Queue Length")}},
	{id: HostNetBytes, sources: pdh, kind: "counter", unit: "per-sec", when: []cond{attrIs("object", "Network Interface"), counter("Bytes Total/sec")}},

	// Processes.
	{id: ProcCPU, sources: pdh, kind: "counter", unit: unitKeep, scope: scopeProc, when: []cond{objProcess, counter("% Processor Time")}},
	{id: ProcCPU, sources: procsnap, kind: "metric", unit: unitKeep, scope: scopeProc, when: []cond{metricIs("cpu")}},
	{id: ProcCPU, sources: dotnet, kind: "metric", unit: unitKeep, scope: scopeProc, when: []cond{counter("CPU Usage")}},
	{id: ProcCPU, sources: pidstat, kind: "metric", unit: unitKeep, scope: scopeProc, pending: true, when: []cond{metricIs("%cpu")}},
	{id: ProcWS, sources: pdh, kind: "counter", unit: "bytes", scope: scopeProc, when: []cond{objProcess, counter("Working Set")}},
	{id: ProcWS, sources: procsnap, kind: "metric", unit: "bytes", scope: scopeProc, when: []cond{metricIs("working-set")}},
	{id: ProcWS, sources: dotnet, kind: "metric", unit: "bytes", scope: scopeProc, when: []cond{counter("Working Set", "dotnet.process.memory.working_set")}},
	{id: ProcWS, sources: pidstat, kind: "metric", unit: "bytes", scale: 1 << 10, scope: scopeProc, pending: true, when: []cond{metricIs("rss")}},
	{id: ProcPrivate, sources: pdh, kind: "counter", unit: "bytes", scope: scopeProc, when: []cond{objProcess, counter("Private Bytes")}},
	{id: ProcPrivate, sources: procsnap, kind: "metric", unit: "bytes", scope: scopeProc, when: []cond{metricIs("private-bytes")}},
	{id: ProcHandles, sources: pdh, kind: "counter", unit: "count", scope: scopeProc, when: []cond{objProcess, counter("Handle Count")}},
	{id: ProcHandles, sources: procsnap, kind: "metric", unit: "count", scope: scopeProc, when: []cond{metricIs("handles")}},
	{id: ProcThreads, sources: pdh, kind: "counter", unit: "count", scope: scopeProc, when: []cond{objProcess, counter("Thread Count")}},
	{id: ProcThreads, sources: procsnap, kind: "metric", unit: "count", scope: scopeProc, when: []cond{metricIs("threads")}},

	// CLR: the PDH Gen 2 count is a running total; dotnet-counters reports a rate.
	{id: CLRGen2, sources: pdh, kind: "counter", unit: "count", cumulative: true, scope: scopeProc, when: []cond{objectCLRMemory, counter("# Gen 2 Collections")}},
	{id: CLRGen2, sources: dotnet, kind: "metric", unit: "count", cumulative: true, rateWhenPerSec: true, scope: scopeProc, when: []cond{counter("Gen 2 GC Count")}},
	{id: CLRGen2, sources: dotnet, kind: "metric", unit: "count", cumulative: true, rateWhenPerSec: true, scope: scopeProc, when: []cond{counter("dotnet.gc.collections"), attrIs("generation", "gen2")}},
	{id: CLRGCTime, sources: pdh, kind: "counter", unit: "percent", scope: scopeProc, when: []cond{objectCLRMemory, counter("% Time in GC")}},
	{id: CLRGCTime, sources: dotnet, kind: "metric", unit: "percent", scope: scopeProc, when: []cond{counter("% Time in GC since last GC", "% Time in GC")}},

	// ASP.NET (Framework, host-wide objects) and ASP.NET Core hosting.
	{id: ASPNetQueued, sources: pdh, kind: "counter", unit: "count", when: []cond{objectASPNET, counter("Requests Queued")}},
	{id: ASPNetCurrent, sources: pdh, kind: "counter", unit: "count", when: []cond{objectASPNET, counter("Requests Current")}},
	{id: ASPNetCurrent, sources: dotnet, kind: "metric", unit: "count", scope: scopeProc, when: []cond{counter("Current Requests", "http.server.active_requests")}},
	{id: ASPNetWaitMs, sources: pdh, kind: "counter", unit: "ms", when: []cond{objectASPNET, counter("Request Wait Time")}},
	{id: IISWPActive, sources: pdh, kind: "counter", unit: "count", scope: scopeProc, when: []cond{attrIs("object", "W3SVC_W3WP"), counter("Active Requests")}},

	// .NET runtime and Kestrel (dotnet-counters).
	{id: NetTPQueue, sources: dotnet, kind: "metric", unit: "count", scope: scopeProc, when: []cond{counter("ThreadPool Queue Length", "dotnet.thread_pool.queue.length")}},
	{id: NetTPThreads, sources: dotnet, kind: "metric", unit: "count", scope: scopeProc, when: []cond{counter("ThreadPool Thread Count", "dotnet.thread_pool.thread.count")}},
	{id: NetGCPause, sources: dotnet, kind: "metric", unit: unitKeep, scope: scopeProc, when: []cond{counter("Time paused by GC", "dotnet.gc.pause.time")}},
	{id: KestrelQueued, sources: dotnet, kind: "metric", unit: "count", scope: scopeProc, when: []cond{counter(
		"Request Queue Length", "Connection Queue Length", "kestrel.queued_requests", "kestrel.queued_connections")}},

	// Redis INFO (W2-h, not merged).
	{id: RedisMem, sources: redisInfo, kind: "metric", unit: "bytes", when: []cond{metricIs("used_memory")}},
	{id: RedisClients, sources: redisInfo, kind: "metric", unit: "count", when: []cond{metricIs("connected_clients")}},
	{id: RedisBlocked, sources: redisInfo, kind: "metric", unit: "count", when: []cond{metricIs("blocked_clients")}},
	{id: RedisOps, sources: redisInfo, kind: "metric", unit: "per-sec", when: []cond{metricIs("instantaneous_ops_per_sec")}},

	// Mongo serverStatus and mongostat.
	{id: MongoQueue, sources: mongoSS, kind: "metric", unit: "count", when: []cond{metricIs("globalLock.currentQueue.total")}},
	{id: MongoQueue, sources: mongostat, kind: "metric", unit: "count", when: []cond{metricIs("qr", "qw")}},
	{id: MongoConns, sources: mongoSS, kind: "metric", unit: "count", when: []cond{metricIs("connections.current")}},
	{id: MongoConns, sources: mongostat, kind: "metric", unit: "count", when: []cond{metricIs("conn")}},
	{id: MongoWTDirty, sources: mongoSS, kind: "metric", unit: "bytes", when: []cond{metricIs("wiredTiger.cache.tracked dirty bytes in the cache")}},
	{id: MongoWTDirty, sources: mongostat, kind: "metric", unit: unitKeep, when: []cond{metricIs("dirty")}},

	// Live sampler (win-live) host metrics. Its process metrics are in the
	// procsnap rules above.
	{id: HostCPU, sources: winlive, kind: "metric", unit: unitKeep, when: []cond{metricIs("host-cpu")}},
	{id: HostMemAvailable, sources: winlive, kind: "metric", unit: "bytes", when: []cond{metricIs("mem-available")}},
	{id: HostMemCommitted, sources: winlive, kind: "metric", unit: "bytes", when: []cond{metricIs("mem-committed")}},
}

// ruleIndex holds the rules by source and kind.
var ruleIndex = func() map[string][]int {
	m := map[string][]int{}
	for i, r := range rules {
		for _, s := range r.sources {
			m[s+"\x00"+r.kind] = append(m[s+"\x00"+r.kind], i)
		}
	}
	return m
}()

// match returns the rule an event matches, if any.
func match(ev evidence.Event) (rule, bool) {
	for _, i := range ruleIndex[ev.Source+"\x00"+ev.Kind] {
		r := rules[i]
		ok := true
		for _, c := range r.when {
			if !c(ev.Attrs) {
				ok = false
				break
			}
		}
		if ok {
			return r, true
		}
	}
	return rule{}, false
}

// metricOf is the Metric an event becomes under r.
func (r rule) metricOf(ev evidence.Event) Metric {
	m := Metric{ID: r.id, Unit: r.unit, Scale: r.scale, Cumulative: r.cumulative}
	if m.Scale == 0 {
		m.Scale = 1
	}
	if m.Unit == unitKeep {
		m.Unit = ev.Unit
	}
	if r.rateWhenPerSec && ev.Unit == "per-sec" {
		m.Unit, m.Cumulative = "per-sec", false
	}
	return m
}

// Lookup returns the Metric an event maps to. ok is false for an event that
// is not in the metric map.
func Lookup(ev evidence.Event) (m Metric, ok bool) {
	r, ok := match(ev)
	if !ok {
		return Metric{}, false
	}
	return r.metricOf(ev), true
}

// MetricInfo describes one metric id: the importers whose events map to it,
// and those among them whose mapping awaits an importer that is not merged
// (the kind and attribute values follow the plan text and are unchecked).
type MetricInfo struct {
	ID      string
	Sources []string
	Pending []string
}

// Metrics lists every metric id in the map, sorted by id.
func Metrics() []MetricInfo {
	by := map[string]*MetricInfo{}
	var ids []string
	add := func(list *[]string, s string) {
		for _, x := range *list {
			if x == s {
				return
			}
		}
		*list = append(*list, s)
	}
	for _, r := range rules {
		mi, ok := by[r.id]
		if !ok {
			mi = &MetricInfo{ID: r.id}
			by[r.id] = mi
			ids = append(ids, r.id)
		}
		for _, s := range r.sources {
			add(&mi.Sources, s)
			if r.pending {
				add(&mi.Pending, s)
			}
		}
	}
	sort.Strings(ids)
	out := make([]MetricInfo, 0, len(ids))
	for _, id := range ids {
		mi := by[id]
		sort.Strings(mi.Sources)
		sort.Strings(mi.Pending)
		out = append(out, *mi)
	}
	return out
}
