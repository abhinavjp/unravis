# Context: blackbox-wizard-popup

Work item: move the Blackbox "Place" wizard out of the drawer into a native `<dialog>` pop-up with small, ADHD-friendly steps.
Owner pick: wireframe variant A (rail on top), 2026-11-16. B and C are out of scope.
Wireframe (untracked, throwaway): `web/prototype/blackbox-wizard-popup.html`.
Backlog items: BL-130, BL-132, BL-133 (front half only), BL-135. BL-131 (Remove/Status) and BL-134 are not in this plan.

Planning note: the `skill-engineer:forge-plan` skill could not be invoked (disable-model-invocation). This tree follows the repo's own `.forge` conventions (`.forge/cold-start-wizard/`, `.forge/pr12-finish/`). No forge-spec was run.

## Goal

The owner places a Blackbox on this PC, or builds a pack for another server, through a pop-up that:
- asks one small thing per step: Name, Where, Record, (SQL), Watch, Keep, Folder, Review;
- shows a connected progress rail and honest progress;
- saves nothing until the last step;
- sends exactly the same `Settings` payload the current drawer wizard sends to `validate`, `install` and `pack`;
- works on the real data path: real `this-pc` scan, real targets list, real install, real pack download.

## Evidence (verified in code, 2026-11-16)

### Current wizard (to be replaced)
- `web/src/components/BlackboxWizard.svelte` (257 lines): 4 parts (Name, Where, Settings, Review). It is mounted inside the Blackbox `Drawer` in `web/src/components/BlackboxPanel.svelte:215-216` as `view.kind === 'wizard'`, opened by the buttons at lines 287 and 331.
- Props: `thisPc`, `canPack`, `onLeave`, `onPlaced`. Phases: idle / working / installed / pack / failed.
- `canInstallHere = thisPc.available && !thisPc.installed`.
- `toReview` = `settingsProblem` then `blackboxApi.validate`. `confirm` = `blackboxApi.install`, or `blackboxApi.pack` + `saveBlob`.
- Escape goes through `wizardEscape(part, busy, finished)` in `web/src/lib/blackbox.ts`.
- Part 3 mounts `web/src/components/BlackboxSettings.svelte` (324 lines). That component is ALSO used by "Apply settings on this PC". It holds the interval select, the 3 record checkboxes, the target picker (fetch, filters, chips, manual add, UX-7 focus on chip removal, aria-live), keep/size/free, folder, port, bind and the logPaths textarea.

### Client contract (`web/src/lib/blackbox.ts`, 528 lines)
- `Settings { name, folder, intervalSeconds, keepDays, maxGB, minFreePercent, port, bind, record: { processes, logsCatalogue, copyLogsOnSnapshot }, logPaths: string[], probe: { enabled }, targets: TargetRef[] }`.
- `defaultSettings()`: folder `D:\UnravisBlackbox`, 15 s, 7 days, 2 GB, 10 %, port 7788, bind 127.0.0.1, record true/true/false, logPaths `['C:\inetpub\logs\LogFiles']`, probe off, targets `[]`.
- Rules: `NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*$/`, `NAME_MAX = 63`, `INTERVALS = [5, 15, 30, 60]`, `TARGETS_MAX = 32`, `TARGET_NAME_MAX = 128` (UTF-8 bytes), `targetsProblem`, `validBind`, `unsafeFolder`, `listensOnAll`, `fieldProblemNow`.
- `settingsProblem` requires the folder to match `/^[A-Za-z]:[\\/]/`, so it REJECTS UNC paths today.
- `ThisPc` has `available, scanned, installed, running, name?, folder?, port?, bind?, configUnreadable?, listed, listId?, problem?, settings?`. It has NO drive list.
- `TargetsAnswer { appPools, services, detected, appPoolsState?, servicesState? }`.

### Server contract
- `internal/blackbox/config.go`: `Config` matches `Settings` plus `Probe.{ServerCertificate, Certificate, HostNameInCertificate}`. `DisallowUnknownFields` is on. There is NO field for SQL counters, per-group or per-figure switches.
- `Validate`: name regex, length ≤ 63; folder `filepath.IsAbs` (UNC is absolute on Windows) plus `unsafeFolder` (refuses a share root); interval 5/15/30/60; keep 1-365; maxGB 0.1-10000; minFree 1-50; port; bind; logPaths ≤ 20 and absolute; target rules; `Probe.Enabled` must be false ("the SQL probe is not part of this release").
- `ChooseFolder`: D: if it exists, else C:.
- Routes (`internal/server/blackbox.go:149-168`): `/api/blackbox`, `/test`, `/validate`, `/pack`, `GET /this-pc`, `GET /this-pc/targets`, `POST /this-pc/install|start|stop|apply|remove|snapshot`, `/{id}/check|key|reveal-key|host-reading`, `DELETE /{id}`. There is NO drives route.
- Drive helpers that exist: `internal/server/archive_path_windows.go` (`driveIsRemote` via GetDriveType, `archiveFreeBytes` via GetDiskFreeSpaceEx), `internal/blackbox/diskspace_windows.go`, `internal/discover/winenv_windows.go:74` (DRIVE_FIXED check).

### What is recorded (catalogue source)
- `internal/blackbox/groups_types.go`: HTTP queues, IIS workers, disk/CPU split, SQL host (13 counters), CLR, Proc10, ProcIO, events, TCP, ASP.NET, memory pressure, VM (type only; collector BL-113 not built), dumps.
- `internal/blackbox/sqlhost.go`: the 13 SQL host counters, host PDH only, no SQL login, default and named instances sampled machine-wide; registered `RegisterGroup("sqlHost", true, ...)` (optional, automatic). Its own comment says the counter names are "UNVERIFIED pending live check".
- `internal/bbsql/`: the deeper checks (BL-101/102); switched off by `Probe.Enabled == false`.

### Reusable UI found
- `web/src/lib/components/importPopup/` (ImportPopup.svelte, popupState.svelte.ts, step components): a pop-up with scrim, `role="dialog"`, focus trap, a stepper that only allows going back, and a state class. It is a `div`, not a native `<dialog>`. Pattern to borrow, not a component to reuse as-is.
- No native `<dialog>`, segmented control or radio-card component exists in `web/src`.
- `Hint.svelte`, `SourcePopover.svelte`, `WindowPicker.svelte` hold popover/focus patterns.

### Tests and build
- `web/package.json`: `npm test` = `node --test tests/*.test.cjs`; `npm run check` = `svelte-check --tsconfig ./tsconfig.json`; `npm run build` = `vite build && vite build -c vite.setup.config.ts`.
- Web tests load TS through `tests/helpers/loadTs.cjs` (`loadTypeScript('src/lib/...')`), so logic must live in `.ts` modules to be unit-testable. Copy is linted through `src/lib/copy/copylint.ts` (`lintScreenStrings`).
- Go: `go test ./...`.
- `web/dist` is committed and embedded in the exe. It is rebuilt locally; CI does not check it (BL-031).

### Branch state
- Current branch `fix/setup-place-blackbox-beside-app`, with commit 702e6c2f not on master. `master` is protected.
- The working tree already has modified `web/dist` assets and `docs/BACKLOG.md`, plus untracked `web/prototype/` and `.forge/pr12-finish/`.
