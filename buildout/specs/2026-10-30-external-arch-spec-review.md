# Review of external spec 'Unravis Technical Architecture & Workflow Specification' (2026-10-30)

Three independent hub reviews (A: fit with real code, job 6721; B: technical correctness, job 31892; C: scope/product, job 6722). Orchestrator caveats: B's claims that time-taken excludes network time and that HTTPERR has no #Fields header are disputed and must be checked on real sample files before use.

---
## Review A
## Verdict

The external spec describes a greenfield “one `.unravis.db` per case” tracing-paper product with domain-specific SQLite tables, macro ingest of live Mongo/SQL into that file, and a coverage ledger. The real app already implements much of the *telemetry semantics* (W3C `#Fields`, HTTPERR, route `{id}` folding, W3C `start = end − time-taken`, Query Store/XE/FREB/Mongo diagnostic *file* importers) but on a different storage and workflow model: encrypted per-case `case.db` folders, a unified `events` table, live Mongo audit via `internal/store`, and health checks that return over HTTP without persisting DMV/profile snapshots into the case. Adopting the spec verbatim would be a large replatform, not an incremental extension.

## Fit with the real app

| Spec item | Real app | Verdict | Evidence path:line |
|-----------|----------|---------|-------------------|
| **§5 / §6** One shareable `.unravis.db` with plain SQL tables (`api_audit_logs`, `iis_w3c_logs`, …) | Per-case folder: sealed `manifest.dat`, DPAPI/master-key `case.key`, adiantum-encrypted `case.db`; export uses `.unravis-case` wrapper, not `.unravis.db` | **conflicts** | `internal/casestore/sqlite/sqlite.go:1-3`, `internal/casestore/casefile.go:28-56`, `internal/casestore/sqlite/schema.go:24-34` |
| **§2.7 / §5** WAL + `synchronous=NORMAL` (+ spec `temp_store=MEMORY`) | WAL and `synchronous=NORMAL` on open; extra hardening pragmas; `temp_store=MEMORY` only on export-restore path, not default case open | **matches / partial** | `internal/casestore/sqlite/sqlite.go:595-606`, `internal/casestore/casefile.go:577` |
| **§2.7** 10 000-row batched commits | Whole import in one serializable transaction; checkpoint on import end | **conflicts** | `internal/casestore/sqlite/case.go:561-563`, `internal/casestore/sqlite/case.go:618-619`, `internal/casestore/sqlite/case.go:767` |
| **§2.7** Post-stream deferred indexes | Indexes applied via `indexSQL` at end of successful import | **matches** | `internal/casestore/sqlite/schema.go:21-23`, `internal/casestore/sqlite/case.go:767` |
| **Storage caps** Spec silent on encryption/retention | 50 000 events max per `Events` query; 30-day closed-case retention + `Sweep`; per-file/case byte caps | **duplicate (different shape)** | `internal/casestore/store.go:101-102`, `internal/casestore/store.go:94-95`, `internal/casestore/sqlite/case.go:33-34`, `internal/casestore/sqlite/sqlite.go:841`, `internal/casestore/sqlite/sqlite.go:1182-1185` |
| **§2.1** W3C + HTTPERR ingest; exclude bulk FREB XML in SQLite | `iis-w3c`, `iis-httperr` importers; FREB ingests derived rows from XML (not raw XML in DB) | **matches / partial** | `internal/importer/all/all.go:31-33`, `internal/importer/iis/freb.go:24-31` |
| **§2.2** Stateful `#Fields` rebind; `-` → null | Shared `run`/`declare`; empty or `"-"` cells skipped | **matches** | `internal/importer/iis/common.go:159`, `internal/importer/iis/common.go:317-345`, `internal/importer/iis/common.go:62-69` |
| **§2.3** Typed W3C columns + `custom_fields_jsonb` | Known columns → `attrs_json` on generic `events`; columns outside `keep` list dropped, not overflow JSONB | **conflicts** | `internal/importer/iis/common.go:317-338`, `internal/casestore/sqlite/schema.go:29-31` |
| **§2.4** `start_utc_ms` / `end_utc_ms` everywhere | Events use `Start`/`End` → `ts_ms`/`te_ms`; W3C subtracts `time-taken` when `w3cTimeIsEnd` | **matches** | `internal/evidence/event.go:48-55`, `internal/importer/iis/w3c.go:14-21`, `internal/importer/iis/w3c.go:175-188`, `internal/casestore/sqlite/case.go:558-559` |
| **§2.4** Clock/timezone | W3C/HTTPERR use `clock.RuleUTCField`; one-second resolution gap documented | **matches** | `internal/importer/iis/w3c.go:75`, `internal/importer/iis/httperr.go:14-18`, `internal/importer/iis/w3c.go:28-30` |
| **§2.5** Partition aggregations by `sc_win32_status` | `win32` stored as request attribute; no spec-style mandatory clean vs abort series split found in UI/backend | **partial / new** | `internal/importer/iis/w3c.go:139-143` |
| **§2.6** Ingest `route_template` + 3-tier implementing agent | `normaliseStem` → `{id}` in `stem` attr; Mongo audit uses `Controller`/`Action` from documents, not tiered handler resolution | **partial / new** | `internal/importer/iis/common.go:395-404`, `internal/store/analyze.go:23-24`, `internal/store/analyze.go:377` |
| **§3.1** Live Mongo: classify audit/app/archive DBs; ingest audit metadata into case SQLite | API audit read live through `store` aggregations; case holds log *imports*, not live audit mirror; archive auto-mute | **conflicts / unverified** | `internal/importer/all/all.go:39-55` (no audit-log importer), `internal/store/analyze.go:17-24`; archive naming mute **unverified** in code search |
| **§3.2** IIS auto-detect (`applicationHost.config` + HTTPERR dir) + file drop | Discover parses `applicationHost.config` site dirs; HTTPERR detector; user import/discover flows | **matches** | `internal/discover/iis_sitedirs.go:13-35`, `internal/server/discover.go:190` |
| **§3.3** Live SQL: Query Store intervals + DMV snapshots into SQLite | Query Store via `sql-querystore` *export* importer; live SQL via `dbhealth` fixed queries, HTTP-only report | **conflicts** | `internal/importer/sqlserver/querystore.go:21-26`, `internal/importer/sqlserver/querystore.go:50-55`, `internal/server/health.go:289`, `internal/dbhealth/sql.go:64-78` |
| **§3.3** `ApplicationIntent=ReadOnly` on connection string | DSN built from target fields; no `ApplicationIntent` in URL builder | **conflicts** | `internal/dbhealth/mssql.go:95-97`, `internal/dbhealth/mssql.go:101-120` |
| **§3.4** Zero credential persistence in case file | Passwords in keeper; case workspace blob holds non-secret Mongo reference (`savedConnectionId`, DB names) | **matches (spirit)** | `internal/casestore/workspaceblob.go:5-7`, `internal/keeper/audit.go:19` (read-only ops; password in keeper) |
| **§4.1–4.2** Tier-1 macro ingest + lazy hydration flags + `fetched_coverage_ledger` | IIS/logs fully in case; Mongo payloads fetched on demand via `Payload` but not cached in SQLite with hydration flags; no coverage ledger table | **conflicts / partial** | `internal/store/payload.go:17-41`; ledger table **absent** (spec-only in repo) |
| **§5** Per-layer relational schema | Single `events` + `imports` + tags/ledger blobs | **conflicts** | `internal/casestore/sqlite/schema.go:24-44`, `internal/casestore/workspaceblob.go:10-21`, `internal/casestore/sqlite/ledgerblob.go:21` |
| **§6 steps 1–7** Launchpad, new case, sources, tracing paper, close checkpoint, reopen offline | Start/case API + wizard; lifecycle open/close/archive/erase/restore/lock; workspace blob + client session; WAL checkpoint on close/import | **partial** | `internal/casestore/lifecycle.go:15-64`, `internal/casestore/sqlite/case.go:184-185`, `internal/server/routes.go:157-158` |
| **§6 step 1** `workspace.json` recent cases | Cases listed from store manifest/API; connections via `/api/connections/recent` | **conflicts** | `internal/casestore/sqlite/manifest.go:19-22`, `internal/server/routes.go:110-113` |
| **§6 step 3.5** Pre-analysis route/status/tenant filters before heavy work | Investigation filters exist in workspace snapshot and query APIs (different UX gate) | **partial** | `internal/casestore/workspaceblob.go:5-7` |
| **Read-only promise** Live `currentOp` / `system.profile` / DMVs into case | `dbhealth` uses read-only command templates (e.g. `profile:-1`, fixed SQL); results not written to case; profiler “switch on” guarded | **partial / conflicts** | `internal/dbhealth/lane_mongo.go:11`, `internal/dbhealth/lane_mongo.go:207`, `internal/dbhealth/lane_mongo.go:282-283`, `internal/dbhealth/sql.go:103-123` |

**Importers present vs spec tables (summary):** W3C, HTTPERR, FREB (derived), Query Store export, XE export, Mongo `serverStatus` / `currentOp` / `profile` / `mongostat` exports (`internal/importer/all/all.go:31-55`) map to **`events` rows**, not spec table names. SQL text/plans deliberately dropped on Query Store import (`internal/importer/sqlserver/querystore.go:50-55`).

### New work implied by the spec (not in app today)

| Size | Item |
|------|------|
| **L** | Replace encrypted folder + unified `events` model with spec `.unravis.db` schema and offline-first macro ingest for Mongo audit + SQL QS/DMV. |
| **L** | `fetched_coverage_ledger`, window-delta fetch, and SQLite-backed lazy hydration (`is_payload_hydrated` / `is_plan_hydrated`). |
| **M** | 3-tier implementing-agent resolver tied to route templates. |
| **M** | Tracing-paper UI: multi-layer synchronized timeline, delta IIS vs audit, HTTPERR markers, Win32-partitioned series (beyond current timeline/resource views). |
| **M** | Live SQL/Mongo pull pipelines that persist into case (today: live audit + ephemeral health). |
| **S** | W3C custom-field JSONB overflow; explicit Win32=0 vs 64/995 aggregation switches. |
| **S** | Spec launch registry (`workspace.json` / environment profiles) aligned with existing recent-connections + case list APIs. |
| **S** | `PRAGMA temp_store=MEMORY` on default case DB open; optional 10k sub-batches if single-TX imports become a bottleneck. |

## Recommendation

**Adopt with changes** — reuse proven ingest semantics (W3C/HTTPERR, route folding, interval timestamps, file importers) but do not fork storage or lifecycle around `.unravis.db` as written.

1. **Keep** encrypted `case.db` + export format; add spec concepts as views or derived indexes over `events`, not parallel table families (**L** avoided).
2. **Extend** lazy payload fetch (`internal/store/payload.go`) with optional case-local cache and explicit hydration flags before building a full coverage ledger (**M**).
3. **Clarify** live vs imported SQL/Mongo: health/lanes stay ephemeral; only user-chosen windows get persisted imports (**M**).
4. **Drop** reliance on `ApplicationIntent=ReadOnly` as a safety control; keep fixed allowlisted queries (`internal/dbhealth`) (**S** policy).
5. **Defer** 3-tier implementing-agent matching until route/`Controller.Action` correlation proves insufficient (**M** optional).
---
## Review B
## Verdict

The architecture is competent and mostly implementable, but several *technical* claims that the whole correlation story rests on are wrong or overstated. The most serious are: (1) the §7 tooltip that `time-taken` ends at the client's final ACK and "includes network transmit latency" — Microsoft states the opposite ("time-taken does not reflect time across the network"); (2) §2.2 applying a `#Fields` parser to `httperr*.log`, which has **no column headings at all**; (3) §2.5 claiming that partitioning by `sc-win32-status` proves "network vs backend", which the cited codes cannot do (995 is explicitly a *server/application-side* abort); and (4) §3.3 treating `ApplicationIntent=ReadOnly` as a safety/read-only guarantee. Section 4's caching design is sound in shape but its `<15 ms`/`80k–150k rows/s` numbers are unmeasured. Query Store field naming also hides a µs-vs-ms conversion trap. **Recommendation: adopt with changes.**

## Claim vs. reality

| Claim | Correct? | Why | Conf. | Source |
|---|---|---|---|---|
| §7 tooltip: `time-taken` = first byte received → **final response ACK** by client; "includes network transmit latency" | **No** | Win32 docs: timer "initialized when HTTP Server API receives the first byte… stopped when the **last send completion** occurs. **Time-taken does not reflect time across the network.**" Last *send completion*, not ACK. | High | MS Learn, W3C Logging; IIS `<logFile>` docs (TimeTaken) |
| §2.4 `start_utc_ms = log_timestamp − time_taken` is a "mathematically" sound start estimate | **Partly** | Directionally right (timestamp is completion-time; time-taken is server duration), but it is an *estimate*: W3C time is second-granular, the log line is written after completion, and MS notes the **first request in each file is inflated** by log-file open. Not "mathematical". | High | MS Learn W3C Logging |
| §2.4 W3C timestamp = request **completion** | **Yes** | #Date is "when the first log file entry was recorded"; time = UTC at which activity occurred (logged on completion). | High | MS Learn W3C Logging |
| §2.4/§1 both logs are UTC | **Yes** | W3C Time = "coordinated universal time (UTC)". HTTPERR Date/Time "based on UTC", W3C format. | High | MS Learn W3C Logging; HTTP Server API Error Log Format |
| §2.4 W3C & HTTPERR share the same timestamp format | **No** | W3C: separate `date` (`YYYY-MM-DD`) + `time` (`HH:MM:SS`) fields. HTTPERR: same values but **no header**, fixed column order. Parsers must differ. | High | HTTP Server API Error Log Format |
| §2.5 partition latency by `sc_win32_status ∈ (64,995,1236)` **proves** "network vs backend" | **No** | Codes exist, but they are ambiguous. MS: 995 = "I/O operation… aborted because of either a **thread exit or an application request**" → app-pool recycle / server timeout, *not* network. A slow client draining a socket yields a **normal 0** status with huge time-taken (post-send flush is partly outside time-taken, but response send *is* in scope). Status partitioning ≠ causality. | High | Win32 System Error Codes 500–999; spec's own §7 tooltip admits 995 = recycles/timeouts |
| §2.5/§7 `64 = ERROR_NETNAME_DELETED` (client reset) | **Yes** | "The specified network name is no longer available." | High | Win32 System Error Codes 0–499 |
| §2.5/§7 `995 = ERROR_OPERATION_ABORTED` | **Yes** (code), tooltip "client aborts" **partly** | Code correct; but primarily server-side abort (thread exit/application request). Listing it as a "Client/Network Abort" is wrong. | High | Win32 System Error Codes 500–999 |
| §2.5/§7 `1236 = ERROR_CONNECTION_ABORTED` | **Yes** | "The network connection was aborted by the local system." | High | Win32 System Error Codes 1000–1299 |
| §2.2 stateful `#Fields` re-binding for **`u_ex*.log`** | **Yes** | W3C files carry `#Fields:`; re-emitted on config change. | High | MS Learn W3C Logging |
| §2.2 stateful `#Fields` re-binding for **`httperr*.log`** | **No** | "HTTP Server API error log files have the same format as W3C error logs **except that [they] do not contain column headings**." No `#Fields`, no re-binding possible. | High | HTTP Server API Error Log Format |
| §2.2 `-` tokens → SQL NULL | **Yes** | MS: unselected/unavailable fields use `-` placeholder. | High | MS Learn W3C Logging |
| §2.3 "**16** standard W3C columns" | **Partly** | Default `logExtFileFlags` is **15** fields (Date, Time, ClientIP, UserName, ServerIP, Method, UriStem, UriQuery, TimeTaken, HttpStatus, Win32Status, ServerPort, UserAgent, HttpSubStatus, Referer). "16" is wrong; count depends on config. | High | IIS `<logFile>` docs |
| §2.3 custom fields beyond standard set exist | **Yes** | IIS 8.5+ `customFields`, W3C-only. | High | IIS `<logFile>` docs |
| §2.2/§2.1 FREB excluded, W3C only post-pipeline | **Mostly yes** | Kernel-served/denied requests log to HTTPERR, not W3C. (Kernel-cache-served static can bypass worker logging — edge case.) | Med | From memory |
| §2.3 JSONB available in SQLite **3.45+** | **Yes** | JSONB introduced 3.45.0 (2024). | High | sqlite/doc/jsonb.md |
| §2.3 JSONB "avoids runtime ALTER TABLE locks"; implies text→JSONB automatic | **Partly** | JSONB must be written via `jsonb(...)`/`json()`; apps must not assume text is stored as JSONB. Extraction is faster but not free. | High | sqlite/doc/jsonb.md |
| §2.7 `PRAGMA synchronous=NORMAL` "eliminates fsync barriers" without durability caveat | **Partly** | True for throughput; but with WAL+NORMAL the last commits **can be lost on power loss/OS crash** (only crash-safe, not power-safe). Spec presents it as lossless. | High | SQLite WAL docs (from memory) |
| §2.7 80,000–150,000 rows/s | **Unverified/plausible** | Achievable for simple indexed writes in large WAL transactions on SSD; *not* end-to-end (parse+typing+multi-index) and **not measured** in the spec. | Low | None cited |
| §2.7 deferred `CREATE INDEX` + `PRAGMA optimize` after bulk load | **Yes** | Standard, effective. | High | From memory |
| §2.7 `BEGIN IMMEDIATE` 10k batches | **Yes** | Correct way to avoid SQLITE_BUSY upgrade deadlock. | High | From memory |
| §3.3 catalog view names `sys.query_store_*` | **Yes** | All five named views exist. | High | SQL docs |
| §3.3 "15- or 60-minute intervals" | **Partly** | `interval_length_minutes` **default = 60**; allowed 1,5,10,15,30,60,1440. "15" is the **flush interval** (900 s), a different setting — likely confused. | High | `sys.database_query_store_options` docs |
| §3.3/§4 Query Store runtime durations as `_ms` | **No (units)** | `sys.query_store_runtime_stats.avg_duration` is in **microseconds**; must ÷1000. Wait stats are in **ms**. Schema label `avg_duration_ms` hides a unit bug. | High | `sys.query_store_runtime_stats` docs |
| §4 wait categories `Lock, Buffer IO, CPU, Memory` | **Yes** (subset) | Real categories incl. CPU, Lock, Buffer IO, Network IO, Parallelism, Memory, Tran Log IO, Other Disk IO, etc. | High | `sys.query_store_wait_stats` docs |
| §3.3/§4 QS wait stats + average-vs-total | **Partly** | `sys.query_store_wait_stats` exposes `total_query_wait_time_ms` and `avg_...`; intervals can have **multiple rows per plan** (disk + in-memory) needing aggregation. Spec never warns; naive `AVG(avg_duration_ms)` across intervals is a classic weighting error. | High | `sys.query_store_wait_stats` / `runtime_stats` docs |
| §3.3 QS read on secondary via `ApplicationIntent=ReadOnly` | **No/overstated** | `ApplicationIntent=ReadOnly` "doesn't turn a database read-only, replace permissions, or route ordinary read-only databases" and needs AG + read-only routing. QS on secondaries relies on the **secondary-replica feature** (data aggregated to primary); absent it, data is missing. Also QS is OFF by default on SQL 2016–2019. | High | SqlClient HA/DR docs; Query Store docs |
| §3.3 permissions | **Omitted (gap)** | QS views need `VIEW DATABASE STATE`; server DMVs (`dm_os_wait_stats`, `dm_exec_requests`, `dm_tran_locks`) need `VIEW SERVER STATE`. Spec never states this. | High | Catalog-view docs |
| §3.3 historical windowed `sys.dm_os_wait_stats` | **No** | Cumulative since restart; only deltas between snapshots are meaningful. Can't "pull wait stats overlapping the Case window". | High | From memory |
| §3.1 `serverStatus`, `$indexStats`, `collStats`, `system.profile`, `currentOp` real fields | **Yes** | All real. `$indexStats` needs **`clusterMonitor`**; `currentOp` needs **`inprog`** and runs on `admin`; `system.profile` only exists if profiling enabled (a *write*), and newer versions forbid reading it inside a transaction. | High | MongoDB docs |
| §3.1 "slow operations (`currentOp` / `system.profile`)" | **Partly** | `currentOp` is a **point-in-time** snapshot, not slow-op history; `system.profile` requires enabling writes to a capped collection — conflicts with "read-only". | High | MongoDB docs |
| §3.3 "Zero credential persistence" (passwords) | **Design claim, not technical fact** | Fine as intent; depends on implementation (secret store), not verifiable from spec. | n/a | — |
| §3.2 `applicationHost.config` sites/logFile parsing; `ErrorLoggingDir` registry default `…\LogFiles\HTTPERR` | **Yes, plausible** | Path and element names are right; env-var expansion needed. Registry value spelling unverified here. | Med | From memory (registry value) |
| §4 "zero network calls / never fetch twice / <15 ms" | **Partly** | The coverage-ledger design correctly prevents re-fetch; latency claim unmeasured and depends on percentile queries over large spans. | Low | None cited |
| §7 HTTPERR `s_queuename` = Application Pool Name | **Partly** | Modern IIS HTTPERR adds a trailing queue/pool name, but the documented format's last field is the **Reason Phrase** (no queue field). Don't hard-require it. | Med | HTTP Server API Error Log Format |
| §7 `Connection_Dropped` = request dropped | **No** | MS: "IIS failed to return the **logging data**… due to a client disconnect… The request was **not necessarily dropped**." | High | Types of Errors Logged |
| §2.6 3-tier agent matching incl. "correlate `correlation_id` to DMV execution contexts" | **Unsure/partly** | Tiers 1–2 are standard; Tier 3's DMV correlation is speculative — DMVs carry no HTTP correlation id. | Med | From memory |

## Claims that are marketing, not engineering

- **"mathematically isolates"** latency locus (§1, §2.5) — no such math; status codes and duration subtraction are heuristics.
- **"Never Fetch Twice"** (§ title 4) and **"zero network calls"** — design intent, unproven.
- **"80,000–150,000 rows/second"** (§2.7) and **"instant `<15 ms`"** (§4, §5) — no benchmark, hardware, or dataset cited; presented as fact.
- **"O(1)"** route lookup and **"sub-15ms percentile"** (§2.3, §2.6) — unsupported precision.
- **"prevents… corrupt latency data"** (§2.2) stated as guaranteed when the same parser is mis-applied to HTTPERR.
- **"reads 100% offline"**, **"restores… in `<100 ms`"** (§6) — unmeasured.
- Tooltips in §7 that assert causality ("proves", "excludes network") that the underlying codes do not support.

## What a careful engineer would change

1. **Rewrite the `time-taken` tooltip and §2.4 rationale.** State that `time-taken` ends at the last *IIS send completion* and **excludes** network transit; the ACK/"includes network" wording is factually wrong. Present `start = completion − time_taken` as an **estimate** with second-granularity and first-row-inflated caveats.
2. **Drop the `#Fields` parser from HTTPERR.** Use two parsers: stateful `#Fields` for W3C; fixed-order, no-header parser for HTTPERR. Verify the trailing queue-name field per IIS version instead of assuming it.
3. **Stop claiming `sc-win32-status` partitions "network vs backend".** Reclassify 995 as server/application-side abort; keep 64/1236 as client/network indications; document that clean-status requests can still be network-slow. Expose this uncertainty in the UI copy.
4. **Fix units and aggregation for Query Store:** divide runtime `avg_duration`/`max_duration` by 1000 (µs→ms); store `count_executions` and compute totals as `Σ(avg×count)`, never `AVG(avg)`. Document the 60-minute default interval and multiple-rows-per-plan aggregation. Add permissions (`VIEW DATABASE STATE` / `VIEW SERVER STATE`) and an explicit "QS OFF / read-only / no secondary replica" degraded-state UX.
5. **Correct the read-only/`ApplicationIntent` story:** state that read-only safety comes from permissions and DB read-only state, not the intent keyword; make it a hard requirement to use a least-privilege login. Add dependency/licensing notes for SQLite JSONB (driver must bundle ≥3.45 — verified OK for `mattn/go-sqlite3` 3.53.4 and current `modernc.org/sqlite`), Mongo privileges (`clusterMonitor`, `inprog`), and the fact that `system.profile` requires enabling writes (conflicts with read-only).

Secondary: state the WAL+NORMAL power-loss caveat; correct "16 standard columns" → 15 defaults; mark all throughput/latency figures as targets to be benchmarked.
---
## Review C
## Verdict

The external spec describes a **greenfield incident workstation** (standalone `.unravis.db`, four telemetry layers, coverage ledger, lazy hydration, live SQL/Mongo pulls). That overshoots the owner’s MVP: **sources → Start tracing → one UTC timeline with audit, SQL, and IIS**, then drill-down to what already exists. Much of the IIS ingest detail **matches or extends code you already have** (`#Fields`, HTTPERR, stem `{id}` folding, optional end-time math). The dangerous part is treating the external doc as the build plan—it implies a second storage model, fourth layer, and unverified performance numbers while **our spec already lists acceptance criteria and caps** the real app enforces (`MaxTimeBuckets`, `MaxEventQuery`, 7-day window). **Adopt ideas, not the architecture wholesale.**

---

## 1. Scope creep vs MVP (Keep / Later / Drop)

| Item | Verdict | One-line reason |
|------|---------|-----------------|
| **4 layers** (Mongo app + host metrics) | **Later** | Owner scoped out host/Blackbox lane (`OUR-TRACING-SPEC` §0). |
| **Separate `.unravis.db` + full SQL schema** | **Drop** (MVP) | App stores imports in encrypted **`case.db`** events, not EXT tables (`internal/casestore/store.go:1-8`). |
| **`fetched_coverage_ledger` + pan +2h fetch** | **Later** | No ledger in repo; MVP recomputes read-only runs (`OUR-TRACING-SPEC` §8). |
| **Two-tier lazy hydration** (payloads, XML plans) | **Later** | MVP forbids payloads in tracing APIs; `/api/payload` is separate (`routes.go:214`). |
| **3-tier implementing-agent matching** | **Later** | No route→handler map in codebase; high build cost vs one chart. |
| **Pre-analysis filter gate** (route regex, tenant, win32) | **Later** | OUR marks audit route filter **Later** (TR-14); reuse investigation filters instead of a new gate. |
| **Tooltip dictionary (§7)** | **Later** | Nice UX; app has glossary/help already (`Glossary.svelte` per CURRENT-APP-FLOWS). |
| **Delta anomaly overlay** (IIS ≫ audit) | **Later** | High value idea; not required for “see three lanes.” |
| **Launchpad / ± New Case / env profiles redesign** | **Later** | Existing create wizard + case home (`CaseHome.svelte:452-463`). |
| **Tracing toolbar in canvas header** | **Later** | Polish; reuse Pulse zoom/brush first (`OUR-TRACING-SPEC` §5). |
| **Live SQL Query Store + DMV snapshots into case DB** | **Later** | MVP SQL lane = **imported** `sql-*` events; live reads exist as **`/lanes/sql`** (`routes.go:134-136`, `lanes.go:21-22`). |
| **Mongo App DB diagnostics (Layer 4)** | **Later** | Same as host lane—owner **Never/Later**. |
| **Stateful `#Fields` parser** | **Keep** | Already shared IIS reader (`internal/importer/iis/common.go:159-252`). |
| **W3C + HTTPERR ingest** | **Keep** (partial) | Importers **`iis-w3c`**, **`iis-httperr`** exist; MVP chart can start W3C-only, HTTPERR as **Later** markers. |
| **`start`/`end` span for IIS (`time-taken`)** | **Keep** (with caveat) | Code applies end-time math when `time-taken` present (`w3c.go:175-188`); **`w3cTimeIsEnd` still UNVERIFIED** (`w3c.go:14-21`). |
| **Route `{id}` normalization on stems** | **Keep** | IIS importers fold IDs (`fix3_test.go:166-177`; attrs `stem`). |
| **`sc-win32-status` partitioning** | **Later** | Parsed into attrs (`w3c.go:139-143`); chart lane model has **no HTTP status field** (`calltrace/types.go:71-78`). |
| **Exclude bulk FREB from SQLite** | **Keep** (concept) | FREB importer exists but events ≠ EXT’s “index only”; don’t bulk FREB into timeline MVP. |
| **JSONB overflow + SQLite 3.45 tuning** | **Drop** (MVP) | Wrong storage layer; perf claims unverified in this app. |
| **No secrets in case files** | **Keep** | Aligns with keeper + scrub patterns (CURRENT-APP-FLOWS Part 2 export note). |

---

## 2. Where the specs disagree (and who wins)

| Topic | EXT-ARCH | OUR-TRACING (+ errata) | Better supported by |
|-------|----------|------------------------|---------------------|
| **Storage** | Dedicated telemetry tables in `.unravis.db` | Case **events** + live **`POST /api/analyze`** for audit | **`case.db`** + importers (`casestore/store.go`); no `start_utc_ms` columns anywhere in `internal/` (grep: none). |
| **IIS timestamps** | Definite `end = log time`, `start = end − time-taken` | Same assumption, must label uncertainty | **`w3cTimeIsEnd = true`** with explicit UNVERIFIED comment (`w3c.go:14-21`). |
| **HTTPERR** | Layer 2B + vertical markers | IIS lane from **`iis-w3c`**; HTTPERR not in MVP lane table | Importer **`httperr.go:39+`**; not wired to a unified chart yet. |
| **FREB** | Do not bulk-ingest XML | Not on MVP chart; importer exists | **`freb.go`**; privacy tests drop cookies/headers (`freb_test.go` themes). |
| **Win32 / client aborts** | Split aggregations (0 vs 64/995/1236) | Not in lane payload | **Attrs** `win32` on events; **not** on `LaneEvent` (`types.go:71-78`). |
| **Window / caps** | Case window + ledger expansion | ≤ **7 days**, **120** buckets, **50k** events | `audit/models.go:17-19`; `audit/query.go:39-40`; `casestore/store.go:101-102`. |
| **Filters** | Pre-run whitelist/blacklist, tenant, IP subnets | Optional audit route **Later**; TZ gate disables multi-source | `timeline.go:363-378` (needs ≥2 sources + validated TZ). |
| **SQL lane data** | Live Query Store intervals in SQLite | Imported **`sql-*`** events; errata adds **live lane** option | Importers + **`POST .../lanes/sql`** (`lanes.go:21-22`). |
| **Call trace UI** | (Not central) | Errata: **wired** from Evidence | `EvidenceView.svelte:105`; tests `callTrace.test.cjs:307-310`. |
| **Privacy on IIS fields** | Store **`cs_cookie`, `cs_referer`, `client_ip`** in SQLite | No payloads in tracing response | Parser **drops** cookies, referers, IPs (`common.go:5-7`)—**EXT conflicts with app policy**. |

---

## 3. Ideas worth stealing (and where they land)

| Idea | Map onto existing app |
|------|------------------------|
| **Start/end spans** | Already on W3C events when `time-taken` exists; tracing API should expose **bucket bounds in UTC** like analyze, with IIS footnote from `gapW3CTimeEnd` (`w3c.go:24-30`). |
| **Delta anomaly (IIS vs audit)** | **Later** derived series: join audit **`duration_ms`** (analyze) to IIS **`DurationMs`** on overlapping windows—no new DB. |
| **Win32 partition** | **Later** IIS lane aggregator: split counts by `attrs["win32"]` before charting; needs lane API fields OUR errata flags missing. |
| **Pre-analysis filters** | Reuse **`investigationRequest`** windows + filter chips (`investigation.go:51-65` per OUR errata)—not a blocking Step 3.5 wizard. |
| **HTTPERR markers** | Import **`iis-httperr`** events as point markers on IIS lane (`QueueFull`, etc.—`httperr.go:25-31`). |
| **`#Fields` mid-file** | No change—keep **`common.go`** parser. |
| **Stem templates `{id}`** | Already on **`stem`** attr; audit lane can group by controller/action from Mongo, not EXT’s `route_template` column. |

---

## 4. Risks (external spec)

| Risk | Detail |
|------|--------|
| **Unmeasured performance** | “&lt;15 ms zoom”, “80k–150k rows/s”, “&lt;100 ms reopen”—no benchmarks in repo. |
| **Empty / error states** | EXT workflow skips “no SQL tag”, “truncated”, “TZ rule”—OUR has AC-01–AC-07; EXT has none. |
| **Privacy / sharing** | EXT schema stores **PII-class IIS columns**; app **refuses** to parse cookies/referers/IPs (`common.go:5-7`). Storing them in a shareable `.unravis.db` fights export/scrub design. |
| **No acceptance criteria** | OUR §9 is testable; EXT is aspirational. |
| **No test strategy** | EXT silent; real IIS semantics **synthetic-only** (`common.go:2-3`). |
| **Implementer traps** | Two case models (`.unravis.db` vs `case.db`); “Layer 3 live SQL” vs import-only MVP; **`/api/analyze` = one series per cohort**—audit lane needs explicit aggregation (OUR errata). |
| **Call trace expectations** | UI **is** reachable (Evidence → Nearby); OUR TR-12 “don’t wire tray” is **stale**—decide link vs duplicate. |

---

## 5. Open decisions (owner, ranked)

| # | Decision | Recommendation |
|---|----------|----------------|
| 1 | **Extend `case.db` events** vs build EXT SQLite schema? | **Extend**—one case model, one export story. |
| 2 | **Confirm IIS `time` = end** before latency alignment? | **Yes**—flip `w3cTimeIsEnd` only after owner samples (`w3c.go:14-21`). |
| 3 | **SQL lane MVP:** imports only vs **live `/lanes/sql`** series? | **Imports + plain copy**; optional “Read SQL day” as **Later** enrich. |
| 4 | **Audit lane:** how to aggregate **`/api/analyze`** cohorts? | **One “all APIs” aggregate** client- or server-side (errata). |
| 5 | **HTTPERR on MVP chart?** | **Later** markers unless kernel drops are the incident. |
| 6 | **Swim lanes vs single axis?** | **Stacked lanes** (OUR D-1). |
| 7 | **Tracing entry vs Guided / Run analysis?** | **Parallel** on case home (OUR D-2); tracing drill-down → **Run analysis** (TR-09). |
| 8 | **New `POST .../tracing` vs extend `/timeline`?** | **New route** (OUR D-6)—text timeline UI already exists (`CaseTools` / errata). |
| 9 | **IIS status-class bars?** | **Later** until lane payload carries status (errata). |
| 10 | **Demo proves three lanes?** | **Seed demo case events** or defer AC-09 (errata). |

**If I were the owner…** I would ship **one read-only tracing screen** that buckets **live audit + case SQL/IIS imports** on shared UTC, reuse **Pulse zoom + analyze drill-down**, show **plain missing-lane strips**, and treat everything in EXT §4–§7 (ledger, hydration, Layer 4, tooltips, new DB) as **Phase 2**—after IIS time semantics are confirmed and a seeded demo passes three-lane acceptance.

---

## Recommendation

**Adopt with changes** — steal ingest/time-alignment **ideas**, reject the **parallel database product**.

**Five changes (ranked):**

1. **Do not implement `.unravis.db` / coverage ledger for MVP**—use **`casestore` events + analyze** (`store.go:101-102`, `routes.go:192`).
2. **Fix the product map:** text timeline UI exists; call trace is **Evidence-reachable**—update TR-12/backlog (`EvidenceView.svelte:105`; `callTrace.test.cjs:307-310`).
3. **Define audit lane aggregation** explicitly (multi-cohort `/api/analyze`), not a second chart product.
4. **Match privacy:** no cookie/referer/IP columns in tracing storage—keep **`common.go:5-7`** policy, reject EXT §5 IIS table fields.
5. **Gate IIS latency claims** on owner confirmation of **`w3cTimeIsEnd`**; until then, counts + status where attrs allow, footnotes always (`w3c.go:14-21`, `types.go:71-78`).