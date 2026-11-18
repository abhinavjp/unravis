# TSK-12 Extract the target picker
Risk: high (Apply settings must not regress; UX-7 focus logic moves). Worker size: strong. Reason: refactor of a shared, stateful component with focus management.

## Outcome
`web/src/components/BlackboxTargetPicker.svelte` holds the whole target picker now inside `BlackboxSettings.svelte` (fetch, filters, group and list states, chips with notes, manual add, UX-7 focus on chip removal, aria-live). `BlackboxSettings.svelte` uses it. Apply looks and works the same.

## Write scope
- `web/src/components/BlackboxTargetPicker.svelte` (new)
- `web/src/components/BlackboxSettings.svelte`
- `web/tests/blackbox.targets.test.cjs`, `web/tests/blackbox.targets.add.test.cjs` (only if a moved helper path changes)

## Must not change
`web/src/lib/blackbox.ts` target rules, the Apply payload, `BlackboxWizard.svelte`.

## Changes
- Props: `bind:targets`, `mode: 'pc' | 'pack'` (pack = typed only, kind picker), `idPrefix`, `problem`.
- Fetch `blackboxApi.targets()` only in pc mode, as today.
- Keep ids `#{idPrefix}-f-targets` so existing field-error focus still works.

## Proof
`cd web && npm test && npm run check && npm run build`. Manual: open Apply settings on this PC, add and remove a target by keyboard, focus lands as before.

## Depends on
None.
