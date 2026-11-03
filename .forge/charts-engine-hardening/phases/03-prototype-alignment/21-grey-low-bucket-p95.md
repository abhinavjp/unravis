# TSK-21 grey-low-bucket-p95

risk: low — display only.

## Outcome

A bucket with fewer than 20 events shows no P95 value (greyed), because a percentile of fewer than 20 events is not meaningful.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- The count `n`. P50. Any other chart. Do not add new visible text beyond what the chart already shows for a missing value.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `auditBuckets` (~247-253), where each bucket object gets `p95: <value>`, change to `p95: s.n >= 20 ? <existing value> : null` (use the bucket's own count variable if it is not `s.n`).
2. Grep the chart code that draws `p95`; confirm it already skips `null` (the same way it skips missing values elsewhere). If it does not, add the same `null` guard the neighbouring series uses. List what you checked in `progress.md`.

## Proof

Load the wireframe with `preview_start`, open a chart that uses `auditBuckets`, and confirm `read_console_messages` is clean. UNMEASURED if the browser cannot run.

## Depends on

TSK-20.
