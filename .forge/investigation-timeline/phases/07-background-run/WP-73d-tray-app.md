# WP-73d Tray menu and dispatch
risk: medium    size: M    wave: 4    model: sonnet
## Outcome
The app has the tray menu and its dispatch as plain, testable functions: "Open Unravis", "Open log folder", "Start at sign-in" (a tick when on; only shown for an installed copy) and "Quit Unravis". Starting the tray and tying it to the app's life is WP-73e; this packet touches no `main.go`. A failing start-at-sign-in change shows a message on its own goroutine, never on the tray thread.

## Write scope
- `cmd/audit-explorer/trayapp.go` (new)
- `cmd/audit-explorer/trayapp_test.go` (new)

## Must not change
- The tray package (`internal/tray`), the autostart package and the instance package; use them through their exported API only.
- `main.go` and every file outside the write scope.

## Changes
1. `trayapp.go`:
   - Constants `idOpen = 1`, `idLogs = 2`, `idAutostart = 3`, `idQuit = 4`.
   - `type trayDeps struct { reopen func(); quit func(); openLogs func(); autostartAllowed func() bool; autostartEnabled func() bool; setAutostart func(on bool) error; warn func(title, text string) }`.
   - `func trayMenu(d trayDeps) []tray.Item`: "Open Unravis", "Open log folder", "Start at sign-in" (only when `autostartAllowed()`, `Checked` from `autostartEnabled()`), a separator, "Quit Unravis".
   - `func trayOnSelect(d trayDeps) func(uint32)`: Open -> `reopen`; logs -> `openLogs`; autostart -> `setAutostart(!autostartEnabled())` and on error `go d.warn("Unravis", "Could not change start at sign-in: " + err.Error())`; Quit -> `quit`; any other ID does nothing.
   - `func productionTrayDeps(reopen, quit func(), warn func(title, text string)) trayDeps`: `openLogs` calls an injectable `runProgram func(name string, arg string) error` (production: `exec.Command(name, arg).Start()`) with `windowsFile("explorer.exe")` and `applog.DefaultDir(os.Getenv)`, no shell and no other argument (`func windowsFile(name string) string` is `filepath.Join(root, name)` where `root` is `os.Getenv("SystemRoot")`, or `C:\Windows` when that is empty, so `explorer.exe` is never looked up through `PATH` or the current folder); the autostart functions use `exe` from `os.Executable()` and `dir` from `autostart.StartupDir()`: `autostart.Allowed(exe)`, `autostart.Enabled(dir, exe)`, `autostart.Enable(dir, exe)` and `autostart.Disable(dir, exe)`; a `StartupDir` or `os.Executable` error makes `autostartAllowed` false. Switching on over, or off for, a shortcut that is not ours (`autostart.ErrNotOurs`, WP-74b) is reported through the same `warn` path as any other toggle error.
2. Tests in `trayapp_test.go` (fakes only):
   - `TestTrayMenuHasTheFourEntries` (autostart allowed): text and order are exactly "Open Unravis", "Open log folder", "Start at sign-in", separator, "Quit Unravis"; the result passes `tray.ValidateItems`.
   - `TestTrayMenuHidesStartAtSignInForAnUninstalledCopy`.
   - `TestTrayMenuTicksStartAtSignInWhenEnabled`.
   - `TestTrayOnSelectDispatches` (table over the four IDs plus an unknown ID that does nothing).
   - `TestTrayToggleTurnsAutostartOnThenOff` (`setAutostart` receives true, then false).
   - `TestTrayToggleErrorWarnsWithoutBlockingTheTray` (a `warn` that blocks until released: `trayOnSelect` returns first; the warn text contains the error text; nothing panics).
   - `TestOpenLogsRunsExplorerWithTheLogFolder` (the fake runner receives `windowsFile("explorer.exe")` and the folder, nothing else).
   - `TestWindowsFileUsesTheWindowsFolder` (`t.Setenv("SystemRoot", `C:\X`)` gives `C:\X\explorer.exe`; empty gives `C:\Windows\explorer.exe`).
   - `TestProductionDepsHideStartAtSignInForATestBinary` (the test binary has no `setup-state.json` beside it, so `autostartAllowed()` is false).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestTray|TestOpenLogs|TestWindowsFile|TestProductionDeps" -count=1 -v` -> 9 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `go build ./...` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- `Select-String -Path cmd/audit-explorer/trayapp.go -Pattern "cmd\.exe|powershell|/c "` -> no output (no shell).

## Depends on
WP-71a, WP-74b, WP-74c
