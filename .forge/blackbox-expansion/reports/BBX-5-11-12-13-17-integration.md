# Wave 11 integration — BBX-5-11/12/13 samplers + BBX-5-17 lane label

Date: 2026-11-14. Orchestrator gate record for the four parallel packets that ran in
`.bbx-wt/{5-11,5-12,5-13,5-17}` on branches `bbx-5-11..13`, `bbx-5-17`. The agents' own
reports are unchanged beside this one; this file records what the gate accepted, corrected
and rejected.

## Taken as delivered

| packet | files | tests | self-reported mutation evidence |
|---|---|---|---|
| BBX-5-11 requests (BL-101) | `internal/bbsql/requests.go`, `requests_test.go` | 8 | 7/7 killed, byte-identical restores |
| BBX-5-12 waits (BL-102) | `internal/bbsql/waits.go`, `waits_test.go` | 13 | every guard mutated, restored |
| BBX-5-13 schedulers/semaphores/tempdb (BL-118) | `internal/bbsql/schedulers.go`, `schedulers_test.go` | 19 | 12 mutants: 11 killed, 1 survived by design (`firstCycle` flag is documentation — the unseen-id branch already covers it; M1 kills 8 tests) |
| BBX-5-17 lane label (web) | `web/src/lib/drill/dbState.ts`, `drillCopy.ts`, `web/src/lib/tracing/model.ts`, `web/src/components/CallLanes.svelte`, `SlowCallScreen.svelte`, `web/tests/drill.blackboxlane.test.cjs` | 14 new | test written first (red), then green; 3021 web tests, `svelte-check` 0 errors |

Total Go tests in `internal/bbsql` for the three samplers: 39, all passing on the integrated
tree. The fixtures are keyed by the shipped statements' output column names — verified by
reading `allowlist.go` (wait_type/tasks/max_wait_ms/blocked/head_blockers/max_block_ms;
wait_type/wait_time_ms/waiting_tasks_count; current_tasks/runnable_tasks;
resource_semaphore_id/grantee_count/waiter_count/forced_grant_count/timeout_count;
internal_pages/user_pages) — and `decodeRows` rejects any other column, so a drifted alias
fails the test rather than silently shipping.

## Gate changes (what I did not accept as delivered)

1. **`bb:waits` cadence: 15 s → 60 s.** The 5-12 agent implemented `BaseInterval` because my
   order said so; the governing documents say 60 s (`blackbox-sql-security-review.md:10`
   "Allow at 60 s, top 20", `docs/BACKLOG.md` BL-102 "every 60 s", and packet BBX-5-12).
   The document wins over the orchestrator's note. `waits.go` now returns
   `DemotedInterval` with the citation, `TestWaitsRegistered` asserts 60 s, and the C4
   per-query limit for this item becomes the 5 s figure the review gives 60 s items.
2. **`bb:tempdb` stays at 60 s.** The 5-13 agent refused my `BaseInterval` order and followed
   BL-118 ("tempdb page totals every 60 s"), and flagged the conflict instead of hiding it.
   That was the right call; the packet is accepted.
3. **The C13 budget fixture was silently wrong and is now corrected.** `budget_test.go`
   scheduled requests at 60 s, waits and tempdb at 15 s — the opposite of the real registry —
   so the measured line counts and the daily storage projection were wrong. Three entries
   fixed. New measured figures on this machine:
   - realistic: 160 lines over 40 ticks, mean line 817 B, mean tick 3 269 B, worst tick
     7 826 B → **~19 MB/day** at the real cadences.
   - at-cap: worst tick 59 421 B, max line 29 556 B; the daily projection is now computed
     **per kind at its own cadence** instead of multiplying one all-hands tick by 240:
     **199 877 760 B/day (~200 MB/day)**, so the 168 h window is ~1.4 GB, not the ~2.4 GB
     previously quoted.
   - `sqlrunner.go`, `runner.go` and the fix report carried the old figure; all corrected.
     The default 2 GB budget gives the SQL folder ~537 MB, i.e. a little over two days at the
     saturated worst case.
4. **Anti-drift gate added** (`internal/bbsql/sampler_registry_test.go`): four tests over the
   live registry — every reviewed kind registered exactly once with the reviewed cadence,
   every registered statement id allowlisted with a non-zero row cap, only `ag` carries a
     gate (and its gate is `bb:hadr`), and **the budget fixture's cadences and caps must equal
     the registry's**, in both directions. Without the last one, item 3 would drift again and
     the footprint numbers would quietly stop describing the service.
5. **`Semaphores.Forced/.Timeouts` stay `int32`.** The agent proposed widening to `int64`; it
   did not apply it, and the gate rejects it: the payload shape is what the lane reader
   parses, a >2^31 forced-grant delta in one 15 s window is not a real event, and the existing
     guard drops the row instead of wrapping into a negative (`TestSemaphoresNoWrappedDelta`,
   `TestSemaphoresCounterBeyondInt32`). Missing beats wrong.
6. **The lane label "every 15 s" became false** once the cadences were corrected, so
   `dbSrcBlackbox`, `dbMsgBlackbox` and `dbShortBlackbox` now read "every 15 to 60 s" /
   "It saves a measure every 15 seconds, and some measures only every 60…" and the four
   assertions in `drill.blackboxlane.test.cjs` (including the rendered-tooltip regex) were
   updated with it. An investigator told "one measure every 15 seconds" would mis-time a
   60 s measure.

## Still only provable on a real SQL Server (owner, before merging)

- Wait deltas over 60 s against PerfMon `Wait Statistics` (BBX-5-12 §"real server"), semaphore
  deltas against `sys.dm_exec_query_resource_semaphores` sampled by hand, and the filestats
  deltas against `sys.dm_io_virtual_file_stats`.
- `bb:tempdb` semantics: `sys.dm_db_session_space_usage` is allocation **accumulated by the
  sessions currently recorded**, not tempdb pages in use right now. The sample is honest as
  labelled; if the owner wants "tempdb in use", that is a statement change (BBX-5-05), not a
  sampler change. Flagged by BBX-5-13, carried forward.
- Whether `bb:semaphores` ever exceeds 16 rows in practice (the cap is `TOP 16` ordered by
  `resource_semaphore_id`; internal resource pools are usually 2).
- Real per-tick CPU and RSS on a loaded instance; the local numbers are a quiet machine with
  synthetic replies.
- Benign-wait filter parity with `internal/dbhealth/sql.go:101-112` against a real
  `sys.dm_os_wait_stats` (the filter is text, and text is where a review can be wrong).
- The lane UI against real NDJSON sample files, including `"rows":null` handling (L11 from the
  fix round, still open).

## Verification for this integration

```
go test ./internal/bbsql/ ./cmd/unravis-blackbox/ -count=1     -> ok 29.8s / ok 3.7s
go vet ./internal/bbsql/ ./cmd/unravis-blackbox/               -> clean
GOOS=linux go vet …                                            -> clean
go test ./... -count=1                                         -> exit 0 (whole repo)
gofmt -l on the new/edited LF files                            -> clean
cd web && npm test                                             -> 3021 tests, 3021 pass, 0 fail
npx svelte-check --output human                                -> 0 errors, 0 warnings
```
