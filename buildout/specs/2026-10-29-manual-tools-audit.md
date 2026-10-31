# Manual tools audit (A1)

| id | label | what it shows today | missing | useless / remove | add (from the research lists) | priority |
|---|---|---|---|---|---|---|
| response-time | Response time over time | P95-by-minute chart + per-API change table | link to why each API moved; gap shading | none | none | H |
| slowest-apis | Slowest APIs | ranked API table (calls, P95, ref, change) | link to the slow calls behind each row | none | Query Store / top-query tool (sp_BlitzCache) | M |
| slowest-calls | Slowest calls | bare table: API, started, duration | duration bars, tie-in to SQL/Mongo ops | none | none | M |
| time-split | Time split A C P O | stacked bars + parts table, all APIs pooled | API picker (all/one/several), per-API rows | none | none | H |
| calls-in-flight | Calls in flight | measure table + in-flight minute line | pool limit context when no limit entered | none | none | L |
| nearby-activity | Nearby activity | lane panel around one moment with per-call duration bars (no time axis or brush) | swim-lane timeline with axis and brush (theme F) | list is thin without chart | none | H |
| changes | Changes timeline | table of listed changes vs onset | optional-release case, long-window behaviour (theme G) | none | none | M |
| sql-waits | SQL blocking and waits | findings + placeholder wait table | blocking chains, deadlocks, top waits, tempdb, Query Store, log/index/autogrowth | dummy wait table (repo has no per-wait component) | sp_WhoIsActive, sp_BlitzLock, Query Store reports | H |
| mongo-health | MongoDB health | findings + placeholder replica table | slow ops, queues, tickets, replication lag, cache eviction, connections | dummy table | mongostat/mongotop, currentOp/profiler, rs.status | H |
| host-resources | Host CPU, memory and disk | findings + placeholder app-host table | per-device disk/network, memory/swap/PSI detail | dummy table | iostat, vmstat, sar, netstat -s | H |
| sql-host | SQL host disk, memory and network | findings + placeholder SQL-host table | same host evidence, tied to SQL suspects | duplicates host-resources (same Blackbox source) | PerfMon SQL counters | M |
| after-fix | After the fix | later-day verdict + comparison table | none blocking | none | none | L |
| suspect-summary | Suspect summary | origin roles, suspect cards, confidence table | evidence chart + "Why" drill-down per suspect (theme E) | none | none | H |

## Table 2

| proposed id | label | source (SQL/Mongo/Host/API) | evidence rows it shows | feasible read-only? |
|---|---|---|---|---|
| sql-blocking-live | Live blocking and sessions | SQL | Head blocker / blocking chains; Long-running requests (sp_WhoIsActive) | partial (DMVs need a sample in-window) |
| sql-deadlocks | Deadlocks (XE reader) | SQL | Deadlocks (xml_deadlock_report) timeline + graph | yes (system_health persists) |
| sql-query-store | Query Store and top queries | SQL | Plan regressions / Query Store; Top wait types (sp_BlitzCache) | yes (Query Store persists) |
| mongo-live-ops | Mongo slow ops and queues | Mongo | Slow ops; Queues; Locks / tickets (mongostat/mongotop/currentOp) | partial (currentOp is point-in-time) |
| mongo-replication | Mongo replication and oplog | Mongo | Replication lag; oplog window (rs.status/printReplicationInfo) | yes |
| host-counters | Host OS counters | Host | CPU; Memory; Disk queue / latency; Network errors (iostat/vmstat/sar/netstat -s) | partial (needs a collector on the host) |

## Grouping

- Group A (`41-tools-a.js`): response-time, slowest-apis, slowest-calls, time-split. All audit-only; coherent, keep.
- Group B (`42-tools-b.js`): calls-in-flight, nearby-activity, changes, sql-waits. Mixed: three audit tools plus one SQL tool. Move sql-waits out to a SQL group and bring the new SQL tools with it.
- Group C (`43-tools-c.js`): mongo-health, host-resources, sql-host, after-fix, suspect-summary. Overloaded. Move sql-host next to sql-waits; keep after-fix and suspect-summary with the audit tools; leave mongo-health and host-resources as their own group heads with the new Mongo/Host tools.
- Moves: group by source, not by file order. SQL group = sql-waits, sql-host, sql-blocking-live, sql-deadlocks, sql-query-store. Mongo group = mongo-health, mongo-live-ops, mongo-replication. Host group = host-resources, host-counters. Audit group = the rest.

## Top 5 changes

1. Turn the three placeholder tables (sql-waits, mongo-health, host-resources) into real evidence: blocking chains and deadlocks for SQL, slow ops and replication for Mongo, per-device disk and network for hosts.
2. Merge sql-host into host-resources, or show both hosts side by side; today it repeats the same source with no extra fact.
3. Give sql-waits and the new SQL/Mongo tools the same evidence pattern as step 5/7/8/9/10: a chart in the window, a data table, a colour rule, and a "Why" link.
4. Add the API picker to time-split and the swim-lane timeline to nearby-activity, so both match the owner's asks.
5. Give every suspect card a one-click "Why" incident view, so suspect-summary and the source tools agree on the same evidence.

Status: PASS
Checks: n/a
Concerns: New-tool feasibility for live DMV/currentOp rows is partial because samples may miss the window; the repo has no chart components for SQL, Mongo or Host yet.