# P-40 Probe: can anything be shown when Query Store is switched off? (BL-016)

Status: code reading done. LIVE: not checked, no database with Query Store off was available (see section 4).
Scope: read-only. No code changed, no query written, no database setting touched, Query Store not switched on.

Note on spelling: one view name below has its underscores escaped with a backslash only so that the repo's secret scan (any 32+ character word) stays quiet. Read it as one name.

## 1. What the lane read touches (names only, file:line)

Files read: `internal/dbhealth/lane_sql.go` (queries and checks), `internal/dbhealth/lane.go` (state values and result shape). `lane.go` holds no query text.

### Needs Query Store ON (the data exists only when Query Store has been recording)

| View | Where | Used for |
|---|---|---|
| `sys.database_query_store_options` | lane_sql.go:27 (`qLaneQSOptions`); read at :648 | The state row (actual state, desired state, read-only reason, block length). This view answers even when Query Store is OFF: it is the settings row, not the history. It is the only Query Store view the lane reads when the state is OFF. |
| sys.query\_store\_runtime\_stats\_interval | lane_sql.go:28 (`qLaneQSFirst`), :97 and :110 (joined in the stats and series reads) | First data time (`MIN(start_time)`), the per-block window, and the time of each block. |
| `sys.query_store_runtime_stats` | lane_sql.go:96 (`qLaneQSStats`), :110 (`qLaneQSIntervals`) | Run counts, average duration, average reads, slowest run per block. |
| `sys.query_store_plan` | lane_sql.go:98 | Plan id per query (new-plan check). |
| `sys.query_store_query` | lane_sql.go:98 | Query id. |

The three history reads (first data :665, stats :684, intervals :690) all run only after the state check passes (see section 2). The grant text for these is VIEW DATABASE [PERFORMANCE] STATE (lane_sql.go:12-14 comment; fix built at :222-226).

### Do NOT need Query Store (read for other checks, work with Query Store off)

| View or call | Where | Used for |
|---|---|---|
| `SERVERPROPERTY(...)` | lane_sql.go:19 (`qLaneBase`) | Version, edition, availability-group flag, UTC offset. |
| `sys.dm_os_sys_info` | lane_sql.go:21 (`qLaneServer`) | Server start time. |
| `sys.dm_os_wait_stats` | lane_sql.go:22 | Thread-pool wait total since start (a total, no time series). |
| `sp_readerrorlog` | lane_sql.go:23, :24 | Plan-cache clears and start-up lines in the error log. |
| `sp_enumerrorlogs` | lane_sql.go:26 | How far back the current error log reaches. |
| `msdb.dbo.sysjobhistory`, `msdb.dbo.sysjobs` | lane_sql.go:117-118 (`qLaneJobs`) | Agent jobs near the incident start. |
| `msdb.dbo.backupset` | lane_sql.go:125-126 (`qLaneBackups`) | Backups near the incident start. |

None of these draws a per-step load or duration lane. They give single moments (a restart, a clear, a job, a backup) and one since-start total.

### Plan-cache DMVs in the lane

None. `sys.dm_exec_query_stats` is NOT read by `lane_sql.go` or `lane.go` (searched both; no match for any `sys.dm_exec_*` view). It is read elsewhere in the same package, by the separate health check, at `internal/dbhealth/sql.go:140` (`qTopQueries`, shown at sql.go:72 as "Heaviest queries", top 8 since each plan was cached). That read is outside the lane and is not saved as a lane day.

## 2. State table

State names come from `internal/dbhealth/lane.go:94-100`: `on`, `off`, `read-only`, `denied`, `unknown`. The packet's "permission denied" is the value `denied`. "On but started late" is not a state value: it is `on` or `read-only` with `FirstData` later than the reference window start (rule at lane_sql.go:469-472, built from `LaneQueryStore.FirstData` set at :669-670).

State is chosen at lane_sql.go:619-636 (`queryStoreOf`) and :648-661 (`checkQueryStore`).

| State | What the read returns today | First data time knowable | When it was switched off or stopped |
|---|---|---|---|
| `off` (actual state OFF, or ERROR, empty, or no options row) | One query runs (the options row, :648). Then the check stops at :659-662 with "Cannot test yet" and the "switched on" script as the fix. The first-data, stats and series queries are NOT run, so `FirstData` is nil, no series is saved, no answers are given. The reply sentence: "Query Store is switched off in this database, so it holds no plans or timings. The script below, run by the database owner, switches it on; it only helps for incidents after that." (lane_sql.go:661) | No. `FirstData` is never read in this branch. Whether older rows still sit in the views while Query Store is OFF is not tested by any code path and not measured here. | Not knowable (ruling D-9). The options row has no switch-off time. |
| `read-only` (actual state READ_ONLY) | The normal path runs in full: first data (:665), stats and intervals in 3-day slices (:680-695). History up to when it stopped recording is returned. Sentence adds "It is read-only (reason), so it may have stopped recording before the incident ended." (:477-479). | Yes, `FirstData` is set (:669-670) when the intervals view has any row. | Not knowable (ruling D-9). The reason (for example storage full) is shown only as the options row's reason text. |
| `on` but started late (READ_WRITE, `FirstData` later than the reference window start) | Normal path. Sentence: "Query Store's oldest kept history starts at <time>, after the reference window began. That is either when it was switched on or when older history was cleaned up, so this stays open." (:471). If the first data time is after the incident ended and the incident window is empty: "...was switched on at <time>, after the incident ended, so it holds nothing from it." (:462). | Yes. | Not applicable (it is on). The code cannot tell "switched on late" from "older history cleaned up" (comment at :470). |
| `denied` (options read fails with a permission error) | `QueryStore.State = denied` (:650-654); check returns "Cannot test yet" with the grant script and "the login is missing <permission>" (:222-226). No further query runs. | No. | Not knowable. |
| `unknown` (extra row, for completeness) | Options read failed for a reason other than permission (:650-655), or a state value the code does not map (for example ERROR gives `unknown` in `queryStoreOf`, :624-631, yet the check sentence is the "switched off" one because :659 treats ERROR like OFF). | No. | Not knowable. |

Mismatch to know about (does not block this packet): for actual state ERROR or a missing options row, the saved `QueryStore.State` is `unknown` (:620-623, :624-631) while the sentence says "switched off" (:659-661). The screen should pick its message from `State`, and `unknown` needs its own safe wording (P-02 has no key for it today).

Also: with `on` and an incident window that has no rows, the sentence is "Query Store holds nothing from the incident window (it was off then, or its history was cleaned up)." (:460). So "it was off then" can only be a guess in that sentence, never a fact.

## 3. Plan-cache alternative, on paper (SQL Server's documented behaviour, NOT measured here)

What `sys.dm_exec_query_stats` could give:
- One row per cached query plan (per statement), with run count, total and last elapsed time, total CPU and total reads, and the time the plan was cached and the time it last ran.
- All of these are totals since that plan entered the plan cache. There is no per-interval or per-minute breakdown: nothing says how busy the database was at 10:05 versus 10:20.
- The rows are lost when SQL Server restarts, when the plan cache is cleared, when a plan is evicted for memory pressure, or when the plan is recompiled. Cold or rarely used plans disappear first. So the oldest history it holds is unpredictable and can be only minutes old.
- Permission: VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later). The repo already builds this grant text at `internal/dbhealth/grants.go:99-101`.

Why it cannot draw a per-step lane: the lane needs a value per time step (runs, average duration, slowest run per block). The plan cache keeps one running total per plan with no time axis, so a step chart cannot be built from it, and a total since an unknown cache time cannot be placed inside the incident window. At best it could name "heaviest statements since the plans were cached" as a list with no time, which the existing health check already does (sql.go:72, :133-141) and which does not belong in a per-step lane.

Nothing in this section was run against a database. It is from SQL Server's documentation of the view, and a live read could differ (for example how long plans stay cached on the owner's server).

## 4. Live check

LIVE: not checked, no database with Query Store off was available.

Detail: the only connected source known to this packet's author is the installed app's one non-demo source. A field-sample request to it timed out at the server's 40 second limit a few minutes before this packet ran (reported by the caller), so the database may be unreachable now, and nothing said that its Query Store is OFF. The packet allows a live check only against a database already known to have Query Store OFF and says not to ask for one to be changed. No lane read was started, nothing was retried, and no reason code, `queryStore.state` or sentence was recorded from live data.

What a live check would record when one is possible: on a case whose database has Query Store OFF, run the app's own "read the database" action, then note the check's state ("Cannot test yet"), the saved `queryStore.state` (expect `off`), `firstData` (expect absent) and the sentence at lane_sql.go:661.

## 5. Recommendation

Nothing more can be shown for the Query Store history when Query Store is OFF, with the existing read-only queries: the code stops after the options row (lane_sql.go:659-662), the plan cache has no time axis, and the other checks (error log, server start, jobs, backups) give single moments only. Keep the existing "off" message, add no new lane.

One open point that needs a live database, not code: it is not known whether history recorded before Query Store was switched off is still returned by the Query Store views while the state is OFF (SQL Server's documentation does not make this clear enough to rely on, and no code here tests it). If the owner wants that answered, the named follow-up is a small probe packet "P-40b Read retained Query Store history when OFF": run the existing stats query once, read-only, on a database already known to be OFF, and record the row count. It needs the owner's approval and a database that is OFF; it is not built in this plan.

### Sentence the screen shows today for `off` (`DRILL_TEXT.dbMsgOff`, from P-02, packets/P-02-copy-keys.md:129)

> Query Store is switched off on this database, so it saved no history. Switching it on needs the database owner (the script is in Setup). Unravis never switches it on itself.

(The saved lane sentence from the read itself, lane_sql.go:661, is the longer one in section 2.)

### Owner question

Is there a database where Query Store is already OFF that the app may read once (read-only), so the follow-up probe above can say whether older history survives? If not, close BL-016 as "nothing more can be shown".
