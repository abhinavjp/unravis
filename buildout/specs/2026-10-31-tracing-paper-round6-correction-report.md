# Round-6 correction report — Tracing Paper spec

## Scope

- Input spec: round-5 baseline `2026-10-30-tracing-paper-impl-spec(5).md` (4,481 lines), reviewed as `inputs/ADV-REVIEW6.md`.
- Output spec: revised `buildout/specs/2026-10-30-tracing-paper-impl-spec.md` (4,620 lines), round-6 corrected and independently reviewed once. Spec line numbers quoted in the table below refer to the pre-review draft (4,620 lines) and may have shifted by a few lines.
- Rulings: 9 (R6-01..R6-09), all binding in `inputs/DEC9-rulings.md`.
- Patches applied: 149 text patches from the ruling passes (`PATCHES9.json`, counted) plus 39 patches from the independent-review fix pass (`RF9_M1.json`, `RF9_M2.json`, counted), 188 in total, plus 4 small closure edits after the re-check (see Independent review).
- Per-finding patch counts (counted from the `finding` field; a patch naming several findings counts once per finding, so these sum to 167 over 149 entries): R6-01 = 30, R6-02 = 28, R6-03 = 19, R6-04 = 23, R6-05 = 1, R6-06 = 29, R6-07 = 16, R6-08 = 4, R6-09 = 17.

## Corrections table

| id | severity (ADV-REVIEW6) | root cause | change made | spec places changed | status |
|---|---|---|---|---|---|
| R6-01 | P0 | Digest preimage was the ordered `(timestamp,_id)` row stream, which omits `durationMs`/`status`/`value`/`severity`/attrs, so changed evidence read as `unchanged`. | Digest is a versioned canonical row-content digest over observed rows (`content_digest_v`); observed-content equality, not post-projection DB state. | Glossary "content digest" (line 37); §5.3; TR-26; WP-26, WP-28, WP-34; Appendix P P6; EV-API-245/264/282; EV-ADV-260; SC-ADV-01/02; SC-API-300; schema `coverage.content_digest_v` | Applied in spec; runtime NOT verified |
| R6-02 | P0 | UTC floor-aligned grid asserted together with span-based "60/96 bucket" ladder statements that are false for unaligned windows, a contradiction inside the short-window rung table. | One presentation axis kept; presented width `max(ladderWidth(span), alignedRung(axisStart,axisEnd))`; acceptance cases rewritten axis-true. | §4.5 (167); §7 (240); TR-06 (255), TR-34 (281); WP-15 (310), WP-33; SC-API-09/10/220/260/286; SC-API-303; EV-API-220/260/286; EV-API-04/34; EV-UI-04/06; AC-03/AC-10; WS-26/WS-280; EV-XC-14; glossary "coarser rung" | Applied in spec; runtime NOT verified |
| R6-03 | P0 | A changed pull persisted a new import with no supersession, so both versions' rows stayed active and double-counted rollups. | Whole-observation atomic replacement scoped by `(source,lane,fingerprint,window)`; partial overlap is `409 window-overlap`. | §5.3 (211); §4.5 (167); TR-26 (273), TR-33 (280); WP-26 (329), WP-27 (330), WP-30 (333), WP-31; Appendix P P6/P9; SC-ADV-01/02/162; EV-ADV-163; EV-API-245; EV-API-281; SC-API-302; schema `observation_key`, `supersedes_import_id` | Applied in spec; runtime NOT verified |
| R6-04 | P1 | "Exactly two raw edge slivers" is false for aligned windows (zero) and same-hour boundary pairs (one). | "Zero, one, or two distinct boundary fragments", zero when aligned; edge budget applies per fragment. | §4.5 (167); §7 (240); TR-06 (255), TR-34 (281); SC-ADV-16/17/18/120; EV-API-220/260/286; EV-ADV-01; AC-03; SC-API-304; EV-API-265; WS-26 | Applied in spec; runtime NOT verified |
| R6-05 | P1 | §9.3 inline edge list was a strict subset of the authoritative `Depends on` table, and handwritten waves placed dependents in the same wave as their dependencies. | `Depends on` table is the single normative graph; §9.3 edges and waves are generated (Kahn, tie-broken by WP id); generation fails on unknown ref, duplicate edge or cycle. | §9.2 (`Depends on` column, WP-06/07/65/65a, WP-58); §9.3 (369, 373+); §0.2; Appendix P global done-check | Applied in spec; runtime NOT verified |
| R6-06 | P1 | `allLanesStored` was applied to a coverage-array response where no lanes exist, giving vacuous truth on an empty array. | `allLanesStored` exists only on `POST .../tracing`; coverage GET returns `{entries,asOf,gaps,provenance:"case-ledger"}` with ledger-row provenance. | §5.3 (211); §5.1 (196/199); TR-33 (280); WP-34 (337); SC-PRV-184; EV-API-223/206; EV-PRV-184; EV-ADV-201/220/223/224; SC-API-302; M1/M2 coverage scenarios | Applied in spec; runtime NOT verified |
| R6-07 | P1 | `complete:true` meant command completion but was read as snapshot stability, so live and stored lanes could be compared as one timeline. | `complete` is execution completion only; each lane carries `consistency`; mixed traces set `mixedConsistency:true` with a non-dismissible UI note. | §5.1 (199), §7 (238); TR-04, TR-07, TR-10; WP-17; SC-API-49/50/51/52; EV-UI-140/221; SC-API-241/261/281; EV-API-241/261/281; SC-API-305; WS-221; EV-UI-280 | Applied in spec; runtime NOT verified |
| R6-08 | P2 | WP-65a claimed "called from every mutation site" while its file scope was only `case.go`, so it could pass with no callers. | WP-65a owns the primitive and storage tests only; new WP-65b owns every mutation-site wiring; WP-65 is confirm integration. | §9.2 WP-06 (301), WP-07 (302), WP-65a (326), WP-65 (327), new WP-65b; §5.2 (207); §4.6 (174); §9.3 generated edges/waves; Appendix P P2 | Applied in spec; runtime NOT verified |
| R6-09 | P2 | A checked-in manifest with "exactly one suite per ID" could pass even when a required ID was dropped from both suites and the manifest. | Required test-ID set is generated from the accepted scenario inventory; release fails unless both directions (coverage and orphan) and both reports hold. | §9.2 WP-62, WP-58; §9.3 (generated); §11; Appendix P global done-check; `tools/e2e/run-e2e.ps1`; SC-ARC-160/161/180; EV-XC-67/180/201/220/280 | Applied in spec; runtime NOT verified |

## Refinements of earlier rulings

- R4-001 / R5-03 (aligned axis, coarser rung): old rule — floor-aligned axis with the next coarser rung selected when the aligned count exceeds 120; new rule — keep the axis, presented width is `max(ladderWidth(span), alignedRung(axisStart,axisEnd))`. Preserved, not reopened.
- R5-06 / R4-007 (coverage fingerprint, content digest, outcome words): old rule — idempotent per window via an import sha256 over the `(timestamp,_id)` stream; new rule — canonical observed row-content digest, versioned by `content_digest_v`. Refined.
- R5-06 / R4-007 (changed pull persists a new import): old rule — changed pull adds a new import; new rule — atomic whole-observation replacement for the exact window, 409 on partial overlap. Refined.
- R5-05 / R4-004 (WP-65a/WP-65): old rule — WP-65a primitive plus WP-65 integration; new rule — WP-65a primitive, new WP-65b wiring, WP-65 confirm integration. Refined.
- R5-12 (test-ID manifest): old rule — manifest is the authority and each ID appears in exactly one suite; new rule — set is generated from the inventory with a reverse orphan check. Refined.
- DEC6 R3-004 / R3-009 (`clipped`/`complete`, `hasRawEdge`): old rule — two raw edge slivers; new rule — zero, one, or two boundary fragments; flags and budgets stand. Refined.
- DEC4 R6 (transactional rollup rebuild): old rule — rebuild from all active events; new rule — rebuild after supersession in the same transaction. Refined.
- No owner decision D-1..D-12 changed. The Consolidated owner sign-off table marks no ruling OWNER-CHANGING.

## Changed dependency edges

- Ruling 8 (new/changed WP edges): `WP-65b → WP-65a`; `WP-65 → WP-65a, WP-65b, WP-06, WP-07`; `WP-58` depends-on list adds `WP-65b`.
- Ruling 5 (graph is authoritative; waves generated): the `Depends on` table in §9.2 is the single normative graph; §9.3 edge list and waves are generated, never hand-maintained.
- New packet WP-65b: owns every mutation-site revision bump (import committed/deleted, source add/change/remove, cookie allowlist set, HMAC key set, exported settings change); generated waves place WP-65a in Wave 1 and WP-65b in Wave 2.
- §9.3 is now generated from the WP table by the checked-in topological-level tool (`tools/spec/genwaves`, proposed); generation fails on an unknown WP reference, a duplicate edge or a cycle.

## New fields and names (all marked proposed)

- `content_digest_v` — canonicalization version for the content digest (starting at 1), mirroring `fingerprint_v`; `coverage.content_digest_v INTEGER DEFAULT 1`.
- `observation_key` — canonical `(source,lane,fingerprint,window)` scope key on `imports`, `UNIQUE` for pull imports (NULL for file imports); a live pull is an observation scoped by this key.
- `supersedes_import_id` — `INTEGER NULL` on `imports`, recording which superseded import a replacement deleted.
- `consistency` — per-lane value in `{live-non-snapshot,stored-as-of,snapshot}`, defaulting to `live-non-snapshot` for the live audit lane and `stored-as-of` for saved SQL/IIS lanes.
- `mixedConsistency` — top-level tracing flag set true when any lane is `live-non-snapshot`; the UI shows a non-dismissible note that cross-lane totals are not a conservation check.
- window-overlap 409 — `409 window-overlap; re-pull the exact stored window or import a disjoint window`, returned when a changed pull's window does not exactly equal a stored observation's window.
- `MaxEdgeScanRows` / `MaxEdgeScanTime` — edge budget `50000` rows / `2s` applied per boundary fragment; the same-grain one-scan optimisation must not double the budget.
- WP-65b — new MVP-1 packet owning mutation-site revision wiring with before/after assertions.
- Removed or restricted: `allLanesStored` now exists only on `POST .../tracing` responses; it never appears on `GET .../coverage` (which returns `entries`/`asOf`/`gaps`/`provenance:"case-ledger"`).

## Independent review (one pass, one fix round)

- Reviewer lane: `cursor-composer-2-5`, three slices, whole-spec Phase A (contradictions) then Phase B (closure of R6-01..R6-09). Verdict: needs-fixes.
- 21 findings were grouped into nine items (W1..W9) and fixed by 39 patches (stale restatements of the boundary-scan count, the axis arithmetic, the coverage response shape and WP-65a/65b/65 scope; a few wording alignments). All applied cleanly; validator re-run: 0 problems.
- Judged cosmetic and left: the EV-UI-38 numbering gap (no reference points at it) and the abbreviated "zero, one, or two ..., zero when aligned" restatements (not contradictory).
- Closure re-check (same reviewer family, two slices): W3..W9 closed; W1 (TR-26 lacked the "Pull now always re-reads the source" clarification) and W2 (R4-001, SC-API-220/286 and EV-API-220/260/286 omitted the same-grain-hour single-scan case) were partly closed; one new P1 was found (section 12.1 intro said "nothing here is a frozen owner decision" next to "APPROVED rows are frozen").
- Those three leftovers were fixed with 4 small wording edits and the validator re-run (0 problems, waves unchanged). That last edit was NOT re-reviewed by an agent; only the structure validator checked it.

## Validation

- This is spec validation only: structure validator, stale-term scan, generated packet graph, and an independent review plus closure re-check. There is NO runtime verification.
- Validator numbers: 361 scenarios, 429 evaluation rows, 53 workstream rows, 67 work packets in 9 waves, 0 structural problems.
- No repository tests, SQL/Mongo, E2E suites or benchmarks were executed.

## Known limits

- R6-02: the aligned axis is an owner-taste choice; reconsidering it would be a new, explicitly labeled owner decision and is not taken or implied.
- R6-03: superseded observations are not kept queryable; forensic retention of superseded pulls is deferred to a future owner decision.
- R6-03: interval segmentation for partially overlapping windows is deferred (MVP-2 returns 409 window-overlap instead).
- R6-07: `snapshot` is an availability-gated capability only; no locked decision requires Mongo snapshot reads, and Mongo snapshot semantics need independent research before any specific consistency guarantee is claimed.
- R6-05: the wave generator tool (`tools/spec/genwaves`) is proposed and unverified for path/existence.
- VF-01 items remain open (IIS time-taken scope, W3C time end, HTTPERR `#Fields`, Mongo profiler enable, wait-stats cumulative, Query Store µs, live pull storage shape).
- Verdicts bind specification text and packet contracts; every repair must still be proven by the listed tests. This report does not claim guaranteed correctness.