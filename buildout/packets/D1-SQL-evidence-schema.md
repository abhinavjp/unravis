# Packet D1-SQL: evidence card data shape, colour rule and SQL scenarios (spec only)

Goal: define the one data shape every evidence card uses, the colour rule and legend text, and 2 to 3 SQL scenarios per colour. Output is a Markdown spec plus a JSON fixture, returned in your reply. No code edits.
Depends on: nothing (F1 may run in parallel; its result later turns some rows gray).

## 1. Context (facts; read these files only)
- `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`, section "SQL Server": what a SQL slowdown investigation should show.
- `buildout/specs/2026-10-29-owner-issues-triage.md`: owner issues 8, 10, 11, 12, 15 (read the lines about evidence, "Why", colours).
- `buildout/specs/2026-10-29-wireframe-v2-implementation-plan.md` sections 2 and 10 (names: E1 evidence panel, E2 "Why" full view, legend component, "ruled out" drawer).
- Wireframe style: ES5 JavaScript in `buildout/wireframes/parts/*.js` (`var`, `function`, no arrows/template literals). Existing tokens: verdict colours already exist in `parts/01-css.html` (look for the state chips `Ready`, `Partly ready`, `Cannot test yet`; reuse the CSS variable names there, define no new colours).
- Owner rule: every evidence card shows a verdict colour, the rule that produced it in plain words, a chart, a data table and a "Why" link that opens the full view. Uncollected evidence is shown gray, labelled "not collected yet".

## 2. Task
Write a spec with these sections:
1. **Schema**: one JS object per evidence card with exactly these fields and meanings: `{id, label, verdict, rule, chart, series, table, why, sampled}`. For each field: type, allowed values, one example. `verdict` is one of `ok` (green), `watch` (amber), `bad` (red), `none` (gray, not collected or no data). `chart` names a helper (`line`, `stackedArea`, `bars`, `none`). `sampled` is true when the data is a sample, not full capture. Add `why` fields: `{summary, steps[], dataNote}`.
2. **Colour rule**: for each verdict, the plain-words test that sets it (a threshold relation between incident and reference window, e.g. "incident is at least 30 % and at least 100 ms worse than reference" for bad, half of that for watch). Use numbers the existing wireframe already uses (30 % and 100 ms) unless the research file gives a better rule; say which.
3. **Legend text**: 4 short lines, one per colour, for readers who are not DBAs.
4. **SQL scenarios**: 2 to 3 per colour (so 8 to 12), each a complete fixture object in the schema above, for evidence rows from the research "SQL Server" section (for example: wait stats, top queries by duration, blocking chains, deadlocks, missing indexes, plan regression, tempdb, memory grants). Use plausible numbers for the demo case (incident 26 Oct 09:00 to 10:00 UTC, reference 08:00 to 09:00, API Orders.Create). Each `table` has at most 5 rows; each `series` at most 12 points. Gray scenarios use rows the research says need Query Store or extended events.
5. **Fixture JSON**: all scenarios as one valid JSON array in one fenced `json` block.

## 3. Do not
Invent colours, add schema fields, edit repo files, or write fixtures for Mongo or Host (a later packet does that).

## 4. Checks (run if you can; otherwise say so)
Paste the JSON block into a file and run `node -e "JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'))" <file>`; it must print nothing. Every scenario has exactly the 9 schema fields.

## 5. Output
The spec Markdown (sections 1 to 5 above, JSON fenced), then 3 status lines: `Status: PASS|FAIL`, `Checks: <output>`, `Concerns: <none or one line>`. Reply is capped near 16,000 output tokens: be terse.
