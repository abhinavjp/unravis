# TSK-59 golden-regen

risk: high — regenerating goldens can hide a real change in an existing suspect; the diff must be checked line by line against the allowed list.

## Outcome

The five goldens in `testdata/investigate/` match the Phase 07 engine, and the diff shows only the changes this phase means to make. `TestGolden` passes again.

## Write scope

- `testdata/investigate/*.golden.json` (only through `-update`)
- `.forge/charts-engine-hardening/phases/07-catalogue-nine/progress.md` (the diff summary)

## Must not change

- Any Go file, any test, any other testdata.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Run the golden test without the flag first and keep the failure output: `.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "^TestGolden$"`. Expected: FAIL (Version, thresholds, new suspects).
2. Regenerate: `.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "^TestGolden$" -update`. If the flag has another name, read `golden_test.go` for it.
3. `git diff --stat testdata/investigate/` and then `git diff testdata/investigate/` file by file. **Allowed** changes only:
   - `version`, any fingerprint/hash of the threshold table, and the six new threshold entries (`burstMin5`, `burstShare`, `burstMinRequests`, `burstRise`, `filterMin`, `filterRise`);
   - nine new suspect objects (rules for their colours in step 4) (`S8`–`S12`, `D16`, `D17`, `M11`, `M12`) and, where the file lists the catalogue, their entries including `"cap": "orange"` on S9;
   - the order or rank index of existing suspects moving to make room for the new ones;
   - new signal keys under the flags map (`queue_burst`, `payload_up`, `filter_reject`, `app_pool_fail`, `recv_timeout`, `drain`);
   - S1 gaining one evidence row for `queue_burst` (role `expect`), with S1's result, colour and why unchanged;
   - readiness or step text whose only change is a count of suspects (e.g. "15" → "17" SQL suspects).
4. **Anything else stops the task (BLOCKED)**: an existing suspect's `result`, `colour` or `why` changing; the headline, origin or confidence changing; any of S9, S10, S11, D16, D17, M11, M12 not Grey (the goldens carry no HTTPERR, sub-status or lane data). S8 (audit `P`) and S12 (its `o_large` route) can read from golden data: a non-grey S8 or S12 is allowed **only** if no existing suspect, headline or origin moved; list each such case in `progress.md` ("S12 Consistent/Orange in sql-pool-exhausted") for the owner review. In that case run `git checkout -- testdata/investigate/` to restore the old goldens, paste the unexpected hunks into `progress.md` under "Not fixed", and stop.
5. Write in `progress.md` under Evidence: one line per golden with the count of changed lines and "allowed only".
6. `.dev\toolchains\go\bin\go.exe test ./internal/investigate/...` (no skip) must PASS.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate/...
git diff --stat testdata/investigate/
```

PASS, and the diff summary in `progress.md`.

## Depends on

58 (every engine change of the phase is in).
