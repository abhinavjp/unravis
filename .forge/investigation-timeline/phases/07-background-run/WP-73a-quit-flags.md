# WP-73a The --quit and --background flags
risk: high    size: M    wave: 4    model: sonnet
## Outcome
`unravis.exe --quit` asks the running copy of the same user to stop and exits 0 (also when nothing is running, with a plain "Unravis is not running." message). `unravis.exe --background` is the start-at-sign-in form: tray and single instance, no browser. Both flags are added to the existing `parseFlags` and validated there; the single-instance behaviour of `--background` itself is wired in WP-73b.

## Write scope
- `cmd/audit-explorer/quit.go` (new)
- `cmd/audit-explorer/quit_test.go` (new)
- `cmd/audit-explorer/main.go`

## Must not change
- Existing flags and their meaning, `--version` (it still wins over every other flag), `--mcp`, the existing `parseFlags` tests in `main_test.go` (they pass unchanged), the server and installer code.
- `--quit` never starts a server, opens a browser, creates a window or writes the log directory; it only signals and exits.

## Changes
1. `main.go`, extend the existing `parseFlags` (do not add a second validator): `runFlags` gets `quit` and `background` booleans; register `--quit` ("ask the running Unravis to quit") and `--background` ("start in the tray without opening a browser; used by start at sign-in"). After the `--version` early return and the positional-argument check add, in this order:
   - `f.quit && flags.NFlag() > 1` -> error `--quit cannot be combined with other options`.
   - `f.background && hostOS != "windows"` -> error `--background is only available on Windows`.
   - `f.background && f.noBrowser` -> error `--background and --no-browser cannot be combined`.
   - `f.background && f.mcp` -> error `--background starts the dashboard; do not combine it with --mcp`.
2. `quit.go`: `var hostOS = runtime.GOOS` (tests swap it) and `func runQuit(w io.Writer, signal func(instance.Kind) error) int` returning the exit code. `signal(instance.Quit)` result: nil -> prints nothing, shows nothing, returns 0. `instance.ErrNoInstance` -> text `Unravis is not running.`, returns 0. `instance.ErrAccessDenied` -> text `Unravis is running under another Windows account or as administrator. Quit it from there.`, returns 1. Any other error -> text `Unravis could not be asked to quit: <error>`, returns 1. For every non-nil case the text goes to `w` with a newline, and when `stderrUnusable()` (WP-70d) is true it goes to `showMessage("Unravis", text)` instead of `w`. The box therefore appears only when there is something to say and no console.
3. `main.go`, in `run`, directly after the `f.version` branch and before `demoEnv`: `if f.quit { if code := runQuit(os.Stderr, quitSignal); code != 0 { exitProcess(code) }; return nil }`, where `quitSignal` is defined in `quit.go` as `func(k instance.Kind) error { n, err := instance.CurrentNames(instance.DefaultApp); if err != nil { return err }; return instance.Signal(n, k) }`. `--demo` cannot be combined with `--quit` (rule 1), so only the default names are used.
4. Tests in `quit_test.go` (the `signal` argument is a fake; `showMessage`, `stderrUnusable` and `hostOS` are overridden and restored with `t.Cleanup`, so no dialog opens and no real event is signalled):
   - `TestRunQuitSuccessPrintsNothingAndShowsNoBox` (code 0, empty writer, `showMessage` not called, with `stderrUnusable` both true and false).
   - `TestRunQuitWhenNothingRunsExitsCleanly` (code 0, writer holds `Unravis is not running.`).
   - `TestRunQuitReportsAnotherAccountOrAdministrator` (code 1, text contains `another Windows account or as administrator`).
   - `TestRunQuitPropagatesOtherErrors` (code 1, text contains the fake error).
   - `TestRunQuitShowsABoxOnlyWhenThereIsNoConsole` (`stderrUnusable` true: the box gets the text and the writer stays empty; false: the writer gets it and no box).
   - `TestParseFlagsQuitStandsAlone` (`--quit --demo`, `--quit --port 1`, `--quit --background`, `--quit --no-browser` rejected; `--quit` alone accepted; `--quit --version` accepted because version wins).
   - `TestParseFlagsBackgroundRules` (`--background --no-browser` and `--background --mcp` rejected; `--background` alone accepted with `hostOS="windows"` and rejected with `hostOS="linux"`).
   - `TestMainRunsQuitBeforeServerSetup` (reads `main.go` as text: `runQuit(` appears before `demoEnv(` inside `run`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestRunQuit|TestParseFlagsQuit|TestParseFlagsBackground|TestMainRunsQuit" -count=1 -v` -> 8 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- Security acceptance: (a) `Select-String -Path cmd/audit-explorer/quit.go -Pattern "os/exec|exec\.Command"` -> no output (the quit route runs nothing); (b) the only input the quit route sends is one of the two fixed `instance.Kind` values; (c) `Select-String -Path cmd/audit-explorer/main.go -Pattern "validateFlags"` -> no output.

## Depends on
WP-70d, WP-72a, WP-72c
