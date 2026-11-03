# Phase 07 background-run acceptance record

Date of automated run: 2026-11-03. Branch `feat/investigation-timeline`, working tree as it stood at the run (uncommitted).

Environment: Windows 11, Go from `.dev\toolchains\go\bin\go.exe` with `GOTOOLCHAIN=local`, `CGO_ENABLED=0`, and `GOCACHE`, `GOMODCACHE`, `GOPATH` under `.dev\toolchains\cache`. No gcc, no `-race`. `build.ps1` was NOT run (it rewrites `cmd/unravis-setup/payload`). No row below makes a timing claim.

How to read the numbers: "N PASS" is the count of top-level `--- PASS` lines that `go test -v` printed for that command (sub-tests are not counted). Where the packet text predicted a different count, the printed number is given and the difference is noted. Nothing in this file marks a manual row as passed; every manual row is PENDING until a person runs it.

## Automated

| id | check | command (repo root) | result | numbers |
|---|---|---|---|---|
| A1 | build and vet | `go build ./...` ; `go vet ./...` | PASS | both exit 0, no output |
| A2 | package tests | `go test ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink ./internal/applog ./internal/server ./internal/setup ./cmd/audit-explorer -count=1` | PASS | 8 packages, all `ok` |
| A3 | windowless build over pipes | `go test ./cmd/audit-explorer -run "TestGuiBuild" -count=1 -v` | PASS | 3 PASS, 0 SKIP, 0 FAIL (GUI subsystem, `--version` over a pipe, MCP stdio round trip) |
| A4 | other-OS compile | `$env:GOOS='linux'; go vet ./cmd/audit-explorer ./internal/applog ./internal/setup ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink` | PASS | exit 0, no output |
| A5 | security checks of the high-risk packets | see the "A5 detail" table | PASS | 10 packets re-run; 13 named security tests PASS; static checks as listed; 3 notes recorded below |
| A6 | `windowsgui` only on the app build | `Select-String -Path build.ps1 -Pattern "windowsgui"` | PASS | 1 match: `build.ps1:72 $GuiLdFlags = "$LdFlags -H=windowsgui"`; `GuiLdFlags` is used on the `unravis.exe` build line (119); the keeper (123), black-box (124) and setup (144) builds use `$LdFlags` |
| A7 | launch link stays out of the log | `go test ./cmd/audit-explorer -run "TestPrintStartupKeepsTheLaunchLinkOutOfTheLog\|TestReopenNeverLogsTheCode" -count=1 -v` | PASS | 2 PASS |
| A8 | no database files tracked | `git ls-files -- "*.unravis.db"` | PASS | 0 files |
| A9 | e2e launcher unchanged | `Select-String -Path tools/e2e/support/launcher.ts -Pattern "no-browser"` | PASS | 1 match (`launcher.ts:78`); the main Playwright run belongs to the WP-58 gate and was not run here |

### A5 detail (security proofs of the high-risk packets)

| packet | proof run now | result | numbers |
|---|---|---|---|
| WP-71d | `Select-String internal/tray/*.go -Pattern ChangeWindowMessageFilter` (non-test files) | PASS | 0 matches |
| WP-71d | every test that starts `Run` sets the hook (`addIconHook` in `tray_windows_test.go`) | PASS | hook saved and replaced in the shared test setup (3 matches) |
| WP-72a | `go test ./internal/instance -count=1 -v` | PASS | 30 PASS (packet text predicted 11; the package has grown) |
| WP-72b | `TestAcquireDetectsAMutexOwnedBySomeoneElse` (in `go test ./internal/instance -run "TestAcquire\|TestRelease"` and again in the security run) | PASS | 6 PASS in the packet run |
| WP-72c | ACL allowlist test `TestEventsGrantWriteOnlyToTheUserSystemAndAdministrators` | PASS | 1 PASS (see note 1) |
| WP-72c | `Select-String internal/instance/signal_windows.go -Pattern "SECURITY_ATTRIBUTES\|SetSecurityInfo\|SetNamedSecurityInfo"` | PASS | 0 matches (default descriptor used) |
| WP-72c | `Select-String internal/instance/signal_windows.go -Pattern "os/exec\|exec\."` | PASS | 0 matches |
| WP-72c | `TestListenRefusesEventsThatAlreadyExist`, `TestSignalDoesNotKeepTheEventAlive`, `TestListenAndSignalDetectEventsOwnedBySomeoneElse` | PASS | 3 PASS; `go test ./internal/instance -count=3 -v` gives 90 PASS (30 tests x 3), 0 FAIL |
| WP-73a | `Select-String cmd/audit-explorer/quit.go -Pattern "os/exec\|exec\.Command"` | PASS | 0 matches (the quit route runs no program) |
| WP-73a | `Select-String cmd/audit-explorer/main.go -Pattern "validateFlags"` | PASS | 0 matches |
| WP-73a | `go test ./cmd/audit-explorer -run "TestRunQuit\|TestParseFlagsQuit\|TestParseFlagsBackground\|TestMainRunsQuit" -count=1 -v` | PASS | 8 PASS |
| WP-73c | `Select-String cmd/audit-explorer/reopen.go -Pattern "log\.\|Logf\|Errorf"` | PASS | 0 matches |
| WP-73c | `Select-String cmd/audit-explorer/reopen.go -Pattern "os\.Exit"` | PASS | 0 matches |
| WP-73c | `TestReopenNeverLogsTheCode` and the throttle tests (`go test ./cmd/audit-explorer -run "TestReopen\|TestSystem32File\|TestMainUsesReo\|TestQuitSignalCancels" -count=1 -v`) | PASS | 13 PASS |
| WP-73c | `Select-String cmd/audit-explorer/main.go -Pattern "newLaunchCode\("` expected no output | PASS with note | 1 match, the declaration `func newLaunchCode()` at `main.go:445`; no call in `main.go` (start-up uses `mintLaunchCode` at lines 280 and 294) and `TestMainUsesReo...` (checks the source for a direct call) PASS (see note 2) |
| WP-74b | `go test ./internal/autostart -count=1 -v` including `TestEnableRefusesAnExeThatIsNotInstalled`, `TestEnableRefusesRelativeAndWrongNamedExe`, `TestEnableWritesItsTempFileOutsideTheStartupFolder` | PASS | 14 PASS |
| WP-74b | `Select-String internal/autostart/autostart.go -Pattern "os\.RemoveAll\|filepath\.Glob\|os\.ReadDir"` and `-Pattern "os/exec"` | PASS | 0 matches each |
| WP-74g | `go test ./internal/setup -run "TestUninstallRemovesDesktop\|TestUninstallToleratesMissing\|TestUninstallKeeps\|TestUninstallAsWorker\|TestDescribeChangesMentions\|TestDescribeChangesWithoutDesktop" -count=1 -v` | PASS | 9 PASS (packet text predicted 8) |
| WP-74g | `Select-String internal/setup/uninstall.go -Pattern "os\.Remove\(l\)"` | PASS | 1 match (`uninstall.go:167`), reached only after the `if !ours { ... continue }` branch |
| WP-74j | `TestWorkerNeverWritesIntoTheUserProfile`, `TestWorkerPrepareSetsNoProfileLinks`, `TestServeWorkerSetsNoProfileLinksEvenWithNilPrepare` (inside the 15-test run) | PASS | 15 PASS |
| WP-74j | `TestWorkerLogIsNotWrittenAnywhere` (`go test ./cmd/unravis-setup -run "TestWorkerLogIsNotWritten\|TestSilentWorker\|TestSilentUpgradeKeeps" -count=1 -v`) | PASS | 4 PASS |
| WP-74j | `Select-String internal/setup/proto.go -Pattern "NoProfileLinks"` | PASS | 2 matches, both assignments `= true` (`proto.go:148`, `proto.go:196`) |
| WP-74j | `Select-String cmd/unravis-setup/main.go -Pattern "openLog"` | PASS | calls only at lines 237 and 371; `runWorker` spans lines 337 to 362 and calls none |
| WP-75b | `go test ./internal/setup -run "TestUpgradeAsksTheApp\|TestUpgradeTerminates\|TestUpgradeDoesNotTerminate\|TestUpgradeFallsBack\|TestUpgradeWithoutAskQuit\|TestUpgradeDoesNotAskWithoutConsent\|TestUpgradeNeverTouches\|TestPollingIsBounded" -count=1 -v` | PASS | 9 PASS (includes consent-first, bounded poll, other-users, reused-PID) |
| WP-75b | `Select-String internal/setup/plan.go -Pattern "AskQuit\("` | PASS | 1 match (`plan.go:189`, no argument) |
| WP-75b | `Select-String internal/setup/plan.go -Pattern "exec\.Command"` and `-Pattern "Proc\.Terminate\("` | PASS | 0 matches each |

Notes on A5:

1. The phase text names the ACL canary `TestEventsGrantNothingToEveryone`. No test has that name. The ACL allowlist test that exists is `TestEventsGrantWriteOnlyToTheUserSystemAndAdministrators` and it passes. The name in the phase text should be corrected by whoever owns it; this packet does not edit it.
2. WP-70c and WP-73c ask for no match of `newLaunchCode\(\)` / `newLaunchCode\(` in `main.go`. The only match is the declaration of the function itself (`main.go:445`). It is reached only through `launchCodeSource` in `startup.go`; `main.go` calls `mintLaunchCode`. The literal "no output" expectation is not met; the intent (no direct call) is met and tested.
3. WP-74i lists `readLinkFile` among the names defined once in `profilelinks.go`. That name does not exist anywhere in `internal/setup` (0 matches); `linkIsOurs`, `linkTarget`, `startupLinkArg` and `quitLinkArg` are each defined once. No test depends on the missing name.

## Automated proof per packet

| packet | automated proof (name or command) | result | numbers |
|---|---|---|---|
| WP-70a | `go test ./cmd/audit-explorer -run "TestStdHandleUsable\|TestUnusableStdHandles\|TestAttachConsoleWith" -count=1 -v` | PASS | 6 PASS |
| WP-70b | `go test ./internal/applog -run "TestTee\|TestOpenStillLogsWhenStderrIsBroken\|TestEphemeral" -count=1 -v`; `Select-String internal/applog/applog.go -Pattern MultiWriter` | PASS | 10 PASS; 0 MultiWriter matches |
| WP-70c | `go test ./cmd/audit-explorer -run "TestPrintStartup\|TestMintLaunchCode\|TestMainCallsTheConsoleShimFirst" -count=1 -v` | PASS | 6 PASS; the `newLaunchCode\(\)` static match is note 2; the hand run of `go run` was not done, the log privacy is covered by A7 |
| WP-70d | `go test ./cmd/audit-explorer -run "TestReportFatal\|TestFatalText\|TestMainUsesReportFatal" -count=1 -v`; `Select-String cmd/audit-explorer/main.go -Pattern "log\.Fatal"` | PASS | 5 PASS (packet text predicted 4); 0 `log.Fatal` matches |
| WP-70e | parse check of `build.ps1` and `run.ps1`; `Select-String` for `windowsgui`, `GuiLdFlags`, `Get-PeSubsystem`, `Start-Process.*-Wait`, `NoNewWindow`, `start "" /wait` | PASS | 0 parse errors in both; `windowsgui` 1 line; `GuiLdFlags` 2 lines (definition and the `unravis.exe` build); `Get-PeSubsystem` 5 lines (definition and call sites for app, keeper, black box and setup); `run.ps1` `-Wait` 1 match, `NoNewWindow` 0; `Start Live.cmd` `/wait` 1 match |
| WP-70f | `go test ./cmd/audit-explorer -run "TestGuiBuild" -count=1 -v`; the same with `-short` | PASS | 3 PASS; with `-short` 3 SKIP, 0 PASS |
| WP-71a | `go test ./internal/tray -run "TestValidate\|TestOptionsValidate" -count=1 -v` | PASS | 7 PASS |
| WP-71b | `go test ./internal/tray -run "TestNotifyIconDataSize\|TestFillTip\|TestExeIcon" -count=1 -v` | PASS | 4 PASS |
| WP-71c | `go test ./internal/tray -run "TestMenuFlags\|TestBuildMenu\|TestShowMenu" -count=1 -v` | PASS | 4 PASS |
| WP-71d | `go test ./internal/tray -count=2 -v`; static checks in A5 detail | PASS | 60 PASS lines over 2 passes (30 tests each), 0 FAIL |
| WP-72a | `go test ./internal/instance -count=1 -v` | PASS | 30 PASS |
| WP-72b | `go test ./internal/instance -run "TestAcquire\|TestRelease" -count=1 -v` | PASS | 6 PASS |
| WP-72c | `go test ./internal/instance -count=3 -v`; security checks in A5 detail | PASS | 90 PASS lines over 3 passes, 0 FAIL |
| WP-72d | `go test ./internal/server -run "TestRearm" -count=1 -v`; `Select-String internal/server/launch.go -Pattern "log\.(Print\|Printf\|Println)"` | PASS | 6 PASS; 0 log matches |
| WP-73a | `go test ./cmd/audit-explorer -run "TestRunQuit\|TestParseFlagsQuit\|TestParseFlagsBackground\|TestMainRunsQuit" -count=1 -v`; security checks in A5 detail | PASS | 8 PASS |
| WP-73b | `go test ./cmd/audit-explorer -run "TestGate\|TestStartupCode\|TestMainSkipsTheGate\|TestIsElevated" -count=1 -v` | PASS | 13 PASS; `launcher.ts` still contains `no-browser` (A9) |
| WP-73c | `go test ./cmd/audit-explorer -run "TestReopen\|TestSystem32File\|TestMainUsesReo\|TestQuitSignalCancels" -count=1 -v` | PASS | 13 PASS; static checks in A5 detail |
| WP-73d | `go test ./cmd/audit-explorer -run "TestTray\|TestOpenLogs\|TestWindowsFile\|TestProductionDeps" -count=1 -v`; `Select-String cmd/audit-explorer/trayapp.go -Pattern "cmd\.exe\|powershell\|/c "` | PASS | 9 PASS; 0 shell matches |
| WP-73e | `go test ./cmd/audit-explorer -run "TestWaitFor\|TestStartTray\|TestStopTray\|TestEndSessionHandler\|TestMainClosesRunDone\|TestMainStartsTheTray" -count=1 -v` | PASS | 9 PASS |
| WP-74a | `go test ./internal/shelllink -count=1 -v`; `go test ./internal/setup -run TestShortcutRoundTrip -count=1 -v` | PASS | 6 PASS; 1 PASS |
| WP-74b | `go test ./internal/autostart -count=1 -v`; security checks in A5 detail | PASS | 14 PASS (packet text predicted 12) |
| WP-74c | `go test ./internal/autostart -run "TestStartupDir" -count=1 -v`; `Select-String internal/autostart/startupdir_windows_test.go -Pattern UserHomeDir` | PASS | 2 PASS; 0 matches |
| WP-74d | `go test ./internal/setup -run "TestNewLayoutHasDesktop\|TestNewLayoutWithoutDesktop\|TestQuitLink\|TestUserDirsRoundTrip\|TestCheckUserDirs" -count=1 -v` | PASS | 6 PASS |
| WP-74e | `go test ./internal/setup -run "TestDesktopInsideProfile\|TestCaptureDirsKeepsDesktop\|TestOSDirsDesktop" -count=1 -v` | PASS | 3 PASS |
| WP-74f | `go test ./internal/setup -run "TestInstallWritesThe\|TestInstallWithoutADesktop\|TestInstallKeepsAnExistingDesktop\|TestStartupShortcutHas\|TestFailedInstallRemoves\|TestInstallWithoutStartAtSignIn\|TestPlanWithNoProfileLinks\|TestInstallSkipsAJunctioned\|TestInstallAcceptsADesktop" -count=1 -v`; `Select-String internal/setup/plan.go -Pattern "func linkIsOurs\|func linkTarget\|const startupLinkArg"` | PASS | 11 PASS; 0 matches |
| WP-74g | `go test ./internal/setup -run "TestUninstallRemovesDesktop\|TestUninstallToleratesMissing\|TestUninstallKeeps\|TestUninstallAsWorker\|TestDescribeChangesMentions\|TestDescribeChangesWithoutDesktop" -count=1 -v` | PASS | 9 PASS |
| WP-74h | `go test ./internal/setup -run "TestUpgradeKeepsStartAtSignIn\|TestUpgradeTreatsALink\|TestUpgradeKeepsTheSavedValue\|TestRepairRecreatesNothing\|TestFreshInstallStillUsesTheCheckbox\|TestSavedStateRecordsTheDerived\|TestWorkerPlanDoesNotRead" -count=1 -v` | PASS | 8 PASS |
| WP-74i | `go test ./internal/setup -run "TestWriteProfileLinks\|TestRemoveProfileLinks\|TestLinkIsOurs\|TestLinkTarget\|TestRunnerHasNoProfileLinks\|TestIsProfileLink\|TestDeriveStartAtSignIn\|TestApplyProfileLinks" -count=1 -v` | PASS | 27 PASS, 0 SKIP; static check: note 3 |
| WP-74j | the 15-test `internal/setup` run and the 4-test `cmd/unravis-setup` run; `go test ./internal/setup ./cmd/unravis-setup -count=1` | PASS | 15 PASS; 4 PASS; both packages `ok` |
| WP-75a | `go test ./internal/setup -run "TestNewFakeSystemLeavesAskQuitNil\|TestUseAskQuit\|TestFakeTerminateIf\|TestTerminateIf" -count=1 -v`; `Select-String internal/setup/system.go -Pattern "AskQuit func\(\) error"` | PASS | 7 PASS; 1 match (`system.go:199`) |
| WP-75b | the 9-test run and static checks in A5 detail | PASS | 9 PASS |
| WP-75c | `go test ./internal/setup -run "TestAskQuit\|TestNewOSSystem" -count=1 -v`; `Select-String internal/setup/os_windows.go -Pattern "exec\.Command.*unravis"` | PASS | 4 PASS; 0 matches |
| WP-76a | `Select-String` on `README.md`, `MCP.md`, `buildout/plan/installer-and-releases.md` as listed in the packet | PASS | README `Quit Unravis` 2, `--quit` 3, `--background` 2, `administrator` 1; MCP.md `windowless\|console window` 1; plan `Background run` 1, `UnravisApp\.` 1, `detected, not prevented` 1. The `git diff --stat` scope check was not run (the shared working tree holds other changes) |
| WP-76b | `Select-String` on `docs/user/install.md`, `upgrade-and-uninstall.md`, `troubleshooting.md` as listed in the packet | PASS | install `Start Unravis when I sign in` 1, `NOT RUN` 7; upgrade `Quit Unravis` 4, `other Windows accounts` 1, `same Windows account` 1; troubleshooting `4688` 1, `as administrator` 4, `hidden` 1, `--quit` 1. The `git diff --stat` scope check was not run |
| WP-77 | this file: `Test-Path`, one `ACCEPTANCE: ` line as the last line, 15 or more PENDING lines, accepted-risk phrases | see the proof lines in the packet | run by the reviewer after writing |

## Manual

Every row is PENDING. The person who runs a row replaces `NOT RUN` with the date, the account and the observed result. Nothing here was run by the automated pass.

| id | step | expected | result |
|---|---|---|---|
| M1 | Install Unravis with "Start Unravis when I sign in" ticked. Sign out and sign in again (or reboot). | No browser window opens. The Unravis icon appears in the notification area. | PENDING - NOT RUN |
| M2 | With Unravis running, sign out, then (separately) shut down from the Start menu. Sign in again. | Unravis exits cleanly; no leftover icon; `session.json` is not left in the data folder. | PENDING - NOT RUN |
| M3 | With Unravis running, in Task Manager restart "Windows Explorer". | The Unravis icon comes back in the notification area. | PENDING - NOT RUN |
| M4 | Start `unravis.exe`. Left-click the tray icon once. | Exactly one browser tab opens on the Unravis page. | PENDING - NOT RUN |
| M5 | With Unravis running, start `unravis.exe` a second time (Desktop shortcut). In Task Manager look for `unravis.exe`. | A tab opens in the running copy; no second `unravis.exe` process remains. | PENDING - NOT RUN |
| M6 | Occupy a port (for example `python -m http.server 8123` in another window), then run `unravis.exe --port 8123` from the Start menu or a shortcut copy. | A clear message box says the port is in use; Unravis does not exit silently. | PENDING - NOT RUN |
| M7 | Configure an MCP client with command `unravis.exe` and argument `--mcp`. Ask it to list sources. | The client gets answers; no console window flashes. | PENDING - NOT RUN |
| M8 | Install an older build, start it, then run the newer `unravis-setup.exe` with Unravis still open. Give consent to quit when asked. | The consent prompt appears, the running app quits, setup finishes, the new version starts. | PENDING - NOT RUN |
| M9 | After a normal install, and after an install that asked for administrator approval, look at the Desktop, Start menu (Unravis and "Quit Unravis") and `shell:startup`. Uninstall, check again. Run repair, check again. | After install: Desktop, Start-menu, Quit and Startup shortcuts exist. After uninstall: the shortcuts Unravis made are gone. After repair: they are back. | PENDING - NOT RUN |
| M10 | Copy a release `unravis.exe` to a PC with Defender and SmartScreen on and run it. | Record any Defender or SmartScreen warning for the windowless exe and what the person had to do. | PENDING - NOT RUN |
| M11 | With Unravis running under account A, start `unravis.exe` under a second Windows account B. | B sees the "another account" message and the text names "another Windows account or as administrator". | PENDING - NOT RUN |
| M12 | Double-click the release `unravis.exe` in Explorer. | No console window appears at any moment. | PENDING - NOT RUN |
| M13 | Choose Quit from the tray menu. Check the notification area and the data folder. | The icon is gone at once and `session.json` is removed. | PENDING - NOT RUN |
| M14 | Right-click `unravis.exe`, "Run as administrator", while a normal copy is closed. Then start a normal copy. | The elevated copy shows the refusal message; the normal copy starts and is not blocked. | PENDING - NOT RUN |
| M15 | From an elevated helper under another account, create the Unravis mutex or event names, then start Unravis. | Unravis reports "another process holds Unravis's name" and exits. | PENDING - NOT RUN |
| M16 | Run `unravis.exe --quit` while Unravis is running; then run it again with nothing running. | First call ends the running copy and the icon disappears; second call exits without a crash or a window. | PENDING - NOT RUN |
| M17 | Open the Start menu and choose "Quit Unravis" while Unravis is running. | The running copy ends and the icon disappears. | PENDING - NOT RUN |
| M18 | Right-click the tray icon, tick "Start Unravis when I sign in", look in `shell:startup`, untick it, look again. Do it twice in a row. | `Unravis.lnk` appears when ticked and is removed when unticked; the tick state always matches the folder. | PENDING - NOT RUN |
| M19 | Open Unravis, close every browser tab, wait, then left-click the tray icon. | The process and the icon stay alive after the last tab closes; the click opens a new tab. | PENDING - NOT RUN |
| M20 | Click the tray icon five times quickly; start `unravis.exe` twice quickly. | No flood of browsers or processes; at most the throttled number of tabs opens; one copy remains. | PENDING - NOT RUN |
| M21 | Choose "Open log folder" from the tray menu. | A file window opens on the log folder; no console window appears. | PENDING - NOT RUN |
| M22 | Delete `Unravis.lnk` from `shell:startup` by hand, then run the upgrade. | The sign-in setting stays off and the shortcut is not recreated. | PENDING - NOT RUN |
| M23 | Install over an existing install whose Startup shortcut lacks `--background`, with the sign-in box left on. | The Startup shortcut is rewritten and now carries `--background`; at the next sign-in no browser opens. | PENDING - NOT RUN |

Accepted risk: any process of the same user at the same or higher integrity level can send the open or quit signal. Open re-arms a new launch code, so the previous link stops working, and each accepted open starts rundll32.exe, which adds a process-creation event. Quit ends the app. This is accepted: such a process already controls the user's session. The session token stays protected by the one-time code.

ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING
