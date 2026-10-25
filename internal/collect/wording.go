package collect

import "strings"

// rule is one kind of log the list can suggest. Its sentences are fixed text:
// they name a kind of file and what it records, never a value from the case.
// They are held to the same bar as every on-screen sentence: plain words, short
// sentences (reading grade 8.5 or lower) and no word that names a cause. The
// web test lints the same sentences, read from testdata/wording.golden.json.
type rule struct {
	id       string
	weight   int
	importer string
	title    string
	why      string
	how      string
	applies  func(profile) bool
}

// The tiers, best first: host counters, application log, the other web logs,
// process snapshot, runtime counters, then the store-specific logs. Gaps in the
// numbers leave room to add a rule without renumbering.
const (
	wHostCounters = 10
	wAppLog       = 20
	wIIS          = 25
	wHTTPERR      = 26
	wEvents       = 27
	wProcsnap     = 30
	wRuntime      = 40
	wSQL          = 50
	wRedis        = 60
	wMongo        = 70
)

func isWindows(p profile) bool {
	return p.any(webSignals...) || p.any(sqlSignals...) || p.any(machineSignals...)
}
func isWeb(p profile) bool   { return p.any(webSignals...) }
func isSQL(p profile) bool   { return p.any(sqlSignals...) }
func isRedis(p profile) bool { return p.any(redisSignals...) }
func isMongo(p profile) bool { return p.any(mongoSignals...) }

// lacks builds a rule test: the host is of the given kind and has no file from
// any of the listed importers.
func lacks(kind func(profile) bool, names ...string) func(profile) bool {
	return func(p profile) bool { return kind(p) && p.none(names...) }
}

var rules = []rule{
	{
		id: "host-counters", weight: wHostCounters, importer: winCounters,
		title:   "Windows performance counters",
		why:     "They show CPU, memory, disk and network use on this host, minute by minute.",
		how:     "Log the counters with Performance Monitor, then run relog to save a CSV file. Import that CSV file.",
		applies: lacks(isWindows, winCounters),
	},
	{
		id: "app-log", weight: wAppLog, importer: strings.Join(appLogImporters, ", "),
		title:   "Application log",
		why:     "It records the warnings and errors the app wrote, with times.",
		how:     "Copy the log4net, Serilog, ASP.NET Core or ANCM stdout log files for the same days. Import them as text, XML or JSON files.",
		applies: lacks(isWeb, appLogImporters...),
	},
	{
		id: "iis-log", weight: wIIS, importer: iisW3C,
		title:   "IIS request log",
		why:     "It lists each request with its time, status and duration.",
		how:     "Make sure the time-taken field is on. Then copy the W3C log files for the same days and import the .log files.",
		applies: lacks(isWeb, iisW3C),
	},
	{
		id: "httperr-log", weight: wHTTPERR, importer: iisHTTPERR,
		title:   "HTTP.sys error log",
		why:     "It records connections and requests that failed before they reached the app.",
		how:     "Copy the files from the HTTPERR log folder for the same days. Import the .log files.",
		applies: lacks(isWeb, iisHTTPERR),
	},
	{
		id: "event-logs", weight: wEvents, importer: winEvents,
		title:   "Windows event logs",
		why:     "They record app pool starts, stops and recycles, and service events, with times.",
		how:     "Save the System and Application logs with Get-WinEvent and Export-Csv. Import the CSV file.",
		applies: lacks(isWindows, winEvents),
	},
	{
		id: "process-snapshot", weight: wProcsnap, importer: winProcsnap,
		title:   "Process snapshots",
		why:     "They list which processes use CPU and memory at each sample time, so you can see what else runs on the host.",
		how:     "Run Get-Process on a timer and save it with Export-Csv. Add a SampleUtc column, then import the CSV file.",
		applies: lacks(isWindows, winProcsnap, winLive),
	},
	{
		id: "runtime-counters", weight: wRuntime, importer: dotnetCnt,
		title:   ".NET runtime counters",
		why:     "They show garbage collection, thread pool and exception counts inside the app process, over time.",
		how:     "Run dotnet-counters collect on the app process and save a CSV or JSON file. Import that file.",
		applies: lacks(isWeb, dotnetCnt),
	},
	{
		id: "sql-errorlog", weight: wSQL, importer: sqlErrorlog,
		title:   "SQL Server error log",
		why:     "It lists server messages, such as errors and restarts, with times.",
		how:     "Copy the ERRORLOG files from the SQL Server log folder for the same days. Import the text files.",
		applies: lacks(isSQL, sqlErrorlog),
	},
	{
		id: "sql-counters", weight: wSQL + 1, importer: sqlPDH,
		title:   "SQL Server performance counters",
		why:     "They show batch rate, waits, memory and buffer use inside SQL Server, minute by minute.",
		how:     "Log the SQL Server counters with Performance Monitor, then run relog to save a CSV file. Import that CSV file.",
		applies: lacks(isSQL, sqlPDH),
	},
	{
		id: "sql-health-events", weight: wSQL + 2, importer: sqlXE,
		title:   "SQL Server health events",
		why:     "The system_health session records waits, blocking reports and deadlocks, with times.",
		how:     "Export the system_health events to a CSV file. Leave out the columns that hold query text, then import the file.",
		applies: lacks(isSQL, sqlXE),
	},
	{
		id: "redis-info", weight: wRedis, importer: redisInfo,
		title:   "Redis INFO output",
		why:     "It shows memory, clients, evictions and command counts at each sample.",
		how:     "Run redis-cli INFO every minute and note the time of each sample. Import the saved text file.",
		applies: lacks(isRedis, redisInfo),
	},
	{
		id: "redis-latency", weight: wRedis + 1, importer: redisLat,
		title:   "Redis latency history",
		why:     "It lists the delays Redis noted for each event, with times.",
		how:     "Run redis-cli LATENCY HISTORY for each event and save the output. Import the text or JSON file.",
		applies: lacks(isRedis, redisLat),
	},
	{
		id: "redis-slowlog", weight: wRedis + 2, importer: redisSlow,
		title:   "Redis slow log",
		why:     "It lists the slowest commands Redis ran, with times and durations. The tool drops keys and values.",
		how:     "Run redis-cli SLOWLOG GET and save the output. Import the text or JSON file.",
		applies: lacks(isRedis, redisSlow),
	},
	{
		id: "redis-log", weight: wRedis + 3, importer: redisLog,
		title:   "Redis server log",
		why:     "It records restarts, saves to disk and warnings, with times.",
		how:     "Copy the Redis log file for the same days. Import the text file.",
		applies: lacks(isRedis, redisLog),
	},
	{
		id: "mongo-serverstatus", weight: wMongo, importer: mongoStatus,
		title:   "MongoDB serverStatus",
		why:     "It shows connections, queues, cache use and operation counts at each sample.",
		how:     "Run db.serverStatus() on a timer and save one JSON line per sample. Import the JSON file.",
		applies: lacks(isMongo, mongoStatus),
	},
	{
		id: "mongo-profile", weight: wMongo + 1, importer: mongoProf,
		title:   "MongoDB profiler collection",
		why:     "It lists the slow operations the database recorded, with times and durations. The tool drops filters and names.",
		how:     "Run mongoexport on the system.profile collection. Import the JSON file.",
		applies: lacks(isMongo, mongoProf),
	},
	{
		id: "mongod-log", weight: wMongo + 2, importer: mongodJSON,
		title:   "MongoDB server log",
		why:     "It records slow operations and server events, with times.",
		how:     "Copy the mongod.log file. It must be in the JSON format of MongoDB 4.4 or later. Import the log file.",
		applies: lacks(isMongo, mongodJSON),
	},
	{
		id: "mongostat", weight: wMongo + 3, importer: mongostat,
		title:   "mongostat output",
		why:     "It shows each second: the operations, the queues and the connections.",
		how:     "Run mongostat with the --json option and save the output. Import the JSON file.",
		applies: lacks(isMongo, mongostat),
	},
}
