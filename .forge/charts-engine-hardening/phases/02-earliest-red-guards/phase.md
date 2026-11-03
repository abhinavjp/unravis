# Phase 02: earliest-red-guards

risk: high — TSK-15 and TSK-16 change which origin and colour a run names; the lane input gains a field; `colour` gets a new parameter.

## Outcome

The origin and the Red colour can no longer be named from a thin or misleading signal:

1. A lane (SQL, Mongo, host) that is configured but unusable counts as an unread tier, so a later tier is not called the origin.
2. An onset must stay up (persistence check) before it can be the earliest tier.
3. Lane clocks get a 5 s margin; app-tier clocks keep the existing margin.
4. Load is never named the origin when retries or errors precede it; the colour drops from Red to Orange in the same case.
5. Boundary behaviour is pinned: 60 s apart is "together", 120 s apart is "separable".

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 08 pair-sep-helper | low | 07 |
| 09 boundary-tests | low | 08 |
| 10 lane-unread-input | medium | 07 |
| 11 lane-unread-tier | medium | 10 |
| 12 persist-helper | low | 07 |
| 13 persist-wire | medium | 12 |
| 14 lane-clock-margin | medium | 08, 13 |
| 15 retries-origin | high | 14 |
| 16 retries-colour | high | 15 |

## Edges

07 → 08 → 09; 07 → 10 → 11; 07 → 12 → 13; 08 + 13 → 14 → 15 → 16. Tasks 08/09, 10/11 and 12/13 are three independent chains that may run in any order, but only one writer at a time on `origin.go`, `evaluate.go`, `facts_parts.go`.

## Review focus

- No new threshold field is allowed in this phase. If one is unavoidable, stop: a second version bump and a second fixture run would be needed (BLOCKED).
- Goldens must stay unchanged in every task (I1). A golden diff → BLOCKED.
- `ComputeOrigin` and `colour` must keep one shared definition of "together" (`pairSep`).
- `Input.LaneUnread` is `omitempty`, so existing JSON inputs are unchanged.
- Second reviewer on TSK-15 and TSK-16.

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
```

Both PASS, goldens unchanged.

Commit: one commit for the phase, after a stop. Never push.
