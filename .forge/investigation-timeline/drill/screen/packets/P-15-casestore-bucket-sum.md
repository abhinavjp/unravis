# P-15 Case store bucket timings carry a sum

model: sonnet, effort: high. Reason: the timing internals are not yet understood; the packet reads first and has a fallback.
risk: medium. Review batch: RB-2. Wave: W0. Requirement: R-04 (web server lane).

## Outcome
`casestore.BucketTiming` carries `Sum`: the summed `DurationMs` of the events counted in that bucket, so the web server lane can show running at once.

## Write scope
- `internal/casestore/buckets.go`
- `internal/casestore/buckets_test.go` (existing or new)

## Must not change
- The signature of `CountBuckets` (line 161) and existing fields of `BucketTiming` (line 80: `Index, Source, ImportID, Bin, Count, Min, Max`). The case store schema (no migration, no new table or index). Other files.

## Changes
1. Read `buckets.go` fully. Write in your report how `Min` and `Max` are computed today (SQL aggregate, or Go loop over rows) and what `Bin` means.
2. Add to `BucketTiming`:
```go
	// Sum is the summed duration in milliseconds of the timed events in this bucket.
	Sum float64
```
3. Fill it in the same place `Min` and `Max` are filled: if they come from a SQL aggregate, add `SUM(<same duration column>)` over the same rows; if from a Go loop, add in the loop. Events with no duration add nothing. The read stays read-only.
4. Fallback: if timings are built from pre-binned data where a sum cannot be known, do not invent one. Leave `Sum` 0, add `HasSum bool`, set it only where the sum is exact, and say so in the report (P-19 reads `HasSum`). If timings are exact everywhere, do not add `HasSum`.
5. Tests: three events (1000, 2000, none) in one bucket → `Count` as before, `Sum` 3000; two buckets do not leak into each other; an empty bucket has `Sum` 0; existing tests still pass unchanged.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/casestore/` → no output.
- [ ] `go test ./internal/casestore/ -count=1` → `ok`.
- [ ] Report says which of step 3 or step 4 was used and why, with file:line.
- [ ] `git diff -- internal/casestore/` shows no schema text change (`CREATE TABLE`, `CREATE INDEX`, `ALTER`).

## Depends on
Nothing. Only Go writer in `internal/casestore`.
