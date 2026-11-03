# Phase 07: background-run

risk: high (a windowless app with a tray icon; the quit route, the open/quit signals, the autostart shortcut and terminate-on-upgrade are security-sensitive)

## Goal

`unravis.exe` runs without a terminal: it is built as a windowless program (owner answer 1, approach A), shows a tray icon, writes a log file, and allows only one copy per user. A second start opens a browser tab in the running copy. After the last tab closes it keeps running until the user quits (tray menu, `unravis.exe --quit`, or the Start-menu entry "Quit Unravis"). Start at sign-in is a setting: an installer checkbox (default on) and a checkable tray item that creates or removes the Startup shortcut at run time; the presence of that shortcut is the one source of truth. The installer adds a Desktop shortcut for the installing user and a "Quit Unravis" Start-menu entry. When setup runs elevated, the worker never writes or deletes the Desktop or Startup shortcut in the user's profile: the non-elevated setup window does that after the worker commits (WP-74i, WP-74j); uninstall removes them, repair brings them back, a failed install leaves none of them, and an upgrade asks the running app to quit before it terminates anything. `--version` and `--mcp` stay in the same exe and keep working over pipes. With `--no-browser` (the e2e launcher) nothing changes: no guard, no tray.

## Packets

| Packet | Title |
|---|---|
| WP-70a | Console shim for a windowless build |
| WP-70b | Log tee that survives a missing console |
| WP-70c | Startup output and console shim wiring |
| WP-70d | Fatal start-up errors that a windowless build can show |
| WP-70e | Build unravis.exe as a windowless program |
| WP-70f | Test that the windowless build still speaks over pipes |
| WP-71a | Tray package API and validation |
| WP-71b | Notification-area icon calls |
| WP-71c | Tray popup menu |
| WP-71d | Tray message loop and hidden window |
| WP-72a | Single-instance names and signal kinds |
| WP-72b | Single-instance guard |
| WP-72c | Open and quit signals between copies |
| WP-72d | Re-arm the launch code of a running server |
| WP-73a | The --quit and --background flags |
| WP-73b | Single-instance gate at start-up |
| WP-73c | Reopen and quit wiring |
| WP-73d | Tray menu and dispatch |
| WP-73e | Tray start and clean shutdown |
| WP-74a | Shortcut writer in its own package |
| WP-74b | Start at sign-in shortcut logic |
| WP-74c | Find the user's Startup folder |
| WP-74d | Desktop folder and new link paths in the installer layout |
| WP-74e | Read the Desktop folder from Windows |
| WP-74f | Install writes the Quit shortcut, and the Desktop and sign-in shortcuts when it runs as the user |
| WP-74g | Uninstall removes only the shortcuts it made, and says so |
| WP-74h | Upgrade and repair keep the sign-in setting the user chose |
| WP-74i | Profile-link helpers: links inside the user's profile are the user's own process's work |
| WP-74j | The setup window and silent mode write the profile links after the elevated worker commits |
| WP-75a | Installer seams: ask Unravis to quit, and a verified terminate |
| WP-75b | Upgrade asks Unravis to quit before it terminates it |
| WP-75c | Real AskQuit for the Windows installer |
| WP-76a | README, MCP guide and installer plan for background run |
| WP-76b | User guides for install, upgrade, uninstall and troubleshooting |
| WP-77 | Background-run acceptance record |

The groups are: 70 console, build and log; 71 the tray package; 72 the instance package and the launch-code re-arm; 73 the app wiring in `cmd/audit-explorer/main.go` (one chain, 70c, 70d, 73a, 73b, 73c, 73e, so edits to `main.go` never overlap); 74 shortcuts and autostart (74i and 74j keep the elevated worker out of the user's profile); 75 quit on upgrade; 76 and 77 docs and acceptance. Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

## Phase proof (repo root, PowerShell, Go environment as in WP-58 step 3)

- `go build ./...`, `go vet ./...` -> no output; `go test ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink ./internal/applog ./internal/server ./internal/setup ./cmd/audit-explorer -count=1` -> all `ok`.
- `go test ./cmd/audit-explorer -run TestGuiBuild -count=1 -v` -> 3 `PASS`: GUI subsystem, `--version` over a pipe, MCP stdio round trip (owner answer 5).
- Other-OS compile: `$env:GOOS='linux'; go vet ./cmd/audit-explorer ./internal/applog ./internal/setup ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink; Remove-Item Env:GOOS` -> no output.
- `build.ps1` is NOT run (it rewrites `cmd/unravis-setup/payload`); static proof only: parse check plus `Select-String` for `windowsgui`.
- The e2e launcher still works unchanged: `Select-String -Path tools/e2e/support/launcher.ts -Pattern "no-browser"` matches, and the main Playwright run is part of the WP-58 gate.
- `git ls-files -- "*.unravis.db"` -> no output.
- WP-77 writes `docs/background-run-acceptance.md`, which ends with `ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING` when every automated row passes.

## Additions to plan.md section 7

- Browser: not applicable (no web change). Manual desktop checks are the "Manual" rows of WP-77 (sign-in start, logoff, Explorer restart, single left click, second launch, port taken, MCP spawn, upgrade while running, uninstall and repair, Defender and SmartScreen, second account, no console window, icon gone after Quit, elevated copy refused). They stay `PENDING` until a person runs them; this phase does not fail on a pending manual row.
- Security canaries: the launch link and its one-time code never appear in a log file (`TestPrintStartupKeepsTheLaunchLinkOutOfTheLog`, `TestReopenNeverLogsTheCode`); the open and quit events grant nothing to Everyone, Authenticated Users or Users (`TestEventsGrantNothingToEveryone`); the quit route runs no program; the autostart shortcut can only point at an installed `unravis.exe`; setup never starts the old exe to ask it to quit; the elevated worker never writes into the user's Desktop or Startup folder (`TestWorkerNeverWritesIntoTheUserProfile`); an object name held by another account is detected, not prevented (`TestAcquireDetectsAMutexOwnedBySomeoneElse`).
- Performance: no hard timing assertions (INV-5). The 20 x 250 ms signal retries, the 20 x 250 ms upgrade poll, the 30 second hang guard of `TestGuiBuildMcpStdioRoundTrip` and the 2 second re-open throttle are retry budgets, guards or product rules; tests use fake clocks and sleeps, and no test checks how long anything took.
- Clumsy-user review: start the exe twice quickly; click the tray icon five times; choose Quit while a tab is open; toggle start at sign-in twice in a row; run `--quit` with nothing running; upgrade while the app is open; delete `Unravis.lnk` by hand and then upgrade (the setting stays off).

## Commit (one commit for the phase, after the section 7 stop; never push)

- `cmd/audit-explorer/` (all new files of this phase and `main.go`)
- `cmd/unravis-setup/main.go`, `cmd/unravis-setup/main_test.go` (one expectation) and the new `cmd/unravis-setup/profile_links_test.go` (WP-74j: silent mode and the worker log)
- `internal/tray/`, `internal/instance/`, `internal/autostart/`, `internal/shelllink/`
- `internal/applog/tee.go`, `internal/applog/tee_test.go`, `internal/applog/applog.go`, `internal/applog/sanitize.go`, `internal/applog/ephemeral_test.go`
- `internal/server/launch.go`, `internal/server/launch_rearm_test.go`
- `internal/setup/lnk.go`, `layout.go`, `runner.go`, `profilelinks.go`, `profilelinks_apply.go`, `proto.go`, `plan.go`, `system.go`, `fake.go`, `os_windows.go`, `capture.go`, `uninstall.go`, `uiserver.go`, `helpers_test.go`, `setup_test.go`, `fixes_b_test.go` and the new test files of WP-74d to WP-75c (layout_desktop, desktop_dirs, desktop_dirs_windows, plan_shortcuts, plan_shortcuts_windows, profilelinks, profilelinks_apply, profilelinks_windows, worker_profile_links, uninstall_shortcuts, plan_signin, askquit_fake, terminateif_windows, plan_askquit, askquit_windows)
- `build.ps1`, `run.ps1`, `Start Live.cmd`
- `README.md`, `MCP.md`, `buildout/plan/installer-and-releases.md`, `docs/user/install.md`, `docs/user/upgrade-and-uninstall.md`, `docs/user/troubleshooting.md`, `docs/background-run-acceptance.md`
