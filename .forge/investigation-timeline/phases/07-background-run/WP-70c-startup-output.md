# WP-70c Startup output and console shim wiring
risk: medium    size: M    wave: 2    model: sonnet
## Outcome
`unravis.exe` calls the console shim first thing, logs through the tolerant tee, registers every launch code it mints with the log sanitizer (through the bounded `applog.RegisterEphemeralSecret` of WP-70b, so a long-running app never grows the secret list), and prints its startup lines through one small function that keeps the launch link (which carries the one-time code) out of the log file. The link goes to the console only; the log file gets the "listening" line and, when the browser was opened, the "opened in your browser" line. Behaviour in a terminal is unchanged.

## Write scope
- `cmd/audit-explorer/main.go`
- `cmd/audit-explorer/startup.go` (new)
- `cmd/audit-explorer/startup_test.go` (new)

## Must not change
- Flags, exit codes, the MCP branch, the server wiring and every other file. `newLaunchCode` and `launchLink` keep their bodies.
- `--version` output and the `--help` text.
- The launch link must still reach a human: it is printed to the console writer exactly as today when the browser was not opened.
- This packet does not add `--background` (WP-73a) and does not change when a code is minted (WP-73b does).

## Changes
1. `startup.go`:
   - `func printStartup(lines []string, opened bool, logLine func(string), console io.Writer) (printed bool)`. `lines` is what `startupLines` returns: line 0 is the "listening at ..." text, line 1 is either the "opened in your browser" note (no link) or the "Open Unravis with this one-time link ... /launch?c=..." line. Line 0 always goes to `logLine`. When `opened` is true line 1 also goes to `logLine`. When `opened` is false line 1 goes to `console` (with a trailing newline) and never to `logLine`. A `console` write error is ignored and gives `printed=false`; `printed` is true only when the link line was written without error.
   - `func mintLaunchCode() (string, error)`: calls `newLaunchCode()` and then `applog.RegisterEphemeralSecret(code)` (never `RegisterSecret`, which keeps every value for good; only the last 8 codes are remembered; INV-3: a code that leaks into any log line is replaced by `[redacted]`). Every place that mints a code calls this function, never `newLaunchCode` directly.
2. `main.go`:
   - First statement of `func main()`: `attachParentConsole()`, then `stdout = os.Stdout` (the package-level `var stdout io.Writer = os.Stdout` was captured before the shim ran).
   - In `run`, replace `log.SetOutput(io.MultiWriter(os.Stderr, logFile.Writer()))` with `log.SetOutput(applog.Tee(os.Stderr, logFile.Writer()))`, and `code, err := newLaunchCode()` with `code, err := mintLaunchCode()`.
   - Replace the `for _, line := range startupLines(...) { log.Print(line) }` loop with `printed := printStartup(startupLines(localURL, code, codeLife, *demo, opened), opened, func(s string) { log.Print(s) }, os.Stderr)`.
   - The `launch` field of the `logFile.Logger.Info("start", ...)` call becomes: `browser_opened` when `opened`, else `link_printed` when `printed`, else `none`.
3. Tests in `startup_test.go`:
   - `TestPrintStartupKeepsTheLaunchLinkOutOfTheLog`: with a link line and `opened=false`, `logLine` never receives text containing `/launch?c=`, `console` does, and the result is true.
   - `TestPrintStartupLogsBothLinesWhenTheBrowserOpened`: with `opened=true`, `logLine` receives lines 0 and 1 and `console` receives nothing.
   - `TestPrintStartupSurvivesAFailingConsole`: a console writer that always errors does not panic, line 0 is still logged, the result is false.
   - `TestMintLaunchCodeRegistersTheSecret`: after `mintLaunchCode`, `applog.Sanitize("x " + code + " y")` does not contain the code.
   - `TestMintLaunchCodeKeepsOnlyTheLastEight`: mint 12 codes; `Sanitize` still hides each of the last 8 and no longer hides the first.
   - `TestMainCallsTheConsoleShimFirst`: reads `main.go` as text and asserts that inside `func main()` the text `attachParentConsole()` comes before `run(` and that `stdout = os.Stdout` follows it.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestPrintStartup|TestMintLaunchCode|TestMainCallsTheConsoleShimFirst" -count=1 -v` -> 6 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `Select-String -Path cmd/audit-explorer/main.go -Pattern "MultiWriter|newLaunchCode\(\)"` -> no output.
- Privacy check by hand: `go run ./cmd/audit-explorer --demo --no-browser --port 0` in a terminal prints the link on the console; the file named in the first log line (the app log directory) contains no `/launch?c=` text (`Select-String -Path <log file> -Pattern "launch\?c="` -> no output). Stop the process with Ctrl+C.

## Depends on
WP-70a, WP-70b
