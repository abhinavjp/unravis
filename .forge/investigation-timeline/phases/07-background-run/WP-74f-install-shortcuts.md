# WP-74f Install writes the Quit shortcut, and the Desktop and sign-in shortcuts when it runs as the user
risk: medium    size: M    wave: 3    model: sonnet
## Outcome
The installer's `shortcuts` step creates the "Quit Unravis" Start-menu entry (target `unravis.exe`, argument `--quit`). It creates the Desktop shortcut for the installing user (when there is a Desktop folder and no shortcut of the same name already points at another program) and the start-at-sign-in shortcut (now with the argument `--background`). When `Runner.NoProfileLinks` is true (the elevated worker) the step skips every link inside the user's profile: always the Desktop and Startup links, and the Start-menu and Quit links too when their path is inside the profile (`isProfileLink`, a just-for-me install). The setup window or silent mode writes those afterwards (the approach in WP-74i, carried out by WP-74j); a machine-scope Start-menu and Quit link (ProgramData) stay in the transaction. A Desktop or Startup folder that is itself a junction or link is skipped for that one link with a log line; it never fails the install. A failed in-process install rolls all written links back through the existing transaction.

## Write scope
- `internal/setup/plan.go`
- `internal/setup/plan_shortcuts_test.go` (new)
- `internal/setup/plan_shortcuts_windows_test.go` (new)
- `internal/setup/setup_test.go`
- `internal/setup/fixes_b_test.go`

## Must not change
- The other plan steps, the transaction and rollback code, `layout.go`, `helpers_test.go`, `profilelinks.go` and every other file in `internal/setup`. The helpers (`startupLinkArg`, `quitLinkArg`, `readLinkFile`, `linkTarget`, `linkIsOurs`, `refuseLinkedFolder`, `isProfileLink`) already exist from WP-74i: use them, define none.
- The existing Start-menu shortcut still opens Unravis exactly as before.
- Test files: only `setup_test.go` and `fixes_b_test.go` may change, and only an expected value (they list the links by `StartMenuLink|StartupLink` at `setup_test.go` lines ~268, 666-703, 962 and `fixes_b_test.go` line ~511). Never weaken or delete an assertion. If a test in any other file fails because of this packet, stop and report it instead of editing that file.

## Changes
1. Read the `shortcuts` step in `plan.go` (it builds a `Shortcut` through the aliases of WP-74a and writes `StartMenuLink` and, when `p.Opts.StartAtSignIn`, `StartupLink` with `t.MkdirAll` and `t.WriteFile(link, data, "shortcut")`), `refuseLink` in `fixes_b.go` and `profilelinks.go` (WP-74i).
2. Step order after the Start-menu link:
   - The Quit link (`p.Layout.QuitLink`; Target `p.Layout.AppExe`, Args `quitLinkArg`, WorkDir the install dir, Description "Quit Unravis", Icon the exe). The Start-menu link and the Quit link are both skipped when `p.r.NoProfileLinks && p.r.isProfileLink(link)`; otherwise written.
   - Only when `!p.r.NoProfileLinks`: the Desktop link when `p.Layout.DesktopLink != ""` and NOT (`exists && !ours` from `linkIsOurs(DesktopLink, AppExe)`), with the same fields as the Start-menu link; and the Startup link when `p.Opts.StartAtSignIn`, with Args `startupLinkArg`. A Desktop link that points elsewhere is left alone.
   - Same `t.MkdirAll` / `t.WriteFile` calls, so rollback removes new files and restores replaced ones.
3. Junction guard (leaf folder only, WP-74i): before the write of the Desktop link and of the Startup link call `p.r.refuseLinkedFolder(filepath.Dir(link))`; on an error skip THAT link and log `shortcut skipped: <error>` the way other steps in `plan.go` log (read `closeApp`); do not return the error from the step and do not touch the other links.
4. Run the whole `internal/setup` suite and update only the expected values the new links change in `setup_test.go` and `fixes_b_test.go`; list each changed test in the packet note.
5. `plan_shortcuts_test.go` (uses the helpers in `helpers_test.go`, real temp folders and the fake system): `TestInstallWritesTheQuitShortcut` (read back with `ReadShortcutTarget` and `ReadShortcutArgs`: target is the installed exe, args `--quit`), `TestInstallWritesTheDesktopShortcut`, `TestInstallWithoutADesktopFolderSkipsTheDesktopShortcut`, `TestInstallKeepsAnExistingDesktopLinkThatPointsElsewhere` (a planted `Unravis.lnk` to another exe is byte-for-byte unchanged), `TestStartupShortcutHasTheBackgroundArgument`, `TestFailedInstallRemovesTheNewShortcuts` (a fault injected after the shortcuts step leaves no Desktop, Quit or Startup link), `TestInstallWithoutStartAtSignInWritesNoStartupLink`, `TestPlanWithNoProfileLinksWritesOnlyTheStartMenuAndQuitLinksInMachineScope` (`NoProfileLinks` true, machine scope: Start-menu and Quit links exist; no Desktop link, no Startup link, even with the sign-in option on; the Desktop and Startup folders are not even created), `TestPlanWithNoProfileLinksWritesNoLinkUnderTheProfileInUserScope` (`NoProfileLinks` true, user scope: none of the four links exists and none of their folders is created). `plan_shortcuts_windows_test.go` (first line `//go:build windows`): `TestInstallSkipsAJunctionedDesktopAndStillSucceeds` (the test makes the Desktop folder a junction with `cmd /c mklink /J <desktop> <other temp dir>`; the install succeeds, no Desktop link exists, the Startup and Quit links exist and nothing is written into the other folder), `TestInstallAcceptsADesktopUnderAReparsePointParent` (the Desktop folder is a normal folder inside a junctioned parent, like OneDrive: the Desktop link is written).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestInstallWritesThe|TestInstallWithoutADesktop|TestInstallKeepsAnExistingDesktop|TestStartupShortcutHas|TestFailedInstallRemoves|TestInstallWithoutStartAtSignIn|TestPlanWithNoProfileLinks|TestInstallSkipsAJunctioned|TestInstallAcceptsADesktop" -count=1 -v` -> 11 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok` (list any test whose expectation changed, with the reason).
- `go vet ./internal/setup` -> no output; `$env:GOOS='linux'; go vet ./internal/setup; Remove-Item Env:GOOS` -> no output.
- `Select-String -Path internal/setup/plan.go -Pattern "func linkIsOurs|func linkTarget|const startupLinkArg"` -> no output (the helpers live in `profilelinks.go`).

## Depends on
WP-74a, WP-74d, WP-74i
