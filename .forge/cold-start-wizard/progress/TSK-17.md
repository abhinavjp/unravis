# TSK-17 - create-wizard shell

Status: done. `npm test` (1151 pass), `npm run check` (0 errors, 0 warnings) and `npm run build` pass in `web/`. Nothing committed. No browser run (no dev server, as asked).

## Files

New:
- `web/src/lib/wizardEnvs.ts` - pure parser for the free-text environments field.
- `web/src/lib/wizardFlow.ts` - pure step and name rules (`StepId`, `canEnter`, `moveToStep`, `stepViews`, `isDirty`, `titleProblem`, `DraftSource`).
- `web/src/lib/wizard.svelte.ts` - the in-memory draft (`wizard`, `nextStep`, `backStep`, `goToStep`, `setSources`, `draftSnapshot`, `resetWizard`).
- `web/src/lib/copy/wizardCopy.ts` - all fixed sentences and small text builders, plus `wizardLintEntries()`.
- `web/src/components/CreateWizard.svelte` - the shell.
- `web/tests/wizardEnvs.test.cjs` (22 tests), `web/tests/wizard.test.cjs` (34 tests: flow rules, draft with the runes stand-in, copy lint, source checks on the component and wiring).

Edited (wiring only):
- `web/src/components/Landing.svelte` - new `mode="create"` renders `<CreateWizard onLeave={goHome} />`.
- `web/src/App.svelte` - `app.screen === 'create'` renders `Landing mode="create"`.
- `web/src/lib/copy/helpCopy.ts` - help keys `wizard` and `wizard-envs`.
- `web/tests/help.test.cjs` - the two keys added to the screen list and component map.
All new and edited files are CRLF.

## What TSK-18 and TSK-19 consume

`CreateWizard` props: `sources?: Snippet` (step 2 body, TSK-18), `review?: Snippet` (step 3 body, TSK-19), `finish?: Snippet` (the last step's main button, drawn where Next is, TSK-19), `busy?: boolean` (freezes steps, Back, Next, Leave and the resume link while the case is being made; also keeps the beforeunload warning on), `onLeave: () => void`. Without a snippet the step shows a short "not ready yet" line (`WIZARD_TEXT.sourcesSoon` / `reviewSoon`). Mount them in `Landing.svelte`'s `create` branch.

`lib/wizard.svelte.ts`:
- `wizard` (`$state`: `step`, `title` as typed, `envText` as typed, `sources: DraftSource[]`). Mutate fields or call `setSources(list)` (copies the list). Never reassign `wizard`.
- `draftSnapshot()` returns `{ title (cleaned), envs (parsed, bounded), sources (copies) }` for the create call.
- `draftEnvs()` gives the parsed list plus what was dropped. `resetWizard()`: call after the case is created. `goToStep`, `nextStep`, `backStep` return whether the move happened.
- `DraftSource { kind, ref, label, env, portal, api, classification }` (in `wizardFlow.ts`): the per-source tags are folded in here. Blank tags mean "not named yet". No secrets in it.
- The footer shows `chosenText(wizard.sources.length)`, so TSK-18 only needs to keep `wizard.sources` current.

Rules the shell enforces: forward moves need a valid name (2-80 characters, no control/format characters); Sources may be skipped; Back is always allowed; the environments field is optional, split on commas or line breaks, at most 32 names of 60 characters, duplicates ignored by case.

## Deviations

- `WIZARD_READY` in `screenFlow.ts` is NOT flipped (`uxnav.test.cjs` asserts it is false), so the wizard is unreachable in the running app until TSK-19 or the end of phase 05 flips it.
- `App.svelte` and the help layer (`helpCopy.ts`, `help.test.cjs`) were edited; the packet did not name them but the route and the HelpNote requirement need them.
- Environment names over 60 characters are shortened and reported, not dropped. A `›` in a name becomes `-` and is reported (the sign separates environment, portal and API on screen).
- A draft survives the header Home button. On return, a notice says so and offers "Start over". Only the in-page Leave button asks for confirmation and clears the draft.
- Fix found while testing: `isDirty` treated an environments field of only commas and spaces as dirty; now it is not.

## Concerns

- No visual or keyboard check in a real browser was done; a11y is covered by source checks and svelte-check only.
- The header Home button leaves without asking (the draft is kept, so nothing is lost until a reload; `beforeunload` warns on reload).
- The same environment list is held as free text in the draft; TSK-18 must offer these names (or free text) for each source's `env` tag and decide what happens to a source whose env is later removed from the list.
