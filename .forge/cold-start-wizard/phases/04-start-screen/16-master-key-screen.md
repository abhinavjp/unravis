# TSK-16: Master-key set-up, unlock and lock screens
risk: medium — the screen where the user can permanently lose access if they do not understand the warning.

## Outcome
Three small screens, all in plain words (R15):
- **Set up** (shown from the start screen as "Protect your cases with a master key", never forced): type the key twice, see a strength hint, and tick "I understand that if I forget this key, my cases cannot be recovered." The button stays disabled until the key is at least 12 characters, both entries match and the box is ticked. The screen offers to re-wrap existing cases afterwards, with a count.
- **Unlock** (shown instead of the case list while the status is `locked`): one field, one button, one identical message for every failure ("That is not the master key for this PC."), and the throttling message (TSK-11) when it applies. Enter submits; the field is cleared after every attempt and never autofilled by the browser (`autocomplete="off"`, type password).
- **Lock** action in the header: locks the keyring and returns to the unlock screen.

## Write scope
- `web/src/components/MasterKeySetup.svelte`, `MasterKeyUnlock.svelte` (new)
- `web/src/lib/masterKey.ts` (pure helpers: strength/validation text, state model), `web/src/lib/masterKeyApi.ts` (the five calls; kept apart from `api.ts` to avoid a clash with TSK-05)
- `web/src/lib/copy/` (wording), `web/tests/masterkey.test.cjs` (new; helpers only, no DOM mounting exists in this repo)
- `web/src/components/AppHeader.svelte` (lock action only; shared with TSK-15, serialise)

## Must not change
- The Terminal theme tokens, filters and drill-down design; existing case-list behaviour when the state is `none` or `unlocked`.
- The passphrase never goes into component state longer than the submit, localStorage, sessionStorage, the URL, the console, or error text (I5).

## Changes
1. `masterKey.ts` exports `canSubmitSetup(pass, confirm, ack)`, `unlockMessage(result)` (one constant for every failure), `strengthHint(pass)` (length and variety only, never a claim of safety), and the status type `none | locked | unlocked`.
2. Calls go through `masterKeyApi.ts` with the existing token and Origin handling; a 423 from any case route routes the app to the unlock screen.
3. Copy follows the ELI10 layer: explains what the key protects, that it is not sent anywhere, that it is asked once per launch, and that nobody can recover it.

## Proof
Tests: submit gate (11 chars, mismatch, unticked, all good); one failure message for wrong key, throttled and network error variants (throttled adds only a wait time); strength hint never says "secure"; the passphrase is absent from any serialised state in a state-snapshot test. `cd web; npm test; npm run check; npm run build` green. Browser pane pass (desktop 1280, mobile 375): set-up, lock, unlock with a wrong then right key, Enter key, back button mid-set-up.

## Depends on
TSK-11, TSK-13
