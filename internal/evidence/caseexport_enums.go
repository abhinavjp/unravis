package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// This file holds the fixed vocabularies of the case export. evidence cannot
// import the importers (they import evidence), so each importer's kinds and
// attribute values are listed here; a test in package evidence_test checks the
// attribute names against every importer's own allowlist, so a new importer
// attribute fails the build until it is either listed in exportPolicy or in
// exportDropped.

// exportDropped lists the importer attributes the export leaves out on
// purpose: the IIS URL stem, and the Application Insights operation name (a URL
// path) and operation id (a join key into the client's telemetry).
var exportDropped = map[string]map[string]bool{
	"iis-w3c":     {"stem": true},
	"iis-httperr": {"stem": true},
	"appinsights": {"name": true, "operation_Id": true},
}

// exportKinds is the per-source list of event kinds. A kind that is not on
// the list of its source is exported as "other".
var exportKinds = map[string]map[string]bool{
	"iis-w3c":      set("request"),
	"iis-httperr":  set("httperr"),
	"win-counters": set("counter"),
	"sql-pdh":      set("counter"),
	"win-events": set("win-event", "apppool-recycle", "apppool-idle-shutdown", "task-start", "task-complete",
		"task-fail", "task-skipped", "task-stop"),
	"mongod-json":       set("slow-op"),
	"schedule-hangfire": set("schedule-boundary", "schedule-unknown", "job-run"),
	"schedule-agent":    set("schedule-boundary", "schedule-unknown", "job-run"),
	"appinsights": set("record", "request", "dependency", "exception", "trace", "pageview", "availability",
		"custom-event"),
	"sql-xe":       set("wait", "diagnostics", "error", "non-yielding", "deadlock"),
	"sql-errorlog": set("io-stall", "start", "stop", "login-failed", "non-yielding"),
}

// exportUnits is the list of event units.
var exportUnits = set("ms", "count", "percent-host", "percent-one-core")

// exportSeverities is the list of event severities.
var exportSeverities = set("", "info", "warning", "error", "critical")

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// fold builds a case-insensitive lookup: lower-case spelling -> the canonical
// spelling that is exported.
func fold(v ...string) map[string]string {
	m := make(map[string]string, len(v))
	for _, s := range v {
		m[strings.ToLower(s)] = s
	}
	return m
}

// canonicalValues is the set of canonical spellings of a fold table.
func canonicalValues(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for _, v := range m {
		out[v] = true
	}
	return out
}

// Banner identifiers. The acknowledgement the analyst ticked is client form
// text; the file carries only an identifier of it, and only for a banner text
// this code knows.
const customBannerID = "custom-not-exported"

// knownBanners are the gating banner texts the product shows (build plan
// section on real client exports). Compared after trimming and collapsing
// white space.
var knownBanners = []string{
	"Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention.",
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func bannerHash(text string) string {
	sum := sha256.Sum256([]byte(collapseSpace(text)))
	return "banner-" + hex.EncodeToString(sum[:])[:16]
}

// knownBannerIDs is the set of identifiers of knownBanners.
var knownBannerIDs = func() map[string]bool {
	m := map[string]bool{customBannerID: true}
	for _, b := range knownBanners {
		m[bannerHash(b)] = true
	}
	return m
}()

// bannerID is the identifier of an acknowledgement text.
func bannerID(text string) string {
	id := bannerHash(text)
	if knownBannerIDs[id] && id != customBannerID {
		return id
	}
	return customBannerID
}

// versionLike is the only shape a version string may have in the file.
var versionLike = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9A-Za-z+_-]{1,12}){0,3}$`)

// exportVersion returns v when it is version-like and does not read as an IP
// address, else "".
func exportVersion(v string) string {
	if versionLike.MatchString(v) && !ipv4.MatchString(v) && claimFree(v) {
		return v
	}
	return ""
}

// Wait types: an exact list plus tight families. PREEMPTIVE_* is not listed.
var (
	waitExact = set("ASYNC_NETWORK_IO", "ASYNC_IO_COMPLETION", "WRITELOG", "IO_COMPLETION", "CXPACKET", "CXCONSUMER",
		"SOS_SCHEDULER_YIELD", "THREADPOOL", "RESOURCE_SEMAPHORE", "RESOURCE_SEMAPHORE_QUERY_COMPILE", "CMEMTHREAD",
		"BACKUPIO", "BACKUPBUFFER", "HADR_SYNC_COMMIT", "TRACEWRITE", "LOGBUFFER", "LOGMGR_FLUSH", "LOGMGR_RESERVE_APPEND",
		"WRITE_COMPLETION", "OLEDB", "SLEEP_BPOOL_FLUSH", "EXECSYNC", "IO_RETRY", "DTC", "TRANSACTION_MUTEX",
		"BROKER_RECEIVE_WAITFOR", "WAITFOR", "SOS_PHYS_PAGE_CACHE", "ASYNC_DISKPOOL_LOCK", "SOS_WORK_DISPATCHER")
	waitFamily = []*regexp.Regexp{
		regexp.MustCompile(`^LCK_M_(?:S|U|X|IS|IU|IX|SIU|SIX|UIX|BU|RS_S|RS_U|RIn_NL|RIn_S|RIn_U|RIn_X|RX_S|RX_U|RX_X|SCH_S|SCH_M)$`),
		regexp.MustCompile(`^(?:PAGEIOLATCH|PAGELATCH|LATCH)_(?:SH|UP|EX|KP|DT|NL)$`),
	}
)

func waitTypeOK(v string) bool {
	if waitExact[v] {
		return true
	}
	for _, re := range waitFamily {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// Fixed attribute vocabularies, per attribute.
var (
	httpMethods = fold("GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH", "TRACE", "CONNECT", "PROPFIND",
		"PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "SEARCH", "REPORT")
	httpErrReasons = fold("AppOffline", "AppPoolTimer", "AppShutdown", "BadRequest", "Client_Reset",
		"Connection_Abandoned_By_AppPool", "Connection_Abandoned_By_ReqQueue", "Connection_Dropped", "ConnLimit", "Disabled",
		"EntityTooLarge", "FieldLength", "Forbidden", "Header", "Hostname", "Internal", "N/A", "Number", "Precondition",
		"QueueFull", "RequestLength", "Timer_AppPool", "Timer_ConnectionIdle", "Timer_EntityBody", "Timer_HeaderWait",
		"Timer_MinBytesPerSecond", "URL", "UrlLength", "Verb", "Version_N/P", "other")
	winEventReasons = fold("time-interval", "request-limit", "scheduled-time", "virtual-memory", "isapi-unhealthy",
		"admin-requested", "config-change", "private-memory", "instance-already-running", "timeout", "user-request")
	winEventLogs      = fold("Application", "System", "Setup", "Security", "Microsoft-Windows-TaskScheduler/Operational")
	winEventProviders = fold("Microsoft-Windows-WAS", "Microsoft-Windows-TaskScheduler", "W3SVC", "Application Error",
		"Application Hang", ".NET Runtime", "Service Control Manager", "Microsoft-Windows-IIS-W3SVC-WP", "ASP.NET 4.0.30319.0",
		"Microsoft-Windows-WinRM", "MSSQLSERVER", "Windows Error Reporting")
	mongoCommands = fold("find", "aggregate", "count", "distinct", "getMore", "insert", "update", "delete", "findAndModify",
		"mapReduce", "createIndexes", "dropIndexes", "drop", "create", "collStats", "dbStats", "listCollections",
		"listIndexes", "killCursors", "bulkWrite", "commitTransaction", "abortTransaction", "explain", "validate", "compact",
		"reIndex", "renameCollection", "collMod")
	mongoTypes  = fold("command", "query", "getmore", "update", "insert", "remove", "none", "write", "transaction")
	mongoStages = fold("COLLSCAN", "IXSCAN", "FETCH", "SORT", "SORT_KEY_GENERATOR", "PROJECTION_SIMPLE", "PROJECTION_COVERED",
		"PROJECTION_DEFAULT", "LIMIT", "SKIP", "COUNT_SCAN", "DISTINCT_SCAN", "EOF", "IDHACK", "SHARDING_FILTER", "TEXT",
		"TEXT_MATCH", "SUBPLAN", "OR", "AND_HASH", "AND_SORTED", "MULTI_PLAN", "CACHED_PLAN", "GROUP", "LOOKUP", "UNWIND",
		"UPDATE", "DELETE", "COUNT", "RECORD_STORE_FAST_COUNT", "QUEUED_DATA", "TRIAL", "SHARD_MERGE", "SINGLE_SHARD",
		"COLUMN_SCAN")
	dependencyTypes = fold("SQL", "HTTP", "Azure blob", "Azure table", "Azure queue", "Azure Service Bus", "Azure Event Hubs",
		"Azure DocumentDB", "Azure Cosmos DB", "Azure Search", "InProc", "Queue Message", "Ajax", "Redis", "WCF", "other")
	successValues = fold("true", "false", "unknown")
	xeEvents      = fold("wait_info", "wait_info_external", "sp_server_diagnostics_component_result", "error_reported",
		"scheduler_monitor_non_yielding_ring_buffer_recorded", "scheduler_monitor_non_yielding_iocp_ring_buffer_recorded",
		"scheduler_monitor_non_yielding_rm_ring_buffer_recorded", "xml_deadlock_report")
	xeWaitClasses = fold("lock", "latch", "preemptive", "other")
	xeComponents  = fold("system", "resource", "query_processing", "io_subsystem", "events")
	xeStates      = fold("clean", "warning", "error", "unknown")
	schedTZ       = fold("UTC", "Etc/UTC")
	schedBasis    = fold("last-execution", "history", "disabled-at-export")
	schedReasons  = fold("cron-unsupported", "tz-unknown", "freq-not-time-based", "disabled-at-export", "month-day-missing",
		"window-wraps", "too-many-boundaries")
	schedDST   = fold("ambiguous", "nonexistent")
	schedFreqs = fold("once", "daily", "weekly", "monthly", "monthly-relative")
)

// Performance counter objects and counters: a curated list, matched without
// regard to letter case and exported in the listed spelling. Anything else
// (custom counters, application names) is "[removed]".
var (
	counterObjects = fold("Processor", "Processor Information", "Process", "Process V2", "Memory", "PhysicalDisk", "LogicalDisk",
		"Network Interface", "System", "Paging File", "TCPv4", "TCPv6", "Web Service", "Web Service Cache", "ASP.NET",
		"ASP.NET Applications", ".NET CLR Memory", ".NET CLR Exceptions", ".NET CLR LocksAndThreads", ".NET CLR Jit",
		".NET CLR Loading", "HTTP Service Request Queues", "HTTP Service Url Groups", "W3SVC_W3WP", "WAS_W3WP", "APP_POOL_WAS",
		"Thread", "Server", "Cache",
		"SQLServer:Buffer Manager", "SQLServer:Memory Manager", "SQLServer:SQL Statistics", "SQLServer:General Statistics",
		"SQLServer:Locks", "SQLServer:Latches", "SQLServer:Access Methods", "SQLServer:Wait Statistics", "SQLServer:Databases",
		"SQLServer:Plan Cache", "SQLServer:Transactions", "SQLServer:SQL Errors", "SQLServer:Buffer Node", "SQLServer:Memory Node",
		"SQLServer:Exec Statistics", "SQLServer:Cursor Manager by Type", "SQLServer:Workload Group Stats",
		"SQLServer:Resource Pool Stats", "SQLServer:Availability Replica", "SQLServer:Database Replica")
	counterNames = fold("% Processor Time", "% User Time", "% Privileged Time", "% Idle Time", "% Interrupt Time", "% DPC Time",
		"% C1 Time", "% C2 Time", "% C3 Time", "Processor Frequency", "% of Maximum Frequency", "Interrupts/sec", "DPCs Queued/sec",
		"Processor Queue Length", "Context Switches/sec", "System Calls/sec", "Threads", "Processes",
		"Available MBytes", "Available KBytes", "Available Bytes", "Committed Bytes", "% Committed Bytes In Use",
		"Commit Limit", "Pages/sec", "Pages Input/sec", "Pages Output/sec", "Page Faults/sec", "Pool Nonpaged Bytes",
		"Pool Paged Bytes", "Cache Bytes", "Free & Zero Page List Bytes", "Standby Cache Normal Priority Bytes",
		"% Usage", "% Usage Peak", "Working Set", "Working Set - Private", "Private Bytes", "Virtual Bytes", "Thread Count",
		"Handle Count", "IO Read Bytes/sec", "IO Write Bytes/sec", "IO Data Bytes/sec", "ID Process", "Elapsed Time",
		"% Disk Time", "% Disk Read Time", "% Disk Write Time", "Avg. Disk sec/Read", "Avg. Disk sec/Write", "Avg. Disk sec/Transfer",
		"Avg. Disk Queue Length", "Avg. Disk Read Queue Length", "Avg. Disk Write Queue Length", "Current Disk Queue Length",
		"Disk Reads/sec", "Disk Writes/sec", "Disk Transfers/sec", "Disk Bytes/sec", "Disk Read Bytes/sec", "Disk Write Bytes/sec",
		"Split IO/Sec", "% Free Space", "Free Megabytes", "Bytes Total/sec", "Bytes Received/sec", "Bytes Sent/sec",
		"Packets/sec", "Packets Received/sec", "Packets Sent/sec", "Current Bandwidth", "Output Queue Length",
		"Packets Outbound Discarded", "Packets Received Discarded", "Packets Outbound Errors", "Packets Received Errors",
		"Connections Established", "Connection Failures", "Connections Reset", "Segments Retransmitted/sec",
		"Current Connections", "Total Method Requests/sec", "Get Requests/sec", "Post Requests/sec", "Bytes Received/sec",
		"Current Anonymous Users", "Current NonAnonymous Users", "Not Found Errors/sec", "Locked Errors/sec", "Service Uptime",
		"Requests Current", "Requests Queued", "Requests Executing", "Requests/Sec", "Requests Total", "Requests Failed",
		"Requests Not Found", "Requests Not Authorized", "Requests Timed Out", "Requests Succeeded", "Request Execution Time",
		"Request Wait Time", "Request Bytes In Total", "Request Bytes Out Total", "Errors Total", "Errors Total/Sec",
		"Errors During Execution", "Errors Unhandled During Execution/Sec", "Worker Processes Running", "Sessions Active",
		"Application Restarts", "Worker Process Restarts", "Pipeline Instance Count", "Cache Total Entries",
		"Cache API Hit Ratio", "Cache Total Hit Ratio", "Output Cache Entries", "Output Cache Hit Ratio",
		"# Bytes in all Heaps", "# Gen 0 Collections", "# Gen 1 Collections", "# Gen 2 Collections", "% Time in GC",
		"Gen 0 heap size", "Gen 1 heap size", "Gen 2 heap size", "Large Object Heap size", "Allocated Bytes/sec",
		"# of Exceps Thrown", "# of Exceps Thrown / sec", "# of Filters / sec", "# of Finallys / sec",
		"Throw To Catch Depth / sec", "Current Queue Length", "Contention Rate / sec", "Total # of Contentions",
		"# of current logical Threads", "# of current physical Threads", "# of Methods Jitted", "% Time in Jit",
		"Current Assemblies", "Bytes in Loader Heap", "Total Requests", "Arrivals Rate", "Rejected Requests",
		"MaxQueueItemAge", "CacheHitRatio", "UrisCached", "FilesCached", "MetadataCached",
		"Page life expectancy", "Buffer cache hit ratio", "Page reads/sec", "Page writes/sec", "Lazy writes/sec",
		"Checkpoint pages/sec", "Free list stalls/sec", "Database pages", "Target pages", "Total pages", "Free pages",
		"Stolen pages", "Readahead pages/sec", "Page lookups/sec", "Total Server Memory (KB)", "Target Server Memory (KB)",
		"Memory Grants Pending", "Memory Grants Outstanding", "Granted Workspace Memory (KB)", "Lock Memory (KB)",
		"Optimizer Memory (KB)", "SQL Cache Memory (KB)", "Connection Memory (KB)", "Batch Requests/sec",
		"SQL Compilations/sec", "SQL Re-Compilations/sec", "Failed Auto-Params/sec", "Auto-Param Attempts/sec",
		"User Connections", "Logins/sec", "Logouts/sec", "Processes blocked", "Transactions", "Active Transactions",
		"Lock Waits/sec", "Lock Wait Time (ms)", "Lock Requests/sec", "Lock Timeouts/sec", "Number of Deadlocks/sec",
		"Average Wait Time (ms)", "Average Latch Wait Time (ms)", "Latch Waits/sec", "Total Latch Wait Time (ms)",
		"Full Scans/sec", "Index Searches/sec", "Page Splits/sec", "Forwarded Records/sec", "Workfiles Created/sec",
		"Worktables Created/sec", "Table Lock Escalations/sec", "Range Scans/sec", "Probe Scans/sec",
		"Errors/sec", "Cache Hit Ratio", "Cache Object Counts", "Cache Pages", "Cache Objects in use",
		"Waits started per second", "Waits in progress", "Cumulative wait time (ms) per second", "Log Flushes/sec",
		"Log Flush Wait Time", "Log Flush Waits/sec", "Log Bytes Flushed/sec", "Log File(s) Size (KB)",
		"Log File(s) Used Size (KB)", "Percent Log Used", "Data File(s) Size (KB)", "Transactions/sec",
		"Write Transactions/sec", "Active Temp Tables", "Log Growths", "Log Shrinks", "Repl. Pending Xacts", "Repl. Trans. Rate",
		"Bytes Sent to Replica/sec", "Bytes Received from Replica/sec", "Log Send Queue Size", "Redo Queue Size",
		"Transaction Delay", "Mirrored Write Transactions/sec", "Log Bytes Received/sec", "Recovery Queue", "Redone Bytes/sec")
)

var counterObjectCanon = canonicalValues(counterObjects)

// canonCounterObject maps an object name to its listed spelling. A named SQL
// Server instance's "MSSQL$NAME:" prefix is read as "SQLServer:", so the
// instance name never reaches the file.
func canonCounterObject(v string) (string, bool) {
	lo := strings.ToLower(v)
	if strings.HasPrefix(lo, "mssql$") {
		i := strings.IndexByte(lo, ':')
		if i < 0 {
			return "", false
		}
		lo = "sqlserver" + lo[i:]
	}
	c, ok := counterObjects[lo]
	return c, ok
}
