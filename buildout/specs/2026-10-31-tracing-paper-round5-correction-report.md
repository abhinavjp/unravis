# Round 5 correction report — Unravis tracing paper

## Scope

- **Input spec:** `2026-10-30-tracing-paper-impl-spec(4).md` (4,222 lines), reviewed by ADV-REVIEW5.
- **Output spec:** `inputs/MERGED-SPEC.md` (4481 lines), the revised spec after applying all patches.
- **Rulings:** 13 (R5-01..R5-13) from `DEC8-rulings.md`.
- **Patches applied:** 177 entries in `inputs/PATCHES8.json` (pass 1 and the mop-up pass), followed by a final-fix pass of reviewer-specified patches and a last residual pass of 12 reviewer-specified one-line edits applied directly. Counted, not estimated.

## Corrections table

| id | severity (ADV-REVIEW5) | root cause | change made (canonical) | spec places changed | status |
|---|---|---|---|---|---|
| R5-01 | P0 | Same paragraph both permits live read and says "never re-reads a source"; coverage prose says gaps "are read". | "Three operations are distinct: `POST .../tracing` may run one ephemeral read-only live read ... never persists; `GET .../coverage` reads `case.db` only ... never fills them; `POST .../pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage." | §4.3, §5.1, §5.3, §5.4; TR-03, TR-13, TR-26; WP-14, WP-17, WP-26, WP-34; SC-API-206, SC-API-227, SC-ADV-21; EV-API-229; AC-19 | Applied in spec; runtime NOT verified |
| R5-02 | P0 | EV-UI-220 asserted `W = 21,600,500 ms` for a 30-day span; true value 2,592,000,000/120 = 21,600,000 ms. | "A 30-day span is 2,592,000,000 ms and `W = span/120 = 21,600,000 ms` (6 h nominal); every expected bucket width ... derived from integer time-boundary arithmetic." | §4.5, §7; TR-06; WP-15; SC-API-220; EV-API-220, EV-UI-220; AC-03, AC-09 | Applied in spec; runtime NOT verified |
| R5-03 | P0 | Re-anchored grid at off-hour starts split every bucket across absolute hours → 240 fragments, not 2. | "The presentation axis is UTC floor-aligned ... never re-anchored. When the aligned count exceeds 120 the next coarser supported rung is selected from `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]`." | §4.5, §4.7, §7; TR-06, TR-34; WP-15, WP-33; SC-API-245, SC-API-260; SC-ADV-16/17/18/120; EV-ADV-01, EV-ADV-221, EV-API-245, EV-UI-02, EV-UI-19, EV-XC-12, EV-XC-14, EV-XC-221; AC-03, AC-10; Glossary "re-anchored grid" | Applied in spec; runtime NOT verified |
| R5-04 | P0 | Confirm implied byte-identical re-derivation, but each export uses a fresh key and its own timestamp. | "Confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output." | §5.2, §12 R3-001 row; TR-21; WP-06, WP-07, WP-65; SC-PRV-1, SC-PRV-104, SC-PRV-182; EV-PRV-46, EV-PRV-182; Appendix P P2 | Applied in spec; runtime NOT verified |
| R5-05 | P1 | WP-65 owned `case.revision` but depended on WP-06/07, which claimed to use it → encoded capability cycle. | "The `case.revision` primitive is packet WP-65a (proposed), which has no dependency on the export packets; WP-65 remains the confirm-integration packet and depends on WP-65a, WP-06 and WP-07." | §4.6, §5.2, §9.2 WP-06/07/65 + new WP-65a, §9.3 edges/waves; Appendix P P2 | Applied in spec; runtime NOT verified |
| R5-06 | P1 | Window-based idempotency `(source+window+first/last doc id)` missed late middle inserts while coverage said `complete`. | "A pull persists every row observed in the requested window and is idempotent on observed content, keyed by a content digest of the observed pages ... Coverage means 'completely read as of `asOf`', not 'current'." | §4.3, §4.3.1, §5.3, §12 R2-008/R3-002 rows; TR-26, TR-33; WP-26, WP-28, WP-34; SC-ADV-21, SC-ADV-260; EV-ADV-226; Appendix P P6, P7 | Applied in spec; runtime NOT verified |
| R5-07 | P1 | Paged raw-row audit scan (`BroadScanBuckets`, page cursor, 10k×720) can miss/duplicate rows and falsely claim complete. | "The live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and the existing broad-scan gate; it never pages raw rows to build counts." | §5.1, §5.4, §7; TR-04, TR-10; WP-17; SC-API-49, SC-API-225; EV-ADV-143, EV-API-226, EV-UI-140, EV-UI-221; WS-100, WS-220; Appendix P P4 | Applied in spec; runtime NOT verified |
| R5-08 | P1 | One top-level `fromStored` hid mixed live/saved lane provenance. | "Every lane carries `origin ∈ {live, case}`, `asOf`, `complete`, `truncated` and `reason`; the top-level `fromStored` is retained only as `allLanesStored`, true iff every lane has `origin:"case"`." | §5.1, §6, §7; TR-04, TR-07; WP-14, WP-17, WP-18; SC-API-53, SC-API-54, SC-API-206, SC-API-223, SC-PRV-184; EV-API-206, EV-API-223, EV-PRV-184; WS-221 | Applied in spec; runtime NOT verified |
| R5-09 | P1 | Invalid duration rejected the whole event; in code the rollback lost the whole file with no quarantine row. | "A non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`; it increments `count_all` and not `count_with_duration`, no invalid float is ever serialized." | §4.5, §7; TR-34; WP-30; SC-API-168, SC-API-203, SC-API-228; EV-ADV-18, EV-ADV-202, EV-ADV-226, EV-API-203, EV-API-225, EV-API-230; AC-20; Glossary "bad duration" | Applied in spec; runtime NOT verified |
| R5-10 | P1 | Two independent scrub policies; read DTO copied every attribute, so IP/referer/cookie could leak on an unchecked surface. | "One pure function `ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string` is applied to every outward-facing event view ...; `includeSensitive=false` is the default." | §4.2, §5.2, §5.5; TR-15, TR-21; WP-02, WP-07; SC-PRV-19, SC-PRV-20, SC-PRV-225, SC-PRV-240, SC-PRV-260; EV-PRV-225, EV-PRV-240, EV-XC-100; D-8; Glossary "privacy projection" | Applied in spec; runtime NOT verified |
| R5-11 | P1 | `serverWorkId`/subscriber dedup added refcount races and a key with no canonical encoding, for no measured gain. | "MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`; there is no server `serverWorkId`, no subscriber count and no shared in-flight read." | §5.1, §6, §12 R3-011 row; TR-03, TR-13; WP-14, WP-16, WP-17; SC-API-166, SC-API-167, SC-API-201, SC-API-222, SC-API-224, SC-UI-200; EV-API-167, EV-API-168, EV-API-183, EV-API-184, EV-API-201, EV-API-222, EV-UI-161, EV-UI-180, EV-UI-200, EV-UI-201, EV-UI-202; WS-161, WS-221 | Applied in spec; runtime NOT verified |
| R5-12 | P2 | Archive gate accepted ">0 tests", so dropped required tests still released. | "A checked-in test-ID manifest lists every required guided scenario; `-Release` fails unless every required ID appears in exactly one of the main/archive suites and both suites pass, and the two reports ... are present and non-empty." | §9.2 WP-58/WP-62, §9.3 wave 8, §11, Appendix P global done-check; SC-ARC-161, SC-ARC-220, SC-ARC-221; EV-XC-201; WP-21, WP-24, WP-64; D-2; TR-01 | Applied in spec; runtime NOT verified |
| R5-13 | P2 | "Implement only that packet" / "do not explore the repo" blocked legitimate compile fixes. | "An implementation packet may edit a file not on its list only when a listed file fails to compile or an existing invariant/test would break without the change ... reported in the completion summary ... Contract/schema/route changes escalate." | §0.2, §9.4; Appendix P global done-check; WP table header note; SC-ARC-221 | Applied in spec; runtime NOT verified |

## Reversals and refinements

- **R5-09 reverses R4-009.** Old rule: a non-finite/negative/overflow duration rejects the whole event (in code the whole file, with no quarantine row). New rule: keep the event with `durationStatus:"invalid"` and null duration; increment `count_all` only; quarantine only unidentifiable or envelope-violating records.
- **R5-11 reverses R3-011 for MVP-1.** Old rule: `serverWorkId` + subscriber count + per-case lock dedup shared in-flight reads. New rule: no server `serverWorkId`, no subscriber count, no shared read; keep per-source concurrency bound, request-context cancellation and client-only per-tab `requestSeq`.
- **R3-001 refined (by R5-04).** Old rule: stateless revision-bound confirm, implied byte-identical re-derivation. New rule: stateless confirm still stands, but the guarantee is same case revision + same scrub policy class, never byte-identical output.
- **R4-001 refined (by R5-03).** Old rule: re-anchor `[start,end)` to `W = ceil(span/120)` so a 30-day window is exactly 120 buckets. New rule: keep a UTC floor-aligned axis, never re-anchor; coarsen to the next supported rung when aligned count exceeds 120, still ≤120 with exactly two edge slivers.
- **R4-010 refined (by R5-07).** Old rule: bounded "named broad-scan path" `audit.BroadScanBuckets` with page/time/concurrency caps. New rule: one server-side `$match`+`$group` per source under existing `CensusQueryBudget`/broad-scan gate; constants deleted; `complete:true` means the command ran to end within budget.

## Changed dependency edges

Verbatim WP edge table from DEC8-rulings.md:

| WP | new Depends-on |
|---|---|
| WP-65a (new) | — |
| WP-06 | WP-65a |
| WP-07 | WP-06 |
| WP-65 | WP-65a, WP-06, WP-07 |
| WP-58 | WP-01, WP-02, WP-03, WP-04, WP-05, WP-06, WP-07, WP-08, WP-09, WP-10, WP-11, WP-12, WP-13, WP-14, WP-15, WP-16, WP-17, WP-18, WP-19, WP-20, WP-22, WP-23, WP-25, WP-60, WP-61, WP-62, WP-63, WP-64, WP-65a, WP-65 |
| WP-17 | WP-15, WP-16 |

All other edges unchanged. No cycle: the only path to WP-65 is via WP-65a/WP-06/WP-07, none of which depends on WP-65.

**WP-65 split:** WP-65a (MVP-1, new) owns the `case.revision` bump primitive (`bumpCaseRevision(tx)`, schema default, wiring into existing mutation sites; casestore tests only) with no export dependency. WP-65 remains the confirm-integration packet and depends on WP-65a, WP-06 and WP-07. WP-65a is Wave 1.

## Glossary of renamed or removed terms

Removed/retired:

- **`serverWorkId`** — retired in MVP-1 (R5-11).
- **subscriber count** — retired in MVP-1 (R5-11); no shared in-flight read.
- **`BroadScanBuckets`** — retired (R5-07); superseded by the audit aggregation + existing budgets.
- **`fromStored`** as a flag — removed as mixed-provenance truth; retained only as `allLanesStored`.
- **byte-identical** — removed as a confirm guarantee (R5-04).

New/canonical (DEC8 Glossary wording):

- **`allLanesStored`** — Redefined top-level `fromStored`; true iff every lane has `origin:"case"`.
- **`readKind`** — Lane/source provenance ∈ `{saved, live}` (R4-008); reconciled with `origin` (`saved↔case`, `live↔live`).
- **`durationStatus`** — `"ok"|"missing"|"invalid"` on the event DTO; `invalid` keeps the event with null duration, increments `count_all` only.
- **`ProjectEventAttrs`** — The single read-side projection function; default drops `client-ip`, `referer`, every `cookie:*`; opt-in explicit.
- **`requestSeq`** — Client-only per-tab monotonic sequence; drives stale-response ignore and per-request cancellation.

## Validation

This is **spec validation only**. Performed: structure validator, term purge grep, and independent review. **No runtime verification** was performed (no repository build, no runtime tests, no live Mongo/SQLite integration tests). Validator numbers (final spec): **345 scenarios, 415 evaluation rows, 53 workstream rows, 0 structural problems**. The scenario count is higher than earlier drafts because the Area E archive scenarios are now inside a fenced gherkin block and counted. Every ruling's status is "Applied in spec; runtime NOT verified".

## Known limits

- Owner sign-off on the two reversals and the refines is still **OPEN**: R5-09, R5-11, R5-03, R5-01, R5-04.
- **R5-07** server-side `$group` is preferred "where supported"; if a deployment cannot `$group` in budget, a bounded `(time,_id)` seek fallback applies. Perf figures (`<15ms`) remain informational until measured.
- **R5-04** optional export manifest (input-level digest) is owner discretion, not adopted.
- **R5-06** `content_digest`, outcome words and **R5-01** `asOf` are marked proposed.
- **R5-11** short-lived memoization is deferred until measured duplicate-read pressure.
- **R5-13** completion output must include `git diff --stat`, out-of-list file list with reasons, and test command + result; escalation error body is proposed.
- Deferred/later per locked decisions: WP-24 Later, WP-21 filter Later, WP-09 CallTrace explain-only, hydration side-table and profiler Later, D-11 Call-trace deep link Later, D-12 win32 estimate series Later, WP-58 not release-ready until `-Release` mandatory.
- Consequence if a reversal/refine is wrong: invalid-duration event stays visible/filterable with no timing and `count_all` includes it (R5-09); two tabs with the same query each pay one bounded live read (R5-11); a 30-day off-hour chart may show 8 h bars (R5-03).

No claim of guaranteed correctness is made.
