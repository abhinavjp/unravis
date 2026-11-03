# WP-74c Find the user's Startup folder
risk: low    size: S    wave: 3    model: haiku
## Outcome
`autostart.StartupDir()` returns the current user's Startup folder (the one Windows reads at sign-in) using the Windows known-folder call, so the tray can enable and disable start at sign-in. Off Windows it reports that there is none. The folder may be redirected (OneDrive, roaming profile), so nothing assumes it sits inside the profile.

## Write scope
- `internal/autostart/startupdir.go` (new)
- `internal/autostart/startupdir_windows.go` (new)
- `internal/autostart/startupdir_other.go` (new)
- `internal/autostart/startupdir_windows_test.go` (new)

## Must not change
- `autostart.go` and `autostart_test.go` from WP-74b, and every file outside `internal/autostart`.

## Changes
1. `startupdir.go` (new, no build tags): `var ErrNoStartupDir = errors.New("autostart: no Startup folder on this platform")`.
2. `startupdir_windows.go` (`//go:build windows`): `func StartupDir() (string, error)` calls `windows.KnownFolderPath(windows.FOLDERID_Startup, 0)` and returns the result; an error or an empty result is returned as an error.
3. `startupdir_other.go` (`//go:build !windows`): `func StartupDir() (string, error)` returns `"", ErrNoStartupDir`.
4. Tests (`startupdir_windows_test.go`, first line `//go:build windows`): `TestStartupDirIsAnExistingAbsoluteStartupFolder` (absolute, exists as a directory, base name equals `Startup` ignoring case; no assertion about the profile folder), `TestStartupDirDoesNotCreateAnything` (the folder's entry count is the same before and after calling `StartupDir()` and `Enabled(dir, os.Args[0])`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/autostart -run "TestStartupDir" -count=1 -v` -> 2 tests `PASS`.
- `go vet ./internal/autostart` -> no output; `$env:GOOS='linux'; go vet ./internal/autostart; Remove-Item Env:GOOS` -> no output.
- `Select-String -Path internal/autostart/startupdir_windows_test.go -Pattern "UserHomeDir"` -> no output (no profile-root assertion).

## Depends on
WP-74b
