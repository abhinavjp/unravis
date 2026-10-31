# Unravis: what works today (the real app)

Date: 2026-10-30. Source: repository at commit 438e752, read by hub models, then fact-checked by a second model family. Part 1 to 6 are the area documents as written; the Errata section at the end lists every correction the reviewers found. Where an errata row contradicts a part, the errata row wins.

Status: not yet verified by running the app; statements come from code reading.


---

## Part 1: Setup, installer, keeper, master key, data folder


### What this area is

Unravis is a local Windows utility that runs an HTTP service bound only to `127.0.0.1` and embeds its dashboard in the executable (`README.md`; `cmd/audit-explorer/main.go:269`). It is installed by a one-file installer, `unravis-setup.exe`, which shows a web page on a loopback port and then either runs in place or hands the work to an elevated worker (`cmd/unravis-setup/main.go`; `internal/setup/uiserver.go`). On a shared (machine) install the installer also creates a Windows service, the **Unravis Key Keeper**, which holds saved SQL/Mongo passwords and Blackbox read keys sealed for its service account and never returns a secret (`internal/keeper/proto.go`; `internal/setup/plan.go:50`). Cases can optionally be protected by a **master key** that the user types once per launch (`internal/server/masterkey.go`; `web/src/components/MasterKeySetup.svelte`). Files live either in a shared `C:\ProgramData\Unravis` layout or a per-user `%APPDATA%\APIAuditExplorer` layout (`internal/datadir/datadir.go:6-11`).

### Flows

| Flow | How the user does it | What the app does (routes/commands) | Status | Evidence |
| --- | --- | --- | --- | --- |
| Build the installer | Developer runs `.\build.ps1` | `npm ci`/check/build, `go test ./...`, `go vet ./...`, builds `dist\unravis.exe`, `unravis-keeper.exe`, `unravis-blackbox.exe`; copies them into `cmd\unravis-setup\payload` with a SHA-256 `manifest.json`; builds `unravis-setup.exe`; then removes the payload copies | Working | `build.ps1` (`-SkipWeb` skips web; payload build block) |
| Start setup (window) | Double-click `unravis-setup.exe` | Window opens a web page on a loopback port with a one-time key in the URL fragment, sent back as `X-Setup-Key`; Host/Origin are checked | Working | `cmd/unravis-setup/main.go`; `internal/setup/uiserver.go` (`Web`, key in fragment, Host/Origin checks) |
| Install as a user | Choose **Just for me (no admin needed)**, pick a Folder, optionally **Start Unravis when I sign in** | No elevation; writes program files, a Start Menu/desktop shortcut, and an Installed apps entry under the current profile | Working (Go test) | `internal/setup/options.go` (Scope user default); `internal/setup/setup_test.go:375` `TestUserInstallNeedsNoApproval`; `internal/setup/proto_test.go` `TestPerformTurnsASecondInstallIntoARepair` |
| Install for everyone | Choose **For everyone on this PC (admin needed)**, continue | Shows **Windows will ask for admin approval** with **Will change** / **Will not change** lists, then the UAC prompt; installs program files, creates the keeper service and its ACLs, writes the HKLM record datadir trusts | Working (Go test) | `web/setup/App.svelte` (`approval` screen, `changes?.willChange`, `changes?.willNotChange`); `internal/setup/options.go` (Scope machine default, A44 shared data); `internal/setup/setup_test.go:222` `TestMachineInstallWritesTheRecordTheReaderTrusts` |
| Upgrade | Run setup again on an existing install; press **Upgrade** | Keeps existing cases, sealed files and the keeper account; refuses a downgrade; refuses a keeper owned by another account | Working (Go test) | `web/setup/App.svelte` (`welcomeNext`, `op === 'upgrade'`); `internal/setup/setup_test.go:579` `TestUpgradeKeepsCasesSealedFilesAndKeeperAccount`, `:642` `TestUpgradeRefusesAKeeperWithAnotherAccount`; `internal/setup/fix_test.go:67` `TestGitDescribeDowngradeIsRefused` |
| Repair | Run setup again; press **Repair** | Puts back missing pieces without wiping data | Working (Go test) | `internal/setup/setup_test.go:659` `TestRepairPutsBackMissingPieces` |
| Uninstall | On the Remove screen, press **Remove**; optionally tick to delete cases/recordings and type `DELETE` or the Blackbox name | Always removes program, shortcuts and Installed apps entry; on machine scope the keeper service; a Blackbox service and firewall rule if present. Data stays unless explicitly ticked; deletion is permanent (no Recycle Bin) | Working (Go test) | `web/setup/App.svelte` (uninstall screen, `CONFIRM_DELETE_WORD`, `FOREVER`); `internal/setup/uninstall.go`; `internal/setup/setup_test.go:691` `TestUninstallKeepsDataByDefault`, `:742` `TestUninstallDeletesOnlyWithTheTypedConfirmation` |
| Silent install/uninstall | Run setup with a `setup.json` | Config is strict-parsed (max 64 KiB); `deleteCases`/`deleteRecordings` delete only when explicitly `true`; a bad config exits 2 and changes nothing | Working (Go test) | `internal/setup/silent.go`; `internal/setup/silent_test.go`; `cmd/unravis-setup/main_test.go:183`, `:350` |
| Cancel or crash mid-run | Press **Cancel**, or close the window, or the process dies | Every step is journalled; a cancelled or crashed run is rolled back; a second run waits for the first; a stale finished journal does not block the next run | Working (Go test) | `internal/setup/runner.go` (`StepUndone`, `StepLeft`); `internal/setup/setup_test.go:840` `TestCancelMidCopyPutsEverythingBack`, `:998` `TestCrashMidRunIsPutBackNextTime`, `:790` `TestDoubleRunWaitsForTheFirst` |
| Close a running app during setup | Tick **Close Unravis for me** when it is running | Stops this person's Unravis, never another person's | Working (Go test) | `web/setup/App.svelte` (`closeCheckbox`); `internal/setup/setup_test.go:898` `TestRunningAppStopsUnlessThePersonSaysClose`; `internal/setup/fixes_b_test.go:225` `TestAnotherPersonsUnravisIsNeverClosed` |
| See where files go | Read the Done screen / log link | Done screen shows version, install folder and Blackbox result; the log path is shown with **Open log folder** | Working | `web/setup/App.svelte` (`screen === 'done'`, `{#snippet logLine()}`) |
| Launch the app | Double-click `Start Live.cmd`, or `dist\unravis.exe`, or `run.ps1` | Binds `127.0.0.1` on a free port (`--port 0`) or a fixed `--port`; opens the browser on a one-time `/launch?c=<code>` link that redirects with the token in the URL fragment | Working | `Start Live.cmd` (`dist\unravis.exe --port 0`); `run.ps1`; `cmd/audit-explorer/main.go:269`; `internal/server/launch.go` (`launchPath`, `WithLaunchCode`, `launchGonePage`) |
| Keeper service role | Machine install; then select a saved connection / Blackbox read | Keeper runs as `NT SERVICE\UnravisKeeper`, seals secrets for its own account, identifies callers by Windows token, never returns a secret | Working (Go test) | `internal/keeper/proto.go`; `internal/setup/plan.go:46-50`; `internal/keeper/keeper_test.go:805` `TestNoOperationGivesOutASecret`; `cmd/unravis-keeper/main_test.go:98` `TestServeKeepsSecretsSealedInItsOwnFolder` |
| Keeper console (dev) | `unravis-keeper console` | Runs the same server outside the service host for a developer session | Implemented, untested | `cmd/unravis-keeper/main.go` (`console` mode) |
| Keeper refuses a bad shared folder | Start the service without the installer's record | Refuses to start when the shared folder is not the trusted one | Working (Go test) | `cmd/unravis-keeper/main_test.go:52` `TestKeeperChecksTheSharedFolder`, `:133` `TestServeStopsWhenSealingIsMissing` |
| First-run master key setup | On the app's start screen, click **Protect your cases with a master key**, type the key twice, acknowledge, press **Set master key** | `POST /api/master-key/setup {passphrase, confirm, acknowledged:true}` writes `masterkey.dat` (Argon2id, 32-byte wrapping key held only in memory) and returns the unlocked status | Working (Go test + e2e) | `web/src/components/MasterKeySetup.svelte`; `internal/server/routes.go:169`; `internal/masterkey/masterkey.go` (`FileName`, `MinPassphraseLen 12`); `internal/masterkey/masterkey_test.go:30` `TestSetupUnlockRoundTrip`; `tools/e2e/specs/keys.spec.ts` (KEY-01, KEY-02) |
| Unlock after launch | Type the master key on **Unlock your cases** | `POST /api/master-key/unlock {passphrase}`; wrong key is 401 `wrong-key`; failures throttle after 5 (1 s doubling to 60 s); passphrase bytes are zeroed | Working (Go test + e2e) | `web/src/components/MasterKeyUnlock.svelte`; `internal/server/masterkey.go` (`throttleWait`, `checkPassphrase`); `internal/masterkey/masterkey_test.go:69` `TestWrongPassphrase`, `:49` `TestUnlockZeroesPassphrase`; `tools/e2e/specs/keys-ui.spec.ts` (KEY-06, KEY-06b) |
| Lock / re-lock | Press **Lock** in the header | `POST /api/master-key/lock`; locked routes answer 423 | Working (Go test + e2e) | `web/src/lib/copy/masterKeyCopy.ts` (`lock`); `internal/server/routes.go:171`; `internal/server/archive_free_test.go:585`; `tools/e2e/specs/keys-ui.spec.ts` (KEY-06) |
| Damaged-key recovery | App shows the damaged screen; press the offered recovery | Damaged `masterkey.dat` closes cases exactly like locked; `POST /api/master-key/reset-damaged {acknowledged:true}` reports the reset impact and lets a new key be set | Working (Go test) | `web/src/components/MasterKeyDamaged.svelte`; `internal/server/masterkey.go:1091` (`masterKeyResetDamaged`, `resetImpact`); `internal/server/case_key_test.go:428`; `internal/masterkey/masterkey_test.go:297` `TestDamagedFilesGiveDamaged`, `:319` `TestKeyFileAsDirectoryIsDamaged` |
| Re-wrap older cases | A notice appears; press **Continue** | `POST /api/master-key/rewrap {caseId}` or `{all:true}`; a partly re-wrapped case is reported by `needsRewrap` and finishes when unlocked | Working (Go test) | `web/src/components/RewrapNotice.svelte`; `internal/server/routes.go:172`; `internal/server/masterkey.go` (`needsRewrapText`, `NeedsRewrap`); `internal/server/case_key_test.go:322` |
| Update check | Press the update button in Help | `POST /api/update-check` (button-only, never automatic); 10 s timeout; statuses `latest`/`available`/`dev`/`unreachable`; always 200; GET refused; 5 s cache | Working (Go test + e2e) | `internal/server/update_check.go:21`, `:106`; `tools/e2e/specs/update.spec.ts` (UPD-01..06); `tools/e2e/specs/keeper-update.spec.ts` (UPD-05..07) |

### Screens or endpoints in this area (list, one line each)

- Setup web page screens: Welcome, **Where and how**, **Windows will ask for admin approval**, progress, Done, Failed, **Remove Unravis** — `web/setup/App.svelte`.
- Setup page API (loopback, one-time key): `state`, `preflight`, `run`, `progress`, `cancel`, `close`, `openApp`, `openLogFolder` — `web/setup/api.ts`.
- `/launch?c=<code>` one-time launch link, redirects with token in the URL fragment, else the gone page — `internal/server/launch.go`.
- `GET /api/master-key/status` — none/locked/unlocked, counts, reset impact while damaged — `internal/server/routes.go:168`.
- `POST /api/master-key/setup`, `/unlock`, `/lock`, `/rewrap`, `/reset-damaged` — `internal/server/routes.go:169-173`.
- `POST /api/update-check` (button-only, always 200) — `internal/server/update_check.go:106`.
- Keeper wire ops (namespaced): `ping/status`, `sql.*`, `mongo.*`, `bb.*`, `audit.*` — `internal/keeper/proto.go`.
- Data folder roots: shared `C:\ProgramData\Unravis` (cases in `<root>\cases`, keeper secrets in `<root>\keeper`) vs per-user `%APPDATA%\APIAuditExplorer` with `%LOCALAPPDATA%\APIAuditExplorer\cases` — `internal/datadir/datadir.go:6-11`, `:108-112`.
- Keeper pipe `\\.\pipe\UnravisKeeper`; env overrides `UNRAVIS_DATA_DIR`, `UNRAVIS_KEEPER_DIR`, `UNRAVIS_KEEPER_PIPE`, `AUDIT_CONFIG_DIR` — `internal/datadir/datadir.go:35-36`, `:118-147`.

### What a user gets out of it today (concrete outputs)

- A working local dashboard at `http://127.0.0.1:<port>`, launched from a one-time link with the token kept in the tab's `sessionStorage` (`README.md`; `internal/server/launch.go`), with `--version` and `/api/version` reporting the stamped version (`internal/version/version.go`; `build.ps1` ldflags).
- An installed app with a Start Menu/desktop shortcut and an Installed apps entry, removable through the same setup window (`internal/setup/uiserver_test.go:420` `TestInstallChangesNameTheShortcutAndTheInstalledAppsEntry`).
- On machine install, a **Unravis Key Keeper** service that stores passwords and read keys sealed for its own account and returns only answers, never secrets (`internal/keeper/proto.go`; `internal/keeper/keeper_test.go:805`).
- Optional master-key protection: cases stay closed while locked (423), unlock with the typed key, lock on demand, and finish a partly re-wrapped case (`internal/server/masterkey.go`; `tools/e2e/specs/keys-ui.spec.ts`).
- A recovery path from a damaged `masterkey.dat` that reports exactly what a reset would affect before it happens (`internal/server/masterkey.go:1091`).
- An on-demand update check that names both versions and links only to the project releases page, or says it could not check, or that a development build has nothing to compare (`internal/server/update_check.go`; `tools/e2e/specs/update.spec.ts`).

#### Developer-machine-only versus test-covered

- **Test-covered (Go unit/integration):** preflight rules, user/machine install, upgrade, repair, uninstall, silent config, rollback on cancel/crash, journal safety, folder/owner/link refusals, shortcut round trip, exit codes, setup page key/one-run/idle behaviour, keeper startup and sealing checks, master-key round trip/unlock/damage/throttle, sealed-file atomicity, datadir layouts and trust, update-check statuses (`internal/setup/*_test.go`, `internal/keeper/*_test.go`, `internal/masterkey/*_test.go`, `internal/datadir/*_test.go`, `cmd/unravis-setup/main_test.go`, `cmd/unravis-keeper/main_test.go`, `internal/version/version_test.go`).
- **Test-covered (e2e, demo build):** update check statuses, no automatic update call, launch-link reuse refusal, loopback-only listener, master-key unlock/lock/erase/no-key-leak, keeper previews statuses and removal refusals, and no key/password in the page, log or case files (`tools/e2e/specs/update.spec.ts`, `keeper-update.spec.ts`, `keeper.spec.ts`, `keys.spec.ts`, `keys-ui.spec.ts`, `security.spec.ts`, `sec.spec.ts`).
- **Manually listed as not automatable:** KEY-09 needs the real PC master key; KEY-06 break-it c and KEY-07 a/b need the real key keeper; KEY-08's "Erasing" text is too short to see (`tools/e2e/COVERAGE.md`).
- **Developer-machine-only (no automated test found):** the actual click-through of the setup window against a real UAC prompt and real `Program Files` on a real profile; the keeper `console` mode; the full first-run master-key ceremony in a real per-user DPAPI profile (the e2e suite runs the demo build with fixed fixtures and a new empty data folder per spec, `tools/e2e/COVERAGE.md` "How to run"). Signing the installer is a release step, not done by `build.ps1` (`build.ps1` comment; `MCP.md` not required here).

---

## Part 2: Cases, keys, ledger, erase, export/import


### What this area is

A case is the unit Unravis investigates. It is a folder on the PC, named by a 26-character base32 id (`internal/casestore/store.go`, `CaseID` pattern `^[a-z2-7]{26}$`), holding a sealed key file (`case.key`), a sealed manifest (`manifest.dat`) and an encrypted SQLite database (`case.db`). The web side calls that id the "case code" and shows its first 8 characters beside the case (`web/src/lib/cases.ts` `caseCode`). A case can be named, tagged, closed and reopened, given a key of its own, archived to a `.unravis-case` file, freed of its working copy, restored, exported and erased. Its own hypotheses live in a per-case ledger; its evidence imports, source tags and history are stored inside the case.

Everything below is the behavior of this repository's code and tests, not of `buildout/`.

### Flows

| Flow | How the user does it | What the app does | Status | Evidence |
|---|---|---|---|---|
| See the start screen | Open the app with a case store available | Shows the title "Start an investigation", reads the master-key status first, then lists cases on the "Use an existing case" card; a locked key hides titles and counts | Working | `web/src/components/StartScreen.svelte` (reads `getMasterKeyStatus` then `listCasesPage`); `web/src/lib/copy/startCopy.ts` `START_TEXT.title`, `intro`; e2e `case-ui.spec.ts` CASE-01 |
| Enter a case by code | "Enter an existing case" card → type a code → Open case | Cleans spaces/dashes/case and invisible characters, checks the 26-char shape, asks the server; any refusal reads one sentence | Working | `web/src/lib/caseCode.ts` `normaliseCode`, `resolveCode`; `web/src/lib/copy/enterCopy.ts` `CODE_NOT_FOUND`; e2e `case-ui.spec.ts` CASE-04 |
| Create a case (3-step wizard) | "Create a new case" → Name → Sources → Review → create | Runs a guided draft; nothing is written until the last step; the last step reads every chosen source and reports per step | Working | `web/src/components/CreateWizard.svelte`; `web/src/lib/wizardFlow.ts` (`STEPS`, `TITLE_MIN` 2, `TITLE_MAX` 80); `web/src/lib/wizardSubmit.ts`; e2e `case-wizard.spec.ts`, `case-ui.spec.ts` CASE-05 |
| Create idempotently | Same title within one wizard run | One create request id is kept per title in sessionStorage, so a reload/restart makes the same single case | Working | `web/src/lib/createRequest.ts` `requestIdFor`; server `POST /api/cases` returns 201 first, 200 for the same request id; e2e `case-api.spec.ts` CASE-07 |
| Name rules | Type a case name | 2–80 characters; spaces-only is empty; control characters refused; angle brackets kept as text | Working | `web/src/lib/wizardFlow.ts` `titleProblem`; server `ValidTitle` (`internal/casestore/store.go`); e2e `case-wizard.spec.ts` CASE-06…06c, `case-api.spec.ts` CASE-07b |
| Add a case key | Case home → "Change or remove the case key" → set a key, typed twice | Stores an optional per-case key (Argon2id KEK wrapping a random case master), state `active`; a `case.wrap` file beside the case | Working | `internal/casekey/casekey.go` `New`, `Seal`, `Wrap`; `internal/server/case_key.go`; `web/src/components/CaseKeyPage.svelte` (screens 125/126); e2e `keys.spec.ts` KEY-01 |
| Unlock a keyed case | Start screen or case page → type the case key → Open case | A locked case lists with title "(locked)" and hides its title until opened with the right key; wrong key 401 `wrong-case-key` | Working | `internal/server/case_key.go`; `internal/casestore/store.go:136`; `web/src/components/CaseKeyCard.svelte`; e2e `keys.spec.ts` KEY-02/03, `keys-ui.spec.ts` KEY-06 |
| Key retry limit | Enter the wrong key repeatedly | Five wrong tries, then 429 `throttled` with a wait that covers even the right key | Working | `internal/server/case_key.go` `caseThrottles`; e2e `keys.spec.ts` KEY-04 |
| Change / remove a case key | Case key page → change or remove option | Change rewrites only the small wrap file; remove clears the case's own key | Working | `internal/casekey/casekey.go`; `web/src/lib/copy/caseKeyCopy.ts` `changeOption`, `removeOption`; e2e `keys.spec.ts` KEY-05 |
| Close a case | Case page → close | Case becomes read-only; state `closed`; header `X-Expect-State` guards against a stale page | Working | `internal/server/case_lifecycle.go`; `web/src/lib/cases.ts` `closeCase`, `believedState`; e2e `case-ui.spec.ts` CASE-08 |
| Reopen a case | Case page → "Reopen this case" | Closed → Open only; a second tab with a stale belief gets 409 `case-changed` | Working | `internal/server/case_lifecycle.go` `caseChangedText`; e2e `case-api.spec.ts` CASE-10a/10b, `case-ui.spec.ts` CASE-09 |
| Read the case history | Open a case | Shows `created`, `closed`, `reopened`, `archived`, `restored`, `freed` lines with a short detail | Working | `internal/casestore/archive.go` `History*`; `web/src/lib/cases.ts` `HistoryEntry`; e2e `case-ui.spec.ts` CASE-03 |
| Keep a "needs another try" list | Open a case that part-failed | Lists failed imports ("Import failed") and audits not added ("Not added to analysis"); each can be retried or removed | Working | `internal/server/import_retry.go`; `internal/server/cases.go`; e2e `case-retries.spec.ts` CASE-11…11e |
| Tag environments | Wizard Sources / case tags | Case holds at most 32 environments, split on commas, duplicates dropped with a note | Working | `web/src/lib/caseTagsApi.ts` `putEnvironments`; e2e `case-wizard.spec.ts` CASE-06g/06h |
| Tag a source | Wizard or Connections | Adds a source tag (kind, label, env, portal, api, classification); Mongo needs a recent successful connection test | Working | `web/src/lib/caseTagsApi.ts` `postSourceTag`; server `internal/server/case_sources.go`; e2e `case-wizard.spec.ts` |
| Keep a case ledger | "Ledger" drawer → add/update hypotheses | Per-case hypotheses with states `open`, `supported_by_correlation`, `supported_by_strong_correlation`, `contradicted`, `not_testable`; up to 200, 2000 chars, 8 MiB total | Working | `internal/server/case_ledger.go`; `internal/ledger/ledger.go`, `types.go`; `web/src/components/LedgerPanel.svelte`, `LedgerCount.svelte` |
| Cite evidence in the ledger | Patterns drawer → "Cite in ledger" | Adds the reference to the ledger store, else copies it to the clipboard | Working | `web/src/lib/citeInLedger.ts` `citeInLedger` |
| Compare theories | Ledger drawer → "Compare theories" | A matrix of saved theories and their support/counterexamples/settling note; nothing is scored | Working | `web/src/lib/theories.ts` `theoryRows`; `web/src/components/LedgerPanel.svelte` |
| Export a case file | Case page → Archive drawer → folder → archive | Writes one `.unravis-case` sealed under the master key or the case's own key; case becomes `archived` | Working | `internal/casestore/casefile.go` `WriteCaseFile`; `internal/server/archive_routes.go`; e2e `case-api.spec.ts` CASE-13/15, `keys-ui.spec.ts` KEY-07 |
| Verify a case file | Archived case → verify with the passphrase | Re-opens the header and checks the database hash; a wrong passphrase is refused | Working | `internal/casestore/casefile.go` `OpenCaseFile`; `internal/server/archive_verify.go`; e2e `case-api.spec.ts` CASE-17 |
| Restore in place | Archived case → "Restore in place" | Turns a kept Archived case into a Closed one; ends Closed; working copy untouched | Working | `internal/server/archive_restore.go`; `web/src/lib/archiveApi.ts` `restoreInPlace`; e2e `case-api.spec.ts` CASE-20 |
| Restore as new | Archived case → "Restore from the case file as a new case" | Makes a NEW case with a new id from the file; never merges or overwrites; ends Closed | Working | `internal/casestore/casefile.go` `RestoreCaseFileFull`; e2e `case-api.spec.ts` CASE-18a |
| Restore from a case file (start screen) | "Enter an existing case" → "Restore from a case file" → path + key | Reads a full `.unravis-case` path, opens with the exporting PC's master key, makes a new case; needs a master key here | Working | `web/src/lib/restoreCase.ts` `checkRestoreInput`; `web/src/lib/caseFileApi.ts` `restoreCaseFile`; e2e `case-start.spec.ts` CASE-24/24b |
| Save a case to a file (case route) | `POST /api/cases/{id}/export` with a path + passphrase | Writes a `.unravis-case` sealed with this PC's master key | Working | `internal/server/case_file.go`; `web/src/lib/caseFileApi.ts` `exportCaseFile` |
| Free the working copy | Archived case → "Free the space" → type the case code | Removes the working copy (key first), keeps the sealed record; needs the master key | Working | `internal/casestore/archive.go` `BeginFree`/`FinishFree`; `web/src/lib/archiveApi.ts` `freeCase`; e2e `case-api.spec.ts` CASE-18a, `case-drawers.spec.ts` CASE-16 |
| Erase a case | Case page → "Erase this case" → type the short code | Overwrites the key file, deletes the folder, leaves an "Erased case xxxxxxxx" line (id and time only); needs the typed code | Working | `internal/casestore/keyfile.go` `EraseKeyFile`; `internal/server/erased_log.go`; `web/src/lib/erase.ts`; e2e `case-api.spec.ts` CASE-22, `case-drawers.spec.ts` CASE-21a, `keys-ui.spec.ts` KEY-08 |
| Clear an erased line | Start screen → "Clear xxxxxxxx" | Removes only that line; the case is already gone | Working | `internal/server/erased_log.go`; `web/src/lib/cases.ts` `clearErasedLine`; e2e `case-drawers.spec.ts` CASE-21a |
| Browse archived cases | Start screen → "Archived cases (n)" | A separate list, 50 per page, with kept/freed chips and actions | Working | `web/src/lib/archiveApi.ts` `listArchivedCases`; `web/src/lib/archiveModel.ts` `archivedActions`, `archivedChips`; e2e `case-drawers.spec.ts` CASE-16 |
| Import files into a case | Case import drawer → choose importer/file → ack → import | Previews in a rolled-back transaction, then commits; big files run as a background job with progress | Working | `internal/server/import_jobs.go`, `importers.go`; `web/src/lib/cases.ts` `previewImport`, `commitImport`, `startCommitJob`; e2e `imports.spec.ts` IMP-01…04 |
| Export evidence from analysis | Analysis → "Export" tab/drawer → options → Preview → Save | Builds a redacted preview, then a JSON file plus an events CSV, each with a SHA-256; nothing saved until Save | Working | `internal/server/export.go`; `web/src/lib/export.ts`; `web/src/components/ExportPanel.svelte`; e2e `exports.spec.ts` EXP-03…09, `export-ui.spec.ts` |

### Screens or endpoints in this area

**Screens (named by their visible labels).** "Start an investigation" has three cards: "Use an existing case", "Enter an existing case" (a "Case code" field plus "Restore from a case file"), and "Create a new one" (`web/src/lib/copy/startCopy.ts`). The create wizard shows steps "Name", "Sources", "Review" (`web/src/lib/wizardFlow.ts` `STEPS`; e2e `case-wizard.spec.ts`). A case has a home area with its story, a "Needs another try" section, "Reopen this case", and archive/erase drawers; case keys show as "This case has its own key", "Key ending xxxx", "Lock this case", and "Change or remove the case key" (`web/src/lib/copy/caseKeyCopy.ts`; `web/src/components/CaseKeyPage.svelte`). The "Ledger" drawer holds the hypotheses and the "Compare theories" matrix. The "Export evidence" drawer has "Preview export" then "Save file" (`web/src/lib/export.ts` `EXPORT_TEXT`).

**HTTP routes** (`internal/server/routes.go`):

- `GET/POST /api/cases`, `GET/DELETE /api/cases/{id}` (list/create/read/erase)
- `POST /api/cases/{id}/close`, `/reopen`, `PUT/DELETE /api/cases/{id}/retries[/{retryId}[/retry]]`
- `GET /api/cases/erased/{id}` clear line
- `GET/POST /api/cases/{id}/key`, `/key/unlock`, `/key/lock`, `/key/set`, `/key/change`, `/key/remove`
- `GET/POST/PUT/DELETE /api/cases/{id}/ledger...` including `/copy-shared` and `/{hid}/reuse`
- `GET/POST /api/cases/{id}/imports`, `GET/DELETE /api/cases/{id}/imports/jobs/{job}`
- `GET/PUT /api/cases/{id}/environments`, `GET/POST/DELETE /api/cases/{id}/sources...`
- `GET /api/cases/{id}/resource`, `/series`, `/collect`, `POST /api/cases/{id}/timeline`, `/report`
- `POST /api/cases/{id}/archive` (`GET`, `DELETE`), `/verify`, `/free`, `/restore-in-place`, `/restore-as-new`
- `POST /api/cases/restore`, `POST /api/cases/{id}/export`
- `GET /api/cases/orphan-files`, `POST /api/export/preview`, `POST /api/export/confirm`

### What a user gets out of it today

A person can start the app, see their cases, enter one by its 26-character code (spaces, dashes and case are ignored), or type a name and walk a three-step wizard to make a new one. They can give a case its own key so it opens only with that key and lists as "(locked)" until then, change or remove that key, and lock it again. They can close a case to make it read-only and reopen it later; a stale second tab is told the case changed.

Inside a case they can read the story of what happened to it, see the items that "need another try" and either retry or drop them, name environments and tag sources, and keep a ledger of hypotheses with evidence citations and a comparison matrix. A case with a failed import keeps that fact until it is dealt with.

They can move a case between computers: archive it to a single encrypted `.unravis-case` file, verify that file, free the working copy to reclaim disk space while keeping the sealed record, and later restore it in place (ends closed) or as a brand-new case. They can also export a case to a file directly or restore one from the start screen by typing its path and key. When a case is no longer needed they erase it by typing its short code, leaving only an "Erased case" line that itself can be cleared.

From the analysis side they can export the findings for the window they are looking at: a preview they inspect first, then a saved JSON file and an events CSV, each verified by SHA-256 before it is written to disk. Secrets, payloads and host names are kept out of those files by design (e2e `exports.spec.ts` EXP-09a).

What is not there today: entering a case code is present, but the "Enter an existing case" card's create-with-no-store path answers `cases-unavailable` (503) when a case store is absent, and several deep analysis routes (their own area) sit behind these case routes. Features the README advertises but that this code does not implement are not described here.

---

## Part 3: Sources, discovery, collection, Blackbox


### What this area is

Unravis gets data into a case three ways: by importing files the owner already has, by scanning the PC Unravis runs on for logs it can read directly, and by reading audit data from a live MongoDB or SQL Server the owner connects to. A small companion service, the Blackbox (`unravis-blackbox.exe`), can be installed on a Windows server to record host counters and process samples over time, which Unravis then reads back either from a folder/share or over an HTTP status port with a read key. Discovery is a Windows-only, user-triggered scan that reads file names, sizes and a few configuration files on the local machine; it never opens a network connection. Coverage and "what to collect next" tell the owner what a case holds and what log would fill a gap.

### Flows

| Flow | How the user does it | What the app does (routes/commands) | Status | Evidence |
|---|---|---|---|---|
| Add a data source to a case | In the case's sources area the user picks Add data (Screen 112) and chooses a card: SQL, Mongo, Blackbox, files, or a day already read | Screen `AddDataPanel.svelte` counts what exists (saved SQL/Mongo connections, days, Blackbox tags) and hands the choice to the page that finishes it; it reads nothing and changes nothing itself | Working | `web/src/components/sources/AddDataPanel.svelte:13`; e2e demo drives this hub |
| Connect to a MongoDB audit source | The user enters connection details and tests; the app lists databases; the user classifies each as audit / other / unused | `POST /api/cases/{id}/sources` creates a source tag (`kind` `mongo-database`); a database label is accepted only if a recent successful test returned that name (30-min TTL, 8 kept) | Working | `internal/server/case_sources.go:22`, `postMongoDatabase`, `mongoTests`, `tagNotTestedText`; `internal/server/routes.go` |
| Save an SQL Server connection | The user fills the SQL form and saves; saved connections appear in a list | `GET`/`POST /api/sqlconnections`; password sealed with DPAPI; at most 20 saved; first test must succeed else the password is asked again (`ErrPasswordAgain`) | Working | `internal/server/sqlconn.go`; `internal/sqlconn/sqlconn.go` (`Input`, `MaxRecords 20`) |
| Test a saved connection | On the Connections page the user presses Test on a row | SQL test uses the saved password server-side (`savedId`), never typing or holding it in the browser; the result is "Works"/"Failed"/"Not tested" held only in memory | Working | `web/src/components/sources/ConnectionsPage.svelte:test`; `ConnectionWord`, `rememberTest` |
| Remove a source tag | The user presses the × next to a source and confirms | `DELETE /api/cases/{id}/sources/{sourceId}`; the answer says the tag was removed but imported events were not deleted | Working | `internal/server/case_sources.go:deleteSourceTag`, `tagRemovedText` |
| Read a day from a live lane | In Step 7 (SQL) or Step 8 (Mongo) the user presses a read button | Lane runners call the lane endpoints; results are stored as a day ("read at", state sticker) and listed newest first | Working | `web/src/components/sources/SourceStep.svelte` (`LaneRunner`, `fetchLaneDays`); `internal/server/lanes.go`, `demo_lanes.go` |
| Scan this computer for logs | In the "This computer" panel (or the wizard's sources step) the user presses Scan | `POST /api/discover/scans` starts a background scan; `GET /api/discover/scans/{scan}` returns findings and gaps; progress and cancel are supported | Working | `internal/server/discover.go:475`, `:664`, `:676`; `web/src/components/DiscoverPanel.svelte:startScan` |
| Import what the scan found | The user ticks findings, picks a time window, acknowledges the notice, presses Import | `POST /api/cases/{id}/discover/imports` with opaque finding ids and a window (max 31 days, max 64 findings); one background job per finding | Working | `internal/server/discover_import.go:85`, constants `maxDiscoverWindow`, `maxDiscoverFindings`; `internal/server/discover.go:320` |
| Point at a file the scan could not read | For an "unverified" finding the user points at the right folder | `POST /api/discover/scans/{scan}/findings/{finding}/locate {path}` — the one place a path leaves the browser, and only in the body | Working | `web/src/lib/discover.ts:533`; `internal/server/discover_locate.go:150` |
| Check reachability | In the start wizard the user checks whether a server answers on a host:port | `POST /api/probe/reachable` opens one TCP connection, sends and reads nothing, returns yes/no plus one reason and never raw error text | Working | `internal/server/probe.go:80`; e2e `web/src/lib/discover.ts:566` |
| Install / control a Blackbox on this PC | The user presses Scan this PC, then Install, Start/Stop, Apply, Snapshot, or Remove | `POST /api/blackbox/this-pc/{install,start,stop,apply,remove,snapshot}`; Unravis never runs elevated — each change runs `unravis-blackbox.exe --json` through an Elevator (a Windows prompt), settings passed in a temp file, not on the command line | Working | `internal/blackbox/thispc.go` (`ThisPC`, `HelperArgs`, `Install`, `Op.NeedsAdmin`); `internal/server/blackbox.go:802` |
| Add a Blackbox by folder, share or address | The user types a place (and, for an address, a read key) and presses Add | `POST /api/blackbox` (`bbAdd`) tries the read first; a Blackbox that cannot be read is never saved; duplicate place is a 409 | Working | `internal/server/blackbox.go:504`, `bbDuplicate`; e2e `tools/e2e/specs/bb-add.spec.ts` BB-04..BB-06 |
| Read a Blackbox for a case | The case holds a Blackbox tag; the investigation host lane reads it | `applyBlackboxLane` finds the Blackbox by name in the saved list, reads it with the sealed key, adds a warning if it cannot be read | Working | `internal/server/blackbox.go:1204`; `internal/blackbox/reader.go` (`ReadSamples`, `ProbeSource`) |
| Show key for the this-PC Blackbox | The user presses Show in the row | `POST /api/blackbox/{id}/reveal-key`; only for a Blackbox this PC installed, key held on view only, never cached or logged | Working | `internal/server/blackbox.go:644`; e2e `blackbox.spec.ts` BB-19, BB-19a |
| Run the coverage census | The user opens Coverage for a source and presses the run button | `GET /api/sources/{id}/coverage?start=&end=`; a live source is refused 409 `broad_scan_refused` unless `--allow-broad-scans`; a refusal is never kept | Working | `internal/server/coverage.go:19`; `web/src/components/CoveragePanel.svelte` |
| See what to collect next | The user opens the "What to collect next" list for a case | `GET /api/cases/{id}/collect` reads only import records (importer name, host label), not events, and returns up to 12 fixed-wording suggestions | Working | `internal/server/collect.go:26`; `internal/collect/collect.go` (`Suggest`, `MaxItems 12`) |
| Resource counters for a case | The user opens the Resource counters panel and opens a host | `GET /api/cases/{id}/resource` (rules RC1–RC9) and `GET /api/cases/{id}/series` for the lines | Working | `internal/server/resource.go:caseResource`; `web/src/lib/resource.ts` |

### Screens or endpoints in this area

- Screen 112 "Add data" — `AddDataPanel.svelte`: SQL, Mongo, Blackbox, files, day cards.
- Sources hub — `SourcesHub.svelte`: connections, SQL, Mongo, add, day, combos tabs.
- Connections — `ConnectionsPage.svelte`: saved SQL and Mongo connections, Test, Edit, Forget, keeper card, 20-connection cap.
- SQL / Mongo step — `SourceStep.svelte` with `SqlStepPanel.svelte`, `MongoStepPanel.svelte`, `LaneRunner.svelte`, `GrantScriptPanel.svelte`, `ProfilerGuide.svelte`.
- Discover "This computer" panel — `DiscoverPanel.svelte`, `DiscoverEntry.svelte`, `LocateFolder.svelte`.
- Wizard sources step — `wizard/SourceGallery.svelte`, `MongoClassify.svelte`, `SqlServerForm.svelte`, `ReachableCheck.svelte`, `SourceTagRow.svelte`.
- Blackbox drawer — dialog "Blackbox" with list, status views, "Place a Blackbox" wizard, "Scan this PC", "Add one by folder, share or address".
- Coverage drawer/panel — `CoveragePanel.svelte`, `CoverageDrawer.svelte`.
- Resource counters — `ResourcePanel.svelte` and `resource/` (CoverageDots, HostLines, RuleChips, RuleDrawer, StripChart).
- MCP/CLI: `cmd/unravis-blackbox/main.go` (the `--json` helper commands).
- HTTP routes: `POST /api/discover/scans`, `GET /api/discover/scans/{scan}`, `GET /api/discover/capabilities`, `POST /api/cases/{id}/discover/imports`, `POST /api/discover/scans/{scan}/findings/{finding}/locate`, `POST /api/probe/reachable`, `GET /api/importers`, `GET /api/cases/{id}/collect`, `GET /api/sources/{id}/coverage`, `GET /api/cases/{id}/resource`, `GET /api/cases/{id}/series`, the `/api/blackbox...` routes, `GET`/`POST /api/sqlconnections`.

### What a user gets out of it today (concrete outputs)

- A ranked, plain-word list of the next logs worth collecting, up to 12, each naming a log kind, why it helps, how to export it, and the importer that reads it (`internal/collect/wording.go`: host-counters, app-log, iis-log, httperr-log, event-logs, process-snapshot, runtime-counters, sql-errorlog, sql-counters, sql-health-events, redis-info/latency/slowlog/log, mongo-serverstatus/profile/mongod-log/mongostat). Every sentence is a constant; no file name, value or count from the case appears.
- A per-source coverage census: rows by kind and day with document and field counts, plus provenance and tenant attribution.
- A discovery list of readings with names and sizes, grouped by kind, each tagged admin/large/export-needed, with gaps shown and their reason; a scan that found nothing readable on a non-elevated run says so.
- A Blackbox status list: nine demo rows in the demo; real rows show Running/Partly ready/Cannot test yet, reached-by, last sample, interval, recorded size, gaps, disk free and host, with Start/Stop/Apply settings/Snapshot now/Remove/Check again and a revealable read key for a this-PC install.
- Host resource strips per host (host CPU, free memory, disk time, processes, runtime/Redis/Mongo queues, log rate, API P95) with rule chips, stall-period bands and start/stop markers.

**What is real vs demo/fake.** The Blackbox recorder, config, read-key hashing, folder/URL readers, and the this-PC helper are real code with tests (`internal/blackbox/`); the demo world with nine named rows and canned answers is in `internal/server/blackbox_demo.go` and is chosen when the app runs in demo mode. The front end has a fake discover client for tests/demos, `web/src/lib/discoverFake.ts`, returning synthetic findings under a fixed scan id. Demo SQL/Mongo lanes with canned rows and database names AuditDb/NoGrantDb/EmptyDb/ProfilerOffDb live in `internal/server/demo_lanes.go`. The `discover` package is real (`internal/discover/`): it reads `mongod.cfg`'s log path, SQL service/registry names and ERRORLOG locations, IIS roots and config, and, for the host, a live sampler plus Perfmon and .NET counters; a `Finding` carries no path field, so a path never leaves the server except through the locate body. Read-only is enforced in code, not just claimed: `discover` scans read names/sizes/config only; the probe sends and reads nothing and blocks metadata/link-local addresses; Blackbox readers have no write method and open only recorder-written file names; `collect` is pure (no store, clock or network); the coverage census refuses broad scans unless explicitly allowed.

**Status notes:**
- Discovery scan and import: **Working** (`internal/server/discover.go`, `discover_import.go`; e2e specs in `tools/e2e/specs/`).
- Blackbox this-PC install/apply/remove: **Working** in code with fakes for the Elevator; the real elevated path is `elevate_windows.go` and is Windows-only (`elevate_other.go` elsewhere).
- Mongo audit-source classification: **Working**, but only for database names a recent live test returned.
- File import from the wizard: **Working** for file kinds already in the wizard list (`wizardSources.ts` `FILE_KINDS`); the standalone "files" Add-data card is **Partial** — it is disabled unless the host build passes `files` (`AddDataPanel.svelte:14`).
- Linux host signals (sadf/pidstat/vmstat) are **Implemented, untested for suggestions** — they are known to `collect` but make no suggestion yet (`internal/collect/collect.go` `linuxSignals` comment).

---

## Part 4: Investigation and analysis flows


### What this area is

Once audit data is in a case, this area is everything the user can look at to reach a conclusion: the guided analysis (eleven numbered steps, each producing a readiness word and a written nutshell), the manual tool list, the three-depth explorer (Pulse → Where → Evidence → one record), the patterns drawer, the call trace, the comparison table, the checklist and saved views, and the report/export. The cockpit is the Svelte app (`web/src/App.svelte`); the engine that computes verdicts lives in Go (`internal/investigate/`). The app is read-only over audit rows: every screen is a projection of data already collected, and nothing in this area writes to or reruns the collection.

### Flows

| Flow | How the user does it | What the app does | Status | Evidence |
|---|---|---|---|---|
| Open the guided analysis | Case → Guided tab | Loads the case's step state; 11 steps (Step 0 Frame … Step 10 Origin) are rendered as an ordered list, all initially Waiting | Working | `web/src/lib/guided/stepModel.ts` `STEP_COUNT=11`; `tools/e2e/specs/guide-steps.spec.ts` GUIDE-01 asserts 11 waiting steps |
| Frame the windows | Step 0: type incident and reference From/To, pick releases | Validates: span ≤ 7 days, ≤ 50 releases, ≤ 100000 pool; times are UTC `YYYY-MM-DDTHH:mm` | Working | `web/src/lib/guided/frame.ts` `MAX_SPAN_MS=7d`, `MAX_RELEASES=50`, `MAX_POOL=100000`; `tools/e2e/specs/guided.spec.ts` GUIDE-04 |
| Run the guided analysis | Press the one primary button per step / "Run again" | POSTs `/api/cases/{id}/investigation`; engine evaluates the 11 steps and returns readiness, nutshell, findings, suspects, facts, signals, origin, confidence | Working | `internal/server/investigation.go`; `internal/investigate/engine.go`; `web/src/lib/guided/api.ts` `runInvestigation`; `tools/e2e/specs/investigate.spec.ts` INV-01 |
| Read each step | Tabs: Steps / Where / Evidence / Checklist / Export (5 tabs) | Each step shows a readiness word (`Ready`, `Partly ready`, `Cannot test yet`, `Failed`), reasons, untested spans and a written nutshell | Working | `internal/investigate/words.go`; `normalizeResult` maps any unknown word to `Cannot test yet` (`web/src/lib/guided/normalize.ts`) |
| Use a manual tool | Tools tab → one of 13 cards | Runs the matching backend call and pastes a sticker with its own word (`Ready`, `Partly ready`, `Cannot test yet`, `Failed`) | Working | `web/src/lib/guided/tools.ts` `TOOLS` (13); `tools/e2e/specs/manual-tools.spec.ts` asserts 13 cards and expected stickers |
| Explore data (D0→D3) | Pulse figure → Where → Evidence → record | Depth model 0-3: Pulse has five figures and a timeline; Where ranks operations; Evidence lists records; one record opens full detail | Working | `web/src/lib/depth.ts` `Depth 0..3`, `depthOf`, `breadcrumb`; `web/src/App.svelte:113-133` |
| Read the five figures | Pulse strip | Five buttons: volume, incomplete/pending, client errors, server errors, P95-latency; a click opens the ranked Where view with that lens | Working | `web/src/components/Pulse.svelte`; `web/src/lib/pulse.ts` `pulseFigures`, `Lens`; `tools/e2e/specs/investigate.spec.ts` INV-01 asserts five figures |
| Search patterns | Header → Patterns drawer → Run | Runs one search over the on-screen window: verdict, findings, episodes, volleys, stalls, repeats, onset, concentration, quiet gaps, step dating, audit series; each optional sub-check runs only when its box is ticked | Working | `web/src/components/PatternsPanel.svelte`; `internal/server/patterns.go`; `internal/server/repeats.go` |
| Trace one call | Evidence drawer → CallTrace | Shows up to 3 calls on one shared time axis with lane states and checks | Working | `internal/server/call_trace.go`; `internal/calltrace/lanes.go`, `checks.go`; `MAX_CALLS=3`, `DEFAULT_MARGIN_MIN=2` in `web/src/lib/callTrace.ts` |
| Compare periods | Where → "All operations" | ComparisonTable aligns this window against the reference window per operation | Working | `web/src/components/ComparisonTable.svelte`; `internal/server/rollup.go`, `slowest.go` |
| Keep a checklist | Checklist tab | Four lists (todo / doing / learned / ruled_out); add, move, edit, delete; items link to a suspect | Working | `web/src/lib/guided/types.ts` `ChecklistList`; `tools/e2e/specs/guide-checklist.spec.ts` GUIDE-14; `internal/server/case_plan.go` |
| Save a view | Manual tool → Save | Persists tool + settings under the case as a saved view | Working | `internal/server/case_views.go`; `web/src/lib/guided/api.ts` `saveView`, `listViews`, `deleteView` |
| Export a report | Export tab → Preview, then Download HTML/CSV | Server builds the report from the request that made the answer on screen; preview renders in a sandboxed iframe; saved file's SHA-256 is shown and matched; holds no payloads, user ids, connection details or passwords | Working | `web/src/lib/guided/report.ts` `reportWarnings`, `reportFileName`; `internal/server/case_report.go`, `case_report_scrub.go`; `tools/e2e/specs/export-ui.spec.ts` EXP-01/02/05/10 |
| Export evidence drawer | Header → Export | Preview first; shows hidden fields, findings, manifest, ledger, pseudonyms; Save file hashes the body; no second save for the same preview | Working | `web/src/components/ExportPanel.svelte`, `EvidenceView.svelte`; `tools/e2e/specs/export-ui.spec.ts` EXP-07 |
| Filter / control the query | FilterChips, QueryControls, filter groups | Filter values ≤ 20, user ids ≤ 50; filters render as removable chips | Working | `web/src/lib/investigation.ts` `MAX_FILTER_VALUES=20`, `MAX_USER_IDS=50`; `tools/e2e/specs/explorer.spec.ts` INV-10a |
| Close or override warnings | A window/list change after a run | Export shows `stale`; saved report repeats it; "Run again" or restoring the windows clears it | Working | `reportWarningCodes` (`stale`, `truncated`); `tools/e2e/specs/export-ui.spec.ts` EXP-05/05a |

### Chart or view | Data sources it uses | Time axis | Status

| Chart or view | Data sources it uses | Time axis | Status |
|---|---|---|---|
| Pulse "Requests over time" | One request-kind selected at a time (Sync / Async / Hangfire / HTTP); count, P95 and slowest request per interval | One axis; follows zoom window | Working |
| Pulse figure strip (5 figures) | Overview rollups of the current query | Whole run (not zoomed) | Working |
| ZoomStrip | Current query windows | One axis; brush / zoom / earlier / later | Working |
| MultiTimeline | Multiple audit sources overlaid | One shared time axis per source | Working |
| OverviewStrip | Rollup totals | Window summary (no time axis) | Working |
| BreakdownPanel | `breakdownDims` per dimension | Window summary | Working |
| ComparisonTable | This window vs reference window, per operation | Two aligned periods | Working |
| Patterns episode/volley/stall tables + idle/step/series sections | Audit rows, SQL, IIS, server logs joined by time | One axis; intervals and episodes share it | Working |
| CallTrace lanes | Up to 3 calls, audit + SQL + IIS lanes | One shared axis, `DEFAULT_MARGIN_MIN` | Working |
| Endpoint daily / HourGrid / DayOverlay | Per-day per-endpoint counts | Calendar-day axes | Working |
| EvidenceView record detail | Single record | Timestamped event list | Working |

### Screens or endpoints in this area

- Guided tabs (Steps, Where, Evidence, Checklist, Export) — `web/src/lib/guided/guided.svelte.ts`.
- Depth screens: D0 Pulse, D1 WhereView, D2 EvidenceView, D3 RecordDetail — `web/src/App.svelte:113-133`.
- Patterns drawer — `web/src/components/PatternsPanel.svelte`.
- Filter panel groups (Chart / Details / Filter / Ranges / What / When) — `web/src/components/filters/`.
- HTTP routes — `internal/server/routes.go`: `/api/cases/{id}/investigation`, `/timeline`, `/call-trace`, `/report` (GET/POST), `/export`, `/views`, `/checklist`, `/api/breakdown`, `/api/slowest`, `/api/patterns`, `/api/activity`, `/api/endpoint-daily`, `/api/export/preview`, `/api/export/confirm`.

### What a user gets out of it today

A user can frame a window, run the guided analysis, and receive a per-step verdict in plain words with the reasons and untested spans behind it, plus a suspect list ranked with the cheapest check for each. They can drop to the manual tools, drill from one headline figure down to a single named record, search for patterns and episodes, trace a specific call across audit, SQL and IIS on one axis, compare against a reference period, keep a running checklist, save views, and export a self-contained report or evidence file that is hashed and scrubbed of payloads, user ids, connection details and passwords. What they cannot do today is see SQL, IIS and API audit interleaved on a single unified time line as one chart — that interleaving exists inside Patterns and CallTrace but not as the single top-level timeline the owner wants; that unification is still to be built.

---

## Part 5: App shell, HTTP API, MCP tools, help


### What this area is

This is everything a person touches around the analysis itself. The app shell is one web page served by the Go process: a header with navigation and utility buttons, a breadcrumb bar, modal and side drawers, and small toggles such as the theme and the plain-words switch (`web/src/App.svelte`, `web/src/components/AppHeader.svelte:18`). Behind that page sits a local-only HTTP API whose whole route table is one ordered list in `internal/server/routes.go:95`. The same executable also runs as a read-only MCP server over stdio, exposing ten tools to Codex or Claude (`MCP.md`, `internal/mcp/schema.go:90`). Built-in help is a drawer with a start block, a searchable glossary built from the copy deck, and the repository's own user guide rendered inside the app (`web/src/components/Glossary.svelte`, `web/src/components/HelpGuide.svelte`). An "Explain simply" switch turns on plain-language blocks everywhere in the UI (`web/src/lib/eli10.svelte.ts`). Everything stays on the machine: the server only accepts localhost hosts and same-host origins, and the whole API is behind a per-run session token (`internal/server/security.go`, `internal/server/server.go:176`).

### Flows

| Flow | How the user does it | What the app does | Status | Evidence |
| --- | --- | --- | --- | --- |
| Open the app | Launch `unravis` (or `--demo`), follow the printed link, or the launch link the app makes | Serves the single page at `/`, injects the session token, starts the Svelte app | Working | `cmd/audit-explorer/main.go` (flags `--demo`, `--port`, `--no-browser`); `internal/server/routes.go:96`; `tools/e2e/specs/help.spec.ts` (demo fixture starts the exe and reads `demo.link`) |
| Move around the app | Use the header buttons (Patterns, Ledger, Export, Import, Blackbox, Glossary), the depth breadcrumb, and the theme button | Opens the matching drawer or changes screen; the breadcrumb shows where you are; below 1100px the drawers fold into a "More" menu | Working | `web/src/components/AppHeader.svelte:18-52,99-140`; `web/src/components/Breadcrumb.svelte` |
| Open the built-in guide | Header button "Glossary" opens Help, then "Open the user guide" | Shows the 11 `docs/user` pages in a fixed order, renders Markdown as Svelte nodes (no raw HTML) | Working | `web/src/components/HelpGuide.svelte`; `web/src/lib/help/pages.ts:7`; `web/src/lib/help/links.ts:8`; `tools/e2e/specs/help.spec.ts` HELP-01/04; HELP-13 bans `{@html}` |
| Read a term | Help's "Search terms" field, or a hint's "Learn more in the glossary" | Filters the glossary by plain text; a hint opens the drawer on one marked term and returns focus | Working | `web/src/components/Glossary.svelte:83-94`; `web/src/lib/appState.svelte.ts:1258-1274`; `tools/e2e/specs/help.spec.ts` HELP-02 |
| Turn on plain words | The "Explain simply" switch (in the header or the More menu) | One flag shows every HelpNote's "In simple words" block; saved to local storage | Working | `web/src/lib/eli10.svelte.ts`; `web/src/components/Eli10Toggle.svelte`; `web/src/lib/copy/helpCopy.ts` (`simple` blocks) |
| Switch time zone | The UTC/Local toggle | Switches how times are shown in the UI | Working | `web/src/components/TimeZoneToggle.svelte`; used from `web/src/components/AppHeader.svelte` |
| Ask an AI to investigate | Start the same exe with `--mcp`, register it in Codex or Claude | Serves ten read-only tools over stdio; finds the dashboard through the session file | Working | `cmd/audit-explorer/main.go` (`--mcp`); `internal/mcp/protocol.go`; `internal/mcp/bridge.go`; `internal/mcp/sessionfile.go`; `internal/mcp/bridge_test.go` |
| Look up a version | Help's build line, or `unravis --version` | Reads the version from `GET /api/version` for the build line; `--version` prints the same | Working | `web/src/components/Glossary.svelte`; `internal/server/version.go`; `tools/e2e/specs/help.spec.ts` HELP-03; `tools/e2e/specs/docs.spec.ts` DOC-10a |
| Check for updates | Help's "Check for updates" button | Calls `POST /api/update-check` only when pressed; the app never checks on its own | Working | `web/src/lib/updateCheck.ts`; `internal/server/routes.go` (`/api/update-check`); `tools/e2e/specs/docs-more.spec.ts` DOC-07a |
| Reach the API directly | A tool sends an HTTP request with the header `X-Audit-Token` | Local-only host/origin check, then token check, then master-key lock gate, then route | Working | `internal/server/server.go:176-224`; `internal/server/routes.go:424-460`; `internal/server/secfix_headers_test.go` |

### Screens or endpoints in this area

#### Page shell and navigation

The page is one Svelte app (`web/src/main.ts` mounts `web/src/App.svelte`). The header carries the brand, a mode badge, a Sources popover, the drawer buttons, the theme button and the "Explain simply" switch; on narrow screens the secondary drawers collapse into a "More" menu (`web/src/components/AppHeader.svelte:18`). A breadcrumb bar shows depth (`web/src/components/Breadcrumb.svelte`). Drawers come in modal and non-modal forms (`web/src/components/Drawer.svelte`). The app tracks open drawers and restores focus on close (`web/src/lib/appState.svelte.ts:1258`). Small helpers: `HelpNote` (always-visible plain line plus an optional simple block), `FieldHelp`, `Hint` (a disclosure popover, one open at a time), `Eli10Toggle`, `TimeZoneToggle`, `Glossary`. Status: Working (components plus the `A11Y-*` and `SEC-11` specs).

#### HTTP routes

Every route is declared in `internal/server/routes.go:95` (the `routes` slice). Matching is first-match; a matched path with the wrong method answers 405 with an `Allow` header, an unknown `/api/` path answers 404, and anything else is served as an asset (`internal/server/routes.go:424`). Public routes (`/`, the launch link, the session-proof endpoint) skip the token check. Screens call these through `web/src/lib/api.ts`. Two routes, `/api/cases/{id}/lanes/sql` and `/lanes/mongo`, are the SQL/Mongo read lanes; `/api/probe/reachable` and `/api/grant-scripts` are the probe and the owner-run grant scripts.

| Method | Path | Purpose | Screen / tool that calls it | Status |
| --- | --- | --- | --- | --- |
| GET, HEAD | `/` | The single page | Browser | Working |
| GET | launch link (`launchPath`) | One-time link that hands the token to the page | Browser start | Working (`secfix_headers_test.go`) |
| GET | `/api/session-proof` | Token proof for the MCP bridge (public) | MCP bridge | Working |
| GET | `/api/info` | Build/mode info, demo flag | Shell, header badge | Working |
| GET | `/api/version` | Version, commit, date | Help build line, `--version` | Working |
| POST | `/api/update-check` | Update check when asked | Help button | Working |
| GET, POST | `/api/sources` | List / add sources | Sources screen | Working |
| GET | `/api/sources/{id}/schema` | Field-presence census (names/types/counts only) | Schema view | Working (`internal/server/schema.go`) |
| GET | `/api/sources/{id}/coverage` | Coverage census, may refuse a broad scan (409) | Coverage drawer | Working |
| DELETE | `/api/sources/{id}` | Remove a source | Sources screen | Working |
| GET, POST | `/api/sql-connections` | List / save SQL connections | Sources / connect | Working |
| POST | `/api/sql-connections/test` | Test a SQL connection | Connect form | Working |
| DELETE | `/api/sql-connections/{id}` | Forget a SQL connection | Connect screen | Working |
| GET | `/api/connections/recent` | Recent connections | Start/connect | Working |
| POST | `/api/connections/remember` | Remember a connection | Connect form | Working |
| DELETE | `/api/connections/recent/{id}` | Forget a recent connection | Connect screen | Working |
| POST | `/api/connections/test` | Test a connection | Connect form | Working |
| POST | `/api/probe/reachable` | Reachability probe | Blackbox wizard | Working |
| GET | `/api/keeper/status` | Key keeper state (preview in demo) | Key keeper cards | Working |
| POST | `/api/keeper/migrate` | Move per-user data to shared layout | Key keeper card | Working |
| POST | `/api/keeper/remove-old` | Remove old copies when confirmed | Key keeper card | Working (`A11Y-06`) |
| GET | `/api/master-key/status` | Master-key state | Unlock screen | Working |
| POST | `/api/master-key/setup` | Set a master key | Master-key screen | Working |
| POST | `/api/master-key/unlock` | Unlock cases for this session | Unlock screen | Working |
| POST | `/api/master-key/lock` | Lock cases | Header lock | Working |
| POST | `/api/master-key/rewrap` | Rewrap after key change | Master-key screen | Working |
| POST | `/api/master-key/reset-damaged` | Reset a damaged key | Master-key screen | Working |
| GET, POST | `/api/cases` | List / create cases | Start, wizard | Working |
| GET, DELETE | `/api/cases/{id}` | Read / erase a case | Case home | Working |
| GET | `/api/cases/{id}/health` | List health targets/checks | Health panel | Working |
| POST | `/api/cases/{id}/health/run` | Run health checks (one at a time; 409 if busy) | Health panel | Working (`internal/server/health.go`) |
| GET | `/api/cases/{id}/lanes` | List DB lanes | Lanes view | Working |
| POST | `/api/cases/{id}/lanes/sql` | Read one SQL database into a day lane | Lanes view | Working |
| POST | `/api/cases/{id}/lanes/mongo` | Read one Mongo database into a day lane | Lanes view | Working |
| GET | `/api/grant-scripts` | Scripts the DB owner runs to allow reads | Lanes view | Working |
| GET, POST | `/api/cases/{id}/ledger` | List / add case theories | Ledger drawer | Working |
| POST | `/api/cases/{id}/ledger/copy-shared` | Copy the shared theories in once | Ledger drawer | Working |
| PUT, DELETE | `/api/cases/{id}/ledger/{hid}` | Edit / delete a theory | Ledger drawer | Working |
| POST | `/api/cases/{id}/ledger/{hid}/reuse` | Reuse a theory | Ledger drawer | Working |
| GET, PUT | `/api/cases/{id}/workspace` | Case workspace note (no secrets) | Case home | Working |
| GET, POST, DELETE | `/api/cases/{id}/sampler` | Resource sampler | Resource panel | Working |
| GET, PUT | `/api/cases/{id}/environments` | Environments | Wizard | Working |
| GET, POST | `/api/cases/{id}/sources` | Case source tags | Wizard / sources | Working |
| DELETE | `/api/cases/{id}/sources/{sourceId}` | Remove a source tag | Sources | Working |
| GET | `/api/cases/{id}/mongo-sources` | Mongo sources for a case | Sources | Working |
| POST | `/api/cases/restore` | Restore a case file as a new case | Enter screen | Working |
| POST | `/api/cases/{id}/export` | Export a case file | Case home | Working |
| DELETE | `/api/cases/erased/{id}` | Clear an Erased line | Cases list | Working |
| GET | `/api/cases/orphan-files` | Orphan case files | Cases list | Working |
| GET, POST, DELETE | `/api/cases/{id}/archive` | Archive status / start / cancel | Case home | Working |
| GET, POST, DELETE | `/api/cases/{id}/verify` | Verify status / start / cancel | Case home | Working |
| POST | `/api/cases/{id}/free` | Free space | Case home | Working |
| POST | `/api/cases/{id}/restore-in-place` | Restore in place | Case home | Working |
| POST | `/api/cases/{id}/restore-as-new` | Restore as a new case | Case home | Working |
| GET, DELETE | `/api/cases/{id}/key` (+ `unlock`, `lock`, `set`, `change`, `remove`) | Optional per-case key lifecycle | Case key screens | Working |
| POST | `/api/cases/{id}/close` / `reopen` | Close / reopen a case | Case home | Working |
| PUT | `/api/cases/{id}/retries` | Set retry plans | Retries view | Working |
| DELETE / POST | `/api/cases/{id}/retries/{retryId}` / `/retry` | Delete / run a retry | Retries view | Working |
| GET, POST | `/api/cases/{id}/imports` | List / start imports | Import drawer | Working |
| GET, DELETE | `/api/cases/{id}/imports/jobs/{job}` | Job status / cancel | Import drawer | Working |
| GET | `/api/cases/{id}/events` | Case events | Guided analysis | Working |
| GET | `/api/cases/{id}/series` | Case series | Guided analysis | Working |
| GET | `/api/cases/{id}/resource` | Resource reading | Resource panel | Working |
| GET | `/api/cases/{id}/collect` | Collected data list | Guided analysis | Working |
| POST | `/api/cases/{id}/timeline` | Case timeline | Guided analysis | Working |
| POST | `/api/cases/{id}/call-trace` | Call trace within a case | Investigator | Working |
| POST | `/api/cases/{id}/investigation` | Guided investigation | Guided analysis | Working |
| GET, POST | `/api/cases/{id}/views` | Saved views | Guided analysis | Working |
| DELETE | `/api/cases/{id}/views/{vid}` | Delete a saved view | Guided analysis | Working |
| GET, POST | `/api/cases/{id}/checklist` | Checklist | Guided analysis | Working |
| PUT, DELETE | `/api/cases/{id}/checklist/{iid}` | Edit / delete checklist item | Guided analysis | Working |
| POST | `/api/cases/{id}/report` | Build the case report | Guided analysis | Working |
| POST | `/api/cases/{id}/discover/imports` | Import discovered logs into a case | Discover drawer | Working |
| POST | `/api/analyze` | Run a comparison | Explorer | Working |
| POST | `/api/requests` | Page request timing rows | Inspector | Working |
| POST | `/api/activity` | Activity across sources | Inspector / MCP | Working |
| POST | `/api/slowest` | Slowest completed requests | Inspector / MCP | Working |
| POST | `/api/breakdown` | Top values of one dimension | Inspector / MCP | Working |
| GET | `/api/daily` | Daily rollup (cached a minute) | Charts | Working |
| GET | `/api/hourgrid` | Hour grid (cached a minute) | Charts | Working |
| POST | `/api/patterns` | Incident patterns | Patterns drawer | Working |
| GET | `/api/endpoint-daily` | Per-operation daily series | Charts | Working |
| POST | `/api/export/preview` | Preview an export | Export drawer | Working |
| POST | `/api/export/confirm` | Confirm an export | Export drawer | Working |
| GET, POST | `/api/ledger` | Older shared theories (read / add) | Ledger drawer | Working |
| PUT, DELETE | `/api/ledger/{id}` | Edit / delete a shared theory | Ledger drawer | Working |
| POST | `/api/ledger/{id}/reuse` | Reuse a shared theory | Ledger drawer | Working |
| POST | `/api/payload` | Read one payload | Record view | Working |
| POST | `/api/record` | Read one record | Record view | Working |
| POST | `/api/export` | Stream a CSV export (capped at 10000 rows) | Export drawer | Working |
| POST | `/api/call-trace` | Call trace without a case | Investigator | Working |
| GET | `/api/discover/capabilities` | What can be discovered here | Discover drawer | Working |
| GET, POST | `/api/discover/scans` | List / start scans | Discover drawer | Working |
| GET, DELETE | `/api/discover/scans/{scan}` | Scan status / cancel | Discover drawer | Working |
| POST | `/api/discover/scans/{scan}/findings/{finding}/locate` | Locate a finding | Discover drawer | Working |
| GET, PUT | `/api/hosts` | Hosts | Topology | Working |
| GET | `/api/importers` | Importer list | Import drawer | Working |

Route-level guardrails: request bodies cap at 1 MiB, responses at 16 MiB, exports at 10000 rows (`internal/server/server.go:38-42`). While a master key exists and is locked, every `/api/cases` route answers 423 before any handler runs, keyed on the route pattern so a new case route cannot forget the gate (`internal/server/routes.go:448`).

#### MCP tools

The same exe with `--mcp` serves ten read-only tools over stdio (`MCP.md`; `internal/mcp/schema.go:90`; dispatch in `internal/mcp/tools.go:110`). Each tool advertises `readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false`. The bridge finds the dashboard through `session.json` in the per-user config folder (or `AUDIT_CONFIG_DIR`), proves the peer on every new connection with `GET /api/session-proof`, and only then sends the token (`MCP.md`; `internal/mcp/bridge.go`; `internal/mcp/sessionfile.go`). One process keeps only its latest successful comparison for ten minutes; pages default to 20 and cap at 100, except the slowest page at 50 (`internal/mcp/tools.go`; `internal/mcp/schema.go`).

| Tool | What it returns | Case-only or live source | Status |
| --- | --- | --- | --- |
| `audit_get_guide` | The investigation workflow, evidence rules, limits and code-correlation guidance | None (static) | Working |
| `audit_list_sources` | Current source labels and IDs | Live metadata | Working |
| `audit_compare` | Ranked groups for one current-vs-reference comparison, plus a cache handle | One live aggregate per source per period | Working |
| `audit_get_groups` | Search, sort and page the cached comparison | Cache only | Working |
| `audit_get_timeline` | One group's cached timeline and reference statistics | Cache only | Working |
| `audit_compare_timelines` | 1–8 APIs aligned on cached slots with ms/percent changes | Cache only | Working |
| `audit_get_requests` | Bounded newest-first timing metadata for one cached group, with record ids | Live metadata query | Working |
| `audit_get_slowest` | Slowest completed requests for the cached comparison, with record ids | Live metadata query | Working |
| `audit_get_breakdown` | Top values of one dimension plus an Other row | Live aggregate query | Working |
| `audit_get_activity` | Timing metadata across sources in an explicit window | Live metadata query | Working |

Identifier exposure is decided only by the local `--mcp-allow-identifiers` switch: without it, user ids and user agents come back hidden, IP breakdown is refused, and `audit_compare` refuses the identifier filters (`MCP.md`; `internal/mcp/schema.go`; `cmd/audit-explorer/main.go`). Inputs and outputs cap at 1 MiB, upstream responses at 16 MiB (`MCP.md`).

#### Help, glossary and the plain-words layer

Help is a modal drawer opened from the header's "Glossary" button (`tools/e2e/specs/help.spec.ts` HELP-01). It shows a "Start here" block, a "Search terms" field, the glossary count, the build line, an update-check button, and "Open the user guide". The glossary is built from the copy deck and renders each term with its hint, definition, a "What it does not mean" line and aliases (`web/src/components/Glossary.svelte:88-94`). Search is plain text and escapes markup, so a typed `<img …>` renders nothing (`HELP-02`). The guide reads the same `docs/user/*.md` files the repository ships, ordered by `PAGE_ORDER` (`web/src/lib/help/links.ts:8`; `web/src/lib/help/pages.ts:7`), parses Markdown itself and never injects raw HTML (`HELP-13`). Guide pages link each other and each heading gets an id and `tabindex="-1"` for jump focus (`A11Y-12`, `A11Y-12a`). The build line carries the version but no folder, user name or secret (`HELP-03`).

The plain-words layer is one flag: when on, every `HelpNote` shows its "In simple words" block (`web/src/lib/eli10.svelte.ts`; `web/src/lib/copy/helpCopy.ts`). The copy is linted against a banned-word list (cause, because, due to, waiting on, blocked by, led to, result in, proof) so help never overclaims (`web/src/lib/copy/copylint.ts`; `tools/e2e/specs/docs-more.spec.ts` DOC-11; `tools/e2e/specs/sec.spec.ts` SEC-11).

### What a user gets out of it today

A person opens one local page, moves between the explorer, cases, sources, guided analysis, imports and the Blackbox with the same header, and can ask for plain words at any point. The same executable gives an AI assistant a small, read-only, well-described set of audit tools without exposing credentials, payloads, raw URLs, IPs or agents unless the operator deliberately turns identifiers on. Help is built into the app from the same documents the repository keeps in step with the code, and the whole surface is checked by end-to-end specs for keyboard use, focus, themes, small screens, reduced motion, security promises and copy linting (`tools/e2e/specs/a11y.spec.ts`, `sec.spec.ts`, `help*.spec.ts`, `docs*.spec.ts`; totals in `tools/e2e/COVERAGE.md`). Every HTTP route answers through one table, under a local-only host/origin check, a per-run token, capped bodies and responses, a lock gate over case routes, and security headers on every kind of reply including 401, 404, 405 and 204 (`internal/server/secfix_headers_test.go`). Nothing here reaches the internet by itself; the one outbound call is the update check the person presses.

---

## Part 6: Test evidence and known defects


### What this area is
This area is the proof behind every other DOC job: which user scenarios the repository actually verifies, and which it only claims. Three test suites exist: Go unit and integration tests under `internal/` (3520 `func Test` functions in 34 packages, per a grep for `func Test`), a browser end-to-end suite under `tools/e2e/specs/` (Playwright, run against a built demo exe), and web unit tests under `web/tests/` (116 `*.test.cjs` files, run by `npm test`). The e2e suite is driven by a scenario catalogue and reports status in `tools/e2e/COVERAGE.md`. The owner should read this as a map of confidence, not a feature list.

### Flows
The "flows" here are the ways evidence is produced.

| Flow | How the user does it | What the app does (routes/commands) | Status | Evidence |
| --- | --- | --- | --- | --- |
| Go tests | Developer runs `go test ./...` (build.ps1 runs it) | Tests each `internal/*` package; 3520 test functions | Working | `build.ps1`; grep `func Test` over `internal/`; per-package counts below |
| E2E browser tests | `powershell -File run-e2e.ps1` from `tools/e2e` | Starts the demo exe per spec file, drives Chromium, writes `report/results.json` | Working | `tools/e2e/run-e2e.ps1`; `tools/e2e/support/launcher.ts` (`startDemo`); `tools/e2e/playwright.config.ts` |
| E2E, chosen scenarios | `run-e2e.ps1 -Only CASE-10,SEC-08` | Greps test titles by scenario id | Working | `run-e2e.ps1` (`-grep`, `-Only`) |
| E2E chooses exe | `run-e2e.ps1 -Exe D:/path/unravis-demo.exe` or `UNRAVIS_EXE` | Sets `UNRAVIS_EXE`, launches it with `--demo --no-browser --port 0` | Working | `run-e2e.ps1`; `launcher.ts` (`exePath`, `DEFAULT_EXE`) |
| Web unit tests | `npm test` in `web/` | Runs `node --test tests/*.test.cjs` (116 files) | Working | `web/package.json` scripts `test`; `web/tests/` listing |
| Expected-failure tests | A scenario id in `known-defects.json` fails清楚ly | Fixture marks the test `expected to fail` with the defect id; a pass flips to "expected to fail, but passed" | Working | `tools/e2e/support/fixtures.ts` (`KNOWN`, `testInfo.fail(true, KNOWN[id])`); `tools/e2e/known-defects.json` |
| Error gating | Any test | Fails on unexpected console/page/request errors unless on `allow-list.ts` or per-test `allow()` | Working | `fixtures.ts` (`issues` fixture); `support/allow-list.ts` (empty list) |

### Scenarios by area
Counts are tests in each spec file (grep of `test(` per file) and the catalogue status in `COVERAGE.md`. "Automated" = tests in the named spec file; "Manual-only" = catalogue rows with a manual-only reason.

| Area | Scenario count | Automated | Manual-only | Evidence file |
| --- | --- | --- | --- | --- |
| A11y (accessibility) | 26 | 26 | 1 (A11Y-11) | `a11y.spec.ts` |
| Blackbox add/change | 24 + 15 + 10 | 49 | BB-21, BB-22, BB-23 (3) | `bb-add.spec.ts`, `bb-change.spec.ts`, `blackbox.spec.ts` |
| Cases (UI/wizard/API/retries/start/drawers) | 7+10+12+6+4+5 | 44 | CASE-14, CASE-19, CASE-25 (3) | `case-ui/-wizard/-api/-retries/-start/-drawers.spec.ts` |
| Docs screens | 5 + 14 | 19 | 0 | `docs.spec.ts`, `docs-more.spec.ts` |
| Explorer | 5 | 5 | 0 | `explorer.spec.ts` |
| Exports | 13 + 12 | 25 | 0 | `export-ui.spec.ts`, `exports.spec.ts` |
| Guided analysis (steps/checklist/guided) | 25+10+6=41 | 41 | GUIDE-12, GUIDE-20, GUIDE-25 (3) | `guide-steps.spec.ts`, `guide-checklist.spec.ts`, `guided.spec.ts` |
| Help | 6 + 15 | 21 | HELP-07 (1) | `help.spec.ts`, `help-guide.spec.ts` |
| Imports (extra ids) | 4 | 4 | 0 | `imports.spec.ts` |
| Investigate | 14 | 14 | INV-14 (1) | `investigate.spec.ts` |
| Keys | 5 + 8 | 13 | KEY-09 (1) | `keys.spec.ts`, `keys-ui.spec.ts` |
| Keeper / update | 8 + 13 + 5 | 26 | KEEP-10..14, UPD-08, UPD-09 (7) | `keeper.spec.ts`, `keeper-update.spec.ts`, `update.spec.ts` |
| Manual tools | 24 | 24 | 0 | `manual-tools.spec.ts` |
| MCP | 0 | 0 | MCP-01..08 (8) | `COVERAGE.md`; Go only: `internal/mcp/*_test.go` |
| Plan | 11 | 11 | 0 | `plan.spec.ts` |
| Security | 8 + 6 | 14 | SEC-10 (1) | `sec.spec.ts`, `security.spec.ts` |
| Sources | 8+9+1+18=36 | 36 | SRC-12, SRC-13, SRC-14 (3) | `sources.spec.ts`, `-ui`, `-limit`, `-steps` |
| Installer / upgrade | 0 | 0 | INST-01..22 (22) | `COVERAGE.md` (no spec) |

Catalogue totals: 179 covered, 2 expected-fail, 54 manual-only, 235 scenarios (`COVERAGE.md` header). The e2e run also executes IMP-01..04, which have no catalogue entry (`imports.spec.ts` header).

### Screens or endpoints in this area
- `tools/e2e/COVERAGE.md` — the scenario-to-spec table and the run instructions.
- `tools/e2e/known-defects.json` — maps scenario id to defect id (`DOC-02`: `D-C-29`, `KEEP-09`: `D-C-01`).
- `tools/e2e/support/fixtures.ts` — per-file demo, `api`/`page`/`allow` helpers, expected-fail and error gating.
- `tools/e2e/support/api.ts` — HTTP client; `createCase`, `importFile`, `waitState`, the import acknowledgement banner.
- `tools/e2e/support/launcher.ts` — starts one demo per spec on port 0, fixed clock `2026-10-20T09:00:00Z`.
- `tools/e2e/fixtures/files/` — checked-in sample logs and expected CSV; made by `fixtures/make-fixtures.mjs`.
- `testdata/investigate/*.golden.json` — five frozen analysis results, plus `thresholds.history.json`.
- `.dev/packets/fix/L4/REPORT.md` — a lane report with "Browser checks needed" (not a test).

### What a user gets out of it today
- A pass/fail list of 179 covered scenarios plus 4 import checks, in `report/results.json`.
- 3520 Go tests across all 34 `internal` packages, plus 116 web test files.
- Five golden analysis days (`golden_test.go`, `TestGolden` over `testdata/investigate/*.golden.json`) and a threshold-fingerprint history (`thresholds_test.go`).
- Two tests that document known-broken behaviour on purpose: `DOC-02` and `KEEP-09`.

### Gaps, defects and fakes
Known defects (from `tools/e2e/known-defects.json`):
- **`KEEP-09` → `D-C-01`** — affects the keeper area (Windows key keeper). Test in `keeper.spec.ts` is an expected failure.
- **`DOC-02` → `D-C-29`** — affects the docs area. Test in `docs.spec.ts` is an expected failure.

The `.dev/scenarios/defects` and `.dev/reports/e2e-ui-2.md` files the coverage notes refer to for defect text and manual-only reasons are **not in this repository** (`.dev` is git-ignored; only `.dev/packets/fix/L4/REPORT.md` exists). So the defect catalogue itself is unreadable here; only the two ids above survive in tracked files.

Areas with **no e2e coverage at all**: the Windows installer and services (INST-01..22, all manual-only), and MCP (MCP-01..08, all manual-only — "needs the MCP bridge process, not started by the demo").

Fakes and gaps inside the testing itself:
- `support/allow-list.ts` is empty, so any real console/request error fails a test — good, but it also means no known browser defect is currently tolerated.
- The launch check in `launcher.ts` and the `page` fixture retry one Windows-only socket error (`ERR_NO_BUFFER_SPACE`); this is a test-PC issue, not the app.
- `install.md` and `upgrade-and-uninstall.md` carry an explicit `> NOT RUN: needs a clean VM` banner: the Windows parts "have not yet run on a real install". The 93 `internal/setup` tests use fakes (`helpers_test.go` "a fake PC"; `fix_test.go` fakes registry/services/firewall).
- `L4/REPORT.md` "Honest limits": its lane tests are source greps and pure functions, "there is no DOM", and several checks are listed as "Browser checks needed" (manual).

### Questions the code could not answer
- The actual wording, severity and fix status of defects `D-C-01` and `D-C-29` — the defect catalogue (`.dev/scenarios/defects`) is not present.
- The per-scenario manual-only reasons in full — `.dev/reports/e2e-ui-2.md` is referenced but absent; only the short reason text in `COVERAGE.md` is available.
- Whether `DOC-02` and `KEEP-09` currently fail or pass at run time — the repository only records them as expected failures; no result file (`report/results.json`) is checked in (it is git-ignored).
- Whether the demo exe the suite targets exists — `DEFAULT_EXE` points to `D:\Projects\api-audit-explorer\.dev\run\scn\unravis-demo.exe`, a machine-local path outside this repo.

#### Note on Go coverage strength
Strong (many `func Test`, per grep): `internal/server` (744), `internal/audit` (308), `internal/store` (247), `internal/discover` (222), `internal/blackbox` (199), `internal/setup` (93), `internal/investigate` (80), `internal/evidence` (79), `internal/dbhealth` (77), `internal/casestore` (61), `internal/ledger` (59), `internal/mcp` (52). Thin (single digits): `internal/sessionproof` (1), `internal/sealedfile` (3), `internal/version` (5), `internal/join` (6), `internal/datadir` (7), `internal/casekey` (7), `internal/clock` (8). Every `internal/*` directory has at least one `_test.go`. 46 files are gated by `//go:build windows`, so on a non-Windows machine those tests do not run (e.g. `blackbox/service_windows_test.go`, `casestore/acl_windows_test.go`).

#### README / START-HERE claims with no test
These claims in `README.md` and `START-HERE.md` are not backed by any Go or e2e test found:
- The signed installer, manifest SHA-256 refusal, journal roll-back and the key-keeper service install (`README.md` build section; `install.md` `NOT RUN` banner) — installer scenarios are all manual-only.
- The AI/MCP workflow end to end (`README.md` "AI investigations", `MCP.md`) — all MCP scenarios are manual-only; only `internal/mcp` unit tests exist.
- The `--mcp` launch-token handshake and `session.json` discovery (`README.md`, `MCP.md`) — no e2e scenario; only unit coverage.
- MongoDB 3.4 legacy streaming with the 100,000-record and 16 MiB caps against a real MongoDB 3.4 — SRC-14 is manual-only ("needs a real MongoDB"); only synthetic Go tests exist.
- Real SQL Server read behaviour (SRC-12, SRC-13 manual-only).
- The five-minute deadline and 256 MiB soft memory target (`README.md` "Limits") — no test verifies them at runtime.

#### What the suites cannot prove
The e2e suite runs only the bundled demo exe with synthetic fixtures and a fixed clock. It therefore cannot prove: real SQL Server connections (SRC-12/13), a real MongoDB including 3.4 (SRC-14), a real Blackbox service (BB-22/23), the Windows key-keeper service (KEEP-10/11/14), old per-user case folders on a real profile (CASE-25), the real release feed (UPD-09), the MCP bridge process (MCP-01..08), the installer, firewall rule, services and UAC prompt on a clean PC (INST-01..22), or the signed installer manifest. The Go tests cover several of these (CASE-14, INV-14, KEEP-12, KEEP-13, UPD-08, HELP-07 are "a Go test scenario, no path through the demo"), but against fakes, not real servers or hardware.

---

# Errata from independent review

Reviewers: analysis part (Part 4) checked by one model, Parts 1 to 3 by another, Parts 5 and 6 by a third. Raw reports follow.

## Review of Part 4

#### Verdict

The document captures many real limits, routes, and components (11 guided steps, frame validation, depth model, pulse figures, patterns API, export scrubbing), but it merges two separate UIs—the case **Guided analysis** overlay and the main **Run analysis** cockpit in `App.svelte`—and mislabels guided tabs as including Where/Evidence. One flow marked **Working** (Call trace) has backend and a `CallTrace.svelte` component but no import or `addToTray` usage anywhere in the app, so users cannot reach it today. The intro’s “read-only” claim is overstated because checklist, saved views, and exports persist case data. **Needs fixes** before it can be treated as a trustworthy map of what users can actually do.

#### Wrong or overstated claims

| Doc claim | What the code shows | Evidence path:line | Corrected wording |
|---|---|---|---|
| Guided tabs: Steps / Where / Evidence / Checklist / Export (5 tabs) | Guided panels are main (Steps **or** Tools by mode), Checklist, After the fix, Nearby activity, Export. Where/Evidence live in `App.svelte`, not guided tabs. | `web/src/lib/components/guided/GuidedAnalysis.svelte:27-62` | Split guided tabs from explorer depth; list five guided panels including fix and nearby. |
| Open guided via “Case → Guided tab” | Case home opens guided via a button (`guidedFor`), not a tab named Guided. | `web/src/lib/components/CaseHome.svelte:460-463` | Case home → open guided analysis (button), not a “Guided tab”. |
| “Nothing in this area writes… collection” (read-only) | Checklist, saved views, and report/export POSTs persist case plan and files; only audit **collection** is not re-run from these screens. | `web/src/lib/guided/api.ts:13-22`; `internal/server/routes.go:288-304` | Read-only over **audit query** data; case checklist/views/exports are written. |
| Trace one call — **Working**; Evidence drawer → CallTrace | `CallTrace.svelte` is never imported; `addToTray` has no callers outside its module. | `web/src/components/CallTrace.svelte:1-15`; `web/src/lib/callTraceTray.svelte.ts:21-28` | Backend exists; UI not wired — **not user-accessible**, not Working. |
| Compare periods only “Where → All operations” | Same action on Pulse D0 and a chip in WhereView. | `web/src/App.svelte:115`; `web/src/components/WhereView.svelte:44` | Pulse or Where → “All operations”. |
| Five figures: volume, incomplete/pending, client errors, server errors, P95 | Labels are Requests, **Completion**, API/Observed P95, 4xx, 5xx. | `web/src/lib/pulse.ts:51-56` | Use actual figure labels; Completion is a rate, not a separate incomplete/pending figure. |
| `/report` (GET/POST) | Case report route is **POST only**. | `internal/server/routes.go:303-305` | `/api/cases/{id}/report` — POST only. |
| Pulse kinds “Sync / Async / Hangfire / HTTP” | Type is `synchronous`, `asynchronous`, `hangfire`, `http`, `http_unknown`, `all`. | `web/src/lib/types.ts:1-2` | Match UI/API kind strings (e.g. synchronous, not Sync). |

#### Missing items

| What exists | Where in code | Why it matters to a user |
|---|---|---|
| Main explorer **Run analysis** (`POST /api/analyze`) | `web/src/lib/appState.svelte.ts:1197`; `web/src/lib/api.ts:167`; `internal/server/routes.go:192` | Pulse/Where/Evidence depend on this run, separate from guided investigation POST. |
| Guided **After the fix** and **Nearby activity** tabs | `web/src/lib/components/guided/GuidedAnalysis.svelte:57-62`; `web/src/lib/copy/guidedCopy.ts:214,257` | First-class guided workflows omitted from flows table. |
| **RollupView** (endpoint daily, hour grid, day overlay) on D0 | `web/src/App.svelte:116`; `web/src/components/RollupView.svelte:1-40` | Calendar-style views under Pulse; listed in chart table but not in flows. |
| **`/api/analyze`** in route list | `internal/server/routes.go:192` | Primary entry for the cockpit the doc describes as `App.svelte`. |
| Import / **Add more data** from Pulse | `web/src/App.svelte:115,51` | Users attach SQL/IIS/logs without leaving analysis. |
| Steps ↔ **Manual tools** mode switch (same main tab) | `web/src/lib/components/guided/GuidedAnalysis.svelte:57-58,79-80` | Explains how 13 tool cards relate to the step rail. |

#### Claims confirmed

- Eleven guided steps (0 Frame … 10 Origin): `web/src/lib/guided/stepModel.ts:9,13-24`.
- Frame limits 7 days, 50 releases, 100000 pool; UTC `YYYY-MM-DDTHH:mm`: `web/src/lib/guided/frame.ts:23-25,5-6`.
- Guided run POST `/cases/{id}/investigation`: `web/src/lib/guided/api.ts:13-14`.
- Unknown readiness → “Cannot test yet”: `web/src/lib/guided/normalize.ts:15-29`.
- Thirteen manual tools: `web/src/lib/guided/tools.ts:32-45`.
- Depth 0–3 and pulse/where/evidence/record crumbs: `web/src/lib/depth.ts:8-19`; stage views `web/src/App.svelte:112-131`.
- Five pulse figures from `pulseFigures`; figures use whole run, hero follows zoom: `web/src/lib/pulse.ts:41-57`; `web/src/components/Pulse.svelte:23-25`.
- Filter caps 20 / 50: `web/src/lib/investigation.ts:14-15`.
- `MAX_CALLS=3`, `DEFAULT_MARGIN_MIN=2`: `web/src/lib/callTrace.ts:28-29`.
- Patterns optional idle/step/audit checkboxes: `web/src/components/PatternsPanel.svelte:31-35,177-181`.
- Checklist four lists: `web/src/lib/guided/normalize.ts:8`.
- Export stale/truncated warning codes: `web/src/lib/guided/report.ts:21-24`.
- Report scrubs payloads, user ids, connections, passwords: `internal/server/case_report.go:26-29,45`.
- ComparisonTable in WhereView: `web/src/components/WhereView.svelte:93`.
- Save view from manual tools UI: `web/src/lib/components/guided/SavedViews.svelte:17-39`.

#### Could not check

- E2e assertions cited in the doc (`guide-steps.spec.ts`, `investigate.spec.ts`, etc.) were not executed; code references for constants and components were verified directly.
- Full patterns engine sub-check list (repeats, concentration, onset, etc.) was not traced through `audit.RunPatterns` internals; UI and `POST /api/patterns` entry point were confirmed.

## Review of Parts 1 to 3

#### Verdict

These three area docs are mostly aligned with the repo: setup, master key, cases, discovery, and Blackbox behavior match the code and tests in the places I opened. A few concrete errors (wrong HTTP method, wrong API path, a bad evidence line for embedded assets, an overstated connection cap) and some gaps (`--mcp`, closed-case retention, discover cancel) mean the set is **trustworthy with targeted fixes**, not unreliable.

#### Wrong or overstated claims

| Doc claim | What the code shows | Evidence path:line | Corrected wording |
| --- | --- | --- | --- |
| DOC-1: dashboard embed cited at `cmd/audit-explorer/main.go:269` | Line 269 is loopback listen, not embed; dashboard is `//go:embed web/dist` in the root module | `assets.go:8-12`; `cmd/audit-explorer/main.go:269` | Cite `assets.go:8-12` (or README) for embedded dashboard |
| DOC-1: install scope “user default” with `options.go` only | UI opens with `scope: 'user'`; empty scope in preflight becomes `machine` | `web/setup/App.svelte:28`; `internal/setup/preflight.go:67-68`; `internal/setup/options.go:24-26` | Default in the setup window is “Just for me”; cite App.svelte; note server empty-scope → machine separately |
| DOC-2: `GET /api/cases/erased/{id}` clears an erased line | Route is **DELETE** only; client uses DELETE | `internal/server/routes.go:351-352`; `web/src/lib/cases.ts:334-336`; `internal/server/erased_log.go:181` | `DELETE /api/cases/erased/{id}` |
| DOC-3: `GET`/`POST /api/sqlconnections` | Registered path is **`/api/sql-connections`** (hyphenated) | `internal/server/routes.go:116-120` | `GET`/`POST /api/sql-connections` (and related `/api/sql-connections/...` routes) |
| DOC-3: Connections page “20-connection cap” (reads as all connections) | Comment and `canSaveAnother` cap **saved SQL** at 20; Mongo uses recent list, same numeric limit not stated there | `web/src/components/sources/ConnectionsPage.svelte:21-22,47`; `internal/sqlconn/sqlconn.go:26` | “At most 20 **saved SQL Server** connections”; do not imply Mongo shares the same server cap without checking |

#### Missing items

| What exists | Where in code | Why it matters to a user |
| --- | --- | --- |
| Closed cases auto-erased after 30 days (`Sweep`) | `internal/casestore/store.go:94-95,351-353` | Closing a case starts retention; old closed cases can disappear without using “Erase” |
| `DELETE /api/discover/scans/{scan}` cancels a scan | `internal/server/routes.go:313-315`; `internal/server/discover.go:676-677` | Users can stop an in-progress “Scan this computer” |
| `--mcp` read-only MCP over stdio (session file + token) | `cmd/audit-explorer/main.go:76-77,133-138,228-237` | Automation/IDE integration beside the browser UI |
| `GET /api/discover/capabilities` | `internal/server/routes.go:307-308` | UI learns what discovery can do on this OS/build |
| `/api/connections/recent`, `/api/connections/test`, `/api/connections/remember` | `internal/server/routes.go:110-113,165-166` | Mongo “saved/recent” and unified connection test paths omitted from DOC-3 route list |

#### Claims confirmed

- Loopback-only dashboard listen on `127.0.0.1`; datadir shared vs per-user layout and keeper pipe/env names (`cmd/audit-explorer/main.go:269`; `internal/datadir/datadir.go:6-11,35-36,108-112`).
- Build pipeline: npm ci/check/build (unless `-SkipWeb`), `go test`/`vet`, three exes + payload manifest + setup exe cleanup (`build.ps1:78-131`).
- Master key: `MinPassphraseLen` 12, routes at 169–173, unlock throttle 5 failures / 1s doubling to 60s (`internal/masterkey/masterkey.go:33-35`; `internal/server/masterkey.go:45-47,307-308`; `internal/server/routes.go:168-173`).
- Update check: POST only, 10s timeout, 5s cache, always 200 (`internal/server/update_check.go:28-37,106-119`; `internal/server/update_check_test.go:207-208`).
- Case id base32 26 chars; `caseCode` first 8; create idempotency 201/200 (`internal/casestore/store.go:26-33`; `web/src/lib/cases.ts:46`; `internal/server/cases.go:781-783`).
- Case key wrong-key 401 `wrong-case-key`; shared throttle semantics (`internal/server/case_key.go:50-52,91-94`).
- Ledger limits 200 hypotheses, 2000 chars, 8 MiB; states include `open` (`internal/ledger/types.go:13-18,34-37`).
- Discover import caps 31 days / 64 findings; Finding has no path in JSON (`internal/server/discover_import.go:32-35`; `internal/discover/discover.go:66-68`).
- Mongo classification gated by 30-minute TTL, 8 tests (`internal/server/case_sources.go:26-29,360`).
- Collect: max 12 suggestions; Linux signals known but no suggestions yet (`internal/collect/collect.go:17-18,99-102`; `internal/server/collect.go:22-26`).
- Coverage live source → 409 `broad_scan_refused` unless `--allow-broad-scans` (`internal/server/coverage.go:14-16,43`).
- Restore flows end **Closed** (`internal/server/archive_restore.go:23-25,30-36`).
- Blackbox demo nine rows (`internal/server/blackbox_demo.go:731`).
- Setup silent bad config exit 2 (`internal/setup/runner.go:54-55`; `internal/setup/setup_test.go:753`).
- Probe: POST `/api/probe/reachable`, dial only (`internal/server/probe.go:15-18,80-100`).

#### Could not check

- E2E spec behavior (CASE-*, KEY-*, BB-*, etc.) without running Playwright.
- Real Windows UAC, service install, DPAPI/keeper on a live profile (docs already mark some of this manual).
- Full `build.ps1` / installer run end-to-end in this Linux workspace.
- Whether every “Working (Go test)” test name still exists and passes (names were spot-checked via grep, not executed).

## Review of Parts 5 and 6

#### Verdict

**DOC-5-surfaces.md** is largely trustworthy: the route table, security model, MCP tool list, and help/glossary behavior match the code. Fix a few wrong line citations, the narrow-layout nav description (Blackbox also moves into “More”), and optional nuance that `--mcp` must not be combined with `--demo`. **DOC-6-evidence.md** is useful on gaps, known defects, and COVERAGE.md totals, but several quantitative claims are stale or wrong (web test file count, Help/Docs/A11y e2e counts, Go package counts in the “thin/strong” table, and the claim that `build.ps1` runs `go test`). Treat DOC-6’s per-area scenario counts as unreliable until re-grepped; the qualitative gap analysis still holds.

#### Wrong or overstated claims

| Doc claim | What the code shows | Evidence path:line | Corrected wording |
| --- | --- | --- | --- |
| Master-key lock gate cited at `routes.go:448` | Lock gate runs at `lockedGate` before `serveRoute`; line 448 is `methodNotAllowed` inside `serveRoute` | `internal/server/routes.go:431-436`, `internal/server/routes.go:441-448` | Cite `routes.go:431-436` (or `masterkey.go:366-375`) for the 423 gate |
| Below 1100px only Patterns, Ledger, Export, Import, Glossary fold into “More” | Blackbox (and Home/Case home/Lock when shown) also go into `menuEntries` in compact mode | `web/src/components/AppHeader.svelte:18-19`, `web/src/components/AppHeader.svelte:53`, `web/src/components/AppHeader.svelte:118-130` | Say Blackbox and other header actions fold into “More” below 1100px |
| Help e2e: `6 + 15` scenarios in `help.spec.ts` / `help-guide.spec.ts` | `help-guide.spec.ts` has 17 `test(` calls, not 15 | `tools/e2e/specs/help.spec.ts` (6), `tools/e2e/specs/help-guide.spec.ts` (17) | Help automated count is 6 + 17 = 23 (plus catalogue manual-only HELP-07) |
| Web unit tests: “116 `*.test.cjs` files” | 114 files under `web/tests/` | `web/package.json:6`; `web/tests/*.test.cjs` (114 files) | “114 web test files” |
| Go tests: “34 packages” under `internal/` | 32 top-level directories under `internal/` (importer, timeline, perfbench, etc. are separate from the listed “34”) | `/data/jobs/6498/work/internal/*` (32 dirs) | “32 top-level `internal/` trees” or count packages with `go list ./internal/...` |
| Go coverage table: `internal/discover` (222), `internal/casestore` (61) | Current `func Test` counts: discover 347, casestore 185 (includes sqlite subpackages) | `internal/discover/*_test.go`, `internal/casestore/*_test.go` (rg counts) | Refresh per-package counts from the repo |
| Docs screens: “5 + 14” automated | `docs.spec.ts` has 7 tests; `docs-more.spec.ts` has 24 | `tools/e2e/specs/docs.spec.ts`, `tools/e2e/specs/docs-more.spec.ts` | Docs automated tests: 7 + 24 = 31 |
| A11y: “26” scenarios in `a11y.spec.ts` | 28 `test(` calls | `tools/e2e/specs/a11y.spec.ts` | “28 tests in `a11y.spec.ts`” (catalogue may still list 26 scenario ids) |
| Go tests flow: “build.ps1 runs” `go test ./...` | `build.ps1` runs `go vet ./...` and builds binaries; only mentions `go test` in a comment | `build.ps1:100-107`, `build.ps1:129` | “Developers run `go test ./...` separately; `build.ps1` vets and builds, it does not run tests” |
| Blackbox automated “49” (24+15+10) | Spec files sum to 50 tests | `tools/e2e/specs/bb-add.spec.ts` (25), `bb-change.spec.ts` (15), `blackbox.spec.ts` (10) | “50 automated tests across the three Blackbox spec files” |

#### Missing items

| What exists | Where in code | Why it matters to a user |
| --- | --- | --- |
| Windows installer / setup UI (second Svelte app) | `web/setup/main.ts`, `web/setup/App.svelte`; built via `web/package.json` `build:setup` | Install/upgrade is a separate surface from the audit dashboard; DOC-5 only describes the main app shell |
| Additional shipped executables (keeper, blackbox, setup) | `build.ps1:102-107`, `build.ps1:127` | Users run more than `unravis`; key keeper and Blackbox are part of the product surface |
| `--mcp` cannot be used with `--demo` | `cmd/audit-explorer/main.go:101-102` | Operators starting MCP against a live dashboard should not expect demo mode on the same process |
| Sources UI e2e volume | `sources-ui.spec.ts` has 11 tests (doc table implied 9) | Understates automated source UI coverage in DOC-6’s table |
| Large `internal/importer` test corpus | 75+ `_test.go` files under `internal/importer/` | DOC-6’s “strong/thin” list omits importers despite heavy test investment |

#### Claims confirmed

- HTTP route table is a single ordered slice starting at `routes.go:99`; first-match dispatch, 405 with `Allow`, `/api/` 404 vs assets (`internal/server/routes.go:95-98`, `internal/server/routes.go:389-428`).
- Local host/origin gate and `X-Audit-Token` on protected API (`internal/server/server.go:173-184`, `internal/server/server.go:202-207`; `internal/server/security.go:37-41`, `internal/server/security.go:63-74`).
- Body/response/export caps 1 MiB / 16 MiB / 10 000 rows (`internal/server/server.go:31-33`).
- Ten MCP tools with read-only annotations (`internal/mcp/schema.go:88-103`); 10-minute result TTL (`internal/mcp/bridge.go:23`).
- Eleven user-guide pages in fixed `PAGE_ORDER` (`web/src/lib/help/links.ts:9-12`; loaded in `web/src/lib/help/pages.ts:15-17`).
- “Explain simply” persisted in `localStorage` (`web/src/lib/eli10.svelte.ts:5-17`).
- Update check only from Help button handler (`web/src/components/Glossary.svelte:46`, `web/src/components/Glossary.svelte:99`).
- `/api/daily` and `/api/hourgrid` rollup cache TTL one minute (`internal/server/rollup.go:22-24`).
- DOC-6 COVERAGE totals 179 / 2 / 54 / 235 and IMP-01–04 note (`tools/e2e/COVERAGE.md:11-12`).
- Known defects map DOC-02 → D-C-29, KEEP-09 → D-C-01 (`tools/e2e/known-defects.json:1-4`; expected-fail wiring `tools/e2e/support/fixtures.ts:100-101`).
- 3520 `func Test` in `internal/` (rg aggregate); five golden days under `testdata/investigate` (`internal/investigate/golden_test.go:16-22`; five `*.golden.json` files).
- Empty e2e allow-list (`tools/e2e/support/allow-list.ts:16-18`).

#### Could not check

- Exact Go package count via `go list ./internal/...` (no `go` binary in this environment); top-level directory count and `func Test` totals were verified with ripgrep.
- Whether DOC-02 / KEEP-09 fail or pass on a current e2e run (no checked-in `report/results.json`, as DOC-6 notes).
- Full manual-only reason text in `.dev/reports/e2e-ui-2.md` (absent from the repo).
- Whether `DEFAULT_EXE` exists on disk (`tools/e2e/support/launcher.ts:11` is a developer-specific path).
