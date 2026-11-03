# WP-74g Uninstall removes only the shortcuts it made, and says so
risk: medium    size: S    wave: 4    model: sonnet
## Outcome
Uninstall removes the Quit and Start-menu shortcuts, and, when it runs as the user, the Desktop and Startup shortcuts, but only each one whose target is this installation's `unravis.exe`; a link that points elsewhere (for example the user's own `Desktop\Unravis.lnk` to a portable copy) is kept and reported as kept. In the elevated worker (`Runner.NoProfileLinks`, WP-74i) no link inside the user's profile is touched: the Desktop and Startup links, and the Start-menu and Quit links of a just-for-me install (`isProfileLink`); the setup window or silent mode removes them afterwards (WP-74j). The machine-scope Start-menu and Quit links are still removed by the worker. The "what will change" text lists the new links. Repair re-creates the links through the normal install steps (WP-74f and WP-74j), so it needs no code here.

## Write scope
- `internal/setup/uninstall.go`
- `internal/setup/uiserver.go`
- `internal/setup/uninstall_shortcuts_test.go` (new)

## Must not change
- Anything else in uninstall (data folder choice, state file, registry entry) and every other file.
- Every existing line of `DescribeChanges` stays word for word (including "Start menu shortcut" and "Remove the Start menu shortcut and the Installed apps entry"), so `uiserver_test.go` is not edited. If an existing test fails, stop and report it.
- `LocalBackend.Run` in `uiserver.go` (WP-74j edits it later); here only `DescribeChanges` changes.

## Changes
1. `uninstall.go`: `removeShortcuts` handles `StartMenuLink` and `QuitLink` (each skipped when `p.r.NoProfileLinks && p.r.isProfileLink(link)`), and `StartupLink` and `DesktopLink` only when `!p.r.NoProfileLinks` (all skipped when empty). If `removeShortcuts` today also removes the Start-menu folder, skip that too under the same condition (the window does it, WP-74i). For each, call `linkIsOurs(link, p.Layout.AppExe)` (defined in `profilelinks.go` by WP-74i): not existing -> nothing; ours -> `os.Remove` (a missing file is not an error; any other error is reported with `p.leftf("Shortcut %s (%v)", l, err)` as today); exists but not ours -> `p.leftf("Shortcut %s (kept: it points to another program)", l)` and the file is untouched.
2. `uiserver.go` (`DescribeChanges`, read the uninstall block near line 428 and the install tail near line 470 first): in the uninstall block add, after the existing combined line, "Remove the Quit Unravis shortcut from the Start menu" and, when `l.DesktopLink != ""`, "Remove the Desktop shortcut (only if it points to Unravis)". In the install, upgrade and repair tail add after "Start menu shortcut": "Quit Unravis shortcut in the Start menu" and, when `l.DesktopLink != ""`, "Desktop shortcut".
3. `uninstall_shortcuts_test.go` (uses `helpers_test.go` and real temp folders): `TestUninstallRemovesDesktopAndQuitShortcuts`, `TestUninstallToleratesMissingNewShortcuts`, `TestUninstallKeepsADesktopLinkThatPointsElsewhere` (the link survives byte for byte and the result lists "kept: it points to another program"), `TestUninstallKeepsALinkItCannotRead` (garbage bytes at `Unravis.lnk` stay), `TestUninstallAsWorkerLeavesTheDesktopAndStartupLinks` (`NoProfileLinks` true, machine scope: both links stay, the Quit and Start-menu links are removed), `TestUninstallAsWorkerLeavesUserScopeStartMenuAndQuitLinks` (`NoProfileLinks` true, user scope: all four links stay), `TestDescribeChangesMentionsDesktopAndQuitShortcuts`, `TestDescribeChangesWithoutDesktopHasNoDesktopLine`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/setup -run "TestUninstallRemovesDesktop|TestUninstallToleratesMissing|TestUninstallKeeps|TestUninstallAsWorker|TestDescribeChangesMentions|TestDescribeChangesWithoutDesktop" -count=1 -v` -> 8 tests `PASS`.
- `go test ./internal/setup -count=1` -> `ok` (no existing test changed); `go vet ./internal/setup` -> no output.
- `Select-String -Path internal/setup/uninstall.go -Pattern "os\.Remove\(l\)"` -> only inside the branch guarded by `linkIsOurs`.

## Depends on
WP-74f
