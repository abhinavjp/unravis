# TSK-14 lane-clock-margin

risk: medium — widens the "together" window for pairs that involve a lane; existing app-tier pairs must be unchanged.

## Outcome

`pairSep` uses `LaneClockToleranceMs` (5 s) instead of `ClockToleranceMs` (1 s) when either tier is a lane (`sql`, `mongo`, `host`). The clock note mentions lanes when a lane tier is dated.

## Write scope

- `internal/investigate/origin.go` (`pairSep`, `ComputeOrigin`)
- `internal/investigate/evaluate.go` (`colour`, clock note)
- `internal/investigate/evaluate_test.go` (append)

## Must not change

- App-tier pairs: any pair with no lane tier gives exactly the old separation.
- Thresholds. Goldens. The existing 30 s-offset test (still gives None) and the clock note still containing "1 s".
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `pairSep(th, a, b)`: add a local helper `isLane(name string) bool` returning true for `"sql"`, `"mongo"`, `"host"` (use the tier-name constants if they exist: grep `tierSQL`, `tierMongo`, `tierHost` in `origin.go`; otherwise the string literals). When `isLane(a) || isLane(b)`, use `th.LaneClockToleranceMs` in place of `th.ClockToleranceMs` in the same expression; otherwise unchanged.
2. `ComputeOrigin` and `colour`: replace `pairSep(th, "", "")` (from TSK-08) with the real pair. `ComputeOrigin` compares the earliest tier with each other tier, so call `pairSep(th, earliestName, otherName)` inside that comparison loop (move the single `sep :=` into the loop). In `colour`, track the earliest tier's name the same way (it already finds the earliest at ~318-323; keep its name in a local) and use it.
3. Clock note: find where the note text is produced (it contains "1 s"). When a lane tier is dated (any tier with a time and `isLane`), append one sentence: `Lane clocks (SQL, Mongo, host) are read at ±5 s.` Do not remove the existing text.
4. Tests, append: `TestLaneAndAppTierThreeSecondsApartAreTogether` — copy the setup of `TestNoClearOriginWhenTiersMoveTogether`, use a lane tier and an app tier whose onsets are 3 s apart; assert no named origin. `TestTwoAppTiersThreeSecondsApartStayOrdered` — two app tiers 3 s apart still resolve exactly as they did (assert the same result as the old behaviour: separable only if over the old margin; if 3 s is under the old bucket limit, use the existing test's offset instead).
5. Run the whole suite; goldens must stay unchanged.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/...
```

PASS, goldens unchanged.

## Depends on

TSK-08, TSK-13.
