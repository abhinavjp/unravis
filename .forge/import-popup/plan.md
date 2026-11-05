# Import popup — implementation plan

Unravis replaces the cramped right-hand "Import files" drawer with a centred
three-step popup (Choose / Check / Result) that matches
`prototype/import-popup-WIREFRAME.html` exactly. This file is a plan only: no
code is changed by writing it.

## Findings

### Current data flow (frontend state -> API -> server -> reasons)

1. Open. `openCaseImport()` at `web/src/lib/appState.svelte.ts:862` calls
   `requestCase`, `requestFilesView`, then `openSidePanel('import')`.
   `panels.sidePanel` (`appState.svelte.ts:195`) is set to `'import'`.
2. Render. `web/src/App.svelte:105` renders `{#if panels.sidePanel === 'import'}<ImportPanel ...>`
   (lazy import at `App.svelte:30`). `App.svelte:80` `addData()` also opens it.
   `CaseHome.svelte` (lines 330, 411, 563, 577) and `TracingPaper.svelte`
   (332, 384, 393) and `Landing.svelte:33` all call `openCaseImport`.
3. Shell. `ImportPanel.svelte` mounts `<Drawer>` (`web/src/components/Drawer.svelte`),
   a modal side drawer with scrim, Esc/Tab trap, `onClose`. Width
   `min(640px, ...)` (`ImportPanel.svelte:607`).
4. Files. `ImportPanel.svelte` composes `ImportBatch.svelte`, which mounts
   `ImportFilesPanel.svelte` in `drawer` mode (`ImportBatch.svelte:102`).
   `ImportFilesPanel.svelte` uses `wizardStaging`/`createStaging`
   (`web/src/lib/wizardFiles.svelte.ts`), `fileKinds` (kind/host/zone need),
   `dropEntries`, `zipRead`, and `ZoneChoice.svelte` for the per-row computer.
5. Send. `ImportBatch.svelte` builds `BatchItem`s and calls `importRunner` +
   `runImportQueue` (`web/src/lib/cases.ts:716,725`). Each item becomes a job:
   - `startPreviewJob` (`cases.ts:593`) -> `POST /api/cases/{id}/imports?preview=1&async=1`
   - `startCommitJob` (`cases.ts:600`) -> same path with `ackGating`+`ackText`, no `preview`
   - `fetchImportJob` (`cases.ts:608`) -> `GET /api/cases/{id}/imports/jobs/{job}`
   - `cancelImportJob` (`cases.ts:613`) -> `DELETE .../jobs/{job}`
   - `waitForImportJob` (`cases.ts:643`) polls until done/failed.
6. Hosts. `listHosts` (`cases.ts:423`), `getHostEntries`/`putHostEntries`
   (`cases.ts:447,470`) -> `GET`/`PUT /api/hosts`. `wizardHosts.svelte.ts`
   `saveHost`/`saveHostZone` add one computer via read-then-PUT.
7. Server routes (`internal/server/routes.go`):
   - `/api/cases/{id}/imports` GET `listImports`, POST `postImport` (routes.go:263)
   - `/api/cases/{id}/imports/jobs/{job}` GET/DELETE (routes.go:267)
   - `/api/hosts` GET `getHosts`, PUT `putHosts` (routes.go:391)
   - `/api/importers` GET `listImporters` (routes.go:395)
8. Server import. `postImport` (`internal/server/cases.go:1392`): `preview=1`
   at 1414 (rolled back, no ack), host lookup at 1443 (refuses 422
   `unknown-host` when the picked host is unknown), gate+slot+async job at
   1498. `importFileWith` (`cases.go:1600`) is the shared reader; `preview=true`
   reads and emits nothing. The async job runs `runImportJob`
   (`internal/server/import_jobs.go:379`) over an encrypted spool, writes to a
   `captureWriter` (`import_jobs.go:471`), and `finish` (`import_jobs.go:314`)
   stores the captured body as `importJobDTO.Result` (`import_jobs.go:104`,
   `json.RawMessage`).
9. Host refusal. `resolveHost` (`internal/importer/iis/common.go:114-153`)
   returns `importer.ErrUnknownHost` (lines 130, 137, 152) when a name is not in
   the host map and there is no fallback; observed names reach
   `Report.ObservedHosts` (`cases.go:577`, `toReportDTO` at 585). Reasons enum:
   `internal/importer/reasons.go` (`ReasonUnknownHost="unknown-host"`,
   `ReasonUnsupportedFormat`, `ReasonArchive`, binary reasons, etc.).
10. Discover reuse. `discover_import.go` already runs many files through the
    shared importer with a `captureWriter` and `importFileWith(preview=true,
    allowUnlistedHost=false)` (`importPiece` at
    `internal/server/discover_import.go:491`). The check endpoint below copies
    that pattern.

### New API contract: check-only endpoint

Route (new): `POST /api/cases/{id}/imports/check?async=1`
- Multipart/form-data, repeated `file` parts (one per staged file). No
  `ackGating`/`ackText` (nothing is stored). No `importer`/`host` (the check
  auto-detects the kind and refuses unknown hosts on purpose, so fix cards can
  name the server).
- Reuses the case gate, the import limiter slot, the job registry and the
  encrypted spool exactly as `postImport` does; the only difference is that
  every file is read with `preview=true` through `importFileWith`, and the
  captured answer is mapped to a verdict instead of stored.
- 202 with `importJobDTO` whose new `Kind` field is `"check"` (see below).
- Progress and cancellation reuse `GET`/`DELETE /api/cases/{id}/imports/jobs/{job}`.

Job DTO change (`internal/server/import_jobs.go`): add `Kind string` to
`importJob` and `importJobDTO` (`json:"kind,omitempty"`, default `"import"` when
empty, so existing jobs and callers are unaffected). `parseImportJob` on the
client gains `kind: 'import' | 'check'`.

Check result (the job's `result` JSON when `state==="done"`):

```go
type checkFileVerdict struct {
    Key        string   `json:"key"`        // stable client id (name|size|lastModified)
    Name       string   `json:"name"`
    Size       int64    `json:"size"`
    Verdict    string   `json:"verdict"`    // ready|needs-computer|needs-zone|not-supported|no-computer
    Importer   string   `json:"importer"`   // detected kind; "" when not detected
    Tied       []string `json:"tied"`       // 2+ when the kind could not be chosen
    ServerName string   `json:"serverName"` // observed host name (needs-computer)
    NeedsZone  bool     `json:"needsZone"`  // local-clock kind without a zone
    Reason     string   `json:"reason"`     // not-supported reason code
    Rows       int      `json:"rows"`
    Emitted    int      `json:"emitted"`
    Rejected   int      `json:"rejected"`
}

type checkReportDTO struct {
    Files     []checkFileVerdict `json:"files"`
    CheckedAt time.Time          `json:"checkedAt"`
}
```

Verdict mapping (server side, in the new handler): `done` preview with an
`importer` and no refusal -> `ready` (or `needs-zone` when
`report.Quality.TimezoneRule` says a local clock and no zone is set);
`unknown-host` with `len(report.ObservedHosts)>0` -> `needs-computer`
(`ServerName` = first observed); `unknown-host` with none -> `no-computer`;
422 no importer / `unsupported-format` / archive / binary -> `not-supported`
(`Reason` = the reason code); tied importers -> `importer=""`, `tied=[...]`,
verdict `not-supported` with reason `tied` is not used — the client treats a
non-empty `tied` as "pick a kind" inside the not-supported card.

### Adding a computer + retry (no new endpoint)

- Adding a computer reuses `PUT /api/hosts` via `saveHost`
  (`web/src/lib/wizardHosts.svelte.ts:72`): read the list, append one
  `HostEntry` (`{label, aliases:[], role:'', tz, logicalCores:null, runtime:''}`),
  PUT it back. The fix card's primary button does this, then marks the card
  resolved; the files it covered become importable because the host map now
  holds the name.
- Retry is client-side: `openCaseImport({retryServerName})` opens the popup on
  the Check step with that server's fix card open and focused. No server route.
- Commit of accepted files reuses `startCommitJob` per file (the host is now
  known, so `resolveHost` accepts it; the host entry's `tz` covers local-clock
  kinds). The Importing step reuses `importRunner`/`runImportQueue`.

## Dependency graph

Indented list. Leaves first; a packet may start when every indented child above
it in its wave is done. Same-wave packets have disjoint write scopes.

```
Wave 1 (foundation)
  P1 server check endpoint + Go types + tests
  P2 frontend API client + types
  P3 copy + glossary
Wave 2 (UI leaves)
  P4 popup shell + state machine            (needs P2, P3)
  P5 grouped list                           (needs P3)
  P6 fix card (with inline zone)            (needs P2, P3)
  P7 not-supported + kind picker            (needs P3)
Wave 3 (steps)
  P8 choose step                            (needs P4, P5)
  P9 check step                             (needs P4, P5, P6, P7)
  P10 importing step                        (needs P2)
  P11 result step                           (needs P3)
Wave 4 (composition)
  P12 popup body + footer + transitions     (needs P4, P8, P9, P10, P11)
Wave 5 (integration + unit tests)
  P13 wiring: openCaseImport opens popup    (needs P12)
  P14 vitest unit tests                     (needs P4, P12)
Wave 6 (end-to-end)
  P15 update existing e2e specs             (needs P13)
  P16 new e2e specs                         (needs P13)
```

## P1 Server check endpoint + Go types + tests
Risk: medium
### Outcome
`POST /api/cases/{id}/imports/check?async=1` reads every uploaded file once
with `preview=true` (nothing stored), maps each to a verdict, and returns a
background job whose `result` is a `checkReportDTO`. Existing import jobs are
unaffected.
### Write scope
- `internal/server/import_check.go` (new)
- `internal/server/import_check_test.go` (new)
- `internal/server/routes.go` (add one route entry)
- `internal/server/import_jobs.go` (add `Kind` field)
### Must not change
- `postImport`, `importFileWith`, `resolveHost`, the reasons enum, the hosts
  handlers, the existing import job lifecycle, spool encryption.
### Changes
- `routes.go`: add `{pattern: "/api/cases/{id}/imports/check", match: caseSubpath("/imports/check"), methods: map[string]routeFunc{http.MethodPost: func(h,w,r,id){ h.postImportCheck(w,r,id) }}}` ahead of the `/api/cases/{id}/imports` entry (so `/imports/check` is not swallowed by `/imports`).
- `import_jobs.go`: add `Kind string` to `importJob` and `importJobDTO` (`json:"kind,omitempty"`); set `kind:"import"` in existing `create`/`snapshot` paths; `importJobDTO.Kind` defaults to `"import"` when empty in `parseImportJob`.
- `import_check.go`:
  - `func (h *handler) postImportCheck(w http.ResponseWriter, r *http.Request, raw string)`: open case (`openCaseForWrite`), refuse closed (`case-closed`), require `registry`, acquire gate+slot (as `postImport` does), create a job via `h.jobs.create(id)` with `job.kind="check"`, read each `file` part into a spool (`newSpool`), then `go h.runCheckJob(...)`. Answer 202 with `job.snapshot()`.
  - `func (h *handler) runCheckJob(job *importJob, items []checkItem, id casestore.CaseID, slot, release func())`: for each item, open a `captureWriter`, call `h.importFileWith(ctx, capture, id, c, imp=nil-detected, hint=nil, supplied=nil, body, preview=true, ackGating=false, ackText="", name, importer.Window{}, allowUnlistedHost=false)`. When no `importer` query was sent the handler auto-detects by first reading with `imp=nil`; reuse the same detect path `postImport` uses when `preview && imp==nil` (the registry's Detect). Map `capture.code()` + parsed `importAnswer`/`caseFailure` to `checkFileVerdict` per the mapping above. Aggregate into `checkReportDTO{Files, CheckedAt}` and `job.finish(now, 200, json)`.
  - Reuse `captureWriter`, `spoolFile`, `countingReader`, `h.gates`, `h.imports` unchanged.
- `import_check_test.go`: `TestImportCheckReady`, `TestImportCheckNeedsComputer` (IIS file naming an unlisted host -> verdict `needs-computer`, `ServerName` set), `TestImportCheckNoComputer` (sql-errorlog with no host -> `no-computer`), `TestImportCheckNotSupported` (a .zip/binary -> `not-supported` with reason), `TestImportCheckJobKind` (DTO `kind=="check"`), using the existing `fakeEnv`/`upload` helpers from `internal/server/discover_test.go` and the W3C fixture under `tools/e2e/fixtures/files`.
### Proof
`go test ./internal/server -run TestImportCheck`
### Depends on
none

## P2 Frontend API client + types
Risk: low
### Outcome
The web client can start a check job, poll it, and parse a `CheckReport`; the
existing `ImportJob` gains a `kind` without breaking callers.
### Write scope
- `web/src/lib/cases.ts`
### Must not change
- `previewImport`, `commitImport`, `startPreviewJob`, `startCommitJob`,
  `waitForImportJob`, `listHosts`, `getHostEntries`, `putHostEntries`,
  `ImportRequest`, `ImportAnswer`, `BatchOutcome`.
### Changes
- Add types:
  - `export type CheckVerdict = 'ready' | 'needs-computer' | 'needs-zone' | 'not-supported' | 'no-computer';`
  - `export interface CheckFileVerdict { key: string; name: string; size: number; verdict: CheckVerdict; importer: string; tied: string[]; serverName: string; needsZone: boolean; reason: string; rows: number; emitted: number; rejected: number; }`
  - `export interface CheckReport { files: CheckFileVerdict[]; checkedAt: string; }`
- `ImportJob`: add `kind: 'import' | 'check'`.
- `parseImportJob`: read `kind` (default `'import'`); when `kind==='check'` and `state==='done'`, parse `result` as `CheckReport` into a new `ImportJob.checkReport` field (keep `answer` null). Add `checkReport: CheckReport | null` to `ImportJob`.
- `startCheckJob(request: { caseId: string; files: { key: string; file: File }[] }, signal?: AbortSignal): Promise<ImportJob>`: multipart with repeated `file` parts (filename = `file.name`), POST `casePath(caseId,'/imports/check') + '?async=1'`, return `readJob(...)`.
- `parseCheckReport(body: unknown): CheckReport | null` shape-checked like `parseImportJob`.
- `waitForImportJob` already returns the job's answer; add `export async function waitForCheckJob(caseId, first, options): Promise<CheckReport>` that polls the same endpoint and resolves when `job.checkReport` is set.
### Proof
`cd web; npm test -- cases`
### Depends on
none

## P3 Copy + glossary
Risk: low
### Outcome
All popup copy lives in one reviewable module; the glossary defines Computer,
Server name, Not supported; the old "unknown host" wording is replaced with
"needs a computer" everywhere it reaches a person.
### Write scope
- `web/src/lib/copy/importPopupCopy.ts` (new)
- `web/src/lib/copy/deck.ts` (add three terms)
- `web/src/lib/copy/importCopy.ts` (reword the user-facing unknown-host strings)
### Must not change
- `GATING_BANNER`, `ACK_LABEL`, `ackTextFor`, `IMPORTER_LABELS`, `REASON_TEXT`
  keys, `importCopyEntries`, the copy lint entry list shape.
### Changes
- `importPopupCopy.ts` exports `POPUP_TEXT` with strings taken verbatim from the wireframe where it appears:
  - `title: 'Import files'`
  - `stepNames: ['Choose','Check','Result']`
  - `prog: { 1: 'Step 1 of 3 · Choose files to import', 2: 'Step 2 of 3 · Check files with the server before import', 3importing: 'Step 3 of 3 · Importing — do not close', 3result: 'Step 3 of 3 · Result' }`
  - `dropTitle: 'Drop files or a folder here'`
  - `dropSub: 'IIS web logs, log4net, SQL error logs, Windows event logs. Text and CSV only.'`
  - `addFiles: 'Add files'`, `addFolder: 'Add a folder'`
  - `continueBtn: 'Continue'`
  - `nothingSent: 'Nothing is sent until you press Import.'`
  - `checking: (n) => 'Checking ' + n + ' files with the server…'`
  - `checkingSub: 'Nothing is imported yet.'`
  - `fixTitleNeedsComputer: (n, name, kind) => n + ' ' + kind + ' files come from ' + name + ', which is not on your computer list.'`
  - `fixHelpNeedsComputer: 'Unravis will not guess which computer wrote a log. Add it once and every file from that server is accepted.'`
  - `fixNoteIis: 'IIS logs are already UTC, no time zone needed.'`
  - `fixAccept: (name, n) => 'Add ' + name + ' as a computer and accept ' + n + ' files'`
  - `fixLeaveOut: (n) => 'Leave these ' + n + ' files out'`
  - `fixAddAll: (n) => 'Add all ' + n + ' servers'`
  - `fixResolved: (name, n) => 'Added ' + name + ', ' + n + ' files accepted'`
  - `fixUndo: 'Undo'`
  - `fixBringBack: 'Bring them back'`
  - `fixLeftOut: (n, kind) => n + ' ' + kind + ' files left out'`
  - `fixNoComputerTitle: (n, kind) => n + ' ' + kind + ' files do not say which computer wrote them.'`
  - `fixNameLabel: 'Name of the computer'`
  - `fixNameHint: 'Suggested from the folder name. Change it if wrong.'`
  - `fixZoneLabel: 'Time zone of its clock'`
  - `fixZoneHelp: 'SQL error logs use the computer\u2019s own clock, so the time zone changes where events land on the timeline.'`
  - `fixUseName: (name, n) => 'Use ' + name + ' and accept ' + n + ' files'`
  - `notSupportedHead: (n) => n + ' files are not supported'`
  - `notSupportedReason: 'This file is not supported'`
  - `removeFromList: 'Remove from list'`, `remove: 'Remove'`
  - `kindPickerPlaceholder: 'Tell us what this is'`
  - `kinds: ['IIS web log','log4net','SQL error log','Windows event log','Hangfire','other']`
  - `ack: 'I understand this is read-only and nothing is changed on these computers'`
  - `importBtn: (n) => 'Import ' + n + (n===1?' file':' files')`
  - `fixesNeeded: (n) => n + (n===1?' fix':' fixes') + ' needed before you can import'`
  - `discardTitle: (n) => 'Discard ' + n + (n===1?' file':' files') + '?'`
  - `discardBody: 'These files are still on the list. Discarding clears them and closes the popup.'`
  - `discardKeep: 'Keep them'`, `discardNow: 'Discard'`
  - `impLine: (p, total) => 'Importing ' + p + ' of ' + total + ' files'`
  - `cancel: 'Cancel'`
  - `resultHero: (n) => n + ' files imported'`
  - `resultImported: 'Imported'`, `resultNotImported: 'Not imported'`
  - `resultNoType: 'No file type chosen'`
  - `computersLine: (n, names) => 'Added ' + n + ' computers: ' + names + '.'`
  - `seeComputers: 'See computers'`
  - `openTimeline: 'Open timeline'`, `importMore: 'Import more'`
  - `badge: { ready: 'ready', found: 'found', needsChecking: 'needs checking' }`
- `deck.ts`: add three terms (`id: 'computer'`, term `Computer`, hint `A server Unravis has been told about.`, definition two sentences, aliases `['Host']`; `id: 'server-name'`, term `Server name`, hint `The name a log file says wrote it.`, definition, aliases `['Host name']`; `id: 'not-supported'`, term `Not supported`, hint `Unravis cannot read this file as it is.`, definition, notMeaning).
- `importCopy.ts`: replace the person-facing strings (not the reason codes): `IMPORT_TEXT.unknownHostBadge` -> `'needs a computer'`; `IMPORT_TEXT.blocked['unknown-host']` and `blocked['unknown-host-none']` -> `'needs a computer'` wording; `REASON_TEXT['unknown-host']` and `'unknown-host-none'` -> `'This file needs a computer. Add it to the list, then try again.'`; `IMPORT_TEXT.noHosts` -> `'The computer list is empty, so a file that names a computer is refused as needing a computer.'`. Keep the `reason` string values (`'unknown-host'`) unchanged so the API contract is stable.
- Add the new strings to `importCopyEntries()` walk by exporting `popupCopyEntries()` from `importPopupCopy.ts` and merging it in the lint caller (the existing lint entry point).
### Proof
`cd web; npm test -- importCopy`
### Depends on
none

## P4 Popup shell + state machine
Risk: medium
### Outcome
A centred modal popup shell with scrim, stepper, progress line, body slot,
footer slot, focus trap, Esc + outside-click discard confirm, and a
phone-width full-screen rule. The step/state machine is testable on its own.
### Write scope
- `web/src/lib/components/importPopup/ImportPopup.svelte` (new)
- `web/src/lib/components/importPopup/popupState.svelte.ts` (new)
### Must not change
- `Drawer.svelte`, `appState.svelte.ts` (wiring is P13), any wizard file.
### Changes
- `popupState.svelte.ts` exports `createPopupState()` returning a `$state` object:
  - `step: 1|2|3`, `checking: boolean`, `importing: boolean`, `importDone: boolean`,
    `importProgress: number`, `expanded: Record<string, boolean>`,
    `selected: Record<string, boolean>`, `cards: Record<string, 'open'|'resolved'|'leftout'>`,
    `ack: boolean`, `discard: boolean`, `closed: boolean`,
    `retryServerName: string | null`, `files: StagedFile[]`, `report: CheckReport | null`.
  - Derived: `fixesNeeded()`, `targetCount()` (selected files whose card is not `leftout`), `canImport()` (`fixesNeeded()===0 && ack`), `listedFiles()`.
  - Actions: `continueToCheck()`, `toggle(group)`, `setSelected(name, on)`, `groupAll(group, on)`, `resolveCard(name)`, `leaveOut(name)`, `undoCard(name)`, `addAllServers()`, `removeUnsupported(key)`, `setKind(key, kind)`, `startImport()`, `cancelImport()`, `importMore()`, `openTimeline()`, `requestClose()` (sets `discard=true` when `listedFiles()>0 && step!==3`), `keepFiles()`, `discardNow()`.
- `ImportPopup.svelte`:
  - Props: `{ caseId: string; caseTitle: string; query: Query; zone: string; onClose: () => void; retryServerName?: string | null }`.
  - Markup mirrors the wireframe: `.scrim`, `.popup[role=dialog][aria-modal=true]`, `.popup-head` (`h2#popTitle` `Import files`, `.case-chip` `Case: {caseTitle}`, `.close-x`), `.stepper` (3 steps, click goes back only to a step < current), `.prog-line`, `.popup-body` (slot rendered by `step`), `.popup-foot` (slot), `.discard-veil`.
  - CSS copied from the wireframe for `.popup`, `.popup-head`, `.stepper`, `.prog-line`, `.popup-body`, `.popup-foot`, `.discard-veil`, `.discard-card`, the `@media(max-width:720px)` full-screen rule, and touch-target bumps. Width `min(960px, calc(100vw - 48px))`.
  - Focus trap: Tab/Shift+Tab cycles focusable elements inside `.popup` (same selector as the wireframe). Esc calls `requestClose()`; if `discard` is open, Esc answers "Keep them". Outside click (scrim) calls `requestClose()`.
  - Discard confirm renders `.discard-card` with `POPUP_TEXT.discardTitle(listedFiles())`, `.discardBody`, buttons `Keep them` / `Discard`.
  - On open, focus the popup; if `retryServerName`, set `step=2`, open that card, focus its name field.
### Proof
`cd web; npm test -- popupState`
### Depends on
P2, P3

## P5 Grouped list
Risk: low
### Outcome
A reusable group line that expands on demand, with a group checkbox and
per-file checkboxes (unselecting works, indeterminate state supported).
Problem files are always visible.
### Write scope
- `web/src/lib/components/importPopup/GroupedList.svelte` (new)
### Must not change
- `ImportFilesPanel.svelte`, `wizardFiles.svelte.ts`, `fileKinds.ts`.
### Changes
- Props: `{ groupKey: string; title: string; meta: string; badge: 'ready'|'found'|'needs checking'; kind?: string; files: { key: string; name: string; size: string }[]; selectable?: boolean; selected?: Record<string, boolean>; expanded?: boolean; onToggle?: (key:string)=>void; onGroupAll?: (key:string)=>void; onSelect?: (key:string, on:boolean)=>void }`.
- Renders `.grp`, `.grp-head` (chevron, name, `.grp-meta`, `.grp-badge`), and when expanded `.grp-files` with `.grp-all` checkbox (indeterminate when some but not all selected) and `.file-row`s (checkbox, `.file-name`, `.file-size`, optional `.file-tag`).
- Badges use `badge-ok`/`badge-muted` per the wireframe.
### Proof
`cd web; npm test -- GroupedList`
### Depends on
P3

## P6 Fix card (with inline zone)
Risk: medium
### Outcome
One guided fix card per server name. Name pre-filled; primary button adds the
computer and accepts its files; add-all shortcut; leave-out; undo; resolved
state. Inline time zone only for local-clock kinds.
### Write scope
- `web/src/lib/components/importPopup/FixCard.svelte` (new)
### Must not change
- `wizardHosts.svelte.ts`, `ZoneChoice.svelte`, the hosts API.
### Changes
- Props: `{ card: 'open'|'resolved'|'leftout'; kind: string; serverName: string; fileCount: number; files: { key:string; name:string; size:string }[]; selected?: Record<string,boolean>; needsZone?: boolean; suggestedZone?: string; expanded?: boolean; onResolve?: (name:string, zone?:string)=>void; onLeaveOut?: ()=>void; onUndo?: ()=>void; onAddAll?: ()=>void; onToggle?: (key:string)=>void; onGroupAll?: (key:string)=>void; onSelect?: (key:string,on:boolean)=>void }`.
- States mirror the wireframe `fixCardA`/`fixCardB`:
  - `open` + not needsZone: title `POPUP_TEXT.fixTitleNeedsComputer(...)`, help `fixHelpNeedsComputer`, note `fixNoteIis`, actions `fixAccept(serverName, fileCount)` (primary), `fixLeaveOut(fileCount)`, `fixAddAll(totalServers)`.
  - `open` + needsZone: title `fixNoComputerTitle(...)`, field `Name of the computer` (value `serverName`, hint `fixNameHint`), `Time zone of its clock` select (options `POPUP_TEXT.kinds`? no — zone list: UTC + common zones, default `suggestedZone`), help `fixZoneHelp`, action `fixUseName(serverName, fileCount)`.
  - `resolved`: `resolved-line` `fixResolved(serverName, fileCount)`, `Undo` button.
  - `leftout`: title `fixLeftOut(fileCount, kind)`, help with `Bring them back` link.
- Embeds an inline file list (`.grp` borderless) reusing the GroupedList markup pattern (or a compact local render to stay one file).
- Calls `saveHost` (from `wizardHosts.svelte.ts`) on resolve; on success calls `onResolve(name, zone)`.
### Proof
`cd web; npm test -- FixCard`
### Depends on
P2, P3

## P7 Not-supported + kind picker
Risk: low
### Outcome
Truly wrong files show "This file is not supported", a Remove action, and a
"Tell us what this is" kind picker.
### Write scope
- `web/src/lib/components/importPopup/NotSupported.svelte` (new)
### Must not change
- `fileKinds.ts`, `IMPORTER_LABELS`.
### Changes
- Props: `{ files: { key:string; name:string; size:string; reason:string }[]; onRemove?: (key:string)=>void; onPickKind?: (key:string, kind:string)=>void; pickedKind?: Record<string,string> }`.
- Renders `.notsupp` with head `POPUP_TEXT.notSupportedHead(files.length)`, one `.notsupp-row` per file: `.file-name`, `.file-size`, `.reason`, row actions `Remove from list`/`Remove` and a `select.kind-pick` with placeholder `Tell us what this is` and options `POPUP_TEXT.kinds`.
- When all removed, shows `All unsupported files removed.`
### Proof
`cd web; npm test -- NotSupported`
### Depends on
P3

## P8 Choose step
Risk: low
### Outcome
Step 1: a drop zone and the list of staged files grouped by detected kind,
each group collapsible. Continue starts the check.
### Write scope
- `web/src/lib/components/importPopup/ChooseStep.svelte` (new)
### Must not change
- `ImportFilesPanel.svelte`, `wizardFiles.svelte.ts`, `dropEntries.ts`, `zipRead.ts`.
### Changes
- Props: `{ files: StagedFile[]; expanded: Record<string,boolean>; onAddFiles: (files: File[])=>void; onAddFolder: (files: File[])=>void; onDrop: (e: DragEvent)=>void; onToggle: (key:string)=>void; onContinue: ()=>void }`.
- Renders `.drop` (`dropTitle`, `dropSub`, buttons `Add files`/`Add a folder`), then one `GroupedList` per kind group (`selectable=false`, badge `found`, meta `N files` or `N files · folder {name}`). Unrecognised files group titled `Not recognised yet` badge `needs checking`.
- Footer (rendered by the shell): `Nothing is sent until you press Import.` + `Continue` (primary), disabled when `files.length===0`.
- Reuses `filesFromDrop`/`readZip` for input; stages via the popup state (P4) — this component only emits `onAddFiles`.
### Proof
`cd web; npm test -- ChooseStep`
### Depends on
P4, P5

## P9 Check step
Risk: medium
### Outcome
Step 2 results: ready groups, one fix card per server name, the not-supported
group, and the no-computer card. Check-all shortcut. Re-runs the check after a
fix is resolved (so a case the check missed still shows).
### Write scope
- `web/src/lib/components/importPopup/CheckStep.svelte` (new)
### Must not change
- `FixCard.svelte`, `NotSupported.svelte`, `GroupedList.svelte`, the server check handler.
### Changes
- Props bound to popup state: `{ report: CheckReport; files: StagedFile[]; cards; expanded; selected; totalServers; onResolve; onLeaveOut; onUndo; onAddAll; onRemoveUnsupported; onPickKind; onToggle; onGroupAll; onSelect }`.
- While `checking`: `.checking` spinner, `POPUP_TEXT.checking(fileCount)`, `checkingSub`.
- Otherwise: ready `GroupedList`s (badge `ready`, selectable, `selected` wiring), then one `FixCard` per distinct `serverName` from `needs-computer` verdicts, then one `FixCard` for the `no-computer` verdicts (with `needsZone` when the kind is `sql-errorlog`/`win-events`), then `NotSupported` for `not-supported` verdicts.
- `totalServers` = distinct server names needing a computer; `fixAddAll(totalServers)` resolves every open card.
- Footer (shell): `fixesNeeded` hint, the ack checkbox (`POPUP_TEXT.ack`), and `Import {targetCount} files` (primary, disabled unless `canImport()`).
### Proof
`cd web; npm test -- CheckStep`
### Depends on
P4, P5, P6, P7

## P10 Importing step
Risk: medium
### Outcome
Step 3 importing: per-file progress bar and per-group mini bars and counts;
Cancel stops after the current file.
### Write scope
- `web/src/lib/components/importPopup/ImportingStep.svelte` (new)
### Must not change
- `cases.ts` (`importRunner`, `runImportQueue`, `startCommitJob`), `ImportBatch.svelte`.
### Changes
- Props: `{ progress: number; total: number; groups: { name:string; done:number; count:number }[]; onCancel: ()=>void }`.
- Renders `.imp-bar-wrap` (`impLine(progress, total)` + percent), `.imp-track`/`.imp-fill`, and one `.imp-grp` per group (`.gname`, `.imp-mini`, `.gcount` `N of M`).
- The popup state (P4) drives `importRunner`/`runImportQueue` over the selected files; `onUpdate` sets `importProgress` and group counts. `Cancel` calls `cancelImport()` -> aborts the runner's stop signal (polite: finish current file).
### Proof
`cd web; npm test -- ImportingStep`
### Depends on
P2

## P11 Result step
Risk: low
### Outcome
Step 3 result: a hero count, Imported / Not imported sections, the computers
added line, and Open timeline / Import more.
### Write scope
- `web/src/lib/components/importPopup/ResultStep.svelte` (new)
### Must not change
- `CaseTools.svelte`, the timeline API.
### Changes
- Props: `{ imported: { name:string; count:number }[]; notImported: { name:string; reason:string }[]; computers: string[]; onOpenTimeline: ()=>void; onImportMore: ()=>void }`.
- Renders `.result-hero` (`{N} files imported`), `.result-section` `Imported` (`.result-line` per group), `.result-section` `Not imported` (per file with reason; `No file type chosen` when none picked), `.computers-line` `Added {n} computers: {names}.` with `See computers` link, footer `Import more` (secondary) + `Open timeline` (primary).
### Proof
`cd web; npm test -- ResultStep`
### Depends on
P3

## P12 Popup body + footer + transitions
Risk: medium
### Outcome
`ImportPopup.svelte` renders the right step body and footer from `popupState`,
runs the check, drives the import, and shows the result. The shell from P4
becomes the full popup.
### Write scope
- `web/src/lib/components/importPopup/ImportPopup.svelte` (edit)
- `web/src/lib/components/importPopup/popupState.svelte.ts` (edit)
### Must not change
- The step components (P8-P11), `appState.svelte.ts`, `App.svelte`.
### Changes
- In `ImportPopup.svelte`: render `ChooseStep` / `CheckStep` / (`ImportingStep` or `ResultStep`) by `step` and state; render the footer per step (Continue / Import+ack / Cancel / Open timeline+Import more) using `POPUP_TEXT`.
- In `popupState.svelte.ts`: implement `continueToCheck()` -> set `checking=true`, call `startCheckJob` (P2), poll with `waitForCheckJob`, set `report`, `checking=false`, open the retry card when `retryServerName` is set. Implement `startImport()` -> build `BatchItem`s from selected, non-leftout files with their resolved host (from added computers) and kind; run `importRunner`/`runImportQueue`; update `importProgress` and group counts; on finish build the result model and set `importDone=true`. Implement `importMore()` -> reset to step 1 keeping the case. Implement `openTimeline()` -> `onClose()` then call the case timeline opener (the shell passes a callback).
- After a fix is resolved, re-run the check for only the affected files (or the whole set) so a missed case still appears; on a refusal during import, reopen the popup on that file's fix card (set `retryServerName`).
### Proof
`cd web; npm test -- ImportPopup`
### Depends on
P4, P8, P9, P10, P11

## P13 Wiring: openCaseImport opens the popup
Risk: medium
### Outcome
`openCaseImport` opens the centred popup instead of the import side drawer.
The old `ImportPanel` drawer usage for import is removed; the Inspector stays a
side panel. Retry reopens on the fix card.
### Write scope
- `web/src/lib/appState.svelte.ts` (edit)
- `web/src/App.svelte` (edit)
### Must not change
- `Drawer.svelte`, the Inspector, other side panels, `openSidePanel` for non-import panels.
### Changes
- `appState.svelte.ts`: replace the `'import'` side-panel path for case import with a popup state. Add `let importPopup = $state<{ caseId: string; caseTitle: string; retryServerName: string | null } | null>(null);` and expose it through `panels` (or a new `importPopup` accessor). Change `openCaseImport()` (line 862) to set `importPopup = { caseId: activeCaseId, caseTitle: caseTitleOf(activeCaseId), retryServerName: null }` instead of `openSidePanel('import')`. Add `openCaseImportRetry(serverName: string)` that sets `retryServerName`. Keep `openSidePanel` for `'blackbox'`, `'patterns'`, `'ledger'`, `'export'`.
- `App.svelte`: remove `{#if panels.sidePanel === 'import'}<ImportPanel ...>` (line 105) and the lazy import (line 30). Add `{#if panels.importPopup}` rendering `<ImportPopup caseId={...} caseTitle={...} zone={scope.zone} query={scope.query} retryServerName={...} onClose={() => panels.importPopup = null} onOpenTimeline={openTracingOrAnalysis} />`. The `addData()` at line 80 now opens the popup too (call `openCaseImport`).
- Callers of `openCaseImport` (`Landing.svelte:33`, `CaseHome.svelte:330/411/563/577`, `TracingPaper.svelte:332/384/393`) need no change (same function name). A retry path (`CaseHome` retry action `add-file`) calls `openCaseImportRetry(serverName)` when the failure was `unknown-host`.
- The `Drawer.svelte` import-history and `HostsEditor` behaviours that lived inside `ImportPanel` move into the popup's Result/Choose steps or are dropped per the wireframe (the wireframe has no import history table inside the popup).
### Proof
`cd web; npm run build` (typecheck) and `cd tools/e2e; npx playwright test specs/import-popup-shell.spec.ts`
### Depends on
P12

## P14 Vitest unit tests
Risk: low
### Outcome
Pure logic of the popup is pinned: state transitions, checkbox math, fix
counting, discard guard, verdict-to-card mapping.
### Write scope
- `web/src/lib/components/importPopup/popupState.test.ts` (new)
- `web/src/lib/components/importPopup/checkMapping.test.ts` (new)
### Must not change
- any non-test source.
### Changes
- `popupState.test.ts`: `continueToCheck` sets checking then report; `resolveCard` reduces `fixesNeeded`; `leaveOut` removes files from `targetCount`; `addAllServers` resolves all open cards; `canImport` false until ack and zero fixes; `requestClose` sets discard only when files listed and step!==3; `groupAll`/`setSelected` keep indeterminate correct; retry opens on step 2 with the card open.
- `checkMapping.test.ts`: map a `CheckReport` to cards — one `needs-computer` card per distinct server name, one `no-computer` card, `needs-zone` true only for `sql-errorlog`/`win-events`, `not-supported` files collected, ready files grouped by importer.
### Proof
`cd web; npm test -- importPopup`
### Depends on
P4, P12

## P15 Update existing Playwright e2e specs
Risk: medium
### Outcome
Every spec that pins the old drawer copy or behaviour is updated or replaced
for the popup and the new wording.
### Write scope
- `tools/e2e/specs/import-drawer.spec.ts` (rewrite -> popup flow)
- `tools/e2e/specs/import-in-case.spec.ts` (update open path + headings)
- `tools/e2e/specs/import-grid.spec.ts` (update)
- `tools/e2e/specs/imports.spec.ts` (update copy assertions)
- `tools/e2e/specs/wizard-import-files.spec.ts` (update; wizard file panel stays, but shared copy changes)
- `tools/e2e/specs/wizard-unlisted-host.spec.ts` (update wording to "needs a computer")
- `tools/e2e/specs/log4net-import.spec.ts` (update)
- `tools/e2e/specs/partial-notices.spec.ts` (update ack text)
- `tools/e2e/specs/case-drawers.spec.ts` (assert import is no longer a drawer)
### Must not change
- `tools/e2e/support/*` unless a helper name changes; `tools/e2e/fixtures/*` fixture files.
### Changes
- Replace `#import-title` / `Many files at once` / `Add a file` openers with the popup opener (`Import files` heading, `role=dialog`). Replace `unknown host` text assertions with `needs a computer`. Replace the per-row `Which computer wrote` combobox with the fix card's `Add {name} as a computer and accept {n} files` button. Replace the ack label with `I understand this is read-only and nothing is changed on these computers`. Keep `createCase`/`ensureHost`/`importFile` API helpers.
- Each rewritten spec keeps its original ID/title intent (e.g. many files at once, zip, leave-out, retry) but through the popup.
### Proof
`cd tools/e2e; npx playwright test specs/import-drawer.spec.ts specs/import-in-case.spec.ts`
### Depends on
P13

## P16 New Playwright e2e specs
Risk: medium
### Outcome
New e2e cases pin each new behaviour, including the investigation timeline
after an import.
### Write scope
- `tools/e2e/specs/import-popup-shell.spec.ts` (new)
- `tools/e2e/specs/import-popup-fix-cards.spec.ts` (new)
- `tools/e2e/specs/import-popup-unsupported.spec.ts` (new)
- `tools/e2e/specs/import-popup-discards.spec.ts` (new)
- `tools/e2e/specs/import-popup-retry.spec.ts` (new)
- `tools/e2e/specs/import-popup-timeline.spec.ts` (new)
### Must not change
- existing specs (P15 owns those), support helpers.
### Changes
New spec cases (ID — title):
- IP-01 — Choose step lists files grouped by kind and Continue starts the check (`import-popup-shell.spec.ts`)
- IP-02 — Check step shows ready groups and per-file checkboxes; unselecting drops a file from the Import count
- IP-03 — Esc with files listed asks `Discard N files?`; Keep them returns; Discard closes (`import-popup-discards.spec.ts`)
- IP-04 — Outside click and focus trap: Tab cycles inside the popup; Shift+Tab wraps (`import-popup-discards.spec.ts`)
- IP-05 — Phone width (viewport 390x844): popup is full screen, no scrim radius (`import-popup-shell.spec.ts`)
- IP-06 — Fix card for a named unknown server: `Add {name} as a computer and accept {n} files` adds the computer and unlocks Import (`import-popup-fix-cards.spec.ts`)
- IP-07 — `Add all N servers` resolves every open fix card (`import-popup-fix-cards.spec.ts`)
- IP-08 — `Leave these N files out` then `Bring them back` (`import-popup-fix-cards.spec.ts`)
- IP-09 — No-computer card suggests a name from the folder and `Use {name} and accept {n} files` works (`import-popup-fix-cards.spec.ts`)
- IP-10 — Inline time zone appears only for SQL error log / Windows event log; IIS never asks (`import-popup-fix-cards.spec.ts`)
- IP-11 — Not-supported file shows `This file is not supported`, Remove drops it, `Tell us what this is` picks a kind (`import-popup-unsupported.spec.ts`)
- IP-12 — Importing step shows per-group progress; Cancel stops after the current file (`import-popup-shell.spec.ts`)
- IP-13 — Result step shows `N files imported`, Imported / Not imported, and `Added N computers: …` (`import-popup-shell.spec.ts`)
- IP-14 — Retry after a partial failure reopens the popup on the failing file's fix card (`import-popup-retry.spec.ts`)
- IP-15 — After import, `Open timeline` shows the case Investigation Timeline with the imported source lines present (`import-popup-timeline.spec.ts`)
- IP-16 — 2,000-file folder: Choose lists without lockup, Check shows a spinner then verdicts, Import completes (`import-popup-shell.spec.ts`)
### Proof
`cd tools/e2e; npx playwright test specs/import-popup-shell.spec.ts specs/import-popup-fix-cards.spec.ts specs/import-popup-unsupported.spec.ts specs/import-popup-discards.spec.ts specs/import-popup-retry.spec.ts specs/import-popup-timeline.spec.ts`
### Depends on
P13

## Wave plan

| Wave | Packets | Can run in parallel | Lane |
|------|---------|---------------------|------|
| 1 | P1, P2, P3 | yes | P1 strong (Go); P2, P3 cheap |
| 2 | P4, P5, P6, P7 | yes | P4, P6 strong; P5, P7 cheap |
| 3 | P8, P9, P10, P11 | yes | P9 strong; rest cheap |
| 4 | P12 | n/a (single) | strong |
| 5 | P13, P14 | yes | P13 strong; P14 cheap |
| 6 | P15, P16 | yes | both strong (Playwright) |

## Risks

- The check endpoint reads every file twice (check then commit). For a
  2,000-file folder this is real cost; mitigate by making the check a single
  background job and showing the spinner, and by committing only selected files.
- `resolveHost` refuses a second distinct unlisted name under one picked host
  (`iis/common.go:130`). The check must report each observed name as its own
  card, not merge them.
- The existing import job DTO gains a `kind` field; any external caller that
  hard-parses `result` as an `importAnswer` will break. The frontend parser is
  the only such caller and is updated in P2.
- Rewording `REASON_TEXT['unknown-host']` (P3) touches every screen that shows
  it, not just the popup; P15 updates those specs.
- The popup reuses `wizardStaging`/`fileKinds`; changes there ripple into the
  create wizard. P8/P12 must not mutate shared staging state for the case popup
  (use `createStaging()` as `ImportBatch` already does).
- `openCaseImport` is called from five components; the popup must work from each
  (case home, tracing, landing) without a case-title lookup race.
- Esc during import must not abort the server job (only the polite stop signal);
  the discard confirm is suppressed on step 3.
- Focus trap + browser Back history: the popup is not a drawer, so it does not
  push a history entry; Esc/outside-click are the only close paths.

## Review focus

The five inputs most likely to break a user:

1. A 2,000-file folder dropped at once (Choose perf, Check job size, Import
   queue length).
2. The same server name appearing in two folders (two fix cards or one? must
   be one card per distinct name).
3. One file named by two servers (IIS with two unlisted names) — `resolveHost`
   refuses; the check must surface both names, not silently merge.
4. Esc pressed during import (must not kill the server job; must not show the
   discard confirm).
5. Retry after a partial failure (one file failed only for a missing computer)
   — reopening must land on that file's fix card, pre-filled, focused.
