# TSK-24 histogram-pin-test

risk: low — a test only.

## Outcome

A seeded test proves the merged-histogram P95 is within 10% of the exact value, including after a split-and-merge.

## Write scope

- `internal/audit/histogram_test.go` (append)

## Must not change

- Any non-test file. Any existing test. `internal/store/pipeline_test.go` (the `$percentile` ban).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Read `histogram_test.go` first: copy the helper usage exactly — `newHistogramAccumulator()`, `h.add(BinIndex(ms), 1, ms, ms, ms)`, `h.stats()` (and the merge function `merge` in `overview.go:276`, if the tests call it directly; copy how an existing test does).
2. Append `TestHistogramP95WithinTenPercentOfExact`:
   - `r := rand.New(rand.NewSource(42))`; generate 2000 durations `ms := math.Exp(r.NormFloat64()*0.8 + 4)` (a log-normal spread around ~55 ms), as float64.
   - exact P95 by nearest-rank: copy the slice, sort ascending, take index `int(math.Ceil(0.95*float64(n))) - 1`.
   - histogram: add every value to one accumulator with `h.add(BinIndex(ms), 1, ms, ms, ms)`, read the P95 `ValueMs` from `h.stats()` (field name per the existing tests).
   - assert `math.Abs(got-exact)/exact <= 0.10`; on failure print `got`, `exact`.
3. Append `TestHistogramP95SurvivesMerge`: same data split into two halves (even and odd indexes), each into its own accumulator, then merged with the same merge call the production code uses; assert the merged P95 equals (to `1e-9`) the P95 of the single accumulator holding all values.
4. If the first test fails because the histogram is further than 10% off, do NOT loosen the bound or change production code: BLOCKED, paste got/exact into `progress.md`.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/audit -run "TestHistogramP95"
```

PASS, 2 tests.

## Depends on

None.
