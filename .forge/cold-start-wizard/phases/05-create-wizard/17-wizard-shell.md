# TSK-17: Wizard shell, case name and environments
risk: medium — step navigation and draft state.

## Outcome
`CreateWizard.svelte` provides the step frame (name → sources → review) with back/next, progress, and a draft that survives step changes. Step 1 collects the case name and an optional comma-separated list of environment names (R4): free text such as "Client A production, Client D sandbox", blank allowed, no built-in Production/Staging, trimmed, case-insensitively de-duplicated, capped to the server bounds.

## Write scope
- `web/src/components/CreateWizard.svelte` (new), `web/src/lib/wizard.svelte.ts` (new draft state), `web/src/lib/wizardEnvs.ts` (new pure parser)
- `web/src/lib/copy/` (wizard wording), `web/tests/wizardEnvs.test.cjs`, `web/tests/wizard.test.cjs` (new)
- `web/src/components/Landing.svelte` (mount point only)

## Must not change
Existing ImportPanel and its API calls; the Terminal theme tokens.

## Changes
1. `parseEnvs(text)` returns `{names, dropped}`: split on commas and newlines, trim, collapse inner whitespace, drop empties and duplicates (case-insensitive), cap at 32 names and 60 chars each, report what was dropped so the UI can say so.
2. Draft state holds name, envs, selected sources and tags; cancelling asks before discarding; nothing touches the server until TSK-19.

## Proof
Tests for `parseEnvs`: blank, one name, "A production, A production", mixed case duplicates, 40 names, 80-char name, commas only, names containing "›". Draft test: back/next preserves entries. `cd web; npm test; npm run check` green.

## Depends on
TSK-07, TSK-15
