# WP-70d Fatal start-up errors that a windowless build can show
risk: medium    size: S    wave: 3    model: sonnet
## Outcome
The one start-up failure path in `unravis.exe` (`log.Fatal(err)` in `main()`, `cmd/audit-explorer/main.go` line 53 before WP-70c) goes through `reportFatal`, which logs the error, shows a short message box when there is no console to read it on (a windowless build started from Explorer, the tray or the Startup folder), and exits with code 1. Today `log.Fatal` is silent in that case, so a double-click that fails does nothing visible.

## Write scope
- `cmd/audit-explorer/fatal.go` (new)
- `cmd/audit-explorer/fatal_windows.go` (new)
- `cmd/audit-explorer/fatal_other.go` (new)
- `cmd/audit-explorer/fatal_test.go` (new)
- `cmd/audit-explorer/main.go`

## Must not change
- Exit code of a failed start (stays 1), the text of existing error messages, `parseFlags`, and every other file.
- Tests must never pop a real dialog: the message box is always reached through the variable `showMessage`.

## Changes
1. `fatal.go` (no build tags):
   - `var exitProcess = os.Exit`, `var showMessage = showMessageBox`, `var stderrUnusable = stderrIsUnusable`.
   - `func reportFatal(err error)`: `log.Print(err)`; when `stderrUnusable()` is true call `showMessage("Unravis could not start", fatalText(err))`; then `exitProcess(1)`.
   - `func fatalText(err error) string`: the error text cut to 800 runes (append "..." when cut), a blank line, then "Details are in the Unravis log folder."
2. `fatal_windows.go` (`//go:build windows`): `func showMessageBox(title, text string)` does `t, _ := windows.UTF16PtrFromString(text)` and then `c, _ := windows.UTF16PtrFromString(title)` (text first, caption second; there is no helper called `ptr`), then calls `windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)` and ignores the result. `func stderrIsUnusable() bool` is `!stdHandleUsable(windows.Handle(os.Stderr.Fd()))` (from WP-70a), and true when `os.Stderr` is nil.
3. `fatal_other.go` (`//go:build !windows`): empty `showMessageBox` and `func stderrIsUnusable() bool { return false }`.
4. `main.go`: replace the one `log.Fatal(err)` in `main()` with `reportFatal(err)`. Run `Select-String -Path cmd/audit-explorer/main.go -Pattern "log\.Fatal"` first: it must show exactly that one line. Flag errors already come back from `parseFlags` as ordinary errors, so they take the same path.
5. Tests in `fatal_test.go` (swap the three variables for fakes, restore with `t.Cleanup`):
   - `TestReportFatalLogsAndExitsWithOne`: `stderrUnusable` false; fake exit gets 1, `showMessage` not called, the log output holds the message.
   - `TestReportFatalShowsAMessageWhenStderrIsUnusable`: `stderrUnusable` true; `showMessage` gets title "Unravis could not start" and a text containing the error.
   - `TestFatalTextIsShortened`: a 5000-rune error gives at most 800 runes of it plus the fixed footer, and ends with the footer.
   - `TestMainUsesReportFatalNotLogFatal`: reads `main.go` as text and asserts it contains no `log.Fatal`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestReportFatal|TestFatalText|TestMainUsesReportFatal" -count=1 -v` -> 4 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- `Select-String -Path cmd/audit-explorer/main.go -Pattern "log\.Fatal"` -> no output.

## Depends on
WP-70c
