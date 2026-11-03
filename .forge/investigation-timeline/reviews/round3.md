# Round 3 review (scoped: verify round-2 fixes)

Result: 0 BLOCKER, 0 MAJOR, 8 MINOR. `check-plan.ps1`: 44 packets in plan, 44 packet files, errors 0, same-wave overlaps 0, warnings 0, OK.

## Verified OK (no finding)

1. Default audit lane. WP-14 line 32 (DEFAULT-AUDIT RULE), line 36 (no audit id added for the default call), line 37 (unknown audit id gives 404, checked by WP-17a), line 97 (`TestTracingSourceIDs`: `demo-west` is 200 while WP-14 runs alone, WP-17a changes it to 404). WP-17a line 11 allows `noRepo` to gain an empty `Sources` and the `demo-west` expectation to change; line 32 defines `auditSourceIDs(ctx, repo, explicit)` with the 50 cap, warning and `errUnknownAuditSource`; `TestTracingDefaultCallHasAuditLane` covers it. `audit.Repository.Sources` exists (`internal/audit/models.go:472`) and `h.repo` is `audit.Repository` (`internal/server/server.go:39`). WP-14 tests still pass when run alone (phase 03 before 04).
2. Seam. `appendLane(&resp, lane)` / `finishTracingMeta(&resp)` are used the same way in WP-18b (lines 19-22) and WP-17a (step 3, line 31, one `finishTracingMeta`). Rollup 1 h slots (`min(W,1h)`, slot `Start` int64 Unix seconds) fold via `axis.Bucket(k)`, which matches WP-15 line 29 (`Bucket(k int)(from,to time.Time,clipped,ok bool)`); `TestAuditLaneAlignsToAxis` covers it.
3. WP-23 / WP-61 absolute-index rule: WP-23 line 35, WP-61 lines 12 and 20 agree. WP-64 line 23 asserts `aria-labelledby="' + id + '"`, matching the WP-16b `home-tracing` h3 id and section `aria-labelledby`.
4. WP-17a wire fields match the WP-14 contract block (lines 41-91): arrays never null, event type `calltrace.LaneEvent`.
5. New edges: WP-07a depends on WP-06a, WP-01b; WP-02b depends on WP-02a, WP-45. Both match plan.md §3 rows (lines 87 and 89).

Round-2 BLOCKER (no audit lane on default call), the seam MAJOR, the axis-misalignment MAJOR, WP-64 `aria-labelledby` MAJOR, WP-23/WP-61 index MAJOR and WP-07b length-test MAJOR are all fixed.

## MINOR

[MINOR] WP-14 line 32 vs WP-17a line 32 — On a `repo.Sources` error WP-14 says "no audit lane and a warning", WP-17a says an incomplete audit lane with state `error`. Two packets, two behaviours for the same case — Reword WP-14 line 32 to: "On a `repo.Sources` error the audit lane behaviour is defined by WP-17a; WP-14 adds no audit lane." Also say in WP-17a that `h.repo` is never nil in production and a nil `h.repo` is treated like an empty source list.

[MINOR] WP-17a line 32 / WP-14 `caseTracing` step 5 — `auditSourceIDs` relies on the caller passing a non-nil empty `[]string{}` to mean "explicit, none", but WP-14 never guarantees `AuditIDs` is non-nil after JSON decode (a JSON `[]` and a missing field both can decode differently depending on the decoder). Ambiguous for a blind implementer — Add an `explicit bool` parameter: `auditSourceIDs(ctx, repo, explicit bool, ids []string)`, set `explicit := len(req.SourceIDs) > 0` in the handler, and state that an empty or null `sourceIds` array is the default call.

[MINOR] WP-17a (uses `req.SourceIDs`) vs WP-14 step 5 — WP-14 never names the decoded request variable in `caseTracing`, so `req` in WP-17a is unanchored — Name it `req` in WP-14 step 5 (`var req tracingRequest`).

[MINOR] WP-23 line 20 — says "seven tests" but eight are listed (lines 37-45) and Proof says "the eight" — Change "seven" to "eight".

[MINOR] WP-23 line 44 — requires the literal `range.from + slotAtFraction(` in the component source while also exporting an `absoluteSlot` helper; an implementer who uses the helper in the component fails the grep — Say "the component must inline `range.from + slotAtFraction(...)`; `absoluteSlot` is for tests only", or loosen the grep to accept either.

[MINOR] Phase 03 phase.md (security canaries, about line 39) — says `noRepo{}` panics on any call. After WP-17a `noRepo` has an empty `Sources`, so `TestTracingOpensNoLiveConnection` and the canary text are out of date after phase 04 — Add: "after WP-17a, `noRepo.Sources` returns an empty list; every other method still panics".

[MINOR] Phase 01 phase.md edge list (lines 38-50) and line 76 — The edge list lacks `01b -> 07a` and `45 -> 02b`, and line 76 says those two pairs are "ordered by wave number only, with no edge". Both edges now exist in the packet files and plan.md §3 (rows WP-02b and WP-07a), so the text is stale and contradicts plan.md — Add `01b -> 65b, 07a` and `45 -> 02b, 04, 59` to the edge list, and change line 76 to "all pairs of writers are ordered by a dependency edge".

[MINOR] Phase 01 phase.md line 36 ("Frontier at phase start") — consistent with the edges but the "Then WP-02b, WP-06b, WP-07a, WP-65b" wave line is fine only if WP-45 is in the first wave; confirm no change needed after the edge fix above — No action beyond the edge-list fix.

## Round-2 findings still open

None at BLOCKER or MAJOR. Only the MINORs above.
