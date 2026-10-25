# TSK-18: Sources gallery and scan integration
risk: high — the step where "trust the scan" meets the user; many source kinds.

## Outcome
Step 2 shows a gallery of what this PC offers (verified findings only as ticked-able; unverified ones shown greyed with their plain reason) plus manual connections for another computer: Mongo by connection string with a test button and per-database classification (audit / other / not used), SQL Server and Redis by address with a reachability check and file choice for their exports, IIS and app logs by file or folder, Hangfire by CSV, and "a file". Quartz is not shown (R12). Each ticked source has an environment/portal/API row with suggestions from TSK-02 offered as chips (R7). A finding that needs a folder asks for it and calls locate (R8). A found service must end in a reachable connection or an explicit "could not reach it, here is why" (R11).

## Write scope
- `web/src/components/wizard/` (new: `SourceGallery.svelte`, `SourceTagRow.svelte`, `LocateFolder.svelte`, `MongoClassify.svelte`, `ReachableCheck.svelte`)
- `web/src/lib/wizardSources.ts` (new pure selection/tag model), `web/tests/wizardSources.test.cjs` (new)
- `web/src/lib/copy/` (gallery wording)

## Must not change
`DiscoverPanel`, `HostsEditor`, `CaseTools` (still used by "Add more data" in the running analysis); existing discover scan polling code is reused, not forked; no credential is stored in the draft beyond what the existing connect flow already holds in memory.

## Changes
1. Selection model: a source has `{kind, ref, label, env, portal, api, classification}`; `canTick(finding)` is `isSelectable` (TSK-05); blank env/portal/api are allowed and mean "not named".
2. Suggestion chips: shown as buttons that fill the field on click; never pre-filled; environment field offers the names from step 1.
3. Mongo block: test connection → list databases → per-database choice; choosing "audit" adds it as an analysis source via the existing connect flow; password never copied into the model.
4. Reachability block calls `probeReachable`; result text uses the fixed reason set from TSK-04.
5. "Another computer" help text says: connect by address, or choose the files you copied from it (Q13).

## Proof
Tests (node:test on the pure model): an unverified finding cannot be ticked; blank tags allowed; duplicate source refs collapse; env must be one of step 1's names or free text when step 1 was blank; classification required before a Mongo database can be added; unreachable result keeps the source ticked but flagged. Browser pass: demo mode shows ready and greyed findings; locate flow works on a log4net fixture. `cd web; npm test; npm run check; npm run build` green.

## Depends on
TSK-05, TSK-08, TSK-17
