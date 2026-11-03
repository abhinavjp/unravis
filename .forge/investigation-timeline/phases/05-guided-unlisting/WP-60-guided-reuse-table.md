# WP-60 Guided reuse table
risk: low    size: S    wave: 1    model: haiku
## Outcome
A document lists every guided chart, component, helper and copy file with its path, what it does and how tracing may reuse it later, so nothing guided is deleted by accident (TR-09 groundwork, owner decision D-2 = A: guided is kept and unlisted).

## Write scope
- `docs/guided-reuse-table.md` (new)

## Must not change
- Any file under `web/`, `internal/`, `tools/`. This packet is a document only; no row means a deletion.
- `docs/user/*.md` (embedded in Help and tied to Go docs tests).
- INV-8: guided code stays.

## Changes
1. Create `docs/guided-reuse-table.md`: title `# Guided reuse table`, then two sentences: guided analysis is kept and unlisted (owner decision D-2 = A); the table says what tracing may reuse later; no row deletes anything; the checklist, After the fix and Nearby activity are NOT rebuilt in Tracing now (they stay in guided only).
2. Add one Markdown table with the columns `Path | What it does | How tracing may reuse it later`, one row per file below (paths relative to repo root, all under `web/src/lib/` except charts which is also there). Copy the text of the third column as written. For rows marked (confirm) open the first 30 lines of the file and write one accurate clause for column two; do not guess.
   - `web/src/lib/charts.ts` | Shared chart drawing, axis and format helpers | Reuse axis and formatting helpers for the tracing chart; do not fork.
   - `web/src/lib/guided/frame.ts` | Incident and reference frame, `frameProblems` overlap check | Copy the overlap rule into `web/src/lib/tracing/windows.ts` (WP-22); do not import it.
   - `web/src/lib/guided/afterFix.ts` | After-the-fix comparison model | Later: reuse the comparison shape if tracing adds a before and after view.
   - `web/src/lib/guided/nearby.ts` | Nearby-activity window and duration bands | Later: reuse the window-expansion rule for trace drill-down.
   - `web/src/lib/guided/stepModel.ts` | Step model, status words, `guidedOpenFor` | Kept in guided only; tracing does not rebuild it.
   - `web/src/lib/guided/tools.ts` | Manual-tools definitions | Kept in guided only; not rebuilt in tracing now.
   - `web/src/lib/guided/report.ts` | Report and export file naming, warnings | Reuse the export scrub plumbing later (TR-21).
   - `web/src/lib/guided/normalize.ts` | Normalises engine answers (result, view, checklist item, report) | Reuse pure helpers when both surfaces need the same normalisation.
   - `web/src/lib/guided/api.ts` | Guided API calls | Reference only; tracing uses its own `POST /api/cases/{id}/tracing`.
   - `web/src/lib/guided/types.ts` | Guided types | Reference only; tracing types live in `web/src/lib/tracing/`.
   - `web/src/lib/guided/guided.svelte.ts` | Guided screen state store | Reference only; tracing keeps its own state.
   - `web/src/lib/guided/inflight.ts` | In-flight async state helper | Reference only; tracing keeps its own loading state.
   - `web/src/lib/components/guided/GuidedAnalysis.svelte` | Guided shell | Stays in the app, unlisted; never opened from the case home after WP-64.
   - `.../Checklist.svelte`, `.../AfterFix.svelte`, `.../NearbyActivity.svelte`, `.../SavedViews.svelte`, `.../ManualTools.svelte` | Checklist, After the fix, Nearby activity, saved views, manual tools panels | Kept in guided only (not rebuilt in Tracing now).
   - `.../StepRail.svelte`, `.../RunBar.svelte`, `.../Step0Frame.svelte` | Step rail, run bar, step-0 frame shell | Reference pattern only; tracing has no steps.
   - `.../SlowCalls.svelte` | Slow-calls table | Later candidate for a tracing drill-down table.
   - `.../Sticker.svelte` | Status sticker | Reuse the sticker styling for lane-state labels if useful.
   - `.../ReportExport.svelte` | Report and export panel | Reuse the export scrub wording later.
   - `.../MinuteLine.svelte` | Minute-level timeline line | Later: pattern for the tracing chart point and label treatment.
   - `.../Step10Origin.svelte`, `.../StepFacts.svelte`, `.../StepShell.svelte`, `.../SuspectCard.svelte` (confirm) | Step 10 origin panel, step facts, shared step wrapper, suspect card | Kept in guided only.
   - `web/src/lib/copy/guidedCopy.ts`, `web/src/lib/copy/guidedShellCopy.ts` (confirm) | Guided screen text and shell text | Reference only; tracing has its own copy file (`web/src/lib/copy/tracingCopy.ts`, WP-16a).
   Write the `.../` rows with the full path `web/src/lib/components/guided/<Name>.svelte` (one row per file, not grouped) so the proof can find every name.
3. Under the table add the line: `Guided unit tests (web/tests/guided.*.test.cjs) and the guided Playwright specs (tools/e2e/archive/guided/) are kept; see docs/guided-unlisted.md.`

## Proof
- `Get-ChildItem web/src/lib/guided, web/src/lib/components/guided -File | ForEach-Object { if (-not (Select-String -Path docs/guided-reuse-table.md -SimpleMatch $_.Name -Quiet)) { $_.Name } }` -> prints nothing (all 28 files named).
- `Select-String -Path docs/guided-reuse-table.md -SimpleMatch 'web/src/lib/charts.ts' -Quiet` -> `True`.
- `git status --short` -> only `?? docs/guided-reuse-table.md` is new from this packet; `git diff --stat -- web internal tools` shows nothing from this packet.

## Depends on
none
