# Round 1 review: phase 03-tracing-api-lanes (WP-12, 13, 14, 15, 18a, 18b, 19, 20, 25)

Scope: phase.md + 9 packets, checked against the Go code and against the phase 04 readers (WP-16a, WP-17a, WP-17b). R6-02 and R6-03 are not findings.

## BLOCKER

[BLOCKER] WP-14 step 4 (Tagger lookup) and WP-18a/WP-18b (CountBuckets) — `openCase` returns `&sharedCase{...}` (internal/server/cases.go:313-322, 830), a struct embedding the `casestore.Case` interface. The wrapper hides optional interfaces: `c.(casestore.Tagger)` fails, so every `cs_<id>` source id 404s and the "real tag -> 200" test cannot pass. `casestore.CountBuckets(ctx, c, ...)` on the wrapper never sees `BucketCounter`, silently takes the `CountBucketsScan` fallback and loses the single GROUP BY (it is then bounded by the 20M / 10 s scan budget). Existing code already unwraps: case_sources.go ~185 and lanes.go:68 use `if sc, wrapped := c.(*sharedCase); wrapped { inner = sc.Case }`. — Fix: WP-14 adds `func unwrapCase(c casestore.Case) casestore.Case` in tracing.go (same pattern); the handler uses the unwrapped case for the Tagger assertion and for every CountBuckets call. Add a WP-18b test that a sqlite-backed case takes the GROUP BY path (for example a counting wrapper or asserting the `BucketCounter` assertion succeeds on the unwrapped handle).

## MAJOR

[MAJOR] WP-14 vs WP-16a/WP-17a/WP-17b — "sourceIds omitted = all sources of the case" is the phase 04 contract (WP-16a `buildTracingBody` omits `sourceIds` when empty and says so in its ESCALATION), but WP-14 line 22 answers 400 "Pick between 1 and 50 sources." for an absent or empty list. Every default call from the UI would fail. — Fix: WP-14 resolves an absent/empty `sourceIds` to every source of the case (still capped at 50, over 50 -> 400), and adds `TestTracingOmittedSourceIdsMeansAll`; keep the 400 only for more than 50 or an explicit unknown id. Update the phase.md note accordingly.

[MAJOR] WP-14/WP-15 response shape vs WP-16a `TracingResponse` and WP-17b — the server puts `bucketSeconds`, `axisStart`, `axisEnd`, `buckets` inside `window` (WP-14 line 33); WP-16a types `bucketSeconds` at the top level, `TracingWindow` has only `start/end`, and `parseTracing` rejects a non-positive `bucketSeconds` (WP-14's shell sends 0, fine, but the field is read from the wrong place). WP-17b `slotGrid` rebuilds the grid from the request window as `floor(start/width)`, which differs from the server's hour-floored axis for a misaligned window (10:07-11:07: server axis 10:00-12:00 at 60 s = 120 buckets, UI n=61), so bars and ticks drift from the server buckets. — Fix: WP-16a `TracingWindow { start; end; axisStart; axisEnd; bucketSeconds; buckets }`, read `response.window.bucketSeconds`; WP-17b builds `SlotGrid` from `window.axisStart/axisEnd/bucketSeconds` (n = `buckets`), never from the request window.

[MAJOR] WP-14/WP-18b vs WP-16a/WP-17b — other field mismatches: (a) WP-16a/WP-17b read `response.truncated` and `response.disabled` at the top level; the server has `disabled` per lane (`"tz-rule"`) and no top-level `truncated` (lane-level only). (b) WP-16a `TracingBucket` requires `errors` (and optional `p50Ms/p95Ms`); the SQL/IIS buckets are `{start,end,count,clipped,complete}` with no `errors`, and WP-17a emits `{start,count,errors}`. (c) `TracingSourceRef` expects `kind/readKind/window`, the server sends `{sourceId,label}`. (d) `requestSeq` is required client-side, optional server-side (WP-14 echoes only when sent; fine, but WP-16a must default it). — Fix: pick one list and write it in WP-14 as the single contract; recommended: client derives top-level `truncated` as `lanes.some(l => l.truncated)`, reads `disabled` from the lane, treats `errors` as optional (default 0, SQL/IIS lanes have no 5xx), `TracingSourceRef` reduced to `{sourceId,label}`, `requestSeq` optional with a client-side default of the sent seq.

[MAJOR] WP-17a Changes 3 — WP-17a says "find the audit-lane hook left by WP-14/WP-15 (a placeholder or TODO)" and STOP if none exists. No packet (WP-14, 15, 18b) defines such a hook; WP-18b only says WP-17a "adds a lane and calls `finishTracingMeta` again". A workhorse model will stop. — Fix: WP-18b (or WP-14) names the exact seam: `func (h *handler) auditLane(ctx, ids []string, from, to time.Time, width time.Duration) tracingLane` returning a not-read lane (`state:"none-seen"`) that WP-17a replaces, or change WP-17a to say "append the audit lane before the SQL lane in the lanes slice, then call `finishTracingMeta`". Same check on the lane order `audit, sql, iis`.

[MAJOR] WP-18b `TestTracingUnselectedImportNotCounted` — `c.Events` filters by importer name only (EventQuery has `Sources` = importer names, no import id; eventFilterAs IN-list at sqlite/case.go ~1104-1131). Events of an unselected import with the same importer name still appear in `events[]` while bucket counts (by ImportID) exclude them, so "adds nothing to counts or events" is false for shared names. — Fix: make the test use a different importer name for the unselected import, and add one line to the packet: "two imports with the same importer name cannot be separated in `events[]`; counts are exact, the list may include the sibling's events (documented limitation)". A real fix needs an import filter in EventQuery, which WP-18a forbids.

[MAJOR] phase.md is stale against plan.md §3 and the packets: the phase table lists WP-19 as wave 4 with deps 18b (plan/packet: wave 5, 18b+17a); WP-20 deps 45,02 (plan/packet: wave 6, 45,02,03,05); the overlap note says "WP-17 wave 3" (plan: WP-17a wave 4); the "missing WP-18 -> WP-13 edge" escalation is fixed in plan.md (18b depends on 15,13,18a). — Fix: update phase.md table, overlap note and escalation list to match plan.md §3.

## MINOR

[MINOR] WP-13 step 3 — uses a type `laneKey` that does not exist; the "unknown importer" sentence is moot because every source has a `validated` entry. — Fix: write `map[string][]SourceRef` and delete the unknown-importer sentence.

[MINOR] WP-14 — "writeJSON 200" omits the required argument: the real signature is `writeJSON(w, status, value any, maxBytes int)` (server.go:843); call_trace.go uses `maxResponseBytes`. `openCase` already calls `caseIDOf`, so the packet's separate `caseIDOf` step is redundant. — Fix: say `writeJSON(w, http.StatusOK, resp, maxResponseBytes)` and drop the duplicate step.

[MINOR] WP-18a tests — memory_test.go holds only contract tests; the fallback test should use the `genCase` helper in internal/casestore/series_test.go (package `casestore_test`) and `casestore.NewMemoryWith`. `mustImport` imports only under the single importer `test.csv`, so the two-importer test needs a new helper (events must carry `Source == meta.Importer`). `eventFilterAs` IN-list is at ~1104-1131, not exactly 1129. — Fix: name those helpers and the new helper in the packet.

[MINOR] WP-20 tests — `w3cFile(3)` rows all carry time-taken; the dash row is only `evs[2]` of the `w3c_good.log` fixture. — Fix: name the fixture for the dash assertion and give the exact extra row, e.g. `"2001-03-01 00:00:00 WEB-A GET /x 200 5000"`.

[MINOR] WP-18b — unlike `/call-trace` there is no 30-minute lookback, so an event that started before the window but overlaps it is neither listed nor counted. — Fix: state "start-based, half-open, no lookback" as deliberate (check against spec 5.x) in the Changes. Also state that the rowcap test needs the sqlite backend to prove the GROUP BY path.

[MINOR] Size — WP-18b (source selection, two lanes, buckets, clock-unchecked, ambiguity, meta, 7 tests) and WP-14 sit at the top of M; WP-19 spans two packages (importer/iis and calltrace). Acceptable, but consider moving `finishTracingMeta` + its tests out of WP-18b if a run overflows.

## Verified, no problems

- WP-12: calltrace signatures (`LaneEvent`, `Lane`, `MaxLaneEvents = 60`, `SourceInfo`, `BuildLanes`, `laneEvents` single caller, `EventLabel`), test anchors (`t0`, `ev`, `laneByKey`, `fp`, `TestEventLabel`, `TestBuildLanesSeverityAllowlist`).
- routes.go anchors (/call-trace and /investigation entries, `caseSubpath`), `writeCaseFailure`, `writeStoreError`, `decodeJSON`, `requestTimeout`.
- WP-18a: sqlite schema (`events` columns, `ev_ts`, `ev_kind_ts`, `ev_import`), `Each`/`ErrScanBudget`/`MaxScanEvents`/`MaxScanTime`, `MaxSeriesBuckets = 2000`.
- WP-19/WP-20: w3c.go (`w3cTimeIsEnd`, `w3cRow` start shift), httperr.go (`httperrReasons`, `AllowedAttrs`, `httperrRow` Start==End), iis_test.go anchors.
- Security/privacy: INV-3, INV-4, INV-5 respected in all nine packets; no raw cookie, IP, query string or UA; no hard timing assertions.
- Dependency edges: no other missing or invented edge found beyond phase.md staleness.

## Not checked (time)

audit/models.go const block, demo_cases.go internals (WP-25 anchors), LintCausal word list against HTTP.sys labels, routes_test.go, phase 01 overlap on w3c.go/httperr.go.
