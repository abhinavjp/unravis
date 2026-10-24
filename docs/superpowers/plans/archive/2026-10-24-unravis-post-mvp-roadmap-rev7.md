# Unravis post-MVP roadmap: evidence-first investigation beyond the API audit log

- **Date:** 2026-10-24
- **Status:** Proposal, **revision 7 (2026-10-24), ready for phase-by-phase approval; decisions 14–15 carry proposed defaults**. Revision 7 adds the deterministic theories screen to P2b (§5A.H). A review plan for this document is in `docs/superpowers/plans/2026-10-24-roadmap-review-plan.md`. Revision 3 added the hub and node evaluation to revision 2. Revision 4 recorded the accepted decisions and added host auto-detect (P0). Revision 5 resolves the last decisions, adds manual hints as a peer of auto-detect, and applies a whole-document consistency pass. Revision 6 reviews the hand-built incident report and adds deterministic theory validation (P2b) with strictly optional, external LLM assistance. This is analysis and planning only: no code was changed and nothing was committed.
- **[Rev 2] What changed in this revision.**
  - Re-analysed the `sql/` evidence. The new step was a per-minute join of host CPU (from `system_health`) against every stall (§1A.A).
  - Added §1A, "Root-cause drilling: what is actually needed". It has five parts: the investigation path, a hypothesis × evidence matrix, drilling ladders, per-phase diagnostic value, and decisions plus a grants checklist.
  - Revised §0, §1, §2 and the phase order.
  - Replaced the offline-first access model with a **live-first connector model**, keeping offline import as the fallback (§3.6–§3.8).
  - P9 is now per-client optional with capability detection.
  - Demoted S1, the lock-wait matching rule. Added a volley rule (B12), a host-CPU recurrence rule (B13), a live SQL request sampler (S14) and a cold-start rule (M9). Also added a change marker (W9), two composites (C9, C10) and a gated serialization item (G9). Every phase now carries a diagnostic-value line.
  - Cut the open decisions to 6 (§24).
  - Every Rev 2 change is marked **[Rev 2]**. Unmarked text stands as written in revision 1.
- **[Rev 3] What changed (hub and node evaluation, 2026-10-24).**
  - Evaluated running Unravis on the servers as a **node** pulled by a central **hub**. The new section §3.9 covers the topology comparison, gate N, the security model, the minimum viable node, codebase impact and clock skew.
  - **Verdict: hybrid, only for named sources, behind gate N, build deferred.** Remote live (§3.7) stays primary, and query packs and the DCS stay the floor.
  - Added the optional node release **N1 "burst catcher"** (slot 4b, after P6) and gate N (§21).
  - Also touched: the grants checklist node row (§1A.E), the open decisions 7–10 (§24), the §25 row and demo, and the out-of-scope line.
  - Every Rev 3 change is marked **[Rev 3]**.
- **[Rev 4] What changed (decisions accepted; host auto-detect, 2026-10-24).**
  - The user accepted every default for decisions 1–10. §24 now lists them as resolved, and only three new auto-detect decisions (11–13) remain open.
  - Added **P0, host auto-detect** (`unravis detect` / `--auto`, §3A). It is a read-only, local, one-shot discovery that writes an editable host profile (JSON + report). The profile machine-fills decision 1's host map and proposes connectors and packs, but enables nothing until a person confirms.
  - P0 is built first (order 0), in parallel with P1. It ships in the main exe and is outside gate N.
  - New rules HP1–HP5.
  - Also touched: the §0 and §2 order tables, §3.9 (the node uses P0), the grants checklist, §25 and Appendix B.
  - Every Rev 4 change is marked **[Rev 4]**.
- **[Rev 5] What changed (hints, decisions closed, consistency pass, 2026-10-24).**
  - Decisions 11–13 are **decided by judgment (user-delegated)**. No decision is open. §24 lists only the client facts still to collect.
  - Added **manual hints** to P0 as a first-class peer of auto-detect (§3A): `unravis-hints.json`, equivalent CLI flags and a UI form. Precedence is hint > detected > default, with per-field provenance and a new rule **HP6** for conflicts. It supports partial hints, hints-only (`--no-detect`) and hub-side hints for remote hosts. There are no secrets in hints: connections are given by `dpapi:` reference. A hints-only run needs read on the hinted paths only.
  - Consistency fixes: stale § references (§0, §2, §3.5, P4, §17), the §25 demos for R2/R3/R8 aligned with the Rev 2 leads, one merged build order in §25, P0 rows in §1A.D, §17, §18 and §23, and naming notes for the R and L letter clashes (Appendix B).
  - Every Rev 5 change is marked **[Rev 5]**. Earlier "What changed" bullets are kept as history. For example, Rev 2's "6 open decisions" are all resolved now.
- **[Rev 6] What changed (incident report review; theory validation, 2026-10-24).**
  - Reviewed the user's own incident report (`sql/report.src.html`, built into `sql/report.html` by `sql/build_report.js`) for structure, theories and chart logic, not data. The new §1B holds the inventory, the gap table and the manual steps to automate.
  - Added **P2b, "Theory checks"** (§5A): typed, versioned theory records; deterministic tests that reuse the existing rules; verdicts of supported, contradicted or inconclusive, which keep the §1 evidence label; anti-self-deception guards (new validation rules **V1–V9**); a reproducibility bundle; and a catalog **TH1–TH19** covering the report's 8 theories and every §1A.B class.
  - P2b is **build slot 2b**, in parallel with P3. It adds no new source and no production query.
  - Added §3.10, **optional LLM assistance**. It is off by default and external only, over MCP. An LLM may propose; only the engine decides.
  - Also touched: §0, §1A.D, §2, §3.1, P2 (§5), P8 (§11), P13 (§16), §17, §18, §19, §22, §23, §24 (new decisions 14–15), §25 and Appendix B. The P12/P13 rows of §1A.D were merged on one line; they are now split.
  - Every Rev 6 change is marked **[Rev 6]**.
- **[Rev 7] What changed (theories screen, 2026-10-24; user-approved).**
  - Added §5A.H, the **theories screen**. It keeps the report's "What is known and what is not" section, now deterministic:
    - a one-line verdict row (verdict, evidence badge, reason when inconclusive, next action and owner);
    - an inspector one click deeper (evidence, gap, what would settle it, what would change my mind, tests, reproduce, history, analyst note);
    - template-generated text, with numbers as engine tokens;
    - a mapping from the old report labels;
    - sort by verdict or by cheapest next evidence;
    - MCP parity through `theory_verdicts view=cards`.
  - The optional LLM may only reword text. It is off by default, labelled and linter-checked, and comes through a fifth MCP tool, `theory_narrative_submit` (19 tools in total).
  - Also touched: §0, §1B.A, §2, §3.10, §5A.E, §16, §17, §25 and Appendix B. P2b effort is now M–L.
  - Every Rev 7 change is marked **[Rev 7]**.
- **[Rev 2] Environment caveat.** The SQL evidence comes from `Brain_SandboxDB`. The audit evidence comes from the sandbox audit database on a Mongo instance that **also hosts four `*_Live` client databases** (`sql/out_mongo_probe.txt:2`). Patterns may differ per client. Each conclusion below is tied to this one environment.
- **Builds on:**
  - `docs/superpowers/specs/2026-10-23-unravis-investigation-mvp.md`
  - `docs/superpowers/specs/2026-10-24-unravis-ui-direction.md`
  - `docs/superpowers/plans/2026-10-23-api-slowness-diagnostics.md` (called the "old diagnostics plan" below, Tasks 9–16)
  - `docs/superpowers/plans/2026-10-23-unravis-mvp-gaps.md` (the "gap plan")
  - `docs/findings/2026-10-23-unravis-mvp-review.md`
  - The incident evidence in `sql/out_*.txt`, the SpikeHunter idea review (scratchpad), and a read-only scan of the brain-payroll repository.
- **Binding principle:**
  1. Existing evidence first.
  2. Then useful investigation capability.
  3. Then evidence gaps.
  4. Only then instrumentation.
- **Conventions used in this document:**
  - Every threshold is a *default, tunable* value. It is written as `45 s (default)`.
  - **Evidence labels.** Every finding carries exactly one of these:
    - **Symptom:** what was observed.
    - **Correlation:** co-occurs in time or shape with another observation.
    - **Strong correlation:** co-occurs, and the duration or shape matches within a stated tolerance and a known mechanism links the two.
    - **Proven cause:** a request-scoped identifier links the slow request to the cause, or a controlled reproduction confirms it.
  - `[unverified]` marks a claim this analysis did not confirm against a primary source or the live system.
  - "Dubai" means Asia/Dubai (UTC+4, no daylight saving). Audit timestamps are stored in UTC.
  - **Out of scope:** AI diagnosis inside the tool, opaque health scores, and agents or collectors installed by Unravis. **[Rev 3]** That last item now reads "no agents or collectors **by default**". An on-server Unravis node is allowed only under gate N (§3.9.2, §21). **[Rev 6]** "No AI diagnosis inside the tool" stands. Theory verdicts are computed by deterministic rules (§5A). An LLM may only propose theories or draft text, and only from outside the tool, over MCP (§3.10).

---

## 0. Executive summary

The MVP answers *which APIs got slower, when, and in which timing phase*, using only `ApiAuditLog`. **[Rev 2]** The incident data in `sql/` shows four distinct problems. The current tool cannot separate them, and the old plans would not separate them either:

1. **Acute stalls of 45–201 s that coincide with recurring host CPU bursts. [Rev 2, revised]**
   - **What the stalls are.** There are 43 sync calls of 45 s or more between 10-09 and 10-23.
     - They are *volleys*. One user's page load fires several requests in the same second. All of them block, then finish one after another, about 4 s apart (§1A.A step 13).
     - They are not the "release cluster" that revision 1 assumed, where requests start at different times and finish at one instant.
   - **The CPU link.**
     - 37 of the 43 stalls fall inside minutes where non-SQL CPU on the DB host was 91–98%.
     - The same clock windows (about 11:10, 13:10, 14:40–15:00, 18:35 and 19:05 Dubai) also reach 90% or more **on days with zero API traffic**. So the bursts are scheduled work on the host, not load the API created.
   - **The lock hypothesis is not supported for long waits.** `system_health` holds **no** lock wait over 30 s and **no** deadlock in 70 days (`sql/out_part2.txt:7-12`).
   - **What is still unknown.** Which process burns the CPU, and why a CPU burst slows a request 100×.
2. **Chronic 2–10 s slowness after a step on 10-07/08, from about 16:00 Dubai. [Rev 2, revised]**
   - Five dashboard endpoints went from a p50 of 211 ms to about 4,100 ms and stayed there (`sql/out_onset.txt:1-4`).
   - Requests of 2 s or more are spread evenly across CPU bands, so this problem is **independent of CPU**.
   - It calls for change and deploy history and per-query history, not counters.
3. **Cold-start audit stalls of about 28 s. [Rev 2, revised]**
   - There are three `Migration/UpdateSage50SalaryRule` calls with no user. Each spent 27.7–28.2 s in `AuditCreateTime` (10-15, 10-19, 10-22).
   - On 10-22 at 14:09, `MasterData/WarmupApi` and `PayrollBase/Test` stalled in the same way, in the same minute.
   - This points to an app-pool start plus Mongo connection set-up or server selection `[hypothesis]`. It is not audit-store load.
4. **A long-standing baseline problem. [Rev 2, new]**
   - Stalls of 80–163 s already occurred on 09-07, 09-17 and 09-18.
   - Non-SQL CPU on the DB host stepped up on 09-21 and stayed higher.
   - Only a 60–90-day view shows either of these.

The native evidence that can test these signatures **already exists and is persisted**, but none of the old plans leads with it:

- **SQL Server `system_health` .xel files.** These cover 08-15 to 10-23. They record lock waits over 30 s, latch waits over 15 s, deadlocks, scheduler-monitor CPU split, and `sp_server_diagnostics` results.
- **SQL error logs and `msdb.backupset`.**
- **The MongoDB diagnostic log and FTDC `diagnostic.data` [unverified retention].**
- **Windows event logs.**
- **IIS and HTTP.sys logs.**
- **Hangfire recurring-job definitions.**
- **Possibly Application Insights.** It is wired in code with SQL command text enabled; production enablement is `[unverified]`.

Query Store, which the old plan put first, is **OFF** on `Brain_SandboxDB`. Database-scoped DMVs reset at the SQL restart on 10-23 09:09.

**[Rev 2] Evidence that does *not* exist natively and decides the main question:**

- **Per-process CPU history on the DB host.** Without it, the process behind problem 1 cannot be named.
  - Also, `system_health` reports SQL CPU = 0 in every sample for 70 days. That is implausible for a busy instance, so the SQL-vs-other split itself may be wrong `[unverified]`.
- **Deploy markers** for 10-07/08 and 09-21.
- **A topology map**: which host runs which process.

The time-boxed perf collector (§1A.E, decision 3) and the change lane (P6) exist to fill these gaps.

**[Rev 2] Recommended build order.** Phase IDs are kept stable. The order changes:

| Order | Phase | Area | Change |
|---|---|---|---|
| 0 | **P0** | **[Rev 4]** Host auto-detect → host profile (`unravis detect`, §3A); **[Rev 5]** plus manual hints as a peer input (`unravis-hints.json`, flags, UI form); read-only, built in parallel with P1 | **new** |
| 1 | P1 | Audit foundations | unchanged |
| 2 | P2 | Audit incident patterns (+ volley and host-CPU recurrence) | revised |
| 2b | **P2b** | **[Rev 6]** Theory checks: typed theories with deterministic verdicts over P1/P2 findings (§5A); cross-source theories light up as P3–P9 ship. **[Rev 7]** Plus the theories screen, "What is known and what is not" (§5A.H) | **new; parallel with P3; no new source** |
| 3 | P3 | SQL Server persisted diagnostics (CPU split first; + live request sampler) | revised; S1 demoted |
| 4 | **P6** | Windows host: perf collector, Task Scheduler, WAS, change lane | **moved up from 6** |
| 4b | **N1** | **[Rev 3]** Optional on-server node "burst catcher" (§3.9.4) | **new; built only if gate N is met, otherwise slides to after P8** |
| 5 | P5 | Scheduled work in SQL Server (Hangfire + Agent) | was 5th; P6 now runs before it |
| 6 | **P9** | Application Insights, **per-client optional** (≈50% of clients) | **moved up from 9**; enabled only where detected |
| 7 | P7 | IIS + HTTP.sys logs (+ cold-start evidence) | unchanged position |
| 8 | **P4** | MongoDB native | **moved down from 4** |
| 9 | P8 | Cross-source correlation timeline | |
| 10 | **P11** | .NET runtime | **moved up from 11**, above Redis |
| 11 | P10 | Redis | moved down: no evidence implicates it |
| 12 | P12 | Application logs (log4net) | |
| 13 | P13 | Evidence API + MCP consolidation | |
| — | G | Instrumentation backlog, gated per evidence gap | |

**One action is urgent and needs no code: preserve evidence now.** Native diagnostic stores roll over:

- FTDC is size-capped.
- `mongod.log` rotates.
- `system_health` files roll over.
- SQL error logs cycle on restart.
- IIS logs may be purged.

Ops should copy them for the 10-08 to 10-24 window this week (revision-1 open decision 4, now resolved; see §24 "old 4").

**[Rev 2]** Evidence preservation is now a standing action rather than an open decision. Extend the window to **09-01..10-31**, so it covers the 09-07/09-17 stalls and the 09-21 step.

Add these to the copy list:

- the DB host's `Microsoft-Windows-TaskScheduler/Operational` log;
- the DB host's System and Application logs;
- the third-party SQL monitor's own history (§1A.E, decision 4).

---

## 1. What the existing evidence already establishes

The roadmap is grounded in these facts, gathered from local files during this analysis. They are listed so that every phase's demo is tied to a real question.

| # | Observation | Source | Label |
|---|---|---|---|
| E1 | Mongo 7.0.16; ~778k docs in the probed `ApiAuditLog`; **only the `_id` index**; data 2025-10-31..2026-10-23 | `sql/out_mongo_probe.txt` | Symptom (and a cost fact for Unravis itself) |
| E2 | Real fields not in the schema doc: `PreActionFilterTimings` (array\|null), `PreActionFilterTotalMilliseconds` (number\|null), `AccountId` | `sql/out_mongo_probe.txt` vs `docs/api-audit-log-schema.md` | Symptom |
| E3 | The ad-hoc stall script misspelled those fields, so the pre-filter values came out null | `sql/mongo_stalls.js` | Process lesson: names must come from the schema census |
| E4 | 43 sync calls ≥ 45 s across 10-09..10-23, from 2 accounts and 7 users. Example: 10-13 10:45:16Z `ReportPackConfiguration/GetReportsForReportConfiguration` took 74,869 ms, of which `ApiTime` was 74,866 ms and audit insert 2 ms; `BulkReportTemplate/GetAll` (74,582 ms) started 9 ms later | `sql/out_stalls.txt`, `stalls.json` | Symptom |
| E5 | Bursts recur at the same clock minutes on several days (14:45–14:49 Dubai on 10-13/14/15/22, among others); Hangfire stays steady at ~4/min | `sql/out_clusters.txt` | Correlation (scheduled-work hypothesis) |
| E6 | 10-22 14:09 Dubai: a sync request took 54,938 ms, with `AuditCreateTime` up to 28,208 ms | `mongo_analysis` outputs | Symptom; points at the Mongo audit store |
| E7 | 10-08: five endpoints went from p50 211 ms (16h) to ~4,100 ms (17h–20h) | `sql/out_onset.txt` | Symptom (a step change) |
| E8 | SQL Server 2022 16.0.1000.6, `Brain_SandboxDB`, **Query Store OFF**; 24 CPUs, ~130 GB RAM; restarted 2026-10-23 09:09 Dubai | `sql/out_incident.txt`, `out_part2.txt` | Capability fact |
| E9 | `msdb.dbo.sysjobs` and `agent_datetime` denied; `system_health` .xel files cover 08-15..10-23; top wait since restart is `ASYNC_NETWORK_IO` | `sql/out_part2.txt` | Capability fact / Symptom |
| E10 | Scheduler monitor: SQL CPU ≈ 0 while "other process" CPU was 30–43% at 14–15h. `sp_server_diagnostics` components were all clean; systemCpu max 77 | `sql/out_part4.txt` | Symptom: contention on the DB host from a non-SQL process |
| E11 | "Other" CPU stepped from ~13–19% to ~27–30% around 09-21/22 and stayed there; daily backups take 1–2 s | `sql/out_trend.txt` | Symptom; backups effectively ruled out |
| E12 | brain-payroll runs .NET Framework 4.7.2 (Api, HangfireServer, EmployeeLogin, ClientLogin; Web on 4.5.2) and .NET 10 (`*.Core`), with Hangfire 1.8.9 on SQL Server storage, StackExchange.Redis and MongoDB.Driver | brain-payroll `*.csproj` | Stack fact |
| E13 | Application Insights is registered in .NET 10 with adaptive sampling at 5 items/s and SQL command text instrumentation; the Framework projects ship `ApplicationInsights.config`; keys appear to be placeholders | `BrainPayroll.Shared.Api/Infrastructure/RegisterApplicationInsights.cs`, `appsettings.json` | Production enablement `[unverified]` |
| E14 | In the API and Hangfire configs, the log4net Elasticsearch appender and `<root>` are commented out; the only active `RollingFileAppender` is in `BrainPayrollWindowsServices/App.config` | brain-payroll configs | API log4net output is probably disabled `[unverified]`; production transforms unknown |

**[Rev 2] Corrections to E4–E11.**

- **E4.** The 10-13 example is a *volley*, not a release cluster. Both requests come from the same user and **started** 9 ms apart (10:45:16.280Z and .289Z, `sql/out_stalls.txt:2`). Their ends differ by about 0.3 s.
- **E5.** The recurring clock windows are a property of **host CPU**, not only of API traffic (E15).
- **E6.** This is one of three or more cold-start audit stalls (E17).
- **E7.** The first request of 5 s or more came at **16:00**, and the 16h p95 was already 8,037 ms (`sql/out_onset.txt:1`). The daily series shows p95 9,424 ms on **10-07**. So the step started on 10-07/08, not "about 17:00 on 10-08".
- **E9.** Only 6 event types exist in 70 days of `system_health`. There is no `wait_info`, no `wait_info_external` and no `xml_deadlock_report` (`sql/out_part2.txt:7-12`). `LCK_M_X` totals 41 s since the restart (`out_part2.txt:49`).
- **E10.** SQL CPU is 0 in **every** sample for 70 days (`sql/out_trend.txt` §2). That makes the SQL/other split suspect (E19).

**[Rev 2] New evidence (E15–E22).** "Derived" means an ad-hoc Rev 2 join, reproducible from `sql/cpu_minute.sql` → `sql/out_cpu_minute.txt` and `sql/mongo_minute.js` → `sql/minute_data.json`. The join was not committed.

| # | Observation | Source | Label |
|---|---|---|---|
| E15 | **37 of 43 stalls** fall in minutes with non-SQL CPU of 91–98% on the DB host. Overall non-SQL CPU: p50 21%, p90 46%, p99 88%. Bursts of 90% or more recur at the same clock windows on days with **zero** sync requests: 14:40–14:56 reached 97% on 10-12, 98% on 10-20 and 96% on 10-23, all with no traffic; the 11:12, 13:10 and 19:06 windows behave the same way. Over 15 days there were 179 minutes at 90% or more; only 74 of them had sync activity in the preceding 3 minutes | Derived | Strong correlation (stall ↔ host CPU burst); scheduled host work `[hypothesis]` |
| E16 | Stalls are same-user volleys: several requests start in the same second, block together, and finish staggered about 4 s apart (e.g. 10-14 15:16:25, one user, 188.9 / 193.2 / 197.8 / 201.1 s). 40 of 43 have `ApiTime` ≥ 90% of observed. Payloads are tiny. One user accounts for 19 of the 43 stalls | `sql/stalls.json` (anonymised) | Symptom; the staggered release suggests **per-user or per-session serialization** `[hypothesis]` |
| E17 | Audit-dominated stalls: 3 × `Migration/UpdateSage50SalaryRule` with null user, `AuditCreateTime` 27.7–28.2 s (10-15 11:52, 10-19 17:21, 10-22 14:09). On 10-22 at 14:09, `WarmupApi` and `PayrollBase/Test` hit about 28 s in the same minute. These fall in 41–45% CPU minutes, not in bursts. Typical low-traffic `AuditCreateTime` is 300–700 ms on average | `sql/out_mongo_analysis2.txt`, `stalls.json` | Symptom; ≈ 28 s is close to the Mongo driver's default 30 s server-selection timeout `[unverified]`; cold start `[hypothesis]` |
| E18 | Requests of 2 s or more are **not** CPU-correlated. They fall 16.5% at <30% CPU, 15.4% at 30–49%, 14.4% at 50–69% and 19.9% at 70%+ | Derived | Counter-evidence: the chronic step is not host contention |
| E19 | Over 70 days, `system_health` `process_utilization` (SQL CPU) is 0 in every sample, while non-SQL CPU averages 12–30% | `sql/out_trend.txt` §2, `sql/out_part4.txt` | Data quality: either the counter is broken `[unverified]` or the instance is idle. Revision 1's and the earlier report's rejection of "SQL saturated" rested on it (`sql/report.src.html` ledger, lines ≈882–926) |
| E20 | Long stalls predate the step: 09-07 max 163,184 ms; 09-17 149,341 ms; 09-18 79,926 ms. Before 10-07, p95 was 0.6–3.6 s. From 10-08 it was 3.5–7 s, with 17–41 slow requests per day on the worst days | `sql/trend_data.json`, `sql/long_days.json` | Symptom: two separate problems (acute vs chronic) |
| E21 | A third-party SQL monitoring tool runs on the instance. Since the 10-23 restart it ran: a `sql_handle` collection query 85 times (avg 69.6 s, max 172.9 s, avg CPU 28 ms); `sysprocesses` polling 41,733 times (~0.8/s); XE `target_data` reads 169 times. **Its queries wait rather than compute** | `sql/out_part2.txt:30-43` | Correlation candidate: the main source of `ASYNC_NETWORK_IO` (6,189 s, line 48) `[likely]`, and a **possible history source** |
| E22 | The SQL server's local clock is **UTC+1** (`server_utc_offset_h = 1`, `out_incident.txt:3`; start 06:09 local = 09:09 Dubai, `out_part2.txt:3`). If that time zone observes DST, server-local schedules shift by 1 h in Dubai terms after the DST change `[unverified zone]` | `sql/out_incident.txt:3` | Capability fact; enables a natural experiment (ladder L4) |
| E23 | The connectivity and `error_reported` events burst on 09-21/22/23, the same days as the CPU step (E11) | `sql/out_trend.txt` §6 | Correlation (change candidate for 09-21) |

**[Rev 2] Verification pointers for the high-impact claims.** Claims not listed here and not re-checked in Rev 2 carry their Rev 1 basis. E12–E14 come from a Rev 1 read of brain-payroll and are `[unverified in Rev 2]`.

| Claim | Pointer |
|---|---|
| Only the `_id` index; ~778k docs | `sql/out_mongo_probe.txt:4-5` |
| Unravis filters on `RequestInitiatedAt` only (no `_id` range) | `internal/store/pipeline.go:99` |
| No `SetAppName` | `internal/store/mongo.go:24` (`ApplyURI` chain); no `SetAppName` anywhere in `internal/store` |
| Query Store OFF | `sql/out_incident.txt:3` |
| Restart 10-23 09:09 Dubai; 24 CPUs | `sql/out_part2.txt:3` |
| `sysjobs` / `agent_datetime` denied | `sql/out_part2.txt:27`; `sql/out_incident.txt:20` |
| No `wait_info` / deadlock in 70 days | `sql/out_part2.txt:7-12` |
| Step from 16:00 on 10-08 | `sql/out_onset.txt:1-4` |
| Mongo instance shared with live clients | `sql/out_mongo_probe.txt:2` |

**Consequence. [Rev 2, revised]** The highest-value next questions are:

1. Which process runs at about 11:10, 13:10, 14:40–15:00, 18:35 and 19:05 on the DB host?
2. Is the SQL CPU counter lying?
3. Why does a CPU burst turn a 2 s page load into a 200 s volley?
4. What changed on 10-07/08 and on 09-21?
5. What happens when the app pool cold-starts against Mongo?

Questions 1 and 3 need **one live, time-boxed capture**: per-process CPU plus a SQL request sample in a predicted window. Persisted stores alone cannot answer them. Questions 2, 4 and 5 are answered by persisted stores.

---

## 1A. Root-cause drilling: what is actually needed [Rev 2, new]

This section drives the phase order and the feature cuts below. It is grounded in what the manual investigation actually did and missed.

### 1A.A Investigation path actually taken

| # | Step (script → output) | Signal found | Dead end / lesson |
|---|---|---|---|
| 1 | Query Store first (`sql/incident.sql` → `out_incident.txt:3`) | QS **OFF**; server UTC+1 | Dead end: the old plan's first source does not exist here |
| 2 | Agent history (`out_incident.txt:20`, `out_part2.txt:27`) | n/a | Dead end: `agent_datetime` and `sysjobs` denied. A grant was missing, not data |
| 3 | Mongo probe (`out_mongo_probe.txt`) | `_id` index only; `PreActionFilter*` and `AccountId` exist; shared instance with live client databases | Lesson: every analysis query was a COLLSCAN. `_id` pruning was added by hand |
| 4 | `mongo_analysis.js` | Hangfire flat (~245/h, p50 12 ms); worst sync hours | Dead end: the per-API section printed nothing. The kind filter `['Sync','sync','SYNC',null]` missed the real value `"Synchronous "` (trailing space). Lesson: the kind and field vocabulary must come from a census |
| 5 | `mongo_analysis2.js` → `out_mongo_analysis2.txt` (regex `^Sync`) | Slowest list; **14:09 on 10-22: three requests at ≈28.2 s `AuditCreateTime`**; 16:00 dashboard burst (6 endpoints × 52 calls, ~1 per 69 s, which looks like auto-refresh) | Dead end: the same-hour-yesterday baseline was empty (sparse sandbox traffic). Lesson: baselines need a minimum n and a fallback (A5 low-n masking) |
| 6 | `mongo_analysis3.js` | Per-minute view; 6 requests never completed; Hangfire max 11–14 ms in stall minutes | n/a |
| 7 | `mongo_onset.js` → `out_onset.txt:1-4` | Step on 10-08: first ≥ 5 s request at 16:00 | n/a |
| 8 | `mongo_clusters.js` → `out_clusters.txt` | 13 events; Hangfire steady at 4/min in every stall | n/a |
| 9 | `mongo_stalls.js` → `out_stalls.txt`, `stalls.json` | 43 stalls, 2 accounts, 7 users | Dead end: the typos `PreActionFilterTotalMillseconds` and `PerActionFilterTimings` made every pre-filter value null (E3). Pre-filter time was **never actually checked** |
| 10 | `sql/part2..part4` → `out_part2..4.txt` | Restart 10-23; `system_health` holds 6 event types only; third-party monitor; 22-Oct CPU split (SQL 0, other 30–43%) | Dead end: date-filtered `xp_readerrorlog` returned 0 lines (`out_part2.txt:16`) |
| 11 | `incident_trend.sql` → `out_trend.txt` | **09-21 non-SQL CPU step**; SQL CPU 0 for 70 days; connectivity bursts on 09-21..23; backups 1–2 s | n/a |
| 12 | Daily series (`trend_data.json`, `long_days.json`) | Stalls already on 09-07/17/18; p95 step on 10-07/08 | n/a |
| 13 | Earlier report ledger (`sql/report.src.html` ≈882–926) | Rejected "SQL saturated", "volume" and "host froze"; left "clock recurrence" open | Lesson: it **rejected "SQL saturated" on the SQL CPU = 0 reading**, which may be a broken counter. It never joined minute CPU to stalls |
| 14 | **Rev 2:** `cpu_minute.sql` → `out_cpu_minute.txt` joined with `mongo_minute.js` → `minute_data.json` | **E15** (37/43 stalls inside CPU bursts; bursts recur on zero-traffic days); **E18** (2 s+ requests independent of CPU); **E16** (volleys) | n/a |

**Still unexplained:**

1. Which process causes the bursts.
2. Whether "other" CPU is really SQL, given a possibly broken counter.
3. The mechanism that turns a burst into a 100× slowdown with ~4 s staggered completion. Candidates: per-user serialization (for example ASP.NET session-state locking `[hypothesis]`), CPU starvation of a single-threaded path, or many short waits under 30 s.
4. What changed on 10-07/08 and on 09-21.
5. Whether the 3 Sage50 calls are a scheduled caller or a warm-up.
6. Why 6 requests never completed.
7. Topology: is IIS/Hangfire/Mongo on the DB host?

**What would have shortened the path:**

1. An automatic join of the minute CPU split to stalls (P2 × P3, rule B13/C9). It would have produced the main lead on day one instead of step 14.
2. A schema and vocabulary census (P1). It would have removed the dead ends in steps 4 and 9.
3. The grants requested up front (§1A.E). They would have removed step 2.
4. A host map plus per-process CPU (P6). It would have named the process instead of leaving "other".
5. Deploy markers (P6 change lane). They would have dated the cause of the steps on 10-07/08 and 09-21.

### 1A.B Hypothesis × evidence discrimination matrix

Power is the source's ability to **confirm or refute** the class here:

- **H** decides it.
- **M** shifts it.
- **L** gives context only.

"Now" is the state after Rev 2.

| # | Hypothesis class | Now | Discriminating evidence (source → field / query) | Power | Consequence for features |
|---|---|---|---|---|---|
| 1 | Sustained step (code/query/data change) | **Open**: chronic 10-07/08 step (E7, E18) | Audit daily/hourly p50 per endpoint (B9) → H for *when*. Change lane: IIS/WAS start events, `bin` file times, MSI/WU events (P6) → H for *what*. QS `runtime_stats` (if enabled, forward only) → H. Plan cache `dm_exec_query_stats.creation_time` (since restart) → M | H | Keep B9 Must; promote the change lane to Must; QS enablement is decision 2 |
| 2 | Brief stalls (single long block) | **Supported**: 43 stalls, volleys (E16) | Audit stall catalog (B1/B2) → H for occurrence; volley detector (B12) → H for shape | H | Keep the catalog; **replace B4 as the headline with B12** |
| 3 | Recurring clock-time cause | **Supported**: on host CPU, also with zero traffic (E15) | `system_health` scheduler-monitor per-minute CPU recurrence (B13) → H. Task Scheduler triggers + Operational log (P6) → H if enabled. Hangfire cron (P5) → M. Agent schedules (P5) → M. Monitor schedule (decision 4) → M | H | B13 is new, Must. P6 moves up. P5 stays |
| 4 | Tenant / endpoint concentration | **Weak**: one user in 19/43, but volleys inflate per-user counts | `AccountId` and user lift (A2/A3) per *volley*, not per request → M | M | A2/A3 count volleys, not requests |
| 5 | Load-correlated (own traffic) | **Refuted for stalls**: bursts at zero traffic (E15). Refuted earlier for volume (report ledger) | Exact in-flight sweep (B11) vs stall; requests per minute vs CPU → M | M | B11 stays High Value, not Must |
| 6 | Audit store (Mongo) | **Narrowed**: only the ~28 s cold-start cases (E17) | `mongod.log` connection accept/auth + slow `insert` at that second (P4) → H. `serverStatus.connections` → L (live) | H for E17 only | P4 moves down to 8th; M9 cold-start rule is new |
| 7 | Lock / blocking | **Refuted for waits ≥ 30 s** (E9). Short blocking is open | `system_health` `wait_info` → H for ≥ 30 s (none found). **Live request sampler** (`dm_exec_requests` + `dm_os_waiting_tasks`, 5 s, in predicted windows, S14) → H for 5–30 s. QS wait stats → M. Blocked-process report (G7) → H, but a configuration change | H (≥ 30 s refuted) / M | **S1 demoted to Later**; S14 is new, High Value |
| 8 | Non-SQL CPU on the DB host | **Strongly correlated** (E15). The process is unknown; the attribution may be wrong (E19) | Per-process CPU (`Process(*)\% Processor Time` via PDH or a DCS, P6 W7) → H. A cross-check of the SQL CPU counter (`Process(sqlservr)` vs ring buffer, ladder L4) → H | H | Perf collector is **Must** (decision 3); W7 and the S4 cross-check are Must |
| 9 | Pool / queue (IIS, ASP.NET, HTTP.sys) | **Open**: may explain volley serialization | httperr `Timer_AppPool`/`QueueFull` (P7) → H for queueing. `ASP.NET\Requests Queued`, `Request Wait Time` (P11a, live/DCS) → H. W3C `time-taken` − observed → M | M–H | P11a moves above Redis. P7 stays |
| 10 | GC / thread pool | **Less likely**: Hangfire unaffected (E5), *if* co-located `[topology unverified]` | `.NET CLR Memory\% Time in GC`, `ASP.NET` counters (P11a); App Insights `RedisTimeoutException` IOCP/WORKER text (P9) → M | M | Stays in P11, Later |
| 11 | Network / DNS / AV / backup | Backup **ruled out** (1–2 s daily, E11). AV/network open, no pointer | Defender Operational 1000/1001 (P6) → H for AV. Backups (msdb) → H (done). DNS/network → L, no native history | M | W4 stays; DNS/network stays Unnecessary |
| 12 | Deploy / config change | **Candidate** for 10-07/08 and 09-21 (E23) | WAS/W3SVC events, `bin` times, MSI/WU, SQL error log "Configuration option" (P3/P6) → H. Default trace → L here (since restart only) | H | Change lane Must; default trace stays Later |
| 13 | Cache / Redis | **No pointer** | `SLOWLOG`, `INFO` (live only, P10); App Insights Redis dependencies (P9) → M | L | P10 moves down |
| 14 | External dependency (HTTP / third-party) | **Open, untested** | App Insights `dependencies` where `type = HTTP` (P9) → H where available. Otherwise none native (G2) | H (P9 clients) / L | P9 per-client is the only native test |
| 15 | Per-user serialization (session lock, per-user app lock) **[Rev 2 class]** | **Open**: fits the ~4 s staggered release (E16) | Audit: same-user overlap structure (B12 stagger metric) → M. ASP.NET session-state configuration (code/config read of brain-payroll) → H for possibility. App Insights per-request start/end per session → H | M | B12 records stagger; config read is an analysis task, not a feature |

**Rank of what to build first, by power on the open classes:**

1. Host-CPU recurrence + per-process CPU (classes 3 and 8).
2. Change lane (classes 1 and 12).
3. Volley detector (classes 2 and 15).
4. Live SQL request sampler (class 7, short waits).
5. App Insights where present (classes 14 and 1).
6. IIS/ASP.NET queueing (class 9).
7. Mongo cold start (class 6).
8. Redis (class 13).

### 1A.C Drilling ladders (cheap first; escalate only when the step is exhausted)

**L1: Acute stall volley (45–200 s)**

1. Audit: B1/B2 catalog → B12 volley (user, endpoints, stagger) → B13 overlap with the CPU burst. *Cost: none (existing data).*
2. `system_health` minute CPU split around the volley ±10 min (P3 S4). If CPU is under 60%, branch to L3 (cold start) or to class 9/15.
3. Recurrence on CPU itself (B13): is the window hot on zero-traffic days? If yes, scheduled host work.
4. Schedule inventory for the predicted window: Task Scheduler triggers + Operational log (P6 W3), Hangfire cron (P5 H1), Agent (P5), third-party monitor schedule (decision 4), Defender (W4).
5. **Live capture in the next predicted window** (decision 3): per-process CPU (W7) + SQL request sampler (S14), plus ASP.NET counters if the API host is separate (P11a T1).
6. *Escalation point.* The process may be named while the 100× mechanism stays unclear, or the sampler may show the volley requests absent from SQL. Then: App Insights waterfall for P9 clients (AI2/AI3). For the rest, **G1** (request ↔ session linkage) and **G4** (thread-pool or queue state at request start).

**L2: Chronic step (2–10 s, 10-07/08)**

1. B9 step dating to the hour per endpoint, plus movers. *(existing)*
2. Change lane ±24 h before the step: WAS/W3SVC start, `bin` mtimes, MSI/WU, SQL "Configuration option" lines (P6 W5, P3 S10).
3. Per-query: plan cache for the movers' statements (`dm_exec_query_stats`, since restart; low power) and QS once enabled (decision 2; forward only). Run the live sampler in a normal busy hour to see the waits of 2–10 s requests (S14).
4. App Insights dependency leaderboard, slow vs baseline (P9 clients).
5. *Escalation point:* **G2** (per-request DB time and call count) when neither QS nor P9 attributes the time.

**L3: Cold-start audit stall (~28 s `AuditCreateTime`)**

1. Audit: B6 + "first request after an idle gap ≥ N min" + co-occurring `WarmupApi`/`Test` calls (M9 precondition). *(existing)*
2. WAS events (5186 idle shutdown; recycle IDs `[unverified]`) and W3SVC start events in the minutes before (P6 W1). IIS W3C first-request after a gap (P7).
3. `mongod.log` at that second: connection accepted, authentication, slow `insert` (P4 M1/M9).
4. App exceptions (`MongoTimeoutException`, server selection) in log4net or App Insights (P12 L2 / P9 AI4).
5. *Escalation point:* none to code. The remedy is app configuration (app-pool start mode, idle timeout, driver warm-up), owned by the app team. **G8** stays "preserve evidence".

**L4: Recurring host CPU bursts (independent of traffic)**

1. 90-day daily and 14-day minute CPU recurrence from `system_health` (B13). *(existing data)*
2. **Counter sanity:** compare ring-buffer `process_utilization` with Δ`sum(total_worker_time)` of `dm_exec_query_stats` over 60 s, and with `Process(sqlservr)\% Processor Time` from PDH. If they disagree, relabel "other" as "unattributed" (S4 caveat).
3. Natural experiment: if the server zone observes DST (E22), do the windows shift by 1 h in Dubai time after the change? A shift points to a host-local schedule (Task Scheduler or Agent). No shift points to a UTC- or Dubai-based scheduler, such as Hangfire with an explicit `TimeZoneId` or an external monitor.
4. Schedule inventory (as in L1 step 4).
5. Per-process CPU in 5 predicted windows (decision 3) → W7 names the process.
6. *Escalation point:* none to code. Native sources suffice once the process is named.

**L5: Baseline drift (09-21 CPU step, pre-existing stalls)**

1. 90-day daily rollups: audit p95, stall count, non-SQL CPU, connectivity/error counts. *(existing)*
2. Change lane on 09-20..09-23: WU/MSI, service installs (7045 `[unverified]`), monitor installation, and the error log (archives go back to 09-12).
3. If unresolved, the third-party monitor's history for 09-21 (decision 4).
4. *Escalation point:* none to code.

### 1A.D Diagnostic-value verdict per phase

| Phase | Verdict (one line) |
|---|---|
| P0 [Rev 5 row] | **Medium as an enabler:** narrows no cause by itself, but finds the cheapest leads first (existing DCS or monitor history, recycles in burst windows, retention at risk) and settles the per-client topology. Built first, in parallel with P1. |
| P1 | **High:** unlocks every ladder cheaply; the census would have prevented two of the dead ends. |
| P2 | **High:** turns E15–E20 into findings. B12 (volley) and B13 (CPU recurrence) carry the main lead; B4 drops to secondary. |
| P3 | **High:** the persisted CPU split (S4, with the sanity check) is the core of L1/L4/L5. Long lock waits (S1) are refuted here, so S1 drops to Later. |
| P6 | **Highest of the new sources:** the only path to the named process (W7), task schedules (W3) and deploy markers (W5). It moves to 4th. |
| P5 | **Medium:** cheap test of Hangfire/Agent schedules for L1/L4. Weaker than P6, because the bursts are non-SQL and Hangfire rows are unaffected. |
| P9 | **High where present (≈50% of clients), absent elsewhere:** the only native request-scoped proof; tests class 14. Moves to 6th, per-client. |
| P7 | **Medium:** counter-evidence for queueing (class 9) and cold-start lifecycle (L3). |
| P4 | **Medium-Low:** explains only the ~28 s cold-start cases (3–5 events); also polices Unravis's own load on a Mongo that serves **live clients**. Moves to 8th. |
| P8 | **High once P2/P3/P6 exist:** C9 (CPU burst explains volley) and C4 (change explains step) are its first useful composites. |
| P11 | **Medium:** the mechanism behind the 100× slowdown, if the API host queues (class 9/10); useful only in captures. Moves above P10. |
| P10 | **Low:** no evidence points at Redis; live only. |
| P12 | **Low-Medium:** a possible scheduled worker (Windows Services); API logs appear disabled. |
| P13 | **Multiplier, not diagnostic:** keep last. |
| P2b [Rev 6 row] | **High as a guard, not a source:** adds no evidence, but turns the ledger's hand-set statuses into reproducible verdicts. It would have blocked the report's main error, rejecting "SQL saturated" on a flat-zero counter (E19, V6), and its n = 2 day comparison for "volume" (V4). |

### 1A.E Decisions and the consolidated permissions/access checklist

The decisions are in **§24**. **[Rev 4]** The user accepted decisions 1–10. **[Rev 5]** Decisions 11–13 are decided by judgment (user-delegated), so **no decision remains open**. §24 lists only the client facts still to collect. **[Rev 6]** Two new decisions (14, the LLM path, and 15, the multiplicity and permutation policy) carry proposed defaults in §24 and await the user's confirmation.

**Permissions and access checklist.** Request everything in one go. All entries are read-only, with least privilege per source. `<login>` and `<user>` are placeholders. Never put real credentials in tickets or in this document.

| Source | Grant / membership (exact) | Unlocks | Notes |
|---|---|---|---|
| SQL Server (instance) | `GRANT VIEW SERVER STATE TO [<login>];` On 2022 the narrower `GRANT VIEW SERVER PERFORMANCE STATE` covers DMVs and XE file reads `[unverified exact split]`. Request `VIEW SERVER STATE` if the DBA prefers one grant | `system_health` reads, ring buffers, DMVs, live sampler (S14), default trace `[unverified: may need ALTER TRACE]` | The error log was already readable with the current login (`out_part3.txt`). If denied, ask for `EXECUTE` on `sys.xp_readerrorlog` `[unverified]`. Do **not** ask for `securityadmin` |
| SQL Server (msdb) | `USE msdb; CREATE USER [<user>] FOR LOGIN [<login>]; GRANT SELECT ON dbo.sysjobs, dbo.sysjobschedules, dbo.sysschedules, dbo.sysjobhistory, dbo.sysjobactivity, dbo.backupset, dbo.backupmediafamily TO [<user>];` Optional: `ALTER ROLE SQLAgentReaderRole ADD MEMBER [<user>];` | Agent schedules and history (P5), backups (S9) | `agent_datetime` is not needed: Unravis converts the integers client-side |
| SQL Server (each app DB) | `CREATE USER [<user>] FOR LOGIN [<login>]; GRANT VIEW DATABASE STATE TO [<user>]; GRANT SELECT ON SCHEMA::[HangFire] TO [<user>];` Optional: `GRANT VIEW DEFINITION TO [<user>];` (resolves object names in lock resources) | QS views (when ON), Hangfire tables (P5), DB DMVs | No `db_datareader`: business tables are never read |
| SQL Server (DBA configuration, not grants) | Query Store ON (decision 2). Blocked-process threshold + XE session only under G7 | Forward-looking per-query history | DBA-owned changes |
| MongoDB (audit instance) | A user with roles `[{role: "clusterMonitor", db: "admin"}, {role: "read", db: "<audit db>"}]` | `serverStatus`, `getLog`, `getCmdLineOpts`, bounded `$currentOp`, `replSetGetStatus`, audit reads | `clusterMonitor` is read-only. The instance serves live clients, so P1 pruning is mandatory |
| Redis | `ACL SETUSER unravis on >(set by ops) resetkeys resetchannels -@all +ping +info +slowlog\|get +latency\|latest +latency\|history +client\|list +client\|setname +config\|get` | P10 | No key access at all. Azure Cache: Azure "Monitoring Reader" instead `[unverified role name]` |
| Windows (each host) | Local groups: **Event Log Readers**, **Performance Monitor Users**. With WinRM: **Remote Management Users**, or better a **JEA endpoint** limited to `Get-WinEvent`, `Get-Counter`, `Get-ScheduledTask`/`Get-ScheduledTaskInfo` and `Get-ChildItem`/`Get-Item` on listed log folders | Event timeline (P6), counters (W7), task triggers (W3) | Reading scheduled tasks remotely without admin may need JEA `[unverified]`. Enabling the TaskScheduler Operational log is an ops change |
| Windows (files) | A read-only SMB share (not `C$`) or a JEA file read over: IIS `LogFiles\W3SVC*`, `System32\LogFiles\HTTPERR`, the app `bin` folders (listing only), log4net folders, the `mongod` log folder | P7, change lane, P12, P4 | Ops may prefer a scheduled copy to a share |
| App Insights (per client, if present) | Entra ID **Log Analytics Reader** on the workspace (or **Reader** on the App Insights resource) `[unverified minimal role]` | P9 | Outbound HTTPS only |
| Network | TCP 1433 (or the instance port + UDP 1434), 27017, 6379 (6380 TLS), 5985/5986 (WinRM), 445 (SMB, if used), 135 + the dynamic RPC range only if the RPC fallback is chosen, 443 outbound for App Insights | Live connectors (§3.7) | Request only the ports for the channels chosen in decision 6 |
| **[Rev 3] Unravis node (only under gate N, per host)** | **Rights.** The service runs as the virtual account `NT SERVICE\UnravisNode`, or as a gMSA when SQL is remote from the node. That account gets local **Performance Monitor Users** and **Event Log Readers**, plus read ACLs on the listed log folders.<br>**SQL.** `VIEW SERVER STATE` for the node's own login (integrated auth to the local instance), instead of the engineer's login.<br>**Install.** Local admin once, for service install and uninstall only (ops).<br>**Network.** One inbound TCP port (default 8766) from the hub's IP only, or **no port** in spool-only mode (decision 8) | The §3.9.4 node packs | The node needs **fewer** network rights than §3.7: no WinRM, RPC or SMB from the engineer's machine to that host, and no remote SQL login. It needs more change approval: a service, a certificate and a firewall rule |
| **[Rev 4] Auto-detect, `unravis detect` (P0, one-shot per host)** | **Default:** ops runs the signed exe once in an **elevated** console. The code is read-only, but the token is elevated (decision 11).<br>**Read-only equivalents, if elevation is refused:**<br>- **Event Log Readers** (channels, retention);<br>- **Performance Monitor Users** (`typeperf -q`);<br>- **Performance Log Users** (`logman query`) `[unverified]`;<br>- an ops-granted **read ACL on `%windir%\System32\inetsrv\config`** (IIS sites, pools, recycle schedules; Administrators-only by default `[unverified]`);<br>- read on the IIS site physical paths and app service folders (configs, log4net, App Insights);<br>- read on the `mongod`/Redis config and data folders.<br>Registry SQL keys and service enumeration work as Users `[unverified per key]` | P0 host profile → decision-1 host map, connector and pack proposals | No network port, no service, no SQL/Mongo/Redis login (P0 never connects). A non-elevated run reports each denied probe with its missing right. Approval needed: placing a signed exe on the server. **[Rev 5] Hints reduce the rights:** a hints-only run (`--no-detect`) needs only read on the hinted paths, with no elevation and no `inetsrv\config` ACL unless that file is hinted. Hub-side hints for remote hosts need no rights on the host until validation |

---

## 1B. The October incident report: inventory and gaps [Rev 6, new]

**Source.** The report is `sql/report.src.html`. `sql/build_report.js` turns it into `sql/report.html` by substituting three JSON bundles (`report_data.json`, `trend_bundle.json` and `minute_bundle.json`). Those bundles come from the `sql/mongo_*.js` scripts and from `prep_bundle.js` and `prep_minute.js`. The result is a static page with the data embedded and charts drawn client-side.

Only the report's prose, section structure, theory list and chart logic were read for this review. No data blobs were read. Section names are quoted; data values are not.

### 1B.A What the report does, and where the roadmap covers it

| Report element (section name) | What it does | Roadmap coverage | Gap → action |
|---|---|---|---|
| Summary: KPI tiles and verdict bullets ("Chronic, not a 22 Oct one-off", "Root cause is not proven" and others) | Headline numbers plus hand-written verdict sentences | None. The ledger has statuses but no summary | **New:** a deterministic summary generated from P2b verdicts (§5A.F item 1). The headline reads "root cause: not proven" unless some verdict carries the `proven` label |
| Explorer: "Zoom from two weeks down to one minute" | Presets, jump-to-stall, previous/next, drag-to-zoom, breadcrumb, bucket choice with a "too fine" fallback. Synced panels show count, Hangfire finished, latency (average, worst one-minute p95, slowest), ≥ 2 s count, host CPU split, and a slow-request dot plot | Partly: the MVP drill model, P2 Pulse, P8 lanes (D0–D3) | P3 adds the CPU lane to P2's Pulse; P8 generalises it. **Later:** the synced crosshair and brush, and "compare with the window just before" arrows (P8 D1) |
| "Is it load?" | Per-minute requests vs latency scatter. Pearson r on log latency over ≥ 3 minutes, mapped to words ("weak", "moderate"), with no p-value and no autocorrelation handling | MVP `SpearmanLoad` (`internal/audit/load_correlation.go:160`) | **Replaced by V4** (§5A.C): Spearman, block permutation, minimum n, load-stratified comparison. **Skip** verbal-only bands |
| "Which hours of the day?" and the heatmap "Which hours were bad?" (metric toggle p95, > 10 s, CPU; cells under 5 requests dashed) | Day × hour grid with thin-cell masking | Partly: P2 recurrence grid (episodes × 5-minute slots); A5 low-n masking | **Add in P2:** a day × hour heatmap with a metric toggle that reuses A5 masking |
| "How did each day compare?" | Daily table vs the previous busy day (≥ 100 requests); low-traffic days greyed; CSV | P1 trend extension, P2 honest zone (n < 20) | Covered. **Add:** CSV export of every table, pseudonymised by default |
| "Compare any two days" | Hourly p95 overlay, drawn only for hours with ≥ 3 requests, plus a count of hours with p95 > 5 s | None | **Later (P2 polish).** P2b regime tests make the formal comparison; this is a convenience view |
| "When did the API get slower, and which endpoints?" Includes a trend from 15 Aug with bands from 8 Oct and from 21 Sep, and "The step change, dated" (onset tiles) | Step dating, plus period bands drawn by hand | B9, movers, W9/annotations (§20) | Covered. P2b TH4/TH7 test the dates (V3); the bands become change-point boundaries, not hand-set dates |
| "Endpoints that got slower / faster" and "Endpoint explorer" | Before = **sampled** days 2 Sep–1 Oct, after = 9–23 Oct. Request-weighted mean of daily p95, 30+ requests in both periods, share of sync time | P2 movers | Covered. **Add:** the minimum n in both periods, the share-of-time column, and BH q-values (V8). Verdicts refuse sampled-day periods (V6) |
| "Do the long stalls repeat?" | Stall ≥ 45 s; bursts with requests, longest, endpoints, users, in-handler share. "Same clock minutes on different days" groups by clock minute (≤ 6 min apart, ≥ 2 distinct days). Notes that few distinct users could make it a user routine | B1/B2/B3 | Covered. The report's own user-routine caveat becomes **guard V7**, and the recurrence gets a **permutation test V2** |
| "Inside a stall" | Per minute: sync started, sync finished, Hangfire finished. Peak finish minute vs median; "Hangfire finished at least one job in X of Y minutes" | B8 (Hangfire z-test); B4 release instant | **New V5** (control-path continuity), with a topology precondition from P0 |
| "When did it go wrong?" (22 Oct, user-flagged Window A and Window B) | Hourly aligned charts with hand-entered reported windows | None | **Add now:** user-reported windows as typed input, tested against measured anomalies (§5A.F item 4) |
| "Was 22 Oct worse than 21 Oct?" | Overlay, with the caveat that 21 Oct is not a clean baseline | None | **V9**, regime-aware baseline selection, flags contaminated baselines automatically |
| "Zoom into one hour", "Where did the time go?" | Episode jumps; busiest endpoints this hour; audit-create vs API-time split of the slowest requests | MVP drill, phases, B6/B7 | Covered |
| "What SQL and the machine were doing" | Day table (CPU, idle samples, backups, diagnostic components, restarts). "Before and after" over three hand-picked periods. A **hardcoded** "22 Oct, by window" table. Notes on blind spots | P3 S4, S9, S10, §3.2 capability rows | Covered by P3. The three periods become V3 regime boundaries; the hardcoded window table becomes a generated table; the blind-spot notes become capability rows |
| "What is known and what is not" | **8 hand-set theories** with status Observed, Consistent, Open or Rejected, plus evidence and gap text | P2 ledger (manual statuses) | **Core gap → P2b** (§5A). Every theory is mapped to a test in §5A.D. **[Rev 7]** The section itself is kept as the theories screen (§5A.H): one verdict row per theory, with evidence, gap and the reproducible tests one click deeper, all generated from templates |
| "Next steps" | Manual to-do list: what changed before 8 Oct 16:00; read the dashboard endpoint code; what runs at the recurring windows and recycles; the 21 Sep host change; enable Query Store and blocked-process reporting; grant `sysjobs` | Spread over C4/C10, HP2/C2, L5, §3.2 remedies, §20 grants generator | **Add now:** a generated "what would change my mind" list (§5A.F item 3). The code read stays manual |
| "How to read this" | Definitions: latency formula, approximate percentiles, the clocks (Mongo UTC, SQL local UTC+1, XE UTC), thresholds (low traffic < 100, p95 blank < 50, stall ≥ 45 s, thin < 5), privacy statement | §3.1 thresholds-as-applied, §3.3 clocks | **Add now:** a methods appendix generated from the rule versions and thresholds actually applied (§5A.F item 1) |

### 1B.B Manual steps the human did that the tool should do

1. **Picked the before and after periods by eye**, and on sampled days. → V3 change points set the regime boundaries; V6 refuses sampled periods for verdicts.
2. **Chose the matched-traffic day pair by hand** (a fast day vs a slow day with similar traffic). → V4 load stratification over all busy days.
3. **Wrote every theory status and its evidence text by hand.** → P2b verdicts with test results attached.
4. **Rejected "SQL saturated" on SQL CPU = 0.** That counter is flat for 70 days (E19). → The V6 precondition makes the verdict inconclusive until the L4 counter cross-check passes.
5. **Rejected "host froze" because Hangfire kept finishing**, without checking that Hangfire runs on the same host. → The V5 precondition reads the topology from the P0 host profile.
6. **Hardcoded the reported windows and the "22 Oct, by window" table.** → Typed reported-window input and generated tables.
7. **Joined CPU minutes to stalls by hand** (§1A.A step 14). → B13/C9, tested by TH10 with a counted base rate (V1).
8. **Converted three clocks by hand** (UTC, UTC+1, UTC+4). → §3.3 (already planned).
9. **Anonymised the user labels by hand.** → P13 HMAC pseudonymisation; P2b exports use the same scheme.
10. **Ran the script pipeline by hand** (`mongo_*.js` → `prep_*` → `build_report.js`). → The tool's own fetch, P2b, and the incident report export.

### 1B.C What the report got right and the roadmap keeps

The roadmap keeps these practices from the report:

- thin-cell and minimum-n guards;
- "only endpoints with 30+ requests in both periods";
- the caveat that a few users could explain recurrence;
- the warning that 21 Oct is not a clean baseline;
- an explicit "Root cause is not proven";
- payloads, URLs and IPs never read.

P2b turns these from prose caveats into enforced preconditions and guards.

---

## 2. Phase order with rationale

| Order | Phase | Technology | Why here |
|---|---|---|---|
| P0 [Rev 4, Rev 5] | Host auto-detect + manual hints | Local registry, configs, services, listening table (read-only); or locations stated by dev/DevOps | Small, read-only, ungated. It machine-fills the decision-1 host map, which every host-side phase (P3–P12) consumes. It also surfaces the cheapest leads first: existing DCS or monitor history, scheduled recycles in burst windows, and retention at risk. Built in parallel with P1. |
| P1 | Audit foundations | `ApiAuditLog` (Mongo) | Makes every later query cheap and correct: `_id` pruning, schema-driven field names, the unused PreActionFilter phase, `AccountId`, time zones, capability and coverage. It is existing data, so it is first by principle. |
| P2 | Audit incident patterns | `ApiAuditLog` | Turns E4–E7 into deterministic findings: stall catalog, clock recurrence, release clusters, Hangfire control, step dating, hypothesis ledger. Every later phase is tested against these findings. |
| P2b [Rev 6] | Theory checks | Unravis (over P1/P2 findings; later phases plug in) | Turns the ledger's hand-set statuses into deterministic, reproducible verdicts with falsifiers and anti-self-deception guards (§5A). The audit-only theories are testable as soon as P2 exists, so this comes before P8, not inside it. It needs no new source, so it runs in parallel with P3 and does not delay it. **[Rev 7]** It also ships the theories screen (§5A.H), so the first thing an engineer sees after P2 is the report's "known / not known" view, now reproducible. |
| P3 | SQL Server persisted diagnostics | SQL Server | **[Rev 2]** The persisted per-minute CPU split for 70 days is the core evidence for E15/E19. The lock-wait test has already been run: there are no waits over 30 s. It also adds the live request sampler (S14) for short waits, and it rules out backups. |
| P4 | MongoDB native | MongoDB (audit server) | Explains E6 (the audit-insert stall). Reuses the existing driver and connection. **[Rev 2]** Built 8th: it explains only the 3–5 cold-start cases (E17). |
| P5 | Scheduled work in SQL Server | Hangfire tables + msdb | Explains clock-minute recurrence (E5) from recurring-job cron definitions and Agent schedules. Reuses the P3 SQL connection. |
| P6 | Windows host | Event Log, Task Scheduler, perf counters | App pool recycles, reboots, AV scans, updates and scheduled tasks. It names the "other process" when a time-boxed counter log exists. **[Rev 2]** Built 4th, because it is the only native path to the named process, task schedules and deploy markers (§1A.B classes 3, 8 and 12). Access is through the live-first model (§3.7). |
| P7 | IIS + HTTP.sys | W3C logs, httperr | Time outside the API pipeline, 503s, queue drops, client disconnects. Less central to stalls that sit inside `ApiTime`, so it comes after P6. |
| P8 | Correlation timeline | Cross-source | Only after P2–P7 are individually useful. Composes their findings on one clock and climbs the evidence ladder. |
| P9 | Application Insights | App Insights | **[Rev 2] Per-client optional.** It is live for about 50% of clients. Built 6th, and enabled per client by capability detection. Where present, it holds sampled per-request dependency timings: existing evidence that makes most of old Task 16 unnecessary for those clients. Every other phase works without it. |
| P10 | Redis | Redis | Confirmed in use (E12). Native evidence is mostly live (`INFO`, `SLOWLOG`, 128 entries), so its value for past incidents is low. Useful during a predicted recurrence window. |
| P11 | .NET runtime | Framework perf counters / .NET 10 meters | Live only, with no history by default. Most useful together with a P6 time-boxed counter log in a recurrence window. Split by runtime because the mechanisms differ. |
| P12 | Application logs | log4net files | Conditional: API log4net looks disabled (E14). The Windows Services log is active and may be the scheduled worker behind E5 `[unverified]`. |
| P13 | Evidence API + MCP | Unravis | Consolidates the per-phase endpoints into one versioned evidence schema, a case export and a few generic MCP tools. |
| G | Instrumentation backlog | brain-payroll code | Each item is opened only by a named evidence gap (§21) after its native alternative has been ruled out. |

**Why not SQL first and audit second?**

- P1 and P2 are cheap and use data already connected.
- They produce the exact timestamps that P3 needs: release-cluster end instants and recurrence windows.
- Running P3 without them means hand-picking windows, which is what the `sql/` scripts did manually.

**Why not correlation earlier?**

- A correlation view over sources that are not individually trustworthy produces confident-looking coincidences.
- The brief forbids it, and the review findings agree.

---

## 3. Cross-cutting design (applies to every phase)

### 3.1 Evidence model (replaces the old "Telemetry Provider seam")

Do not build a generic `Provider{Kind, Diagnose, Close}` seam up front. Grow one small model, phase by phase:

```
Observation  {source, kind, startUTC, endUTC, entity, metrics{}, raw_ref}
Finding      {rule_id, rule_version, label: symptom|correlation|strong_correlation|proven,
              title, window, entities[], evidence[]Observation refs, thresholds{} (as applied),
              counter_evidence[], gaps[]}
Capability   {source, check, status: ok|denied|off|absent|unknown, detail, remedy}
```

- **No `Severity` and no `Advice` free text.**
  - Order findings by *measured impact*: Δmean × count (as in the MVP) or seconds of affected request time.
  - "Remedy" exists only on `Capability` rows. It names a native step, for example "grant `VIEW SERVER STATE`" or "enable Query Store".
- **Every finding stores the thresholds it applied.** This lets a reviewer reproduce it, and changing a threshold re-labels findings visibly.
- **`counter_evidence` is mandatory where a rule could be refuted.** Example: "Backups overlap: none; nearest backup was 1.4 s at 02:00."
- **[Rev 6]** Two record types are added on top of the model: `Theory` (a typed, versioned hypothesis) and `Verdict` (the engine's deterministic outcome for one theory version over one fingerprinted input set). Both are specified in §5A.B. Findings stay the only evidence. A verdict only *references* findings and test results; it never creates evidence.

### 3.2 Capability and permission probe (every source)

Each phase starts with a read-only probe that renders as the first panel for that source. It shows:

- version and edition;
- each required permission, granted or denied;
- each native store: on, off or absent, plus its retention or horizon;
- the clock offset between the source and the UTC reference;
- the *exact* native remedy text for anything denied.

The probe is the phase's first demo artifact. "Query Store OFF; DMVs since 2026-10-23 09:09; `sysjobs` denied" is a finding in its own right. Examples:

- **SQL:** `SERVERPROPERTY`, `sys.dm_os_sys_info.sqlserver_start_time`, `HAS_PERMS_BY_NAME(null,null,'VIEW SERVER STATE')`, `sys.database_query_store_options.actual_state_desc`, `sys.dm_xe_sessions` (is `system_health` running), file target path, `fn_xe_file_target_read_file` min/max `timestamp_utc`.
- **Mongo:** `buildInfo`, `connectionStatus` (roles), `getCmdLineOpts` (log path, `diagnosticDataCollectionEnabled` [unverified field name]), `getParameter` (`slowms` via `profile: -1` read), `replSetGetStatus` (if a replica set).

### 3.3 Time and clocks

- One internal clock: UTC, with display time zone selectable (default Asia/Dubai, persisted per user).
- Each source declares the time basis of its timestamps:
  - IIS W3C: UTC by default.
  - SQL error log: server local time.
  - `system_health`: `timestamp_utc` (SQL 2017+).
  - Event Log: UTC.
  - `mongod` log: ISO 8601 with offset.
  - Audit: UTC.
- The capability probe measures clock skew where possible:
  - SQL `SYSUTCDATETIME()`.
  - Mongo `serverStatus.localTime`.
  - Windows `Kernel-General` event 1 marks "system time changed" `[unverified ID]`.
- Correlation tolerances (P8) add the measured skew.
- **[Rev 2]** Every source also records its **time zone and DST rule**, not only an offset.
  - The SQL host here is UTC+1 (E22), so the error log and Agent schedules are in that local time.
  - Recurrence views can then be shown in either Dubai or source-local time.
  - A recurrence that is fixed in source-local time and shifts across a DST change is evidence of a host-local scheduler (ladder L4).

### 3.4 Safety envelope (every source)

- **Read-only by construction.**
  - A per-source allowlist of commands and query templates, with no user-typed SQL, Mongo or Redis commands.
  - SQL: `ApplicationIntent=ReadOnly`, `SET LOCK_TIMEOUT 5000`, `SET DEADLOCK_PRIORITY LOW`, `SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED` for DMV and `msdb` reads. This keeps the old Task 11 guard, but the allowlist replaces the fragile "starts with SELECT/WITH" check.
- **Bounded.**
  - Per-source concurrency of 1 by default and 2 maximum, following the semaphore pattern in `internal/store/store.go:25-60`.
  - 5-minute deadline, as in the MVP.
  - Row caps per query (default 5,000).
  - Byte cap of 16 MiB, as in the MVP.
  - Time window caps per source (§ per phase).
- **Evidence never destroyed.** The tool never resets counters (`DBCC SQLPERF(... CLEAR)`, `SLOWLOG RESET`, `LATENCY RESET`), never clears logs (`wevtutil cl`), never changes the profiler level, and never runs `CONFIG SET`.
- **Self-identification.** Set the application name on every client so a DBA can see and kill Unravis sessions:
  - Mongo `appName=Unravis`. It is not set today: `internal/store/mongo.go:24-29` does not call `SetAppName`.
  - SQL `Application Name=Unravis`.
  - Redis `CLIENT SETNAME unravis`. This is a connection-scoped attribute, not a config change.
- **Production-hurting operations register (§3.5).** Every phase cites it.

### 3.5 Production-hurting operations register (never run, or opt-in with an explicit warning)

| Source | Operation | Why it hurts | Unravis policy |
|---|---|---|---|
| Mongo | `setProfilingLevel` / `profile: 2` | Profiler "can degrade MongoDB performance"; level 2 records every operation [R1] | Never change; read `system.profile` only if already enabled |
| Mongo | `$currentOp` / `currentOp` with `allUsers` + `idleConnections` on a busy server, unfiltered | Large output; command docs truncated at 1 KB; the `currentOp` command is deprecated since 6.2 [R3] | `$currentOp` with `idleConnections:false`, a `$match` on `secs_running ≥ 1`, `$limit 50`; on demand only |
| Mongo | `validate`, `compact`, `dbHash`, `reIndex` | Locking or heavy I/O | Never |
| Mongo | Unpruned aggregates on `ApiAuditLog` | COLLSCAN of ~778k docs per query (E1); **Unravis's own current behaviour** | P1 `_id` range pruning is mandatory |
| SQL | Creating or altering XE sessions, `sp_configure` (e.g. `blocked process threshold`) | Server configuration change | Never by the tool; documented as a DBA remedy |
| SQL | Query Store `sp_query_store_remove_query` / `ALTER DATABASE … CLEAR` / changing QS settings | Destroys evidence or changes configuration | Never |
| SQL | `DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR)`, `DBCC FREEPROCCACHE`, `DROPCLEANBUFFERS` | Destroys evidence or causes a recompile storm | Never |
| SQL | `sys.dm_db_index_physical_stats` in `SAMPLED`/`DETAILED` mode (even `LIMITED` scans) | Heavy I/O on large tables | Not in the roadmap (old Task 14 fragmentation item removed, §19) |
| SQL | `sys.dm_exec_query_plan` across the whole plan cache | CPU on plan XML | At most 20 plans, on demand, per selected query |
| SQL | `fn_xe_file_target_read_file` over all files with no event filter | XML shredding of up to 1 GB on the server | Filter by `object_name` and `timestamp_utc` window; cap rows; window ≤ 24 h per call |
| Redis | `KEYS` | "Use extreme care … in production"; O(N) blocking [R5] | Never; `SCAN` only in a later opt-in big-key sample |
| Redis | `MONITOR` | One client can cut throughput by more than 50% [R6] | Never |
| Redis | `CONFIG SET latency-monitor-threshold`, `DEBUG *`, `SAVE`, `BGSAVE`, `FLUSH*`, `CLIENT KILL`, `SLOWLOG RESET`, `LATENCY RESET` | Configuration change, blocking, or evidence destruction | Never |
| Windows | `wevtutil cl`, enabling Task Scheduler history, creating a Data Collector Set | Evidence destruction or configuration change | Never by the tool; DCS and history enablement are DBA/ops remedies with a generated command |
| IIS | Any `appcmd set` / `recycle` | Configuration change or outage | Never |
| .NET | `dotnet-dump`, `dotnet-trace` with heavy providers, `procdump -ma` | Pauses or slows the process | Not in the roadmap; `dotnet-counters` only (P11b, conditional) |

### 3.6 Demo strategy (local tiers stay; production is now live-first) [Rev 2, revised]

**[Rev 2]** Production investigations use the live-first connectors (§3.7). The tiers below remain the **demo and test** strategy, plus the fallback for clients that refuse live access. There are no live credentials in this development environment, so each phase stays demoable through three tiers. Each phase lists which tiers it uses.

1. **Local real instances** (preferred: real native data, deterministic scripts).
   - Local MongoDB 7, SQL Server 2022 Developer, Redis (Docker Desktop or Memurai `[unverified]` on Windows), and IIS / IIS Express.
   - A seeded `ApiAuditLog` (generated) plus a scripted incident that produces *real* native evidence. Example: a SQL session holding `UPDATE … ; WAITFOR DELAY '00:01:15'` produces a genuine `system_health` lock-wait event over 30 s.
2. **Deterministic generators behind `--demo`.**
   - Extend `audit.NewDemoWithStaleWork` (`internal/audit/demo.go`, `activity_demo.go`; flag in `cmd/audit-explorer/main.go:47,81`) with per-source demo readers.
   - They reproduce the incident shapes from §1: a 14:45 recurrence, release clusters, a step on day N, and an audit-insert stall.
3. **Imported artifacts.** Offline files in their native format:
   - `.xel` from a local SQL instance;
   - `.evtx` exported with `wevtutil epl`;
   - IIS W3C and httperr logs;
   - `mongod.log`;
   - `relog`-converted `.blg` counter logs.
   - The production `sql/*.json` and `out_*.txt` extracts must **not** be committed as fixtures without an anonymisation review (they contain account and user identifiers). Prefer synthetic fixtures that reproduce their *shape*. **[Rev 2]** This is now a standing default, not an open decision.

### 3.7 Live-first access model [Rev 2, new; replaces revision 1's "offline-first"]

Live access is the main mode. Offline import is graceful degradation into **the same evidence model**. Unravis stays a local exe on the engineer's machine, serving on loopback only. Connectors are outbound clients; nothing is installed on production hosts.

**[Rev 3]** "Nothing is installed" remains the default. §3.9 adds an optional on-server **node** as a fourth channel, after JEA, RPC and import. It is allowed per host only under gate N. The hub's UI and MCP stay on loopback.

**Connectors and authentication**

| Source | Connector | Authentication (preferred → fallback) | Transport |
|---|---|---|---|
| SQL Server | `go-mssqldb` | Windows integrated (Kerberos via SSPI, the engineer's own identity; nothing stored) → SQL login | TLS (`encrypt=true`; server certificate validated by default; `TrustServerCertificate` only by explicit per-profile opt-in, with a warning) |
| MongoDB | existing driver | SCRAM-SHA-256 → x509 client certificate | TLS where the server offers it |
| Redis | `go-redis` with a command allowlist | ACL user (§1A.E) → legacy `AUTH` password | TLS (6380 on Azure) |
| Windows hosts | see the channel options below | Current Windows identity (Kerberos; NTLM only within the domain) → alternate credential from Credential Manager | Channel-dependent |
| Files (logs) | SMB read share, or a JEA file read | As for Windows hosts | SMB 3 encryption where enabled |
| App Insights | HTTPS KQL (Log Analytics query API `[unverified endpoint]`) | Entra ID interactive or device-code token for the engineer (no client secret stored) | 443 outbound |

**Remote Windows channels: options and trade-offs** (decision 6 picks per client)

| Option | Ports | Setup | Pros | Cons |
|---|---|---|---|---|
| **WinRM / PowerShell remoting + JEA** (recommended) | 5985 (HTTP, Kerberos-encrypted) / 5986 (HTTPS) | Ops creates a JEA endpoint once, with an allowlist of read cmdlets | One port. Kerberos. **The server enforces read-only** (JEA). A transcript is kept on the host. Covers events, counters, tasks and files | One-time ops work. Unravis shells out to the engineer's `powershell.exe` with fixed script templates (no new Go dependency), or uses a WinRM library `[licence unverified]` |
| Native RPC tools (`wevtutil /r:`, `typeperf -s`, `schtasks /s`) | 135 + dynamic RPC (49152–65535); PDH also uses Remote Registry over 445 `[unverified]` | Group membership only; Remote Registry service running for counters | No endpoint set-up. Signed system binaries | A wide firewall range. Remote Registry is often disabled. `schtasks /s` usually needs admin `[unverified]`. Read-only only by group rights |
| WMI / DCOM (`Win32_PerfFormattedData_*`, `Win32_NTLogEvent`) | 135 + dynamic RPC | DCOM and WMI namespace permissions | Rich classes | Fragile permissions. Slow. `Win32_NTLogEvent` is costly on large logs. **Not recommended** |
| SMB read share | 445 | Ops creates a read-only share (never `C$`) | Simple for log files | Files only; one share per host |
| Offline import | none | Ops runs Unravis-generated export commands or query packs | No standing access | Not live; ops effort per investigation |

**Per-source capability probe (extends §3.2)**

- **Every source:** reachability, round-trip time, authenticated identity, clock offset and zone, and each grant in §1A.E as ok / denied / absent, with the exact remedy text.
- **Redis:** `INFO server`, `ACL WHOAMI`, which allowlisted commands succeed, `slowlog-log-slower-than`, and whether the latency monitor is on.
- **Windows:** which channel works; group memberships; whether the TaskScheduler Operational log is enabled; Remote Registry state; whether the IIS/log folders are readable; `Process(*)` counter availability.
- **App Insights:** whether a token is obtained; `AppRequests | where TimeGenerated > ago(1d) | take 1`; sampling (`itemCount`); whether dependency SQL text is present; the `cloud_RoleName` and `cloud_RoleInstance` list, which **auto-fills the topology map** (decision 1).
- **Results:** each source ends up **live**, **import-only** or **absent**. The UI and the rules then use whatever is available, with no error states.

**Read-only guarantees (defence in depth)**

1. The server-side least privilege in §1A.E is the primary guarantee: the server refuses writes.
2. A fixed allowlist of query, command and script templates per source. There is no free-text SQL, Mongo, Redis or PowerShell.
3. Session settings: SQL `ApplicationIntent=ReadOnly`, `READ UNCOMMITTED`, `LOCK_TIMEOUT 5000`, `DEADLOCK_PRIORITY LOW`. Redis ACL `-@all`. JEA cmdlet allowlist.
4. A local **query journal**: every remote call is logged with time, source, template ID, duration and rows/bytes, but no parameters that might hold PII. It is exportable for the DBA.
5. Unit tests assert that every template is in the allowlist and that no write verbs appear.

**Timeouts and limits (defaults, tunable; extends §3.4)**

| Source | Connect | Per call | Concurrency | Caps |
|---|---|---|---|---|
| SQL | 10 s | 30 s (context) + `LOCK_TIMEOUT 5 s`; XE reads ≤ 24 h windows | 1 (max 2) | `TOP 5000`, 16 MiB |
| Mongo | 10 s | `maxTimeMS` 30 s (request views), 120 s per day-slice (trend) | the existing source slot | `$limit`, 16 MiB |
| Redis | 3 s | 2 s | 1 | `SLOWLOG GET 128`; watch ≤ 30 min at ≥ 10 s |
| Windows | 10 s | 60 s per command | 1 per host | Event queries windowed (≤ 24 h, ≤ 10k events); counter watch ≤ 30 min at ≥ 15 s; file copy ≤ 2 GB |
| App Insights | 10 s | 60 s | 1 | ≤ 24 h per KQL, `take` 5,000 |
| Samplers (S14, counter watch, Redis watch) | n/a | n/a | ≤ 1 sampler per source | ≤ 30 min per run (60 min with explicit confirmation); ≤ 5 runs per day; auto-stop at the deadline |

**Kill-switch and circuit breakers**

- A global **Stop all remote activity** control in the UI, plus `--offline` on the CLI. It cancels every context, closes connections and stops samplers.
- The kill-switch is also exposed as an MCP-safe evidence API state, so external clients see "offline".
- Per-source pause controls.
- A **DBA-side kill** is always possible: every connection carries `Application Name=Unravis`, `appName=Unravis` or `CLIENT SETNAME unravis`.
- **Automatic back-off.** A sampler pauses itself, and asks before resuming, when any of these happens:
  - the source's probe round-trip time reaches 3× its start-of-session baseline;
  - the sampled host CPU stays at 95% or more for 3 samples;
  - a sampler query exceeds 50% of its interval.
- **Investigation budget readout:** docs examined, rows and elapsed seconds per source.

**Offline import as graceful degradation**

- Every live reader has an importer that produces **the same `Observation` records**, with `origin: live|import`, `collected_at` and a provenance hash of the file. Findings and rules do not change.
- **Query packs.** Unravis exports the exact allowlisted templates as `.sql`, `.js` and `.ps1` files with fixed output formats (JSON/CSV). Ops can run them where live access is refused, and the importer reads the results. This formalises what `sql/*.sql` and `sql/mongo_*.js` did by hand.
- Native files are imported as before: `.evtx` (parsed with the engineer's `wevtutil`), W3C/httperr, `mongod.log`, `relog` CSV and `dotnet-counters` CSV.
- The capability panel shows "import (as of <time>)" so stale evidence is never mistaken for live evidence.

### 3.8 When live time-boxed collection and the 90-day window are justified [Rev 2, new]

**Time-boxed perf collector (decision 3).**

- **Needed: DB host, in the predicted burst windows.**
  - The windows are about 11:05–11:20, 13:05–13:20, 14:35–15:05, 18:30–18:45 and 19:00–19:15 Dubai, plus 06h. Capture 5 working days. Spanning a DST change is useful (L4 step 3).
  - Counters at 15 s: `Process(*)\% Processor Time`, `Process(*)\ID Process`, `Process(*)\IO Data Bytes/sec`, `Processor(_Total)\% Processor Time`, `System\Processor Queue Length`, `Memory\Available MBytes`, `PhysicalDisk(_Total)\Avg. Disk sec/Read|Write`. Size cap: 200 MB.
  - *Why:* 37/43 stalls sit in these bursts (E15). `system_health` can only say "not SQL", and may even be wrong about that (E19). No other native source keeps per-process CPU history (G3).
- **Needed only if the API host is separate (decision 1): the API host, in the same windows.**
  - Add `ASP.NET\Requests Queued`, `ASP.NET\Request Wait Time`, `ASP.NET Applications(__Total__)\Requests Executing`, `.NET CLR Memory(w3wp*)\% Time in GC` and `Process(w3wp*)`.
  - *Why:* this is the mechanism question (class 9/15), whether the API host queues or serializes during the burst.
- **Not needed for the chronic step.** Slow requests (≥ 2 s) are spread evenly across CPU bands (E18).
- **How.**
  - The first capture is an ops-created DCS from an Unravis-generated `logman` command. It runs unattended, so it does not depend on the engineer's machine being on.
  - After that, Unravis's live **counter watch** (PDH via the chosen channel) covers predicted windows on demand.
- **Live SQL request sampler (S14), same windows.** Every 5 s it reads `dm_exec_requests` joined to `dm_os_waiting_tasks` and `dm_exec_sessions` (head blocker, wait type, wait time, `program_name`, `host_name`), capped at 200 rows per sample. It is the native alternative to G7 for waits of 5–30 s. It runs only during a confirmed run (decision 5).

**90-day window (decision 5 of revision 1, now resolved).** Use it only for **daily rollups**. The justification is what the longer windows revealed that 7 days could not:

- **30 days.** It dates the 10-07/08 step against a clean baseline. A 7-day window opened after 10-15 shows "always slow".
- **60 days.** The 09-21 CPU step needs a pre-step baseline from mid-August. `system_health` holds 70 days here.
- **45 days or more.** The stalls of 09-07, 09-17 and 09-18 show that the acute problem **predates** the chronic step, so these are two problems, not one.
- **90 days.** It covers three payroll month-ends, to separate month-end load from incidents `[hypothesis: monthly payroll cycle]`.
- **What does not use 90 days.**
  - Minute-grain recurrence uses 14 days.
  - Request-level views keep 7 days.
  - The live sampler and counter watch are minutes long.

### 3.9 Hub and node topology: Unravis running on servers [Rev 3, new]

**The user's idea.** Run Unravis, or a slim read-only build of it, **on** the DB, API and IIS servers as a **node**. The investigator's instance acts as the **hub** and pulls from the nodes, instead of or alongside the §3.7 remote channels.

**Naming.** This document uses **hub** (the investigator's instance) and **node** (the on-server collector). It does not use "master/slave".

**The honest framing.**

- A node **is a deployed component**: a binary, a Windows service, a listening port and a certificate on a production host.
- That conflicts with the out-of-scope line in the header ("no agents or collectors installed by Unravis") and with the binding principle "existing evidence first".
- It is **not** app instrumentation: it reads only native evidence and changes nothing in brain-payroll.
- It is therefore classified as a **gated access mechanism (gate N)**, with the same discipline as the G backlog. It is not a default transport.

#### 3.9.1 Topologies compared on evidence

| Source / concern | (a) Central live remote (§3.7) | (b) Node on every server + hub | (c) Hybrid: node only where gated in | (d) Offline query packs |
|---|---|---|---|---|
| Per-process CPU for the burst problem (E15, G3) | Remote `Get-Counter` via JEA is fine for ≤ 30 min watches. Multi-day capture needs the engineer's machine on, so in practice it is **the ops-created DCS** (§3.8), which is native and needs no node | **Easier.** Continuous low-cost local sampling. **Triggered capture:** when total CPU ≥ 90%, take a top-N snapshot with PID → service or app-pool name and command-line hash. Ships only burst minutes. Maps `w3wp#2` to its pool | Same as (b), on the DB host only | DCS `.blg` → `relog` CSV import. Not triggered: the whole window is captured |
| Event Log, Task Scheduler | Good (JEA `Get-WinEvent`) | Marginal gain | Same as (a) | `.evtx` export |
| IIS/HTTPERR, log4net, `mongod.log` files | SMB or JEA. Whole files cross the network (GBs) | **Easier.** Parsed locally; only the window or aggregates are sent | Node where files are large or no share is allowed | Ops copies files |
| Redis, Mongo or SQL bound to localhost | **Impossible** unless ports are opened | **Possible.** A real advantage | Node only on those hosts | Query packs run locally by ops |
| `.xel` | Readable remotely through SQL (`fn_xe_file_target_read_file`); no file access needed | No gain | Same as (a) | Query pack |
| FTDC `diagnostic.data` | File copy (≈200 MB) | Local decode of the window only | Optional | Ops copy |
| ETW (kernel process, CLR) | Not practical | Possible, but needs admin and is heavy. **Out of scope**; only behind a G item | n/a | `.etl` captured by ops, then imported (Later) |
| Install and approval burden per client | Grants plus one JEA endpoint (ops, once) | **Highest.** A signed binary, a service, a certificate, a firewall rule, change approval **per host**, and an update process | Per host where gated in only | Lowest |
| Blast radius if compromised | The engineer's Kerberos identity reaches every host (wide, but JEA-limited per host) | Per-host identity (narrow per credential), but **N listening endpoints** (more attack surface) | Fewer endpoints than (b) | None standing |
| Typical failure modes | Firewall or Remote Registry refused; the engineer's machine asleep during the window; Kerberos double-hop | The node loads the host it is diagnosing; spool fills the disk; certificate expiry; version drift; AV quarantine of an unsigned exe; hung PDH or WMI counter providers `[unverified]` | As (b), on fewer hosts | Stale data; ops effort; human error |
| Fits "existing evidence first" | Yes | **No, as a default.** It is a collector | **Yes, if gated** | Yes |

**Conclusion.**

- Topology (b) as a default is not justified by the evidence.
- The main unexplained lead (E15/G3) can be closed **without** a node, by the ops-created DCS (§24 decision 3).
- A node earns its place only where a named question needs local-only evidence and the native or remote paths have failed for that client.
- **Recommendation: (c) hybrid under gate N, with (a) as the primary channel and (d) as the floor.**

#### 3.9.2 Gate N: when a node may be deployed (all four must hold)

1. **A named, active question needs evidence that is local-only or too heavy remotely.** Examples:
   - triggered per-process capture across many days (G3, once the DCS has failed or been refused, or when bursts are rare or irregular);
   - Redis, Mongo or SQL bound to localhost;
   - multi-GB log files with no share allowed.
2. **The alternatives have been tried for that client:** the §3.7 remote channel, the ops-created DCS, and query packs are refused, failed, or demonstrably insufficient. The reason is recorded in the investigation.
3. **Time-boxed by default.** The node gets an expiry date (default 14 days) and stops and disables itself afterwards. A permanent node is a separate client decision (§24 decision 9).
4. **The client approves the change**, including the host list, port, identity and data categories.

#### 3.9.3 Hub ↔ node security model

| Control | Design |
|---|---|
| Direction | **Pull only.** The hub requests; the node never initiates writes anywhere. The node keeps a bounded local spool (default ≤ 200 MB ring), so a sleeping hub loses nothing. **Spool-only mode (the default first step, §24 decision 8):** there is no listener at all. The hub collects signed spool batches through the existing JEA or share path, or ops hands them over. |
| What a node will run | **Only allowlisted query-pack IDs** from a versioned manifest that is **hash-pinned and signed** (Authenticode on the binary; manifest signed with the release key `[design; key custody is §24 decision 10]`). No arbitrary SQL, PowerShell, commands or file paths. Pack parameters are typed (time window, top-N, threshold) and range-checked. |
| Authentication | **Mutual TLS** (TLS 1.2+). The hub has a client certificate; each node has a server certificate pinned by the hub. Fallback: a per-node bearer token over TLS, held with DPAPI (machine scope on the node, CurrentUser on the hub). |
| Authorisation | Per-node allowlist of hub certificate thumbprints. Per-pack roles (e.g. `read.host`, `read.sql`). |
| Bind address | The MVP binds `127.0.0.1` only (`cmd/audit-explorer/main.go:96`, the `tcp4` listener). **That stays true for the hub's UI and MCP.** Node mode adds a **separate listener** on one configured interface (never `0.0.0.0` by default) and port (default 8766, tunable), plus a Windows Firewall rule scoped to the hub's address. The `allowedHost`/`allowedOrigin` checks in `internal/server/security.go` stay on the UI listener; the node listener uses mTLS instead. |
| Credentials | **Never leave the node.** The node runs as a virtual account (`NT SERVICE\UnravisNode`) or a gMSA and uses integrated auth to local SQL, so no password is stored. Mongo and Redis secrets, where unavoidable, are held with DPAPI machine scope. The hub never receives connection strings. |
| Resource caps | CPU ≤ 2% average and ≤ 5% peak (job object or self-measured), memory ≤ 100 MB, spool ≤ 200 MB, ≤ 1 concurrent pack, outbound response ≤ 16 MiB. |
| Self-throttle | When host CPU ≥ 90% (the very condition being studied), the node does only the triggered snapshot (one `Process(*)` read every 15 s) and defers parsing and serving until CPU < 70%. Its own CPU is reported in every response, so the hub can prove it was not the cause. |
| Audit log | Every call is recorded with time, caller thumbprint, pack ID and version, parameters, duration and bytes. It is kept locally (rolling) and is retrievable by the client's ops. |
| Kill-switch | Hub-side: the §3.7 "Stop all" also tells nodes to stop collecting. Node-side: `unravis-node stop`, stopping the service, or deleting the firewall rule. Captures auto-expire. The node disables itself at its expiry date. |
| Data minimisation | Pseudonymisation runs **at the node** (keyed HMAC, the same scheme as P13) for users, accounts, IPs and command lines. SQL text is masked. Raw values never leave the host by default. |
| Evidence model | The node emits **the same `Observation` records** as live remote and import, tagged `origin: node` plus `node_id`, `node_version`, `pack_id@version` and `clock_offset_ms`. The hub code paths are shared. The same query-pack registry drives three executors: remote live, node, and offline pack. |

#### 3.9.4 What a node would do (minimum viable)

| Phase | Node collectors (allowlisted packs) | Stays hub-only |
|---|---|---|
| P6 | **Triggered per-process CPU sampler** (W7). Counters at 15 s (the §3.8 list). PID → service or app-pool map. Event Log reads (same ID allowlist). `schtasks` triggers. `bin`/config write times (W9). | Rules, correlation, UI |
| P3 | Local SQL reads with integrated auth: the same templates (CPU split, `system_health` slices, S14 sampler when confirmed) | Everything else in P3 (remote SQL works well) |
| P7 | Local W3C/httperr parse; window extract or per-minute aggregates; the audit join stays on the hub | Join, rules |
| P10 | Only if Redis is bound to localhost: the same allowlisted commands | Otherwise remote |
| P11 | Framework counters (P11a) locally. `dotnet-counters` stays out (it is a tool install) | |
| P12 | Local log4net window extract | Pattern rules |
| P1/P2/P4/P5/P8/P9/P13 | None. Mongo audit, Hangfire, App Insights and correlation are remote or cloud and do not need a node | All |

**Smallest first node release: N1 "burst catcher".** It pays for itself and can be demoed on its own.

- It runs on the DB host. Its scope:
  - the triggered per-process CPU sampler with PID → service mapping;
  - the §3.8 counters;
  - Event Log + Task Scheduler + change markers;
  - the spool, collected spool-only first (§24 decision 8);
  - mTLS pull, added only where live pull is needed.
- It directly targets the main unexplained lead (E15/G3).
- **Placement: 4b, immediately after P6, but built only when gate N is met.** Typical triggers: the DCS is refused or failed, bursts turn out irregular, or ≥ 2 clients refuse WinRM and RPC.
- Otherwise it slides to after P8.
- **P6 does not become node-based.** P6 ships with remote (JEA/RPC) and import paths. N1 adds a third executor for the same packs.
- **Demo:**
  - the node runs as a service on the engineer's own machine, and the hub pulls over mTLS on a second loopback port;
  - a scheduled CPU burner fires;
  - the node catches the burst, names `burner.exe` with its service or task, and reports its own CPU of about 0.5%;
  - the hub shows W7 → C9.

**Clients that cannot install anything** (the fallback chain): remote live (JEA → RPC) → ops-created DCS + query packs → preserved native exports → audit-only (P1/P2), with findings labelled "host evidence absent". Every phase already degrades this way (§3.7).

**App Insights (about 50% of clients).** The node is orthogonal to it:

- **Where App Insights is live:** it gives request-level timing and the node gives host-level process evidence. Together they can reach "proven for a sampled request" (AI2) next to a named process (W7).
- **Where it is absent:** node + P7 is the closest request-adjacent evidence. C-rules still cap at strong correlation (G1 is unchanged).

#### 3.9.5 Codebase impact (Go)

- **Reusable:**
  - Mongo connector and sanitized errors (`internal/store/mongo.go`, `connections.go`);
  - DPAPI (`internal/history/platform_windows.go`);
  - the §3.1 evidence model;
  - the §3.7 query-template allowlist, which becomes the shared pack registry;
  - HTTP routing patterns (`internal/server/server.go`);
  - MCP and server stay hub-only.
- **New packages (suggested):**
  - `internal/packs`: the registry, a hash-pinned manifest and three executors;
  - `internal/node`: the collector scheduler, triggered sampler, spool, self-throttle and expiry;
  - `internal/transport`: mTLS server and client, certificate pinning, handshake (version, pack manifest hash, clock).
- **Entry point:**
  - Option 1: a `--node` flag. `cmd/audit-explorer/main.go` already uses a `FlagSet` with `--demo`, `--port`, `--no-browser` and `--mcp`.
  - Option 2, **recommended**: a separate build target `cmd/unravis-node` from the same repository. It links only the node packages, giving a smaller attack surface and an easier security review.
- **Windows service.** Install and uninstall subcommands use the service control APIs (e.g. `golang.org/x/sys/windows/svc`, BSD-3 `[licence to verify]`; a new direct dependency).
  - `install`: registers the service under a virtual account, creates the firewall rule scoped to the hub, enrols the certificate, writes the expiry date.
  - `uninstall`: removes the service, firewall rule, spool, certificate and config, leaving nothing behind.
  - PDH can be read by shelling out to `typeperf` (no new dependency), or by direct `pdh.dll` calls (new code).
- **Footprint target:** a single exe of about 15–25 MB `[estimate]`, ≤ 100 MB RSS, ≤ 2% CPU.
- **Updates and versions:**
  - no auto-update; ops redeploys a signed binary;
  - the handshake reports node version and manifest hash;
  - the hub refuses packs the node does not have and supports node versions N and N−1.
- **Clock skew** (this matters for P8):
  - on every pull, the hub estimates the offset Cristian-style: offset = node_time − (t_send + RTT/2);
  - the node also reports `w32tm /query /status` source and offset `[unverified output parse]`;
  - every observation carries `clock_offset_ms`, and P8 widens its match tolerance by |offset| + RTT/2;
  - offsets above 2 s raise W8-style data-quality warnings (§3.3).

#### 3.9.5a Relationship to host auto-detect (P0) [Rev 4]

- **P0 is not a node.** `unravis detect` (§3A) is a one-shot, read-only run of the main exe with no service, listener or persistence, so it does not need gate N.
- **The node builds on P0.**
  - A node install needs a **confirmed host profile**. Its packs are taken only from the profile's accepted proposals.
  - The node re-runs the same detect probes daily into its spool, so drift (HP1) is tracked for as long as the node lives.
- **Gate N criterion 1 is often evidenced by P0.** Examples: a Redis bind of `127.0.0.1` in `redis.conf`, or IIS log folders of many GB with no share. The profile is attached to the gate N record.
- **[Rev 5] Hints count as confirmation input.** A node's confirmed profile may be built from hints, detection or both. Its packs read only the confirmed locations, hinted ones included. The daily re-detect re-validates the hinted paths and raises HP6 when they diverge. On a locked-down host, a node can run hints-only (`--no-detect`), which keeps its read ACLs limited to the hinted folders.

#### 3.9.6 Recommendation

- **Adopt only for named sources, behind gate N. Defer the build** until the ops-created DCS result is known.
- Named sources:
  - triggered per-process CPU capture (N1);
  - services bound to localhost;
  - large local log files.
- Remote live (§3.7) stays the primary channel, and offline packs stay the floor.
- The header's out-of-scope line is amended in place: "no agents or collectors **by default**; an on-server node only under gate N (§3.9)".
- The node-related decisions are in §24 (decisions 7–10, resolved in Rev 4).

### 3.10 Optional LLM assistance: proposals only, never verdicts [Rev 6, new]

**Stance.** Unravis must be fully useful with no LLM at all. The build contains **no LLM client**. The only LLM path is an **external** AI client, such as Claude Code, talking to the existing MCP server. This keeps the header's "no AI diagnosis inside the tool" principle and the §19 verdict "SpikeHunter LLM loop: do not adopt". An in-tool LLM slot is decision 14 (§24); the default is no.

**Where an LLM may sit (all via MCP, all as proposals):**

| Role | Input it sees | Output | How the engine treats it |
|---|---|---|---|
| Suggest candidate theories | Findings, capability rows and the catalog (pseudonymised, under the 1 MiB cap) | `theory_propose`: a catalog template ID (TH1–TH19, §5A.D) plus parameters | Validated against the template schema and the allowed parameter ranges. Stored with `provenance: llm-proposal` and status `proposed`. Thresholds looser than the defaults are refused, unless a human confirms them, which bumps the version |
| Translate a plain-English question into a template | The question plus the catalog | Template ID plus parameters, from a **closed** catalog | Shown to the user, who confirms before it runs. Text that does not map to a template returns "no template matches". Free-form tests are never run |
| Draft a narrative | The verdict JSON (pseudonymised) | Prose in which **every number is a reference token**, for example `{{TH4.tests[0].effect}}`. The engine resolves the tokens | A deterministic **narrative linter** rejects any digit outside a token, and any status or label word that disagrees with the verdict. The rendered text is marked "LLM-drafted" |

**Where an LLM may not sit:**

- deciding or changing a status or label;
- computing any number;
- choosing a baseline or a window that the engine did not derive;
- seeing raw identifiers, payloads, SQL text or secrets (MCP serves pseudonymised, masked data only; §3.4, P13);
- raw query passthrough (already out of scope in P13);
- accepting its own proposals.

**Invariant.** The verdict for a theory is a pure function of the theory version, the input fingerprint, the rule versions and the seed. **Provenance never enters that function.** An LLM-proposed theory and the same theory typed by a human get byte-identical verdicts.

**MCP surface.** Four theory tools are added next to the existing 10 audit tools. They ship in P2b, because the audit MCP already exists (the §16 per-phase note). P13 folds them into the versioned evidence schema:

- `theory_catalog`: list the templates and their parameter ranges;
- `theory_propose`: validate and store a proposal;
- `theory_run`: run accepted theories. A proposed theory runs as a dry run whose verdict is marked `provisional`, and it enters the ledger only when a human accepts it;
- `theory_verdicts`: read verdicts and test results.

All four are read-only against production: they compute over cached findings. With P13's 4 evidence tools, the total is 18 tools.

**[Rev 7]** A fifth theory tool, `theory_narrative_submit`, stores an external, linter-checked wording draft for one theory card (§5A.H). It writes only to the local draft store and never touches a verdict. `theory_verdicts` gains `view=cards`, which returns the theories screen's own view model. The total becomes **19 tools**.

**Guide.** The MCP evidence-first guide (`internal/mcp/schema.go:5`) gains a rule: "Verdicts come only from `theory_verdicts`. Quote the status and label verbatim. Never upgrade a label. Propose; do not conclude."

---

## 3A. Phase P0: Host auto-detect and host profile (`unravis detect`) [Rev 4, new]

**Purpose.** The user's requirement: "if I install this utility directly on the server, there should be a way to just say 'auto-detect'."

- Run once on a server, P0 discovers **locally and read-only** what runs there, where its evidence lives, and what Unravis could collect.
- It writes the result to a reviewable, editable **host profile**. The profile is the machine-filled version of decision 1's host map (§24).
- **Nothing is enabled until a person confirms it.** P0 describes the host. It does not connect to anything, collect anything or change anything.
- **[Rev 5] Two peer inputs, one profile.** Auto-detect finds things. **Manual hints** (`unravis-hints.json`, CLI flags or a UI form) let dev or DevOps state exact locations and facts directly. Either one alone, or both together, produces the same profile (see "Manual hints" below).

**Questions answered:**

- What is this host: workstation or server, which OS build, which .NET?
- Which roles does it play (IIS/API, SQL, Mongo, Redis, the app's Windows Services, monitoring or backup agents), and on what evidence?
- Where does each source's evidence live (log folders, FTDC, `.xel`, HTTPERR, log4net files), and how far back does it go?
- Which other hosts does the app point at (connection-string targets)? That gives the cross-host topology.
- Which Unravis connectors and packs make sense here, and which rights are missing?

**Invocation.**

- `unravis detect` is a subcommand of the **main exe**. `--auto` is accepted as an alias.
  - Flags: `--out <dir>`, `--format json|md|both` (default both), `--diff <previous.json>`, `--accept <proposal-ids>`. **[Rev 5]** Plus `--hints <file>`, `--no-detect` and the per-field hint flags (see "Manual hints").
  - `cmd/audit-explorer/main.go` today parses one `FlagSet` (`--demo`, `--port`, `--no-browser`, `--mcp`). Subcommand dispatch is added before that parse.
- In the UI, a "Detect this host" action appears when the hub runs on Windows. It runs the same probes and opens the profile review page.
- The same probe set runs inside a node (§3.9) and later through JEA as a subset (§24 decision 11).

**What it discovers.** Every probe is local and read-only, and each one declares the right it needs.

| Area | How (read-only, no WMI or registry writes, no `appcmd`) | Recorded | Needs |
|---|---|---|---|
| Host basics | Registry `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion` (`ProductName`, `CurrentBuild`, `InstallationType` Server/Client); CPU count, RAM, last boot, domain membership, network interfaces and IPs | OS, edition, server vs workstation, uptime | Users |
| .NET | `HKLM\SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full` `Release` (maps to 4.x); a directory listing of `%ProgramFiles%\dotnet\shared\Microsoft.NETCore.App` and `Microsoft.AspNetCore.App` | Framework release; Core/ASP.NET runtimes (P11a vs P11b) | Users |
| IIS | Parse `%windir%\System32\inetsrv\config\applicationHost.config` (stdlib XML), and detect shared configuration via `redirection.config`. Sites, bindings, physical paths, applications. App pools: CLR version, pipeline, identity **type and name only**, `startMode`, `idleTimeout`, **`periodicRestart` time and schedule**. Per-site `logFile` directory and format. HTTPERR folder from `HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters\ErrorLoggingDir` (default `System32\LogFiles\HTTPERR`) | Sites → pools → paths → log folders; **scheduled recycle times** (checked against B13 windows, rule HP2); cold-start settings (G8 remedy, M9) | **Administrators by default** `[unverified ACL per build]`. Fallback: W3SVC/WAS service state plus default log folder presence, marked "partial" |
| Windows services | Service Control Manager enumeration and per-service configuration (`golang.org/x/sys/windows/svc/mgr`) | Name, display name, state, start type, binary path (arguments redacted, see below), run-as account | Users for enumeration; some services deny config reads to non-admins `[unverified]` |
| SQL Server | Registry `...\Microsoft SQL Server\Instance Names\SQL` → per-instance `Setup` (`Version`, `Edition`, `PatchLevel`), `MSSQLServer\SuperSocketNetLib\Tcp\IPAll` (`TcpPort`, `TcpDynamicPorts`), startup `Parameters` (`-e` error-log path → the log folder that also holds `system_health` `.xel`); services `MSSQLSERVER` / `MSSQL$<name>`, `SQLSERVERAGENT`, `SQLBrowser`; the TCP listening table mapped to PID and service. SQL Browser is **not** queried by default: the registry is enough, and P0 makes no network calls | Instances, version and CU, ports, Agent present, `.xel` and error-log folders; FCI/AG hints (cluster service present) | Users for registry reads `[unverified]`; the listening table needs Users, but mapping a port to its owning process may need admin |
| MongoDB | A service whose binary is `mongod.exe`; the `--config` path from its arguments; read `mongod.cfg` keys `net.port`, `net.bindIp`, `systemLog.path`, `storage.dbPath` (FTDC = `dbPath\diagnostic.data`, with size and oldest file time), `replication.replSetName`, and whether `security.authorization` is on or off | Port, bind address (localhost-only flags node relevance, §3.9), log and FTDC paths and retention, replica set | Read ACL on the config and data folders (often admin or service account only) |
| Redis | A service whose binary is `redis-server.exe` or Memurai `[unverified binary names]`; the config path from its arguments; `redis.conf` keys `port`, `bind`, `protected-mode`, `logfile`, `dir`, `maxmemory`, `save`, `appendonly` | Port, bind address, persistence settings (fork risk, P10) | Read ACL on the config |
| App config, log4net | For each IIS site's physical path and each app service's binary folder: `web.config`, `*.exe.config`, `log4net.config`, `appsettings*.json`. Only targeted elements are read: the `<log4net>` section or file reference (appender type, `file`, rolling style, **conversion pattern**); `<connectionStrings>` (provider, host and port, database, auth mode); assembly file versions of the app's main DLLs and Hangfire (`Hangfire.*.dll` present tells P5 which DB holds the schema) | Log file locations and patterns (P12), API → SQL/Mongo/Redis edges (topology), versions (drift markers) | Read on site and service folders |
| App Insights | `ApplicationInsights.config` or `appsettings*.json` `ApplicationInsights` section; SDK version from `Microsoft.ApplicationInsights.dll` file version | **Present or absent, SDK version, and a key fingerprint** (first 8 hex of SHA-256 of the instrumentation key or connection string). The key itself is never recorded | Read on the site folder |
| Perf counters | `typeperf -q` for the §3.8 objects (Process, Processor, Memory, PhysicalDisk, ASP.NET, .NET CLR Memory, `SQLServer:*` / `MSSQL$<name>:*`) | Available, missing or disabled counters (W7, P11a feasibility) | Performance Monitor Users |
| Event Log | `wevtutil el` / `gl` for System, Application, TaskScheduler Operational, WAS, IIS-Configuration and Defender channels; the oldest record time via one-record reads | Enabled, max size, **retention days** (preservation urgency, §0) | Event Log Readers |
| Scheduled work | `schtasks /query /fo csv /v` | Tasks with trigger times (W3; checked against the B13 windows) | A non-admin run sees a subset `[unverified]` |
| Time | `time.Local` and the registry `TimeZoneInformation` (zone, DST flag); `w32tm /query /status` (source, last sync, offset) | Timezone and DST per host (P8, the UTC+1 SQL clock caveat); NTP health | `w32tm` may need admin `[unverified]` |
| Spool and disk | Free space per fixed volume | Whether a 200 MB spool or a DCS fits | Users |
| Existing collectors | `logman query` (data collector sets and output paths); `*.blg` files under `%SystemDrive%\PerfLogs` | **Existing per-process history may already exist** (it would answer G3 at no cost) | Performance Log Users or admin `[unverified]` |
| Third-party agents | Service and binary names matched against a **versioned signature list** shipped in the pack manifest. Examples (names `[unverified per vendor version]`): SQL monitors (Redgate SQL Monitor, SolarWinds DPA, SQL Sentry, Idera, Quest Spotlight); APM and infrastructure agents (Datadog, New Relic, Dynatrace, Zabbix, PRTG, SCOM/Azure Monitor `HealthService`, Splunk UF, Elastic Beats); backup (Veeam, Commvault, MARS); AV/EDR (Defender, CrowdStrike, SentinelOne, Sophos, Trellix, Symantec) | **Candidate burst sources** (E21, HP3) and **existing evidence stores** (a monitor may already hold per-process CPU history) | Users |

**What it never does.**

- No connection to SQL, Mongo, Redis or App Insights. Connection tests are the §3.7 probe, run later and only after confirmation.
- No credential guessing, default-password tries or brute force.
- No port or network scanning. Only the local listening table is read.
- No decrypting of protected config sections (`configProtectionProvider`). They are reported as "encrypted, not read", even when the token could decrypt them.
- No reading of LSASS, credential stores, browser data or user profiles.
- No self-elevation. A non-elevated run says what to rerun elevated.
- No writes outside the output folder. No registry, service, firewall or IIS changes.
- **Footprint:** below-normal process priority, a total time budget of 60 s (default), ≤ 5 MB per config file, bounded directory depth, sequential reads.

**Secret handling (redaction at parse time; secrets never reach the output).**

- **Connection strings.** Keep only provider, host, port, database, auth mode (integrated or SQL), and options such as `Application Name`. `Password`/`Pwd` become `***`. `User ID` becomes `***` too, by default (§24 decision 12). For `mongodb://` URIs the userinfo is dropped, and the `password=` part of Redis strings becomes `***`.
- **appSettings.** Not copied at all, except an allowlist of non-secret keys `[allowlist to define]`. Any key matching `pass|pwd|secret|key|token|conn|sas` is redacted even when allowlisted.
- **Server config files.** In `redis.conf`, `requirepass`, `masterauth` and `user … >secret` are redacted. In `mongod.cfg`, `security.keyFile` is recorded as a path only (never read), and `net.tls.*Password` is redacted.
- **Command-line arguments** in service binary paths that look like credentials (`-p`, `--password`, `pwd=`) are redacted.
- **Self-check before writing.** The profile is scanned for known secret shapes (password-key patterns, URI userinfo, Azure key formats, long high-entropy strings). A hit blanks the value and raises a warning.
- **Storage.** The output file ACL is limited to the running user. Secrets are never logged.

**Least privilege.**

- Every fact carries `visibility: seen | partial | denied`. A `denied` or `partial` fact also carries the missing right, for example "local Administrators, or a read ACL on `inetsrv\config`".
- The report starts with a **visibility summary**: what was seen and what was not, with the exact right to request. This is a coverage count, not a health score.
- **Read-only equivalents of admin:**
  - Event Log Readers;
  - Performance Monitor Users / Performance Log Users;
  - an ops-granted read ACL on `inetsrv\config` and on the app and data folders.
- Without these, an elevated one-shot run by ops is the default (§24 decision 11).

**Output: the host profile.**

- **`host-profile.json`** (schema `unravis.hostprofile/1`), with these parts:
  - `host`: identity, OS, timezone, uptime;
  - `roles[]`: each role with its evidence pointers, e.g. "sql: service `MSSQLSERVER` running + `IPAll` port 1433 + registry instance `MSSQL16.MSSQLSERVER`";
  - `sources[]`: path or endpoint, retention, `visibility`;
  - `edges[]`: API → DB/Mongo/Redis targets from configs;
  - `agents[]`;
  - `clock`;
  - `proposals[]`;
  - `overrides`;
  - `generated_at`, `tool_version`, and a content hash.
- Every fact carries `origin` (registry path, file and element, or command) and `confidence: observed | inferred`.
- **`host-profile.md`.** The human report: visibility summary, roles, evidence locations with retention ("FTDC: 6.5 days left at the current rate"), topology edges, candidate burst sources, proposed packs, and the rights to request.
- **Editable.** A person corrects roles, marks a service as "the app" or "not relevant", and adds notes. Edits go in `overrides`, so a re-detect never overwrites them.
- **Proposals, all `enabled: false`.** Each one names a connector or pack with its reason, e.g.:
  - "S-pack CPU split: SQL instance found";
  - "P7 IIS logs: `W3SVC2` folder, 31 days";
  - "API-host counters: IIS and SQL are on different hosts";
  - "node not proposed: no gate N condition".
- Proposals are confirmed in the UI or with `--accept`. Until then nothing runs.

**How the hub consumes profiles.**

- **Getting them in.** Profiles are imported by file, from a node spool, or from the in-UI detect. They are grouped per client into a **topology**: hosts, then roles, then sources, then edges.
- **Matching edges.** Edges are matched to hosts by name or IP from each profile's own interface list. Nothing is resolved by scanning.
- **What the topology feeds:**
  - decision 1's host map, pre-filled for ops review;
  - the §3.7 connector proposals and ports, and the grants checklist rows that apply to this client;
  - B13 and S4 interpretation (same host or separate hosts);
  - P7 and P12 paths and patterns, P4 FTDC and log paths, P5's Hangfire database;
  - the P9 per-client capability. The AI key fingerprint can be matched to a resource the engineer can read, without the key ever leaving the server;
  - P8 per-host clock and timezone, which flags a mixed-timezone fleet (rule HP4).
- **Missing evidence.** Remedies cite the profile, e.g. "TaskScheduler Operational disabled on db01: enable it (ops) or rely on `schtasks` triggers".

**Re-detect and drift.**

- Each profile is timestamped and hashed. `--diff` (or the hub's drift view) compares it with the previous one.
- Compared items:
  - services added, removed or changed;
  - app pool settings;
  - app DLL file versions and config write times;
  - SQL version and CU;
  - ports and bind addresses;
  - new agents or AV;
  - Event Log retention and timezone.
- Each change becomes an `Observation` (`origin: detect`, rule **HP1 config drift**). It feeds W9 and C10 (change explains the chronic step). This is a cheap partial answer to the missing 10-07/08 deploy marker, if an earlier profile exists.
- The hub prompts for a re-detect when a profile is older than 7 days (default). A node (gate N only) re-runs detect daily into its spool. Without a node there is **no standing re-detect**: detect is always one-shot.

**On a dev laptop, or with nothing to detect.**

- `InstallationType = Client` and no server roles found: P0 prints a clear message and exits with code 0. Example: "This looks like a workstation (Windows 11 Pro). No IIS, SQL Server, MongoDB or Redis services found. Nothing to profile. Run `unravis detect` on a server, or use `--demo`."
- Local developer instances found (IIS Express, SQL Express, a local `mongod`): the profile is written but stamped `environment: workstation`. The hub will not merge it into a client topology without an explicit override.
- On a non-Windows OS: "detect is Windows-only". It follows the existing `platform_windows.go` / `platform_other.go` split.

**Manual hints: a first-class peer of auto-detect [Rev 5, new].**

The user's requirement: support both auto-detect and, "if they can find it and the developer/DevOps person can give the location (or similar) directly", let them state it. Hints are not a fallback. A DevOps engineer who already knows where things live should not have to wait for a scan, run elevated, or argue with a wrong guess.

- **Three equivalent inputs, one hints object.**
  - A reviewable file, **`unravis-hints.json`** (schema `unravis.hints/1`, strict: unknown keys are rejected, so a typo never silently does nothing, which is the E3 lesson).
  - **CLI flags** with the same fields (below).
  - A **UI form**, "Tell Unravis where things are", per host. It validates each path inline and saves to the same hints file, so a form entry is as reviewable as a hand-written file.
  - Flags, file and form produce the identical merged hints object. Flags override the file field by field. `--write-hints <file>` saves the merged result for review.
- **Modes.**
  - `unravis detect`: auto-detect only (as above).
  - `unravis detect --hints unravis-hints.json`: hints plus detect for every field not hinted. **Partial hints are normal.**
  - `unravis detect --hints … --no-detect`: **hints only**, for locked-down hosts. Only the hinted locations are touched. Unhinted facts are recorded as `visibility: not-probed`, never guessed.
  - **Hub side, for remote hosts:** `unravis profile --from-hints unravis-hints.json` (or the UI form on the hub) builds a profile for hosts the engineer cannot run anything on. Its facts are `visibility: unvalidated` until the first §3.7 probe, JEA subset run, query pack or node confirms them. A pack generated from such a profile carries the hinted paths, so ops' pack run reads exactly those paths and discovers nothing else.
- **Precedence: hint > detected > default.** It is never silent. When a hint and a detection disagree on the same field, the profile keeps both values, uses the hint, and raises **HP6 hint/detect conflict** for a person to resolve.
- **Per-field provenance.** Every fact carries `provenance: hinted | detected | both-agree | conflict | default`. A `default` example is the standard HTTPERR folder. The report's visibility summary also counts facts per provenance. That is a coverage count, not a score.
- **Validation (on the host where the path lives).**
  - Each hinted path must exist, be readable by the running account, and be of the declared type (file or folder). A failure records `visibility: denied | missing` with the reason, and the hint is kept as stated. It is never replaced by a guess.
  - **Probe scope.** When a field is hinted, only that location is read for that field: no wider search. With `--no-detect`, nothing else is read at all.
  - Instances and ports are checked syntactically only. P0 still makes no connection. With detect on, a listening-table match is recorded as corroboration.
  - **No secrets in the hints file.** The file goes through the same secret-shape scanner as the profile (§3A secret handling). Any hit **rejects the whole file**: the tool exits non-zero and names the key, but never echoes the value. Connection details are given **by reference only**, as `connection_ref: "dpapi:<profile-name>"`, which points at a DPAPI-protected connection profile on the hub (the existing `internal/store/profiles.go` pattern). Inline connection strings, user IDs and passwords are rejected.
- **The same output and the same guarantees.** Hints merge into the same `host-profile.json` (`unravis.hostprofile/1` gains `provenance` per fact and the hints file's hash) and the same `host-profile.md`. "What it never does", redaction, footprint and "nothing enabled until confirmed" apply unchanged. A hint can mark a proposal as *requested*, but it still stays `enabled: false` until confirmed.
- **Fewer rights needed.** A hints-only run needs read access to the hinted paths and nothing else: no elevation, no service-enumeration rights, no `inetsrv\config` ACL unless that path is hinted (grants checklist, §1A.E). Giving `remote_hosts` also means P0 never has to open `<connectionStrings>` to draw topology edges.

**Example `unravis-hints.json`** (fictional names; it holds no secret, and connections are by DPAPI reference):

```json
{
  "schema": "unravis.hints/1",
  "client": "client-a",
  "hosts": [
    {
      "name": "web01",
      "roles": ["api", "redis"],
      "timezone": "Arabian Standard Time",
      "iis": {
        "config_path": "C:\\Windows\\System32\\inetsrv\\config\\applicationHost.config",
        "sites": [{ "name": "BrainPayrollApi", "log_dir": "D:\\IISLogs\\W3SVC2" }],
        "app_pools": ["BrainPayrollApiPool"],
        "httperr_dir": "C:\\Windows\\System32\\LogFiles\\HTTPERR"
      },
      "redis": { "conf": "C:\\Program Files\\Memurai\\memurai.conf", "port": 6379 },
      "log4net": ["D:\\Apps\\BrainServices\\logs\\service.log"],
      "app_insights": { "app_id": "00000000-0000-0000-0000-000000000000" },
      "perf_logs": ["C:\\PerfLogs\\Admin\\UnravisBurst"],
      "remote_hosts": { "sql": "db01", "mongo": "mongo01" }
    },
    {
      "name": "db01",
      "roles": ["sql"],
      "timezone": "W. Europe Standard Time",
      "sql": {
        "instance": "db01\\PROD",
        "port": 1433,
        "databases": ["Brain_ClientA"],
        "errorlog_dir": "E:\\MSSQL\\Log",
        "connection_ref": "dpapi:clientA-sql-readonly"
      }
    },
    {
      "name": "mongo01",
      "roles": ["mongo"],
      "mongo": {
        "cfg": "C:\\Program Files\\MongoDB\\Server\\7.0\\bin\\mongod.cfg",
        "log": "E:\\MongoDB\\log\\mongod.log",
        "ftdc": "E:\\MongoDB\\data\\diagnostic.data",
        "port": 27017
      }
    }
  ]
}
```

**CLI flags (same fields):** `--hints <file>`, `--no-detect`, `--write-hints <file>`, `--role api,sql,mongo,redis`, `--timezone <id>`, `--iis-config <path>`, `--iis-site <name>=<log_dir>` (repeatable), `--app-pool <name>` (repeatable), `--httperr-dir <path>`, `--sql-instance <server\inst>`, `--sql-port <n>`, `--sql-db <name>` (repeatable), `--sql-errorlog-dir <path>`, `--sql-conn-ref dpapi:<name>`, `--mongo-cfg|--mongo-log|--mongo-ftdc <path>`, `--mongo-port <n>`, `--redis-conf <path>`, `--redis-port <n>`, `--log4net <path>` (repeatable), `--ai-app-id <id>` or `--ai-resource-id <id>`, `--perf-logs <folder>` (repeatable), `--remote <role>=<host>` (repeatable).

**What each hint unlocks.**

| Hint | Unlocks | Right then needed |
|---|---|---|
| `roles` | Topology, which proposals appear, same-host vs separate-host reading of B13/S4 | None |
| `iis.config_path`, `sites[].name`, `app_pools` | Site → pool map; recycle schedule (HP2); `startMode`/`idleTimeout` (M9, G8 remedy) | Read on that one file (admin only if its default ACL is kept) |
| `iis.sites[].log_dir`, `httperr_dir` | P7 import (I-rules); retention (HP5) | Read on those folders |
| `sql.instance`, `port`, `databases` | §3.7 SQL connector target; P3/P5 templates; HP4 with the host timezone | None on the host; the SQL grants are unchanged (§1A.E) |
| `sql.errorlog_dir` | `system_health` `.xel` and error-log location for P3 offline packs; retention (HP5) | Read on that folder |
| `sql.connection_ref` | Which DPAPI connection profile the hub uses, with no secret in the file | None |
| `mongo.cfg`, `log`, `ftdc`, `port` | P4 log import and M9; FTDC retention (HP5); a bind-address check (gate N criterion 1) | Read on those paths |
| `redis.conf`, `port` | P10 target; persistence and fork settings | Read on that file |
| `log4net` | P12 parser input (pattern read from the file's own config if that is readable) | Read on those files |
| `app_insights` resource or app id | P9 per-client capability without a key fingerprint search | Reader on the AI resource (engineer side) |
| `perf_logs` | W7 import of existing `.blg` files (G3 for free) | Read on that folder |
| `timezone` | P8 per-host clock; HP4 | None |
| `remote_hosts` | Topology edges without reading any connection string | None |

**Deterministic rules:**

| Rule | Condition | Label |
|---|---|---|
| HP1 Config drift | A profile diff shows a change in services, pools, DLL versions, configs, SQL CU, ports or agents | Symptom (change marker; feeds W9, C10) |
| HP2 Recycle in burst window | An app pool `periodicRestart` time, or a scheduled task trigger, falls within ±5 min of a B13 recurring window | Correlation |
| HP3 Agent as candidate burst source | A monitoring, backup or AV agent is present on a host with B13 recurrences | Symptom (candidate; needs W7 to name the process) |
| HP4 Clock or timezone mismatch | Hosts in one topology differ in timezone or DST setting, or a `w32tm` offset is > 2 s | Symptom (data quality, P8 tolerance) |
| HP5 Evidence at risk | Event Log, FTDC or `system_health` retention is shorter than the investigation window | Symptom (preservation urgency) |
| HP6 Hint/detect conflict [Rev 5] | A hinted value (path, port, instance, role, timezone) differs from the detected value for the same field | Symptom (data quality; the hint is used, both values are shown, a person resolves it) |

**Production safety.**

- The probes are read-only, local, time-budgeted and run at low priority. There are no network calls.
- The one-shot run leaves only the two output files behind.
- A copy of a signed exe still needs client approval to be placed on a production server. It is "a read-only tool run by ops", not an installed component, so **gate N does not apply**: there is no service, listener or persistence.

**Codebase.**

- **New package `internal/detect`.** It defines a `Probe` interface (ID, required right, run with a context, returns facts plus visibility) and a registry of probes. Windows probes sit behind build tags, and a stub covers other platforms.
- **Dependencies.**
  - `golang.org/x/sys/windows/registry` and `svc/mgr`: a new direct dependency, shared with the node's service code (§3.9.5).
  - The standard library `encoding/xml` handles IIS and `.config` files.
  - A restricted key reader handles `mongod.cfg`, the few keys needed, to avoid a YAML dependency. Line parsing handles `redis.conf`.
- **Redaction** is its own unit with fixture tests.
- **[Rev 5] Hints** are a small unit in `internal/detect`: a strict-schema loader (stdlib `encoding/json` with `DisallowUnknownFields`), a flag-to-hints mapper, a merge that assigns provenance, and path validation. Each probe takes an optional hinted location and, when given one, reads only there.
- **Hub side.** A profile store per client (DPAPI-protected like `internal/history`), a topology builder, a drift differ, and a profile review page.
- **Proposals** are pack IDs from the same §3.9.3 registry.

**Demo.**

1. **Setup.** A Windows Server VM, or Windows 11 with the IIS feature, SQL Express, a local `mongod` service and Memurai. Two sites, one app pool with `periodicRestart` at 13:10, log4net in `web.config`, and an App Insights config. A connection string with a seeded fake password, and a `mongodb://user:pass@…` URI.
2. **Non-elevated run.** `unravis detect`. IIS shows `denied — needs local Administrators or a read ACL on inetsrv\config`. The other probes are partial or seen.
3. **Elevated run.** The full profile appears:
   - roles, ports, FTDC path and retention, `.xel` folder;
   - a log4net pattern, and the AI fingerprint without the key;
   - Defender listed as an agent;
   - **HP2** fires for the 13:10 recycle against a seeded B13 window.
   - `grep` over both outputs finds **no** seeded secret.
4. **Edit and accept.** Edit one role, accept two proposals, and import into the hub. The topology shows web → SQL/Mongo edges.
5. **Drift.** Change the pool's `idleTimeout`, re-detect, and HP1 appears as a change marker on the P8 timeline.
6. **Laptop.** Run on a clean laptop and get the workstation message.
7. **Hints [Rev 5].**
   - **Hints without admin.** A non-elevated run with a hints file naming the IIS log folder and `mongod.cfg` (read ACL granted on just those): the facts show `provenance: hinted`, `visibility: seen`, with no elevation.
   - **Conflict.** Hint Mongo port 27018 while `mongod.cfg` says 27017: **HP6** shows both values and uses the hint.
   - **Secret rejected.** A hints file with an inline `Password=` is rejected. The key is named and the value is not echoed.
   - **Hints only.** `--no-detect` under a Process Monitor trace: only the hinted paths are opened.
   - **Hub side.** Hints for a remote `db01` give a profile with `unvalidated` facts and a `web01 → db01` topology edge.

**Acceptance criteria.**

- A full detect finishes in ≤ 60 s on the demo host, and the process stays at or below 5% CPU average `[target]`.
- Zero writes outside the output folder, checked by a file and registry audit in the demo.
- Zero seeded secrets in the output, checked by the redaction fixture suite (connection strings, Mongo and Redis URIs, `redis.conf` auth, appSettings, command-line arguments, encrypted sections).
- Every `denied` or `partial` fact names the missing right. The visibility summary matches a manual check.
- Roles are correct on the demo host. A renamed or second SQL instance and a shared IIS config are detected, or reported as "partial", never silently wrong.
- Nothing is enabled until confirmation: a profile with no `--accept` produces zero collector runs.
- The workstation message and exit code appear on a laptop with no server roles.
- `--diff` lists exactly the changed items.
- **[Rev 5] Hints.**
  - A hints-only run opens only the hinted paths (verified by a trace) and needs no elevation.
  - Every fact carries a provenance. Each hint/detect difference yields exactly one HP6, never a silent override.
  - A hints file with any secret shape, an inline connection string or an unknown key is rejected without echoing a value.
  - Flags, file and UI form produce the identical merged hints for the same input.
  - With partial hints, the unhinted fields are filled by detect, or recorded as `not-probed` under `--no-detect`.

**Dependencies:** none (it ships in the main exe). The proposals reference the pack registry as it grows. **Remaining gaps:** remote detect through JEA (later). **[Rev 5]** Until then, hub-side hints cover remote hosts, validated on first contact. **Instrumentation:** none.

**Diagnostic value: Medium (as an enabler), High for setup cost.**

- By itself it does not narrow a root cause.
- It does turn up the cheapest leads first:
  - existing DCS or monitor history (G3 for free);
  - scheduled recycles and agents in the burst windows (HP2, HP3);
  - retention at risk (HP5);
  - the topology that decides how B13 and S4 are read.
- It also removes per-client setup guesswork.

**Priority: Must, built first (order 0) in parallel with P1.**

- **Why first:** it is small and read-only, needs no gate, and every host-side phase (P3–P12) consumes its output.
- **Effort:** S–M. **[Rev 5]** Hints add about S: a strict schema, a merge with provenance, path validation, and the form. Hints can ship before the heavier probes: a hints-only P0 is a valid first slice.
- **Main risks:**
  - **False detections:** renamed services, multiple instances, FCI/AG names, IIS shared configuration, named-pipe-only SQL, containers. Mitigation: evidence pointers per role, `confidence: inferred`, and human review. **[Rev 5]** Also hints: a person who knows the answer states it, and HP6 flags any disagreement.
  - **Stale or wrong hints [Rev 5]:** path validation, HP6, and the 7-day re-detect prompt also re-validate the hinted paths.
  - **AV/EDR flagging:** service enumeration, registry and config reads look like reconnaissance. Mitigation: Authenticode signing, a published hash and behaviour sheet, no network scanning or credential-store access, and client EDR allowlisting.
  - **Sensitive config handling:** parse-time redaction, no decryption, size limits, and the self-check.
  - **The profile as a reconnaissance map:** client-confidential handling (§24 decision 13).
  - **Load on a busy host:** low priority and a time budget.

---

## 4. Phase P1: Audit foundations (ApiAuditLog)

**Purpose.** Make the existing audit evidence cheap, correct and complete before any new source is added.

**Questions answered:**

- Is every query pruned, or is Unravis itself scanning production?
- Which fields actually exist in this source?
- How much `ApiTime` sits in the pre-action filter phase?
- Which tenant is affected?
- Are there holes in the audit log itself?
- What does "14:45" mean in UTC?

**Native sources:**

- `ApiAuditLog` fields: `_id` (ObjectId timestamp), `RequestInitiatedAt`, `AuditCreateTime`, `ApiTime`, `PayloadRecordTime`, `PreActionFilterTimings[]`, `PreActionFilterTotalMilliseconds`, `AccountId`, plus the existing MVP fields.
- Collection metadata: `$collStats` (`storageStats.count`, `size`, `nindexes`) and `$indexStats` (or `listIndexes`).

**Features and reports:**

| Feature | Detail | Drill-down |
|---|---|---|
| `_id` range pruning | Add `_id ∈ [ObjectIdFromTimestamp(from − 1h), ObjectIdFromTimestamp(to + 4h))` ahead of the `RequestInitiatedAt` match (`internal/store/pipeline.go:99`; Mongo driver v2 `bson.NewObjectIDFromTimestamp`). Margins cover insert lag (default −1 h/+4 h, tunable). Same for `internal/store/legacy_pipeline.go`. Show "docs examined" from `explain` in a debug panel. | n/a |
| Schema-driven field resolution | Field paths come from `SampleSchema` (`internal/store/schema.go`); known aliases are mapped; a missing field shows "absent in this source" rather than null. Prevents the E3 class of error. | Schema panel (existing `/api/sources/{id}/schema`) |
| PreActionFilter phase | New phase "Pre-action filters" from `PreActionFilterTotalMilliseconds`; per-filter breakdown from `PreActionFilterTimings[]`. Whether this time is *inside* `ApiTime` must be confirmed in the brain-payroll filter code before it is drawn as a sub-phase `[unverified]`. Until then, show it as a separate series and never subtract it. | Phase tab → slowest requests with high pre-filter share |
| `AccountId` dimension | Add to breakdown dimensions (`internal/store/breakdown.go` allowed fields; `internal/audit/breakdown.go`). Hidden-by-default treatment like `UserIp` is **not** needed. Tenant identifiers can still be sensitive, so show them pseudonymised in exports (P13). | Breakdown → filter chip |
| Lift ("BubbleUp") on any dimension | For a slow subset vs all: lift = share in slow ÷ share in all, per value. Reuses breakdown queries twice. | Value → filtered request list |
| Coverage / data-quality report | Rows per minute vs the same minute on the previous 7 days; zero-row minutes in business hours; pending-async counts; null-timing counts per field. | Gap minute → activity view |
| Display time zone | UTC internal; Dubai default display; every exported finding carries both. | n/a |
| Capability panel | `$collStats` count/size, index list (flags "only `_id` index: every time filter is a scan; pruning active: yes/no"), server version, legacy path in use. | n/a |
| Trend window extension | Allow up to 90 days (default; tunable) **only** for daily or hourly trend endpoints. Implemented as day-split scans with `_id` ranges, run sequentially through the existing source slot. The 7-day cap stays for request-level views. **[Rev 2]** Use 90 days for daily rollups only. Minute-grain recurrence is capped at 14 days. The justification is in §3.8. | Day → hourly → MVP comparison |

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| A0 Unpruned scan | `explain` shows `totalDocsExamined` ≥ 5× `nReturned` + 10,000 on an Unravis query | Symptom (tool self-check) |
| A1 Pre-filter dominance | `PreActionFilterTotalMilliseconds / ApiTime` ≥ 0.5 and `ApiTime` ≥ 2,000 ms, n ≥ 20 | Symptom |
| A2 Tenant concentration | lift ≥ 3 and slow count ≥ 10 for one `AccountId` | Correlation |
| A3 Lift on dimension | lift ≥ 2, slow count ≥ 10, all count ≥ 20 | Correlation |
| A4 Audit coverage gap | ≥ 5 consecutive minutes with 0 rows where the 7-day same-minute median is ≥ 10 rows | Symptom (data quality) |
| A5 Low-n masking | Percentiles with n < 20 are hollow and excluded from rules. This exists in the MVP; keep it. | n/a |

**Production safety.**

- Pruning cuts each query from a full scan to the window's slice.
- Trend scans are day-split, sequential, and use projection-only `$group`. They are cancellable.
- No index creation is proposed by the tool. Recommending an index on `RequestInitiatedAt` is a DBA remedy text on the capability panel, which notes the write-amplification trade-off on a hot insert collection.

**Demo.**

- Local Mongo seeded with 800k synthetic rows, including `PreActionFilter*` and `AccountId`.
- Show `explain` docsExamined before and after pruning (tier 1). Then show an `AccountId` lift table and a coverage gap created by deleting a minute of rows.
- `--demo` gains `PreActionFilter*` and `AccountId` (tier 2).

**Dependencies:** none. **Remaining gaps:** whether the pre-filter time nests inside `ApiTime`, which needs a code read in brain-payroll. **Instrumentation:** none.

**[Rev 2] Diagnostic value: Medium.**

- It does not narrow a root cause by itself.
- It removes the E3-class mistakes, where null fields were read as zero, and the scan load.
- It supplies the 60–90-day baselines that dated the two problems (L2, L5).

**Priority: Must.** It is the cheapest phase, it uses only existing evidence, and it fixes Unravis's own production load. **Effort:** M. **Main risk:** an `_id`/`RequestInitiatedAt` mismatch if audit rows are inserted long after the request starts (async completion). Mitigation: margins plus the post-filter on `RequestInitiatedAt`, which stays authoritative.

---

## 5. Phase P2: Audit incident patterns (ApiAuditLog)

**Purpose.** Turn the manual `sql/mongo_*.js` investigations into deterministic, drillable findings.

**Questions answered:**

- Which requests stalled?
- Do stalls recur at clock times?
- Did several requests get released at the same instant (a shared block)?
- Was the Hangfire path affected at the same time?
- When exactly did a step change start?
- Which hypotheses are open, supported or refuted?

**Native sources:** `ApiAuditLog` only. Fields: start (`RequestInitiatedAt`), end (start + observed), `ApiTime`, `AuditCreateTime`, kind, controller/action, `AccountId`, user, status.

**Features and reports:**

| Feature | Detail |
|---|---|
| Stall catalog | All requests with observed ≥ 45 s (default) in ≤ 90 days. Merged into episodes when gaps are < 6 min (default). Columns: start/end (UTC + Dubai), count, max, dominant phase (`ApiTime` vs `AuditCreateTime`), APIs, accounts, users. Retrieval is server-side filtered (`ApiTime ≥ threshold` with `_id` pruning), so a 90-day scan returns tens of rows, not hundreds of thousands. |
| Clock recurrence view | Episode start minute-of-day across days, as a grid of days × 5-minute slots. Highlights clock windows that recur. |
| Release-cluster detector | For each episode: requests whose end times fall within 1,000 ms (default) of each other. Slope test: durations vs start times should fall on a line of slope ≈ −1, because everything waits for one release instant. Optional "covering call": a request that started at or before the earliest cluster member and ended within the cluster's release window. |
| Hangfire control differential | For each episode window: Hangfire-kind p50/p95 vs the same window over the previous 7 days, next to the Sync-kind change (Mann-Whitney z from `internal/audit/significance.go:21`). |
| Long-span trend + step dating | Daily p50/p95 per API (≤ 90 days, from the P1 trend extension). A candidate step day is refined to the hour. "Movers" lists the APIs with the largest sustained ratio change. An "honest zone" hides days with n < 20. |
| Timeout pile-up | Histogram of observed durations near known ceilings: 30 s, 60 s, 90 s, 100 s (HttpClient default), 110 s (ASP.NET `executionTimeout` default) `[unverified for this app's config]`. |
| Exact peak in-flight sweep | Exact concurrency from start/end intervals (sweep line) for a ≤ 24 h window. It complements Little's-law `EstimateConcurrency` (`internal/audit/load_correlation.go:144`). |
| Hypothesis ledger | Per investigation: hypothesis text, linked findings (supporting / refuting), status (open / supported / refuted / needs-evidence), and the named evidence gap. Session-local by default; export in P13. It is a structured notebook, not a verdict engine. **[Rev 6]** In P2 it stays a notebook. In P2b (§5A), entries become typed `Theory` records. Each status is then the engine's deterministic verdict; a hand-set status is still allowed, but it is shown as "analyst note", not as a verdict. |

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| B1 Stall | observed ≥ 45,000 ms | Symptom |
| B2 Stall episode | ≥ 1 stall; merge gap < 6 min | Symptom |
| B3 Clock recurrence | episodes start within the same ±3 min clock window on ≥ 2 distinct days in 14 days | Correlation (hypothesis: scheduled work) |
| B4 Release cluster [Rev 2: secondary; not observed in the 10-13 example] | ≥ 3 requests, each with `ApiTime` ≥ 10,000 ms; end times within 1,000 ms; start-time spread ≥ 5,000 ms; slope of duration on start in [−1.2, −0.8] and R² ≥ 0.8 | Correlation (shared block released at one instant) |
| B5 Covering call [Rev 2: secondary] | B4 holds, and a request started ≤ the earliest member and ended within the release window | Correlation (candidate blocker) |
| B6 Audit-insert stall | ≥ 1 request with `AuditCreateTime` ≥ 5,000 ms and `AuditCreateTime / observed` ≥ 0.3 | Symptom; routes to P4 |
| B7 ApiTime stall | B1 with `ApiTime / observed` ≥ 0.8 | Symptom; routes to P3/P5/P9 |
| B8 Hangfire unaffected | Sync |z| ≥ 3 (n ≥ 20, slower) and Hangfire |z| < 2 (n ≥ 20) in the same window | Correlation (narrows the scope: the stall is not shared by the Hangfire path) |
| B9 Step change | the 3 days after vs the 3 days before: p50 ratio ≥ 1.5 and |z| ≥ 3, n ≥ 20 per side; refine to the hour with the same test on 6 h | Symptom with a date and time |
| B10 Timeout pile-up | ≥ 5 requests within ±2% of one ceiling in a day | Symptom (a timeout is hit; not the cause) |
| B11 In-flight peak | exact in-flight ≥ 2× the p95 of the same hour over 7 days | Symptom |
| **B12 Volley** [Rev 2, new] | ≥ 2 requests from the same user/account whose **starts** fall within 1,000 ms and that are each ≥ 45 s. Records the end stagger: the sorted end gaps (here about 4 s) and the start gap | Correlation. Hypothesis: per-user/session serialization (class 15) or a client retry fan-out. Here it replaces B4 as the primary shape (E4, E16) |
| **B13 Host-CPU recurrence** [Rev 2, new] | Needs the P3 CPU series. Non-SQL CPU ≥ 90% (S4 inputs) recurs in the same ±5-minute clock window on ≥ 3 days in 14, **including days with zero sync traffic** | Correlation. Scheduled host work, not caused by requests (E15) |

**Re-evaluation of SpikeHunter ideas for this phase.**

- **Adopted (reimplemented from the idea, no code copied):**
  - stall catalog;
  - release cluster with slope test and covering call;
  - Hangfire control;
  - long-span trend with honest zone and movers;
  - timeout pile-up;
  - exact sweep;
  - low-n masking;
  - hypothesis ledger.
- **Not adopted:**
  - the dense detector: B2/B3 are simpler and explainable;
  - multi-layer verdicts;
  - the time-budget gap rule. It needs sub-timings, which the audit log does not have beyond `AuditCreateTime`/`ApiTime`/pre-filter. It is reconsidered in P9.

**Production safety.**

- Stall retrieval uses `$match` on the duration fields within `_id` ranges, a projection, and `$limit` 5,000.
- Cluster, recurrence, sweep and step computations are client-side, over already-fetched rows or MVP histograms.
- Trend scans are day-split and sequential.

**Demo.**

- Tier 2 generator: stalls at 14:45 Dubai on 4 of 14 days; one release cluster of 3 requests ending within 9 ms; a step on day 8 at 17:00; Hangfire steady.
- Tier 1: the same data inserted into local Mongo.
- Scenario (revision 1): Pulse → "Stalls (43)" → recurrence grid lights 14:45 on 4 days → open 10-13 → release cluster (3 requests, slope −0.98, R² 0.97) → ledger entry "Shared block at 10:45:16Z (UTC)" marked *needs evidence: SQL lock waits (P3)*.
- **[Rev 2] Revised scenario.**
  - The generator also emits a 2-request volley: same user, starts 9 ms apart, ends about 4 s apart, both around 60 s.
  - Pulse → "Stalls (43)" → recurrence grid → open 10-13 → **B12 volley** (same user, start gap 9 ms, end stagger 4 s).
  - Ledger entry: "Per-user serialization or host CPU burst?" marked *needs evidence: P3 CPU split (S4/B13); request trace (P9 if present)*.
  - The release-cluster scenario stays as a secondary demo.

**Dependencies:** P1 (pruning, trend extension). B13 needs the P3 CPU series. **Remaining gaps:** what the blocking resource or the serialization point *is*. That is outside the audit log by definition. **Instrumentation:** none.

**[Rev 2] Diagnostic value: High.**

- B12 and the clock recurrence produced the two strongest leads (E15, E16).
- B8 ruled out a database-wide block (Hangfire was unaffected).
- B9 dated the step to 10-07/08.
- Every later phase is aimed at windows that P2 emits.

**Priority: Must.** It converts known evidence into reusable findings and supplies the exact windows for P3–P8. **Effort:** L. **Main risk:** false release clusters at high traffic. Mitigation: the slope test and the R² requirement; a minimum `ApiTime` of 10 s.

**[Rev 6]** P2 also gains the day × hour heatmap with A5 thin-cell masking and CSV export, which are pseudonymised by default (§1B.A).

---

## 5A. Phase P2b: Theory checks (deterministic theory validation) [Rev 6, new]

**Purpose.** The user asked whether the report's theory validation can be made deterministic, with LLM input optional. It can. A theory becomes a typed, versioned, pre-registered record. Each prediction and falsifier points to an existing rule or a small validation rule (V1–V9). The engine returns a reproducible verdict that keeps the §1 evidence label. Nothing here is a score, and nothing here is AI.

**Questions answered:**

- For each theory: is it supported, contradicted, or not decidable with this data?
- Why not decidable: missing data, a failed precondition, or an open confounder?
- What exactly would change the verdict?
- Can someone else reproduce it?

### 5A.A What P2b is not

It is not the SpikeHunter multi-layer verdict table, which §19 rejects. The differences:

- One theory gets one verdict. There is no aggregation across theories, no ranking and no posterior. A summary never merges two theories.
- Every verdict lists each test with its rule ID, rule version, parameters as applied, n, statistic, threshold and outcome.
- Statuses are a documented function of the test outcomes (§5A.B). Nothing is weighted.
- The label is capped by what the tests *can* show: a co-occurrence test can never yield `proven`.

### 5A.B Records and the verdict function

```
Theory   {id: "TH4", version, title, claim (one falsifiable sentence),
          class (§1A.B 1–15), scope {window, entities, client/environment},
          predictions[] {observable, test: rule ID (B*/S*/C*/V*/...), params{}, expect, min_n},
          falsifiers[]  {observable, test, params{}, expect, min_n},
          preconditions[] {test: V6 | capability row | P0 topology fact},
          confounders[] {name, guard: rule ID},
          label_ceiling: symptom|correlation|strong_correlation|proven,
          required_evidence[] {source, capability, remedy},
          what_would_change_my_mind (generated, never hand-written),
          provenance: catalog|analyst|llm-proposal,
          state: proposed|accepted (only accepted theories enter the ledger; §3.10),
          history[] (prior versions)}
Verdict  {theory_id, theory_version, status: supported|contradicted|inconclusive,
          reason: ok|insufficient_data|precondition_failed|confounder_open|mixed,
          label (≤ label_ceiling), tests[] {rule_id, rule_version, params_applied,
          n, statistic, p?, effect, threshold, outcome: pass|fail|insufficient, method},
          multiplicity {family_id, method, alpha, p_adjusted},
          inputs {sources[], window_utc, observation_fingerprint (SHA-256),
          capability_snapshot_hash}, seed, engine_version, verdict_hash,
          generated_at (outside the hashed body)}
```

**Verdict function.** The steps apply in order, and the first that matches wins:

1. Any precondition fails → **inconclusive**, `precondition_failed`.
2. Any falsifier fails its expectation with n ≥ min_n → **contradicted**. A contradiction outranks support.
3. Any prediction or falsifier test is `insufficient` → **inconclusive**, `insufficient_data`.
4. Every prediction passes after multiplicity adjustment:
   - with a confounder guard that fails → **inconclusive**, `confounder_open`;
   - otherwise → **supported**.
5. Anything else → **inconclusive**, `mixed`.

**Label.** The label is the lowest label among the supporting tests, capped by `label_ceiling`:

- co-occurrence tests reach at most `correlation`;
- C-rules with a shape or duration tolerance plus a mechanism reach `strong_correlation`;
- only request-scoped linkage (P9 AI2, G1) reaches `proven`.

A contradicted theory carries the label of its strongest falsifying test, so "contradicted (correlation)" stays distinct from "contradicted (proven)". A `symptom` theory, such as TH4, only certifies an observation.

**Pre-registration.** A theory's predictions, falsifiers and thresholds are stored before it runs. Editing any of them bumps `version`. Verdicts of the old version are kept. If a version was created after a verdict existed, it is flagged "thresholds changed after seeing a verdict". This shuts the forking-paths route, where thresholds are tuned until a verdict comes out.

### 5A.C Validation rules V1–V9 (guards; they reuse the domain rules, they do not duplicate them)

The domain rules (B*, S*, M*, H*, W*, I*, C*, AI*, R*, T*, L*, HP*) still produce findings. V-rules are the small statistical and data-validity checks that turn findings into decidable tests:

| Rule | Check (defaults are tunable) | Guards against |
|---|---|---|
| V1 Counted base rate | Compare the share of target events (for example stalls) in a condition (for example a CPU-burst minute) with the condition's base rate among **matched reference units**: active minutes, same hours of day, busy days ≥ 100 requests. Exact binomial, or a permutation with the V2 engine. Reports the lift, p and n | Coincidence; business-hours confounding (both stalls and bursts happen when people work) |
| V2 Recurrence permutation | Null model: each day's episode starts are redrawn from that day's active minutes, weighted by request count. Count the clock windows recurring on ≥ 2 days (B3 parameters). p = (1 + #null ≥ observed) / (1 + N), with N = 9,999 | "Same minute on different days" by chance at low traffic |
| V3 Searched change point | When a step date was *searched* rather than pre-stated: a max-statistic permutation over all candidate split hours, with whole days shuffled as blocks. Plus held-out persistence: the step holds on ≥ 3 days after the search window. B9 still computes the ratio and z | The look-elsewhere effect in step dating; one-day blips |
| V4 Load stratification | Split minutes into load quartiles (requests per minute). Per quartile: post/pre p95 ratio, n ≥ 20 per side. Plus Spearman ρ (load, latency) with a 1-hour block permutation (`SpearmanLoad` reused) | "It was just traffic"; per-minute autocorrelation inflating n |
| V5 Control-path continuity | For a control kind (Hangfire by default): the share of episode minutes with ≥ 1 completion, plus the p95 ratio vs the same hours on the baseline days (B8 reused). **Precondition:** the P0 host profile says the control path shares the host or pool under test | "Whole host froze" claims; topology assumptions |
| V6 Source sanity (precondition gate) | Fails when a series is flat (constant for ≥ 7 days), impossible, missing > 20% of days, sampled rather than complete, clock-skewed beyond the §3.3 tolerance, or flagged by P1 coverage (A-rules) or the S4 counter cross-check (L4 step 2). It consumes those rules; it does not recompute them | Verdicts on broken counters (E19) or sampled periods |
| V7 Entity concentration | Distinct users and accounts among the supporting events; top-1 share. If distinct ≤ 2 or top-1 ≥ 50%, a "routine or tenant" confounder is open, unless a guard breaks it (for example B13 recurrence on zero-traffic days) | User routines posing as scheduled work; volleys inflating counts (§1A.B class 4) |
| V8 Multiplicity | Holm over the theories in one verdict run (the family). Benjamini–Hochberg q-values on exploratory lists, such as movers, which are labelled "exploratory" and never feed a verdict directly | Many tests, one lucky hit |
| V9 Regime-aware baseline | A baseline must lie in a single regime, between V3 boundaries or explicitly pre-step, with matched weekday type and hours. A baseline that crosses a boundary is flagged "contaminated", and its comparisons are `insufficient` | Comparing 22 Oct with 21 Oct, both post-step (the report's own caveat) |

**Minimum sample sizes (defaults):**

- Mann-Whitney: n ≥ 20 per side (as B8/B9).
- p95 statistics: n ≥ 50 (the report's own rule); below that, p95 is `insufficient`, never estimated.
- Heatmap and stratum cells: ≥ 5.
- Day-level regime tests: ≥ 3 days per side; ≥ 7 preferred and reported.
- Permutations: N = 9,999. The seed is the first 8 bytes of the SHA-256 of the canonical input, so p is reproducible. Its Monte Carlo error at N = 9,999 is stated.
- Percentiles: the method is recorded on each test. Mongo `$percentile` is approximate. Where n ≤ 10,000 the value is recomputed exactly, client-side.

**Statistics stay boring and visible.** Only these are used: Mann-Whitney (`internal/audit/significance.go:21`), Spearman (`internal/audit/load_correlation.go:160`), exact binomial, permutation and block permutation, Holm and BH. There are no learned models and no black boxes.

### 5A.D Theory catalog (the report's 8 theories and the §1A.B classes)

"Expected verdict on the sandbox data" is a prediction from E1–E23. It is `[unverified until P2b runs]`.

| TH | Claim (falsifiable) | §1A.B class | Predictions → tests | Falsifier | Preconditions / confounder guards | Ceiling | Report status → expected engine verdict |
|---|---|---|---|---|---|---|---|
| TH1 | SQL Server CPU was saturated during the stalls | 8 (SQL side) | S4 SQL CPU share ≥ 70% in ≥ 50% of stall minutes; `sp_server_diagnostics` not clean | S4 SQL CPU < 20% in ≥ 80% of stall minutes, and components clean | **V6 on `process_utilization`** (flat 0 for 70 days fails it); the L4 cross-check | correlation | Rejected → **inconclusive, `precondition_failed`** until the counter cross-check passes (E19) |
| TH2 | The API's own request volume caused the slowdown | 5 | V4 ρ ≥ 0.4 in the post-step period (n ≥ 200 active minutes); the step vanishes within load quartiles | Post/pre p95 ratio ≥ 1.5 in ≥ 3 of 4 load quartiles (n ≥ 20 per side), **or** stalls in minutes with in-flight ≤ the hour's p50 (B11) | V9 baseline; V6 | correlation | Rejected (one day pair, n = 2) → **contradicted (correlation)** expected, on all busy days (E15, E18) |
| TH3 | The whole host froze during the stalls | 8 (whole host) | V5: Hangfire completions stop (minutes with ≥ 1 completion < 50%) | V5: ≥ 1 completion in ≥ 90% of stall minutes and B8 Hangfire |z| < 2 | **P0 topology:** Hangfire on the API/DB host under test; otherwise inconclusive | correlation | Rejected → **contradicted** if the topology is confirmed, else **inconclusive, `precondition_failed`** (class 10 caveat) |
| TH4 | Dashboard latency stepped up on 10-07/08 at about 16:00 Dubai | 1 | B9 on the fixed top-5 dashboard set, ratio ≥ 1.5, \|z\| ≥ 3; V3 searched-date permutation p < α; persists ≥ 3 days | Ratio < 1.2, or no held-out persistence | V6 (no sampled days); V9 | symptom | Observed → **supported (symptom)**, dated to the hour |
| TH5 | A shared lock, pool or dependency in the web request path holds unrelated requests and releases them together | 2 / 9 | B4 release cluster (distinct users, start spread ≥ 5 s, end spread ≤ 1 s, slope test) in ≥ 50% of episodes | ≥ 80% of stalled requests sit in same-user B12 volleys with staggered ends (E16) | V7 | correlation | Consistent → **contradicted for the one-instant release shape** expected; re-targeted to TH9 |
| TH6 | The 22 Oct incident is the chronic post-step problem, not a new one | 1 + 2 | The 22 Oct daily p95 and stall count lie within [p10, p90] of the post-step days (≥ 7 days); its episodes fall in B3/B13 windows | 22 Oct outside p99, or its top movers are not regime movers (BH q > 0.1) | V9 (post-step regime only); V6 | correlation | Consistent → **supported (correlation)** expected; the 14:09 warm-up part moves to TH11 |
| TH7 | Host CPU stepped up on 09-21 (TH7a); that step caused the 10-08 latency step (TH7b) | 8 / 12 | TH7a: V3 on daily total and non-SQL CPU, ratio ≥ 1.3, ≥ 7 days per side. TH7b: C10 conditions | TH7b: the latency step persists in low-CPU minutes (C10 "not S4"; E18) | V6 on the CPU series (the attribution may be wrong; the total stays valid) | symptom / correlation | Consistent → **TH7a supported (symptom)**; **TH7b contradicted (correlation)** expected |
| TH8 | Stalls recur at fixed clock minutes because of scheduled work | 3 | B3 with V2 p < α (Holm); B13 recurrence on zero-traffic days; C2 or HP2 schedule match | V2 p ≥ 0.2 with n ≥ 10 episodes | **V7 user-routine guard** (the report's caveat); B13 zero-traffic recurrence breaks it | correlation → strong_correlation (with W7/C2) | Open → **supported (correlation)** expected, via E15 |
| TH9 | One user's concurrent requests are serialised (session or per-user lock) | 15 | B12: ≥ 80% of stalled requests in same-user volleys; end-gap coefficient of variation < 0.3 | ≥ 3 distinct users with overlapping stalls that do not stagger | V7 | correlation (proven only via P9/G9) | not in report → supported (correlation) expected |
| TH10 | Non-SQL CPU bursts on the DB host slow the requests (C9) | 8 | V1: stall share in burst minutes vs the base rate among matched active minutes; lift ≥ 3, p < α | Lift ≤ 1.2 with ≥ 20 stalls | V6 on the CPU series; V1 hour matching | correlation → strong_correlation (W7 names the process) | not in report → supported (correlation) expected (E15) |
| TH11 | App-pool cold start against Mongo causes the ~28 s audit stalls | 6 | M9 + W1/IIS start: each B6 stall ≥ 25 s follows an idle gap or recycle | ≥ 2 such stalls with no idle gap or recycle before them | Capability: `mongod.log` and WAS horizon cover the window | correlation → strong_correlation | partly in report (theory 6) → inconclusive until P4/P6 |
| TH12 | Lock waits ≥ 30 s caused the stalls | 7 | S1 matches | 0 `wait_info` ≥ 30 s in the window | Capability: `system_health` running and the file horizon covers the window | correlation → strong_correlation (C1) | not in report → **contradicted** (E9) |
| TH13 | A deploy or configuration change explains the step | 1 / 12 | C4 / C10 within 24 h before the TH4 date | W5/W9/S10 complete for the window, with no change found | Capability: change-lane coverage (P6) | correlation | report "Next step" 1 → inconclusive until P6 |
| TH14–TH19 (templates) | Tenant concentration (4: A2/A3 per volley + V7); pool or queue (9: I-rules, P11a T1); GC or thread pool (10: T-rules); AV, backup or network (11: W4, S9); Redis (13: R-rules, renamed RD at implementation); external dependency (14: AI-rules) | 4, 9, 10, 11, 13, 14 | As listed | Rule-specific | Source capability rows | per rule | inconclusive until their phase ships, with the remedy shown |

Every §1A.B class now has at least one theory. Composite rules (C1–C10) are predictions *inside* theories; they are not theories themselves.

### 5A.E How it connects to the existing pieces

- **Ledger (P2).** A theory is a typed ledger entry. P2's notebook entries can be promoted to theories.
- **Rules.** Rules stay the only evidence producers. P2b never re-implements B9, B12, S4 and the rest; it calls them with the theory's parameters.
- **P3–P9.** Each phase registers its rules as available tests. Theories that waited on a missing source re-run when its capability turns `ok`.
- **P8.** The composites (C1–C10) become predictions. P8's "Ledger integration" shows P2b verdicts, and its "Missing evidence" panel reads `required_evidence`.
- **P13.** Verdicts, theories and the reproducibility bundle join the evidence API and the case export unchanged, under the same HMAC pseudonymisation.
- **MCP.** The four theory tools are described in §3.10. **[Rev 7]** A fifth, `theory_narrative_submit`, and the `view=cards` parity are described in §5A.H.
- **[Rev 7] Screen.** The theories screen (§5A.H) is the ledger's main view from R2b on.

### 5A.F Report features: what to add, and when

| # | Feature | Label | Reason |
|---|---|---|---|
| 1 | **Deterministic incident report export** (Markdown plus a self-contained HTML page) from the ledger. It contains: the summary, the verdict table, per-theory tests, a data-quality section (V6), a methods appendix generated from the rule versions and thresholds applied, and the privacy statement | **Add now** (P2b) | Replaces the hand-built `report.src.html` + `build_report.js` pipeline. Charts reuse the UI's SVG later |
| 2 | **Reproducibility bundle** (`theory-run.zip`): the theory versions, verdicts, a pseudonymised observation snapshot, fingerprints, seeds, the engine version and the capability snapshot. `unravis theory rerun` gives byte-identical verdicts, and a live re-run shows an input drift diff | **Add now** | The acceptance criteria depend on it. P13's case ZIP later embeds it |
| 3 | **"What would change my mind"**, generated per theory from the falsifiers, `required_evidence` and the capability remedies | **Add now** | A deterministic projection of the record. It replaces the hand-written "Next steps" |
| 4 | **User-reported windows** as typed input (who reported, when, the window). Tested by V1 against measured anomalies: "reported window matches an anomaly / does not" | **Add now** | Tiny. The report hardcoded two such windows |
| 5 | Null and planted fixtures (calibration suite) | **Add now** | Proves that V2/V3 do not fire on random data. It extends §20's incident replay fixture pack |
| 6 | Sensitivity sweep: re-run a verdict over a threshold grid (for example stall at 30, 45 or 60 s) and report whether the status is stable | **Later** (after P2b) | High value against threshold-tuning, but it multiplies output. Add when the first real client verdicts exist |
| 7 | Compare any two days or periods (UI) | **Later** (P2 polish) | A convenience; regime tests already make the formal comparison |
| 8 | Synced crosshair and brush explorer with "vs the window just before" arrows | **Later** (P8 D1) | Belongs with the lanes |
| 9 | Verbal-only correlation bands ("weak", "moderate") | **Skip** | Words without n and p mislead. Show ρ, n and p |
| 10 | Likelihood ratios, posteriors or a ranked "most likely cause" | **Skip** | Opaque aggregation; conflicts with §19 and §5A.A |
| 11 | Auto-written "root cause" headline | **Skip** | The headline is the verdict table. "Not proven" is shown until a `proven` label exists |

### 5A.G Delivery

**Production safety.** P2b runs no new production queries. It computes over already-fetched, bounded findings and observations, as P8 does. Permutations run client-side, capped at 9,999 draws per test, with a time budget per run.

**Demo.** It uses Tier 2 synthetic fixtures only (no real extracts; §24 old 8):

1. Load the P2 generator plus a 10-07/08-style step, Hangfire continuity, and two "users" who produce the recurring stalls.
2. Run the catalog. Expected results:
   - TH4 is supported (symptom), with its V3 p-value.
   - TH2 is contradicted: slower in all four load quartiles.
   - TH3 is inconclusive, because the topology is not confirmed. Confirm the topology in the P0 profile, and it becomes contradicted.
   - TH8 is inconclusive with `confounder_open` (V7: 2 users).
   - TH1 and TH10 are inconclusive, `precondition_failed`, because the CPU source is absent; the remedy is shown.
3. Load a CPU fixture with zero-traffic bursts. TH8 becomes supported (correlation), and TH10 becomes supported.
4. Load a flat-zero SQL CPU fixture. TH1 stays inconclusive (V6). This reproduces the report's E19 trap.
5. Edit TH2's threshold. The version bumps, the old verdict is kept, and the "changed after verdict" flag shows.
6. Re-run from the bundle on a second machine. The `verdict_hash` is identical.
7. Run the null fixture (random stall times). TH8 is not supported.
8. Export the incident report. Ask Claude Code over MCP to draft the narrative. A planted free number is rejected by the linter.

**Acceptance criteria:**

- The same bundle gives **byte-identical verdict JSON** (excluding `generated_at`) on Windows and Linux CI.
- Each verdict lists every test with its rule ID and version, parameters as applied, n, statistic, threshold, outcome and percentile method.
- **No supported or contradicted verdict when any decisive test has n < min_n.**
- The label never exceeds the ceiling. There is no `proven` without request-scoped linkage.
- On 200 seeded null fixtures, TH8 and TH4 are supported in ≤ 5% of runs (α = 0.05, Holm). On planted-effect fixtures, each TH with a planted effect is supported, and each planted falsifier contradicts.
- A theory edit after a verdict bumps the version, keeps the history and shows the flag.
- Everything works with no LLM and no MCP client. The LLM path changes no verdict byte (§3.10 invariant).
- There are zero production queries; this is checked with the query journal (§3.7).

**Dependencies:** P2 (findings, ledger), P1 (coverage, for V6), and P0 (topology facts, for V5). It gains power as P3, P6 and P9 ship. **Remaining gaps:** proof still needs request-scoped linkage (P9 or G1). **Instrumentation:** none.

**Diagnostic value: High as a guard.** It finds nothing new. It stops wrong conclusions and makes the right ones reproducible.

**Priority: High Value.** The V6 source-sanity gate is **Must**. **Effort:** M. It is a new package, `internal/theory`, which reuses the existing statistics. **[Rev 7]** The theories screen (§5A.H) brings the phase to M–L.

**Main risk:** false precision, meaning p-values on autocorrelated minute data. Mitigations: block permutations, day-level tests for regimes, stated Monte Carlo error, and V9.

### 5A.H Theories screen: "What is known and what is not" [Rev 7, new]

**Why.** The user liked the report's "What is known and what is not" section: one card per theory, each with a title, a status chip, EVIDENCE text and GAP text. The screen keeps that reading experience. What changes is where the content comes from. Every word on it comes from the P2b verdict and from versioned text templates; nothing is hand-written. An LLM may only reword the text. It is optional, off by default, and labelled when used.

**Layout.** It follows `docs/superpowers/specs/2026-10-24-unravis-ui-direction.md`: progressive depth, and CRAP. The report's boxed cards become **hairline rows**, because the spec says "space instead of cards, no nested cards".

- **Level 1, the verdict row** (one line per theory, the screen's only list). From left to right:
  1. the verdict chip: `Supported`, `Contradicted` or `Inconclusive`;
  2. the evidence-class badge: `SYMPTOM`, `CORRELATION`, `STRONG CORRELATION` or `PROVEN`, in small tracked caps;
  3. the theory title;
  4. one muted line of text. For **Inconclusive** this is always the reason, for example "counter failed sanity check". For the other verdicts it is the first EVIDENCE sentence;
  5. right-aligned, "Next: <action> · <owner>", for example "Next: cross-check SQL CPU counter · P3 / DBA".

  Nothing else is shown at this level. That is one focal element per row (Contrast) and one chip vocabulary shared with the rest of the app (Repetition).
- **Level 2, the inspector** (a right drawer on wide screens, a full view on narrow ones, as D2 in the spec). It opens on click or Enter. Its sections are shown only when they have content, in this order:
  1. **Evidence**: the template sentences, one per supporting or falsifying test.
  2. **Gap**: the template sentences.
  3. **What would settle it**: the next missing evidence and who acts.
  4. **What would change my mind**.
  5. **Tests**: a table with rule ID and version, parameters as applied, n, statistic, threshold, outcome and percentile method; numbers are right-aligned and `tabular-nums`.
  6. **Reproduce**: the `verdict_hash`, the input fingerprint, a "Download bundle" button and the exact `unravis theory rerun` command.
  7. **History**: theory versions, with the "changed after verdict" flag.
  8. **Analyst note**.
- **Level 3, the underlying findings and records.** Every test row opens the findings it used, and every finding opens its records. This keeps the spec's drill-down integrity rule.
- Esc steps back up one level, the breadcrumb reads "Theories › TH1", and the state survives going back.
- Colour never carries meaning alone.
  - Supported is a filled neutral chip. Contradicted is an outline chip with a strike glyph. Inconclusive is the spec's hatched slate, which already means "incomplete/pending".
  - The single accent marks focus and selection only.
  - Screen readers hear, for example, "TH1, Inconclusive, reason: counter failed sanity check, evidence class: correlation".

**Display vocabulary and the old report labels.** The screen shows a verdict plus a badge, and never a single merged word. When a theory came from the October report, its old label is kept as a read-only "Report said" field in the inspector, and a mismatch is shown, for example "Report: Rejected → Engine: Inconclusive (counter failed sanity check)".

| Old report label | Engine display | Notes |
|---|---|---|
| Observed | Supported · SYMPTOM | An observation certified by the tests |
| Consistent | Supported · CORRELATION | Only if the engine agrees; otherwise the mismatch is shown |
| Open | Inconclusive · *reason* | The reason is always shown |
| Rejected | Contradicted · *label of the falsifying test* | TH1 shows Inconclusive instead (E19) |
| (none) | Supported · STRONG CORRELATION / PROVEN | New levels the report never had |

**Inconclusive always states its reason.** The Verdict record gains an additive field, `reason_detail {source_test_or_capability, template_key}`. Each key maps to a fixed phrase:

| `reason` | Example `template_key` → phrase |
|---|---|
| `precondition_failed` | `v6.flat` → "counter failed sanity check (flat for {{days}} days)"; `v6.sampled` → "source is sampled in this window"; `p0.topology` → "host topology not confirmed"; `cap.absent` → "source not connected: {{source}}" |
| `insufficient_data` | `n.low` → "too few events (n = {{n}}, needs {{min_n}})" |
| `confounder_open` | `v7.users` → "could be one user's routine ({{distinct_users}} users)"; `v9.contaminated` → "baseline crosses a regime change" |
| `mixed` | `mixed` → "predictions disagree ({{passed}} of {{total}} passed)" |

**EVIDENCE and GAP sentences are generated, never free text.**

- One template per `(rule_id, outcome)`. For example, `B9.pass`: "Dashboard p95 rose {{ratio}}× from {{date_local}} ({{n_pre}} vs {{n_post}} requests, z = {{z}})."
- Every number is a reference token into the verdict JSON, resolved by the engine. Number formatting is fixed per token type: ratio to 2 decimal places, p to 3 significant figures, dual Dubai/UTC timestamps (§3.3).
- **GAP sentences** come from three places, in this order:
  1. the label ceiling ("Cannot exceed correlation without request-level linkage (P9 or G1).");
  2. open confounders;
  3. unmet `required_evidence`.
- The templates live in a versioned file, `theory-text/1`. Its version is recorded in the export. The same verdict plus the same template version gives byte-identical text.
- A template that is missing for a `(rule_id, outcome)` pair is a build-time test failure. There is no fallback prose.

**"What would settle it" (one line, deterministic).**

- Take the theory's `required_evidence` list. Drop the entries whose capability row is `ok` in the capability probe (§3.2) and in the P0 host profile.
- Order what is left by **cost tier**:
  - 0: data already available, test not run;
  - 1: config or code read;
  - 2: DBA grant;
  - 3: ops action (enable a log, run the DCS);
  - 4: a phase not built yet;
  - 5: gated instrumentation (G).
- Break ties by build order, then by ID.
- Show the first entry, with its owning phase and the remedy text from the capability or profile row, for example "Grant VIEW SERVER STATE (DBA) → P3 S4 runs". If the list is empty, show "Nothing missing: the verdict is as strong as this data allows".

**"What would change my mind".**

- For a **Supported** theory, each falsifier is rendered as "If {{observable}} {{expect}} with n ≥ {{min_n}}, this becomes Contradicted."
- For a **Contradicted** theory, the failed falsifier is rendered with what would have to hold instead.
- For an **Inconclusive** theory, the line reads "Becomes decidable when: …", from `reason_detail` plus "What would settle it".

**Sort and filter.**

- Filter chips: verdict, evidence class, §1A.B class, owning phase of the next evidence, and source (§1B). Chips are removable, as everywhere else.
- Default sort mirrors the report's "known, then not known" order:
  1. Supported, strongest label first;
  2. Contradicted;
  3. Inconclusive.
  Ties are broken by TH ID.
- The other sort is **"cheapest next evidence"**: cost tier, then build order, then ID. It turns the screen into an action list for ops and the DBA.
- Sorting is stable and deterministic, and the URL state keeps the sort and filters.

**Analyst note.**

- A free-text field per theory, stored with an author and a timestamp. It is shown in a muted style under the "Analyst note" heading.
- It is **outside** the verdict function and the `verdict_hash`; a test asserts that editing a note leaves the hash unchanged.
- Exports include notes in a separate, clearly marked section, after a prompt that warns notes may contain identifiers.
- MCP leaves notes out by default (`include_notes=true` adds them), because they are the only unpseudonymised free text on the screen.

**Optional LLM wording (off by default; everything works without it).**

- A toggle, "Show LLM wording", appears only if a draft exists for that theory. It switches the Evidence and Gap sentences to the draft. The draft carries an "LLM-drafted, numbers checked" label, and "Show template text" switches back.
- Drafts arrive only from an external client over MCP. **[Rev 7]** This adds a fifth theory tool, `theory_narrative_submit (theory_id, verdict_hash, text)`.
- The §3.10 narrative linter checks each draft. It rejects any digit outside a `{{token}}`, any status or label word that disagrees with the verdict, and any draft bound to an outdated `verdict_hash`.
- A new verdict hides the old draft automatically. The verdict row (level 1) always uses template text.
- The build still contains no LLM client.

**MCP parity.**

- The server builds one view model, `TheoryCardView`. It holds the verdict, the badge, the reason phrase, the resolved sentences with their tokens and values, "What would settle it", the change-my-mind list, the sort keys and the report-said label.
- The screen and `theory_verdicts (view=cards)` serialise the same struct, so an AI client sees exactly what the engineer sees, in the same order for the same sort.
- A test compares the screen's JSON payload with the MCP output byte for byte.

**Test strategy (fixtures drive every state).**

- **State fixtures.** One synthetic fixture per verdict state and reason:
  - supported, with a planted effect;
  - contradicted, with a planted falsifier;
  - inconclusive/`precondition_failed`, with a flat-zero counter and with a missing topology;
  - inconclusive/`insufficient_data`, with n below the minimum;
  - inconclusive/`confounder_open`, with two users;
  - inconclusive/`mixed`, with split predictions.

  Each fixture asserts the chip, the badge, the reason phrase, "What would settle it" and a golden-file text render.
- **Null fixtures** (random stall times, 200 seeds): TH8 is shown as Supported in ≤ 5% of them, the same bound as §5A.G.
- **Template completeness:** every `(rule_id, outcome)` and every `reason_detail` key has a template, enforced at build time.
- **Linter tests:** a planted free digit, a wrong status word and a stale hash are each rejected.
- **UI tests:**
  - an Inconclusive row never renders without a reason;
  - the old-label mapping table holds;
  - sorting is deterministic;
  - keyboard reach and focus work;
  - the screen is usable at 390 px and at 1600 px;
  - empty, loading and partial states render (no theories yet; a source still loading);
  - with the LLM toggle absent, the screen is complete.
- **Parity test:** the screen payload equals the MCP output.

**Demo (adds to §5A.G).**

9. Open Theories. The rows show TH4 as "Supported · SYMPTOM" and TH1 as "Inconclusive · counter failed sanity check · Next: cross-check SQL CPU counter · P3 / DBA". The inspector for TH1 shows "Report: Rejected → Engine: Inconclusive".
10. Switch to "cheapest next evidence" and see the action list.
11. Add an analyst note to TH1. The `verdict_hash` does not change.
12. Ask Claude Code over MCP for `theory_verdicts view=cards`. It returns the same rows. Have it submit a narrative for TH4 and toggle "Show LLM wording". Then submit one with a planted free number: it is rejected.

**Acceptance criteria (adds to §5A.G):**

- Every row is one line on desktop.
- Every Inconclusive row shows a reason phrase from `reason_detail`.
- No sentence on the screen contains a number that is not a resolved token. This is checked by scanning the rendered DOM against the token list.
- The same verdict and template version render byte-identical text on Windows and Linux.
- "What would settle it" changes only when the capabilities or the profile change. Flipping a capability row in the fixture changes it as expected.
- Editing an analyst note never changes a verdict or its hash.
- With no LLM and no MCP client, every section renders fully.
- The screen JSON equals the `theory_verdicts view=cards` output.
- WCAG AA in both themes.

**Effort:** S–M on top of the P2b engine, which takes P2b to **M–L** in total. **Priority:** High Value, shipped with R2b. The verdict row and inspector are **Must** for R2b. The LLM toggle is optional and comes last.

---

## 6. Phase P3: SQL Server persisted diagnostics (system_health first)

**Purpose.** Use SQL Server's own persisted evidence to test the P2 hypotheses retrospectively.

**Questions answered:**

- Was a session waiting on a lock for the same duration as the stall?
- Were there deadlocks, long latch waits, I/O stalls, non-yield or severe errors?
- Was SQL using the CPU, or was something else on the host?
- Did a backup, restart, autogrowth or configuration change coincide?

**Native sources (exact):**

| Source | What | Horizon here | Permission |
|---|---|---|---|
| `system_health` XE file target via `sys.fn_xe_file_target_read_file('system_health*.xel', NULL, NULL, NULL)` with `timestamp_utc` (2017+) and `object_name` filters | `wait_info` (LCK_M_* > 30 s, latch > 15 s, preemptive), `xml_deadlock_report`, `error_reported` (severity ≥ 20, memory errors), `scheduler_monitor_non_yielding*`, `sp_server_diagnostics_component_result` (system, resource, query_processing, io_subsystem, events), `scheduler_monitor_system_health_ring_buffer_recorded`, `security_error_ring_buffer_recorded`, `connectivity_ring_buffer_recorded`, `memory_broker_ring_buffer_recorded` [R7] | 08-15..10-23 (E9). The files are 5 MB × 4 or 100 MB × 10 depending on version/edition [R7] | `VIEW SERVER STATE` (2022: possibly `VIEW SERVER PERFORMANCE STATE` `[unverified]`) |
| `sys.dm_os_ring_buffers` (`RING_BUFFER_SCHEDULER_MONITOR`) | Per-minute SQL vs idle CPU, ~256 min | Since the last ~4 h | `VIEW SERVER STATE` |
| `sys.sp_enumerrorlogs` + `sys.sp_readerrorlog` (`xp_readerrorlog` is undocumented) | Error 833 (I/O > 15 s), 17883/17884, 1205, "Configuration option … changed", login failures, restart markers, memory messages | 6 logs by default; #2 covers 10-17..10-23 (E9) | Was readable in this environment (E9) |
| Default trace `sys.fn_trace_gettable` (path from `sys.traces where is_default = 1`) | Autogrow (events 92/93), Object:Altered/Created/Deleted (deploy evidence), sort and hash warnings, Errorlog | Since restart only (E11) | `ALTER TRACE` or `VIEW SERVER STATE` `[unverified]` |
| `msdb.dbo.backupset` / `backupmediafamily` | Backup start, finish, type, size | Long | msdb read |
| DMVs (live; reset at restart 10-23 09:09) | `sys.dm_os_wait_stats`, `sys.dm_exec_requests` + `sys.dm_exec_sessions` (blocking chain), `sys.dm_io_virtual_file_stats`, `sys.dm_exec_query_stats` (plan cache, skewed), `sys.dm_os_sys_info` | Since restart | `VIEW SERVER STATE` |
| Query Store (when ON) | `sys.query_store_runtime_stats`, `…_runtime_stats_interval`, `…_wait_stats` (2017+) | n/a here (OFF; E8) [R8] | db `VIEW DATABASE STATE` |

**Features and reports:**

- Capability panel: version, start time, Query Store state, permissions, `system_health` horizon, error-log horizon, default-trace horizon.
- **Lock-wait and deadlock timeline.** Every `wait_info` ≥ 30 s and every deadlock in the window, showing end time, duration, wait type, resource, session, `sql_text` and client app/host where the event carries them `[unverified field set]`.
- **CPU split chart.** SQL / other / idle per sample, from scheduler-monitor ring-buffer records persisted in `system_health`. This is the E10 chart, as a product feature.
- `sp_server_diagnostics` component states over time (clean/warning/error), with I/O `longestPendingRequests`.
- Error-log viewer: window-filtered, grouped by message ID, with a fixed allowlist of patterns (833, 17883, 1205, 701, "Configuration option", "Starting up").
- Default trace: autogrowth and DDL events since restart.
- Backups overlay.
- **Live mode** (on demand, never background): top waits as a delta over 30 s (the old Task 13 idea, kept only here), current blocking chain with head blocker, file I/O latency delta.
- Query Store regressions, shown **only when QS is ON** (old Task 12 rules, unchanged).

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| S1 Matching lock wait [Rev 2: build in **Later**. Refuted in this environment: 0 `wait_info` events in 70 days (`sql/out_part2.txt:7-12`)] | `wait_info` LCK_M_* with duration ≥ 30,000 ms whose end is within ±2 s (+ skew) of a P2 release instant and whose duration is within ±2 s of the longest cluster member's `ApiTime` | Strong correlation |
| S2 Lock wait in window | any LCK_M_* ≥ 30 s overlapping a stall episode | Correlation |
| S3 Deadlock | `xml_deadlock_report` in the window | Symptom (plus correlation if in an episode) |
| S4 Non-SQL CPU | `100 − SQL% − idle%` ≥ 30% for ≥ 3 consecutive samples | Symptom: CPU contention on the host; process unknown (gap G3). **[Rev 2] Caveat:** SQL% read 0 for 70 days here (E19). Before S4 is shown, a **counter sanity check** runs. It compares Δ`sum(total_worker_time)` from `dm_exec_query_stats` over a 60 s interval with the ring buffer's SQL% (and `Process(sqlservr)` when P6 is present). If they disagree by more than 2×, S4 is labelled "SQL vs other split unreliable; total host CPU only" |

| S5 Severe error | `error_reported` severity ≥ 20, 17883/17884, or memory errors in the window | Symptom |
| S6 Long latch | latch waits > 15 s in the window | Symptom (I/O or tempdb) |
| S7 Storage stall | error 833 in the window, or `io_subsystem` component not clean | Symptom |
| S8 Autogrowth | autogrow event with duration ≥ 1,000 ms in the window (since restart only) | Correlation |
| S9 Backup overlap | a `backupset` interval overlaps the episode; otherwise **counter-evidence** "no backup overlap; nearest X" | Correlation / ruled out |
| S10 Restart or config change | start time, "Configuration option changed", or DDL within ±24 h of a P2 step | Correlation (change candidate) |
| S11 Client consumption | `ASYNC_NETWORK_IO` ≥ 30% of non-idle wait time since start | Symptom (the client reads results slowly or the result sets are large; not a server bottleneck) |
| S12 Live blocking | live `blocking_session_id` chain with head-blocker wait ≥ 5,000 ms | Symptom (live) |
| S13 QS regression | only if QS is ON: avg duration ≥ 2× with ≥ 20 executions in both periods | Correlation |
| **S14 Live request sampler** [Rev 2, new] | Every 5 s, run only during a confirmed sampler run (≤ 30 min, the predicted windows, decision 5). It reads `dm_exec_requests` ⋈ `dm_exec_sessions` ⋈ `dm_os_waiting_tasks`: session, `program_name`, `host_name`, `login_name`, status, wait type, wait time, `blocking_session_id`, `statement_text` (truncated), CPU and reads deltas. Fires when any request has wait ≥ 5,000 ms or a head blocker holds ≥ 2 waiters | Symptom (live). It covers the 5–30 s waits that `system_health` cannot see (G7) and names the third-party monitor's sessions (E21) |

**Production safety.**

- XE file reads are filtered by `object_name` in the `WHERE` and by a `timestamp_utc` window of ≤ 24 h per call (default), with `TOP 2000`.
- `READ UNCOMMITTED`, `LOCK_TIMEOUT 5000`, `DEADLOCK_PRIORITY LOW`.
- No XE session creation and no QS changes (§3.5).
- **Note:** `system_health` itself has no noticeable overhead [R7]. Reading it costs XML parsing on the server, so bound every read.

**Demo (tier 1, real native data).**

- Local SQL Server 2022 Developer.
- Script: session A runs `BEGIN TRAN; UPDATE dbo.ReportConfig …; WAITFOR DELAY '00:01:15'; COMMIT`. Sessions B and C, started 5 s and 20 s later, run `SELECT` on the same row.
- Result: a genuine `system_health` `wait_info` over 30 s for B and C.
- The seeded audit rows place a P2 release cluster at the same instant.
- Scenario: P2 ledger "shared block at 10:45:16Z" → SQL tab → S1 fires: "LCK_M_S 74.6 s ending 10:45:16.3Z, resource KEY …, sql_text …" → ledger moves to *supported (strong correlation)*. The proof gap G1 (request ↔ session linkage) is stated beside it.
- CPU split: run a CPU burner (e.g. a PowerShell busy loop) on the local host and show S4.
- **[Rev 2] Lead scenario.** Seed P2 stalls at the times of the burner runs, on 3 days.
  - S4 + B13 fire together.
  - The sanity check flags that the SQL% counter disagrees with Δworker time (simulated with a mocked series in tier 2).
  - The ledger then reads "host CPU burst, process unknown → P6 collector (G3)".
- The lock-wait script stays as a secondary demo for S2/S12/S14.

**Dependencies:** P2 (windows), and a SQL connection with DPAPI-stored credentials (new connection kind).

**Remaining gaps:**

- Lock waits between 5 s and 30 s are invisible to `system_health` (G7).
- Which non-SQL process used the CPU (G3).
- Request-to-session linkage (G1).

**Instrumentation:** none. Enabling Query Store is a DBA native change (§24 decision 2), not instrumentation.

**[Rev 2] Diagnostic value: High.**

- The CPU split tied 37/43 stalls to bursts (E15).
- The 70-day `wait_info` and deadlock history refuted lock waits ≥ 30 s.
- The connectivity ring buffer surfaced the 09-21..23 bursts (E23).
- The monitor's query footprint pointed at the ASYNC_NETWORK_IO source (E21).
- S14 is the cheapest native next step for the 5–30 s range.

**Priority: Must.** It holds the most direct persisted test of the dominant stall signature (the host CPU split and the ruling-out of lock waits), with 70 days of retention already present. **Effort:** L. It needs a new driver, a new connection kind, an XE XML parser and the error-log parser. **Main risk:** permissions. `VIEW SERVER STATE` may be denied in production, as `sysjobs` was. Mitigation: the capability panel degrades per source and generates the exact `GRANT` request text for the DBA.

---

## 7. Phase P4: MongoDB native (audit server)

**Purpose.** Explain audit-insert stalls (E6, B6) and Mongo-side contention. Also check that Unravis's own queries are not a load source.

**Questions answered:**

- Was the audit write slow because of ticket queueing, cache eviction, checkpoints, locks, connection surges or replication?
- Which operations were slow at that time?

**Native sources (exact):**

| Source | What | Horizon | Notes |
|---|---|---|---|
| `serverStatus` (sections only: `connections`, `opLatencies`, `opcounters`, `globalLock`, `locks`, `wiredTiger` (cache, concurrentTransactions), `queues` (8.0+), `flowControl`, `mem`, `metrics.operation`) [R4] | Snapshot and delta (two snapshots, 10 s apart by default) | Live only | `clusterMonitor` role or `serverStatus` privilege |
| `getLog: "global"` [R2] | Last 1,024 log events in RAM; "Slow query" entries with `durationMillis`, `ns`, `planSummary`, `docsExamined`, `keysExamined` | Minutes to hours (RAM) | Entries truncated at 1,024 chars |
| **`mongod.log` file** (structured JSON in 4.4+) | The same slow-op entries, persisted; `slowms` default 100 ms, so a 28 s insert should be present [R1] | Until rotation `[unverified retention]` | Needs file access or an offline copy |
| **FTDC `diagnostic.data`** | ~1 s resolution `serverStatus`/`replSetGetStatus` history `[unverified]` | Size-capped (default ~200 MB) → typically days `[unverified]` | Undocumented binary format; parsing is the main risk |
| `$currentOp` (`allUsers` needs `inprog`; admin db) [R3] | Live long operations | Live | Bounded as in §3.5 |
| `system.profile` | Only if the profiler is already on | Capped at 1 MB by default [R1] | Read-only; never change the level |
| `$collStats`, `$indexStats`, `listIndexes` | Audit collection size, index use | Current | Cheap |
| `replSetGetStatus` / `rs` log lines | Elections, lag | Current / log | If a replica set |

**Features and reports:**

- Capability panel: version, role, profiler level, `slowms`, log path, FTDC enabled, replica-set state, Unravis `appName` present.
- **Server snapshot** (live): connections, operation latencies per class, ticket or queue state, cache fill and dirty percentages, app-thread evictions, lock acquire waits. Shown as a 10 s delta with raw counters visible.
- **Slow-op log** (`getLog` live, or an imported `mongod.log` file): filter by namespace, op type, duration and plan summary; group by `queryHash`/shape.
- **Audit-write lane:** slow `insert` entries on the `ApiAuditLog` namespace aligned with B6 episodes.
- **FTDC import** (phase stretch, behind a capability flag): a cache, ticket and checkpoint time series around the episode. If parsing proves too costly, fall back to `mongod.log` only (see the main risk under this phase's priority line).
- **Self-load panel:** Unravis's own ops, tagged by `appName`, and their `docsExamined`.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| M1 Slow audit write | `mongod.log` slow `insert` on the audit namespace ≥ 1,000 ms, with its end within ±2 s of a B6 request end | Strong correlation |
| M2 Ticket queueing | Δ`totalTimeQueuedMicros` / Δ`addedToQueue` ≥ 10 ms (8.0 `queues.execution`; 7.0 `concurrentTransactions` out ≈ total) [R9]; low "available" alone is **not** enough [R9] | Symptom |
| M3 App-thread eviction | cache used ≥ 95% or dirty ≥ 20% **and** Δ"pages evicted by application threads" > 0 `[unverified default triggers]` | Symptom |
| M4 Checkpoint pressure | FTDC/log checkpoint duration ≥ 60 s overlapping the episode `[unverified metric name]` | Correlation |
| M5 Collection scan on a large namespace | `planSummary: COLLSCAN` and `docsExamined` ≥ 100,000 | Symptom (also flags Unravis's own unpruned queries) |
| M6 Connection surge | `connections.current` ≥ 2× the 1 h baseline, or `available` < 10% | Symptom |
| M7 Lock acquire wait | Δ`timeAcquiringMicros` / Δ`acquireWaitCount` ≥ 10 ms on any lock type | Symptom |
| M8 Replication event | election or lag ≥ 10 s in the window | Correlation |
| **M9 Cold-start audit stall** [Rev 2, new] | B6 **and** the request is the first on that API host/app after an idle gap of ≥ 20 min, or follows a W1 recycle or a warm-up call (`WarmupApi`/`Test`). Escalates when `mongod.log` shows connection-accepted or authentication lines from the app's host in the same second as the stalled insert | Correlation (strong with the log lines). Driver connection set-up and server selection on a cold pool (E17; the driver's 30 s server-selection default is `[unverified]` for this app's driver) |

**Production safety.**

- `serverStatus` with excluded sections; two snapshots at most per user action.
- `getLog` is cheap.
- `$currentOp` is filtered and limited.
- Never touch the profiler.
- File reads happen on copies.
- Set `appName=Unravis` in `internal/store/mongo.go` client options. It is missing today.

**Demo.**

- Tier 1: local Mongo 7 with `slowms` default. Run an insert loop on `ApiAuditLog` while a heavy unindexed `$group` and a large `insertMany` saturate the cache, which produces real slow-op log lines and `serverStatus` deltas.
- Tier 3: import the resulting `mongod.log`.
- Scenario: P2 B6 episode (audit insert 28 s) → Mongo tab → M1 shows the matching slow insert entry, with M3 (eviction) during it → ledger: *audit-store write stall, correlated with cache eviction*.

**Dependencies:** P1 (connection reuse), P2 (B6).

**Remaining gaps:**

- Historical server metrics depend on FTDC retention and parse feasibility.
- If neither FTDC nor the log is available for 10-22, the E6 cause stays a hypothesis. Preserving the evidence now matters (§0).

**Instrumentation:** none.

**[Rev 2] Diagnostic value: Medium, now built 8th.**

- The E6/E17 audit stalls (3 × about 28 s on 10-22) look like cold-start or connection set-up, not server contention. M9 plus the imported `mongod.log` tests this. The log must be preserved now (§0), which is a standing action and needs no phase.
- Live `serverStatus` adds little after the fact.
- The instance is shared with four `*_Live` client databases (`sql/out_mongo_probe.txt:2`). Every live call must stay inside the §3.7 limits, and `appName=Unravis` is mandatory.

**Priority: High → built 8th [Rev 2].** One known incident signature, a cheap connection, and it also polices Unravis's own load. **Effort:** M without FTDC, L with it. **Main risk:** FTDC parsing (undocumented). Mitigation: ship the log-based M1 first.

---

## 8. Phase P5: Scheduled work in SQL Server (Hangfire storage + SQL Agent)

**Purpose.** Explain clock-minute recurrence (E5, B3) by listing every schedule stored in SQL Server whose fire times match.

**Questions answered:**

- Which recurring Hangfire jobs or SQL Agent jobs are due at 14:45 Dubai?
- Did they run, and for how long?
- Were Hangfire servers alive?

**Native sources (exact):**

- **Hangfire SQL storage (schema `HangFire`, Hangfire 1.8.9) [R14]:**
  - `Hash` keys `recurring-job:{id}` with fields `Cron`, `TimeZoneId`, `Queue`, `LastExecution`, `NextExecution`, `LastJobId` `[unverified field list]`;
  - `Set` key `recurring-jobs`;
  - `Job` (`Id`, `StateName`, `InvocationData`, `CreatedAt`, `ExpireAt`);
  - `State` (`JobId`, `Name`, `Reason`, `CreatedAt`, `Data` JSON: Processing `StartedAt`, Succeeded `PerformanceDuration`, `Latency` `[unverified]`);
  - `Server` (`Id`, `Data`, `LastHeartbeat`);
  - `JobQueue` (`Queue`, `FetchedAt`);
  - `AggregatedCounter` (`stats:succeeded:yyyy-MM-dd-HH` hourly) `[unverified key format]`.
  - Retention: succeeded jobs expire after ~1 day by default `[unverified]`. Run history is short, but cron definitions are permanent.
- **SQL Agent:** `msdb.dbo.sysjobs`, `sysjobschedules`, `sysschedules`, `sysjobhistory` (`run_date`, `run_time`, `run_duration` as integers), `sysjobactivity`. Currently **denied** (E9); requires `SQLAgentReaderRole` in msdb.

**Features and reports:**

- **Schedule calendar.** Expands each cron or Agent schedule into fire instants over ≤ 14 days, in the job's own time zone, converted to UTC. It is overlaid on the P2 recurrence grid.
- Recent runs: Hangfire Processing → Succeeded/Failed transitions (within retention), with duration; Agent job history with duration.
- Hangfire server heartbeat timeline and queue depth (live).
- Job-name drill-down to the `InvocationData` type/method name (no arguments by default: PII).

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| H1 Schedule matches recurrence | a cron or Agent schedule fires within ±2 min of a B3 clock window on ≥ 2 of the recurrence days | Correlation (candidate scheduled work) |
| H2 Run overlaps episode | a Hangfire or Agent run interval overlaps a stall episode | Correlation; strong correlation if the run ends within ±2 s of a B4 release instant |
| H3 Server heartbeat gap | `LastHeartbeat` older than 5 min, or `Server` row churn in the window | Symptom |
| H4 Queue backlog | `JobQueue` unfetched count ≥ 100, or the oldest age ≥ 10 min (live) | Symptom |
| H5 Failed burst | ≥ 5 Failed transitions in 10 min | Symptom |

**Production safety.**

- Reads go to small tables, with `READ UNCOMMITTED`, `TOP` and `LOCK_TIMEOUT`.
- Never touch `JobQueue` with locking hints that compete with workers. Use `NOLOCK` and keep reads short.
- Never trigger, delete or requeue jobs.

**Demo.**

- Tier 1: a local Hangfire SQL schema, created by running a tiny Hangfire sample once or from the published install script `[unverified]`, seeded with recurring jobs (`45 10 * * *` UTC) and some Processing/Succeeded states.
- Tier 2: the generator overlays the same schedule.
- Scenario: P2 recurrence at 14:45 Dubai → Schedules tab → H1: "recurring-job `ReportPackScheduler` cron `45 10 * * *` (UTC) fires 14:45 Dubai daily" (illustrative name) → ledger: *scheduled job candidate; needs run-overlap evidence (retention short)*.

**Dependencies:** P3 (SQL connection), P2 (recurrence).

**Remaining gaps:**

- Run history beyond about 1 day. Mitigation: the audit log's own Hangfire-kind rows already carry long-retention durations (P2 control), so no instrumentation is needed.
- The Agent permission grant.

**Instrumentation:** none.

**[Rev 2] Diagnostic value: High, if the burst process is SQL-scheduled.**

- B13 bursts recur on days with no traffic, which points to scheduled work.
- P5 names Hangfire and Agent candidates at 11:10, 13:10, 14:40–15:00, 18:35 and 19:05 Dubai, and at 06h.
- Mind the SQL server's UTC+1 clock (E22) when expanding Agent schedules.
- If no SQL-side schedule matches, the candidate moves to Task Scheduler or AV (P6) or the third-party monitor (E21; §24 decision 4).

**Priority: High.** It directly tests the strongest recurring signature, and it is small. **Effort:** S–M. **Main risk:** Hangfire schema or version differences in field names. Mitigation: the schema census, the same pattern as P1.

---

## 9. Phase P6: Windows host (Event Log, Task Scheduler, performance counters)

**Purpose.** Host-level explanations: recycles, crashes, reboots, AV scans, updates and scheduled tasks. Name the "other process" when a counter log exists.

**Questions answered:**

- Did the app pool recycle or crash?
- Did the host reboot?
- Did Defender scan, or Windows Update install?
- Which scheduled task fires at 14:45?
- Which process used the CPU on the DB host?

**Access model [Rev 2: live-first; replaces "offline-first"; channel per client is §24 decision 6].**

- **Primary: WinRM/PowerShell remoting to a JEA endpoint** (§3.7). The endpoint exposes only:
  - `Get-WinEvent` with a fixed `FilterHashtable` (logs and IDs from the table below, a time window, `-MaxEvents`);
  - `Get-ScheduledTask` / `Get-ScheduledTaskInfo`;
  - `Get-Counter` with a fixed counter list, `-SampleInterval ≥ 15`, `-MaxSamples ≤ 120`;
  - `Get-ChildItem` / `Get-Item` on allowlisted folders (`bin` write times, log folders);
  - `Get-Content -Tail` on allowlisted log files.
- **Fallback: native RPC tools** from the engineer's machine: `wevtutil qe /r:<host>`, `typeperf -s <host>`, `schtasks /query /s <host> /v /fo csv`. These need Event Log Readers, Performance Monitor Users, Remote Registry, and RPC ports.
- **Last resort: offline import** of the same data: `wevtutil epl` / `.evtx`, `schtasks` CSV, `relog` CSV of a DCS. Unravis parses `.evtx` by shelling out to the engineer's own `wevtutil qe <file> /lf:true /f:xml`, with no third-party parser. It produces the same `Observation` records (`origin: import`).
- The tool stays a local exe serving on loopback only. Nothing is installed on hosts. The JEA endpoint and any DCS are created by ops.
- **[Rev 3] Optional fourth executor: an on-server node** (§3.9), only under gate N. It runs **the same allowlisted packs**: the triggered per-process sampler, the counters, the Event Log reads and the change markers. It emits the same `Observation` records with `origin: node`. P6 itself is **not** node-based. It ships with JEA, RPC and import. The node is the separate optional release N1.

**[Rev 2] When the time-boxed collector is needed.**

- Use it when S4/B13 fire and W7 has no counter data. Here that is now (E15).
- The windows, counters, size cap and justification are in §3.8.
- The first capture is a DCS created by ops (unattended for 5 working days). After that, the live counter watch (`Get-Counter` via JEA, ≤ 30 min) covers predicted windows on demand.
- It is not needed for the chronic step (E18).

**Native sources (exact).** Event IDs not confirmed here are marked.

| Log / provider | Event IDs | Meaning |
|---|---|---|
| System / WAS | 5186 (idle shutdown, normal), 5117 (memory-limit recycle), 5011 (fatal communication error with worker) [R12]; 5074/5079/5080 periodic/config/other recycle `[unverified]`; 5002/5013 `[unverified]` | App pool lifecycle. Recycle logging depends on `logEventOnRecycle` `[unverified defaults]`. The default periodic restart is 1,740 min `[unverified]`, so it drifts across clock times and cannot create a same-minute daily pattern. A `schedule`-based recycle can. |
| System | 6005/6006 (Event Log start/stop), 6008 (unexpected shutdown), 1074 (initiated restart), Kernel-Power 41 | Reboots |
| System / Service Control Manager | 7036 (service state change), 7031/7034 (service crash) | Service restarts (including SQL and Mongo services) |
| System / disk, storport, storahci | 153 (I/O retried), 129 (reset) `[unverified IDs]` | Storage stalls |
| System / Resource-Exhaustion-Detector | 2004 | Low virtual memory |
| System / Kernel-General | 1 (system time changed) `[unverified]` | Clock jumps (correlation hygiene) |
| System / WindowsUpdateClient | 19 (install success), 20 (failure) `[unverified]` | Change history |
| Application / MsiInstaller | 11707, 1033 `[unverified]` | Change history (installs) |
| Application / .NET Runtime, Application Error, ASP.NET | 1026, 1000, 1309 | Crashes and unhandled exceptions in w3wp |
| Microsoft-Windows-Windows Defender/Operational | 1000/1001 (scan start/finish) `[unverified]` | AV windows |
| Microsoft-Windows-TaskScheduler/Operational | 100/102 (task start/complete), 200/201 (action start/complete) | Task runs. **History is often disabled by default** `[unverified]`; enabling it is an ops change. |
| `schtasks /query /v /fo csv` | Static triggers, next and last run, last result | Schedules without history |
| Performance counters | `Process(*)\% Processor Time`, `Process(*)\IO Data Bytes/sec`, `Processor(_Total)\% Processor Time`, `Memory\Available MBytes`, `PhysicalDisk(*)\Avg. Disk sec/Read\|Write`, `System\Processor Queue Length` | **Not persisted by default.** Read live via PDH or `typeperf`, or import a time-boxed Data Collector Set (`logman create counter … -si 15 -b … -e …`), created by ops |

**Features and reports:**

- An event timeline per host with a fixed curated ID allowlist (above). Unknown IDs are counted but not interpreted.
- Recycle and crash lane per app pool.
- Change lane: updates, installs, service installs (7045 `[unverified]`), and file write times of deployed `bin` folders. The file times come from a read-only directory listing on a share, or are imported. This is the evidence for B9 step dating.
- Scheduled-task calendar: static triggers expanded to fire instants. Run history is shown when it exists.
- Counter view: live 15 s sampling for ≤ 15 min (on demand), or an imported DCS CSV. "Top processes by CPU" per sample.
- **Generated DCS command** for ops: time-boxed to a recurrence window (e.g. 14:30–15:15 Dubai, 5 days), with an explicit size cap.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| W1 Pool lifecycle | WAS recycle or crash event within ±5 min of an episode or step | Correlation |
| W2 Reboot or service restart | 6008/1074/41/7036 (SQL, Mongo, W3SVC, app services) within ±10 min | Correlation |
| W3 Task due at recurrence | a static trigger fires within ±2 min of a B3 window on ≥ 2 recurrence days | Correlation (candidate) |
| W4 AV scan overlap | Defender scan interval overlaps the episode | Correlation |
| W5 Change near step | an update, install or `bin` write within ±24 h before a B9 step | Correlation (change candidate) |
| W6 Storage warning | disk/storport warnings in the window | Symptom |
| W7 Named CPU consumer | one non-SQL process ≥ 30% of host CPU for ≥ 3 samples inside a S4 interval (needs a counter log) | Strong correlation for S4 (it names the process) |
| W8 Clock jump | a time-change event in the window → widen correlation tolerance and warn | Data quality |
| **W9 Change marker** [Rev 2, new] | Any of these within the B9/L2/L5 step window (10-07 16:00 ±24 h; 09-21 ±24 h): a `bin` or `web.config` write time, an MSI or update install, a service install, or an IIS config change (`applicationHost.config` write time) | Correlation (deploy candidate for the chronic step; there are no deploy markers natively, §0) |

**Production safety.**

- Read-only queries and exports.
- No subscriptions, no log clearing, no history enablement and no DCS creation by the tool.
- Live counter sampling: ≤ 15 min, 15 s interval, a single counter set, on demand. **[Rev 2]** The limit rises to ≤ 30 min (60 min with confirmation), under the §3.7 kill-switch and back-off.

**Demo.**

- Tier 1: the engineer's own machine. Real System and Application logs; a local scheduled task set for 2 min ahead that runs a CPU burner; a `logman` DCS (created by the demo operator) capturing it.
- Tier 3: import a `.evtx` export.
- Scenario: P3 S4 ("other CPU 38% 14:45–14:52") → Windows tab → W3: task "NightlyExport" `[illustrative]` trigger 14:45 → DCS import → W7: `someprocess.exe` 36% → ledger: *other-CPU explained (strong correlation)*.

**Dependencies:** P2/P3 windows. Host access granted by ops.

**Remaining gaps:**

- Without a DCS during a recurrence, the CPU consumer stays unnamed (G3).
- Task history may be disabled.

**Instrumentation:** none. A DCS is native configuration that ops create, scoped and time-boxed.

**[Rev 2] Diagnostic value: Highest of the not-yet-built phases.**

- It is the only native source that can **name the burst process** (W7; L4).
- It can date the chronic step through change markers (W9; L2).
- It can explain a cold start through WAS recycle events (W1; L3).
- It fills the topology through the host list and the services present.

**Priority: Must; built 4th [Rev 2, moved up from 6th].** E10/E11/E15 point at host contention, and recurrence can come from Task Scheduler or AV. **Effort:** M. **Main risk:** access and permissions to production hosts. Mitigation: JEA, then RPC, then offline import (§3.7).

---

## 10. Phase P7: IIS W3C + HTTP.sys error logs

**Purpose.**

- Measure time outside the API pipeline.
- Find what never reached the app: queue drops, 503s and pool-abandoned connections.
- Find clients that gave up.

**Questions answered:**

- Did requests queue before the controller?
- Did HTTP.sys reject or drop requests?
- Did clients disconnect at a timeout?
- Are there API requests missing from the audit log?

**Native sources (exact):**

- **W3C logs** (`%SystemDrive%\inetpub\logs\LogFiles\W3SVC<id>\u_ex*.log`, UTC by default). Fields: `date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Referer) sc-status sc-substatus sc-win32-status time-taken`. Optional: `sc-bytes`, `cs-bytes`.
  - `time-taken` starts at HTTP.sys's first byte and ends at the last send. It includes network time, except for small (≤ 2 KB) or buffered responses, so slow clients inflate it [R10].
- **HTTP.sys** `%SystemRoot%\System32\LogFiles\HTTPERR\httperr*.log`. Reason phrases [R11]:
  - `Timer_ConnectionIdle`: benign, the 120 s idle close.
  - `Timer_AppPool`: the request waited too long in the app-pool queue.
  - `Connection_Abandoned_By_AppPool`: the worker died or recycled with requests in flight.
  - `Connection_Dropped`.
  - `QueueFull`.
  - `AppOffline`.
  - `Timer_EntityBody`.

**Features and reports:**

- Log import with a streaming parser. The `#Fields` header drives the columns.
- Per-minute volume, status mix (including substatus and `sc-win32-status`), and `time-taken` quantiles per URI stem.
- **Audit join.** Match IIS lines to audit rows by URI stem ↔ controller/action route map, status, and end time ±1 s. The W3C `time` field is assumed to be the request completion time `[unverified]`.
  - Per matched request: `outside = time-taken − observed`.
  - Unmatched API requests are listed as coverage gaps.
- httperr reason timeline, excluding `Timer_ConnectionIdle` by default (shown as a muted count).

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| I1 Time outside the pipeline | median `outside` ≥ 2,000 ms over ≥ 20 matched requests in a 5-min slot | Symptom (queueing, auth modules, or slow client; tells them apart only with P11 counters) |
| I2 Pool queue timeouts | ≥ 1 `Timer_AppPool` or `QueueFull` in the window | Symptom (requests waited in the app-pool queue) |
| I3 Pool abandoned | ≥ 1 `Connection_Abandoned_By_AppPool` → join to W1 | Correlation with a recycle or crash |
| I4 503 burst | ≥ 5 `503` responses per minute (substatus shown) | Symptom |
| I5 Client gave up | `sc-win32-status` 64 or 1236 with `time-taken` ≥ 30 s; join with B10 | Symptom |
| I6 Audit coverage gap | ≥ 5% of IIS API requests in a slot have no audit match | Data quality |

**Production safety.** Files only (copies), parsed locally. Window-limited reads skip irrelevant daily files by name.

**Demo.**

- Tier 1: local IIS or IIS Express hosting a tiny app with a `Thread.Sleep` endpoint, plus an app-pool queue length set to 10 and a load burst. This produces real 503.2 and `QueueFull` entries `[unverified exact httperr reason for queue full]`.
- Scenario: P2 B10 timeout pile-up at 110 s → IIS tab → I5: 37 client disconnects at 110–112 s → I1 median outside 0.3 s → *the time is inside the app; IIS queueing ruled out (counter-evidence)*.

**Dependencies:** P1/P2. File access (the same model as P6).

**Remaining gaps:**

- The exact semantics of the W3C `time` field `[unverified]`.
- A route map from URI to controller/action. It can be derived from audit `Url` fields, which the MVP already holds.

**Instrumentation:** none.

**[Rev 2] Diagnostic value: Medium.**

- **L3 cold start.** The W3C lines of the 10-22 14:09 `WarmupApi`/`Test` call and the 3 Sage50 calls show whether the warm-up preceded the stalled calls. httperr `Connection_Abandoned_By_AppPool` and W1 show whether the pool had just started.
- **L1.** It confirms whether the volley members arrived as separate HTTP requests within 1 s (a client fan-out or retry) and whether `time-taken` exceeds `ApiTime` (queueing ahead of the app; class 9/15).
- **6 never-completed requests.** It tests the hypothesis that they had a client disconnect (I5).

**Priority: Medium-High.** Valuable counter-evidence; it confirms or rules out pipeline queueing cheaply. **Effort:** M. **Main risk:** join ambiguity at high request rates. Mitigation: match on (stem, status, end ±1 s, nearest duration) and report the match rate.

---

## 11. Phase P8: Cross-source correlation timeline

**Purpose.** Put the individually useful findings from P2–P7 on one clock and climb the evidence ladder explicitly.

**Questions answered:**

- For this episode, which observations from every source line up?
- Which hypotheses do they support or refute?
- What evidence is still missing?

**Sources.** Findings and observations from P2–P7. Later phases plug in as they ship.

**Features and reports:**

- **Episode workspace.** Lanes per source on a shared UTC axis, with display time zone and skew-corrected:
  - audit (requests, phases, clusters);
  - SQL (waits, deadlocks, CPU split, errors);
  - Mongo (slow ops, tickets);
  - schedules (Hangfire, Agent, Task Scheduler);
  - Windows events;
  - IIS/httperr.
- **Composite rules (below).** Each is the AND of single-source findings and inherits their thresholds.
- **Ledger integration.** Composite findings attach to hypotheses as supporting or refuting. Refutations count as much as support (e.g. "no backup overlap", "Hangfire unaffected", "IIS outside-time 0.3 s").
- Banner on every view: "Same time window. Correlation, not attribution." This keeps the old Task 15 banner, generalized. Strong correlation and proven cause are labelled explicitly.
- "Missing evidence" panel: for each open hypothesis, the capability rows that are denied or absent, with remedies (from §3.2).
- **[Rev 6] Theory checks (P2b, §5A).** The C-rules are registered as tests that theories can name as predictions or falsifiers (for example TH8 uses C2, TH13 uses C4/C10, TH10 uses C9). Ledger integration shows the P2b verdict for each theory, with its label and tests. Composite findings never set a status by themselves. The "Missing evidence" panel reads each theory's `required_evidence`. A ledger move such as "supported (strong correlation)" in the demo below is the P2b verdict function at work, not a hand-set status.

**Composite rules:**

| Rule | Composition | Label |
|---|---|---|
| C1 Lock release explains cluster [Rev 2: depends on S1, which moved to Later; build with S1] | B4 + S1 (end ±2 s, duration ±2 s) | Strong correlation |
| C2 Schedule explains recurrence | B3 + (H1 or W3), on ≥ 2 recurrence days | Correlation |
| C3 Scheduled run blocks | C2 + H2 (run overlaps) + S2 (lock wait in the run window) | Strong correlation |
| C4 Change explains step | B9 + (S10 or W5 or a deploy annotation) within 24 h before | Correlation |
| C5 Audit store explains audit stall | B6 + M1 (+ M2/M3/M4) | Strong correlation |
| C6 Host contention | S4 + W7 | Strong correlation |
| C7 Pool event explains failures | I3 + W1 | Strong correlation |
| C8 Narrowing | B8 (Hangfire unaffected) + I1 negative → stall inside the sync request path, after the pipeline | Correlation (scope reduction) |
| **C9 CPU burst explains volley** [Rev 2, new] | B12 + (B13 or S4 overlapping the volley) | Correlation. **Strong correlation** when W7 names the process for that interval |
| **C10 Change explains chronic step** [Rev 2, new] | B9 (10-07/08) + W9 or S10, and **not** S4 (the step persists in low-CPU minutes, E18) | Correlation |

"Proven cause" is only reachable with request-scoped linkage: App Insights `operation_Id` in P9, or G1. Otherwise C-rules cap at strong correlation.

**Production safety.** No new queries. Composition runs over already-fetched, bounded observations.

**Demo.** Tier 1/2: combine the P3 blocking demo, the P5 schedule and the P6 task. Scenario: open the "10-13 14:45 Dubai" episode → C2 + C1 fire → ledger "Scheduled report job holds a lock for ~75 s" moves to *supported (strong correlation)*, with the proof gap stated.

**[Rev 2] Revised demo.** The lead scenario is C9: a volley plus a burner-task burst plus a DCS naming the process. The C1/C2 scenario stays secondary.

**[Rev 2] Diagnostic value: High as a multiplier.** It adds no new evidence, but it is where the 4 problems in §0 are kept apart rather than merged.

**Dependencies:** P2 + at least P3; better with P5–P7. **[Rev 6]** Also P2b, which supplies the verdict function and the ledger contract. **Remaining gaps:** proof needs request-scoped IDs (P9 or G1). **Instrumentation:** none.

**Priority: High** once ≥ 3 sources are useful. **Effort:** M. **Main risk:** visual overload. Mitigation: the D0–D3 progressive-depth UI spec (episode → lane → observation → raw).

---

## 12. Phase P9: Application Insights (per client, optional; capability-detected) [Rev 2, revised]

**Purpose.** If production telemetry already exists, use its per-request dependency timings. This is existing evidence, so it is not new instrumentation.

**Questions answered:**

- Which dependency call (SQL statement, HTTP, Redis) took the time inside a slow request?
- How much time is unattributed?
- Which exceptions spiked?

**[Rev 2] Per-client capability detection (replaces the revision-1 gate).** App Insights is in production for about 50% of clients. Each client profile runs the §3.7 probe:

1. An Entra ID token is obtained for the engineer, and the workspace is resolvable.
2. `AppRequests | where TimeGenerated > ago(1d) | take 1` returns a row. Otherwise the result is "configured but silent".
3. Sampling: `summarize avg(itemCount)` over the window. Above 1, every count is an estimate (AI5).
4. Dependency SQL text present: is `Data` non-empty on SQL dependencies?
5. `cloud_RoleName` / `cloud_RoleInstance` values. They auto-fill the host topology (§24 decision 1) and give API host identity per request.

The result is **live**, **import** (exported KQL JSON) or **absent** per client.

- **Every phase works without P9.** Without it, C-rules cap at strong correlation, and G1/G2 stay gated.
- With it, AI2 can reach *proven for that sampled request*, and the per-user serialization question (class 15; L1 step 4) gets request-level evidence: overlapping operations of one user and their dependency gaps.

**Placement:** built 6th overall, but **run first in an investigation** for a client where the probe says live. Code shows placeholders for this repo's configuration (E13), which is consistent with "per client".

**Native sources (exact):**

- Tables (workspace-based, KQL): `AppRequests`/`requests` (`duration`, `resultCode`, `operation_Id`, `operation_Name`, `itemCount`), `AppDependencies`/`dependencies` (`type`, `target`, `name`, `data` = SQL command text, enabled in .NET 10 via `EnableSqlCommandTextInstrumentation`, `duration`, `success`), `AppExceptions`, `AppTraces`.
- `performanceCounters` is disabled in .NET 10 (`EnablePerformanceCounterCollectionModule=false`, E13).
- Query API: Log Analytics query API via an Entra ID token `[unverified which API applies; the classic App Insights REST API is being retired `[unverified]`]`.
- Sampling: adaptive at 5 items/s, per type (E13). The SDK samples whole operations together, so a sampled request keeps its dependencies `[unverified for the dual-processor setup]`. `itemCount` gives the weight.

**Features and reports:**

- Request waterfall per sampled slow request: dependencies on a time axis, with the SQL text shown (sanitized, literals masked by default).
- **Unattributed time** = request duration − union of dependency intervals. This is SpikeHunter's time-budget gap idea, adopted here because real sub-timings exist. It is a metric, not a verdict.
- Dependency leaderboard for a slow subset vs baseline (target, command hash), with Δ duration × count impact.
- Exception lane: `RedisTimeoutException` (its message includes `WORKER`/`IOCP` busy and min/max thread counts, a free thread-pool signal `[unverified format]`), `SqlException` numbers (-2 timeout, 1205 deadlock victim).
- Join to audit rows by route plus time ±1 s (no shared ID). Show the match rate.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| AI1 Dependency-bound | Σ union(dependency) / request ≥ 0.7, request ≥ 2 s | Symptom (where the time went) |
| AI2 Single dependency ≈ request | one dependency duration within ±1 s of the request duration | Proven cause *for that sampled request* (same `operation_Id`) |
| AI3 Unattributed | unattributed ≥ 50% and ≥ 2 s | Symptom (in-process: CPU, locks, thread pool, GC); routes to P11 |
| AI4 Exception spike | ≥ 5/min of one exception type, ≥ 3× baseline | Symptom |
| AI5 Sampling honesty | `itemCount` > 1 → counts are estimates; a never-sampled episode → "no evidence", not "no problem" | Data quality |

**Production safety.**

- Read-only KQL with `take`/`summarize` limits and a time window of ≤ 24 h per query.
- Credentials are stored with DPAPI.
- Outbound HTTPS only. It does not affect loopback serving.
- SQL text masked by default (PII).

**Demo.**

- Tier 1: a local .NET 10 sample with the App Insights SDK sending to a dev resource is possible, but needs an Azure subscription `[unverified availability]`.
- Fallback tier 3: import an exported query result as JSON or CSV.
- Scenario: B4 cluster member → matched sampled request → AI2: one SQL dependency of 74.8 s → SQL text names the `ReportConfig` read → *proven for that sampled request*; C1 lifts to proven for the matched request.
- **[Rev 2] Lead scenario.**
  - A B12 volley member is matched to its sampled request.
  - AI3 fires: about 55 s unattributed, with dependencies only at the end.
  - The second member's dependencies start only after the first member's last dependency. This supports per-user serialization (class 15).
  - Shown with and without P9, to demonstrate degradation.

**Dependencies:** a per-client probe that returns live or import; P2. **Remaining gaps:** sampling may miss rare stalls. That is where G2 becomes justifiable. **Instrumentation:** none. Turning off sampling for slow requests would be configuration (G4) and is not proposed now.

**[Rev 2] Diagnostic value: Very high per client where live.** It is the only native request-scoped source (it answers G1/G2 for sampled requests). Elsewhere it adds nothing.

**Priority: High, per client [Rev 2].** It moves up from "conditional, after P3" to 6th in the build order: after P6 and P5, which serve every client, and before P7. **Credentials:** an Entra token for the engineer, with no client secret stored (§3.7). **Effort:** M. **Main risk:** the access model (Entra ID) and sampling bias.

---

## 13. Phase P10: Redis native

**Purpose.** Rule Redis in or out as the shared block or latency source.

**Questions answered:**

- Did Redis have slow commands, fork stalls, evictions, blocked or rejected clients, or memory pressure?

**Native sources (exact):**

- `INFO` sections: `server` (version, uptime), `clients` (`connected_clients`, `blocked_clients`, `maxclients`), `memory` (`used_memory`, `maxmemory`, `mem_fragmentation_ratio`), `persistence` (`rdb_last_bgsave_status`, `rdb_last_bgsave_time_sec`, `aof_rewrite_in_progress`), `stats` (`instantaneous_ops_per_sec`, `rejected_connections`, `evicted_keys`, `expired_keys`, `keyspace_hits/misses`, `latest_fork_usec`), `commandstats` (`usec_per_call`), `latencystats` (7.0+).
- `SLOWLOG GET 128`: `slowlog-log-slower-than` defaults to 10,000 µs and `slowlog-max-len` to 128 `[unverified for the deployed version]`.
- `LATENCY LATEST` / `HISTORY`: only if the monitor is already enabled. It is off by default (`latency-monitor-threshold 0`); enabling it is `CONFIG SET`, which is never done by the tool [R13].
- `CLIENT LIST`.
- `CONFIG GET maxmemory*|save|appendonly|slowlog*`. These may be renamed or blocked, for example on Azure Cache.
- Deployment type unknown (self-hosted on Windows, Memurai, Azure Cache) `[unverified]`. Azure Cache blocks some admin commands but provides Azure Monitor metrics.

**Features and reports:**

- Capability panel: version, deployment hints, which commands are allowed, slowlog length and threshold, latency monitor on or off.
- Snapshot plus a 10 s delta of `INFO` counters.
- Slowlog table: command (arguments truncated and masked), duration, time, client name.
- **Watch window.** A user-started, bounded sampler: `INFO` every 10 s for ≤ 30 min (default), intended for a *predicted* recurrence window from B3. This is not speculative collection: it is scoped to a hypothesis.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| R1 Slow command | slowlog entry ≥ 10 ms in the window; ≥ 100 ms flagged | Symptom |
| R2 Fork stall | `latest_fork_usec` ≥ 500,000 or a bgsave started in the window | Correlation |
| R3 Evictions | Δ`evicted_keys` > 0 | Symptom |
| R4 Client pressure | `blocked_clients` > 0, Δ`rejected_connections` > 0, or `connected_clients` ≥ 80% of `maxclients` | Symptom |
| R5 Fragmentation | `mem_fragmentation_ratio` ≥ 1.5 or < 1.0 | Symptom |
| R6 Hit-ratio drop | hit ratio drops ≥ 20 points vs the watch baseline | Symptom |

**Production safety.**

- Allowlist: `INFO`, `SLOWLOG GET`, `LATENCY LATEST|HISTORY`, `CLIENT LIST`, `CLIENT SETNAME`, `CONFIG GET`.
- Never `KEYS`, `MONITOR`, `CONFIG SET`, `DEBUG`, `SAVE`/`BGSAVE`, `FLUSH*`, `SLOWLOG RESET` [R5][R6].
- Big-key analysis via `SCAN` + `MEMORY USAGE` is **Later**, opt-in only, with `COUNT` ≤ 100 and a key cap.

**Demo.** Tier 1: local Redis. Load a large hash, run `HGETALL` on it (slowlog entry), trigger `BGSAVE` from the *demo script* (not Unravis) to show R2, and set a low `maxmemory` to show R3.

**Dependencies:** a connection kind (DPAPI). **Remaining gaps:** no history. Past incidents can only be judged through app exceptions (P9/P12). **Instrumentation:** none.

**[Rev 2] Diagnostic value: Low here.**

- Redis is not implicated by any E1–E23 evidence.
- It is useful only as counter-evidence in L1 (a slow log or fork during a volley).
- It is a live watch only. Connection: ACL user + TLS (§3.7, §1A.E).

**Priority: Medium.** Confirmed dependency, but weak retrospective value. **Effort:** S. **Main risk:** command restrictions on managed Redis.

---

## 14. Phase P11: .NET runtime (split: P11a Framework on IIS, P11b .NET 10)

**Purpose.** Tell in-process causes apart: thread-pool starvation, GC, lock contention, request queueing. Use it when P9 AI3 or P7 I1 points inside the process.

**Why split.** The diagnostics differ fundamentally:

- Framework exposes **Windows performance counters**, readable remotely by PDH and loggable by a DCS.
- .NET 10 exposes **`System.Runtime` meters**: in-process, live, and read externally via EventPipe with `dotnet-counters`. They are not persisted [R15].

**P11a sources (.NET Framework 4.7.2 / 4.5.2 under IIS) [R16]:**

- `ASP.NET\Requests Queued`, `ASP.NET\Request Wait Time`, `ASP.NET\Requests Rejected`, `ASP.NET Applications(__Total__)\Requests Executing`;
- `HTTP Service Request Queues(*)\CurrentQueueSize`, `RejectedRequests` `[unverified counter-set name]`;
- `.NET CLR Memory(w3wp*)\% Time in GC`, `# Gen 2 Collections`;
- `.NET CLR LocksAndThreads(w3wp*)\Contention Rate / sec`, `Current Queue Length`;
- `Process(w3wp*)\% Processor Time`, `Thread Count`, `Private Bytes`.
- Instance ambiguity (`w3wp#1`) needs a `Process\ID Process` mapping. There is no thread-pool queue counter on Framework `[unverified]`.

**P11b sources (.NET 10):**

- `System.Runtime` meter [R15]: `dotnet.thread_pool.queue.length`, `dotnet.thread_pool.thread.count`, `dotnet.gc.pause.time`, `dotnet.monitor.lock_contentions`, `dotnet.exceptions`, `dotnet.process.cpu.time`.
- ASP.NET Core and Kestrel meters (`http.server.active_requests`, `kestrel.queued_connections`) `[unverified names]`.
- Read via `dotnet-counters collect --format csv` run by ops on the host, then imported. `dotnet-counters` is a separate install on production, so it needs ops approval.

**Features and reports:**

- Imported or live counter series aligned to episodes, using the same viewer as P6.
- Generated DCS command for P11a, time-boxed to the recurrence window and combinable with the P6 DCS.
- P11b import of the `dotnet-counters` CSV.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| T1 ASP.NET queueing | `Requests Queued` > 0 for ≥ 3 samples, or `Request Wait Time` ≥ 1,000 ms | Symptom |
| T2 GC pressure | `% Time in GC` ≥ 20% for ≥ 3 samples (Framework), or `gc.pause.time` Δ ≥ 200 ms/s (.NET 10) | Symptom |
| T3 Thread-pool starvation (.NET 10) | `thread_pool.queue.length` > 0 for ≥ 3 samples while `thread.count` rises steadily (injection signature) | Symptom |
| T4 Lock contention | contention rate ≥ 3× baseline | Symptom |
| T5 HTTP.sys queue | `CurrentQueueSize` > 0 for ≥ 3 samples; joins with I2 | Strong correlation with I2 |

**Production safety.**

- Counter reads are lightweight.
- `dotnet-counters` has low overhead but runs on the host.
- Never dump or trace (§3.5).

**Demo.** Tier 1: a local sample app (Framework on IIS Express, .NET 10 on Kestrel) with a sync-over-async endpoint under load, which produces real thread-pool starvation (T3) and ASP.NET queueing (T1).

**Dependencies:** P6 access model; P7/P9 to point inward. **Remaining gaps:** no history unless a DCS was running (G4). **Instrumentation:** none.

**[Rev 2] Diagnostic value: Medium, live only.**

- It becomes useful **after** P6 has named or bounded the burst process, as the next step in L1 when the API host's thread pool or GC is suspected (class 9).
- The ASP.NET/CLR counters already sit in the §3.8 API-host collector, which covers most of P11a natively.

**Priority: Medium (later).** Live only; most useful once a recurrence can be predicted. **Effort:** M (a: S–M, b: M). **Main risk:** counter-instance mapping, and production tool approval for P11b.

---

## 15. Phase P12: Application logs (log4net)

**Purpose.** Use app-written logs where they actually exist.

**Questions answered:**

- What did the scheduled worker or service do at 14:45?
- Which exceptions occurred during episodes?

**Gate.** In the repository, the APIs' log4net `<root>` and Elasticsearch appender are commented out (E14). Only `BrainPayrollWindowsServices` has an active `RollingFileAppender`. Production config transforms are unknown `[unverified]`. Start with the **Windows Services log**, which may be the scheduled worker behind E5 `[hypothesis]`.

**Native sources.**

- Rolling log files. The layout comes from the configured `conversionPattern`: the parser is generated from the pattern, with fields such as `%date`, `%thread`, `%level`, `%logger` and `%message`.
- Elasticsearch indexes, if production enables the appender (read via `_search` with a time-range query, `size` ≤ 1,000).

**Features and reports:**

- Pattern-driven parser.
- Level and logger timeline.
- Exception-type grouping (first line plus type), aligned to episodes.
- "Job start/end" extraction by user-defined regex pairs, saved per source, to produce intervals for P8.

**Deterministic rules:**

| Rule | Condition (defaults are tunable) | Label |
|---|---|---|
| L1 Error burst | ≥ 10 ERROR/min, ≥ 3× baseline | Symptom |
| L2 Known exception | `SqlException` -2/1205, `RedisTimeoutException`, `TimeoutException`, `TaskCanceledException` in an episode | Symptom |
| L3 Service run overlaps | an extracted job interval overlaps an episode or matches B3 recurrence | Correlation |
| L4 Log gap | no lines for ≥ 10 min where the baseline is ≥ 10/min | Data quality |

**Production safety.** Files are copied and parsed locally. Elasticsearch queries are bounded. Messages are PII-masked by default (emails, national-ID patterns, tokens).

**Demo.** Tier 1: a log4net sample writing with the same pattern as `BrainPayrollWindowsServices`, including a scheduled job that logs start and end at a fixed minute.

**Dependencies:** P2, P8. **Remaining gaps:** whether production writes any API logs. **Instrumentation:** none. Re-enabling log4net in the APIs would be a configuration change for the app team, listed as G5, not proposed by default.

**[Rev 2] Diagnostic value: Low to Medium.** It may hold Mongo driver connection and timeout errors for L3 and the volley user's request sequence for L1 `[unverified: depends on log4net level and content]`. Files are read through the SMB share or JEA (§3.7).

**Priority: Conditional Medium.** **Effort:** S–M. **Main risk:** varied layouts and PII.

---

## 16. Phase P13: Evidence API, case export and MCP consolidation

**Purpose.**

- A stable, versioned, machine-readable evidence surface for external AI clients and scripts.
- Portable investigation cases.
- No diagnosis inside the tool.

**Features:**

- **REST:**
  - `GET /api/evidence/capabilities`
  - `GET /api/evidence/findings?episode|window&sources`
  - `GET /api/evidence/observations?source&kind&window` (paged, capped)
  - `GET /api/evidence/episodes`
  - These are loopback-only and token-gated, reusing `internal/server/security.go`.
- **Schema:** the §3.1 model, with `schema_version`, rule ID and version, the thresholds applied, and labels.
- **Case export:**
  - a ZIP with `manifest.json` (sources, windows, capability snapshot, tool version), findings, observations and the ledger;
  - **pseudonymised** user, account and IP values (keyed HMAC with a per-case key, kept only locally, so the same value maps to the same token across a case);
  - SQL text and log messages masked.
- **MCP.** Add 4 generic read-only tools next to the existing 10 audit tools (`internal/mcp/tools.go:95`):
  - `evidence_capabilities`
  - `evidence_findings`
  - `evidence_observations`
  - `evidence_episode`
  - The existing guide (`internal/mcp/schema.go:5`) gets an "evidence ladder" section: labels, no attribution beyond the label.
  - Keep the 1 MiB output cap and the cached-result model.
- **Not in scope:** AI diagnosis, auto-remediation, or raw query passthrough.
- **[Rev 6] Theory surface (from P2b, §5A).**
  - `GET /api/evidence/theories` and `GET /api/evidence/verdicts` join the REST list.
  - The schema adds the `Theory` and `Verdict` records (§5A.B).
  - The case ZIP embeds the P2b reproducibility bundle unchanged, so `unravis theory rerun` works on an exported case.
  - The four theory MCP tools from P2b (`theory_catalog`, `theory_propose`, `theory_run`, `theory_verdicts`; §3.10) stay as they are. The total becomes **18 tools**: 10 audit, 4 evidence and 4 theory. **[Rev 7]** With `theory_narrative_submit` (§5A.H) the total is **19 tools**: 10 audit, 4 evidence and 5 theory.
  - The guide's evidence-ladder section gains the §3.10 rules: cite verdict hashes and observation IDs; never restate a number that is not in a tool result. The narrative linter (§3.10) checks exported narratives.

**Rules:** none new. The API exposes existing findings verbatim.

**Production safety.** No new production queries. It serves cached and bounded observations. Fresh fetches go through each source's existing limits.

**Demo.** Claude Code via MCP runs on the demo episode, lists findings with labels, and cites observation IDs. The case export is opened on another machine with pseudonymised identifiers.

**Dependencies:** P2 + P3 at least; **[Rev 6]** P2b for the theory surface. **Priority: High** after P8 (it multiplies the value of all sources). **Effort:** M. **Main risk:** schema churn. Mitigation: version it from day one; additive changes only.

**Note on per-phase MCP.** P2 should still extend the *existing* audit MCP tools (stall catalog, clusters, recurrence) because the audit MCP already exists. Other sources wait for P13. **[Rev 2]** This is now a standing default rather than an open decision.

**[Rev 2] Diagnostic value: Indirect.** It adds no evidence. It lets external AI clients follow the §1A.C ladders without re-running ad-hoc scripts, which is the main thing that would have shortened the §1A.A path.

---

## 17. Feature classification

Must Have = needed for credible investigations of the known incident signatures. High Value = a strong return, but the roadmap works without it. Later = useful once prerequisites exist. Unnecessary/Low Value = cut or not worth the cost here.

| Feature | Phase | Class | Reason |
|---|---|---|---|
| `_id` range pruning | P1 | Must Have | Every query is a COLLSCAN today (E1); Unravis is a production load source |
| Schema-driven field resolution | P1 | Must Have | Prevents E3-class errors |
| PreActionFilter phase | P1 | High Value | Existing unused timing; nesting semantics unverified |
| `AccountId` dimension + lift | P1 | High Value | Tenant concentration (E4: 2 accounts) |
| Audit coverage report | P1 | High Value | The audit log's own gaps are evidence |
| Display time zone + dual stamps | P1 | Must Have | Every investigation note uses Dubai; data is UTC |
| 90-day trend extension (trend endpoints only) | P1 | Must Have | Step dating (E7) and E11 need > 7 days |
| Capability panel (per source) | P1+ | Must Have | Permissions and stores decide what is answerable (E8, E9) |
| Stall catalog | P2 | Must Have | E4 |
| Clock recurrence grid | P2 | Must Have | E5 |
| Release-cluster detector + slope + covering call | P2 | High Value [Rev 2, was Must] | Still the discriminator for a shared block, but the observed shape is a volley (E4/E16) |
| **Volley detector (B12)** [Rev 2] | P2 | Must Have | The observed stall shape (E16); separates per-user serialization from a shared block |
| **Host-CPU recurrence (B13)** [Rev 2] | P2/P3 | Must Have | 37/43 stalls sit in recurring bursts, including on zero-traffic days (E15) |
| Hangfire control differential | P2 | High Value | Scope narrowing (B8) |
| Step dating + movers | P2 | Must Have | E7 |
| Timeout pile-up | P2 | High Value | Cheap; separates ceilings from causes |
| Exact in-flight sweep | P2 | High Value | Precise load during episodes |
| Hypothesis ledger | P2 | Must Have | Carries labels and gaps across phases |
| **Day × hour heatmap + CSV export** [Rev 6] | P2 | High Value | The report's most-used view; A5 thin-cell masking; CSV pseudonymised by default |
| **Theory engine: typed theories, deterministic verdicts, V1–V5/V7–V9** [Rev 6] | P2b | High Value | Replaces hand-set statuses (§1B); falsifiers, base rates and multiplicity; byte-reproducible |
| **Source-sanity gate (V6)** [Rev 6] | P2b | Must Have | No verdict on a broken or flat counter (E19), on sampled days or on coverage gaps |
| **Incident report export (Markdown + self-contained HTML)** [Rev 6] | P2b | High Value | Replaces the hand-built `report.html` pipeline; methods appendix generated from the applied rule versions |
| **Reproducibility bundle + `theory rerun`** [Rev 6] | P2b | High Value | Byte-identical verdicts elsewhere; embedded in the P13 case ZIP |
| **Theory MCP tools (proposals only)** [Rev 6] | P2b | High Value | External LLM can suggest; the engine decides (§3.10) |
| **Theories screen: verdict rows + inspector** [Rev 7] | P2b | Must Have (for R2b) | The report's best section, made deterministic (§5A.H); without it the verdicts have no home |
| Optional LLM wording toggle [Rev 7] | P2b | Later / optional | Off by default; template text is complete on its own |
| Sensitivity sweep over threshold grids [Rev 6] | P2b+ | Later | Stability check once real client verdicts exist |
| Compare any two days or periods (UI) [Rev 6] | P2 | Later | Convenience; regime tests already do the formal comparison |
| Likelihood/posterior ranking of theories [Rev 6] | n/a | Unnecessary | Opaque aggregation (§5A.A, §19) |
| Dense spike detector (SpikeHunter) | n/a | Unnecessary | B2/B3 are simpler and explainable |
| Multi-layer verdict table (SpikeHunter) | n/a | Unnecessary | Opaque; conflicts with traceability |
| `system_health` lock waits / deadlocks | P3 | High Value [Rev 2, was Must]; S1 matching itself is Later | It ruled out ≥ 30 s lock waits here (0 events in 70 days); it stays as cheap counter-evidence |
| Scheduler-monitor CPU split | P3 | Must Have | E10/E11/E15. **[Rev 2]** Shown with the counter sanity check (E19) |
| **Live request sampler (S14)** [Rev 2] | P3 | High Value | The native 5–30 s wait and blocking view in predicted windows (G7 step 1) |
| `sp_server_diagnostics` components | P3 | High Value | Persisted resource and I/O state |
| Error-log allowlist viewer | P3 | High Value | 833, 17883, configuration and restart markers |
| Default trace (autogrow, DDL) | P3 | Later | Since-restart only here (E11) |
| Backup overlay (msdb) | P3 | High Value | Cheap counter-evidence (E11) |
| Live waits delta / blocking chain | P3 | Later → **folded into S14 (High Value)** [Rev 2] | Useful during a predicted window now that live access is the main mode |
| Query Store regressions | P3 | Later | QS OFF (E8); keep for databases where it is ON |
| Missing-index DMVs | old T14 | Unnecessary/Low Value | Not slowness evidence; reset at restart; noisy |
| Index fragmentation (opt-in) | old T14 | Unnecessary | Harmful scan (§3.5); rarely causal for stalls |
| Configuration checklist (MAXDOP, CTFP…) | old T14 | Low Value | Static advice, not incident evidence |
| Stats staleness (≥ 20%, > 7 days) | old T14 | Later | Only with a QS plan regression |
| Mongo server snapshot + deltas | P4 | High Value | Live context for audit-store stalls |
| `mongod.log` slow-op import | P4 | Must Have (for E6) | Persisted evidence for the 10-22 stall. **[Rev 2]** Also the connection and auth lines that M9 needs (E17) |
| **Cold-start audit rule (M9)** [Rev 2] | P4 | High Value | E17: 3 × about 28 s after a warm-up/idle gap |
| FTDC import | P4 | Later (stretch) | High value but costly to parse |
| `$currentOp` bounded | P4 | Later | Live only |
| Self-load panel (`appName`) | P4 | High Value | Unravis accountability |
| Hangfire recurring schedule calendar | P5 | Must Have | Direct test of E5 |
| Hangfire run states / heartbeat | P5 | High Value | Short retention |
| SQL Agent jobs/history | P5 | High Value | Needs a grant (E9) |
| Event Log curated timeline | P6 | High Value | Recycles, reboots, AV, updates |
| Task Scheduler static triggers | P6 | High Value | Recurrence candidate |
| Live counters / DCS import | P6 | **Must Have** [Rev 2, was High Value] | The only native way to name the burst process (G3, E15) |
| Deploy `bin` timestamps / change lane (W9) | P6 | **Must Have** [Rev 2, was High Value] | No native deploy markers exist (§0); the chronic step is CPU-independent (E18) |
| **Live-first connectors + probe + kill-switch** [Rev 2] | §3.7 | Must Have | Live access is the main mode |
| **Query packs (offline fallback)** [Rev 2] | §3.7 | High Value | Same templates and model when live access is refused |
| IIS W3C import + audit join | P7 | High Value | Outside-pipeline time, coverage |
| httperr reasons | P7 | High Value | Queue and pool failures |
| DNS/network diagnostics | n/a | Unnecessary (now) | No evidence implicates the network |
| Load-balancer/proxy logs | n/a | Later | Only if I1 shows outside time and no IIS cause |
| Correlation workspace + composite rules | P8 | Must Have | Objective of the investigation |
| App Insights waterfall + unattributed time | P9 | High Value per client (live for about 50%) [Rev 2] | Existing per-request evidence; detected by capability, optional |
| Redis snapshot / slowlog / watch | P10 | Later | Live only |
| Redis big-key `SCAN` sampling | P10 | Low Value | Risky; no evidence implicating Redis |
| Framework counters (P11a) | P11 | Later | Live/DCS only |
| .NET 10 `dotnet-counters` import (P11b) | P11 | Later | Needs a tool install on production |
| log4net Windows Services parser | P12 | Later (Conditional) | Possible scheduled worker |
| log4net API logs / Elasticsearch | P12 | Low Value (now) | Appears disabled |
| Evidence API + schema | P13 | High Value | Machine-readable surface |
| Case export with pseudonymisation | P13 | High Value | Sharing without PII |
| Generic MCP evidence tools | P13 | High Value | AI clients without in-tool AI |
| In-tool AI diagnosis / LLM loop | n/a | Unnecessary | Out of scope by design. **[Rev 6]** Unchanged: the optional LLM path is external, via MCP, proposals only (§3.10; decision 14) |
| Local cache of daily summaries | P1/P2 | Later | Speeds the 90-day trend; derived data only |
| Instrumentation (old Task 16) | G | Later (gated) | §21 |
| **Host auto-detect + manual hints (host profile)** [Rev 4/5] | P0 | Must Have | Machine-fills or accepts the per-client host map that every host-side phase needs; read-only, no gate |

---

## 18. Missing from the prior plans

1. **Evidence preservation.** No plan says to copy rolling native stores before they vanish: FTDC, `mongod.log`, `system_health` .xel, the SQL error logs (6 kept, cycled on restart), the default trace (already lost for the incident, E11), and IIS logs. It is time-critical for the 10-08 to 10-23 incidents.
2. **`_id` range pruning.** The old Task 9 talked generically about pipeline performance. The concrete fix is `_id` pruning at `internal/store/pipeline.go:99`, because only the `_id` index exists (E1).
3. **Schema-driven fields** (E2/E3) and the three fields the schema doc does not mention.
4. **Capability and permission probing** as a first-class, demoable feature, with remedy text.
5. **Clocks and time zones.** No plan normalizes Dubai/UTC/server-local across sources or measures skew.
6. **Recurrence and schedules.** No plan tests clock-minute recurrence or reads Hangfire cron, Agent or Task Scheduler schedules. This is the strongest unexplained pattern (E5).
7. **Release-cluster analysis.** No plan distinguishes "shared block released at one instant" from general slowness.
8. **Change and deploy history** for step dating (E7): DDL in the default trace, configuration changes in the error log, Windows Update/MSI, `bin` timestamps, manual annotations.
9. **Host contention outside SQL** (E10/E11). The old plan assumed a SQL-internal cause.
10. **The audit store as a suspect** (E6). The old plan treated Mongo only as the data source, never as a cause.
11. **Unravis self-load accountability** (`appName`, docs examined).
12. **Counter-evidence as output.** Ruling things out (backups, Hangfire, IIS queueing) is half of the investigation.
13. **Application Insights as possibly existing evidence** (E13). The old Task 16 would instrument what App Insights may already record.
14. **The Windows Services log** as the only active log4net output (E14).
15. **PII handling** for exports, SQL text and log messages.
16. **[Rev 2] A volley shape** (same user, same-second starts, staggered ends) and the per-user serialization hypothesis (E16).
17. **[Rev 2] A sanity check on the counters themselves.** SQL% was 0 for 70 days (E19).
18. **[Rev 2] Source DST and time-zone drift as a natural experiment.** The SQL server is on UTC+1 (E22).
19. **[Rev 2] Live-first access engineering:** JEA, kill-switch, back-off, query journal and query packs (§3.7).
20. **[Rev 4/5] Host discovery as a product feature:** auto-detect plus manual hints with provenance (§3A). Every prior plan assumed someone already knew where each log, config and instance lived.
21. **[Rev 6] Deterministic theory validation (§5A).** No plan, and not the October report, tests a theory against its own falsifiers, a matched base rate, a recurrence null model or a multiplicity correction, or checks that the source is sane before trusting it. The report set every status by hand. One of them rested on a flat-zero counter (E19), and one on a two-day comparison.

---

## 19. Remove or simplify (by source plan and item)

| Source plan → item | Action | Reason |
|---|---|---|
| Old diagnostics plan → Telemetry Provider seam (`Kind/Diagnose/Close`) built up front | **Replace** with the §3.1 evidence model, grown per phase | A speculative abstraction; one technology per phase does not need a provider registry |
| Old diagnostics plan → `Finding{Severity, Title, Evidence, Advice}` | **Simplify**: drop `Severity` and free-text `Advice`; add `label`, thresholds and counter-evidence | Severity is an opaque score; advice drifts into diagnosis |
| Old diagnostics plan → Task 11 guard "SELECT/WITH only" | **Replace** with a fixed query-template allowlist; keep `ApplicationIntent=ReadOnly`, `LOCK_TIMEOUT 5000`, `DEADLOCK_PRIORITY LOW`, `VIEW SERVER STATE` | Prefix checks are bypassable (e.g. `WITH … SELECT … INTO`, or functions with side effects) |
| Old diagnostics plan → Task 12 Query Store first | **Demote** to a conditional feature in P3 | QS OFF here (E8); restored or upgraded databases keep their old setting [R8] |
| Old diagnostics plan → Task 13 30 s wait delta as the core view | **Demote** to live mode only | DMVs reset on restart; retrospective questions dominate |
| Old diagnostics plan → Task 13 I/O thresholds (read > 20 ms, log > 5 ms) | **Keep as live-only defaults**; persisted I/O comes from error 833 and `io_subsystem` | `virtual_file_stats` is cumulative since restart |
| Old diagnostics plan → Task 14 indexes / configuration / fragmentation | **Remove** fragmentation and the configuration checklist; **defer** missing-index and stats | Not slowness evidence; fragmentation is a harmful scan |
| Old diagnostics plan → Task 15 DB evidence UI | **Keep the banner**; fold the UI into P3 and P8 | Same intent, broader |
| Old diagnostics plan → Task 16 brain-payroll instrumentation (`DbTimeMs`, `DbCallCount`, `ExceptionType`, `TraceId`, `SlowDbCalls[]`) | **Gate** behind G1/G2 after P3 and the P9 gate | App Insights may already have it; `system_health` answers > 30 s waits |
| Old diagnostics plan → Task 9 pipeline performance | **Replace** with concrete `_id` pruning (P1) | Concrete and measurable |
| Gap plan → write `RequestPollingType` at insert | **Gate** (G6), only if the HTTP-unknown share is material in P1's coverage report | An app change; the share may be small |
| SpikeHunter → multi-layer verdicts / verdict-rules.json as an engine | **Do not adopt**; use NET-R1/SQL-R1 only as a checklist when writing per-phase rules | Opaque layering; not traceable. **[Rev 6]** P2b (§5A) is not this: one theory gets one verdict, every test is listed, nothing is aggregated or weighted (§5A.A) |
| SpikeHunter → time-budget gap rule | **Re-scope**: an "unattributed time" metric only in P9, where sub-timings exist | Meaningless with only two phases |
| SpikeHunter → dense detector | **Do not adopt** | B2/B3 suffice |
| SpikeHunter → LLM loop | **Do not adopt** | Out of scope |
| MVP → 7-day hard cap everywhere | **Simplify** to 7 days for request views, 90 days for trend endpoints | Step and trend analysis needs it |
| **[Rev 6]** October report → hand-built pipeline (`report.src.html` + `build_report.js` + data JSON) | **Replace** with the P2b incident report export (§5A.F item 1) | Manual and unversioned; methods and thresholds live only in prose |
| **[Rev 6]** October report → hand-set theory statuses ("Rejected", "Consistent", "Open") | **Replace** with P2b verdicts; a hand-set status survives only as an "analyst note" | Two of the report's rejections rest on a broken counter (TH1, E19) or n = 2 (TH2) |

---

## 20. Recommended but not required

- A local cache of derived daily summaries per source and API, so a 90-day trend runs once a day. It holds derived data only and stays local.
- Saved investigations: the ledger plus episode windows persisted locally, with DPAPI where they contain identifiers.
- Manual change annotations ("deploy 10-08 16:50"), stored locally and shown in the change lane.
- A "predicted recurrence" helper: the next expected fire times for B3 windows, to schedule ops' DCS and the Redis watch.
- A DBA request generator: exact `GRANT` statements and the QS enablement script, which a human reviews and runs.
- A sanitized, synthetic "incident replay" fixture pack in the repository, for regression tests of every rule.
- A Unravis query-budget readout: docs examined and elapsed time per source for each investigation.

---

## 21. Evidence gaps that may justify instrumentation (gated backlog G)

Each item is opened only when its question is active and its native alternatives have been ruled out.

| Gap | Question | Native alternatives considered | Why they may be insufficient | Minimal instrumentation if justified |
|---|---|---|---|---|
| G1 Request ↔ SQL session linkage | Which statement and session was *this* slow request waiting on? | `system_health` `wait_info` (only > 30 s, no request ID); QS (aggregates; OFF); App Insights `operation_Id` (P9, if live) | If App Insights is not live, linkage stays time-based (strong correlation at best) | Set the SQL connection `Application Name` or `SESSION_CONTEXT('req', id)` per request (not a schema change); log the request ID in the audit row |
| G2 Per-request DB time and call count | How much of `ApiTime` is SQL? | App Insights dependency sums (sampled); QS (aggregate) | Not live, or sampling misses stalls | `DbTimeMs`, `DbCallCount` in the audit row (old Task 16 subset); no `SlowDbCalls[]` arrays (size, PII) |
| G3 Process CPU history on the DB host | Which process is the "other" CPU (E10/E11)? | Scheduler monitor (SQL vs other only); Event Log (no CPU) | No per-process history by default | **Native**: a time-boxed `logman` DCS (`Process(*)\% Processor Time`, 15 s) in recurrence windows. Not code. **[Rev 2] Justified now** (E15; §3.8; §24 decision 3) |
| G4 Pre-controller queueing history | Did requests wait before the action? | IIS `time-taken` − observed (P7); ASP.NET counters (live) | Slow clients blur `time-taken`; counters are not persisted | **Native** DCS first; only then log `ThreadPool.GetAvailableThreads` at request start |
| G5 App logs in the APIs | What did the API log during episodes? | App Insights traces (if live); the Windows Services log | Appears disabled (E14) | Re-enable an existing appender (configuration, app team decision) |
| G6 HTTP-unknown kind | Is HTTP-unknown sync or async? | Route naming, payload markers, timing shape | Ambiguous if the share is large | `RequestPollingType` written at insert (gap plan) |
| G7 Lock waits of 5–30 s | Shorter blocking that `system_health` misses | `system_health` (> 30 s only); QS wait stats (if enabled); **[Rev 2]** the S14 live sampler in predicted windows | QS OFF | **Native first**: the S14 sampler (no server change), then enable Query Store (DBA, §24 decision 2). Only then a blocked-process-report XE session (server configuration, DBA-owned) |
| G8 Audit-insert stall internals | Why did the audit write take 28 s? | `mongod.log`, FTDC, `serverStatus` (P4); **[Rev 2]** M9 + W1 + IIS warm-up lines (L3) | If logs and FTDC rolled over | Preserve evidence (no code). **[Rev 2]** It looks like cold-start connection set-up (E17), so the remedy is configuration (pool warm-up, `minPoolSize`, `startMode=AlwaysRunning`), which is the app team's call, not instrumentation |
| **G9 Per-user/session serialization** [Rev 2, new] | Do one user's concurrent requests run one after another? | App Insights overlapping operations per user (P9, where live); IIS arrival times (P7); a code and config read of session-state mode and per-user locks in brain-payroll (no production access) | Without P9 there is no request-scoped timing | **No code first**: read the session-state configuration (ASP.NET `SessionStateBehavior` / `sessionState mode`) `[unverified]`. Only then log a per-request "wait-before-start" stamp |

The rejected item is **OpenTelemetry, a new App Insights rollout, agents or profilers**. Every question above has a native or existing alternative that has to be exhausted first.

**[Rev 3] Gate N: the on-server Unravis node (§3.9).** It is not instrumentation, because it changes nothing in the application. It is a deployed component, though, so it gets the same discipline. It opens per client and per host only when all four §3.9.2 conditions hold:

1. A named question needs local-only or remotely-too-heavy evidence. The typical case is G3 when the DCS was refused, failed, or the bursts are irregular. Others are Redis, Mongo or SQL bound to localhost, or multi-GB logs with no share.
2. The §3.7 remote channel, the DCS and query packs have been tried, and the reason they fell short is recorded.
3. The node is time-boxed, with a default expiry of 14 days.
4. The client approves the host list, port, identity and data categories.

The node never runs ETW, profilers or `dotnet-counters`. Those stay rejected as above.

---

## 22. Challenges to prior plans / things I'd push back on

1. **"Query Store first"** fails in the actual environment. QS is OFF and DMVs reset on 10-23. The persisted SQL source is `system_health`, and it covers the incident window.
2. **"The cause is in SQL"** is an assumption. E10/E11 show SQL CPU ≈ 0 and *other-process* CPU at 30–43%. E6 implicates the Mongo audit store. Evidence points at three different places. **[Rev 2]** The revised evidence sharpens this:

- Acute stalls track recurring host-CPU bursts (E15).
- Lock waits ≥ 30 s are refuted (0 in 70 days).
- The chronic step is CPU-independent (E18).
- Audit stalls look like a cold start (E17).

These are 4 problems, not one SQL problem.
3. **The old Task 16 instruments too early.** App Insights is wired with SQL command text (E13). If it is live, most fields already exist per request. If not, G1/G2 are the minimal subset.
4. **The provider seam and severity** are premature and opaque. Grow a tiny evidence model instead, and replace severity with impact plus labels.
5. **SpikeHunter verdict layers** would produce confident labels without traceable evidence. Keep its useful detectors (stall catalog, release cluster, Hangfire control, long trend, ledger) as simple rules.
6. **The 7-day cap** blocks the very analyses the incident needs: the 10-08 step and the 09-21 CPU step.
7. **The MVP's own query cost.** A COLLSCAN of ~778k docs per query is a production risk created by Unravis. Fix it before adding sources.
8. **"Correlation view" as an early deliverable** would mostly show coincidences. P2–P7 must each stand alone first.
9. **Old Task 14** (indexes, fragmentation, configuration) is hygiene, not incident evidence, and one of its items is itself a harmful query.
10. **The ad-hoc scripts' lesson (E3)**: hand-typed field names silently produced nulls. Tool queries must be schema-driven.
11. **Live-only sources (Redis, runtime counters, `$currentOp`)** cannot explain past incidents. Schedule them against *predicted* recurrences, rather than ranking them high.
12. **[Rev 6] The October report's own verdicts** (§1B). It is careful in places: an evidence column, caveats and n ≥ 50 for p95. But four of its calls need correcting:
    - "SQL saturated: rejected" uses the SQL CPU counter that sat at 0 for 70 days (E19). The honest status is *inconclusive* until the counter is cross-checked (V6).
    - "Own volume: rejected" compares two days. V4 stratifies all busy days by load.
    - "Whole host froze: rejected" assumes Hangfire shares the host under test. That is a topology fact for P0 to confirm (V5 precondition).
    - "Shared lock released together" conflicts with the staggered same-user volleys (E16). It is superseded by per-user serialization (TH9).

    `[unverified until P2b runs]`.

---

## 23. Feasibility cross-check against the Go codebase (reuse vs new)

| Need | Reuse | New | Notes |
|---|---|---|---|
| Host auto-detect + hints (P0) [Rev 5 row] | `cmd/audit-explorer/main.go` flag parsing (subcommand dispatch added before the existing `FlagSet`); `platform_windows.go`/`platform_other.go` split; DPAPI and profiles (`internal/history`, `internal/store/profiles.go`) for `connection_ref` | `internal/detect` (Probe interface, registry, redaction, hints loader with strict schema, provenance merge); `golang.org/x/sys/windows/registry` and `svc/mgr` | Windows-only probes behind build tags; hints work on any OS for hub-side profiles |
| `_id` pruning | `internal/store/pipeline.go:99` (match stage); `internal/store/legacy_pipeline.go` | ObjectID-from-time helper (driver v2 `bson.NewObjectIDFromTimestamp`) | Tests in `pipeline_test.go`, `legacy_pipeline_test.go` |
| Schema-driven fields | `internal/store/schema.go` (`SampleSchema`), `internal/server/schema.go` | Alias map plus "absent" state in `internal/audit/models.go` | |
| PreActionFilter phase | `internal/audit/phases.go`, `phases_test.go` | New phase constant and series | Nesting semantics need a brain-payroll code check |
| `AccountId` dimension | `internal/store/breakdown.go`, `internal/audit/breakdown.go`, `internal/mcp/breakdown.go` | Add to allowed dimensions | |
| Trend extension | `internal/audit/histogram.go` (γ=1.1 bins), `internal/audit/ranges.go` | Day-split scheduler under the source slot (`internal/store/store.go:25-60`) | Keep the 7-day cap for `Analyze`/`Requests` |
| Stall catalog | `internal/store/slowest.go` (keyset cursor), `internal/store/activity.go` | Multi-day duration-filtered query; episode merging in `internal/audit` | `activity.go:23` 24 h cap stays for activity |
| Release cluster / sweep | `internal/audit/load_correlation.go:144` (`EstimateConcurrency`), `:160` (`SpearmanLoad`) | Sweep line plus slope regression (pure functions) | Easy to unit-test |
| Hangfire control / step dating | `internal/audit/significance.go:21` (Mann-Whitney), `internal/audit/kind.go` | Window-vs-baseline orchestration | |
| Hypothesis ledger | n/a | In-memory store plus UI; export in P13 | |
| Theory checks (P2b) [Rev 6 row] | `internal/audit/significance.go:21` (Mann-Whitney), `load_correlation.go:160` (`SpearmanLoad`), the P2 rules as test functions, `crypto/sha256`, `encoding/json` (canonical output, sorted keys) | `internal/theory`: records, verdict function, V1–V9, exact binomial, (block) permutation with `math/rand/v2` PCG seeded from the input hash, Holm/BH, bundle ZIP, Markdown/HTML report renderer (`html/template`, no CDN) | Determinism needs fixed float formatting and sorted iteration everywhere; test on Windows and Linux CI |
| Mongo native (P4) | `internal/store/mongo.go` (`RunCommand` already used for `buildInfo`, line 48), `connections.go` sanitized errors | `serverStatus`/`getLog`/`$currentOp` readers; `mongod.log` JSON parser; `SetAppName` | FTDC parser is the risk; legacy 3.4 path gets "limited" capability |
| SQL (P3/P5) | Connection UX pattern (`/api/connections/*` in `internal/server/server.go:141-232`), DPAPI (`internal/history/history.go`, `platform_windows.go`), profiles (`internal/store/profiles.go`) | `github.com/microsoft/go-mssqldb` (BSD-3 `[unverified]`), a query-template allowlist, XE XML parsing (`encoding/xml`), error-log parser | History entries need a source-kind discriminator `[unverified current shape]` |
| Windows (P6) / IIS (P7) | Import UX patterns | `os/exec` of native `wevtutil`, `relog`, `typeperf`; W3C and httperr streaming parsers; file-import endpoint (bounded size) | Shelling out to signed system binaries only, fixed argument templates |
| Redis (P10) | Semaphore and limit patterns | `github.com/redis/go-redis/v9` (BSD-2 `[unverified]`) with a command allowlist wrapper | |
| App Insights (P9) | DPAPI | Entra ID token flow plus a KQL client (HTTP) | Outbound only |
| Correlation (P8) | Drill model (`internal/audit/drill.go`, `overview.go`) | Episode, lanes, composite rules | |
| Evidence API / MCP (P13) | `internal/server/server.go` routing, `security.go` (`allowedHost`, `allowedOrigin`), `internal/mcp/bridge.go:25`, `tools.go:95` (`call()`), `schema.go:5` (guide), the 10-minute result cache | 4 tools; versioned JSON schema; ZIP export with HMAC pseudonymisation | Keep the 1 MiB MCP cap |
| Demo per source | `cmd/audit-explorer/main.go:47,81` (`--demo`), `internal/audit/demo.go`, `activity_demo.go` | `Demo<Source>Reader` per phase implementing the same reader interfaces | `--demo` still not combinable with `--mcp` (keep, or lift in P13) |
| Repository seam | `internal/audit/models.go:459-463` (`Repository`, `ActivityReader`, `BreakdownReader`, `SlowestReader`, `SchemaSampler`) | Per-source reader interfaces (`SQLReader`, `MongoServerReader`, …), each small | Same style; no generic provider registry |

**Overall feasibility.**

- **[Rev 5]** P0 is a self-contained new package. Its hints-only slice needs no Windows APIs at all.
- P1, P2, P4 (without FTDC), P5, P7 and P8 are straightforward extensions.
- **[Rev 6]** P2b is pure computation over existing findings. Its risk is determinism discipline (float formatting, map order, seeding), not access.
- P3 needs one new driver and an XML parser.
- P6 and P11 depend on access logistics more than code.
- P9 depends on an external gate.

---

## 24. Decisions [Rev 5: 1–13 resolved; Rev 6: 14–15 proposed defaults]

**Resolved by the user's revision-2 answers:**

| Old item | Resolution |
|---|---|
| Old 1, host access | Live-first, with offline import as the fallback (§3.7). The channel per client was decision 6, now resolved below. |
| Old 2, DBA grants | Yes. The exact list is in §1A.E. |
| Old 3, App Insights | Live for about 50% of clients. P9 is detected per client (§12). |
| Old 4, preservation | A standing action (§0), with the window extended to 09-01..10-31. |
| Old 5, 90 days | Yes, for daily rollups only (§3.8). |
| Old 7, DCS | Folded into decision 3, with revised windows. |

**Kept as defaults (not open):**

- Old 6: P2 extends the audit MCP tools, and the 4 generic tools come in P13.
- Old 8: no real extracts as fixtures.

**[Rev 4] Resolved by the user: every default for decisions 1–10 was accepted (2026-10-24).** These are now binding plan inputs.

| # | Decision | Resolution (accepted default) |
|---|---|---|
| 1 | Host topology per client | **Ops confirms a host map per client.** **[Rev 4]** P0 auto-detect (§3A) fills it in by machine. Ops reviews and corrects it rather than writing it from scratch. Where P9 is live, `cloud_RoleInstance` cross-checks it. |
| 2 | Query Store | **On** for the app database: `READ_WRITE`, `QUERY_CAPTURE_MODE = AUTO`, `INTERVAL_LENGTH_MINUTES = 60`, `WAIT_STATS_CAPTURE_MODE = ON`, default size. The DBA runs it. |
| 3 | 5-day perf DCS | **Yes.** Unravis generates it and ops creates it on the DB host (and the API host if separate), in the §3.8 burst windows, ideally spanning a DST change. |
| 4 | Third-party SQL monitor | **Read-only console or export access** to its history and job schedule (E21). |
| 5 | S14 sampler limits | **5 s cadence, ≤ 30 min per run, ≤ 5 runs per day**, predicted windows only, auto-stop, `Application Name=Unravis`. |
| 6 | Windows remote channel | **WinRM + JEA endpoint.** Native RPC tools are the fallback, then offline import (§3.7). |
| 7 | On-server node | **Hybrid, only for named sources, behind gate N. Build deferred** until the DCS result is in (§3.9). |
| 8 | Node transport | **Spool-only first.** mTLS pull only where live pull is needed. |
| 9 | Node identity and lifetime | **Virtual service account** (`NT SERVICE\UnravisNode`), or a gMSA if needed. **14-day expiry** with auto-disable. A permanent node needs separate approval. |
| 10 | Node signing and updates | **Authenticode-signed binary + hash-pinned signed pack manifest.** No auto-update. The hub supports node versions N and N−1. The tool owner holds the release key. |

**[Rev 5] Decided by judgment (user-delegated, 2026-10-24).** The user delegated these to the planner. They were the Rev 4 proposed defaults, adjusted for manual hints. They are binding plan inputs, and each one can be revisited per client.

| # | Decision | Resolution (decided by judgment, user-delegated) |
|---|---|---|
| 11 | How `unravis detect` runs on a client server | **Ops runs the signed exe one-shot, from either of two equal paths.** (a) Auto-detect in an **elevated** console: the code is read-only, but `applicationHost.config` and some service and task details are admin-only by default `[unverified per OS build]`. (b) **Non-elevated with a hints file** (`--hints`, optionally `--no-detect`) when elevation is refused or the host is locked down: it needs only read on the hinted paths. A non-elevated run without hints is allowed and reports what it could not see. Remote detect through JEA (a subset of probes) comes later. Hub-side hints cover remote hosts until then. |
| 12 | Identities in the host profile | **Record service and app-pool run-as account names**, because the grants need them. **Redact SQL-auth user IDs** and every secret in connection strings. Never record interactive user names. Hints files may not contain identities or secrets either; connections are given by `dpapi:` reference only. |
| 13 | Where host profiles (and hints files) live and who sees them | Both are effectively reconnaissance maps, so treat them as **client-confidential**. They are stored in the hub's per-client store, DPAPI-protected like `internal/history`. The file ACL on the server copy is limited to the running user. They leave the client boundary only pseudonymised (P13 scheme). Re-detect (and re-validation of hinted paths) prompts after 7 days by default. |

**[Rev 6] Open, with proposed defaults (theory validation, §5A and §3.10).**

| # | Decision | Proposed default | Alternative |
|---|---|---|---|
| 14 | LLM path | **External only, via MCP.** The build contains no LLM client. An LLM may propose theories from catalog templates, translate a question into a template (the user confirms), and draft narratives with reference tokens checked by the linter. It never sets a verdict, computes a number or sees raw identifiers (§3.10) | An in-tool client behind an opt-in flag, with the same restrictions. Not recommended: it adds a secret, a network egress and a vendor dependency for no verdict value |
| 15 | Multiplicity and permutation policy | **Holm at α = 0.05 per verdict run** (the family is the theories run together). **Benjamini–Hochberg q-values** for exploratory lists only, which never feed a verdict. **N = 9,999 permutations**, seed = the first 8 bytes of the SHA-256 of the canonical input. Day-block or 1-hour-block permutation for minute data | Bonferroni (simpler, more conservative), or a per-theory α with no family correction (not recommended: the look-elsewhere effect) |

**[Rev 5] Needs client or user facts (inputs to collect, not decisions).**

- **Per client:** a hints file or a confirmed detect run per host (roles, locations, timezone). This is decision 1's host map, now gathered through P0.
- **Per client:** whether the API host is separate from the DB host (decides where the DCS of decision 3 runs and how B13/S4 are read).
- **Per client:** which App Insights resource, if any, belongs to it, and who grants Reader (P9).
- **Per client:** approval of the client actions already decided: the JEA endpoint (decision 6), the DCS (decision 3), Query Store by the DBA (decision 2), the third-party monitor access (decision 4), and placing the signed exe for P0.
- **Code read (no production access):** the brain-payroll PreActionFilter nesting (P1) and the session-state mode for G9.

---

## 25. Final roadmap table

**[Rev 2] Build order.**

- The release IDs below keep the revision-1 numbering for traceability. The **build order is now R1 (P1) → R2 (P2) → R3 (P3) → R6 (P6) → R5 (P5) → R9 (P9, per client) → R7 (P7) → R4 (P4) → R8 (P8) → R11 (P11) → R10 (P10) → R12 (P12) → R13 (P13) → G**.
- The §3.7 connector layer ships with R3, the first new live source, and is extended per release.
- Changes to the demos:
  - R2 leads with the B12 volley.
  - R3 leads with the CPU split and B13 (S1 is secondary).
  - R8 leads with C9.
- **[Rev 4]** **R0 (P0 host auto-detect)** is built first, in parallel with R1. Its host profile feeds the connector and pack proposals of R3–R12. The R6 demo also gains a HP2 check: the scheduled burner's trigger time is matched to the B13 window from the profile.
- **[Rev 5] Full build order, all revisions merged:** **R0 ∥ R1 → R2 → R3 → R6 → (RN1, gate N only) → R5 → R9 (per client) → R7 → R4 → R8 → R11 → R10 → R12 → R13 → G**. This is the same order as §0 and §2. R0 includes manual hints from its first slice.
- **[Rev 6] Full build order:** **R0 ∥ R1 → R2 → (R2b ∥ R3) → R6 → (RN1, gate N only) → R5 → R9 (per client) → R7 → R4 → R8 → R11 → R10 → R12 → R13 → G**. R2b (P2b theory checks) runs in parallel with R3. It needs no new source and no production access. It supersedes the Rev 5 line above; the only change is R2b. It matches the §0 and §2 tables (slot 2b).
- **[Rev 3]** The optional release **RN1 (node N1)** sits after R6, but only when gate N is met. Otherwise it slides to after R8. It reuses R6's packs and rules, and no other release depends on it.

| Release | Phase(s) | What an engineer can do with it | Sources | Demo |
|---|---|---|---|---|
| R0 [Rev 4, Rev 5] | P0 | On a server, say "auto-detect", **or state the locations directly** (`unravis-hints.json`, flags or the UI form; hints-only with `--no-detect`; hub-side hints for remote hosts): get a reviewable host profile (roles, evidence locations and retention, topology edges, candidate burst sources, missing rights, proposed packs) with per-field provenance and HP6 conflicts, nothing enabled until confirmed; diff against an earlier profile for drift | Local registry, `applicationHost.config`, SCM, app configs, `mongod.cfg`/`redis.conf`, `typeperf -q`, `wevtutil gl`, `schtasks`, `logman query`, listening table | VM with IIS + SQL Express + `mongod` + Memurai: non-elevated run shows denied IIS with the missing right; elevated run gives the full profile; HP2 fires for a 13:10 recycle; zero seeded secrets in output; drift HP1 after a pool change; workstation message on a clean laptop; **[Rev 5]** non-elevated hints run sees the hinted IIS log folder and `mongod.cfg`, a wrong hinted port raises HP6, a hints file with a password is rejected, `--no-detect` opens only hinted paths |
| R1 | P1 | Run cheap pruned queries; see pre-filter time, tenant lift, audit coverage gaps, capabilities; read everything in Dubai and UTC | `ApiAuditLog`, `$collStats` | Local Mongo 800k rows: `explain` before and after; `AccountId` lift; induced coverage gap |
| R2 | P2 | Find stalls, recurring clock windows, release clusters, Hangfire control, dated step changes; keep a hypothesis ledger; query stalls via MCP | `ApiAuditLog` | Generator: **[Rev 5, aligned with Rev 2]** a same-user volley (B12) inside a recurring host-CPU window (B13), a 14:45 recurrence, a 17:00 step; the release cluster stays secondary |
| R2b [Rev 6] | P2b | Write a theory once (claim, predictions, falsifiers, guards); get a deterministic verdict (supported, contradicted or inconclusive, with the reason and label) that lists every test; see "what would change my mind"; export the incident report and a bundle that re-runs byte-identically elsewhere; optionally let Claude Code propose theories over MCP. **[Rev 7]** Read it all on the theories screen: one verdict row per theory (verdict + evidence badge, the reason when inconclusive, the next action and its owner), with evidence, gap, tests and bundle one click deeper; sort by cheapest next evidence; add analyst notes | P1/P2 findings, P0 topology facts; later every phase's rules | Synthetic replay (§5A.G): TH4 supported (symptom); TH2 contradicted across load quartiles; TH3 inconclusive until topology confirmed; flat-zero SQL CPU keeps TH1 inconclusive (V6); threshold edit bumps version; null fixture leaves TH8 unsupported; identical `verdict_hash` on a second machine. **[Rev 7]** Theories screen: TH1 row reads "Inconclusive · counter failed sanity check · Next: cross-check SQL CPU counter · P3 / DBA" next to "Report: Rejected"; an analyst note leaves the hash unchanged; the MCP `view=cards` output equals the screen |
| R3 | P3 | Test CPU-split, error, backup and restart hypotheses (and lock waits and deadlocks as counter-evidence) against 70 days of persisted SQL evidence; sample live requests in predicted windows (S14); see capability and remedy text | `system_health` .xel, ring buffers, error log, default trace, msdb backups, DMVs (live), QS (if on) | **[Rev 5, aligned with Rev 2]** Local SQL 2022: a scripted non-SQL CPU burner → the persisted CPU split (S4, with the counter sanity check) shows "other" CPU in the B13 window; S14 catches a scripted short block. Lock-wait matching (S1) is a secondary demo |
| R4 | P4 | Explain audit-insert stalls with slow-op log, tickets, cache and locks; see Unravis's own load | `serverStatus`, `getLog`, `mongod.log`, `$currentOp`, `$collStats` (FTDC stretch) | Local Mongo under cache pressure → slow insert log lines align with B6 |
| R5 | P5 | See which Hangfire recurring jobs or Agent jobs fire at the recurring minutes; job runs and heartbeats | Hangfire tables, msdb Agent tables | Local Hangfire schema with cron `45 10 * * *` → H1 on the recurrence grid |
| R6 | P6 | Check recycles, reboots, AV, updates and scheduled tasks; name the CPU consumer from a DCS | Event Log (`wevtutil`), `schtasks`, PDH/`typeperf`/`relog` | Own machine: a scheduled CPU burner + DCS → W3 + W7 |
| RN1 [Rev 3, optional, gate N] | N1 node "burst catcher" (§3.9.4) | Catch rare or irregular host CPU bursts unattended for days; name the process and its service, app pool or task; prove the node's own CPU cost; the hub pulls only burst minutes | Local PDH `Process(*)`, Event Log, `schtasks`, change markers, via node spool / mTLS | Node as a service on the engineer's own machine + a scheduled CPU burner; the hub pulls (second loopback port) → W7 names `burner.exe` with its task → C9; node self-CPU ≈ 0.5% |
| R7 | P7 | Measure time outside the pipeline, pool queue drops, 503s, client disconnects, missing audit rows | IIS W3C, HTTPERR | Local IIS with a small queue + burst → `QueueFull`/503 + audit join |
| R8 | P8 | Open an episode, see all sources on one clock, composite strong correlations, counter-evidence, missing-evidence remedies | P2–P7 findings | **[Rev 5, aligned with Rev 2]** Combined demo: C9 (volley + burner-task burst + DCS naming the process) → ledger *supported (strong correlation)*, proof gap stated; C2 secondary |
| R9 | P9 (per client, capability-detected; built 6th) [Rev 2] | See the dependency waterfall per sampled slow request; unattributed time; exception spikes; request-level proof | App Insights (`requests`, `dependencies`, `exceptions`) | Dev resource or imported query export → AI2 proven for a sampled request |
| R10 | P10 | Rule Redis in or out: slowlog, forks, evictions, client pressure; a bounded watch in a predicted window | `INFO`, `SLOWLOG`, `LATENCY` (if on), `CLIENT LIST`, `CONFIG GET` | Local Redis: big `HGETALL`, `BGSAVE`, low `maxmemory` |
| R11 | P11a/b | Distinguish GC, thread-pool starvation, lock contention and ASP.NET/HTTP.sys queueing during a recurrence | Framework perf counters / `System.Runtime` via `dotnet-counters` import | Local sync-over-async app under load → T1/T3 |
| R12 | P12 | Read Windows Services (and, if enabled, API) logs aligned to episodes; job intervals; exception bursts | log4net rolling files (pattern-driven), optionally Elasticsearch | log4net sample with a fixed-minute job → log rule L3 (to be renamed LG3, Appendix B) |
| R13 | P13 | Query evidence from scripts or AI clients; export pseudonymised cases | Unravis evidence model | Claude Code via MCP on the demo episode; case ZIP opened elsewhere |
| — | G | Add minimal, gated instrumentation for proven gaps only | brain-payroll | Per gap |

---

## 26. References (bounded)

- [R1] MongoDB, Database Profiler: https://www.mongodb.com/docs/manual/tutorial/manage-the-database-profiler/
- [R2] MongoDB, `getLog`: https://www.mongodb.com/docs/manual/reference/command/getLog/
- [R3] MongoDB, `$currentOp`: https://www.mongodb.com/docs/manual/reference/operator/aggregation/currentOp/
- [R4] MongoDB, `serverStatus`: https://www.mongodb.com/docs/manual/reference/command/serverStatus/
- [R5] Redis, `KEYS`: https://redis.io/docs/latest/commands/keys/
- [R6] Redis, `MONITOR`: https://redis.io/docs/latest/commands/monitor/
- [R7] Microsoft, system_health session: https://learn.microsoft.com/en-us/sql/relational-databases/extended-events/use-the-system-health-session
- [R8] Microsoft, Query Store on by default in SQL Server 2022: https://www.microsoft.com/en-us/sql-server/blog/2022/08/18/query-store-is-enabled-by-default-in-sql-server-2022/ ; Manage Query Store: https://learn.microsoft.com/en-us/sql/relational-databases/performance/manage-the-query-store
- [R9] MongoDB, WiredTiger storage engine (tickets, queues): https://www.mongodb.com/docs/manual/core/wiredtiger/
- [R10] Microsoft, IIS `time-taken` field: https://learn.microsoft.com/en-us/troubleshoot/iis/time-taken-field-http-log
- [R11] Microsoft, HTTP.sys error log reason phrases: https://technet.microsoft.com/en-us/library/cc787144(v=ws.10).aspx ; Timer_ConnectionIdle: https://learn.microsoft.com/fr-fr/archive/blogs/webapps/should-i-worry-about-timer_connectionidle-entries-in-httperr-log
- [R12] Microsoft, WAS events: https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-server-2008-r2-and-2008/cc735034(v=ws.10)
- [R13] Redis, latency monitor: https://redis.io/docs/latest/operate/oss_and_stack/management/optimization/latency-monitor/
- [R14] Hangfire, SQL Server storage: https://docs.hangfire.io/en/latest/configuration/using-sql-server.html
- [R15] Microsoft, .NET built-in runtime metrics: https://learn.microsoft.com/en-us/dotnet/core/diagnostics/built-in-metrics-runtime
- [R16] Microsoft, ASP.NET performance counters: https://learn.microsoft.com/en-gb/previous-versions/visualstudio/visual-studio-2008/fxk122b4(v=vs.90)

---

## Appendix A: Data volume, retention and query-cost summary

| Source | Volume / horizon here | Query cost driver | Default bound |
|---|---|---|---|
| `ApiAuditLog` | ~778k docs, ~1 year; `_id` index only | COLLSCAN without pruning | `_id` range; 7 days (requests), 90 days (trend, day-split) |
| `system_health` | 08-15..10-23 (rolling files) | XML shredding on the server | ≤ 24 h window, `object_name` filter, `TOP 2000` |
| SQL error logs | 6 files; cycles on restart | Text scan | Allowlisted patterns, window filter |
| Default trace | Since restart (5 × 20 MB rolling) `[unverified size]` | Trace file read | Window filter |
| msdb backups / Agent | Long | Small tables | `TOP`, window |
| Hangfire | Cron permanent; runs ~1 day `[unverified]` | Small tables | `NOLOCK`, `TOP` |
| `mongod.log` | Until rotation `[unverified]` | Local parse of a copy | File size cap (default 2 GB per import) |
| FTDC | ~200 MB cap → days `[unverified]` | Local decode | Window-only decode |
| Event Log exports | Per channel size cap | Local `wevtutil` XML | Window query at export |
| IIS / HTTPERR | Daily files; retention per ops | Local parse | File-name window skip |
| App Insights | 90 days default `[unverified]`; sampled | KQL | ≤ 24 h, `take`/`summarize` |
| Redis | No history; slowlog 128 entries | `INFO`/`SLOWLOG` cheap | Watch ≤ 30 min @ 10 s |
| Perf counters | None unless a DCS exists | PDH cheap | Live ≤ 15 min @ 15 s; DCS time-boxed. **[Rev 2]** ≤ 30 min live (60 min with confirmation); DCS 5 days @ 15 s, ≤ 200 MB (§3.8) |
| **SQL live sampler (S14)** [Rev 2] | None (live) | 3 DMVs every 5 s, ≤ 200 rows | ≤ 30 min per run, ≤ 5 runs per day |

## Appendix B: Rule ID index

- **A**: audit foundations (P1).
- **B**: audit incident patterns (P2).
- **S**: SQL Server (P3).
- **M**: MongoDB (P4).
- **H**: Hangfire / Agent (P5).
- **W**: Windows (P6).
- **I**: IIS (P7).
- **C**: composite correlation (P8).
- **AI**: Application Insights (P9).
- **R**: Redis (P10).
- **T**: .NET runtime (P11).
- **L**: logs (P12).
- **G**: gated instrumentation.
- **[Rev 2] New rules:** B12 volley, B13 host-CPU recurrence, S14 live request sampler, M9 cold-start audit stall, W9 change marker, C9 CPU burst explains volley, C10 change explains chronic step, G9 per-user serialization.
- **[Rev 4] New rules (P0, prefix HP = host profile; D0–D3 stays the P8 UI depth levels):** HP1 config drift, HP2 recycle or task in burst window, HP3 agent as candidate burst source, HP4 clock or timezone mismatch, HP5 evidence at risk (retention shorter than the window).
- **[Rev 2] Rules downgraded:** S1 and C1 move to Later; B4 and B5 become secondary.
- **[Rev 2] Naming note:** the drilling ladders in §1A.C are named **L1–L5**. They are not P12 log rules. Rename the log rules to **LG1…** at implementation time to avoid the clash.
- **[Rev 5] New rule:** HP6 hint/detect conflict (P0 manual hints).
- **[Rev 5] Naming note:** the release IDs **R0–R13** (§25) and the references **[R1]–[R16]** (§26) share the letter with the Redis rules **R1–R3** (P10). In this document, a bare `R<n>` in §25 is a release, `[R<n>]` in brackets is a reference, and a rule is always written next to P10. Rename the Redis rules to **RD1…** at implementation time.
- **[Rev 6] New prefix V (validation rules, P2b, §5A.C):** V1 counted base rate, V2 recurrence permutation, V3 searched change point, V4 load stratification, V5 control-path continuity, V6 source sanity (precondition gate), V7 entity concentration, V8 multiplicity, V9 regime-aware baseline. V-rules guard and test; they never produce incident findings of their own.
- **[Rev 6] Naming note:** **TH1–TH19** (§5A.D) are theory IDs, not rules. They are distinct from the .NET runtime rules **T1…** (P11) and the Hangfire rules **H1…** (P5). Decisions 14–15 (§24) are decision numbers.
- **[Rev 7] Naming note:** the verdict-row text uses template keys of the form `<rule_id>.<outcome>` (for example `B9.pass`) and `reason_detail` keys (for example `v6.flat`, `p0.topology`), from `theory-text/1` (§5A.H). They are text-template IDs, not rules. The old report words (Observed, Consistent, Open, Rejected) are display history only, never statuses.
- **[Rev 5] Naming note:** decision numbers 1–13 (§24) are not rule or release IDs. G1–G9 are gated-backlog items, and gate N is the node gate (§21).

Every rule records `rule_id`, `rule_version` and the applied thresholds in its finding (§3.1). Changing a default bumps `rule_version` and visibly re-labels findings.
