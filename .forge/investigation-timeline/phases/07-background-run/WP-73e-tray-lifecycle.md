# WP-73e Tray start and clean shutdown
risk: high    size: M    wave: 7    model: sonnet
## Outcome
The running app shows the tray icon, and the icon and the session file are always cleaned up. `run` waits (bounded) for `tray.Run` to return after cancelling, so the icon is deleted before the process exits (no ghost icon). When Windows ends the session, the end-session handler cancels the app and waits (bounded) until `run` has finished, so `session.json` is removed before Windows stops the process. If the tray cannot start, the app still runs.

## Write scope
- `cmd/audit-explorer/lifecycle.go` (new)
- `cmd/audit-explorer/lifecycle_test.go` (new)
- `cmd/audit-explorer/main.go`

## Must not change
- The tray, autostart and instance packages, `trayapp.go` and `reopen.go` (use them as they are), and every file outside the write scope.
- The tray starts only when `interactive` (WP-73b). With `--no-browser` nothing changes.

## Changes
1. `lifecycle.go`:
   - `var runDone = make(chan struct{})` (closed by `main` when `run` has returned) and `var endingSession atomic.Bool`.
   - Guards: `const trayExitGuard = 2 * time.Second` and `const endSessionGuard = 3 * time.Second`. They only bound a wait; no test asserts a duration.
   - `func waitFor(ch <-chan struct{}, guard time.Duration) bool`: true when `ch` closed before the guard.
   - `func startTray(ctx context.Context, run func(context.Context, tray.Options) error, o tray.Options, logf func(string, ...any)) <-chan struct{}`: runs `run` on a goroutine; an error is logged once with `logf("Tray icon unavailable: %v", err)`; the returned channel is closed when `run` returns, error or not.
   - `func stopTray(cancel func(), ending *atomic.Bool, trayDone <-chan struct{}, guard time.Duration)`: `cancel()`, then `waitFor(trayDone, guard)` unless `ending.Load()` (during an end-session the handler is itself waiting on `run`, so waiting here would only burn the guard).
   - `func endSessionHandler(cancel func(), ending *atomic.Bool, done <-chan struct{}, guard time.Duration) func()`: the returned function sets `ending`, calls `cancel()`, then `waitFor(done, guard)`.
2. `main.go`:
   - `func main()` keeps WP-70c/70d's first statements, then `err := run(os.Args[1:], os.Getenv)`, `close(runDone)`, and only then `if err != nil { reportFatal(err) }`.
   - In `run`, when `interactive`, after WP-73c's `Listen` block: `deps := productionTrayDeps(reopener.Reopen, cancel, showMessage)`; `opts := tray.Options{Tooltip: "Unravis", Menu: func() []tray.Item { return trayMenu(deps) }, OnSelect: trayOnSelect(deps), OnOpen: reopener.Reopen, OnEndSession: endSessionHandler(cancel, &endingSession, runDone, endSessionGuard), Logf: log.Printf}`; `trayDone := startTray(appCtx, tray.Run, opts, log.Printf)`; `defer stopTray(cancel, &endingSession, trayDone, trayExitGuard)`.
3. Tests in `lifecycle_test.go` (fakes; waits on channels with a 10 second hang guard only):
   - `TestWaitForReturnsTrueWhenClosedAndFalseAfterTheGuard` (a 50 ms guard on an open channel returns false).
   - `TestStartTrayClosesDoneWhenRunReturns` and `TestStartTrayLogsAnErrorOnceAndStillCloses`.
   - `TestStopTrayWaitsForTheIconToBeRemoved`: the fake `run` blocks until its context is cancelled, appends `remove` to a list and returns; after `stopTray` returns the list holds `remove`.
   - `TestStopTraySkipsTheWaitDuringEndSession`: `ending` true and a fake `run` that never returns; `stopTray` still returns.
   - `TestEndSessionHandlerCancelsThenWaitsForRunToFinish`: a goroutine closes `done` after the handler started; the handler returns only after that, and `cancel` was called before it.
   - `TestEndSessionHandlerStopsWaitingAtTheGuard` (a `done` that never closes and a 50 ms guard: the handler returns).
   - `TestMainClosesRunDoneAfterRun` and `TestMainStartsTheTrayOnlyWhenInteractive` (text tests of `main.go`: in `main()` the text `run(` comes before `close(runDone)`, which comes before `reportFatal`; `startTray(` sits inside an `if interactive` block).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./cmd/audit-explorer -run "TestWaitFor|TestStartTray|TestStopTray|TestEndSessionHandler|TestMainClosesRunDone|TestMainStartsTheTray" -count=1 -v` -> 9 tests `PASS`.
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output; `go build ./...` -> no output; `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.
- Manual rows (recorded as PENDING in WP-77): the icon appears, left click opens one tab, right click shows the menu once, Quit leaves no icon behind (M2), signing out leaves no `session.json`.

## Depends on
WP-71d, WP-73c, WP-73d
