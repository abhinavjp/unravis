# WP-72b Single-instance guard
risk: medium    size: S    wave: 2    model: sonnet
## Outcome
`instance.Acquire` takes the named mutex and tells the caller whether it is the first copy. A second copy learns it is not first without an error. A mutex that exists but is owned by someone other than the current user is reported as `ErrNameHeldByOther` (squatting is detected, not prevented). Off Windows it always reports first, so the code compiles and runs everywhere.

## Write scope
- `internal/instance/guard_windows.go` (new)
- `internal/instance/guard_other.go` (new)
- `internal/instance/guard_windows_test.go` (new)

## Must not change
- The WP-72a files, and every file outside `internal/instance`.
- The mutex uses the default security descriptor and a name that carries the user's SID (WP-72a). The SID is public, so another account in the same session CAN pre-create the name; this is detected (owner check below), not prevented. A squatted mutex can only stop Unravis starting (the caller shows an error); it never leads to code execution, and Unravis never treats a mutex it did not create as its own.

## Changes
1. `guard_windows.go` (`//go:build windows`): `func Acquire(names Names) (release func(), first bool, err error)`. Validate that `names.Mutex` starts with `Local\` (otherwise return an error). Call `windows.CreateMutex(nil, false, name)`; when it returns a handle together with `windows.ERROR_ALREADY_EXISTS`, call `ownerCheck(handle)` (WP-72a), close that handle, and: owner is the current user -> return `first=false`, a no-op `release` and no error; owner is someone else or the check fails -> return `first=false`, a no-op `release` and `ErrNameHeldByOther`; when the call fails with `ERROR_ACCESS_DENIED` return `first=false` with `ErrAccessDenied` (the caller words the message, see WP-73b); any other failure returns the error. On success `release` closes the handle once (safe to call twice).
2. `guard_other.go` (`//go:build !windows`): `Acquire` returns a no-op release, `first=true`, no error.
3. Tests (`guard_windows_test.go`; every test uses `uniqueNames(t)` from WP-72a so a real Unravis is never touched): `TestAcquireFirstThenSecond` (second call returns `first=false`, nil error), `TestAcquireAfterReleaseIsFirstAgain`, `TestReleaseTwiceIsSafe`, `TestAcquireRejectsNamesOutsideLocal` (a `Global\x` mutex name returns an error), `TestAcquireDoesNotShareAcrossUsers` (two `NewNames` with the same app and different SIDs both acquire as first), `TestAcquireDetectsAMutexOwnedBySomeoneElse` (create the unique mutex in the test, swap `ownerCheck` for a fake returning false, restore with `t.Cleanup`: `Acquire` returns `first=false` and `ErrNameHeldByOther`; with the real `ownerCheck` the same second call returns `first=false` and a nil error).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/instance -run "TestAcquire|TestRelease" -count=1 -v` -> 6 tests `PASS`.
- `go vet ./internal/instance` -> no output; `$env:GOOS='linux'; go vet ./internal/instance; Remove-Item Env:GOOS` -> no output.
- The foreign-account case (access denied) needs a second account and is manual check M7 in WP-77.

## Depends on
WP-72a
