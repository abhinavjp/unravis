# Phase 06: fetchability-build

risk: medium — new importer kinds, new engine input fields and two new server readers; no catalogue or threshold change.

## Outcome

The data the nine new suspects (Phase 07) need can be fetched. Build order D10 of FETCHABILITY-AUDIT §3, decided by the owner on 2026-11-02 (decision 8):

- **6a SQL data**: the XE importer keeps completed-call rows (with a duration), blocked-process reports (count only, scrub unchanged) and attention events. Imported SQL rows (XE and error log) reach `investigate.Input`. D2 gets a lane answer from imported deadlock rows.
- **6b HTTPERR**: imported HTTPERR rows and the IIS status/sub-status reach `investigate.Input`. A shared burst counter exists for S1 and S10–S12. The series endpoint can return a per-bucket quantile and a status-class split.
- **6c Blackbox sampler**: the sampler records disk latency; the host lane answers D9 and D10 beyond H1 as far as the audit supports; a PDH rule reads SQL "Memory Grants Pending" and D6 gets a lane answer from it.

Nothing in this phase touches `catalog.go`, `signals.go`, `thresholds.go` or `evaluate.go`. All new marks are lane-local constants, so `Version` does not move here.

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 29 xe-call-kind | medium | P02 done |
| 30 xe-blocked-kind | medium | 29 |
| 31 xe-attention-kind | low | 30 |
| 32 input-fields | medium | P02 done |
| 33 burst-count | low | 32 |
| 34 sqllane-d2 | medium | 33 |
| 35 server-imported-events | medium | 29, 30, 31, 34 |
| 36 series-quantile | medium | P02 done |
| 37 series-status-class | low | 36 |
| 38 metrics-mem-grants | low | P02 done |
| 39 blackbox-disk-latency | medium | P02 done |
| 40 hostsample-disk | low | 39 |
| 41 hostlane-d9-d10 | medium | 40 |
| 42 d6-mem-grants-wiring | medium | 35, 38 |

## Edges

```
29 → 30 → 31 ─┐
32 → 33 → 34 ─┴→ 35 ─┐
38 ──────────────────┴→ 42
36 → 37
39 → 40 → 41
```

Frontier at phase start (parallel, disjoint files): **29, 32, 36, 38, 39**. Then 30 → 31; 33 → 34; 37; 40 → 41. 35 waits for 31 and 34; 42 waits for 35 and 38.

Single writer per shared file in this phase:

| File | Writers, in order |
|---|---|
| `internal/importer/sqlserver/xe.go`, `xe_test.go` | 29 → 30 → 31 |
| `internal/evidence/caseexport_enums.go` | 29 → 30 → 31 |
| `internal/investigate/types.go` | 32 only |
| `internal/investigate/sqllane.go` (new) | 34 (then 51, 52 in P07) |
| `internal/server/investigation.go` | 35 → 42 |
| `internal/casestore/series.go` | 36 → 37 |
| `internal/resource/metrics.go`, `resource_test.go` | 38 (then 55 in P07) |
| `internal/blackbox/*` | 39 only |
| `internal/investigate/hostlane.go` | 40 → 41 |
| `internal/server/blackbox.go` | 40 only |

## Review focus

- 29/30: the scrub list `xeScrubFragments` is unchanged; a file with a report/XML column is still quarantined. Blocked-process rows carry no XML.
- 29: the XE `duration` column of `rpc_completed`/`sql_batch_completed` is in **microseconds**; the packet divides by 1000. A wrong unit makes every call 1000× slow.
- 32: the I6 facts output is byte-identical for inputs without SQL/HTTPERR rows (all new fields `omitempty` or `json:"-"`); the 5 goldens do not move in this phase.
- 35/42: readers use only validated sources, respect `casestore.MaxEventQuery`, and add a `read.Warnings` line on a cap hit, the same as the IIS read.
- 41: D9/D10 never overwrite a lane `Yes` set by another reader; H1 behaviour is unchanged.

## Out of scope / needs owner decision

These stay NOT FETCHABLE after this phase (FETCHABILITY-AUDIT lines ~63, ~64, ~73, ~75, ~130):

- **Top wait** card (needs per-wait deltas the sampler does not take).
- **Per-database file I/O share** (needs `sys.dm_io_virtual_file_stats` snapshots).
- **D1 blocking chain**: needs the blocked-process XML. Keeping it means an owner decision to accept an **unscrubbed XML** column; this phase keeps the scrub and stores a count only.
- **"Slow database calls over 1 s"** card: TSK-29 can store calls, but `system_health` does not capture `rpc_completed`/`attention`/`blocked_process_report`. A **custom XE session on the customer server** (and the DBA dry run of R12) is an owner/DBA matter; until then these rows exist only if the customer exports such a session.
- **H2–H6**: no counters are sampled for them.
- **M5 / M10** feeds (need change per the audit).
- Moving the lane-local constants of 34, 41, 42 into `Thresholds` (would bump `Version`).

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
cd web; npm test; npm run check
```

All PASS, or each failure/skip written in `progress.md`.

Commit: one commit for the phase, after a stop. Never push.
