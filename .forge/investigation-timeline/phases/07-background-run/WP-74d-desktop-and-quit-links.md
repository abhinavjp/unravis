# WP-74d Desktop folder and new link paths in the installer layout
risk: low    size: S    wave: 1    model: sonnet
## Outcome
The installer's folder model knows the installing user's Desktop folder and has two new link paths: `Layout.DesktopLink` (the Desktop shortcut) and `Layout.QuitLink` (the "Quit Unravis" Start-menu entry). Nothing writes them yet (WP-74f does); this packet only adds the data and its checks.

## Write scope
- `internal/setup/layout.go`
- `internal/setup/helpers_test.go`
- `internal/setup/layout_desktop_test.go` (new)

## Must not change
- Existing `Dirs` fields, their JSON names, `NewLayout` results for the existing links, and every other file. Old saved dirs without a Desktop value must still load and validate.

## Changes
1. Read `layout.go` first (`Dirs`, `UserDirs`, `User()`, `WithUser`, `CheckUserDirs`, `Layout`, `NewLayout`). Add `Desktop string` to `Dirs` and `Desktop string` with `json:"desktop"` to `UserDirs`; `User()` copies it into `UserDirs` and `WithUser` copies it back.
2. `CheckUserDirs`: when `Desktop` is non-empty it must end with the `\Desktop` suffix by the same suffix whitelist rule the other user folders use; empty is allowed (older records, machines without a Desktop folder).
3. `const QuitShortcutName = "Quit Unravis.lnk"`. `Layout` gets `DesktopLink` and `QuitLink`. `NewLayout` sets `DesktopLink = filepath.Join(d.Desktop, ShortcutName)` only when `d.Desktop != ""` (otherwise empty), and `QuitLink = filepath.Join(filepath.Dir(StartMenuLink), QuitShortcutName)`.
4. `helpers_test.go`: `newEnv` creates a Desktop directory inside the fake user profile and puts it in the dirs it builds (next to the lines that build Programs and Startup).
5. `layout_desktop_test.go`: `TestNewLayoutHasDesktopAndQuitLinks`, `TestNewLayoutWithoutDesktopHasNoDesktopLink`, `TestQuitLinkSitsNextToTheStartMenuLink`, `TestUserDirsRoundTripKeepsDesktop` (`User()` then `WithUser`), `TestCheckUserDirsAcceptsAnEmptyDesktop`, `TestCheckUserDirsRejectsADesktopWithTheWrongSuffix`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestNewLayoutHasDesktop|TestNewLayoutWithoutDesktop|TestQuitLink|TestUserDirsRoundTrip|TestCheckUserDirs" -count=1 -v` -> the 6 new tests `PASS` (plus any existing `TestCheckUserDirs*` that match).
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output.

## Depends on
none
