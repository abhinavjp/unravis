# Plan: charts-engine-hardening

mode: detailed
risk: high — changes numeric verdicts in the Go engine (Phase 01 TSK-06, Phase 02 TSK-15/16, Phase 07 TSK-44/57/59) and is pinned by a threshold fingerprint plus 5 goldens; Phase 06 adds importer kinds and engine inputs, Phase 07 adds nine catalogue suspects.
commit_granularity: phase
history_style: separate

## Outcome

The investigate engine and the wireframe use ONE honest gate for rate and P95 comparisons (reference ≥ 60 events, P95 ≥ 100, incident ≥ 20, spread rule, ratios over covered minutes), the five earliest-red guards are in the engine, the percentile method is pinned by a test, and the invented numbers are gone from the wireframe. Every threshold change is versioned (Version bump, history entry, goldens regenerated).

## Scope

- Go: `internal/investigate/` (gate, guards, thresholds), `internal/server/lanes.go` (lane unread input), `internal/audit/histogram_test.go` (pin test only).
- Go, Phase 06 (fetchability build, decision 8): `internal/importer/sqlserver/xe.go`, `internal/evidence/caseexport_enums.go`, `internal/investigate/types.go`, `sqllane.go`, `hostlane.go`, `internal/server/investigation.go`, `blackbox.go`, `internal/casestore/series.go`, `internal/resource/metrics.go`, `internal/blackbox/*`.
- Go, Phase 07 (nine suspects, decision 6): `internal/investigate/` (catalog, signals, new `sus_*.go`, evaluate, thresholds), `internal/importer/mongoexports/`, `internal/evidence/caseexport.go`, `internal/dbhealth/lane_mongo.go`, `internal/server/investigation_metrics.go`, the 5 goldens.
- Wireframe: `prototype/tracing-paper-PROTOTYPE.html` (alignment only).
- Docs: `docs/user/guided-analysis.md`, `tools/e2e/specs/plan.spec.ts:94` (version literal only).

## Exclusions (do not do, do not plan)

- Audit coverage policy (B8). Not decided.
- Items Phase 06 and Phase 07 list as "Out of scope / needs owner decision" (top-wait card, per-database file I/O, D1 blocking-chain XML, custom XE session, H2–H6, M5/M10, a separate `p` tier for S8).
- Mongo `$percentile` (banned: `internal/store/pipeline_test.go:58`).
- Anything under `sql/`, `.claude/`, `.mcp.json`, `unravis-review-pack.zip`, `prototype/CHARTS-AND-STATS-REVIEW.md`, `prototype/CHARTS-DECISIONS.md`, `prototype/research/` (never read sql/, never commit the others).

## Source basis (point to, do not copy)

- `prototype/research/R1-R4-THRESHOLDS-AND-GATE.md` — gate numbers and spread rule.
- `prototype/research/R2-R3-PERCENTILE-AND-EARLIEST-RED.md` — percentile method, five earliest-red guards.
- `prototype/research/FETCHABILITY-AUDIT.md` — what is fetchable; build order D10 (§3) drives Phase 06, the nine suspects' data sources drive Phase 07.
- Owner rulings: D11 keep the "19 Oct" line; D12 no demo-data labels in the wireframe.

## Deviation from the research text (flagged, needs owner confirm)

The spread rule's "skip at low rate" uses the MEAN events per covered reference minute, not the median. The median form is vacuous (a median of 0 or 1 skips almost every sparse reference). Phase 01 TSK-03 and Phase 03 TSK-19 implement the mean form.

## Requirements

| ID | Requirement | Phase / tasks |
|---|---|---|
| R1 | A rate comparison is shown only when reference events ≥ 60, incident events ≥ 20 and the spread rule passes; ratios use covered minutes on both sides. | P01 TSK-03,04,05,06,07; P03 TSK-17,18,19 |
| R2 | A P95 comparison additionally needs reference events ≥ 100. | P01 TSK-04,06,07; P03 TSK-17 |
| R3 | Percentile method is pinned: merged 192-bin histogram within ~10% of exact nearest-rank; no `$percentile`. | P04 TSK-24,25 |
| R4 | Guard 1: a configured-but-unusable lane counts as an unread tier. | P02 TSK-10,11 |
| R5 | Guard 2: the earliest tier must persist (not a brief blip). | P02 TSK-12,13 |
| R6 | Guard 3: wider cross-source clock margin for lane tiers (5 s). | P02 TSK-08,14 |
| R7 | Guard 4: never name load as the origin when retries or errors precede it. | P02 TSK-15,16 |
| R8 | Guard 5: 60 s apart is together, 120 s apart is separable (pinned by tests). | P02 TSK-08,09 |
| R9 | Wireframe shows the same gate and drops the invented `*1.6` and `onsetMinSamples` 20. | P03 TSK-17..23, P04 TSK-25 |
| R10 | Every `thresholds.go` change bumps Version, adds a history entry, regenerates goldens. | P01 TSK-01,02; P02 TSK-13,14 (if they add fields); P07 TSK-43,59 |
| R11 | The catalogue holds S8–S12, D16, D17, M11, M12 (decision 6); each is Grey with a plain reason when its data is absent, never Green without a complete source; S9 is never red; S1 gains the `queue_burst` Expect; no existing suspect's result, colour or why changes. | P07 TSK-43..60 |
| R12 | The data those suspects need is fetchable, in order SQL data → HTTPERR → Blackbox sampler (decision 8): XE call/blocked/attention kinds, imported SQL and HTTPERR rows in `investigate.Input`, burst counter, series quantile and status class, disk latency, D2/D6/D9/D10 lane answers; scrub unchanged, facts output unchanged for inputs without the new rows. | P06 TSK-29..42 |

## Invariants

- I1 The 5 goldens change only in version, fingerprint and new threshold entries. Anything else → BLOCKED with the diff. Phase 07 only: the allowed list of TSK-59 also applies (nine new suspects, six new signal keys, S1's `queue_burst` evidence row, suspect counts); an existing suspect's result, colour or why still → BLOCKED.
- I2 Existing tests pass unchanged (only exception: a test-only generator raise recorded in `progress.md`).
- I3 No Mongo `$percentile`.
- I4 D11 "19 Oct" line kept; D12 no demo-data labels in the wireframe.
- I5 Never commit the excluded paths; never push; stop before every commit.
- I6 Facts JSON output is unchanged: new `Gate` field is `json:"-"`; new `LaneUnread` input is `omitempty`.

## Phase dependency graph

```
P01 gate-in-engine ──► P02 earliest-red-guards ──┬──► P06 fetchability-build ──► P07 catalogue-nine
P03 prototype-alignment ──► P04 percentile-method ┼──► P05 verification-and-docs
P01 ─────────────────────────────────────────────┘
```

Real edges: P02 after P01 (both edit `thresholds.go`, `evaluate.go`, `origin.go`, `facts_parts.go`; thresholds history is append-only and ordered). P04 TSK-25 after P03 TSK-22 (same Rules card). P05 after P01–P04. P03 has no code edge to P01; it only uses the same fixed numbers. P06 after P02 (P06 adds `investigate.Input` fields and lane readers on top of P02's `LaneUnread` and origin changes). P07 after P06 (its suspects read P06's inputs, burst counter, `sqllane.go`, `resource/metrics.go` and `readMetricLanes`; TSK-43 appends to the thresholds history after P01/P02). P05 stays the checkpoint for P01–P04 only; P06 and P07 each end with their own full-checks packet (TSK-60 for P07; the P06 phase proof).

Executable frontier now: P01 and P03 (disjoint files; can run in parallel). P04 TSK-24 (Go test only) may also start now. Then P02, then P05 and P06 (may run alongside; P06 does not touch P05's docs), then P07.
Inside P06 the frontier is TSK-29, 32, 36, 38, 39. Inside P07 it is TSK-43, 53, 55; after TSK-44: 45–51, 54, 56, 57 in parallel.

## Task index

| Task | Phase | Slug | Risk | Depends on |
|---|---|---|---|---|
| 01 | P01 | thresholds-fields | medium | — |
| 02 | P01 | thresholds-fixtures | medium | 01 |
| 03 | P01 | spread-rule | low | 01 |
| 04 | P01 | gate-evaluator | low | 03 |
| 05 | P01 | gate-in-facts | medium | 02, 04 |
| 06 | P01 | gate-applies | high | 05 |
| 07 | P01 | gate-scenario-tests | low | 06 |
| 08 | P02 | pair-sep-helper | low | 07 |
| 09 | P02 | boundary-tests | low | 08 |
| 10 | P02 | lane-unread-input | medium | 07 |
| 11 | P02 | lane-unread-tier | medium | 10 |
| 12 | P02 | persist-helper | low | 07 |
| 13 | P02 | persist-wire | medium | 12 |
| 14 | P02 | lane-clock-margin | medium | 08, 13 |
| 15 | P02 | retries-origin | high | 14 |
| 16 | P02 | retries-colour | high | 15 |
| 17 | P03 | ref-thresholds | low | — |
| 18 | P03 | covered-rate-refcmp | medium | 17 |
| 19 | P03 | spread-helper-and-sites | medium | 18 |
| 20 | P03 | drop-invented-max-and-onset | low | — |
| 21 | P03 | grey-low-bucket-p95 | low | 20 |
| 22 | P03 | rules-card | low | 19 |
| 23 | P03 | flow-check-run | low | 22 |
| 24 | P04 | histogram-pin-test | low | — |
| 25 | P04 | tolerance-wording | low | 22 |
| 26 | P05 | docs-greyed-out | low | 16, 23 |
| 27 | P05 | full-checks | low | 26 |
| 28 | P05 | human-comprehension-test | low | 27 |
| 29 | P06 | xe-call-kind | medium | P02 done |
| 30 | P06 | xe-blocked-kind | medium | 29 |
| 31 | P06 | xe-attention-kind | low | 30 |
| 32 | P06 | input-fields | medium | P02 done |
| 33 | P06 | burst-count | low | 32 |
| 34 | P06 | sqllane-d2 | medium | 33 |
| 35 | P06 | server-imported-events | medium | 29, 30, 31, 34 |
| 36 | P06 | series-quantile | medium | P02 done |
| 37 | P06 | series-status-class | low | 36 |
| 38 | P06 | metrics-mem-grants | low | P02 done |
| 39 | P06 | blackbox-disk-latency | medium | P02 done |
| 40 | P06 | hostsample-disk | low | 39 |
| 41 | P06 | hostlane-d9-d10 | medium | 40 |
| 42 | P06 | d6-mem-grants-wiring | medium | 35, 38 |
| 43 | P07 | thresholds-burst-fields | medium | P06 done |
| 44 | P07 | catalogue-skeleton | high | 43, 32, 33 |
| 45 | P07 | s1-queue-burst | medium | 44, 35 |
| 46 | P07 | s8-payload-up | medium | 44 |
| 47 | P07 | s9-filter-reject | medium | 44, 32, 35 |
| 48 | P07 | s10-app-pool-fail | low | 44, 35 |
| 49 | P07 | s11-recv-timeout | low | 44, 35 |
| 50 | P07 | s12-drain | medium | 44, 35 |
| 51 | P07 | d16-attention-lane | medium | 31, 34, 35, 44 |
| 52 | P07 | d17-login-storm-lane | low | 51 |
| 53 | P07 | m11-used-disk-gap | medium | P06 done |
| 54 | P07 | m11-spill-lane | medium | 53, 44 |
| 55 | P07 | m12-rejected-projection | medium | 38 |
| 56 | P07 | m12-rejected-lane | medium | 55, 42, 44 |
| 57 | P07 | cap-orange | high | 44 |
| 58 | P07 | missing-words-grey | medium | 57, 45–56 |
| 59 | P07 | golden-regen | high | 58 |
| 60 | P07 | full-checks | low | 59 |

Same-file ordering inside P01/P02 (thresholds.go, evaluate.go, origin.go, facts_parts.go): run in table order, one writer at a time. P06 and P07 each carry a single-writer table in their `phase.md`; follow it.

## Integration points

- `Analyze` (`internal/investigate/engine.go:23`) calls `ComputeFacts` → `ComputeSignals` → `Steps` → `Evaluate` → `ComputeOrigin` → `ComputeConfidence`. The gate lives inside `ComputeFacts` (`groups()` in `facts.go`), so signals, steps, suspects and origin all see the gated numbers.
- `ComputeOrigin` (origin.go) and `colour` (evaluate.go) each recompute the "together" window; Phase 02 TSK-08 makes them share one helper.
- `applyLaneRuns` (`internal/server/lanes.go`) feeds `Input.LaneOnsets`; TSK-10 adds `Input.LaneUnread` beside it.
- Threshold table: `thresholds.go` ↔ `testdata/investigate/thresholds.history.json` ↔ 5 goldens ↔ `tools/e2e/specs/plan.spec.ts:94` ↔ `docs/user/guided-analysis.md:50`.

## Cross-phase proof

- After P01: `.dev\toolchains\go\bin\go.exe test ./internal/...` PASS, goldens differ only per I1.
- After P02: same command PASS; new boundary and guard tests present.
- After P03: `node prototype/flow-check.cjs` PASS, or UNMEASURED when Playwright is missing (it is not installed here).
- After P04: `go test ./internal/audit` PASS with the new pin test.
- After P05: Go all PASS, `go build ./...` OK, `cd web; npm test; npm run check` PASS.
- After P06: `go build ./...` OK, `go test ./internal/...` PASS, web checks PASS; the 5 goldens unchanged (`git diff --stat testdata/investigate/` empty for this phase).
- After P07: TSK-59 golden diff holds only the allowed changes, TSK-60 build, Go and web checks PASS; e2e `plan.spec.ts` UNMEASURED unless Playwright is installed.

## Final acceptance

All 12 requirements traced to a PASSING check or an explicit UNMEASURED line in a `progress.md`; I1–I6 hold; `git status` shows no excluded path staged; no push done; one commit per phase made only after the owner says go.

## Conditional controls

- If a golden changes beyond version, fingerprint and new entries → task is BLOCKED; report the diff; do not "fix" the golden by hand.
- If an existing test fails only because its generated reference has fewer than 60 (or 100) events → allowed fix is a TEST-ONLY raise of `genCfg.perMin` or the reference span; record it in the phase `progress.md`. Any production-code workaround is not allowed.
- If a packet says "find X" and you cannot find it in the named file within 3 searches → stop, mark BLOCKED, report what you searched. Do not widen scope.
- High-risk packets (TSK-06, 15, 16, 44, 57, 59) need a second reviewer before the phase gate.
- Phase gate: run the phase proof, review the diff, at most 2 fix loops, then STOP before the commit. Never push.
- Delegation: implement via the Runner Hub (lanes in `~/.claude/runner-hub.md`); Opus only for TSK-06/15/16/44/57/59 review. Never print the hub token.

## Open decisions (suggested answers)

1. Lane clock margin → 5000 ms (R3 guard 3).
2. Persistence share → 0.5.
3. "Unread lane" = a lane kind with an overlapping held day that gave no onset and no usable "No" answer (whole-cover).
4. Spread skip uses MEAN not median (deviation above) → confirm.
5. Wireframe :1204 zero-event incident case now greys → confirm.
6. Decided 2026-11-02: add all nine (S8–S12, D16, D17, M11, M12) now → Phase 07, TSK-43..60.
7. Audit coverage policy B8 (outside this plan).
8. Decided 2026-11-02: fetchability build is Phase 06 of this plan, order SQL data → HTTPERR → Blackbox sampler → TSK-29..42.
