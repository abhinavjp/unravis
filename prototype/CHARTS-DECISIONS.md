# Charts and stats: decisions (owner answers, 2 Nov 2026)

Source: Decision Desk answers pasted by the owner. Companion to `CHARTS-AND-STATS-REVIEW.md`.
Status: decided, not yet applied to the prototype or the real app.

## Decisions

| # | Topic | Answer | What it means |
|---|---|---|---|
| 1 | Three low-value items (Completion, 4xx, Databases block) | A: trim the noise, finish the half-done | Cut the noise; give the half-built ones a real comparison or remove them |
| 2 | 5xx red rule | A: Timeline rule everywhere | One rule (1.5× the rate) on Run analysis and the Timeline |
| 3 | Where red begins | A: 1.5× or more is red | Cards and lane check use the same boundary |
| 4 | Hidden limits (2% errors, 25% slow calls) | A: show on the rules card | Add both to the rules card |
| 5 | "About 1 times slower" headline | A: print the tested ratio, name the operation | Headline prints the ratio it actually tests |
| 6 | Blocking: card vs suspect | A: align them | One definition of blocking in both places |
| 7 | Pinned suspect Q1 missing | B: check the real catalogue first | Done, see Check results |
| 8 | "Completion" | A: keep, label "results found" | Rename the label |
| 9 | Which limits users can change | A: split by purpose | |
| 10 | Build order for real data | A: SQL data, then HTTPERR, then the sampler | |
| 11 | "Running in this demo since 19 Oct" | **Revised 2 Nov: keep the line as-is** (supersedes A) | No rewording, no demo-data label |
| 12 | Invented numbers | **Own answer** (see below) | Overrides the recommended A |
| 13 | How much of the real app to show | A: stay at 25, add a "not shown" note | |
| 14 | How P95 is worked out | A: prototype keeps the label, product uses a real percentile | |

### Decision 12, owner's words

"No need to label demo data anywhere, as this is just wireframe. We need to ensure whatever stats and data we bring are actually fetchable and meaningful and useful."

Consequences:
- Do not add demo-data labels to the wireframe. Decision 11 is resolved the same way: the owner confirmed on 2 Nov to keep the "19 Oct" line as it is.
- The test for every stat is: can the product really fetch it from a named source, is it meaningful, is it useful. This is the fetchability audit below.

## Research (all: hand to another agent in parallel)

| # | Question | Brief |
|---|---|---|
| R1 | Where do our thresholds (1.5×, 2%, 25%, 2× for 5 min) come from? | Find the origin of each number in code and docs, say whether it is evidence-based or a placeholder, and propose how to validate each against real incident data |
| R2 | A true P95 across minutes | Per-minute P95s cannot be averaged into a period P95. Compare options (merge raw samples, histogram buckets, t-digest) for cost, accuracy and what the audit store can supply |
| R3 | Is "earliest layer goes red" safe? | Test the rule against the five golden cases and the real incidents in `testdata/investigate/`; find cases where an earlier layer goes red for an unrelated reason |
| R4 | Is the comparison gate right (80% coverage, 30 events)? | Check the gate against real window sizes; find windows where it blocks a valid comparison or passes a weak one |

Each agent returns findings with evidence (file and line, or query and result), not opinions.

## Check results

| Check | Result |
|---|---|
| Find Q1 in the real catalogue | **Found.** `internal/investigate/catalog.go:58`, "Clock, time-zone or DST error", evaluated in `evaluate.go:186`. The real catalogue has 44 entries. Decision 7 is settled: Q1 is real, so the wireframe is missing it (add it to the 25). |
| Re-run the demo numbers (4,216 requests, 522 ms, 2,005 slow calls) | **Reproduced with the incident window selected** (09:00–11:30 on 20 Oct, reference 06:30–09:00): 4,216 requests, 522 ms. The default 1 h window shows 1,779 and 389 ms, which is why they looked wrong. Side finding: with that 06:30–09:00 reference, both 5xx cards show "Cannot compare with the earlier period". |
| Read Evidence tabs (History, Failures) | Not done |
| Read FLOW-COVERAGE.md, FLOWS-GUIDE.md, NOTEBOOKLM-SOURCE.md for gaps | Not done. Files exist: `prototype/FLOW-COVERAGE.md`, `prototype/video/FLOWS-GUIDE.md`, `prototype/video/NOTEBOOKLM-SOURCE.md` |
| Non-expert comprehension test | Not done. Needs a person or a fresh agent with no context |

## Research results (full reports in `prototype/research/`)

**Fetchability audit** (`FETCHABILITY-AUDIT.md`): 23 items. 13 fetchable now (A1–A11, B2, B12), 9 need an importer or change (B1, B3, B5–B11), 1 not fetchable as designed (B4, the SQL lane panel). Not fetchable inside B1/B10: SQL calls over 1 s, blocking, top wait, per-database file I/O.
- The real Part B is not the wireframe's three-lane screen. Real equivalents: the guided `/investigation` engine, `internal/timeline`, the resource panel and the live SQL/Mongo lanes (`internal/dbhealth`).
- 9 of the 25 wireframe suspects (S8–S12, D16, D17, M11, M12) are not in the real 44-entry catalogue. 8 real entries (D3, D5, D11–D15, M6) are fed by live lanes and the wireframe never shows them.
- The Blackbox recorder, Windows sampler and H1 exist, but record only CPU, memory, disk queue, network and processes. SQL disk latency, memory grants and top wait are fetched nowhere.
- The HTTPERR importer feeds the timeline but no engine signal reads it. `/series` returns only min/mean/max/count, so IIS P95 and status-class stacks need a small change.
- The real app already has a 4xx delta (`WhereList.svelte`), a histogram P95, one 2× headline ratio and a 5-sample onset minimum. So part of D1 ("add a 4xx change number") is already real.

**R1 thresholds / R4 gate** (`R1-R4-THRESHOLDS-AND-GATE.md`): no threshold is evidence-based against measured data. 1.5×, 2%, 25% and the 30-event / 80% gate are prototype placeholders; the engine's A22–A24/A35 values and the 1 s slow-call line are convention. The gate passes by 40–60× on all golden cases, so it only bites on low-traffic endpoints and short windows. At 30 events and 1.5× a Poisson model gives about 6% false red. An 80%-covered reference turns a true 1.2× into a displayed 1.5×. Recommended: compute rates over covered minutes, at least 60 reference events (100 for P95), a spread rule, an incident floor of 20, the engine's gap rule, constants moved into `thresholds.go` (12 pinning tests listed).

**R2 percentile / R3 earliest-red** (`R2-R3-PERCENTILE-AND-EARLIEST-RED.md`): the prototype's "Observed P95" is a call-weighted mean of per-minute P95s, so it can be wrong in both directions (example: 2,550 ms vs a true 100 ms). Recommended for the product: the fixed-bucket log histogram merged by summing counts (about ±5%, already in Go); exact nearest-rank only where rows are loaded; never Mongo `$percentile` (7.0+, approximate, forbidden by `pipeline_test.go:58`). The earliest-layer rule is already conservative (4 of 5 golden cases refuse to name a layer). Add guards: a configured but empty lane counts as unread, the earliest tier must stay elevated, a wider lane/audit skew margin, load is not an origin when retries or errors precede it (9 tests listed).

## Fixes applied to the prototype (2 Nov, via the Runner Hub, uncommitted)

| Packet | Decisions | What changed in `tracing-paper-PROTOTYPE.html` |
|---|---|---|
| A | 2, 3, 4, 5, 6 | Constants `RED_RATIO` 1.5, `ERR_SHARE` 2%, `SLOW_SHARE` 25%, `MARK_RATIO` 2. One 5xx rule (1.5× the per-minute rate) on Run analysis and the Timeline. Red at ≥ 1.5× everywhere. Headline prints the tested ratio and names the operation ("Payroll.Run: its P95 is 4.9 times…"). Rules card lists the red figure, errors and slow limits. Blocking card follows the suspect's rule. |
| B | 7, 13 | Q1 "Clock, time-zone or DST error" added (26 suspects). "Not shown" note against the 44-entry catalogue. |
| C | 1, 8 | "Completion" renamed "Results found" and hidden unless below 99%. 4xx tile shows a change figure (never red). Databases block collapsed. |

Checked in the browser pane: headline, tiles, Q1 and the closed Databases block. `node --check` passes. `flow-check.cjs` was updated (26 suspects, K13 opens the Databases block) but cannot run here, because Playwright is not installed.

## Next steps

1. Run `flow-check.cjs` where Playwright is installed.
2. Read the Evidence tabs (History, Failures) and the three companion docs for gaps.
3. Non-expert comprehension test (a person or a fresh agent with no context).
4. Product work from the research: percentile from the merged histogram (R2), threshold constants in `thresholds.go` with pinning tests (R1/R4), the four earliest-red guards (R3), and the build order SQL data → HTTPERR → sampler (D10).
