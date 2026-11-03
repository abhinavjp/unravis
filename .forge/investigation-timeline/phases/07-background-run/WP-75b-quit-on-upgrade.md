# WP-75b Upgrade asks Unravis to quit before it terminates it
risk: high    size: M    wave: 5    model: sonnet
## Outcome
When an upgrade or repair finds Unravis running and the user agreed to close it, setup first asks the app to quit (so it can finish its work and remove its tray icon), waits a bounded time for it to go, and only then terminates what is still left. "Still left" is decided from a fresh process list each round and matched on PID and full program path, and the terminate itself re-checks the program path on the opened process, so a PID that Windows has given to another program in the meantime is never killed. If asking is not possible or fails, setup goes straight to the terminate path, so an upgrade never gets stuck. When the elevated worker runs as a different account than the window's user, the quit signal cannot reach the app (`ErrNoInstance` is expected, WP-75c) and the terminate path does the work.

## Write scope
- `internal/setup/plan.go`
- `internal/setup/plan_askquit_test.go` (new)

## Must not change
- The consent rule (`ErrAppRunning` when the user has not agreed), the split into the user's own processes and other users' processes, and every other file.
- Setup never starts the old `unravis.exe` to ask it to quit.
- `StartProgram` (the post-install launch in `window_windows.go`) is not changed: it starts the app as the person running the window, and a second launch of a running app only signals "open" (WP-73b). Only an elevated token reaches the app's refusal (`ErrElevated`, WP-73b); the user then starts Unravis from the Start menu.

## Changes
1. Read `closeApp` in `plan.go` (`runningApps`, `splitApps` into mine and others, `ErrAppRunning` without consent, the `Proc.Terminate` loop for mine, `p.appWasRunning = true`) and `System.sleep`.
2. Add to `plan.go`: `const askQuitRounds = 20`, `const askQuitPause = 250 * time.Millisecond`, and `func stillRunning(original, latest []Process) []Process` returning the entries of `latest` whose PID and `samePath(Exe)` both match an entry of `original`.
3. In `closeApp`, after consent is confirmed: when `Sys.AskQuit != nil` and `mine` is not empty, call `AskQuit()`; an error is logged with `t.Logf` and the upgrade goes on with `left = mine` (`instance.ErrNoInstance` and `ErrNameHeldByOther` are ordinary errors here: nothing special is done for them). On success loop up to `askQuitRounds` times: `Sys.sleep(askQuitPause)`, then `running, err := p.runningApps()` (an error is returned as before), `latestMine, _ := p.r.splitApps(running)`, `left = stillRunning(mine, latestMine)`, and break when `left` is empty. When `AskQuit` is nil, `left = mine`. Then, for each entry of `left` only (never the original list), call `p.r.Sys.Proc.TerminateIf(q.PID, q.Exe)`; `ErrNotFound` counts as fine and any other error is returned as the existing `StepError`. The sleep happens before each re-list, so the fake sleep is called at most 20 times. Processes of other users are never touched, as today. `p.appWasRunning` is set as before.
4. `plan_askquit_test.go` (fake system plus `UseAskQuit`; a test that needs a changing process list sets `s.AskQuit` itself):
   - `TestUpgradeAsksTheAppToQuitFirst` (`AskQuitCalls == 1`; with `QuitRemovesApps` no terminate call is made).
   - `TestUpgradeTerminatesWhatIgnoredTheQuitRequest` (`QuitRemovesApps` false: polling runs 20 rounds on the fake sleep, then `TerminateIf` removes the process).
   - `TestUpgradeDoesNotTerminateAReusedPID` (after `AskQuit` the fake list holds the same PID with another `Exe`: that process is still in the list afterwards and no terminate call was made for it).
   - `TestUpgradeTerminatesOnlyWhatIsLeftInTheLastRound` (two processes; one quits during the polling; only the other is terminated).
   - `TestUpgradeFallsBackToTerminateWhenAskQuitFails`.
   - `TestUpgradeWithoutAskQuitSeamBehavesAsBefore` (nil seam).
   - `TestUpgradeDoesNotAskWithoutConsent` (`ErrAppRunning`, `AskQuitCalls == 0`).
   - `TestUpgradeNeverTouchesOtherUsersProcesses`.
   - `TestPollingIsBounded` (the fake sleep is called at most 20 times).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestUpgradeAsksTheApp|TestUpgradeTerminates|TestUpgradeDoesNotTerminate|TestUpgradeFallsBack|TestUpgradeWithoutAskQuit|TestUpgradeDoesNotAskWithoutConsent|TestUpgradeNeverTouches|TestPollingIsBounded" -count=1 -v` -> 9 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output.
- Security acceptance: (a) `Select-String -Path internal/setup/plan.go -Pattern "AskQuit\("` shows the call has no argument and `Select-String -Path internal/setup/plan.go -Pattern "exec\.Command"` shows no new match, so the old exe is never run; (b) `TestUpgradeDoesNotAskWithoutConsent` passes (consent first); (c) `TestPollingIsBounded` passes (bounded wait); (d) `TestUpgradeNeverTouchesOtherUsersProcesses` passes (same-user only); (e) `TestUpgradeDoesNotTerminateAReusedPID` passes and `Select-String -Path internal/setup/plan.go -Pattern "Proc\.Terminate\("` -> no output (only `TerminateIf` is used).

## Depends on
WP-75a, WP-74h
