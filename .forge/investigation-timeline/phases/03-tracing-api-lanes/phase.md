# Phase 03: tracing-api-lanes
risk: high (new public route, query budget)

## Goal
Add the read-only `POST /api/cases/{id}/tracing` route and give it the saved SQL and IIS lanes: strict request, 30-day window, the R6-02 UTC floor-aligned bucket ladder, per-lane metadata (`origin, asOf, complete, truncated, reason, consistency`, top-level `mixedConsistency`, `allLanesStored`), uncapped local bucket counts, HTTPERR markers and the IIS point-at-logged-timestamp rule. The audit lane is phase 04 (WP-17); this phase leaves its slot and its sourceId rules ready.

## Packets
| Packet | Title |
|---|---|
| WP-12 | SQL lane proof test (Appendix P5) |
| WP-13 | Lane source refs, status class and win32 on lane events |
| WP-14 | Tracing route shell, strict request, response contract |
| WP-15 | Window validation, 30-day cap and R6-02 bucket ladder |
| WP-18a | Store bucket aggregate (CountBuckets + sqlite GROUP BY) |
| WP-18b | SQL and IIS lanes in the tracing response |
| WP-19 | HTTP.sys error markers on the IIS lane |
| WP-20 | IIS request is a point at the logged timestamp |
| WP-25 | Demo case with SQL and IIS lanes for the tracing view |

WP-18 of the spec is split into WP-18a and WP-18b. Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them. The spec's WP-25 asserts three lanes; the audit lane needs WP-17a (phase 04), so WP-25 here asserts the sql and iis lanes only and phase 04 adds the audit assertion.

Contracts this phase hands to phase 04 (read them in the packets, they are not repeated here):
- The one JSON response contract block (every field with its exact JSON path, per-lane metadata, top-level `truncated`, `mixedConsistency`, `allLanesStored`, `requestSeq`, `sources`, optional bucket `errors`) is in WP-14, Changes step 7.
- The request rules (omitted or empty `sourceIds` means every case source capped at 50; more than 50 explicit ids is a 400; an unknown `cs_`/`imp_` id is a 404 `unknown-source`) are in WP-14. Default-audit rule (WP-14 step 4): empty `AuditIDs` means "resolve the defaults", so when `sourceIds` is omitted the audit sources ARE included, loaded by the WP-17a lane code with `h.repo.Sources(ctx)`; an explicit audit id the repository does not list is also 404 `unknown-source` (checked by WP-17a).
- The seam WP-17a uses to add the audit lane is in WP-18b, Changes step 1: `appendLane(resp *tracingResponse, lane tracingLane)` then `finishTracingMeta(resp *tracingResponse)`. WP-14 also owns `unwrapCase(c casestore.Case) casestore.Case`; any tracing code that needs `casestore.Tagger` or `casestore.BucketCounter` goes through it, because `openCase` returns a `*sharedCase` wrapper that hides them.

## Phase proof
Run from `D:\Projects\api-audit-explorer`:
- `go build ./...` -> no output
- `go vet ./internal/calltrace/... ./internal/casestore/... ./internal/server/... ./internal/importer/iis/... ./cmd/audit-explorer/...` -> no output
- `go test ./internal/calltrace/... ./internal/casestore/... ./internal/importer/iis/... -count=1` -> `ok` for every package
- `go test ./internal/server/... -run "Tracing|CallTrace|Demo" -count=1` -> `ok`
- `go test ./cmd/audit-explorer/... -run Demo -count=1` -> `ok`
- `go test ./internal/server/... -count=1` -> `ok` (full server package: the call-trace JSON gained only additive fields)
PASS = all of the above green, plus the checks under "Additions to plan.md section 7" below.

## Additions to plan.md section 7 (reviewer, browser and canaries)
- No UI lands here. Look at the route only: with the demo running (`--demo`, `UNRAVIS_DEMO_CASES=1`), POST the demo tracing case (WP-25) with a 1-hour incident window and read the JSON. Compare field names against the WP-14 contract block (Changes step 7) and the WP-15 axis fields; phase 04 UI packets rely on them letter for letter.
- Security canaries: a response for a case holding `client-ip`, `referer` and `cookie:session` attrs must contain none of those three key names or values (grep the raw body). A request with an unknown field, with `route`, more than 50 `sourceIds`, or with a foreign `sourceId` returns 400/404 and opens no connection (the test repository is `noRepo{}`, which panics on any call; after WP-17a in phase 04, `noRepo.Sources` returns an empty list and every other method still panics). Read-only: no `case.db` write (revision and `meta` unchanged after a POST).
- Perf bounds (state which mode was measured, never assert a millisecond figure): the SQL and IIS bucket counts come from one `GROUP BY` per request, not from the 50 000 raw-row read. A 30-day case with 200 000 events must still report exact bucket totals while `events[]` stays at most 60 per lane (`calltrace.MaxLaneEvents`). Aligned and misaligned 30-day windows give 6 h/120 buckets and 8 h/91 buckets (integer arithmetic, see WP-15).
- Escalations to read before merge: the sourceId wire format (`cs_<id>`, `imp_<id>`), the omitted-`sourceIds`-means-all rule, the additive call-trace JSON fields (including the new "web request" / "HTTP.sys error" labels), `reference` accepted but unused, tracing is start-based with no lookback (unlike `/call-trace`), and the two-imports-same-importer limitation of `events[]` (counts stay exact).
- Wrapper canary: the bucket counts of a sqlite case must come from the `GROUP BY` path (WP-18b `TestTracingBucketsUseGroupBy`); if the spy sees a handle that is not a `casestore.BucketCounter`, `unwrapCase` was skipped.

## Overlap note
`internal/server/tracing.go` and `internal/server/tracing_test.go` are also written by phase 04 WP-17a (audit lane). Within this phase they are written by WP-14 (new), WP-15, WP-18b and WP-19, always in different waves, in that order. WP-17a depends on WP-18b in plan.md §3, so it runs after it: it adds the audit lane through `appendLane` and `finishTracingMeta` and must not edit `tracingSQLIISLanes`, `tracingCountBuckets`, `appendLane` or `finishTracingMeta`. WP-19 (marker step inside `tracingSQLIISLanes`) and WP-17a edit different regions; phase order 03 then 04 settles it.

## Commit file list (explicit paths, `git add -A` is forbidden)
```
internal/calltrace/types.go
internal/calltrace/lanes.go
internal/calltrace/lanes_test.go
internal/casestore/buckets.go
internal/casestore/buckets_test.go
internal/casestore/sqlite/case.go
internal/casestore/sqlite/buckets_test.go
internal/audit/models.go
internal/server/routes.go
internal/server/routes_test.go
internal/server/tracing.go
internal/server/tracing_axis.go
internal/server/tracing_test.go
internal/server/tracing_axis_test.go
internal/importer/iis/w3c.go
internal/importer/iis/iis_test.go
internal/importer/iis/httperr.go
internal/importer/iis/httperr_reason_test.go
cmd/audit-explorer/demo_cases.go
cmd/audit-explorer/demo_cases_test.go
.forge/investigation-timeline/phases/03-tracing-api-lanes/
```
