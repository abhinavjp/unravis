# Tray, update notices and Blackbox-only servers: analysis and recommendation

Asked by nachi (2026-11-26): show the installed version and useful indicators in the system tray; tell the person when Unravis or Blackbox
has been updated (or is updating); say what happens on a server that has only Blackbox. Analysis first, prototype next
(`web/prototype/tray-and-notices-PROTOTYPE.html`), nothing built until nachi approves.

## What exists today (checked in the code)

| Part | Today |
| --- | --- |
| Unravis tray | `internal/tray` (raw Win32 `Shell_NotifyIcon`, version 4) and `cmd/audit-explorer/trayapp.go`. Menu: Open Unravis, Open log folder, Start at sign-in, Quit. The tooltip is a fixed word. The menu is rebuilt each time it opens (`Options.Menu`), at most 16 flat items of 64 characters; an item can be disabled (greyed). |
| Pop-up notices | The notice structure already has the balloon fields, but nothing sends one. Messages today use a plain message box (`showMessage`). |
| Update state in the app | `internal/server/updater.go` `Status()`: up to date / available / downloading / ready / failed, current version, channel, checked time, and `Result` = the last silent update (`last-update.json`: done, rolled-back, failed, from, to, plain message). The Updates page (PR #338) already reads it. |
| Unravis after an update | An unattended update writes `last-update.json`, the app restarts, and the next start can read it. Nothing tells the person outside the Updates page. |
| Blackbox | A Windows service (`UnravisBlackbox`) in session 0. It has no desktop, so it can never own a tray icon or show a pop-up. It serves `/status` on `127.0.0.1:7788` with `update` (`mode`, `state`, `latest`, `checked`, `result`) and already writes to the Windows Event Log. |
| Blackbox updating itself | `SelfUpdater` (`internal/blackbox/selfupdate.go`): modes install / notify / off; it does nothing when the `managed-by-unravis` marker exists (Unravis updates it then); a helper installs the new program and rolls back when it does not answer. States: idle, managed, available, held, skipped, downloading, installing, failed, off. |
| Unravis showing Blackbox | `internal/server/blackbox.go` already copies a Blackbox's update state into the Blackbox screen (`bbUpdate`). |
| Tauri shell tray | BL-308 (not built). The shell would own the tray; this design must not be a second tray. |

## The answers

1. **Tray:** one tray icon per signed-in person, owned by the Unravis app. A Blackbox-only server has none, because a service cannot show one.
2. **Installed version and indicators:** put the version in the tooltip and as the first (greyed) line of the menu, then one line per thing that
   needs a glance. No indicator that is always the same.
3. **Update notices:** a pop-up (Windows notification) only when something changed or went wrong. Never for a check that found nothing.
4. **Blackbox-only server:** no tray. The honest places are (a) the Windows Event Log (already used), (b) the `/status` page, and
   (c) any Unravis app that has this server in its list, which shows the update state and raises the same notices from it.
   Where a person sits at the server and wants a tray, an optional tiny Blackbox helper in their own session is offered, off by default.

## Indicators (what the tray says)

Tooltip, 127 characters at most: `Unravis 1.4.0-rc.62 · up to date` / `... · update ready, restart to finish` / `... · Blackbox: last sample 12 s ago`.

Icon: the normal icon; a small dot on it only when attention is needed (update ready and waiting, update failed or rolled back, Blackbox stopped
or silent for more than 5 minutes). One dot colour per kind is not needed: the menu says what.

Menu (flat, as the validator needs), top to bottom:

| Line | Behaviour |
| --- | --- |
| `Unravis 1.4.0-rc.62 (rc channel)` | greyed, the installed version |
| `Up to date, checked 3 minutes ago` / `Update 1.4.0-rc.63 ready: Restart now` / `Updating to 1.4.0-rc.63...` / `Update failed: see Updates` | one line; clickable only when it is an action |
| `Blackbox: recording, last sample 12 s ago` | greyed; only when a recorder is known; `Blackbox: stopped` is a clickable line that opens its screen |
| `Code mode is on` | only while on; opens its settings |
| separator | |
| `Open Unravis` | |
| `Check for updates` | runs a check and shows the answer on the line above |
| `What's new` | opens the What's new window for the installed version |
| `Open Blackbox` | opens the Blackbox screen (only when a recorder exists) |
| `Start at sign-in` | as today |
| `Open log folder` | as today |
| separator | |
| `Quit Unravis` | |

A line is dropped when it has nothing to say; the longest menu is 12 items, inside the limit of 16.

## Notices (Windows notifications)

Sent by the app through the tray icon's own notification (`NIF_INFO`), so no extra registration, no extra program and no network. Clicking the
notice runs its one action. A button inside the notice would need a separate app identity and a COM activator; that is more code and more attack
surface than this needs, so the click is the action. (A real button is a later option, noted below.)

| When | Notice | Click opens |
| --- | --- | --- |
| Unravis starts after an unattended update that worked | `Unravis has been updated to 1.4.0-rc.63` / `Click to see what is new.` | What's new |
| Unravis starts after an update that rolled back or failed | `Unravis could not update to 1.4.0-rc.63` / `You are still on 1.4.0-rc.62. Nothing was lost.` | Updates page |
| An update is downloaded and waiting for the person (not auto-applied) | `An update is ready` / `1.4.0-rc.63 will install when you restart Unravis.` | Updates page |
| An unattended update is about to restart the app | `Unravis is updating and will restart in a moment` (one notice, then the restart) | nothing |
| A Blackbox this app knows changes version | `Blackbox on SERVER-A has been updated to 1.4.0-rc.63` | Blackbox screen |
| A Blackbox update failed or rolled back | `Blackbox on SERVER-A could not update` / `It keeps recording on 1.4.0-rc.62.` | Blackbox screen |
| A Blackbox stops answering | `Blackbox on SERVER-A has stopped recording` | Blackbox screen |

Not notified: no-change checks, "downloading", update held by the control file, and anything while the person has Windows "Do not disturb" on
(Windows already hides it; the tray dot and the Updates page still show it). At most one notice per event, remembered by `from→to` so a restart
does not repeat it; a rollback notice is shown once.

## Blackbox-only server: options

| Option | What it is | Verdict |
| --- | --- | --- |
| A. Nothing extra | Event Log and `/status` only | Works, but nobody sees a failed update unless they look |
| B. Service shows a pop-up | Not possible: session 0 has no desktop (and showing a window from a service is blocked by Windows) | Rejected |
| C. Optional per-user helper | A very small program (`unravis-blackbox-tray`) started at sign-in by the person who sits at the server: tray icon, version, the same indicator lines, the same notices, reads `127.0.0.1:7788/status`. Off unless asked; installing it is a normal per-user setup, no admin | Recommended where someone sits at the server |
| D. Unravis app watches it | The Unravis app (any PC) lists the server as a Blackbox it knows; it already reads its `/status`; it raises the notices in the table above | Recommended for everything else, and it is nearly built |

Recommendation: **D first** (small, reuses `bbUpdate`), **then C as an optional extra** and **write an Event Log line for every Blackbox update
event** (started, done, rolled back, failed; version numbers only). C would reuse `internal/tray` unchanged, so it adds no second tray stack.

How Blackbox updates itself, so the answer is complete: with the `managed-by-unravis` marker, Unravis updates it and the Blackbox waits; with none, the service
checks the signed feed on its own interval, downloads, verifies, installs through the helper and rolls back when the new program does not answer. Either way
the result is in `/status` (`update.state`, `update.result`), in the Event Log (to add), and, through D, in the notice.

## What must be built (after approval)

| # | Piece | Where | Size |
| --- | --- | --- | --- |
| 1 | `tray.Notify(title, text, onClick)`: the balloon call and its click event (`NIN_BALLOONUSERCLICK`) | `internal/tray` | S |
| 2 | Tray state: tooltip, dot, disabled info lines, the items above, fed by the existing update status and Blackbox status | `cmd/audit-explorer/trayapp.go` | M |
| 3 | One-time notices on start: read `last-update.json`, remember the last version told, send the notice; the same for the "ready" state | `cmd/audit-explorer` + `internal/server/updater.go` | S |
| 4 | Blackbox notices in the app: watch the version and update state of known recorders, notice on change or failure | `internal/server/blackbox.go` | M |
| 5 | Event Log lines for Blackbox update events | `cmd/unravis-blackbox` | S |
| 6 | Optional: `unravis-blackbox-tray` helper | new `cmd/` + `internal/tray` | M |
| 7 | Tauri shell (BL-308) adopts 2 to 4 through the pipe; no second icon | `desktop/` | later |

Tests: the menu rules (limits, no duplicate ids) and each state's menu in `trayapp_test.go`; notice once per `from→to`; a Blackbox `/status` fake for
each update state. Live to check on a PC: the notice really appears and the click opens the right screen; an unattended update shows "updated" after the restart.
Not buildable without a person: the notice appearing under "Do not disturb" and the Windows 10 versus 11 look.

## Open points for nachi

1. Click the notice (recommended) or add real buttons later?
2. Is the optional Blackbox helper (C) wanted now, or only D?
3. The tray dot: only for attention (recommended), or also while updating?
