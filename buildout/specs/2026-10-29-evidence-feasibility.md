# Evidence feasibility (F1)

27 rows: 7 yes, 13 partial, 7 no; 20 gray, 0 skip (every gap is read-only obtainable).

## SQL Server

| # | Source | Evidence row (short) | Captured | Where | What is missing | Needs elevated permission? | |
|---|---|---|---|---|---|---|---|
| 1 | SQL | Head blocker / blocking chains | partial | `internal/dbhealth/sql.go` qRequests/evalRequests (dm_exec_requests.blocking_session_id) | Only the direct blocker, no recursive chain walk, no resource_description, point-in-time only | VIEW SERVER STATE | gray |
| 2 | SQL | Lock waits by resource | partial | `internal/dbhealth/sql.go` qWaits (generic LCK_M_* lumped into top-8) | No dm_tran_locks / dm_os_waiting_tasks.resource_description breakdown; no heatmap | VIEW SERVER STATE | gray |
| 3 | SQL | Deadlocks (xml_deadlock_report) | partial | `internal/dbhealth/sql.go` qDeadlocks/evalDeadlocks (cumulative counter only); `internal/discover/detect_sql.go:220` detects system_health*.xel for export; `internal/collect/wording.go:117` sql-health-events import | No deadlock graph parsing (victim/resources/timeline), only a running count | VIEW SERVER STATE | gray |
| 4 | SQL | Top wait types (deltas) | yes | `internal/dbhealth/sql.go` qWaits/evalWaits (dm_os_wait_stats top 8 + waitMeaning) | Live cumulative-since-start snapshot, not a stored per-sample delta series | VIEW SERVER STATE | |
| 5 | SQL | tempdb contention | partial | `internal/dbhealth/sql.go` waitMeaning() PAGELATCH prefix (generic mention only) | No tempdb-specific resource_description / PFS-GAM-SGAM identification | VIEW SERVER STATE | gray |
| 6 | SQL | Long-running requests | partial | `internal/dbhealth/sql.go` qRequests/evalRequests (elapsed_time, wait_time) | No dm_exec_sql_text / dm_exec_query_plan join for the specific running request | VIEW SERVER STATE | gray |
| 7 | SQL | Plan regressions / Query Store | yes | `internal/dbhealth/lane_sql.go` qLaneQSStats/evalLaneQueryStore (ref vs incident, query_id/plan_id); `internal/dbhealth/sql.go:408` runQueryStore/evalQueryStore (hourly spikes) | n/a | VIEW DATABASE PERFORMANCE STATE | |
| 8 | SQL | CPU & scheduler pressure — runnable queue | no | none found for: dm_os_schedulers, runnable_tasks_count, work_queue_count | Not queried anywhere | VIEW SERVER STATE | gray |
| 9 | SQL | CPU & scheduler pressure — signal-wait % | no | none found for: signal_wait_time_ms, "signal wait" | qWaits already reads dm_os_wait_stats but not this column | VIEW SERVER STATE | gray |
| 10 | SQL | Memory grants | partial | `internal/dbhealth/sql.go` qMemory/evalMemory ("Memory Grants Pending" counter, RESOURCE_SEMAPHORE in top waits) | No dm_exec_query_memory_grants per-query detail, no dm_os_memory_clerks | VIEW SERVER STATE | gray |
| 11 | SQL | Log growth — space used % | no | none found for: dm_db_log_space_usage | Not queried anywhere | VIEW DATABASE STATE | gray |
| 12 | SQL | Log growth — VLF count | no | none found for: dm_db_log_stats, VLF | Not queried anywhere | VIEW DATABASE STATE | gray |
| 13 | SQL | Index maintenance | partial | `internal/dbhealth/lane_sql.go` qLaneJobs/qLaneBackups/evalLaneJobs (any Agent job/backup overlapping the incident window) | Not filtered to index maintenance; no dm_db_index_physical_stats fragmentation, no dm_db_index_usage_stats | SQLAgentReaderRole in msdb | gray |
| 14 | SQL | Autogrowth | no | none found for: autogrow, default trace, dm_io_virtual_file_stats | Not queried anywhere | VIEW SERVER STATE | gray |

## MongoDB

| # | Source | Evidence row (short) | Captured | Where | What is missing | Needs elevated permission? | |
|---|---|---|---|---|---|---|---|
| 15 | Mongo | Slow ops | yes | `internal/dbhealth/mongo.go` runCurrentOp/evalCurrentOp (secs_running≥5); runProfiler/evalProfiler (system.profile, millis≥100) | serverStatus.opLatencies percentiles not read | admin (clusterMonitor-type role) | |
| 16 | Mongo | Queues | partial | `internal/dbhealth/mongo.go` evalServerStatus (globalLock.currentQueue.total); `internal/resource/metrics.go:222` MongoQueue rule | Only the total; no readers-vs-writers breakdown | admin | gray |
| 17 | Mongo | Locks | partial | `internal/dbhealth/mongo.go` evalCurrentOp (boolean waitingForLock only) | No lock-type/mode breakdown, no heatmap | admin | gray |
| 18 | Mongo | Tickets (wiredTiger.concurrentTransactions) | partial | `internal/evidence/caseexport_enums.go:531-532` (attribute allowed on import only); `internal/collect/wording.go:115` mongo-serverstatus import path | No live dbhealth check, no `internal/resource/metrics.go` chart rule | admin | gray |
| 19 | Mongo | Replication lag | yes | `internal/dbhealth/mongo.go` runReplication/evalReplication (replSetGetStatus, per-member optimeDate lag) | Oplog window (rs.printReplicationInfo) not read | admin | |
| 20 | Mongo | Cache eviction | partial | `internal/dbhealth/mongo.go` evalServerStatus (dirty bytes vs max cache); `internal/resource/metrics.go:226` MongoWTDirty | No "pages evicted" / "pages read into cache" rate, no app-thread-eviction detail | admin | gray |
| 21 | Mongo | Connection pool | yes | `internal/dbhealth/mongo.go` evalServerStatus (connections.current/available); `internal/resource/metrics.go:224` MongoConns | totalCreated (churn) not read | admin | |

## Host resources

| # | Source | Evidence row (short) | Captured | Where | What is missing | Needs elevated permission? | |
|---|---|---|---|---|---|---|---|
| 22 | Host | CPU | partial | `internal/blackbox/collector.go:98` Collect (host.cpu from kernel/user/idle delta); `internal/sampler/engine.go:164`; `internal/resource/metrics.go` HostCPU rules (live + vmstat/sadf/PDH import) | No user/sys/iowait/steal breakdown or run-queue line, just one busy % | none | gray |
| 23 | Host | Memory — available / page faults | yes | `internal/blackbox/collector.go:115` (MemAvail/MemCommit); `internal/resource/metrics.go` HostMemAvailable/HostPages (PDH "Available MBytes"/"Pages/sec") | n/a | none | |
| 24 | Host | Memory — swap / Linux PSI | no | none found for: swap, PSI, pressure | Not read anywhere | none | gray |
| 25 | Host | Disk queue | yes | `internal/blackbox/extras_windows.go:76` DiskQueue (live PDH `\PhysicalDisk(_Total)\Current Disk Queue Length`); `internal/resource/metrics.go` HostDiskQueue | n/a | none | |
| 26 | Host | Disk latency | partial | `internal/resource/metrics.go` HostDiskSecPerTx (PDH "Avg. Disk sec/Transfer", import-only) | No live source (blackbox has no latency, only queue); no sys.dm_io_virtual_file_stats per-file latency | none | gray |
| 27 | Host | Network errors | no | none found for: netstat -s, drops, retransmits; counter names exist only in `internal/evidence/caseexport_enums.go:334` export enum with no `internal/resource/metrics.go` chart rule; `internal/blackbox/extras_windows.go:96` Network() only sums bytes in/out | No error/retransmit/drop counter is collected or charted | none | gray |

## Summary for the wireframe
- Rows safe to draw as real: 7 (#4, 7, 15, 19, 21, 23, 25)
- Rows to draw gray "not collected yet": 20 (#1, 2, 3, 5, 6, 8, 9, 10, 11, 12, 13, 14, 16, 17, 18, 20, 22, 24, 26, 27)
- Rows to skip: 0

## Notes
- Query Store and log-space/VLF DMVs are database-scoped; I used VIEW DATABASE [PERFORMANCE] STATE rather than VIEW SERVER STATE for those rows per `internal/dbhealth/grants.go`.
- `sql/*.js` and `sql/*.sql` ad-hoc probe scripts were treated as background corroboration, not cited per row, to keep cells short; none of them change a "no"/"partial" verdict since the product's own collectors were checked first.
- Mongo lock/queue/ticket breakdowns are technically accepted by the case-export attribute enum (so a manually imported serverStatus JSON line can be stored) but have no `internal/resource/metrics.go` chart rule, so they don't reach the UI — I scored them partial, not yes.
- Every no/partial row is obtainable with a read-only SELECT, read command, or OS/perf counter, consistent with Unravis's read-only-collector design, so nothing was marked "skip".
- "Top wait types" and "Plan regressions / Query Store" are scored "yes" even though they're a live snapshot / hourly rollup rather than the per-sample delta chart the research envisions — the underlying number is genuinely captured and stored.