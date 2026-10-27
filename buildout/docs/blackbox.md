# Unravis Blackbox

The Blackbox is a small program that sits on a server and keeps a flight recorder of it. When an incident happens, Unravis reads the recording and shows what the server was doing at that minute. It only reads the server. It never changes anything on it except its own recordings folder.

## Place

Screens in this part: `p9-blackbox-list`, `p9-wizard-1`, `p9-wizard-2`, `p9-wizard-3`, `p9-wizard-4`.

Open **Blackbox** in the header, on any screen. The list shows every Blackbox Unravis knows, each with a sticker for its state, how it is reached, the last sample and how often it samples. The Blackbox is not part of a case: it records all the time, before any incident.

![The Blackbox list, dark](../screenshots/P9/p9-blackbox-list-dark.png)
![The Blackbox list, light](../screenshots/P9/p9-blackbox-list-light.png)

**Place a Blackbox** starts a four-part wizard. Nothing is saved until the last part.

1. **Name.** Letters, numbers and dashes, up to 63 characters. Keep it the same as the server name so cases find it.

![Place a Blackbox, part 1 of 4, dark](../screenshots/P9/p9-wizard-1-dark.png)
![Place a Blackbox, part 1 of 4, light](../screenshots/P9/p9-wizard-1-light.png)

2. **Where.** On this PC, Unravis installs the recorder as a Windows service (Windows asks for administrator approval). On another server, Unravis builds a zip, the pack, that you copy there. The pack holds the recorder program, `blackbox.json` and a README. It holds no passwords and no data. If a Blackbox is already installed on this PC, the first choice is off.

![Place a Blackbox, part 2 of 4, dark](../screenshots/P9/p9-wizard-2-dark.png)
![Place a Blackbox, part 2 of 4, light](../screenshots/P9/p9-wizard-2-light.png)

3. **Settings.** The sampling interval (5, 15, 30 or 60 seconds), what to record, how long to keep it, the size limit, the free disk to keep, the folder, the port and the address it listens on. The defaults match the table in What it records. The listen address stays `127.0.0.1` unless you change it (decision A53).

![Place a Blackbox, part 3 of 4, dark](../screenshots/P9/p9-wizard-3-dark.png)
![Place a Blackbox, part 3 of 4, light](../screenshots/P9/p9-wizard-3-light.png)

4. **Review.** One screen with every choice. **Install and start here** or **Download the pack** is the only button that changes anything.

![Place a Blackbox, part 4 of 4, dark](../screenshots/P9/p9-wizard-4-dark.png)
![Place a Blackbox, part 4 of 4, light](../screenshots/P9/p9-wizard-4-light.png)

**On this PC.** Install runs the recorder's own `install` through a small helper that Windows asks you to approve. Unravis makes the read key itself and seals it on this PC with Windows data protection the moment it exists. It is never shown, so there is nothing to copy. If the install fails, it is undone and nothing is left half done. The page then offers **Start**, **Stop**, **Apply settings** (restarts the service with new settings), **Snapshot now** (copies the recordings into one folder) and **Remove**. Remove keeps the recordings unless you tick **Delete the recordings too**, which needs the exact Blackbox name typed.

**On another server.** The pack is a zip named `unravis-blackbox-<name>.zip`. Copy it to the server, unzip it, and run `unravis-blackbox.exe install` as an administrator (see Install). The install prints the read key once on that server; copy it then, and use it in Add. For a Blackbox on another server Unravis offers script steps only. It never stops, starts or removes anything there.

## Add

**Add one by folder, share or address** adds a Blackbox that already exists. Type one of:

- a folder on this PC, for example `D:\UnravisBlackbox`;
- a network share, for example `\\web-b\UnravisBlackbox`;
- an address and port, for example `web-b:7788`.

Unravis does not scan the network. It only goes where you point it. Only an address needs a read key. A folder or a share needs none, so the field is not shown. The key is sent once to be tried, then sealed with Windows data protection and kept on this PC. It is never put in a case file or an export, and it is not shown again. A wrong or old key can be replaced with **Type a new read key**.

Add checks the place first and reports one of the states below. Adding the same Blackbox twice, by the same place or by the same server name and recorder name, is refused with a note that it is already in the list. Redirects are never followed. If a share drops while it is being read, the Blackbox shows **Unreachable**. **Check again** reads it afresh.

**Scan this PC** looks for a Blackbox installed on this PC. If it finds one that is not in the list, it offers **Add it to the list**.

## States

Screens in this part: `p9-state-running`, `p9-state-running-late`, `p9-state-stopped`, `p9-state-disk-low`, `p9-state-clock-differs`, `p9-state-not-found`, `p9-state-unreachable`, `p9-state-incomplete`, `p9-state-not-placed`.

Every Blackbox is in one of nine states. The sticker carries the word, and the readiness word is what a case step says: **Ready**, **Partly ready** or **Cannot test yet**. A sticker is never colour alone.

| State | Readiness | What it means and what to do |
| --- | --- | --- |
| Running | Ready | Nothing to do. It is recording. |
| Running late | Partly ready | The newest sample is more than three intervals old. On the server, open Services, find UnravisBlackbox and restart it. |
| Stopped | Cannot test yet | On this PC press Start. On another server start it from Services. |
| Disk low | Partly ready | Free space on that drive, or lower the size limit. Recording resumes by itself. |
| Clock differs | Partly ready | The server clock and this PC differ by more than a second. Fix the clock on the server. Until then, events less than a second apart are not put in order. |
| Not found | Cannot test yet | The folder, share or address does not exist. Check it, then check again. |
| Unreachable | Cannot test yet | Nothing answered, the key was refused, or the share dropped. Check the share permissions or the firewall port, then check again. |
| Incomplete | Partly ready | The recording has gaps. A gap of up to 2 minutes only warns. A longer gap makes the minutes inside it Cannot test yet. |
| Not placed | Cannot test yet | A this-PC entry whose service is gone. Place a Blackbox again, or forget the place. |

![State Running, web-a, dark](../screenshots/P9/p9-state-running-dark.png)
![State Running, web-a, light](../screenshots/P9/p9-state-running-light.png)

![State Running late, web-b, dark](../screenshots/P9/p9-state-running-late-dark.png)
![State Running late, web-b, light](../screenshots/P9/p9-state-running-late-light.png)

![State Stopped, web-old, dark](../screenshots/P9/p9-state-stopped-dark.png)
![State Stopped, web-old, light](../screenshots/P9/p9-state-stopped-light.png)

![State Disk low, web-c, dark](../screenshots/P9/p9-state-disk-low-dark.png)
![State Disk low, web-c, light](../screenshots/P9/p9-state-disk-low-light.png)

![State Clock differs, web-d, dark](../screenshots/P9/p9-state-clock-differs-dark.png)
![State Clock differs, web-d, light](../screenshots/P9/p9-state-clock-differs-light.png)

![State Not found, old-box, dark](../screenshots/P9/p9-state-not-found-dark.png)
![State Not found, old-box, light](../screenshots/P9/p9-state-not-found-light.png)

![State Unreachable, web-e, dark](../screenshots/P9/p9-state-unreachable-dark.png)
![State Unreachable, web-e, light](../screenshots/P9/p9-state-unreachable-light.png)

![State Incomplete, web-f, dark](../screenshots/P9/p9-state-incomplete-dark.png)
![State Incomplete, web-f, light](../screenshots/P9/p9-state-incomplete-light.png)

![State Not placed, web-g, dark](../screenshots/P9/p9-state-not-placed-dark.png)
![State Not placed, web-g, light](../screenshots/P9/p9-state-not-placed-light.png)

**Clock differs** is only checked for an address, where the server reports its own clock. **Disk low** for a folder or share comes from the recorder's own pause and resume events in the manifest. A recording in a newer format than Unravis knows, a refused key and a throttled address all show as **Unreachable**, with the reason in the detail line.

## Use in a case

Screens in this part: `p9-step-9`, `p9-gap-warning`.

Step 9, Host and network, asks one question: was the web server or the SQL server short of CPU, memory or disk? A case that names a Blackbox as a source reads it once, for the incident window and the reference window. The case keeps only the Blackbox name. It never keeps the address, the folder or the read key.

![Step 9 read from web-a, dark](../screenshots/P9/p9-step-9-dark.png)
![Step 9 read from web-a, light](../screenshots/P9/p9-step-9-light.png)

The panel can also run the same read by hand: open a Blackbox, open **Read for a case window**, type the two windows (UTC) and press **Read the host for these windows**. This is how the pictures here were made. In this release the host data gives CPU, memory, disk and network. Checks that need the database or the process list stay **Cannot test yet**, so Step 9 is at most **Partly ready**.

### Gaps

A recording can have holes: the PC was busy, the service was restarted, the share dropped. Missing minutes are never counted as zero.

- A gap of **2 minutes or less** only warns. The check runs on the minutes that exist.
- A gap of **more than 2 minutes** is not tested. Those minutes say **Cannot test yet**, and the warning lists each one with its start, end and which window it falls in.

![A gap longer than 2 minutes in the reference window of web-b, dark](../screenshots/P9/p9-gap-warning-dark.png)
![A gap longer than 2 minutes in the reference window of web-b, light](../screenshots/P9/p9-gap-warning-light.png)

If the Blackbox is stopped or removed while a case reads it, the read that is under way finishes on what the recorder already wrote. The next read says **Cannot test yet** for the minutes that are missing. Reading never changes the Blackbox.

Screenshots in this page are made on a developer PC. The CI runner is the reference renderer, so they are replaced on the next CI run.

## About the recorder

The sections below describe the recorder program itself.

## What it records

Every 15 seconds (`intervalSeconds`) it writes one line with:

- CPU use of the whole machine, available memory, committed memory, disk queue length and network bytes in and out.
- Free space on the recordings drive, as a percent.
- The busiest processes: name, process id, start time, CPU seconds, CPU percent, working set, private bytes, handles and threads.
- A catalogue of the `*.log` files found in `logPaths` (path, size, last write time). It does not open, copy or read the content of those files.

It does not record request bodies, passwords, environment variables or command lines.

Recordings are plain text, one JSON line per sample, one file per hour: `bb-YYYYMMDD-HH.ndjson`. A `manifest.json` next to them lists the files and is rewritten atomically. If two samples are more than three intervals apart, the manifest records a gap, with its cause when the recorder knows it (for example `disk-low`), so the case screens show "no data" instead of a flat line.

Settings are in `blackbox.json`. Before install it sits next to the program in the unpacked zip; install keeps the live copy in `%ProgramFiles%\Unravis Blackbox`, next to the installed program (see Install):

| Setting | Default | Meaning |
| --- | --- | --- |
| `name` | computer name | The name shown in Unravis and in the service description |
| `folder` | `D:\UnravisBlackbox` (`C:\UnravisBlackbox` with no D: drive) | Where recordings go |
| `intervalSeconds` | 15 | Seconds between samples |
| `keepDays` | 7 | Delete recordings older than this |
| `maxGB` | 2 | Delete the oldest recordings above this size |
| `minFreePercent` | 10 | Delete the oldest recordings when the drive has less free space |
| `port` | 7788 | Status page port |
| `bind` | `127.0.0.1` | Address the status page listens on. Loopback means only this computer can reach it (decision A53) |
| `record.processes` | true | Record the busiest processes |
| `record.logsCatalogue` | true | Record the log file catalogue |
| `record.copyLogsOnSnapshot` | false | Include log files in a snapshot |
| `logPaths` | IIS log folder | Folders searched for log files |
| `probe.enabled` | false | Reserved. The SQL probe is not built yet; leave it off |

## Install

Copy `unravis-blackbox.exe`, `blackbox.json` and `README.txt` to the server. Open PowerShell as administrator in that folder and run:

```
.\unravis-blackbox.exe install
```

Add `--name web-b` to choose a name, `--folder E:\Rec` to choose the folder, `--port 7790` for another port, `--bind 10.0.0.5` (or `0.0.0.0`) to let other computers reach the status page; see Status page below. Add `--json` for one line of machine-readable output. A success has `"ok":true` and the fields `readKey`, `service`, `folder`, `port`, `name`, `exePath` and `configPath`; a failure is `{"ok":false,"reason":"port-busy","message":"..."}` with one of the reasons listed below.

It does these things, in this order:

1. Checks the install can work: administrator rights, no Blackbox installed yet, port free, folder writable with at least 1 GB free, and that antivirus has not quarantined the program.
2. Makes the recordings folder and the program folder `%ProgramFiles%\Unravis Blackbox`, and restricts both so only administrators and the system can change them (ordinary users can still read the recordings folder). The service runs as the system account, so it never runs the program from the unpacked zip, where other users might be able to replace it: install copies `unravis-blackbox.exe` into the program folder, and keeps `blackbox.json` and the fingerprint of the read key there too.
3. Creates the `UnravisBlackbox` Windows service from that copy (automatic start, restarts itself after a crash).
4. Starts the service and opens its status page once to prove it answers.

The read key is printed once:

```
Installed. The UnravisBlackbox service is running.
Folder: D:\UnravisBlackbox
Program and settings: C:\Program Files\Unravis Blackbox
Status port: 7788

Read key (shown once, copy it now):
  ubk-...

Only a fingerprint of the key is kept on this server.
```

Copy the key now. Unravis asks for it when you add this Blackbox to a case. If you lose it, remove and install again to get a new one.

If something fails, the install says why and undoes everything it started, so a second try starts clean. The reasons, as the install reports them:

| Reason | Meaning | What to do |
| --- | --- | --- |
| `no-admin` | Not run as administrator | Open PowerShell as administrator |
| `service-exists` | A Blackbox is already installed | Use `apply` to change settings, or `remove` first |
| `port-busy` | Another program uses the port | Choose another with `--port` |
| `folder` | The folder is read-only or has under 1 GB free | Choose another with `--folder` |
| `antivirus` | The program file was removed or changed during install | Allow `unravis-blackbox.exe` in the antivirus, then retry |

Other commands:

| Command | What it does |
| --- | --- |
| `start` / `stop` | Start or stop recording. Recordings are kept |
| `apply` | Check `blackbox.json`, then restart with the new settings. A broken file is refused and nothing is stopped |
| `snapshot` | Copy the recordings (and, if set, the log files) to `<folder>\snapshots\<name>-YYYY-MM-DD-HHMM` |
| `run` | Record in this window instead of as a service. Ctrl+C stops it |
| `version` | Print the version |

## Remove

```
.\unravis-blackbox.exe remove
```

stops and removes the service, deletes the stored fingerprint of the read key and the installed copy of the program (when it is not the one running the command; if you run `remove` from the program folder, `unravis-blackbox.exe` stays and can be deleted by hand). `blackbox.json` and the recordings stay where they are.

After an install, run `apply`, `remove` and the other commands from the program folder, or from the unpacked zip: they use the installed `blackbox.json` when there is one.

To delete the recordings too, type the Blackbox name so a slip cannot do it:

```
.\unravis-blackbox.exe remove --delete-recordings --confirm-name web-b
```

This deletes only the files the Blackbox made (recordings, `manifest.json`, leftovers and the `snapshots` folder). It removes the folder itself only if nothing else is in it.

## Housekeeping

The Blackbox cleans up after itself and this cannot be turned off, so it cannot fill the drive. At start and then about once a minute it deletes the oldest recordings when any of these is true:

- A recording is older than `keepDays`.
- All recordings together are over `maxGB`.
- The drive has less than `minFreePercent` free.

The file being written is never deleted. If Windows will not let a file go (a reader has it open), the Blackbox skips it, carries on with the next file and the other limits, notes it in the manifest, and tries again at the next pass; a recording that is paused for a full drive stays paused until the drive really has room. Age deletes at most 6 files per pass, and skips the pass right after the clock jumps forward, so a wrong clock cannot erase the history in one go. Snapshots are not touched by housekeeping: delete them yourself when you have what you need.

If the drive is still low after everything older has gone, recording pauses, the status page shows state `disk-low`, and recording resumes by itself once there is room. The pause shows up as a gap with cause `disk-low`.

## Status page

The recorder runs a small read-only web server on `port`. It answers only GET and HEAD. Every answer needs the read key in the `X-Blackbox-Key` header. It is never accepted in the address. An address that sends too many wrong keys in a minute is turned away for a while, and at most 8 requests are served at the same time.

| Path | Answers |
| --- | --- |
| `/status` | State, counts and free space (below) |
| `/manifest` | The manifest of recordings |
| `/files` | The list of recording files |
| `/files/<name>` | One recording file |

States: `running`, `running-late` (the newest sample is more than three intervals old, for example under heavy load), `disk-low` and `stopped`.

Every answer carries the Blackbox clock in `time`, so Unravis can show a clock difference between the server and the PC reading it.

The page listens on this computer only (`127.0.0.1`) until you opt in. To let Unravis read it from another PC, do both: set `bind` to the server address (or `0.0.0.0`) in `blackbox.json` and run `apply` (or install with `--bind`), and add a Windows Firewall rule for the port that allows only the addresses you type. Without both steps nothing on the network can reach it.

Captured `/status` answer from a foreground run of `unravis-blackbox run`, 30 seconds at the default 15 second interval (`p8-status-json`; the computer name and version are replaced):

```json
{"format":1,"time":"2026-10-27T20:38:08.6343162Z","name":"web-b","version":"0.1.0","host":"SERVER01","state":"running","run":"16f9a0caf978","intervalSeconds":15,"started":"2026-10-27T20:37:30.7575331Z","firstSample":"2026-10-27T20:37:30.757Z","lastSample":"2026-10-27T20:38:00.757Z","samples":3,"files":1,"bytes":9890,"gaps":0,"freePercent":35.62075395669758,"minFreePercent":10,"keepDays":7,"maxGB":2,"manifestOK":true,"writeOK":true,"probe":{"enabled":false}}
```

A wrong or missing key gets HTTP 401:

```json
{"error":"the read key is missing or wrong","format":1,"time":"2026-10-27T20:38:08.6715332Z"}
```
