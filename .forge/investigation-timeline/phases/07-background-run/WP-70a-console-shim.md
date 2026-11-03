# WP-70a Console shim for a windowless build
risk: medium    size: S    wave: 1    model: sonnet
## Outcome
`cmd/audit-explorer` gets two helpers that make a GUI-subsystem build behave on a terminal: `stdHandleUsable` says whether a standard handle can be written, and `attachParentConsole` gives a terminal-started copy its console back (printing and reading work again) without touching handles a parent passed as pipes. Nothing calls them yet (WP-70c wires them); this packet is only the helpers and their tests.

## Write scope
- `cmd/audit-explorer/console_windows.go` (new)
- `cmd/audit-explorer/console_other.go` (new)
- `cmd/audit-explorer/console_windows_test.go` (new)

## Must not change
- Every other file, including `main.go`. No new module dependency (`golang.org/x/sys/windows` is already in `go.mod`); no cgo.
- A usable standard handle is never replaced: a parent that passed pipes (the MCP client, the e2e launcher, setup's `--version` check) keeps them.
- Background: under `-ldflags -H=windowsgui` Windows hands the process NULL or invalid standard handles unless the parent passed some. `x/sys/windows` has `GetStdHandle`, `SetStdHandle`, `GetFileType` and `CreateFile`, but NOT `AttachConsole`, so that one call goes through `windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")`.

## Changes
1. `console_windows.go` (`//go:build windows`, package `main`):
   - `var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")` and `const attachParentProcess = ^uint32(0)` (ATTACH_PARENT_PROCESS).
   - `func stdHandleUsable(h windows.Handle) bool`: false when `h` is 0 or `windows.InvalidHandle`, or when `windows.GetFileType(h)` returns `windows.FILE_TYPE_UNKNOWN` (an error from `GetFileType` also means false); true otherwise (disk file, pipe and character device such as a console or NUL all count as usable).
   - `func unusableStdHandles(get func(id uint32) (windows.Handle, error), usable func(windows.Handle) bool) (unusable [3]bool)`: index 0 = `windows.STD_INPUT_HANDLE`, 1 = `STD_OUTPUT_HANDLE`, 2 = `STD_ERROR_HANDLE`; an index is true when `get` fails or `usable` says no.
   - `func attachConsoleWith(unusable [3]bool, attach func() bool, replace func(index int) bool) bool`: if no index is unusable, return false WITHOUT calling `attach`; if `attach()` returns false, return false; otherwise call `replace(i)` for each unusable index and return true when at least one `replace` returned true.
   - `func attachParentConsole() bool`: runs `attachConsoleWith(unusableStdHandles(windows.GetStdHandle, stdHandleUsable), attach, replace)` where `attach` calls `procAttachConsole.Call(uintptr(attachParentProcess))` (non-zero return = success) and `replace(i)` opens `CONIN$` (index 0, `windows.GENERIC_READ`) or `CONOUT$` (index 1 and 2, `windows.GENERIC_READ|windows.GENERIC_WRITE`) with `windows.CreateFile` (share read and write, `windows.OPEN_EXISTING`), calls `windows.SetStdHandle` with the matching id, and reassigns `os.Stdin`, `os.Stdout` or `os.Stderr` to `os.NewFile(uintptr(h), name)`. A failed `replace` leaves that handle as it was and returns false.
2. `console_other.go` (`//go:build !windows`): `func attachParentConsole() bool { return false }` and `func stdHandleUsable` is NOT defined here (only windows code uses it).
3. Tests (`console_windows_test.go`, `//go:build windows`):
   - `TestStdHandleUsableRejectsNullAndInvalid`: handle 0 and `windows.InvalidHandle` give false.
   - `TestStdHandleUsableAcceptsAPipeAndAFile`: the read end of an `os.Pipe()` and a file from `os.CreateTemp` (use `windows.Handle(f.Fd())`) give true.
   - `TestUnusableStdHandlesReportsEachHandleSeparately`: with fake `get`/`usable` where only the error handle is bad, the result is `[3]bool{false, false, true}`; a `get` error marks that index true.
   - `TestAttachConsoleWithSkipsAttachWhenNothingIsUnusable`: `attach` is never called, result false.
   - `TestAttachConsoleWithStopsWhenAttachFails`: `replace` is never called, result false.
   - `TestAttachConsoleWithReplacesOnlyTheUnusableHandles`: unusable `{false, true, true}` calls `replace` for 1 and 2 only and returns true; if both `replace` calls return false the result is false.
   - Tests never call `attachParentConsole` itself (it would change the test process's console).

## Proof
- `go` below is `.dev\toolchains\go\bin\go.exe` run from the repo root with the environment of WP-58 step 3 (`GOTOOLCHAIN=local`, `CGO_ENABLED=0`, `GOCACHE`, `GOMODCACHE`, `GOPATH`).
- `go test ./cmd/audit-explorer -run "TestStdHandleUsable|TestUnusableStdHandles|TestAttachConsoleWith" -count=1 -v` -> 6 tests `PASS` (7 with the two sub-cases counted as one each).
- `go test ./cmd/audit-explorer -count=1` -> `ok`; `go vet ./cmd/audit-explorer` -> no output.
- Other-OS stub compiles: `$env:GOOS='linux'; go vet ./cmd/audit-explorer; Remove-Item Env:GOOS` -> no output.

## Depends on
none
