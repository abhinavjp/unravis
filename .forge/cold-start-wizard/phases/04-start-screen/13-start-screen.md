# TSK-13: Three-card start screen
risk: medium — first-screen replacement.

## Outcome
`StartScreen.svelte` renders the three cards from the signed-off wireframe (R1): "Use an existing case" (lists cases with title, created date, import and source counts), "Enter an existing case" (code field, TSK-14), "Create a new one". All three are always shown, even with zero or one case. Terminal theme with IBM Plex, ELI10 hints through the existing `HelpNote`/`Hint` components.

## Write scope
- `web/src/components/StartScreen.svelte` (new), `web/src/components/Landing.svelte` (use it for the "start" mode)
- `web/src/lib/copy/` (start-screen wording)
- `web/tests/startscreen.test.cjs` (new; logic helpers only — no DOM mounting exists in this repo)

## Must not change
- The Landing modes `loading`, `connect`, `running` and their behaviour; the Terminal theme tokens; existing case-list API usage.

## Changes
0. Lock state: read `GET /api/master-key/status` first. `locked` shows the unlock screen (TSK-16) instead of the case list and shows no case titles or counts; `none` and `unlocked` show the cards. A small "Protect your cases with a master key" link shows when the state is `none`.
1. Pure helpers in `web/src/lib/startScreen.ts` (card model, case-row summary text, empty-state text) so they are testable under node:test.
2. Component binds the helpers; "Create a new one" calls an `onCreate` prop (wired in TSK-15).

## Proof
`cd web; npm test; npm run check; npm run build` green; helper tests: zero cases, one case, 500 cases (list is capped with "show more"), long titles truncated, closed case labelled, locked state hides titles and counts; browser pane screenshots at 1280 and 375 width show all three cards.

## Depends on
TSK-05, TSK-06, TSK-11
