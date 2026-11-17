# TSK-06 Dialog shell
Risk: high (a11y and keyboard; stray Enter could submit). Worker size: strong. Reason: native dialog, focus and key handling across browsers.

## Outcome
`web/src/components/blackboxWizard/WizardDialog.svelte` per contract C-4, with the leave layer.

## Write scope
- `web/src/components/blackboxWizard/WizardDialog.svelte` (new)
- `web/src/lib/wizardKeys.ts` (new: pure `enterDoesPrimary(target)` and `escapeAction(busy, finished)`)
- `web/tests/blackbox.wizard.keys.test.cjs` (new)

## Must not change
`Drawer.svelte`, `ImportPopup.svelte` (borrow patterns only).

## Changes
- `showModal()` on open, `close()` on leave; the `cancel` event is prevented and opens the leave layer (`role="alertdialog"`, "Keep going" focused, "Leave").
- Esc while busy does nothing and shows a short note why.
- Enter does the primary action unless the target is a button, summary, textarea, or marked `data-own-enter`.
- `focusHeading()` exported for the wizard.
- Mobile ≤ 640 px full screen; `prefers-reduced-motion` respected.
- Labels come in as props (no copy import), so it can be reused for BL-131.

## Proof
`cd web && node --test tests/blackbox.wizard.keys.test.cjs && npm run check`.

## Depends on
None.
