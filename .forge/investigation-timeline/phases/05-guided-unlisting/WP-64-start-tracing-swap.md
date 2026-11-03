# WP-64 Case home: remove the Guided section
risk: medium    size: M    wave: 11    model: sonnet
## Outcome
The case home no longer offers Guided analysis: the Guided section is gone and the Investigation Timeline entry that WP-16b added stays exactly as it is, in the place after Analysis (TR-01, owner decision D-2 = A). The guided component, store, routes and copy stay in the app, unlisted. This packet adds no entry, no label and no button of its own.

## Write scope
- `web/src/lib/components/CaseHome.svelte` (delete the Guided section and the import only it used)
- `web/tests/p3.fix.test.cjs` (one assertion)
- `web/tests/startTracing.test.cjs` (new)

## Must not change
- The `Open the analysis` section (`home-analysis`, `openAnalysis`, `CASE_HOME_TEXT.analysisButton`) and every other case-home section.
- The `home-tracing` section, `home-tracing-open`, the `tracingFor` / `tracingOpen` state, the `{:else if tracingOpen}` branch, `TRACING_TEXT` and `web/src/lib/copy/tracingCopy.ts`: all owned by WP-16a/WP-16b. The entry label is "Open the Investigation Timeline" (heading "Investigation Timeline"); this packet does not rename or restyle it, and `caseHomeCopy.ts` gets no new key.
- Guided code (INV-8): `web/src/lib/guided/*`, `web/src/lib/components/guided/*`, `web/src/lib/copy/guidedCopy.ts` (`GUIDED_TEXT.openGuided` stays defined), `internal/server/routes.go`, and in `CaseHome.svelte` the `GuidedAnalysis` import and mount, the `guidedOpenFor` import, `guidedFor` and `guidedOpen`.
- `web/tests/defects1.test.cjs` (it counts exactly one `primary-button` between `home-analysis` and `home-erase`; the timeline button is `secondary-button`).
- `docs/user/*.md` (see WP-63 note) and the archived e2e specs.
- The tracing screen itself (WP-16b).

## Changes
Note: anchor on symbols, not line numbers. Today (`CaseHome.svelte`) the Guided section is `<section class="block" aria-labelledby="home-guided">` with `<h3 id="home-guided">{GUIDED_TEXT.heading}</h3>`, the note `{GUIDED_TEXT.openGuidedNote}` and the button `{GUIDED_TEXT.openGuided}` (about lines 472-476, directly after `home-analysis`); its button sets `guidedFor = caseId`; state is `guidedFor` and `guidedOpen = guidedOpenFor(guidedFor, caseId)` (lines 63-64), and the mount is `{:else if guidedOpen}` (about line 386). `GUIDED_TEXT` is imported on line 16 and used only by that section (the grep for `GUIDED_TEXT` in the file shows lines 16 and 473-475 only).
Note: WP-16b (earlier wave) already put the `home-tracing` section after `home-analysis` and before `home-guided`. Open `CaseHome.svelte` first and confirm it with `Select-String -Path web/src/lib/components/CaseHome.svelte -Pattern "home-tracing"`. If it is missing, stop and report; do not create the section here.
1. `CaseHome.svelte`: delete the whole `<section class="block" aria-labelledby="home-guided">...</section>` block (heading, note and button). Delete the `import { GUIDED_TEXT } from '../copy/guidedCopy';` line (nothing else in the file uses it). Keep the `GuidedAnalysis` import and mount, the `guidedOpenFor` import, `guidedFor` and `guidedOpen` untouched: the mount becomes unreachable from the UI, which is the intended unlisted state.
2. `web/tests/p3.fix.test.cjs` line 100: the loop is `for (const id of ['home-analysis', 'home-guided']) assert.ok(at(id) > open1 && at(id) < story, id)` where `at` is `indexOf`. Do NOT just swap the id for `'home-tracing'`: the bare literal `home-tracing-open` also appears in the `CaseHome.svelte` script (WP-16b `closeTracing` focus), which sits before the first `{#if offersWork}`, so `at('home-tracing')` would hit it and fail. Instead change the loop to `for (const id of ['home-analysis', 'home-tracing']) { const k = at('aria-labelledby="' + id + '"'); assert.ok(k > open1 && k < story, id); }` (the attribute form occurs once, in the section tag). The archived-case test still asserts the section sits inside the first `offersWork` run, before `home-story`).
3. Create `web/tests/startTracing.test.cjs` (node:test, `node:assert/strict`, `fs.readFileSync` of source files from `path.resolve(__dirname, '..')`). Tests, by exact name:
   - `the case home still offers the Investigation Timeline`: `CaseHome.svelte` matches `/aria-labelledby="home-tracing"/`, `/id="home-tracing-open"/`, `/TRACING_TEXT\.entryButton/`, and the entry button still has `class="secondary-button"` (text between `id="home-tracing"` and the next `</section>` has no `primary-button`).
   - `the case home has no Guided button`: `CaseHome.svelte` does not match `/home-guided/`, `/GUIDED_TEXT/` or `/guidedFor = caseId/`.
   - `the guided mount and its copy are still in the app`: `CaseHome.svelte` still matches `/GuidedAnalysis/` and `/guidedOpenFor/`; `src/lib/copy/guidedCopy.ts` still matches `/openGuided: 'Open guided analysis'/`.
   - `the timeline entry sits after the analysis section`: in `CaseHome.svelte` the index of `aria-labelledby="home-tracing"` is greater than the index of `aria-labelledby="home-analysis"` and smaller than the index of `aria-labelledby="home-story"`.

## Proof
- `cd web; node --test tests/startTracing.test.cjs tests/p3.fix.test.cjs tests/defects1.test.cjs tests/tracingPaper.test.cjs` -> all pass (4 new tests plus the existing ones).
- `cd web; npm test` -> exit 0, `# fail 0` (copy lint and `distChunks` included; run `npx vite build` first so `distChunks` is not skipped).
- `cd web; npm run check` -> 0 errors (no unused import left).
- `git grep -n "home-guided" -- web/src web/tests` -> no output.
- `cd tools/e2e; powershell -File run-e2e.ps1 -Exe <branch exe> -Only CASE-10` (any case-home scenario) -> passed; the main suite has no `Open guided analysis` click (`git grep -n -i "Open guided analysis" -- tools/e2e/specs` -> no output).

## Depends on
WP-61, WP-63
