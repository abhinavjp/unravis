# Packet F1: can the collectors really capture the evidence the wireframe will show?

Goal: one analysis table. No code changes. Output is a Markdown document returned in your reply.
Depends on: nothing. Read-only.

## 1. Context (facts; do not explore beyond the files named)
- The research file `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md` lists the evidence a slowdown investigation should show for SQL Server, MongoDB and the host (sections "SQL Server", "MongoDB", "Host resources", "Tools to add"). Each bullet or sub-bullet there is one **evidence row**.
- The product is Unravis (Go backend, read-only collectors, per-case storage). Collector code lives in `internal/` (look first in `internal/collect`, `internal/sqlconn`, `internal/dbhealth`, `internal/resource`, `internal/sampler`, `internal/hosts`, `internal/evidence`, `internal/discover`, `internal/blackbox`). Ad-hoc probe scripts are `sql/*.js` and `sql/*.sql` (e.g. `mongo_*.js`, `checks.sql`, `qs_analysis.sql`, `cpu_minute.sql`, `net_wait.sql`). The guided UI tool list is `web/src/lib/guided/tools.ts`.
- "Captured" means: the code already runs a query, command or counter that returns this data AND stores or returns it to the UI. A one-off probe script in `sql/` that was run by hand counts as **partial** (the data can be obtained, the product does not collect it).

## 2. Task
For EVERY evidence row in sections SQL Server, MongoDB and Host resources of the research file (do not skip any; if a bullet holds several measures, split it into rows), produce one table row:
`| # | Source (SQL/Mongo/Host) | Evidence row (short) | Captured: yes / partial / no | Where (file:symbol or sql/ file) | What is missing | Needs elevated permission? (none / VIEW SERVER STATE / admin / unknown) |`
Then for every row marked **no** or **partial**, one word in a last column: `gray` (draw it in the wireframe as a gray "not collected yet" state) or `skip` (cannot be gathered read-only; do not draw).

Facts you must check in code, not guess: search with grep for the DMV / command / counter name (for example `dm_exec_query_stats`, `Query Store`, `query_store`, `xml_deadlock_report`, `dm_os_wait_stats`, `serverStatus`, `currentOp`, `system.profile`, `Win32`, `typeperf`, `Get-Counter`, `perfmon`). Quote the file path and symbol. If you cannot find it, say `no` with `none found for: <terms searched>`.

## 3. Do not
Edit any file, run the product, or invent evidence rows that are not in the research file. If a row is ambiguous, pick the safest reading and add it to Notes.

## 4. Output (exactly this structure, nothing else)
```
# Evidence feasibility (F1)
<one paragraph: totals, e.g. "31 rows: 9 yes, 12 partial, 10 no; 4 skip">
## SQL Server
<table>
## MongoDB
<table>
## Host resources
<table>
## Summary for the wireframe
- Rows safe to draw as real: <numbers>
- Rows to draw gray "not collected yet": <numbers>
- Rows to skip: <numbers>
## Notes
<max 5 bullets>
```
Reply is capped near 16,000 output tokens: keep each cell to one short phrase.
