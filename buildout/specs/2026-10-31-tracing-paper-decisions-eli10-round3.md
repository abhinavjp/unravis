I have all the material. Now I'll write the ELI10 document.

# Round 3 Rulings, Explained Simply

## Read this first

1. This explains fourteen fixes ("rulings") to the Tracing Paper plan, in plain words.
2. Each ruling says what was wrong, what was decided, why, what it costs if the decision is bad, and whether it reopens an earlier choice.
3. A few rulings change a previously locked choice; those are marked and need your approval (see the table at the end).
4. "Win32" below always means the IIS Win32 error code, never a Windows version number.
5. Read only the rulings you care about; each stands alone.

## 1. The one-time export ticket that could not remember anything [R3-001]

**What was wrong.** The plan treated a "preview code" like a cinema ticket that works once and then expires. But the code is just a fingerprint of the case, so it has no memory of being used and cannot expire — like writing today's date on your hand and calling it a used ticket.

**What I decided.** The code becomes a freshness label only. Preview and confirm both recompute it; if the case changed since the preview, confirm replies 409 ("the preview changed; run the preview again"). There is no stored one-shot state, no expiry, and no "unknown preview" 404.

**Why.** A pure hash cannot track issuance or consumption, so the old promises could never be built.

**Cost if wrong.** A stale-but-matching preview could be replayed within the same case revision, which is acceptable because the bytes are simply re-derived from current inputs.

**Does this reopen an earlier decision?** Yes — it changes DEC5 R2-010 and DEC4 R9's previewId clause, which is why it needs your OK.

## 2. "Refresh" that secretly meant three different things [R3-002]

**What was wrong.** One "refresh" button was described as redrawing the screen, re-reading the saved ledger, and reaching out to the customer's live database — all at once. That is like labelling a light switch "on/off/replace bulb".

**What I decided.** Refresh now only re-fetches the screen state. The request bit is renamed `bypassLedger` and only re-evaluates the stored ledger. A live source read becomes its own explicit button and endpoint ("Pull now", `POST /pulls`).

**Why.** A read-only GET must never open a customer connection or write to the case database.

**Cost if wrong.** Users might expect Refresh to fetch new data and think the system is stale when it is not.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-008 and DEC4 R8 without contradicting them.

## 3. Two recipes for hiding cookies [R3-003]

**What was wrong.** One part of the plan used a 16-byte "salt" and the other a 32-byte key, with different input formats. The same cookie would then produce two different hidden codes. That is like two cooks salting the same soup differently.

**What I decided.** One recipe everywhere: a single 32-byte per-case key named `cookie.hmac_key_v1`, one fixed prefix label, lowercase name, 32 hex characters out. The word "salt" is dropped from new text.

**Why.** Shared, deterministic cookie matching only works if every component computes the same digest.

**Cost if wrong.** If real cookie hashes already exist from the old recipe, they would stop matching — but nothing is deployed yet.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-015 and DEC4 R9; "pseudonymous", "first wins" and "no rotation" all stand.

## 4. Cutting the two ends of an odd window [R3-004]

**What was wrong.** For windows starting at odd minutes, the plan said to use saved hourly summaries but never said exactly how to trim the two ends, so a slice of time could vanish or be counted twice. It is like tiling a room but not deciding how to cut the edge tiles.

**What I decided.** Every display bucket is intersected with the real window. Whole contained hours come from hourly summaries; the two end slivers come from raw events. Two separate flags are defined: `clipped` (normally cut by the window edge) and `complete` (all pieces actually read). They are never swapped.

**Why.** Exactness requires each piece of the window to be counted once, with clear labels for edges versus missing data.

**Cost if wrong.** Wrong end-slivers would make totals subtly off at window boundaries.

**Does this reopen an earlier decision?** No — it refines DEC4 R1/R6 and DEC5 R2-003/R2-004/R2-012.

## 5. Steps running before the tables they need exist [R3-005]

**What was wrong.** One step recorded live reads before the table that stores read attempts existed, and another read hourly summaries before anything wrote them. That is like opening a shop before the shelves are built.

**What I decided.** Declare the prerequisites: the pull step depends on the ledger and attempts tables; the hourly read path depends on the hourly writer; and no long-window zoom may claim correctness above 24 hours until hourly rollups exist.

**Why.** Out-of-order packets would produce inconsistent data and tests that pass only in one build order.

**Cost if wrong.** A packet could ship that appears to work but silently lacks its storage layer.

**Does this reopen an earlier decision?** No — it extends DEC5 R2-012/R2-014 and DEC4 R12.

## 6. One stamp computed from all sources at once [R3-006]

**What was wrong.** The "we already read this" stamp was computed from all selected sources together, so the same source queried alone and alongside others got two different stamps. That is like changing your house number depending on who visits.

**What I decided.** Stamp each source by its own effective query only — its id, lane, window and filters — never by the other sources in the request. The attempts table carries the same stamp and a link to the failed import.

**Why.** A source's own retrieval is identical no matter who else is selected, so its cache key must be too.

**Cost if wrong.** False cache misses would cause needless re-reads; or a failed attempt could be matched to the wrong query.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-001 and R2-007.

## 7. The source list that mixed names and IDs [R3-007]

**What was wrong.** One place listed sources by importer name, another by saved source id, so two IIS sites of the same kind looked identical. That is like listing employees by job title instead of name.

**What I decided.** Every lane returns each source as `{sourceId, kind, label}`, and every event's `source` is always the saved `case_sources.id`. The importer type appears only as `kind`.

**Why.** An id and an importer name are not interchangeable when two sites share a type.

**Cost if wrong.** The screen could merge two distinct sources into one, hiding data or mislabelling it.

**Does this reopen an earlier decision?** No — it extends DEC5 R2-007's `source` = `case_sources.id` rule to the lane response.

## 8. Live remote counts described as saved-row counts [R3-008]

**What was wrong.** The plan said counts are always exact because they count all saved rows — but the audit lane is read live from a remote server and never saved. That is like claiming a live phone poll gives the same certainty as a written census.

**What I decided.** The audit lane is a live remote aggregation with its own completeness and error reporting; the SQL and IIS lanes are uncapped exact local counts. A failed remote page marks only the audit lane `complete=false`, naming the page.

**Why.** Saved-event exactness cannot be transferred to data that was never saved.

**Cost if wrong.** The screen could claim exact audit numbers when remote pages failed or the window exceeded the store's limit.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-004 and keeps DEC4 R13's independent audit reporting.

## 9. Raw scans demanded even when there is no edge [R3-009]

**What was wrong.** One test said an aligned one-hour zoom must read only summaries, while another rule said every edge is always re-read from raw events — but an aligned hour has no edge. That is like being told never to open the fridge and always to open the fridge.

**What I decided.** Raw edge scans happen only when the window does not align to the hourly grain (`hasRawEdge`). Aligned windows read rollups only. Benchmarks state which mode they measured.

**Why.** There is nothing to fix at an edge that does not exist.

**Cost if wrong.** Either needless slow scans on aligned windows, or missing edge data on unaligned ones.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-003 and DEC4 R6.

## 10. Exact durations stored as decimals [R3-010]

**What was wrong.** The plan promised exact durations but stored minimum and maximum as decimals, and never said how to round or what to do with a missing or impossible duration. That is like insisting on whole pennies while keeping the books in fractions.

**What I decided.** Durations are whole microseconds. Round by bankers' rounding (`ms * 1000`), refuse NaN, infinity and negatives at import, and track `count_all` versus `count_with_duration` separately. Minimum and maximum become integers `min_us`/`max_us`.

**Why.** The exactness promise only holds in integer arithmetic, and percentiles need an unambiguous denominator.

**Cost if wrong.** Tests demanding bit-exact totals could fail, or missing durations could skew p95.

**Does this reopen an earlier decision?** No — it refines DEC4 R6 and DEC5 R2-013.

## 11. One token meaning both "this tab" and "merge these" [R3-011]

**What was wrong.** A single "generation" token was asked to mean both "this tab's request" and "merge identical requests", so one tab could cancel another or an expensive read could run forever. That is like one key that both opens your flat and switches off the street lights.

**What I decided.** Each tab gets a `clientSessionId`, each request a rising `requestSeq`, and shared work is de-duplicated by a `dedupKey`. Cancelling a tab detaches only that subscriber; shared work stops only when its last subscriber leaves. A client token is never authorization.

**Why.** Cancellation and de-duplication are different jobs and must not share one field.

**Cost if wrong.** Two tabs could interfere, or stale responses could overwrite fresh chart state.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-011 and DEC4 R12.

## 12. The privacy pill that always said "hidden" [R3-012]

**What was wrong.** The little scrub pill always claimed the export hides sensitive fields even when hiding was switched off, so a user could export more than they expected. That is like a sign saying "door locked" while the door stands open.

**What I decided.** The pill always states the real export state: on reads "Export hides IP, referer and cookie hash"; off reads "Export includes IP, referer and cookie hash (scrub is off)"; before a preview it reads "Default: export hides...". It never says stored data is masked.

**Why.** A privacy message that misstates the actual setting is worse than none.

**Cost if wrong.** A user could unintentionally export sensitive fields believing they were hidden.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-015 and DEC4 R9's scrub-pill clause.

## 13. Steps pointing at a file nobody shipped [R3-013]

**What was wrong.** Many steps pointed at an external packet file that was not included, and one step still said both hourly writers build both bucket sizes. That is like a recipe saying "see the other cookbook" when there is no other cookbook.

**What I decided.** Appendix P inside this document is the single canonical detail. Every step points at a numbered anchor there. The 60-second writer is one work package and the hourly writer/verifier is another. Coverage tests carry `@TR-33`, rollup tests `@TR-34`, and `@TR-24` remains only the umbrella tag.

**Why.** An agent following a pointer to a missing file could implement outdated instructions.

**Cost if wrong.** Implementers could follow stale detail and claim the wrong requirement coverage.

**Does this reopen an earlier decision?** No — it extends DEC4 R4/R5 and DEC5 R2-012/R2-013.

## 14. A release rule no single command could run [R3-014]

**What was wrong.** The release rule said both test suites must pass, but no single command ran both, and the second run overwrote the first report. That is like a safety check requiring two inspections but only filing one sheet.

**What I decided.** Add one release command, `./run-e2e.ps1 -Release`, that runs the main suite then the archive suite, writes `report/main.json` and `report/archive.json`, and fails if either fails or discovers zero tests. It is the only release entrypoint; `-Only` fails when its pattern matches nothing.

**Why.** Proving "both green" needs one command that keeps both sets of evidence.

**Cost if wrong.** An archive regression could pass release unnoticed, or one report could erase the other.

**Does this reopen an earlier decision?** No — it extends DEC4 R13 and DEC5 R2-014.

## Needs your OK

These rulings change a previously locked decision or make a taste call the owner must approve. Everything else is a refinement that contradicts nothing.

| Ruling | What it changes or chooses |
|---|---|
| R3-001 | Drops the one-shot/expiry/404 claims from DEC5 R2-010 and DEC4 R9; confirm is stateless and revision-bound, 409 is the only stale code. |
| R3-004 | Chooses `clipped` vs `complete` and the intersection/clip algorithm implied by DEC4 R1/R6 but not previously stated. |
| R3-010 | Chooses integer-microsecond durations and `count_all` / `count_with_duration` (a data-precision taste choice). |
| R3-011 | Chooses to keep server de-duplication with a subscriber count rather than omit it in MVP-1. |
| R3-013 | Declares this document's Appendix P canonical, removing the second external source. |