# WP-06b Web client sends the case id on export confirm
risk: low    size: S    wave: 3    model: sonnet

## Outcome
The Case tools export keeps working after WP-06a: the confirm request carries `caseId`, and a 409 ("the preview changed; run the preview again") returns the panel to the idle state so the user runs a new preview (TR-21).

## Write scope
- `web/src/lib/cases.ts`
- `web/src/lib/components/CaseTools.svelte`
- `web/tests/import.test.cjs`

## Must not change
- `previewCaseExport(id, signal)` and its request body (`{ caseId }`): the server default scrub is ON, so the preview does not send `scrubSensitive` yet (a later toolbar packet does).
- The SHA-256 check (`contentMatches`) and the two downloads in `save()`.
- No new copy strings: the 409 text comes from the server through `CaseApiError`.

## Changes
1. `cases.ts`: change `confirmCaseExport(previewId: string, signal?)` to `confirmCaseExport(previewId: string, caseId: string, signal?: AbortSignal)`. Throw `new CaseApiError('That is not a case id.', 400, 'bad-case-id')` when `!isCaseId(caseId)`, as `previewCaseExport` does. The POST body to `/export/confirm` becomes `{ previewId, caseId }`. Update the doc comment above it: the answer is no longer once-only; the server rebuilds the files from the case as it is now and answers 409 when the case changed after the preview.
2. `CaseTools.svelte` `save()`: call `confirmCaseExport(shown.previewId, caseId)` (the component already has `caseId`). In the `catch`, when `error instanceof CaseApiError && error.status === 409`, set `phase = { kind: 'idle' }` (the shown preview is stale) after setting `exportProblem = failure(error)`; every other error keeps `phase = { kind: 'preview', preview: shown }`. Remove the sentence "Confirming used the preview up" from the nearby comment.
3. `import.test.cjs`: in the test named `case export: preview posts the case id, confirm posts the preview id, and the answers are checked`, change the call to `cases.confirmCaseExport('p-1', CASE_ID)` and the expected body to `{ previewId: 'p-1', caseId: CASE_ID }`; add `await assert.rejects(cases.confirmCaseExport('p-1', '../x'), (e) => e.reason === 'bad-case-id')`.
4. Add a test `case export: a 409 on confirm is shown as a stale preview` in the same file: `mockServer` answers `/api/export/confirm` with status 409 and body `{ error: 'the preview changed; run the preview again' }` (the shape `writeError` sends); assert `confirmCaseExport` rejects with `e.status === 409`. Add a second assertion that calls the file's existing helper `read('src/lib/components/CaseTools.svelte')` inside the test (the same path as the `caseTools` constant near the end of the file; do not use `src/components/`) and matches `/status === 409/`, in the file's existing "read as source" style.

## Proof
- `cd web && node --test tests/import.test.cjs` -> all pass, including the two tests above.
- `cd web && npm test` -> pass.
- `cd web && npm run check` -> 0 errors (the changed signature is used in one place).

## Depends on
WP-06a
