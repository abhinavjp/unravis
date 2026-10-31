# D1-SQL: Evidence card data shape, colour rule and SQL scenarios

## 1. Schema

One JS object per evidence card, exactly these 9 fields.

| Field | Type | Allowed values | Meaning | Example |
|---|---|---|---|---|
| `id` | string | lowercase-dash slug, unique | stable row key | `"sql-waits-bottleneck"` |
| `label` | string | short title | card heading a reader sees | `"Top wait types (delta)"` |
| `verdict` | string | `"ok"` (green), `"watch"` (amber), `"bad"` (red), `"none"` (gray: not collected or no data) | the colour | `"bad"` |
| `rule` | string | one plain sentence | the rule in words that produced the verdict | `"Bad: ... at least 30 % ... and 100 ms ..."` |
| `chart` | string | `"line"`, `"stackedArea"`, `"bars"`, `"none"` | named helper that draws the card | `"stackedArea"` |
| `series` | array of `{name, points}`; `points` is `[[timeLabel, value], ...]` | ≤ 12 points per series; `[]` when `chart` is `"none"` | plotted data | `[{"name":"WRITELOG","points":[["09:00",27]]}]` |
| `table` | `{columns: string[], rows: any[][]}` | ≤ 5 rows | the data table under the chart | `{"columns":["Wait type"],"rows":[["WRITELOG"]]}` |
| `why` | `{summary, steps[], dataNote}` | `summary` and `dataNote` strings, `steps` array of strings | full-view content: what it means, how it was derived, caveat | `{"summary":"...","steps":["..."],"dataNote":"..."}` |
| `sampled` | boolean | `true` when data is a sample, not full capture | shows the sample flag on the card | `true` |

No other fields. Colours are never stored as values; `verdict` maps to existing tokens (section 2).

## 2. Colour rule

Base test compares an incident-window metric with the reference window (26 Oct, incident 09:00–10:00 UTC vs reference 08:00–09:00, API Orders.Create).

- **bad (red):** incident is at least **30 % worse and at least 100 ms per task worse** than reference, **or** the row's dominant-bottleneck test holds (the named distinct wait holds the wait delta and scales with load).
- **watch (amber):** at least **half** of that (**15 % and 50 ms per task worse**), or a rise that is real but not yet queuing.
- **ok (green):** below both watch tests.
- **none (gray):** no data, or the source that would produce it was never collected ("not collected yet"). Absent evidence, not a pass.

**Which numbers, and why:** keep the wireframe's existing **30 % and 100 ms** as the size test; the research file gives no better numeric threshold — its colour rules are qualitative (which wait type dominates, sustained vs sporadic) — so we use the research's direction words as the shape test on top of 30 %/100 ms. Where the research does state numbers, the SQL exception is **log growth**: used % **< 70** green, **70–90** amber, **near-full** red. (CPU 70–85/>85 and Mongo tickets >75 % are Host/Mongo numbers and belong to the later packet.)

Colour tokens (existing, no new colours): reuse `--suspect-red`, `--suspect-orange` (amber), `--suspect-green`, `--suspect-grey` from `parts/00-head.html`, matching the state chips (Ready, Partly ready, Cannot test yet) and the legend in `parts/32-steps-10-13.js`. Soft fills use `--suspect-*-soft`.

## 3. Legend text

- **Green:** this row was checked and looked normal in the window.
- **Amber:** this row moved the wrong way, but a little, so it is worth a look.
- **Red:** this row is a likely cause of the slowdown — it clearly worsened in the window.
- **Gray:** we could not collect this row for this case, so we do not know either way.

## 4. SQL scenarios

11 scenarios, 3 per colour plus 2 gray: green (`sql-waits-benign`, `sql-topqueries-orders`, `sql-log-growth`); amber (`sql-lock-waits-rising`, `sql-memory-grants`, `sql-index-maintenance`); red (`sql-waits-bottleneck`, `sql-blocking-chain`, `sql-tempdb-contention`); gray (`sql-plan-regression`, `sql-deadlocks`). Gray rows are exactly the research rows that need **Query Store** and **extended events**, so they are shown "not collected yet". Covered research rows: wait stats, top queries by duration, blocking chains, deadlocks, index maintenance, plan regression, tempdb, memory grants, log growth.

## 5. Fixture JSON

```json
[
{
 "id":"sql-waits-benign",
 "label":"Top wait types (delta)",
 "verdict":"ok",
 "rule":"Green: benign background waits hold most of the delta and no bottleneck wait is both 30 % larger and 100 ms per task worse than the reference window.",
 "chart":"stackedArea",
 "series":[
  {"name":"SLEEP_TASK","points":[["08:00",62],["08:30",64],["09:00",61],["09:30",63],["09:59",62]]},
  {"name":"LAZYWRITER","points":[["08:00",22],["08:30",21],["09:00",23],["09:30",22],["09:59",22]]},
  {"name":"PAGEIOLATCH_SH","points":[["08:00",3],["08:30",3],["09:00",4],["09:30",3],["09:59",3]]}
 ],
 "table":{"columns":["Wait type","Delta share","Delta ms per task"],"rows":[["SLEEP_TASK","62%","1.1"],["LAZYWRITER","22%","0.4"],["PAGEIOLATCH_SH","3%","0.9"],["CHKPT","2%","0.3"]]},
 "why":{"summary":"Benign background waits dominate; no bottleneck wait grew in the incident window.","steps":["Delta wait_time_ms between the last reference snapshot and each incident snapshot.","Compare each wait's delta share and per-task ms with the reference window.","No wait is both 30 % larger and 100 ms per task worse, so the row is green."],"dataNote":"Wait stats are cumulative and delta'd across snapshots taken every 60 s; spikes between samples can be missed."},
 "sampled":true
},
{
 "id":"sql-topqueries-orders",
 "label":"Top queries by duration",
 "verdict":"ok",
 "rule":"Green: every query is under the 400 ms hard SLA and within 30 % and 100 ms of its reference duration.",
 "chart":"bars",
 "series":[{"name":"Orders.Create p95 ms","points":[["08:00",180],["08:15",176],["08:30",182],["09:00",190],["09:30",188],["09:59",185]]}],
 "table":{"columns":["Query","Calls","Avg ms","p95 ms"],"rows":[["Orders.Create insert","1,204","142","190"],["OrderLines select","3,318","31","58"],["Stock lookup","2,907","12","26"],["Audit write","1,204","9","21"]]},
 "why":{"summary":"The slowest query in the window, Orders.Create, stays under SLA and barely moves between windows.","steps":["Read total_elapsed_time and wait_time for in-flight requests each snapshot.","Grouped by query text and took avg and p95 over the window.","All values are below the hard SLA, so the row is green."],"dataNote":"sys.dm_exec_requests is a point-in-time gauge; only requests alive at a sample appear."},
 "sampled":true
},
{
 "id":"sql-log-growth",
 "label":"Log growth",
 "verdict":"ok",
 "rule":"Green: log used stays under 70 % and no autogrowth event falls inside the window.",
 "chart":"line",
 "series":[{"name":"Log used %","points":[["08:00",41],["08:20",41],["08:40",42],["09:00",42],["09:20",43],["09:40",44]]}],
 "table":{"columns":["Metric","Reference","Incident"],"rows":[["Log used %","41-42","42-44"],["VLF count","312","312"],["Autogrowth events","0","0"]]},
 "why":{"summary":"The log grew a little but stayed far under 70 % and never had to autogrow.","steps":["Read total_log_size and used_log_space each snapshot.","Counted autogrow events in the default trace for both windows.","Used % is below 70 % and no growth event fired, so the row is green."],"dataNote":"sys.dm_db_log_space_usage is a gauge; autogrow events come from the default trace, which rolls over."},
 "sampled":false
},
{
 "id":"sql-lock-waits-rising",
 "label":"Lock waits by resource",
 "verdict":"watch",
 "rule":"Watch: one secondary wait is rising (LCK_M_X is at least 15 % and 50 ms per task worse than reference) but no sessions are queued behind it.",
 "chart":"stackedArea",
 "series":[
  {"name":"LCK_M_S","points":[["08:00",41],["08:30",40],["09:00",52],["09:30",55],["09:59",54]]},
  {"name":"LCK_M_X","points":[["08:00",8],["08:30",9],["09:00",27],["09:30",31],["09:59",29]]},
  {"name":"LCK_M_IX","points":[["08:00",5],["08:30",5],["09:00",9],["09:30",10],["09:59",9]]}
 ],
 "table":{"columns":["Lock wait","Delta ms","Resource"],"rows":[["LCK_M_X","1,840","Orders (key)"],["LCK_M_S","1,510","Orders (page)"],["LCK_M_IX","320","Orders (key)"],["LCK_M_U","70","OrderLines (key)"]]},
 "why":{"summary":"X and S lock waits on the Orders object roughly doubled in the incident window, but nothing is queueing yet.","steps":["Delta LCK_M_* from sys.dm_os_wait_stats between snapshots.","Grouped the delta by resource_description.","The rise passes the watch test (half of red) but not red, and no session is blocked, so the row is amber."],"dataNote":"dm_os_wait_stats is a cumulative delta; the per-resource split needs a waiting_tasks sample."},
 "sampled":true
},
{
 "id":"sql-memory-grants",
 "label":"Memory grants",
 "verdict":"watch",
 "rule":"Watch: RESOURCE_SEMAPHORE waits appear occasionally (at least 15 % and 50 ms per task worse than reference) but no request sits in the grant queue at the end of the window.",
 "chart":"line",
 "series":[
  {"name":"RESOURCE_SEMAPHORE delta ms","points":[["08:00",0],["08:20",0],["08:40",20],["09:00",210],["09:20",180],["09:40",90]]},
  {"name":"Grant queue depth","points":[["08:00",0],["08:20",0],["08:40",0],["09:00",2],["09:20",1],["09:40",0]]}
 ],
 "table":{"columns":["Metric","Reference","Incident"],"rows":[["RESOURCE_SEMAPHORE ms","0","480"],["Peak grant queue","0","2"],["Granted vs requested","1.0x","0.8x"]]},
 "why":{"summary":"A few sorts waited briefly for memory grants in the incident window, but the queue drained on its own.","steps":["Delta the RESOURCE_SEMAPHORE wait from dm_os_wait_stats.","Sampled dm_exec_query_memory_grants for outstanding grants.","Waits are occasional and the queue clears, so the row is amber, not red."],"dataNote":"The wait delta is durable; the queue depth is a snapshot and can miss a short queue."},
 "sampled":true
},
{
 "id":"sql-index-maintenance",
 "label":"Index maintenance overlap",
 "verdict":"watch",
 "rule":"Watch: a rebuild or reorg job overlaps the window (at least 15 % and 50 ms per task worse on the affected file) but no bottleneck wait shares its timestamp.",
 "chart":"bars",
 "series":[{"name":"Maintenance job in window","points":[["08:00",0],["08:30",0],["09:00",42],["09:30",35],["09:59",0]]}],
 "table":{"columns":["Job","Window","Overlap min","Fragmentation after"],"rows":[["IX_Orders_Cust_rebuild","09:00-10:00","42","2 %"],["IX_Lines_Order_reorg","09:20-09:55","35","8 %"]]},
 "why":{"summary":"An index rebuild ran inside the incident window and added mild I/O, but no wait spike lines up with it.","steps":["Read msdb job history for maintenance jobs overlapping the window.","Read fragmentation from dm_db_index_physical_stats.","The overlap is real but not tied to a bottleneck wait, so the row is amber."],"dataNote":"Job history is durable; fragmentation is a gauge from the last scan."},
 "sampled":false
},
{
 "id":"sql-waits-bottleneck",
 "label":"Top wait types (delta)",
 "verdict":"bad",
 "rule":"Bad: PAGEIOLATCH_SH and WRITELOG hold at least 30 % of the wait delta and are at least 100 ms per task worse than reference, and both scale with load.",
 "chart":"stackedArea",
 "series":[
  {"name":"PAGEIOLATCH_SH","points":[["08:00",14],["08:30",15],["09:00",44],["09:30",51],["09:59",48]]},
  {"name":"WRITELOG","points":[["08:00",9],["08:30",10],["09:00",27],["09:30",30],["09:59",28]]},
  {"name":"SLEEP_TASK","points":[["08:00",58],["08:30",57],["09:00",22],["09:30",14],["09:59",17]]}
 ],
 "table":{"columns":["Wait type","Reference share","Incident share","Per-task delta"],"rows":[["PAGEIOLATCH_SH","14%","44%","+124 ms"],["WRITELOG","9%","27%","+96 ms"],["SLEEP_TASK","58%","22%","-3 ms"],["CXPACKET","4%","9%","+21 ms"]]},
 "why":{"summary":"Storage and log waits take over the incident window and replace the benign background waits.","steps":["Delta dm_os_wait_stats between the reference and incident snapshots.","Compare the share of the delta and the ms per waiting task.","Both bottleneck waits pass the red test (30 % and 100 ms) and grow with load, so the row is red."],"dataNote":"Cumulative deltas are durable; the per-resource split needs a matching waiting_tasks sample."},
 "sampled":true
},
{
 "id":"sql-blocking-chain",
 "label":"Head blocker / blocking chains",
 "verdict":"bad",
 "rule":"Bad: a head blocker is itself waiting on LCK_M_* or PAGEIOLATCH_* and the chain is sustained at least 30 % of the window and 100 ms longer than reference.",
 "chart":"bars",
 "series":[{"name":"Blocked seconds by session","points":[["09:00",38],["09:15",52],["09:30",61],["09:45",58],["09:59",55]]}],
 "table":{"columns":["Session","Blocked by","Wait","Blocked s"],"rows":[["spid 78","spid 61","LCK_M_X","61"],["spid 84","spid 61","LCK_M_X","54"],["spid 61 (head)","-","PAGEIOLATCH_SH","48"],["spid 90","spid 78","LCK_M_S","22"]]},
 "why":{"summary":"A chain forms with a head blocker that is itself waiting on I/O, and it lasts most of the window.","steps":["Sampled dm_exec_requests.blocking_session_id and dm_os_waiting_tasks.","Walked the chain to its head and timed each hop.","The head blocker is waiting, not running, and the chain is sustained, so the row is red."],"dataNote":"Only visible if a snapshot landed during the window; blocking that started and ended between samples is missed."},
 "sampled":true
},
{
 "id":"sql-tempdb-contention",
 "label":"tempdb allocation contention",
 "verdict":"bad",
 "rule":"Bad: PAGELATCH_UP or PAGELATCH_EX on tempdb allocation pages (2:x:1/2/3) is sustained at least 30 % and 100 ms per task worse than reference.",
 "chart":"line",
 "series":[{"name":"PAGELATCH_UP delta ms","points":[["08:00",0],["08:20",5],["08:40",12],["09:00",420],["09:20",510],["09:40",470]]}],
 "table":{"columns":["Wait resource","Delta ms","Pages"],"rows":[["2:1:1","610","PFS"],["2:3:2","280","GAM"],["2:4:3","120","SGAM"]]},
 "why":{"summary":"Waits pile up on tempdb allocation pages, the classic few-files signature.","steps":["Delta PAGELATCH_* from dm_os_wait_stats.","Grouped by resource_description to find the tempdb file:page.","Waits concentrate on 2:x:1/2/3 and stay high, so the row is red."],"dataNote":"Durable as a delta; the file:page split needs a waiting_tasks sample in the window."},
 "sampled":true
},
{
 "id":"sql-plan-regression",
 "label":"Plan regressions (Query Store)",
 "verdict":"none",
 "rule":"Gray, not collected yet: Query Store is off, so no plan history was captured for this case. Absent evidence, not a pass.",
 "chart":"none",
 "series":[],
 "table":{"columns":["Query","plan_id","Avg ms"],"rows":[]},
 "why":{"summary":"Cannot tell yet whether Orders.Create changed plan, because Query Store is not enabled on this database.","steps":["Check sys.database_query_store_options for the case database.","Result: enabled = 0, so there is no runtime stats or plan history to read.","The row is shown gray and labelled not collected yet; turning Query Store on and re-running would fill it."],"dataNote":"Query Store persists across restarts; without it, plan changes leave no trace in the snapshots."},
 "sampled":false
},
{
 "id":"sql-deadlocks",
 "label":"Deadlocks (system_health XE)",
 "verdict":"none",
 "rule":"Gray, not collected yet: the system_health session is not writing to a persisted event file, so the deadlock report cannot be read. Absent evidence, not a pass.",
 "chart":"none",
 "series":[],
 "table":{"columns":["Time","Victim","Resources"],"rows":[]},
 "why":{"summary":"Cannot tell yet whether deadlocks fired in the window, because system_health has no persisted event file to read.","steps":["Look for xml_deadlock_report entries in the system_health XE target.","Result: no file target, so only the ring buffer exists and it holds recent events only.","The row is shown gray and labelled not collected yet; enabling a file target would fill it."],"dataNote":"system_health survives snapshots; without a file target, deadlock graphs are lost once they leave the ring buffer."},
 "sampled":false
}
]
```

Status: PASS
Checks: `JSON.parse` printed nothing (PARSE_OK); 11 scenarios, every one has exactly the 9 fields; verdict counts ok=3 watch=3 bad=3 none=2; every table ≤5 rows and every series ≤12 points.
Concerns: Wireframe shared helpers are `line`, `stackedArea`, `bars`, `none` only, so the research's swim-lane blocking-chain and heatmap tempdb rows are carried as `bars` and `line` here; a `swimlane`/`heatmap` helper would need adding in C1–C3.