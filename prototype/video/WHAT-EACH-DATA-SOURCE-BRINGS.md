# What each data source brings

Source pack for an explainer video. Written for a complete beginner (support engineer, junior developer): plain English first, then the technical term. Demo numbers are made up. Unravis only ever **reads**. Anything that changes a server (a setting, a grant, a log session) is a script shown to the server's owner, never run by Unravis.

## 0. How to read this file

| Tag | Meaning | Where to check it |
|---|---|---|
| **[Real app]** | What the shipped Go code on `origin/master` reads | `internal/importer/*` (one folder per source), `internal/audit`, `internal/store/pipeline.go`, `internal/dbhealth`, `internal/blackbox`, `docs/api-audit-log-schema.md`, `docs/user/sources.md` |
| **[Prototype]** | What the clickable prototype (`prototype/tracing-paper-PROTOTYPE.html`) pretends each source holds | the `READS`, `SUS_CAT`, `SUS_SIG`, `SUS_SRC`, `SUS_NEEDS` tables |
| **[Vendor doc]** | The research notes in the repo (they cite official Microsoft and MongoDB pages plus practitioner experience; each item is tagged `[doc]` or `[exp]` there) | `buildout/research/investigation-walkthrough.md`, sections 2 and 9 |

Four ways a source can be available. They explain most "grey" suspects on screen.
1. **Already on.** Nothing to do (the audit log, IIS logs, HTTP.sys error log, `system_health`).
2. **Switch on.** A server setting must be changed by its owner (Query Store, the MongoDB profiler, extra SQL Server log sessions).
3. **Grant a permission.** A read-only login needs a named permission (shown as a script).
4. **Export and bring it.** A person copies or exports a file into the case, because it rolls over or lives on another machine.

A suspect that needs a source the case does not hold is **grey** ("Cannot test yet"). Grey means "we could not look", never "fine".

A major finding to keep in mind: the **prototype is more generous than the shipped importers** for SQL Server and MongoDB. Where the two disagree this file says so (marked **Gap**), because the video must not suggest that the shipped app already reads everything the demo shows.

---

## 1. At a glance

| Source | Tells you | Cannot tell you | Needs |
|---|---|---|---|
| API audit log (MongoDB) | Every audited request: when, how long, which route, which status, how the time splits (A, C, P) | Why it was slow; database timings; which server; exception text; requests that were not audited | Already on. Read login on the audit database. Index on `RequestInitiatedAt` for cheap reads |
| IIS W3C web log | Every request that reached the web server: status, sub-status, time taken, bytes | What the application did inside; requests rejected before IIS logged them (those go to HTTPERR) | Already on (check `time-taken` is logged). Copy the file in |
| HTTP.sys error log | Connections dropped or refused before the application saw them, with a reason | How many requests were fine; anything inside the app | On by default. Copy the file in |
| IIS failed request traces | Per-request module timings for requests that matched a rule (slow or failing) | Anything for requests that did not match the rule | Switch on (rule on a status or time-taken). Short retention |
| SQL Server extended events | Deadlocks, long waits, severe errors (shipped); slow calls and blocked-process reports (needs extra log sessions, **Gap**) | Waits shorter than the logged thresholds; statement text (never read) | `system_health` is already on and small; extra sessions to be created by the DBA |
| SQL error log | Restarts, I/O taking over 15 seconds, severe errors, login failures (counted) | Anything that is not written as a log line | Already on. Permission to read the log (2022: `VIEW ANY ERROR LOG`) |
| Query Store | Per-interval average duration, CPU, reads, plan changes, wait categories per query | Exact moment inside an interval; queries still running; history before it was switched on | Switch on and keep `READ_WRITE`. Permission `VIEW DATABASE PERFORMANCE STATE` |
| SQL server counters (waits, file I/O, schedulers, memory grants) | Whether the server as a whole was waiting on locks, disk, CPU or memory | Which database or query, unless sampled; a past window unless someone sampled it | Live checks: `VIEW SERVER PERFORMANCE STATE`. History: a sampler (**Gap**, not built) |
| MongoDB slow-operation log | Operations over `slowms` with duration, plan summary, docs examined | Operations under the threshold; command bodies (dropped) | Already on at 100 ms by default; keep verbosity 0, rotate and keep 14 to 30 days |
| MongoDB profiler | Slow operations with a stored, queryable record | Anything older than the capped collection | Switch on (level 1, audit database only); administrator action; older records lost |
| MongoDB `serverStatus` | Connections, queue lengths, operation counters, cache, tickets | Which operation; any history unless sampled (counters are cumulative) | `clusterMonitor` role; a sampler loop every 10 to 15 s |
| MongoDB `top` | Not read by any importer in the repo | n/a | n/a |
| MongoDB `currentOp` | What is running right now | The past (not retroactive) | `clusterMonitor`; sample only while degraded |
| Windows host counters and event log (later source) | CPU, memory, disk queue, network; app-pool recycles, crashes, restarts | Which request or query caused it | Performance counter log or the Blackbox; event log export |
| Typed-in change list | When releases and changes happened (as you typed them) | Whether a change caused anything; changes you did not type | A person types them |

---

## 2. API audit log in MongoDB

**One line.** The application's own record of each request: the only per-request record Unravis reads.

**Where from and what must be on.**
- **[Real app]** Mongo collection `ApiAuditLog`, one document per API call or Hangfire job, written by the application itself (already on; scope may be only some routes). Unravis needs a read login on the audit database. Reads are bounded: a broad scan of a live source is refused unless explicitly allowed (`--allow-broad-scans`), and there is a docs-examined budget.
- **[Vendor doc]** Ask the owners to audit all routes for the affected tenants for at least 14 days before and during an incident. Add an index on `RequestInitiatedAt` or reads become collection scans.
- Retention is whatever the application keeps. The demo audit source keeps records from 13 Oct (made up).

**Exact fields read. [Real app]** (`internal/store/pipeline.go`, `docs/api-audit-log-schema.md`)

| Field | Plain meaning |
|---|---|
| `RequestInitiatedAt` (fallback `CreatedOn`) | When the audit filter started: the request's start time |
| `RequestCompletedAt` | When the action finished. Missing means the second write never landed |
| `AuditCreateTime` (A) | Audit set-up plus the synchronous Mongo insert |
| `ApiTime` (C) | The rest of the filter chain plus the action |
| `PayloadRecordTime` (P) | Reading and cleaning the payloads, after the action |
| `ControllerName`, `ActionName` | The route, shown as `Controller.Action` |
| `StatusCode` | A .NET status **name** (`OK`, `NotFound`), not a number. Hangfire jobs use `Succeeded` or `Failed` |
| `RequestPollingType` | Synchronous, Asynchronous or Hangfire. Stored with a trailing space; trimmed before grouping |
| `UserAgent` | Used for grouping |

Not stored (and so never available): headers except user agent and UI version, tokens, database timings, any breakdown inside the action, the server name, the account id for HTTP calls, exception type or stack trace. Payloads are truncated (request default 5,000 characters, response 1,000), so a short payload says nothing about size.

**Question it answers.** Was the application slow or failing, since when, on which routes, and in which part of the request (A, C or P)?

**Cannot tell.** Why. Whether the slow part was the database, a lock, the code or the machine. The time before the audit filter runs (HTTP.sys queue, IIS queue, authentication) is outside the recorded time; that needs the IIS log.

**Feeds.**
- Figure cards: Requests to the application, Slow-request time (P95), Application server errors (5xx). Lane: API requests (audit log). Tile: Application.
- Suspects: **Q2** (audit coverage gap, compared with IIS), **L1** (traffic surge), **L2** (one route dominates), **S5** (audit-write coupling), **S8** (payload slowdown). Almost every other suspect uses "audit time up" as its first signal.

**Example.** "Payroll.Run: 4,216 requests, estimated P95 522 ms, 2.4 times the comparison period." (demo)

## 3. IIS W3C web log

**One line.** The web server's front-door record: every request that reached IIS, with its total time and status.

**Where from and what must be on.**
- **[Real app]** Importer `iis-w3c`. A text file with a `#Fields:` header; IIS-format and NCSA-format logs have no such header and are not detected as W3C. Times are UTC.
- **[Vendor doc]** Make sure these fields are logged: date, time, method, URI stem, status, **sub-status**, **win32 status**, bytes sent and received, **time-taken**, port. Add `X-Forwarded-For` as a custom field when a load balancer sits in front. Verify the newest file time, `time-taken` in the header, and free space.
- **[Real app]** Limits: 2 GiB, 10 million rows per file; the reject limit is 5% of rows.

**Exact fields read. [Real app]** (`iis/w3c.go`) `date`, `time`, `cs-method`, `cs-uri-stem`, `sc-status`, `sc-substatus`, `sc-win32-status`, `time-taken`, `s-port`, `s-sitename`, `s-computername`, `cs-username` (pseudonymised), `sc-bytes`, `cs-bytes`. Other columns, including the query string and client address, are not kept.

**Question it answers.** How many requests did the web server see, how many failed (4xx, 5xx), and how long did each take from IIS's point of view?

**Cannot tell.** What the application did. Caveat: `time-taken` includes sending the response to the client, so it can exceed the application's own time. **[Real app]** the importer marks the `time` field's meaning (start or end) as **unverified** and assumes it is the request end; the IIS owner must confirm.

**Feeds.**
- Figure cards: Web server errors (IIS 5xx), Slow web requests (IIS P95). Lane: Web server (IIS). Tile: Web server.
- Suspects: **Q2** (audit versus IIS), **S2** (outside time: IIS time minus audit time), **S9** (request-filtering rejections, by `sc-substatus`), **S12** (time outside the application up while the application is flat).
- **[Real app]** also gives time outside the application (O) in the time split, by matching IIS rows to audit rows.

**Example.** "210 web server errors, 4.4% of 4,816 requests." (demo)

## 4. HTTP.sys error log

**One line.** The Windows kernel's log of connections it dropped or refused before the application saw them, each with a reason.

**Where from.** **[Real app]** Importer `iis-httperr`. **[Vendor doc]** `%SystemRoot%\System32\LogFiles\HTTPERR`, on by default. Date and time are UTC. 64 MiB and 1 million rows per file.

**Fields read. [Real app]** (`iis/httperr.go`) date and time, `s-reason`, status, method, URI stem, queue name, site id. Reasons are matched against an allowlist (for example `QueueFull`, `Timer_AppPool`, `AppPoolTimer`, `Timer_HeaderWait`, `Timer_EntityBody`, `Timer_MinBytesPerSecond`, `Connection_Dropped`, `AppOffline`, `AppShutdown`); anything else is kept as "other".

**Question it answers.** Did requests die before reaching the application, and for what reason: full queue, app pool trouble, slow client, receive timeout?

**Cannot tell.** How many requests were fine. Resolution is one second, so order inside a second is lost.

**Feeds. [Prototype]** Lane: web connection errors (HTTP.sys). Suspects **S1** (queue backup), **S10** (app pool unavailable), **S11** (header or body receive timeout), **S12** (slow client or proxy). A burst counts at 3 rows in 5 minutes or 1% of requests.

**Example.** "9 `Timer_HeaderWait` rows in the window against 1 before: a receive-timeout suspect." (illustrative)

## 5. IIS failed request traces

**One line.** A detailed per-request trace of which IIS module held the request longest, kept only for requests that match a rule.

**Where from.** **[Real app]** Importer `iis-freb` (files named `fr######.xml`). **[Vendor doc]** Optional; larger files and short retention; for example a rule on `*` with `time-taken` over 5,000 ms.

**Fields read. [Real app]** (`iis/freb.go`) from `failedRequest`: URL (path only), verb, status, trigger status, time taken, failure reason. From trace events: time, computer, module name, notification; the importer derives "slow-module" rows (the modules with the longest gap between two events). Message text is never read.

**Question it answers.** Inside IIS, which module was slow for this request?

**Cannot tell.** Anything for requests that did not match the rule.

**Feeds.** No suspect in the prototype reads it. It is an extra lane source in the real importer set.

## 6. SQL Server extended events

**One line.** SQL Server's event recorder: deadlocks, long waits, severe errors, and (if set up) slow calls and blocked-process reports.

**Where from and what must be on.**
- **[Real app]** Importer `sql-xe` reads a **CSV export of `system_health`** events. `system_health` is on by default but keeps little. It logs lock waits over **30 s**, latch waits over **15 s** and preemptive waits over **5 s**. A shorter wait never appears, so absence is never worded as "SQL was not waiting".
- **[Vendor doc]** Copy `system_health*.xel` off the server regularly. For blocked-process reports, set `blocked process threshold (s)` to 5 or more and create your own session (`system_health` does not keep it). For per-call outliers create a session with `rpc_completed` and `sql_batch_completed` where `duration >= 1000000` microseconds (1 s), plus `attention`, `error_reported` (severity 11 or more) and `database_file_size_change`. Statement text is deliberately not captured. A DBA runs these scripts; Unravis does not.

**Exact fields read. [Real app]** (`sqlserver/xe.go`) Columns `timestamp_utc`, event `name`, `wait_type`, `duration`, `component`, `state`, `error_number`, `severity`, `server_instance_name`. Event names kept: `wait_info`, `wait_info_external`, `sp_server_diagnostics_component_result`, `error_reported`, three `scheduler_monitor_non_yielding_*` events and `xml_deadlock_report`. Any other event is counted, not imported. A file with a column that could hold SQL text, user names, host names or database names is **refused**.

**Gap.** **[Real app]** no shipped importer reads `sql_batch_completed`, `rpc_completed` or `blocked_process_report`. The prototype's "slow database calls (over 1 s)" and "blocked-process reports" assume the extra sessions from **[Vendor doc]** section 9.3 and an importer for them.

**Question it answers.** Was there a deadlock, a long lock or latch wait, a severe error, or a stuck scheduler? **[Prototype]** also: how many database calls were slow, and did commands time out (`attention`)?

**Cannot tell.** Short waits; which statement (text is never read).

**Feeds.**
- Figure cards: Slow database calls, Database errors and blocking. Lane: Database (SQL Server). Tile: Database.
- Suspects: **D1** (blocking chain), **D2** (deadlock storm), **D16** (command timeout burst), **D17** (login storm, `error_reported` 18456).

**Example.** "11 blocked-process reports against 0 before, top wait is a lock wait: D1 is consistent." (demo)

## 7. SQL error log

**One line.** SQL Server's own text log of what happened to the server.

**Where from.** **[Real app]** Importer `sql-errorlog` (file `ERRORLOG`) and a live read through `sp_readerrorlog`. Permission: `VIEW ANY ERROR LOG` on SQL Server 2022; on 2019 and earlier no narrower grant exists, so the check stays "Cannot test yet" unless the owner allows `securityadmin`. **[Vendor doc]** keep 30 or more error logs.

**Fields read. [Real app]** (`sqlserver/errorlog.go`) Only an allowlist, never the message text: start and stop lines; message 833 (I/O requests taking longer than N seconds, with the count); message 17883 (a non-yielding scheduler); `Error: n, Severity: s, State`; `Login failed` (counted only). Lines are in the **server's local time**; the `UTC adjustment` line is used.

**Question it answers.** Did the server restart, report slow disk I/O, or log severe errors or failed logins?

**Cannot tell.** Anything the server did not log; the cause of an error (text is dropped).

**Feeds.** **[Real app]** answer D5 (restart) in Step 7. No suspect in the prototype uses it directly.

## 8. Query Store

**One line.** A black-box recorder inside each database that stores query timings per time interval.

**Where from and what must be on.**
- **[Real app]** Importer `sql-querystore` reads a CSV or JSON export; a live check reads `sys.query_store_*`. Permission: `VIEW DATABASE PERFORMANCE STATE` (2022) or `VIEW DATABASE STATE` (2019 and earlier).
- **[Vendor doc]** Must be on and `actual_state_desc = READ_WRITE` (`READ_ONLY` means it is full and has silently stopped). Default interval is 60 minutes, which is too coarse for a sudden jump: 15 minutes (5 for a short window) is recommended, with wait-stats capture ON. Run `sp_query_store_flush_db` before exporting. The research recommends a 30-day stale threshold. There is no history from before it was switched on.

**Exact fields read. [Real app]** (`sqlserver/querystore.go`) `start_time`, `end_time` (UTC), `query_id`, `plan_id`, `count_executions`, `avg_duration` (microseconds, converted to ms), `max_duration`, `avg_cpu_time`, `avg_logical_io_reads`, `avg_rowcount`, `last_execution_time`, `execution_type`, `is_forced_plan`, `wait_category_desc`, `avg_query_wait_time`. Query text and plans are **never read**.

**Question it answers.** Which queries got slower, did a plan change, and which wait category grew, per interval?

**Cannot tell.** The exact minute inside an interval (each row is an average). A request still running at the time (it appears only after finishing). Queries it did not capture.

**Feeds.**
- **[Real app]** answers D3, D14, D15 (plan and query regressions).
- **[Prototype]** per-database P95 history for "Where does the evidence point?". If the interval is longer than the investigation window, evidence is capped at "Some evidence".

**Example.** "Interval 60 minutes, window 30 minutes: Query Store shows the hour, not the minute. Evidence capped at Some." (prototype rule)

## 9. SQL server counters and DMV snapshots (waits, file I/O, schedulers, memory grants)

**One line.** Server-wide meters: what SQL Server has been waiting on, how fast its disks answer, whether CPU schedulers queue, whether queries wait for memory.

**Where from and what must be on.**
- **[Real app]** Two things exist today. (1) Live **health checks** (`internal/dbhealth/sql.go`, "present moment") read: version and uptime, a CPU ring buffer (last 30 minutes), top waits since start (`sys.dm_os_wait_stats`), memory (page life expectancy, Memory Grants Pending), running requests that are blocked or over 30 s, connections, heaviest queries, Query Store, missing indexes, backups, settings, deadlock count and failed Agent jobs. (2) Importer `sql-pdh` reads Windows performance counter CSVs for SQL Server (`SQLServer:` or `MSSQL$<name>:` objects).
- **Permission:** `VIEW SERVER PERFORMANCE STATE` (2022) or `VIEW SERVER STATE` (2019 and earlier); read on `msdb` (SQLAgentReaderRole) for jobs and backups.
- **Gap.** **[Real app]** no shipped collector samples these over time, and no shipped code reads `sys.dm_io_virtual_file_stats`. **[Vendor doc]** calls this the highest-value gap: a job every 30 to 60 s keeping **raw cumulative values** with a capture time and the server start time, so differences and restarts can be handled. It proposes the Blackbox runs it in a later release. **[Prototype]** simulates it as a "Blackbox sampler" and shows no numbers when none ran.

**Why raw cumulative values.** Wait and I/O counters count since the last restart. A single reading says nothing about a past minute; only the difference between two samples does.

**Question it answers.** Was the server (not one query) held back by locks, disk, CPU or memory during the window?

**Cannot tell.** Which database or statement, from server-wide totals alone. Any past window without samples.

**Feeds. [Prototype]** (`SUS_SRC`)
- **D1** (top wait is a lock wait, `dm_os_wait_stats`), **D6** (memory grants pending, `dm_exec_query_memory_grants`), **D9** (disk latency 15 ms line or a rise of 30% and 5 ms, `dm_io_virtual_file_stats`), **D10** (runnable queue on half the schedulers for 5 samples, `dm_os_schedulers`).
- "Where does the evidence point?": disk latency, CPU, and each database's share of file I/O, including the unnamed **Other databases** row.

**Example.** "Disk latency 38 ms (earlier 5 ms) while two databases slowed: a shared resource looks saturated." (prototype, "Whole server" scenario)

## 10. MongoDB slow-operation log

**One line.** The MongoDB server's text log. Every operation slower than `slowms` is written as a "Slow query" line.

**Where from and what must be on.**
- **[Real app]** Importer `mongod-json` reads structured JSON logs (MongoDB 4.4 and later). The older plain-text format is not supported.
- **[Vendor doc]** On by default at `slowms` 100 ms. Keep verbosity at 0 (higher logs every operation). Rotate daily and keep 14 to 30 days. From MongoDB 8.0 the slow decision uses `workingMillis`, which excludes lock and flow-control waits, so always read `durationMillis` too.

**Fields read. [Real app]** (`mongodlog`) timestamp, `durationMillis` (operation time), and an allowlist: `type`, command name only, `planSummary`, `keysExamined`, `docsExamined`, `nreturned`, `numYields`, `queryHash`, `workingMs`. Command bodies, filters, client, remote and application names are dropped. Sampling or a changed threshold inside the file is noted.

**Question it answers.** Which operations were slow, how slow, and were they scanning many documents?

**Cannot tell.** Operations under the threshold; what the application was doing.

**Feeds.** **[Prototype]** the Mongo lane and signal "slow audit writes" (it needs the profiler to isolate the insert on the audit collection, so S5 stays grey without it). Step 8 slow-operation answer in the guided analysis.

## 11. MongoDB profiler

**One line.** MongoDB's built-in slow-operation recorder that keeps entries in a collection (`system.profile`).

**Where from and what must be on.** **[Vendor doc]** Off by default. Optional: level 1 with `slowms` 50 to 100 on the **audit database only**, after recreating `system.profile` as a capped collection of about 100 MB (default is 1 MB). It adds writes to the node being diagnosed. A short maintenance window is needed; the switch-on script replaces the collection so older records are lost. **[Real app]** the profiler guide shows three scripts (check, switch on, switch off); Unravis never switches it on. Read login needs `read` on the audit database and `clusterMonitor`. Live reads are restricted to a fixed read-only list (it may read the profiling level and `find` on `system.profile`).

**Fields read. [Real app]** (`mongoexports/profile.go`, importer `mongo-profile`, one JSON line per entry) `op`, `millis`, `ts`, command name, `planSummary`, `keysExamined`, `docsExamined`, `nreturned`, `numYields`, `queryHash`, `workingMs`, `responseLength`. The namespace is dropped.

**Gap.** `usedDisk` (spill to disk) is not in the shipped allowlist, yet the prototype's suspect **M11** reads it.

**Question it answers.** Which operations were slow, as a stored and queryable record; how long did the insert on the audit collection take (to tell audit coupling apart)?

**Cannot tell.** Anything older than the capped collection. The profiler keeps only the latest entries, so MongoDB is "Not by day" in Add another day.

**Feeds. [Prototype]** **S5** (audit-write coupling), **M11** (spill to disk). Without it: grey with "MongoDB profiler switched on" listed as the need.

## 12. MongoDB serverStatus

**One line.** A snapshot of the MongoDB server's counters, taken by a command.

**Where from.** **[Real app]** Importer `mongo-serverstatus`: one projected JSON line per snapshot (the owner runs a `mongosh` loop, for example every 15 s, appending to a file). Role `clusterMonitor` on `admin`. **[Vendor doc]** keep **raw cumulative values**, compute deltas later, and discard a delta that spans a restart (uptime going down).

**Exact fields read. [Real app]** (`serverstatus.go`) `uptime`; `connections.current`, `.available`; `globalLock.currentQueue.total/readers/writers`; `globalLock.activeClients.readers/writers`; `opcounters` (query, insert, update, delete, getmore, command); `mem.resident`, `.virtual`; `wiredTiger.cache` (bytes in cache, dirty bytes, maximum bytes); `wiredTiger.concurrentTransactions` read and write (`out`, `available`).

**Gap.** `connections.rejected` and `opLatencies` are not in the shipped projection; the prototype's suspect **M12** (connection rejection, MongoDB 6.3 or later) assumes the former.

**Question it answers.** Were connections, queues or tickets exhausted, and was the cache under pressure?

**Cannot tell.** Which operation; anything for a past window without samples. **[Vendor doc]** from MongoDB 7.0 "no free tickets" is not a queue; only a waiting queue counts.

**Feeds. [Prototype]** **M5** (ticket or lock queue), **M12** (connection rejection), and Mongo signals in "Where does the evidence point?".

## 13. MongoDB top (not read)

**[Real app]** No importer, health check or suspect reads the `top` command. It is listed only because it was asked about. Do not describe it as a Unravis source.

## 14. MongoDB currentOp

**One line.** A list of the operations running on MongoDB at this instant.

**Where from.** **[Real app]** Importer `mongo-currentop` (one JSON line per active operation) and a live health check. **[Vendor doc]** `$currentOp` with `active: true, secs_running >= 1`, every 10 to 30 s **only while degraded**; moderate cost on a busy server.

**Fields read. [Real app]** `op`, `secs_running` (as the duration), `waitingForLock`, `numYields`, `planSummary`. Namespace is dropped on import. Client and command bodies are dropped too.

**Question it answers.** What is running, and what is waiting on a lock, right now?

**Cannot tell.** The past: it is **not retroactive**; it helps the next incident only if sampled during it.

**Feeds.** Supports **M5** and **M10** ("current operations by namespace" is their cheapest check). `mongostat` (`mongostat-json`) is a cheaper alternative sampler: columns insert, query, update, delete, getmore, command, dirty, used, flushes, vsize, res, qrw, arw, net_in, net_out, conn.

**Example.** "One operation waiting 40 s for a lock while a nightly job runs: a suspect to check, not proof." (illustrative)

## 15. Windows host counters and event log (later source)

**One line.** The machine's own meters (CPU, memory, disk, network) and its system event log.

**Status.** In the Investigation Timeline demo this is **not collected**, so host suspects are grey and the screen says what to add.

**Where from.**
- **[Real app] Counters.** Importer `win-counters` / `sql-pdh` reads `relog` PDH-CSV files. The Blackbox (Windows service) samples host health into hourly NDJSON files: `cpu` percent, `memAvail`, `memCommit`, `diskQueue`, `netIn`, `netOut`, `diskFreePct`, the top 20 processes, and a catalogue of log files (names, sizes, times only). A missing value is left out, never written as zero.
- **[Real app] Event log.** Importer `win-events` reads `Get-WinEvent | Export-Csv` with columns `TimeCreatedUtc` (or a local time with the picked host's zone), `MachineName`, `LogName`, `ProviderName`, `Id`, `Level` and optionally `AppPool`. The message text is dropped. It classifies application-pool recycles and failures from the `Microsoft-Windows-WAS` provider, application crashes and hangs (ids 1000 to 1002), service crashes, unexpected shutdown, low memory and ASP.NET unhandled errors. These ids are cited to public pages but "have not been checked against a real export".
- **[Vendor doc]** One collector every 15 s to a separate disk: Processor, Memory, `LogicalDisk\Avg. Disk sec/Read|Write`, Processor Queue Length, TCP retransmits, `w3wp` CPU, `ASP.NET\Requests Queued`, `HTTP Service Request Queues`, plus SQL counters. For recycles, set `recycling.logEventOnRecycle`. If the MongoDB service reads host counters, its account needs the **Performance Monitor Users** and **Performance Log Users** groups.

**Question it answers.** Was the machine itself busy (CPU 80% or more), short of memory, or did the pool recycle or crash?

**Cannot tell.** Which request or query used the resource. A hypervisor's own steal time is not visible from inside a guest.

**Feeds. [Prototype]** **H1** (host CPU, 80% line), **H6** (shared effect across tiers, needs a second workload measured), **S2** (thread-pool starvation, needs .NET counters). All grey in the demo.

## 16. Typed-in change list

**One line.** A list of releases and changes that you type in, so they can be set beside the start of the slowdown.

**Where from.** **[Real app]** Step 5. Each entry is a time and a label you typed (`Release{At, Label}`); labels are shown exactly as typed. An empty list entered on purpose reads "No change listed"; a list never entered reads "Cannot test yet". The list is hand-typed, so the step always says so.

**Rule.** A change **leads** the start when it sits within **30 minutes before** the start (t0) and not after it. Overlapping is not leading. **[Prototype]** uses the same 30 minutes.

**Question it answers.** Did something change just before it got slow?

**Cannot tell.** Whether the change caused anything ("A change before the start is a lead, not proof"). Changes nobody typed.

**Feeds.** Suspect **C1** (deploy, configuration or infrastructure change). Grey in the main demo because no list was entered.

**Example.** "Deploy 09:10; slowdown starts 09:14: a lead of 4 minutes." (illustrative)

---

## 17. Which suspects need which source

| Source | Suspects it feeds |
|---|---|
| API audit log | Q2, L1, L2, S5, S8 and the first signal of most others |
| IIS W3C log | Q2, S2, S9, S12 |
| HTTP.sys error log | S1, S10, S11, S12 |
| SQL extended events | D1, D2, D16, D17 |
| SQL counters and DMV samples | D1 (lock wait), D6, D9, D10 |
| Query Store | D3, D14, D15 (real); evidence-strength history (prototype) |
| MongoDB slow log, profiler, serverStatus, currentOp | S5, M5, M10, M11, M12 |
| Host counters and event log | H1, H6, S2 |
| Typed change list | C1 |

The real rule catalog has 44 entries (42 active suspects); the prototype shows 25 of them (16 from the existing guided analysis, 9 added from the research report). A suspect never names a cause. It lists the evidence for and against and the cheapest next check.
