# Unravis: investigating a sudden P95 jump across API audit, MongoDB and SQL Server

Status: design draft for review. Nothing here is built yet. Not committed.

Tags used below: **[doc]** = checked against official docs by a council member or me; **[exp]** = practitioner experience, widely held but not from one official page; **[verify]** = field name, default or version detail that must be checked on your servers before the product relies on it.

Product wording rule (applies to every card the engine shows): plain, non-causal. Say "consistent with", "points to", "does not fit". No percentages, no scores, no single "root cause" label. This matches the existing Ledger "Compare theories" matrix and `evidence.LintCausal`.

---

## 0. How this was produced

The same generic brief (no private data) went to a council. Results:

| Member | Result |
|---|---|
| Claude (Sonnet) A, data sources | Strong. Checked FTDC, Query Store and slowms against official pages. |
| Claude (Sonnet) B, hypotheses | Strong. 40 hypotheses, decomposition D = A + C + P + R, good Little's Law section. |
| Cursor | Short, shallow, but agrees on the main points. |
| Kilo Nemotron Ultra | Long. Useful decision tree. Some invented or doubtful field names (see section 8), one malformed Query Store command. Used with care. |
| Droid | Finished, but its answer was cut off after the MongoDB collection section (Part 1 only). Agrees with the rest; adds: Atlas downloadable logs keep 30 days and Free/Flex tiers have none, record effective write concern (`w`, `j`, `wtimeoutMS`) and pool limits per run, and a starting retention of 30 days raw / 90 days incident bundles. |
| Antigravity | Failed: account quota reached. |
| Manus | Failed: the remote task stopped to ask for input. |
| Muse | Not tried (the hub entry is a browser to muse.ai, probably not an AI chat). |

Where members disagreed I took the claim backed by an official page. Disagreements that matter are listed in section 8.

---

## 1. What we have today

### 1.1 In the API audit record (always on, usually targeted)

Per request: start time (`InitiatedAt`), completion time, `DurationMs`, `AuditCreateTimeMs` (A), `ApiTimeMs` (C), `PayloadRecordTimeMs` (P), controller, action, outcome, source id.

**What the timestamps really measure (read from `origin/uat` of brain-payroll, refs `c01578c3a61` (22 Oct 2026) and `1730dccda54` (26 Oct 2026, after a VPN fetch; no relevant change between them), and confirmed unchanged from the older 5 Oct checkout; Web API action filter `RequestAndResponseLogHandler`, .NET Framework 4.7.2 on IIS).** All are `DateTime.UtcNow` values taken **inside the action filter**, not at the start of the HTTP request. The filter is the first global action filter (`WebApiConfig.cs:49`):

| Field | Computed as | What it covers |
|---|---|---|
| `RequestInitiatedAt` | first line of `OnActionExecuting` | the moment the audit filter runs. Everything before it is **outside D**: HTTP.sys, IIS, message handlers, authentication and authorization filters, model binding |
| `CreatedOn` (as stored) | the filter stamps it into a request header **after** the skeleton insert returns; `OnActionExecuted` reads that header back | the end of the audit set-up **including the synchronous Mongo insert** |
| A `AuditCreateTime` | `CreatedOn − RequestInitiatedAt` | audit set-up work **plus the synchronous Mongo insert of the skeleton record** (the first skeleton row holds a smaller value taken before the insert; the final update overwrites it) |
| C `ApiTime` | `RequestCompletedAt − CreatedOn` | the rest of the action-filter chain (`ValidateActivePermission`, `ValidateRequestAccessIds`, ...) plus the action itself (SQL, downstream calls, code). **Not** the Mongo insert |
| `RequestCompletedAt` | start of `OnActionExecuted` | end of the action, before payload capture |
| P `PayloadRecordTime` | `UpdatedOn − RequestCompletedAt` | reading and sanitising request/response bodies (blocking reads); runs **after** `RequestCompletedAt` |
| final audit update | background queue, drained every 10 s (`ThreadSleepTimeMillisecond`), sync `ReplaceOne` | **not measured**, outside the response |

**Correction (26 Oct, later the same day).** An earlier version of this table said the Mongo insert was inside C. That was wrong: I read `CreateApiAuditLogs` (which sets `CreatedOn` before the insert) and missed the header line that re-stamps `CreatedOn` after the insert (`RequestAndResponseLogHandler.cs:181`, present in the older checkout as well, line 105). The stored values follow the header, so **a slow audit insert shows up in A, not C**.

Consequences:
1. **D = `RequestCompletedAt − RequestInitiatedAt` = A + C exactly.** So "Other time R = D − A − C" is **zero by construction** and cannot show queueing. P is outside D.
2. **A is the audit overhead and includes the Mongo insert.** A large A means audit set-up (user lookup, config lookup, StructureMap instance, client IP) or the Mongo insert is slow. A slow insert slows the user, and it is visible as A, separate from C (SQL and code). The split inside A (set-up versus insert) is not stored; the Mongo server lane tells them apart.
3. **The audit duration understates what the user waited.** Time before the filter (HTTP.sys queue, IIS thread-pool queue, authentication, authorization, model binding) and after it (P, response serialisation, sending) is missing. The new "outside time" is **O = IIS `time-taken` − D − P**, per request where the IIS row can be matched (same time, method, path), or per minute as an aggregate difference. IIS `time-taken` includes the time to send to the client, so a persistent offset is normal; a **change** in O is the signal. This is why the IIS log (9.4) is needed for the queue-before-code hypotheses (S1, S2, S3).
4. The update is in the background and can lag, but that lag is not request latency. A saturated background writer (bag growing) is a separate memory/ThreadPool concern (S2).
5. Blocking reads in `OnActionExecuted` (`AsyncHelper.RunSync` on the response body, synchronous request body read) hold a request thread; under thread-pool starvation they make things worse (S2).

**`PreActionFilterTimings` (new on `origin/uat`, not in the older checkout).** Class `ApiAuditPreActionTimingAttribute` (`BrainPayroll.Shared.Api/Helpers/ApiAuditPreActionTimingAttribute.cs`), registered last in `WebApiConfig.cs:56`. On .NET Framework it replaces the Web API action invoker and wraps every authentication, authorization and action filter (global, controller, action) so each is timed with `Stopwatch` from its start until it calls the next step. Each filter that took **1 ms or more** is stored as `{FilterName, ElapsedMilliseconds}`; `PreActionFilterTotalMilliseconds` is their sum. Nothing is timed when audit logging is off. The code comment says `ApiTime` is unchanged. Meaning for the model:
- Authentication and authorization filters run **before** `RequestInitiatedAt`, so their time is part of **O**, not D.
- Action filters registered after the audit filter run **after** `CreatedOn`, so their time is part of **C**. Subtracting gives `C − (sum of those) ≈ the action body`.
- The array mixes both groups, so do not add the total to A, C or D. Split it by `FilterName` (see which names are authentication/authorization versus action filters) before using it.
- The attribute does not time model binding or the action body. It is null when no filter took 1 ms (the 20 newest sandbox documents sampled had null).

**What real sandbox arrays contain (26 Oct 2026, read-only aggregation to 18:07Z: 3549 of 3790 documents carry the array, none empty).** Per filter, entries | p50 | p95 | max ms:

| Group | Filter | Entries | p50 | p95 | max |
|---|---|---|---|---|---|
| Before D (part of O) | `BrainPayrollTokenAuthorizeAttribute` | 2429 | 55 | 107 | 251 |
| Before D (part of O) | `BaseTokenAuthorize`, `EmployeeTokenAuthorizeAttribute`, `RefTokenAuthorizeAttribute`, `MailMergeTokenAuthorizeAttribute` | 49, 15, 4, 2 | 55 to 81 | 83 to 200 | 92 to 239 |
| Audit filter itself | `RequestAndResponseLogHandler` | 3575 | 2.2 | 106 | 81 190 |
| Inside C | `ValidateActivePermissionAttribute` | 2476 | 30.5 | 4588 | 181 349 |
| Inside C | `ValidateRequestAccessIdsAttribute` | 713 | 21.2 | 93 | 2894 |
| Inside C | `PermissionAuthenticationAttribute`, `ValidatePermissionAttribute`, `NotifySharpAttribute`, `ReplacePropertyCharactersFilterAttribute`, `AutoExecuteApiMigrationAttribute`, `ApplicationFileExtensionAttribute`, `EmployeeNotifySharpAttribute`, `RoleAuthorizeAttribute` | 1 to 469 each | 1 to 44 | 12 to 1432 | up to 1648 |

Reading it:
- The common order is token authorize > `RequestAndResponseLogHandler` > `ValidateActivePermissionAttribute` (1229 documents). 985 documents hold only `RequestAndResponseLogHandler`. The audit filter's own entry appears 3575 times in 3549 documents, so it repeats in some.
- The total exceeds `ApiTime` in 66% of documents (2344 of 3549). That is expected: token authorize time sits before D, so the total is not a part of C. Never subtract the total from C; subtract only the names in the "Inside C" group.
- The three slowest requests of the day (about 169 to 181 s) are about 99.9% `ValidateActivePermissionAttribute`, while A is only 1.7 to 4 ms in them. So today's long stalls are in the permission check, not the Mongo audit insert. The code path is `PermissionHelperService.ValidateActiveUserPermissions`, which calls `IEffectivePermissionService.HasAnyPermission(userId, controller, action)` from the `BTL.RoleAndPermission` package. Its backing store (Redis, SQL or cache) is **not verified**; the package source was not read.
- The one audit-insert stall (max 81 s) shows as a `RequestAndResponseLogHandler` outlier, which is the Mongo insert lane (M-group) and is rare compared with the permission stalls.
- Resolved: the 452 `sum(array) != PreActionFilterTotalMilliseconds` cases were floating-point noise. With a 0.001 ms tolerance there are 0 mismatches, so the total is exactly the sum of the array. Compare with a tolerance, never `!=`.

**Day totals (26 Oct 2026, to about 19:00Z; 4177 documents, 3933 with the array, no `StatusCode` of 500, 401 or 403 anywhere).** Read-only aggregation, shares are of the sum of `ApiTime`:

| Filter | Entries | Sum ms | % of all `ApiTime` | % of `ApiTime` in the 159 documents of 5 s or more |
|---|---|---|---|---|
| `ValidateActivePermissionAttribute` | 2808 | 3 951 980 | **67.5** | **69.8** |
| `RequestAndResponseLogHandler` | 3959 | 683 813 | 11.7 | 2.6 |
| `BrainPayrollTokenAuthorizeAttribute` (before D) | 2761 | 170 043 | 2.9 | 0.2 |
| `ValidateRequestAccessIdsAttribute` | 843 | 38 029 | 0.6 | 0.2 |
| all others | | each under 8 000 | 0.1 or less | 0 |

- `ApiTime` over all documents: p50 66 ms, p95 4.2 s, p99 20.6 s, max 252 s (approximate percentiles). Documents without the array: p50 5.5 ms, p95 128 ms.
- **The three slowest requests (252, 202, 181 s) were one shared stall, not three loners.** All started 16:00 to 16:01Z. In 16:01 to 16:03 five requests were in flight, almost nothing new arrived and nothing completed. In 16:04 eighteen completed together (13 with `ValidateActivePermissionAttribute` of 1 s or more, p50 7.2 s). Same shape as the earlier stalls: something blocked, then released at one moment.
- Permission-check slowness of 1 s or more: 399 requests, in 91 distinct start minutes, clustered (top minutes hold 8 to 14 each), spread over 69 routes (mostly heavy dashboard and lookup GETs, 30 to 60% of those routes' calls). Not one route: the same filter slows unrelated routes in the same minutes, which points at the shared dependency behind the filter, not at the actions.
- Load did not cause it: during the stall λ was 0 to 4 per minute (about 0.06 per second on average over the day).

Derived by Unravis: call spans, clock anomalies (end before start, spans over 6 h), per-minute counts, percentiles, reference-window comparison. Any column that assumed R = D − A − C must be re-labelled (see Step 3).

Limits to remember:
- It is targeted: audits cover chosen routes or tenants, so arrival rate λ is only the audited slice unless the window or scope is widened. Widen it **before** the next incident, not after.
- No row for requests that never finished (a request still stuck is invisible until it ends).

### 1.2 Importers already in the app (evidence kinds)

| Lane | Importers (per my notes; verify the exact formats each accepts) |
|---|---|
| SQL Server | `sql-xe` (Extended Events files), `sql-errorlog`, `sql-pdh` (perf counters), `sql-querystore` |
| MongoDB | `mongod-json` (server log), `mongo-profile`, `mongo-currentop`, `mongo-serverstatus`, `mongostat-json` |
| Redis | `redis-slowlog`, `redis-latency`, `redis-info`, `redis-log` |
| IIS | `iis-w3c`, `iis-httperr`, `iis-freb` |
| App and host | app-log classifiers (sql-pool-timeout, sql-exec-timeout, mongo-selection-timeout, mongo-pool-wait, redis-timeout, redis-connection), `dotnetcounters`, `wincounters`, `winevents`, `procsnap`, `appinsights`, `schedule` (cron, Hangfire, b3, Agent), log4net/serilog/MEL parsers |

Screens that already use them: Call trace (per-call lane view with checks consistent / contradicted / not-enough-data / not-testable), Nearby activity, Case home, Health checks, Ledger with "Compare theories".

### 1.3 Gaps against what the investigation needs

| Needed | Today | Gap |
|---|---|---|
| SQL wait-stat **deltas**, file IO stalls, requests and blocking snapshots | Query Store waits, XE, PDH | No importer for DMV snapshot tables. This is the "timer-based SQL snapshot collector" already on the open list. Highest value gap. |
| SQL `blocked_process_report` and deadlock graphs | `sql-xe` may read them if the session exists | The blocked-process XE session must be created on the server (system_health does not keep it). |
| Mongo FTDC (`diagnostic.data`) | none | Binary format. Needs MongoDB tooling or a decoder. Treat as an off-line step: decode outside, import the CSV through `mongo-serverstatus`-like path. |
| Mongo `replSetGetStatus` timeline (lag, elections) | election lines in `mongod-json` only | Add a small sampler or accept log lines only. |
| Driver pool events (checkout wait, pool exhausted) | text classifiers only | Needs the API to log driver CMAP events (section 2.3). |
| SqlClient pool counters | `dotnetcounters` may carry them if the EventSource is listed | Verify counter names are collected. |
| Seasonal baseline (3+ days) | reference window only | Fine for now; baseline needs history. |

---

## 2. What to enable or collect

Principle from the council: **most of it is already on. The risk is that it rolls over or is never exported.** Order below is by value per effort.

### 2.1 MongoDB (you asked what to enable)

Short answer: very little must be switched on. Do these:

1. **Keep the mongod log, and make sure the slow-operation lines exist.** Slow operations are written to the log whenever they exceed `slowms` (default 100 ms). Lines carry `durationMillis` and, in 8.0+, `workingMillis`. Keep verbosity at 0 (higher logs every operation). Rotate daily, keep 14 to 30 days. **[doc]**
   - Version caveat: from 8.0 the slow threshold is judged on `workingMillis`, which excludes time spent waiting for locks or flow control. Always read `durationMillis` too, or lock waits are invisible. **[doc]**
2. **Copy `diagnostic.data` (FTDC) off the server on a schedule** (for example hourly). It is on by default, samples about every second, includes `serverStatus`, `replSetGetStatus`, connection pool stats and host CPU/disk/memory, but the folder is capped (250 MB; 500 MB from 8.3) and the oldest files are overwritten, so it covers roughly days. It holds no query text. On Windows the service account needs Performance Monitor Users and Performance Log Users membership or the host counters are missing. **[doc]**
3. **A small sampler for `serverStatus` every 10 to 15 s**, stored with raw cumulative values (we compute deltas). Fields to keep: `connections.current/available/active/totalCreated`, `globalLock.currentQueue.*`, `opcounters`, `opLatencies`, `wiredTiger.cache` (dirty bytes, bytes in cache, pages evicted by application threads), tickets (`wiredTiger.concurrentTransactions` before 8.0; `queues.execution` from 8.0), `flowControl.*`, `repl.*`. **[doc]** for the sections, **[verify]** the exact leaf names on your version. `mongostat` writing CSV is a cheaper alternative with fewer fields.
4. **Profiler**: do not use level 2 in production. If you want a stored, queryable record, use level 1 with `slowms` 50 to 100 on the **audit database only**, and first resize `system.profile` (default 1 MB, a few thousand entries) by dropping and recreating it as a capped collection of about 100 MB. The profiler adds writes to the node you are diagnosing. The log already gives the same information with no write cost, so the profiler is optional. **[doc]**
5. **Replica set status** every 30 to 60 s (`rs.status()`): member state, `optimeDate` lag, `electionDate`, ping. Election and role-change lines are also in the log. **[doc]**
6. **`currentOp` snapshots only while the system is degraded** (every 10 to 30 s, `secs_running ≥ 1`). Not retroactive; useful for the next incident. **[exp]**
7. **API-side driver events** (section 2.3). These are the only way to separate "pool wait" from "server time" for the audit write.
8. **Atlas versus self-hosted**: Atlas gives no FTDC file access (support only) and metrics at about 1 minute with limited fine-grained retention; self-hosted gives everything above. **I do not know which you run. This changes the plan: please tell me.** **[verify]**

Overhead: log and FTDC negligible; sampler negligible; profiler level 1 low but not zero.

### 2.2 SQL Server

You already enabled Query Store. Adjust:

1. **Query Store settings** (per database). Defaults that matter: interval length **60 minutes** (too coarse for a sudden jump), capture mode AUTO (2019+; ALL on 2016/2017), wait-stats capture ON by default from 2017 with no back-fill, data flush every 900 s. Recommended: `INTERVAL_LENGTH_MINUTES = 15` (5 for a short investigation window; smaller means faster storage growth), `WAIT_STATS_CAPTURE_MODE = ON`, `MAX_STORAGE_SIZE_MB = 1000` to `2048`, cleanup AUTO, stale threshold 30 days, `MAX_PLANS_PER_QUERY = 200`. Before exporting for analysis run `sp_query_store_flush_db`. Check `sys.database_query_store_options`: `actual_state_desc = READ_ONLY` means it is full and **silently stopped capturing**. **[doc]**
   - Reading rule: Query Store stores per-interval averages of **completed** statements. A request still running at the incident minute shows up only after it finishes. Waits are grouped into categories per plan, not raw wait types. **[doc]**
   - Since it was just enabled there is no history before that date. The seasonal baseline cannot use Query Store yet.
2. **system_health Extended Events** are on by default (deadlock graphs, long waits, non-yielding schedulers, severe errors). Retention is small (4 files × 5 MB on some editions). **Copy `system_health*.xel` off the server regularly.** **[doc]**
3. **Blocked process report**: `sp_configure 'blocked process threshold (s)', 5; RECONFIGURE` (use 5 or more; no restart), then create **your own** XE session capturing `sqlserver.blocked_process_report` to an `event_file` target (system_health does not keep it). **[doc]**
4. **One more XE session** for per-execution outliers: `sqlserver.rpc_completed` and `sql_batch_completed` with predicate `duration >= 1000000` (microseconds), plus `sqlserver.attention` (client timeouts and cancels), `sqlserver.error_reported`, and autogrow events (`database_file_size_change`). Event file target, 5 files × 100 to 256 MB. Low overhead because of the predicate. **[doc/exp]**
5. **DMV snapshot job every 30 to 60 s** into a table with `capture_time_utc` and `sqlserver_start_time` (from `sys.dm_os_sys_info`, to detect restarts). Take **raw cumulative values and difference them later**: `sys.dm_os_wait_stats`, `sys.dm_io_virtual_file_stats(NULL,NULL)`, `sys.dm_os_performance_counters`. Take point-in-time snapshots of `sys.dm_exec_requests` (wait_type, wait_time, blocking_session_id, status), `sys.dm_os_waiting_tasks`, `sys.dm_os_schedulers` (runnable_tasks_count), `sys.dm_exec_query_memory_grants` (grant_time null means waiting). This is the single biggest missing piece. **[doc]**
6. **tempdb and ops**: `sys.dm_db_file_space_usage`, `msdb.dbo.sysjobhistory` (run_date and run_time are integers in server-local time), `msdb.dbo.backupset`, `sp_readerrorlog` (look for "I/O requests taking longer than 15 seconds", autogrow, failover, restart). Keep 30+ error logs. **[doc/exp]**
7. **Windows counters** (every 15 s to a separate disk): Processor, Memory, `LogicalDisk\Avg. Disk sec/Read|Write`, `System\Processor Queue Length`, `TCPv4\Segments Retransmitted/sec`, plus the SQL counters (Batch Requests/sec, Compilations/sec, Page life expectancy, Memory Grants Pending, Lock Waits/sec, Number of Deadlocks/sec, Log Growths, Log Flush Wait Time). **[exp]**

### 2.3 App and host

1. **.NET runtime counters** (`dotnet-counters collect`, 5 to 10 s): `threadpool-queue-length`, `threadpool-thread-count`, `time-in-gc`, `total-pause-time-by-gc`, `gen-2-gc-count`, `monitor-lock-contention-count`, `exception-count`. On .NET Framework use perf counters: `.NET CLR Memory\% Time in GC`, `ASP.NET\Requests Queued`, `ASP.NET Applications\Requests Executing`. **[doc]**
2. **SqlClient pool counters** (Microsoft.Data.SqlClient EventSource): `number-of-free-connections`, `number-of-active-connections`, `number-of-pooled-connections`. A pool at its limit (Max Pool Size default 100) with free ≈ 0 while SQL itself shows few running requests is the classic "SQL looks idle but the API is waiting" picture. **[doc]**
3. **MongoDB driver events** via `ClusterConfigurator`: command succeeded/failed with duration, connection-pool check-out started/finished/failed (wait = difference), server heartbeat failures, cluster description changes (primary change). Aggregate to 10 s buckets rather than storing every event. Default `maxPoolSize` 100 **[verify for your driver version]**.
4. **IIS**: W3C log with `time-taken` (includes the time to send the response to the client), `sc-win32-status`; HTTP.sys counters `HTTP Service Request Queues\CurrentQueueSize`, `MaxQueueItemAge`, `RejectedRequests`; HTTPERR logs; WAS recycle events in the System log. **[doc]**
5. **Host**: CPU by process (to catch antivirus, updates, backup copy), disk latency, memory, NIC retransmits; if virtual, hypervisor CPU ready or steal (not visible from inside a Hyper-V guest). **[exp]**
6. **Audit log scope**: widen the targeted window now for a few routes so λ and the baseline are real. Keep a count of **all** requests per minute from IIS to compare with audit counts.

---

## 3. The investigation, step by step

Assumption: the edge case needs all three sources. Each step says what question it answers, what it reads, what it does, and which hypotheses (section 4) it moves. Steps are ordered cheapest first; later steps run only when earlier ones leave the question open.

### Step 0. Frame the incident
- Pick the window (start, end), the affected route (or "all"), and the reference: a quiet window just before, or the same weekday and hour on previous weeks when history exists.
- Fix the time zone: everything in UTC. Note each source's own zone (Mongo UTC; IIS UTC by default; SQL `GETDATE()` local and `GETUTCDATE()`; `msdb` job times local integers; perfmon local).
- Output: incident window and reference window, both in UTC.

### Step 1. Trust the data (gate)
- Questions: do durations make sense, do the clocks agree, is the audit complete?
- Checks: negative or huge durations (clock anomaly), start/end ordering, audit count per minute against IIS request count per minute (a dip while IIS shows more means audit loss or survivorship), whether each source's time zone rule is validated, restarts (SQL `sqlserver_start_time`, Mongo uptime, app-pool recycle) inside the window because every cumulative counter resets there.
- Moves: Q1 (clock), Q2 (audit gap), D5/H3 (restarts) as "data may be invalid", never as a cause by themselves.
- Rule: missing data never contradicts a hypothesis. It makes it "cannot test yet".

### Step 2. Shape of the change: when, how fast, how wide
- Reads: P95 by minute (audit).
- Onset t0 = first minute where P95 exceeds the baseline by the threshold for 3 of 5 minutes.
- Shape: **step** (jumps within about 2 minutes), **ramp** (builds over many minutes or days), **periodic** (repeats at 60 s, 15 min, 1 h).
- Scope: one route, a few routes, one tenant/client, or everything.
- Why it matters: step plus one route points to a query/plan/lock; step plus everything points to shared infrastructure or a pool; ramp points to growth or leak; periodic points to checkpoints, jobs, TTL, scheduled work.
- Moves: almost all hypotheses (section 4 uses shape and scope as required signals).

### Step 3. Where did the request time go? (A, C, P, O)
- Definitions per 1.1: A = audit set-up **plus the synchronous Mongo insert**, C = the rest of the action-filter chain plus the action (no insert), P = payload capture after the action, O = outside time = IIS `time-taken` − D − P (needs the IIS log). There is no R: D − A − C is zero.
- Compare each part's total excess over the reference. The part with the largest share of the excess names the **lane to look at first**:
  - C large: SQL, downstream calls, CPU in code. The Mongo audit insert is **not** in C. If `PreActionFilterTimings` is present, split C by filter name (see 1.1): subtract the "Inside C" filters (`ValidateActivePermissionAttribute`, `ValidateRequestAccessIdsAttribute`, ...) to get the action body. A large `ValidateActivePermissionAttribute` share points at the permission check (hypothesis S7), not at SQL or code.
  - A large: the audit filter's set-up (user, config and client-IP lookups, StructureMap) **or the synchronous Mongo insert**. Split by the Mongo lane: slow insert lines on the audit collection at the same minutes (M-group) mean audit coupling; otherwise CPU/GC/thread starvation in the app tier.
  - P large: big payloads, or blocking body reads under thread starvation.
  - O large or growing: work waiting **before** the filter runs (including authentication and authorization filters, which `PreActionFilterTimings` lists) or **after** the action ends: HTTP.sys queue, thread-pool queue, GC pause, recycle, slow client, noisy host. If IIS rows are missing, O is "cannot test yet", not zero.
- This is a measurement, not a ranking of hypotheses. Show it as a stacked chart plus a plain sentence.

### Step 4. Load or slowdown? Little's Law
Little's Law: **L = λ × W.** Average number of requests in the system (L) equals arrival rate (λ, per second) times average time each spends there (W).

Use it at each tier, using start and completion times to count how many requests overlap each second.

| λ (arrivals) | W (time each) | L (in flight) | Reading |
|---|---|---|---|
| up | up | up | More work arrived. Capacity or load. |
| flat | up | up | Same work arrives but each takes longer: something got slower. |
| down | up | up or flat | Requests are stuck or stalled (clients backing off, timeouts). |
| any | any | flat-tops at a limit | A pool or thread cap is full. |

Tier by tier:
- **API**: L = arrivals × D. Compare with configured limits (thread pool, IIS queue length, concurrent requests).
- **SQL**: L ≈ running requests. Compare with SqlClient Max Pool Size (default 100) and, on the server, `dm_exec_requests` counts.
- **Mongo**: L ≈ operations in progress. Compare with driver `maxPoolSize` and server tickets.
- **Origin versus victim**: the tier where L rises **first**, and whose **own** service time (measured on its own side, such as Query Store duration or Mongo `workingMillis`) grew, is the origin candidate. Tiers that only queue behind it show larger waits (pool wait, lock wait, semaphore wait), not larger work.
- Moves: L1/L2/L3 (load), S1 to S4 (queue/pool), and the whole origin/victim ordering in Step 10.

### Step 5. What changed? (calendar)
- Reads: deploys and config changes (if provided), app-pool recycles (WAS events 5074 and neighbours), SQL and Mongo restarts, failovers and elections, SQL Agent jobs (index rebuild, stats update, backup, CHECKDB), Hangfire/cron schedules, Query Store enabled time, certificate or DNS changes.
- Test: does a change sit at or just before t0 (lead), not just near it? Coincidence needs lead-lag, not overlap alone.
- Moves: C1 (deploy/config), D11 (maintenance), D5 (restart/failover), H3 (recycle), M4 (election), D14 (Query Store itself, observer effect).

### Step 6. App-tier saturation
- Reads: thread-pool queue length and thread count, GC time and pauses, SqlClient and Mongo pool counters, HTTP.sys queue, IIS `time-taken` against audit D, WAS events, app-log pool-timeout classifiers.
- Key discriminator: **SQL (or Mongo) looks idle while the API waits** points to a pool or thread problem **before** the backend, not a slow backend.
- Moves: S1 to S4, H2, H3.

### Step 7. SQL Server lane
Order inside the lane, cheapest first:
1. **Wait deltas** (difference of consecutive `dm_os_wait_stats` snapshots, per second): `LCK_M_*` blocking; `PAGEIOLATCH_*`, `WRITELOG` IO; `SOS_SCHEDULER_YIELD`, `CXPACKET` and high signal-wait ratio CPU; `RESOURCE_SEMAPHORE` memory grants; `PAGELATCH_*` on tempdb pages; `THREADPOOL` worker exhaustion; `ASYNC_NETWORK_IO` slow client; `HADR_SYNC_COMMIT` availability group. Drop benign waits.
2. **Blocking**: `blocking_session_id` chains and blocked-process reports. Find the **head** blocker (a victim with the longest wait is not the cause).
3. **Query Store**: top regressed queries at t0, plan change (same query, new plan), runtime and reads up, wait category per plan, store state.
4. **IO and log**: `io_stall` per IO per file (log and tempdb separately), autogrow, error-log 15-second IO warnings.
5. **Memory and tempdb**: grants pending, page life expectancy, spills, version store.
6. **Jobs**: `sysjobhistory`, `backupset` overlap with t0.
7. **Errors**: deadlocks (error 1205), timeouts (`attention`), failovers.
- Moves: D1 to D15.

### Step 8. MongoDB lane
1. **Server time versus client time** for the audit write: driver command duration (client) against server `durationMillis`/`workingMillis` (slow log) and `opLatencies`. A large gap means pool wait, network or serialization, not Mongo work.
2. **Slow-operation lines on the audit collection** (planSummary COLLSCAN, `docsExamined`, `numYields`, lock `timeAcquiringMicros`, `waitForWriteConcernDurationMillis`, flow control time).
3. **Cache and disk**: dirty cache ratio, pages evicted by application threads, eviction waits, checkpoint cadence (about every 60 s), disk latency.
4. **Replica set**: lag, elections, member state, write concern in use.
5. **Tickets and queues**: `globalLock.currentQueue`, execution queue.
6. **Connections**: `totalCreated` delta (churn), `current` near cap.
7. **Background work**: TTL deletes (monitor runs about every 60 s), index builds, large deletes.
- Moves: M1 to M9, S4, S5.

### Step 9. Host and network
- CPU by process, disk latency, memory, retransmits, DNS or TLS delays, port exhaustion, antivirus or update activity, hypervisor steal, time sync.
- Key test for a shared cause: **do unrelated trivial operations slow at the same moment on every tier?** Yes points to a shared host/VM/network cause (H6), not any single tier.

### Step 10. Origin versus victims, confirm, close
- Line up onset times per tier (UTC, minute buckets). The earliest sustained departure whose **own** work metric rose is the origin candidate. Later tiers that show waits are victims. Onsets within about a minute across unrelated tiers: suspect a shared cause (H6) or clock error (Q1).
- Retry storms are amplifiers: the origin is the first failure that preceded the surge.
- Pick the **cheapest discriminating action** per surviving hypothesis (section 4 lists one each), run it, and record the result in the Ledger.
- After a fix or change: re-run the same facts on the new window ("after-fix check") and compare to the incident.

---

## 4. Hypothesis catalog

Each entry: **Points to** (lane), **Must hold** (needed signals), **Signature** in API audit / MongoDB / SQL, **Contradicted by**, **Cheapest check** (also the discriminator against its nearest neighbour), **Cascades to**, and **Today** (can Unravis test it now: ✔ yes, ◐ partly, ✖ needs new data).

Notation (corrected 26 Oct, see 1.1): A audit set-up plus the synchronous audit insert, C the rest of the filter chain plus the action, P payload capture, O outside time from the IIS log, D = A + C. Older text below that says "R" means O.

### Q. Data quality (always evaluated first; these gate everything)

**Q1. Clock, time-zone or DST error.** Points to: data. Signature: negative or inverted durations, start times not in order, a one-hour offset between sources, SQL and Mongo onsets exactly 1 h apart. Contradicted by: all sources validated to UTC and consistent. Check: time-zone rule per import, end-before-start count. Cascades: every other hypothesis (misplaced onset). Today ✔ (clock anomaly and zone validation exist).

**Q2. Audit coverage gap or survivorship.** Points to: data. Signature: audit count per minute dips while IIS count does not; only slow requests present; audit write failing under load. Contradicted by: audit ≈ IIS counts. Check: per-minute audit count ÷ IIS request count. Cascades: hides L1 (surge). Today ◐ (needs IIS import in the case).

### L. Load and capacity

**L1. Organic traffic surge.** Points to: load. Must hold: λ above baseline. Signature: API λ up, per-request A/C flat at first then all grow; Mongo `opcounters` up; SQL batch requests/sec up. Contradicted by: λ flat or down. Check: λ per minute against reference or baseline; per-request service time unchanged early separates it from slowdown. Cascades: S2, S3, S4, D10, D13. Today ◐ (audited slice only until scope widened).

**L2. Mix shift: one route, tenant or payload size dominates.** Points to: workload. Signature: P95 up, median flat; one route or client dominates the slow set. Contradicted by: uniform across routes. Check: group slow requests by route, client and body size. Cascades: D3 (a heavy plan). Today ✔.

**L3. Retry storm or client loop (amplifier).** Points to: load, secondary. Signature: duplicates from the same client and route with short gaps; failures or timeouts **precede** the arrival surge; extra audit inserts; repeated identical statements. Contradicted by: no failures before the surge. Check: group by client, route, body fingerprint; compare time of first failures to time of surge. Cascades: S2, S3, S4, D1. Today ◐.

### S. Queueing and saturation (app tier)

**S1. HTTP.sys or IIS queue backup.** Points to: IIS. Must hold: O large (needs the IIS log; otherwise "cannot test yet"). Signature: IIS `time-taken` much larger than audit D; 503s; HTTPERR queue-full; audit starts late. Contradicted by: `CurrentQueueSize` 0. Check: HTTP.sys counters and HTTPERR against audit D. Cascades: S2. Today ◐ (`iis-w3c`, `iis-httperr`; counters via `wincounters`).

**S2. .NET thread-pool starvation (including sync-over-async).** Points to: app runtime. Must hold: O large, or A growing (filter work before the insert); C otherwise small per request. Signature: many requests start late; completions arrive in steps (thread injection roughly every half second); Mongo and SQL idle; trivial routes slow too. Contradicted by: `threadpool-queue-length` flat at 0. Check: queue length and thread count; trivial-route latency. Cascades: looks like every tier is slow. Today ◐ (`dotnetcounters`).

**S3. SqlClient connection-pool exhaustion.** Points to: app and SQL boundary. Must hold: L at SQL limit; C large. Signature: C includes waits near the connect timeout then errors "timeout expired … obtaining a connection from the pool"; durations cluster at identical values; SQL shows few running requests. Contradicted by: free connections > 0, no pool timeouts. Check: pool counters and pool-timeout log lines against `dm_exec_requests` count. Cascades: S2, L3. Today ◐ (app-log classifier `sql-pool-timeout`).

**S4. Mongo driver pool or wait-queue exhaustion.** Points to: app and Mongo boundary. Signature: A large with steps at the wait-queue timeout; check-out failures; server `connections.current` low or flat; no slow lines on the server. Contradicted by: check-out wait normal. Check: driver check-out wait against command duration. Cascades: audit loss, L3. Today ◐ (`mongo-pool-wait` classifier; driver events not collected).

**S5. Audit-write coupling.** Points to: audit design. Signature: A large for every route while the Mongo server is healthy; D ≈ C + A (audit awaited inside the request). Contradicted by: A small. Check: server-side latency for inserts on the audit collection only; confirm the write is synchronous. Cascades: the whole API looks slow whenever Mongo is slow. Today ◐.

**S6. Slow downstream HTTP dependency.** Points to: third party. Signature: C large only on routes that call it; no SQL waits. Contradicted by: SQL or Mongo time explains C. Check: timing by route and by call. Today ✖.

**S7. Slow permission check (`ValidateActivePermissionAttribute`).** Points to: the roles-and-permission service the filter calls (`IEffectivePermissionService.HasAnyPermission`); backing store unverified (Redis, SQL or in-process cache). Must hold: C large; `PreActionFilterTimings` shows `ValidateActivePermissionAttribute` holding most of C; A small. Signature: a few requests take minutes while most are fast; the same filter is slow across unrelated routes; the stall is one filter, not the action. Seen in sandbox on 26 Oct: 67.5% of the day's total `ApiTime`, p95 4.6 s, max 181 s; the slowest requests were 99.9% this filter and formed one shared stall (see "Day totals" in 1.1): in-flight requests held for minutes with almost no arrivals, then released together. Contradicted by: the filter is under 1 ms or missing from the array. Check: if Redis, its slowlog, latency monitor and connection count at those minutes; otherwise the SQL or service call behind it, and thread-pool queue length (S2) since a blocked wait looks the same. Cascades: S2. Today ◐ (array is stored; the lane behind it is not collected).

### D. SQL Server

**D1. Blocking chain (head blocker or open transaction).** Points to: SQL. Signature: C large and clustered on routes touching the same tables; releases together; Mongo flat; `LCK_M_*` waits, `blocking_session_id` chain, blocked-process report. Contradicted by: no lock waits. Check: head blocker in `dm_exec_requests`/BPR; separates from D3 (waits are lock, not CPU). Cascades: S3, S2. Today ◐ (needs wait/blocking snapshots).

**D2. Deadlock storm and retries.** Signature: error 1205 in the audit outcome; retry durations doubling; `xml_deadlock_report`. Contradicted by: none logged. Check: system_health deadlock events. Cascades: L3. Today ◐ (`sql-xe`).

**D3. Plan regression or parameter sniffing.** Signature: C large for specific route and parameter; Query Store: same query, new plan, duration and reads up at t0; Mongo flat. Contradicted by: plan unchanged. Check: plan history for that query (`last_compile_start_time`), try the old plan; separates from D4 (stats). Cascades: D1, D10. Today ✔ (`sql-querystore`, after interval enlargement).

**D4. Statistics change or auto-update recompile.** Signature: step after a row-modification threshold; short spike of compilations. Contradicted by: no stats update near t0. Check: `dm_db_stats_properties.last_updated` against t0. Cascades: D3. Today ✖.

**D5. Cold cache after restart, failover or plan-cache clear.** Signature: all C high right after t0, recovering over minutes; page-life-expectancy drop; compilations spike; error log "starting up"; `sqlserver_start_time` changed. Contradicted by: PLE steady, no restart. Check: `sqlserver_start_time`, error log. Cascades: D9. Today ◐ (`sql-errorlog`).

**D6. Memory pressure or grant waits.** Signature: big queries slow, small fine; `RESOURCE_SEMAPHORE`, Memory Grants Pending > 0, PLE collapse. Contradicted by: no grant waits. Check: `dm_exec_query_memory_grants` with `grant_time` null. Cascades: S3. Today ◐ (`sql-pdh`).

**D7. tempdb contention or spills.** Signature: C up for sort/hash-heavy queries; `PAGELATCH_UP/EX` on tempdb pages; spills. Contradicted by: no latch waits. Check: waiting-task resource descriptions starting `2:`. Cascades: D6. Today ✖.

**D8. Log flush stall, autogrow or large VLF count.** Signature: write routes slow, reads fine; `WRITELOG` up; autogrow events; error-log IO warnings. Contradicted by: WRITELOG flat. Check: log-file write stall per IO. Cascades: D1. Today ◐.

**D9. Disk IO latency.** Signature: `PAGEIOLATCH_*` up; `io_stall` per IO high; host disk latency high. Contradicted by: normal latency. Check: `dm_io_virtual_file_stats` deltas by file. Cascades: D5, M2. Today ◐.

**D10. SQL CPU saturation or parallelism.** Signature: `SOS_SCHEDULER_YIELD`, `CXPACKET`, runnable tasks > 0, high signal-wait share. Contradicted by: CPU low. Check: SQL CPU against OS CPU (separates D10 from H1: who owns the CPU). Cascades: S3. Today ◐.

**D11. Maintenance job overlap (index rebuild, stats update, backup, CHECKDB).** Signature: periodic at job time; `LCK_M_SCH_*`, `BACKUPIO`; IO stall. Contradicted by: no job overlap. Check: `sysjobhistory`/`backupset` against t0 (mind local-time integers). Cascades: D1, D9. Today ◐ (`schedule` importer for jobs).

**D12. Availability-group synchronous-commit delay.** Signature: write routes slow; `HADR_SYNC_COMMIT`. Contradicted by: no AG. Check: wait delta and AG health. Today ✖.

**D13. Worker-thread exhaustion (`THREADPOOL`).** Signature: `THREADPOOL` waits; new logins stall. Contradicted by: zero THREADPOOL wait. Check: wait delta. Cascades: D1. Today ✖.

**D14. Query Store itself (recently enabled).** Signature: modest slowdown; store READ_ONLY (full); `QDS_*` waits; size-based cleanup cadence; capture mode ALL on high-volume ad-hoc. Contradicted by: store healthy, onset unrelated to the `ALTER DATABASE` time. Check: compare onset with the enable time; read `actual_state_desc`, `current_storage_size_mb`. Note: one council member recommends turning size-based cleanup off; that came from a blog and I do not adopt it without testing. Today ✔.

**D15. Data growth crossing a threshold, or missing index.** Signature: gradual rise then step; reads per execution rise across days in Query Store. Contradicted by: a sudden step with no history of growth. Check: multi-day reads-per-execution trend. Today ◐ (history too short until Query Store has days).

### M. MongoDB

Reading note (from 1.1, corrected): the audit **insert** is synchronous and is counted in **A** (`AuditCreateTime`), because the filter re-stamps `CreatedOn` after the insert returns. Wherever an item below says "A large", it means audit set-up or the Mongo insert is slow; confirm against the Mongo lane (slow insert lines on the audit collection at the same minutes). A per-call Mongo timing would split set-up from insert if the app is changed to record one. **Production Mongo is standalone**, so M3 and M4 do not apply. Item **M10** is new.

**M1. WiredTiger cache pressure or application-thread eviction.** Signature: A large and spiky, all collections; dirty ratio high, pages evicted by application threads rising; slow lines for varied operations. Contradicted by: cache steady. Check: FTDC cache and eviction series; separates from M2 (disk). Cascades: M2. Today ◐ (`mongo-serverstatus`).

**M2. Disk IO saturation or checkpoint stall.** Signature: A rises at a regular cadence (about 60 s); read-time inside slow lines up. Contradicted by: no cadence. Check: disk latency in FTDC or host counters. Cascades: M1. Today ◐.

**M3. Write concern, replication lag or flow control. NOT APPLICABLE in production (standalone, no replication; w:1 and local write only; keep only the flow-control and `waitForWriteConcernDurationMillis` reading for a replica-set sandbox).** Signature: A large for writes only; `waitForWriteConcernDurationMillis` large; secondary lag; flow control time. Contradicted by: lag near zero, writes use w:1. Check: `replSetGetStatus` lag against local write time. Cascades: L3. Today ◐ (**[verify]** field names).

**M4. Election or failover. NOT APPLICABLE in production (standalone, no election). A mongod restart looks similar: check the process start time and the Windows service log instead.** Signature: A errors then retries; a gap of about 10 to 12 s; "not writable primary"; cluster-description change; election lines. Contradicted by: stable primary. Check: election and role-change log lines. Cascades: L3, S4. Today ◐ (`mongod-json`).

**M5. Ticket or lock queue.** Signature: A up with queued operations; `queues.execution` or older ticket counters near exhausted; `durationMillis` much larger than `workingMillis`. Contradicted by: both equal. Check: duration − working gap, `globalLock.currentQueue`. Cascades: M1. Today ◐.

**M6. Collection scan or missing index on the audit collection.** Signature: A creeping up; `planSummary: COLLSCAN`, `docsExamined ≫ returned`. Contradicted by: IXSCAN. Check: slow-line plan summary. Cascades: M1. Today ◐.

**M7. TTL delete burst, index build or large delete.** Signature: periodic A bursts; TTL and index-build lines; collection locks. Contradicted by: none present. Check: log lines for TTL and index builds. Cascades: M1, M2. Today ◐.

**M8. Large payloads or document growth.** Signature: P large and A correlates with payload size. Contradicted by: uniform sizes. Check: correlation of A or P with body size. Cascades: M1. Today ◐.

**M9. Connection storm or churn.** Signature: A spikes (the insert is in A); `totalCreated` delta high; connection accept/end bursts. Contradicted by: steady. Check: `totalCreated` delta. Cascades: S4. Today ◐. **Code check (26 Oct, `origin/uat` `c01578c3a61`, unchanged from the older checkout):** confirmed that every audit commit calls `new MongoClient(...)` (`MongoContext.CommitWithoutTransaction` → `Utility.GetDatabase`, `MongoUnitOfWork.Net45/Utility.cs`, four overloads at lines 20 to 40, each creating a new client), once for the insert in the request and once for the background update, and the connection string sets no pool options. Whether this creates a new connection pool each time is a **driver behaviour I have not verified in this code**: from memory, driver 2.x reuses one cluster (and pool) for clients with identical settings, so the cost is probably small CPU per call, not a pool per call. Treat the storm as unproven. Verify on the server: `connections.totalCreated` delta per minute against audited request rate (if the pool is shared, `totalCreated` stays low and flat).

**M10. Shared mongod: another tenant or an archive/maintenance job slows this tenant's audit insert.** Signature: slowness at the same minutes across audit collections of different databases, or a heavy operation (archive move, TTL, index build, large query without index) on another database in the same process; WiredTiger cache/disk pressure is process-wide. Contradicted by: other databases flat while this one is slow. Check: slow lines grouped by database (`ns`), `currentOp` by namespace, size and growth of `ExternalDetails*` and `ArchiveExternalDetails*`. Cascades: M1, M2, M5. Today ◐.

### H. Host, runtime and infrastructure

**H1. Host CPU contention (antivirus, updates, backup copy, noisy neighbour).** Signature: host CPU high while SQL and IIS process CPU are low; periodic. Contradicted by: process CPU explains it. Check: CPU by process. Cascades: S2, D10. Today ◐ (`wincounters`, `procsnap`).

**H2. GC pause (gen 2 or large object heap).** Signature: O or A large; all in-flight requests delayed together, aligned to GC; large payloads. Contradicted by: GC time flat. Check: GC pause counters; separates from S2 by steady thread count. Cascades: S2. Today ◐.

**H3. App-pool recycle, cold start, JIT.** Signature: first requests after restart slow; WAS events; new connections and sessions. Contradicted by: no recycle. Check: WAS event times. Cascades: D5, M9. Today ◐ (`winevents`).

**H4. Network (retransmits, DNS, TLS, port exhaustion).** Signature: durations at 1, 3, 9 s multiples (TCP retransmission timing) or 15 to 21 s connect; client-side time ≫ server-side time on both Mongo and SQL. Contradicted by: retransmits flat. Check: `TCPv4 Segments Retransmitted/sec`, TCP port-exhaustion events. Cascades: L3, S3, S4. Today ◐.

**H5. App-host disk or certificate-check delay.** Signature: bimodal first-call slowness at 15 or 30 s steps. Check: timing mode. Today ✖.

**H6. Shared cause across tiers (VM, hypervisor, storage, network).** Signature: every tier slows at the same minute, even for trivial operations. Contradicted by: only one tier slow. Check: do trivial, unrelated operations slow at the same moment on every tier? Cascades: all. Today ◐.

### C. Change

**C1. Deploy, configuration or infrastructure change.** Signature: step at the change time; new query shapes or plans; changed pool or timeout settings. Contradicted by: no change inside the window before t0. Check: change calendar against t0 (lead, not overlap). Cascades: D3, S2, H2. Today ◐ (depends on what you can supply).

---

## 5. Deterministic logic

### 5.1 Facts (computed, no judgment)
For incident and reference windows: λ per minute; W percentiles (P50, P95, P99) overall and per route; L per minute (overlap count); A, C, P, O medians and their excess over reference; onset t0 and its shape; scope; audit completeness; clock sanity; per-tier onsets; per-source coverage.

### 5.2 Signals (yes / no / unknown), named and versioned thresholds
Examples (defaults are starting points to tune on demo data; each is shown in the "Why?" view): `arrivals_up`, `p95_up` (above baseline + 3 × MAD for 3 of 5 minutes), `shape_step/ramp/periodic`, `scope_single_route`, `component_dominant_A/C/P/R` (largest share of excess), `r_large` (R/D above a set share and rising), `l_flat_tops` (L within a tolerance of a configured cap for several minutes), `audit_complete`, `clock_ok`, `restart_in_window`, `sql_wait_class_up(class)`, `mongo_server_time_up`, `driver_wait_up`, `pool_free_zero`, `tier_onset_first(tier)`.
A signal is **unknown** when its data is absent. Unknown is never "no".

### 5.3 Evaluation
Each catalog entry lists must-hold signals, expected signals, and contradicting signals. Result per entry:
- **Consistent**: all must-hold signals yes, none contradicting.
- **Partly consistent**: some must-hold unknown or some expected signals no, none contradicting.
- **Contradicted**: any hard contradicting signal yes. The reason is shown.
- **Cannot test yet**: required data absent. The card lists what to import or enable.

Ordering is fixed and visible: state group, then earliest tier onset (origin first), then cheapest check, then catalog id. There is **no score and no percentage**, in keeping with the Ledger rule. (Two council members proposed numeric scores; deliberately not adopted.) What the card may show is the measured **share of excess time** by component (a fact from Step 3), never a share for a hypothesis.

### 5.4 Seasonal baseline
- Key: weekday and hour (15-minute for periodic checks), per route. Median and MAD, not mean and standard deviation, so spikes do not move the baseline. At least 3 comparable samples; up to 4 to 8 is better. Exclude known job windows into a separate baseline.
- Fallback when history is short (Query Store was just enabled): use the reference window and say so on the chart.
- Distinguish step from drift by also comparing with the preceding 60 minutes.
- Periodicity: autocorrelation at 60 s, 15 min, 1 h.

### 5.5 Cheapest-first order the engine follows
1. Data quality (Q1, Q2, restarts). 2. Arrival rate. 3. A/C/P/O split. 4. Per-route split. 5. Change calendar. 6. Host CPU, disk, memory. 7. App counters (thread pool, GC, pools, HTTP.sys). 8. SQL wait deltas by class. 9. Query Store regressions. 10. Mongo slow lines, cache, replica set. 11. Network. 12. Deep: blocked-process reports, deadlock graphs, currentOp, dumps.
Stop expanding when one tier explains most of the excess **and** is first by onset; still list the others as "not ruled out".

### 5.6 Mistakes the engine must guard against
1. Cumulative counters (`dm_os_wait_stats`, `dm_io_virtual_file_stats`, per-second `dm_os_performance_counters`, Mongo `serverStatus` counters) read without differencing; drop the delta that spans a restart. **[doc]**
2. Time zones and DST (see Step 0). **[doc/exp]**
3. Survivorship: slow-only logs (slowms, XE duration predicate, profiler level 1) have no denominator; take rates from the full audit or IIS counts. **[doc]**
4. Averages hiding the tail; always split by route. Query Store keeps per-interval averages of completed statements only. **[doc]**
5. Treating the most-blocked query as the cause; find the head blocker.
6. Reading IIS `time-taken` as app time; it includes sending to the client. **[doc]**
7. Mongo 8.0+ slow threshold on `workingMillis` hides lock and flow-control waits; read `durationMillis`. **[doc]**
8. Blaming Mongo because every request writes an audit, when the audit part is flat or the server time is flat.
9. Query Store full (read-only) silently stops capture. **[doc]**
10. Accepting "it started when we changed X" without checking that X came before t0.
11. Clock differences between hosts above about a second invalidate sub-second ordering.
12. FTDC rotation overwrote the incident window; copy early. **[doc]**
13. Coincident events (backup and deploy together) need lead-lag.
14. Comparing floating-point sums with `!=` (the 452 false mismatches); use a tolerance.
15. Showing a P95 or P99 from too few requests: show a percentile only when the route and bucket hold at least a set count (starting point: 20 for P95, 100 for P99), otherwise show the max and the count.16. Ordering tiers by onset closer than the minute bucket plus the measured clock offset between hosts: report "not separable", not an order.

### 5.7 Performance principles: which apply, and what each needs
Applied to every investigation, but a model runs only when its assumptions and measurements exist; otherwise the card says what is missing. Nothing here is a score.

| Principle | Used where | Needs | Available today |
|---|---|---|---|
| Measure first | all signals are computed facts; unknown is never "no" | none | yes |
| Little's Law | Step 4, per tier | λ, W, overlap count L over a stable window; the same definition of "in flight" at each tier | audit only (API tier); SQL and Mongo need their own counters |
| Amdahl | Step 3 and every finding | share *p* of response time spent in the part; bound on gain is 1 ÷ (1 − *p*) | yes for API parts (A, C, P, per-filter); O needs IIS |
| Queueing and saturation | Steps 4, 6; S1 to S4 | utilization against a known limit, queue depth, variability of service time | no: no thread-pool, IIS queue or CPU counters collected |
| P50, P95, P99 | 5.1 | enough requests per route and bucket (5.6 item 15) | yes |
| USE method (Utilization, Saturation, Errors per resource) | Steps 6 to 9 | per resource: CPU, memory, disk, network, threads, SQL pool, Mongo pool | partly; each resource needs an Errors reading (pool timeouts, refused or dropped connections, failed check-outs), which is not collected; host and pool counters not collected |
| Four Golden Signals per service | 5.1 and 5.2 | latency W, traffic λ, errors, saturation | latency, traffic and errors yes (`StatusCode`); saturation no |
| Universal Scalability Law | not for an incident | throughput at several concurrency levels, from a load test | **cannot test**: no varied-concurrency runs |
| Unbounded queues, concurrency, retries | S2 to S4, L3, D2, M9 | queue length over time; retry counts | partly (retries covered); the audit update queue, drained every 10 s, is **not measured**: check that it does not grow |
| Evidence and expected benefit | every finding | measured share of excess, the Amdahl bound, the source rows | yes for API parts |

Rules:
- A recommendation (bounded queue, backpressure, throttling, load shedding, retry limit, jitter, a cache or timeout on the permission call) is a **finding with evidence**, never generic advice. Each carries: the measured share, the Amdahl upper bound, the rows it came from, and what is unknown. No benefit figure without those three.
- Where a model's data is missing, state the missing data and the cheapest way to get it, and do not estimate.
- New signals from today's data: `shared_stall` (requests in flight at or above a count, zero completions for at least 2 minutes with low arrivals, then a burst of completions; points at a shared blocked dependency, not at load), `errors_up` (share of `StatusCode` 500 or above per route and per hour; zero today), `near_saturation` (L above a set share of a known cap; unknown while the cap is unknown).
- BubbleUp-style comparison for the slow set against normal: for each field (route, tenant, filter name, hour, status), compare its share among slow requests with its share among the rest and show the largest differences. Today it would show `ValidateActivePermissionAttribute` at 69.8% of slow time against 0.2% for token authorize.

**Worked example (26 Oct sandbox).** `ValidateActivePermissionAttribute` is 67.5% of all `ApiTime` and 69.8% of the time in requests of 5 s or more. Amdahl: removing that time entirely would cut the day's total by at most 1 ÷ (1 − 0.675) ≈ 3.1 times, and the slow requests by at most about 3.3 times. This is an upper bound that assumes the check can cost nothing. It says nothing about the typical request (median 66 ms): the median cannot be bounded from this data because the filter's entries and the median request are different sets. Little's Law: λ was 0 to 4 per minute while 5 requests sat in flight for 3 minutes, which is the "down arrivals, flat L, rising W" row of Step 4: stalled, not overloaded. Queueing, USE and Four Golden Signals saturation stay "cannot test yet" until thread-pool, host and permission-service counters exist. Cheapest next data: what backs `HasAnyPermission` and its own latency and error counters at 16:01 to 16:04Z.

---

## 6. Worked example (synthetic)

An edge case where only the three sources together settle it.

1. Step 1: audit ≈ IIS counts, clocks valid. Passes.
2. Step 2: t0 = 10:42 UTC, step shape, all routes affected.
3. Step 3: excess is split A 25%, C 45%, O 30% (O from the IIS log; see 1.1). No single lane stands out, which already argues against "Mongo" or "SQL" alone.
4. Step 4: λ flat, W up, L rising and then flat-topping at 100 on the SQL side: "something got slower", then "a pool is full".
5. Step 5: a SQL Agent index rebuild started at 10:41.
6. Step 6: SqlClient free connections reach 0 at 10:44; thread-pool queue grows from 10:45; Mongo pool normal.
7. Step 7: `LCK_M_SCH_M` wait delta jumps at 10:42; head blocker is the rebuild; Query Store plans unchanged.
8. Step 8: audit-write client time is up, but Mongo server `durationMillis` and `workingMillis` are flat; driver check-out waits start at 10:46, **after** the thread-pool queue grew.
9. Step 9: host CPU low.
10. Step 10: SQL tier leads (10:42), app pool follows (10:44), thread pool follows (10:45), the Mongo audit write (A) is last (10:46) and shows no server-side work increase.

The catalog reads: D11 and D1 consistent (and first by onset), S3 and S2 consistent as followers, S4 and M1 to M9 contradicted by flat Mongo server time, H6 contradicted (host CPU low and tiers do not move together). The Mongo data is what turns "audit write is slow" from a false lead into a victim. Cheapest action: confirm with the DBA that the rebuild is online or offline, and reschedule it.

---

## 7. Where this lands in the product

The existing parts reused: lanes and the check vocabulary from Call trace, importers, the Ledger.

**Phase A (audit only; no new importers).** Facts and signals computed server-side; charts for L (in flight), A/C/P stacked (O added when the IIS log is imported), arrival rate, baseline band with reference fallback; catalog entries evaluated for Q1, Q2, L1 to L3, S1/S2/S5 (with caveats), H2/H3 from existing imports; "Cannot test yet" cards listing what to import. Golden tests: same input, same output.

**Phase B (SQL and Mongo lanes from imports).** Lane signals: SQL wait deltas, blocking, Query Store plan change, Mongo server-vs-client time, cache, replica set, tickets. Needs the SQL snapshot collector or an importer for the snapshot tables, plus Mongo `replSetGetStatus` and an FTDC decode path. Tier onset ordering and the origin/victim view.

**Phase C (drill-down and closure).** "Why?" view per card, "Add to ledger" (as a candidate theory, never ranked), after-fix check on a new window, export.

Constraints carried over: evidence lint on every user-facing string, no scores or percentages, missing data is "cannot test yet", thresholds named and versioned, and demo data synthetic only. A clumsy-user review follows each phase against the demo.

---

## 8. Open questions and facts to verify

The five original questions are answered in 8.1. Answered since (26 Oct): production Mongo is **standalone, not a replica set** (so M3 replication lag and M4 elections do not apply; remove `rs.status()` sampling, 9.2 steps 1 and 5; write concern is local only); production SQL Server is **the same version as the sandbox** (2022 RTM). Still open:
1. ~~What does `PreActionFilterTimings` measure?~~ Answered in 1.1 from `origin/uat` (`c01578c3a61`, 22 Oct): per-filter time of authentication, authorization and action filters, 1 ms minimum, null when none reached 1 ms. The sandbox runs this newer code; the 5 Oct checkout did not contain it. After a VPN fetch `origin/uat` is `1730dccda54` (26 Oct), with no relevant change. Real filter names, timings and order are tabulated in 1.1 (26 Oct sandbox data). The 452 sum mismatches were floating-point noise (0 with a 0.001 ms tolerance). Still open: what backs `HasAnyPermission` (S7), and what blocks it for minutes at a time.
2. Is the Mongo connection pool shared across the per-call `new MongoClient` (M9)? Check `connections.totalCreated` against request rate.
3. Can the production audit collection be indexed on `RequestInitiatedAt` (9.1 step 4)?
4. Can the audit scope be widened to all routes for a trial period, and can IIS and audit rows be matched per request (a shared request id in both)?

Facts the council disagreed on or I could not confirm (do not hard-code):
- FTDC cap: 200 MB (one member), 250 MB (docs, used here), 500 MB from 8.3.
- Mongo slow-line queue fields: `queues.execution.totalTimeQueuedMicros` (8.0+) versus `queues.execution.admissionWaitMillis`. **[verify]** by reading one real slow line.
- `ticketWaitMillis`, `flowControl.timeAcquiringMicros`, `storage.timeWaitingMicros.cache`, `waitForWriteConcernDurationMillis` presence per version. **[verify]**
- Mongo driver `maxPoolSize` default 100 and wait-queue behaviour in driver 3.x. **[verify]**
- Rejected as unsupported by any source I could find: Kilo's `wiredTiger.transaction.log.flush.duration`, `command.insert.documentSize`, `oplog.window`, and a Query Store command that set capture mode twice. Also not found anywhere: `waitingForLatch`, `ticketsAvailable`, a `queued` slow-log field.
- Query Store blocked-process threshold values 1 to 4 being no-ops: stated by one member, unconfirmed; use 5 or more.

### 8.1 Answers received (26 Oct 2026) and what they change

| Question | Answer | Effect on this plan |
|---|---|---|
| MongoDB hosting | Self-hosted. Version 7.0.16 (read from the sandbox server by the earlier probe). | FTDC and log files are reachable (no Atlas limits). On 7.0 the slow-operation threshold is judged on `durationMillis`, so the 8.0 `workingMillis` blind spot does not apply yet; it will apply after an upgrade to 8.0. Production is **standalone** (answered 26 Oct), so replica-set checks are skipped. |
| SQL Server version | Sandbox database server reports 16.0.1000.6 = SQL Server 2022 RTM, engine edition 3 (Enterprise/Developer class). On 23 to 24 Oct the sandbox Query Store was **OFF** with the default 60-minute interval and 0 MB used; it has been enabled since. Server local time is UTC+1 at that moment. | 2022 has the granular `VIEW SERVER PERFORMANCE STATE` and `VIEW DATABASE PERFORMANCE STATE` permissions, Query Store on by default for new databases, and Query Store hints. Query Store history starts from the enable date only. Production is the same version (answered 26 Oct); still run the 9.3 query once to record the edition. |
| Is the audit write awaited? | **Partly.** The skeleton record is inserted synchronously inside the request (counted in A, see 1.1); the final update with payloads is queued and written in the background every 10 s (not in the response). Detail in 1.1. | R = D − A − C is zero by construction; use O from IIS instead. A **includes** the Mongo insert; C does not. |
| Stack facts from the code | API: .NET Framework 4.7.2 on IIS (a net10.0 project exists too). Mongo driver 2.21.0, audit connection `mongodb://<host>:27017/ApiAuditMongoDB` with no pool, timeout, write-concern or retry options (driver defaults). SQL: EF 6.5.2 over System.Data.SqlClient, MARS on, no Max Pool Size / Connect Timeout / Command Timeout set (defaults: pool 100, connect 15 s, command 30 s). | A new `MongoClient` per audit call is likely (M9). Default 30 s SQL command timeout and 100-connection pool shape S4, D-group pool hypotheses. A .NET Framework app has no `dotnet-counters`; use Windows performance counters (9.6). |
| Create the SQL snapshot job ourselves? | Yes, in the **Blackbox** (see 9.7), with a read-only login and no objects created on the server. One-time settings changes (Query Store, blocked process threshold, XE sessions) stay a DBA step with a script we show. | Gap in 1.3 can be closed without asking the DBA for a job. |
| Audit and IIS logs | Audit has been on for a long time (oldest sandbox record 31 Oct 2025). IIS detailed logging was switched on just now, so IIS history before that has fewer fields. | Baselines for audit are possible now (a year of history). IIS-based checks (S1, Q2) have history only from today. |

Facts seen in the sandbox Mongo data that matter for the catalog:
- One `mongod` hosts many databases: per-tenant audit databases (PayPacket, Rickard, Stephenson, WageMate), per-tenant `ExternalDetails*` and `ArchiveExternalDetails*` databases, and event-log databases. Added hypothesis **M10** (shared server, one tenant or an archive job slows the others) (Step 2 already asks whether the change is one tenant; a per-tenant split of the audit data needs the tenant field per source).
- The audit documents carry `PreActionFilterTotalMilliseconds` and `PreActionFilterTimings` besides `AuditCreateTime`, `ApiTime`, `PayloadRecordTime`. There is **no stored total**: D = `RequestCompletedAt` − `RequestInitiatedAt`. The filter-timing fields are explained in 1.1; do not add them to A, C or D.
- The sandbox `ApiAuditLog` collection had only the `_id_` index. If live is the same, any time-range read of the audit collection (including by Unravis) is a full collection scan. Check with `getIndexes()` (9.2) before pointing Unravis at a live audit database; the scan itself can be the cause of audit-write slowness (M6) and an observer effect.

---

## 9. Source setup guide (for the use case wizard)

Each source gets a guide card in the wizard: **why it is needed, what to enable, the exact command or query, how to verify it, cost, and what Unravis cannot say without it.** Unravis only ever **reads**; anything that changes a server is shown as a script for its owner to run, never run by Unravis. Credentials are never shown or stored in clear text (existing rule). The "Verify" queries double as a readiness check: the Health checks panel runs them and shows a tick, a cross or "cannot read" per item.

Placeholders: `<AuditDb>`, `<ApiDb>`, `<from>`, `<to>`, `<login>`.

### 9.1 API audit logs (MongoDB `ApiAuditLog`) and the application

**Why:** it is the only per-request record: when, how long, which route, which parts.
**Already on.** What to do:
1. **Widen the scope for the investigation window.** Ask the API owners to audit all routes for the affected tenants for at least 14 days before and during an incident (a baseline needs the same weekday and hour on several weeks).
2. **Know what the fields mean.** Stored fields seen in sandbox: `RequestInitiatedAt`, `RequestCompletedAt`, `AuditCreateTime`, `ApiTime`, `PayloadRecordTime`, `PreActionFilterTotalMilliseconds`, `PreActionFilterTimings`, `ControllerName`, `ActionName`, `StatusCode`, `RequestLengthInKiloBytes`, `ResponseLengthInKiloBytes`, `UserIp`, `UserAgent`. Totals are derived. What each time part covers is in 1.1 (the synchronous insert is inside ApiTime).
3. **Check completeness** per minute against IIS (below). Verify:
```js
// mongosh: audit rows per minute for one tenant database
db.getSiblingDB("<AuditDb>").ApiAuditLog.aggregate([
  { $match: { RequestInitiatedAt: { $gte: ISODate("<from>"), $lt: ISODate("<to>") } } },
  { $group: { _id: { $dateTrunc: { date: "$RequestInitiatedAt", unit: "minute" } },
              n: { $sum: 1 }, avgApi: { $avg: "$ApiTime" }, maxApi: { $max: "$ApiTime" } } },
  { $sort: { _id: 1 } }
], { allowDiskUse: true })
```
4. **Check that reading it is cheap** (otherwise add an index on `RequestInitiatedAt` through the database owner):
```js
db.getSiblingDB("<AuditDb>").ApiAuditLog.getIndexes()
db.getSiblingDB("<AuditDb>").ApiAuditLog.find({ RequestInitiatedAt: { $gte: ISODate("<from>"), $lt: ISODate("<to>") } })
  .limit(1).explain("executionStats").executionStats   // look for COLLSCAN vs IXSCAN and docsExamined
```
**Cost:** none for the application; a missing index makes reads expensive.
**Without it:** nothing can be measured per request.

### 9.2 MongoDB 7.0, self-hosted

Read-only login for Unravis and the Blackbox (least privilege):
```js
// mongosh, run once by the Mongo administrator
db.getSiblingDB("admin").createUser({
  user: "<login>", pwd: passwordPrompt(),
  roles: [ { role: "clusterMonitor", db: "admin" },
           { role: "read", db: "<AuditDb>" } ]      // add one read role per audit database
})
```
Step 1: **identify the server** (answers version, standalone or replica set, paths):
```js
db.version(); db.hello();                       // setName present = replica set; isWritablePrimary
db.adminCommand({ getCmdLineOpts: 1 })           // log path, dbPath, configured slowms
rs.status()                                      // error "not running with --replSet" = standalone (production: expected)
```
Step 2: **slow-operation log.** Slow operations go to the mongod log above `slowms` (default 100 ms). Check and set (applies to the whole process; profiling level 0 keeps the log line):
```js
db.getProfilingStatus()                          // per database: was, slowms, sampleRate
db.setProfilingLevel(0, { slowms: 100, sampleRate: 1.0 })   // log every op over 100 ms, no profiler writes
```
Keep `systemLog.verbosity` at 0 (higher levels log every operation). Rotate daily and keep 14 to 30 days: `db.adminCommand({ logRotate: 1 })` from a scheduled task, plus `systemLog.logRotate: reopen` or the default rename behaviour. Slow lines contain `durationMillis`, `planSummary`, `docsExamined`, `keysExamined`, `numYields`, `locks`, `flowControl`, `storage`, `waitForWriteConcernDurationMillis` where present for that operation type. Importer: `mongod-json`.
Step 3: **FTDC** (on by default; verify, and read the real cap on this server because sources disagree):
```js
db.adminCommand({ getParameter: 1, diagnosticDataCollectionEnabled: 1,
                  diagnosticDataCollectionPeriodMillis: 1,
                  diagnosticDataCollectionDirectorySizeMB: 1 })
```
Copy `<dbPath>\diagnostic.data` to a safe folder every hour (a scheduled `robocopy /E /XO`), because older files are overwritten. It holds server counters, replica set status and (on Windows) host CPU, disk and memory; it holds no query text. On Windows the mongod service account must belong to **Performance Monitor Users** and **Performance Log Users** or the host numbers are missing. Decoding the binary files is done outside Unravis for now.
Step 4: **sampler** every 15 s (cheap; produces our own deltas). Run with `mongosh --quiet "<uri>" --file mongo_sampler.js >> serverstatus.ndjson`:
```js
// mongo_sampler.js
while (true) {
  const s = db.getSiblingDB("admin").serverStatus({ metrics: 0, tcmalloc: 0, repl: 1 });
  print(EJSON.stringify({ t: new Date(), uptime: s.uptime, connections: s.connections,
    opcounters: s.opcounters, opLatencies: s.opLatencies, globalLock: s.globalLock,
    cache: s.wiredTiger && s.wiredTiger.cache,
    tickets: s.wiredTiger && s.wiredTiger.concurrentTransactions,   // 7.0 name; verify on your build
    flowControl: s.flowControl, network: s.network, repl: s.repl }));
  sleep(15000);
}
```
Keep the raw cumulative values and difference them later; discard a delta that spans a restart (`uptime` going down). The exact output layout must match what the `mongo-serverstatus` importer expects; if it does not, the importer is adjusted, not the file hand-edited.
Step 5: **replica set status** (SKIP in production, which is standalone; kept for a replica-set sandbox) every 30 to 60 s: `rs.status()` fields `members[].stateStr`, `optimeDate`, `lastHeartbeatMessage`, `electionDate`.
Step 6: **`currentOp` while degraded** (every 10 to 30 s during an incident; it is not retroactive):
```js
db.getSiblingDB("admin").aggregate([
  { $currentOp: { allUsers: true, idleConnections: false } },
  { $match: { active: true, secs_running: { $gte: 1 } } },
  { $project: { opid: 1, ns: 1, op: 1, secs_running: 1, waitingForLock: 1, planSummary: 1, client: 1, "command.comment": 1 } }
])
```
Step 7: **profiler (optional).** Only if a stored, queryable record is wanted: level 1 on the audit database only, with a bigger capped collection. Needs a short maintenance window because the collection is dropped and recreated:
```js
const d = db.getSiblingDB("<AuditDb>");
d.setProfilingLevel(0);
d.system.profile.drop();
d.createCollection("system.profile", { capped: true, size: 100 * 1024 * 1024 });
d.setProfilingLevel(1, { slowms: 100, sampleRate: 1.0 });
```
Step 8: **application side.** The Mongo driver pool is invisible from the server. Ask the API owners for driver command and connection-pool events (check-out start/finished/failed, with durations) aggregated per 10 s into the app log. Check the effective write concern and pool settings in the connection string.
**Verify list for the readiness check:** version, hello/replica set, `getProfilingStatus`, FTDC parameters and folder size and newest file time, audit index present, log file exists and has slow lines in the last 24 h.
**Cost:** log and FTDC negligible; sampler negligible; `currentOp` moderate on a busy server (only while degraded); profiler adds writes on the node being diagnosed.

### 9.3 SQL Server 2022

**Login for Unravis and the Blackbox (read-only):**
```sql
-- run once by the DBA (server level)
CREATE LOGIN [<login>] WITH PASSWORD = N'<set by the DBA>', CHECK_POLICY = ON;   -- or a Windows login
GRANT VIEW SERVER PERFORMANCE STATE TO [<login>];     -- 2022: replaces VIEW SERVER STATE for the DMVs below
GRANT VIEW ANY ERROR LOG TO [<login>];                -- sp_readerrorlog (2022); verify on your build
-- in each application database (Query Store reads):
USE [<ApiDb>]; CREATE USER [<login>] FOR LOGIN [<login>]; GRANT VIEW DATABASE PERFORMANCE STATE TO [<login>];
-- in msdb (Agent job history):
USE msdb; CREATE USER [<login>] FOR LOGIN [<login>]; ALTER ROLE SQLAgentReaderRole ADD MEMBER [<login>];
```
(On a pre-2022 server use `GRANT VIEW SERVER STATE` and `VIEW DATABASE STATE`.)

**Step 1: identify the server** (also the readiness check and time-zone note):
```sql
SELECT CAST(SERVERPROPERTY('ProductVersion') AS varchar(20)) AS ver,
       CAST(SERVERPROPERTY('Edition') AS nvarchar(100)) AS edition,
       SERVERPROPERTY('EngineEdition') AS engine_edition,
       (SELECT sqlserver_start_time FROM sys.dm_os_sys_info) AS started_local,
       DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()) / 60.0 AS server_utc_offset_h,
       SERVERPROPERTY('IsHadrEnabled') AS always_on;
```
**Step 2: Query Store** (per database; sandbox showed it OFF with a 60-minute interval):
```sql
SELECT actual_state_desc, desired_state_desc, readonly_reason, interval_length_minutes,
       current_storage_size_mb, max_storage_size_mb, query_capture_mode_desc,
       wait_stats_capture_mode_desc, flush_interval_seconds, stale_query_threshold_days
FROM sys.database_query_store_options;           -- actual_state_desc must be READ_WRITE
-- change (DBA, per database; 5 to 15 minute intervals suit incidents):
ALTER DATABASE [<ApiDb>] SET QUERY_STORE = ON (
  OPERATION_MODE = READ_WRITE, INTERVAL_LENGTH_MINUTES = 15, DATA_FLUSH_INTERVAL_SECONDS = 900,
  MAX_STORAGE_SIZE_MB = 2048, QUERY_CAPTURE_MODE = AUTO, SIZE_BASED_CLEANUP_MODE = AUTO,
  CLEANUP_POLICY = (STALE_QUERY_THRESHOLD_DAYS = 30), WAIT_STATS_CAPTURE_MODE = ON, MAX_PLANS_PER_QUERY = 200);
EXEC sp_query_store_flush_db;                     -- before any export
```
`READ_ONLY` means the store is full and has **silently stopped** capturing. Export for a window:
```sql
SELECT q.query_id, LEFT(qt.query_sql_text, 200) AS txt, p.plan_id, p.last_compile_start_time, p.is_forced_plan,
       i.start_time, rs.count_executions, rs.avg_duration / 1000.0 AS avg_ms, rs.max_duration / 1000.0 AS max_ms,
       rs.avg_logical_io_reads, rs.avg_cpu_time / 1000.0 AS avg_cpu_ms
FROM sys.query_store_runtime_stats rs
JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id
JOIN sys.query_store_plan p ON p.plan_id = rs.plan_id
JOIN sys.query_store_query q ON q.query_id = p.query_id
JOIN sys.query_store_query_text qt ON qt.query_text_id = q.query_text_id
WHERE i.start_time >= @from AND i.start_time < @to          -- datetimeoffset
ORDER BY rs.avg_duration * rs.count_executions DESC;
-- waits per plan and interval (categories, not raw wait types):
SELECT ws.plan_id, ws.runtime_stats_interval_id, ws.wait_category_desc, ws.total_query_wait_time_ms
FROM sys.query_store_wait_stats ws;
```
Reading rule: per-interval averages of **completed** statements; a request still running at the incident minute appears only after it finishes. Importer: `sql-querystore`.

**Step 3: blocked process report and the long-statement session** (DBA, once; files must be on a disk that is not the data disk, and copied off regularly):
```sql
EXEC sp_configure 'show advanced options', 1; RECONFIGURE;
EXEC sp_configure 'blocked process threshold (s)', 5; RECONFIGURE;      -- 5 or more; no restart

CREATE EVENT SESSION [unravis_blocked] ON SERVER
  ADD EVENT sqlserver.blocked_process_report
  ADD TARGET package0.event_file (SET filename = N'D:\xe\unravis_blocked.xel', max_file_size = (100), max_rollover_files = (5))
  WITH (MAX_MEMORY = 4096 KB, EVENT_RETENTION_MODE = ALLOW_SINGLE_EVENT_LOSS, MAX_DISPATCH_LATENCY = 30 SECONDS, STARTUP_STATE = ON);
ALTER EVENT SESSION [unravis_blocked] ON SERVER STATE = START;

CREATE EVENT SESSION [unravis_slow] ON SERVER
  ADD EVENT sqlserver.rpc_completed (ACTION (sqlserver.database_name, sqlserver.client_app_name, sqlserver.client_hostname, sqlserver.session_id)
      WHERE duration >= 1000000),                                          -- microseconds: 1 s
  ADD EVENT sqlserver.sql_batch_completed (ACTION (sqlserver.database_name, sqlserver.client_app_name, sqlserver.client_hostname, sqlserver.session_id)
      WHERE duration >= 1000000),
  ADD EVENT sqlserver.attention (ACTION (sqlserver.database_name, sqlserver.client_app_name, sqlserver.client_hostname, sqlserver.session_id)),
  ADD EVENT sqlserver.error_reported (ACTION (sqlserver.database_name, sqlserver.client_app_name, sqlserver.session_id) WHERE severity >= 11),
  ADD EVENT sqlserver.database_file_size_change
  ADD TARGET package0.event_file (SET filename = N'D:\xe\unravis_slow.xel', max_file_size = (256), max_rollover_files = (5))
  WITH (MAX_MEMORY = 8192 KB, EVENT_RETENTION_MODE = ALLOW_SINGLE_EVENT_LOSS, MAX_DISPATCH_LATENCY = 30 SECONDS, STARTUP_STATE = ON);
ALTER EVENT SESSION [unravis_slow] ON SERVER STATE = START;
```
Statement text is deliberately not captured (the `sql_text` action costs more and can hold personal data; the `statement` field of the completed events is enough for most work, and Unravis redacts values). The built-in `system_health` session already holds deadlock graphs; copy its `system_health*.xel` files off the server too (it keeps little). Verify the sessions exist: `SELECT name, startup_state FROM sys.server_event_sessions;`. Importer: `sql-xe`. **Cost:** low with the 1 s predicate; test on sandbox first.

**Step 4: samples every 30 to 60 s** (read-only queries, no objects created; the Blackbox in 9.7 runs these and keeps raw cumulative values with the capture time and the start time so deltas and restarts are handled). Run with `SET LOCK_TIMEOUT 2000; SET DEADLOCK_PRIORITY LOW;` and a 5 s command timeout so the sampler can never become the problem:
```sql
-- (a) waits, cumulative: difference consecutive captures
SELECT SYSUTCDATETIME() AS capture_utc, wait_type, waiting_tasks_count, wait_time_ms, signal_wait_time_ms
FROM sys.dm_os_wait_stats WHERE waiting_tasks_count > 0;
-- (b) file IO stalls, cumulative
SELECT SYSUTCDATETIME() AS capture_utc, DB_NAME(vfs.database_id) AS db, mf.type_desc, mf.physical_name,
       vfs.num_of_reads, vfs.io_stall_read_ms, vfs.num_of_writes, vfs.io_stall_write_ms
FROM sys.dm_io_virtual_file_stats(NULL, NULL) vfs
JOIN sys.master_files mf ON mf.database_id = vfs.database_id AND mf.file_id = vfs.file_id;
-- (c) who is running and who waits, point in time
SELECT SYSUTCDATETIME() AS capture_utc, r.session_id, r.status, r.command, r.wait_type, r.wait_time,
       r.blocking_session_id, r.total_elapsed_time, r.cpu_time, r.logical_reads, DB_NAME(r.database_id) AS db,
       s.program_name, s.host_name
FROM sys.dm_exec_requests r JOIN sys.dm_exec_sessions s ON s.session_id = r.session_id
WHERE s.is_user_process = 1 AND r.session_id <> @@SPID;
-- (d) head blockers (the cause), not the most-blocked victim
WITH b AS (SELECT session_id, blocking_session_id FROM sys.dm_exec_requests WHERE blocking_session_id <> 0)
SELECT DISTINCT blocking_session_id AS head_session FROM b WHERE blocking_session_id NOT IN (SELECT session_id FROM b);
-- (e) CPU queue and worker pressure
SELECT SYSUTCDATETIME() AS capture_utc, SUM(runnable_tasks_count) AS runnable, SUM(work_queue_count) AS work_queue,
       SUM(current_workers_count) AS workers FROM sys.dm_os_schedulers WHERE scheduler_id < 255;
-- (f) memory grants waiting
SELECT SYSUTCDATETIME() AS capture_utc, COUNT(*) AS grants,
       SUM(CASE WHEN grant_time IS NULL THEN 1 ELSE 0 END) AS waiting FROM sys.dm_exec_query_memory_grants;
-- (g) connections per application host (pool evidence: each app instance and pool string is capped, default Max Pool Size 100)
SELECT SYSUTCDATETIME() AS capture_utc, host_name, program_name, DB_NAME(database_id) AS db, COUNT(*) AS connections
FROM sys.dm_exec_sessions WHERE is_user_process = 1 GROUP BY host_name, program_name, database_id;
-- (h) counters; the per-second ones are cumulative too
SELECT SYSUTCDATETIME() AS capture_utc, object_name, counter_name, instance_name, cntr_value, cntr_type
FROM sys.dm_os_performance_counters
WHERE counter_name IN (N'Batch Requests/sec', N'SQL Compilations/sec', N'Page life expectancy', N'Memory Grants Pending',
  N'Lock Waits/sec', N'Number of Deadlocks/sec', N'Log Flush Wait Time', N'Log Growths', N'User Connections', N'Processes blocked');
```
**Step 5: jobs and the error log** (explains maintenance overlap; mind the time zone: Agent times are server-local integers):
```sql
SELECT j.name, msdb.dbo.agent_datetime(h.run_date, h.run_time) AS start_local, h.run_duration AS hhmmss, h.run_status
FROM msdb.dbo.sysjobhistory h JOIN msdb.dbo.sysjobs j ON j.job_id = h.job_id
WHERE h.step_id = 0 AND msdb.dbo.agent_datetime(h.run_date, h.run_time) >= @from_local
ORDER BY start_local;
EXEC sp_readerrorlog 0, 1, N'longer than 15 seconds';     -- slow disk warnings
EXEC sp_readerrorlog 0, 1, N'Starting up database';       -- restarts
SELECT name, growth, is_percent_growth FROM sys.master_files;   -- autogrow sizes
```
Importers: `schedule` (jobs), `sql-errorlog`, `sql-pdh`.
**Step 6: application side.** From the API owners: the SQL connection string (`Max Pool Size`, `Connect Timeout`, `Command Timeout` values only, never the password), and SqlClient pool counters or the pool-timeout log lines.
**Cost:** DMV samples are cheap at 30 to 60 s; never sample `dm_exec_query_stats` with text at high frequency.

### 9.4 IIS

**Why:** the front-door record: which requests arrived, their total time, status, and whether the server queued them.
1. **Log fields.** Detailed logging was just enabled; make sure these fields are on (the `time-taken`, status and sub-status fields matter most):
```powershell
Import-Module WebAdministration
Get-WebConfigurationProperty -PSPath 'MACHINE/WEBROOT/APPHOST' -Filter 'system.applicationHost/sites/siteDefaults/logFile' -Name logExtFileFlags
Set-WebConfigurationProperty -PSPath 'MACHINE/WEBROOT/APPHOST' -Filter 'system.applicationHost/sites/siteDefaults/logFile' -Name logExtFileFlags `
  -Value 'Date,Time,ClientIP,UserName,SiteName,ComputerName,ServerIP,Method,UriStem,UriQuery,HttpStatus,HttpSubStatus,Win32Status,BytesSent,BytesRecv,TimeTaken,ServerPort,UserAgent,Referer,Host,ProtocolVersion'
```
Also add the client-address header field (`X-Forwarded-For`) through IIS Manager > Logging > Custom Fields when a load balancer sits in front. W3C times are **UTC**. `time-taken` includes the time to send the response to the client, so it can exceed the application's own duration. Importer: `iis-w3c`.
2. **HTTP.sys error log** (queue full, rejected, timeouts): `%SystemRoot%\System32\LogFiles\HTTPERR` is on by default. Importer: `iis-httperr`.
3. **Queue and runtime counters** (also used for Little's Law on the API tier): see 9.6.
4. **Application pool recycles** (a recycle explains a slow minute and resets caches):
```powershell
Get-WinEvent -FilterHashtable @{ LogName='System'; ProviderName='Microsoft-Windows-WAS'; StartTime=(Get-Date).AddDays(-14) } |
  Select TimeCreated, Id, Message
Import-Module WebAdministration
Get-ItemProperty IIS:\AppPools\<pool> -Name recycling.logEventOnRecycle
Set-ItemProperty IIS:\AppPools\<pool> -Name recycling.logEventOnRecycle -Value 'Time,Requests,Schedule,Memory,IsapiUnhealthy,OnDemand,ConfigChange,PrivateMemory'
```
Importer: `winevents`.
5. **Failed Request Tracing** for requests over a time limit (optional; larger files, short retention): rule on `*` with time-taken 5000 ms; importer `iis-freb`.
**Verify:** newest log file time, presence of `time-taken` in the `#Fields:` header, log directory free space.

### 9.5 Redis (only if the APIs use it)

```
CONFIG GET slowlog-log-slower-than       ; microseconds, default 10000
CONFIG GET slowlog-max-len               ; default 128, raise to 1024
CONFIG SET slowlog-log-slower-than 10000
CONFIG SET slowlog-max-len 1024
CONFIG SET latency-monitor-threshold 100 ; milliseconds
SLOWLOG GET 1024                         ; export: it is a ring buffer and is lost on restart
LATENCY LATEST
INFO all                                 ; sample every 15 s: connected_clients, blocked_clients, used_memory, evicted_keys, instantaneous_ops_per_sec
```
Importers: `redis-slowlog`, `redis-latency`, `redis-info`, `redis-log`. Cost: negligible.

### 9.6 Application server and host counters (Windows)

One collector, 15 s, written to a separate disk; for the SQL counters on a named instance replace `SQLServer:` with `MSSQL$<instance>:`.
```bat
logman create counter unravis_host -si 15 -f csv -o D:\perf\unravis_host ^
 -c "\Processor(_Total)\% Processor Time" "\System\Processor Queue Length" "\Memory\Available MBytes" ^
    "\LogicalDisk(*)\Avg. Disk sec/Read" "\LogicalDisk(*)\Avg. Disk sec/Write" "\TCPv4\Segments Retransmitted/sec" ^
    "\Process(w3wp*)\% Processor Time" "\.NET CLR Memory(w3wp*)\% Time in GC" ^
    "\ASP.NET\Requests Queued" "\ASP.NET Applications(__Total__)\Requests Executing" ^
    "\HTTP Service Request Queues(*)\CurrentQueueSize" "\HTTP Service Request Queues(*)\MaxQueueItemAge" ^
    "\SQLServer:SQL Statistics\Batch Requests/sec" "\SQLServer:Buffer Manager\Page life expectancy" ^
    "\SQLServer:Memory Manager\Memory Grants Pending" "\SQLServer:Locks(_Total)\Lock Waits/sec"
logman start unravis_host
```
Importers: `wincounters`, `sql-pdh`. The API is .NET Framework 4.7.2 on IIS, so the `.NET CLR`, `ASP.NET` and `.NET Data Provider for SqlServer` performance counters above are the right source (add `"\.NET Data Provider for SqlServer(*)\NumberOfPooledConnections"` and `"\.NET CLR LocksAndThreads(w3wp*)\Current Queue Length"`; verify instance names with `typeperf -q`). For a .NET (Core) process (the net10.0 project, if deployed) use `dotnet-counters collect --process-id <pid> --refresh-interval 10 --format csv -o <file> --counters System.Runtime,Microsoft.Data.SqlClient.EventSource` (importer `dotnetcounters`). The thread-pool queue length, GC pause time and SqlClient pool counters answer S2, S3 and H2. The Windows System and Application event logs (`winevents`) give recycles, restarts and disk warnings. Cost: very low.

### 9.7 The Blackbox as the collector (answer to "can we do the SQL job ourselves?")

**Yes, and it fits the existing Blackbox decisions** (separate feature, Windows service, manifest plus NDJSON, nothing copied by default, one-way). Proposal for a later release, after the first Blackbox release that records host health and the log catalog:
- A **database probe** inside the Blackbox that runs the 9.2 sampler and the 9.3 step-4 queries with the read-only logins above, every 30 to 60 s, and writes NDJSON (raw cumulative values, capture time in UTC, server start time) under the same housekeeping caps. No tables, jobs or XE sessions are created on the database servers.
- **Never changes anything.** Query Store settings, the blocked process threshold and the XE sessions remain scripts shown in the wizard for the DBA to run; the Blackbox only **checks** that they exist and reports "present", "missing" or "cannot read".
- **Safety:** one short connection per cycle, 5 s command timeout, `LOCK_TIMEOUT` 2 s, `DEADLOCK_PRIORITY LOW`, backs off when a cycle fails, never samples query text at high frequency, and credentials are protected the same way as case connections (a Windows login for the service account is preferred over a stored password).
- **Where it runs:** on the application or database host; DMVs and Mongo commands work over the network, but host counters, IIS logs, FTDC and the Mongo log are files on each host, so a Blackbox per host is the simple model.
- **Cost of building it:** it adds a SQL driver and a Mongo driver to the Blackbox, which the first release deliberately excludes, and needs the importers to read the new NDJSON. That belongs in Phase B of section 7.

### 9.8 What the wizard must show for each source

A card with: one sentence on why, "what you need" (settings and login), "copy this" blocks like the ones above (read-only ones can be run by Unravis after the user gives a connection), "check it" (the verify query and its result), "how far back it goes", "cost and risk", "what we cannot tell without it", and a link to the matching hypotheses ("this source is needed to test S3, D1, D9"). A source that cannot be read shows "cannot read" (never "fine"). One panel lists the whole case: sources present, history length per source, and the hypotheses currently "cannot test yet" because of a missing source.
