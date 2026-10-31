# Flow coverage: existing Unravis app + proposed tracing, as reproduced in `tracing-paper-PROTOTYPE.html`

Throwaway prototype material. Synthetic data, simulated operations. It shows intended flow and state behaviour; it does not prove the software works.

## Baseline used

| Item | Value |
|---|---|
| Application | `master` @ `96dcc659b14fc5fbd8408453d76f13427e065743` (2026-10-29, "Add journey and error logging"). The prototype branch `prototype/tracing-paper-e2e` is based on it; no application file was changed. |
| Tracing specs | `docs/tracing-paper-spec-round6` @ `1396fd62690e6d2c8db2cb8d075f58cce5be90ad`. It differs from `master` only by build-out docs (and by lacking the newest logging commit), so the two do not conflict. |
| Was the real app run? | **Partly.** `go build ./cmd/audit-explorer` and `vite build` worked; `unravis -demo` ran and its analysis screen was walked in Chromium (screenshot `screenshots/00-real-app-analysis.png`). The case store needs Windows data protection (`demo cases could not be opened: encrypted storage is unavailable on this platform`), so the **start screen, create guide, case home, case key, archive and erase flows could not be run on Linux**. Those were traced from the Svelte components, handlers, `screenFlow.ts` and the copy files (`web/src/lib/copy/*`). Fidelity below says which is which. |

Authority applied: the task brief, then actual app code for existing behaviour, then explicit owner decisions in the specs for intentional changes. Prototype assumptions from earlier rounds were not treated as requirements.

### Intentional changes verified in the specs (everything else is preserved)

| Decision | Source | Effect in the prototype |
|---|---|---|
| Guided button on the case home is swapped for **Start tracing**; guided code stays, unlisted | D-2 = A, TR-01, WP-64, WP-63 (`impl-spec` §12, §9) | Case home shows an Analysis block and a **Tracing** block where the Guided block was. Guided analysis is not shown anywhere. |
| Tracing windows: 1 hour default, **30 days** maximum | D-4 | Tracing validation. The analysis keeps its 7-day limit. |
| Reference overlapping the incident is a **non-blocking** notice with a fix | TR-08, AC-04, WP-22, WS-12 | Warning on Tracing only. |
| Bucket click hands off to Run analysis | TR-09, WP-61 | "Open Run analysis for this bucket". |
| One UTC floor-aligned axis, width = max(ladder, aligned rung), at most 120 buckets, 0/1/2 raw scans | §4.5, §7, R6-02 (approved) | Axis maths and the "How this axis was worked out" box. |
| Export preview/confirm stamped with the case revision, scrub on by default, 409 on change | §5.2, D-9, R3-001 | Added to the existing Export drawer. |
| Live pulls (observation = source+lane+fingerprint+window, 409 on partial overlap), coverage ledger | §5.3, R6-03 (approved) | **MVP-2**, behind the stage switch in the prototype panel. |
| Mixed live/saved consistency note, `complete` is not "snapshot" | §5.1, §7, R6-07 | Tracing chart. |

## How to open and drive it

Double-click `prototype/tracing-paper-PROTOTYPE.html`. A fresh launch lands on the real three-card start screen. The normal journey needs no scenario shortcuts. The dock at the bottom is **prototype-only** (separate from product navigation): a scenario pill per screen (arrows, select, or ← → keys), a layout pill on Tracing, "What if" switches, **Prototype** (launch conditions, MVP-1/MVP-2 stage, test aids, assumptions and questions) and **State** (full state after every action; also in the console). Shareable links: `#/start`, `#/case/<id>`, `#/tracing/<id>`, `#/running/<id>`.

Persistence: cases and settings live in `localStorage`, the current screen, unlocked state and windows in `sessionStorage` (the real app keeps cases on disk and resumes a tab). "Reset prototype data" wipes both. Nothing leaves the browser.

## Flow-coverage table

Fidelity: **Full** = layout order, labels, validation and outcomes reproduced; **Core** = main behaviour and real copy, details simplified; **Stub** = reachable with feedback, content not expanded. "Code" = traced from source (not run). Prototype location = function in the HTML file.

### 1. Launch, master key, start screen

| # | Existing screen or action | Source | Destination · return | Prototype location | Existing vs approved change | Discrepancy / limit |
|---|---|---|---|---|---|---|
| 1 | App starts (loading → key state → start screen, unless demo/deep link/resume) | `App.svelte`, `screenFlow.ts` `initialScreen`, `appState` startup | start; resume to a remembered tab/deep link | `boot()`, `initialScreen`, `SCREENS.loading` | Preserved | Demo server skips the start screen (not reproduced; "Go to the analysis" covers that screen). Code. |
| 2 | Key locked/damaged at launch → unlock screen first, no titles/counts | `Landing.svelte` modes `unlock`, `MasterKeyUnlock/Damaged.svelte` | start (or the work it interrupted) | `SCREENS.unlock`, `ACT.unlockSubmit`, `damagedReset` | Preserved | Throttle is 3 wrong tries/5 s here; real server decides. Wrong-key sentence paraphrased. |
| 3 | Set a master key ("Protect your cases…") | `MasterKeySetup.svelte`, `KEY_TEXT` | done → start; "Not now" → back | `SCREENS.setup`, `ACT.setupSubmit` | Preserved | Older-cases rewrap notice not reproduced. |
| 4 | Lock (header) | `AppHeader.svelte` `lockCases` | unlock screen | `ACT.lock` | Preserved | Shown on start and case home only. |
| 5 | Start screen: three cards always shown | `StartScreen.svelte`, `START_TEXT` | — | `SCREENS.start` | Preserved | — |
| 6 | Use an existing case: loading / empty / failed + Try again / rows / Show more | `StartScreen.svelte`, `lib/startScreen.ts` | Open → case home; browser Back returns | `SCREENS.start`, `ACT.openCase`, `ACT.startRetry`, `startMore` | Preserved | Page size 50 as in `CASE_PAGE_SIZE`. |
| 7 | Row states: Closed, Key ending, legacy, archiving n%, working copy missing + Erase | `StartScreen.svelte` | Open or Erase confirm | `rowOf`, `SCREENS.start` | Preserved | Locked/legacy cases named by code, not title. |
| 8 | Erase a missing-copy case from the list (type the code) | `StartScreen.svelte` `erase()` | stays on list; one line left | `ACT.startErase*` | Preserved | — |
| 9 | Erased lines + Clear; Archived cases toggle and list; "cases from before…" note | `StartScreen.svelte`, `ArchivedList.svelte` | list | `SCREENS.start`, `archivedToggle`, `clearErased` | Preserved | Archived list paging not reproduced. |
| 10 | Enter a case code (spaces/dashes/case ignored; one not-found sentence) | `EnterCase.svelte`, `caseCode.ts` | opens case; keyed case lands on key card | `enterCard`, `ACT.enterCode`, `normaliseCode` | Preserved | **Discrepancy kept:** lists show an 8-character code, the card asks for 26. Test-aid button reveals the full code. Code. |
| 11 | Restore from a case file (path/key checks, one failure sentence, throttle, always a new case) | `EnterCase.svelte`, `restoreCase.ts` | new case → case home | `ACT.enterRestore`, `checkRestore` | Preserved | Needs a master key (set-up link when none). Fake files: see prototype panel. |
| 12 | Create a new one → guide | `Landing.svelte` → `CreateWizard.svelte` | create | `ACT.startCreate` | Preserved | — |
| 13 | "Go to the analysis" footer link | `StartScreen.svelte` | running (no case) | `ACT.goAnalysis` | Preserved | Analysis of the demo audit source. |
| 14 | Browser Back/Forward between start, case, create, running (+ tracing) | `screenFlow.ts` `historyStep` | history entries | `go`, `historyStep`, popstate handler | Preserved; **tracing added as a history screen** | New screen is an assumption (spec does not say). |

### 2. Create a new case

| # | Existing screen or action | Source | Destination · return | Prototype location | Existing vs approved change | Discrepancy / limit |
|---|---|---|---|---|---|---|
| 15 | Name step: name 2–80, environments (dedupe, 32 max, 60 chars, `›`), optional own key (≥12, repeat) | `CreateWizard.svelte`, `wizardFlow.ts`, `wizardCopy.ts` | Next blocked with the reason | `SCREENS.create`, `titleProblem`, `parseEnvs`, `keyProblem` | Preserved | — |
| 16 | Steps strip, Back/Next, draft kept between steps, Enter in the name field | `CreateWizard.svelte` | steps | `ACT.wizGo/Next/Back` | Preserved | — |
| 17 | Leave the guide (asks only when dirty); resume banner + Start over; beforeunload warning | `CreateWizard.svelte`, `wizard.svelte.ts` | start; draft discarded or kept | `ACT.wizLeave/Keep/Discard/Reset`, `beforeunload` | Preserved | — |
| 18 | Sources: scan this computer, ticks, "Where is it?", cannot-read/needs-export rows, gaps | `SourceGallery.svelte`, `LocateFolder.svelte` | chosen list | `wizSources`, `FINDINGS` | Preserved | Six fake findings; detectors and progress lines simplified. |
| 19 | Another computer: MongoDB (classify audit/other/unused), SQL by address, Redis, a file/export | `MongoClassify`, `SqlServerForm`, `ReachableCheck` | chosen list | `ACT.wizMongo*`, `wizSql*`, `wizRedis*`, `wizFileAdd` | Preserved | Core: address check + classification; TLS/CA, saved-connection picker not reproduced. |
| 20 | Name sources by Environment / Portal / API; Remove | `SourceTagRow.svelte` | list | `wizSources` | Preserved | Suggestion chips not reproduced. |
| 21 | Review: summary, duplicate-name note, file per file source, how far back, admin notice | `ReviewStep.svelte`, `wizardCreateCopy.ts` | Create blocked with the reason | `wizReview`, `reviewBlockers` | Preserved | Scan-stale rule not reproduced. |
| 22 | Creation progress (case, environments, source names, read sources), failed source keeps the case, Open the case (anyway) | `CreateProgress.svelte`, `wizardCreate.svelte.ts` | case home (replaces the create history entry) | `wizProgress`, `step`, `ACT.wizOpen` | Preserved | Locked-mid-create "Continue" not reproduced. |

### 3. Case home and what hangs off it

| # | Existing screen or action | Source | Destination · return | Prototype location | Existing vs approved change | Discrepancy / limit |
|---|---|---|---|---|---|---|
| 23 | Case home header, counts, state notes (closed, archived, archiving, legacy, missing) | `CaseHome.svelte`, `caseHomeCopy.ts` | — | `SCREENS.case` | Preserved | — |
| 24 | Loading / failed + Try again / gone / changed in another tab + Reload | `CaseHome.svelte` | start | `RT.chForce`, `U.ch.changed` | Preserved | Forced by scenarios (no real race). |
| 25 | Locked case → key card (wrong key, throttle, Cancel); Erase from the card | `CaseKeyCard.svelte` | case home | `keyCard`, `ACT.caseKeyOpen` | Preserved | — |
| 26 | Legacy case → explanation, Restore from a case file, Erase | `CaseHome.svelte`, `eraseCopy.ts` | start with the restore card open | `SCREENS.case`, `chRestoreIntent` | Preserved | — |
| 27 | Needs another try list: Add the file again / Try again / Remove | `CaseHome.svelte`, `caseLifecycle.ts` | import drawer or retry | `ACT.chRetry`, `chDropRetry` | Preserved | First retry fails then succeeds (simulated). |
| 28 | Archive this case (folder, sealed with, passphrase, progress, cancel) | `ArchiveDrawers.svelte` | case home (archived) | `DRAWERS.archive`, `ACT.arcGo` | Preserved | Verify-file drawer not reproduced. |
| 29 | Restore, Restore as a new case, Free the working copy | `ArchiveDrawers.svelte` | case home / new case | `ACT.arcRestore*`, `arcFree` | Preserved | — |
| 30 | Reopen this case (case home); Close case (Import panel) | `CaseHome.svelte` `reopen`, `ImportPanel.svelte`, `importCopy.ts` | case home | `ACT.chReopen`, `chClose` (button inside `DRAWERS.import`) | Preserved | Close lives in the Import panel, as in the app. |
| 31 | **Analysis block** "Open the analysis" | `CaseHome.svelte` | running; Case home button returns | `ACT.openAnalysis` | Preserved | — |
| 32 | **Guided block → Tracing block "Start tracing"** | `CaseHome.svelte` ~460–463 | tracing; Case home returns | `tracingBlock`, `ACT.startTracing` | **Approved change** D-2/WP-64; Start tracing off when closed or no sources (WS-01/04) | Guided code kept but unlisted. |
| 33 | Story so far | `CaseHome.svelte`, `lib/story.ts` | — | `story()` | Preserved | — |
| 34 | Theories: list, Open/Add (ledger drawer), add, state, remove | `CaseHome.svelte`, `LedgerPanel.svelte` | drawer; Close returns focus | `DRAWERS.ledger`, `ACT.theory*` | Preserved | State labels guessed (Open/Testing/Supported/Ruled out). Earlier-theories copy offer and compare view not reproduced. |
| 35 | Health checks (kept SQL, run, history) | `HealthPanel.svelte`, `healthCopy.ts` | in page | `healthPanel`, `ACT.chHealth` | Core | Mongo checks not reproduced. |
| 36 | Saved connections (SQL, MongoDB): test, edit, forget (asks), + SQL, + MongoDB, Refresh | `SourcesHub.svelte`, `sourcesCopy.ts` | in page / Connections drawer | `hubPanel`, `ACT.hub*`, `DRAWERS.connections` | Core | Edit not expanded; key-keeper states not reproduced. |
| 37 | Files list + Add a file (Import drawer): file, type, host, preview, import, duplicate/empty/too big/compressed, history | `ImportPanel.svelte`, `importCopy.ts` | drawer; list refreshes | `DRAWERS.import`, `ACT.imp*`, `FILES` | Preserved | Fake file list instead of a file picker. A file with no time-zone rule is previewed as such (feeds the tracing gate). |
| 38 | Case key: add / change / remove | `CaseKeyPage.svelte` | case home | `keyPage`, `ACT.caseKeySave` | Preserved | — |
| 39 | Erase this case (type the code) | `EraseCase.svelte` | start with one line left | `eraseView`, `ACT.chEraseGo` | Preserved | — |
| 40 | Header: Home, Case home, Ledger count, Import, Blackbox, Glossary, Explain simply, UTC/Local, Theme | `AppHeader.svelte` | various | `headerHTML` | Preserved | Blackbox and Glossary: Core/Stub. Local(UTC) label uses the browser zone. |

### 4. Analysis (existing "running" screen)

| # | Existing screen or action | Source | Destination · return | Prototype location | Existing vs approved change | Discrepancy / limit |
|---|---|---|---|---|---|---|
| 41 | Scope bar: quick ranges (compare with the equal period right before), Type, More filters, Run analysis + hint | `SubBar.svelte`, `QueryControls.svelte`, `time.ts` | auto-run on chip/type; Run applies window edits | `SCREENS.subbar`, `ACT.quick`, `kind`, `runBtn` | Preserved | Details and Ranges groups: Stub. |
| 42 | **When panel: Investigate From/To and Reference From/To**, browser time zone + UTC echo | `filters/WhenGroup.svelte`, `Query.baselineStart/End` | stale note until Run | `dtFields`, `ACT.winEdit` | Preserved; reference called the **comparison period** | — |
| 43 | Window rule: reference must end at or before the incident starts; ≤7 days; ≤120 points | `autorun.ts` `scopeProblem`/`scopeHint` | Run off with the hint | `analysisProblem`, `analysisHint` | Preserved | **Differs from Tracing on purpose** (row 52). |
| 44 | Pulse: five figures with change vs reference, headline, missing-timing strip, hero chart | `Pulse.svelte` | Where is it? | `pulse`, `heroChart` | Preserved | Deltas **withheld** without a usable comparison period (prototype rule: ≥30 events, ≥80% covered) and compared per minute when the two windows differ in length. |
| 45 | Where is it?: lenses, compare to earlier, ranked operations | `WhereView.svelte` | Evidence | `whereView` | Core | Split-by and quality panels reduced. |
| 46 | Evidence: tabs, request history, Export CSV, Inspect, Nearby | `EvidenceView.svelte`, `RequestTable` | Record; Back | `evidenceView`, `ACT.inspect/nearby` | Core | CSV is simulated. |
| 47 | Record detail | `RecordDetail.svelte` | Back to the evidence | `recordView` | Preserved | — |
| 48 | Nearby activity | `ActivityContext.svelte` | in Evidence | `nearbyBlock` | Core | **Call trace is not wired in the app** (`CallTrace.svelte` is mounted nowhere); the prototype says so and does not invent a link (D-11 is Later). |
| 49 | Breadcrumb, Back, source popover, coverage drawer, partial-source banner | `Breadcrumb`, `SourcePopover`, `CoverageDrawer`, `App.svelte` | depth/drawers | `goDepth`, `DRAWERS.sources`, `coverageBlock` | Core | — |
| 50 | Drawers: Patterns, Ledger, Export, Import, Connections, Glossary, Blackbox | `App.svelte` panels | drawers; Esc/Close | `DRAWERS.*` | Core / Stub | Patterns are three canned entries. |
| 51 | Export drawer: window, options, Preview, Save | `ExportPanel.svelte` | drawer | `DRAWERS.export` | **Approved change**: scrub (on by default), previewId stamped with the case revision, 409 and forced new preview | Spec does not say where tracing exports from (question 1). |

### 5. Tracing (new)

| # | Action | Spec | Destination · return | Prototype location | MVP stage | Discrepancy / limit |
|---|---|---|---|---|---|---|
| 52 | Incident and reference windows, independently editable; zone explicit; UTC echo; quick ranges; validation | TR-02, D-4, TR-08, WP-22 | Start tracing | `winTrigger`, `ACT.pk*`, `tracingChecks` | MVP-1 | **Overlap warns on Tracing, blocks in Run analysis.** 30 d vs 7 d. Distinction shown after the hand-off. |
| 53 | Sources chosen from the case; lanes from case sources | §5.1, §7 | trace | `trParts`, `ACT.trChoose` | MVP-1 | Lanes count only chosen sources. |
| 54 | Three lanes: audit live, SQL and IIS saved; provenance, `asOf`, consistency, mixed note | §5.1, §7, R6-07 | chart | `doTrace`, `laneRow`, `chartHTML` | MVP-1 | `allLanesStored` vacuous case shown (question 5). |
| 55 | Lane states: no data, out of range, none seen, clock unchecked, incomplete (timeout, rowcap, error) | WS-18–25, §7 | chart | `doTrace` | MVP-1 | — |
| 56 | Time-zone gate | TR-11, WS-25 | chart off | `doTrace` (`disabled:'tz-rule'`) | MVP-1 | Driven by the file's preview. |
| 57 | Reference on the chart | TR-08 | — | lane `cmp` | MVP-1 | **Spec defines no rendering.** Prototype shows a per-lane rate against the comparison period, or "No comparison: <reason>". Flagged. |
| 58 | Axis: UTC floor-aligned, coarser rung, ≤120 buckets, 0/1/2 raw scans; 30 d 00:01 → 8 h, 91 | §4.5, §7, R6-02 | explanation box | `axis()`, `traceDetails` | MVP-1 | Scan count is informational in MVP-1 (question 8). |
| 59 | Hover shows all lanes; click or ← → Home End select; Enter opens analysis | TR-09, WS-27/28 | analysis | `ACT.ovKey`, mousemove handler | MVP-1 | — |
| 60 | **Hand-off to Run analysis** with explicit incident/reference strip; Back to tracing keeps selections | TR-09, WP-61 | running; return to tracing | `ACT.handoff`, `backToTracing` | MVP-1 | Reference kept; if it overlaps, analysis refuses and offers "Use the equal period right before" (prototype suggestion). |
| 61 | Layouts A Report / B Workbench / C Lane ledger | prototype only | — | `SCREENS.tracing` | — | For choosing a layout; not a spec item. |
| 62 | Live SQL pull (observation scoped by source+lane+fingerprint+window): completed / unchanged / changed (atomic replace) / 409 overlap / 502 / rollback | §5.3, R6-03 | coverage card | `doPull`, `pullPanel` | **MVP-2** | Sub-window pulls get 409 (question 6). |
| 63 | Coverage ledger: ribbon, no-coverage, provenance, no `allLanesStored` | §5.3, R6-06 | card | `coverageCard` | **MVP-2** | — |
| 64 | Not shown (Later): route filter, persisted trace window, win32 series, call-trace deep link, delta overlay | TR-14, TR-17, D-11, D-12 | — | — | Later | Intentionally absent. |

## Unresolved product decisions (with sources)

1. Where does tracing export from, and does it replace the Export drawer's preview? (§5.2; `ExportPanel.svelte`).
2. What should the reference window draw on the tracing chart? (TR-08, §5.1 request schema; no rendering rule). The prototype's per-lane rate is an interpretation.
3. Overlap rule: Tracing warns (WP-22), Run analysis refuses (`autorun.ts` `scopeProblem`). After a bucket hand-off the user can land on a refusal.
4. 30-day tracing vs 7-day analysis limits (D-4 vs `autorun.ts`).
5. `allLanesStored` is vacuously true with nothing stored (§5.1/§7; R6-06 fixed only GET coverage).
6. Sub-window pulls: §5.3 "contains → skipped" vs R6-03 "not exactly equal → 409".
7. Different filters overlap and double count; a regenerated IIS file has no replacement rule (§4.3, §4.5, R6-03).
8. Boundary raw scans in MVP-1, where rollups do not exist yet (§5.1 vs §7, SC-UI-07).
9. Case code: lists show 8 characters, the Enter card needs 26 (`cases.ts` `caseCode`, `enterCopy.ts`).
10. Tracing as a browser-history screen (not in `screenFlow.ts`).

## Verification

`node prototype/flow-check.cjs [screenshot-dir]` drives the prototype in Chromium: 95 checks covering all three start-card journeys, code and restore validation, the create guide (validation, Back/Next, leave, failed source, retry), protected-case unlock, closed/archived/legacy/failed-load states, tracing → analysis → evidence → record → back with both windows preserved, both window rules, export 409, launch conditions, MVP-2 pulls, keyboard on the chart, browser Back/Forward and narrow layouts. Results are in the commit message and the final report.
