# TSK-16 retries-colour

risk: high — changes the headline colour (Red → Orange) in one class of runs. Second reviewer required.

## Outcome

`colour` no longer turns Red on a load suspect when retries or errors came first; it gives Orange, matching the origin rule from TSK-15.

## Write scope

- `internal/investigate/evaluate.go` (`colour` and its callers)
- `internal/investigate/evaluate_test.go` (append)

## Must not change

- Colours for runs with no retry/error precedence. Goldens (unchanged; a golden change → BLOCKED). Thresholds.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `colour` (`evaluate.go` ~296) has no `Signals` parameter. Add `sig Signals` as a parameter (put it right after the `Facts` parameter), and update every caller: run `.dev\toolchains\go\bin\go.exe build ./...` and fix each reported call by passing the signals value that `Evaluate` (~121) already holds.
2. Inside `colour`, at the switch (~335) find the case that returns Red for an L-group (load) suspect. Add the condition: when `sig.SigRetriesUp == Yes || sig.SigErrorsPrecede == Yes`, return Orange instead of Red. Use the same constants and colour names as the neighbouring cases. Add a short reason string if the neighbouring cases return one (copy their shape, text: `load follows retries; not new demand`).
3. Tests, append: `TestRetriesBeforeLoadGiveOrangeNotRed` (retries on → colour Orange) and `TestLoadSuspectStillRedWithoutRetries` (retries off → colour Red as before). Reuse the scenarios from TSK-15's tests.
4. Run the whole suite; a golden change → BLOCKED.

Second reviewer: confirm the new branch sits only in the L-group Red case and that every caller of `colour` was updated.

## Proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
```

Both PASS, goldens unchanged.

## Depends on

TSK-15.
