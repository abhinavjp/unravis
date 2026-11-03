# Phase 05: guided-unlisting

risk: medium (a case-home button swap plus moving 80 Playwright tests; no server or schema change)

## Goal

Guided analysis stays in the app, code and unit tests untouched, but is no longer offered on the case home: the Guided section is removed and the Investigation Timeline entry added in phase 04 (WP-16b) is the one entry (TR-01, owner decision D-2 = A). Clicking a bar with events in the tracing chart opens the analysis on that bar's time slot (TR-09). The guided Playwright specs leave the default e2e suite and live in `tools/e2e/archive/guided/`, runnable with `-Archive`. Nothing guided is deleted.

## Packets

| Packet | Title |
|---|---|
| WP-60 | Guided reuse table |
| WP-61 | Bucket click hand-off to the analysis |
| WP-62a | Guided e2e archive harness |
| WP-62b | Move the guided-only specs to the archive |
| WP-62c | Extract the guided tests from mixed specs, update COVERAGE |
| WP-63 | Guided kept: note and guard test |
| WP-64 | Case home: remove the Guided section |

WP-62 of the spec was split into 62a/62b/62c (one id was about twelve files); WP-62b and WP-62c have disjoint write scopes. Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

## Phase proof (repo root, PowerShell)

- `cd web; npm test` -> exit 0, `# fail 0`. Includes `tracingHandoff`, `guidedKept`, `startTracing`, the edited `p3.fix` and `distChunks` (build first: `npx vite build`).
- `cd web; npm run check` -> 0 errors.
- `.dev\toolchains\go\bin\go.exe test ./internal/server/... -count=1` -> `ok` (routes file is untouched).
- `git grep -n -i "Open guided analysis" -- tools/e2e/specs` -> no output.
- Main e2e on a freshly built branch exe (recipe: WP-58 Changes step 5): `cd tools/e2e; powershell -File run-e2e.ps1 -Exe <exe>` -> exit 0.
- Archive e2e: `run-e2e.ps1 -Archive -Exe <exe>` -> 80 tests pass when run after WP-62c and BEFORE WP-64 (the entry button still exists); that run is a step of WP-63 and its result is recorded in `progress.md` ("Archive baseline before WP-64"). After WP-64 the archive cannot open Guided (open owner decision, plan.md §10.4); do not treat that as a regression of this phase.
- `git ls-files -- "*.unravis.db"` -> no output; `Get-ChildItem -Recurse -Force -Filter *.unravis.db -ErrorAction SilentlyContinue | Where-Object { $_.FullName -notmatch 'node_modules|\\.forge\\' }` -> no output.

## Additions to plan.md section 7

- Browser (in-app browser, seeded demo case, 1280 px and 375 px, light and dark, once keyboard only): case home shows the Investigation Timeline section (heading "Investigation Timeline", outlined button "Open the Investigation Timeline", from WP-16b) in the place after Analysis, no Guided section, the Open the analysis section unchanged; the button opens the tracing screen; click one bar with events in the tracing chart (wireframes 08, 08a) and the analysis view opens with that bar's slot as the primary window and an equal-length reference window before it; click an empty bar: the plain sentence shows and nothing opens; Back from the analysis returns to the case home (the tracing screen is not restored; `tracingFor` is case-home state and the analysis is a separate screen). Check the console and network: opening the case home makes no `/investigation`, `/views`, `/checklist` or `/report` call.
- Archived case home (read-only): no Investigation Timeline or analysis sections (they sit inside the `offersWork` guard, as the Guided section did).
- Security canaries: no new route, no new API; `routes.go` has no diff; the four guided routes still answer (guard test in WP-63). The hand-off passes only two ISO timestamps; assert no case id, query string or source name is put in the URL hash.
- Performance: no new request on case home; `distChunks` shows no static chunk cycle (INV-6); record the bundle size delta (expected near zero, one small `handoff` module). No `<15 ms`/`<100 ms` assertions (INV-5).
- Clumsy-user review: double click on a bar gives one hand-off and one analysis screen; an empty bar or a bar of a hidden lane does nothing and says why; opening the timeline on a case with no sources says why instead of a blank screen (behaviour of WP-16b); reload on the analysis screen after a hand-off keeps the window.

## Commit (one commit for the phase, after the section 7 stop; never push)

- `docs/guided-reuse-table.md`, `docs/guided-unlisted.md`
- `web/src/lib/tracing/handoff.ts`, `web/src/components/TracingPaper.svelte`
- `web/src/lib/components/CaseHome.svelte`
- `web/src/lib/components/guided/GuidedAnalysis.svelte`, `web/src/lib/guided/guided.svelte.ts` (header comments only)
- `web/tests/tracingHandoff.test.cjs`, `web/tests/guidedKept.test.cjs`, `web/tests/startTracing.test.cjs`, `web/tests/p3.fix.test.cjs`
- `tools/e2e/playwright.archive.config.ts`, `tools/e2e/run-e2e.ps1`, `tools/e2e/package.json`, `tools/e2e/COVERAGE.md`, `tools/e2e/support/ui.ts`
- `tools/e2e/archive/guided/` (all files: 5 moved specs, `exports-guided.spec.ts`, `keeper-update-guided.spec.ts`, `support.ts`, `README.md`)
- `tools/e2e/specs/exports.spec.ts`, `tools/e2e/specs/keeper-update.spec.ts` and the five deleted-by-move paths under `tools/e2e/specs/`
