# P-42 e2e: new scenarios for the Slow call screen

model: sonnet, effort: high. Reason: six new browser scenarios including layout measurement and history.
risk: medium. Review batch: RB-7. Wave: W6. Requirements: R-01..R-03, R-06, R-11, R-13, R-14, R-16, R-18.

## Outcome
`slow-call-screen.spec.ts` proves on the demo: open, back paths, reload, lane fit at both sizes, the window drives the lists, and every pretend state has its sentence. `COVERAGE.md` lists the new ids.

## Write scope
- `tools/e2e/specs/slow-call-screen.spec.ts` (new)
- `tools/e2e/COVERAGE.md` (new rows only)

## Must not change
- `tools/e2e/support/*`, other specs, product code. A failing scenario that shows a product bug is reported with a screenshot path, not worked around.

## Changes
Copy `openTimeline` and `clickFirstDiamond` from `timeline-drill.spec.ts` (lines 10-25) into this file. Case title `'Orders API stall, 19 October'`. Scenarios (title starts with the id):
1. `SCALL-01: a diamond opens the Slow call screen with three lanes and the call's facts`: `slow-call-screen` visible; `scall-lane-audit`, `scall-lane-iis`, `scall-lane-sql` visible; `scall-thiscall` has text; `scall-db-source` not empty; `scall-map` or `scall-map-hidden` present; no element with `role=dialog`.
2. `SCALL-02: Back button, Esc and the browser Back all return to the timeline with the same window`: read the timeline's window text before opening; for each of the three ways: open, leave, expect `.tracing-paper` visible, window text unchanged, no new `**/tracing` request for the timeline (count requests whose body `requestSeq` ... simpler: count all tracing requests made in the 500 ms after leaving = 0). Then browser Forward shows the screen again.
3. `SCALL-03: a reload on the screen comes back to the same call`: note the text of `scall-thiscall`; `page.reload()`; `slow-call-screen` visible and the same text; then Back shows the timeline.
4. `SCALL-04: the three lanes fit without an inner scroll at 1440x800 and 1100x760`: for each size `page.setViewportSize`, open the screen, then in `page.evaluate`: each `[data-testid^="scall-lane-"]` has `getBoundingClientRect().top >= 0` and `.bottom <= window.innerHeight`; `scall-pin` and every ancestor up to `slow-call-screen` has `scrollHeight <= clientHeight + 1` OR computed `overflow-y` of `visible`; after `window.scrollTo(0, 400)` the `scall-pin` top is still within the viewport (pinned). Attach a screenshot per size (`testInfo.attach`).
5. `SCALL-05: the window drives the lanes and both lists`: note `scall-nearby-hint` text; click `scall-preset-15`; the hint text and `scall-window-line` change; click `scall-later`; `scall-window-line` shows the moved wording and `scall-tocall` is enabled; click it → back. Switch to the All sources tab: `scall-all-heading` visible. Drag `scall-map-box` 60 px right with the mouse: during the drag no `**/tracing` request starts; after mouse up exactly one visible-window read happens (allow 1-2: count ≤ 2).
6. `SCALL-06: each pretend state shows its own sentence and never the words Query Store is on when it is not`: for each key in `qs, qsfile, api, off, ro, late, denied, events, notread`: click `scall-pretend-{key}`; `scall-db-state` has `data-state={key}` and non-empty text different from the previous key's text; for `off|denied|events|notread` the database lane (`scall-lane-sql`) shows no cell (`.cell` count 0, use the class P-27 gave lane cells; read the component). `scall-pretend-real` restores the first sentence.
7. Add six rows to `tools/e2e/COVERAGE.md` in the file's existing table format.

## Proof
- [ ] `cd tools/e2e && npm run e2e -- -Only SCALL` → 6 passed (if `-Only` needs full ids: `SCALL-01,SCALL-02,SCALL-03,SCALL-04,SCALL-05,SCALL-06`).
- [ ] Run it a second time → 6 passed (no flake). A flaky scenario is reported, not retried into green.
- [ ] Screenshots for both sizes are attached; report their paths.
- [ ] Demo only by nature; the same checks on live data are P-46.

## Depends on
P-41 (same demo exe; P-41 proves the build path), P-33.
