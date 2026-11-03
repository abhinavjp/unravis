# WP-72c Open and quit signals between copies
risk: high    size: M    wave: 2    model: sonnet
## Outcome
The running copy can listen for "open" and "quit" requests, and another copy of the same user (a second launch, `unravis.exe --quit`, the Start-menu Quit entry, or setup during an upgrade) can send them. The signals are two named auto-reset events that carry no data, so nothing a sender provides is ever parsed or executed. The sender closes its event handle after each signal, so a long-lived sender (setup) never keeps the event alive after the app exits.

## Write scope
- `internal/instance/signal_windows.go` (new)
- `internal/instance/signal_other.go` (new)
- `internal/instance/signal_windows_test.go` (new)

## Must not change
- The WP-72a and WP-72b files, and every file outside `internal/instance`.
- The events use the default security descriptor only: no explicit grant to Everyone, Authenticated Users or Users. An elevated copy of the same user can signal a normal copy; the reverse (a normal copy signalling an elevated one) gets access denied and is reported by WP-73b as `ErrElevated` or another-account. Another account gets access denied too. The user SID in the names (WP-72a) is public and does not stop same-session squatting: a squatted name is detected by the owner check (`ownerCheck`, WP-72a) in both `Listen` and `Signal` and reported as `ErrNameHeldByOther`; the signal is never sent to an object owned by someone else.

## Changes
1. `signal_windows.go` (`//go:build windows`):
   - `func Listen(ctx context.Context, names Names, onOpen, onQuit func()) (stop func(), err error)`: creates two auto-reset, initially unsignalled events with `windows.CreateEvent`; when either already exists (`ERROR_ALREADY_EXISTS`) it runs `ownerCheck` on the returned handle, closes what it made, and returns `ErrNameHeldByOther` when the owner is not the current user (or the check fails) and `ErrEventsTaken` otherwise (never adopt events someone else created); `ERROR_ACCESS_DENIED` returns `ErrAccessDenied`. A goroutine blocks in `windows.WaitForMultipleObjects` on the open event, the quit event and a private stop event; it calls `onOpen` or `onQuit` on that one goroutine (callbacks never run in parallel, and auto-reset events merge repeated signals). Context cancel and `stop()` both set the stop event; `stop` waits for the goroutine to finish, closes all three handles, and is safe to call twice. A panic in a callback is recovered. Callers must not block in a callback (WP-73c runs any modal box in its own goroutine).
   - `func Signal(names Names, k Kind) error`: `windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.READ_CONTROL, false, name)`, then `defer windows.CloseHandle(h)`, then `ownerCheck(h)`: when the owner is not the current user (or the check fails) return `ErrNameHeldByOther` without calling `SetEvent`; otherwise `windows.SetEvent`; `ERROR_FILE_NOT_FOUND` -> `ErrNoInstance`; `ERROR_ACCESS_DENIED` -> `ErrAccessDenied`; an unknown kind returns an error without any Windows call.
2. `signal_other.go` (`//go:build !windows`): `Listen` returns a no-op stop and nil; `Signal` returns `ErrNoInstance`.
3. Tests (`signal_windows_test.go`; `uniqueNames(t)` from WP-72a; waits use channels with a generous 10 second hang guard only, never a duration assertion):
   - `TestSignalOpenReachesTheListener`, `TestSignalQuitReachesTheListener`.
   - `TestSignalWithNoListenerIsErrNoInstance`.
   - `TestListenStopsWhenContextIsCancelled` (the goroutine exits; `stop()` afterwards returns at once).
   - `TestListenRefusesEventsThatAlreadyExist` (a second `Listen` with the same names returns `ErrEventsTaken`; the first keeps working).
   - `TestRepeatedOpenSignalsAreHandledOneAtATime` (the callback fails the test if two callbacks overlap).
   - `TestSignalUnknownKindIsRejected`.
   - `TestListenAndSignalDetectEventsOwnedBySomeoneElse`: swap `ownerCheck` for a fake returning false (restore with `t.Cleanup`). A second `Listen` on names a first listener holds returns `ErrNameHeldByOther`; `Signal` on the same names returns `ErrNameHeldByOther` and the first listener's callback is NOT called (call `stop()`, which waits for the goroutine to finish, then assert the callback count is 0: no sleeping and no timing).
   - `TestSignalDoesNotKeepTheEventAlive`: `Signal` once, stop the listener, then a new `Listen` on the same names succeeds (no `ErrEventsTaken`).
   - `TestEventsGrantWriteOnlyToTheUserSystemAndAdministrators`: open the listener's open event with `windows.OpenEvent(READ_CONTROL|SYNCHRONIZE)`, read its DACL with `windows.GetSecurityInfo`, walk the ACEs with `windows.GetAce`. For every ACCESS_ALLOWED ACE whose mask has any of `EVENT_MODIFY_STATE` (0x2), `GENERIC_WRITE` (0x40000000), `GENERIC_ALL` (0x10000000), `WRITE_DAC` or `WRITE_OWNER`, the SID must be the current token user, SYSTEM (`S-1-5-18`) or Administrators (`S-1-5-32-544`); any other SID fails the test (an allowlist, so the logon SID, Everyone, Users and Authenticated Users all fail).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/instance -count=3 -v` -> all tests `PASS`; `-count=3` shows no leftover event state.
- `go vet ./internal/instance` -> no output; `$env:GOOS='linux'; go vet ./internal/instance; Remove-Item Env:GOOS` -> no output.
- Security acceptance (all must hold, recorded in the packet progress note): (a) the ACL allowlist test passes; (b) `Select-String -Path internal/instance/signal_windows.go -Pattern "SECURITY_ATTRIBUTES|SetSecurityInfo|SetNamedSecurityInfo"` -> no output, so the default descriptor is used; (c) `Select-String -Path internal/instance/signal_windows.go -Pattern "os/exec|exec\."` -> no output (a sender can only pick one of two fixed kinds); (d) `TestListenRefusesEventsThatAlreadyExist`, `TestSignalDoesNotKeepTheEventAlive` and `TestListenAndSignalDetectEventsOwnedBySomeoneElse` pass.

## Depends on
WP-72a
