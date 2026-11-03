# WP-73b Single-instance gate at start-up
risk: medium    size: M    wave: 5    model: sonnet
## Outcome
Starting `unravis.exe` a second time no longer starts a second server: the second copy asks the first to open a tab and exits 0. A `--background` start that finds a running copy exits silently. An elevated ("Run as administrator") copy is refused with one clear message before it touches the mutex, so it can never take the name and lock a normal start out. A name already held by an object that another account owns (a squatter, WP-72a/72b/72c) is reported with one clear message and the app exits. A `--background` start arms no launch code and prints no link; a code is minted only when someone asks to open (tray or second launch, WP-73c). The gate applies only when the app is interactive (Windows and not `--no-browser`), so the e2e launcher (`--demo --no-browser --port 0`) behaves exactly as before.

## Write scope
- `cmd/audit-explorer/gate.go` (new)
- `cmd/audit-explorer/gate_windows.go` (new)
- `cmd/audit-explorer/gate_other.go` (new)
- `cmd/audit-explorer/gate_test.go` (new)
- `cmd/audit-explorer/gate_windows_test.go` (new)
- `cmd/audit-explorer/main.go`

## Must not change
- Behaviour with `--no-browser`: no mutex, no events, no tray. `--mcp`, `--version` and `--quit` run before the gate.
- Every file outside the write scope. The browser-open and launch-link behaviour of a normal (not `--background`) start stays as WP-70c left it.

## Changes
1. `gate.go`:
   - Errors (text is the user-facing message): `ErrAlreadyRunning` = "Unravis is already running but could not be reached. Quit it from the tray icon or Task Manager and start it again."; `ErrRunningForAnotherAccount` = "Unravis is already running under another Windows account or as administrator. Quit it there, then start Unravis again."; `ErrElevated` = "Unravis does not run as administrator. Start it from the Start menu or a normal window."; `ErrNameHeld` = "Another process holds Unravis's name, so Unravis cannot start. Quit the other program or sign out of Windows and in again, then start Unravis again."
   - `type gateDeps struct { names func(app string) (instance.Names, error); acquire func(instance.Names) (func(), bool, error); signal func(instance.Names, instance.Kind) error; sleep func(time.Duration); elevated func() bool }` and `type gateResult struct { release func(); names instance.Names; proceed bool }`.
   - `func enterSingleInstance(demo, background bool, d gateDeps) (gateResult, error)`. App is `instance.DemoApp` when `demo`, else `instance.DefaultApp`. First step, before any name or mutex is touched: if `d.elevated()` return `ErrElevated` (`acquire`, `signal` and `names` are not called). Then a first copy returns `proceed=true` with release and names. Not first and `background`: `proceed=false`, nil error, no signal. Not first otherwise: signal `Open` up to 20 times with `d.sleep(250*time.Millisecond)` between tries, retrying only on `ErrNoInstance` (the first copy owns the mutex but has not created its events yet); success -> `proceed=false`; `ErrAccessDenied` -> `ErrRunningForAnotherAccount`; 20 tries of `ErrNoInstance` -> `ErrAlreadyRunning`. `acquire` returning `ErrAccessDenied` -> `ErrRunningForAnotherAccount`. `instance.ErrNameHeldByOther` from `acquire` or from `signal` -> `ErrNameHeld` (no retry, no signal sent). Other errors pass through. Production deps: `instance.CurrentNames`, `instance.Acquire`, `instance.Signal`, `time.Sleep`, `isElevated`. The 20 x 250 ms is a retry budget; tests use a fake `sleep`.
   - `func startupCode(background bool, mint func() (string, error)) (string, error)`: returns `"", nil` without calling `mint` when `background`, else `mint()`.
2. `gate_windows.go` (`//go:build windows`): `func isElevated() bool` is true only when the process token is elevated and its `TokenElevationType` is full (3, "Run as administrator"). Where UAC is off the type is "default", so such a user is not locked out. `gate_other.go` (`//go:build !windows`): `func isElevated() bool { return false }`.
3. `main.go`: `interactive := !f.noBrowser && hostOS == "windows"`, `background := f.background`, `openBrowser := interactive && !background`. When `interactive`, `enterSingleInstance` runs after the `--mcp` branch and before the session token and listener exist; `proceed == false` returns nil from `run` (exit 0); an error returns to `main`, which reports it through `reportFatal`; `result.release` is deferred and `result.names` is kept for WP-73c. Replace the `mintLaunchCode()` call WP-70c left by `code, err := startupCode(background, mintLaunchCode)`; add `server.WithLaunchCode(...)` only when `code != ""`. The startup report is: `code == ""` -> one `log.Printf("%s running in the tray at %s (demo=%t)", version.Short(), localURL, *demo)` and no link line; otherwise `printStartup` exactly as WP-70c wrote it. The `launch` log field is then `none` for a background start by WP-70c's own rule.
4. Tests in `gate_test.go` (fakes only, no real mutex or event): `TestGateFirstCopyProceeds`, `TestGateSecondCopySignalsOpenAndExitsZero`, `TestGateBackgroundSecondCopyExitsSilently` (the fake signal is never called), `TestGateRetriesWhileTheFirstCopyIsStarting` (`ErrNoInstance` 3 times then nil; sleep called 3 times), `TestGateGivesUpWithErrAlreadyRunning`, `TestGateAnotherAccountIsReportedClearly` (from `acquire` and from `signal`; text contains "or as administrator"), `TestGateUsesTheDemoNamesForDemo`, `TestGateRefusesWhenElevatedBeforeTouchingTheMutex` (elevated: `ErrElevated`, `proceed=false`, and the fake `names`, `acquire` and `signal` are never called), `TestGateReportsASquattedName` (`ErrNameHeldByOther` from `acquire` and from `signal` gives `ErrNameHeld`, whose text contains "holds Unravis's name"; `signal` is not retried), `TestStartupCodeIsEmptyInBackground` (mint never called), `TestStartupCodeMintsOtherwise`, `TestMainSkipsTheGateWithNoBrowser` (reads `main.go` as text: the call to `enterSingleInstance` sits inside an `if interactive` block). `gate_windows_test.go`: `TestIsElevatedAgreesWithTheTokenFlag` (when `windows.GetCurrentProcessToken().IsElevated()` is false, `isElevated()` is false).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestGate|TestStartupCode|TestMainSkipsTheGate|TestIsElevated" -count=1 -v` -> 13 tests `PASS` (12 in `gate_test.go` and 1 in `gate_windows_test.go`).
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- Regression for the e2e launcher: `Select-String -Path tools/e2e/support/launcher.ts -Pattern "no-browser"` -> still present, and `TestMainSkipsTheGateWithNoBrowser` passes.
- `Select-String -Path cmd/audit-explorer/main.go -Pattern "newLaunchCode|mintLaunchCode"` -> `mintLaunchCode` only as the argument of `startupCode` (and WP-73c's `reo`), never called directly at start-up.

## Depends on
WP-70c, WP-72a, WP-72b, WP-73a
