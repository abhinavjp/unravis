# Importer wave 2: app logs, runtime, Redis, Mongo and resource evidence

- **Date:** 2026-10-25. **Trunk:** `master`. **Card format, foundations, harness and §4 rules:** `2026-10-24-importer-wave-plan.md` (wave 1). Wave 1's §4 is pasted into every brief, with §2 below added.
- **Goal (user, 2026-10-25):** import every log that helps find the root cause of API slowness and outages. Root cause stays a ledger decision: importers and rules emit **Symptom** or **Correlation** only (wave 1 §0.6). The roadmap §6.6 wordings are used, and new wordings get twins in `wording.go` and `wordings.ts`.
- **All examples are synthetic.** Dates are in 2001. One synthetic stall runs through every fixture: **2001-10-25T01:02:31Z**, which is 14:02:31 local on `web-a` (Pacific/Auckland, +13:00 in October 2001). IPs are RFC 5737; hosts are `web-a`, `sql-a`, `redis-a`, `mongo-a` and `*.example.test`; jobs are `job-alpha`; the sentinel is `SENTINEL-7f3a`.

## 0. Rulings this plan needs (none is granted by writing it)

| ID | Ruling | Why | Blocks |
|---|---|---|---|
| R-W2a | Reopen roadmap §7 **E1 (Redis)** for **owner-run exports only**: no live access and no credentials in the tool | E1 says "Deferred" | W2-h, W2-i |
| R-W2b | Add five §6.6 rows (§4.0 below) for metric trends, sample overlap, markers, episode shares and top-N | §6.6 is "the only permitted wording" for event questions; metric descriptions are a new situation | A2, U2 |
| R-W2c | §5.4 rule 2: the user's goal of 2026-10-25 counts as the incident question for these sources. Rules 3 and 4 still apply, so an importer unused after three incidents is retired | No importer is built without a question | all cards |
| R-W2d | Accept the **message-family** approach (§2.3): message text is classified in memory against a fixed table and then dropped; only the family name and allowlisted numbers are kept | Privacy-heavy design choice | W2-a and its dependents |

**Rulings (2026-10-25, delegated design authority, user goal of the same day): R-W2a, R-W2b, R-W2c and R-W2d are all ACCEPTED as written.** Redis stays owner-run exports only (no live connection, no credentials in the tool). Cost if wrong: retire an importer after three unused incidents (rule 3/4).

## 1. Gap table (verified against `master` on 2026-10-25)

| Source / format | Exists? (importer) | Wanted in wave 2 |
|---|---|---|
| IIS W3C | yes, `iis-w3c`: method, URI stem as `{id}` template, status, substatus, win32, port, site, pseudonymised user; IPs, query, UA, cookie and referer dropped | `sc-bytes` and `cs-bytes` as `bytes-out` and `bytes-in`, so "time-taken includes delivery" can be read against size (W2-g) |
| HTTP.sys HTTPERR | yes, `iis-httperr`: kind `httperr`, 30 `s-reason` values allowlisted, else "other" | nothing |
| WAS (System log) | yes, through `win-events` and `iis.ClassifyWAS`: recycles 5074–5080 and 5117 (`reason`), idle shutdown 5186 | worker-process failures and rapid-fail disable (W2-g) |
| Application log: crashes, hangs, ANCM, ASP.NET 1309 | rows come in as generic `win-event` (provider and id); Message and Properties are dropped | typed kinds plus an allowlisted faulting-app column (W2-g) |
| **PDH CSV (relog, typeperf, Export-Counter)** | yes, `win-counters`. Facts: **any object or counter is accepted** (there is no counter allowlist at import; the export keeps names from a fixed `counterNames` list and shows others as "[removed]"). **Instances are an allowlist:** Processor, Processor Information, Process, Process V2, Network Interface, `.NET CLR*`, LogicalDisk (drive letter or volume), PhysicalDisk, the fixed SQL categories, and `_Total`/`__Total__`. All other instances are dropped or numbered, so `ASP.NET Applications(<app>)` survives only as `__Total__` and `W3SVC_W3WP(<pid>_<pool>)` is dropped. **CPU:** `Processor(_Total)` gets `percent-host` with the gap "SQL attribution unknown"; Process and Process V2 `% Processor/User/Privileged Time` are divided by `LogicalCores`, else `percent-one-core` with a gap; legacy Process columns are ignored when V2 is present. **Every non-CPU counter (memory, handles, GC, queues) gets unit `""`.** typeperf CSV has the same `(PDH-CSV 4.0)` header, so typeperf is already covered | keep `W3SVC_W3WP` instances as pid only; add the missing counter names to the export list (`Process ID`, `Active Requests`, `Active Threads Count`, `Requests Rejected`, `Requests / Sec`); a **resource catalogue** that maps counters to metrics and units at read time (A1). No parser rewrite (W2-f) |
| .blg binary counters | refused (unsupported-format) | stays refused; the recipe relogs to CSV |
| Get-Process / tasklist snapshots | no | `win-procsnap` (W2-f) |
| log4net (PatternLayout, XmlLayoutSchemaLog4j, XmlLayout) | no | `log4net-text`, `log4net-xml` (W2-b) |
| Serilog CLEF and text | no | `serilog-clef`, `serilog-text` (W2-c) |
| MEL console and JSON; ANCM stdout | no | `mel-console`, `mel-json`, `ancm-stdout` (W2-d) |
| dotnet-counters CSV and JSON (EventCounters and Meters, .NET 6–10) | no | `dotnet-counters` (W2-e) |
| EventPipe `.nettrace`, ETW `.etl`, dumps | no | **out of scope**: binary, and they can hold payloads. Refused with "use dotnet-counters" |
| Redis SLOWLOG, LATENCY HISTORY, INFO | no (§7 E1 deferred) | `redis-slowlog`, `redis-latency`, `redis-info` (W2-h) |
| redis.log | no | `redis-log` (W2-i) |
| Node Redis proxy logs (pino, winston JSON) | no | `node-jsonl` (W2-j, **conditional** on the owner's field-name list) |
| mongod.log | yes, `mongod-json`: kind `slow-op` only | start and stop markers (W2-l) |
| Mongo profiler, `$currentOp`, serverStatus, mongostat | no | `mongo-profile`, `mongo-currentop`, `mongo-serverstatus`, `mongostat-json` (W2-k) |
| FTDC `diagnostic.data` | no | out of scope (binary); refused with "use the serverStatus recipe" |
| IIS Failed Request Tracing (FREB XML), ANCM debug log | no | **deferred**: they hold URLs, headers and paths, and no question needs them yet |
| Linux host CPU and memory (if Redis or Mongo run on Linux) | no | `linux-sadf`, `linux-pidstat` (W2-m, **conditional** on the host OS) |
| Resource view, trend and overlap rules | none. The P8 timeline overlaps only events with a span > 0, so counter samples never qualify | A0–A2, U1–U3 |

## 2. Shared rules for every wave-2 card (add to the §4 paste)

### 2.1 Time

| Source | Rule | Refusal or gap |
|---|---|---|
| log4net `%date` / `%d{ISO8601}` / `%d{DATE}` | `RuleHostLocal`; the picked host's TZ is required; rows in a DST fold or gap are rejected, never guessed | no TZ: file refused (`tz-unlabelled`). `%utcdate` cannot be told apart from local, so the owner states `log-time=utc` (§4.3 U3), which gives `RuleUTCField` plus the gap "UTC per owner statement" |
| log4net `%d{yyyy-MM-ddTHH:mm:ss.fffzzz}`, Serilog `zzz`, MEL with an offset or `Z` | `RuleOffsetInField` / `RuleUTCField` | none |
| log4net `%timestamp` (ms since start), `%d{ABSOLUTE}`, Serilog console `HH:mm:ss`, MEL with no timestamp | none: the line has no date | file refused (`unsupported-format`, hint "configure a full date") |
| log4j XML `timestamp`, pino `time`, Redis epochs | Unix epoch, so `RuleUTCField` | none |
| ANCM stdout file name `stdout_<yyyyMMddHHmmss>_<pid>.log` | `RuleHostLocal` (**unverified**) | gap "file-name start time zone unverified" |
| dotnet-counters CSV | local, culture-formatted (**unverified**): `RuleHostLocal` plus the PDH date-order gap | no TZ: refused |
| redis.log | `RuleHostLocal` | the year-less Windows-port form needs an owner-stated `year`; a backwards month means year + 1, with a gap |
| Mongo `$date` with `Z`, or an ISO time with an offset | UTC / offset | a naive time rejects the row |
| mongostat `time` (HH:MM:SS only) | `RuleHostLocal` plus an owner-stated `date`; a midnight rollover advances the date | either missing: refused |
| Recipe sample lines (`# unravis-sample <ISO Z>`, `SampleUtc`) | `RuleUTCField` | missing: refused |

There is no skew correction between hosts. Joins use `Tolerance{Resolution: source resolution, Method: sample interval}` (§4.2), and the panel states "clock skew between hosts is not corrected".

### 2.2 Privacy at parse (client data: drop, or reduce to a fixed category plus counts)

- **Never stored:** message text, rendered messages, message templates (`@mt`, `{OriginalFormat}`), properties and scopes, stack frames, SQL and command text, Redis keys and values, Mongo filters and command bodies, URLs and queries, user and identity fields, thread names, NDC/MDC, file paths, IPs, connection strings, `TargetProcess`, client names in raw form.
- **Kept:** level, normalised severity, durations, sizes, status codes, counts, and allowlisted enums: Redis command names (the fixed command table), Mongo command names (reuse `mongoCommands`), HTTP methods, and Node errno codes (`ECONNRESET`, `ETIMEDOUT`, `ECONNREFUSED`, `EPIPE`, `ENOTFOUND`, `EAI_AGAIN`).
- **Logger or category:** kept to depth 3 when it starts with an allowlisted framework prefix (`System.`, `Microsoft.`, `MongoDB.`, `StackExchange.Redis.`, `Serilog.`, `log4net.`, `Hangfire.`, `Polly.`, `Grpc.`, `Azure.`, `Newtonsoft.`, `NHibernate.`, `ioredis`, `redis`). Anything else becomes `Pseudonymize("logger", full)`, a per-case `logger-xxxxxxxx`. Fail-closed: if `Pseudonymize` returns `""`, the file is refused.
- **Exception type:** the first exception line and each `--->` inner exception are matched against `^(System|Microsoft|MongoDB|StackExchange\.Redis|Newtonsoft|Npgsql|NHibernate|Hangfire|Polly|Grpc|Azure|log4net)(\.[A-Za-z0-9_]+)*\.[A-Za-z0-9_]*(Exception|Error)$`. A match is kept; anything else is `app-exception`. At most 2 types per entry (outer, innermost).
- **Process names:** kept only as an image name matching `^[A-Za-z0-9_.#-]{1,64}$` with `.exe` stripped, else `other`. Never a path or command line. The export pseudonymises them as `win-counters` instances already are.
- **Client name** (Redis `CLIENT SETNAME`): `Pseudonymize("client", v)`. **Redis client address:** dropped.
- **Unscrubbed columns quarantine the file** (header name only): `User Name`, `Window Title`, `CommandLine`, `Path`, `MainWindowTitle`, `UserName`, `Company`, `Description` (Get-Process and tasklist); `command`, `filter`, `client`, `effectiveUsers` are dropped quietly in Mongo JSON (nested and expected).

### 2.3 App-log core (W2-a): what every app-log framer emits

A framer (log4net, Serilog, MEL, ANCM, Node) turns lines into in-memory records `{time, level, category, message, exceptionText, eventId, numeric props}`. The core classifies and emits the events, then discards the record. The **family table** is fixed in code, and each row cites the public page its message format comes from:

| family | match (message start, category/event id, or exception type) | numeric captures (attrs) |
|---|---|---|
| `redis-timeout` | `^Timeout performing ` or `StackExchange.Redis.RedisTimeoutException` | `timeout-ms` from `(5000ms)`, `qs`, `qu`, `in`, `worker-busy`, `worker-min`, `iocp-busy`, `iocp-min`. `Key:`/`next:`/`serverEndpoint` are never read |
| `redis-connection` | `RedisConnectionException`, `^No connection is (active|available)` | none |
| `sql-pool-timeout` | `^Timeout expired.\s+The timeout period elapsed prior to obtaining a connection from the pool` | none |
| `sql-exec-timeout` | `^Execution Timeout Expired` | none |
| `mongo-selection-timeout` | `^A timeout occurred after (\d+)ms selecting a server` | `timeout-ms` |
| `mongo-pool-wait` | `MongoConnectionPoolPausedException`, `^Timed out waiting for a connection` | none |
| `http-client-timeout` | `HttpClient.Timeout of (\d+) seconds elapsing` | `timeout-ms` |
| `heartbeat-late` | category `Microsoft.AspNetCore.Server.Kestrel`, event 22 (verify) or `the heartbeat has been running for "(…)"` | duration from the quoted TimeSpan. The message's own `As of "<time> <offset>"` gives a time for console logs that have none |
| `request-finished` | `Microsoft.AspNetCore.Hosting.Diagnostics` event 2, or `Serilog.AspNetCore.RequestLoggingMiddleware` | duration from `ElapsedMilliseconds`/`Elapsed`, `status`, `method` |
| `app-start` / `app-stop` | `Microsoft.Hosting.Lifetime`: `^Application started`, `^Now listening on`, `^Application is shutting down` | none |
| `out-of-memory`, `thread-abort`, `task-canceled` | exception types `System.OutOfMemoryException`, `System.Threading.ThreadAbortException`, `System.Threading.Tasks.TaskCanceledException`/`OperationCanceledException` | none |

**Kinds:**
- `log-entry`: one per line at warning or above, or in a classified family; cap 1M per file, after which rows are counted only, with a gap.
- `log-rate`: per UTC minute × level × logger label × family, `Value` = count, unit `count`.
- `app-request`: `request-finished` lines with a duration of at least `app-request-min-ms` (default 1000, owner-settable), as the interval `[t − d, t]`.
- `heartbeat-late`: interval.
- `app-start`, `app-stop`: points.

**Attrs (exactly these 16, shared by all app-log importers):** `level`, `logger`, `exception`, `family`, `timeout-ms`, `qs`, `qu`, `in`, `worker-busy`, `worker-min`, `iocp-busy`, `iocp-min`, `status`, `method`, `command`, `error-code`.

**Levels:**
- log4net `DEBUG/INFO/WARN/ERROR/FATAL`
- Serilog `VRB/DBG/INF/WRN/ERR/FTL` (CLEF: no `@l` means Information)
- MEL `trce/dbug/info/warn/fail/crit`
- pino `10..60`, winston names

These map to severity `""/info/warning/error/critical`, with the original level kept in `level`.

### 2.4 Limits (refuse whole above any)

| Importer | Bytes | Rows / events | Line |
|---|---|---|---|
| log4net-text, serilog-text, mel-console | 2 GiB | 10M lines / 2M events | 64 KiB |
| log4net-xml, serilog-clef, mel-json, node-jsonl | 2 GiB | 10M / 2M events | 256 KiB |
| ancm-stdout | 256 MiB | 2M | 64 KiB |
| dotnet-counters | 512 MiB | 5M / 5M | 64 KiB |
| win-procsnap | 256 MiB | 5M / 10M | 64 KiB |
| redis-slowlog / redis-latency / redis-info / redis-log | 16 / 4 / 64 / 512 MiB | 200k / 100k / 1M / 5M | 64 KiB |
| mongo-profile / mongo-currentop / mongo-serverstatus / mongostat-json | 1 GiB / 256 MiB / 512 MiB / 256 MiB | 2M / 2M / 1M / 2M | 1 MiB / 1 MiB / 1 MiB / 64 KiB |
| linux-sadf / linux-pidstat | 256 MiB | 5M | 64 KiB |

### 2.5 Acceptance for every importer card

- Wave 1 `importertest.Run` passes, with a `FuzzParse` seeded from the fixtures and run for 30 s.
- The sentinel `SENTINEL-7f3a` is placed in every dropped field (message, key, user, filter, command line). It must never appear in an event, report, gap, error or log.
- No network: a failing `http.DefaultTransport` is swapped in.
- Every event passes `Validate` against the card's attrs, **and those attrs equal the W2-F export policy for that source** (the drift test).
- Detection margin: the card's fixtures score at least 10 points above every other registered importer (INT2 runs the full matrix).
- Every kind, attr, unit and gap sentence is a constant. Gap sentences pass `LintCausal`.

## 3. Cards

| ID | Branch | Model | Effort | Depends on | Parallel group |
|---|---|---|---|---|---|
| W2-F | `w2/f-foundation` | Opus | M | none | first, alone |
| W2-a | `w2/a-applog-core` | Opus | M | W2-F (stub) | G1 |
| W2-b | `w2/b-log4net` | Sonnet | M | W2-a API (stub) | G2 |
| W2-c | `w2/c-serilog` | Sonnet | M | W2-a API (stub) | G2 |
| W2-d | `w2/d-mel-ancm` | Sonnet | M | W2-a API (stub) | G2 |
| W2-e | `w2/e-dotnet-counters` | Sonnet | M | W2-F | G1 |
| W2-f | `w2/f-procsnap-counters` | Sonnet (Opus review) | M | W2-F | G1 |
| W2-g | `w2/g-winevents-iis` | Sonnet | S | W2-F | G1 |
| W2-h | `w2/h-redis-cli` | Sonnet | M | W2-F, R-W2a | G1 |
| W2-i | `w2/i-redis-log` | Sonnet | S | W2-F, R-W2a | G1 |
| W2-j | `w2/j-node-proxy` | Sonnet | M | W2-a, the owner's field list | G2, conditional |
| W2-k | `w2/k-mongo-exports` | Sonnet (Opus review) | M | W2-F | G1 |
| W2-l | `w2/l-mongod-markers` | Haiku | S | none | G1 |
| W2-m | `w2/m-linux` | Sonnet | S | W2-F, host OS confirmed | G1, conditional |
| S2 | `w2/s2-series-supplied` | Sonnet | M | W2-F | G1 |
| A1 | `w2/a1-resource-catalogue` | Opus | M | S2 (stub) | G2 |
| A2 | `w2/a2-rules` | Opus | L | A1, R-W2b | G3 |
| U1 | `w2/u1-resource-panel` | Sonnet | M | S2 contract, A1 contract, `wave/drill` merged | G3 |
| U2 | `w2/u2-rule-chips` | Sonnet | S | A2 contract | G4 |
| INT2 | `w2/int2` | Sonnet | S | all merged | last |

### W2-F: wave-2 foundation (Opus). It owns every shared file, so the other cards touch only their own package.

- **Files:**
  - `internal/evidence/caseexport.go`, `caseexport_enums.go`: the `exportPolicy`, `exportKinds` and `exportDropped` entries for **every source in §1** with the exact attrs in each card; `exportUnits` gains `bytes`, `percent`, `per-sec`, `ratio`; `counterNames` gains the W2-f names.
  - `internal/evidence/event.go`: the Unit doc.
  - `wording.go` with `web/src/lib/copy/wordings.ts`: the §4.0 templates, plus the twin test.
  - `internal/timeline/timeline.go`: `displayNames` and `eventPhrase` for the new sources and kinds.
  - `web/src/lib/cases.ts` `IMPORT_CAPS`, and `copy/importCopy.ts` `IMPORTER_LABELS` with one plain-language help line per importer.
- **Also lands:** the W2-a API as signatures with a no-op body (`internal/importer/applog/api.go`), so the G2 cards compile without a stub commit.
- **Tests:** drift test entries for not-yet-registered importers are skipped by name until INT2 registers them. The wording twins match. Copy lint passes.

### W2-a: app-log core (Opus)

- **Files:** `internal/importer/applog/{core,families,levels,exceptions,logger,rate}.go` with tests.
- **API:**
  - `type Record struct{ Time time.Time; Rule string; Level, Category string; EventID int; Message, Exception string; Num map[string]float64 }`
  - `func NewSink(src string, opts importer.Options, emit func(evidence.Event) error) *Sink`
  - `(*Sink).Add(r Record, line int64) error` and `(*Sink).Finish(rep *importer.Report) error`, which flushes `log-rate` and adds the gaps.
  - `func Classify(category string, eventID int, msg, exc string) (family string, num map[string]float64)`
  - `func ExceptionTypes(exc string) []string`
  - `func LoggerLabel(cat string, ps func(kind, v string) string) (string, error)`
- **Rules:** §2.2 and §2.3. `Message` and `Exception` are zeroed after `Add`. A test asserts no `Record` field reaches an event except through the family captures.
- **Tests:**
  - The `redis-timeout` message in §3 W2-b gives `qs=41`, `worker-busy=63`, `worker-min=8`, `iocp-busy=0`, `iocp-min=8`, `timeout-ms=5000`, and no key.
  - `Synthetic.Api.OrdersController` becomes `logger-xxxxxxxx`; `Microsoft.AspNetCore.Hosting.Diagnostics` is kept.
  - `Synthetic.Api.QuotaException` becomes `app-exception`.
  - 1M+1 warnings give 1M entries plus a gap, and the rate counts stay exact.
  - A DST-fold row is rejected.

### W2-b: log4net (text and XML)

- **Files:** `internal/importer/log4net/{pattern,text,xml}.go` with testdata.
- **Pattern:** the owner may state `pattern` (the `conversionPattern` value only, §5). Supported tokens are `%date/%d{…}`, `%utcdate`, `%thread/%t`, `%level/%p` with padding, `%logger/%c{n}`, `%ndc/%x`, `%property/%P`, `%message/%m`, `%newline/%n`, `%exception`; the token text `%username/%identity/%location/%file/%line/%method/%class/%w/%u/%l/%F/%L/%M/%C` is matched and dropped. With no stated pattern, the importer tries `%date [%thread] %-5level %logger - %message%newline`, then `%date [%thread] %-5level %logger [%ndc] - %message%newline` and `%date %-5level %logger - %message%newline`. Lines that match no header are continuation lines: they feed `Exception` and are never rejected.
- **Text fixture** (host web-a, local):
```
2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.OrdersController - Request handled for alice@example.test
2001-10-25 14:02:31,123 [17] WARN  StackExchange.Redis.ConnectionMultiplexer - Timeout performing GET (5000ms), next: GET session:SENTINEL-7f3a, inst: 0, qu: 0, qs: 41, aw: False, bw: SpinningDown, rs: ReadAsync, ws: Idle, in: 8192, serverEndpoint: redis-a.example.test:6379, mgr: 10 of 10 available, IOCP: (Busy=0,Free=1000,Min=8,Max=1000), WORKER: (Busy=63,Free=32704,Min=8,Max=32767), v: 2.6.122.38350
2001-10-25 14:02:31,400 [17] ERROR Synthetic.Api.OrdersController - Unhandled error SENTINEL-7f3a
System.TimeoutException: The operation has timed out. ---> System.Data.SqlClient.SqlException: Timeout expired.  The timeout period elapsed prior to obtaining a connection from the pool.
   at Synthetic.Api.OrdersController.Get(Int32 id) in C:\src\Synthetic\OrdersController.cs:line 42
2001-10-25 14:02:35,500 [5] FATAL Synthetic.Api.Global - Application_End
```
- **ISO8601 with offset variant:** `2001-10-25T14:02:31.123+13:00 [17] WARN  Synthetic.Api.CacheClient - slow cache call`
- **XmlLayoutSchemaLog4j fixture** (the fragment has no root; the decoder wraps it with `xmlns:log4j`, and a DOCTYPE refuses the file):
```
<log4j:event logger="Synthetic.Api.OrdersController" timestamp="1003971751400" level="ERROR" thread="17"><log4j:message>Unhandled error SENTINEL-7f3a</log4j:message><log4j:throwable>System.TimeoutException: The operation has timed out.</log4j:throwable><log4j:properties><log4j:data name="log4jmachinename" value="web-a"/><log4j:data name="log4net:UserName" value="EXAMPLE\alice"/></log4j:properties></log4j:event>
<log4j:event logger="StackExchange.Redis.ConnectionMultiplexer" timestamp="1003971751123" level="WARN" thread="17"><log4j:message>Timeout performing GET (5000ms), qs: 41, in: 8192, IOCP: (Busy=0,Free=1000,Min=8,Max=1000), WORKER: (Busy=63,Free=32704,Min=8,Max=32767)</log4j:message></log4j:event>
```
- **Also accepted:** the native `XmlLayout` (`<log4net:event timestamp="2001-10-25T14:02:31.123+13:00" …>`, `RuleOffsetInField`). `log4jmachinename` and `log4net:HostName` feed `Observe` and the host mapping only.
- **Negatives:**
  - The `%timestamp` pattern is refused.
  - A missing TZ is refused.
  - An external entity or DOCTYPE is refused.
  - A 300 KiB element is a line-limit breach.
  - A pattern containing `%message` twice is refused (`unsupported-format`).
  - The sentinel never appears in output.

### W2-c: Serilog (CLEF and text)

- **Files:** `internal/importer/serilog/{clef,text}.go`.
- **CLEF fixture:**
```
{"@t":"2001-10-25T01:02:30.9500000Z","@mt":"HTTP {RequestMethod} {RequestPath} responded {StatusCode} in {Elapsed:0.0000} ms","RequestMethod":"GET","RequestPath":"/orders/SENTINEL-7f3a","StatusCode":200,"Elapsed":1834.2,"SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}
{"@t":"2001-10-25T01:02:31.1230000Z","@mt":"Cache call slow for {UserId}","@l":"Warning","UserId":"alice","SourceContext":"Synthetic.Api.CacheClient"}
{"@t":"2001-10-25T01:02:31.4000000Z","@mt":"Unhandled exception","@l":"Error","@x":"System.TimeoutException: The operation has timed out.\r\n   at Synthetic.Api.CacheClient.GetAsync(String key)","SourceContext":"Microsoft.AspNetCore.Diagnostics.ExceptionHandlerMiddleware"}
{"@t":"2001-10-25T01:05:00.0000000+13:00","@m":"Application started. Press Ctrl+C to shut down.","SourceContext":"Microsoft.Hosting.Lifetime"}
```
  `@m`, `@mt`, `@r`, `@i` and all non-allowlisted properties are dropped. Numbers are read only for the `request-finished` family (`Elapsed`, `StatusCode`, `RequestMethod`).
- **Text fixture** (default file template `{Timestamp:yyyy-MM-dd HH:mm:ss.fff zzz} [{Level:u3}] {Message:lj}{NewLine}{Exception}`):
```
2001-10-25 14:02:30.950 +13:00 [INF] HTTP GET /orders/SENTINEL-7f3a responded 200 in 1834.2000 ms
2001-10-25 14:02:31.123 +13:00 [WRN] Cache call slow for "alice"
2001-10-25 14:02:31.400 +13:00 [ERR] Unhandled exception
System.TimeoutException: The operation has timed out.
   at Synthetic.Api.CacheClient.GetAsync(String key)
```
  In text, `request-finished` is matched only by the fixed Serilog request-logging sentence shape `^HTTP [A-Z]+ \S+ responded (\d{3}) in ([0-9.]+) ms$`; the path is never kept.
- **Negatives:** the console template (`[14:02:31 INF] …`) is refused. A CLEF line without `@t` is rejected. `@t` with no offset is rejected (`tz-unlabelled`).

### W2-d: Microsoft.Extensions.Logging console and JSON, ANCM stdout

- **Files:** `internal/importer/mel/{console,json,stdout}.go`.
- **Console** (simple formatter with `TimestampFormat` and `UseUtcTimestamp`; the multi-line form and `SingleLine` are both accepted):
```
2001-10-25T01:02:30.120Z info: Microsoft.Hosting.Lifetime[14]
      Now listening on: http://web-a.example.test:5000
2001-10-25T01:02:31.123Z warn: Microsoft.AspNetCore.Server.Kestrel[22]
      As of "10/25/2001 01:02:31 +00:00", the heartbeat has been running for "00:00:01.5000000" which is longer than "00:00:01". This could be caused by thread pool starvation.
2001-10-25T01:02:31.400Z fail: Microsoft.AspNetCore.Diagnostics.ExceptionHandlerMiddleware[1]
      An unhandled exception has occurred while executing the request.
      System.TimeoutException: The operation has timed out.
```
  The message is dropped. The Kestrel sentence feeds `heartbeat-late` with a duration of 1500 ms and no causal text.
- **JSON console** (one object per line; `Timestamp` exists only when configured, otherwise the file is refused):
```
{"Timestamp":"2001-10-25T01:02:31.123Z","EventId":2,"LogLevel":"Information","Category":"Microsoft.AspNetCore.Hosting.Diagnostics","Message":"Request finished HTTP/1.1 GET http://web-a.example.test/orders/SENTINEL-7f3a - 200 512 application/json 1834.2000ms","State":{"ElapsedMilliseconds":1834.2,"StatusCode":200,"Method":"GET","Path":"/orders/SENTINEL-7f3a","QueryString":"?user=alice"}}
{"Timestamp":"2001-10-25T01:02:31.400Z","EventId":1,"LogLevel":"Error","Category":"Microsoft.AspNetCore.Diagnostics.ExceptionHandlerMiddleware","Message":"An unhandled exception has occurred","Exception":"System.TimeoutException: The operation has timed out.","State":{}}
```
- **ANCM stdout:** the file name gives the session start and pid; the body is console text, usually without timestamps.
```
info: Microsoft.Hosting.Lifetime[0]
      Application started. Press Ctrl+C to shut down.
warn: Microsoft.AspNetCore.Server.Kestrel[22]
      As of "10/25/2001 01:02:31 +00:00", the heartbeat has been running for "00:00:01.5000000" which is longer than "00:00:01". This could be caused by thread pool starvation.
Unhandled exception. System.InvalidOperationException: Sequence contains no elements SENTINEL-7f3a
   at Synthetic.Api.Program.Main(String[] args)
```
  It emits one `app-session` point at the file-name time (attrs `exception`, `level` = `crit` when `Unhandled exception.` is seen), plus a `heartbeat-late` for each Kestrel line, timed from its own `As of`. Lines with no time are counted in the Report and never emitted.
- **Negatives:** console with no timestamp and no heartbeat lines is refused. A stdout file name that does not match the pattern is refused (`unsupported-format`).

### W2-e: dotnet-counters (CSV and JSON)

- **Files:** `internal/importer/dotnetcounters/`.
- **Step 1 (30 min):** pin the exact CSV and JSON shapes and the time format from the public dotnet-diagnostics source at a stated tag. Correct the fixtures below if they differ, and record the tag in a comment.
- **CSV:**
```
Timestamp,Provider,Counter Name,Counter Type,Mean/Increment
10/25/2001 14:02:30,System.Runtime,CPU Usage (%),Metric,12.5
10/25/2001 14:02:31,System.Runtime,ThreadPool Queue Length,Metric,214
10/25/2001 14:02:31,System.Runtime,ThreadPool Thread Count,Metric,63
10/25/2001 14:02:31,System.Runtime,Gen 2 GC Count (Count / 1 sec),Rate,1
10/25/2001 14:02:31,System.Runtime,% Time in GC since last GC (%),Metric,38
10/25/2001 14:02:31,Microsoft.AspNetCore.Hosting,http.server.request.duration (s)[http.request.method=GET;http.response.status_code=200;http.route=/orders/SENTINEL-7f3a;Percentile=95],Metric,4.21
10/25/2001 14:02:31,System.Runtime,dotnet.thread_pool.queue.length ({work_item}),Metric,214
```
- **JSON:**
  `{"TargetProcess":"Synthetic.Api","StartTime":"10/25/2001 14:02:30","Events":[{"timestamp":"2001-10-25 14:02:31Z","provider":"System.Runtime","name":"ThreadPool Queue Length","tags":"","counterType":"Metric","value":214}]}`
- **Counter allowlist:**
  - EventCounters: CPU Usage, Working Set, GC Heap Size, Gen 0/1/2 GC Count, % Time in GC, Allocation Rate, ThreadPool Thread Count, Queue Length and Completed Work Item Count, Monitor Lock Contention Count, Exception Count, Time paused by GC, LOH Size, GC Fragmentation; Kestrel Connection Queue Length, Request Queue Length and Current Connections; Hosting Request Rate, Current Requests and Failed Requests.
  - Meters: `dotnet.process.cpu.time`, `dotnet.process.memory.working_set`, `dotnet.gc.collections`, `dotnet.gc.pause.time`, `dotnet.gc.last_collection.heap.size`, `dotnet.thread_pool.{thread.count,queue.length,work_item.count}`, `dotnet.monitor.lock_contentions`, `dotnet.exceptions`, `http.server.{request.duration,active_requests}`, `kestrel.{active_connections,queued_connections,queued_requests}`, `http.client.{request.duration,open_connections,request.time_in_queue}`.
  - Units are normalised: MB becomes `bytes`, s becomes ms, `(%)` becomes `percent-host` for CPU (**verify**) and `percent` otherwise, and `/ 1 sec` becomes `per-sec`. Anything else is dropped, with a gap.
- **Tags:** `Percentile`, `gc.heap.generation`, `http.request.method` (method list), `http.response.status_code`, `error.type` (exception rule) are kept. `http.route`, `server.address`, `server.port` and `url.*` are dropped.
- **Emits:** kind `metric`; attrs `provider`, `counter`, `percentile`, `generation`, `method`, `status`, `error-type`, `pid` (the owner-stated `pid`, numeric).
- **Negatives:** `TargetProcess` never appears in output. An unknown counter is dropped and counted. A missing TZ for CSV is refused.

### W2-f: process snapshots and counter tweaks (Opus review)

- **Files:** `internal/importer/procsnap/`; `internal/importer/wincounters/wincounters.go` (instance rule only, plus its tests rerun for `sql-pdh`).
- **`win-procsnap`, from the Get-Process recipe** (§5):
```
"SampleUtc","ProcessName","Id","CPU","WorkingSet64","PrivateMemorySize64","HandleCount","Threads","StartTimeUtc"
"2001-10-25T01:02:15.0000000Z","w3wp","1240","812.44","1932735283","2147483648","2210","96","2001-10-24T19:00:02.0000000Z"
"2001-10-25T01:02:15.0000000Z","redis-server","3120","90.10","402653184","398458880","310","8","2001-10-20T00:00:00.0000000Z"
"2001-10-25T01:02:30.0000000Z","w3wp","1240","826.90","1940000000","2150000000","2214","104","2001-10-24T19:00:02.0000000Z"
"2001-10-25T01:02:30.0000000Z","w3wp","2210","0.30","120000000","90000000","400","20","2001-10-25T01:02:28.0000000Z"
```
- **tasklist (wrapped by the recipe):** `"SampleUtc","Image Name","PID","Session Name","Session#","Mem Usage"` with `"2001-10-25T01:02:15Z","w3wp.exe","1240","Services","0","1,887,436 K"`. `Session Name` is dropped. `User Name` or `Window Title` (the `/v` form) quarantine the file.
- **Emits:**
  - kind `metric` (`metric` = `cpu`, `working-set`, `private-bytes`, `handles`, `threads`). CPU is Δ`CPU` ÷ Δwall ÷ `LogicalCores` between snapshots of the same (pid, StartTimeUtc), unit `percent-host`, else `percent-one-core` with the wave-1 gap.
  - `process-start` (a point at StartTimeUtc, first seen only).
  - `process-gone` (the interval [last seen, first missing]).
  - Attrs `metric`, `process`, `pid`.
- **Counter tweaks:**
  - `instanceKept` adds `W3SVC_W3WP` with `^[0-9]{1,7}_` and **keeps the pid only** (`instance` = `pid-1240`); the pool name is never kept from a counter.
  - The export `counterNames` additions land in W2-F.
  - PDH regression fixture for A1 (already parseable):
```
"(PDH-CSV 4.0) (Synthetic Daylight Time)(-780)","\\web-a\Processor(_Total)\% Processor Time","\\web-a\Memory\Available MBytes","\\web-a\Process(w3wp)\Private Bytes","\\web-a\Process(w3wp)\ID Process","\\web-a\.NET CLR Memory(w3wp)\% Time in GC","\\web-a\.NET CLR Memory(w3wp)\# Gen 2 Collections","\\web-a\ASP.NET\Requests Queued","\\web-a\W3SVC_W3WP(1240_PoolSENTINEL-7f3a)\Active Requests"
"10/25/2001 14:02:15.000","42.5","2048","1932735283","1240","3.1","880","0","12"
"10/25/2001 14:02:30.000","97.8","812","2147483648","1240","38.0","884","214","230"
"10/25/2001 14:02:45.000"," ","790","2150000000","1240","41.2","885","230","241"
```
- **Negatives:** PID reuse with a different StartTimeUtc is a new series. A negative ΔCPU rejects the pair. A non-image process name becomes `other`. The pool sentinel never appears in output.

### W2-g: Windows event classifiers and IIS bytes

- **Files:** `internal/importer/winevents/classify_app.go` (exports `ClassifyApp`), `internal/importer/iis/was.go` (ids added), `internal/importer/iis/w3c.go` (bytes).
- **Kinds:** every id gets a code-comment citation of the public page, and an uncited id stays `win-event`.

| Kind | Provider and ids |
|---|---|
| `worker-process-failure` | WAS 5009, 5010, 5011, 5013 |
| `apppool-disabled` | WAS 5002 |
| `app-crash` | Application Error 1000, .NET Runtime 1026, Windows Error Reporting 1001 |
| `app-hang` | Application Hang 1002 |
| `service-crash` | Service Control Manager 7031, 7034 |
| `host-unexpected-shutdown` | EventLog 6008, Kernel-Power 41 |
| `low-memory` | Resource-Exhaustion-Detector 2004 |
| `aspnet-unhandled` | ASP.NET 4.0.30319.0 1309 |
| `ancm-start-failure` | IIS AspNetCore Module V2, ids from the ANCM troubleshooting page |

- **Optional column `FaultingApp`** (the recipe takes `Properties[0]` for ids 1000 and 1002): kept as `faulting-app` only for `^(w3wp|dotnet|node|redis-server|memurai|mongod|sqlservr)\.exe$`, else `other`.
- **W3C:** `sc-bytes` and `cs-bytes` become `bytes-out` and `bytes-in` (numeric).
- **Fixture** (a recipe CSV row): `"2001-10-25T01:02:40Z","web-a","System","Microsoft-Windows-WAS","5011","3",""`.
- **Negatives:** an uncited id stays generic. A `FaultingApp` of `SENTINEL-7f3a.exe` becomes `other`.

### W2-h: Redis via redis-cli (SLOWLOG, LATENCY HISTORY, INFO)

- **Files:** `internal/importer/redis/{resp,slowlog,latency,info,commands}.go`. This card alone owns `commands.go`, the fixed Redis command table.
- **Inputs:** each accepts the `--no-raw` tree text, `--json` (redis-cli 7+) and a RESP2 dump. `--raw` output is refused (`unsupported-format`, "use --no-raw"), because argument boundaries cannot be recovered from it.
- **SLOWLOG fixture** (`--no-raw`, picked host redis-a):
```
1) 1) (integer) 14
   2) (integer) 1003971751
   3) (integer) 15230
   4) 1) "KEYS"
      2) "session:SENTINEL-7f3a"
   5) "192.0.2.10:52144"
   6) "web-a-conn"
2) 1) (integer) 12
   2) (integer) 1003971740
   3) (integer) 1012000
   4) 1) "HGETALL"
      2) "user:alice"
   5) "198.51.100.7:50110"
   6) ""
```
  The same entries as `--json`: `[[14,1003971751,15230,["KEYS","session:SENTINEL-7f3a"],"192.0.2.10:52144","web-a-conn"]]`. As RESP2: `*1\r\n*6\r\n:14\r\n:1003971751\r\n:15230\r\n*2\r\n$4\r\nKEYS\r\n…`.
- **SLOWLOG emits:** `slow-command` as the interval `[ts − dur, ts]`, with `ts` taken as completion time to 1 s resolution (**verify**). Attrs `command` (table, else `other`), `argc`, `client` (pseudonym), `id`.
- **SLOWLOG quality:**
  - Threshold: the owner states `slowlog-log-slower-than` µs, else "unknown".
  - Coverage runs from the oldest kept entry to the export time.
  - A gap in id numbering, or holding `slowlog-max-len` entries, adds "SLOWLOG is a ring buffer; older entries were overwritten".
  - Always added: "SLOWLOG measures command execution only; network, queueing and I/O time are not included".
- **LATENCY HISTORY** (the recipe writes `# latency-event: <name>` before each block):
```
# latency-event: fork
1) 1) (integer) 1003971745
   2) (integer) 230
# latency-event: aof-fsync-always
1) 1) (integer) 1003971750
   2) (integer) 1180
```
  It emits `latency-spike` points (ms) with attr `event`. Events come from the documented list (`command`, `fast-command`, `fork`, `aof-*`, `expire-cycle`, `eviction-*`, `active-defrag-cycle`, `rdb-unlink-temp-file`). Gap: "latency monitor records only events above latency-monitor-threshold (default 0 = disabled); 160 samples per event".
- **INFO loop** (the recipe writes the sample line; `\r\n` is accepted):
```
# unravis-sample 2001-10-25T01:02:30Z
# Server
redis_version:7.2.4
uptime_in_seconds:86400
# Memory
used_memory:1073741824
used_memory_rss:1288490188
mem_fragmentation_ratio:1.20
# Clients
connected_clients:412
blocked_clients:3
# Stats
instantaneous_ops_per_sec:5120
rejected_connections:0
evicted_keys:0
latest_fork_usec:230120
```
  It emits `metric` points (attrs `metric`, `role`) for the allowlist: the fields above plus `used_memory_peak`, `maxmemory`, `total_connections_received`, `expired_keys`, `keyspace_hits`, `keyspace_misses`, `used_cpu_sys`, `used_cpu_user` (cumulative, converted to `percent-one-core` by delta), `rdb_bgsave_in_progress`, `aof_rewrite_in_progress`, `role`. A drop in `uptime_in_seconds` also emits `redis-start`. Every other line, including `# Keyspace`, `executable`, `config_file`, `run_id`, `master_host` and `os`, is dropped.
- **Negatives:** a file with no sample line is refused. The sentinel key never appears in output. An unknown command becomes `other`.

### W2-i: redis.log

- **Files:** `internal/importer/redis/log.go` (imports only `commands.go`, read-only).
- **Fixture** (host redis-a, local):
```
1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240
1240:C 25 Oct 2001 14:02:31.900 * DB saved on disk
1234:M 25 Oct 2001 14:02:32.004 * Background saving terminated with success
1234:M 25 Oct 2001 14:02:33.500 * Asynchronous AOF fsync is taking too long (disk is busy?). Writing the AOF buffer without waiting for fsync to complete, this may slow down Redis.
1234:M 25 Oct 2001 14:02:40.000 # Client id=91 addr=192.0.2.10:52144 name=SENTINEL-7f3a closed for overcoming of output buffer limits.
1234:M 25 Oct 2001 14:05:00.000 # User requested shutdown...
1300:M 25 Oct 2001 14:05:03.100 * Ready to accept connections tcp
[1234] 25 Oct 14:02:30.120 * Background saving started by pid 1240
```
- **Kinds,** by prefix allowlist: `redis-start`, `redis-stop`, `bgsave-start`, `bgsave-done`, `bgsave-fail`, `aof-rewrite-start`, `aof-rewrite-done`, `aof-fsync-slow`, `client-buffer-closed`, `replication-event`, `config-warning`. Attrs `role` (M/S/C/X become master/replica/child/sentinel) and `family`. Lines that match nothing are counted. The message is never stored.
- **Negatives:** a missing TZ is refused. The year-less form with no stated year is refused. The sentinel client name never appears in output.

### W2-j: Node Redis proxy (conditional)

- **Precondition:** the owner sends the **field names only** (§5), and the card adjusts the mapping table. Without that list the card is not launched.
- **Framer for pino and winston JSON lines:**
  - time: `time` (epoch ms) or `timestamp` (ISO with Z or an offset)
  - level: `level`
  - duration: one of `durationMs`, `duration_ms`, `latencyMs`, `responseTime`, `elapsedMs`
  - command: `cmd` or `command`, checked against the Redis table
  - error: `err.code` checked against the errno list, and `err.type` through the exception rule
  - client event: `event` in `connect`, `ready`, `reconnecting`, `error`, `end`
  - everything else (`msg`, `key`, `args`, `hostname` except for mapping) is dropped
- **Fixture:**
```
{"level":30,"time":1003971751123,"pid":4100,"hostname":"redis-proxy-a","msg":"command done","cmd":"GET","key":"session:SENTINEL-7f3a","durationMs":812}
{"level":40,"time":1003971751200,"pid":4100,"hostname":"redis-proxy-a","msg":"redis reconnecting","event":"reconnecting","delay":200}
{"level":50,"time":1003971751300,"pid":4100,"hostname":"redis-proxy-a","err":{"type":"Error","message":"connect ETIMEDOUT 192.0.2.20:6379","code":"ETIMEDOUT"},"msg":"redis error"}
{"level":"warn","message":"slow command","command":"HGETALL","latencyMs":1500,"timestamp":"2001-10-25T01:02:32.000Z"}
```
- **Emits:** the §2.3 kinds, plus `proxy-command` intervals at or above `app-request-min-ms`, and `client-event` points.

### W2-k: Mongo exports (Opus review)

- **Files:** `internal/importer/mongoexports/{profile,currentop,serverstatus,mongostat}.go`. The keep and drop lists are reused from `mongodlog` (read-only import).
- **Profiler** (mongoexport, relaxed JSON, one document per line):
```
{"op":"query","ns":"appdb.orders","command":{"find":"orders","filter":{"customer":"SENTINEL-7f3a"}},"keysExamined":0,"docsExamined":120000,"nreturned":3,"numYield":937,"millis":1830,"planSummary":"COLLSCAN","queryHash":"0A1B2C3D","ts":{"$date":"2001-10-25T01:02:31.123Z"},"client":"192.0.2.10","appName":"job-alpha","user":"svc@appdb","responseLength":512}
```
  It emits `slow-op` (attrs as `mongod-json`, plus `responseLength`). `ts` is taken as the end time (**verify**), so Start = ts − millis. Threshold: the owner states `slowms`, else "unknown". Gap: "profiler level and filter unknown unless stated".
- **$currentOp loop:**
  `{"currentOpTime":"2001-10-25T14:02:31.000+13:00","op":"query","ns":"appdb.orders","secs_running":28,"microsecs_running":28100000,"waitingForLock":false,"numYields":930,"planSummary":"COLLSCAN","client":"192.0.2.10:50000"}`
  It emits `inflight-op` as `[t − microsecs, t]` with attrs `op`, `ns`, `waitingForLock`, `numYields`, `planSummary`. Gap: "snapshots: operations shorter than the sampling interval are not observed".
- **serverStatus loop** (the recipe projects these fields):
  `{"localTime":{"$date":"2001-10-25T01:02:30.000Z"},"host":"mongo-a.example.test","version":"7.0.12","uptime":86400,"connections":{"current":412,"available":50788},"globalLock":{"currentQueue":{"total":37,"readers":30,"writers":7},"activeClients":{"readers":128,"writers":4}},"opcounters":{"query":1200400,"insert":3000,"update":55000},"mem":{"resident":6144},"wiredTiger":{"cache":{"bytes currently in the cache":5368709120,"tracked dirty bytes in the cache":805306368,"maximum bytes configured":8589934592}}}`
  It emits `metric` (attr `metric`, a fixed dotted-path list). Opcounters are cumulative and stay raw; A1 takes the deltas. A drop in uptime emits `mongod-start`.
- **mongostat --json** (least preferred; needs `date` and host TZ):
  `{"mongo-a.example.test:27017":{"arw":"30|7","conn":"412","dirty":"9.4%","qrw":"30|7","query":"1200","res":"6.0G","time":"14:02:31","used":"62.5%"}}`
- **Negatives:** the sentinel in a filter never appears in output. `user`, `appName` and `client` are absent from output. An FTDC magic header is refused.

### W2-l: mongod.log markers (Haiku)

Add kinds `mongod-start` and `mongod-stop` to `mongodlog`, keyed by log ids cited from the docs ("Waiting for connections", shutdown start). The export kinds were already added by W2-F. Test: one start and one stop line in a synthetic 4.4+ log.

### W2-m: Linux sadf and pidstat (conditional on the host OS)

- **Inputs:**
  - `sadf -d` (UTC): `mongo-a.example.test;600;2001-10-25 01:00:01 UTC;-1;12.0;0.0;4.0;30.1;0.0;53.9`
  - `pidstat -H -h -u -r`: `1003971750  999  3120  41.00  9.00  0.00  0.00  50.00  2  0.00  0.00  512000  402000  2.45  redis-server`
- **Emits:** `metric` with attrs `metric`, `process`, `pid`, `cpu`. The `-l` form (command lines) is refused (`unscrubbed-column`).

## 4. Analysis layer

### 4.0 New wordings (twins in `wording.go` and `wordings.ts`, landed by W2-F, needs R-W2b)

| Function | Template | Label |
|---|---|---|
| `MetricRose` | "<subject>: <metric> rose by <delta> over <span> (from <from> to <to>)." | Symptom |
| `MetricSampleOverlap` | "A <source> sample showed <metric> at <value> during this interval (Correlation)." | Correlation |
| `MarkerBefore` | "This interval starts <offset> after a <marker> on <host> (Correlation)." | Correlation |
| `EpisodeShare` | "<condition> in <k> of <n> stall episodes, and in <m> of <M> comparison windows outside stalls." | Symptom |
| `TopInWindow` | "Highest <metric> in this window: <list>." | Symptom |

Interval events (Redis slow commands, in-flight Mongo operations, app requests, heartbeat-late) reuse `OverlapUnique`/`OverlapAmbiguous`. Absence uses only `NotObservedThreshold` or `NotObservedGap`. "leak", "starvation", "saturated", "caused" and "because" never appear in any copy, and a copy-lint fixture pins this.

### 4.1 S2: series query and owner-supplied settings (server)

- **Files:** `internal/casestore/{store.go,memory.go,sqlite/*}`, `internal/server/cases.go`, `routes.go` (one appended block). This card alone touches the server in this wave.
- **Series:** `Case.Each(ctx, EventQuery, fn func(evidence.Event) error) error` streams events over `ev_kind_ts` with no 50 000 cap, bounded by a scan budget (20M events or 10 s), after which the result is marked partial. `GET /api/cases/{id}/series?from&to&bucket&sel=` returns per-series min, mean, max and count per bucket, at most 2 000 buckets × 50 series. The API keeps the 50 000 cap on `/events`.
- **Supplied:** `POST …/imports` accepts `s.<key>=<value>` only for keys the chosen importer declares through a new optional `interface{ SuppliedKeys() map[string]importer.SuppliedSpec }` (enum or bounded number). Unknown keys or values are rejected with `bad-supplied`. Values go to `Options.Supplied` and the import manifest, never to a log.
- **Tests:** 3M synthetic events give a correct bucket mean; the budget gives partial; supplied validation is enforced; the memory and SQLite contract tests both pass.

### 4.2 A1: resource catalogue (`internal/resource`, Opus)

- **Metric map** `(source, kind, attrs) → Metric{ID, Unit, Scale, Cumulative bool}`:
  - `host.cpu` from `Processor(_Total)` or sadf
  - `host.mem.available`: Available MBytes becomes bytes
  - `host.mem.committed`, `host.pages`
  - `host.disk.sec_per_transfer`, `host.disk.queue`, `host.net.bytes`
  - `proc.cpu`, `proc.ws`, `proc.private`, `proc.handles`, `proc.threads`
  - `clr.gen2` (cumulative), `clr.gc_time`
  - `aspnet.queued`, `aspnet.current`, `aspnet.wait_ms`
  - `iis.wp.active`
  - `net.tp.queue`, `net.tp.threads`, `net.gc_pause`
  - `kestrel.queued`
  - `redis.mem`, `redis.clients`, `redis.blocked`, `redis.ops`
  - `mongo.queue`, `mongo.conns`, `mongo.wt.dirty`
- **Process identity:**
  - Process V2 gives `name:pid`.
  - Legacy `w3wp#1` is re-keyed per sample through `ID Process`, or `.NET CLR Memory\Process ID`.
  - `W3SVC_W3WP` `pid-1240` joins to the same pid.
  - Get-Process and pidstat give (name, pid, start).
  - Without a pid, the series stays on its instance name with the gap "legacy instance numbers can move between processes".
- **Sample spans:** a PDH or rate sample covers `[t − interval, t]`, where the interval is the median cadence. This lets point samples overlap stall intervals (P8 needs a span > 0).
- **Markers:** derived start and stop markers from WAS recycles, `worker-process-failure`, `app-start`/`app-session`/`app-stop`, `process-start` and `process-gone`, a new pid for the same image, `redis-start`, `mongod-start`, a reset in uptime or `Elapsed Time`, and `host-unexpected-shutdown`.
- **Coverage:** per series, the fraction of expected buckets present in the window.
- **API:** `Catalogue(c casestore.Case, w importer.Window) (Index, error)`; `(Index).Series(metric, filter) []Series`; `Markers() []Marker`; `Coverage(series) float64`.

### 4.3 A2: rules (`internal/resource/rules`, Opus)

Every rule follows the same pattern:

- **Inputs:** the view window, audit stall intervals grouped into **episodes** (10 min quiet gap, §10.2), and catalogue series.
- **Thresholds:** stated, tunable, and shown with the result.
- **Coverage first:** a series under 80 % coverage in the window gives `NotObservedGap("<n> % of samples present for <metric> on <host>")`, never a negative.
- **Output:** each row carries its wording, label and evidence IDs.

| Rule | Evidence required | Output | Absence |
|---|---|---|---|
| **RC1 top-N processes** | `proc.cpu` (percent-host) or `proc.private` | `TopInWindow`, top 5 by mean CPU and by peak private bytes. "All other processes" = `host.cpu` − Σ top-N, shown only when `host.cpu` exists; otherwise "sum of listed processes only" | no process series: gap "no per-process counters for <host>" |
| **RC2 memory rise** | `proc.private`, `proc.ws` or `redis.mem`; at least 12 buckets; coverage ≥ 80 %; no start marker inside (segments split at markers) | `MetricRose` when the rise is ≥ 20 % and ≥ 256 MiB over ≥ 30 min, and ≥ 70 % of bucket steps go up. Theil–Sen slope shown beside it | segment too short: no row, plus a coverage line |
| **RC3 host pressure overlap** | `host.cpu` ≥ 90 %, `host.mem.available` < 500 MiB, `host.disk.sec_per_transfer` > 25 ms | per episode `MetricSampleOverlap`; summary `EpisodeShare` | gap per §4.3 coverage |
| **RC4 GC overlap** | `clr.gc_time` ≥ 10 %, a Δ`clr.gen2` ≥ 1 per sample, `net.gc_pause` | `MetricSampleOverlap`, `EpisodeShare` | .NET Core with no dotnet-counters: gap "no runtime counters for this process" |
| **RC5 thread-pool queue overlap** | `net.tp.queue` > 0 for ≥ 2 samples, `aspnet.queued` > 0, `kestrel.queued` > 0, `redis-timeout` entries with `worker-busy` > `worker-min`, `heartbeat-late` | `MetricSampleOverlap`. For timeouts: `EpisodeShare("Redis timeout entries with WORKER busy above min", …)`. `heartbeat-late` gives `OverlapUnique` | .NET Framework: "no thread-pool counter exists; ASP.NET Requests Queued shown instead" |
| **RC6 Redis overlap** | `slow-command`, `latency-spike`, `aof-fsync-slow`/`bgsave-*`, `redis.blocked` | `OverlapUnique`/`OverlapAmbiguous` per interval | `NotObservedThreshold("Redis SLOWLOG", "commands slower than <n> µs (execution time only)")` only when the threshold is stated **and** the ring buffer's coverage spans the interval with no id gap; otherwise `NotObservedGap(...)` |
| **RC7 app-log rate** | `log-rate` at warning and above, per minute; families | strip plus `EpisodeShare("ERROR entries above <p95 outside stalls>/min", …)`; top families in the window as `TopInWindow` | no app log for host: gap |
| **RC8 restart / cold start** | A1 markers | `MarkerBefore` when an episode starts within 5 min after a marker on the same host | markers are source-dependent: "recycle events are logged only for enabled triggers" (`RecycleGap`) is always shown |
| **RC9 Mongo overlap** | `slow-op`, `inflight-op`, `mongo.queue` > 0 | as RC6 with the `mongod-json` threshold rules | as P4 `Absence` |

**Tests:**
- Positive: the shared synthetic stall gives RC3, RC4, RC5, RC6 and RC8 rows with exact strings.
- Negative: a 28 s stall with a SLOWLOG threshold stated but the buffer coverage starting after it gives the gap wording, not the threshold wording.
- A memory rise interrupted by a recycle gives two segments and no `MetricRose`.
- Every string passes `LintCausal`.
- No row carries a label stronger than Correlation.

### 4.4 UI (progressive disclosure)

- **U1 `ResourcePanel.svelte` + `web/src/lib/resource.ts`**, mounted in `CaseTools.svelte` below the Timeline. Not in `App.svelte`, which `wave/drill` and INT own.
  - **Level 1 (collapsed):** one summary line per host, e.g. "web-a: CPU peak 97.8 % · w3wp private bytes rose by 1.2 GiB over 3 h · 41 Redis timeout entries", plus coverage dots.
  - **Level 2 (expanded):** stacked strips on one time axis:
    - stall episodes as shaded bands, plus an API p95 strip (read-only from the existing app state when present)
    - host CPU and memory
    - top-5 process lines (toggle CPU, working set, private bytes, handles, threads), with "all other processes" as a grey band
    - runtime (GC, thread pool, queues)
    - Redis and Mongo
    - log rate
    - markers as vertical rules across all strips
  - **Level 3:** clicking a point or band opens the existing `Drawer` with the rule rows, wordings and evidence IDs (provenance is import and line).
  - **Zoom:** the panel reads the `zoom.ts` view window and calls its setter on brush. If `wave/drill` has not merged, U1 waits; it does not design zoom. Data is fetched from `/series` with a bucket of view span ÷ 600.
  - **Tests:** shape validation, the empty and gap states, and that a zoom change refetches.
- **U2 rule chips:** one chip per fired rule in the panel header, with exact wording and label, and a plain-language hint (P14a rules). Copy-linted.
- **U3 settings** (part of S2's contract, UI in `ImportPanel.svelte`): a collapsed "Settings the owner told you" group shows only the declared keys for the chosen importer (selects and numbers, no free text): `log-time`, `pattern` (log4net, ≤ 512 chars, token-validated), `slowlog-log-slower-than`, `slowms`, `year`, `date`, `pid`, `app-request-min-ms`.

## 5. Collection guide (owner-run, read-only; send the output files, nothing else)

| Source | Recipe | Do NOT include |
|---|---|---|
| log4net | Copy the rolled files for the window (`robocopy <logdir> <export> app.log.2001-10-25* /COPY:D /R:0`). Send the `conversionPattern` value only | `web.config`, `log4net.config` (they hold connection strings), other days |
| Serilog | Copy the files for the window. For CLEF, the file sink with `CompactJsonFormatter` (a config change is the owner's decision). Send the `outputTemplate` line only | `appsettings*.json`, Seq API keys |
| MEL / ANCM | Copy `logs\stdout_*.log` for the window. Enabling `stdoutLogEnabled` or JSON console timestamps is an owner change, and stdout has no size cap, so set it briefly and remove it | `web.config` beyond the two attributes |
| dotnet-counters | `dotnet-counters collect -p <pid> --refresh-interval 5 --duration 00:30:00 --format csv -o C:\export\web-a-counters.csv --counters System.Runtime,Microsoft.AspNetCore.Hosting,Microsoft.AspNetCore.Server.Kestrel,System.Net.Http` (attaches to a live process: the owner approves). State the pid | `dotnet-dump`, `dotnet-trace`, `.nettrace` files |
| PDH (typeperf) | `typeperf -cf counters.txt -si 15 -sc 1440 -f CSV -o C:\export\web-a-perf.csv`, where counters.txt lists `\Processor(_Total)\% Processor Time`, `\Memory\Available MBytes`, `\Memory\Committed Bytes`, `\Memory\Pages/sec`, `\Process(*)\{% Processor Time,Working Set,Private Bytes,Handle Count,Thread Count,ID Process}` (or `\Process V2(*)\…`), `\.NET CLR Memory(*)\{% Time in GC,# Gen 2 Collections,Process ID}`, `\ASP.NET\{Requests Queued,Requests Current,Request Wait Time,Requests Rejected}`, `\W3SVC_W3WP(*)\Active Requests`, `\LogicalDisk(*)\{Avg. Disk sec/Transfer,Current Disk Queue Length}`, `\Network Interface(*)\Bytes Total/sec`. On an English-language OS. Or use logman to `.blg`, then `relog x.blg -f csv -o x.csv`. Long runs are a DCS (§7 C2) | `.blg` files, counter sets beyond the list |
| Get-Process | `while($true){ $t=(Get-Date).ToUniversalTime().ToString('o'); Get-Process \| Select @{n='SampleUtc';e={$t}},ProcessName,Id,CPU,WorkingSet64,PrivateMemorySize64,HandleCount,@{n='Threads';e={$_.Threads.Count}},@{n='StartTimeUtc';e={try{$_.StartTime.ToUniversalTime().ToString('o')}catch{''}}} \| Export-Csv C:\export\web-a-procs.csv -Append -NoTypeInformation; Start-Sleep 15 }` | `-IncludeUserName`, `Path`, `CommandLine`, `MainWindowTitle`, `tasklist /v` |
| Windows events | The wave-1 recipe, adding the Application log providers in W2-g and optionally `@{n='FaultingApp';e={if($_.Id -in 1000,1002){$_.Properties[0].Value}}}` | `Message`, `Properties` |
| Redis SLOWLOG | `redis-cli -h redis-a.example.test --no-raw SLOWLOG GET 1024 > slowlog.txt` (or `--json`). Read `CONFIG GET slowlog-log-slower-than` and `slowlog-max-len`, then type the values into the import settings | `SLOWLOG RESET`, `MONITOR`, `CLIENT LIST`, `KEYS`, `DEBUG`, any `CONFIG SET` |
| Redis LATENCY | `LATENCY LATEST` to list events, then for each: write `# latency-event: <name>` and append `redis-cli --no-raw LATENCY HISTORY <name>` | `LATENCY RESET` |
| Redis INFO | A loop writing `# unravis-sample <UTC ISO>`, then `redis-cli INFO server memory clients stats persistence`, every 15 s | `INFO keyspace`, `INFO commandstats`, `CLIENT LIST` |
| redis.log | Copy the file for the window | `redis.conf` (`requirepass`, `masterauth`) |
| Node proxy | First send the field names only: `Get-Content x.log -TotalCount 1000 \| ConvertFrom-Json \| % { $_.PSObject.Properties.Name } \| Sort -Unique`. Then copy the files for the window | `.env`, proxy config |
| Mongo profiler | Only if profiling is **already** on (the tool never enables it, §7 A4; this needs A3-level access, so the owner runs it): `mongoexport -d appdb -c system.profile -q '{"ts":{"$gte":{"$date":"2001-10-25T00:00:00Z"},"$lt":{"$date":"2001-10-25T02:00:00Z"}}}' --fields op,ns,millis,ts,keysExamined,docsExamined,nreturned,numYield,planSummary,queryHash,responseLength -o profile.json` | `command`, `client`, `user`, `appName` (the projection keeps them on the server) |
| Mongo currentOp / serverStatus | A mongosh loop printing `EJSON.stringify` of the projected fields in W2-k every 15 s (`$currentOp` with `{$match:{active:true,secs_running:{$gte:5}}}` and a `$project`) | `command`, `client`, `effectiveUsers`, FTDC |
| Linux | `sadf -d /var/log/sa/sa25 -- -u -r -d -n DEV`; `pidstat -H -h -u -r 15 240` | `pidstat -l`, `ps aux` |

Every recipe gives a file the owner reads before sending it, and §0 R2/R8 still gate real imports. The banner text is unchanged.

## 6. Build order and parallelisation

1. **W2-F alone** (Opus review batch 1). It pre-registers every shared vocabulary item, so **no G1 or G2 card edits** `internal/evidence/caseexport*.go`, `wording.go`, `wordings.ts`, `internal/timeline/timeline.go`, `cases.ts` `IMPORT_CAPS`, `importCopy.ts` or `internal/importer/all/all.go`. A card that needs a new value writes it in its PR description, and INT2 applies it.
2. **G1, in parallel (8 to 10 worktrees):** W2-a, W2-e, W2-f, W2-g, W2-h, then W2-i (same package, separate files, merged after W2-h because `commands.go` belongs to W2-h), W2-k, W2-l, W2-m (if confirmed), S2.
3. **G2:** W2-b, W2-c, W2-d, W2-j (conditional) against the W2-a API that W2-F landed. A1 against the S2 contract.
4. **G3:** A2 (after A1 and R-W2b); U1 (after S2, A1 and `wave/drill`).
5. **G4:** U2. Then **INT2**:
   - register everything in `all.go` (with `ClassifyApp` added to the `winevents.New` classifier list)
   - enable the drift entries
   - run the cross-detect matrix (§2.5)
   - run the **golden stall test**: the shared stall in the log4net (local), CLEF (Z), redis-slowlog (epoch), PDH (bias −780), dotnet-counters (local) and Get-Process (UTC) fixtures aligns within each tolerance, and four mutations (one source shifted by +13 h) must fail
   - rebuild `web/dist`, `go vet`, `go test ./...`, `npm test`

**Merge hotspots and how to avoid them:**

| File | Owner | Others |
|---|---|---|
| `internal/importer/all/all.go` | INT2 | none (cards expose `New…()`) |
| `internal/evidence/caseexport*.go`, `wording.go`, `wordings.ts` | W2-F, then INT2 | request in the PR |
| `internal/timeline/timeline.go` | W2-F | none |
| `internal/casestore/*`, `internal/server/{cases,routes}.go` | S2 | none (also check against `wave/drill`'s server edits, if any) |
| `internal/importer/wincounters/wincounters.go` | W2-f | P3's `sql-pdh` tests rerun |
| `internal/importer/iis/{was,w3c}.go`, `winevents/` | W2-g | none |
| `internal/importer/redis/commands.go` | W2-h | W2-i and W2-j read it only |
| `web/src/lib/cases.ts`, `copy/importCopy.ts` | W2-F; U3 (S2's UI part) for settings only | none |
| `ImportPanel.svelte` | U3 | none |
| `CaseTools.svelte` | U1 (one mount line) | U2 adds inside `ResourcePanel` only |

**Opus review batches:**
1. W2-F
2. W2-a with W2-b, W2-c, W2-d (privacy at parse)
3. W2-f, W2-h, W2-i, W2-k (drop lists, ring buffer and threshold wording)
4. S2 with A1 (scan budget, identity)
5. A2 with U1 and U2 (wording, causal lint, coverage)
6. INT2 whole diff against wave 1 §4 and §2 here

Haiku re-reviews the fix rounds. After merge and run: one "clumsiest user" review (memory rule).

## 7. Risks and unverified assumptions

- **Formats pinned from memory, not from a source in this repo:**
  - dotnet-counters CSV and JSON shape, time zone and culture (W2-e step 1)
  - whether `CPU Usage (%)` is a share of the whole machine
  - the Kestrel heartbeat event id 22
  - the ANCM stdout file-name time zone and the ANCM event ids
  - WAS 5002, 5009–5013 and the other W2-g ids (each needs a citation, or the row stays generic)
  - the Redis SLOWLOG time being completion time
  - redis-cli `--json` (7.0+)
  - the profiler `ts` being end time
  - the StackExchange.Redis timeout field set (it varies by version; missing fields are simply absent)
- **Time:**
  - log4net `%date` versus `%utcdate` cannot be told apart from the file, so a wrong owner statement shifts the whole file by the host offset. The golden-stall mutation test catches only fixtures, not real files.
  - MEL console and ANCM output usually have no timestamps, so they contribute only sessions and heartbeat lines.
  - Clock skew between hosts is not corrected.
- **Coverage:**
  - SLOWLOG holds 128 entries by default and LATENCY is off by default, so absence is usually "gap".
  - .NET Framework has no thread-pool counter.
  - Process V2 may be missing on older Windows Server.
  - typeperf counter names are localised on non-English OS builds, so the parse matches nothing.
  - The PDH date order is locale-bound (existing gap).
- **Volume:**
  - `Process(*)` × 6 counters × 15 s over 24 h for 200 processes is about 6.9M events, under the 10M `MaxEvents` but heavy. The recipe limits the window.
  - `Each` scans need the S2 budget, and an untested SQLite scan rate on 10M rows could make the panel slow (measure in S2).
- **Privacy:**
  - Framework logger prefixes and the exception regex are allowlists; a client namespace that starts with `Microsoft.` would pass (accepted, rare).
  - Process names and Mongo `ns` are kept in the encrypted case store and pseudonymised only at export, as in wave 1.
  - The family regexes run on client text in memory. A ReDoS-safe review is part of batch 2 (Go RE2 is linear).
- **Governance:**
  - R-W2a (Redis) and R-W2b (wordings) are unruled.
  - The `OverlapUnique` "session" wording reads oddly for Redis and app requests; a reword needs R-W2b too.
  - The §5.4 three-incident rule may retire importers that go unused.
  - The Node proxy format is unknown, so W2-j is conditional.
  - Linux hosts are unconfirmed, so W2-m is conditional.
- **Dependency:** U1 depends on `wave/drill`'s `zoom.ts` names. If they change, only the adapter in `resource.ts` changes.

## Ruling: logger depth for W2-a

The intended ruling was that the log4net, Serilog and Microsoft.Extensions.Logging (MEL) importers record a logger name only to its first two dotted segments. The code does not do that, and this note records what the code does.

- The three importers share `internal/importer/applog` (`logger.go`, `LoggerLabel`). No importer cuts a logger name short.
- A name is kept whole when it matches the framework allowlist: `System`, `Microsoft`, `MongoDB`, `Serilog`, `log4net`, `Hangfire`, `Polly`, `Grpc`, `Azure`, `Newtonsoft` or `NHibernate`, plus at most two more segments (three segments in all); `StackExchange.Redis` plus at most one more segment; or `ioredis` or `redis` alone. Each segment is 1 to 64 letters, digits or `_`.
- Any other name becomes a per-case `logger-xxxxxxxx` pseudonym. This includes a framework name that is deeper than three segments, so a long framework name is replaced whole, not cut to its first two segments. If no pseudonym can be made, the file is refused.
- The export applies the same rule (`internal/evidence/caseexport.go`). `TestLoggerRuleMatchesExport` and `TestW2FExportValueRules` check that the two agree.

If the owner still wants a two-segment cut, that is a new change to `applog/logger.go` and the export rule together.
