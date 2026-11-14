# R1 and R4: where the thresholds come from, and whether the comparison gate is right

Date: 2 Nov 2026. Read-only research. Nothing in `sql/` was read. No web source was used, so every statistics claim below is marked [Inference] (my own reasoning from the numbers) or comes from a repo file.

Paths are relative to `D:\Projects\api-audit-explorer`. `P` = `prototype/tracing-paper-PROTOTYPE.html`. `T` = `internal/investigate/thresholds.go`.

Verdict key:
- EVIDENCE-BASED: a measurement, a spec derivation or a recorded finding backs the number.
- CONVENTION: an owner decision or a round number, pinned by a test, but no data or reasoning recorded.
- PLACEHOLDER: a starting point somebody typed in, said to need tuning or validation.

## 0. Headline findings

1. No threshold in this tool is EVIDENCE-BASED against a measured baseline. The best-supported numbers are the ones with a recorded reason in `.dev/reports/p5-implement.md` (5-sample minute P95, 100 ms floor inside onset, `retryMinShare` 0.02 from a golden finding). Those are reasoned rulings, not measurements.
2. The red line (1.5x), the layer limits (2% errors, 25% slow calls) and the comparison gate (30 events, 80% coverage) exist only in the prototype. The Go engine (`internal/investigate`) has none of them. The review doc says so itself: A3 (`CHARTS-AND-STATS-REVIEW.md:110`) calls 1.5x a prototype default, B0 (`:205-212`) calls 80%/30 prototype defaults, B9 (`:312`) says the 2%/25% limits are hidden from the user. `P:254` carries the comment "prototype rule (not in the spec)".
3. The prototype gate and the real engine disagree. Prototype: at least 30 reference events plus at least 80% coverage, once per lane over the whole reference window. Real engine: at least 20 valid samples (`audit.MinSampleCount`) plus a minute-based gap rule (T: `GapWarnMinutes` 2, `GapRefMinPerMinute` 5). Whichever is kept, the other has to change (see section R4.6).
4. Two concrete defects in the gate (R4.3): the ratio formula divides by full window length, not by covered minutes, so an 80%-covered reference can turn a true 1.2x into a displayed 1.5x (red); and there is no minimum at all on the incident side.
5. Owner decisions in `prototype/CHARTS-DECISIONS.md` (decisions 2-4) already say: one 1.5x rule, "1.5x or more" is red, and show 2%/25% on the rules card. Those fix the wording, not the values.

## R1. Threshold provenance

### R1.1 Table

| # | Threshold | Prototype (file:line) | Real code (file:line) | Reason recorded anywhere? | Verdict |
|---|---|---|---|---|---|
| 1 | Red above 1.5x (rate or P95 versus reference) | `P:1095, 1098, 1305, 1325, 1329, 1331, 1334` use `> 1.5`; `P:1197` builds the rate; `P:1781-1795` (`layerRead`) uses `>= 1.5` | None in investigate. Other, unrelated 1.5 values: `T:162` StepRampBelowFactor, `internal/audit/stepchange.go:33` stepTrafficHigh, `internal/audit/patterns_idle.go:33` idleFalsifyLift, `internal/calltrace/checks.go:298` | `CHARTS-AND-STATS-REVIEW.md:110` (A3) says "prototype default"; `:502-504` (5.3) lists the `>` versus `>=` inconsistency; `CHARTS-DECISIONS.md` decisions 2-3 pick one rule, "1.5x or more". No reason for the value itself | PLACEHOLDER |
| 2 | Layer limit: 2% server errors | `P:1781-1795`: `s.c5 / n >= 0.02` for Application and Web server | None. `T` `RetryMinShare` 0.02 is a different quantity (retry share, from a golden finding, `.dev/reports/p5-implement.md` ~:40-60). `ClockAnomalyShare` 0.01 is also unrelated | `CHARTS-AND-STATS-REVIEW.md:312` (B9) says hidden; `CHARTS-DECISIONS.md` decision 4 says show it. No reason | PLACEHOLDER |
| 3 | Layer limit: 25% slow calls (Database) | `P:1781-1795`: `s.slow / n >= 0.25`; also "blocking" fires on any report (`mark > 0`) | None | Same as row 2. The "2,005 slow calls at 40%" example in the calculations guide (quoted at `CHARTS-AND-STATS-REVIEW.md:526`) is demo output, not a basis | PLACEHOLDER |
| 4a | Onset: 2x the reference P95 | `P:1487` (`SUS_TH.onsetFactor` 2), `P:1599` (`susOnset`) | `T:158` OnsetFactor 2; pinned by `thresholds_test.go:145` | Decision A24 in `buildout/plan/decisions.md:37-41` (A22-A27 block). No rationale recorded. The original design used baseline + 3 x MAD for 3 of 5 minutes (`buildout/research/investigation-walkthrough.md:190, 385, 422`); A24 replaced it with no recorded reason | CONVENTION |
| 4b | Onset: held for 5 minutes in a row | `P:1487` onsetRun 5 | `T:159` OnsetRunMinutes 5; pinned `thresholds_test.go:146` | Same A24; user-facing text `docs/user/guided-analysis.md:40-70` | CONVENTION |
| 4c | Onset: minimum samples per minute | `P:1487` onsetMinSamples 20 | `T:160` OnsetMinSamples 5 | `.dev/reports/p5-implement.md` ruling (~:40-60, :76): a 5-sample minute P95 was chosen, with a 100 ms floor inside onset. Prototype keeps 20 and no floor (`prototype/video/HOW-THINGS-ARE-CALCULATED.md:134, 301-317`) | Real: reasoned ruling (closest to EVIDENCE-BASED, but not measured). Prototype: PLACEHOLDER. The two disagree |
| 5 | The 1-second "slow call" line | `P:1229, 1277, 1334, 1789` (text "over 1 s"); also a count rule "duration > 1000 ms" (`CHARTS-AND-STATS-REVIEW.md:225`) | `internal/audit/rollup.go:38` `DefaultRollupSlowMs = 1000.0` (callers may pass 1 to 3,600,000, `:39, :100-104`); a 1 s XE predicate appears in `buildout/research/investigation-walkthrough.md:681` | Round number; no reason. `CHARTS-AND-STATS-REVIEW.md:230` warns the shipped SQL importers cannot yet produce per-call durations, so the card is design only | CONVENTION |
| 6 | Change is real: 30% AND 100 ms | `P:1487` chgPct 0.3, chgMs 100 | `T` ChangeMinPercent 30, ChangeMinMs 100; pinned `thresholds_test.go:143-144` | Decision A23. No rationale | CONVENTION |
| 7 | Sample group sizes 20 (P95) and 100 (P99); clock tolerance 1 s; arrival rate 40/s | not applicable | `T` GroupSizeP95 20, GroupSizeP99 100, ClockToleranceMs 1000, ArrivalRatePerSec 40; `thresholds_test.go:134-135, 141-142` tie the group sizes to `internal/audit/thresholds.go` MinSampleCount 20 and P99MinSampleCount 100 | Decision A22. Sizes are tied to existing audit constants (consistency), not justified. Original note in `investigation-walkthrough.md`: "starting point: 20 for P95, 100 for P99" | CONVENTION |
| 8 | Gap rule: 2 minutes warn, reference median at least 5 per minute | not applicable | `T` GapWarnMinutes 2, GapRefMinPerMinute 5; pinned `thresholds_test.go:147`; `facts_test.go:101` exercises a gap case | Decision A35. No rationale | CONVENTION |
| 9 | 80% coverage (several different meanings) | `P:255-262`, `P:1197` (comparison gate); `P:1487` scopeAllShare 0.8 | `T:168` ScopeAllShare, `T:174` PartsMinCoverage, `T:177` IISOffsetClusterShare; `internal/server/lanes.go:489` laneCoverShare; `internal/ledger/guards.go:44`; `internal/resource/rules/rules.go:50` MinCoverage; `web/src/lib/callTrace.ts:88` MIN_EVENT_PCT; `web/src/lib/guided/afterFix.ts:18` PARTLY_SHARE | None found. It is repeated across at least eight places, which is a convention, not evidence | CONVENTION |
| 10 | 30 events minimum for a comparison | `P:254` SUFFICIENT_REF = 30, comment "prototype rule (not in the spec)"; used `P:258` and `P:1197` | No equivalent; real minimum is 20 valid samples (`internal/audit/overview.go` minReferenceSamples = MinSampleCount) | `CHARTS-AND-STATS-REVIEW.md:205-212` (B0), `:515` (5.4 Q3) | PLACEHOLDER |
| 11 | Planner starting points: audit dip 0.3, change lead 30 min, scope-all 0.8, parts coverage 0.8, retry share 0.02, clock max span 6 h, and similar | `P:1487` SUS_TH | `T:148-189` (`Current()`); header `T:27-30` says fields with no decision number are "planner starting points ... tuned on the demo data" | The header is the reason, and it says they are placeholders. RetryMinShare has a recorded golden finding (`.dev/reports/p5-implement.md`) | PLACEHOLDER (RetryMinShare: reasoned ruling) |
| 12 | Vendor-claim cutoffs: disk 15 ms, CPU 80%, MongoDB version gates | `P:1487` diskMs 15, cpuPct 80 | none found | `CHARTS-AND-STATS-REVIEW.md:517` (5.4 Q5) asks for a primary source | PLACEHOLDER, unverified. I did not fetch any vendor source, so I cannot say whether 15 ms or 80% matches published guidance |
| 13 | Bucket ladder: 1-minute buckets up to 2 hours, at most 120 bars, UTC-hour-aligned axis | `P:304-306` (RUNGS, ladderWidth), `P:321` bucketForWindows | web side not traced | Spec reference in a comment at `P:307` ("spec 4.5 / 7"); 120 is a chart-width choice | CONVENTION |

Test coverage: `thresholds_test.go:46-84` (history) and `:97-130` (fingerprint moves) stop silent change; `:132-154` pins A22, A23, A24 and A35 values. `testdata/investigate/thresholds.history.json` holds one entry (version `2026-10-27`, fingerprint `3ccee624d8e4e32a`). These tests prove a value did not move. None of them says why it has that value.

### R1.2 One way to validate each placeholder, using data the product already holds

Principle: do not read customer data. Use (a) the synthetic generators already in the repo (`audit.NewDemo`, the golden-case builders in `internal/investigate/golden_test.go` and `scenario_test.go`, the prototype's deterministic `opMinute` / `metricMinute`), which know the true injected onset time and magnitude, and (b) a product-side, privacy-safe "baseline variability" report computed by the product on the owner's machine from stored hour rollups (spec 4.5), which emits only the distribution (percentiles), never rows.

| Placeholder | Validation method |
|---|---|
| Red above 1.5x (rows 1) | Seeded replay. Generate 1,000 no-incident days with `audit.NewDemo` at 20, 30 and 100 events per minute, split each into two adjacent equal windows (15, 60 and 150 minutes), and record the share of window pairs with ratio above 1.5 (rate and P95 separately). That share is the false-red rate. Then inject known 1.3x, 1.5x, 2x and 3x steps and record the hit rate. Choose the line where the false-red rate is at or below an agreed target (for example 1%). The product-side report then repeats the false-red count on real rollups of a quiet period and only prints the percentiles. |
| 2% errors (row 2) | Same seeded replay for the share of 5xx per window in no-incident data; plus the product-side report of per-lane 5xx share percentiles. If a lane's quiet-period p95 share is already near 2%, a fixed 2% is wrong for that lane and a relative rule (a multiple of the lane's own reference share, with a floor) is needed. |
| 25% slow calls (row 3) | Same method on the SQL lane, but only after the importers produce per-call durations (`CHARTS-AND-STATS-REVIEW.md:230`). Until then it cannot be validated with real data; use the prototype's `metricMinute` series and the `sql-pool-exhausted` golden case as the incident, and the `no-incident` golden case as the quiet baseline, and report the margin between them. |
| Planner starting points (row 11), 4c prototype value | For each field, a one-at-a-time sweep (half, as-is, double) over the five golden cases plus 1,000 seeded no-incident and 1,000 seeded injected-onset runs. Report false-onset rate and onset delay versus the known injected minute. Keep the value only if the result is flat around it. This is cheap to build because `TestThresholdsFingerprintMoves` already iterates every numeric field by reflection. |
| Vendor-claim cutoffs (row 12) | Not data-validatable. Needs a primary vendor source for each number, or the card should say "common rule of thumb" and not present it as a finding. Unverified. |
| 30-event minimum (row 10) | See R4.3 and R4.5. The false-red rate versus reference count can be measured by the same seeded replay (it is a one-parameter sweep). |

CONVENTION rows (A22, A23, A24, A35) do not need a data change now. They would benefit from the same seeded sweep being recorded once, as a sentence in `buildout/plan/decisions.md`, so the numbers stop being unexplained. The A24 replacement of the MAD design is the one most worth recording.

## R4. The comparison gate

### R4.1 Definition

- Prototype constant: `P:254` `SUFFICIENT_REF = 30` ("prototype rule (not in the spec)").
- Prototype classifier: `referenceState` `P:255-262`. Order of checks: no events means state `none`; fewer than 30 events means `sparse`; coverage under 0.8 means `partial`; else `ok`. Coverage for the audit lane is minutes with data divided by minutes in the window (`auditWindow`, `P:~237-248`, uses `available(m)`).
- Prototype comparison builder: `refCmp` `P:1197`: `cov >= 0.8 && refTot >= SUFFICIENT_REF ? { rate: (tot/incMinutes) / (refTot/refMinutes) ... } : { rate: null, why: ... }`.
- Where applied: once per lane, over the whole reference window, in the tracing view (`P:1046` for the audit lane card, `P:1208` for other lanes with `coverageFraction` over import intervals, `P:286-289`). Layer read (`P:1781`) and the figure cards (`P:1305-1334`) only compare when `cmp.rate != null`.
- Not applied: to the incident window (no minimum there); to individual buckets (the chart draws every bucket whatever its count); to P95 separately from the rate (the P95 ratio uses the same gate, so P95 is allowed with the same 30 events as a simple count).
- Real engine: no such gate. It uses `audit.MinSampleCount` 20 valid samples for a reference (`internal/audit/overview.go`, state `insufficient_data`) and the minute-based gap rule A35 (gap flagged only when the reference median is at least 5 per minute; gaps of 2 minutes or less warn; longer gaps make the affected minutes "Cannot test yet"; see `prototype/video/HOW-THINGS-ARE-CALCULATED.md:301-317`). The prototype notes this difference itself (same file, lines 134 and 355-362).

### R4.2 What the windows and buckets actually look like

| Case | Window | Rate | Events in reference | 30 events equals | Bucket (from `P:304-321`) | Events per bucket |
|---|---|---|---|---|---|---|
| Demo (calculations guide) | 150 minutes incident, equal reference | about 28 per minute (4,216 requests quoted at `CHARTS-AND-STATS-REVIEW.md:526`; 150 x 28 = 4,200, consistent) | about 4,200 | about 1.1 minutes of traffic | span over 2 h, up to 24 h: first width in 5, 10, 15 minutes with at most 120 bars, so 5 minutes (30 bars) | about 140 |
| Demo, 1-hour or shorter window | 60 minutes | about 28 per minute | about 1,680 | 1.1 minutes | 1 minute | about 28 (below 30) |
| Golden cases (`golden_test.go`, `scenario_test.go`, `stdWindows`) | 60-minute reference 09:00-10:00, 90-minute incident 10:00-11:30 (150 minutes combined), 30 per minute | 30 per minute | 1,800 | 1 minute | 1 minute (span at or under 2 h per window) | 30 |
| Engine demo (`demoInput`, `audit.NewDemo`) | 15-minute reference, 15-minute incident | about 28 per minute | about 420 | 1.1 minutes | 1 minute | about 28 |
| Quick range "Last 15 min" (`P:1197` QUICK list near `P:317`) at 1 per minute (low-traffic endpoint) | 15 minutes | 1 per minute | 15 | 30 minutes | 1 minute | 1 |

Reading: on every case the project actually uses, the 30-event floor passes by a factor of 40 to 60, so it is never the thing that decides a golden or demo result. It matters only for low-traffic endpoints and short windows, which no golden case covers.

### R4.3 Where the gate blocks a valid comparison or lets a weak one through

Statistical noise arithmetic, [Inference] (standard Poisson approximation: count n has standard deviation square-root of n; the log of a ratio of two independent counts R and I has standard deviation sqrt(1/R + 1/I); chance of a false red is the normal tail above ln 1.5 = 0.405):

| Reference count R = incident count I (true ratio 1.0) | SD of log ratio | z for 1.5x | Chance of a false red (one side) |
|---|---|---|---|
| 20 | 0.316 | 1.28 | about 10% |
| 30 (current gate) | 0.258 | 1.57 | about 6% |
| 50 | 0.200 | 2.03 | about 2% |
| 100 | 0.141 | 2.87 | about 0.2% |

So a reference that just clears the 30-event gate gives roughly one false red in sixteen quiet comparisons, before any other effect. That is an estimate from a Poisson model; real traffic is burstier than Poisson, so the true number is worse. Not measured against real data (unverified).

P95 is noisier than a count. With 30 events the top 5% is 1.5 requests, so the "P95" is effectively set by the one or two slowest calls. The repo already treats this as too thin for P99: `GroupSizeP99` 100 / `audit.P99MinSampleCount` 100. P95 from 30 events has no repo rule backing it. (The prototype's P95 is also only an estimate from per-minute weighted averages, see `P:1305` tooltip "Estimated from per-minute figures"; the real product will use all request durations.)

Cases:

1. Blocks a valid comparison (false block).
   - A 12-minute outage inside a 60-minute reference: coverage is 48/60 = 0.80, passes. A 13-minute outage: 47/60 = 0.78, the whole comparison is switched off for that lane. The real engine would instead mark only the missing minutes "Cannot test yet" and compare the rest, as long as the reference median is at least 5 per minute. Result: the prototype throws away 47 good minutes (about 1,400 events at 30 per minute) because of 13 bad ones.
   - The same on the 15-minute engine demo reference: 3 missing minutes pass (12/15 = 0.80), 4 missing minutes block.
   - A quiet endpoint with 0.5 requests per minute over a 60-minute reference has 30 events and passes by exactly one event; at 0.4 per minute (24 events) it is blocked, though the data may be 100% covered. The threshold flips on one request, which is a bad property for a hard gate.
2. Lets a weak comparison through (false pass).
   - Concentrated events: 30 events in one minute of a 150-minute reference, with the import interval covering all 150 minutes. Coverage by import interval is 100% (`coverageFraction`, `P:286-289`, counts the time the import spans, not minutes that contain events), count is 30, so the gate passes. The reference rate is 30/150 = 0.2 per minute. Against the demo incident rate of 28 per minute that shows a 140x ratio, red. The reference was a single burst, not a baseline.
   - Coverage is measured over the whole reference window, never per bucket. A reference with 80% coverage can have all of its gap in the last third of the window, right next to the incident, which is the stretch that matters most for "what changed just before".
   - No incident-side gate: if the incident window has 3 events, a ratio is still computed from the incident count against a 30-event reference.
3. Denominator bias (the most important finding). `refCmp` (`P:1197`) divides the reference total by the full reference length, but the total only contains events from covered minutes. With coverage c the displayed reference rate is c times the true rate, so the ratio is inflated by 1/c. At the allowed minimum c = 0.8 that is 1.25x. Example: true ratio 1.2, reference 80% covered, displayed ratio 1.2 / 0.8 = 1.5, which is red under the "1.5x or more" rule (decision 3). The incident side has the opposite error: a gap in the incident window lowers the incident rate and can turn a real 1.6x into a green 1.3x (coverage 0.8). Neither side is gated or corrected. (Whether the audit lane reaches `refCmp` with minutes-with-data coverage or import coverage was not traced to the end: unverified. The formula effect holds for any lane.)
4. Bucket size interacts with the count rule. If the 30-event rule were applied per bucket, it would blank every one-minute bucket on the demo (about 28 events per minute) and sit exactly at the limit on the golden cases (30 per minute). It must stay a window-level rule. But the chart currently draws a per-bucket P95 line from those same one-minute buckets with no floor (`P:1105`, `P:1232-1245`), where a bucket P95 from 28 events is the second-slowest request. The real engine uses a per-minute floor of 5 samples for onset (`T:160`), so the prototype's chart is noisier than the engine's detector.

### R4.4 Verdict

Keep the idea of a two-part gate. Change both numbers and the way coverage is measured. The prototype's 30 is a PLACEHOLDER with a measurable false-red rate of about 6% at the threshold; the 80% is a CONVENTION that matches seven other 0.8 values in the repo and can stay, provided the ratio is computed over covered minutes.

### R4.5 Recommendation (specific values)

1. Ratio over covered minutes. Compute both rates as events divided by minutes that have data (the same definition on both sides). This removes the 1.25x inflation and the incident-side dilution. Keep coverage at 0.8 for the reference and add a separate 0.8 for the incident window (below it, show the figure but mark "incident window only partly covered").
2. Two minimums, by metric (reference events, after dropping uncovered minutes):
   - count or rate comparison: at least 60 events (false red about 1% at equal sizes, from the table; [Inference]);
   - P95 comparison: at least 100 events, matching `GroupSizeP99` and the real engine's existing P99 floor;
   - below those, show the figure with "earlier period has too little data" (existing wording at `P:1305`) and do not colour it.
   Do not raise the real engine's 20-sample reference floor without the seeded sweep in R1.2; just align the vocabulary (one named threshold in `T`, not a prototype constant).
3. Spread rule, to stop the burst case: also require that at least 50% of the covered reference minutes contain at least one event, or that no single minute holds more than 25% of the reference events. For a low-traffic endpoint where most minutes are legitimately empty, skip the spread rule when the reference median is under 1 per minute (mirrors the engine's `GapRefMinPerMinute` 5 idea) and rely on the larger event minimum instead. Values here are proposals; validate with the seeded replay.
4. Incident-side minimum: at least 20 events (the existing sample floor) before a ratio is coloured; below that show the number, grey.
5. Gaps: adopt the engine's rule (gaps of 2 minutes or less warn; longer gaps remove only those minutes) instead of the all-or-nothing 80% switch, so a 13-minute outage no longer discards 47 good minutes. Keep 80% only as a "comparison is mostly built from partial data" warning.
6. Put the constants in `T` with the others (version-bump and fingerprint test cover them), and make the prototype read the same names, so the prototype stops being a second source of truth.
7. Chart: grey out or dot the per-bucket P95 for buckets with fewer than 20 events (the existing `audit.MinSampleCount`); this is display only.

### R4.6 Test cases that would pin the new values

Add as unit tests next to `internal/investigate/golden_test.go` / `scenario_test.go` (using the existing `stdWindows` and generator with `perMin` set explicitly), and as scripted cases for the prototype:

1. `ref_exactly_at_minimum`: reference of exactly 60 events spread evenly over 60 covered minutes, incident at true 1.2x: expect comparison allowed, not red. With 59 events: expect "too little data", grey.
2. `ref_one_burst`: 150-minute reference with 100 events all in one minute, imports cover the full window: expect the spread rule to block the comparison.
3. `ref_80pct_covered_true_1_2x`: 60-minute reference with 12 consecutive minutes missing and true ratio 1.2: expect displayed ratio 1.2 (covered-minutes denominator), not red. Mutation check: with the old full-span formula the same input yields 1.5 and must fail the test.
4. `ref_13_minute_gap`: same with 13 missing minutes: expect the comparison to remain allowed on the 47 covered minutes with a warning, not blocked.
5. `incident_gap_dilution`: incident window 80% covered, true 1.6x: expect displayed 1.6x, red.
6. `incident_thin`: incident window with 5 events against a healthy reference: expect grey, no colour.
7. `low_traffic_endpoint`: 0.5 per minute, 60-minute reference (30 events): expect "too little data" (under 60); 1.5 per minute over 60 minutes (90 events): expect allowed for rate, blocked for P95 (under 100).
8. `p95_thin`: 80 events with one outlier at 10x: expect no P95 ratio; with 120 events: expect P95 ratio allowed.
9. `exact_boundary_pair`: for each new constant, one case at the value and one just below (a one-event flip is the property to pin, so the boundary is a conscious choice).
10. `demo_150_min_stays_ok`: the 150-minute, 28-per-minute demo must still produce a comparison on every lane (about 4,200 events) and `no-incident` golden must still show no red, so the stricter gate does not break the existing cases.
11. `false_red_rate`: seeded no-incident replay (R1.2) asserting the share of window pairs coloured red stays under 1% at 60 events and 100 events per window.
12. Threshold table: extend `TestThresholdsMatchDecisions` (`thresholds_test.go:132-154`) with the new names once an owner decision (an A-number) exists, so they stop being "planner starting points".

## Unverified items

- No measured false-red or false-onset rate exists anywhere in the repo; all rates in R4.3 are Poisson arithmetic ([Inference]). Real traffic is likely burstier.
- Source of the 80% coverage for the audit lane at the point `refCmp` is called (minutes-with-data versus import interval) was not traced end to end.
- Disk 15 ms, CPU 80% and MongoDB version gates (`P:1487`) were not checked against any vendor source.
- Exact line of decision A24 within `buildout/plan/decisions.md` (cited as the A22-A27 block, lines 37-41).
- Whether a web-side (TypeScript) copy of the 1.5x rule or the 30-event rule exists outside the prototype was not exhaustively searched; only `callTrace.ts:88` and `afterFix.ts:18` (both 80% values) were found.
- The `.dev/reports/p5-*.md` line numbers are approximate (~:40-60, :76).
