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
