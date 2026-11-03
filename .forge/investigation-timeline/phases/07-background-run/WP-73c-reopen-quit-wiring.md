# WP-73c Reopen and quit wiring
risk: high    size: M    wave: 6    model: sonnet
## Outcome
The first copy listens for the other copies. An "open" request (second launch or, in WP-73e, the tray) mints a fresh one-time launch code, registers it with the log sanitizer (only the last 8 codes are remembered, WP-70b/70c, so the list never grows), re-arms the running server with it and opens the browser, at most once every 2 seconds however many requests arrive. A "quit" signal cancels the app's main context so the normal graceful shutdown runs. If the browser cannot be opened, a message box (shown on its own goroutine, one at a time) gives the base address and tells the user to use the tray "Open Unravis" item; it never contains the one-time code or link.

## Write scope
- `cmd/audit-explorer/reopen.go` (new)
- `cmd/audit-explorer/reopen_test.go` (new)
- `cmd/audit-explorer/main.go`

## Must not change
- The shutdown path (`shutdownOnSignal`), the launch-code rules of the server (single use, 2 minute browser life) and every file outside the write scope.
- The launch link and the code are never passed to `log`, `Logf`, an error string, a message box or the tray tooltip.

## Changes
1. `reopen.go` (no `log` import, no `fmt.Errorf`): `const reopenMinGap = 2 * time.Second`, `const reopenCodeLife = 2 * time.Minute`, and `func system32File(name string) string` = `filepath.Join(root, "System32", name)` where `root` is `os.Getenv("SystemRoot")`, or `C:\Windows` when that is empty.
   - `type reopener struct { base string; rearm func(code string, until time.Time) bool; newCode func() (string, error); open func(url string) error; warn func(title, text string); now func() time.Time; link func(code string) string; mu sync.Mutex; last time.Time; warning atomic.Bool }`.
   - `func (r *reopener) Reopen()`: under `mu` return when `now()-last < reopenMinGap`, else set `last`. Then `code, err := r.newCode()` (error -> `r.notify("Unravis", "Could not prepare a new link. Try again in a moment.")`, return); `r.rearm(code, r.now().Add(reopenCodeLife))` (false -> `notify` "Unravis is still starting; try again in a moment.", return); `r.open(r.link(code))` (error -> `notify("Unravis could not open your browser", "Unravis is running at " + r.base + ".\n\nYour browser could not be opened. Right-click the Unravis icon in the notification area and choose \"Open Unravis\" to try again.")`).
   - `func (r *reopener) notify(title, text string)`: if `r.warning.CompareAndSwap(false, true)` then `go func() { defer r.warning.Store(false); r.warn(title, text) }()`. A modal box therefore never blocks the signal listener or the tray thread, and a second box is not stacked on an open one.
2. `main.go`:
   - Add `func openInBrowser(url string) error { return exec.Command(system32File("rundll32.exe"), "url.dll,FileProtocolHandler", url).Start() }` (a full path, so a planted `rundll32.exe` in the current folder or on `PATH` is never found) and use it for the first start too (`opened = openInBrowser(launchLink(localURL, code)) == nil`).
   - Create `appCtx, cancel := context.WithCancel(context.Background())` with `defer cancel()`, and use `appCtx` as the parent of the existing `signal.NotifyContext(...)`, so cancelling it runs the same shutdown as Ctrl+C.
   - After `served := server.New(...)` and the listener address are known, build the value `reo := &reopener{base: localURL, rearm: func(c string, u time.Time) bool { return server.Rearm(served, c, u) }, newCode: mintLaunchCode, open: openInBrowser, warn: showMessage, now: time.Now, link: func(c string) string { return launchLink(localURL, c) }}`. The variable is named `reo` on purpose: `reopener` is the type name and must stay usable in the same scope.
   - When `interactive`: after the server is serving and the session file is written, `stopListen, lerr := instance.Listen(appCtx, result.names, reo.Reopen, cancel)` (`result` from WP-73b). A `Listen` failure (for example `ErrEventsTaken`, or `ErrNameHeldByOther` when another account squatted the event names) logs `log.Printf("Second launches cannot reach this copy: %v", lerr)` and the app keeps running; on success `defer stopListen()`.
3. Tests in `reopen_test.go` (all dependencies are fakes, a controllable `now`; fake `warn` reports on a channel because it runs on its own goroutine; every channel wait has a 10 second hang guard only):
   - `TestReopenRearmsAndOpensTheBrowser`: `rearm` receives the code from `newCode` and an `until` of now plus 2 minutes; `open` receives the link built from the same code.
   - `TestReopenIsThrottled`: a second call inside `reopenMinGap` does nothing; after advancing the fake clock it works again.
   - `TestReopenWarnsWithoutTheLinkWhenTheBrowserFails`: the warn text contains `base` and `Open Unravis`, and contains neither the code nor `/launch`.
   - `TestReopenDoesNotBlockOnAModalWarning`: a `warn` that blocks until released; `Reopen` returns and a later `Reopen` (clock advanced) still re-arms.
   - `TestReopenShowsOneWarningAtATime`: while the first fake box is blocked, a second failure starts no second `warn`.
   - `TestReopenNeverLogsTheCode`: `log.SetOutput` to a buffer for the call; the buffer holds neither the code nor `/launch?c=`.
   - `TestReopenReportsAFailedRearm`, `TestReopenReportsAFailedCodeGeneration`.
   - `TestReopenRegistersTheCodeAsASecret`: with `newCode: mintLaunchCode`, `applog.Sanitize` of a string holding the code that `rearm` received does not contain it.
   - `TestReopenManyTimesKeepsTheSecretListBounded`: with `newCode: mintLaunchCode` and an advancing fake clock, 30 calls leave `Sanitize` hiding the last 8 codes `rearm` received and no longer hiding the first (the bound is implemented by WP-70b; this checks the wiring uses it).
   - `TestSystem32FileUsesTheWindowsFolder`: with `t.Setenv("SystemRoot", `C:\X`)` the result is `C:\X\System32\rundll32.exe`; with the variable empty it is `C:\Windows\System32\rundll32.exe`. `main.go` read as text has no `exec.Command("rundll32.exe"`.
   - `TestMainUsesReoNotAShadowedType`: reads `main.go` as text and asserts it contains `reo.Reopen` and not `reopener.Reopen`.
   - `TestQuitSignalCancelsTheAppContext`: reads `main.go` as text: the `instance.Listen(` call receives `cancel`, `signal.NotifyContext(appCtx` is present and `shutdownOnSignal` is still called.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestReopen|TestSystem32File|TestMainUsesReo|TestQuitSignalCancels" -count=1 -v` -> 13 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- Security acceptance: (a) `Select-String -Path cmd/audit-explorer/reopen.go -Pattern "log\.|Logf|Errorf"` -> no output; (b) the throttle test passes (a flood of signals cannot start many browsers); (c) the quit route only cancels a context, and `Select-String -Path cmd/audit-explorer/reopen.go -Pattern "os\.Exit"` -> no output; (d) the signal carries no data, see WP-72c; (e) `Select-String -Path cmd/audit-explorer/main.go -Pattern "newLaunchCode\("` -> no output (every code goes through `mintLaunchCode`).

## Depends on
WP-70c, WP-72c, WP-72d, WP-73b
