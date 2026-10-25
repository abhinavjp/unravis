# TSK-19: Review, create the case and import
risk: medium — first write to the server; partial-failure handling.

## Outcome
Step 3 summarises "N environments, M portals, K APIs, S shared sources" (blank = "not named"), then creates the case, writes environments and source tags (TSK-07), starts imports through the existing `postDiscoverImports` / `postImport` (I2), shows progress per source, and opens the analysis when at least one import has finished. If the case is created but an import fails, the case is kept, the failure is explained per source, and the user can retry from the analysis' "Add more data".

## Write scope
- `web/src/components/wizard/ReviewStep.svelte`, `web/src/components/wizard/CreateProgress.svelte` (new)
- `web/src/lib/wizardSubmit.ts` (new orchestration), `web/tests/wizardSubmit.test.cjs` (new)
- `web/src/lib/api.ts` (wrappers for the TSK-07 routes), `web/src/lib/types.ts` (source/env types)

## Must not change
The import job polling contract; existing `importJob` tests; gating acknowledgement flow (`ackGating`, `ackText` still required and shown).

## Changes
1. `submitWizard(draft, api)`: create case → set environments → add source tags → start imports → poll jobs; each step reports its own status; on failure after case creation it returns `{caseId, failures[]}` rather than throwing, and never deletes the case.
2. Abandoning before submit creates nothing on the server.
3. Gating banners from the importers are shown in the review before the user confirms.

## Proof
Tests with a fake `api`: success path order of calls; failure on environments call (case kept, reported); failure on one of three imports (two continue); cancel before submit → zero calls; double-click submit → one case. `cd web; npm test; npm run check; npm run build` green.

## Depends on
TSK-07, TSK-18
