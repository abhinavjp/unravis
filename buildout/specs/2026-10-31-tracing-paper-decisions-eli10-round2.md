# The Owner-Delegated Decisions, Explained Simply

A plain-language walkthrough of the 15 rulings from the second adversarial review. Each ruling fixes a problem found in the tracing-paper plan for Unravis, a local single-user tracing tool. "Win32" below means the IIS Win32 error code (a number Windows/IIS reports for a specific failure), not a version of Windows.

---

## 1. Two searches over the same dates could be confused
**What was wrong** — The plan said each coverage record is stamped with the query it came from, but the actual database table never had a column for that stamp. It is like labeling two jars "jam" without saying which fruit is inside.
**What I decided** — Add a `fingerprint` column to the coverage table, change its index to use it, and define the fingerprint as a short lowercase-hex SHA-256 hash of the sorted, de-duplicated `sourceIds` plus filter fields, with a `fingerprint_v` version column.
**Why** — Without a stamp, a search with one filter could be reused for a different filter and give wrong coverage.
**Cost if wrong** — Results for one filter silently stand in for another, so coverage claims are false.
[R2-001]

## 2. Promised a per-event note table that was never built
**What was wrong** — The glossary and one requirement promised a table holding a value for each event field, but the real schema only has an import-level table recording whether a field exists and how big it is. It is like advertising a filing cabinet of individual pages when only a summary sheet exists.
**What I decided** — Delete the per-event promise from the glossary and the requirement, state once that "hydrate" means lazily reading the payload already stored in `case.db`, and keep the import-level table as a presence/size hint only.
**Why** — A local single-user app should read what is already saved, not invent a new storage scheme.
**Cost if wrong** — Someone builds or waits for a table that does not exist, stalling the feature.
[R2-002]

## 3. Long-window buckets did not line up with saved summaries
**What was wrong** — The chart slices were anchored to an arbitrary window start (say 10:07), but the saved summaries only cover whole UTC-aligned hours. A 6-hour window starting at 10:07 cannot be assembled exactly from hourly summaries. It is like trying to fill a box that starts mid-brick from whole bricks only.
**What I decided** — On rollup zoom, align the presentation buckets to whole UTC hours and read the two partial edge pieces directly from raw events, marking those edge buckets `partial`. State this in the plan and keep exact totals coming from the raw edge scan.
**Why** — Exact totals and honest labeling matter more than pretending the aligned summaries fit.
**Cost if wrong** — Charts show slightly wrong numbers with no warning.
[R2-003]

## 4. Screen said "exact" while showing only the first slice
**What was wrong** — One rule said a cut-off list cannot claim exact counts, but a test and a screen message said IIS bucket counts "are exact" even when the list was capped. It is like a receipt claiming the full total when only the first few items were rung up.
**What I decided** — Keep exact counts by running a separate, uncapped server-side `GROUP BY` over all matching events, independent of the 50,000-row detail cap; only if that aggregate cannot finish does the lane say `complete:false`. Reword the test to "counts from the uncapped aggregate; the raw list is capped."
**Why** — Counts can be exact cheaply, and the list cap is a display limit, not a math limit.
**Cost if wrong** — Users distrust or misread totals, or the screen lies about completeness.
[R2-004]

## 5. Unverified timing shown as a known span
**What was wrong** — A rule and a requirement say an IIS event is a single point until VF-01 confirms otherwise, but a work packet still says "start/end spans" and a test invents a start 250 ms before the end. It is like drawing a duration for a stopwatch you have not confirmed works.
**What I decided** — Draw a point at the logged timestamp, show `time-taken` separately, and only add a derived span in a later packet gated on VF-01. Rewrite the tests to assert `start == end == logged timestamp`.
**Why** — Do not present unverified semantics as fact.
**Cost if wrong** — Users read a made-up duration as real event timing.
[R2-005]

## 6. A filter box the first version should not have
**What was wrong** — The first-release request example included a `route` field, but the plan puts route/status/tenant filters in a later phase. It is like shipping a form with a checkbox that does nothing yet.
**What I decided** — Remove `route` from the first-release request example and schema so a strict validator rejects it with a 400, and keep the filter contract only in the later-phase documents. Add a test that a first-release request with `route` gets a 400 and triggers no source read.
**Why** — A strict validator cannot both accept and reject the same field.
**Cost if wrong** — Hidden filter fields get half-implemented or silently ignored.
[R2-006]

## 7. Two servers of the same kind looked identical
**What was wrong** — Summary rows were keyed by `source` plus kind, but `source` was ambiguous, so two IIS sites of the same importer kind could collapse into one row when filtering by host. It is like two people named "Chris" sharing one mailbox.
**What I decided** — Define `source` everywhere (rollups, coverage, lane payload) as the immutable `case_sources.id`, and add `kind` as the importer/event kind. Add a done-check that two IIS sites in the same minute, selected separately and together, produce distinct rollup rows and correct rebuilds.
**Why** — Provenance must survive filtering and rebuilding.
**Cost if wrong** — One host's data is lost or misattributed during a rebuild.
[R2-007]

## 8. "Never re-reads a window" was an over-promise
**What was wrong** — The glossary claimed the app would never read the same window twice, but a query stamp cannot freeze live, changing upstream data. It is like promising a photo of a busy street will always match the street.
**What I decided** — Soften the claim to "skips windows already read unless the user forces a refresh," add a `refresh` bit to the coverage and tracing requests, and state that late arrivals only appear when the source is re-imported, with no TTL daemon or watermark machinery.
**Why** — A local single-user app does not need heavy freshness tracking to be useful.
**Cost if wrong** — Users expect data that can never arrive, or build unnecessary background services.
[R2-008]

## 9. A "forced hiding" switch that was never defined
**What was wrong** — Privacy tests depended on a "forced scrub mode" that has no field, default, or lifecycle anywhere. It is like a door with a "locked" sign but no lock.
**What I decided** — Remove forced scrub entirely: `includeSensitive=1` returns the three attrs whenever the caller holds the case key. Export scrub default ON stays as an independent export-only choice. Delete the extra test, reword the other, and drop the mode clause from the requirement.
**Why** — Undefined policy state cannot be implemented or tested.
**Cost if wrong** — Sensitive reads behave inconsistently, or a phantom flag blocks them.
[R2-009]

## 10. Confirmed export only loosely tied to the preview
**What was wrong** — The plan bound a `previewId` to case revision and scrub settings, but the work packets only added a scrub flag and no revision source or expiry behavior. It is like a quote with no expiry date or version number.
**What I decided** — Define `previewId` as a deterministic SHA-256 over case id, case revision, scrub flag, and projection version; confirm recomputes it, returns 404 with `exportGoneMessage` if unknown/expired and 409 if it differs, and previews are one-shot per revision. Add the fields and concurrency tests to the packets.
**Why** — A confirmation must match the exact preview the user saw.
**Cost if wrong** — A stale or mismatched export is confirmed, leaking the wrong data.
[R2-010]

## 11. A second window could cancel the first window's search
**What was wrong** — The rule "one in-flight trace per case; a new Start cancels the previous" let a second tab silently abort the first tab's search. It is like a shared phone line where picking up hangs up someone else.
**What I decided** — Scope cancellation to a per-request `generation`/session token echoed by the client, keep the server's per-case read lock only to de-duplicate identical in-flight requests, and allow two tabs with the same case key their own read. Add the missing scenario tests and bound per-source concurrency separately.
**Why** — A newer request must never cancel a different caller's work.
**Cost if wrong** — A user's search dies mysteriously because another tab acted.
[R2-011]

## 12. Table-count confusion and doubled work
**What was wrong** — One section said "four DDL blocks," a work packet named only three and omitted `import_attempts`, and two packets both built the one-hour summaries. It is like a shopping list that says four items but lists three, while two cooks make the same sauce.
**What I decided** — The schema packet must create and verify all four tables (`coverage`, `import_attempts`, `hydration`, `rollups`) with a `sqlite_master` assertion; one packet builds the 60 s pass, the other becomes the 3600 s pass/verification; attempt bookkeeping goes in its own packet.
**Why** — An agent following one packet should not miss a table or duplicate a build.
**Cost if wrong** — A missing table breaks migrations, or the hour pass runs twice.
[R2-012]

## 13. One label used for two features, one wrong mapping
**What was wrong** — Requirement TR-24 covered both coverage and rollups while tests used it for zoom and histograms, a work packet credited filters to a decision rule about Win32 error-code partitioning, and another requirement still said "reuse filters" in the first release. It is like one aisle sign pointing at bread, milk, and hardware.
**What I decided** — Keep TR-24's history but add TR-33 for coverage ledger/fingerprint and TR-34 for rollups/percentile/zoom, point the tests at TR-34, fix the packet's decision mapping to the filter ruling, split the filter requirement into a later phase, and add an audit test that each requirement maps to one phase and one decision.
**Why** — Traceability must not tell an agent to build later work now or cite the wrong authority.
**Cost if wrong** — Filters get built early, and audits point at the wrong owner decision.
[R2-013]

## 14. Release checklist disagreed with the dependency picture
**What was wrong** — One packet treated its main test run as the release gate, the dependency graph listed fewer incoming edges than the packet table, and two later-phase packets still sat in first-release waves. It is like a map showing roads that the itinerary ignores.
**What I decided** — Regenerate edges and waves mechanically from the canonical packet table, keep that packet as "main suite only," add an explicit release-gate step that runs `./run-e2e.ps1 -Archive` and requires main plus archive green, and move the two packets to a later wave.
**Why** — One source of truth prevents early gates and misplaced work.
**Cost if wrong** — A release ships without the archive tests, or later work blocks the first release.
[R2-014]

## 15. Messages that said the opposite, and a test that tested nothing
**What was wrong** — A screen message said counts were exact with a cut list, another showed "export hides..." even when scrub was off, a chart action was called "Scrub" (colliding with privacy scrub), and an edge test passed when its selector matched no archived test. It is like a test that grades an empty exam as perfect.
**What I decided** — Reword the count message to "counts from the full aggregate; list capped," reword the export message to say export includes IP, referer and cookie hash when scrub is off, rename chart dragging to "time-range brush," and make `run-e2e.ps1 -Only` fail loudly with "no test matched" on zero matches.
**Why** — Users must not be misled and a check must actually check something.
**Cost if wrong** — Users believe exports are scrubbed when they are not, and a real regression slips through.
[R2-015]

---

## Needs your OK

These rulings change an earlier owner choice or an earlier ruling. The rest are refinements within existing authority.

| Ruling | What it changes |
|---|---|
| 3 | Chooses the UTC-aligned presentation-bucket policy on rollup zoom — a design choice implied by earlier rulings but not previously stated. |
| 7 | Makes `source` mean `case_sources.id`, refining the earlier ruling's key `(source, kind, bucket_ms, at_ms)`. |
| 8 | Narrows the earlier "never reads the same window twice" promise to a skippable-ledger claim. |
| 9 | Removes the "forced scrub mode" flag entirely; read opt-in no longer depends on a case policy flag. |
| 13 | Adds TR-33/TR-34 rather than renumbering, extending the earlier requirement-traceability ruling. |
| 14 | Splits the release gate from the per-packet main run, extending the earlier main+archive requirement. |
