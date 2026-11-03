# Phase 04: percentile-method

risk: low — one new test and one sentence; no behaviour change.

## Outcome

The percentile method (fixed-bucket log histogram, merged by summing counts, within about ±5%) is pinned by a test and stated to the reader as "within about 10%". Mongo `$percentile` stays banned.

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 24 histogram-pin-test | low | — |
| 25 tolerance-wording | low | 22 |

## Edges

24 has no dependency. 25 edits the same wireframe card as 22, so it waits for Phase 03.

## Review focus

- The test uses a fixed seed (reproducible) and compares against exact nearest-rank.
- The existing `$percentile` rejection test (`internal/store/pipeline_test.go:58`) is untouched.

## Phase proof

```
.dev\toolchains\go\bin\go.exe test ./internal/audit
```

PASS. Record the TSK-25 grep result.

Commit: one commit for the phase, after a stop. Never push.
