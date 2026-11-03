# WP-70f Test that the windowless build still speaks over pipes
risk: medium    size: M    wave: 5    model: sonnet
## Outcome
A Go test builds the app with the same `-H=windowsgui` flag the release build uses and proves three things on the real binary: its PE subsystem is GUI, `--version` writes the version to a pipe, and the MCP stdio server (`--mcp`) answers an `initialize` request over pipes and exits with code 0 when stdin closes. This is the explicit proof for owner answer 5 (`--version` and `--mcp` stay in the same exe).

## Write scope
- `cmd/audit-explorer/stdio_windows_test.go` (new)

## Must not change
- Every non-test file. No change to `main.go`; if a test fails because of a product bug, record the failure and stop, do not fix it here.
- The test never starts a listener, a tray or a browser. Every run sets `AUDIT_CONFIG_DIR` to a temp directory: it is both the session-file folder (`mcp.SessionDir`) and the log folder (`applog.DefaultDir`), so the user's real Unravis data is never touched.

## Changes
1. `stdio_windows_test.go` (`//go:build windows`). Helper `buildGuiExe(t *testing.T) string`: `goBin, err := exec.LookPath("go")`; on error `t.Skip("go toolchain not on PATH; run with the repo toolchain first on PATH")`. Run `goBin build -ldflags "-H=windowsgui -X api-audit-explorer/internal/version.Version=test-gui" -o <dir>\unravis-gui.exe .` in the package directory with the current environment plus `GOTOOLCHAIN=local` and `CGO_ENABLED=0`. Each test builds into its own `t.TempDir()` (the Go build cache makes the second and third build fast; there is no shared state and no `TestMain`). Every test starts with `if testing.Short() { t.Skip("builds the app") }`.
2. `TestGuiBuildHasTheWindowsSubsystem`: open the exe with `debug/pe`; assert `OptionalHeader.(*pe.OptionalHeader64).Subsystem == 2` (IMAGE_SUBSYSTEM_WINDOWS_GUI).
3. `TestGuiBuildVersionGoesToAPipe`: run `unravis-gui.exe --version` with `cmd.Stdout` a `bytes.Buffer` (Go makes a pipe) and `AUDIT_CONFIG_DIR` set; assert exit code 0 and that the output contains `test-gui`.
4. `TestGuiBuildMcpStdioRoundTrip`, with these facts verified in `main.go` and `internal/mcp`:
   - `mcp.New(0, WithSessionFile(path))` needs only a session-file path, not the file: with `AUDIT_CONFIG_DIR` set the path exists in name, and `initialize` is answered without any dashboard running.
   - Send `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` plus a newline on `StdinPipe`. Read one line from `StdoutPipe` with `bufio.Reader`; it must decode to JSON whose `result.protocolVersion` equals `"2025-11-25"`.
   - Close stdin; `cmd.Wait()` must return nil (exit code 0, which `Serve` gives on end of input).
   - A 30 second `context.WithTimeout` that kills the process only prevents a stuck run; no test asserts a duration.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3 (that puts `go` on PATH for the test).
- `go test ./cmd/audit-explorer -run "TestGuiBuild" -count=1 -v` -> 3 tests `PASS` (not `SKIP`).
- `go test ./cmd/audit-explorer -short -count=1 -v -run TestGuiBuild` -> the three tests are `SKIP`.
- `go vet ./cmd/audit-explorer` -> no output.
- Manual check, recorded in WP-77: double-click the release `unravis.exe` shows no console window.

## Depends on
WP-70d, WP-70e
