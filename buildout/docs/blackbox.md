# Unravis Blackbox

The Blackbox is a small program that sits on a server and keeps a flight recorder of it. When an incident happens, Unravis reads the recording and shows what the server was doing at that minute. It only reads the server. It never changes anything on it except its own recordings folder.

Screenshots pending harness: this page has one captured terminal block (`p8-status-json`) and no browser screens. `tools/screens/docs-check.mjs` does not exist yet (P1 creates it), so this page has not been run through it.

## What it records

Every 15 seconds (`intervalSeconds`) it writes one line with:

- CPU use of the whole machine, available memory, committed memory, disk queue length and network bytes in and out.
- Free space on the recordings drive, as a percent.
- The busiest processes: name, process id, start time, CPU seconds, CPU percent, working set, private bytes, handles and threads.
- A catalogue of the `*.log` files found in `logPaths` (path, size, last write time). It does not open, copy or read the content of those files.

It does not record request bodies, passwords, environment variables or command lines.

Recordings are plain text, one JSON line per sample, one file per hour: `bb-YYYYMMDD-HH.ndjson`. A `manifest.json` next to them lists the files and is rewritten atomically. If two samples are more than three intervals apart, the manifest records a gap, with its cause when the recorder knows it (for example `disk-low`), so the case screens show "no data" instead of a flat line.

Settings are in `blackbox.json` next to the program:

| Setting | Default | Meaning |
| --- | --- | --- |
| `name` | computer name | The name shown in Unravis and in the service description |
| `folder` | `D:\UnravisBlackbox` (`C:\UnravisBlackbox` with no D: drive) | Where recordings go |
| `intervalSeconds` | 15 | Seconds between samples |
| `keepDays` | 7 | Delete recordings older than this |
| `maxGB` | 2 | Delete the oldest recordings above this size |
| `minFreePercent` | 10 | Delete the oldest recordings when the drive has less free space |
| `port` | 7788 | Status page port |
| `bind` | `0.0.0.0` | Address the status page listens on |
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

Add `--name web-b` to choose a name, `--folder E:\Rec` to choose the folder, `--port 7790` for another port, `--bind 127.0.0.1` to listen on this computer only. Add `--json` for one line of machine-readable output (`{"ok":true,"reason":"","message":"..."}`).

It does these things, in this order:

1. Checks the install can work: administrator rights, no Blackbox installed yet, port free, folder writable with at least 1 GB free, and that antivirus has not quarantined the program.
2. Makes the folder, stores the fingerprint of the read key outside it, and creates the `UnravisBlackbox` Windows service (automatic start, restarts itself after a crash).
3. Starts the service and opens its status page once to prove it answers.

The read key is printed once:

```
Installed. The UnravisBlackbox service is running.
Folder: D:\UnravisBlackbox
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

stops and removes the service and deletes the stored fingerprint of the read key. `blackbox.json` and the recordings stay where they are.

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

The file being written is never deleted. If Windows will not let a file go (a reader has it open), the Blackbox notes it in the manifest and tries again at the next pass. Snapshots are not touched by housekeeping: delete them yourself when you have what you need.

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

Without a firewall rule only this computer can reach the page. To let Unravis read it from another PC, allow the port in Windows Firewall.

Captured `/status` answer from a foreground run (`p8-status-json`):

```json
{"format":1,"time":"2026-10-27T20:03:45.5386874Z","name":"web-b","version":"0.1.0","host":"DESKTOP-QQDVSD1","state":"running","run":"7d6facbb0d7a","intervalSeconds":15,"started":"2026-10-27T20:03:41.117834Z","firstSample":"2026-10-27T20:03:41.117Z","lastSample":"2026-10-27T20:03:44.436Z","samples":5,"files":1,"bytes":11743,"gaps":0,"freePercent":80,"minFreePercent":10,"keepDays":7,"maxGB":2,"manifestOK":true,"writeOK":true,"probe":{"enabled":false}}
```

A wrong or missing key gets HTTP 401:

```json
{"error":"the read key is missing or wrong","format":1,"time":"2026-10-27T20:03:45.539224Z"}
```
