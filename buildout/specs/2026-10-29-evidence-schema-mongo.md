# D1-Mongo: Mongo evidence card scenarios (spec + fixture)

## 1. Context
- Mongo evidence model per `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`, section "MongoDB": slow ops, queues, locks/tickets, replication lag, cache eviction, connection pool.
- Owner issues 8, 10, 11, 12, 15 (`2026-10-29-owner-issues-triage.md`): every suspect needs evidence chart(s) in-window, a data table, a verdict colour with a one-line rule, and a "Why" view. Non-suspects show why they were set aside.
- Plan sections 2 and 10 (`2026-10-29-wireframe-v2-implementation-plan.md`): D1 fixes the schema `{id,label,verdict,rule,chart,series,table,why,sampled}`, 2-3 scenarios per colour; gray = sampling never saw the window (absent, not negative); colour rule fixed in the packet.
- Demo case: incident 26 Oct 09:00-10:00 UTC, reference 08:00-09:00 UTC, API Orders.Create. `t` = minute of the two-hour axis (0-59 reference, 60-119 incident).

## 2. Task

### 2.1 Mongo evidence rows (row | verdict test | needs which permission)
| id | label | verdict test in plain words | needs which permission |
|---|---|---|---|
| MG1 | Slow ops by namespace | Latency (ms). Red if incident p95 ≥30% and ≥100 ms worse than reference; amber ≥15% and ≥50 ms; else green. | `currentOp` needs the `inprog` privilege; profiler/slow-log rows need profiling level 1/2 or a `slowms` threshold (off by default). |
| MG2 | Queues (globalLock read/write) | Queue depth (count). Red if ≥30% and ≥5 ops worse; amber ≥15% and ≥2; else green. Unit replaces 100 ms. | `serverStatus` read; no special privilege. |
| MG3 | Locks / WiredTiger tickets | Tickets out of 128 (count). Red if ≥30% and ≥25 tickets; amber ≥15% and ≥12; else green. Unit replaces 100 ms. | `serverStatus.wiredTiger` read; no special privilege. |
| MG4 | Replication lag | Seconds. Red if ≥30% and ≥5 s worse; amber ≥15% and ≥2 s; else green. Unit replaces 100 ms. | `replSetGetStatus`; needs `clusterMonitor` role. |
| MG5 | Oplog window | Hours of oplog headroom (ratio). Red if ≥30% and ≥6 h shorter; amber ≥15% and ≥3 h; else green. Unit replaces 100 ms. | `rs.printReplicationInfo`; needs `clusterMonitor` or local-database read. |
| MG6 | Cache eviction / pressure | Cache used % and eviction rate. Red if ≥30% and ≥15 percentage points worse; amber ≥15% and ≥10 pp; else green. pp replaces 100 ms. | `serverStatus.wiredTiger.cache` read; no special privilege. |
| MG7 | Connection pool | Current vs available connections (count). Red if ≥30% and ≥50 connections worse; amber ≥15% and ≥20; else green. Unit replaces 100 ms. | `serverStatus.connections` read; no special privilege. |
| MG8 | Collection scans (COLLSCAN) | Scans per minute (count). Red if ≥30% and ≥20 scans/min worse; amber ≥15% and ≥10; else green. Unit replaces 100 ms. | Profiler output (`system.profile`); needs profiling on. |
| MG9 | Index usage (IXSCAN vs COLLSCAN) | Index hit ratio (%). Red if ≥30% and ≥15 pp worse; amber ≥15% and ≥10 pp; else green. pp replaces 100 ms. | Profiler output or `$indexStats`; needs profiling on or the `$indexStats` privilege. |
| MG10 | Collection size growth | Bytes growth per hour (ratio). Red if ≥30% and ≥25% worse; amber ≥15% and ≥10%; else green. % replaces 100 ms. | `collStats` / `db.stats` read; no special privilege. |

### 2.2 Legend wording (per colour, only where Mongo differs)
- Green (ok): same as generic.
- Amber (watch): Mongo rising without a full stall — tickets above about 75%, cache creeping toward max, or a small non-zero queue, but ops still finish.
- Red (bad): Mongo sustained across the hour, not one spike — tickets exhausted near 128, slow ops still climbing at 10:00, or lag/tickets/wait queue holding for most of the window.
- Gray (none): Mongo needs a permission or profiler setting that is off (profiling level 0, no `clusterMonitor`, no `$indexStats`), so the window was never sampled; blank means absent, not green.

### 2.3 Fixture JSON
