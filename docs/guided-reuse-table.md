# Guided reuse table

Guided analysis is kept and unlisted (owner decision D-2 = A). This table says what tracing may reuse later; no row deletes anything, and the checklist, After the fix and Nearby activity are NOT rebuilt in Tracing now (they stay in guided only).

| Path | What it does | How tracing may reuse it later |
|---|---|---|
| `web/src/lib/charts.ts` | Shared ECharts setup, theme palette and chart option builders; axis and format helpers come from `chartTime.ts` and `chartScale.ts` | Do not import: `tracingChart.test.cjs` forbids `charts.ts` and ECharts in the tracing chart. Reuse `chartTime.ts` and `chartScale.ts` helpers instead. |
| `web/src/lib/guided/frame.ts` | Incident and reference frame, `frameProblems` overlap check | Copy the overlap rule into `web/src/lib/tracing/windows.ts` (WP-22); do not import it. |
| `web/src/lib/guided/afterFix.ts` | After-the-fix comparison model: classifies a later day against the earlier one (`classifyDay`) | Later: reuse the comparison shape if tracing adds a before and after view. |
| `web/src/lib/guided/nearby.ts` | Nearby-activity window and duration bands | Later: reuse the window-expansion rule for trace drill-down. |
| `web/src/lib/guided/stepModel.ts` | Step model, status words, `guidedOpenFor` | Kept in guided only; tracing does not rebuild it. |
| `web/src/lib/guided/tools.ts` | Manual-tools definitions: tool list, source needs and saved-view settings | Kept in guided only; not rebuilt in tracing now. |
| `web/src/lib/guided/report.ts` | Report file naming, warning sentences and warning codes | Reuse the export scrub plumbing later (TR-21). |
| `web/src/lib/guided/normalize.ts` | Normalises engine answers (result, view, checklist item, report) | Reuse pure helpers when both surfaces need the same normalisation. |
| `web/src/lib/guided/api.ts` | Guided API calls under `/api/cases/{id}` (investigation, views, checklist, report) | Reference only; tracing uses its own `POST /api/cases/{id}/tracing`. |
| `web/src/lib/guided/types.ts` | Guided types | Reference only; tracing types live in `web/src/lib/tracing/`. |
| `web/src/lib/guided/guided.svelte.ts` | Guided screen state store, one instance per open case, held in memory | Reference only; tracing keeps its own state. |
| `web/src/lib/guided/inflight.ts` | In-flight async state helper (`InFlight`): a second press on the same key is ignored | Reference only; tracing keeps its own loading state. |
| `web/src/lib/components/guided/GuidedAnalysis.svelte` | Guided shell: the step rail, run bar and panels for steps 0 to 10 and manual tools | Stays in the app, unlisted; never opened from the case home after WP-64. |
| `web/src/lib/components/guided/Checklist.svelte` | Checklist panel: four lists (todo, doing, learned, ruled out) with counters | Kept in guided only (not rebuilt in Tracing now). |
| `web/src/lib/components/guided/AfterFix.svelte` | After-the-fix panel: runs the engine on a later day beside the incident day | Kept in guided only (not rebuilt in Tracing now). |
| `web/src/lib/components/guided/NearbyActivity.svelte` | Nearby-activity panel: calls around one moment, in duration bands | Kept in guided only (not rebuilt in Tracing now). |
| `web/src/lib/components/guided/SavedViews.svelte` | Saved views panel: named windows, API and tool filters | Kept in guided only (not rebuilt in Tracing now). |
| `web/src/lib/components/guided/ManualTools.svelte` | Manual tools panel: runs one tool at a time, in any order | Kept in guided only (not rebuilt in Tracing now). |
| `web/src/lib/components/guided/StepRail.svelte` | Step rail: the left list of steps 0 to 10, each with a sticker | Reference pattern only; tracing has no steps. |
| `web/src/lib/components/guided/RunBar.svelte` | Run bar: the "Run the steps" and "Run again" button and frame problems | Reference pattern only; tracing has no steps. |
| `web/src/lib/components/guided/Step0Frame.svelte` | Step 0 frame panel: incident and reference windows, API, pool limit and change list | Reference pattern only; tracing has no steps. |
| `web/src/lib/components/guided/SlowCalls.svelte` | Slow-calls table: the longest calls in the incident window (route, start, duration) | Later candidate for a tracing drill-down table. |
| `web/src/lib/components/guided/Sticker.svelte` | Status sticker: word and tone for Ready, Partly ready, Cannot test yet, Failed, Waiting and Running | Reuse the sticker styling for lane-state labels if useful. |
| `web/src/lib/components/guided/ReportExport.svelte` | Report and export panel: HTML or CSV report, warnings, file saved only when its checksum matches | Reuse the export scrub wording later. |
| `web/src/lib/components/guided/MinuteLine.svelte` | Small line chart of one number per minute; a minute with no value breaks the line | Later: pattern for the tracing chart point and label treatment. |
| `web/src/lib/components/guided/Step10Origin.svelte` | Step 10 panel: what moved first, suspects, confidence and the one check that would overturn the answer | Kept in guided only. |
| `web/src/lib/components/guided/StepFacts.svelte` | Steps 1 to 9 panel: one engine part per step, with minute line, slow calls and suspects | Kept in guided only. |
| `web/src/lib/components/guided/StepShell.svelte` | Shared step wrapper: title, status sticker, "In a nutshell" sentence, untested reasons, body and Previous and Next | Kept in guided only. |
| `web/src/lib/components/guided/SuspectCard.svelte` | One suspect card: colour word, result, why, evidence, next check and what would overturn it | Kept in guided only. |
| `web/src/lib/copy/guidedCopy.ts` | Guided screen text (`GUIDED_TEXT`) for the guided analysis, manual tools, checklist, after-the-fix and report export | Reference only; tracing has its own copy file (`web/src/lib/copy/tracingCopy.ts`, WP-16a). |
| `web/src/lib/copy/guidedShellCopy.ts` | Guided shell and manual-tools text (`SHELL_TEXT`) that guidedCopy.ts does not hold | Reference only; tracing has its own copy file (`web/src/lib/copy/tracingCopy.ts`, WP-16a). |

Guided unit tests (web/tests/guided.*.test.cjs) and the guided Playwright specs (tools/e2e/archive/guided/) are kept; see docs/guided-unlisted.md.
