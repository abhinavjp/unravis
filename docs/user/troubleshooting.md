# Troubleshooting

## The browser did not open

Start Unravis with `--no-browser`, then paste the one-time link from its window into your browser.

## The link says it has expired or was already used

The link works once. A link that Unravis opens in your browser for you expires after 2 minutes. A link printed in the window for you to paste (started with `--no-browser`) expires after 10 minutes. Restart Unravis to get a new one.

## Unravis will not start on my port

Leave out `--port` and Windows picks a free port for you.

## Health checks say they need a real server

In the demo, the health checks are off, because the demo has no SQL Server or MongoDB. Connect a real source to use them.

## Which version do I have?

Run `unravis.exe --version`.

## Free the space says to set a master key

Free and Restore as a new case work only with the master key of this computer. If no master key exists, the drawer says so and nothing is changed. Press "Set a master key", then come back. If the master key is locked, unlock it first.

## The archive folder is refused

Archive refuses a folder inside Unravis's own folders: the cases folder, the folder that holds it, and the folder with the master key file, saved connections and history. Choose a folder somewhere else, such as a documents or backup folder on a local drive.

## "This file can only be opened with the key it was made with"

The case file was sealed with a key that is not the one in use now. On the start screen use Restore from a case file, and type the master key of the computer it was saved on, or the passphrase typed when the file was made.

## Files left behind after an archive or restore

Case files that an archive or a restore finished but did not commit are called left-over files. They appear under "Case files left over" in the health checks of the case. Unravis lists the name, the folder, when it was made and its size. It never deletes, moves or adopts one. Keep it, move it, or delete it yourself in File Explorer.

A temporary folder from a check (`.verify`) that could not be removed is reported as "Cleanup pending" and is removed the next time Unravis starts.

If an archive was stopped by a crash or a power cut, start Unravis again. The case comes back as it was; a half-written `.part` file is not a case file and can be deleted.

## I do not see the Unravis icon

> NOT RUN: the tray steps in this section have not been run on a clean machine (see the clean-machine check in [Install](install.md)).

Windows 11 can put a new tray icon in the hidden-icons area. Click the up arrow (^) at the right end of the taskbar and look there. To keep the icon visible, drag it out of that area onto the taskbar. If the icon is not there at all, Unravis is not running: start it from the Start menu.

## Starting Unravis again does nothing

Unravis runs one copy per Windows account, so a second start does not open a second copy. It asks the running copy to open a new browser tab. If no tab appears, look at the tray icon: Unravis is running there. If the message is "Unravis is already running but could not be reached. Quit it from the tray icon or Task Manager and start it again.", the running copy is not answering. Quit it from the tray icon, or end the `unravis.exe` process in Task Manager, then start Unravis again.

## Unravis says it is running under another account

The message is "Unravis is already running under another Windows account or as administrator. Quit it there, then start Unravis again." A copy under another Windows account, or one started with **Run as administrator**, cannot be reached from your account. Sign in as the account that runs it and quit Unravis from its tray icon, or from Task Manager there, then start it again.

## Unravis could not start

A message box titled "Unravis could not start" appears when Unravis fails to start and there is no usable console to show the error on. The box gives the reason and says where the details are. By default the log folder is `%LOCALAPPDATA%\Unravis\logs`; while Unravis is running, the tray item **Open log folder** opens it. If the box says the port is already in use, see [Unravis will not start on my port](#unravis-will-not-start-on-my-port).

## Turn start at sign-in off

- Click **Start at sign-in** in the tray menu to remove its tick. The item is there only in an installed copy.
- If the tray shows "Could not change start at sign-in", a `Unravis.lnk` in your Startup folder points to another program, and Unravis does not change it. Delete that file yourself, then use the tray item again. The Startup folder is `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup`.

## Quit Unravis from a script

`unravis.exe --quit` asks the running copy of your account to quit, the same as **Quit Unravis** in the tray. It prints nothing when it works. When Unravis is not running it says "Unravis is not running." and exits with code 0. When it cannot ask, for example because Unravis runs under another account or as administrator, it says so and exits with code 1. `--quit` cannot be combined with other options. The **Quit Unravis** entry in the Start menu runs the same command; when Unravis is not running it shows the same text in a box.

## Unravis does not run as administrator

A copy started with **Run as administrator** is refused with "Unravis does not run as administrator. Start it from the Start menu or a normal window." This keeps an administrator copy from locking out a normal start. Start Unravis from the Start menu or from a normal window.

## What shows up in logs and security tools

Opening a tab starts `rundll32.exe url.dll,FileProtocolHandler <link>`, by its full path under Windows. The one-time link is therefore on that command line, where process-creation auditing (event 4688, Sysmon or an EDR product) can record it. The link works once and only for 2 minutes. A browser-open failure message never shows it.
