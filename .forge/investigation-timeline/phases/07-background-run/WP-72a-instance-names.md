# WP-72a Single-instance names and signal kinds
risk: medium    size: M    wave: 1    model: sonnet
## Outcome
A new package `internal/instance` defines the names the running copy of Unravis owns (one mutex, one "open" event, one "quit" event, all in the per-session `Local\` namespace and all carrying the current user's SID) and the two signal kinds. The SID in the name keeps different accounts' copies apart, but a SID is public, so it does NOT stop another account in the same sign-in session from creating the objects first (squatting). Squatting is therefore detected, not prevented: the package also provides `OwnedByCurrentUser(handle)`, which the guard (WP-72b) and the signal functions (WP-72c) use to refuse an object whose owner is not the current user. A squatter can still stop Unravis starting (denial of service is accepted), but it is noticed and reported, never trusted. Demo mode gets its own names so a demo run never collides with the installed app.

## Write scope
- `internal/instance/instance.go` (new)
- `internal/instance/names_windows.go` (new)
- `internal/instance/names_other.go` (new)
- `internal/instance/instance_test.go` (new)
- `internal/instance/names_windows_test.go` (new)

## Must not change
- Every file outside `internal/instance`. No new module dependency (`golang.org/x/sys/windows` is already in `go.mod`).

## Changes
1. `instance.go` (package `instance`, no build tags):
   - `const DefaultApp = "UnravisApp"` and `const DemoApp = "UnravisDemoApp"`.
   - `type Kind int` with `Open` and `Quit` (start at 1 so the zero value is invalid) and `func (k Kind) String() string` returning `"open"`, `"quit"`, and `"unknown"` for anything else.
   - `var ErrNoInstance = errors.New("instance: no running instance")`, `ErrAccessDenied = errors.New("instance: access denied")` and `ErrEventsTaken = errors.New("instance: events already exist")` and `ErrNameHeldByOther = errors.New("instance: another process holds Unravis's name")` (all used by WP-72b/72c; the last one means "the named object exists but its owner is not the current user", so the caller shows the clear "another process holds Unravis's name" message of WP-73b and exits).
   - `type Names struct { Mutex, Open, Quit string }` and `func NewNames(app, userSID string) (Names, error)`: `app` must be 1 to 32 ASCII letters or digits; `userSID` must match `^S-1-[0-9]+(-[0-9]+)+$` and be at most 184 characters. The result is `Local\<app>.<userSID>`, `Local\<app>.<userSID>.Open`, `Local\<app>.<userSID>.Quit`. Anything else returns an error.
2. `names_windows.go` (`//go:build windows`):
   - `func CurrentNames(app string) (Names, error)` reads the SID string of the current process token user (`windows.OpenCurrentProcessToken()`, `GetTokenUser()`, `User.Sid.String()`, close the token) and returns `NewNames(app, sid)`.
   - `func OwnedByCurrentUser(h windows.Handle) (bool, error)`: `sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)`, `owner, _, err := sd.Owner()`; the token user SID is as above; the token default owner SID comes from `windows.GetTokenInformation(token, windows.TokenOwner, ...)` called twice (size, then data; the buffer starts with one `*SID` pointing inside the same buffer, read it with `*(**windows.SID)(unsafe.Pointer(&buf[0]))`). The answer is `ownerMatches(owner, user, defaultOwner)`.
   - `func ownerMatches(owner, user, defaultOwner *windows.SID) bool` is true when `owner` equals `user` or `defaultOwner` (`windows.EqualSid`), false for any other SID and for a nil `owner`. The default owner is accepted because an elevated administrator's objects are owned by the Administrators group, not by the user.
   - `var ownerCheck = OwnedByCurrentUser`: the one seam that WP-72b and WP-72c call and their tests swap to simulate a squatter.
3. `names_other.go` (`//go:build !windows`): `func CurrentNames(app string) (Names, error)` returns `NewNames(app, "S-1-0-0")`.
4. Every later packet gets names with `CurrentNames(DefaultApp)` (or `DemoApp`); only tests call `NewNames` directly.
5. Tests in `instance_test.go`: `TestNewNamesBuildsTheThreeNames`, `TestNewNamesRejectsBadApp` (table: empty, `a\b`, `a.b`, `a b`, 33 characters, non-ASCII), `TestNewNamesRejectsBadSID` (empty, `abc`, `S-1-`, a 185-character SID, one containing a backslash), `TestKindStrings`, `TestNamesUseTheLocalNamespace` (every field starts with `Local\`), `TestNamesContainTheUserSID` (the SID appears in all three names), `TestDefaultAndDemoNamesDiffer`, and `TestCurrentNamesUseTheTokenUserSID` (skipped unless `runtime.GOOS == "windows"`; compares with `user.Current().Uid`, which is the SID string on Windows). The same file also holds the helper `func uniqueNames(t *testing.T) Names` returning `NewNames("T"+16 random hex characters, "S-1-5-21-1-2-3-1001")`; WP-72b and WP-72c tests use it.
6. Tests in `names_windows_test.go` (`//go:build windows`): `TestOwnerMatchesAcceptsOnlyUserOrDefaultOwner` (table built with `windows.StringToSid`: user, default owner, an unrelated `S-1-5-21-9-9-9-500` and a nil owner), `TestFreshMutexIsOwnedByCurrentUser` and `TestFreshEventIsOwnedByCurrentUser` (create a uniquely named object with `uniqueNames(t)`, `OwnedByCurrentUser` is true, close it).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/instance -count=1 -v` -> 11 tests `PASS` (8 plus the 3 of step 6).
- `go vet ./internal/instance` -> no output; `$env:GOOS='linux'; go vet ./internal/instance; Remove-Item Env:GOOS` -> no output.

## Depends on
none
