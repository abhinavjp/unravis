# TSK-14: Enter an existing case by its code or case file
risk: medium — input from the user decides which encrypted case opens or is created.

## Outcome
The second card takes the 26-character case code, trims and lower-cases it, checks `^[a-z2-7]{26}$` locally, and opens the case if it exists on this PC (R2). A malformed code, an unknown code and a closed/erased case all show the same message, so the screen never reveals which case ids exist. The same card has "Restore from a case file": the user picks the path of an exported file, types the master key it was exported with, and the app calls `POST /api/cases/restore` (TSK-12) and opens the **new** case it creates (R3). The card says in plain words that restoring always makes a copy and never changes an existing case. A bad file, bad key or damaged file all show one message; restore needs the master key set and unlocked on this PC, otherwise the card offers set-up (TSK-16).

## Write scope
- `web/src/lib/caseCode.ts` (new), `web/src/components/StartScreen.svelte` (the code card), `web/tests/caseCode.test.cjs` (new)
- `web/src/lib/cases.ts` (open-by-id and restore helpers only if they do not exist)

## Must not change
- The server's case-id validation and uniform 404 behaviour; no new server route (restore uses TSK-12's route). The restore passphrase is never kept after the call (I5).

## Changes
1. `normaliseCode(input)` strips spaces, dashes and case, returns the 26-char code or `null`.
2. On submit: `null` → inline message; otherwise open-by-id; any failure (404, 400, network) → the same message "That code does not match a case on this computer."

## Proof
Tests: valid code; uppercase; pasted with spaces/dashes; 25 and 27 chars; non-base32 characters; empty; code of an erased case → same message as unknown; the message string is one constant used on every failure path; restore: empty path, empty key, wrong key, locked state and success each map to the right screen state, and the failure text is one constant. `cd web; npm test; npm run check` green.

## Depends on
TSK-12, TSK-13
