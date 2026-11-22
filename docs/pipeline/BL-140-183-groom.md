# Groom: BL-140, BL-183 (and BL-141, BL-166)

Groomed 2026-11-21 against master @452f3b9 and open PR #70 (`claude/next-bl` @3917757).
Research: `/mnt/project-files/research/outputs/backlog/BL-140-183-windows-secure-config-files.md` (looked at rc.55 and an older checkout).

## 1. Verdict

BL-140 is mostly built (PR #65, merged): install-sql and enable-sql prove the file and every folder above it (owner + access list), and each SQL service start repeats a file-only proof. What is still open is (1) the live proof on nachi's PC (this is BL-141 too: one elevated run covers both) and (2) a small hardening: read the settings through the same handle that was checked, so the proof and the read are the same file. Build that one slice. Skip the research's bigger "installer-managed fixed path plus import" redesign for now. The default path is already the program folder, and custom paths are already proven at enable-sql. BL-183(a) is half done in PR #70: `RecoverJournal` now checks the folder before it parses. But PR #70 misses `JournalPending`, which still parses the machine journal before any check in the elevated run. Finish (a) with a handle-based journal open. BL-183(c) is safe and small: in production the SID is never empty for `unravis.exe`, so only fakes change. BL-183(b) is a native notice before the Windows approval prompt in `--recover`. Build it with the existing `ShowNotice` MessageBox style. RedirectionGuard: defer. BL-166 is not covered by this research. Groom it on its own.

## 2. What the research found that matters

- [SOURCE] `FILE_FLAG_OPEN_REPARSE_POINT` only stops a link at the last part of the path. Folders above must be checked on their own. Our code already checks every folder above the file for reparse points (`refuseSettingsFileChain`).
- [SOURCE] `GetSecurityInfo` on an open handle reads the owner and access list of the exact object that was opened. A name-based read (`GetNamedSecurityInfo`) can judge one file and then read another.
- [INFERRED] Check and read must use one handle: open the leaf once (no-follow, share read only), check type, reparse bit, link count, owner and access list on that handle, then read bounded bytes from it. Never reopen by path.
- [SOURCE] The SQL virtual account has only traverse on the folders, so it cannot read the folders' access lists. A start-time folder proof would need extra `READ_CONTROL` grants. This matches design decision 5 in `docs/design/bl-140-settings-folder-proof.md`.
- [SOURCE] BL-183a: the branch moves the folder check before `LoadJournal`, but both are still path-based, and the leaf is read with `os.ReadFile`.
- [INFERRED] A failed proof must not look like "no journal". Keep the journal intact, report the reason, do not delete it.
- [SOURCE] `OSElevator.Start` and `RunElevatedWait` call `ShellExecuteExW` with `runas`, `SW_HIDE` and no owner HWND.
- [INFERRED] For BL-183b: show a visible notice first, let the person click, then start `runas`. Foreground is best effort only. Never use focus tricks or topmost loops.
- [SOURCE] `splitApps` counts an empty SID as the person's. `processSID` returns `UnknownOwner` on failures.
- [INFERRED] RedirectionGuard (`ProcessRedirectionTrustPolicy`, 16) is extra cover. There is no full Windows Server support table. Probe it, then run audit mode before enforce mode. Not needed for these fixes.

Research errors or stale points:
- It says the active checkout "predates BL-140 work". That is stale: PR #65 is on master.
- It does not see that `JournalPending` (recover.go) also parses before the check.
- For BL-183c, it says to "validate image and SID on one handle". Master already ends a process only through `TerminateIf(pid, exe)`, which checks the path on the opened handle. The SID is still read in a separate open (`processSID`). This is a small residual gap, not a bug.

## 3. Current state in code

BL-140 (built):
- `internal/blackbox/sqlservice_windows.go`:
  - `verifyAdminOnlyFile` = `resolveSettingsPath` + `verifySettingsFileResolved` + `verifySettingsFolderChain`. Runs at install-sql and enable-sql.
  - `VerifySettingsFileForStart` → `verifySettingsFileOnly`: drive and path checks, `refuseSettingsFileChain` (reparse check on each folder), owner (`ownerSID`) and access list (`describeACEs`). All by path, through `GetNamedSecurityInfo`.
- `cmd/unravis-blackbox/main.go` `runSQLService`: runs `d.verifyConfig(p.ConfigPath)`, then `blackbox.LoadConfig(p.ConfigPath)`. That is a second open by path.
  - Missing: the read through the checked handle (design decision 7 says "Not done").
- `internal/blackbox/config.go` `ParseConfig([]byte)` already exists with the `maxConfigBytes` limit, so a handle reader can use it.
- Handle patterns to reuse:
  - `internal/blackbox/elevate_guard_windows.go` `GuardHelper`: CreateFile with share read only, then `os.NewFile`.
  - `internal/blackbox/service_secure_windows.go:116`: `GetSecurityInfo(h, ...)`.
- Live proof missing. The BL-140 design doc says a PC run was done unelevated only. Not yet verified: the elevated enable-sql run, and a real service start under `NT SERVICE\UnravisBlackboxSql` (= BL-141).

BL-183:
- (a) `internal/setup/runner.go` `RecoverJournal`:
  - On master: `LoadJournal` runs before `datadirCheck`.
  - PR #70 changes the order to: `os.Lstat` leaf → `Lstat` dir for symlink/irregular → `datadirCheck` → `LoadJournal`.
  - Still missing:
    - `internal/setup/recover.go` `JournalPending(true)` runs `os.Stat` + `LoadJournal` before any check. `Recover` calls it first, and so does the elevated `cmd/unravis-setup/recover.go:47`.
    - `LoadJournal` (`internal/setup/journal.go:182`) uses `os.ReadFile` with no size limit and follows a leaf link.
    - `datadirCheck` → `datadir.ReadFolderSecurity` reads by name. Only the journal's own folder is checked, not the folders above it (`C:\ProgramData` is stock).
- (b) The hidden path is `internal/update/launch_windows.go` `LaunchRecover`: it runs `--recover` with `HideWindow`. In `cmd/unravis-setup/recover.go`, the machine path closes the app (`r.CloseApp`) before `e.elevateWait`, so there is no tray at prompt time.
  - `setup.RunElevatedWait` (`internal/setup/elevate_windows.go:~245`) uses `SW_HIDE`, no hwnd, and waits 5 minutes.
  - `setup.ShowNotice` (`internal/setup/notice_windows.go`) is an existing MessageBox helper, already wired as `env.notice`.
- (c) `internal/setup/fixes_b.go` `splitApps`: `case q.SID == "": mine`.
  - In production, `osProcesses.List` sets the SID for every `unravis.exe`, and every caller (`runningApps`, `closeAppIn`) keeps only `unravis.exe`, so production never sends an empty SID.
  - The fake treats an empty SID as mine (`fake.go:703`, `:765`).
  - The worker sets `r.UserSID = ""` (`cmd/unravis-setup/main.go:456`) until `WorkerPrepare` fills it.
- RedirectionGuard: not used anywhere in the code (only mentioned in an old `.forge` review note).

BL-166: `internal/blackbox/thispc.go` `ScanThisPC` (`ConfigUnreadable`). This is about the host recorder's config in Program Files, which an unelevated app cannot read. The research does not touch it.

## 4. Scope (one PR each)

- **S1 — BL-140b: read the SQL settings through the checked handle.** Size M (~250 lines).
  - Goal: a new `openVerifiedSettings(path) ([]byte, error)` (Windows). It:
    1. runs the existing drive, path and `refuseSettingsFileChain` checks;
    2. opens the leaf once with `CreateFile(GENERIC_READ|READ_CONTROL, FILE_SHARE_READ, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT)`;
    3. checks `GetFileType==DISK`, then no reparse bit, not a directory and link count 1 (via `GetFileInformationByHandle`);
    4. checks owner + access list with `GetSecurityInfo(h)` and feeds them to the existing pure rules (`trustedFileOwner`, `adminOnlyDACL`);
    5. reads at most `maxConfigBytes+1` bytes from the same handle.
  - `runSQLService` then uses `ParseConfig(bytes)`. The `verifyConfig` dep becomes `loadVerifiedConfig func(path) (Config, error)`.
  - Files: `internal/blackbox/sqlservice_windows.go` (or a new `settings_open_windows.go`), `sqlservice_other.go`, `cmd/unravis-blackbox/main.go`, tests, `docs/design/bl-140-settings-folder-proof.md` (decision 7 → done).
- **S2 — BL-183a finish: check the machine journal before it is parsed, everywhere.** Size M (~200 lines).
  - Goal:
    - `JournalPending(elevated=true)` does no parse before the folder check. It returns pending-or-refused without parsing.
    - New `loadMachineJournal(dir)`: no-follow `Lstat` of each folder from the ProgramData root down (no reparse points). Then open the leaf by handle (no-follow, share read), check it is not a reparse point, has link count 1 and has an admin/SYSTEM owner. Then read with a size limit (e.g. 4 MiB). Then parse.
    - A proof failure returns a "refused" error, never "absent". The journal is kept.
    - The leaf handle read needs a small Windows file in `internal/setup` (fake: path read).
  - Files: `internal/setup/recover.go`, `runner.go`, `journal.go`, new `journal_open_windows.go` / `_other.go`, `setup_test.go`. Build on PR #70's branch, or after it merges.
- **S3 — BL-183c: an empty SID is not the person's.** Size S (~60 lines).
  - Goal: in `splitApps`, `SID == ""` goes to `others`. Also make `UserSID == ""` put every process in `others`, so nothing is closed.
  - Update the fake (`fake.go` QuitRemovesApps) and the fixtures so they use an explicit `f.UserSID`.
  - Optional: `processSID` checks `IsValid()` on the SID.
  - Files: `internal/setup/fixes_b.go`, `fake.go`, `os_windows.go`, tests.
- **S4 — BL-183b: visible notice before the approval prompt in `--recover`.** Size S/M (~150 lines).
  - Goal: in `runRecover`, when `pendingMachine` is true and the run is interactive (not the elevated copy), first show a native OK/Cancel box:
    - Text: "An Unravis update was cut off. To finish putting it back, Windows will ask for administrator approval. Click OK, then answer Yes." (plain words).
    - Cancel = no prompt, a "failed" result note with the reason, and the journal is kept.
    - OK = then `RunElevatedWait`. The click gives this process foreground rights.
    - Optional: pass a real owner HWND (a small visible owned window kept until the shell returns) instead of 0. Never use the foreground window of another app.
  - Order matters: show the notice before `CloseApp`, so a Cancel leaves the app running.
  - Files: `cmd/unravis-setup/recover.go`, `internal/setup/notice_windows.go` (add `AskOKCancel`), `_other.go`, `cmd/unravis-setup` tests (fake `notice`/`ask`).
- **S5 — BL-140/BL-141 live proof run (no code, or test fixes only).** Size S.
  - Goal: on DESKTOP-QQDVSD1, elevated: install-sql, then `icacls` on the settings file, then enable-sql, then the service starts and records.
  - Also start once with the file swapped by a user-owned copy: the start is refused with `sql: settings-not-locked`.
  - Record the results in the design doc. Tick BL-140 and BL-141 only if all of it passes.

Not in scope:
- RedirectionGuard probe (defer).
- Managed fixed-path config plus import (drop for now).
- BL-166 (separate groom).

## 5. Risks and unknowns

- **Security / fail closed (S1).** The handle reader asks for `READ_CONTROL` on the file. The service has `FILE_GENERIC_READ` on it, which includes `READ_CONTROL`, so this should work. Proven only on a real service start. If it is wrong, the SQL service will not start at all. The fixed sentence and event code already exist.
- **Ancestor reads under the virtual account (S1, S5).** `refuseSettingsFileChain` needs read-attributes on `C:\` and `Program Files` as `NT SERVICE\UnravisBlackboxSql`. This is not proven live yet. It is the top unknown of the BL-140 work.
- **Share mode (S1, S2).** Opening with share read only can make the open fail while the installer is writing the file. The service start would fail. Reads are short; the remedy is a retry by service recovery, never a looser open.
- **Data loss (S2).** A wrong refusal leaves a real cut-off install unrepaired. A wrong accept replays an untrusted journal as admin. Keep the journal on refusal. Never delete it. Test the committed, uncommitted, missing and broken cases.
- **Behaviour change (S3).** If any future list path stops setting the SID, setup will refuse with "open for another person" instead of closing the app. That is the safe direction.
- **UX (S4).** A MessageBox from a hidden detached process may itself open behind other windows. `MB_SETFOREGROUND` is best effort. With a hidden recover there is no other surface: the app was asked to start it, and the app is still running until `CloseApp`. Unknown until tested on the PC with other windows in front.
- **Windows-only.** S1, S2 and S4 code paths only run on Windows. Linux can test the pure rules and the fakes only.
- **Rollback.** Each slice is a plain code revert. No data format change: journal and config formats stay the same.
- **Footprint.** All checks run once per service start or once per recover. There is no cost in the 15 s loop. CPU and memory are negligible. No network.

## 6. Acceptance per slice

- **S1**
  - The service start reads the settings only through the handle that was checked. There is no `LoadConfig(path)` call left in `runSQLService`.
  - Each case below is refused with `errSQLSettingsNotLocked` and the `sql: settings-not-locked` log code: a reparse-point leaf, a hard-linked leaf (link count > 1), a file owned by a non-admin, an access list that grants write to Users, and a file over `maxConfigBytes`.
  - A properly locked file in the program folder starts.
  - Linux: the pure rules and the `runSQLService` dep tests pass.
- **S2**
  - In the elevated run, `JournalPending` and `RecoverJournal` never read a journal file's bytes when its folder (or any folder above it) is a link, is owned by a non-admin, or can be changed by Users.
  - A leaf that is a link or has more than one link is refused.
  - A refusal returns `found=true` and an error. The journal file is still there afterwards.
  - The unelevated per-user path behaves as before.
- **S3**
  - `splitApps` with `SID ""` → others.
  - `UserSID ""` → all processes go to others.
  - With an unknown or empty SID, setup refuses to close and says "open for another person". The fake tests are updated and green.
- **S4**
  - On a pending machine journal in an interactive unelevated `--recover`, a notice appears before any Windows prompt and before the app is closed.
  - Cancel: no prompt, the app is not closed, the journal is kept, and `update-result` says "failed" with the reason.
  - OK: the prompt follows. Yes → the journal is replayed. No → `ErrUACCancelled` is reported.
  - An elevated run (`--recover --no-restart`) shows no notice.
- **S5**
  - The `icacls` output on the PC shows: owner Administrators; SYSTEM and Administrators full; the SQL SID read.
  - The service runs and writes SQL recordings.
  - The swapped-file start is refused.

## 7. LIVE-PATH CHECK per slice

- **S1**
  - (a) Linux: unit tests of the pure rules and of the `runSQLService` wiring; `GOOS=windows go vet`.
  - Windows CI (windows-2022, elevated runner) runs Windows tests on real NTFS with real access lists: make temp files with owner BA, a junction, a hard link and a Users-writable list, and assert each outcome. That is real Windows code, not dummy data.
  - (b) PC: a real service start under `NT SERVICE\UnravisBlackboxSql` reading the real `blackbox.json`, then live SQL DMV recordings appearing. This needs elevation for install-sql and enable-sql.
  - (c) An oplock or race attack on the window cannot be shown here. The test can only show that there is one open.
- **S2**
  - (a) Linux: fake tests (the order of calls, no parse on refusal).
  - Windows CI: a test that writes a real machine journal under a temp ProgramData-like folder, with a junction and a Users-writable list, and runs the real `loadMachineJournal`.
  - (b) PC: cut off a real per-machine update (kill setup mid-run), then the app starts `--recover`, the elevated run replays, and the install is whole. Plus one run with the journal folder's access list loosened, to see the refusal and that the journal is kept.
  - (c) A power loss can only be simulated with a kill.
- **S3**
  - (a) Linux: `internal/setup` fake tests. They run on Linux (some setup tests fail on Linux on master; compare by name). Windows CI is the real run.
  - (b) PC: an upgrade with the app open closes it as before (the SID is read). An optional second account showing "open for another person" needs a second Windows login.
  - (c) An empty SID cannot happen on the real path today, so there is nothing to see live for that exact case.
- **S4**
  - (a) Linux: `cmd/unravis-setup` tests with a fake notice and a fake elevateWait (order, Cancel, OK).
  - (b) PC only: whether the notice and then the UAC prompt really come to the front from the hidden `LaunchRecover` start, with other windows in front. Test OK, Cancel, and No at UAC.
  - (c) Focus is OS-decided. There is no guarantee across UAC policies, RDP or Server Core. Report what was seen; do not claim it always works.
- **S5**: (b) only. It needs nachi's PC, elevated. It cannot be done here.

## 8. Needs a new collector or agent?

No. All slices change existing code paths (the SQL service start, setup recover). No new collector, agent or service. BACKLOG rule quoted: "Items under "Backlog (deferred)" are not to be built without the owner's go; today that means anything that needs a new collector or agent." Not triggered.

## 9. Dependencies and file collisions

- PR #70 (BL-183a) owns `internal/setup/runner.go` (`RecoverJournal`), `setup_test.go` and the BL-183 BACKLOG line.
  - S2 must build on top of #70 (same branch or after the merge). Do not race it.
  - S3 and S4 touch `fixes_b.go`, `fake.go`, `cmd/unravis-setup/recover.go` and `notice_*.go`. There is no overlap with #70's diff, but `setup_test.go` may conflict a little.
- PR #71 (BL-155) and PR #72 (releases dashboard): no overlap expected.
- OTA thread files (`internal/update`, `internal/server/updater*.go`, `internal/blackbox/selfupdate.go`): no slice needs them.
  - S4 deliberately puts the notice in `unravis-setup` and does not change `internal/update/launch_windows.go` `LaunchRecover`.
  - If SPEC decides that the app must show an "approval waiting" state in the dashboard instead, that touches `internal/update`, and it needs the OTA thread's agreement.
- S1 touches `internal/blackbox/sqlservice_windows.go` and `cmd/unravis-blackbox/main.go`. Not OTA-owned, but `cmd/unravis-blackbox/main.go` also has the self-update wiring: keep the diff inside `runSQLService` and `realDeps`.

## 10. Recommended order and handoff to SPEC

Order:
1. S3: small and safe.
2. S2: after #70 merges, or stacked on `claude/next-bl`.
3. S1.
4. S4.
5. S5: run S5 on the PC with a build that has S1, so a single elevated session proves BL-140, BL-141 and S1 together.

Hand to SPEC next: **S2** (the open security gap: `JournalPending` parses untrusted bytes as admin) and **S1**. Give SPEC exact handle flags, which pure rules to reuse, and the Windows CI test fixtures (junction, hard link, Users-writable list, non-admin owner).

Drop or defer:
- **Managed fixed config path plus import (research BL-140 main pick):** drop for now. The default is already the program folder, and custom paths are proven at enable-sql and on every start. A move changes the UX and the install layout for little gain.
- **Folder-chain proof at service start:** drop. It needs new `READ_CONTROL` grants on system folders. Design decision 5 already rejected it for good reason.
- **RedirectionGuard:** defer. Our own no-reparse checks cover these two readers. There is no server support table, and it needs a compatibility run first.
- **Authz-based effective-access checks:** drop. The fixed allow-list rule is simpler and stricter.
- **BL-166:** not covered by this research. Groom it separately: it is about reading the host recorder's config unelevated.
