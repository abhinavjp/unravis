package applog

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Keys of the number map Classify returns besides the attribute names
// (AttrTimeoutMs, AttrQs, ...). The same keys may be set by a framer in
// Record.Num from structured properties (plan §3 W2-c, W2-d).
const (
	// NumDuration is a duration in milliseconds: the request time of
	// request-finished or the heartbeat time of heartbeat-late.
	NumDuration = "duration-ms"
	// NumElapsedMs and NumElapsed are the structured request-time properties
	// (MEL ElapsedMilliseconds, Serilog Elapsed), both in milliseconds.
	NumElapsedMs = "ElapsedMilliseconds"
	NumElapsed   = "Elapsed"
	// NumStatus is the structured status code property (StatusCode).
	NumStatus = "StatusCode"
	// NumMethod holds an HTTP method as its index in Methods (MethodNum);
	// Record.Num has no room for text.
	NumMethod = "method"
)

// Methods is the fixed HTTP method list (the export's httpMethods). A method
// travels in a number map as its index here.
var Methods = []string{"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH", "TRACE", "CONNECT", "PROPFIND",
	"PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "SEARCH", "REPORT"}

// MethodNum returns the index of method m in Methods (exact, upper case).
func MethodNum(m string) (float64, bool) {
	for i, v := range Methods {
		if v == m {
			return float64(i), true
		}
	}
	return 0, false
}

// methodName is the inverse of MethodNum.
func methodName(v float64) (string, bool) {
	i := int(v)
	if float64(i) != v || i < 0 || i >= len(Methods) {
		return "", false
	}
	return Methods[i], true
}

// Category and event ids the family table keys on.
const (
	catHostingDiagnostics = "Microsoft.AspNetCore.Hosting.Diagnostics"
	catSerilogRequests    = "Serilog.AspNetCore.RequestLoggingMiddleware"
	catKestrel            = "Microsoft.AspNetCore.Server.Kestrel"
	catLifetime           = "Microsoft.Hosting.Lifetime"
	eventRequestFinished  = 2
	eventHeartbeatSlow    = 22 // (verify) KestrelTrace HeartbeatSlow
)

// The family table (plan §2.3). Every pattern is RE2 (linear time), anchored
// on a fixed sentence start where the source format allows it, and reads only
// numbers. Sources, all public:
//
//   - StackExchange.Redis timeouts:
//     https://stackexchange.github.io/StackExchange.Redis/Timeouts
//     ("Timeout performing GET (5000ms), ... qu, qs, in, IOCP: (Busy=,Min=),
//     WORKER: (Busy=,Min=)"; 2.x also "Timeout awaiting response (...,
//     timeout is 5000ms)"). The field set varies by version; missing fields
//     are simply absent. Key, next, serverEndpoint and every other field are
//     never read.
//   - StackExchange.Redis connection failures ("No connection is
//     active/available", "It was not possible to connect to the redis
//     server(s)"): https://stackexchange.github.io/StackExchange.Redis/Configuration
//   - SqlClient pool and command timeouts ("Timeout expired.  The timeout
//     period elapsed prior to obtaining a connection from the pool",
//     "Execution Timeout Expired"):
//     https://learn.microsoft.com/en-us/sql/connect/ado-net/sql-server-connection-pooling
//   - MongoDB C# driver ("A timeout occurred after 30000ms selecting a server",
//     "Timed out waiting for a connection"):
//     https://www.mongodb.com/docs/drivers/csharp/current/faq/
//   - HttpClient ("The request was canceled due to the configured
//     HttpClient.Timeout of 100 seconds elapsing"):
//     https://learn.microsoft.com/en-us/dotnet/api/system.net.http.httpclient.timeout
//   - Kestrel heartbeat (event 22, verify; "As of "<time>", the heartbeat has
//     been running for "<TimeSpan>" ..."):
//     https://github.com/dotnet/aspnetcore/blob/main/src/Servers/Kestrel/Core/src/Internal/Infrastructure/KestrelTrace.General.cs
//   - Hosting request finished (event 2, "Request finished HTTP/1.1 GET <url> -
//     200 512 application/json 1834.2000ms"; before .NET 6 "Request finished in
//     1834.2ms 200 application/json"):
//     https://github.com/dotnet/aspnetcore/blob/main/src/Hosting/Hosting/src/Internal/HostingRequestFinishedLog.cs
//   - Serilog request logging ("HTTP GET <path> responded 200 in 1834.2000 ms"):
//     https://github.com/serilog/serilog-aspnetcore
//   - Host lifetime ("Application started.", "Now listening on:",
//     "Application is shutting down..."):
//     https://github.com/dotnet/runtime/blob/main/src/libraries/Microsoft.Extensions.Hosting/src/Internal/ConsoleLifetime.cs
var (
	reRedisTimeout    = regexp.MustCompile(`^Timeout (?:performing|awaiting response)`)
	reRedisTimeoutMs  = regexp.MustCompile(`^Timeout performing [A-Za-z]{1,32} \((\d{1,9})ms\)`)
	reRedisTimeoutIs  = regexp.MustCompile(`, timeout is (\d{1,9})ms\)`)
	reRedisQs         = regexp.MustCompile(`(?:^|[ ,])qs: (\d{1,12})(?:,|$)`)
	reRedisQu         = regexp.MustCompile(`(?:^|[ ,])qu: (\d{1,12})(?:,|$)`)
	reRedisIn         = regexp.MustCompile(`(?:^|[ ,])in: (\d{1,12})(?:,|$)`)
	reRedisIOCP       = regexp.MustCompile(`IOCP: \(Busy=(\d{1,12}),Free=\d{1,12},Min=(\d{1,12}),Max=\d{1,12}\)`)
	reRedisWorker     = regexp.MustCompile(`WORKER: \(Busy=(\d{1,12}),Free=\d{1,12},Min=(\d{1,12}),Max=\d{1,12}\)`)
	reRedisConnection = regexp.MustCompile(`^(?:No connection is (?:active|available)|It was not possible to connect to the redis server)`)
	reSQLPool         = regexp.MustCompile(`^Timeout expired\.\s+The timeout period elapsed prior to obtaining a connection from the pool`)
	reSQLExec         = regexp.MustCompile(`^Execution Timeout Expired`)
	reMongoSelection  = regexp.MustCompile(`^A timeout occurred after (\d{1,9})ms selecting a server`)
	reMongoPoolWait   = regexp.MustCompile(`^Timed out waiting for a connection`)
	reHTTPClient      = regexp.MustCompile(`HttpClient\.Timeout of (\d{1,9}(?:\.\d{1,6})?) seconds elapsing`)
	reHeartbeat       = regexp.MustCompile(`the heartbeat has been running for "((?:\d{1,8}\.)?\d{1,2}:\d{2}:\d{2}(?:\.\d{1,7})?)"`)
	reRequestFinished = regexp.MustCompile(`^Request finished (?:HTTP/[0-9.]{1,5} )?([A-Z]{3,9}) \S+ - (\d{3}) .*?(\d{1,12}(?:\.\d{1,7})?)ms$`)
	reRequestOld      = regexp.MustCompile(`^Request finished in (\d{1,12}(?:\.\d{1,7})?)ms (\d{3})(?: |$)`)
	reSerilogRequest  = regexp.MustCompile(`^HTTP ([A-Z]{3,9}) \S+ responded (\d{3}) in (\d{1,12}(?:\.\d{1,7})?) ms$`)
	reAppStarted      = regexp.MustCompile(`^(?:Application started|Now listening on)`)
	reAppStopping     = regexp.MustCompile(`^Application is shutting down`)
)

// Exception types that name a family by themselves (plan §2.3).
var excFamily = map[string]string{
	"StackExchange.Redis.RedisTimeoutException":         FamilyRedisTimeout,
	"StackExchange.Redis.RedisConnectionException":      FamilyRedisConnection,
	"MongoDB.Driver.MongoConnectionPoolPausedException": FamilyMongoPoolWait,
	"System.OutOfMemoryException":                       FamilyOutOfMemory,
	"System.Threading.ThreadAbortException":             FamilyThreadAbort,
	"System.Threading.Tasks.TaskCanceledException":      FamilyTaskCanceled,
	"System.OperationCanceledException":                 FamilyTaskCanceled,
}

// classify is Classify's body. Order: category and event id rules, then the
// message sentence, then the message of each exception in the text, then the
// exception types. The first match wins.
func classify(category string, eventID int, msg, exc string) (string, map[string]float64) {
	msg = strings.TrimSpace(msg)
	switch category {
	case catHostingDiagnostics:
		if eventID == eventRequestFinished || strings.HasPrefix(msg, "Request finished ") {
			return FamilyRequestFinished, requestNums(msg)
		}
	case catSerilogRequests:
		return FamilyRequestFinished, requestNums(msg)
	case catKestrel:
		if eventID == eventHeartbeatSlow {
			return FamilyHeartbeatLate, heartbeatNums(msg)
		}
	case catLifetime:
		if reAppStarted.MatchString(msg) {
			return FamilyAppStart, nil
		}
		if reAppStopping.MatchString(msg) {
			return FamilyAppStop, nil
		}
	}
	if reSerilogRequest.MatchString(msg) {
		return FamilyRequestFinished, requestNums(msg)
	}
	if f, num := classifyMessage(msg); f != "" {
		return f, num
	}
	segs := scanExceptions(exc)
	for _, s := range segs {
		if f, num := classifyMessage(s.msg); f != "" {
			return f, num
		}
	}
	for _, s := range segs {
		if f, ok := excFamily[s.typ]; ok {
			return f, nil
		}
	}
	return FamilyOther, nil
}

// classifyMessage matches one message (a log message or the message of one
// exception) against the sentence rows of the table.
func classifyMessage(m string) (string, map[string]float64) {
	if m == "" {
		return "", nil
	}
	switch {
	case reRedisTimeout.MatchString(m):
		return FamilyRedisTimeout, redisNums(m)
	case reRedisConnection.MatchString(m):
		return FamilyRedisConnection, nil
	case reSQLPool.MatchString(m):
		return FamilySQLPoolTimeout, nil
	case reSQLExec.MatchString(m):
		return FamilySQLExecTimeout, nil
	case reMongoSelection.MatchString(m):
		num := map[string]float64{}
		putNum(num, AttrTimeoutMs, reMongoSelection.FindStringSubmatch(m)[1], 1)
		return FamilyMongoSelectionTimeout, num
	case reMongoPoolWait.MatchString(m):
		return FamilyMongoPoolWait, nil
	case reHTTPClient.MatchString(m):
		num := map[string]float64{}
		putNum(num, AttrTimeoutMs, reHTTPClient.FindStringSubmatch(m)[1], 1000)
		return FamilyHTTPClientTimeout, num
	case reHeartbeat.MatchString(m):
		return FamilyHeartbeatLate, heartbeatNums(m)
	}
	return "", nil
}

// putNum stores the decimal text v times scale under key when it is a finite
// number that the export can print (at most 15 integer digits).
func putNum(num map[string]float64, key, v string, scale float64) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return
	}
	f *= scale
	if f >= 1e15 {
		return
	}
	num[key] = f
}

// lastSub returns the submatches of the last match of re in m, so a value
// inside an earlier field (a key, say) is overridden by the real field.
func lastSub(re *regexp.Regexp, m string) []string {
	all := re.FindAllStringSubmatch(m, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

// redisNums reads the numeric fields of a StackExchange.Redis timeout message.
func redisNums(m string) map[string]float64 {
	num := map[string]float64{}
	if s := reRedisTimeoutMs.FindStringSubmatch(m); s != nil {
		putNum(num, AttrTimeoutMs, s[1], 1)
	} else if s := lastSub(reRedisTimeoutIs, m); s != nil {
		putNum(num, AttrTimeoutMs, s[1], 1)
	}
	for _, f := range []struct {
		re  *regexp.Regexp
		key string
	}{{reRedisQs, AttrQs}, {reRedisQu, AttrQu}, {reRedisIn, AttrIn}} {
		if s := lastSub(f.re, m); s != nil {
			putNum(num, f.key, s[1], 1)
		}
	}
	if s := lastSub(reRedisIOCP, m); s != nil {
		putNum(num, AttrIOCPBusy, s[1], 1)
		putNum(num, AttrIOCPMin, s[2], 1)
	}
	if s := lastSub(reRedisWorker, m); s != nil {
		putNum(num, AttrWorkerBusy, s[1], 1)
		putNum(num, AttrWorkerMin, s[2], 1)
	}
	return num
}

// heartbeatNums reads the quoted TimeSpan of the Kestrel heartbeat sentence.
func heartbeatNums(m string) map[string]float64 {
	s := reHeartbeat.FindStringSubmatch(m)
	if s == nil {
		return nil
	}
	ms, ok := parseTimeSpan(s[1])
	if !ok {
		return nil
	}
	return map[string]float64{NumDuration: ms}
}

// parseTimeSpan reads a .NET TimeSpan "[d.]hh:mm:ss[.fffffff]" as milliseconds.
func parseTimeSpan(v string) (float64, bool) {
	days := 0.0
	clock := v
	if i := strings.IndexByte(v, ':'); i >= 0 {
		if j := strings.IndexByte(v[:i], '.'); j >= 0 {
			d, err := strconv.ParseFloat(v[:j], 64)
			if err != nil {
				return 0, false
			}
			days, clock = d, v[j+1:]
		}
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h > 23 || m > 59 || sec >= 60 {
		return 0, false
	}
	ms := ((days*24+float64(h))*60+float64(m))*60*1000 + sec*1000
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms >= 1e15 {
		return 0, false
	}
	return ms, true
}

// requestNums reads method, status and duration from the fixed request
// sentences of Hosting and Serilog. The path, host, query, content type and
// protocol are matched as \S+ or skipped and never kept.
func requestNums(m string) map[string]float64 {
	num := map[string]float64{}
	var method, status, dur string
	if s := reRequestFinished.FindStringSubmatch(m); s != nil {
		method, status, dur = s[1], s[2], s[3]
	} else if s := reRequestOld.FindStringSubmatch(m); s != nil {
		status, dur = s[2], s[1]
	} else if s := reSerilogRequest.FindStringSubmatch(m); s != nil {
		method, status, dur = s[1], s[2], s[3]
	}
	if i, ok := MethodNum(method); ok {
		num[NumMethod] = i
	}
	putStatus(num, status)
	if dur != "" {
		putNum(num, NumDuration, dur, 1)
	}
	return num
}

// putStatus stores an HTTP status code in 100..599.
func putStatus(num map[string]float64, v string) {
	n, err := strconv.Atoi(v)
	if err == nil && n >= 100 && n <= 599 {
		num[AttrStatus] = float64(n)
	}
}
