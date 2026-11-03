# WP-75a Installer seams: ask Unravis to quit, and a verified terminate
risk: medium    size: M    wave: 1    model: sonnet
## Outcome
The installer's `System` gets one new optional seam, `AskQuit`, and the process list gets a verified terminate, `TerminateIf(pid, exe)`, which ends a process only when the program image of that exact process still is `exe` (a reused PID can never be killed by mistake). The fake system simulates both. `AskQuit` takes no program path: the real version (WP-75c) signals the running app's quit event inside the setup process and never starts the old, user-writable `unravis.exe`. A nil `AskQuit` means "not supported" and the installer behaves as today.

## Write scope
- `internal/setup/system.go`
- `internal/setup/fake.go`
- `internal/setup/os_windows.go`
- `internal/setup/askquit_fake_test.go` (new)
- `internal/setup/terminateif_windows_test.go` (new)

## Must not change
- Every existing `System` field, `Processes.List`, `Processes.Terminate` and the default result of `NewFakeSystem` (it leaves `AskQuit` nil, so existing tests behave as before), and every other file.
- `os_windows.go`: only `osProcesses` changes here. WP-74e (the Desktop folder) and WP-75c (the `AskQuit` wiring) edit other parts of the same file later and depend on this packet.

## Changes
1. `system.go`: add `AskQuit func() error` to `System` with a one-line comment (asks the running app of the same user to quit; nil = unsupported; takes no exe path on purpose). Add `TerminateIf(pid uint32, exe string) error` to the `Processes` interface with a comment (ends the process only when its image path is `exe`; `ErrNotFound` otherwise).
2. `fake.go`: `FakeProcesses.TerminateIf(pid, exe)` checks the same fault key as `Terminate` (`proc.Terminate:<pid>`), then removes the entry with that PID only when `samePath(entry.Exe, exe)`, else returns `ErrNotFound`. `Fakes` gets `AskQuitCalls int`, `AskQuitErr error` and `QuitRemovesApps bool`, and `func (f *Fakes) UseAskQuit(s *System)` sets `s.AskQuit` to a function that increments `AskQuitCalls`, returns `AskQuitErr` when set, and when `QuitRemovesApps` is true removes every process of the current user from `FakeProcesses.List_` (mine), leaving other users' processes. Read `FakeProcesses` and `Process{PID, Exe, SID}` first and pick "mine" the same way `splitApps` does.
3. `os_windows.go`: `func (osProcesses) TerminateIf(pid uint32, exe string) error`: `OpenProcess(PROCESS_TERMINATE|SYNCHRONIZE|PROCESS_QUERY_LIMITED_INFORMATION, false, pid)`; on the opened handle call `QueryFullProcessImageName`; when the result is empty or `!samePath(result, exe)` return `ErrNotFound` (the open handle pins the process, so the check and the kill are about the same process); otherwise `TerminateProcess(h, 1)` and wait up to 10 s as `Terminate` does. Close the handle on every path. Accepted residual (one-line note in the function comment): the check is the image path only, not the owner or the creation time. In a machine-scope install every user's copy has the same path, so a PID that another user's `unravis.exe` receives between the caller's fresh list and this open could be ended; adding an owner or creation-time check would change the `TerminateIf` signature and the `Process` record, which this packet does not do. The caller (WP-75b) only passes PIDs of the current user's own processes from a list rebuilt a moment before.
4. Tests. `askquit_fake_test.go`: `TestNewFakeSystemLeavesAskQuitNil`, `TestUseAskQuitCountsCalls`, `TestUseAskQuitReturnsTheConfiguredError`, `TestUseAskQuitCanRemoveOnlyMyProcesses`, `TestFakeTerminateIfChecksTheExe` (same PID with another exe: `ErrNotFound` and the entry stays). `terminateif_windows_test.go` (first line `//go:build windows`): `TestTerminateIfChecksTheImagePath` (start `cmd.exe /c pause` with a stdin pipe from `os.Getenv("ComSpec")`; `TerminateIf` with the path `C:\not\unravis.exe` returns `ErrNotFound` and the child is still running after it), `TestTerminateIfEndsTheMatchingImage` (the same child with the real `ComSpec` path is ended). Each test kills the child in `t.Cleanup`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestNewFakeSystemLeavesAskQuitNil|TestUseAskQuit|TestFakeTerminateIf|TestTerminateIf" -count=1 -v` -> 7 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok`; `go vet ./internal/setup` -> no output; `$env:GOOS='linux'; go vet ./internal/setup; Remove-Item Env:GOOS` -> no output; `go build ./...` -> no output.
- `Select-String -Path internal/setup/system.go -Pattern "AskQuit func\(\) error"` -> one match (no exe parameter).

## Depends on
none
