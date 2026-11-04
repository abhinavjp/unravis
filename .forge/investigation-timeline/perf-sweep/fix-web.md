# Web fixes for large windows (PF-F-01..08)

Gate (from web/): npm run check 0 errors 0 warnings; node --test tests/*.test.cjs 2217 pass 0 fail; npm run build + distChunks 2 pass.
E2E (UNRAVIS_EXE=.dev/run/perf-ui/unravis-gate.exe): large-window 4/4 pass; tracing-screen + investigate 30/30 pass.
No exe left in repo root. web/dist and cmd/unravis-setup/ui/assets/index.css are dirtied by the build and left alone.

## Owner ruling: Timeline cap 186 days
Done. web/src/lib/tracing/windows.ts (MAX_TRACE_DAYS 186, tooLongText "The longest window is 186 days. Shorten the ..."). Rollup cap stays 120 days.
Tests: web/tests/tracingWindows.test.cjs, tools/e2e/specs/tracing-screen.spec.ts (TRACE-11).

## PF-F-01 request timeout (150 s on reads): done
- New web/src/lib/requestTimeout.ts (READ_TIMEOUT_MS, guardSignal with AbortSignal.timeout + AbortSignal.any and a timer fallback, typed timeout error).
- web/src/lib/api.ts (reads default to the limit; exportConfirm has none), web/src/lib/cases.ts (GET default limit, polls pass null, fetchTimeline explicit), web/src/lib/tracing/model.ts (fetchTracing, tracingErrorText), web/src/lib/guided/api.ts (runInvestigation, buildReport), web/src/lib/archiveApi.ts and web/src/lib/discover.ts (polls: no limit).
- A timeout shows "This is taking longer than expected. Try a shorter window." in each screen's error state with Try again. A user cancel stays a cancel.
- Tests: web/tests/largeWindow.test.cjs (20), callTrace/collect/tracingChart/tracingRound4 updated; e2e LW-02, LW-04.

## PF-F-02 Rollup in parallel with Cancel and Try again: done
web/src/components/RollupView.svelte (Promise.all for daily and hour grid, Cancel, cancelled note, error alert with Try again), web/src/lib/rollup.ts. E2E LW-03, LW-04.

## Still-reading line after about 6 s: done (timeline, rollup, analysis)
web/src/lib/slowRead.ts, slowRead.svelte.ts, TracingPaper.svelte, RollupView.svelte, SubBar.svelte, App.svelte (Try again on an analysis timeout). Cancel stays visible, no toasts, Cancel and Try again carry title tooltips. E2E LW-01, LW-03.

## Lane partial note: done
web/src/lib/tracing/lanes.ts: "Showing part of this lane: the read hit its time limit. Try a shorter window."

## Daily series partial flag: PENDING
.forge/investigation-timeline/perf-sweep/fix-backend.md did not exist when I finished. The text is ready in WAIT_TEXT.dailyTimeout (web/src/lib/copy/waitCopy.ts); it still has to be wired in RollupView once the backend names the JSON flag.

## PF-F-03 7-day Analysis refusal: done
web/src/lib/autorun.ts SEVEN_DAY_TEXT appends "Use the Timeline or the Daily view for longer periods." Tests: pure.test.cjs, tools/e2e/specs/investigate.spec.ts.

## PF-F-04 optional signal: done for exportPreview (ExportPanel passes its signal) and exportConfirm. Ledger, sources and keeper calls not changed.

## PF-F-07 strings into dictionaries: partial
Moved to web/src/lib/copy/waitCopy.ts (WAIT_TEXT, RUN_TEXT, GRID_TEXT, ROLLUP_RANGE_LABELS) and rollupCopy: rollup labels and status, HourHeatmap.svelte aria and legend text, SubBar run controls. Lint scan extended through waitCopyEntries() and windowCopyEntries(). Other hard-coded strings from the findings list were not touched.

## PF-F-05, 06, 08: not touched in this pass.

## Files
New: tools/e2e/specs/large-window.spec.ts, web/src/lib/copy/waitCopy.ts, web/src/lib/requestTimeout.ts, web/src/lib/slowRead.ts, web/src/lib/slowRead.svelte.ts, web/tests/largeWindow.test.cjs.
Edited: web/src/lib/{api,cases,rollup,autorun,archiveApi,discover}.ts, web/src/lib/tracing/{windows,model,lanes}.ts, web/src/lib/guided/api.ts, web/src/components/{TracingPaper,RollupView,HourHeatmap,SubBar,ExportPanel}.svelte, web/src/App.svelte, web/tests/{tracingWindows,tracingChart,tracingRound4,callTrace,collect,pure}.test.cjs, tools/e2e/specs/{tracing-screen,investigate}.spec.ts.
