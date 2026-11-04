# Frontend large-window sweep (read-only)

Scope: `web/src` (Svelte 5). Nothing edited, `sql/` not read, app not run. Findings come from reading the code only,
so every cost below is an estimate from counts, not a measurement. Dataset sizes in "Test" are what the tester should build.

Headline: the render side is safe at large windows. The server caps every timeline lane at 120 buckets and the
analysis charts at 120 slots, so the DOM does not grow with the window. A 3-month or 12-month window never reaches
the timeline server: the client refuses it. The real gaps are feedback gaps (no timeout, Rollup has no Cancel/Retry)
and server wait time.

## Findings

### PF-F-01  Medium  No request timeout anywhere in the fetch layer
- Where: `web/src/lib/api.ts:101-106` (`request`), `web/src/lib/api.ts:220` (`export`), `web/src/lib/cases.ts:184-193` (`send`, used by tracing, case, archive, import calls).
- 3-month case: a daily/hour-grid read (up to 120 days) or an analysis that the server scans slowly waits forever if the server stalls or the socket half-opens. Screens stay on "Reading the window..." / "Reading the sources..." until the user cancels, and Rollup and Pulse have no Cancel (see PF-F-02). `send` maps only a hard network failure to "The server could not be reached."; a hang never reaches that branch.
- No automatic retry either (fine; a blind retry would double a heavy scan). Manual "Try again" exists on the timeline, case home, resource panel and call trace.
- Fix: in `request`/`send`, combine the caller signal with `AbortSignal.timeout(ms)` (via `AbortSignal.any`), e.g. 120 s for reads, none for job polls and downloads. Map a timeout abort (not a user abort) to a new sentence in `web/src/lib/copy/*` ("This is taking longer than expected. Try a shorter window.") and let each screen show it in its existing error state.
- Test: node test in `web/tests` (new `fetchTimeout.test.cjs`) with a fake `fetch` that never resolves and fake timers; assert the error type and that a user abort still reads as cancelled. No dataset needed.

### PF-F-02  Medium  Rollup (daily table / hour grid, up to 120 days) has no Cancel, no Try again, no disabled Span
- Where: `web/src/components/RollupView.svelte:27-45` (`load`), `:71` (Span select), `:79` (status line), `:80` (error).
- 3-month case: choosing "Last 90 days" or "Last 120 days" runs `api.daily` and then `api.hourGrid` one after the other (lines 37 and 39), so wall time is the sum of two scans. The only feedback is a small aria-live line. The user cannot stop the read or retry a failure except by changing the Span select. Changing the select does abort the old read correctly (sequence guard plus `AbortController`), so there is no stale-data bug.
- Fix: (a) run both reads with `Promise.all` (same params, independent); (b) add a Cancel button while `loading` (calls the existing abort) and a "Try again" button inside the error alert (calls `load()`); (c) move the hard-coded "Reading the sources..." to `rollupCopy` and add `cancel` / `retry` entries there.
- Test: Playwright spec in `tools/e2e/specs` (new `rollup-slow.spec.ts`): route `/api/.../daily` with a 5 s delay, assert the Cancel button appears and the status clears on click; then a 500 response and assert Try again repeats the request. Dataset: 120 days x 24 hours of hour-grid cells (2880), a few thousand requests per day.

### PF-F-03  Low  Timeline refusal for 3 or 12 months gives no way forward
- Where: `web/src/lib/tracing/windows.ts:10,93-94` (`MAX_TRACE_SPAN_MS`, "The longest window is 30 days. Shorten the ...").
- 3-month case: correct and instant (no request, no hang), but it is a dead end: no sentence says that the Analysis daily/hour view reaches 120 days. 12 months is not offered by any screen (Rollup tops out at `MAX_ROLLUP_DAYS = 120`, `web/src/lib/rollup.ts:30`; Analysis windows are 7 days, `web/src/lib/time.ts`, `autorun.ts:28`).
- Fix: append one short sentence to the 30-day message pointing to the daily view (copy dictionary, kept in `WINDOW_TEXT`, which `windowCopyEntries()` already lints). Owner decision needed if 12 months must ever be possible.
- Test: extend the windows test (node, `web/tests`, existing window tests) to assert the new text. No dataset.

### PF-F-04  Low  Export preview Cancel only hides the answer
- Where: `web/src/components/ExportPanel.svelte:45-52`, `web/src/lib/api.ts` (`exportPreview`, `exportConfirm`, ledger, sources and keeper calls take no `signal`).
- 3-month case: a preview over a large window keeps running on the server after the user presses Cancel or closes the drawer; the page only ignores the result. `busy` correctly disables the options and buttons, so there is no double submit.
- Fix: add an optional `signal` to `exportPreview` and pass `mine.signal`. Same for the other signal-less calls if they can be slow.
- Test: node test with a fake `fetch`: abort during preview, assert the request received an aborted signal. Dataset: none.

### PF-F-05  Low  Search boxes filter the whole list on every keystroke
- Where: `web/src/components/ComparisonTable.svelte:24,53,98`, `web/src/components/OperationTable.svelte`, `web/src/components/RequestVolume.svelte:13,16,33`.
- 3-month case: the list is the API groups of the 7-day analysis (hundreds, perhaps a few thousand rows). Each keystroke re-filters and re-sorts all rows, but only 10 rows are drawn, so cost is a few ms. No problem now; it only matters if group counts reach tens of thousands.
- Fix (optional): debounce the bound value by about 150 ms.
- Test: node test on the filter helper with 5,000 synthetic groups, timing under a loose bound.

### PF-F-06  Low  Hour grid draws up to 2880 buttons with three handlers each
- Where: `web/src/components/HourHeatmap.svelte:53-73` (120 rows x 24 cells, each with `onfocus`, `onkeydown`, `onclick`, `title`, `aria-label`).
- 3-month case: Last 90 days draws 2160 buttons, Last 120 days draws 2880. The date formatters are cached (`web/src/lib/time.ts:93-98`), so label building is cheap; an estimated first paint of tens of ms. Switching the metric chip changes only the shade classes. Acceptable. Roving tabindex is already used, so keyboard use is fine.
- Fix (only if profiling shows jank): one delegated handler on the grid element reading `data-r`/`data-c`.
- Test: Playwright, 120-day grid fixture, measure the time from click to the grid being visible; assert under 1 s on CI.

### PF-F-07  Low  Hard-coded UI text outside `web/src/lib/copy/*`
- Where: `web/src/components/RollupView.svelte:79` ("Reading the sources..."), `web/src/components/SubBar.svelte:36` ("Cancel run", "Comparing...", "Run analysis"), `web/src/components/HourHeatmap.svelte:37,46` (aria labels), `web/src/lib/rollup.ts:42` (range labels).
- The rule says new text goes through the copy dictionaries; these are older strings, so they are listed rather than treated as regressions. Any fix for PF-F-02 should add its text to `rollupCopy`, not inline.
- Test: `copylint.test.cjs` / `copy.screen.test.cjs` already scan copy; extend the scan list to cover these files if the strings move.

### PF-F-08  Info  Needs the backend sweep
- The timeline Cancel and the analysis `cancelAnalysis` (`web/src/lib/appState.svelte.ts:1287`) abort the browser request only. Whether the Go handler stops scanning when the request context ends is a backend question; the backend agent should confirm that for the 30-day tracing read and the 120-day daily read.

## Checked and fine
- Timeline `TracingPaper.svelte`: `run()` aborts the old controller, uses a sequence number, disables submit while loading, shows "reading" text plus Cancel, a veil over the old chart (inert, `aria-busy`), an error panel with Try again that repeats the last body, and aborts on destroy. No double-fetch (runs only on submit).
- `TracingChart.svelte`: at most 120 `.bar` and 120 overlay `.column` nodes per lane; pointer moves are rAF-coalesced; the tooltip derives from one slot; no full-data scan per move. `slotGrid` returns null above `SERIES_CAP = 2000` slots (guard). `barsFor`/`hasAnyData` are O(buckets).
- `canvas.ts` tick and zoom loops are bounded by the 120 shown buckets. `chartZoom.ts` drag/hover work is bounded and rebuilds the readout only when the slot pair changes. `zoomView.svelte.ts` has abort, a run sequence, a 24-entry cache, loading and error states.
- Server tracing axis (`internal/server/tracing_axis.go`, read for context): `tracingMaxBuckets = 120`, so response size is bounded for any window.
- `windows.ts`: 30-day client cap blocks 3 and 12 months before any request; window fields persist in sessionStorage.
- Analysis (`appState.svelte.ts` `runAnalysis` ~1258-1286): abort, sequence, ignored manual click while loading, old result kept, error banner, auto-run guarded by `scopeProblem` (7-day cap). SubBar shows a disabled "Comparing..." button and a "Cancel run" button.
- `RequestVolume`, `Pulse`: ECharts over 120 slots, ResizeObserver, disposed on destroy. `traffic.ts` window loop capped at 120.
- `OperationTable`, `ComparisonTable`: client paging of 10. `RequestTable`: cursor paging of 100 with loading text, disabled buttons while loading.
- `DailyTable`: at most 120 plain rows, CSV built once on click. `DayOverlay`: ECharts with two series of 24 points.
- `PatternsPanel`: abort plus stale guard, run button disabled while running, error state, re-run available. `EndpointDaily`: abort per effect, loading and error text, Days select re-runs (no explicit Try again; low value). `CallTrace`: abort, sequence, loading, error with Try again, at most 3 calls. `ResourcePanel`: abort, loading, error with Try again.
- Case open `CaseHome.svelte`: abort, loading status, failed state with Try again, quiet reloads.
- Archive / check / free drawers and `ImportPanel`: submit disabled while starting, progress percent, Stop/Cancel buttons (disabled while stopping), 1 s job polling with an unreachable counter (`JOB_MAX_UNREACHABLE`), alive flag so polls stop on close, preview cancelled on close. Restore drawer disables inputs and Cancel while working. ArchivedList pages 50 at a time.
- `ExportPanel`: preview/save phases, options disabled while busy, Cancel/Preview again, hash check before download.

## Not checked
- Real timings (app not run, by instruction). `lib/charts.ts`, `StallMinutesPanel`, `TrafficSlowness`, blackbox and wizard components were not read.
- No browser profiling; "tens of ms" figures are estimates from node counts.

## Existing coverage worth extending
- Node: `web/tests/rollup.test.cjs`, `rollupOverlay.test.cjs`, `callTrace.test.cjs`, `importJob.test.cjs`, `export.test.cjs`, `copylint.test.cjs`.
- Playwright: `tools/e2e/specs/tracing-screen.spec.ts`, `explorer.spec.ts`, `exports.spec.ts`, `imports.spec.ts`. None delays or fails a daily/hour-grid response, none has a hung request.
