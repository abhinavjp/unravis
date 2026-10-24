package applog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Synthetic StackExchange.Redis timeout in the documented field order. The
// key carries the sentinel and a decoy "qs" field; neither may be read.
const redisMsg = "Timeout performing GET (5000ms), next: GET k-" + sentinel + ", qs: 999, inst: 0, qu: 0, qs: 41, " +
	"aw: False, rs: ReadAsync, ws: Idle, in: 0, serverEndpoint: 192.0.2.10:6379, mc: 1/1/0, " +
	"mgr: 10 of 10 available, clientName: web-a, IOCP: (Busy=0,Free=1000,Min=8,Max=1000), " +
	"WORKER: (Busy=63,Free=32704,Min=8,Max=32767), v: 2.2.4.27433"

func TestRedisTimeoutCaptures(t *testing.T) {
	fam, num := Classify("StackExchange.Redis", 0, redisMsg, "")
	want := map[string]float64{AttrTimeoutMs: 5000, AttrQs: 41, AttrQu: 0, AttrIn: 0, AttrWorkerBusy: 63,
		AttrWorkerMin: 8, AttrIOCPBusy: 0, AttrIOCPMin: 8}
	if fam != FamilyRedisTimeout || !reflect.DeepEqual(num, want) {
		t.Fatalf("Classify = %q %v, want redis-timeout %v", fam, num, want)
	}
	// 2.x "awaiting response" form.
	fam, num = Classify("", 0, "Timeout awaiting response (outbound=0KiB, inbound=0KiB, 5600ms elapsed, timeout is 5000ms), "+
		"command=GET, next: GET k1, inst: 0, qu: 0, qs: 2, in: 0", "")
	if fam != FamilyRedisTimeout || num[AttrTimeoutMs] != 5000 || num[AttrQs] != 2 {
		t.Errorf("awaiting response = %q %v", fam, num)
	}

	res := mustRun(t, testOpts(), []Record{rec("WARN", "StackExchange.Redis", redisMsg)})
	le := byKind(res.evs, KindLogEntry)
	if len(le) != 1 {
		t.Fatalf("log-entry events = %d", len(le))
	}
	wantAttrs := map[string]string{AttrLevel: "WARN", AttrLogger: "StackExchange.Redis", AttrFamily: FamilyRedisTimeout,
		AttrTimeoutMs: "5000", AttrQs: "41", AttrQu: "0", AttrIn: "0", AttrWorkerBusy: "63", AttrWorkerMin: "8",
		AttrIOCPBusy: "0", AttrIOCPMin: "8"}
	if !reflect.DeepEqual(le[0].Attrs, wantAttrs) {
		t.Errorf("attrs = %v\nwant    %v", le[0].Attrs, wantAttrs)
	}
	if le[0].Severity != "warning" {
		t.Errorf("severity = %q", le[0].Severity)
	}
}

func TestFamilyTable(t *testing.T) {
	cases := []struct {
		name, cat string
		eid       int
		msg, exc  string
		fam       string
		num       map[string]float64
	}{
		{"redis no connection", "", 0, "No connection is available to service this operation: GET k1", "", FamilyRedisConnection, nil},
		{"redis connect", "", 0, "It was not possible to connect to the redis server(s). ConnectTimeout", "", FamilyRedisConnection, nil},
		{"sql pool", "", 0, "Timeout expired.  The timeout period elapsed prior to obtaining a connection from the pool.  " +
			"This may have occurred because all pooled connections were in use and max pool size was reached.", "", FamilySQLPoolTimeout, nil},
		{"sql exec", "", 0, "Execution Timeout Expired.  The timeout period elapsed prior to completion of the operation.", "",
			FamilySQLExecTimeout, nil},
		{"mongo selection", "", 0, "A timeout occurred after 30000ms selecting a server using CompositeServerSelector.", "",
			FamilyMongoSelectionTimeout, map[string]float64{AttrTimeoutMs: 30000}},
		{"mongo pool", "", 0, "Timed out waiting for a connection after 120000ms.", "", FamilyMongoPoolWait, nil},
		{"http client", "", 0, "The request was canceled due to the configured HttpClient.Timeout of 100 seconds elapsing.", "",
			FamilyHTTPClientTimeout, map[string]float64{AttrTimeoutMs: 100000}},
		{"heartbeat event 22", catKestrel, 22, `As of "10/25/2001 01:02:31 +00:00", the heartbeat has been running for ` +
			`"00:00:01.2345678" which is longer than "00:00:01".`, "", FamilyHeartbeatLate, map[string]float64{NumDuration: 1234.5678}},
		{"heartbeat by sentence", "", 0, `As of "10/25/2001 01:02:31 +00:00", the heartbeat has been running for "1.00:00:02".`, "",
			FamilyHeartbeatLate, map[string]float64{NumDuration: 86402000}},
		{"request finished", catHostingDiagnostics, 2, "Request finished HTTP/1.1 GET http://web-a.example.test/api/orders?id=" +
			sentinel + " - 200 512 application/json 1834.2000ms", "", FamilyRequestFinished,
			map[string]float64{NumMethod: 0, AttrStatus: 200, NumDuration: 1834.2}},
		{"request finished old", catHostingDiagnostics, 2, "Request finished in 1834.2ms 200 application/json", "",
			FamilyRequestFinished, map[string]float64{AttrStatus: 200, NumDuration: 1834.2}},
		{"serilog request", catSerilogRequests, 0, "HTTP POST /api/orders responded 500 in 2500.1000 ms", "",
			FamilyRequestFinished, map[string]float64{NumMethod: 1, AttrStatus: 500, NumDuration: 2500.1}},
		{"serilog request other logger", "", 0, "HTTP PATCH /a responded 204 in 12 ms", "",
			FamilyRequestFinished, map[string]float64{NumMethod: 6, AttrStatus: 204, NumDuration: 12}},
		{"app started", catLifetime, 0, "Application started. Press Ctrl+C to shut down.", "", FamilyAppStart, nil},
		{"now listening", catLifetime, 14, "Now listening on: http://[::]:80", "", FamilyAppStart, nil},
		{"app stopping", catLifetime, 0, "Application is shutting down...", "", FamilyAppStop, nil},
		{"lifetime other", catLifetime, 0, "Hosting environment: Production", "", FamilyOther, nil},
		{"oom", "", 0, "Unhandled", "System.OutOfMemoryException: Exception of type 'System.OutOfMemoryException' was thrown.\n" +
			"   at Synthetic.Api.Run()", FamilyOutOfMemory, nil},
		{"thread abort", "", 0, "", "System.Threading.ThreadAbortException: Thread was being aborted.", FamilyThreadAbort, nil},
		{"task canceled", "", 0, "", "System.Threading.Tasks.TaskCanceledException: A task was canceled.", FamilyTaskCanceled, nil},
		{"operation canceled", "", 0, "", "System.OperationCanceledException: The operation was canceled.", FamilyTaskCanceled, nil},
		{"redis exception message", "", 0, "Cache read failed", "StackExchange.Redis.RedisTimeoutException: Timeout performing " +
			"GET (5000ms), inst: 0, qu: 0, qs: 3, in: 0\n   at Synthetic.Api.Cache.Get()", FamilyRedisTimeout,
			map[string]float64{AttrTimeoutMs: 5000, AttrQs: 3, AttrQu: 0, AttrIn: 0}},
		{"redis exception type", "", 0, "", "StackExchange.Redis.RedisConnectionException: SENTINEL-7f3a", FamilyRedisConnection, nil},
		{"inner sql pool", "", 0, "Query failed", "System.InvalidOperationException: wrapper ---> System.InvalidOperationException: " +
			"Timeout expired.  The timeout period elapsed prior to obtaining a connection from the pool.", FamilySQLPoolTimeout, nil},
		{"mongo paused", "", 0, "", "MongoDB.Driver.MongoConnectionPoolPausedException: paused", FamilyMongoPoolWait, nil},
		{"other", "Synthetic.Api.Orders", 0, "Order " + sentinel + " placed", "", FamilyOther, nil},
		{"not anchored", "", 0, "user typed Timeout performing GET (5000ms)", "", FamilyOther, nil},
		{"empty", "", 0, "", "", FamilyOther, nil},
	}
	for _, c := range cases {
		fam, num := Classify(c.cat, c.eid, c.msg, c.exc)
		if fam != c.fam {
			t.Errorf("%s: family = %q, want %q", c.name, fam, c.fam)
			continue
		}
		if len(num) == 0 && len(c.num) == 0 {
			continue
		}
		if !reflect.DeepEqual(num, c.num) {
			t.Errorf("%s: num = %v, want %v", c.name, num, c.num)
		}
	}
}

func TestExceptionTypes(t *testing.T) {
	cases := []struct {
		exc  string
		want []string
	}{
		{"System.InvalidOperationException: outer ---> Synthetic.Api.QuotaException: " + sentinel +
			"\n   at Synthetic.Api.Run()\n   --- End of inner exception stack trace ---", []string{"System.InvalidOperationException", AppException}},
		{"Synthetic.Api.QuotaException: over quota", []string{AppException}},
		{"System.Net.Sockets.SocketException (10054): An existing connection was forcibly closed by the remote host.",
			[]string{"System.Net.Sockets.SocketException"}},
		{"Unhandled exception. System.AggregateException: One or more errors occurred. (t) ---> System.TimeoutException: t",
			[]string{"System.AggregateException", "System.TimeoutException"}},
		{"System.Xml.XmlException: framework type off the curated list", []string{AppException}},
		{"System.Exception: a\n ---> System.InvalidOperationException: b\n ---> System.TimeoutException: c",
			[]string{"System.Exception", "System.TimeoutException"}},
		{"System.TimeoutException: a ---> System.TimeoutException: b", []string{"System.TimeoutException"}},
		{"System.TimeoutException", []string{"System.TimeoutException"}},
		{"\n   at Synthetic.Api.Run()\nSystem.IO.IOException: late outer line", []string{"System.IO.IOException"}},
		{"", nil},
		{"just text " + sentinel, nil},
		{"Exception", []string{AppException}},
		{".Exception: x", nil},
	}
	for _, c := range cases {
		if got := ExceptionTypes(c.exc); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExceptionTypes(%q) = %v, want %v", c.exc, got, c.want)
		}
	}
}

// Every curated type matches the §2.2 rule and comes out of the case export
// unchanged, so storing it never makes the export drop the attribute.
func TestCuratedExceptionsSurviveExport(t *testing.T) {
	line := int64(1)
	for typ := range curatedExceptions {
		if !exceptionRule.MatchString(typ) {
			t.Errorf("curated %q does not match the framework rule", typ)
		}
		v := typ + "," + AppException
		if got := exportAttrs(t, attrEvent(line, map[string]string{AttrException: v}))[AttrException]; got != v {
			t.Errorf("export of %q = %q", v, got)
		}
		line++
	}
	if got := exportAttrs(t, attrEvent(line, map[string]string{AttrException: "System.Xml.XmlException"}))[AttrException]; got != removedVal {
		t.Errorf("uncurated type exported as %q; the curated list may have drifted", got)
	}
}

// The logger rule is TestW2FExportValueRules' rule (internal/evidence/
// w2f_vocab_test.go): its kept and relabelled names are checked here against
// LoggerLabel, and every name is cross-checked against the case export.
func TestLoggerRuleMatchesExport(t *testing.T) {
	kept := []string{"StackExchange.Redis", "Microsoft.AspNetCore.Hosting", "StackExchange.Redis.ConnectionMultiplexer",
		"ioredis", "System", "redis", "Serilog.AspNetCore.RequestLoggingMiddleware", "Microsoft.Hosting.Lifetime",
		"MongoDB.Driver", "log4net", "Polly"}
	relabel := []string{"Synthetic.Api.CacheClient", "Microsoft.AspNetCore.Hosting.Diagnostics", "SystemX.Foo",
		"Microsoft." + sentinel, "redis-a", "Synthetic.Api.OrdersController", "Microsoft.AspNetCore.Server.Kestrel",
		"StackExchange.Redis.A.B", "microsoft.extensions", "Microsoft." + strings.Repeat("a", 65)}
	line := int64(1)
	for _, v := range kept {
		got, err := LoggerLabel(v, hashPS)
		if err != nil || got != v {
			t.Errorf("LoggerLabel(%q) = %q, %v; want kept", v, got, err)
		}
		if e := exportAttrs(t, attrEvent(line, map[string]string{AttrLogger: got}))[AttrLogger]; e != v {
			t.Errorf("export keeps %q as %q", v, e)
		}
		line++
	}
	for _, v := range relabel {
		got, err := LoggerLabel(v, hashPS)
		if err != nil || !loggerPseudonym.MatchString(got) {
			t.Errorf("LoggerLabel(%q) = %q, %v; want a logger- label", v, got, err)
		}
		// The raw name itself is not kept by the export either.
		if e := exportAttrs(t, attrEvent(line, map[string]string{AttrLogger: v}))[AttrLogger]; e == v || !strings.HasPrefix(e, "logger-") {
			t.Errorf("export of raw %q = %q, want a logger- label", v, e)
		}
		line++
	}
	if got, err := LoggerLabel("", nil); got != "" || err != nil {
		t.Errorf("empty category = %q, %v", got, err)
	}
	if got, err := LoggerLabel("System", nil); got != "System" || err != nil {
		t.Errorf("framework logger with no pseudonymiser = %q, %v", got, err)
	}
	for name, ps := range map[string]func(string, string) string{
		"nil":    nil,
		"empty":  func(string, string) string { return "" },
		"raw":    func(_, v string) string { return v },
		"shape":  func(string, string) string { return "logger-" + sentinel },
		"prefix": func(string, string) string { return "user-0a1b2c3d" },
	} {
		if got, err := LoggerLabel("Synthetic.Api.Orders", ps); !errors.Is(err, ErrPseudonym) || got != "" {
			t.Errorf("%s pseudonymiser: %q, %v; want ErrPseudonym", name, got, err)
		}
	}
}

func TestLevels(t *testing.T) {
	// The export's appLogLevels spellings (internal/evidence/caseexport_enums.go).
	spellings := []string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL", "TRACE", "error", "warn", "info", "http", "verbose",
		"debug", "silly", "VRB", "DBG", "INF", "WRN", "ERR", "FTL", "trce", "dbug", "fail", "crit", "Verbose", "Debug",
		"Information", "Warning", "Error", "Fatal", "Trace", "Critical", "None", "10", "20", "30", "40", "50", "60"}
	for i, v := range spellings {
		stored, _, ok := Level(" " + v + " ")
		if !ok || stored != v {
			t.Errorf("Level(%q) = %q, %v", v, stored, ok)
			continue
		}
		e := exportAttrs(t, attrEvent(int64(i+1), map[string]string{AttrLevel: stored}))[AttrLevel]
		if e == "" || e == removedVal || !strings.EqualFold(e, v) {
			t.Errorf("export of level %q = %q", v, e)
		}
	}
	if len(levelSeverity) != 29 {
		t.Errorf("levelSeverity has %d spellings; the export lists 29 distinct ones", len(levelSeverity))
	}
	for v, sev := range map[string]string{"INF": "info", "Warning": "warning", "fail": "error", "crit": "critical",
		"60": "critical", "DEBUG": "", "http": ""} {
		if _, got, _ := Level(v); got != sev {
			t.Errorf("severity of %q = %q, want %q", v, got, sev)
		}
	}
	for _, v := range []string{"NOTICE", sentinel, "", "70"} {
		if s, sev, ok := Level(v); ok || s != "" || sev != "" {
			t.Errorf("Level(%q) = %q %q %v; want refused", v, s, sev, ok)
		}
	}
}

func TestHeartbeatAsOf(t *testing.T) {
	got, ok := HeartbeatAsOf(`As of "10/25/2001 01:02:31 +02:00", the heartbeat has been running for "00:00:01.5".`)
	if want := t0.Add(28 * 1e9).Add(-2 * 3600 * 1e9); !ok || !got.Equal(want) || got.Location().String() != "UTC" {
		t.Errorf("HeartbeatAsOf = %v %v, want %v", got, ok, want)
	}
	for _, m := range []string{"", "heartbeat", `As of "13/45/2001 01:02:31 +00:00"`, `As of "10/25/2001 01:02:31`} {
		if _, ok := HeartbeatAsOf(m); ok {
			t.Errorf("HeartbeatAsOf(%q) accepted", m)
		}
	}
}

func TestMethodNum(t *testing.T) {
	for i, m := range Methods {
		v, ok := MethodNum(m)
		if !ok || v != float64(i) {
			t.Errorf("MethodNum(%q) = %v %v", m, v, ok)
		}
		if back, ok := methodName(v); !ok || back != m {
			t.Errorf("methodName(%v) = %q", v, back)
		}
	}
	for _, v := range []float64{-1, 0.5, float64(len(Methods))} {
		if _, ok := methodName(v); ok {
			t.Errorf("methodName(%v) accepted", v)
		}
	}
	if _, ok := MethodNum("get"); ok {
		t.Error("lower-case method accepted")
	}
}
