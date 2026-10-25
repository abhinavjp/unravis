# TSK-14 - Enter an existing case by code or restore from a case file

Status: done. `npm test` 1095 pass / 0 fail, `npm run check` 0 errors 0 warnings (244 files), `npm run build` ok (rebuilds tracked `web/dist/`). Nothing committed or pushed.

## Files

New
- `web/src/components/EnterCase.svelte` - the card in the start screen's `enter` slot: code form (Open case) plus a collapsible "Restore from a case file" form (path, master key, "Restore as a new case").
- `web/src/lib/caseCode.ts` - `normaliseCode` (case, whitespace, dashes, zero-width chars; `^[a-z2-7]{26}$`), `resolveCode` (one message `CODE_NOT_FOUND` for malformed, unknown, erased, locked or unreachable; only an abort is rethrown).
- `web/src/lib/restoreCase.ts` - pure: `checkRestoreInput`, `restoreFailureOf`, `restoreView`, `retryAfterOf`, `restoreNeedsKey`.
- `web/src/lib/caseFileApi.ts` - `restoreCaseFile` and `exportCaseFile` (dedicated, not `cases.ts send()`, because `send()` forgets the session on any 401 and ignores Retry-After; a wrong export key is a 401 `wrong-key`).
- `web/src/lib/copy/enterCopy.ts` - all card text, `CODE_NOT_FOUND`, `RESTORE_FAILED`, `throttledText`, `enterLintEntries`.
- `web/tests/caseCode.test.cjs` (17 tests) and `web/tests/casefile.test.cjs` (17 tests).

Edited
- `web/src/components/Landing.svelte` - passes `<EnterCase>` as StartScreen's `enter` snippet.
- `web/src/lib/copy/startCopy.ts` - `enterText` reworded; `enterSoon` kept as fallback.
- `web/src/lib/copy/helpCopy.ts` - new `enter` help key (1-sentence plain, 2-sentence simple).
- `web/tests/help.test.cjs` - `enter` added to the required screens, key list and component map (`EnterCase`).

## Behaviour
- Code: normalised, then existence checked with the existing `listImports` call (no new server route). Any failure shows `That code does not match a case on this computer.` Success calls `openCase(id)`.
- Restore: validates locally (path present, full drive path, `.unravis-case`, key present), then `POST /api/cases/restore`. Copy states it always makes a new case and never changes an existing one. On success opens the new case.
- Every 422 `bad-case-file` reason (wrong key, wrong path, damaged file, anything) shows the single sentence `RESTORE_FAILED`. The only different messages are non-file ones: throttled (with the Retry-After seconds, header first then body, and the button held off for that long), locked (423, also sends the app to unlock), no master key on this PC (409, offers set-up), busy (409), unreachable, other.
- Needs a master key on this PC: when status is `none` the card shows the set-up link instead of the form.
- I5: the passphrase is a local `$state`; `const attempt = pass; pass = '';` runs before the request; it goes only in the JSON body; no URL, header, storage, console, or error text. Error messages are fixed text; the card shows model text, never `error.message`. Source-grep tests lock this.
- A11y: labelled inputs, `aria-expanded`/`aria-controls` toggle, `role="alert"` errors, Enter submits each form, focus moves to the field to fix, `autocomplete="off"` and `type="password"` for the key.

## Deviations and concerns
1. Closed cases: the packet says closed or erased should give the not-found message; here a closed case that still exists opens (only erased/unknown give the message). No closed-state check exists without a new route.
2. Export-case-file UI not added: the packet does not call for it. `exportCaseFile()` exists and is tested, ready for a later task.
3. Added an `enter` help key and edited `help.test.cjs` accordingly (the help table must match REQUIRED exactly).
4. Existence check uses `listImports`; a 423 there is mapped to the same not-found message after the global lock signal fires, which is intended.
5. Restore path accepts drive-letter absolute paths only (matches server refusal of UNC/device prefixes); no file picker (browser cannot give a real path).
6. Not exercised in a running browser (no dev servers per brief); verified via tests, svelte-check and build.
