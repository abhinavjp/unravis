# Round 1 review: Phase 07 background-run

Scope: phases/07-background-run (phase.md + 32 packets WP-70a..WP-77), plan.md section 3 rows, WP-58 step 10a. Read-only review; spot-checked against `cmd/audit-explorer/main.go`, `internal/applog/applog.go`, `internal/server/launch.go`, `internal/setup/{lnk,layout,plan,system,os_windows,window_windows}.go`, `tools/e2e/support/launcher.ts`.

Verdict: no BLOCKER that stops the plan; 7 MAJOR items that make a blind implementation wrong or leave a ghost icon / stale session file; the rest MINOR.

## BLOCKER

none

## MAJOR

[MAJOR] WP-74a — `IconIndex` is specified as `int`, but `internal/setup/lnk.go` declares `Shortcut{Target, Args, WorkDir, Description, Icon string; IconIndex int32}`. The packet also says to read the arguments "the same way the target is read", but `ReadShortcutTarget` only walks the LinkInfo block (Unicode local base path offset); no StringData reader exists. A workhorse model will either change the type (breaking `Bytes()` callers) or invent a wrong reader — Fix: state `IconIndex int32`; spell out the StringData walk: after the LinkInfo block (size = uint32 at `li[0:4]`), for each header flag in order HasName 0x04, HasRelativePath 0x08, HasWorkingDir 0x10, HasArguments 0x20 read a uint16 character count then count*2 UTF-16LE bytes (IsUnicode 0x80 set); return the 0x20 string. Add a round-trip test (`Shortcut{Args:"--background"}.Bytes()` then `ReadShortcutArgs`).

[MAJOR] WP-75c — consumes `instance.Signal`, `instance.Listen`, `instance.NewNames`, `instance.Quit` (WP-72a/72c) but `Depends on` lists neither; wave ordering may build it before they exist — Fix: add `WP-72a, WP-72c` to Depends on (and to the plan.md section 3 row).

[MAJOR] WP-71d — the tray message loop is under-specified for a blind model: (1) NOTIFYICON_VERSION_4 callbacks carry the event in `LOWORD(lParam)` and the icon id in `HIWORD(lParam)`; the packet does not say so and does not list constants (WM_USER+1 callback id, NIN_SELECT 0x400, WM_LBUTTONUP 0x202, WM_RBUTTONUP 0x205, WM_CONTEXTMENU 0x7B, NIM_SETVERSION). (2) It handles both WM_RBUTTONUP and WM_CONTEXTMENU, which under version 4 both arrive for one right click, so the menu shows twice or flickers. (3) It does not say how the single shared window procedure finds its per-tray state (a package-level pointer guarded by a mutex, one tray per process), nor what the "ready" hook receives (the window handle, the posted-quit function). (4) No reentrancy guard on TrackPopupMenu — Fix: handle only WM_CONTEXTMENU (and NIN_SELECT / WM_LBUTTONUP for the left click); add the constants and the LOWORD decode; name the package-level state variable; define the ready hook signature; ignore a second menu request while one is open.

[MAJOR] WP-73c / WP-73d — the "could not open the browser" `warn` is a modal MessageBox. WP-73c calls it from the `instance.Listen` handler goroutine and WP-73d from the tray menu callback, so a modal box blocks the signal listener (second launches then queue silently) or the tray thread (the icon stops responding, the shell may flag it hung) — Fix: wrap every `warn(...)` call in `go warn(...)`, and add a test that a blocking `warn` fake does not stop the next signal being handled.

[MAJOR] WP-73c / WP-73d — `run()` returns (and the process exits) without waiting for `tray.Run` to return, so `NIM_DELETE` may not run and a ghost icon stays in the notification area until hover; this contradicts WP-77 manual check M2 (icon gone after Quit) — Fix: `run` creates a `trayDone` channel closed when `tray.Run` returns, and after `cancel()` waits for it with a bounded select (2 s fallback, only a guard, not an assertion); test with a fake tray that records Remove before run returns.

[MAJOR] WP-73d — `OnEndSession: cancel` returns immediately. On WM_ENDSESSION Windows terminates a GUI process shortly after the handler returns, so the shutdown path (server stop, `session.json` removal) may not finish and a stale `session.json` is left, which the MCP side then trusts — Fix: the end-session handler calls `cancel()` and then waits (bounded, 3 s guard) on the run-complete channel before returning; test with a fake that completes after the handler starts.

[MAJOR] WP-74f — Write scope omits the other `internal/setup` test files whose expectations the packet itself says must change (anything asserting the exact list of written files or the `shortcuts` step output; WP-74d also adds a Desktop dir to `helpers_test.go`) — Fix: list every `internal/setup/*_test.go` the packet edits (find them with `Select-String -Path internal/setup/*_test.go -Pattern "StartMenuLink|StartupLink"` and name the hits), or state the single file to edit and require "no other test file changes".

## MINOR

[MINOR] WP-73a — invents a new `validateFlags` although `parseFlags`/`runFlags` already exist in `cmd/audit-explorer/main.go` (and `parseFlags` returns early for `--version`). "No other flag with `--quit`" detection is unspecified; `runQuit` shows a modal box even on success — Fix: extend `parseFlags` instead; define "other flag" as `flag.NFlag() > 1`; show a message box only on failure, print nothing on success.

[MINOR] WP-70c — the "split if shared" hedge is unneeded; say do one packet. The launch link moves from stderr to stdout: checked `tools/e2e/support/launcher.ts:86-124`, which reads both streams, so the e2e launcher is unaffected (no blocker). The log field `launch=link_printed` is misleading under `--background` (nothing printed) — Fix: remove the hedge; log `launch=none` when `--background` or no console.

[MINOR] WP-70b — contradictory typed-nil text (the tee both "returns nil writer" and "never returns a typed nil") — Fix: pick one: `NewTee` returns `io.Writer`; with both targets unusable it returns `io.Discard`.

[MINOR] WP-70d — says "replace every `log.Fatal`", but `main()` has exactly one (`cmd/audit-explorer/main.go:53`) — Fix: say "the one `log.Fatal(err)` at main.go:53".

[MINOR] WP-70e — `run.ps1` ArgumentList quoting is unspecified (paths with spaces), and `-NoNewWindow` is irrelevant for a GUI-subsystem exe — Fix: pass an array to `Start-Process -ArgumentList` with each element quoted, drop `-NoNewWindow`.

[MINOR] WP-70f — relies on `go` being on PATH inside the test (use `exec.LookPath("go")` and `t.Skip` with a clear message otherwise); it does not say what `mcp.New` does with no session file, which protocolVersion to send in `initialize`, or the expected exit code when stdin closes — Fix: state the three values after reading `cmd/audit-explorer/main.go` ~129-146.

[MINOR] WP-72d — the "possibly wrapped" hedge about `server.New` is unneeded: `server.New` (server.go:98) returns `*handler` unwrapped, and `launchState` is the value field `h.launch` — Fix: state it; `Rearm` is a method on `*handler`, reached through the existing exported accessor pattern.

[MINOR] WP-74c — the `UserHomeDir` containment assertion is flaky for users with redirected Startup/Desktop folders (OneDrive, roaming) — Fix: assert only that the result is absolute and ends with `Startup`, compared case-insensitively.

[MINOR] WP-74e — build tag of the test file not stated (it must be `//go:build windows`) — Fix: add it.

[MINOR] WP-77 M6 — "port taken" cannot occur with the default `--port 0` — Fix: change the manual step to start with `--port <busy port>` or drop it.

[MINOR] WP-75 vs research — installer `StartProgram` (window_windows.go:54, "start as the person running the window") and the unravis-setup console behaviour are not touched in the packets, while the research implies the post-install launch should hit the new single-instance path — Fix: add one sentence to WP-75b confirming `StartProgram` is unchanged and that a second launch just signals Open.

## Checked and fine (none)

- Packet sizes: all S/M.
- Write-scope overlaps: none within a wave on `main.go`, `plan.go`, `os_windows.go`, uiserver files; chains 70c->70d->73a->73b->73c->73d (main.go) and 74f->74h->75b (plan.go) are correct.
- Windows API: NOTIFYICONDATAW size 976 on 64-bit, TrackPopupMenu with the WM_NULL post, `Local\UnravisApp` names with auto-reset events and the default DACL (the security canary test covers Everyone/Users).
- INV-5: no timing assertions; retry budgets and fake clocks only.
- GUI-subsystem pitfalls (nil std handles, CONIN$/CONOUT$, AttachConsole for `--version`, `--mcp` over pipes): covered by WP-70a/70b/70f.
- Owner answers: start-at-sign-in setting with Startup shortcut as single source, keep running after the last tab, Desktop shortcut and "Quit Unravis" Start-menu entry, `--version`/`--mcp` in the same exe, uninstall/repair/rollback coverage.
- WP-58 step 10a matches the last line of WP-77.
