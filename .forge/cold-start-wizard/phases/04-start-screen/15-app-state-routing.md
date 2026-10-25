# TSK-15: App-state routing for start, create and running
risk: low — state wiring, covered by existing session tests.

## Outcome
`appState.svelte.ts` gains a `startMode` flow: unlock (when the master key is locked) → start screen → wizard (phase 05) or open case → running; "close case" and the header's home action return to the start screen, and a 423 from any case route returns to unlock. Until phase 05 lands, "Create a new one" opens the existing import panel.

## Write scope
- `web/src/lib/appState.svelte.ts`, `web/src/components/Landing.svelte` (wiring only), `web/src/components/AppHeader.svelte` (home/back action only)
- `web/tests/session.test.cjs`, `web/tests/uxnav.test.cjs` (extend)

## Must not change
- Drawer history, filter state, zoom state and the investigation ledger; deep links already supported; the demo-mode flow on port 18770.

## Changes
1. Add a small reducer-style `go(screen)` with legal transitions; illegal transitions are ignored and logged to the console in dev only.
2. Header home action returns to the start screen without closing the case unless the user chooses to.

## Proof
Existing `session`/`uxnav` tests still pass; new tests cover each legal transition and one illegal one. `cd web; npm test; npm run check` green.

## Depends on
TSK-13
