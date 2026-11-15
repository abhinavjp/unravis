# P-43 Security review of everything that reads SQL Server or MongoDB (read-only)

model: opus, effort: high. Reason: owner rule: any packet that touches SQL access gets an Opus security review.
risk: high. Review batch: RB-6 (this packet IS the batch). Wave: W7. Requirements: R-19, plan ground rules.

HARD LIMITS: read-only. You edit no source file. You do not run the app against a live database. You do not spawn subagents. Output at most 60 lines.

## Outcome
`screen/reviews/RB-6-security.md` holds the findings and a verdict SHIP or FIX-FIRST for the data-access changes of this plan.

## Write scope
- `.forge/investigation-timeline/drill/screen/reviews/RB-6-security.md` (new)

## Must not change
- Everything else.

## Changes
1. Read the diffs of these packets only (`git diff -- <paths>`): P-13 `internal/audit/activity*.go`; P-16 `internal/dbhealth/lane.go`, `lane_sql.go`; P-17 `internal/store/activity.go`; P-18 `internal/server/activity.go`; P-20 `internal/server/tracing_sqllive.go`; P-21 `internal/server/tracing_qsfile.go`; P-34 `internal/store/record_perf.go`; P-35 `internal/audit/models.go`; P-36 `internal/store/requests.go`; P-37 `internal/audit/sqlbusy*.go`; P-38 `internal/store/sqlbusy.go`; P-39 `internal/server/tracing_apiseen.go`; plus `screen/probe/fields.md` and `screen/probe/qs-off-history.md`.
2. Check, and cite file:line for each answer:
   - SQL Server: every statement is a SELECT on catalog views / DMVs; no `ALTER`, `EXEC`, `SET QUERY_STORE`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`; nothing switches Query Store on or changes its mode; identifiers and values are parameters, never string-built from request input; timeouts are set.
   - MongoDB: read-only operators only (`$match`, `$group`, `$project`, `$sort`, `$limit`); no `$out`, `$merge`, writes or index creation; every new query is bounded by an indexed time range, a `$limit` and a server-side time limit; 3.4-safe operators.
   - Input: the new `userId` / `operation` filters (P-13, P-17, P-18) are validated for length and type before use, are compared as values (no operator injection through a JSON object, no regex built from user text unless escaped and anchored), and strict JSON decoding still rejects unknown fields.
   - Privacy: no record text, user id, SQL text, connection string, token or host name goes to a log line, an error string returned to the browser, a test fixture or a probe file. `userId` is not written to logs.
   - Authorisation: the changed handlers still sit behind the same token check and case/source ownership checks as before.
   - Demo: the pretend switch cannot be reached when `app.info.demo` is false (read `CallInspector.svelte` and its mount).
3. Findings format, one per line: `SEC-nn | severity blocker|major|minor|nit | confidence high|medium|low | file:line | evidence (quoted code, max 1 line) | one-line fix | owning packet P-nn`.
4. Last line: `VERDICT: SHIP` or `VERDICT: FIX-FIRST` (any blocker or major → FIX-FIRST).

## Proof
- [ ] The findings file exists, at most 60 lines, every finding has all seven fields.
- [ ] A line `VERDICT: …` is the last line.
- [ ] `git status --short` shows no source change from this packet.
- [ ] For every checklist bullet in step 2 there is either a finding or a line `OK: <bullet> (file:line)`.

## Depends on
P-13, P-16, P-17, P-18, P-20, P-21, P-34..P-40 done (skipped packets are listed as "skipped", not reviewed).
