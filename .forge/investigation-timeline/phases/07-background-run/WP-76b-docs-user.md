# WP-76b User guides for install, upgrade, uninstall and troubleshooting
risk: low    size: S    wave: 8    model: haiku
## Outcome
The user guides match the new behaviour: what the installer adds (Desktop shortcut, Quit entry, start at sign-in), what happens to a running copy on upgrade, what uninstall and repair do with the shortcuts, and how to fix the common problems (no icon visible, the second launch seems to do nothing, the port is taken, the app will not start).

## Write scope
- `docs/user/install.md`
- `docs/user/upgrade-and-uninstall.md`
- `docs/user/troubleshooting.md`

## Must not change
- Every other file. Steps that were not run on a clean machine carry the "NOT RUN" marker the install guide already uses; do not describe a manual result as verified.

## Changes
1. `install.md`: the installer page has a checkbox "Start Unravis when I sign in" (on by default); the installer adds a Desktop shortcut for the installing user and a Start-menu entry "Quit Unravis"; after install Unravis starts in the tray; where to find the tray icon on Windows 11 (the hidden-icons arrow). Add the steps for a manual check on a clean machine and mark them "NOT RUN" until `docs/background-run-acceptance.md` shows them done.
2. `upgrade-and-uninstall.md`: upgrade while Unravis runs asks the app to quit and then closes what is left (with the existing consent prompt); a graceful quit only works when the setup window and its administrator part run as the same Windows account, otherwise setup ends the program instead (it checks the program path first); the sign-in setting you chose in the tray is kept; uninstall removes the Desktop shortcut, the Quit entry, the Start-menu entry and the sign-in shortcut; repair puts them back; if an install fails midway none of the new shortcuts is left behind. Uninstall removes a shortcut only when it points to this installation (a Desktop shortcut of your own that points elsewhere is kept). A `Unravis.lnk` in your Startup folder that points to another program is not changed by the tray item "Start at sign-in"; delete it yourself if you want to use the tray item. The Start at sign-in shortcuts that other Windows accounts created from the tray stay behind after uninstall and point to a program that is gone; they do nothing harmful, and each account can delete `Unravis.lnk` from its own Startup folder.
3. `troubleshooting.md`: new entries: "I do not see the icon" (hidden-icons area, pin it); "Starting Unravis again does nothing" (it opens a tab in the running copy; if it says Unravis is running but cannot be reached, quit it from Task Manager); "Unravis says it is running under another account" (sign in as that account or quit it there); "Unravis could not start" message box and where the log folder is; "turn start at sign-in off" (tray item or delete `Unravis.lnk` in the Startup folder); "quit from a script" (`unravis.exe --quit`); "Unravis does not run as administrator" (start it from the Start menu or a normal window; an elevated copy is refused so it cannot block the normal one); "Unravis is already running under another Windows account or as administrator" (quit it there). Add a short "What shows up in logs and security tools" paragraph: opening a tab starts `rundll32.exe url.dll,FileProtocolHandler <link>`, so the one-time link appears on that command line in process-creation auditing (event 4688, Sysmon, EDR); the link works once and only for 2 minutes, and a browser-open failure message never shows it.

## Proof
- `Select-String -Path docs/user/install.md -Pattern "Start Unravis when I sign in"` -> one match; `Select-String -Path docs/user/install.md -Pattern "NOT RUN"` -> at least one match.
- `Select-String -Path docs/user/upgrade-and-uninstall.md -Pattern "Quit Unravis"` -> at least one match; `Select-String -Path docs/user/upgrade-and-uninstall.md -Pattern "other Windows accounts"` -> one match; `Select-String -Path docs/user/upgrade-and-uninstall.md -Pattern "same Windows account"` -> one match.
- `Select-String -Path docs/user/troubleshooting.md -Pattern "4688"` -> one match; `Select-String -Path docs/user/troubleshooting.md -Pattern "as administrator"` -> at least two matches.
- `Select-String -Path docs/user/troubleshooting.md -Pattern "hidden"` -> at least one match; `Select-String -Path docs/user/troubleshooting.md -Pattern "--quit"` -> at least one match.
- `git diff --stat -- docs/user` shows only these three files changed by this packet.

## Depends on
WP-73e, WP-75b, WP-74g, WP-74j
