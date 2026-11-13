# BBX-5-17 Blackbox SQL lane source label (web)
Backlog: BL-103 | Group: 5 | Model: performance | Reason: Svelte/TS lane-source label + copy + tests; frontend.
Depends on: BBX-5-16 | Blocks: none
Reviewer: Ultimate (batch with the lane server endpoint).

## Goal
Show the Blackbox SQL lane source with its own label in the slow-call DB lane: a new dbState key "blackbox" mapped to "Blackbox sampling every 15 s since <date>", understandable to a first-time investigator.

## Context
- Files: `web/src/lib/drill/dbState.ts` (DbStateKey line 7, pickKey 64-79, dbState() 81-109 maps key→DRILL_TEXT.dbSrc*), `web/src/lib/drill/drillCopy.ts` (dbSrc* strings 159-165; laneSource(label) 92), `web/src/components/SlowCallScreen.svelte` (dbState call 193-206; lane source list 240-244), `web/src/components/CallLanes.svelte:282` (renders laneSource(db.source)). Test `web/tests/drill.copy.test.cjs` (count guards) + a dbState test.
- The server (BBX-5-16) returns source key "blackbox" + a since timestamp.
- Copy count rule (survey): adding a plain DRILL_TEXT string is auto-counted; adding a FUNCTION key needs a SAMPLE entry + fns row in drillCopy.ts; DRILL_HELP keys are fixed at 26 (do NOT add help keys). The label needs a date → a function key `dbSrcBlackbox(date)`; add its SAMPLE + fns row.
- Rules: **first-time-investigator wording**; **no banned copy terms** (copylint.ts); **missing never 0** (no data → existing "Not recorded", not a zero lane).

## Do
1. `dbState.ts`: add 'blackbox' to DbStateKey; in pickKey, when the server source key is "blackbox" return it; in dbState() map 'blackbox' → source = DRILL_TEXT.dbSrcBlackbox(since), a calm message, short label, shape.
2. `drillCopy.ts`: add `dbSrcBlackbox: (date: string) => `Blackbox sampling every 15 s since ${date}`` and a `dbMsgBlackbox` plain string (e.g. "Sampled on the server every 15 seconds."). Add the function's SAMPLE entry + fns row so drillCopyEntries stays consistent.
3. `SlowCallScreen.svelte`: pass the server's since timestamp into dbState; the lane source list already uses db.source — no structural change needed beyond the input.
4. Tests: a dbState test asserting key 'blackbox' → the label with the date; copy lint + count guards pass.

## Do not
- Do NOT add a DRILL_HELP key (fixed at 26) or break the count test.
- Do NOT use banned wording; do NOT show a zero lane when absent (server falls through).
- Do NOT change other dbSrc* labels.
- Do NOT introduce a new UI library.

## Tests to write first
- `web/tests/drill.blackboxlane.test.cjs`: dbState({sourceKey:'blackbox', since}) → key 'blackbox', source contains "Blackbox sampling every 15 s since" + the date.
- copy test: dbSrcBlackbox appears in drillCopyEntries; lint clean; count guard holds.
- absent source → existing 'notread'/"Not recorded" unchanged.

## Verification checklist
- [ ] cd web && npm test passes (new tests included)
- [ ] cd web && npm run check passes
- [ ] go build ./... still passes
- [ ] Footprint: no polling added
- [ ] Missing reading is omitted, never 0 (absent → Not recorded)
- [ ] No text captured beyond the fixed label
- [ ] Copy lint + count guards pass; first-time-investigator wording
- [ ] Existing dbState/copy tests still pass

## Live-check needed?
YES — owner confirms the label renders on a real case with Blackbox SQL data (run the dev server and view the slow-call DB lane). Fake tests verify logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-17.md`.