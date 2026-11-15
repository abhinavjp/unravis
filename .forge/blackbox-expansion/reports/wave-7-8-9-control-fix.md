# Wave 7/8/9 SQL control-plane fix — status and residual work

Date: 2026-11-15. Companion to [wave-10-11-data-fix.md](wave-10-11-data-fix.md).

## How this landed (read this before trusting the scope)

The control-plane subagent **hit its 150-turn limit and did not finish**. It was
mid-debug on an `install-sql` failure when it stopped, and it wrote no report. Its code
compiles and the suites pass, but "the agent stopped" is not "the findings are closed".
What follows separates what is verified from what is not.

Orchestrator completion work:
- The CLI rig's `fakeSQLDirEnv` did not implement the new `sqlLaunchGranter` interface, so
  every `install-sql` test failed with the generic "other" reason. Added
  `GrantSQLLaunchAccess` to the fake (recording calls). All CLI tests then passed. This was
  the exact failure the agent was debugging.
- Unified the duplicated budget divisor: `cmd/unravis-blackbox/sqlrunner.go` now uses
  `blackbox.SQLBudgetShare` instead of its own `sqlBudgetShare = 4`, so the host's reserved
  share and the SQL share cannot drift apart (W10-02 seam the data-plane worker flagged).
- Corrected the storage comment to the data-plane worker's max-width measurement
  (~319 MB/day, not the earlier ~200 MB/day) and recorded the open C13 owner decision.

## Verified green at commit time

```
go build ./...                                   clean
go vet ./...                                     clean
GOOS=linux go vet (bbsql, blackbox, cmd)         clean
GOOS=windows GOARCH=386 go build                 clean
go test ./internal/bbsql/ -count=1               ok 45.8s
go test ./internal/blackbox/ -count=1            ok 93.4s
go test ./cmd/unravis-blackbox/ -count=1         ok 3.9s
TestSchedulersSemaphoresTempdbRegistered -count=2 ok (W11-07 reproduction now passes)
```

An earlier run showed `TestFixedQueryErrorUsesTheContextReason` failing and two blackbox
"server never listened" failures. Both were transient: the first was the control-plane
agent mid-edit (it passes on re-run, and a direct probe of `fixedQueryError` returns
`query-timeout` correctly), the second is the documented load-sensitive flake when several
packages and agents run at once (blackbox alone is green). Neither was "fixed" by changing
an assertion.

## Findings addressed in code (control plane)

- **W7-01** — `GrantSQLLaunchAccess` gives the SQL service SID least-privilege read/execute
  on the installed program and read on the settings, with directory traversal; write/owner/
  DACL stay admin-only. `InstallSQL` now also requires the installed exe and the settings
  file to actually exist, so a supplied config alone no longer proves a host installation.
- **W7-03** — `SecureSQLDir` verifies owner, ancestors and existing children before mutating
  anything, and refuses reparse points.
- **W7-05** — the C2 lint gained grammar checks beyond prefix/count/blacklist.
- **W8-01 / W8-02 / W9-02** — connection lifecycle and ceiling admission work landed in
  `conn.go`/`ceiling.go`, including a `ceilingError` origin that `fixedQueryError` preserves
  so a failed C8 proof is not flattened into a generic query code (W9-03).
- **W8-03 / W10-06** — install/activation separation and an owner enable path.
- **W8-04** — `/status` validation and freshness.
- **W8-07 / W8-09** — fixed CLI sentences; absolute canonical config path.
- **W10-11** — folder trustee/identity pinning.

## NOT verified closed — do not read this as a pass

Because the agent stopped early, the following are **unverified** and need a focused pass:

1. **Per-physical-connection ceiling (W8-02/W9-02).** Code changed, but there is no test
   proving a `database/sql` pool replacement or `ErrBadConn` retry cannot run a sampler on
   an unproved generation. This needs a fake `database/sql/driver` recording connection IDs.
2. **Driver deadline reality (W8-01).** The data-plane worker reports `ConnectFunc`/`Open`
   still need a `ctx` parameter for cancellable connect. Until that seam is threaded, the
   5 s connect / 5 s tick bounds are not proven against prelogin/TLS/cancel-drain stalls.
3. **Effective rights (W9-01).** No live proof that custom/nested roles, Windows groups,
   public and SQL 2022 `##MS_*##` roles are covered; needs a real SQL Server matrix.
4. **W8-06** — whether driver `Ping`'s untagged `select 1;` is gone from the startup
   transcript is not proven by a wire-level test.
5. **Install/activation ordering (W8-03)** — the CLI tests pass with fakes; a real elevated
   install with a delayed owner grant is unproven.

## Open owner decisions (not silently taken)

- **C13 "<2 KB per sample" is UNMET** at the C6 row caps: a realistic filestats line is
  ~4.4 KB and an at-cap max-width line ~53 KB. The bound was **not** relaxed. It needs an
  explicit owner ruling, or a row-cap/payload change.
- **W11-02 / W11-03** need `allowlist.go` statement changes (numeric `pool_id` for the
  composite semaphore key; an AG locality predicate). Not made here.
- Same-second PID reuse at the per-process join needs `ResolvedTarget.StartTicks`
  (`groups.go`) and an `IISWorker` creation field (`groups_types.go`) — wave-5 follow-up.

Everything above that needs a real SQL Server, a real elevated install or a live Windows
host remains an **Unverified Assumption** until the owner runs it. `docs/BACKLOG.md` stays
unticked.
