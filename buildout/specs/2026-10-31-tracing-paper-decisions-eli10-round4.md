# Round 4 Rulings, Explained Simply

## Read this first

1. Round 4 found 12 problems, mostly about different workers building things that wouldn't fit together.
2. Four are urgent (P0): bucket count, database access, shared results, and a stale-export risk.
3. I accepted all 12, some with changes; the main choices are noted below.
4. Nothing was reopened: each ruling only sharpens an earlier decision.
5. A few items need your sign-off at the end.

---

## 1. The chart can overflow by one slice [R4-001]

**What was wrong**
A 30-day window is drawn in 6-hour slices. If the window starts at 00:01 instead of 00:00, the rounding adds one extra hour-group, producing 121 slices. That is one more than the chart permits, like a 12-egg carton that suddenly needs a 13th slot because you started counting mid-row.

**What I decided**
For long windows, anchor the grid to the window's own start second, so you always get exactly 120 slices. The default 1-hour window keeps its usual hour-aligned grid.

**Why**
Starting from the window itself removes the misalignment instead of changing the slice width or allowing an over-limit chart.

**Cost if wrong**
A 30-day chart could silently drop or double-count a boundary second.

**Does this reopen an earlier decision?**
No; it refines the R1 bucket-width rule by adding a fallback for long, unaligned windows.

---

## 2. The "pull" button was the only door to the customer database [R4-002]

**What was wrong**
One rule said only the pull action may open a customer database. But the MVP‑1 live audit lane must read that same database. Read literally, an audit trace could neither read its data nor connect to it. It is like saying only the delivery van may enter the building, while the inspector is told to inspect inside.

**What I decided**
Tracing may open a read-only connection to a customer source. Only the pull button may save what it reads, and only the pull button writes an import.

**Why**
The intent was always "only pulls persist data"; the wording wrongly also blocked read-only audit reads.

**Cost if wrong**
Tracing would either break the audit lane or accidentally be allowed to write customer data.

**Does this reopen an earlier decision?**
No; it repairs the over-broad "only" wording in R3-002 (which already split live reads from pulls).

---

## 3. Two different questions were treated as the same [R4-003]

**What was wrong**
The shared-work key hashed the case, sources, incident window, and case version, but ignored the reference window and query flags. Two tabs asking about the same incident but a different reference window could share one result and its warnings. That is like two people ordering "the usual" at a counter that assumes they want the same drink.

**What I decided**
Key shared work by a versioned fingerprint of the whole effective question, including reference window, bucket size, flags, and case revision. Only truly identical requests share one read.

**Why**
If any request-shaping field differs, the warnings and projections differ, so the work cannot be reused.

**Cost if wrong**
A user sees warnings or numbers computed for someone else's question.

**Does this reopen an earlier decision?**
No; it widens the de-duplication key from R3-011 while keeping `clientSessionId`/`requestSeq`.

---

## 4. The case change-counter had no owner [R4-004]

**What was wrong**
The case revision must change on every export-affecting edit (imports, sources, privacy settings), but no build packet was assigned to bump it. A missed bump would let a stale export preview be accepted, like a "milk fresh until" sticker nobody updates after restocking.

**What I decided**
Add packet WP-65 to own the counter. Every mutation bumps it in the same transaction, and confirm re-checks it inside a read-consistent snapshot before producing bytes, returning 409 if it changed.

**Why**
Checking the revision then re-reading separately leaves a window where a change slips through; one snapshot closes it.

**Cost if wrong**
A signed-off export could contain bytes from a different case state than the one approved.

**Does this reopen an earlier decision?**
No; it keeps R3-001's stateless preview and 409, and only assigns the counter writes and closes the timing gap.

---

## 5. A "refresh" switch that refreshed nothing [R4-005]

**What was wrong**
`bypassLedger:true` claimed to re-evaluate the ledger but actually read nothing, then labelled the result `fromLedger:false`. That is false provenance, like a "freshly squeezed" label on juice you never opened. GET-query versus JSON-boolean form was also undefined.

**What I decided**
Delete `bypassLedger` and `fromLedger`. A stored result simply reports `fromStored:true`; the only genuine re-read is an explicit pull.

**Why**
A flag that changes no data path should not exist, and the pull button already does the real refresh.

**Cost if wrong**
Users could believe data was refreshed when it was not.

**Does this reopen an earlier decision?**
No; it refines R3-002 by removing the misleading bit that ruling introduced.

---

## 6. "Already read" stamps blocked all reuse [R4-006]

**What was wrong**
The coverage fingerprint included the exact start and end dates, so a small range inside a big already-read range was never reused. That is like refusing to reuse an open highway because your trip starts at a different exit.

**What I decided**
Stamp only the source and filters in the fingerprint; keep the window in its own columns. Any stored complete range that covers the requested one is reused, and only uncovered slivers are read.

**Why**
An interval ledger exists precisely to skip already-read slices, which exact-window hashing made impossible.

**Cost if wrong**
Every nearby query re-reads remote data, wasting time and load.

**Does this reopen an earlier decision?**
No; it refines R3-006 (fingerprint over one source's own query) by dropping the window from that preimage.

---

## 7. Deleting a source had no enforceable rule [R4-007]

**What was wrong**
The prose said deleting a source deletes its coverage but keeps its failed-attempt record for 30 days. The database tables had no foreign keys or cleanup, so neither promise could hold. One key would forbid the tombstone; a cascade would erase it.

**What I decided**
Coverage really follows the source via a cascade key. Failed attempts store a historical text label instead, surviving source deletion, and rows older than 30 days are pruned when the case opens.

**Why**
A tombstone must outlive the source it names, so it cannot carry a live reference to it.

**Cost if wrong**
Orphan coverage or lost attempt history, plus unbounded table growth.

**Does this reopen an earlier decision?**
No; it keeps the tombstone and 30-day retention from R3-006, R8, and R14, and makes one key real and the other pruned.

---

## 8. The source list had three different shapes [R4-008]

**What was wrong**
The API returned `{sourceId,kind,label}`, one packet declared a struct with `ReadKind` and `Window` but no `kind`, and another omitted fields again. A worker could pass its own tests yet break the client, like three forms asking for the same thing with different boxes.

**What I decided**
Publish exactly one shape everywhere: `{sourceId, kind, label}` required, with optional `readKind` and `window`. One shared fixture is used by the packets and the API test.

**Why**
A single schema is the only way the server and UI can stay compatible.

**Cost if wrong**
The client rejects or misreads lane sources at runtime.

**Does this reopen an earlier decision?**
No; it enforces R3-007's `{sourceId,kind,label}` shape by resolving a competing struct.

---

## 9. Bad durations were counted and discarded at once [R4-009]

**What was wrong**
One test said a bad duration is refused at import but still counted, another said nothing is written. These cannot both be true. It is like saying a broken item is returned to the shelf *and* thrown in the bin.

**What I decided**
A non-finite, negative, or oversized duration rejects the whole event: not stored, no count, with one quarantine row. A merely missing duration stores the event and counts it, but not its timing.

**Why**
Choosing one policy makes the counters and the tests agree.

**Cost if wrong**
Counts and totals disagree, or audit records vanish silently.

**Does this reopen an earlier decision?**
No; it picks one side of a contradiction left open by R3-010.

---

## 10. A 30-day remote scan with no limits [R4-010]

**What was wrong**
The live audit lane may scan 30 days of a remote database, but no page size, page count, timeout, or concurrency was named. "Server-side aggregation" alone does not bound the work, like promising a fast library with no rules on how many books you may borrow.

**What I decided**
Name the path `audit.BroadScanBuckets` and cap it: 30-day span, 10,000 rows per page, 720 pages, 60-second read, two concurrent reads. Any failure or cap marks the lane incomplete and names the reason; unsupported spans are refused with the limit stated.

**Why**
Concrete bounds make the work predictable and let partial results be reported honestly.

**Cost if wrong**
A single trace could hang or exhaust memory, or silently shorten the window.

**Does this reopen an earlier decision?**
No; it keeps R3-008's separate audit lane and R2-004's exact local counts, adding only bounds.

---

## 11. "Main tests only" was mistaken for release [R4-011]

**What was wrong**
The main-suite packet's done-check said "full main suite green", which a reader could mistake for release-ready, even though the real gate runs an extra archive suite. That is like calling a plane ready because the engines started, without the safety checks.

**What I decided**
Make `./run-e2e.ps1 -Release` mandatory and blocking. It runs both suites, writes both reports, and fails on a suite failure, zero tests, or a missing/empty report.

**Why**
Release readiness must be one command that cannot be short-circuited.

**Cost if wrong**
A build ships with untested archive behaviour.

**Does this reopen an earlier decision?**
No; it keeps R3-014's single release command and separate reports, and makes it mandatory.

---

## 12. Packets still pointed at outside files and old counts [R4-012]

**What was wrong**
Packets claimed no external dependency but still told workers to read `PERF-RESEARCH`, `SPECB`, and `EXT-REVIEW`, and one required exactly 78 tests. A worker limited to its packet could lack the facts it was told to follow, like a recipe that says "use the sauce from the other book".

**What I decided**
This document's Appendix P holds every normative test, value, and constant. Other references are informational only, and test counts are discovered at run time with `npx playwright test --list`.

**Why**
A packet must be self-contained so one worker can finish without hunting for outside files.

**Cost if wrong**
Workers guess at values or hard-code a stale test count.

**Does this reopen an earlier decision?**
No; it keeps R3-013's canonical Appendix P and removes leftover external pointers without renumbering any test requirement.

---

## Needs your OK

| Ruling | What needs your decision | Proposed default |
|---|---|---|
| R4-001 | Anchor long-window grids to the window start (exact 120 slices) rather than allow 121 | Re-anchor to window start; 6 h nominal, exact widths |
| R4-004 | Add packet WP-65 and make `case.revision` writes an owned, snapshotted contract | Accept WP-65; confirm snapshots revision and 409s on mismatch |
| R4-005 | Retire `bypassLedger`/`fromLedger` rather than keep false provenance | Drop the bit; use `fromStored`; pull is the only real re-read |
| R4-009 | Reject the whole event for a bad duration (tests disagreed) | Reject whole event; missing duration keeps event, not timing |
| R4-010 | Invented audit broad-scan limits (page size, pages, timeout, concurrency) | 30 days, 10,000 rows/page, 720 pages, 60 s, 2 concurrent |