# Round 2 review: phases 04-tracing-ui, 05-guided-unlisting, 06-mvp1-gate

Read-only. No plan or source file edited, no build run. Owner decisions R6-02 and R6-03 and plan.md section 10 items are not re-raised. Open owner decision (guided entry point after WP-64, plan.md 10.4) is not a finding.

## New findings

[BLOCKER] WP-17a:30 — the audit lane never appears in the default UI call. Step 30 tells the implementer to use "the audit ids from the request (all audit sources of the case when sourceIds was omitted ... the ids are the ones WP-14 kept in auditIDs)" and also "When auditIDs is empty ... do not add an audit lane". WP-14:32 says `AuditIDs` is empty when `sourceIds` was omitted and WP-14:36 says no audit id is added for the default call. WP-16a/WP-16b never send `sourceIds`. Result: the default call returns no audit lane, which breaks the MVP-1 gate (plan.md:179), WP-25's "phase 04 adds the third lane" claim and WP-61's audit data. — In WP-17a (or WP-14 step for `AuditIDs`) state that an omitted `sourceIds` makes the handler call `h.repo.Sources(ctx)` (`internal/audit/models.go:471`, `Repository` interface) and use the case's audit sources, name the selection rule, and add a test "default call returns an audit lane".

[MAJOR] WP-17a:30 — says to insert the audit lane at index 0 of the `lanes` slice and call `finishTracingMeta` over the whole slice. WP-18b:21-22 defines the seam as `appendLane(resp *tracingResponse, lane tracingLane)` and `finishTracingMeta(resp *tracingResponse)`. As written it does not compile against the seam. — Say "build the lane, `appendLane(&resp, lane)`, then the single `finishTracingMeta(&resp)`"; if audit must be first, say so and have WP-18b's appendLane keep lane order by a fixed rank.

[MAJOR] WP-64 step 2 (:73) with WP-16b step 2 — replacing `'home-guided'` with `'home-tracing'` in `web/tests/p3.fix.test.cjs:100` (`for (const id of ['home-analysis','home-guided']) assert.ok(at(id) > open1 && at(id) < story, id)`, `at` = `indexOf`, `open1 = at('{#if offersWork}')`) makes `at('home-tracing')` hit the first occurrence of the literal. WP-16b puts `home-tracing-open` in the script (closeTracing focus, script ends at `CaseHome.svelte:354`), which is before `{#if offersWork}` (:464). The assertion fails and the WP-64 Proof breaks. — Assert on `at('aria-labelledby="' + id + '"')` (or avoid the literal id in the script by using `bind:this`), and say so in WP-64 step 2.

[MAJOR] WP-23 step 2 with WP-61 — click, arrow and `onSelect` indices under zoom are unspecified; only the tooltip slot is given as `range.from + slotAtFraction(..., range.to-range.from)`. A zoomed click can hand WP-61 a visible-relative index that WP-61 treats as absolute, opening the analysis on the wrong hour. — Specify: click slot = `range.from + slotAtFraction(fraction, range.to - range.from)`; arrows clamp to `[range.from, range.to-1]`; `onSelect` always receives an absolute slot index; add a `.cjs` source-text assertion and a WP-61 note.

[MINOR] WP-17a:30 — the audit lane mapping omits wire fields from WP-14 step 7: `label`, `sources`, `events: []`, `hidden`, and bucket `end`, `clipped`, `complete`; arrays must be non-null. — list them in the step, with a test that JSON has `[]` not `null`.

[MINOR] Invented edges — WP-22 depends on WP-17c (:106) and WP-23 on WP-17a/WP-18b with no symbol consumed. — drop the edges in the packet and plan.md section 3, or name the consumed symbol.

[MINOR] WP-16a:115 — `TRACING_TEXT.emptyNoSources` is never rendered, though phase 04 phase.md:49 and phase 05 phase.md:39 assume an empty-case sentence. — render it in `TracingPaper.svelte` when `sources` is empty, with a test string.

[MINOR] phase 05 phase.md:36 and the `startTracing` identifiers — still say "Start tracing" while the label is "Open the Investigation Timeline". — reword the sentence; identifier rename optional.

[MINOR] WP-23 header says "six tests" but lists seven. — fix the count.

[MINOR] WP-14 shell returns `bucketSeconds=0` before WP-15 while the WP-16a parser throws on a non-positive value. Fine once WP-15 merges; WP-16a depends on WP-15, so only a note is needed. — none required beyond a one-line note.

## Round-1 verification (fixed means verified in the packet files and against the repo)

| Id | State | Evidence |
|---|---|---|
| R1-1 WP-16b primary-button | FIXED | WP-16b:147, :164, :168, :176; `web/tests/defects1.test.cjs:68` counts exactly one `class="primary-button"` between `home-analysis` and `home-erase`; `npm test` in Proof |
| R1-2 WP-64 vs WP-16b duplicate block | FIXED | WP-16b:137/:164 own `home-tracing`; WP-64:54/:63 add no label |
| R1-3 WP-61 vs chart | FIXED | WP-61:20/:25 use `slotIndex` per WP-17b:76-78; `appState.svelte.ts:782` `openAnalysis`, `:1463` `setWindowFromHour(window: RollupWindow)`, `rollup.ts:27/:216/:227` exist |
| R1-4 tooltips | FIXED | WP-17c; dictionary ids exist (WP-08:20/:25/:33); WP-09 and 17b edges in plan section 3 |
| R1-5 WP-11 deps | FIXED | WP-11:141 equals plan table |
| R1-6 WP-58 ids | FIXED | WP-58:15 |
| R1-7 stale phase tables | FIXED | phase 04 :10, phase 05 :21, phase 06 :56 |
| R1-8 WP-10 swatches | FIXED | WP-10:72, :80, :87 |
| R1-9 Back wording | FIXED | phase 05 phase.md:35 |
| R1-10 WP-58 git status | FIXED | WP-58:37 |
| R1-11 size | OPEN (MINOR) | WP-17b split into 17b/17c but still 4 files; WP-23 unsplit with an overflow instruction (WP-23:20). Accept with note |

## Checked and fine
- Client matches the WP-14 wire contract: WP-16a:102-112 parser (`window.axisStart/axisEnd/bucketSeconds`, bucket `{start,count,errors?}`, `sources[{sourceId,label}]`, `requestSeq`, `mixedConsistency`); WP-17b:76-78 uses `floor` for clipped bucket starts, matching the WP-15 axis.
- Lane states match `internal/calltrace/types.go:21-27`.
- Web chunk cycles: `TracingPaper` is lazy-only (WP-16b:149/:172); the chart has no echarts/charts.ts import; `lazyComponent` at `web/src/lib/lazy.ts:9` is used as designed; `distChunks.test.cjs` cycle matcher will see no new cycle.
- Same-wave write scopes disjoint (WP-22/WP-23 in wave 6).
- Proof commands runnable and test names match; WP-61 test count of 10 correct.
- Phase 04 web files showed no pre-existing modifications in git status.
- Single `home-tracing` label owner is WP-16b; WP-64 adds none.

## Not verified
- WP-62a/b/c and WP-63 not re-read in this round (no changes expected from round 1).
- Phase 03 handler code does not exist yet; WP-14/WP-18b read as specs only.
