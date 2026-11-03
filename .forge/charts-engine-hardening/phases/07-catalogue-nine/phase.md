# Phase 07: catalogue-nine

risk: high — nine new catalogue entries, six new signals, a threshold Version bump and a golden regen; a wrong rule paints a suspect red that should not be.

## Outcome

The engine catalogue holds the nine suspects the owner added on 2026-11-02 (decision 6), each fed by the data Phase 06 made fetchable:

| Id | Title | Tier | Must | Fed by |
|---|---|---|---|---|
| S8 | Payload capture or response-body handling slowdown | a | `payload_up` | audit `P` (PayloadRecordTime) |
| S9 | IIS request-filtering rejection | o | `filter_reject` | IIS status and sub-status (TSK-32/35); **capped at orange** |
| S10 | Application pool unavailable or worker failure | o | `app_pool_fail` | HTTPERR rows (TSK-35) |
| S11 | HTTP.sys header or body receive timeout | o | `recv_timeout` | HTTPERR rows |
| S12 | Slow client or proxy: response drain or disconnect | o | `drain` | HTTPERR rows, else IIS time outside the app |
| D16 | SQL command timeout or cancellation burst | sql (lane) | `c_large` | XE attention rows (TSK-31/35) |
| D17 | SQL login or authentication storm | sql (lane) | `c_large` | error-log login-failed rows (TSK-35) |
| M11 | MongoDB spill to disk | mongo (lane) | `a_large` | profiler `usedDisk` (TSK-53) |
| M12 | MongoDB incoming-connection rejection | mongo (lane) | `a_large` | serverStatus `connections.rejected` (TSK-55) |

S1 also gains an Expect signal `queue_burst` from HTTPERR `QueueFull`/`Timer_AppPool`/`AppPoolTimer` rows.

Every suspect reads Grey ("cannot test yet") with a plain reason when its data is absent. Six new marks move into `Thresholds` (TSK-43), so `Version` moves once; lane marks (D16, D17, M11, M12) stay lane-local like Phase 06.

## Tasks

| Task | Risk | Depends on |
|---|---|---|
| 43 thresholds-burst-fields | medium | P06 done |
| 44 catalogue-skeleton | high | 43, 32, 33 |
| 45 s1-queue-burst | medium | 44, 35 |
| 46 s8-payload-up | medium | 44 |
| 47 s9-filter-reject | medium | 44, 32, 35 |
| 48 s10-app-pool-fail | low | 44, 35 |
| 49 s11-recv-timeout | low | 44, 35 |
| 50 s12-drain | medium | 44, 35 |
| 51 d16-attention-lane | medium | 31, 34, 35, 44 |
| 52 d17-login-storm-lane | low | 51 |
| 53 m11-used-disk-gap | medium | P06 done |
| 54 m11-spill-lane | medium | 53, 44 |
| 55 m12-rejected-projection | medium | 38 |
| 56 m12-rejected-lane | medium | 55, 42, 44 |
| 57 cap-orange | high | 44 |
| 58 missing-words-grey | medium | 57, 45–56 |
| 59 golden-regen | high | 58 |
| 60 full-checks | low | 59 |

## Edges

```
43 → 44 ─┬→ 45, 46, 47, 48, 49, 50 ─────────────┐
         ├→ 51 → 52 ────────────────────────────┤
53 ──────┼→ 54 ─────────────────────────────────┤
55 ──────┴→ 56 ─────────────────────────────────┤
44 → 57 ────────────────────────────────────────┴→ 58 → 59 → 60
```

Frontier at phase start (parallel, disjoint files): **43, 53, 55**. After 44: **45, 46, 47, 48, 49, 50, 51, 54, 56, 57** all in parallel (each writes its own files; see the table). 52 waits for 51. 58 waits for every task 45–57. 59, then 60, run alone.

From TSK-43 until TSK-59, `TestGolden` fails by design (the goldens carry `Version` and the threshold table). Every proof in 43–58 runs with `-skip "^TestGolden$"`; 59 regenerates and checks the diff.

Single writer per shared file in this phase:

| File | Writers, in order |
|---|---|
| `internal/investigate/thresholds.go`, `thresholds_test.go`, `testdata/investigate/thresholds.history.json` | 43 only |
| `tools/e2e/specs/plan.spec.ts`, `docs/user/guided-analysis.md` (Version literal) | 43 only |
| `internal/investigate/catalog.go`, `signals.go` | 44 only |
| `internal/investigate/sus_burst.go` (new) | 44 only |
| `internal/investigate/sus_s1.go`, `sus_s8.go` … `sus_s12.go` (stubs by 44) | 44 → one owner each: s1 45, s8 46, s9 47, s10 48, s11 49, s12 50 |
| `internal/investigate/sqllane.go`, `sqllane_test.go` | 51 → 52 |
| `internal/importer/mongoexports/profile.go`, `internal/evidence/caseexport.go` | 53 only |
| `internal/dbhealth/lane_mongo.go`, `lane_mongo_test.go` | 53 → 54 |
| `internal/importer/mongoexports/serverstatus.go`, its testdata, `values_test.go` | 55 only |
| `internal/evidence/caseexport_enums.go` | 55 only |
| `internal/resource/metrics.go`, `resource_test.go` | 55 only |
| `internal/server/investigation_metrics.go`, `_test.go` | 56 only |
| `internal/investigate/evaluate.go`, `evaluate_test.go` | 57 → 58 |
| `testdata/investigate/*.golden.json` | 59 only |

## Review focus

- 43: one `Version` bump for the phase; every copy of the old literal moved; the history file gains exactly one entry.
- 44: catalogue text passes the causal-word lint and has no percent sign; `catalogIndex` still unique; S1 change is an Expect, not a Must (so S1's own result cannot be lost to missing HTTPERR).
- 45–50: a missing source is `missing(...)` (Grey), never `No` (no false green). A `No` needs a complete source for the window.
- 47 / 57: S9 can never be red — counted rows alone do not date an onset.
- 51/52, 54, 56: lane answers never overwrite a `Yes` set by another reader; lane marks stay local, so `Version` does not move again.
- 53/55: the export policy adds only `usedDisk` (boolean) and `connections.rejected` (count); no free text is let through.
- 59: the golden diff holds only the allowed changes (see TSK-59). Any existing suspect changing result, colour or why stops the phase.

## Out of scope / needs owner decision

- **S8 tier**: the engine has no payload tier; S8 is ordered on tier `a` (the audit write path). If the owner wants a separate `p` tier, that is a new tier in `pairSep` and the origin rule, a later phase.
- **M12 Must**: as a Mongo lane it needs `a_large`; a rejection burst while A stays flat reads Partly/Orange, not Consistent. The prototype listed `connRejected` alone. Keep, or make M12 a non-lane suspect later.
- **D16 / D17 data**: attention rows exist only when the customer runs a custom XE session (see Phase 06 out of scope); until then D16 stays Grey.
- **Version literal**: the run date of TSK-43 (with a letter suffix if the date equals the current literal). The owner may want a named date.

## Phase proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
cd web; npm test; npm run check
```

All PASS, or each failure/skip written in `progress.md`.

Commit: one commit for the phase, after a stop. Never push.
