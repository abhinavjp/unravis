# SQL Server, MongoDB, and host evidence for a slowdown window

Read-only investigators only see periodic snapshots, so the tables separate **persisted history** (Query Store, system_health XE, default trace) from **point-in-time gauges** (DMVs) that can only be delta'd between samples. Waits and counters are cumulative and delta cleanly; blocking chains and "who is running now" are only visible if a sample landed during the window.

## SQL Server

| Evidence | Source | Healthy vs suspect | Chart | Colour rule |
|---|---|---|---|---|
| Head blocker / blocking chains | `sys.dm_exec_requests.blocking_session_id`, `sys.dm_os_waiting_tasks` (`blocking_session_id`, `resource_description`), `sys.dm_exec_sessions` (snapshot) | Healthy: no session blocked beyond a few seconds. Suspect: chain whose head blocker is itself waiting (`LCK_M_*`, `PAGEIOLATCH_*`) and sustained | Swim-lane / dependency graph: blocked session → blocker, with duration bars | Green none; amber short or non-head chain; red sustained head blocker during window; gray if no sample fell in-window |
| Lock waits by resource | `sys.dm_tran_locks`, `sys.dm_os_waiting_tasks.resource_description`, `LCK_M_*` delta in `sys.dm_os_wait_stats` | Healthy: locks brief, low `LCK_M_*` delta. Suspect: rising `LCK_M_S/X/IX` concentrated on one object/key | Stacked area of lock wait types over time; heatmap of wait resource | Green flat; amber rising `LCK`; red dominant lock waits with queueing |
| Deadlocks (`xml_deadlock_report`) | `system_health` XE session (persisted event file), deadlock graph, trace flags. **Survives snapshots** | Healthy: zero events. Suspect: recurring graphs, same victim/resources | Timeline of deadlock count; graph view of the cycle | Green zero; amber isolated; red recurring pattern; gray XE not persisted |
| Top wait types (deltas) | `sys.dm_os_wait_stats` delta (`wait_time_ms`, `waiting_tasks_count`): `PAGEIOLATCH_*`, `WRITELOG`, `RESOURCE_SEMAPHORE`, `THREADPOOL`, `CXPACKET`, `SOS_SCHEDULER_YIELD` | Healthy: benign waits (`SLEEP_TASK`, `LAZYWRITER`) dominate. Suspect: bottleneck waits scale with response time | 100% stacked area of top waits per snapshot; bar of delta share | Green benign noise; amber one secondary wait rising; red `PAGEIOLATCH`/`WRITELOG`/`RESOURCE_SEMAPHORE`/`THREADPOOL` dominant and load-scaling |
| tempdb contention | `PAGELATCH_*` on tempdb (2: pages), `sys.dm_os_waiting_tasks.resource_description`, allocation PFS/GAM/SGAM | Healthy: no `PAGELATCH` on allocation pages. Suspect: `PAGELATCH_UP/EX` on `2:x:1/2/3` with too few files | Heatmap of wait resource (file:page); timeline of `PAGELATCH` delta | Green none; amber sporadic; red sustained allocation-page latch |
| Long-running requests | `sys.dm_exec_requests` (`total_elapsed_time`, `wait_type`, `wait_time`), `sys.dm_exec_sql_text`, `sys.dm_exec_query_plan` | Healthy: elapsed within SLA, little wait. Suspect: requests beyond baseline accumulating waits | Timeline/Gantt of request durations; scatter elapsed vs wait_time | Green under SLA; amber above p95 baseline; red beyond hard SLA or stuck waiting |
| Plan regressions / Query Store | Query Store `sys.query_store_runtime_stats`, `sys.query_store_plan`, plan forcing. **Persists restarts** | Healthy: stable `plan_id`, stable duration/CPU per `query_id`. Suspect: new `plan_id` with worse avg duration, or high plan variability | Timeline of avg duration/CPU per `query_id` with `plan_id` change markers | Green stable; amber plan change with modest regression; red large regression after deploy; gray Query Store off |
| CPU & scheduler pressure | `sys.dm_os_schedulers` (`runnable_tasks_count`, `work_queue_count`), `sys.dm_os_sys_info`, signal-wait % from wait stats | Healthy: `runnable_tasks_count` ≈ 0, signal waits low. Suspect: sustained runnable tasks, signal waits > 25% | Timeline of runnable tasks + signal-wait %; per-scheduler heatmap | Green no queue; amber transient runnable; red sustained runnable/worker exhaustion |
| Memory grants | `sys.dm_exec_query_memory_grants`, `RESOURCE_SEMAPHORE` wait, `sys.dm_os_memory_clerks` | Healthy: no/rare semaphore waits. Suspect: grant queue, granted ≪ required | Timeline of `RESOURCE_SEMAPHORE` delta + outstanding grant depth; scatter granted vs requested | Green none; amber short occasional waits; red persistent grant queue |
| Log growth | `sys.dm_db_log_space_usage`, `sys.dm_db_log_stats` (VLFs), `WRITELOG` waits | Healthy: steady used %, no growth. Suspect: % used climbing, VLF bloat, log waits | Timeline of used % and VLF count; growth-event timeline | Green < 70%; amber 70–90% or frequent growth; red near-full/autogrowth spikes |
| Index maintenance | `sys.dm_db_index_physical_stats`, `sys.dm_db_index_usage_stats`, msdb job history | Healthy: fragmentation stable, rebuilds outside window. Suspect: rebuild/reorg overlapping the slowdown, adding I/O | Timeline of job windows overlaid on wait spikes; fragmentation bars | Green no overlap; amber overlap with mild impact; red maintenance coincides with bottleneck |
| Autogrowth | Default trace autogrow events, `sys.dm_db_log_space_usage`, `PAGEIOLATCH` during growth, `sys.dm_io_virtual_file_stats` | Healthy: fixed size, no grow events. Suspect: frequent small-percent growth on data/log | Timeline annotating grow events over read/write latency | Green none; amber occasional; red repeated growth correlating with stalls |

## MongoDB

| Evidence | Source | Healthy vs suspect | Chart | Colour rule |
|---|---|---|---|---|
| Slow ops | `db.currentOp()` (`secs_running`, `$all:true`), slow query log (`slowms`), profiler `system.profile`, `serverStatus.opLatencies` | Healthy: few ops past `slowms`, stable percentiles. Suspect: queue of slow ops, high `micros` | Timeline of slow-op count + p95 latency; scatter of op duration | Green under `slowms`; amber sporadic; red sustained queue. `currentOp` is point-in-time; log/profiler persists |
| Queues | `serverStatus.globalLock.currentQueue` (`total`, `readers`, `writers`) | Healthy: near-zero queue. Suspect: rising non-zero read/write queue | Stacked area of readers vs writers queue over time | Green 0; amber transient; red sustained |
| Locks / tickets | `db.currentOp().locks`, `serverStatus.wiredTiger.concurrentTransactions` (`out`, `available`, `totalTickets`, default 128) | Healthy: tickets available. Suspect: `out` near `totalTickets`, lock waits | Timeline of tickets out/available; heatmap of lock types | Green plenty available; amber > 75% consumed; red exhausted with waits |
| Replication lag | `rs.status()` / `replSetGetStatus` (`optimeDate` vs primary), oplog window (`rs.printReplicationInfo`) | Healthy: lag seconds, window ≫ peak. Suspect: growing lag, shrinking window | Timeline of per-member lag seconds; secondary line per member | Green few seconds; amber growing; red beyond SLA/window shrinking |
| Cache eviction | `serverStatus.wiredTiger.cache` (bytes in cache vs max, `pages evicted`, `pages read into cache`, dirty %) | Healthy: cache below max, low forced eviction, dirty < 5%. Suspect: near max, high eviction, app threads evicting | Timeline of cache used/max % + eviction rate; stacked dirty vs clean | Green under threshold; amber pressure rising; red near max with application-thread eviction |
| Connection pool | `serverStatus.connections` (`current`, `available`, `totalCreated`) | Healthy: below limit, `available` > 0. Suspect: `current` near `maxIncomingConnections`, `available` 0, churn | Timeline of current vs available connections | Green headroom; amber approaching limit; red exhausted |

## Host resources

| Evidence | Source | Healthy vs suspect | Chart | Colour rule |
|---|---|---|---|---|
| CPU | OS counters (`/proc/stat`, Windows perf counters, node_exporter): user/sys/iowait/steal, run queue | Healthy: < 70–80%, low iowait/steal. Suspect: saturation with run queue, steal, or iowait dominating | Stacked area (user/sys/iowait/steal) + run-queue line | Green normal; amber 70–85% or rising load; red saturated or iowait/steal dominating |
| Memory | OS available memory, page faults, swap in/out, Linux PSI | Healthy: headroom, no swap thrash. Suspect: near-zero available, paging, OOM | Timeline of available memory + swap activity | Green headroom; amber low headroom; red thrash/OOM |
| Disk queue / latency | `iostat`/Windows `\PhysicalDisk\Avg Disk sec/Transfer` + queue length; `sys.dm_io_virtual_file_stats` per file | Healthy: queue ≈ 0, latency < 10 ms (SSD). Suspect: queue growth, latency spikes | Timeline of latency + queue depth; per-device heatmap | Green normal; amber rising; red sustained queue and high latency |
| Network errors | `netstat -s`, Windows NIC counters, drops/retransmits | Healthy: near-zero errors/retransmits. Suspect: rising errors/drops correlated with app or replica lag | Timeline of errors/retransmits/drops | Green near-zero; amber occasional; red sustained errors with impact |

## Tools to add to a manual-tools menu

- **SQL Server:** `sp_WhoIsActive` (live blocking), `sp_BlitzLock` (deadlock graph analysis), `sp_BlitzFirst`/`sp_BlitzCache`, Query Store reports, Activity Monitor, `system_health` XE reader, PerfMon SQL counters.
- **MongoDB:** `mongostat`, `mongotop`, `db.currentOp()`/profiler, `rs.status()`/`rs.printReplicationInfo`, `mtools` `mloginfo`, Atlas Performance Advisor.
- **Host:** `iostat -x`, `vmstat`, `sar`, `netstat -s`, PerfMon disk/NIC counters, node_exporter + Grafana.

## Sources

- sys.dm_os_wait_stats: https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-wait-stats-transact-sql
- Deadlocks guide / xml_deadlock_report: https://learn.microsoft.com/en-us/sql/relational-databases/sql-server-deadlocks-guide
- sys.dm_os_schedulers: https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-schedulers-transact-sql
- sys.dm_exec_query_memory_grants: https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-exec-query-memory-grants-transact-sql
- Monitor performance with Query Store: https://learn.microsoft.com/en-us/sql/relational-databases/performance/monitoring-performance-by-using-the-query-store
- sys.dm_db_log_space_usage / manage log size: https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-db-log-space-usage-transact-sql
- tempdb allocation contention: https://learn.microsoft.com/en-us/troubleshoot/sql/database-engine/performance/recommendations-reduce-allocation-contention
- MongoDB serverStatus: https://www.mongodb.com/docs/manual/reference/command/serverStatus/
- MongoDB currentOp: https://www.mongodb.com/docs/manual/reference/command/currentOp/
- MongoDB replication lag: https://www.mongodb.com/docs/manual/troubleshooting/replication-lag/
- MongoDB replSetGetStatus: https://www.mongodb.com/docs/manual/reference/command/replSetGetStatus/

Highest-yield confirmation for a suspect: a persisted deadlock/Query Store event that brackets the window (hard evidence), then a wait-type delta or blocking chain that shares the suspect's timestamp (strong), then a single snapshot gauge that merely coincides (weak, pending corroboration). Gray means the sampling never observed the window, so the evidence is absent, not negative.