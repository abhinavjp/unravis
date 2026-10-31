# Owner decisions, round 6

## Read this first

1. The round-6 external review produced nine findings (R6-01..R6-09); the delegated decision document `inputs/DEC9-rulings.md` gives every one of them an **owner change of "No"** — all nine are repairs to the specification's own wording inside already-locked architecture, not new owner choices.
2. The specification (`inputs/MERGED-SPEC.md`, §12.1) already follows each ruling as a *delegated* ruling, so nothing needs your signature for the build to be internally consistent.
3. The two optional confirmations, R6-02 and R6-03, were **approved by the owner on 2026-10-31**; their §12.1 rows are now APPROVED and no row is OPEN.
4. Nothing is committed and nothing is started: no build-out files are committed, no work-packet chain has begun, the wireframe has not been revised, and no release candidate has been cut.

## Optional confirmation 1 — R6-02: keep the UTC floor-aligned axis

**Question:** Should the UTC floor-aligned presentation axis ever be replaced by a request-anchored axis; or should the aligned axis be kept, with a coarser rung on off-hour windows?

**Recommended answer:** Keep the UTC floor-aligned axis with the coarser-rung rule. The one-sentence rule: there is exactly one presentation axis — the UTC floor-aligned, never-re-anchored grid (`axisStart = floorUTC(start, 3600000)`, `axisEnd = ceilUTC(end, 3600000)`) with half-open buckets intersected with `[start, end)` — and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung whose aligned bucket count is at most 120.

**Alternatives:**
- *Replace the axis with a request-anchored one* (`[start+kW, start+(k+1)W) ∩ [start,end)`). Cost: it reopens the owner-approved R5-03/R4-001 alignment decision, changes every acceptance case a second time, and (per `inputs/RES9-A.md`) fragments the rollup path into up to 239 raw fragments for a 30-day off-hour window, forfeiting the two-sliver performance contract. Rejected by DEC9.
- *Split the design:* request-anchored windows for short rungs, aligned plus coarsening for long rungs (RES9-A's option (b)). Cost: a visible behavioural seam at the 24-hour/1-hour-rung boundary, so a 24 h + 1 s window is aligned while an exactly-24 h window is not; that seam would need documenting and testing.

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 (row R6-02), §4.5, §7, TR-06, WP-15, SC-API-09/10/220/260/286; `inputs/DEC9-rulings.md` Ruling 2 and its Consolidated owner sign-off; `inputs/ADV-REVIEW6.md` R6-02; `inputs/RES9-A.md` (§3 aligned-count table, §5 option comparison, §7 recommendation).

**Consequences if the owner agrees:** R5-03/R4-001 stay in force unchanged; the contradictory short-window "60/96 buckets" prose is deleted and the acceptance cases are rewritten to be axis-true, so a 1-hour window starting at 10:07 genuinely admits 60-second buckets at 120 aligned buckets, exactly-2-hours windows may be forced off 60 seconds when the aligned count would exceed 120, and an off-hour 30-day window may display 8-hour buckets (91 aligned buckets); the user-facing promise of at most 120 buckets, 30 days served, and at most two boundary slivers is preserved.

**Consequences if the owner disagrees:** a new, explicitly labelled owner decision replaces the axis; DEC9's Ruling 2 is superseded; every acceptance case is re-baselined a second time; and off-hour-window bucket counts and the 30-day presentation guarantee are re-decided rather than merely relabelled.

**Approval status: APPROVED by the owner on 2026-10-31 (recommended answer confirmed)**

## Optional confirmation 2 — R6-03: refuse a partly overlapping pull with 409 window-overlap

**Question:** Should an MVP-2 changed pull over a partially overlapping stored window return 409 window-overlap, segment the interval, or keep superseded observations queryable?

**Recommended answer:** Refuse the partly overlapping pull with 409 window-overlap and replace only an exact-window match. The one-sentence rule: a live pull is an observation scoped by `(source, lane, fingerprint, window)`, where `window` is the exact half-open interval requested; a changed pull whose window exactly equals a stored complete observation's window replaces it atomically in one transaction under the existing import path and cascade machinery, and a changed pull whose window does not exactly equal a stored observation's window is refused with `409 window-overlap; re-pull the exact stored window or import a disjoint window`.

**Alternatives:**
- *Interval segmentation* (split the request into the parts that overlap the stored window and the parts that do not). Cost: the most complex of the three; it needs a segmentation algorithm, extra coverage rows, and re-derivation of rollup buckets across the split boundaries before MVP-2 can start.
- *Keep superseded observations queryable* (a versioned, append-only history with query-time dedup). Cost: every rollup and trace read must deduplicate at query time, and no locked decision requires forensic retention of superseded pulls — DEC9 notes this would be a new owner decision if you want it.

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 (row R6-03), §4.5, §5.3, TR-26, TR-33, WP-26, WP-27; `inputs/DEC9-rulings.md` Ruling 3 and its Consolidated owner sign-off; `inputs/ADV-REVIEW6.md` R6-03; `inputs/RES9-B.md` (§2: the events schema has no `active` column, rollup rebuild is literally `FROM events`, and the existing dedup is import-level file SHA-256, not `(source, _id)`).

**Consequences if the owner agrees:** a changed pull over the *same* exact window replaces the prior observation (stage and validate, delete the superseded import and its cascaded events/coverage, insert the new import and events, rebuild affected rollup buckets, commit), so overlap can never double-count; a partly overlapping pull fails loudly with a named remedy and no rows change; file imports are unaffected because their `observation_key` is NULL.

**Consequences if the owner disagrees:** partial overlap is silently unioned and double-counts overlapping rows in every report built on rollups, or superseded versions stay queryable and force query-time deduplication on every read — either way the round-6 defect returns, and choosing the history alternative is itself a new owner decision.

**Approval status: APPROVED by the owner on 2026-10-31 (recommended answer confirmed)**

## Delegated rulings (no owner change)

| id | one-line ruling | owner change |
|---|---|---|
| R6-01 | The content digest is widened to a versioned, canonical, observed row-content digest over the ordered observed rows, so two pulls differing only in `durationMs`/`status`/`value`/`severity`/`attrs` are no longer wrongly called unchanged. | No |
| R6-02 | The UTC floor-aligned presentation axis is kept; the presented width is the coarser of the span-nominal ladder rung and the aligned rung needed to keep the aligned bucket count at most 120. | No |
| R6-03 | A changed pull atomically replaces a stored observation of the *same exact window*; a partially overlapping pull is refused with 409 window-overlap instead of being unioned. | No |
| R6-04 | Raw boundary scans are "zero, one, or two" distinct fragments (zero when aligned), not "exactly two"; the edge budget applies per fragment. | No |
| R6-05 | Waves are generated from the authoritative `Depends on` table by a checked-in topological-level algorithm; the inline §9.3 edge list is a generated artifact, never hand-maintained. | No |
| R6-06 | `allLanesStored` exists only on `POST .../tracing` responses; coverage GET returns coverage-specific ledger provenance and an empty ledger reports `state:"no-coverage"`. | No |
| R6-07 | `complete:true` means only "the read command returned within budget"; a separate `consistency` label plus `mixedConsistency` and a non-dismissible UI note prevents live and saved lanes being read as one snapshot. | No |
| R6-08 | WP-65a owns only the revision primitive and its storage tests; a new WP-65b owns every mutation-site wiring with named symbols and before/after assertions. | No |
| R6-09 | The release-gate required test-ID set is generated from the accepted scenario inventory and checked in both directions (no missing IDs, no orphans), with baseline deletions reported. | No |

## Needs your OK

Nothing. Both optional confirmations (R6-02 aligned axis, R6-03 409 window-overlap) were approved on 2026-10-31.

## Not started

Each of the following is a separate not-started item — none has been committed or begun:

- Commit the build-out files.
- Start the WP implementation chain.
- Revise the wireframe.
- Cut `v0.1.0-rc.6`.

This document records recommended answers based on the cited rulings and evidence; it does not assert that the repairs are guaranteed correct, and DEC9 requires every repair to be proven by its listed tests before packet execution proceeds.