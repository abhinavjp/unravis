# Packet D1-Host: host resource evidence card scenarios (spec + fixture only)

Goal: write 2 to 3 host-resource scenarios per colour using the evidence card schema. Output is a Markdown spec plus a JSON fixture in your reply. No code edits.
Depends on: nothing (D1-SQL defines the same schema in parallel; use the schema below verbatim so the two match).

## 1. Context (facts; read these files only)
- `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`, section "Host resources": what a slowdown investigation should show for CPU, memory, disk, network and processes.
- `buildout/specs/2026-10-29-owner-issues-triage.md`: owner issues 8, 10, 11, 12, 15.
- `buildout/specs/2026-10-29-wireframe-v2-implementation-plan.md` sections 2 and 10.
- `internal/resource`, `internal/sampler`, `internal/hosts` (Go): what the collector already samples (read file names and exported types only, do not review the code). Rows the collector does not sample today are gray scenarios.
- Schema (fixed): one object `{id, label, verdict, rule, chart, series, table, why, sampled}`. `verdict` is `ok` (green) | `watch` (amber) | `bad` (red) | `none` (gray, "not collected yet"). `chart` is one of `line`, `stackedArea`, `bars`, `none`. `series` is an array of `{name, win, pts:[{t,v}]}` (`win` = `normal` or `incident`, `t` minute 0 to 119, at most 12 points per series). `table` is `{cols:[...], rows:[[...]]}` with at most 5 rows. `why` is `{summary, steps[], dataNote}`. `sampled` is true when the data is a sample (host samples are usually every 15 to 60 seconds, so set it according to the research).
- Colour rule for host rows: `bad` = incident value at least 30 % and at least 15 percentage points (or the unit's equivalent) worse than reference, or a saturation level the research names (for example CPU above 90 % for 5 minutes); `watch` = at least half of that; `ok` = neither; `none` = not collected. State the unit per row.
- Demo case: incident 26 Oct 09:00 to 10:00 UTC, reference 08:00 to 09:00, API Orders.Create. Use plausible numbers.
- Style: plain words for engineers who are not sysadmins.

## 2. Task
1. **Host evidence rows**: a table of 8 to 10 rows (for example CPU by process, run queue, memory and swap, page faults, disk latency and queue, network retransmits, TCP connection counts, GC pauses, thread pool starvation). Columns: `id | label | verdict test in plain words | collected today? yes/partial/no | needs which permission`.
2. **Scenarios**: 2 to 3 per colour (8 to 12 in total), each a complete fixture object in the schema.
3. **Legend wording**: four short lines, one per colour, only where they differ from a generic reading (otherwise write "same as generic").
4. **Fixture JSON**: all scenarios as one valid JSON array in one fenced `json` block.

## 3. Do not
Invent colours, add schema fields, edit repo files, or write SQL or Mongo fixtures.

## 4. Checks (run if you can; otherwise say so)
Save the JSON block to a file and run `node -e "const a=JSON.parse(require('fs').readFileSync(process.argv[1],'utf8'));const k='id,label,verdict,rule,chart,series,table,why,sampled'.split(',');console.log(a.every(o=>Object.keys(o).join()===k.join())?'PASS':'FAIL')" <file>`.

## 5. Output
The spec Markdown (sections 1 to 4), then 3 status lines: `Status: PASS|FAIL`, `Checks: <output>`, `Concerns: <none or one line>`. Reply is capped near 16,000 output tokens: be terse.
