# Owner decisions — Round 5 (recommended answers)

## Read this first

1. This document asks you to confirm, refine or reverse ten delegated choices; only you can make them final, and every block below is still open.
2. The recommended answers are the Round-5 rulings (DEC8); where a ruling changes a choice you made earlier, a separate block says so explicitly.
3. "Win32" below means the IIS Win32 error code recorded in IIS logs; other jargon is explained on first use where it appears.
4. Nothing here is a guarantee of correctness: the arithmetic and code facts come from research notes and a checkout that was read, not built or run.

---

## R3-001 — Export confirm binding

**Question:** Should export confirm be stateless and bound only to the case revision, instead of a one-shot token that expires?

**Recommended answer:** CONFIRM the earlier choice, with a REFINE of the guarantee — stateless, revision-bound confirm stands; preview and confirm recompute freely and 409 (stale) is the only error; the promise is "same case revision and same scrub policy class", never byte-identical output.

**Alternatives:**
- (a) Keep the older one-shot token with expiry and a 404 for an unknown preview: costs two same-revision previews colliding and replay/expiry state to store and test (per §12.1).
- (b) Advertise byte-identical confirm output: costs an unattainable claim, because each export uses a fresh pseudonym key and its own creation time (RES8-C, ruling 4).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R3-001 row and §5.2; DEC8 ruling 4; code facts in `inputs/RES8-C.md` (`internal/server/case_views.go`, `internal/evidence/caseexport.go`, `internal/evidence/build.go`).

**Consequences if the owner agrees:** the spec stops implying a byte-level guarantee; confirm remains stateless and simple; optional input-level manifest digest can be added later for audit.

**Consequences if the owner disagrees:** the one-shot/expiry/404 machinery returns, or an audit claim is made that the code cannot honour.

**Approval status:** PENDING OWNER APPROVAL

---

## R3-004 — Clipped vs complete

**Question:** Should presentation clipping and missing data remain two separate flags?

**Recommended answer:** CONFIRM — keep separate `clipped` (geometry: the bucket sticks out past the requested window) and `complete` (data: the underlying read finished) flags; only the grid anchoring changes so the "two edge slivers" promise is actually true.

**Alternatives:**
- (a) One `partial` label for both meanings: costs losing the ability to tell a geometric edge from missing or unfinished data (§12.1).
- (b) Keep the flags but keep re-anchoring: costs the two-sliver promise, because a re-anchored off-hour grid produces slivers at every bucket, not just the two window edges (RES8-B, finding F-3).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R3-004 row, §4.5, §7; DEC8 rulings 3 and 9; arithmetic in `inputs/RES8-B.md` (240 fragments vs 2).

**Consequences if the owner agrees:** edge geometry and data gaps stay distinguishable; the fix is confined to how the bucket count is held at or below 120.

**Consequences if the owner disagrees:** the old partial wording returns and users cannot tell a clipped edge from a real gap.

**Approval status:** PENDING OWNER APPROVAL

---

## R3-010 — Duration arithmetic and counts

**Question:** Should durations be integer microseconds with separate `count_all` and `count_with_duration`?

**Recommended answer:** CONFIRM — integer microseconds via `toMicros`, with `count_all` and `count_with_duration` separate and `min_us`/`max_us` integers; the invalid-duration side of R4-009 is reversed separately (see R4-009 block).

**Alternatives:**
- (a) Return to float `min_ms`/`max_ms` and one count as denominator: costs the exactness integer arithmetic provides and reintroduces denominator ambiguity (§12.1).
- (b) Keep integer counts but reject bad durations: rejected, because that is the R4-009 change handled below.

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R3-010 row, §4.5, §7, `AC-20`; DEC8 ruling 9; arithmetic in `inputs/RES8-D.md` (`toMicros(1.5)=1500`, `toMicros(1.4999)=1500`).

**Consequences if the owner agrees:** rollups stay bit-exact in integer arithmetic and the invalid-duration fix cannot move the totals.

**Consequences if the owner disagrees:** float rounding returns and a single count must explain itself.

**Approval status:** PENDING OWNER APPROVAL

---

## R3-011 — Server de-duplication

**Question:** Should MVP-1 keep server-side de-duplication of identical in-flight reads with a subscriber count?

**Recommended answer:** REVERSE for MVP-1 — keep only per-source concurrency bounds, request-context cancellation, and the client-only per-tab request sequence; there is no server work key, no subscriber count and no shared in-flight read.

**Alternatives:**
- (a) Keep de-duplication with a canonical key encoding: costs refcount and cancellation races, an unmeasured benefit, and a key-encoding contract the spec never defines (RES8-D, F6; ADV-REVIEW5 R5-11).
- (b) Keep de-duplication as specified: costs the same, plus tests for subscriber detach that exist only to support the shared read.

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R3-011 row, §5.1, §6, WP-14; DEC8 ruling 11 and Owner sign-off; `inputs/RES8-D.md` F6 (`grep serverWorkId` empty; client `requestSeq` in `web/src/lib/appState.svelte.ts`); `inputs/RES8-A.md` §1.5 (per-source semaphore).

**Consequences if the owner agrees:** two tabs with the same query each pay one bounded live read; the highest-complexity new state in MVP-1 disappears.

**Consequences if the owner disagrees:** the server work key, subscriber refcount and their races and tests stay.

**Approval status:** PENDING OWNER APPROVAL

---

## R3-013 — Canonical packet detail

**Question:** Should this document's Appendix P be the only canonical packet detail?

**Recommended answer:** CONFIRM — Appendix P is canonical and self-contained, with the 60 s writer in WP-30 and the 3600 s writer in WP-31; the retired `BroadScanBuckets` constants are deleted rather than kept as a second normative source.

**Alternatives:**
- (a) Keep an external packet file as a second normative source: costs cross-document drift and the need to verify ID and link agreement in two places (§12.1).
- (b) Keep the retired constants listed externally: costs two competing contracts, since the code's real constants are `MaxRollupGroups`, `CensusQueryBudget` and `PerSourceConcurrency` (RES8-A §7).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R3-013 row, §9.1, §9.4, Appendix P; DEC8 rulings 7 and 12; `inputs/RES8-A.md` §1.7 and §7.

**Consequences if the owner agrees:** one source of truth; automated ID/link checks gate it.

**Consequences if the owner disagrees:** two normative packet sources must be kept in sync by hand.

**Approval status:** PENDING OWNER APPROVAL

---

## R4-001 — Bucket grid for long windows

**Question:** Should long windows re-anchor the presentation grid to keep exactly 120 buckets?

**Recommended answer:** REFINE — keep the observable promises (at most 120 buckets, a 30-day window always served, exactly two raw edge slivers) but replace re-anchoring with a coarser UTC-aligned rung chosen from `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]`.

**Alternatives:**
- (a) Allow 121 buckets: costs the hard `≤120` contract in `AC-03`/`TR-06` while keeping 6 h at 30 days (RES8-B, option b).
- (b) Keep re-anchoring and decompose every bucket into hour pieces: costs 130–840 raw scans instead of 2, and there is no defined per-edge budget for that (RES8-B, option d).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R4-001 row, §4.5, §7, §4.7; DEC8 ruling 3 and Owner sign-off; `inputs/RES8-B.md` tables (a)–(d) and F-1/F-3/F-4.

**Consequences if the owner agrees:** a 30-day window starting off the hour may show 8 h bars where you expected 6 h; the window is still served.

**Consequences if the owner disagrees:** either the 121-bucket contract breaks, or the two-sliver promise is silently false.

**Approval status:** PENDING OWNER APPROVAL

---

## R4-004 — Owner of `case.revision`

**Question:** Should a new MVP-1 packet WP-65 own the `case.revision` writes and the confirm snapshot?

**Recommended answer:** CONFIRM, with a split — WP-65a is the `case.revision` primitive (no export dependency); WP-65 remains the confirm integration and depends on WP-65a, WP-06 and WP-07.

**Alternatives:**
- (a) Move WP-65 earlier without splitting: costs leaving WP-06/07 tests able to pass before the primitive exists (RES8-C, Q3).
- (b) Leave counter ownership to WP-06/07: costs missed source and policy mutations, so a stale preview can still be accepted (§12.1).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R4-004 row, §9.2/§9.3, WP-06, WP-07, WP-65; DEC8 ruling 5 and Owner sign-off; edge analysis in `inputs/RES8-C.md` Q3.

**Consequences if the owner agrees:** the encoded dependency cycle is removed; no export packet can claim the snapshot invariant before the counter exists.

**Consequences if the owner disagrees:** the cycle stays, or the counter's ownership is unclear at packet boundaries.

**Approval status:** PENDING OWNER APPROVAL

---

## R4-005 — `bypassLedger` and `fromLedger`

**Question:** Should the `bypassLedger` and `fromLedger` concepts be dropped?

**Recommended answer:** CONFIRM — `bypassLedger` and `fromLedger` stay deleted, and the top-level `fromStored` (a single yes/no saying "these results came from saved data") is narrowed to `allLanesStored`, true only when every lane is saved.

**Alternatives:**
- (a) Keep the ledger-bypass bit: costs reporting false provenance in GET coverage and POST tracing (§12.1).
- (b) Keep one top-level `fromStored`: costs hiding which lanes were live, because a trace can mix a live audit lane with saved IIS/SQL lanes (RES8-A, R5-08).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R4-005 row, §4.3, §5.1, §5.3, WP-14, WP-34; DEC8 ruling 8; per-lane provenance analysis in `inputs/RES8-A.md` §6 and `inputs/RES8-D.md`.

**Consequences if the owner agrees:** each lane reports its own origin, time and completeness; the single field means what it says.

**Consequences if the owner disagrees:** mixed provenance stays hidden behind one boolean.

**Approval status:** PENDING OWNER APPROVAL

---

## R4-009 — Invalid duration at import

**Question:** Should a bad duration reject the whole event at import?

**Recommended answer:** REVERSE — keep the event with no duration and `durationStatus:"invalid"`; it increments `count_all` and not `count_with_duration`, no invalid number is ever stored, and only records that cannot be safely identified are quarantined.

**Alternatives:**
- (a) Reject the whole event (the earlier R4-009 choice): costs losing the event from the fault timeline, and in code it aborts the whole file with no quarantine row (RES8-D, F1/F2).
- (b) File-level quarantine: costs the same file-level loss and misuses a table that records refusals, not warnings (RES8-D, option 3).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R4-009 row, §4.5, §7, WP-30; DEC8 ruling 9 and Owner sign-off; `inputs/RES8-D.md` F1–F3 and arithmetic (overflow threshold ≈292,471 years; missing-duration case already blessed).

**Consequences if the owner agrees:** a corrupt-duration event stays visible and filterable with no timing, and `count_all` includes it.

**Consequences if the owner disagrees:** one bad duration can discard every sibling event in its file with no record.

**Approval status:** PENDING OWNER APPROVAL

---

## R4-010 — Bounded audit read

**Question:** Should the 30-day audit broad-scan path carry the proposed explicit limits?

**Recommended answer:** REFINE — the bounded audit read stands, realised as one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot.

**Alternatives:**
- (a) Keep `BroadScanBuckets` paging with a fixed page-one high-water mark: costs up to 7,200,000 rows over the wire and a page cap that can falsely report `complete` (RES8-A §2, option 2).
- (b) Unbounded 30-day scan: costs the named page, time and concurrency bounds (§12.1).

**Evidence:** `inputs/MERGED-SPEC.md` §12.1 R4-010 row, §5.1, §5.4, §7, WP-17; DEC8 ruling 7; `inputs/RES8-A.md` §1.2/§2/§4 (23,280 lane groups vs `MaxRollupGroups=600000`; code already groups server-side).

**Consequences if the owner agrees:** counts come from one bounded server-side command; the spec's own §5.1 sentence is honoured.

**Consequences if the owner disagrees:** the audit lane scans a 30-day span with no named bound, or ships raw rows to rebuild buckets client-side.

**Approval status:** PENDING OWNER APPROVAL

---

# Blocks for rulings that change an earlier owner choice

## R5-03 (changes your R4-001 re-anchor choice)

**Question:** R4-001 told the grid to re-anchor to `[start,end)`; should that be replaced by a coarser UTC-aligned rung?

**Recommended answer:** REFINE — delete re-anchoring; keep the axis UTC floor-aligned and step to the next coarser rung so the aligned count is at most 120 and exactly two raw edge slivers are scanned.

**Alternatives:**
- (a) Allow 121 aligned buckets: costs the `≤120` contract (RES8-B, option b).
- (b) Per-bucket sub-hour decomposition: costs up to 840 raw scans with no per-edge budget (RES8-B, option d).

**Evidence:** DEC8 ruling 3 and Owner sign-off; `inputs/RES8-B.md` tables (a)–(d), F-3/F-4; `inputs/MERGED-SPEC.md` §4.5, §7, `EV-ADV-01`.

**Consequences if the owner agrees:** at an off-hour 30-day start the disclosed bucket size may be 8 h; the window is still served.

**Consequences if the owner disagrees:** the two-sliver promise remains false off the hour, or the bucket cap breaks.

**Approval status:** PENDING OWNER APPROVAL

---

## R5-09 (changes your R4-009 reject-whole-event choice)

**Question:** R4-009 told the importer to reject a bad-duration event; should it keep the event instead?

**Recommended answer:** REVERSE — keep the event with `durationStatus:"invalid"` and null duration; in code the current rule throws away the whole file with no quarantine row.

**Alternatives:**
- (a) Keep-with-invalid (recommended): costs a visible corrupt-duration event and `count_all` including it.
- (b) Reject whole event (R4-009): costs file-level loss of sibling events with no record.
- (c) File-level quarantine: costs the same loss and confuses refusal with quality warnings.

**Evidence:** DEC8 Owner sign-off; `inputs/RES8-D.md` F1–F3 and arithmetic; `inputs/MERGED-SPEC.md` §4.5, TR-34, WP-30.

**Consequences if the owner agrees:** the fault timeline keeps the event; totals stay exact because only `count_all` moves.

**Consequences if the owner disagrees:** a single bad duration can erase a file's evidence silently.

**Approval status:** PENDING OWNER APPROVAL

---

## R5-11 (changes your R3-011 shared-read choice)

**Question:** R3-011 told MVP-1 to keep server de-duplication with a subscriber count; should it be removed?

**Recommended answer:** REVERSE for MVP-1 — keep the per-source semaphore (proposed 2 concurrent reads), per-request cancellation and the client-only per-tab request sequence; drop the server work key and subscriber count.

**Alternatives:**
- (a) Remove dedup (recommended): costs two tabs each paying one bounded live read.
- (b) Keep dedup with a canonical key encoding: costs refcount/cancellation races and an encoding contract, for no measured gain.

**Evidence:** DEC8 ruling 11 and Owner sign-off; `inputs/RES8-D.md` F6; `inputs/ADV-REVIEW5.md` R5-11; client `requestSeq` in `web/src/lib/appState.svelte.ts`.

**Consequences if the owner agrees:** MVP-1 loses the most complex new state; the cost is a bounded duplicate read in a single-user local app.

**Consequences if the owner disagrees:** subscriber refcounts, a key-encoding contract and their race tests stay in scope.

**Approval status:** PENDING OWNER APPROVAL

---

## R5-01 (refines your R4-002 read-only intent)

**Question:** R4-002 allowed read-only audit reads and made pulls the only persisting action; should that be split into three named operations?

**Recommended answer:** REFINE — `POST /tracing` may run one ephemeral read-only live read for the audit lane and never persists; `GET /coverage` reads the case database only and reports gaps without filling them; `POST /pulls` is the only operation that reads remote SQL/Mongo and persists.

**Alternatives:**
- (a) Three operations (recommended): costs one wording change across §5.1/§5.3/§5.4 and WP-14/17/26/34.
- (b) One shared caching operation: costs pretending the three endpoints behave the same and keeps the current self-contradiction.

**Evidence:** DEC8 ruling 1 and Owner sign-off; `inputs/ADV-REVIEW5.md` R5-01; `inputs/RES8-A.md` §6 (the code already separates these); `inputs/MERGED-SPEC.md` §5.1/§5.3.

**Consequences if the owner agrees:** the contradiction is resolved and each lane carries `readKind` (`saved`/`live`) and an `asOf` time.

**Consequences if the owner disagrees:** the same paragraph keeps saying the tracing button both may and may not read the source.

**Approval status:** PENDING OWNER APPROVAL

---

## R5-04 (wording choice, plus optional manifest)

**Question:** Should the export confirm promise be pinned to snapshot plus scrub class, and should an input-level manifest digest be added?

**Recommended answer:** REFINE — state "confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output"; the export-manifest digest (hashing inputs, excluding the creation time and per-export key) is optional owner discretion.

**Alternatives:**
- (a) Manifest digest (optional): costs extra build and tests, but gives an input-level audit trail.
- (b) Byte-identical claim: costs an unattainable promise without a stable key and frozen timestamp, and would change the privacy contract (RES8-C, Q1).

**Evidence:** DEC8 ruling 4 and Owner sign-off; `inputs/RES8-C.md` Q1; code facts in `internal/server/case_views.go`, `internal/evidence/caseexport.go`, `internal/evidence/build.go`.

**Consequences if the owner agrees:** the spec promise matches what the code can do.

**Consequences if the owner disagrees:** the spec keeps implying identical bytes that fresh keys and timestamps make impossible.

**Approval status:** PENDING OWNER APPROVAL

---

## Needs your OK

Default if you say nothing: the recommended answer is **not** applied as an owner decision; the spec already follows it as a delegated ruling.

| id | one-line change | default if you say nothing |
|---|---|---|
| R3-001 | Confirm stateless revision-bound confirm; guarantee is snapshot + scrub class, not bytes | Applied as delegated ruling only; not an owner decision |
| R3-004 | Separate `clipped` and `complete` flags; grid anchoring fixed so two edge slivers is true | Applied as delegated ruling only; not an owner decision |
| R3-010 | Integer microseconds; separate `count_all` and `count_with_duration` | Applied as delegated ruling only; not an owner decision |
| R3-011 | Drop server `serverWorkId`/subscriber dedup for MVP-1 | Applied as delegated ruling only; not an owner decision |
| R3-013 | Appendix P canonical and self-contained; retired constants deleted | Applied as delegated ruling only; not an owner decision |
| R4-001 | Replace grid re-anchor with a coarser UTC-aligned rung (≤120 buckets) | Applied as delegated ruling only; not an owner decision |
| R4-004 | Split `case.revision` into WP-65a (primitive) + WP-65 (integration) | Applied as delegated ruling only; not an owner decision |
| R4-005 | Keep `bypassLedger`/`fromLedger` deleted; narrow `fromStored` to `allLanesStored` | Applied as delegated ruling only; not an owner decision |
| R4-009 | Keep invalid-duration event with `durationStatus:"invalid"`, null duration | Applied as delegated ruling only; not an owner decision |
| R4-010 | One server-side `$match`+`$group` per source; retire `BroadScanBuckets` paging | Applied as delegated ruling only; not an owner decision |
| R5-03 | Delete re-anchoring; use coarser UTC-aligned rung (refines R4-001) | Applied as delegated ruling only; not an owner decision |
| R5-09 | Reverse R4-009: keep event, not reject (owner sign-off) | Applied as delegated ruling only; not an owner decision |
| R5-11 | Reverse R3-011 for MVP-1: remove dedup (owner sign-off) | Applied as delegated ruling only; not an owner decision |
| R5-01 | Split tracing/coverage/pulls into three explicit read kinds (refines R4-002) | Applied as delegated ruling only; not an owner decision |
| R5-04 | Pin export guarantee to revision + scrub class; manifest optional | Applied as delegated ruling only; not an owner decision |
