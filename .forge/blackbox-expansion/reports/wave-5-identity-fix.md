# Wave 5 identity fix — W5-01..W5-04

Scope: `internal/blackbox/clr.go`, `proc10.go`, `procio.go`, `iis.go` and their
tests, plus one new test file `proc_identity_test.go`. No other file was edited.
No commit/push. No live process/service operation, no upload. Both build tags
compile.

## Identity contract (what the shared view actually exposes)

The resolver confirms every target against the inventory on the **lossless**
`(PID, StartTicks)` key (`ProcInfo.StartTicks`, raw FILETIME). But the per-tick
shared view (`tickView.targets`) carries `ResolvedTarget`, whose creation field
is `Start` — **unix seconds** (`groups.go:15`). `ResolvedTarget` has **no
`StartTicks`** field, and `tickView`/`GroupDeps` expose no inventory accessor.
`groups.go`, `groups_types.go`, `resolve*.go`, `host.go` are all out of scope.

So the join the groups can perform is:

- Capture the ONE shared view **once, before collection** (`targets :=
  g.targets()`), and never call `g.targets()` again afterwards. This removes the
  W5-01 post-collect re-resolution that emitted old readings under a new identity.
- Read the reading-side creation **losslessly from PDH**: `\Process(*)\Creation
  Time` (FILETIME ticks). proc10/procIO read it directly; CLR and IIS add
  `\Process(*)\ID Process` + `\Process(*)\Creation Time` mapping counters to their
  own query (one atomic `PdhCollectQueryData`) to map PID -> creation, since the
  CLR and W3SVC_W3WP objects expose no creation time themselves.
- Confirm a reading against a captured target with `creationConfirmed`: the
  reading's FILETIME must fall in the target `Start` second's tick window
  `[Start*1e7+epoch, (Start+1)*1e7+epoch)`, with a 16-tick slack for PDH's float64
  formatting (one ulp at the current epoch). The slack is microseconds; it can
  never bridge two whole seconds, so a different generation >= 1 s away is always
  rejected, and a delayed read of the same generation always confirms (identity is
  independent of the collection clock — W5-04).
- **Ambiguous duplicate mappings fail closed**: a PID claimed by more than one
  Process instance (or, for CLR, more than one CLR instance) is rejected entirely,
  never first-wins.

**Residual that genuinely needs a forbidden file (reported, not edited):** two
same-PID lifetimes created within the *same unix second* cannot be distinguished
at the group join, because `ResolvedTarget` carries only `Start` seconds. Closing
that requires `ResolvedTarget.StartTicks` (in `groups.go`) populated from the
inventory. Likewise `IISWorker` (`groups_types.go:21`) has no creation field, so a
verified worker generation cannot be carried on the wire; and for an
out-of-process pool the view carries the *child's* identity, not the parent
worker's, so the parent's creation cannot be cross-checked against the view. These
are contract changes in `groups.go`/`groups_types.go`, which I did not touch.

## Per-finding changes

**W5-01 (CLR/proc10).** Both now capture the view before `src.Collect()` and join
on `(PID, creation)` from that captured slice — never a post-collect `Targets()`.
proc10 reads `Creation Time`; CLR maps CLR-instance -> PID (CLR `Process ID`) ->
creation (Process object) and confirms against the target `Start`. Duplicate PID
mappings fail closed. `t.Start == 0` (unreadable creation) never confirms.

**W5-02 (IIS).** Added Process-object `ID Process` + `Creation Time` to build a
**verified worker set** (PID -> creation, ambiguity rejected) from the same atomic
query — kept separate from the application-child target identities. Each
`<PID>_<pool>` instance is accepted only if: the worker PID is a live, unambiguous
process with a confirmed creation; the pool is selected; and, when the selected
target IS that worker (in-process), its creation confirms the target `Start`.
Out-of-process parents and overlapping (web-garden) workers are accepted on
verified-alive + selected-pool. Wrong/exited worker PIDs and stale in-process
generations are rejected. No command lines are read.

**W5-03 (selection boundary).** Identity-mapping counters are read from the raw
sample; numeric detail is filtered to verified selected instances **before**
`engine.Process`, so unselected detail never enters engine output or rate history.
The `errNoTargets` factory guard is preserved (no machine-wide detail source for an
empty selection). Machine-wide counters elsewhere are untouched. See PDH limits
below.

**W5-04 (procIO).** Removed `procIOStartSkew` and the `now.Unix()-elapsed`
estimate entirely. Identity is `Creation Time` (precise, clock-independent);
`Elapsed Time` is retained only as the `ElapsedSec` metric. A different creation
within 3 s is rejected; a valid delayed read is accepted.

## What PDH can and cannot narrow

- **Cannot:** `PdhExpandWildCardPathW` expands a wildcard to **every** instance;
  there is no API to expand to a selected subset, and `PDHSource.Collect` formats
  every added counter (`pdh_windows.go`, out of scope). Truly narrowing the native
  query to selected instances would mean rebuilding it with per-instance paths on
  every selection change — that lives in `pdh_windows.go` and would add per-tick
  query churn. **I did not pretend the query was narrowed.**
- **Can / did:** filter at the earliest point under my control — mapping counters
  read raw, numeric detail filtered to verified selected instances before the
  engine. Added only lightweight mapping counters (Process `ID Process` +
  `Creation Time` for CLR/IIS; `Creation Time` for proc10/procIO); numeric detail
  stays selected-only. Source-spy tests assert unselected detail never reaches
  engine history (rate groups) / output (IIS gauges), not just emitted rows.

## Pre-existing assertions updated (old behaviour encoded)

- `procio_test.go`: `TestProcIOStartMismatch` now uses `Creation Time` (was
  `Elapsed Time`); `procIOInstance` adds the `Creation Time` path; new `procIOIdent`
  supplies identity; `procIOAging` ages only `Elapsed Time` (creation is fixed).
- `proc10_test.go`: `proc10Instance` now takes `start` and emits `Creation Time`;
  added `newProc10TestGroupFn` (targets func) for the mid-collect test.
- `clr_test.go`: added `clrProcessIdent` (Process-object identity); every CLR test
  now supplies PID -> creation.
- `iis_test.go`: `TestIISGroupPoolMatch` previously accepted workers 100/200 for
  target PID 999 with **no inventory validation**; workers must now exist in the
  inventory (added `iisWorkerIdent`). All IIS tests supply verified workers.

## Exact commands and results

Go: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`
(go1.27.3), `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`.

On the **live working tree**, the shared package build is currently blocked by
**another worker's in-progress edits to forbidden files** (not mine):
`sqlservice_windows.go:179/193` (`windows.ACL.AclSize` undefined) and
`sqldata_budget_test.go:14` (`maxGB` int vs float64). These errors appeared and
evolved across retries while I worked; I did not touch those files.

- `go build ./...` -> FAIL (only the two SQL-worker files above).
- `go vet ./internal/blackbox/` -> FAIL (same SQL-worker files).
- `GOOS=linux go vet ./internal/blackbox/` -> PASS (exit 0) earlier; later also
  blocked by the SQL worker's untracked `sqldata_budget_test.go`.
- `go test ./internal/blackbox/ -count=1 -timeout 300s` -> **PASS, `ok 81.593s`**,
  captured when the package was consistent (before the SQL worker's current broken
  edits). The four owned families alone: `ok 1.384s`.

To verify my work **cleanly and currently**, isolated from the concurrent breakage,
I built a scratch package = committed HEAD baseline + my nine files (no forbidden
file edited; scratch removed afterwards):

- `go build ./zz_verify/blackbox/` -> exit 0.
- `go vet ./zz_verify/blackbox/` -> exit 0.
- `GOOS=linux go vet ./zz_verify/blackbox/` -> exit 0.
- `go test ./zz_verify/blackbox/ -count=1 -timeout 300s` -> **PASS, `ok 144.136s`**
  (full package, including `TestNoTargetsCreatesNoDetailSource` and all other
  groups' tests).

gofmt: all nine files gofmt-clean (verified modulo line endings). The eight
pre-existing files keep **CRLF**; the new `proc_identity_test.go` is **LF**.

## Conflict that stopped me

I did not need to edit any forbidden file for the fix itself. Two things are
reported rather than changed:
1. The concurrent SQL worker's broken `sqlservice_windows.go` /
   `sqldata_budget_test.go` block the live-tree build/vet/test; the isolated scratch
   run above proves my change set keeps the full package green.
2. Lossless same-second disambiguation at the group join needs
   `ResolvedTarget.StartTicks` (`groups.go`), and worker-generation on the wire needs
   an `IISWorker` creation field (`groups_types.go`) — both forbidden. The fix uses
   the finest identity the view exposes (creation second) plus lossless PDH
   creation on the reading side.

## Still needs a real server

- `Process\Creation Time` semantics (assumed FILETIME, 100-ns since 1601) and its
  float64 formatting/rounding at second boundaries; English vs non-English paths.
- `WAS_W3WP\Ping Response Latency` name/units; W3SVC_W3WP instance layout.
- CLR cross-object instance identity (CLR `Process ID` vs Process `ID Process`
  agreeing on PID within one query); in-process vs out-of-process hosting; web-garden
  overlap; default service permissions.
- Footprint of the added mapping counters and the whole resolver+groups pass against
  < 0.5% of one core, < 50 MB, and the 1 s group budget at a stated host population.
- Same-second PID reuse behaviour (the residual above) on a live recycled pool.