# Phase 03: prototype-alignment

risk: medium — one large HTML file with shared helpers; edits are small but every chart reads from them. No Go, no tests; proof is the flow check.

## Outcome

`prototype/tracing-paper-PROTOTYPE.html` follows the same gate as the engine: reference ≥ 60 events (≥ 100 for P95), incident ≥ 20, spread rule, rates over covered minutes, no invented `*1.6` ceiling, onset needs 5 samples, per-bucket P95 greyed under 20 events, and the "Rules used" card says so. No demo-data labels (D12); the "19 Oct" line stays (D11).

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 17 ref-thresholds | low | — |
| 18 covered-rate-refcmp | medium | 17 |
| 19 spread-helper-and-sites | medium | 18 |
| 20 drop-invented-max-and-onset | low | — |
| 21 grey-low-bucket-p95 | low | 20 |
| 22 rules-card | low | 19 |
| 23 flow-check-run | low | 22 |

## Edges

17 → 18 → 19 → 22 → 23; 20 → 21. Chains 17-19 and 20-21 are independent, but all tasks edit the same file, so only one writer at a time.

## Review focus

- Line numbers in the packets were verified at planning time; each packet says to locate by the quoted text if a line has drifted.
- No new visible "demo"/"sample" wording anywhere (D12). The "19 Oct" line is untouched (D11).
- Numbers shown to the user (60/100/20, 5, 20) match `thresholds.go` (Phase 01).

## Phase proof

```
node prototype/flow-check.cjs
```

Prints all checks OK. If Playwright is missing it cannot run: record UNMEASURED with the reason (do not claim a pass).

Commit: one commit for the phase, after a stop. Never push.
