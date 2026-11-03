# WP-77 Background-run acceptance record
risk: low    size: S    wave: 9    model: sonnet
## Outcome
One file, `docs/background-run-acceptance.md`, records Phase 07 acceptance: the automated checks with PASS or FAIL and the numbers they printed, and the manual checks that need a real desktop, a second account or a clean VM, each marked PENDING until a person runs them. Its last line is the verdict the main-suite gate (WP-58) reads.

## Write scope
- `docs/background-run-acceptance.md` (new)

## Must not change
- Every file outside the write scope. A failing command is recorded (command, exit status, first failing lines) and not fixed here.
- No manual row is ever marked PASS by this packet: an item that needs a person stays `PENDING` and the verdict says so.
- `build.ps1` is NOT run (it rewrites `cmd/unravis-setup/payload`). No hard timing assertion appears in any row.

## Changes
1. Create the file with a table "Automated" (columns: id, check, command, result, numbers). Run each command from the repo root with the Go environment of WP-58 step 3 and fill in `PASS` or `FAIL` plus the printed numbers:
   - A1 `go build ./...` and `go vet ./...` exit 0.
   - A2 `go test ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink ./internal/applog ./internal/server ./internal/setup ./cmd/audit-explorer -count=1` all `ok`.
   - A3 `go test ./cmd/audit-explorer -run "TestGuiBuild" -count=1 -v` shows 3 `PASS` (GUI subsystem, `--version` over a pipe, MCP stdio round trip).
   - A4 `$env:GOOS='linux'; go vet ./cmd/audit-explorer ./internal/applog ./internal/setup ./internal/tray ./internal/instance ./internal/autostart ./internal/shelllink` exit 0.
   - A5 the security checks of the high-risk packets re-run as listed in their Proof sections (WP-71d, WP-72a, WP-72b, WP-72c, WP-73a, WP-73c, WP-74b, WP-74g, WP-74j, WP-75b): record each as PASS or FAIL.
   - A6 `Select-String -Path build.ps1 -Pattern "windowsgui"` shows the app build uses the flag and the keeper and black-box builds do not.
   - A7 the launch link stays out of the log: `TestPrintStartupKeepsTheLaunchLinkOutOfTheLog` and `TestReopenNeverLogsTheCode` pass.
2. A second table "Manual" (columns: id, step, expected, result) with every row `PENDING` and a `NOT RUN` note for the person to replace: M1 start at sign-in (reboot or sign out and in, no browser opens, the icon appears); M2 sign out and shut down while Unravis runs (it exits cleanly, no leftover icon); M3 restart Explorer (the icon comes back); M4 a single left click on the icon opens exactly one tab; M5 starting Unravis a second time opens a tab in the running copy and no second process appears; M6 start with `--port <a port another program already uses>` (the default `--port 0` can never be busy): a clear message box, no silent exit; M7 an MCP client spawns `unravis.exe --mcp` and gets answers; M8 upgrade while running (consent prompt, app quits, new version starts); M9 after install (also an install that asked for administrator approval) the Desktop, Start-menu, Quit and Startup shortcuts exist, uninstall removes them and repair brings them back; M10 Defender and SmartScreen behaviour for the windowless exe; M11 a second Windows account starting Unravis while the first runs gets the "another account" message, and the text names "another Windows account or as administrator"; M12 double-click of `unravis.exe` shows no console window; M13 after Quit from the tray the icon is gone at once and `session.json` is removed; M14 a copy started with Run as administrator shows the refusal message and the normal copy is not blocked; M15 a process of another account that already holds Unravis's object names is reported as "another process holds Unravis's name" and Unravis exits (needs an elevated helper to create the objects).
   Accepted risk to record under the tables, not as a row (the sentence is identical to the one in WP-71d, word for word): "Accepted risk: any process of the same user at the same or higher integrity level can send the open or quit signal. Open re-arms a new launch code, so the previous link stops working, and each accepted open starts rundll32.exe, which adds a process-creation event. Quit ends the app. This is accepted: such a process already controls the user's session. The session token stays protected by the one-time code."
3. End the file with exactly one verdict line as the last line: `ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING` when every Automated row is PASS, or `ACCEPTANCE: AUTOMATED FAIL: A<n>` naming the first failing row. WP-58 reads this line (its step 10a).

## Proof
- `Test-Path docs/background-run-acceptance.md` -> `True`; `Select-String -Path docs/background-run-acceptance.md -Pattern "^ACCEPTANCE: "` -> one match, on the last line.
- `Select-String -Path docs/background-run-acceptance.md -Pattern "PENDING"` -> 15 matches or more (one per manual row); no manual row says PASS.
- The accepted-risk sentence is the same as in WP-71d: `Select-String -Path docs/background-run-acceptance.md -Pattern "same or higher integrity level"` -> one match and `Select-String -Path docs/background-run-acceptance.md -Pattern "starts rundll32.exe"` -> one match; `Select-String -Path docs/background-run-acceptance.md -Pattern "same integrity level"` -> no output; the same two phrases match in `internal/tray/tray_windows.go` (read-only check).
- `git status --short -- docs/background-run-acceptance.md` -> only `?? docs/background-run-acceptance.md`.

## Depends on
WP-76a, WP-76b, WP-70f, WP-75c
