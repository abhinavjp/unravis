# Later packets L-20 .. L-25 (BL-020 .. BL-025)

NOT IN ANY WAVE of this plan. Each one is a separate, later piece of work and starts only when the owner says so. They are written short on purpose: the first step of each is to read the named sources and turn the packet into exact anchors (a planner pass on Sonnet, about 15 minutes each). Backlog texts are quoted from `docs/BACKLOG.md`.

---

## L-20 (BL-020) "Correct the source cap from 50 to 8."

model: sonnet, effort: low. risk: low. Review batch: later-1 (Sonnet).

### Outcome
The place that says or enforces 50 sources uses 8, in code, copy and tests.
### Write scope
UNVERIFIED: find it first: `grep -rn "\b50\b" internal/server/tracing*.go internal/casestore web/src/lib/tracing docs/` and the backlog item's own pointer. Expect one Go constant, one web constant or copy line, their tests.
### Must not change
Any other limit. Stored cases that already hold more than 8 sources must still open (decide: show the first 8 and say so; ask the owner if unclear).
### Changes
1. Locate the cap and every text that states it. 2. Change to 8. 3. Add a test for a case with 9 sources.
### Proof
- [ ] Go and web tests for the touched packages pass. - [ ] `grep` shows no "50 sources" text left. - [ ] Checked on a live case with more than 8 sources, or stated as not checked.
### Depends on
Nothing in this plan. Touches `internal/server`: run after P-39.

---

## L-21 (BL-021) "Inline fixes still owed from earlier rounds."

model: sonnet, effort: medium. risk: low. Review batch: later-1.

### Outcome
Every fix still listed as owed in the earlier drill rounds is done or struck out with a reason.
### Write scope
UNVERIFIED: the list lives in the earlier round files under `.forge/investigation-timeline/drill/` (findings and delegation notes). First step: build the list (file:line, one line each) and get it confirmed by the orchestrator. One packet per file touched if it is more than 3 files.
### Must not change
Anything not on the confirmed list.
### Changes
1. Collect. 2. Split into packets of 1-3 files. 3. Fix with a test each.
### Proof
- [ ] Each fix has a test or a browser check. - [ ] Full web and Go gates pass.
### Depends on
P-48 (so fixes do not collide with this plan's hot files).

---

## L-22 (BL-022) "'Other work' semantics in the breakdown."

model: sonnet, effort: medium. risk: medium. Review batch: later-1.

### Outcome
"Other work" in "Where the time went" has one written meaning (total time minus the recorded parts, never negative, "not recorded" when the parts are missing) and the screen says it in plain words.
### Write scope
`web/src/lib/drill/overview.ts` (the breakdown maths), `web/src/lib/drill/drillCopy.ts` (`partOther`, `otherUnknown` help text), their tests.
### Must not change
The other parts' numbers. Banned copy words.
### Changes
1. Write the rule as a table of cases (parts sum > total, parts missing, total missing). 2. Owner confirms the table. 3. Implement and test each row.
### Proof
- [ ] `node --test tests/drill.overview.test.cjs tests/drill.copy.test.cjs` pass. - [ ] Checked on one live record whose parts sum to more than the total, or stated as not found.
### Depends on
P-34 (real part numbers make the cases visible).

---

## L-23 (BL-023) "D3: Breakdown, Reveal, SEC-D3 security review on Opus, A8."

model: sonnet for build, opus for SEC-D3. effort: high. risk: high. Review batch: later-2 (Opus security).

### Outcome
The Breakdown and Reveal parts of the drill work inside the Slow call screen (as a third and fourth tab or inspector sections), with the security review done before any text of a record is shown.
### Write scope
To be planned from the earlier D3 plan under `.forge/investigation-timeline/drill/`; expect `SlowCallScreen.svelte` (tabs only), new `CallBreakdown.svelte`, `CallReveal.svelte`, the reveal endpoint in `internal/server`, tests. Reveal shows record text: never logged, never in fixtures.
### Must not change
The rule that list rows carry no text. `_perfSlowSql` / `_perfSlowHttp` text stays unprojected until SEC-D3 says how.
### Changes
1. Re-plan D3 against the new screen (planner pass, Opus for the design only). 2. Build in small packets. 3. SEC-D3 on Opus. 4. A8 acceptance.
### Proof
- [ ] SEC-D3 verdict SHIP. - [ ] Live check of Reveal on one record. - [ ] e2e for both tabs.
### Depends on
P-48. Decides whether P-49 may delete the pop-up.

---

## L-24 (BL-024) "SQL packets S1 to S5."

model: sonnet, effort: high. risk: high. Review batch: later-2 (Opus security: SQL access).

### Outcome
The five SQL packets of the earlier plan, re-checked against what P-16, P-20, P-21 and P-39 already built.
### Write scope
To be planned: read the S1..S5 texts in the earlier plan, mark each as done by this plan / still open / dropped, and write packets only for the open ones.
### Must not change
Read-only SQL. Never switch Query Store on. One Go writer per package.
### Changes
1. Mapping table S-n → state. 2. Packets for the open ones. 3. Opus security review.
### Proof
- [ ] Mapping table reviewed by the owner. - [ ] Gates and security verdict.
### Depends on
P-43, P-48.

---

## L-25 (BL-025) "e2e TSQL-03 (not run) and an add-computer e2e."

model: sonnet, effort: medium. risk: low. Review batch: later-1.

### Outcome
TSQL-03 (`tools/e2e/specs/timeline-sources.spec.ts:432`) runs and passes, and one new scenario covers adding a computer.
### Write scope
`tools/e2e/specs/timeline-sources.spec.ts` (TSQL-03 only), one new spec file for add-computer, `tools/e2e/COVERAGE.md`.
### Must not change
Other scenarios. Product code (report product bugs).
### Changes
1. Find why TSQL-03 is "not run" (skipped, needs a fixture, or fails): run `npm run e2e -- -Only TSQL-03` and report. 2. Make it run. 3. Write the add-computer scenario from the UI flow in the app (find the screen: `grep -rn "Add a computer\|add-computer" web/src`).
### Proof
- [ ] `npm run e2e -- -Only TSQL-03` → 1 passed, twice. - [ ] The new id passes twice and is in `COVERAGE.md`.
### Depends on
Nothing in this plan; can run any time after P-42 (same demo exe).
