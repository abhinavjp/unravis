# Triage: "AI Agent SQL Server Analysis Playbook" vs Unravis

Source: `ai_agent_sql_server_analysis_playbook.md` (external, written as a system prompt for a live AI DBA agent).
Date: 2026-10-29. Status: proposal only, nothing implemented.

## Context the reviewing agent needs

- Unravis is a **read-only** investigation tool: SQL Server, MongoDB and host evidence. It holds `VIEW`-level grants only and never changes a customer server.
- Owner ruling: **no SQL probe**.
- Guided-analysis cards come from fixed rules over collected evidence. Card schema: `{id, label, verdict, rule, chart, series, table, why, sampled}`. 35 cards today (SQL 11, Mongo 12, Host 12).
- Collector gaps are listed in `buildout/specs/2026-10-29-evidence-feasibility.md`. Existing SQL collectors live in `internal/dbhealth/sql.go`, `lane_sql.go`, `grants.go`.
- Wireframe work so far is mock data in `buildout/wireframes/parts/`. Go collectors are not changed by wireframe work.

## Adopt

| Playbook rule | Current state | Action |
|---|---|---|
| Dependency banners: Query Store off, `system_health` stopped, no `VIEW SERVER STATE` | The collector knows these states (`grants.go`, Query Store check). Cards do not show them, so a missing source can read as "no problem found". | Add a per-card "What we couldn't check, and why" strip. Add a `dependency_status` field to the evidence schema. |
| Replace generic "High CPU" with a cause | CPU card reports a percentage. | Name the top wait type or top CPU query from Query Store. See CPU row under "Partly" below. |
| Plain-English wait type on every anomaly | `waitMeaning()` translates the top server waits. No per-query wait. | Add a per-query wait field to the SQL evidence schema, shown in the query tables. |
| Numbered "how to investigate" steps, acronyms defined | Demo 2b help text (`EVHELP`) already has look / what / normal / issue / check / terms. | Check consistency only. No rewrite. |

## Not adopted as written (reasons, for the other agent to challenge)

| Playbook item | Reason | Question for the other agent |
|---|---|---|
| JSON payload shape (`anomaly_id`, `root_cause_queries`, ...) | Our schema drives 35 cards, help text, checkers and tests. The playbook shape has no chart, series or table and holds one wait type per query. Swapping means rewriting all of it for no user-visible gain. | Is there any field the playbook shape carries that ours cannot? Only `dependency_status` found so far. |
| "Enable Query Store" and "Start system_health" buttons | Product is read-only with `VIEW`-level grants. `ALTER DATABASE ... SET QUERY_STORE = ON` needs elevated rights and changes the customer's server. | Can the same help be shown as copyable command text with no write path? |
| "View Execution Plan" button and plan-XML root causes (missing index, no join predicate, table scan) | Plan XML is not collected (feasibility row 6: no `dm_exec_query_plan` join). Plans can embed literal values, which clashes with the "no payloads" stance. It touches the "no SQL probe" ruling. Needs a new collector, redaction and tests. | Is reading the plan cache or Query Store plans a probe under the ruling? Can literals be redacted reliably? |
| Query Store off, fall back to plan-cache DMVs (`dm_exec_query_stats`) | Second query-history source with different semantics: resets on restart, no per-interval history, so the reference-vs-incident comparison cannot use it. Needs its own logic and labelling. | Can the fallback support an incident window at all, or is banner-only the honest answer? |
| `system_health` stopped means "no historical data" | Not fully true here. `internal/discover/detect_sql.go:220` detects `system_health*.xel` for export and import, so history can exist while the live session is stopped. | What do imported `.xel` files give us for deadlock graphs? Banner should say "not available live; import the .xel files" when that applies. |
| Autonomous AI agent persona | Our cards come from fixed rules, so verdicts are reproducible and testable. An agent reading raw telemetry would not be. | Confirm there is no runtime LLM in the design. |
| "Why (Evidence)" claims (Microsoft Docs, SQLskills) | Asserted without citations. | Verify each against Microsoft docs before any user-facing text uses it. |

## Partly doable

| Item | Limit | Question |
|---|---|---|
| CPU card with a cause | Runnable queue and signal-wait share (feasibility rows 8 and 9) are not collected. Can name the top wait or top CPU query, but not "scheduler pressure vs just busy". | Add those two collectors, or word the card as "CPU is high; here are the top waits and queries"? |
| Per-query wait type | Assumed to come from Query Store wait stats, which exist only on newer SQL Server versions with wait capture on. Plan-cache DMVs have none. Not yet verified. | Verify version and setting requirements, and decide what the card shows when the data is absent. |

## Decisions needed from the owner

Recommendations are mine.

1. **Plan-cache or plan-XML read.** Allowed under "no SQL probe"? If yes, redact or omit literals? *Recommend: defer. Show the query ID with a Copy button.*
2. **Query Store off.** Build the plan-cache fallback, or banner only? *Recommend: banner only for now.*
3. **CPU and scheduler collectors.** Add runnable-queue and signal-wait collection now? *Recommend: yes, as a separate Go change. Until then, word the CPU card as above.*
4. **Wireframe or backend.** Is this round mock-data wireframe only, or does it include Go collector changes? *Recommend: wireframe first, plus a spec listing which collector each banner needs.*
5. **Banner scope and order.** Build the banner pattern before wiring Mongo and Host evidence (Steps 8 and 9, currently on hold for the Demo 2b review)? Extend it to their equivalents (profiler off, no host access)? *Recommend: yes to both, so the pattern is built once.*
6. **Remediation commands.** May the product show copyable fix commands (for example `ALTER DATABASE ... SET QUERY_STORE = ON`) as advice, without running them? *Recommend: yes.*

## Related items from the first external report (`sql_guided_analysis_comprehensive_review_enhancement_report.md`)

- Adopt: axis numbers and units, incident marker on stacked and bar charts, jargon as dotted-underline hover tooltips, an "In plain words" impact line per card, collapse Watch cards by default, Copy SQL on query tables.
- Reject: Kill Session (read-only product), View EXPLAIN Plan (needs a live query, SQL Server has showplans not `EXPLAIN`, "no SQL probe"), the PostgreSQL data-source table (wrong database).
- Defer: deployment markers (no data source), throughput-vs-latency chart (check feasibility first), DML breakdown chart (no cheap per-statement-type counters on SQL Server).

## Reviewing agent's answers (2026-10-29) and corrections

The reviewing agent agreed with all six recommendations. Status: **agreed between agents, awaiting owner confirmation.**

| # | Decision | Outcome |
|---|---|---|
| 1 | Plan cache / plan XML | Defer. Show query ID plus Copy button. No XML capture (literal redaction too risky). |
| 2 | Query Store off | Banner only. No plan-cache incident window. |
| 3 | CPU / scheduler collectors | Defer. Word the card "CPU is high; here are the top waits and queries". Backlog the two collectors. |
| 4 | Scope | Wireframe first, plus a spec mapping each banner to an existing collector. |
| 5 | Banner pattern | Build once, extend to Mongo and Host. |
| 6 | Remediation | Copyable command text only. Never executed. |

Per-query wait type: `sys.query_store_wait_stats` needs SQL Server 2017+ with wait-stats capture on. On older versions or with capture off, omit the wait column and show the note "Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled."

### Corrections to the reviewing agent's answers

1. **No `EXPLAIN` on SQL Server.** It carried that over from the PostgreSQL report. The Copy button text must say the user can open the plan in SSMS or Azure Data Studio (Estimated or Actual Execution Plan), by query ID.
2. **Wrong DMV.** `runnable_tasks_count` is in `sys.dm_os_schedulers`, not `sys.dm_os_workers`. Signal waits come from `sys.dm_os_wait_stats.signal_wait_time_ms`. Backlog entry must use these.
3. **`.xel` banner wording is premature.** The feasibility audit (row 3) says there is no deadlock-graph parsing yet, only detection of `system_health*.xel` for export. Do not promise "import to view past deadlocks" until an importer parses them. Interim wording: "Live deadlock capture is stopped. Past deadlocks may be in the system_health .xel files on the server."
4. **"Reading the plan cache is not a probe"** is the reviewing agent's opinion. The ruling is the owner's; decision 1 stands as defer either way.
5. **Mongo and Host banners need their own definitions.** The playbook only defines SQL states. The spec must list the Mongo and Host "could not check" states (for example profiler off, no host access) before the pattern is applied there.
