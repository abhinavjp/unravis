# WP-74e Read the Desktop folder from Windows
risk: low    size: S    wave: 2    model: sonnet
## Outcome
The installer fills `Dirs.Desktop` from the real Windows known folder, and records it in the captured user folders, but only when the folder lies inside the user's profile (a Desktop redirected to a network share or OneDrive outside the profile is ignored, so the installer never writes outside the profile).

## Write scope
- `internal/setup/os_windows.go`
- `internal/setup/capture.go`
- `internal/setup/desktop_dirs_windows_test.go` (new)
- `internal/setup/desktop_dirs_test.go` (new)

## Must not change
- The other known folders, `OSDirs` error handling and every other file.

## Changes
1. Read `OSDirs` in `os_windows.go` (the table of `FOLDERID_*` into `Dirs` fields) and `captureDirs` in `capture.go`.
2. `os_windows.go`: add `func desktopInsideProfile(profile, desktop string) bool`: true only when `desktop` is absolute (`filepath.IsAbs`), `insideFolder(desktop, profile)` (in `options.go`) is true and `samePath(desktop, profile)` is false. In `OSDirs`, after the table loop, look up `windows.FOLDERID_Desktop`; set `d.Desktop` only when the lookup succeeds and `desktopInsideProfile(d.UserProfile, path)`. A failed lookup leaves `Desktop` empty without failing `OSDirs`.
3. `capture.go`: in `captureDirs()` set `Desktop` to the profile path followed by `\Desktop`, next to the other user folders.
4. `desktop_dirs_windows_test.go` (first line `//go:build windows`; it uses `desktopInsideProfile`, which lives in the Windows-only file): `TestDesktopInsideProfile` (table: inside, equal to the profile, outside, relative, different drive) and `TestOSDirsDesktopIsEmptyOrInsideTheProfile` (whatever this machine returns is empty or passes `desktopInsideProfile`).
5. `desktop_dirs_test.go` (no build tag, so it runs on every OS): `TestCaptureDirsKeepsDesktop` (`captureDirs().Desktop` is `C:\Users\ana\Desktop`, and `CheckUserDirs` accepts the result of `captureDirs().User()`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestDesktopInsideProfile|TestCaptureDirsKeepsDesktop|TestOSDirsDesktop" -count=1 -v` -> 3 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output; `$env:GOOS='linux'; go vet ./internal/setup; Remove-Item Env:GOOS` -> no output (the untagged test compiles off Windows).

## Depends on
WP-74d, WP-75a
