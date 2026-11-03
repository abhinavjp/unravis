# WP-71c Tray popup menu
risk: low    size: S    wave: 2    model: sonnet
## Outcome
The tray package can turn a validated `[]Item` into a native popup menu and show it at the cursor, returning the chosen item's ID. Flag mapping and menu building are testable without showing anything on screen.

## Write scope
- `internal/tray/menu_windows.go` (new)
- `internal/tray/menu_windows_test.go` (new)

## Must not change
- Every other file, including the WP-71a files.
- No cgo; use lazy `user32.dll` procs.

## Changes
1. `menu_windows.go` (`//go:build windows`):
   - `func menuFlags(it Item) uint32`: separator -> `MF_SEPARATOR`; otherwise `MF_STRING`, plus `MF_CHECKED` when `Checked`, plus `MF_GRAYED|MF_DISABLED` when `Disabled`.
   - `func buildMenu(items []Item) (windows.Handle, error)`: `CreatePopupMenu`, then one `AppendMenu` per item (ID = `Item.ID`, text = UTF-16 of `Text`); a failed append destroys the menu and returns the error. An empty list returns a valid empty menu.
   - `func menuItemCount(menu windows.Handle) int` (`GetMenuItemCount`) used by tests.
   - `func showMenu(hwnd windows.Handle, items []Item) (id uint32, ok bool)`: validates with `ValidateItems` first; builds the menu; `GetCursorPos`; `SetForegroundWindow(hwnd)`; `TrackPopupMenu` with `TPM_RETURNCMD|TPM_RIGHTBUTTON|TPM_NONOTIFY`; posts `WM_NULL` to the window afterwards (the documented fix so the menu closes when the user clicks elsewhere); always `DestroyMenu`. `ok` is false when the user dismissed the menu (the call returned 0) or building failed.
2. Tests (`menu_windows_test.go`): `TestMenuFlags` (table: plain, checked, disabled, separator, checked+disabled), `TestBuildMenuHasOneEntryPerItem` (three items, `menuItemCount` is 3, then `DestroyMenu`), `TestBuildMenuAcceptsAnEmptyList`, `TestShowMenuRejectsInvalidItems` (duplicate IDs return `ok == false` without showing a menu). No test calls `TrackPopupMenu`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/tray -run "TestMenuFlags|TestBuildMenu|TestShowMenu" -count=1 -v` -> 4 tests `PASS`.
- `go vet ./internal/tray` -> no output.

## Depends on
WP-71a
