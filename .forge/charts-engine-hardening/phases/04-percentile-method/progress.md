# Progress: Phase 04 percentile-method

## Done

- TSK-24 DONE: `TestHistogramP95WithinTenPercentOfExact` and `TestHistogramP95SurvivesMerge` appended to `internal/audit/histogram_test.go` (seed 42, 2000 log-normal values; imports `math/rand`, `sort` added). No bound loosened, no production code touched.
- TSK-25 DONE: one `<li>` added after the three TSK-22 items, before "Results found".

## Evidence

- `go.exe test ./internal/audit -run "TestHistogramP95"` PASS (2 tests); `go.exe test ./internal/audit` PASS.
- TSK-25 grep `within about 10% of the exact value`: 1 hit.

## Not fixed

(none)

## UNMEASURED

(none for this phase; the wireframe flow check is recorded in Phase 03)
