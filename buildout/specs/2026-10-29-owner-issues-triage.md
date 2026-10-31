# Owner issues on the guided wireframe: triage (2026-10-29)

Source: `Unravis Issues.docx` (15 issues, 15 screenshots). Wireframe: `buildout/wireframes/guided-analysis-layout.html`.

## Root causes found (verified in the built file)

| ID | Cause | Issues it explains |
|---|---|---|
| RC1 | Rail variant CSS `.A .row { display:grid; grid-template-columns:22px 1fr auto }` (parts/01-css.html:101) also matches every `.row` button strip in the pane, so the first button lands in a 22 px column and wraps ("Add a source", "+ Add item", "Download report", "+ Add a change", "Change the window"). | 1, 3, 9, 13, 14 |
| RC2 | `SERIES` (parts/10-data.js:175) pushes reference and incident points interleaved, so `minuteLine` starts a new run on every point: 106 one-point paths, nothing visible. The chart never drew. | 6, 15 (and step 2 P95, step 4 in-flight, tools) |
| RC3 | Several step bodies are text only by design (the repo has no chart for SQL, Mongo, Host, Origin, Changes, Time split per API) and the wireframe copied that. The owner wants evidence visuals. | 4, 5, 8, 10, 11, 12 |
| RC4 | Copy is derived from engine strings, not written for a reader. | 2.1, 2.2 |
| RC5 | Frame step has no "optional release", no long-window or overlap behaviour. | 7 |

## Work themes

- A. Layout bugs (RC1): scope `.A .row` to the rail, audit every strip (button rows, forms) at 3 widths. Pure CSS, tiny.
- B. Chart bug (RC2): interleaving fix, then verify all MinuteLine uses. Tiny.
- C. Copy: step 1 purpose line ("What is this screen for": checks the data can be trusted before any conclusion: coverage, gaps, volume) and the gap sentence ("Long gap: these minutes are Cannot test yet" becomes plain words: which minutes, why it matters, what is excluded).
- D. Step 3 Time split: say plainly it is the average across selected APIs (default all), add an API picker (all / one / several) with the table and bars following it.
- E. Evidence model (the big one). Every suspect on steps 5, 7, 8, 9, 10 gets: evidence chart(s) in the window, a data table, a verdict colour with a one-line rule, and a "Why" link that opens a full-size incident timeline view (chart + data + elaboration). Non-suspects show why they were set aside, using the same view. Each source menu (SQL, Mongo, Host) gets 2 to 3 sample scenarios per colour (red, amber, green, gray) in the state switcher, plus a legend that says what each colour means wherever reused.
- F. Nearby activity: timeline (swim-lane) chart with lanes API calls, SQL, Mongo, Host, a time axis, brush for 2/5/15 minutes, click a bar to see the row. List stays as the table below.
- G. Frame step: release date optional with a "no release notice" scenario; long windows (incident up to 3 months, reference 3 months plus) show a consequence line (estimated size and time), never block, paginate results, warn on overlap; recurring-slowdown mode (e.g. last Thursday each month: pick N occurrences, overlay them).
- H. Manual tools review: for every one of the 13 tools list what is missing, what is useless, what to add (SQL: locks, deadlocks, waits, top queries, tempdb, Query Store; Mongo and Host equivalents), and give each the same evidence pattern as E.

## Research needed (delegated)

- R1 (Manus, cloud 27959): evidence per SQL, Mongo and Host menu, best chart, colour rule, tools to add.
- R2 (Manus, cloud 27960): long windows and recurring-slowdown UX, "Why" drill-down pattern, non-suspect display.

## Work packets (draft; dependency graph in the implementation plan after research)

| Packet | Type | Lane | Output |
|---|---|---|---|
| P1 | Impl | hub droid-az-grok46 or droid-sonnet-5 | Fix RC1 and RC2, audit all strips, rebuild |
| P2 | Impl | hub | Copy fixes C and step 3 API picker D |
| P3 | Analysis | hub reviewer | Manual tools review H: per tool gap/useless/add list from `tools.ts` and the 41-43 parts |
| P4 | Design | me (Opus review) | Evidence component spec E: data shape, "Why" view, colour rules from R1 |
| P5 | Impl | hub | Shared chart helpers (timeline, swim-lane, stacked area, heatmap) in parts/10-data.js |
| P6 | Impl | hub | Steps 7, 8, 9 evidence and scenarios; Why view |
| P7 | Impl | hub | Step 5, 10 and suspect cards evidence |
| P8 | Impl | hub | Nearby activity timeline F |
| P9 | Impl | hub | Frame step G |
| P10 | Impl | hub | Manual tools updates from P3, P4 |
| P11 | Review | hub, other model family | Visual QA of every step and tool at 3 widths, light and dark |
| P12 | Doc | hub | Spec sections updated and new scenarios |
