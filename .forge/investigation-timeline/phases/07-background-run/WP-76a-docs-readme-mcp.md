# WP-76a README, MCP guide and installer plan for background run
risk: low    size: S    wave: 8    model: haiku
## Outcome
The top-level docs describe how Unravis now runs: no terminal needed, a tray icon, a log folder, quit options, and the `--background` and `--quit` flags. The MCP guide notes that the same exe still serves MCP over pipes. The installer plan lists the new shortcuts and the sign-in setting.

## Write scope
- `README.md`
- `MCP.md`
- `buildout/plan/installer-and-releases.md`

## Must not change
- Every other file. No claim that was not verified: a behaviour only checked by hand is described as "expected" and points to `docs/background-run-acceptance.md`.

## Changes
1. `README.md` (the "Start" section: the `Start Live.cmd` paragraph that tells the user to keep the terminal open, and the `-NoBrowser` paragraph): say that Unravis needs no terminal; it starts in the tray, a second start opens a new browser tab in the running copy, and it keeps running after the last tab closes until the user chooses Quit (tray menu "Quit Unravis", the Start-menu entry "Quit Unravis", or `unravis.exe --quit`). Mention that Windows 11 may put a new icon in the hidden-icons area, with a tip to drag it to the taskbar. Mention the log folder (tray menu "Open log folder"), start at sign-in (installer checkbox, tray item "Start at sign-in") and that `--version` and `--mcp` still work from a terminal. For scripts: `start "" /wait unravis.exe ...` in cmd, or `Start-Process -Wait -PassThru` in PowerShell, because a windowless program is not waited for. State once that Unravis does not run as administrator: a copy started with "Run as administrator" is refused with a message, so a normal start is never locked out; start it from the Start menu or a normal window. State that `--background` opens no browser and shows no link until someone asks (tray "Open Unravis" or a second start).
2. `MCP.md` (around line 77): the MCP command is unchanged (`unravis.exe --mcp` over stdin and stdout); the program is built without a console window, which does not change pipes a client passes. Note that the session file (`session.json`) is removed when the app quits, so an MCP client started with no app running reports that Unravis is not running.
3. `buildout/plan/installer-and-releases.md`: add a short "Background run" section: the Desktop shortcut for the installing user and the Startup shortcut (and, for a just-for-me install, the Start-menu and Quit entries) are written as the user, by the setup window or by silent mode, never by the elevated worker, and the elevated worker writes no log file of its own (its lines appear in the window's log as `worker: ...`); the "Quit Unravis" Start-menu entry; the Startup shortcut with `--background` (setting-based; installer default ON); uninstall removes them; repair re-creates them; a failed install leaves none of them (the machine-scope Quit and Start-menu entries are rolled back with the install, and every link inside the user's profile is only written after a successful install); upgrade asks the running app to quit and then terminates what is left (only a process whose program path is still the installed `unravis.exe`). Record one known limit: if the setup window is closed or crashes between the worker's commit and the removal of the Startup link on an uninstall, an orphan Startup link to a deleted exe can remain (an error at sign-in; delete the link by hand). Add the single-instance rule: the instance names contain the Windows account's SID (`UnravisApp.<sid>` in the `Local` namespace), so each account has its own copy, and a copy owned by another account or running as administrator is reported as "another Windows account or as administrator". The SID in a name is public, so an object name already held by another account is detected, not prevented: Unravis compares the owner of the object with the current user and, when they differ, shows its own "another process holds Unravis's name" message and exits.

## Proof
- `Select-String -Path README.md -Pattern "Quit Unravis"` -> at least one match; `Select-String -Path README.md -Pattern "--quit"` -> at least one match.
- `Select-String -Path README.md,MCP.md -Pattern "--background"` -> at least one match in `README.md`.
- `Select-String -Path README.md -Pattern "administrator"` -> at least one match.
- `Select-String -Path MCP.md -Pattern "windowless|console window"` -> at least one match.
- `Select-String -Path buildout/plan/installer-and-releases.md -Pattern "Background run"` -> one match; `Select-String -Path buildout/plan/installer-and-releases.md -Pattern "UnravisApp\."` -> at least one match.
- `Select-String -Path buildout/plan/installer-and-releases.md -Pattern "detected, not prevented"` -> one match.
- `git diff --stat -- README.md MCP.md buildout/plan/installer-and-releases.md` shows only these three files changed by this packet.

## Depends on
WP-73e, WP-70e, WP-74g, WP-74j, WP-75b
