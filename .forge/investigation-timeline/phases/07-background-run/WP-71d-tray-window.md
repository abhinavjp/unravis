# WP-71d Tray message loop and hidden window
risk: medium    size: M    wave: 3    model: sonnet
## Outcome
`tray.Run(ctx, Options)` works on Windows: it shows the icon, opens Unravis on a left click, shows the menu on a right click or the menu key (once, never twice), puts the icon back after Explorer restarts, retries if the shell is not ready yet, tells the app when the Windows session really ends, and returns, with the icon deleted, when the context is cancelled. Everything runs on one locked OS thread. Window messages carry no access check, so only a private stop message (gated by an internal flag) and a genuine system end-of-session can quit it; a foreign `WM_CLOSE`, `WM_DESTROY`, `WM_QUIT` or posted `WM_ENDSESSION` does nothing.

## Write scope
- `internal/tray/tray_windows.go` (new)
- `internal/tray/tray_windows_test.go` (new)

## Must not change
- Every other file, including the WP-71a to WP-71c files (use their functions as they are; if one needs a change, record it and stop).
- The window is a hidden top-level window, not a message-only window: message-only windows do not receive the broadcast `TaskbarCreated` message.
- `ChangeWindowMessageFilter` and `ChangeWindowMessageFilterEx` are never called, so the integrity-level wall (UIPI) against low-integrity senders stays up.

## Changes
1. `tray_windows.go` (`//go:build windows`): `func Run(ctx context.Context, o Options) error`.
   - Constants: callback message `callbackMsg = WM_APP+1` (0x8001), private stop message `stopMsg = WM_APP+2` (0x8002), `trayIconID = 1`, `retryTimerID = 1`, `NIN_SELECT = 0x400`, `NIN_KEYSELECT = 0x403`, `WM_NULL = 0`, `WM_LBUTTONUP = 0x202`, `WM_RBUTTONUP = 0x205`, `WM_CONTEXTMENU = 0x7B`, `WM_TIMER = 0x113`, `WM_QUERYENDSESSION = 0x11`, `WM_ENDSESSION = 0x16`, `WM_CLOSE = 0x10`, `WM_DESTROY = 0x2`, `WM_QUIT = 0x12`, `SM_SHUTTINGDOWN = 0x2000`. Under `NOTIFYICON_VERSION_4` the callback `lParam` carries the event in `LOWORD(lParam)` and the icon id in `HIWORD(lParam)`; a message whose id is not `trayIconID` is ignored.
   - Shared state: `type trayState struct { o Options; hwnd windows.Handle; taskbarMsg uint32; added bool; menuOpen, stopping, destroying atomic.Bool }` and one package variable `active atomic.Pointer[trayState]` (one tray per process). The single window procedure (a `windows.NewCallback` kept in a package variable) loads `active`; when nil it calls `DefWindowProc`. A second `Run` while `active` is set returns the error `tray: already running`.
   - `Run` validates options, calls `runtime.LockOSThread`, registers a class `UnravisTrayWindow<counter>` (counter so tests can run it repeatedly), creates a hidden `WS_POPUP` window without `WS_VISIBLE`, calls `RegisterWindowMessage("TaskbarCreated")`, adds the icon with `exeIcon()`, then calls `readyHook(hwnd)`. If the add fails it logs and arms a `WM_TIMER` (`retryTimerID`, 2000 ms) through `armRetryHook(hwnd)` that retries until it works, then calls `disarmRetryHook(hwnd)`.
   - Test seams (package variables, defaults are the real calls): `addIconHook = addIcon`, `deleteIconHook = deleteIcon`, `popupHook = showMenu`, `endingSession = systemEnding`, `armRetryHook = func(hwnd windows.Handle)` (real `SetTimer`), `disarmRetryHook = func(hwnd windows.Handle)` (real `KillTimer`), `shuttingDownMetric = func() int32` (real `GetSystemMetrics(SM_SHUTTINGDOWN)`), `readyHook = func(hwnd windows.Handle) {}` (called on the tray thread with the new window handle once the window exists). `func systemEnding() bool` is `shuttingDownMetric() != 0` and nothing else: `InSendMessage` is not used, because any process may send a message and make it true.
   - Messages: `callbackMsg` with event `WM_LBUTTONUP` or `NIN_KEYSELECT` -> `OnOpen`. Event `WM_CONTEXTMENU` (and only it; ignore `WM_RBUTTONUP` and `NIN_SELECT`, version 4 also sends those) -> if `menuOpen.CompareAndSwap(false, true)` then `popupHook(hwnd, o.Menu())`, `OnSelect(id)` when `ok`, and `menuOpen.Store(false)`; a request while a menu is open is dropped. `taskbarMsg` -> `addIconHook` again. `WM_QUERYENDSESSION` -> 1. `WM_ENDSESSION` with wparam non-zero AND `endingSession()` -> `OnEndSession` (when set); otherwise ignored. `WM_CLOSE` -> return 0, does nothing. `stopMsg` -> only when `stopping.Load()`: if `menuOpen` is set call `EndMenu` and re-post `stopMsg`, else `destroying.Store(true)` and `DestroyWindow`. `WM_DESTROY` -> only when `destroying.Load()`: `deleteIconHook`, `PostQuitMessage(0)`; otherwise return 0 and do nothing (a foreign `SendMessage(WM_DESTROY)` must not delete the icon or quit).
   - A goroutine waits for `ctx.Done()`, sets `stopping`, and posts `stopMsg`. The `GetMessage` loop ends only when `GetMessage` returns 0 AND `destroying.Load()` is true; a return of -1 (invalid window handle) is fatal: log it and leave the loop, never `continue` (no busy spin); a `WM_QUIT` posted by a foreign process while `destroying` is false is ignored and the loop goes on. `Run` clears `active` and returns nil. The icon is deleted before `Run` returns on every path that created the window. A panic in a callback is recovered and logged so the loop keeps going.
2. Tests (`tray_windows_test.go`; each starts `Run` in a goroutine with hooks that replace the real icon calls, takes the window handle from `readyHook`, and drives it with `SendMessage`/`PostMessage`; waits are channels with a generous 10 second guard only, never an asserted duration).
   - `TestTrayWindowIsTopLevelAndHidden`: `GetParent` is 0, `IsWindowVisible` false, no `WS_CHILD`.
   - `TestTrayLeftClickCallsOnOpen`: lParam `MAKELONG(WM_LBUTTONUP, 1)` and `MAKELONG(NIN_KEYSELECT, 1)` each call `OnOpen`; `NIN_SELECT` and `WM_RBUTTONUP` do not; an id of 2 does not.
   - `TestTrayContextMenuIsBuiltFromMenuFunc`: `MAKELONG(WM_CONTEXTMENU, 1)` makes the fake `popupHook` receive `Menu()`'s items and `OnSelect` receive the fake's ID.
   - `TestTrayIgnoresASecondMenuRequestWhileOpen`: the fake `popupHook` sends `WM_CONTEXTMENU` again from inside itself; `popupHook` ran once.
   - `TestTrayReaddsTheIconOnTaskbarCreated`: after the registered `TaskbarCreated` message the fake add ran twice.
   - `TestTrayRetriesWhenAddFails`: `armRetryHook` and `disarmRetryHook` are faked (no real timer runs, so nothing races); the add fails once; sending `WM_TIMER` (wparam `retryTimerID`) makes it succeed (exactly two add calls, the fake disarm ran once) and a further `WM_TIMER` calls nothing.
   - `TestSystemEndingFollowsTheShuttingDownMetric`: with `shuttingDownMetric` faked to 1 `systemEnding()` is true, faked to 0 it is false; with the real metric in a normal test session `systemEnding()` is false.
   - `TestTrayEndSessionCallsOnEndSession`: `endingSession` faked true, wparam 1 calls `OnEndSession`; wparam 0 does not.
   - `TestTrayPostedEndSessionIsIgnored`: `endingSession` faked false, wparam 1 does not call `OnEndSession`.
   - `TestTrayIgnoresForeignClose`: `SendMessage(WM_CLOSE)` and `PostMessage(stopMsg)` without a context cancel leave `IsWindow` true and the delete hook uncalled.
   - `TestTrayIgnoresForeignDestroyAndQuit`: `SendMessage(WM_DESTROY)` and `PostThreadMessage(threadID, WM_QUIT, 0, 0)` (thread id from `GetWindowThreadProcessId(hwnd)`) leave `IsWindow` true, the delete hook uncalled and `Run` still running; afterwards cancelling the context still ends `Run`.
   - `TestTrayStopsWhenContextIsCancelled`: cancelling makes `Run` return nil with the delete hook already called.
   - `TestTrayRejectsASecondConcurrentRun`: a second `Run` returns the `already running` error.
   - `TestTrayLoopLeavesOnAGetMessageError`: reads `tray_windows.go` as text and asserts the `GetMessage` result is compared with `-1` and that branch contains `break` or `return` and no `continue`.
   - `TestTrayNeverLoosensTheMessageFilter`: reads every non-test `.go` file in `internal/tray` and asserts none contains `ChangeWindowMessageFilter`.
3. Security note (record in the file comment on `Run`, and word it exactly like WP-77's accepted-risk sentence): "Accepted risk: any process of the same user at the same or higher integrity level can send the open or quit signal. Open re-arms a new launch code, so the previous link stops working, and each accepted open starts rundll32.exe, which adds a process-creation event. Quit ends the app. This is accepted: such a process already controls the user's session. The session token stays protected by the one-time code." The rate is limited by WP-73c.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/tray -count=2 -v` -> all tests `PASS` (15 new, plus those of WP-71a to WP-71c); `-count=2` shows the class registration does not clash.
- `go vet ./internal/tray` -> no output; `$env:GOOS='linux'; go vet ./internal/tray; Remove-Item Env:GOOS` -> no output.
- `Select-String -Path internal/tray/*.go -Pattern "ChangeWindowMessageFilter" -Exclude *_test.go` -> no output.
- No test leaves an icon in the real tray: `Select-String -Path internal/tray/tray_windows_test.go -Pattern "addIconHook"` -> every test that starts `Run` sets the hook.

## Depends on
WP-71b, WP-71c
