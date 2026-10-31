# Packet A1: audit of the 13 manual tools (owner issue 15, theme H)

Goal: for each manual tool say what is missing, what is useless and what to add. Analysis only; output a Markdown document in your reply. No code edits.
Depends on: nothing. Read-only.

## 1. Context (read these files only)
- `web/src/lib/guided/tools.ts`: the list of manual tools (13 entries, ids and labels).
- `buildout/wireframes/parts/40-manual-home.js`, `41-tools-a.js`, `42-tools-b.js`, `43-tools-c.js`: what the wireframe currently draws for each tool (global `TOOLS` has the ids).
- `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`: sections "SQL Server", "MongoDB", "Host resources" and "Tools to add to a manual-tools menu".
- `buildout/specs/2026-10-29-owner-issues-triage.md`: issue 15 and theme H (owner complaints about the manual tools).
- Readers are engineers investigating an API slowdown; the data is read-only.

## 2. Task
Table 1, one row per existing tool (13 rows): `| id | label | what it shows today | missing | useless / remove | add (from the research lists) | priority H/M/L |`. Keep cells to one short phrase.
Table 2, tools to add that no current tool covers (from "Tools to add"): `| proposed id | label | source (SQL/Mongo/Host/API) | evidence rows it shows | feasible read-only? yes/partial/unknown |`.
Then a section "Grouping": which tools belong in group A (`41-tools-a.js`), B (`42-`), C (`43-`) as the files exist today, and any tool that should move.
Then "Top 5 changes" for the owner, in plain words.

## 3. Do not
Edit files, invent tools not supported by the research file or the owner issue, or propose more than 6 new tools.

## 4. Output (exactly this structure)
`# Manual tools audit (A1)`, Table 1, Table 2, `## Grouping`, `## Top 5 changes`, then 3 status lines: `Status: PASS|FAIL`, `Checks: n/a`, `Concerns: <none or one line>`. Reply is capped near 16,000 output tokens: be terse.
