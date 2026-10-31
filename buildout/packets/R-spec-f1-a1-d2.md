# Review packet R-spec: fact-check F1, A1 and D2 against the repo

You are an independent reviewer. Do not edit files. The three specs were written by another model; check that they are true and complete, with evidence.

## Read
- `buildout/specs/2026-10-29-evidence-feasibility.md` (F1), `buildout/specs/2026-10-29-manual-tools-audit.md` (A1), `buildout/specs/2026-10-29-frame-step-spec.md` (D2).
- Sources they rest on: `buildout/specs/2026-10-29-research-sql-mongo-host-evidence.md`, `buildout/specs/2026-10-29-research-long-windows.md`, `buildout/specs/2026-10-29-owner-issues-triage.md`, `web/src/lib/guided/tools.ts`, `internal/{collect,dbhealth,resource,sampler,hosts,evidence,discover,sqlconn}` (Go, no tests), `buildout/wireframes/parts/4*.js`, `parts/30-steps-0-4.js`.

## Tasks
1. F1: pick the 12 rows with verdict "yes" (captured) across SQL, Mongo and Host, with at least 3 per source. For each, find the function, query or struct in `internal/` or `sql/` that proves it is captured, and give file and line. Mark `confirmed` or `wrong`. Then pick 6 rows marked "no" and confirm that nothing in the repo captures them (grep evidence). Report each wrong row with the correct verdict.
2. F1: list evidence rows present in the research file's SQL, Mongo and Host sections that F1 omits.
3. A1: confirm Table 1 has exactly the 13 tools in `tools.ts` (ids match) and that "what it shows today" matches the wireframe tool code in `parts/41-43`. List mismatches. Confirm every proposed new tool is supported by the research file or the owner issue (quote the line).
4. D2: list every numeric limit not marked `proposed` and check it against the long-windows research file (quote the line); list any that contradicts it. Check that the DST copy and rollup thresholds agree with each other across sections 2, 4 and 6.

## Output
Per task a table or bullet list with evidence (file:line or quote). End with `Verdict: ACCEPT | FIX` and the list of corrections (spec file, row or section, the corrected text). Be terse; the reply is capped near 16,000 output tokens.
