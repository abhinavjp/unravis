# TSK-03 spread-rule

risk: low — one pure function with a table test.

## Outcome

`spreadOK(counts []int, th Thresholds) bool` exists in a new file `internal/investigate/gate.go` and is proven by `gate_test.go`.

## Write scope

- `internal/investigate/gate.go` (create)
- `internal/investigate/gate_test.go` (create)

## Must not change

- Every other file. No call site yet (TSK-05 wires it).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

Both files: `package investigate`. `floatEps` already exists in `stats.go` (const, 1e-9); use it, do not redefine.

`gate.go`: one doc comment explaining "events spread over the covered reference minutes; a reference that is one burst cannot stand for normal". Then:

`func spreadOK(counts []int, th Thresholds) bool` — `counts` is events per COVERED reference minute (one entry per minute, zeros included). Rules, in this order:

1. `len(counts) == 0` → false.
2. `total` = sum, `n` = len. `mean = float64(total)/float64(n)`.
3. If `mean < th.GateSpreadSkipBelowPerMin - floatEps` → true (rule skipped: at a trickle, empty minutes are expected).
4. `withEvent` = number of entries > 0. If `float64(withEvent)/float64(n) >= th.GateSpreadMinShare - floatEps` → true.
5. `max` = largest entry. If `total > 0 && float64(max)/float64(total) <= th.GateSpreadMaxMinuteShare + floatEps` → true.
6. Otherwise false.

`gate_test.go`: one table test `TestSpreadOK` with `th := Current()`, rows `{name string; counts []int; want bool}`:

- nil → false
- `[0,0,0,0]` → true (mean 0, skipped)
- `[1,0,1,0]` → true (mean 0.5, skipped)
- `[3,3,3,3]` → true
- nine 0s then `20` (ten entries) → false
- `[0,0,0,0,10,10,10,10]` → true (half the minutes hold events)
- ten entries: four of them `5`, six of them `0` → true (share 0.4 fails rule 4, but max/total = 0.25 passes rule 5)
- `[4,0,0,0]` → false (mean exactly 1 is NOT skipped; share 0.25; max/total 1)

Run each row as `t.Run(name, ...)`; fail with `got/want` text.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run TestSpreadOK
```

PASS, 8 subtests.

## Depends on

TSK-01 (needs the three `GateSpread*` fields in `Thresholds`).
