# WP-63 Guided kept: note and guard test
risk: low    size: M    wave: 10    model: sonnet
## Outcome
The archive baseline (80 passing archived tests, run before WP-64 removes the entry button) is recorded in `progress.md`. Guided analysis is documented as unlisted but maintained, and a guard test fails if a guided unit test, a guided source file, a guided API route or an archived guided e2e spec is ever deleted (owner decision D-2 = A, INV-8). No behaviour changes.

## Write scope
- `web/src/lib/components/guided/GuidedAnalysis.svelte` (header comment only)
- `web/src/lib/guided/guided.svelte.ts` (header comment only)
- `docs/guided-unlisted.md` (new)
- `web/tests/guidedKept.test.cjs` (new)

## Must not change
- `internal/server/routes.go` and every Go file: `/api/cases/{id}/investigation`, `/views`, `/checklist`, `/report` routes stay exactly as they are.
- Any guided test file (`web/tests/guided.*.cjs`), any guided source file other than the two header comments, and the archived e2e specs.
- `docs/user/*.md`: embedded in Help (`web/src/lib/help/pages.ts`) and tied to `internal/investigate/docs_test.go`. Note: `docs/user/guided-analysis.md` (lines 5 and 151) and `docs/user/manual-tools.md` (line 7) still say "choose Open guided analysis"; after WP-64 that button is gone. Fixing it changes Help text and Go docs tests, so it is an ESCALATION in the authoring report, not part of this packet.

## Changes
1. `GuidedAnalysis.svelte`: add as the very first line (above `<script lang="ts">`) the HTML comment `<!-- Guided analysis is kept and maintained but unlisted: the case home no longer opens it (Start tracing replaced the Guided section). Do not delete it. See docs/guided-unlisted.md. -->`.
2. `web/src/lib/guided/guided.svelte.ts`: add as the first line the comment `// Guided analysis is kept and maintained but unlisted (see docs/guided-unlisted.md). Do not delete.`
3. Create `docs/guided-unlisted.md` (about 30 lines): what unlisted means (code, unit tests, API routes and copy stay; only the case-home entry goes); the four kept routes `/api/cases/{id}/investigation`, `/views`, `/checklist`, `/report`; the guided unit tests (`web/tests/guided.logic.test.cjs`, `guided.steps.test.cjs`, `guided.ui.test.cjs`, fixture `guided.fixture.cjs`); the archived e2e specs and how to run them (`powershell -File run-e2e.ps1 -Archive`, `npm run e2e:guided`, folder `tools/e2e/archive/guided/`); link to `docs/guided-reuse-table.md`; a "Known gap" paragraph: after the case-home swap no screen opens `GuidedAnalysis` (the `#/case/guided/N` route is defined in `web/src/lib/screenRoute.ts` but nothing in `src` reads it), so the archived e2e specs need a build from before the swap or an entry hook, which the owner has to decide.
4. Create `web/tests/guidedKept.test.cjs` (node:test, `node:fs`, `node:path`; `root = path.resolve(__dirname, '..')`, repo root `path.resolve(root, '..')`). Tests, by exact name:
   - `the guided unit tests and their fixture are still present`: `tests/guided.logic.test.cjs`, `tests/guided.steps.test.cjs`, `tests/guided.ui.test.cjs`, `tests/guided.fixture.cjs` exist.
   - `the guided source folders are intact`: `src/lib/guided/` contains `afterFix.ts`, `api.ts`, `frame.ts`, `guided.svelte.ts`, `inflight.ts`, `nearby.ts`, `normalize.ts`, `report.ts`, `stepModel.ts`, `tools.ts`, `types.ts`; `src/lib/components/guided/GuidedAnalysis.svelte` exists and the folder has at least 17 `.svelte` files.
   - `the guided API routes are still registered`: read `internal/server/routes.go` from the repo root and assert it contains each of the four pattern strings `"/api/cases/{id}/investigation"`, `"/api/cases/{id}/views"`, `"/api/cases/{id}/checklist"`, `"/api/cases/{id}/report"`.
   - `the archived guided e2e specs still exist`: under `tools/e2e/archive/guided/` the files `guide-steps.spec.ts`, `guide-checklist.spec.ts`, `guided.spec.ts`, `export-ui.spec.ts`, `manual-tools.spec.ts`, `exports-guided.spec.ts`, `keeper-update-guided.spec.ts`, `support.ts`, `README.md` exist.
   - `the guided shell and store say they are unlisted`: both edited files match `/unlisted/`.

## Proof
- `cd web; node --test tests/guidedKept.test.cjs` -> 5 tests pass.
- `cd web; npm test` -> exit 0, `# fail 0`.
- BASELINE BEFORE WP-64 (hard step): on a branch exe built from the tree at this point (recipe: WP-58 Changes step 5), run `cd tools/e2e; powershell -File run-e2e.ps1 -Archive -Exe <exe>` -> 80 passed, exit 0, `report/archive.json` non-empty. Write the result (count and exe path) into `progress.md` as "Archive baseline before WP-64: 80 passed". Do not mark this packet done without that line. After WP-64 removes the entry button the archive cannot open Guided and is informational only.
- `git grep -n -i -l guided -- web/tests` -> still lists `guided.logic.test.cjs`, `guided.steps.test.cjs`, `guided.ui.test.cjs`, `guided.fixture.cjs`.
- `git diff --stat -- internal` -> empty. `Get-ChildItem tools/e2e/archive/guided -Filter *.spec.ts | Measure-Object` -> Count 7.

## Depends on
WP-62b, WP-62c
