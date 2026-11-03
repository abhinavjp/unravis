# TSK-12 persist-helper

risk: low — one pure function with unit tests.

## Outcome

`persistShare(bins, from, hit)` returns the share of minutes after an onset that stay above the limit. Not used by the engine yet.

## Write scope

- `internal/investigate/facts_parts.go` (append the function)
- `internal/investigate/facts_parts_test.go` (create if absent, else append)

## Must not change

- `metricOnset`, `loadOnset`, `tiers`. Every other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Read `metricOnset` (`facts_parts.go` ~532-555) first: it walks `bins` looking for a run of `OnsetRunMinutes` minutes where a `hit` predicate is true, skipping minutes with `b.gap != ""` or too few samples. Reuse its bin type name and its notion of a "skipped" minute (gap or untested). Do not change it.
2. Append `func persistShare(bins []<bin type from step 1>, from int, hit func(<bin type>) bool) float64`. Semantics: look at `bins[from:]`; ignore skipped minutes; `hits` = minutes where `hit` is true; `misses` = tested minutes where it is false; return `float64(hits)/float64(hits+misses)`; return `1` if the denominator is 0 or `from` is out of range.
3. Unit tests `TestPersistShare` (table): all hits → 1; half hits → 0.5; a gap minute ignored; empty tail → 1; `from` past the end → 1. Build bins with the same literal construction the existing tests use for `metricOnset` (copy the helper; if none exists, construct the struct literal directly using the field names from the type definition).

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run TestPersistShare
```

PASS.

## Depends on

TSK-07.
