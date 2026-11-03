# Phase 05: verification-and-docs

risk: low — one doc note and checks.

## Outcome

The user guide explains when a comparison is greyed out; the full Go and web checks are run and recorded; a manual comprehension test is queued for a human.

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 26 docs-greyed-out | low | 16, 23 |
| 27 full-checks | low | 26 |
| 28 human-comprehension-test | low | 27 |

## Edges

Linear: 26 → 27 → 28. Starts only after Phases 01–04 are green.

## Review focus

- The doc numbers (60 / 100 / 20, spread rule) match `thresholds.go`.
- UNMEASURED items are listed honestly (e.g. Playwright e2e not runnable here).

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
cd web; npm test; npm run check
```

All PASS, or each failure/skip written in `progress.md`.

Commit: one commit for the phase, after a stop. Never push.
