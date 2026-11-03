# TSK-13 persist-wire

risk: medium — can remove an onset from the earliest-tier race for every metric.

## Outcome

An onset that does not stay up (share of later minutes above the limit < `OriginPersistShare`) is dropped: the tier is reported as not tested with the reason "moved briefly but did not stay up". Lane onsets coming from the server are unchanged.

## Write scope

- `internal/investigate/facts_parts.go` (`metricOnset`, `loadOnset` only)
- `internal/investigate/facts_parts_test.go` (append)

## Must not change

- Lane onsets (`p.in.LaneOnsets`). Thresholds. Goldens (unchanged — if one changes, BLOCKED).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `metricOnset` (~532-555) and `loadOnset` (~557-584), at the point where an onset time is about to be returned: compute `share := persistShare(bins, i, hit)` where `i` is the index of the first minute of the qualifying run and `hit` is the same predicate that found the run. If `share < p.th.OriginPersistShare` (use `-floatEps` tolerance: `share < p.th.OriginPersistShare-floatEps`), return a nil onset, the reason text `moved briefly but did not stay up`, and `tested = false` — use the exact return shape the function already uses for "no onset found" so callers need no change.
2. Tests, append: `TestOnsetDroppedWhenItDoesNotPersist` — build bins where five minutes exceed the limit then the next ten fall back below it; assert nil onset. `TestOnsetKeptWhenItPersists` — five minutes exceed and the rest stay above; assert an onset. Use the same bin-building style as the existing `metricOnset` tests.
3. Run the whole suite. If an existing test fails because its fixture spikes briefly, extend the fixture's elevated stretch in that test only and list it in `progress.md`. A golden change → BLOCKED.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/...
```

PASS, goldens unchanged.

## Depends on

TSK-12.
