# WP-75c Real AskQuit for the Windows installer
risk: medium    size: S    wave: 3    model: sonnet
## Outcome
On Windows the installer's `AskQuit` seam sends the quit signal to the running Unravis of the same user, through `instance.Signal` with the SID-scoped names of that user. When nothing is running or the signal cannot be sent, it returns an error and the upgrade falls back to terminating (WP-75b).

## Write scope
- `internal/setup/os_windows.go`
- `internal/setup/askquit_windows_test.go` (new)

## Must not change
- The other seams in `NewOSSystem`, `osProcesses` (WP-75a), `OSDirs` (WP-74e) and every other file. No exe is started.
- `instance.ErrNoInstance` (or `ErrAccessDenied`) from a different account is expected and safe: `askQuitFor` builds its names from the token of the process that runs it, so when an elevated worker runs as another administrator than the window's user the names differ and the quit signal never reaches the user's app. Setup then falls through to the verified `TerminateIf` path (WP-75b). A graceful quit therefore only happens when the window and the worker are the same account. Do not add a cross-account signal and do not "fix" this.

## Changes
1. `os_windows.go`: `func askQuitFor(names instance.Names) func() error` returns a function that calls `instance.Signal(names, instance.Quit)` and returns its error unchanged (`instance.ErrNoInstance` means there was nothing to ask; the caller in WP-75b logs it and goes on). In `NewOSSystem` set `AskQuit = askQuitFor(names)` where `names, err := instance.CurrentNames(instance.DefaultApp)`; if `err != nil`, leave `AskQuit` nil.
2. `askquit_windows_test.go` (first line `//go:build windows`). The tests build their own names with `instance.NewNames("UnravisTest"+<8 random hex digits>, "S-1-5-21-1-2-3")`, so they never signal a real running Unravis: `TestAskQuitSignalsTheQuitEvent` (an `instance.Listen` on those names receives the quit callback), `TestAskQuitWithNoListenerReturnsAnError` (`errors.Is(err, instance.ErrNoInstance)`), `TestNewOSSystemWiresAskQuit` (the field is non-nil), `TestNewOSSystemDoesNotUseTheDemoNames` (reads `os_windows.go` as text: it references `instance.DefaultApp` and not `instance.DemoApp`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestAskQuit|TestNewOSSystem" -count=1 -v` -> 4 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output; `go build ./...` -> no output.
- `Select-String -Path internal/setup/os_windows.go -Pattern "exec\.Command.*unravis"` -> no output.

## Depends on
WP-72a, WP-72c, WP-75a, WP-74e
