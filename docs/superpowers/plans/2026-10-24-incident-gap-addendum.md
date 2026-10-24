# Incident-gap addendum: six candidate additions

Status: plan only, nothing built. Inputs: roadmap rev 9 (§5.3 card, §5.4 stop rule, §6.4–6.6, §7.4, §8.2–8.3, §8.9, §10.2, §11), decision rulings R1–R16, T0.6 code reads, theory engine spec (design only, needs ruling R-TE), and the current code (`internal/audit/patterns_*`, `phases.go`, `breakdown.go`, `activity.go`, `models.go`).

All values below are tunable defaults, not client facts. Every new finding follows the existing P2-lite output rule: it carries an evidence label, n, a low-n flag, incomplete/pending/invalid counts, the P1a coverage figure, and provenance. Evidence IDs are `p2:<rule>:<16hex>`, and `p1c:<rule>:<16hex>` for P1c.

## 0. Placement summary and §5.4 verdicts

| # | Item | Placement | Rule ID (§11) | Tier | §5.4 verdict |
|---|---|---|---|---|---|
| 1 | Per-filter timing | **New slice P1c** "pre-action filter phase" (the A1 work P1a deferred) | A1, restated | T1 | **Build after preconditions.** Blocked on T0.6b and T0.7 (see below) |
| 2 | Idle gap vs audit create | **P2-lite extension** | B6g (new; a gap-conditioned form of B6) | T1 | **Build, opt-in, Symptom only.** Needs a new narrow read |
| 3 | Step-change dating | **P1b** (already T2+, off by default) | B9, restated | T2+ | **Defer the dating; merge the ranked list.** The before/after ranked list already exists as the comparison Rollup (`P95DeltaMs`, `TrafficDelta`). Only the automatic date is new, and that needs multi-day data (stop rules 4 and 5) |
| 4 | Same-clock-minute stalls | **P2-lite extension** (the audit-only half of B3) plus a UI-only per-minute view | B3a (new) | T1 | **Build now.** Adds no reads. Naming a schedule stays with B3/B13 after P5/P6 |
| 5 | Retry amplification | **Merged into B12** as a second shape | B12r (new) | T1 | **Build the count; defer the lift-vs-control test** (stop rule 4) |
| 6 | Concentration ratio | **P2-lite extension** | A3, restated | T1 | **Build now for operation and user. Defer AccountId** (A2 needs AccountId coverage ≥ 80 %, and AccountId is null on writer `master`) |

**Retire rule (§5.4.3) for every new rule.** If B3a, B6g, B12r or A3 changes no ledger entry across three incidents, it moves behind a "more rules" toggle and stops being maintained.

**Pre-existing gap found while planning (outside this addendum's scope).** `ValidatePattern` and the other views accept windows of up to 7 days (`MaxInterval`). Under §6.5, a live read over more than 24 h is *broad*, but only Coverage refuses broad scans (`ErrBroadScanRefused`). The new reads in this addendum refuse live windows over 24 h. Whether to apply the same refusal to the existing views is listed as user decision D3.

## 1. Slice cards

### 1.1 P1c: pre-action filter phase (item 1)

- **Slice:** P1c, T1, audit-only.
- **Preconditions:**
  - **T0.6b (new, code read, read-only):** T0.6 found no `PreActionFilter*` field on the writer's `master` model. The writer has to be located (another filter or another branch, as with AccountId). That read must confirm three things:
    - (a) the element field names inside `PreActionFilterTimings`. `FilterName` is known; the elapsed-time field is not.
    - (b) the clock used.
    - (c) whether the filters run before the audit filter's first line (outside `RequestInitiatedAt`, so outside observed time), inside the audit-create span, or inside `ApiTime`.
  - **T0.7:** the corrected stall script, rerun on a read-only copy or off-peak, confirms the fields are populated.
  - **Presence gate:** in the window, at least 95 % of sync rows carry `PreActionFilterTotalMilliseconds`. Otherwise the view prints "not written in this window" and evaluates nothing.
- **Incident question:** "Is time spent in pre-action filter <F> the reason for the slow requests in <window>?"
- **Rule definitions:**
  - Per-filter table. Each `FilterName` gets n present, mean ms and p95 ms. The p95 is printed only when n ≥ 20 (`MinSampleCount`).
  - Two cohorts are shown side by side:
    - slow cohort: rows with observed time ≥ the stall threshold (B1, 45 000 ms default).
    - fast cohort: rows with observed time below the window median.
  - The difference shown is slow mean minus fast mean, per filter.
  - **A1, restated:** pre-filter dominance holds when `PreActionFilterTotalMilliseconds` ≥ 0.5 × D, D ≥ 2 000 ms, and n ≥ 20 (label Symptom). The denominator D is chosen by T0.6b(c):
    - if the filters are nested in `ApiTime`, D = `ApiTime` (rev 7 A1 unchanged).
    - if they are in the audit-create span, D = `AuditCreateTime`.
    - if they are outside `RequestInitiatedAt`, there is **no ratio**: a separate series only.
  - In every case the filter time is drawn as its own series and never subtracted from another phase (rev 7 rule).
- **Falsification:** if no filter's slow-minus-fast difference reaches 10 % of the slow cohort's mean observed time, print "not observed: filter time does not separate slow from fast requests in this window".
- **Evidence inputs:** `PreActionFilterTotalMilliseconds`, `PreActionFilterTimings[]`. The array is capped at 20 elements in the projection, keeping name and elapsed time only. Existing timing fields are also used.
- **Overhead:**
  - Two extra projected fields on rows already read (`SlowestReader` for the slow cohort).
  - One new fast-cohort read through the existing Activity/Requests pager. It is narrow: ≤ 24 h, `_id`-hinted, capped at 2 000 rows, maxTimeMS 10 000, concurrency 1 on shared sources.
  - If the cap or maxTimeMS is hit, the result is "partial: <reason>" (§6.4).
  - Bytes per row are measured on a lab or restored copy before live use. The kill switch is the existing source disable.
- **Privileged ops:** none.
- **Retention:** in memory per request. The ledger keeps evidence IDs only (R16).
- **Provenance:** source, window, query mode, the docsExamined line, and both cohort sizes.
- **Plain-language copy:**
  - "Slow requests spent <a> ms on average in <filter>; fast requests spent <b> ms (<window>, n = <n>)."
  - Never "blocked by" or "waiting on".
  - If T0.6b finds the filters run before observed time starts, the copy adds: "This time is not part of the request time shown elsewhere."
- **Positive demo:** demo data has one filter that is much slower on stalls. It ranks first, and A1 holds.
- **Negative demo:** demo data has the same filter times across cohorts and prints "not observed". A source with presence below 95 % prints "not written in this window".
- **Approvals:** T0.7 needs the Mongo owner (off-peak or a read-only copy).
- **Stop condition:** T0.7 shows the fields are null or absent in practice. P1c then closes with a note and is not built.
- **Filter role mapping** (permission/audit/token): an analyst setting, never hard-coded. It is not in v1; v1 shows the raw `FilterName`. The theory engine's X8 would add it.
- **Files:**
  - Backend: `internal/audit/models.go` (RequestRecord gains `PreFilterTotalMs`, `PreFilters`), `internal/store/record.go`, `internal/store/requests.go`, `internal/store/legacy_pipeline.go`, `internal/store/activity.go` (projection), new `internal/audit/filter_timings.go` with its test, `internal/audit/demo.go` (demo fields), new `internal/server/filter_timings.go` with its test, `internal/server/routes.go`, `internal/server/routes_test.go`.
  - UI: new `web/src/lib/filterTimings.ts` with its test, new `web/src/components/FilterTimingPanel.svelte`, `web/src/lib/types.ts`, `web/src/lib/copy/deck.ts`.
  - CSV export and RecordDetail leave the new fields out in v1. A test pins that.
- **Tests:**
  - Decoding when the array is missing, null or over-long.
  - Presence gate at exactly 95 % and just below.
  - The A1 denominator switch, all three branches.
  - Low-n masking.
  - A partial result when the cap is hit.
  - Copy lint on the new strings.

### 1.2 B6g: audit create after quiet gaps (item 2)

- **Slice:** P2-lite extension, T1. Opt-in per run (`idleGaps: true`), because it reads every row in the window, not just stalls.
- **Preconditions:**
  - P2-lite is built.
  - The lab overhead measurement for the full-window read is done before live use (R14: the docsExamined budget is unset until measured). Until then the rule runs on demo, fixture or restored data only.
  - Sync (Web API) rows only. T0.6 shows the Hangfire path stores a pre-insert `CreatedOn`, so its `AuditCreateTime` excludes the insert. Hangfire rows are excluded and counted.
- **Incident question:** "Is a quiet gap before a request the reason audit create was slow in <window>?" This is the cold-connection signature, stated without naming connections.
- **Rule definitions:**
  - Rows are sorted by `RequestInitiatedAt` per source. Gap(i) = start(i) − start(i−1). The first row of the read has gap "unknown" and is excluded.
  - Gap bands: < 30 s (the reference band), 30 s–1 min, 1–4 min, 4–10 min, ≥ 10 min.
  - Per band, the view shows n, median and p95 `AuditCreateTime`, and the rate of B6-slow rows (`AuditCreateTime` ≥ 5 000 ms).
  - **Lift(G)** = the slow rate for gaps ≥ G ÷ the slow rate for gaps < 30 s, for G ∈ {1 min, 4 min, 10 min}.
  - "Raised after quiet gaps" holds when lift ≥ 5, with ≥ 20 rows in the G band and ≥ 20 in the reference band.
  - Label: always **Symptom**. The source has no host field, so gaps are measured across all servers writing to it. An upgrade to Correlation is left to the theory engine (B19-a) if R-TE is granted.
- **Falsification:** lift ≤ 1.5 with ≥ 50 rows in the G band prints "not observed: audit create was not slower after quiet gaps of at least <G> in this window".
- **Evidence inputs:** `RequestInitiatedAt`, `AuditCreateTime`, kind. The projection is minimal.
- **Overhead:**
  - New read through the existing Activity/Requests pager, oldest first.
  - Narrow only: live windows over 24 h are refused with the §6.5 message.
  - Cap `MaxGapRows` (proposal 20 000, set after the lab measurement), maxTimeMS 10 000, concurrency 1 on shared sources.
  - If cut, the result is "partial: <reason>" and lift is withheld.
- **Privileged ops:** none.
- **Retention:** in memory. No row IDs leave the report, only band counts.
- **Plain-language copy:**
  - "After quiet gaps of at least <G>, audit create took <m> s at the median (n = <n>). After gaps under 30 s it took <m0> s (n = <n0>)."
  - M10 caveat line: "Audit create covers setup work and the database write, not just the write."
  - Never "insert took", never "cold connection".
- **Positive demo:** demo rows where audit create exceeds 5 s only after gaps of 10 min or more → "raised after quiet gaps".
- **Negative demo:** demo rows with flat audit create across bands → "not observed". A window over 24 h on a live source → refusal text.
- **Stop condition:** retire per §5.4.3, or earlier if T0.6-style evidence shows gaps cannot be measured (for example, a source that mixes several apps).
- **Files:** new `internal/audit/patterns_idle.go` with its test, plus the shared P2-lite files in §2 W1.
- **Tests:**
  - Band edges.
  - Unknown first gap.
  - Hangfire exclusion count.
  - Lift withheld below 20.
  - Partial when cut.
  - The > 24 h live refusal.

### 1.3 B9 step-change dating (item 3), deferred to P1b

- **Slice:** P1b, T2+, off by default.
- **Preconditions** (any one):
  - a restored copy or owner export (R13 fallback);
  - off-peak with the owner's written approval;
  - the `RequestInitiatedAt` index (§7.1 A5);
  - plus an incident question that needs more than 7 days.
- **Incident question:** "Is a change on <date> the reason <operation> got slower?"
- **Rule definitions (B9, restated):**
  - A two-segment fit on log daily p95 per operation, for the top 10 operations by volume.
  - Each segment ≥ 5 days.
  - Ratio ≥ 2.
  - The traffic ratio stays within [0.67, 1.5], so that a volume shift does not read as a slowdown.
  - At least 3 operations must share the break date ± 1 day.
  - Label: Symptom with a date, never a cause.
  - The ranked "slower / faster" list is the existing comparison Rollup, run with the detected date as the split.
- **Falsification:** no break meeting the thresholds → "no step found in <range>".
- **Overhead:** broad, so never run live (§6.5). Daily rollups are computed once from the export or copy and capped at 120 buckets (theory X10).
- **Privileged ops:** none.
- **Retention:** the rollup file follows the import contract (R16).
- **Copy:** "Daily p95 for <op> moved from <a> s to <b> s around <date>. This dates a change in the numbers; it does not name a cause."
- **Demos:**
  - Positive: a fixture series with a 3× step across 4 operations.
  - Negative: a 3× step with a 3× traffic rise is refused as a volume shift.
- **Interim (no code):** the analyst picks the before/after split by hand in Compare; the movers list already ranks the operations. Once P18 change history exists (XS11), the found date is matched to deploy markers.
- **Files later:** new `internal/audit/stepchange.go` with its test, plus the P1b import path. Not in this round.

### 1.4 B3a: same-clock-minute stall onsets, and the per-minute view (item 4)

- **Slice:** P2-lite extension, T1. It is the audit-only half of B3. B3 itself, which matches onsets against a schedule, still waits for P5 (and P3 backups or P6 Task Scheduler through XS5).
- **Preconditions:** P2-lite is built. The window spans at least 3 days, which on live sources meets the §6.5 issue in D3. Until D3 is decided, run multi-day B3a on a restored copy or export.
- **Incident question:** "Is something on a fixed clock minute the reason stalls started at <mm> on several days in <window>?"
- **Rule definitions:**
  - The onset of each B2 episode is its first stall start.
  - Minute-of-hour is computed in the display time zone. Minute-of-hour does not change under whole-hour offsets or DST.
  - Recurring onset minute: ≥ 3 onsets on ≥ 3 distinct days within ± 2 min of one minute-of-hour, and at least 3× the uniform expectation (n_onsets × 5 / 60 for a 5-minute window over the hour).
  - Label: Symptom. It never names a job.
- **Falsification:** if no ± 2 min window holds onsets from more than 1 day, with ≥ 5 onsets in total, print "not observed: stall onsets do not repeat at one clock minute".
- **Evidence inputs:** the B2 episodes already computed. **No new reads.**
- **Overhead:** none beyond the existing P2-lite read. If `StallsTruncated` is set, B3a is withheld ("partial: stall rows capped").
- **Per-minute "inside a stall" view (UI only):**
  - For one chosen episode, the view calls the existing Activity endpoint for episode ± 15 min (≤ 24 h, existing page caps).
  - It buckets per minute: started, completed, in flight, and stalls in flight.
  - No backend change. Truncation shows "partial".
- **Privileged ops:** none.
- **Retention:** in memory.
- **Copy:**
  - "Stalls started near minute :<mm> of the hour on <k> different days in this window."
  - Hint: "A scheduled task could line up with this; the job list import is needed to check."
- **Demos:**
  - Positive: demo episodes at :<mm> on 3 days → recurring onset minute.
  - Negative: 3 onsets on 1 day → "single day", not recurring. Onsets spread over the hour → "not observed".
- **Files:** new `internal/audit/patterns_recurrence.go` with its test (backend). UI: new `web/src/lib/stallMinutes.ts` with its test, and new `web/src/components/StallMinutesPanel.svelte`.
- **Tests:**
  - ± 2 min edges, including the hour wrap (:59 vs :01).
  - The distinct-day count.
  - The expectation threshold.
  - Withheld when truncated.
  - A DST-crossing fixture.

### 1.5 B12r: repeat of the same operation while the first is in flight (item 5)

- **Slice:** merged into B12, T1. B12 keeps the simultaneous shape (starts within 1 000 ms); B12r adds the staggered shape.
- **Preconditions:**
  - P2-lite is built.
  - Lab overhead measurement before live use.
  - The privacy rules in §7.4/§8.9 apply: the UI shows user IDs as B12 does today, exports use keyed per-case pseudonyms, and thin cells below 20 are masked.
  - MCP is untouched: there is no patterns tool, and R10 keeps 10 tools.
- **Incident question:** "Is repeated starting of the same operation by one user the reason the stall in <episode> grew?"
- **Rule definitions:**
  - Within an episode, a **repeat** is a request whose start falls after the start of an earlier stall by the same user on the same `Controller.Action`, and before that stall completed (or while it is still incomplete).
  - The repeat need not be a stall itself.
  - "Same request" requires an equal `RequestKB` as well. Repeats with a different size are reported separately as "same operation, different request".
  - Output per episode: users with repeats, total repeats, and the largest chain per user.
  - Label: Correlation, matching B12. Copy says "started again", never "retry".
  - **Deferred:** a lift against non-stall controls (theory B11-a), under stop rule 4.
- **Falsification:** no repeats in any episode → "not observed: no user started the same operation again while it was running".
- **Evidence inputs:**
  - Stall rows already read.
  - One new read per episode, through the Requests pager filtered by that episode's stall user IDs (≤ 50, `MaxUserIDs`), for the episode span + 2 min.
  - Narrow, ≤ `MaxPatternRows` per episode.
  - At most 10 episodes, largest first; beyond that the result is partial.
- **Overhead:** as above. maxTimeMS 10 000, concurrency 1 on shared sources.
- **Privileged ops:** none.
- **Retention:** in memory. The ledger keeps evidence IDs only.
- **Copy:** "The same user started <operation> again <k> times while an earlier one was still running, during this stall (<episode>)."
- **Demos:**
  - Positive: a demo user starts one operation 3 times, 10 s apart, inside one stall.
  - Negative: the same user starts different operations, or starts the same one after the first completed → no repeat.
- **Files:** new `internal/audit/patterns_repeat.go` with its test, plus the shared P2-lite files. Exports: `internal/server/export.go` with its test (pseudonyms for the new user rows).
- **Tests:**
  - The in-flight boundary (start equal to the completion time is not a repeat).
  - An incomplete earlier request.
  - Splitting on `RequestKB`.
  - The episode cap.
  - Export pseudonymisation and thin-cell masking.

### 1.6 A3: concentration ratio (item 6)

- **Slice:** P2-lite extension, T1.
- **Preconditions:**
  - P2-lite is built.
  - Traffic denominators come from one existing aggregation over the same window: the analysis Rollup for operations and the `userId` breakdown for users. On a live source this is allowed only when the window is ≤ 24 h. Otherwise "traffic share not read".
  - User concentration adds no new exposure, because the `userId` breakdown dimension already exists. Exports pseudonymise it.
  - AccountId is deferred to A2.
- **Incident question:** "Is <operation or user> the reason for the stalls in <window>, or just a large share of traffic?"
- **Rule definitions (A3, restated from rev 7):**
  - Lift = (stall rows for X ÷ all stall rows) ÷ (all rows for X ÷ all rows).
  - Concentrated when lift ≥ 2, stall count ≥ 10 and all count ≥ 20.
  - Label: Correlation when X appears in ≥ 3 independent episodes (§8.6 guard); otherwise Symptom "single episode".
  - Below 20 the cell is withheld.
- **Falsification:** lift ≤ 1.2 with ≥ 50 stall rows → "not observed: stalls are spread like traffic".
- **Overhead:** no new row reads. One existing aggregation, which may already be cached in the session's analysis. Withheld if `StallsTruncated` is set.
- **Privileged ops:** none.
- **Retention:** in memory.
- **Copy:** "<X> had <a> % of stall requests but <b> % of all requests in this window (<lift>× its traffic share)."
- **Demos:**
  - Positive: a demo operation with 5 % of traffic and 40 % of stalls across 3 episodes.
  - Negative: an operation with 40 % of traffic and 40 % of stalls → "spread like traffic". A user with fewer than 20 rows → withheld.
- **Files:** new `internal/audit/patterns_concentration.go` with its test, plus the shared P2-lite files.
- **Tests:**
  - Lift edges.
  - Withholding below 20.
  - The episode guard (Correlation vs Symptom).
  - The > 24 h live denominator refusal.
  - Truncation.

## 2. Build order and parallel worktrees

The order follows cost: no new reads first, then new narrow reads, then work blocked on outside steps.

| Task | Scope | Files (disjoint) | State |
|---|---|---|---|
| **W1** P2-lite additions (backend) | Commits in order: (1) B3a and A3, which add no reads; (2) B12r; (3) B6g (opt-in) | `internal/audit/patterns_types.go`, `patterns_run.go`, `patterns_demo.go`, `patterns_run_test.go`, `patterns_demo_test.go`, `patterns_parallel_test.go`; new `patterns_recurrence.go`, `patterns_concentration.go`, `patterns_repeat.go`, `patterns_idle.go` and their tests; `internal/server/patterns.go`, `patterns_test.go`, `export.go`, `export_test.go` | **Ready now** on demo and fixture data. Live use of B12r and B6g is blocked on the lab overhead measurement (R14) |
| **W2** P2-lite UI and copy | Findings rows for B3a, A3, B12r and B6g; the per-minute stall view; glossary entries ("quiet gap", "onset minute", "concentration") | `web/src/lib/patterns.ts`, `web/src/lib/types.ts`, `web/src/components/PatternsPanel.svelte`, `web/src/lib/copy/deck.ts`, `web/src/lib/copy/wordings.ts`, `web/src/lib/export.ts`; new `web/src/lib/stallMinutes.ts` and its test, new `web/src/components/StallMinutesPanel.svelte` | **Ready now** against the JSON contract below, with fixture JSON. Start it after any in-flight UI polish work on `PatternsPanel.svelte` lands |
| **W3** T0.6b code read and schema doc fix | Locate the `PreActionFilter*` writer, element names, clock and nesting (read-only, in the writer repo). Correct the schema doc: `AuditCreateTime` covers the whole start phase on the Web API path; add `PreActionFilter*` and `AccountId` | Appendix in `docs/superpowers/plans/2026-10-24-t0-6-code-reads.md`; `docs/api-audit-log-schema.md` | **Ready now** (docs only) |
| **W4a** P1c backend | Projection, model, the A1 rule, demo data, route | `internal/audit/models.go`, `internal/audit/demo.go`, new `internal/audit/filter_timings.go` and its test, `internal/store/record.go`, `requests.go`, `legacy_pipeline.go`, `activity.go`, new `internal/server/filter_timings.go` and its test, `internal/server/routes.go`, `routes_test.go` | **Blocked** on W3, for field names and nesting. T0.7 gates live use only |
| **W4b** P1c UI | Filter-timing panel | new `web/src/lib/filterTimings.ts` and its test, new `web/src/components/FilterTimingPanel.svelte`; edits to `web/src/lib/types.ts` and `web/src/lib/copy/deck.ts` | **Blocked** on W4a, and on W2 merging (both edit `types.ts` and `deck.ts`) |
| **W5** B9 step dating (P1b) | Not started | none now | **Blocked** on a data route (decision D1) and an incident question that needs more than 7 days |

W1, W2 and W3 can run in parallel now; their file sets do not overlap. W4a is disjoint from W1 and can start as soon as W3 lands.

**JSON contract for W2.** All additions to `PatternReport` are optional:

- `onsetMinutes: [{minute, days, onsets, episodes[]}]`
- `concentration: [{dimension: "operation"|"user", key, stallShare, trafficShare, lift, stallN, allN, episodes, withheld}]`
- `repeats: [{episode, userId, operation, repeats, sameSize, longestChain}]`
- `idleGaps: {bands: [{fromS, toS, n, medianMs, p95Ms, slowRate}], lift: [{gS, value, withheld}], excludedHangfire, partial}`
- `findings[].rule` gains `B3a`, `A3`, `B12r`, `B6g`.

## 3. How held items link later

- **P5 (Hangfire/msdb export) and P3/P6 (ERRORLOG backups, Task Scheduler):** B3a onset minutes become the input to B3/B13 schedule matching (XS5). B3a itself never names a job.
- **P18 change history (XS11):** B9 dates are matched to deploy markers.
- **Theory engine (needs R-TE):** these views become extractors:
  - B6g → X7 and check B19-a.
  - P1c → X8 and check A5-c, with the role mapping.
  - B9 rollups → X10 and check A4-a.
  - B12r → X12 and check B11-a.
  - B3a → check A13-a.
  - A3 → check B12-a.

  If R-TE is refused, every item here still stands alone as a descriptive view at Symptom or Correlation, with no verdicts.

## 4. Recommended roadmap rev 10 change-log entry

| Where | Change | Review ref |
|---|---|---|
| §8.2 | **New P1c** "pre-action filter phase" (T1). It carries A1, restated with the denominator chosen by T0.6b nesting, shown as a separate series and never subtracted. Blocked on T0.6b (writer location, element names, nesting) and T0.7 | Incident-gap addendum §1.1 |
| §5 T0 list | **Add T0.6b:** a read-only code read of the `PreActionFilter*` writer | Addendum §1.1 |
| §8.3 | **P2-lite gains B3a** (onset-minute recurrence, audit-only, Symptom), **A3** (concentration by operation and user), **B12r** (repeat of the same operation while the first is in flight, merged into B12; lift deferred) and **B6g** (audit create after quiet gaps, opt-in, Symptom, sync rows only). B3 schedule matching still waits for P5 | Addendum §1.2, §1.4–1.6 |
| §8.2 P1b | **B9 step dating is placed in P1b**, broad, never live. The ranked before/after list is the existing comparison Rollup. Not built until a data route and a question exist | Addendum §1.3 |
| §6.5 | New reads (P1c fast cohort, B6g, B12r) refuse live windows over 24 h. The existing views' 7-day windows are flagged for a decision | Addendum §0, D3 |
| §11 | Rule IDs added: B3a, B6g, B12r. A1, A3 and B9 are restated in their cards. A2 stays blocked on AccountId coverage | Addendum §0 |

## 5. Decisions that need the user

- **D1:** the data route for B9 step dating: an owner export or restored copy (the R13 fallback), off-peak with written approval, or asking the owner for the A5 index. This is owner-side and not the implementer's to rule.
- **D2:** asking the Mongo owner to run T0.7 (read-only copy or off-peak). P1c cannot go live without it.
- **D3:** whether the existing views should also refuse live windows over 24 h, to match §6.5 and R14 as written. This would restrict a feature that is already built.
