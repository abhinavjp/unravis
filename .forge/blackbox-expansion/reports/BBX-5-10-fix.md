# BBX-5-10 fix round — Ultimate gate on BL-123 + BL-124 (SQL subsystem)

Date: 2026-11-14. One fix round, per the orchestration rule: batch review findings go back
once to the code, then commit. Scope of the gate: the whole shipped SQL subsystem
(`internal/bbsql/*`, `cmd/unravis-blackbox/sql*.go`, `grant.go`) against controls C1–C14.

## Findings and what was done

| id | finding | change | proof |
|---|---|---|---|
| H1 | The query seam flattened every driver failure to one code, so "the login lacks a grant" was indistinguishable from "the server is down". C7 auto-off could therefore never fire on the one failure class that must stop the service, and the operator got a back-off instead of a denial. | `fixedQueryError` (internal/bbsql/conn.go:361) classifies at the seam: `Classify` keeps the driver number/phrase mapping, a live `ctx.Err()` becomes the C4 query-budget code, only genuinely unknown driver errors keep the generic query code. Raw driver text is still never stored or surfaced (C10). | `internal/bbsql/conn_queryerr_test.go`: driver codes survive classification; the context reason wins over the driver text; a raw `mssql.Error{Number:229}` through the *real* helper reaches `CodePermission` → `AutoOff` → `StateDenied` → `/status` disabled. Mutant M1 killed. |
| H2 | The service hard-coded its own disk bounds and used a nil-prone clock field when stamping the connection, so the owner's config never reached the runner and a reset path could panic. | `sqlrunner.go`: `sqlDiskBounds` derives `MinFreePercent`, `KeepDays*24` hours and `MaxBytes()/sqlBudgetShare` (share documented: the SQL folder gets a quarter of the owner's budget; 2 GB ≈ three days of worst-case samples, oldest whole hours first). `reconnect` stamps with the nil-safe `r.now()`. | `cmd/unravis-blackbox/sqlrunner_test.go`: bounds for a normal config and for the smallest legal one (all positive); `sqlFreePercent` range. |
| H3 | Nothing checked that the `<folder>\sql` DACL is the one `install-sql` set. A writable or inherited folder lets another account plant, read or fill the samples, and the service would have recorded into it anyway. | New `internal/bbsql/folder.go` (pure fail-closed rule: no DACL / unknown ACE form / unprotected / inherited ACE / empty grantee list / a grantee outside SYSTEM, Administrators, the service SID → refuse) + `folder_windows.go` (`GetNamedSecurityInfo`, `GetAce`, `INHERITED_ACE`, allow forms collected, deny forms ignored, object forms marked unknown, own SID from the process token) + `folder_other.go` (no ACL reader means refuse, never assume). Wired before the first connect: the service does not start on an unlocked folder. Fixed code `folder-lock`; the sentence names no path, SID or account. | `folder_test.go`: accept table (the install-sql DACL, reordered and lower-case, service-only), 12 refusal cases, message-leaks-no-path test, real temp-folder smoke test. Mutant M7 killed. **Behaviour change: dev and CI runs must go through `install-sql` or point at an already-locked folder.** |
| M4a | Age retention alone does not bound the folder: the measured worst case is ~342 MB a day, so the 168 h window is ~2.4 GB with no ceiling. | `rotate` drops age-expired hours, then whole hours oldest-first while `MaxBytes > 0 && total > MaxBytes && len(kept) > 1` — the newest hour is never dropped, because a reader must always find the file the service writes into. | `runner_c13_test.go:TestByteCapDropsOldestHours`. Mutant M4 first **SURVIVED** (the post-tick folder looked the same whether the sweep kept the newest hour or deleted everything and let the tick rewrite it), so the test now also calls `rotate` directly with room for nothing and asserts one file survives and that it is the newest hour. M4 killed on re-run. |
| M4b | Rights inherited through Windows groups or custom server roles are not checked — the ceiling looks at the principal's own grants. Needs a recursive `sys.server_role_members` walk. | **Not fixed this round.** Recorded as a known limitation for the owner; the ceiling is still enforced at every connect, it is just not complete for group-inherited membership. |
| M5 | `OnSuccess` reset the whole back-off, so one fast sample erased a slow-response demotion and the service went straight back to the busy cadence. | `State.OnSuccess` now floors at `DemotedInterval` while `SlowStrikes > 0`; the strike count is cleared only by a genuinely fast `OnSlow` sample. Auto-off still survives restarts and is re-enabled only by the owner. | `backoff_test.go:TestSlowDemotionSurvivesSuccess`. Mutant M6 killed. |
| M6 | `r.rotated` was marked before the sweep ran, so one unreadable directory entry left the folder unbounded for the rest of the hour with no signal at all. | The hour is marked only after a successful sweep; a failure returns the fixed `errDiskSweep` (`disk-low`, "could not be swept, so its retention bound is not enforced") and the next tick retries. | `TestSweepFailureIsRetriedAndFixed` (folder pointed at a regular file: code, sentence, `rotated` still empty, nothing sent). Mutant M5 killed. |
| M7 | A free-space read error was ignored, so sampling continued with no idea of the drive. | `checkDisk` fails closed: an unreadable percentage stops the tick; free space is not an instance failure and does not count towards the C7 back-off. | `TestUnreadableFreeSpaceStopsTheTick`, plus the existing floor test that asserts nothing is sent below the floor. Mutant M3 killed. |
| L8 | `sqlrunner.go` carried a dead `time` import and stub leftovers after the budget rewrite. | Loop rewritten: `MkdirAll` → folder check → runner construction with owner bounds → `Run(ctx)`. | compiles clean; `gofmt`; `go vet` windows + linux. |
| L9 | Row caps were not pinned per statement: the Go-side cap and the SQL `TOP n` could drift silently, and one statement passed only by falling through to a default. | `output.go`: `CapCeilingPerms = 2`, `CapSingleRow = 1`, explicit `case` per statement. `allowlist_test.go`: `TestEveryStatementHasItsOwnRowCap` — registry size equals the required id list, `statementCap(text)` equals the SQL `TOP n`, no statement has two `TOP` clauses, no `GROUP BY` without `TOP`, scalar/SUM selects pinned to one row. | It found two real defects: `bb:ceiling_perms` had `TOP 2` against a Go cap of 1, and `bb:ceiling_role` only passed through the default. Mutant M8 killed. |
| L10 | The C5 hourly reconnect and the C8 ceiling re-check at that reconnect were not covered. | `TestConnectionLifetimeReprovesCeiling`: one ceiling statement at connect, still one and zero closes inside the lifetime, two sends and one close past `ConnMaxLifetime`, plus `connExpired` boundary checks. | Mutant M2 killed. |
| L12 | C13 was asserted with a Go-heap proxy and no per-line bound. | `budget_test.go` now measures real RSS (`meminfo_windows_test.go` reaches psapi through `K32GetProcessMemoryInfo`, falling back to `GetProcessMemoryInfo`; the non-Windows twin returns `(0,false)` so the gate *skips* instead of substituting a heap number), pins `maxLine` in both tiers, and documents the owner decision below the caps. | Measured 2026-11-14, this machine: realistic tick 24.6 ms CPU (cap 150 ms), 173 KB allocated/tick, mean line 680 B, mean tick 3 230 B, worst tick 7 826 B; at-cap tick 7.8 ms CPU, mean line 3 550 B, mean tick 16 864 B, worst tick 59 421 B → 342 264 960 B/day at the 15 s cadence; working set 16.9 MB → 17.6 MB over 60 ticks (growth 684 KB, cap 10 MB); goroutines 2 → 2. |
| L13 | Under-grant: `bb:filestats` (BL-119) needs the databases to be *visible*, and the login had no `VIEW ANY DATABASE`, so rows for databases the service cannot see simply never appear. | `grant.go`: `GRANT VIEW ANY DATABASE TO [NT SERVICE\UnravisBlackboxSql];` with a comment that it is a visibility right inside the C8 ceiling, plus the matching `REVOKE` in the revoke script. `TestGrantScriptLint` extended: allowed statements only, both grants required, and `CONTROL SERVER`, `ALTER TRACE`, `VIEW DEFINITION` asserted absent. | lint test in `cmd/unravis-blackbox/sqlcli_test.go`. |
| L11 | The lane reader's tolerance of `"rows":null` was flagged. | **Not changed** — needs the real web reader against real sample files before touching the JSON shape. Listed under live-server checks. |

## Only a real server can prove these

- Folder lock (H3) against a real `install-sql` DACL on the actual drive, including a folder
  created before this change and one with an explicit deny entry.
- Auto-off (H1) against a login whose rights were revoked mid-run, and re-enable by the owner.
- Named instances: `conn.go` dials TCP/1433 and drops the `lpc:` hint, so a named instance is
  not reachable from this code path yet — untested, and no test claims otherwise.
- Real RSS growth under a loaded instance (the local number is a quiet machine, 60 synthetic ticks).
- `Open`'s worst-case 5 s ping stop latency on an instance that accepts TCP but never answers.
- L11: `"rows":null` compatibility with the dashboard reader.
- Row caps against actual DMV cardinalities (the caps were chosen from the control document,
  not from a busy server's `sys.dm_os_wait_stats` distribution).

## Owner decisions (not hidden behind a threshold)

1. C13 says "<2 KB per sample". The mean line is 680 B and the mean tick is 3.2 KB, but one
   realistic `bb:filestats` line is 4 463 B (40 files in the group). The claim holds as a mean,
   not as a per-line cap; the test asserts both numbers instead of quietly raising the bound.
2. Storage: ~342 MB/day at the caps → ~2.4 GB over the 168 h window. The default 2 GB SQL
   budget share therefore holds about three days of worst-case samples.
3. H3 changes service start-up: an unlocked folder now refuses to run. That is intended, and it
   will be felt by dev/CI flows that point the SQL service at a plain folder.
4. M4b (group-inherited rights) is a known gap, not a fixed one.
5. The Runner Hub operator token was pasted into chat earlier in this work — rotate it.

## Verification run for this round

```
gofmt -l            # only pre-existing CRLF files flagged; the new/edited LF files are clean
go vet ./internal/bbsql/ ./cmd/unravis-blackbox/
GOOS=linux go vet ./internal/bbsql/ ./cmd/unravis-blackbox/
GOOS=windows GOARCH=386 go build ./internal/bbsql/
go test ./internal/bbsql/ ./cmd/unravis-blackbox/ -count=1
  ok  api-audit-explorer/internal/bbsql      30.6s
  ok  api-audit-explorer/cmd/unravis-blackbox 3.2s
```

Mutation evidence (each mutant applied alone, targeted `go test -run`, file restored byte-for-byte):

```
M1 flat query error            -> KILLED | clean-restore
M2 connExpired off             -> KILLED | clean-restore
M3 disk reader error ignored   -> KILLED | clean-restore
M4 byte cap can empty folder   -> KILLED | clean-restore
M5 rotate marked before run    -> KILLED | clean-restore
M6 OnSuccess clears strikes    -> KILLED | clean-restore
M7 folder grantee check off    -> KILLED | clean-restore
M8 ceiling perms cap removed   -> KILLED | clean-restore
```

8/8 killed. M4 needed a test rewrite first — recorded above rather than smoothed over.

## Correction (same day, after wave 11 integrated)

The ~342 MB/day and ~2.4 GB figures above were computed while the C13 fixture still ran
three of the seven samplers on the wrong cadence (requests at 60 s, waits and tempdb at
15 s). With `bb:waits` and `bb:tempdb` moved to their documented 60 s cadences and the
fixture aligned to the registry, the honest figures are:

- realistic tier: ~19 MB/day (mean tick 3 269 B, worst tick 7 826 B);
- at-cap tier: ~200 MB/day (199 877 760 B), each kind at its own cadence, so the 168-hour
  window is ~1.4 GB, not ~2.4 GB;
- the default 2 GB budget gives the SQL folder ~537 MB, i.e. a little over two days at the
  saturated worst case (the share itself is unchanged).

`budget_test.go` now prints the cadence-aware projection, `internal/bbsql/sampler_registry_test.go`
fails if the fixture and the registry disagree again, and the comments in `runner.go` and
`cmd/unravis-blackbox/sqlrunner.go` carry the corrected numbers. Full record:
`BBX-5-11-12-13-17-integration.md`.
