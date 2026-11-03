# Round 1 review: phases 04-tracing-ui, 05-guided-unlisting, 06-mvp1-gate

Criteria 1, 2, 3, 4, 5, 7 plus wireframe comparison. Read-only; no plan or source files edited.

## Findings

[MAJOR] WP-16b — the "Open the Investigation Timeline" entry uses `primary-button`; `web/tests/defects1.test.cjs:67` counts `class="primary-button"` between `home-analysis` and `home-erase` and expects exactly 1, so `npm test` breaks. The Proof list also omits `npm test`, so the break goes unseen — use `secondary-button` for the tracing entry and add `npm test` to Proof (and to the wireframe-check wording, which currently says "primary button").

[MAJOR] WP-64 vs WP-16b — both create the case-home `home-tracing` block. WP-16b makes heading "Investigation Timeline" / button "Open the Investigation Timeline" (TRACING_TEXT). WP-64 creates a second `home-tracing` with `CASE_HOME_TEXT.tracingHeading 'Tracing'` / `tracingStart 'Start tracing'` and its tests assert those strings. Plan §1 and phase 04 acceptance use a third wording. Duplicate ids and contradictory labels — rewrite WP-64 to only delete the Guided section, the guided copy and the `p3.fix` assertion, keep WP-16b's block, and have the owner pick one label in plan.md §1 (all packets and tests then use it).

[MAJOR] WP-61 — written against a chart that does not exist: assumes an ECharts click and `web/src/lib/tracing/types.ts` with bucket start/end epoch fields. Phase 04 builds a plain HTML/CSS chart exposing `onSelect(slotIndex)`, types live in `model.ts`, and `TracingBucket` has only `start`, `count`, `errors`. Step 2 cannot be implemented as written — rewrite step 2 to take `slotIndex` from `onSelect`, compute ISO start/end from `slotGrid`/`slotStartMs` + the slot width, state the empty-bucket rule (no hand-off, or hand-off allowed), and fix the test regexes to match.

[MAJOR] Phase 04 (no packet) — plan §4 says the TR-22 `explain()` dictionary supplies tooltips to the tracing chart, plan.md:9 and the MVP-1 gate (plan.md:174) require "tooltips from a plain-word dictionary", but grep of phase 04 finds no tooltip/`explain(` wiring in any packet (WP-17b, WP-23 and WP-10 do not mention it). Nothing assigns it; WP-58 gate cannot pass — add a tooltip step to WP-17b (bar hover/focus text from `explain(laneId, kind)`, imported from the WP-08/09 module, with a `.cjs` test asserting the `title`/`aria-label` source), or a new small packet after WP-23; add WP-08/09 to Depends on.

[MAJOR] WP-11 — edits `TracingPaper.svelte` and relies on WP-23's `visible` prop on TracingChart but `Depends on` lacks WP-16b and WP-23 — add both edges in the packet, plan.md §3 and phase.md (check-plan.ps1 will then enforce wave order; WP-11 must move after WP-23).

[MINOR] WP-58 — `Depends on` omits WP-45 and WP-59 and mixes split and unsplit ids — list the real final packets of phases 03 and 05.

[MINOR] phase.md tables (04, 05, 06) — wave numbers and some deps are stale vs packets/plan §3 (phase 04 shows WP-17a dep as WP-15 only; WP-16b body says "inside wave 2" but header says wave 3). Phase 05 still tells the caller to update plan rows that are already updated — regenerate the tables from plan §3.

[MINOR] WP-10 — `--lane-*` swatch variables are defined on the TracingChart root, so they are undefined in the sibling TraceToolbar — define them on the TracingPaper root, or use `var(--lane-audit, #xxxxxx)` fallbacks.

[MINOR] Phase 05 browser acceptance — "Back returns to the tracing screen" is unsupported: `tracingFor` is CaseHome-local state and analysis is a different screen — reword to "Back returns to the case home" or add state restore to WP-61.

[MINOR] WP-58 Proof — `git status --short` expected clean is unrealistic (the working tree has ~269 modified files) — scope it: `git status --short -- .forge/investigation-timeline/progress.md`.

[MINOR] Size — WP-17b (4 files) and WP-23 (drag brush, zoom, polyline, keyboard) exceed M — split WP-23 into brush/zoom and keyboard/polyline; WP-17b into chart and panels.

## Wireframe comparison (prototype/tracing-paper-PROTOTYPE.html)
- Phase 04 acceptance does compare against the prototype (phase.md:40: 1280/375 px, light/dark, row order, lane colours, width caption, mixed-time note, wrapping toolbar). Good.
- Prototype elements deliberately excluded by WP-17b (comparison-period rows, 4xx/5xx splits, HTTP.sys and blocking panels): the exclusion is in the packet but phase acceptance does not list them as accepted deviations — add one line to phase.md acceptance so the visual check does not fail on them.
- Tooltips: see the MAJOR above.

## Areas with no problems
- Test style fits the repo: node:test `.cjs` in `web/tests`, `.svelte` read as text, Playwright only under `tools/e2e`; no Vitest.
- No packet tells the implementer to read a whole spec or large file.
- WP-60 (28 files), WP-62a/b/c line references, e2e spec counts (25/10/6/13/24 = 78, +2 = 80), and slot-grid test numbers (120, 91) are accurate.
- Cited symbols verified present: `cases.ts` (send, casePath, CaseApiError, isCaseLocked), `lazy.ts`, `time.ts` formatTime, `format.ts` formatDuration, `timeZone.svelte.ts`, `copylint.ts` lintScreenStrings, `appState.svelte.ts` (openAnalysis, setWindowFromHour, go), `rollup.ts` RollupWindow, `screenRoute.ts`, `distChunks.test.cjs`.

## Not verified
- Phase 03 `tracing.go` audit-lane hook that WP-17a depends on (WP-14/15/18b).
- WP-58 environment claims (`.dev\toolchains\go\bin\go.exe`, `./cmd/audit-explorer`, `run-e2e.ps1 -Exe`).
