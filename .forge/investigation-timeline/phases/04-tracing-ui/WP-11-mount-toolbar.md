# WP-11 Mount the toolbar in the timeline screen only
risk: low    size: M    wave: 7    model: sonnet

## Outcome
The Investigation Timeline screen shows the trace toolbar: the three lane buttons really hide and show their rows in the chart, and the scrub pill states the export setting last seen in an export preview, or the "Default" sentence when none has run in this session. The per-call tray (`CallTrace.svelte`) does not get the toolbar (TR-23, TR-12, AC-15).
Size note for the caller: spec says S (one file); the pill needs a small shared state module and one line in `CaseTools.svelte`, so this is M.

## Write scope
- `web/src/components/TracingPaper.svelte` (only the `data-region="toolbar"` region and the `visible` prop passed to the chart)
- `web/src/lib/exportScrub.svelte.ts` (new)
- `web/src/lib/components/CaseTools.svelte` (one line in `startPreview` success path; nothing else)
- `web/tests/traceToolbarMount.test.cjs` (new)

## Must not change
- `web/src/components/CallTrace.svelte` and `web/src/lib/callTraceTray.svelte.ts` (INV-7, AC-15): the toolbar is never mounted there.
- `web/src/components/TraceToolbar.svelte` and `web/src/lib/tracing/toolbar.ts` (WP-10), `TracingChart.svelte` and `lanes.ts` (WP-17b, WP-23); the `windows`, `chart` loading/error branches and `notes` regions of `TracingPaper.svelte`; its request/stale-response logic.
- The export flow in `CaseTools.svelte`: no change to the preview, the save, the saved-file name, `previewId` handling or any copy. The new line only reads the preview result.
- Never write the scrub value to storage, a URL or a request body (INV-3, nothing leaves the screen); no button on the timeline screen changes the export setting.

## Changes
Verified in code: `CaseTools.svelte` calls `previewCaseExport(caseId, mine.signal)` inside `startPreview()` and stores the answer as `phase = { kind: 'preview', preview: result }` after `if (!mine.signal.aborted)`; `CaseExportPreview` is `{ previewId: string; export: EvidenceExport }` in `web/src/lib/cases.ts`. Phase 01 WP-06a adds the `scrubSensitive` boolean to the preview body and WP-06b edits `CaseTools.svelte` (the case id handling) before this packet, which is why WP-06b is a dependency; this packet reads the boolean defensively, so it does not depend on the type being present.
Note: the spec's WP-11 lists only `TracingPaper.svelte`; the extra files are needed because the export preview lives in `CaseTools.svelte`, a different screen from the timeline.
1. `web/src/lib/exportScrub.svelte.ts` (same pattern as `web/src/lib/timeZone.svelte.ts`): module state `let value = $state<boolean | null>(null)`; `export const exportScrub = { get value() { return value; }, set(next: boolean | null) { value = next; } }`. In memory only: no localStorage, no URL.
2. `web/src/lib/components/CaseTools.svelte`: add `import { exportScrub } from '../exportScrub.svelte';` and, directly after `if (!mine.signal.aborted) phase = { kind: 'preview', preview: result };` set `exportScrub.set(typeof (result.export as { scrubSensitive?: unknown }).scrubSensitive === 'boolean' ? (result.export as { scrubSensitive: boolean }).scrubSensitive : null)` inside the same `if` block (braces added). Nothing else in the file changes.
3. `web/src/components/TracingPaper.svelte`:
   - Imports: `TraceToolbar` from `./TraceToolbar.svelte`; `allLanesVisible`, `toggleLane` from `../lib/tracing/toolbar`; `exportScrub` from `../lib/exportScrub.svelte`.
   - State: `let visible = $state(allLanesVisible());` (kept across re-runs and zoom; not reset by a new request).
   - Region `toolbar`: `<TraceToolbar {visible} onToggle={(key) => (visible = toggleLane(visible, key))} scrub={exportScrub.value} />`. Shown in every state of the screen except while the very first request is loading, and also for closed and read-only cases (it only changes the view).
   - Chart region ready branch: pass `{visible}` to `<TracingChart ... {visible} />`. The `visible` prop (`Record<TracingLaneKey, boolean>`) is added to `TracingChart.svelte` by WP-23, a dependency of this packet; this packet also builds on WP-22 (the `windows` region and run logic of `TracingPaper.svelte`) and WP-16b/WP-17b (the screen and the chart). If `TracingChart.svelte` has no `visible` prop, STOP and report instead of adding it.
4. `web/tests/traceToolbarMount.test.cjs` (reads files as text). Tests:
   - `toolbar is mounted only by the timeline screen`: scan every `.svelte` under `web/src`; the only files containing `TraceToolbar` are `TracingPaper.svelte` and `TraceToolbar.svelte`; `CallTrace.svelte` contains neither `TraceToolbar` nor `exportScrub`.
   - `timeline screen wires toggles, scrub state and chart visibility`: `TracingPaper.svelte` contains `<TraceToolbar`, `toggleLane(`, `exportScrub.value` and `{visible}` on the chart element, and `data-region="toolbar"` still wraps the toolbar.
   - `export scrub state is in memory and set by the preview`: `exportScrub.svelte.ts` contains `$state<boolean | null>(null)` and no `localStorage`/`sessionStorage`/`location`; `CaseTools.svelte` contains `exportScrub.set(` exactly once, after `previewCaseExport(`.
   - `export flow is untouched`: `CaseTools.svelte` still contains `previewCaseExport(caseId, mine.signal)`, `canSaveCaseExport` and `confirmCaseExport`.

## Proof
- `cd web && node --test tests/traceToolbarMount.test.cjs tests/traceToolbar.test.cjs tests/tracingPaper.test.cjs tests/tracingChart.test.cjs tests/callTrace.test.cjs` -> all pass (`callTrace.test.cjs` unchanged and green).
- `cd web && npm test` -> all pass; `cd web && npm run check` -> 0 errors.
- `cd web && npm run build && node --test tests/distChunks.test.cjs` -> pass.
- Wireframe check (`prototype/tracing-paper-PROTOTYPE.html`, toolbar over the chart): API audit / SQL / IIS buttons pressed by default; pressing one removes that row and sets `aria-pressed="false"`, pressing again brings it back; before any export preview the pill reads "Default: export hides IP, referer and cookie hash"; after running Preview in the case tools (scrub on) it reads "Export hides IP, referer and cookie hash", with scrub off "Export includes IP, referer and cookie hash (scrub is off)"; the per-call tray still has no toolbar. Check at 1280 px and 375 px, light and dark; Tab reaches every control; console clean; no request is sent when a lane is toggled.

## Depends on
WP-10, WP-22, WP-23, WP-06b
