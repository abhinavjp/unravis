# TSK-19 Review and create/import step of the create wizard

Status: done. In `web/`: `npm test` 1273/1273, `npm run check` 0 errors 0 warnings, `npm run build` ok. At the root: `go vet ./...` clean, `go test ./... -count=1` all ok (no Go edited; no route was missing). Nothing committed.

## What it does
- Step 3 (`ReviewStep.svelte`): summary line "N environments, M portals, K APIs, S shared sources" (blank shown as "not named"), case title, environments, every tagged source (`Env > Portal > API`) with what will happen to it, a duplicate-title note (allowed), the empty-case note, a file picker per file source, a read-back window for this computer's logs, the stale-scan blocker, and the gating banner (`GATING_BANNER`) with the `ACK_LABEL` tick box above the button.
- "Create case" is in the frame's footer (`finish` snippet in `Landing.svelte`); disabled while a blocker stands (stale scan, notice not ticked, a file source with no file) or a run is in flight.
- `wizardSubmit.ts` (pure orchestration, injected `SubmitApi`): create case, PUT environments, post each tag (Mongo: test the held connection once, then the audit source), then import scan findings (chunks of 64, `confirmHost` first) and files in parallel chains. Every step records its own status; a failed step never stops the next; the case is never deleted. Double click = one run (in-flight guard on the state plus a synchronous `running` flag in the store).
- 423 mid-run: the run stops with `locked`, keeps what is done; after the unlock the create screen comes back (`unlock -> create` is now a legal move, `afterUnlock('create')` returns `create`), the progress screen shows "Continue", and the same state resumes without making the case again or repeating a written tag.
- `CreateProgress.svelte`: steps and per-source rows with percent, `role=status` live line, `role=alert` list of failures (each names the source and says to use Add more data), focus moves to the result. Opens the analysis by itself when everything went through (`shouldOpenAnalysis`); otherwise "Open the case" / "Open the case anyway". Opening clears the draft then calls `openCase(id)` (which also opens the import drawer = Add more data).
- Back, step buttons and Leave are frozen once a run starts or a case exists (`busy`). Leaving before pressing Create makes zero server calls (tested).
- `WIZARD_READY` is now `true`; the start screen's Create card reaches the wizard.
- Help: `wizard-review` entry in `helpCopy.ts`, one `HelpNote` in `ReviewStep`; `help.test.cjs` updated (SCREENS, component map, SCREEN_KEYS now also covers the three earlier wizard keys).

## Files
New: `web/src/components/wizard/{ReviewStep,CreateProgress}.svelte`, `web/src/lib/wizardSubmit.ts`, `wizardSubmitApi.ts` (real adapter over existing clients), `wizardCreate.svelte.ts` (store), `caseTagsApi.ts` (the two TSK-07 tag routes), `copy/wizardCreateCopy.ts`, `web/tests/wizardSubmit.test.cjs` (29 tests, fake api with a call log, synthetic fixtures only).
Edited: `web/src/components/Landing.svelte`, `lib/screenFlow.ts`, `lib/copy/helpCopy.ts`, `tests/help.test.cjs`, `tests/uxnav.test.cjs`, `tests/wizard.test.cjs`, `tests/wizardSources.test.cjs` (Landing regexes), `tests/p04.clumsy.test.cjs`. All CRLF.

## Deviations
- The review offers 1h / 6h / 24h / 7d for this computer's logs only. No incident or custom window: there is no case yet to take one from.
- "Shared source" is read as a source with no portal named.
- Packet file list also touched `lib/caseTagsApi.ts`, `wizardSubmitApi.ts`, `wizardCreate.svelte.ts`, `copy/wizardCreateCopy.ts` and `Landing.svelte` (not in the write scope list; needed to keep the api wrappers, store and copy modules separate).
- `p04.clumsy.test.cjs` asserted `unlock -> create` is refused. It is now allowed, only so a case being made resumes after the key is typed (only `finishUnlock` with a remembered origin goes there). Test updated with that reason.
- The progress percent is whatever `importProgressPercent` gives; a file source's preview step counts toward its row.
- Failed imports are not retried by a "Try again" button here; the text sends the person to Add more data, as specified.

## Unmeasured
- No real browser run: focus order, the focus move to the result, sticky mobile footer and the real 423 round trip through the unlock screen are covered by source-level and fake-api tests only.
- No run against a real scan with large numbers of findings or a real Mongo connection.
