# Backend large-window fixes: remote review round 1

Lane droid-az-fw-gpt-6.1-sol-2 (remote hub), job 37089, status done, exit 0, about 7.5 min. Bundle: slim single-commit (327 KB, 105 files; REVIEW_DIFF.patch of 16 changed + 4 new files; no whole-file churn after --ignore-cr-at-eol). Reviewer verdict: FIX-FIRST (A1, B1, C1, C2, C4, C5, C6, E2). After triage no blocker holds.

The bundle held only non-test sources plus the 4 new tests, so the reviewer could not see buckets_test.go and rollup_test.go (G9, G10 are wrong for that reason).

Reviewer counts: 35 findings, 0 blocker, 8 major, 21 minor, 6 nit.
Triage counts: CONFIRMED 12, UNSURE 5, REJECTED 18.

## Triage (against the working tree)

| id | verdict | why |
|---|---|---|
| A1 chunks use separate snapshots | REJECTED | A cell lies wholly inside one chunk, so each cell count is self-consistent; only cross-cell totals could mix commit points while an import commits. Low impact. |
| A2 WidthMs near MaxInt64 overflow | REJECTED | Unreachable: widths come from the axis ladder (max 48 h); no external caller sets WidthMs. Optional hardening. |
| A3 scan-fallback partial leaves buckets Complete | REJECTED | Pre-existing behaviour of the scan-budget path, not changed by this patch. |
| B1 cancel between check and soft stop becomes partial | REJECTED | The finished days are valid and the request is gone anyway; a cancelled ctx at loop top or inside Analyze is still an error. |
| B2 expired timer converts any concurrent error | REJECTED | Needs a real error in the same instant as the timer; the result is a valid partial of finished chunks. |
| C1 importRun published after importMu acquired | CONFIRMED (minor) | case.go lockImport: a second Import that fails TryLock in the gap before importRun.Store(true) falls to unconditional importMu.Lock() (old behaviour: no 1 s bound, not ctx-cancellable). Narrow window, degrades to previous behaviour. Fix: publish a pending flag before TryLock, or poll in all cases. |
| C2 writer passes check, import wins folder lock | REJECTED | Documented limit in fix-backend.md (waits for the folder lock up to 5 s as before). |
| C3 gates call handle() before busy check | REJECTED | c.mu is held only briefly; at worst a few ms. Moving the importRun check above handle() is a free micro-improvement. |
| C4 CloseCase TryLock fail skips busy check | REJECTED | By design (best effort, falls back to old wait). |
| C5 separate Stores / processes not detected | REJECTED | Documented limit (flag is per Store). |
| C6 HTTP gates queue before store gates | UNSURE (design) | closeCase (cases.go:896) and a second upload (cases.go:1443) wait in h.gates.acquire until the import ends; that matches the existing comment ("an import in flight finishes first"), so over HTTP they get no 409. The 409 reaches workspace/tag/plan/retry writers and non-gate callers (archive_restore.go, case_file.go). Owner call whether close over HTTP should 409 too (needs a try-acquire). |
| D1 cache drops a larger newer entry, keeps a smaller older one | CONFIRMED (minor) | rollup.go put: the loop skips an entry that does not fit but continues to older ones, so it packs instead of evicting oldest first. Fix: break at the first entry that does not fit. |
| D2 zero-length fresh retains old array | REJECTED | fresh := c.entries[:0:0] has capacity 0; append allocates anew, nothing old is retained. |
| D3 Groups() ignores AddRow/AddBin | CONFIRMED (minor) | rollup.go:212,262: only AddCounts increments groups; the Go-scan path (internal/store/rollup.go:280 via AddRow) reports 0 so such reads escape the 1.2M cap. Bounded by the scan row cap, low risk. Fix: count slots+bins held. |
| E1 non-deadline error at the deadline becomes partial | REJECTED | Needs ctx.Err()==DeadlineExceeded at that instant; the finished days are valid. |
| E2 step partial then audit series fails on expired ctx | REJECTED (pre-existing) | Before the patch the same deadline already failed step change and the whole report; all opt-in builders share ctx. The 80 percent soft stop leaves later builders time. |
| E3 partial=true with daysLeftOut 0 | UNSURE | Not verified in full; cosmetic wire meaning. |
| E4 121-122 day windows, localDays keeps newest 120 | REJECTED | Pre-existing validation; warning wording still true. |
| F1 any SQLite FULL becomes case-cap | CONFIRMED (minor) | case.go:944 mapDBErr: disk-full also surfaces as FULL and now shows "case is over its size cap". Fix: keep generic cap-exceeded unless the page cap is the cause, or neutral wording. |
| F2 memory fake not updated | CONFIRMED (nit) | memory.go still returns generic cap errors and waits unbounded for a second import. |
| G1 fail-fast tests run synchronously | CONFIRMED (minor) | A blocking regression hangs the test instead of failing; wrap in goroutine + select with timeout. |
| G2 held import bypasses h.gates in server test | CONFIRMED (minor) | Test calls Case.Import directly, so the HTTP gate path (see C6) is not covered. |
| G3 held-import completion error only logged | UNSURE | Not re-read in detail; cheap to assert. |
| G4 discovery mapper not tested | CONFIRMED (minor) | grep: no failErr/discoverPull in import_running_test.go; the three new cases in discover_import.go are untested. |
| G5 real-time budgets in dailyseries_budget_test | CONFIRMED (minor) | StopsAtSoftDeadline asserts took<=1 s and n>=5 with 20 ms days: flaky on a loaded runner. Use the seriesNow stepping clock. |
| G6 80 percent threshold not pinned | CONFIRMED (minor) | Test only checks finish before 100 percent; a 99 percent threshold would pass. |
| G7 mid-day deadline path not forced | CONFIRMED (minor) | The test comment itself says either path may end the read. Force one day to fail on the deadline and assert the exact prefix. |
| G8 provenance / JSON assertions vacuous | UNSURE | Not re-read in detail; the "JSON flag left out" test does not marshal. |
| G9 no storage-level budget tests | REJECTED | They exist in buckets_test.go (TestBucketCountsTimeoutIsPartial, TestBucketCountsChunksMatchWhole, TestBucketCountsStopsWhenRequestEnds, TestEventsTimeBudgetAndCancel); not in the bundle. |
| G10 no cache tests | REJECTED | They exist in rollup_test.go (TestRollupCachePutPrunesExpiredEntries, TestRollupCacheCapsTotalGroups). D1 and D3 are real but untested. |
| G11 50 ms sleep in short-import test | UNSURE | Real sleep against a 1 s wait; low flake risk. |
| G12 missing contention/cleanup tests | REJECTED | A wish list, not a defect. |
| G13 no 186/187-day axis edge tests | REJECTED | tracing_axis_test.go covers 30d/31d/93d/186d per fix-backend.md. |
| O1 stale comments | CONFIRMED (nit) | case.go:1190 still says "one GROUP BY"; tracing_audit.go:49 lists ReasonCode values without broad-scan. |
| O2 test hooks unused | REJECTED | bucketChunkHook is used in buckets_test.go; eventRowHook in TestEventsTimeBudgetAndCancel. |

## Confirmed list (suggested fixes)
1. C1 case.go lockImport: publish a pending-import flag before TryLock (or always poll) so the 1 s bound always applies.
2. D1 rollup.go put: stop at the first entry that does not fit.
3. D3 rollup.go: count slots and bins held, so AddRow scans are bounded by the group cap.
4. F1 case.go:944: do not call every SQLITE_FULL a case cap.
5. F2 memory.go: align the fake or document it.
6. Test hardening G1, G2, G4, G5, G6, G7.
7. O1 stale comments.
Open design question: C6.

---

# Raw review (droid-az-fw-gpt-6.1-sol-2, job 37089)

## (a) Time budgets and partial results

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| A1 | major | high | internal/casestore/sqlite/case.go:1299 | Each chunk gets a separate database snapshot. An import committing between chunks can contribute newer cells while its older cells remain missing, yet the result reports complete; timeout `DoneTo` guarantees can also become false. | Read every chunk within one read transaction. |
| A2 | minor | high | internal/casestore/sqlite/case.go:1288 | A positive `WidthMs` near `MaxInt64` passes validation, but adding it to a modern axis timestamp overflows. The resulting reversed query bounds silently return zero counts as complete. | Clamp chunk bounds before overflow-prone multiplication/addition. |
| A3 | minor | high | internal/server/tracing.go:788–802 | Existing scan-fallback partial results still leave every bucket `Complete=true`. With an unknown completion boundary, those counts can be incomplete anywhere. | Mark all buckets incomplete when a partial result has no trustworthy boundary. |

## (b) Cancellation and deadline attribution

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| B1 | major | med | internal/audit/dailyseries.go:257–297 | Cancellation between the context check and soft-stop decision can escape as a partial success. Cancellation during final-day post-processing also receives no final check. | Recheck cancellation after each read and before returning success or partial results. |
| B2 | minor | med | internal/casestore/sqlite/case.go:1156–1158,1304 | An expired timer converts any concurrent error into a budget result, including damaged data or shutdown failures. The flag proves expiration, not that expiration caused the failure. | Convert only errors attributable to the internal budget cancellation. |

## (c) Import gates and locking

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| C1 | major | high | internal/casestore/sqlite/case.go:414–415 | Import A acquires `importMu` before publishing `importRun=true`. Import B can observe false and enter unconditional `Lock()`, bypassing both cancellation and the one-second limit. | Synchronize owner publication with acquisition and remove the unbounded fallback. |
| C2 | major | high | internal/casestore/sqlite/tags.go:47–50; internal/casestore/sqlite/retries.go:36–39 | A writer checks false, then an import wins the folder lock. The writer subsequently blocks rather than returning `ErrImportRunning`, without a cancellable acquisition. | Make import admission and writer lock acquisition one coordinated operation. |
| C3 | minor | high | internal/casestore/sqlite/tags.go:43–47; internal/casestore/sqlite/retries.go:32–36 | Both gates call `handle()` before checking import state. Import manifest synchronization can hold `c.mu`, delaying an otherwise fail-fast refusal. | Check busy state before blocking on `c.mu`, alongside synchronized admission. |
| C4 | major | high | internal/casestore/sqlite/sqlite.go:853–860 | Failed `s.mu.TryLock()` skips busy detection. A last-handle close holding `s.mu` while shutdown waits for an import makes `CloseCase()` wait on that import too. | Keep import-state lookup independent of long-held store operations. |
| C5 | major | med | internal/casestore/sqlite/case.go:633–642 | Separate Stores have separate flags and mutexes. A second Store importing the same case bypasses the one-second wait and blocks on the shared folder lock; its other writers similarly miss busy detection. | Coordinate ownership across Stores/processes or explicitly restrict the contract. |
| C6 | major | high | internal/server/cases.go:896,1443; internal/server/discover_import.go:199 | Normal HTTP imports hold `h.gates`; close requests, second uploads, and discovery requests queue there before reaching the new store gates. Ordinary requests therefore cannot obtain the promised timely 409. | Add import-aware fail-fast/bounded admission to the HTTP gate. |

## (d) Rollup cache

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| D1 | minor | high | internal/server/rollup.go:113–117 | With newest-to-oldest groups `[500k,800k,300k]`, insertion keeps 500k and 300k but drops the newer 800k entry. This is capacity packing, not oldest-first eviction. | Retain the newest prefix and evict from its oldest end. |
| D2 | minor | high | internal/server/rollup.go:100–108 | If all entries expire and the incoming read exceeds the cap, `fresh` remains a zero-length slice pointing at the old backing array. Its expired builders remain reachable despite an apparently empty cache. | Use nil for an empty retained set and clear discarded backing-array references. |
| D3 | minor | med | internal/server/rollup.go:46 | `Groups()` counts aggregation input rows, not retained slot/bin groups; `AddRow()` increments it zero times and `AddBin()` does not increment it. Readers using those paths can under-account cached memory. | Count retained slot/bin allocations or enforce a consistent builder accounting contract. |

## (e) Daily-series deadline behavior

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| E1 | minor | med | internal/audit/dailyseries.go:263–265 | A non-deadline repository failure arriving as the context deadline expires is silently converted to partial success. This contradicts the rule that other read errors stop the series. | Require the returned error to be deadline-related before preserving partial results. |
| E2 | major | high | internal/audit/stepchange.go:260; internal/audit/patterns_run.go:302–305 | With both step change and audit series enabled, a mid-day deadline can produce a valid partial step result. The following audit-series read uses the expired context and discards the entire report with an error. | Preserve completed results and skip later optional work after deadline exhaustion. |
| E3 | minor | med | internal/audit/stepchange.go:273–277 | A shortened fit can report `partial=true` and `daysLeftOut=0`, although many planned days were excluded. Only partially read days contribute to the existing counter. | Include unfinished planned days in the exclusion count, or clarify its wire meaning. |
| E4 | minor | med | internal/audit/endpoint_daily.go:96–104 | Existing validation accepts 121–122 whole days, then `localDays` retains only the newest 120. The new warning calls returned days the oldest, and `daysPlanned` understates the whole requested window. | Validate actual whole-day count before truncation or expose the selected-window distinction. |

## (f) JSON and error compatibility

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| F1 | minor | med | internal/casestore/sqlite/case.go:397–398,944 | Every SQLite `FULL` error becomes `case-cap`. Filesystem exhaustion can also produce `FULL`, so an unknown disk failure receives a specific configured-case-cap diagnosis. | Use a generic capacity error unless configured page-cap exhaustion is established. |
| F2 | nit | high | internal/casestore/store.go:409–418; internal/casestore/memory.go:490,554,579 | The memory fake still returns generic cap errors and waits indefinitely for a second import. Its documented SQLite-equivalent semantics now diverge from the new public errors. | Update fake behavior or document these guarantees as SQLite-only. |

Specific cap errors correctly unwrap to `ErrCapExceeded`; no relevant `==` consumer was found. Available mappings use the requested reason values. Client-side switches are absent from this bundle, so consumer compatibility remains unverified.

## (g) Test quality

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| G1 | minor | high | internal/casestore/sqlite/import_running_test.go:104; internal/server/import_running_test.go:123–130 | Fail-fast calls run synchronously, while import release occurs afterward. A blocking regression deadlocks the test before elapsed-time assertions or cleanup can run. | Run calls asynchronously with bounded selects, unconditional release, and goroutine joins. |
| G2 | minor | high | internal/server/import_running_test.go:103–110 | The held import starts directly through `Case.Import`, bypassing `h.gates`. The HTTP test can pass while normal uploads still block close requests. | Start the held import through the actual upload endpoint. |
| G3 | minor | high | internal/server/import_running_test.go:131–132 | Unexpected held-import completion errors only produce a log message. A broken import path can therefore pass the test. | Assert the expected completion error or success explicitly. |
| G4 | minor | high | internal/server/import_running_test.go:139–158 | The test claims discovery mapping coverage but never calls `discoverPull.failErr`. Discovery-specific mapping regressions pass unnoticed. | Table-test the discovery mapper directly. |
| G5 | minor | med | internal/audit/dailyseries_budget_test.go:44–59,111–119 | Tight real-time deadlines and elapsed-time assertions depend on scheduler load. Correct code can exceed these bounds or finish too few days on a busy runner. | Use controlled clocks and synchronization for budget decisions. |
| G6 | minor | high | internal/audit/dailyseries_budget_test.go:58–59 | The soft-stop test checks only completion before 100% of the budget. Changing the intended 80% threshold to 99% can still pass. | Assert behavior immediately before and at the 80% boundary using `seriesNow`. |
| G7 | minor | high | internal/audit/dailyseries_budget_test.go:80–87 | Normally four 60-ms days finish around 240 ms, then the 200-ms soft stop fires before the 250-ms deadline. Removing mid-day deadline handling can still pass this purported deadline-path test. | Force a particular day to fail on the deadline and assert the exact finished prefix. |
| G8 | nit | high | internal/audit/dailyseries_budget_test.go:71–75,92–103 | Provenance assertions are vacuous when provenance is empty; the “JSON flag is left out” test never marshals JSON. Those regressions can pass. | Assert nonempty expected provenance and inspect marshalled keys. |
| G9 | minor | high | internal/server/tracing_timeout_test.go:20–36,103 | Timeouts are injected after a complete count or returned by a fake. No supplied test exercises SQLite chunk boundaries, exact complete sums, first-chunk timeout, actual event-budget cancellation, or caller deadlines. | Add deterministic storage-level budget and boundary tests. |
| G10 | minor | high | internal/server/rollup.go:95–119 | No new cache tests cover fake-clock expiration, replacement, mixed-size oldest-first eviction, the 1.2M cap, or concurrent access. D1 and D2 remain undetected. | Add focused cache accounting and eviction tests. |
| G11 | nit | med | internal/casestore/sqlite/import_running_test.go:146–150 | The short-first-import test relies on a real 50-ms sleep and completion within the production one-second wait. Scheduling/database delays can fail correct behavior. | Control release and wait expiration independently. |
| G12 | minor | high | internal/casestore/sqlite/import_running_test.go:76–137 | Supplied tests omit contested flag publication, failed store `TryLock`, retry writes, cancellation, panic cleanup, and cross-Store contention. They exercise only already-published busy state. | Add synchronized contention and cleanup-path tests. |
| G13 | nit | high | internal/server/tracing_timeout_test.go:99–101 | New tracing tests use an hour-long axis only. Empty/one-day windows, exact 186/187-day limits, and 48-hour rung alignment have no new coverage. | Add table-driven window-cap and axis-boundary tests. |

## Other

| id | severity | confidence | file:line | evidence | one-line fix |
|---|---|---|---|---|---|
| O1 | nit | high | internal/casestore/sqlite/case.go:1190; internal/server/tracing_audit.go:49–50 | Comments still describe one GROUP BY and an enum excluding `broad-scan`, despite the new behavior. | Update the affected contract comments. |
| O2 | nit | high | internal/casestore/sqlite/case.go:1329–1333 | Neither new test hook is assigned anywhere in the supplied tests. Production carries mutable per-row/per-chunk seams without exercising their intended purpose. | Use them in deterministic tests or remove them. |

No outside-scope patch files or new secret logging found. Builds/tests were not run because the bundle lacks required imports and existing test helpers; no files were changed.

**Verdict: FIX-FIRST. Blocker list: A1, B1, C1, C2, C4, C5, C6, E2.**
Examined: full patch, all 20 scoped files, and relevant callers, locking, builders, provenance, validation, and memory-fake sources; nothing under `sql/`.
