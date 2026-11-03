# WP-71b Notification-area icon calls
risk: medium    size: S    wave: 2    model: sonnet
## Outcome
The tray package can add, retune and remove a notification-area icon on Windows: the `NOTIFYICONDATAW` structure with the right size, tooltip truncation, add with version-4 behaviour, and loading the app icon from the exe's resources with a safe fallback. Nothing creates a window yet (WP-71d does).

## Write scope
- `internal/tray/notifyicon_windows.go` (new)
- `internal/tray/notifyicon_windows_test.go` (new)

## Must not change
- Every other file, including the WP-71a files.
- No cgo; only lazy procs from `shell32.dll` and `user32.dll` plus `golang.org/x/sys/windows`.

## Changes
1. `notifyicon_windows.go` (`//go:build windows`):
   - `type notifyIconData struct` laid out exactly like `NOTIFYICONDATAW` (cbSize, hWnd, uID, uFlags, uCallbackMessage, hIcon, szTip [128]uint16, dwState, dwStateMask, szInfo [256]uint16, uTimeoutOrVersion, szInfoTitle [64]uint16, dwInfoFlags, guidItem, hBalloonIcon). On amd64 its `unsafe.Sizeof` is 976.
   - `func fillTip(dst *[128]uint16, tip string)`: encodes UTF-16, keeps at most 127 units and always writes a terminating zero; never splits a surrogate pair (drop a trailing high surrogate).
   - `func addIcon(hwnd windows.Handle, id uint32, callbackMsg uint32, icon windows.Handle, tip string) error`: `Shell_NotifyIconW(NIM_ADD)` with `NIF_MESSAGE|NIF_ICON|NIF_TIP|NIF_SHOWTIP`, then `NIM_SETVERSION` with version 4 (`NOTIFYICON_VERSION_4`). A failed add returns an error that wraps the last error; a failed set-version is ignored (log-only by the caller).
   - `func modifyTip(hwnd windows.Handle, id uint32, tip string) error` (`NIM_MODIFY`, `NIF_TIP|NIF_SHOWTIP`) and `func deleteIcon(hwnd windows.Handle, id uint32)` (`NIM_DELETE`, errors ignored).
   - `func exeIcon() windows.Handle`: `LoadImage` of resource id 1 from the running exe (`GetModuleHandle(nil)`, `IMAGE_ICON`, small-icon size, `LR_SHARED`); when that returns 0 fall back to `LoadIcon(0, IDI_APPLICATION)`. It never returns 0 on a normal desktop.
2. Tests (`notifyicon_windows_test.go`): `TestNotifyIconDataSize` (amd64: 976; use `runtime.GOARCH` to skip other architectures), `TestFillTipTruncatesTo127UTF16Units` (a 300-character tip gives 127 non-zero units and a zero at index 127; an emoji straddling the limit is dropped whole), `TestFillTipShortStringIsZeroTerminated`, `TestExeIconAlwaysReturnsAHandle` (the test binary has no icon resource, so this exercises the fallback). The tests never call `addIcon` (that would put an icon in the developer's tray); WP-71d tests do, through a hook.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/tray -run "TestNotifyIconDataSize|TestFillTip|TestExeIcon" -count=1 -v` -> 4 tests `PASS`.
- `go vet ./internal/tray` -> no output; `go build ./internal/tray` -> no output.

## Depends on
WP-71a
