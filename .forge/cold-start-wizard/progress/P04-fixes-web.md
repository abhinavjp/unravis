# P04 web fixes (F1, F3, F4, F5 client, F6, F8, F9, F10)

Result: `npm test` 1180/1180 pass (the 11 `bug:` tests are now `fixed:`), `npm run check` 0 errors, `npm run build` ok. No Go files touched, no wizard/Sources/discover files touched, nothing committed.

## What changed

- F1: `MasterKeyUnlock.svelte` (`submit`) and `EnterCase.svelte` (`openByCode`, `restore`) set `busy`/`codeBusy = false` before `await tick()` and `focus()`, so the field is enabled when focus is set.
- F3: `copy/masterKeyCopy.ts` `UnlockFailure` kinds are now wrong-key, throttled, network, not-set, conflict, locked, server, other, with `UNLOCK_FAILURE_TEXT`. `masterKey.ts` `unlockFailureOf`: wrong-key only for 401 + reason `wrong-key`; 429 -> "Too many tries. Try again in N seconds."; status 0 -> "could not be reached"; 409 not-set / other 409; 423; 5xx; anything else is "could not be checked". New texts are in the copy lint entries. Updated `masterkey.test.cjs` (the "one message" test became "only a refused key says not the master key", plus a new kind-mapping test; the wait, no-which-part-wrong and wrong-key-keeps-session tests were adjusted).
- F4: `appState.svelte.ts` `lockCases` has a `locking` re-entry guard, returns if already on the unlock screen, and sets `unlockOrigin` after the await only if the screen is not already `unlock` (a 423 elsewhere may have set it).
- F5/F6 client: `masterKey.ts` `visibleCount` (NFC, then drop Z*, Cc, Cf and blank filler letters U+115F/1160/2800/3164/FFA0). `setupProblem` length rule and the strength hint use it; the byte limit is checked on the NFC form. Note this is stricter than the server (12 runes total): a key with 12 characters of which some are spaces is "too short" in the form. Mismatch still compares the raw strings.
- F8: `caseCode.ts` `IGNORED` now uses `\p{Cf}` (covers bidi controls, zero-width, soft hyphen, BOM, U+061C) plus whitespace and dashes.
- F9: `restoreCase.ts` `normalisePath` strips `\p{Cf}` (also inside the quotes).
- F10: `restoreCase.ts` `MAX_PATH_BYTES = 520`, counted in UTF-8 bytes (the server compares `len(string)`), new message `ENTER_TEXT.pathTooLong`. A server 400 `bad-request` now maps to a new `bad-request` failure kind with `ENTER_TEXT.badRequest` (focus on the path). `casefile.test.cjs` updated: 2100-char path now gives `pathTooLong`, plus 520/521-byte edge cases.

## Notes for the Go side / others

- Unlock failures with an unknown reason (for example the new F2 damaged-key-file reason) fall to "The key could not be checked. Try again." (401 non-wrong-key, or 4xx) or the server-problem text (5xx). Once the Go agent fixes the status/reason for F2, add a dedicated message in `unlockFailureOf` and `UNLOCK_FAILURE_TEXT`.
- Server F5/F7/F11 and F2 are not handled here.

## Follow-up: damaged master key file, wired into the web UI
Status: DONE. `npm test` 1195/1195, `npm run check` 0 errors, `npm run build` ok. Nothing committed; no Go, wizard, Sources or discover files touched.

- State `damaged` added to `LockState`/`parseStatus`; `closesCases(state)` is true for locked and damaged. `initialScreen`, `startView`, `StartScreen` and `chooseFirstScreen` treat damaged like locked (no case data shown). `noteLocked` never overwrites `damaged` with `locked`.
- New `components/MasterKeyDamaged.svelte` (shown by `Landing` on the unlock screen when `app.lockState === 'damaged'`): plain-language explanation, "nothing was changed", DPAPI cases unaffected, warning that cases protected by the old key stay unreadable, a checkbox, and "Start over with a new master key" (disabled until ticked; `submit` also guards, and `resetDamagedKey(false)` throws before any fetch). POST `/api/master-key/reset-damaged {acknowledged:true}`. Done phase shows `keptAs` (validated plain name only, never a path) and "Set a new master key", which calls `finishReset(status)` (unlock -> start -> setup, since unlock -> setup stays forbidden). Focus: h2 on mount and on done; checkbox after a failure (busy cleared first); errors in `role="alert"`.
- `MasterKeyUnlock` checks status once on mount and swaps to the damaged screen if damaged; a 409 `key-file-damaged` on unlock does the same. `MasterKeySetup` does the same for a 409 on set-up (goes to unlock, which shows the damaged screen).
- Mappings: unlock/setup 409 `key-file-damaged` -> damaged text; setup 400 `blank` and `invalid-characters` -> their own text; restore 400 `path-too-long` -> `ENTER_TEXT.pathTooLong` (focus path); restore 423 while damaged -> `ENTER_TEXT.damaged` (`restoreView(failure, app.lockState)`); reset failures (not-damaged, ack-required, reset-failed, unavailable, network, other) each have text.
- Client rules now mirror the server: total >= 12 characters (NFC) AND >= 8 visible (same blank list as `masterkey.Visible`), lone surrogates and U+FFFD refused before sending. This replaces my earlier "12 visible" rule; existing tests still pass.
- Tests: new `web/tests/damagedkey.test.cjs` (15 tests: status, routing, each mapping, confirm gate with `calls.length === 0` and exactly one POST, `keptAs` validation, screen source checks, copy lint). `startscreen.test.cjs` regex updated to `closesCases(lock)`.
