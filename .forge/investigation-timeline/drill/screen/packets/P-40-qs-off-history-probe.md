# P-40 Investigation: can anything be shown when Query Store is switched off? (BL-016)

model: sonnet, effort: medium. Reason: read-only investigation with a written answer; no code.
risk: medium (touches the SQL read path, read-only). Review batch: RB-6 reads the report. Wave: W0. Requirement: R-09 (BL-016).

HARD LIMITS: read-only. Never run `ALTER DATABASE`, never switch Query Store on, never write to the customer database, never print a connection string, password or token. No code change in the repo.

## Outcome
`screen/probe/qs-off-history.md` answers, with evidence: when Query Store is off, which history (if any) the app's existing read-only queries can still read, how far back, and what the screen should say. It ends with a recommendation: "nothing more can be shown" or a named follow-up packet (which the owner must approve; it is not built in this plan).

## Write scope
- `.forge/investigation-timeline/drill/screen/probe/qs-off-history.md` (new)

## Must not change
- Any source file. Any database setting. Do not read `.env` or the `sql/` folder.

## Changes
1. Code reading (always possible): in `internal/dbhealth/lane_sql.go` and `lane.go` list every catalog view / DMV the lane read touches (names only), which of them need Query Store ON, and which do not (for example plan-cache DMVs such as `sys.dm_exec_query_stats`, if the code already reads them). Give file:line for each. Do not propose a new query in code; describe it in words only.
2. From the code and P-16's `LaneQueryStore.State` values, write the table: state (`off`, `read-only`, `on but started late`, `permission denied`) → what the read returns today → what is knowable (first data time: yes/no; when it was switched off: not knowable, per ruling D-9).
3. Plan-cache alternative, on paper: what `sys.dm_exec_query_stats` could give (totals since each plan was cached, no time series, lost on restart or cache eviction), the permission it needs (`VIEW SERVER STATE`), and why it cannot draw a per-step lane (no history per interval). State clearly that this is from SQL Server's documented behaviour, not measured.
4. Live check (only if the owner has a connected database whose Query Store is OFF): through the app's own "read the database" action on a case, record the lane's reason code, `queryStore.state` and the sentence shown. If no such database is available, write "LIVE: not checked, no database with Query Store off was available" and stop there. Do not ask for one to be changed.
5. End with: recommendation; the exact sentence the screen shows today for `off` (`DRILL_TEXT.dbMsgOff`, from P-02); owner question if any.

## Proof
- [ ] The file exists, has the state table and the list of views with file:line.
- [ ] `Select-String -Path <file> -Pattern 'Password=|Pwd=|Server=.*;|[A-Za-z0-9_\-]{32,}'` → nothing.
- [ ] `git status --short | grep -v "^ M\|^??.*\.forge"` shows no file from this packet outside `screen/probe/`.
- [ ] The report says plainly "live-checked" or "not live-checked, reason".

## Depends on
Nothing (reads P-16's packet for the state names; does not need its code).
