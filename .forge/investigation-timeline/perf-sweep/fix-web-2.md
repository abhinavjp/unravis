# Web wiring for the backend partial contract (fix-web-2)

Gate (from web/): npm run check 0 errors 0 warnings; node --test tests/*.test.cjs 2237 pass 0 fail (20 new in tests/partialNotices.test.cjs); npm run build + distChunks 2 pass.
E2E: not run. This machine has no Go toolchain, so no unstamped exe could be built; the existing .dev/run/perf-ui exe predates these changes. No spec was added to large-window.spec.ts (an unrun spec would be a risk). The Daily view is opened from the "Daily view" button on a comparison-table row; a spec would stub GET /api/endpoint-daily with `partial: true`, `daysPlanned`, and assert `[data-testid=daily-partial]` plus the Try again button.
web/dist and cmd/unravis-setup/ui/assets/index.css are dirtied by the build and left alone. No exe in the repo root.

## Endpoint daily partial
- web/src/lib/types.ts: EndpointDaily gains `partial?` and `daysPlanned?`.
- web/src/lib/copy/waitCopy.ts: `partialDaysText(shown, planned)` ("Showing N of M days; the read ran out of time. Shorten the window or Try again."), WAIT_TEXT.daysPartialUnknown, lint sample.
- web/src/lib/patterns.ts: `dailyPartialNotice`, `dailyWarnings` (drops the server cut warning when partial; the dictionary notice replaces it), `stepPartialNotice`, `stepSection(report, asked, planned?)` now returns `partial`.
- web/src/components/EndpointDaily.svelte: notice (role=status, data-testid daily-partial) with Try again (re-reads the same span); finished days stay drawn.
- web/src/components/PatternsPanel.svelte: step-change notice (data-testid step-partial) with Try again; the days asked for are kept in the done state (`stepPlanned`).

## Tracing lane reasons
- web/src/lib/tracing/model.ts: reason `broad-scan` added (`timeout` was already known).
- web/src/lib/tracing/lanes.ts: INCOMPLETE_TEXT for timeout and broad-scan (dictionary text wins over the server text), `laneCutShort`, `anyLaneCutShort`, `laneNoteTone` (warn for timeout, rowcap, broad-scan; bad otherwise), `laneNote` and `laneStatusLine` updated (a timed-out lane that kept state "events" still shows its note).
- web/src/lib/tracing/tooltip.ts: `laneHeadHint` (lane hint plus the cut-short sentence).
- web/src/components/TracingChart.svelte: tooltip note, lane head hint, in-panel note with `warn` style, status line "Some lanes were cut short..." (data-testid lanes-cut-short).

## Import copy and 409/413 consumers
- web/src/lib/copy/importCopy.ts: REASON_TEXT `file-cap`, `case-cap`, `case-import-running` (exact task sentences). ImportPanel, DiscoverPanel, CasePanel (close/reopen/create), erase, HostsEditor, CaseTools already map reasons through errorText/reasonText, so they show the sentences with no change.
- web/src/lib/api.ts: ApiError keeps the server `reason` (6th constructor argument).
- web/src/lib/caseWorkspace.ts: saver `failed` hook, `workspaceFailureNote`. web/src/lib/appState.svelte.ts: `workspaceNote` (set on a refused save, cleared by the next good one). web/src/App.svelte: inline status (role=status, Dismiss).
- web/src/lib/lanes.ts `runProblem` and web/src/lib/components/HealthPanel.svelte `failure`: 409 with `case-import-running` shows the import-running sentence.

## Tests
New: web/tests/partialNotices.test.cjs. Updated: web/tests/tracingChart.test.cjs (INCOMPLETE_TEXT gains broad-scan; lane-note class assertion).
