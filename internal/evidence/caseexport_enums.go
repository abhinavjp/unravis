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

// appLogKinds are the kinds every app-log importer emits (wave-2 plan §2.3).
var appLogKinds = []string{"log-entry", "log-rate", "app-request", "heartbeat-late", "app-start", "app-stop"}

func appLogKindsPlus(extra ...string) map[string]bool {
	return set(append(append([]string{}, appLogKinds...), extra...)...)
}

// exportKinds is the per-source list of event kinds. A kind that is not on
// the list of its source is exported as "other". The wave-2 sources are
// listed before their importers are registered (wave-2 plan, card W2-F).
var exportKinds = map[string]map[string]bool{
	"iis-w3c":      set("request"),
	"iis-httperr":  set("httperr"),
	"win-counters": set("counter"),
	"sql-pdh":      set("counter"),
	"win-events": set("win-event", "apppool-recycle", "apppool-idle-shutdown", "task-start", "task-complete",
		"task-fail", "task-skipped", "task-stop",
		// W2-g
		"worker-process-failure", "apppool-disabled", "app-crash", "app-hang", "service-crash",
		"host-unexpected-shutdown", "low-memory", "aspnet-unhandled", "ancm-start-failure"),
	"mongod-json":       set("slow-op", "mongod-start", "mongod-stop"),
	"schedule-hangfire": set("schedule-boundary", "schedule-unknown", "job-run"),
	"schedule-agent":    set("schedule-boundary", "schedule-unknown", "job-run"),
	"appinsights": set("record", "request", "dependency", "exception", "trace", "pageview", "availability",
		"custom-event"),
	"sql-xe":       set("wait", "diagnostics", "error", "non-yielding", "deadlock"),
	"sql-errorlog": set("io-stall", "start", "stop", "login-failed", "non-yielding"),
	// Wave 2 (W2-b, W2-c, W2-d, W2-j): app logs.
	"log4net-text": appLogKindsPlus(),
	"log4net-xml":  appLogKindsPlus(),
	"serilog-clef": appLogKindsPlus(),
	"serilog-text": appLogKindsPlus(),
	"mel-console":  appLogKindsPlus(),
	"mel-json":     appLogKindsPlus(),
	"ancm-stdout":  appLogKindsPlus("app-session"),
	"node-jsonl":   appLogKindsPlus("proxy-command", "client-event"),
	// W2-e, W2-f
	"dotnet-counters": set("metric"),
	"win-procsnap":    set("metric", "process-start", "process-gone"),
	// W2-h, W2-i
	"redis-slowlog": set("slow-command"),
	"redis-latency": set("latency-spike"),
	"redis-info":    set("metric", "redis-start"),
	"redis-log": set("redis-start", "redis-stop", "bgsave-start", "bgsave-done", "bgsave-fail", "aof-rewrite-start",
		"aof-rewrite-done", "aof-fsync-slow", "client-buffer-closed", "replication-event", "config-warning"),
	// W2-k
	"mongo-profile":      set("slow-op"),
	"mongo-currentop":    set("inflight-op"),
	"mongo-serverstatus": set("metric", "mongod-start"),
	"mongostat-json":     set("metric"),
	// W2-m
	"linux-sadf":    set("metric"),
	"linux-pidstat": set("metric"),
}

// exportUnits is the list of event units.
var exportUnits = set("ms", "count", "percent-host", "percent-one-core", "bytes", "percent", "per-sec", "ratio")

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
		"Microsoft-Windows-WinRM", "MSSQLSERVER", "Windows Error Reporting",
		// W2-g
		"EventLog", "Microsoft-Windows-Kernel-Power", "Microsoft-Windows-Resource-Exhaustion-Detector",
		"IIS AspNetCore Module V2", "IIS AspNetCore Module")
	// faultingApps is the W2-g faulting-app list; the importer writes "other" for any other image.
	faultingApps = fold("w3wp.exe", "dotnet.exe", "node.exe", "redis-server.exe", "memurai.exe", "mongod.exe",
		"sqlservr.exe", "other")
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
		"Transaction Delay", "Mirrored Write Transactions/sec", "Log Bytes Received/sec", "Recovery Queue", "Redone Bytes/sec",
		// W2-f: W3SVC_W3WP and APP_POOL_WAS counters A1 reads.
		"Process ID", "Active Requests", "Active Threads Count", "Requests Rejected", "Requests / Sec")
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

// Wave-2 vocabularies (wave-2 plan §2.2, §2.3 and the cards W2-b to W2-m).
// They are listed before the importers exist so that no card edits this file.
var (
	// appLogLevels holds the level spellings of log4net, winston, Serilog
	// (short and CLEF), MEL (short and JSON) and pino. Spellings that differ
	// only in letter case share one exported form, the last listed: "info",
	// "warn", "Error", "Debug", "Verbose", "Fatal", "Trace".
	appLogLevels = fold("DEBUG", "INFO", "WARN", "ERROR", "FATAL", "TRACE",
		"error", "warn", "info", "http", "verbose", "debug", "silly",
		"VRB", "DBG", "INF", "WRN", "ERR", "FTL",
		"trce", "dbug", "info", "warn", "fail", "crit",
		"Verbose", "Debug", "Information", "Warning", "Error", "Fatal", "Trace", "Critical", "None",
		"10", "20", "30", "40", "50", "60")
	appLogFamilyNames = []string{"redis-timeout", "redis-connection", "sql-pool-timeout", "sql-exec-timeout",
		"mongo-selection-timeout", "mongo-pool-wait", "http-client-timeout", "heartbeat-late", "request-finished",
		"app-start", "app-stop", "out-of-memory", "thread-abort", "task-canceled", "other"}
	appLogFamilies = fold(appLogFamilyNames...)
	// nodeFamilies adds the Redis client events of node-jsonl: a client-event
	// carries its event name in "family" as client-<event>.
	nodeFamilies = fold(append(append([]string{}, appLogFamilyNames...),
		"client-connect", "client-ready", "client-reconnecting", "client-error", "client-end")...)
	// exceptionTypes is the export list of framework exception types; the
	// importer writes "app-exception" for every type outside the §2.2 rule, and
	// the export shows a framework type that is not listed here as "[removed]".
	exceptionTypes = fold("app-exception",
		"System.Exception", "System.SystemException", "System.TimeoutException", "System.OutOfMemoryException",
		"System.InsufficientMemoryException", "System.StackOverflowException", "System.InsufficientExecutionStackException",
		"System.Threading.ThreadAbortException", "System.Threading.ThreadInterruptedException",
		"System.Threading.SynchronizationLockException", "System.Threading.Tasks.TaskCanceledException",
		"System.OperationCanceledException", "System.AggregateException", "System.InvalidOperationException",
		"System.ArgumentException", "System.ArgumentNullException", "System.ArgumentOutOfRangeException",
		"System.NullReferenceException", "System.ObjectDisposedException", "System.FormatException",
		"System.InvalidCastException", "System.IndexOutOfRangeException", "System.NotSupportedException",
		"System.NotImplementedException", "System.UnauthorizedAccessException", "System.DivideByZeroException",
		"System.OverflowException", "System.Collections.Generic.KeyNotFoundException", "System.IO.IOException",
		"System.IO.FileNotFoundException", "System.IO.DirectoryNotFoundException", "System.IO.EndOfStreamException",
		"System.Net.Sockets.SocketException", "System.Net.WebException", "System.Net.Http.HttpRequestException",
		"System.Net.HttpListenerException", "System.ComponentModel.Win32Exception",
		"System.Data.SqlClient.SqlException", "System.Data.DataException", "System.Data.DBConcurrencyException",
		"System.Transactions.TransactionAbortedException", "System.Text.Json.JsonException",
		"System.Web.HttpException", "System.Web.HttpUnhandledException", "System.Web.HttpRequestValidationException",
		"System.ServiceModel.CommunicationException", "System.ServiceModel.EndpointNotFoundException",
		"System.Security.Authentication.AuthenticationException", "System.Security.SecurityException",
		"Microsoft.Data.SqlClient.SqlException", "Microsoft.EntityFrameworkCore.DbUpdateException",
		"Microsoft.EntityFrameworkCore.DbUpdateConcurrencyException",
		"Microsoft.AspNetCore.Connections.ConnectionResetException",
		"Microsoft.AspNetCore.Connections.ConnectionAbortedException",
		"Microsoft.AspNetCore.Server.Kestrel.Core.BadHttpRequestException",
		"Microsoft.AspNetCore.Http.BadHttpRequestException",
		"StackExchange.Redis.RedisTimeoutException", "StackExchange.Redis.RedisConnectionException",
		"StackExchange.Redis.RedisServerException", "StackExchange.Redis.RedisCommandException",
		"StackExchange.Redis.RedisException",
		"MongoDB.Driver.MongoException", "MongoDB.Driver.MongoConnectionException",
		"MongoDB.Driver.MongoConnectionPoolPausedException", "MongoDB.Driver.MongoWaitQueueFullException",
		"MongoDB.Driver.MongoExecutionTimeoutException", "MongoDB.Driver.MongoCommandException",
		"MongoDB.Driver.MongoWriteException", "MongoDB.Driver.MongoNotPrimaryException",
		"MongoDB.Driver.MongoNodeIsRecoveringException", "MongoDB.Driver.MongoAuthenticationException",
		"Newtonsoft.Json.JsonReaderException", "Newtonsoft.Json.JsonSerializationException",
		"Npgsql.NpgsqlException", "Npgsql.PostgresException", "NHibernate.ADOException",
		"NHibernate.StaleObjectStateException", "Hangfire.Storage.DistributedLockTimeoutException",
		"Polly.Timeout.TimeoutRejectedException", "Polly.CircuitBreaker.BrokenCircuitException",
		"Polly.Bulkhead.BulkheadRejectedException", "Grpc.Core.RpcException", "Azure.RequestFailedException",
		"log4net.Core.LogException")
	errnoCodes = fold("ECONNRESET", "ETIMEDOUT", "ECONNREFUSED", "EPIPE", "ENOTFOUND", "EAI_AGAIN", "other")
	// redisCommands is the export list of Redis command names. W2-h owns the
	// importer's table (internal/importer/redis/commands.go); a name the
	// importer keeps that is missing here is shown as "[removed]".
	redisCommands = fold("other",
		"GET", "SET", "MGET", "MSET", "MSETNX", "SETEX", "SETNX", "PSETEX", "GETSET", "GETDEL", "GETEX", "APPEND",
		"STRLEN", "SETRANGE", "GETRANGE", "INCR", "INCRBY", "INCRBYFLOAT", "DECR", "DECRBY", "SETBIT", "GETBIT",
		"BITCOUNT", "BITOP", "BITPOS", "BITFIELD", "DEL", "UNLINK", "EXISTS", "EXPIRE", "PEXPIRE", "EXPIREAT",
		"PEXPIREAT", "EXPIRETIME", "TTL", "PTTL", "PERSIST", "TYPE", "KEYS", "SCAN", "RENAME", "RENAMENX", "DUMP",
		"RESTORE", "OBJECT", "TOUCH", "RANDOMKEY", "COPY", "MOVE", "MIGRATE", "SORT", "SORT_RO",
		"HGET", "HSET", "HMGET", "HMSET", "HGETALL", "HDEL", "HEXISTS", "HINCRBY", "HINCRBYFLOAT", "HKEYS", "HVALS",
		"HLEN", "HSCAN", "HSETNX", "HSTRLEN", "HRANDFIELD",
		"LPUSH", "RPUSH", "LPUSHX", "RPUSHX", "LPOP", "RPOP", "LRANGE", "LLEN", "LINDEX", "LSET", "LREM", "LTRIM",
		"LINSERT", "LPOS", "LMOVE", "BLMOVE", "BLPOP", "BRPOP", "BRPOPLPUSH", "RPOPLPUSH", "LMPOP", "BLMPOP",
		"SADD", "SREM", "SMEMBERS", "SISMEMBER", "SMISMEMBER", "SCARD", "SPOP", "SRANDMEMBER", "SINTER", "SINTERCARD",
		"SUNION", "SDIFF", "SINTERSTORE", "SUNIONSTORE", "SDIFFSTORE", "SSCAN", "SMOVE",
		"ZADD", "ZREM", "ZRANGE", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGEBYLEX",
		"ZRANGESTORE", "ZRANK", "ZREVRANK", "ZSCORE", "ZMSCORE", "ZCARD", "ZCOUNT", "ZLEXCOUNT", "ZINCRBY",
		"ZREMRANGEBYSCORE", "ZREMRANGEBYRANK", "ZREMRANGEBYLEX", "ZUNIONSTORE", "ZINTERSTORE", "ZDIFFSTORE", "ZUNION",
		"ZINTER", "ZDIFF", "ZSCAN", "ZPOPMIN", "ZPOPMAX", "BZPOPMIN", "BZPOPMAX", "ZMPOP", "BZMPOP", "ZRANDMEMBER",
		"XADD", "XREAD", "XREADGROUP", "XRANGE", "XREVRANGE", "XLEN", "XACK", "XDEL", "XTRIM", "XGROUP", "XINFO",
		"XPENDING", "XCLAIM", "XAUTOCLAIM", "XSETID",
		"GEOADD", "GEODIST", "GEOHASH", "GEOPOS", "GEORADIUS", "GEORADIUSBYMEMBER", "GEOSEARCH", "GEOSEARCHSTORE",
		"PFADD", "PFCOUNT", "PFMERGE",
		"PUBLISH", "SUBSCRIBE", "UNSUBSCRIBE", "PSUBSCRIBE", "PUNSUBSCRIBE", "PUBSUB", "SPUBLISH", "SSUBSCRIBE",
		"SUNSUBSCRIBE", "MULTI", "EXEC", "DISCARD", "WATCH", "UNWATCH",
		"EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO", "SCRIPT", "FCALL", "FCALL_RO", "FUNCTION",
		"PING", "ECHO", "AUTH", "HELLO", "SELECT", "QUIT", "RESET", "CLIENT", "INFO", "CONFIG", "DBSIZE", "FLUSHDB",
		"FLUSHALL", "SAVE", "BGSAVE", "BGREWRITEAOF", "LASTSAVE", "SHUTDOWN", "SLOWLOG", "LATENCY", "MEMORY",
		"MONITOR", "DEBUG", "COMMAND", "CLUSTER", "READONLY", "READWRITE", "REPLICAOF", "SLAVEOF", "ROLE", "SYNC",
		"PSYNC", "REPLCONF", "WAIT", "WAITAOF", "TIME", "LOLWUT", "MODULE", "ACL", "SWAPDB", "FAILOVER")
	dotnetProviders = fold("System.Runtime", "Microsoft.AspNetCore.Hosting", "Microsoft-AspNetCore-Server-Kestrel",
		"Microsoft.AspNetCore.Server.Kestrel", "System.Net.Http", "System.Net.NameResolution", "System.Net.Sockets")
	// dotnetCounters are the W2-e counter display names without their unit
	// suffix, and the meter instrument names.
	dotnetCounters = fold("CPU Usage", "Working Set", "GC Heap Size", "Gen 0 GC Count", "Gen 1 GC Count",
		"Gen 2 GC Count", "% Time in GC", "% Time in GC since last GC", "Allocation Rate", "ThreadPool Thread Count",
		"ThreadPool Queue Length", "ThreadPool Completed Work Item Count", "Monitor Lock Contention Count",
		"Exception Count", "Time paused by GC", "LOH Size", "GC Fragmentation", "Connection Queue Length",
		"Request Queue Length", "Current Connections", "Request Rate", "Current Requests", "Failed Requests",
		"Total Requests",
		"dotnet.process.cpu.time", "dotnet.process.memory.working_set", "dotnet.gc.collections", "dotnet.gc.pause.time",
		"dotnet.gc.last_collection.heap.size", "dotnet.thread_pool.thread.count", "dotnet.thread_pool.queue.length",
		"dotnet.thread_pool.work_item.count", "dotnet.monitor.lock_contentions", "dotnet.exceptions",
		"http.server.request.duration", "http.server.active_requests", "kestrel.active_connections",
		"kestrel.queued_connections", "kestrel.queued_requests", "http.client.request.duration",
		"http.client.open_connections", "http.client.request.time_in_queue")
	gcGenerations = fold("gen0", "gen1", "gen2", "loh", "poh")
	procMetrics   = fold("cpu", "working-set", "private-bytes", "handles", "threads")
	// redisLatencyEvents is the documented LATENCY event list plus "other".
	redisLatencyEvents = fold("command", "fast-command", "fork", "aof-fsync-always", "aof-write",
		"aof-write-pending-fsync", "aof-write-active-child", "aof-write-alone", "aof-fstat", "aof-rename",
		"aof-rewrite-diff-write", "expire-cycle", "eviction-cycle", "eviction-del", "eviction-lazyfree",
		"active-defrag-cycle", "rdb-unlink-temp-file", "other")
	redisInfoMetrics = fold("uptime_in_seconds", "used_memory", "used_memory_rss", "used_memory_peak", "maxmemory",
		"mem_fragmentation_ratio", "connected_clients", "blocked_clients", "instantaneous_ops_per_sec",
		"rejected_connections", "evicted_keys", "expired_keys", "latest_fork_usec", "total_connections_received",
		"keyspace_hits", "keyspace_misses", "used_cpu_sys", "used_cpu_user", "rdb_bgsave_in_progress",
		"aof_rewrite_in_progress")
	// redisRoles: redis.log M/S/C/X, and INFO role ("slave" is written as "replica").
	redisRoles = fold("master", "replica", "child", "sentinel")
	// redisLogFamilies refines a redis.log kind; W2-i writes "other" for a line
	// of a known kind that has no finer family.
	redisLogFamilies = fold("rdb", "aof", "fork", "output-buffer-limit", "master-link-down", "master-link-up",
		"full-sync", "partial-sync", "replica-connected", "replica-lost", "overcommit-memory",
		"transparent-huge-pages", "tcp-backlog", "maxclients", "no-config-file", "other")
	mongoOps   = fold("none", "update", "insert", "query", "getmore", "remove", "killcursors", "command")
	boolValues = fold("true", "false")
	// serverStatusMetrics are the dotted serverStatus paths the W2-k recipe projects.
	serverStatusMetrics = fold("uptime", "connections.current", "connections.available",
		"globalLock.currentQueue.total", "globalLock.currentQueue.readers", "globalLock.currentQueue.writers",
		"globalLock.activeClients.readers", "globalLock.activeClients.writers", "opcounters.query",
		"opcounters.insert", "opcounters.update", "opcounters.delete", "opcounters.getmore", "opcounters.command",
		"mem.resident", "mem.virtual", "wiredTiger.cache.bytes currently in the cache",
		"wiredTiger.cache.tracked dirty bytes in the cache", "wiredTiger.cache.maximum bytes configured",
		"wiredTiger.concurrentTransactions.read.out", "wiredTiger.concurrentTransactions.read.available",
		"wiredTiger.concurrentTransactions.write.out", "wiredTiger.concurrentTransactions.write.available")
	// mongostatMetrics are the mongostat columns; "qrw" and "arw" are split
	// into their read and write halves.
	mongostatMetrics = fold("insert", "query", "update", "delete", "getmore", "command", "dirty", "used", "flushes",
		"vsize", "res", "qr", "qw", "ar", "aw", "netIn", "netOut", "conn")
	// linuxMetrics are the sadf -d CPU and memory columns and the pidstat -u -r
	// columns, as metric names. Sizes are written in bytes (unit "bytes").
	linuxMetrics = fold("cpu-user", "cpu-nice", "cpu-system", "cpu-iowait", "cpu-steal", "cpu-idle",
		"mem-free", "mem-avail", "mem-used", "mem-used-percent", "mem-commit-percent",
		"cpu-usr", "cpu-guest", "cpu-wait", "cpu", "minflt-per-sec", "majflt-per-sec", "vsz", "rss",
		"mem-percent")
)
