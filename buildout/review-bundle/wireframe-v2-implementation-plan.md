# Wireframe v2: implementation plan and work packets (2026-10-29)

Inputs: `2026-10-29-owner-issues-triage.md` (RC1-RC5, themes A-H), `2026-10-29-research-long-windows.md` (R2), `2026-10-29-research-sql-mongo-host-evidence.md` (R1).
Scope: the HTML wireframe (`buildout/wireframes/parts/*` + `build-guided.js`) and its spec. No Svelte (`web/`) change until the owner approves the wireframe.
Rules: all implementation by Runner Hub; I plan and review; every review goes to a different model family from the implementer; one packet = one part file (or one small edit set); each packet ends with `node build-guided.js` clean and `node --check` on the script.

## 1. What the research settled

- **Evidence rows (R1):** each suspect gets {claim, source, healthy-vs-suspect rule, chart type, colour rule, "sampled?" flag}. Green/amber/red/gray; **gray = the sampling never saw the window (absent, not negative)**. Strength ladder: persisted event bracketing the window (hard) > wait/counter delta sharing the timestamp (strong) > single snapshot gauge that merely coincides (weak).
- **Charts needed:** timeline with markers, stacked area (100% for waits), swim-lane (blocking chains, nearby activity), heatmap (resource, day×hour), small multiples. Four shared helpers cover everything.
- **Long windows (R2):** rollup with a visible bucket-size label, 3-level drill (day, hour, minute); recurrence = calendar heatmap + overlay of N occurrences + explicit recurrence selector ("last Thursday, monthly"); estimate line before Run, background run with Stop, server-side pagination, non-blocking overlap warning; "Why" opens chart + data table; ruled-out suspects collapsed with one-line reasons.
- **Open feasibility risk:** the wireframe may promise evidence the read-only collectors cannot gather (Query Store, deadlock XE, ticket counts). Packet F1 checks this before screens are built; infeasible items are drawn as gray "not collected" states, not removed.

## 2. Packets

Size target: under ~15 KB of output each, so a 16K-token reply cap is never hit. Lane key: **S** = local `droid-sonnet-5` (implementer); **G** = cloud `droid-az-grok46` (works); **D** = cloud `droid-az-deepseek-v41-flash` (research/analysis, works); **K** = local `droid-kimi-k3` or `droid-glm-5-3` (reviewer, other family); **O** = me (Opus/Sonnet, graph + final review only). Local cursor/droid lanes were exhausted earlier; fall back to G for implementation if S is down.

### Wave 0: bugs (independent, start now)
| ID | Type | Output | Lane | Review |
|---|---|---|---|---|
| W0-1 | Impl | RC1: scope `.A .row` (parts/01-css.html:101) to the rail only (`.A .rail .row`); audit every `.row`/button strip (Add a change, Change the window, Add a source, + Add item, Download report); no new classes elsewhere | S | W0-3 |
| W0-2 | Impl | RC2: `SERIES` in parts/10-data.js builds reference points m=0..59, then incident points; confirm `minuteLine` draws two continuous paths; check every `minuteLine` caller (steps 2, 4, tools) | S | W0-3 |
| W0-3 | Review | Rebuild; check the 6 squashed buttons and the 4 blank charts (steps 2, 4; tool Response time) at 3 widths, light and dark; list anything else clipped | K | O skims |

### Wave 1: analysis and spec (parallel, no code)
| ID | Type | Output | Lane |
|---|---|---|---|
| F1 | Analysis | For each R1 evidence row: does the current collector/probe code (internal/, `sql/*.js` probes, `tools.ts`) already capture it? Table: row, source, captured yes/partial/no, what is missing. Output into `specs/2026-10-29-evidence-feasibility.md` | G (repo upload) |
| A1 | Analysis | Manual tools review (issue 15, theme H): for each of the 13 tools, what is missing, what is useless, what to add (from R1 tool lists); from `tools.ts` + parts 41-43 | G |
| D1 | Spec | Evidence data shape + colour rule + legend text (one JS object schema: `{id,label,verdict,rule,chart,series,table,why,sampled}`), 2 to 3 scenarios per colour for SQL, Mongo, Host. Output as a spec section and a JSON fixture draft | G, O reviews |
| D2 | Spec | Frame step behaviour: optional release (+ "no release notice" scenario), estimate line, overlap warning copy, pagination, recurrence selector options; all copy written for a reader | D |
| D3 | Copy | Plain-words copy: step 1 purpose line and the "long gap" sentence; step 3 Time split "average across selected APIs (default all)" | D, O reviews |

### Wave 2: shared building blocks (after W0 merged; D1 for E1/E2)
| ID | Output | Part file | Lane | Depends on |
|---|---|---|---|---|
| C1 | Helpers `tlChart(series, markers)` and `stackedArea(series, pct)` | `11-charts-a.js` | S | W0-2 |
| C2 | Helper `heatmap(cells, cols, rows)` (resource heatmap and day×hour) | `11-charts-b.js` | S | W0-2 |
| C3 | Helper `swimlane(lanes, window, brush)` | `11-charts-c.js` | S | W0-2 |
| E1 | Evidence panel component: verdict chip + rule + chart slot + table + "Why" link; legend component (what green/amber/red/gray mean) | `12-evidence.js` | S | D1, C1 |
| E2 | "Why" full view (chart + data table + elaboration + back) and the "ruled out" collapsed drawer | `13-why.js` | S | E1 |
| E3 | Scenario fixtures: SQL, Mongo, Host (2 to 3 per colour) wired into the state switcher | `14-scenarios.js` (+`.b` if >20 KB) | S | D1, E1 |
| R2 | Review C1-C3, E1-E3 together: API consistency, colour contrast (tokens, not new colours), keyboard access for chart/Why | K | all above |

### Wave 3: screens (each independent once Wave 2 is merged)
| ID | Screen change | Part | Depends on |
|---|---|---|---|
| S1 | Step 1 copy (D3); step 3 API picker (all / one / several) with table and bars following it | `30-steps-0-4.js` | D3 |
| S2 | Step 0 frame: optional release, estimate line, overlap warning, long-window rollup label, pagination note | `30-steps-0-4.js` | D2 |
| S3 | Step 0 recurrence mode: selector (last Thursday monthly), calendar heatmap, overlay of N occurrences | `30-steps-0-4.js` | D2, C2, C1 |
| S4 | Step 4 load chart + Nearby activity as a swim-lane (API, SQL, Mongo, Host lanes, brush 2/5/15 min, table below) | `30-` / `31-steps` | C3 |
| S5 | Step 7 SQL evidence | `31-steps-5-9.js` | E1-E3 |
| S6 | Step 8 Mongo evidence | `31-steps-5-9.js` | E1-E3 |
| S7 | Step 9 Host evidence | `31-steps-5-9.js` | E1-E3 |
| S8 | Step 5 suspect cards (evidence per suspect, ruled-out drawer) | `31-steps-5-9.js` | E1, E2 |
| S9 | Step 10 Origin suspects: charts, data, evidence, Why | `32-steps-10-13.js` | E1, E2 |
| S10 | Manual tools updates, group A (from A1, E1) | `41-tools-a.js` | A1, E3 |
| S11 | Manual tools updates, group B | `42-tools-b.js` | A1, E3 |
| S12 | Manual tools updates, group C | `43-tools-c.js` | A1, E3 |

Rule: S5 to S7 and S8 to S9 and S1 to S4 touch the same files, so run packets that share a file one at a time (S1, S2, S3, S4 in order; S5, S6, S7, S8 in order). Different-file chains run in parallel.

### Wave 4: spec, QA, hand-off
| ID | Type | Output | Lane |
|---|---|---|---|
| P-SPEC | Doc | Update `2026-10-29-guided-layout-spec.md`: new sections (evidence, Why view, recurrence, frame), new scenario IDs GL-23.., MT-14.. (new tool states), GS-.. per colour; header status line | G, O reviews |
| QA-1 | Review | Full visual pass, every step and tool, 3 widths, light and dark, with a click-through script; defect list ranked | K (not the implementer family) |
| FIX | Impl | One fixer pass per round from QA-1, then re-review only what failed (cap 2 rounds) | S |
| REL | Owner | Open wireframe, pick variant A/B/C, approve; I then commit wireframe + spec | owner |

## 3. Dependency graph

```
W0-1 ─┐                                   D3 ─> S1
W0-2 ─┼> W0-3 ─> C1 ─┐                    D2 ─> S2 ─> S3 (also C1,C2)
      │         C2 ──┤                    C3 ─> S4
      │         C3 ──┤
F1 ───┼─────────────┐│
D1 ───┴> E1 ─> E2    ││ E1,E2,E3 ─> S5 ─> S6 ─> S7 ─> S8 ─> S9
         └──> E3 ────┘│
A1 ──────────────────────> S10, S11, S12 (also E3)
R2 gates Wave 3; S* all ─> P-SPEC ─> QA-1 ─> FIX ─> REL (owner)
```

F1 feeds D1 (infeasible rows become "not collected / gray" fixtures) and is the only packet that can change scope.

## 4. Order of execution and parallelism

1. Now: W0-1, W0-2 (parallel, different files) then W0-3. Same time: F1, A1, D2, D3 on cloud lanes.
2. D1 after F1; then C1 to C3 in parallel (distinct files), E1 > E2, E3.
3. R2 review, then Wave 3 chains in parallel by file: `30-` chain (S1 > S2 > S3 > S4), `31-` chain (S5 > S6 > S7 > S8), `32-` (S9), tools (S10, S11, S12 parallel, distinct files).
4. P-SPEC in parallel with Wave 3 tail; QA-1 last.
Total: about 30 hub jobs, no Opus-heavy step; my own work is graph, D1/D3 review, R2 and QA-1 triage (about 5 short reviews).

## 5. Muse
Nothing needed now (wireframe is local, hub-only). After the owner approves, the real Svelte implementation in `web/` (layout fix, evidence panel, Why route, recurrence, frame changes) can go to Muse as GitHub issues/PRs; I will write those prompts then, one per screen group, with the spec as the acceptance source.

## 6. Owner decisions that would change the plan
- Variant A, B or C of the layout (still open).
- Whether evidence the collectors do not capture today (F1 result) should be shown as aspirational gray "not collected yet" states, or hidden. Default: shown gray, labelled.
- Whether the recurrence mode ships in the first real release or stays wireframe-only. Default: wireframe now, build later.

## 7. Done criteria for the wireframe
All 15 issues re-checked against the built file (each issue number ticked in QA-1 output); no squashed button or blank chart at 3 widths in light or dark; every suspect on steps 5, 7, 8, 9, 10 has chart, table, verdict, rule and a working Why view; each source menu offers 2 to 3 scenarios per colour; step 0 shows the optional release, estimate and overlap behaviour; every manual tool has a reviewed gap list applied; spec updated with matching scenario IDs.

## 8. Packet format (blind-implementable) and batching

### 8.1 Rule
An implementer never explores. Every decision (names, copy, data, colours, placement) is made in a prior packet or by me; the implementer transcribes and verifies. If a packet needs a decision that is not in its file, it is not ready and does not go out.

### 8.2 Packet file (one markdown file per packet, under `buildout/packets/`)
1. **ID and goal**: one sentence.
2. **Edit file(s)**: exact path(s); at most one part file per packet.
3. **Context pack**: only the files the agent needs, with line ranges (e.g. `parts/01-css.html:95-110`); never "the repo".
4. **Anchors and change**: for each edit, the exact existing lines to find (copied verbatim) and the exact new lines, or for new code the exact function signatures, data literals and copy strings.
5. **Do not touch**: file and symbol list.
6. **Checks (run, with expected result)**: `node build-guided.js` clean; `node --check`; the deterministic check script (8.4) prints PASS for named assertions.
7. **Output**: a unified diff only; no explanation beyond a 3-line report (status, checks run, concerns).
8. **Size cap**: diff under ~12 KB.

### 8.3 Who fills packets
- Wave 0 packets are fully specified now (I know the exact lines).
- Wave 3 packets are written after D1/D2/D3/F1 land, because they carry D1's JSON fixtures and D2/D3's final copy verbatim. A cheap lane (G) drafts each packet from the spec; I review the packet, not the code.

### 8.4 Token savers
1. **Batch same-shape edits into one job**: W0-1 + W0-2 together; S5 + S6 + S7 (SQL, Mongo, Host steps all call the same `evidencePanel(fixture)`); S10 + S11 + S12 (tool groups, one pattern). Limit: one job's total diff under ~12 KB and files not shared with another running job.
2. **Small context pack, uploaded once**: one trimmed upload (`wfrepo`: parts, build script, spec sections needed) per wave, reused by all jobs in that wave; a shared preface file is identical across jobs so provider prompt caching hits.
3. **Deterministic checks replace model review**: new tiny packet **T0** writes `buildout/wireframes/check-guided.js` (node, no deps): builds, parses the script, loads the page in a DOM stub or reads the built HTML, and asserts: no `.row` button strip narrower than 60 px worth of grid column, each `SERIES` yields 2 continuous paths, every step renders without throwing, every suspect has a verdict/rule/Why. Reviewers only see failures.
4. **One review per wave, not per packet**: a different-family reviewer reads all diffs of the wave together, against the checklist; only failed items get a scoped re-review on a cheap model; max 2 fix rounds.
5. **Cheap lanes for mechanical work, strong lanes only for design copy**; local hub first, cloud as overflow (exhausted lanes listed in the plan).
6. **No re-reads by me**: I review the packet files and the check output, not full parts files.

### 8.5 Worked example
`buildout/packets/W0-batch-bugfix.md` is the format applied to W0-1 and W0-2 batched into one job. All other packets follow it.
