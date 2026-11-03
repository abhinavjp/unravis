# Round 2 review: Phase 07 background-run

Scope: phase.md, the 33 packets WP-70a..WP-77 (WP-70a..75c, 76a, 76b, 77), plan.md section 3 rows, round1-p07.md. Read-only. Spot-checked against `cmd/audit-explorer/main.go`, `internal/server/{launch,server}.go`, `internal/setup/{lnk,layout,plan,system,fake,capture,uninstall,uiserver,options,os_windows,window_windows,fixes_b}.go`, `internal/setup/uiserver_test.go`, `build.ps1`.

`check-plan.ps1`: `packets in plan: 77; packet files: 77; errors: 0; same-wave overlaps: 0; warnings: 0` then `OK`.

Verdict: all round-1 MAJORs are fixed. No BLOCKER, no MAJOR. Seven MINOR items remain (none stops a blind implementation; each is a one-line clarification).

## Round-1 verification by id

| Round-1 finding | Verdict |
|---|---|
| MAJOR 74a `IconIndex` type + no StringData reader | FIXED. `int32`, StringData walk and `ReadShortcutArgs` round-trip test specified; matches `lnk.go` (`Shortcut` fields, `Bytes()` string order, `ReadShortcutTarget` reads LinkInfo only). New package `internal/shelllink` is consistent with it. |
| MAJOR 75c missing Depends on 72a/72c | FIXED (packet and plan.md row: 72a, 72c, 75a, 74e). |
| MAJOR 71d tray message loop | FIXED: LOWORD/HIWORD decode, single WM_CONTEXTMENU path, package-level state with mutex, ready hook, popup reentrancy guard. Two constants still missing (see MINOR 1). |
| MAJOR 73c/73d modal `warn` blocks listener/tray | FIXED: `go d.warn`, one-at-a-time CompareAndSwap guard, test with a blocking fake. |
| MAJOR 73c/73d ghost icon after quit | FIXED by new WP-73e (`startTray`, `stopTray`, `waitFor`, `trayExitGuard` 2 s as a guard only, fake tray records Remove before `run` returns). |
| MAJOR 73d `OnEndSession` returns at once | FIXED: `endSessionHandler` cancels and waits on `runDone` (`endSessionGuard` 3 s); `main` closes `runDone` after `run`; `stopTray` skips its wait while ending. |
| MAJOR 74f write scope | FIXED: test files named; the lines they assert were checked against the source. |
| MINOR 73a (`validateFlags`) | FIXED: extends `parseFlags`, `flags.NFlag() > 1`, box only on failure. Real `parseFlags` uses a local FlagSet named `flags`, so `NFlag()` exists. |
| MINOR 70c hedge, `launch=` log field | FIXED (one packet; `launch=none` for background/no console). |
| MINOR 70b typed nil | FIXED (`io.Writer`, `io.Discard`). |
| MINOR 70d single `log.Fatal` | FIXED (names the one call in `main()`). |
| MINOR 70e run.ps1 quoting | FIXED. |
| MINOR 70f LookPath / values | FIXED. |
| MINOR 72d hedge | FIXED: states `server.New` returns `*handler` and `launchState` is the value field `h.launch`; matches `launch.go`/`server.go`. |
| MINOR 74c UserHomeDir assertion | FIXED (absolute + ends with `Startup`, case-insensitive). |
| MINOR 74e build tag | FIXED (`//go:build windows`). |
| MINOR 77 M6 port taken | FIXED: `--port <busy port>`. |
| MINOR 75b `StartProgram` | FIXED: sentence states it is unchanged. Confirmed `window_windows.go` `StartProgram` is a plain `exec.Command(exe)`. |

## BLOCKER

none

## MAJOR

none

## MINOR

[MINOR] WP-71d — the tests use `NIN_SELECT` (0x400), `WM_RBUTTONUP` (0x205) and `WM_NULL` (0) but the constants list omits them; a blind model invents values or imports from `x/sys/windows` under another name — Fix: add the three constants to the list in the Changes step that already lists `WM_USER+1`, `WM_LBUTTONUP`, `WM_CONTEXTMENU`.

[MINOR] WP-71d `TestTrayRetriesWhenAddFails` — the test drives the retry with a manual `WM_TIMER` message while the real 2 s `SetTimer` is also armed; on a slow machine the real timer can fire during the test and add a second `Add` call (INV-5-adjacent flake) — Fix: have the test inject the retry timer through the existing fake seam (or assert `Add` was called at least twice, not exactly twice).

[MINOR] WP-70d — the helper `ptr(...)` used in `fatal_windows.go` is never defined; `windows.MessageBox(hwnd, text *uint16, caption *uint16, flags)` needs `windows.UTF16PtrFromString` and the text-then-caption argument order — Fix: replace `ptr(s)` with "`p, _ := windows.UTF16PtrFromString(s)` for each of text and caption (text first)".

[MINOR] WP-73c — the local variable is named `reopener` and shadows the type `reopener` (legal with `:=`, but a blind model may then fail to compile a second use of the type in the same scope) — Fix: name the variable `reo`.

[MINOR] WP-72c — the Outcome says an "elevated process of the same user" is included; only the elevated-to-normal direction works (a normal copy cannot open the events of an elevated one, default DACL), and that case is already covered by the WP-73b `ErrElevated` path and manual row M14 — Fix: reword the Outcome to "an elevated copy can signal a normal copy of the same user; the reverse is reported as `ErrElevated`/another-account by WP-73b".

[MINOR] WP-75b / WP-75c — an elevated setup worker that runs as a different administrator account computes different SID-scoped names, so `AskQuit` returns `ErrNoInstance` and setup falls through to the verified `TerminateIf` path; correct and safe, but the packets do not say so, and a model may try to "fix" it — Fix: one sentence in WP-75c Must-not-change: "ErrNoInstance from a different account is expected; do not add a cross-account signal".

[MINOR] WP-76a — "the paragraphs near lines 7 and 21" of README.md: line 7 is the `Start Live.cmd` "keep its terminal open" paragraph, line 21 is the live-data/`-NoBrowser` paragraph (checked); the anchor is loose, though the Proof greps make a wrong placement visible — Fix: say "the 'Start' section (the `Start Live.cmd` paragraph that tells the user to keep the terminal open, and the `-NoBrowser` paragraph)".

## Checked and fine

- Dependency edges and waves: os_windows.go chain 75a (w1) -> 74e (w2) -> 75c (w3); plan.go chain 74f (w2) -> 74h (w3) -> 75b (w4); main.go chain 70c (w2) -> 70d (w3) -> 73a (w4) -> 73b (w5) -> 73c (w6) -> 73e (w7). 74e and 74f are in the same wave with disjoint write scopes. No same-wave overlap (check-plan agrees).
- WP-73e and WP-76a/76b dependency lists cover the symbols they document (73e, 70e, 74g, 75b); WP-77 depends on 76a, 76b, 70f, 75c; the packets of A5 (71d, 72c, 73a, 73c, 74b, 74g, 75b) are transitive dependencies.
- WP-74e adds `Desktop` to `captureDirs()` in `capture.go`. `uiserver_test.go` (lines ~421, ~454) calls `DescribeChanges(captureDirs())` but asserts only the counts of "Start menu shortcut"/"Installed apps entry" and `len(WillNotChange) == 4`, and WP-74g keeps every existing `DescribeChanges` line word for word, so `uiserver_test.go` needs no edit. The step titles in `capture.go` (lines 115, 120, 196) are not renamed by any packet.
- `Processes` has only `List`/`Terminate`, implemented by `FakeProcesses` (fake.go) and `osProcesses`; adding `TerminateIf` (WP-75a) cannot break another implementer.
- `UserDirs` gains `Desktop` (WP-74d); `CheckUserDirs` suffix whitelist and `removeShortcuts` in `uninstall.go` are named correctly in WP-74d/74g.
- Real `main.go`: single `log.Fatal(err)` in `main()`, `parseFlags` with a local FlagSet, `startupLines(localURL, code, life, demo, opened)`, `newLaunchCode`, `shutdownOnSignal`, `var stdout io.Writer` all exist as the packets say. The launch code is registered with `applog.RegisterSecret`.
- Timing: no hard timing assertion. The 20 x 250 ms retries, 2 s/3 s/10 s/30 s guards and the 2 s re-open throttle are guards or product rules with fake clocks (INV-5 respected).
- Security: events grant nothing to Everyone/Users (`TestEventsGrantNothingToEveryone`), the quit route runs no program, setup never starts the old exe, the launch link never reaches the log.
- Sizes: all S or M. Docs packets 76a/76b are Haiku-sized and have grep-based Proofs.
- `build.ps1` currently has no `windowsgui` (lines 102-127 use only `-ldflags $LdFlags`); WP-70e is the packet that adds it and WP-77 A6 checks it after.

## Judgment: the 14 PENDING manual rows in WP-77

Reasonable. Every row needs something a unit test cannot give (reboot, logoff, Explorer restart, second account, elevation, Defender/SmartScreen, clean VM, a real tray). The `PENDING` rule, the "no manual row is ever PASS" rule, the accepted-risk note and the last-line verdict `ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING` (matches WP-58 step 10a) are consistent with phase.md section 7. Two small notes, neither a finding: M9 tests only the removal side, so add "after install, the Desktop and Quit shortcuts exist" to M9's expected text; and the trailing sentence "M11 also checks the text names ..." is stray and can move into M11's expected column.
