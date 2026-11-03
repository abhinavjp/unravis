# Phase 01: gate-in-engine

risk: high — TSK-06 changes numeric verdicts for every run; the threshold table is pinned by a fingerprint, a history file and 5 goldens.

## Outcome

`ComputeFacts` evaluates a gate (reference ≥ 60 events, P95 reference ≥ 100, incident ≥ 20, spread rule over covered reference minutes). When the gate fails, the rate (`PerSec`) is withheld from both windows and, for P95, the reference P95 is withheld. Rate-based signals then read "unknown"/missing instead of a false verdict. Threshold table is version `2026-11-02` with 8 new entries.

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 01 thresholds-fields | medium | — |
| 02 thresholds-fixtures | medium | 01 |
| 03 spread-rule | low | 01 |
| 04 gate-evaluator | low | 03 |
| 05 gate-in-facts | medium | 02, 04 |
| 06 gate-applies | high | 05 |
| 07 gate-scenario-tests | low | 06 |

## Edges

01 → 02, 01 → 03 → 04 → 05 (also 02 → 05) → 06 → 07. One writer at a time on `thresholds.go`, `facts.go`.

## Review focus

- Goldens: only `version`, `fingerprint` and the 8 new entries may differ (I1).
- `Facts` JSON unchanged: `Gate` is `json:"-"` (I6).
- The gate uses covered reference minutes only (`b.gap == ""`), never gap minutes counted as zero.
- No existing test edited except the version literal and the pins added in TSK-01; a test-only generator raise (TSK-06) must be recorded in `progress.md`.
- Second reviewer on TSK-06.

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
```

Both PASS. Then `git diff --stat` shows only: `internal/investigate/{thresholds.go,thresholds_test.go,gate.go,gate_test.go,facts.go,...}`, `testdata/investigate/*`, `tools/e2e/specs/plan.spec.ts`, `docs/user/guided-analysis.md`.

Commit: one commit for the phase, after a stop. Never push.
