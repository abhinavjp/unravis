# Packet D1-Mongo: Mongo evidence card scenarios (spec + fixture only)

Goal: write 2 to 3 Mongo scenarios per colour using the evidence card schema, plus the legend wording where Mongo needs its own. Output is a Markdown spec plus a JSON fixture in your reply. No code edits.
Depends on: nothing (D1-SQL defines the same schema in parallel; use the schema below verbatim so the two match).

## 1. Context (facts; read these files only)
- `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`, section "MongoDB": what a Mongo slowdown investigation should show.
- `buildout/specs/2026-10-29-owner-issues-triage.md`: owner issues 8, 10, 11, 12, 15 (evidence, "Why", colours).
- `buildout/specs/2026-10-29-wireframe-v2-implementation-plan.md` sections 2 and 10.
- Schema (fixed): one object `{id, label, verdict, rule, chart, series, table, why, sampled}`. `verdict` is `ok` (green) | `watch` (amber) | `bad` (red) | `none` (gray, "not collected yet"). `chart` is one of `line`, `stackedArea`, `bars`, `none`. `series` is an array of `{name, win, pts:[{t,v}]}` (`win` = `normal` or `incident`, `t` minute 0 to 119, at most 12 points per series). `table` is `{cols:[...], rows:[[...]]}` with at most 5 rows. `why` is `{summary, steps[], dataNote}`. `sampled` is a boolean (true when the data is a sample, not full capture).
- Colour rule (fixed): `bad` = incident at least 30 % and at least 100 ms worse than reference; `watch` = at least 15 % and at least 50 ms worse; `ok` = neither; `none` = not collected or no data. For non-latency rows (counts, ratios) use the same percentage test and say which unit replaces the 100 ms.
- Demo case: incident 26 Oct 09:00 to 10:00 UTC, reference 08:00 to 09:00, API Orders.Create. Use plausible numbers.
- Style: plain words for engineers who are not Mongo experts.

## 2. Task
1. **Mongo evidence rows**: a table of 8 to 10 evidence rows from the research "MongoDB" section (for example slow ops by namespace, collection scans, index usage, lock or ticket waits, cache pressure, oplog or replication lag, connection count, document size growth). Columns: `id | label | verdict test in plain words | needs which permission`.
2. **Scenarios**: 2 to 3 per colour (8 to 12 in total), each a complete fixture object in the schema. Gray scenarios use rows the research says need a permission or profiler setting that is usually off.
3. **Legend wording**: four short lines, one per colour, only where they differ from a generic reading (otherwise write "same as generic").
4. **Fixture JSON**: all scenarios as one valid JSON array in one fenced `json` block.

## 3. Do not
Invent colours, add schema fields, edit repo files, or write SQL or Host fixtures.

## 4. Checks (run if you can; otherwise say so)
Save the JSON block to a file and run `node -e "const a=JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'));const k='id,label,verdict,rule,chart,series,table,why,sampled'.split(',');console.log(a.every(o=>Object.keys(o).join()===k.join())?'PASS':'FAIL')" <file>`.

## 5. Output
The spec Markdown (sections 1 to 4), then 3 status lines: `Status: PASS|FAIL`, `Checks: <output>`, `Concerns: <none or one line>`. Reply is capped near 16,000 output tokens: be terse.
