# BBX-2-04 SQL host performance counters (no login), default + named instances
Backlog: BL-100, BL-107 | Group: 2 | Model: performance | Reason: instance discovery (default vs MSSQL$named) + a large counter set; host counters only, no SQL login.
Depends on: BBX-1-05, BBX-1-06 | Blocks: BBX-5-16 (lane can use these)

## Goal
Sample SQL Server host performance counters every 15 s with NO SQL login (host counter access only), for the default instance and every named instance (`MSSQL$<name>`): the BL-100 base set plus the BL-107 extension.

## Context
- New file `internal/blackbox/sqlhost.go` (no tag) + test `internal/blackbox/sqlhost_test.go` (fakes). Group pattern as BBX-2-01 (init RegisterGroup("sqlHost", true, ...); optional).
- Instance discovery: the PDH object prefix is `SQLServer:` for the default instance and `MSSQL$<NAME>:` for named instances. Discover which exist by enumerating PDH objects (`PdhEnumObjectsW`, lazy pdh.dll) and matching prefixes `SQLServer:` / `MSSQL$`. (The repo also reads `HKLM\...\SQL Instance Names\SQL` in discover — you may reuse that, but PDH enumeration is authoritative for which counters exist.)
- Researched counters (UNVERIFIED — live check), per instance prefix `<P>`:
  - BL-100: `<P>:SQL Statistics\Batch Requests/sec`, `<P>:General Statistics\User Connections`, `<P>:Locks(_Total)\Lock Waits/sec`, `<P>:General Statistics\Processes Blocked`.
  - BL-107: `<P>:Memory Manager\Memory Grants Pending`, `<P>:Buffer Manager\Page life expectancy`, `<P>:Buffer Manager\Free list stalls/sec`, `<P>:SQL Statistics\SQL Compilations/sec`, `<P>:SQL Statistics\SQL Re-Compilations/sec`, `<P>:Locks(_Total)\Number of Deadlocks/sec`, `<P>:Locks(_Total)\Lock Wait Time (ms)`, `<P>:Databases(_Total)\Log Flush Wait Time`, `<P>:Access Methods\Forwarded Records/sec`.
- `SQLHostGroup{Instances []SQLHostInstance}` with the 13 fields (groups_types.go). Instance label: "MSSQLSERVER" for default, the named instance name otherwise.
- Rules: **no SQL login** (host counters only); **machine-wide** (all instances); **two baselines before a rate**; **valid-status check**; **missing never 0**; **no text** (instance names + numbers).

## Do
1. `discoverSQLInstances() ([]string, error)` — enumerate PDH objects, return distinct prefixes (default + named). Factor so tests can fake it.
2. Factory builds a PDH source from the per-instance counter paths for every discovered instance; engine specs map each to the SQLHostInstance field (rates = KindPDHRate; UserConnections/ProcessesBlocked/MemoryGrantsPending/PageLifeExpectancy = KindGauge; LockWaitTimeMs/LogFlushWaitTime = gauge ms).
3. `Collect`: engine.Process, group fields by instance → []SQLHostInstance. An instance with no valid counters → omit it. Return *SQLHostGroup; nil if none.
4. Re-discover instances only on ProcessSetChanged() (a new SQL instance is rare); otherwise reuse handles.

## Do not
- Do not open a SQL connection or use any login (this is host PDH only).
- Do not emit 0 for a missing counter or a stopped instance — omit.
- Do not capture query text (counters only).
- Do not hardcode only the default instance — named instances must work.
- Do not re-discover every tick; no goroutine/ticker.

## Tests to write first (fakes)
- `TestSQLHostDefaultAndNamed` — fake discovery returns ["SQLServer:", "MSSQL$PAYROLL:"] → two instances with correct labels.
- `TestSQLHostFieldMapping` — fake readings map to the right fields (BatchRequestsPerSec etc.).
- `TestSQLHostRateGated` — a /sec counter absent until two baselines.
- `TestSQLHostMissingOmitted` — invalid counter → nil field; instance with all invalid → omitted.
- `TestSQLHostNoDiscoveryNil` — no SQL instances → nil group (not an error).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handles; re-discover on change only; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestSQLHostMissingOmitted)
- [ ] No text captured; no SQL login used (code review)
- [ ] Default AND named instances (TestSQLHostDefaultAndNamed)
- [ ] Two baselines before a rate; valid-status check
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — on a real SQL server run `typeperf -q | findstr /i "SQLServer MSSQL$"` to confirm object/counter names (especially "Lock Wait Time (ms)" and "Log Flush Wait Time"); compare Batch Requests/sec to perfmon. Confirm named-instance prefixes. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-2-04.md`.