# TSK-23 flow-check-run

risk: low — a check, no edits.

## Outcome

The wireframe flow check result is recorded: PASS, or UNMEASURED with the reason.

## Write scope

- `.forge/charts-engine-hardening/phases/03-prototype-alignment/progress.md` only.

## Must not change

- Any other file. If the check fails, do not fix the wireframe here: record the failing line and stop (BLOCKED).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Run `node prototype/flow-check.cjs` from the repo root.
2. If it prints all checks OK, write `flow-check: PASS` in `progress.md` Evidence with the last output line.
3. If it fails because Playwright is not installed (module not found), write it under UNMEASURED with the exact error line. Do not install anything.
4. If a check fails for another reason, paste the failing check and its message into `progress.md` and stop.

## Proof

`progress.md` has one of: a PASS line, an UNMEASURED line with the reason, or a BLOCKED entry.

## Depends on

TSK-22.
