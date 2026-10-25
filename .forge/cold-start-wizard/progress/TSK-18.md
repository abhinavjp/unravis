# TSK-18 Sources gallery step of the create wizard

Status: done. `npm test` 1244/1244, `npm run check` 0 errors 0 warnings, `npm run build` ok (in `web/`). Nothing committed.

## What it does
- Part 1, "On this computer": scan this PC only; findings grouped as in the This computer panel. Only `verified === true` findings have an enabled checkbox. Unverified file findings are shown greyed with the plain reason (`unreadable` key) and a "Where is it?" control (`LocateFolder`) that posts the typed place to the locate route and, if verified, replaces the finding so it can be ticked. "Needs steps" findings show the steps and no checkbox. Live-series and Quartz findings are hidden in the wizard.
- Part 2, "Another computer": MongoDB (tested connection, then per database: API audit logs / other data / not used), SQL Server and Redis by host:port (reachability probe; added even if unreachable, then flagged), and "a file or export" (kind + short name; the file itself is picked when the case is made).
- Part 3, "Your sources (N)": each chosen source has Environment, Portal, API text fields (blank allowed, free text allowed). Chips are buttons: Environment chips are step 1's names; Portal/API chips are the scan's `suggest` plus names already used on other sources. Nothing is ever auto-filled.
- Help: `HelpNote id="wizard-sources"` once, in `SourceGallery.svelte`; entry in `helpCopy.ts`; `help.test.cjs` SCREENS lists it.
- Keyboard/a11y: labelled inputs, chips and toggles are real buttons with aria-expanded/controls, Enter in a field commits and does not advance the wizard (stopPropagation), Escape closes "Where is it?" and returns focus, status lines are `role=status`, focus goes to the ticked row / list heading after a change.

## Files
New: `web/src/lib/wizardSources.ts` (pure model), `wizardSources.svelte.ts` (store), `wizardConnections.ts` (in-memory Mongo holder), `copy/wizardSourcesCopy.ts`, `web/src/components/wizard/{SourceGallery,SourceTagRow,LocateFolder,ReachableCheck,MongoClassify}.svelte`, `web/tests/wizardSources.test.cjs` (49 tests).
Edited: `lib/wizard.svelte.ts` (`onWizardReset(hook)`; `resetWizard()` also calls `releaseConnections()` and the hooks), `lib/copy/helpCopy.ts`, `tests/help.test.cjs`, `components/Landing.svelte` (mounts `<SourceGallery />` in the `sources` snippet), `tests/wizard.test.cjs` (Landing regex).

## Exports TSK-19 consumes
- `wizard.sources: DraftSource[]` (`{kind, ref, label, env, portal, api, classification}`; no secrets). Kinds: `discover` (ref = finding id), `mongo-database` (ref `mongo:<connId>/<db>`, classification `audit` or `other`), `sql-server` / `redis` (ref `net:<kind>:<host>:<port>`), file kinds (`iis-logs`, `app-log`, `hangfire-csv`, `redis-slowlog-csv`, `file`; ref `file:<kind>:<n>`, no path yet).
- `sourcesView` (`wizardSources.svelte.ts`): `scanId`, `startedAt`, `findings`, `host`, `reach` (by source key), `connections`.
- `heldConnection(id)` / `releaseConnections()` (`wizardConnections.ts`) with `parseDatabaseRef(ref)` and `parseAddressRef(ref)` (`wizardSources.ts`) to open the audit databases and addresses; `auditDatabaseCount`, `flaggedCount`, `sourceKey`, `tagPath` for the review step.
- `resetWizard()` / `onWizardReset(hook)` (`wizard.svelte.ts`); `scanIsStale()` (scan ids expire after 1 hour).

## Deviations
- `WIZARD_READY` not flipped (TSK-19 owns it).
- `wizard.svelte.ts` gained `onWizardReset` so the store clears with the draft without a circular import.
- A finding is tickable only when verified (stricter than a plain importer check).
- Scan and Mongo connection state live in the store (`sourcesView`), not the draft, because the step component unmounts between steps.
- A database set to "not used" is not kept in the draft.
- Quartz and live-series findings are hidden. Mongo TLS mode is fixed to the URI's own setting.
- Removed unused copy keys `scanOldRows`, `notChosen`, `addrFlag`, `addrReachedLead`, `addrFailedLead`.

## Concerns
- No real-browser or screen-reader run was done (no dev server allowed); checks are unit tests, source-shape tests, svelte-check and build.
- A held Mongo password lives in memory only and is lost on reload; scan results are not persisted.
- A tag or typed address that looks like a connection string is refused client-side to match the server's 422 rule; the rule is a regex, not a guarantee.
- Earlier in the work `svelte-check` reported a type error in untracked `src/components/EnterCase.svelte` (another agent's file); it was gone by the final run (0 errors).
