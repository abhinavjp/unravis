import{$ as e,B as t,C as n,Ct as r,E as i,F as a,G as o,H as s,J as c,K as l,L as u,N as d,Q as f,T as p,Tt as m,U as h,V as g,Z as _,_t as v,at as y,b,bt as x,dt as S,ft as C,h as w,ht as T,lt as E,m as D,mt as O,nt as k,ot as A,q as j,tt as M,ut as N,w as ee,wt as P,yt as te}from"./time-D68YfwAD.js";import{i as F}from"./api-T9RfsXU-.js";import{Bo as ne,Gr as I,Hr as L,Kr as re,Lo as R,Ro as z,Wr as B,Xr as ie,qr as V,v as ae,zo as oe}from"./index-CG8obpT0.js";function se(e){let t=(e?.version??``).trim();if(!t)return``;let n=/^\d/.test(t)?`v`+t:t,r=[],i=(e?.commit??``).trim().slice(0,7);i&&r.push(`build `+i);let a=(e?.date??``).trim();return a&&r.push(a),`Unravis `+n+(r.length?` (`+r.join(`, `)+`)`:``)}var H=`# Unravis Blackbox\r
\r
The Blackbox is a small program that sits on a server and keeps a flight recorder of it. When an incident happens, Unravis reads the recording and shows what the server was doing at that minute. It only reads the server. It never changes anything on it except its own recordings folder.\r
\r
## Place\r
\r
Open **Blackbox** in the header, on any screen. The list shows every Blackbox Unravis knows, each with a sticker for its state, how it is reached, the last sample and how often it samples. The Blackbox is not part of a case: it records all the time, before any incident.\r
\r
![The Blackbox list, dark](screenshots/P9/p9-blackbox-list-dark.png)\r
![The Blackbox list, light](screenshots/P9/p9-blackbox-list-light.png)\r
\r
**Place a Blackbox** starts a four-part wizard. Nothing is saved until the last part.\r
\r
1. **Name.** Letters, numbers and dashes, up to 63 characters. Keep it the same as the server name so cases find it.\r
\r
![Place a Blackbox, part 1 of 4, dark](screenshots/P9/p9-wizard-1-dark.png)\r
![Place a Blackbox, part 1 of 4, light](screenshots/P9/p9-wizard-1-light.png)\r
\r
2. **Where.** On this PC, Unravis installs the recorder as a Windows service (Windows asks for administrator approval). On another server, Unravis builds a zip, the pack, that you copy there. The pack holds the recorder program, \`blackbox.json\` and a README. It holds no passwords and no data. If a Blackbox is already installed on this PC, the first choice is off.\r
\r
![Place a Blackbox, part 2 of 4, dark](screenshots/P9/p9-wizard-2-dark.png)\r
![Place a Blackbox, part 2 of 4, light](screenshots/P9/p9-wizard-2-light.png)\r
\r
3. **Settings.** The sampling interval (5, 15, 30 or 60 seconds), what to record, how long to keep it, the size limit, the free disk to keep, the folder, the port and the address it listens on. The defaults match the table in What it records. The listen address stays \`127.0.0.1\` unless you change it (decision A53).\r
\r
![Place a Blackbox, part 3 of 4, dark](screenshots/P9/p9-wizard-3-dark.png)\r
![Place a Blackbox, part 3 of 4, light](screenshots/P9/p9-wizard-3-light.png)\r
\r
4. **Review.** One screen with every choice. **Install and start here** or **Download the pack** is the only button that changes anything.\r
\r
![Place a Blackbox, part 4 of 4, dark](screenshots/P9/p9-wizard-4-dark.png)\r
![Place a Blackbox, part 4 of 4, light](screenshots/P9/p9-wizard-4-light.png)\r
\r
**On this PC.** Install runs the recorder's own \`install\` through a small helper that Windows asks you to approve. Unravis makes the read key itself and seals it on this PC with Windows data protection the moment it exists. It is shown again only when you press **Show** or **Copy** on that Blackbox's status screen, and only for the Blackbox on this PC: the row stays masked until then, is hidden again after 30 seconds, and the key is never written to a log, a case file or a stored answer. A key you typed for another server is never shown back. If the install fails, it is undone and nothing is left half done. The page then offers **Start**, **Stop**, **Apply settings** (restarts the service with new settings), **Snapshot now** (copies the recordings into one folder) and **Remove**. Remove keeps the recordings unless you tick **Delete the recordings too**, which needs the exact Blackbox name typed.\r
\r
**On another server.** The pack is a zip named \`unravis-blackbox-<name>.zip\`. Copy it to the server, unzip it, and run \`unravis-blackbox.exe install\` as an administrator (see Install). The name comes from the pack's \`blackbox.json\`, so you do not need to type it. The install prints the read key once on that server; copy it then, and use it in Add. For a Blackbox on another server Unravis offers script steps only. It never stops, starts or removes anything there.\r
\r
## Add\r
\r
**Add one by folder, share or address** adds a Blackbox that already exists. Type one of:\r
\r
- a folder on this PC, for example \`D:\\UnravisBlackbox\`;\r
- a network share, for example \`\\\\web-b\\UnravisBlackbox\`;\r
- an address and port, for example \`web-b:7788\`.\r
\r
Unravis does not scan the network. It only goes where you point it. Only an address needs a read key. A folder or a share needs none, so the field is not shown. The key is sent once to be tried, then sealed with Windows data protection and kept on this PC. It is never put in a case file or an export, and it is not shown again. A wrong or old key can be replaced with **Type a new read key**.\r
\r
Add checks the place first and reports one of the states below. Adding the same Blackbox twice, by the same place or by the same server name and recorder name, is refused with a note that it is already in the list. Redirects are never followed. If a share drops while it is being read, the Blackbox shows **Unreachable**. **Check again** reads it afresh.\r
\r
**Scan this PC** looks for a Blackbox installed on this PC. If it finds one that is not in the list, it offers **Add it to the list**.\r
\r
## States\r
\r
Every Blackbox is in one of nine states. The sticker carries the word, and the readiness word is what a case step says: **Ready**, **Partly ready** or **Cannot test yet**. A sticker is never colour alone.\r
\r
| State | Readiness | What it means and what to do |\r
| --- | --- | --- |\r
| Running | Ready | Nothing to do. It is recording. |\r
| Running late | Partly ready | No sample for more than three intervals. On the server, open Services, find UnravisBlackbox and restart it. |\r
| Stopped | Partly ready | On this PC, press Start. On another server, start it from Services. |\r
| Disk low | Partly ready | Free space on that drive, or lower the size limit. Recording resumes by itself. |\r
| Clock differs | Partly ready | Fix the clock on that server. Until then, events less than a second apart are not put in order. |\r
| Not found | Cannot test yet | Check the folder, share or address, then check again. |\r
| Unreachable | Cannot test yet | Check the share permissions or the firewall port, then check again. |\r
| Incomplete | Partly ready | The recording has gaps. A gap of up to 2 minutes only warns. A longer gap makes the minutes inside it Cannot test yet. |\r
| Not placed | Cannot test yet | Place a Blackbox before you need it. Until then host steps say Cannot test yet. |\r
\r
Stopped is Partly ready because the recordings are kept and can still be read. Clock differs means the server clock and this PC differ by more than a second. Not found means the folder, share or address does not exist. Unreachable means nothing answered, the key was refused, or the share dropped. Not placed is a this-PC entry whose service is gone; place a Blackbox again, or forget the place.\r
\r
![State Running, web-a, dark](screenshots/P9/p9-state-running-dark.png)\r
![State Running, web-a, light](screenshots/P9/p9-state-running-light.png)\r
\r
The Blackbox on this PC also shows its read key, masked. **Show** reveals it for 30 seconds and **Hide** puts it away at once. **Copy** puts it on the clipboard without showing it. This is for giving the key to another PC's Unravis. The demo key in the picture is made up.\r
\r
![The read key of web-a shown, dark](screenshots/P9/p9-read-key-dark.png)\r
![The read key of web-a shown, light](screenshots/P9/p9-read-key-light.png)\r
\r
![State Running late, web-b, dark](screenshots/P9/p9-state-running-late-dark.png)\r
![State Running late, web-b, light](screenshots/P9/p9-state-running-late-light.png)\r
\r
![State Stopped, web-old, dark](screenshots/P9/p9-state-stopped-dark.png)\r
![State Stopped, web-old, light](screenshots/P9/p9-state-stopped-light.png)\r
\r
![State Disk low, web-c, dark](screenshots/P9/p9-state-disk-low-dark.png)\r
![State Disk low, web-c, light](screenshots/P9/p9-state-disk-low-light.png)\r
\r
![State Clock differs, web-d, dark](screenshots/P9/p9-state-clock-differs-dark.png)\r
![State Clock differs, web-d, light](screenshots/P9/p9-state-clock-differs-light.png)\r
\r
![State Not found, old-box, dark](screenshots/P9/p9-state-not-found-dark.png)\r
![State Not found, old-box, light](screenshots/P9/p9-state-not-found-light.png)\r
\r
![State Unreachable, web-e, dark](screenshots/P9/p9-state-unreachable-dark.png)\r
![State Unreachable, web-e, light](screenshots/P9/p9-state-unreachable-light.png)\r
\r
![State Incomplete, web-f, dark](screenshots/P9/p9-state-incomplete-dark.png)\r
![State Incomplete, web-f, light](screenshots/P9/p9-state-incomplete-light.png)\r
\r
![State Not placed, web-g, dark](screenshots/P9/p9-state-not-placed-dark.png)\r
![State Not placed, web-g, light](screenshots/P9/p9-state-not-placed-light.png)\r
\r
**Clock differs** is only checked for an address, where the server reports its own clock. **Disk low** for a folder or share comes from the recorder's own pause and resume events in the manifest. A recording in a newer format than Unravis knows, a refused key and a throttled address all show as **Unreachable**, with the reason in the detail line.\r
\r
## Use in a case\r
\r
Step 9, Host and network, asks one question: was the web server or the SQL server short of CPU, memory or disk? A case that names a Blackbox as a source reads it once, for the incident window and the reference window. The case keeps only the Blackbox name. It never keeps the address, the folder or the read key.\r
\r
To use one, open the case, open **Blackbox** in the header, open the Blackbox and press **Use in this case**. A case reads one Blackbox, so press **Stop using it in this case** before choosing another. Stopping only takes the name out of the case. The Blackbox and its recordings are as they were.\r
\r
![Step 9 read from web-a, dark](screenshots/P9/p9-step-9-dark.png)\r
![Step 9 read from web-a, light](screenshots/P9/p9-step-9-light.png)\r
\r
The panel can also run the same read by hand: open a Blackbox, open **Read for a case window**, type the two windows (UTC) and press **Read the host for these windows**. This is how the pictures here were made. In this release the host data gives CPU, memory, disk and network. Checks that need the database or the process list stay **Cannot test yet**, so Step 9 is at most **Partly ready**.\r
\r
### Gaps\r
\r
A recording can have holes: the PC was busy, the service was restarted, the share dropped. Missing minutes are never counted as zero.\r
\r
- A gap of **2 minutes or less** only warns. The check runs on the minutes that exist.\r
- A gap of **more than 2 minutes** is not tested. Those minutes say **Cannot test yet**, and the warning lists each one with its start, end and which window it falls in.\r
\r
Where the long gap falls matters. A gap of more than 2 minutes in the incident window hides minutes of the very window being judged, so the host step cannot be read as flat and says Cannot test yet. The same gap in the reference window only means the usual level is known from fewer readings, so the answer is weaker and reads Partly ready rather than Cannot test yet.\r
\r
![A gap longer than 2 minutes in the reference window of web-b, dark](screenshots/P9/p9-gap-warning-dark.png)\r
![A gap longer than 2 minutes in the reference window of web-b, light](screenshots/P9/p9-gap-warning-light.png)\r
\r
If the Blackbox is removed from this PC while a case uses it, the entry stays and shows Not placed. The case says the Blackbox could not be read, and Step 9 is not Ready. If the entry is also forgotten, the case says the Blackbox is no longer in the list. The case itself is not changed. Reading never changes the Blackbox.\r
\r
Screenshots in this page are made on a developer PC. The CI runner is the reference renderer, so they are replaced on the next CI run.\r
\r
## About the recorder\r
\r
The sections below describe the recorder program itself.\r
\r
## What it records\r
\r
Every 15 seconds (\`intervalSeconds\`) it writes one line with:\r
\r
- CPU use of the whole machine, available memory, committed memory, disk queue length and network bytes in and out.\r
- Free space on the recordings drive, as a percent.\r
- The busiest processes: name, process id, start time, CPU seconds, CPU percent, working set, private bytes, handles and threads.\r
- A catalogue of the \`*.log\` files found in \`logPaths\` (path, size, last write time). It does not open, copy or read the content of those files.\r
\r
It does not record request bodies, passwords, environment variables or command lines.\r
\r
Recordings are plain text, one JSON line per sample, one file per hour: \`bb-YYYYMMDD-HH.ndjson\`. A \`manifest.json\` next to them lists the files and is rewritten atomically. If two samples are more than three intervals apart, the manifest records a gap, with its cause when the recorder knows it (for example \`disk-low\`), so the case screens show "no data" instead of a flat line.\r
\r
Settings are in \`blackbox.json\`. Before install it sits next to the program in the unpacked zip; install keeps the live copy in \`%ProgramFiles%\\Unravis Blackbox\`, next to the installed program (see Install):\r
\r
| Setting | Default | Meaning |\r
| --- | --- | --- |\r
| \`name\` | computer name | The name shown in Unravis and in the service description |\r
| \`folder\` | \`D:\\UnravisBlackbox\` (\`C:\\UnravisBlackbox\` with no D: drive) | Where recordings go |\r
| \`intervalSeconds\` | 15 | Seconds between samples |\r
| \`keepDays\` | 7 | Delete recordings older than this |\r
| \`maxGB\` | 2 | Delete the oldest recordings above this size |\r
| \`minFreePercent\` | 10 | Delete the oldest recordings when the drive has less free space |\r
| \`port\` | 7788 | Status page port |\r
| \`bind\` | \`127.0.0.1\` | Address the status page listens on. Loopback means only this computer can reach it (decision A53) |\r
| \`record.processes\` | true | Record the busiest processes |\r
| \`record.logsCatalogue\` | true | Record the log file catalogue |\r
| \`record.copyLogsOnSnapshot\` | false | Include log files in a snapshot |\r
| \`logPaths\` | IIS log folder | Folders searched for log files |\r
\r
## Install\r
\r
Copy \`unravis-blackbox.exe\`, \`blackbox.json\` and \`README.txt\` to the server. Open PowerShell as administrator in that folder and run:\r
\r
\`\`\`\r
.\\unravis-blackbox.exe install\r
\`\`\`\r
\r
Without \`--name\`, the name is the one in the pack's \`blackbox.json\`, or the computer name when there is no pack. Add \`--name web-b\` to choose a name, \`--folder E:\\Rec\` to choose the folder, \`--port 7790\` for another port, \`--bind 10.0.0.5\` (or \`0.0.0.0\`) to let other computers reach the status page; see Status page below. Add \`--json\` for one line of machine-readable output. A success has \`"ok":true\` and the fields \`readKey\`, \`service\`, \`folder\`, \`port\`, \`name\`, \`exePath\` and \`configPath\`; a failure is \`{"ok":false,"reason":"port-busy","message":"..."}\` with one of the reasons listed below.\r
\r
It does these things, in this order:\r
\r
1. Checks the install can work: administrator rights, no Blackbox installed yet, port free, folder writable with at least 1 GB free, and that antivirus has not quarantined the program.\r
2. Makes the recordings folder and the program folder \`%ProgramFiles%\\Unravis Blackbox\`, and restricts both so only administrators and the system can change them (ordinary users can still read the recordings folder). The service runs as the system account, so it never runs the program from the unpacked zip, where other users might be able to replace it: install copies \`unravis-blackbox.exe\` into the program folder, and keeps \`blackbox.json\` and the fingerprint of the read key there too.\r
3. Creates the \`UnravisBlackbox\` Windows service from that copy (automatic start, restarts itself after a crash).\r
4. Starts the service and opens its status page once to check that it answers.\r
\r
The read key is printed once:\r
\r
\`\`\`\r
Installed. The UnravisBlackbox service is running.\r
Folder: D:\\UnravisBlackbox\r
Program and settings: C:\\Program Files\\Unravis Blackbox\r
Status port: 7788\r
\r
Read key (shown once, copy it now):\r
  ubk-...\r
\r
Only a fingerprint of the key is kept on this server.\r
\`\`\`\r
\r
Copy the key now. Unravis asks for it when you add this Blackbox to a case. If you lose it, remove and install again to get a new one.\r
\r
If something fails, the install says why and undoes everything it started, so a second try starts clean. The reasons, as the install reports them:\r
\r
| Reason | Meaning | What to do |\r
| --- | --- | --- |\r
| \`no-admin\` | Not run as administrator | Open PowerShell as administrator |\r
| \`service-exists\` | A Blackbox is already installed | Use \`apply\` to change settings, or \`remove\` first |\r
| \`port-busy\` | Another program uses the port | Choose another with \`--port\` |\r
| \`folder\` | The folder is read-only or has under 1 GB free | Choose another with \`--folder\` |\r
| \`antivirus\` | The program file was removed or changed during install | Allow \`unravis-blackbox.exe\` in the antivirus, then retry |\r
\r
Other commands:\r
\r
| Command | What it does |\r
| --- | --- |\r
| \`start\` / \`stop\` | Start or stop recording. Recordings are kept |\r
| \`apply\` | Check \`blackbox.json\`, then restart with the new settings. A broken file is refused and nothing is stopped |\r
| \`snapshot\` | Copy the recordings (and, if set, the log files) to \`<folder>\\snapshots\\<name>-YYYY-MM-DD-HHMM\` |\r
| \`run\` | Record in this window instead of as a service. Ctrl+C stops it |\r
| \`version\` | Print the version |\r
\r
## Remove\r
\r
\`\`\`\r
.\\unravis-blackbox.exe remove\r
\`\`\`\r
\r
stops and removes the service, deletes the stored fingerprint of the read key and the installed copy of the program (when it is not the one running the command; if you run \`remove\` from the program folder, \`unravis-blackbox.exe\` stays and can be deleted by hand). \`blackbox.json\` and the recordings stay where they are.\r
\r
After an install, run \`apply\`, \`remove\` and the other commands from the program folder, or from the unpacked zip: they use the installed \`blackbox.json\` when there is one.\r
\r
To delete the recordings too, type the Blackbox name so a slip cannot do it:\r
\r
\`\`\`\r
.\\unravis-blackbox.exe remove --delete-recordings --confirm-name web-b\r
\`\`\`\r
\r
This deletes only the files the Blackbox made (recordings, \`manifest.json\`, leftovers and the \`snapshots\` folder). It removes the folder itself only if nothing else is in it.\r
\r
## Housekeeping\r
\r
The Blackbox cleans up after itself and this cannot be turned off, so it cannot fill the drive. At start and then about once a minute it deletes the oldest recordings when any of these is true:\r
\r
- A recording is older than \`keepDays\`.\r
- All recordings together are over \`maxGB\`.\r
- The drive has less than \`minFreePercent\` free.\r
\r
The file being written is never deleted. If Windows will not let a file go (a reader has it open), the Blackbox skips it, carries on with the next file and the other limits, notes it in the manifest, and tries again at the next pass; a recording that is paused for a full drive stays paused until the drive really has room. Age deletes at most 6 files per pass, and skips the pass right after the clock jumps forward, so a wrong clock cannot erase the history in one go. Snapshots are not touched by housekeeping: delete them yourself when you have what you need.\r
\r
If the drive is still low after everything older has gone, recording pauses, the status page shows state \`disk-low\`, and recording resumes by itself once there is room. The pause shows up as a gap with cause \`disk-low\`.\r
\r
## Status page\r
\r
The recorder runs a small read-only web server on \`port\`. It answers only GET and HEAD. Every answer needs the read key in the \`X-Blackbox-Key\` header. It is never accepted in the address. An address that sends too many wrong keys in a minute is turned away for a while, and at most 8 requests are served at the same time.\r
\r
| Path | Answers |\r
| --- | --- |\r
| \`/status\` | State, counts and free space (below) |\r
| \`/manifest\` | The manifest of recordings |\r
| \`/files\` | The list of recording files |\r
| \`/files/<name>\` | One recording file |\r
\r
States: \`running\`, \`running-late\` (the newest sample is more than three intervals old, for example under heavy load), \`disk-low\` and \`stopped\`.\r
\r
Every answer carries the Blackbox clock in \`time\`, so Unravis can show a clock difference between the server and the PC reading it.\r
\r
The page listens on this computer only (\`127.0.0.1\`) until you opt in. To let Unravis read it from another PC, do both: set \`bind\` to the server address (or \`0.0.0.0\`) in \`blackbox.json\` and run \`apply\` (or install with \`--bind\`), and add a Windows Firewall rule for the port that allows only the addresses you type. Without both steps nothing on the network can reach it.\r
\r
Captured \`/status\` answer from a foreground run of \`unravis-blackbox run\`, 30 seconds at the default 15 second interval (\`p8-status-json\`; the computer name and version are replaced):\r
\r
\`\`\`json\r
{"format":1,"time":"2026-10-27T20:38:08.6343162Z","name":"web-b","version":"0.1.0","host":"SERVER01","state":"running","run":"16f9a0caf978","intervalSeconds":15,"started":"2026-10-27T20:37:30.7575331Z","firstSample":"2026-10-27T20:37:30.757Z","lastSample":"2026-10-27T20:38:00.757Z","samples":3,"files":1,"bytes":9890,"gaps":0,"freePercent":35.62075395669758,"minFreePercent":10,"keepDays":7,"maxGB":2,"manifestOK":true,"writeOK":true,"probe":{"enabled":false}}\r
\`\`\`\r
\r
A wrong or missing key gets HTTP 401:\r
\r
\`\`\`json\r
{"error":"the read key is missing or wrong","format":1,"time":"2026-10-27T20:38:08.6715332Z"}\r
\`\`\`\r
`,ce=`# Cases\r
\r
A case keeps one investigation together: the files you added, your theories, the health checks you ran, and the story of what you did. Cases are saved on this computer, in your Unravis folder.\r
\r
## The case list\r
\r
The start page lists your cases. Open one, type a case code, restore a case from a case file, or create a new case. A closed case is marked Closed.\r
\r
![The case list, dark](screenshots/P1/p1-case-list-dark.png)\r
![The case list, light](screenshots/P1/p1-case-list-light.png)\r
\r
The case list pictures show made-up cases from the capture demo. They are not real investigations.\r
\r
## Create a case\r
\r
Press Create a new case. The guide walks you through the steps. Step 1 asks what you are looking at.\r
\r
![Create a case, step 1, dark](screenshots/P1/p1-create-1-dark.png)\r
![Create a case, step 1, light](screenshots/P1/p1-create-1-light.png)\r
\r
### Pressing Create twice, reloading, or closing the window\r
\r
Each run of the guide has its own request number, kept in this browser tab. Unravis makes one case for one request number. So these all end with one case, not two:\r
\r
- You press Create twice, or press it again because nothing seemed to happen.\r
- You reload the page in the middle of Create. The same title picks up the same request number.\r
- The computer or Unravis stops in the middle of Create. When Unravis starts again it finishes the steps that were left.\r
\r
If you press Create for a case that was already made and then archived, you see "This case was already made. Find it on the start screen." Starting the guide again from the start, or changing the title, makes a new case.\r
\r
## Case home\r
\r
Case home shows what happened so far, in order, oldest first. It shows only what the case has saved: when it started, the files you added, your theories and your health checks.\r
\r
![Case home, dark](screenshots/P1/p1-case-home-dark.png)\r
![Case home, light](screenshots/P1/p1-case-home-light.png)\r
\r
On a narrow window:\r
\r
![Case home on a narrow window, dark](screenshots/P1/p1-case-home-dark-narrow.png)\r
![Case home on a narrow window, light](screenshots/P1/p1-case-home-light-narrow.png)\r
\r
## Health checks\r
\r
Health checks look at the SQL Server or MongoDB your logs depend on and say in plain words what looks healthy and what needs a look. They only read. They run only when you press Run checks.\r
\r
![Health checks in Case home, dark](screenshots/P1/p1-health-dark.png)\r
![Health checks in Case home, light](screenshots/P1/p1-health-light.png)\r
\r
The demo has no SQL Server kept and has run no checks, so these pictures show the empty state: "No SQL Server is kept yet" and "No checks have been run". Pictures with results arrive with the health-check phase.\r
\r
## Export evidence\r
\r
Export builds a file of the findings in the window you are looking at. You see a preview before anything is saved.\r
\r
![Export evidence, dark](screenshots/P1/p1-export-dark.png)\r
![Export evidence, light](screenshots/P1/p1-export-light.png)\r
\r
## Close and Reopen\r
\r
Close puts a case away without losing anything. A closed case can be read, but nothing can be added to it: no files, no theories, no checks. Close is on the Import page, next to each open case. Case home then says "This case is closed. You can read it, but nothing can be added." and the story shows "Case closed".\r
\r
Closing is not final. Press Reopen on Case home (or on the Import page, in the row of the closed case) and the case is open again. Reopen:\r
\r
- changes nothing but the state: the case is open again and what you may add to it is back;\r
- never starts anything by itself. Sampling and checks start only when you press them;\r
- writes "Case reopened" into the story.\r
\r
A closed case is still erased 30 days after it was closed, unless you reopen it.\r
\r
The MongoDB link and the filters the case had are not part of Reopen. They are saved with the case as you work, and Unravis puts them back whenever you open the case, closed or not. While the case is closed they cannot be changed, so nothing new is saved; once you reopen it, saving carries on.\r
\r
The first picture is the case list on the Import page. The second is a closed case with its buttons turned off. The third is the same case with the Reopen button. The pictures are made by \`tools/screens/run-screens.ps1 -Update\`.\r
\r
![Close case, in the Import page case list, light](screenshots/P2/p2-close-light.png)\r
![A closed case, read only, light, narrow](screenshots/P2/p2-closed-readonly-light-narrow.png)\r
![A closed case with the Reopen button, dark, narrow](screenshots/P2/p2-reopen-dark-narrow.png)\r
\r
Pressing Reopen twice quickly sends one request. The second press does nothing while the first is running.\r
\r
## Needs another try\r
\r
When you create a case and some steps do not go through (an environment list that was not saved, a source that was not tagged, a file that could not be read), the case is still made. What did not go through is kept in the case, in a list called Needs another try. It stays after you close Unravis, and it travels inside a case file.\r
\r
Each item says what failed, in one of four ways. They are different on purpose:\r
\r
| Label | What it means | Try again does |\r
|---|---|---|\r
| Environments not saved | The environment names were not saved. | Saves them again. |\r
| Source not saved | A log source was not tagged. | Tags it again. |\r
| Not added to analysis | A database was connected but not added as an audit source. | Nothing here. The line says to add it again from Connections. |\r
| Import failed | A file could not be read. | Opens the file picker, because the file has to be chosen again. |\r
\r
You can also Remove an item from the list. On a closed case the list says "This case is closed. Reopen it to try again." and shows no buttons. The list never keeps passwords or text from your files.\r
\r
The list sits above Analysis, and Case home counts the items in the header ("Needs another try").\r
\r
![Needs another try list, light](screenshots/P2/p2-retry-list-light.png)\r
\r
## Two tabs\r
\r
You can have the same case open in two browser tabs. Every case action checks what the page believes the case is (open, closed) against what it is now, and only one action runs on a case at a time.\r
\r
If another tab changed the case first, the page changes nothing and says: "This case changed in another tab." with a Reload button. Press Reload to see the case as it is now. Until you do, Reopen and the retry buttons on that page are turned off. In the Import page's case list the same refusal says the list now shows how the case is, because the list has already refreshed. This is the 409 answer with the message id M2. It is different from the two older answers: 423 means the master key is locked, and 409 "untrusted" means the case was not opened because its folder was not here when the key was set up.\r
\r
Examples: you close a case in tab A, and then add a file in tab B. Tab B is told the case is closed. You press Reopen in two tabs: the first reopens, the second sees M2.\r
\r
A second tab reopens the case, then the first tab presses Reopen. The tool puts the case back as it was afterwards.\r
\r
![Changed in another tab, light](screenshots/P2/p2-changed-elsewhere-light.png)\r
![Changed in another tab, dark, narrow](screenshots/P2/p2-changed-elsewhere-dark-narrow.png)\r
\r
## Archive\r
\r
Archive saves the whole case in one file you can keep, copy or move. You do not need to close the case first. Press "Archive this case" on Case home, choose a folder, and press "Archive the case".\r
\r
- The folder must be a drive path you can write to. A network drive, a relative path, a short 8.3 name and a folder inside Unravis's own folders are refused. Unravis's own folders are the cases folder, the folder that holds it, and the folder with the master key file, saved connections and history.\r
- The file is sealed with the master key of this computer. Without a master key you type a passphrase of at least 12 characters. Keep it: without it the file cannot be opened.\r
- Closing the drawer while the file is written does not stop the archive. Case home says "This case is being archived." with a percent and a "Show progress" button, and updates by itself when the archive is done.\r
- While the file is written the case is marked "Archiving". It can be cancelled ("Cancel archiving"); the case is then back as it was.\r
- When the file is written, Unravis reads it back to check it, and only then marks the case "Archived". An archived case is read-only. It moves to the Archived list on the start screen, and the working copy is kept.\r
- A case file Unravis finished but could not commit (for example Cancel at the last moment) is never deleted. It is listed under "Case files left over" in the health checks. See Troubleshooting.\r
\r
The pictures show the archive drawer on an open case.\r
\r
![Archive drawer, light](screenshots/P3/p3-archive-light.png)\r
![Archive drawer, dark, narrow](screenshots/P3/p3-archive-dark-narrow.png)\r
\r
## Check the case file\r
\r
"Check the case file" confirms that the case file can be restored. Unravis restores it to a temporary store (the \`.verify\` folder under the case root), compares it with the case, and removes the temporary store. The check does not change the case or the file. It can be cancelled. A passed check counts for one hour and only for that exact file: a file that changed since has to be checked again. A passed check is never trusted on its own to free space (see below).\r
\r
If the file is missing or does not match, the check fails with "The case file did not pass the check. The working copy was kept."\r
\r
## Free the space\r
\r
Free removes the working copy of an archived case to get its space back. The case file stays and becomes the only copy. It has three stages:\r
\r
1. Check the case file. This is the check above. Free cannot go on without a pass.\r
2. Confirm. The page shows the file that will be the only copy and warns that it can only be opened with the key it was made with. Type the case code.\r
3. Freeing. Just before anything is erased, and while no one else can change the case, Unravis checks the record and reads the case file again. If the file was deleted, moved, cut short, replaced, or archived again since the check, Free stops with "The case file did not pass the check. The working copy was kept." and the check has to be made again. Then a "being freed" flag stops new readers while the working copy goes. If something stops in the middle, Unravis finishes or undoes it the next time it starts; the case file is never touched.\r
\r
Free and Restore as a new case need the master key of this computer to be set up and unlocked. If there is no master key yet, the drawer says "This needs the master key of this computer. Set a master key first." with a "Set a master key" button, before the check starts. Nothing is changed. Restore in place does not need the key.\r
\r
Before a master key is reset ("Start over with a new master key"), the page says how many freed archived cases are sealed with the old key and that they can no longer be opened after the reset.\r
\r
A freed case is not "Missing". An archived case page is read-only: the analysis and Reopen are not offered, and the page says to restore first. A freed case stays in the Archived list, labelled "Space freed", and can be restored as a new case.\r
\r
The pictures show the first stage. The demo has no master key, so it shows the "Set a master key" message instead of the check.\r
\r
![Free the space, stage 1, dark](screenshots/P3/p3-free-1-dark.png)\r
![Free the space, stage 1, light](screenshots/P3/p3-free-1-light.png)\r
\r
## Restore\r
\r
There are two ways back.\r
\r
- Restore (in place) puts the case back in the working list. It is only offered while the working copy was kept, and nothing is read from the case file. The case ends Closed: Reopen it to add data.\r
- Restore from the case file as a new case reads the file and makes a new case with a new code. The archived case and the file are not changed. If the file was sealed with a typed passphrase, type it. A file sealed with the master key needs the same master key. The form on the start screen takes either a master key or a passphrase. A file that can only be opened with a different key says so, and the way out: use Restore from a case file with the master key or passphrase it was made with.\r
\r
A case file made before this version still restores with "Restore from a case file" on the start screen.\r
\r
The pictures show restoring in place, then restoring as a new case. The demo has no master key, so the second shows the "Set a master key" message.\r
\r
![Restore in place, light](screenshots/P3/p3-restore-in-place-light.png)\r
![Restore as a new case, dark](screenshots/P3/p3-restore-as-new-dark.png)\r
\r
## The Archived list\r
\r
The start screen shows "Archived cases (N)" under the working cases. The list is paged 50 at a time with "Show more archived cases". Each row shows the short case code ("Code 3f9a27c1"), so a case restored twice from one file can be told apart. Each row says "Archived", whether the working copy is kept or the space is freed, and "Key ending xxxx". A row whose working copy has disappeared reads "Working copy missing" and offers no Open. Rows offer Open, Restore and, under More, "Restore from the case file as a new case", "Free the space" and Erase.\r
\r
The first picture is the archived list. The second is Case home of an archived case.\r
\r
![Archived list, light](screenshots/P3/p3-archived-list-light.png)\r
![An archived case, light, narrow](screenshots/P3/p3-archived-home-light-narrow.png)\r
\r
## History\r
\r
Case home keeps a history of the case files made and the restores: "Case files made earlier", with the file name, the folder and "Key ending xxxx". The ending is the last four characters of the key check value, so two files made with different keys can be told apart without showing the key. The story lists "Case archived", "Space freed" and "Restored as a new case" with their times.\r
\r
## Case key (optional)\r
\r
A case can have its own key. It is off by default, and the demo never asks for one. A case without its own key opens as before: with the PC key if you set one, or with nothing if you chose Not now.\r
\r
You can add a key when you create the case: on the name step, tick "Protect this case with its own key", then type the key twice. Next stays off until the two match and the key is at least 12 characters. You can also add, change or remove it later, from the "Case key" block on Case home.\r
\r
What the key does:\r
\r
- It seals this case only. It is separate from your Windows sign-in and from the PC key. Neither of them opens a case that has its own key.\r
- Unravis shows the last four characters of a check code as "Key ending xxxx", so you can tell two keys apart. The key itself is never shown, never written to the log and never saved in a case file. It is not in the address bar either.\r
- A case with a key opens on one page: one field and one Open case button. A wrong key changes nothing and says so. After five wrong keys in a row Unravis makes you wait: one second, then twice as long each time, up to one minute. The Open case button is held for that time. The wait covers the whole case, not one browser tab.\r
- The case list shows a locked case by its short code ("Case k7m2qx4a") with its key ending. Its title is not read until the case is open.\r
- Master Lock in the top bar locks the case keys as well. After you unlock the PC key, a keyed case still asks for its own key.\r
\r
Change and Remove both ask for the current key. A key that is lost cannot be recovered and there is no reset. The case, and any case file made from it, stay shut. The only way to free the space is Erase.\r
\r
If you reset a damaged PC key, Unravis tells you first how many cases it will leave alone. Cases with their own key are never changed or counted by that reset, because the PC key does not seal them.\r
\r
An archive of a case with its own key is sealed with that key, not with the PC key, and shows that key's "Key ending xxxx". The case key has to be open to archive the case. The file opens anywhere with the case key alone, and the PC key does not open it. A freed case with its own key stays locked until you enter its key; Restore as a new case then reads the file. If the key was changed after the archive, Verify, Free and Restore say "This file can only be opened with the key it was made with."; type the key the file was made with. The reset warning counts only the freed archives sealed with the PC key, never the ones sealed with a case key.\r
\r
The first picture is the name step with the key fields. The second is the page that opens a locked case. The demo's keyed case is "Payroll run discrepancy, 14 October", and the picture types its demo key into the field without pressing the button.\r
\r
![Create a case with its own key, light](screenshots/P4/p4-case-key-create-light.png)\r
![Open a case that has its own key, dark, narrow](screenshots/P4/p4-case-key-unlock-dark-narrow.png)\r
\r
## Erase a case\r
\r
Erase removes a case from this computer for good. It is on Case home ("Erase this case"), and on the key page and the untrusted page when a case cannot be opened. It is not offered while an archive of the case is running: cancel the archive first.\r
\r
What Erase does:\r
\r
- You type the short case code, for example k7m2qx4a. The button stays off until the code matches.\r
- It removes the working copy, notes, suspects and checklist from this computer. An import or recording for that case stops.\r
- A case file you saved is not touched.\r
- It cannot be undone.\r
\r
Erase first writes an "Erasing" mark. Once the mark is there, the erase is committed: if Unravis stops half way, for example the PC loses power, the next start finishes it. Nothing is half erased and left open. While it runs, the case answers "This case is being erased."\r
\r
If another tab still has the case open, Erase waits up to 30 seconds for that tab to let go. If the tab does not, you see "The case is in use. Close its other tabs, then try again." and nothing is removed. Press Erase again after you close it.\r
\r
What is left: one line on the case list, "Erased case q2w7exak", with the time. It holds only the case code and the time, no title and no file name. Press Clear to remove the line. Clear never touches anything else.\r
\r
The first picture is the dialog when it opens. The second is the same dialog with the code typed and the Erase button on. The third is the case list with two Erased lines. The fourth is a legacy case that an older version made without a seal. Unravis will not open it, because it cannot tell whether it was changed. You can restore a case file you made yourself, or erase it.\r
\r
![Erase dialog, light](screenshots/P4/p4-erase-1-light.png)\r
![Erase dialog with the code typed, dark](screenshots/P4/p4-erase-2-dark.png)\r
![The case list with Erased lines, light](screenshots/P4/p4-erased-home-light.png)\r
![A legacy case that cannot be opened, light, narrow](screenshots/P4/p4-untrusted-home-light-narrow.png)\r
\r
`,le=`# Getting started\r
\r
## What you need\r
\r
- Windows 10 or 11, or Windows Server 2019 or later.\r
- A web browser. Unravis opens its screens in your own browser, on your own computer. Nothing is sent to the internet unless you press Check for updates.\r
\r
## Start Unravis\r
\r
Run \`unravis.exe\`. Your browser opens on the Unravis start page. Unravis listens only on your own computer (the loopback address), on a port that Windows picks for you.\r
\r
If you would rather open the page yourself, start it with \`--no-browser\`:\r
\r
\`\`\`\r
unravis.exe --no-browser\r
\`\`\`\r
\r
Unravis then prints a one-time link in its window. Paste the link into your browser. The link works once and expires after 10 minutes. To open Unravis again, restart it.\r
\r
Other options:\r
\r
| Option | What it does |\r
|---|---|\r
| \`--demo\` | Runs with made-up sample data, so you can look around without any logs or servers. |\r
| \`--port 8080\` | Uses a port you choose. Without it, Windows picks a free one. |\r
| \`--version\` | Prints the version and stops. |\r
\r
An older copy of the program, \`api-audit-explorer.exe\`, still starts the same Unravis.\r
\r
## Check the version\r
\r
\`\`\`\r
unravis.exe --version\r
\`\`\`\r
\r
The line shows the version, the commit it was built from and the build date, for example \`Unravis v1.0.0 (commit 065fbfd, built 2026-10-27)\`. A build that was not made from a release shows a short commit name in place of the version number. Quote this line when you ask for help.\r
\r
## Try the demo\r
\r
\`\`\`\r
unravis.exe --demo\r
\`\`\`\r
\r
The demo shows a quiet 15 minutes of sample traffic from two sample sources. The time at the top reads "Data reference time", because the sample data stands still at one moment.\r
\r
![The Unravis start page in the demo, dark](screenshots/P1/p1-home-dark.png)\r
![The Unravis start page in the demo, light](screenshots/P1/p1-home-light.png)\r
\r
The same page on a narrow window, such as a small laptop or a phone-sized browser. The header folds some buttons into a More menu.\r
\r
![The start page on a narrow window, dark](screenshots/P1/p1-home-dark-narrow.png)\r
![The start page on a narrow window, light](screenshots/P1/p1-home-light-narrow.png)\r
\r
## The Glossary\r
\r
Press Glossary in the header for plain-words meanings of the terms Unravis uses. Turn on Explain simply to see short hints beside the figures.\r
\r
![The Glossary, dark](screenshots/P1/p1-glossary-dark.png)\r
![The Glossary, light](screenshots/P1/p1-glossary-light.png)\r
\r
## Help inside Unravis\r
\r
The Glossary button in the header opens Help. Help holds the glossary and the same pages as this guide. Press Open the user guide in Help to read them. The pictures are left out in the app. Each picture shows a short line of text instead.\r
`,U=`# Guided analysis\r
\r
The guided analysis walks a slowdown through eleven steps, Step 0 to Step 10. It reads data you already have, turns it into plain facts, reads signals off the facts, and then checks a catalog of suspects against the signals. It never names what is to blame. It says what fits the data, what does not, and what could not be tested.\r
\r
This page describes the method first, then the screens that show it (see [The screens](#the-screens)). The screens live in the case: open a case, then choose **Open guided analysis**. Each screen is shown in dark and light.\r
\r
## How the engine thinks\r
\r
The engine works in three layers, always in this order.\r
\r
1. **Facts.** Numbers computed from the data with no judgment: the middle and the slow end of the times, the minute the slowdown started, how the time splits, how many calls arrived, and which changes sit near the start.\r
2. **Signals.** Each signal reads facts and answers yes, no or unknown. A signal says "no" only when the data was read and showed nothing. When the data is missing or too thin, the answer is unknown.\r
3. **Evaluation.** Each suspect names the signals it needs and the signals that normally go with it. The result is one of four words.\r
\r
| Word | Meaning |\r
| --- | --- |\r
| Consistent | The signals it needs read yes, and none that normally go with it read no. |\r
| Partly consistent | The main signal reads yes, but one that normally goes with it reads no or unknown. |\r
| Contradicted | A signal it needs read no on data that was actually read. |\r
| Cannot test yet | A signal it needs is unknown, as the data is missing. |\r
\r
Unknown never counts as no. Missing data never contradicts a suspect.\r
\r
### Readiness words per step\r
\r
Every step shows one readiness word.\r
\r
| Word | Meaning |\r
| --- | --- |\r
| Ready | The step ran on all the data it needs. |\r
| Partly ready | The step ran on some of the data. What was not read is listed. |\r
| Cannot test yet | A source the step needs is missing. The step does not pass. |\r
| Failed | The step could not run on what it was given: a window with no later end, two windows that overlap, or an audit source that could not be read. |\r
\r
A step with missing data never shows a pass. In this first version only the audit and the IIS logs are read, so Step 1 stays Partly ready at best, and Steps 7, 8 and 9 say Cannot test yet until their own data is imported.\r
\r
### Suspect colours\r
\r
| Colour | Meaning |\r
| --- | --- |\r
| Red | The most likely suspect. It is red only when its tier moved clearly first. Data-quality suspects never compete for red. |\r
| Orange | Likely. It fits the data, or it moved together with others. |\r
| Green | Ruled out by the data we have. This is not a claim about data we do not have. |\r
| Grey | Not sure, not testable, or does not apply. Grey is never a pass. |\r
\r
Each suspect also shows the cheapest next check that would tell it apart from its neighbours. Suspects about the host, MongoDB and SQL Server stay grey until their own data is imported.\r
\r
### Names that never change\r
\r
Every number the engine compares against sits in one table of named thresholds. The table has a version, and the version travels with every result, so an answer can always be read against the rules it was made under. Changing a threshold means changing the version; a test fails otherwise. The current version is 2026-11-03.\r
\r
The values that matter most:\r
\r
- A change is meaningful only when it is at least 30 in 100 **and** at least 100 milliseconds. Both must hold.\r
- The slowdown starts at the first minute whose slow end stays above twice the reference for five minutes in a row. A minute needs at least five calls to count.\r
- A group needs 20 calls before its P95 is shown and 100 before its P99 is shown.\r
- Clocks on different hosts may differ by up to 1 second, so onsets less than a minute and a second apart are not put in order.\r
- A gap of 2 minutes or less in the data is a warning and the step runs on the minutes that exist. A longer gap makes those minutes Cannot test yet. Missing minutes are never counted as zero.\r
\r
## When a comparison is greyed out\r
\r
A comparison shows grey instead of a number when the data is too thin to trust it.\r
\r
- A rate is compared only if the reference window has at least 60 events and the incident window at least 20.\r
- A reference P95 is compared only if the reference has at least 100 events.\r
- The reference events must be spread over the window. If nearly all fall in one or two minutes, the comparison is greyed out. At very low traffic (under one event per minute on average) this spread check is skipped.\r
- Rates are counted over minutes that have data on both sides. Minutes with missing data are left out, not counted as zero.\r
- P95 values come from a histogram and are within about 10 in 100 of the exact value.\r
\r
## Step 0. Frame\r
\r
> **In a nutshell:** A slow period only means something next to a normal one. Pick the slow stretch and a normal stretch from before it, and we compare the two.\r
\r
You choose the slow window, the normal window and, if you like, one route. Everything is read in UTC. A window whose end is not after its start, or two windows that overlap, make the step Failed. If the normal window sits after the slow window, or holds fewer than 20 calls, the step is Partly ready and says why.\r
\r
## Step 1. Trust data\r
\r
> **In a nutshell:** Before reading any number we check that the data can be trusted: clocks agree, no call is cut off, and nothing is missing.\r
\r
The engine counts calls that end before they start, or that last longer than six hours, as clock anomalies. They are left out and counted. It looks for gaps: minutes with no calls where the normal window would have had at least five a minute. It compares the number of audit calls with the number of IIS requests to see whether the audit missed part of the traffic. With only the audit, Step 1 never reads Ready; it says what it could and could not check.\r
\r
## Step 2. Shape\r
\r
> **In a nutshell:** We find when things first got slower, whether it was a sudden jump, a slow climb or a repeating pattern, and how many routes moved.\r
\r
The start is the first minute whose slow end stays above twice the reference for five minutes in a row. The shape is periodic when at least three slow bursts start a whole number of 15 or 60 minutes apart; otherwise it is a step when the slow end was at or below one and a half times the reference in the two minutes before the start, and a ramp when it was already building. The scope is one route, a few routes (up to three), many routes or everything (at least four routes in five).\r
\r
If the audit shows no clear start but the time outside the app does, the start is taken from the outside time and the step says so.\r
\r
## Step 3. Time split\r
\r
> **In a nutshell:** Each call's time is split into audit set-up, the work itself and time outside the app. The part that grew most shows where to look first.\r
\r
A call's recorded time is audit set-up (A) plus the action (C). Payload capture (P) comes after and is outside it. Time outside the app (O) is the IIS time taken, minus the recorded time, minus P. Each part's growth is shown as a share of the extra time. A part is dominant when it holds at least half of the extra time and has risen by a meaningful amount.\r
\r
O needs the IIS logs. Without them O is Cannot test yet; it is never shown as zero. The IIS logs are matched to audit calls by their end time within 3 seconds and the same route, one to one. If fewer than half of the audit calls find a match, O is not shown. O is also not shown when the matched pairs do not line up at one steady clock difference between the two hosts, as they may then be different calls on the same route. A steady difference of more than 1 second is shown as a note on Step 1.\r
\r
## Step 4. Load\r
\r
> **In a nutshell:** We set the number of calls arriving against how long each one takes, to see whether a pool or a limit could have filled up.\r
\r
Calls in flight equal the arrival rate times the time each call spends. The audit covers a slice of the traffic, so the engine uses a planning arrival rate of 40 calls a second and shows the measured audit rate next to it. If you enter a pool limit, the engine reports whether the calls in flight reached 95 in 100 of it, and whether the top stayed flat for at least three minutes within 5 in 100 of one level. A flat top is a sign that something filled up; it does not say what.\r
\r
## Step 5. Changes\r
\r
> **In a nutshell:** We place the changes you listed next to the start of the slowdown. A change counts only if it came just before the start, not if it merely overlapped.\r
\r
A listed change leads the start when it sits within 30 minutes before it and not after it. An empty list entered on purpose reads "No change listed". A list you did not enter reads "Cannot test yet". The list is typed by hand, so the step always says so. Your release labels are shown exactly as you typed them.\r
\r
## Step 6. App tier\r
\r
> **In a nutshell:** We check the app's own queues and pools, using what the audit and the IIS logs can show. What they cannot show is marked as not tested.\r
\r
The audit and the IIS logs can show retried calls (the same client calling the same route again within 10 seconds; a rise counts at 30 in 100 more, with at least 5 retried calls and at least 2 in every 100 calls retried), server errors and their order against the start, and whether outside time grew. Thread counts, queue lengths and pool counters are not in this data; the suspects that need them are listed as not tested.\r
\r
## Step 7. SQL\r
\r
> **In a nutshell:** We check SQL Server against its own data: waits, blocking and plans. Until that data is imported, this step says Cannot test yet.\r
\r
This step reads one day of a SQL Server with four fixed read-only checks: server start and thread pool, the error log, Query Store, and Agent jobs and backups. A check that cannot run says Cannot test yet and lists what is missing. With no SQL Server read in the case, the whole step says Cannot test yet and lists what to add. See [Sources](sources.md).\r
\r
## Step 8. Mongo\r
\r
> **In a nutshell:** We check MongoDB against its own data: slow operations, cache and disk. Until that data is imported, this step says Cannot test yet.\r
\r
The step reads the slow operations the profiler recorded and when they began. Other Mongo questions are not read yet and are listed as Cannot test yet. Even with MongoDB data, this step stays at Partly ready in the first release.\r
\r
## Step 9. Host\r
\r
> **In a nutshell:** We check the host against its own counters: CPU, memory, disk and network. Until that data is imported, this step says Cannot test yet.\r
\r
This step reads the CPU, memory, disk and network counters of a Blackbox. With no Blackbox in the case it says Cannot test yet. Even with host data, this step stays at Partly ready in the first release.\r
\r
## Step 10. Origin\r
\r
> **In a nutshell:** We line up when each tier first moved. The earliest is the origin and the others followed or amplified it. If they moved together, we say there is no clear origin.\r
\r
Each tier that can be dated gets a start minute: the arrival rate, audit set-up, the action, outside time, and the SQL, Mongo and host lanes when the case holds their data. The engine then reads them in order:\r
\r
- One tier clearly first (more than a minute and a second ahead of the others): that tier is the origin, the others are victims or amplifiers.\r
- Tiers within a minute and a second of each other: they moved together, and the engine says there is no clear origin.\r
- Only one tier could be dated: it is named the origin only if every other tier was read in every slow minute and held flat. If any tier could not be read, the answer is not enough data.\r
- No tier could be dated: no origin is named.\r
\r
How sure the engine is shows as one word: None, Low, Medium or High. None means there is no answer. Low means the audit alone, or no clear origin. Medium means the audit and the IIS logs. High means the audit, the IIS logs and at least one other tier's own data. A clock anomaly or a long gap lowers it one step. There is no score and no percentage.\r
\r
The step also lists the sources used, the sources missing, the suspects that could not be tested, and one check that would overturn the answer.\r
\r
## The screens\r
\r
Open a case and choose **Open guided analysis**. The page has a left rail with Steps 0 to 10, a tab row (Steps, Checklist, After the fix, Nearby activity, Export) and one **Run the steps** button. The windows are typed in UTC. A run only reads: it cannot change a case or a source.\r
\r
Every step page has the same frame: the step name, a status sticker, an **In a nutshell** sentence, the reasons for any step that could not be fully tested, the body, and Previous step and Next step.\r
\r
Status words are fixed. After a run they are Ready, Partly ready, Cannot test yet or Failed. Before a run a step reads Waiting, during a run Running, and both are hatched, never green. These two and the **Out of date, rerun** line are the only words beyond the four. They say a step has no answer to show yet, or that the answer is old, and never that anything passed. A run that fails before any answer arrives reads Failed on every step. When the windows, sources or lists change after a run, each step after Step 0 says **Out of date, rerun**. The numbers on screen stay those of the earlier run until you run again.\r
\r
The rail rows are buttons (outlined). The status next to each row is a sticker: information, not a button. There is one primary button on a screen. On the Steps tab it is **Run the steps** until an answer is on screen, then **Run again** (outlined) takes over.\r
\r
You can jump to any step without running first. It then shows the last run, or says to run the steps first.\r
\r
Use **Previous step**, **Next step** and the step list to move between steps. The browser's Back button does not step back through them, and Reload does not return to the same step. Steps and cases have no address of their own in this release.\r
\r
### Step 0 screen\r
\r
![Step 0, the question, dark](screenshots/P6/p6-step-0-dark.png)\r
![Step 0, the question, light](screenshots/P6/p6-step-0-light.png)\r
\r
The question: an incident window and a normal reference window, both in UTC, an optional API to focus on, an optional SqlClient pool limit, and a list of changes (up to 50, each with a time and a label). Quick picks (Last hour, Today, Yesterday) fill the incident window and a normal period of the same length just before it. After a run the page shows the onset Unravis found and how many calls the run used. When no onset is found, the page says that is a finding, not an error, and offers **Carry on without an onset**.\r
\r
A change needs a time and a name. Changes you type are used in Step 5 only when **I have listed every change I know of** is ticked.\r
\r
Windows that overlap, run backwards or are longer than 7 days are explained in a sentence and the run button waits.\r
\r
### Step 1 screen\r
\r
![Step 1, trust the data, dark](screenshots/P6/p6-step-1-dark.png)\r
![Step 1, trust the data, light](screenshots/P6/p6-step-1-light.png)\r
\r
Trust the data: cards for the incident and reference windows (calls, how many had no start time or no end time), one row per source with its status, and the gaps found in the minutes. If a window has too few calls, the page says so and offers to widen the incident window.\r
\r
### Step 2 screen\r
\r
![Step 2, shape, dark](screenshots/P6/p6-step-2-dark.png)\r
![Step 2, shape, light](screenshots/P6/p6-step-2-light.png)\r
\r
Shape: the minute-by-minute line of the slow end, the onset, the shape of the slowdown in a word and a table of APIs ranked by the biggest rise. A rise counts only when it passes both marks, 30 percent and 100 ms. When nothing moved, the page says what the two marks were.\r
\r
### Step 3 screen\r
\r
![Step 3, time split, dark](screenshots/P6/p6-step-3-dark.png)\r
![Step 3, time split, light](screenshots/P6/p6-step-3-light.png)\r
\r
Time split: the parts A, C, P and O drawn as bars on one scale. Only parts that could be tested are drawn. Part O needs an IIS file, and when it is missing the page names it as Cannot test yet.\r
\r
### Step 4 screen\r
\r
![Step 4, load, dark](screenshots/P6/p6-step-4-dark.png)\r
![Step 4, load, light](screenshots/P6/p6-step-4-light.png)\r
\r
Load: four measures for both windows (calls a second, time in system, calls in flight on average, calls in flight at the peak), each with No data when it could not be measured, and the pool answer when a limit was entered.\r
\r
### Step 5 screen\r
\r
![Step 5, changes, dark](screenshots/P6/p6-step-5-dark.png)\r
![Step 5, changes, light](screenshots/P6/p6-step-5-light.png)\r
\r
Changes: each change you listed with its time in UTC, its label and how it sits against the onset, and the suspects of that group. Changes are only compared when you list them in Step 0.\r
\r
### Step 6 to Step 9 screens\r
\r
![Step 6, application, dark](screenshots/P6/p6-step-6-dark.png)\r
![Step 6, application, light](screenshots/P6/p6-step-6-light.png)\r
![Step 7, SQL Server, dark](screenshots/P6/p6-step-7-dark.png)\r
![Step 7, SQL Server, light](screenshots/P6/p6-step-7-light.png)\r
![Step 8, MongoDB, dark](screenshots/P6/p6-step-8-dark.png)\r
![Step 8, MongoDB, light](screenshots/P6/p6-step-8-light.png)\r
![Step 9, host, dark](screenshots/P6/p6-step-9-dark.png)\r
![Step 9, host, light](screenshots/P6/p6-step-9-light.png)\r
\r
One page per tier: application, SQL Server, MongoDB and host. Each page shows the findings the engine made for the tier and a card for every suspect of the tier. The SQL, Mongo and Host pages say Cannot test yet until the case holds that source, and offer **Add data**. A suspect that could not be tested is grey and never counts as ruled out.\r
\r
### Step 10 screen\r
\r
![Step 10, origin and suspects, dark](screenshots/P6/p6-step-10-dark.png)\r
![Step 10, origin and suspects, light](screenshots/P6/p6-step-10-light.png)\r
![Step 10, origin and suspects, dark, narrow](screenshots/P6/p6-step-10-dark-narrow.png)\r
![Step 10, origin and suspects, light, narrow](screenshots/P6/p6-step-10-light-narrow.png)\r
![Step 10 when no origin is clear, dark](screenshots/P6/p6-no-clear-origin-dark.png)\r
![Step 10 when no origin is clear, light](screenshots/P6/p6-no-clear-origin-light.png)\r
\r
Origin and suspects. The page shows, in order:\r
\r
1. **What moved first**: the origin and victim table (tier, when it first moved in UTC, what moved, role). When two tiers moved together the page says **No clear origin** and lists them without calling one first.\r
2. **Colours**: red most likely, orange likely, green ruled out, grey not sure. Grey is never a pass. The green card reads "Ruled out by the data we have", so a ruling-out is never worded as stronger than the data behind it.\r
3. **Suspects**: each ranked suspect as a card with its colour word, why it fits, the evidence kind (observed on its own data or inferred), the check that would overturn it, and **Add this check** to the checklist. Grey suspects follow, then the ones ruled out (collapsed).\r
4. **The cheapest check** to make next.\r
5. **How sure we are**: None, Low, Medium or High, the sources used and missing, and the one check that would overturn the answer.\r
\r
Unravis does not name a culprit.\r
\r
### Nearby activity screen\r
\r
This panel has no screen of its own: it is captured inside the Tools screens.\r
\r
Pick a moment in UTC and a span of 0 to 120 minutes either side. The audit lane lists the calls of the window (the first 100 at most) as bars, banded by their time: under 1 second, 1 to 2 seconds, over 2 seconds. These bands are not the suspect colours. A call with no time is shown as No time, never as fast. The SQL Server, MongoDB and Host lanes read Cannot test yet. The page never shows a user, an address or a URL. Nearby activity is not included in the report.\r
\r
### Checklist screen\r
\r
![The Checklist tab, dark](screenshots/P6/p6-checklist-dark.png)\r
![The Checklist tab, light](screenshots/P6/p6-checklist-light.png)\r
![The Checklist tab, dark, narrow](screenshots/P6/p6-checklist-dark-narrow.png)\r
![The Checklist tab, light, narrow](screenshots/P6/p6-checklist-light-narrow.png)\r
\r
Four lists: Need to do, Working on, What we learned and Ruled out. A counter shows how many items are open and how many are done. You can add an item (up to 300 characters, 500 items in all), move it to another list, or delete it. **Add suggested checks from Step 10** adds each suggested check once. On a closed case the lists can be read but not changed.\r
\r
### After the fix screens\r
\r
![After the fix, before a later day is picked, dark](screenshots/P6/p6-after-fix-1-dark.png)\r
![After the fix, before a later day is picked, light](screenshots/P6/p6-after-fix-1-light.png)\r
![After the fix, with a later day compared, dark](screenshots/P6/p6-after-fix-2-dark.png)\r
![After the fix, with a later day compared, light](screenshots/P6/p6-after-fix-2-light.png)\r
\r
Pick a later day. Unravis compares the same hours on that day with the incident window you ran, using the incident as the normal period. The page shows the earlier day, the later day and the change for the slow end, the middle, the mean and the load. The verdict is one of Fixed, Better (not fixed), Unchanged, Worse, Partly there, No data for that day or The days are not alike.\r
\r
- Fixed needs a known normal range and a slow end back inside it.\r
- A fall that is still above normal is Better, not Fixed.\r
- A day with missing minutes is Partly there. Missing minutes are not counted as zero.\r
- If the two days are not alike (the calls differ by more than 50 percent, or the API mix moved by more than 0.3, or the later day has less than 0.8 of the minutes) the page warns and never says Fixed. **Pick a more alike day** and **Compare one route only** are offered.\r
\r
**Record result** writes one line (300 characters at most) under What we learned. Nothing is recorded without a click.\r
\r
### Export screens\r
\r
![The Export tab, dark](screenshots/P6/p6-export-1-dark.png)\r
![The Export tab, light](screenshots/P6/p6-export-1-light.png)\r
![The report preview after a run, dark](screenshots/P6/p6-export-2-dark.png)\r
![The report preview after a run, light](screenshots/P6/p6-export-2-light.png)\r
\r
The report is made from the run on screen. The server reads the case data again to build it, so a large case can take a moment. Tick what to include: the headline figures, the suspects, the checklist and the sources. **Download report (HTML)** is the one primary button. **Download data (CSV)** has the numbers only. **Preview** shows the report in a sandboxed frame before you save.\r
\r
- The file never contains a request payload, a user id, a connection detail or a password.\r
- The CSV starts with a byte-order mark so a spreadsheet reads it as UTF-8, and any cell that starts with =, +, - or @ is written as text so it cannot run as a formula.\r
- The file is saved only when its checksum matches what was built. If it does not, nothing is saved and the page says so.\r
- A report made from a run that is out of date, or from a read that stopped at its limit, says so at the top and the button asks you to confirm.\r
- An empty case still exports, with a note that nothing was analysed.\r
\r
## What the engine does not do yet\r
\r
- It does not read SQL Server, MongoDB or host data. Those lanes stay Cannot test yet.\r
- It does not compare against the same weekday and hour of earlier weeks; the normal window is the one you pick.\r
- It does not read shared stalls, error surges or near-saturation signals.\r
`,W=`# Unravis user guide\r
\r
Unravis helps you find out what went wrong in production. It reads your audit logs and, when you allow it, the health of your SQL Server and MongoDB. It only reads. It never changes your systems.\r
\r
| Page | What it covers |\r
|---|---|\r
| [Getting started](getting-started.md) | Start Unravis, open it, check the version, try the demo |\r
| [Cases](cases.md) | Keep one investigation together: files, theories, health checks |\r
| [Sources](sources.md) | Connect SQL Server and MongoDB, and add log files |\r
| [Blackbox](blackbox.md) | Always-on recording of what the servers were doing |\r
| [Guided analysis](guided-analysis.md) | The step-by-step method that walks you to a finding |\r
| [Manual tools](manual-tools.md) | The charts and lists you can use on your own |\r
| [Install](install.md) | The one-file installer |\r
| [Upgrade and uninstall](upgrade-and-uninstall.md) | Move to a new version, or remove Unravis |\r
| [Troubleshooting](troubleshooting.md) | Common problems and what to do |\r
| [Logs](logs.md) | Where the app's log lives, what it records, what it never records, and how to send it |\r
| [Security](security.md) | What Unravis reads, what it keeps, and what it never does |\r
| [Screenshots](screenshots.md) | How the pictures in these pages are made (for contributors) |\r
\r
Not yet run on a clean PC: install, upgrade and uninstall, the key keeper and the Blackbox service.\r
\r
The same pages are in the app, under Help.\r
`,ue=`# Install\r
\r
Unravis comes as one file, \`unravis-setup.exe\`. It checks the PC, asks a few questions, and puts Unravis on it. If any step fails, setup puts back everything it changed, so nothing is left half done. To move to a new version, repair, or remove Unravis, see [Upgrade and uninstall](upgrade-and-uninstall.md).\r
\r
Setup never asks for a password, never connects to a database, and holds no secrets. The programs inside it are listed in a manifest with their SHA-256, and setup refuses to run if one was changed after the build.\r
\r
> NOT RUN: needs a clean VM. The real Windows parts (registry, services, firewall, the Windows approval prompt, shortcuts, folder permissions) were built behind interfaces and tested with fakes on a development PC. They have not yet run on a real install.\r
\r
## Checks\r
\r
Setup opens its window in Microsoft Edge or Google Chrome, in a plain app window. The first screen lists the checks it made, each with a sticker: **Ready**, **Partly ready** or **Failed**.\r
\r
![Install Unravis, checks all ready, dark](screenshots/P11/p11-welcome-dark.png)\r
![Install Unravis, checks all ready, light](screenshots/P11/p11-welcome-light.png)\r
\r
| Check | What it looks at |\r
|---|---|\r
| Windows | Windows 10 version 1809 or later, or Windows Server 2019 or later, 64-bit. |\r
| Setup program | The programs inside setup match their manifest. |\r
| Other setup | No other Unravis setup is running. |\r
| Earlier install | Whether Unravis is already on this PC. If it is, setup upgrades or repairs it instead. |\r
| Unravis open | Whether Unravis is running. Setup can close your own Unravis for you and open it again afterwards. If it is open for another person on this PC, this check fails with "Unravis is open for another person on this PC": setup never closes someone else's Unravis, so ask them to close it or sign out. |\r
| Blackbox service | Only when you place a Blackbox: no Blackbox is installed on this PC yet. |\r
| Browser | Microsoft Edge or Google Chrome, which Unravis opens in. |\r
| Setup window | Whether WebView2 is installed. Without it the window opens in the browser. Nothing to do. |\r
| Space for the program | At least 100 MB free on the drive of the program folder. |\r
| Space for the recordings | Only with a Blackbox: at least 1 GB free on the drive of the recordings folder. |\r
| Blackbox port | Only with a Blackbox: the port is free. |\r
| Recordings folder | Only with a Blackbox: the folder can be used. Recordings already in it are kept. |\r
| Administrator approval | Whether the install needs it, and whether this account can give it. |\r
| Restart | A restart Windows is waiting for. Setup can go on. |\r
| Earlier setup | An earlier setup that did not finish. Setup puts back what it changed before it starts. |\r
| Shared data folder | For everyone on this PC: \`C:\\ProgramData\\Unravis\` is free, or was made by an earlier setup. |\r
| Program folder | A new install does not go into a folder that already holds other files. For everyone on this PC, an existing folder is only used when an earlier Unravis setup made it, or when it is empty and only administrators own it. A folder that is a link to another place is refused, for this folder and for the data folders. |\r
| Read key file | Only with a Blackbox and a read key file in silent mode: the file does not exist yet. Setup never replaces a file. |\r
\r
A **Failed** check stops the install and says what to do. Fix it, then press **Check again**. A **Partly ready** check does not stop anything.\r
\r
![A failed check: not enough space, dark](screenshots/P11/p11-preflight-fail-dark.png)\r
![A failed check: not enough space, light](screenshots/P11/p11-preflight-fail-light.png)\r
\r
## Options\r
\r
- **Just for me** installs into \`%LOCALAPPDATA%\\Programs\\Unravis\`. It needs no administrator approval, unless you also place a Blackbox.\r
- **For everyone on this PC** installs into \`C:\\Program Files\\Unravis\`. It also makes the shared data folder \`C:\\ProgramData\\Unravis\`, records it under \`HKLM\\SOFTWARE\\Unravis\`, and sets up the key keeper service (\`UnravisKeeper\`) that holds saved connections for everyone. This needs administrator approval.\r
- **Folder.** You can type another folder. Spaces and letters such as é are fine. There is no Browse button. A name cannot end with a dot or a space, because Windows would drop it. Short names such as \`PROGRA~1\` are read as the real folder before anything is checked. For everyone on this PC the folder cannot be inside a person's profile (such as \`C:\\Users\\Ana\`).\r
- **Start Unravis when I sign in** is ticked by default.\r
- **Close Unravis for me** appears only when Unravis is open. It is ticked by default. When Unravis is not open the option is not shown.\r
- **Also place a Blackbox recorder on this PC** installs the Blackbox service (\`UnravisBlackbox\`) in \`C:\\Program Files\\Unravis Blackbox\`. It uses the same settings as the Place a Blackbox wizard: **Name**, **Folder**, **Keep (days)**, **Max (GB)**, **Keep free (%)** and **Port**. See [Unravis Blackbox](blackbox.md).\r
- **Allow other PCs to read it (firewall rule)** is shown under the Blackbox choices. It adds a Windows firewall rule, \`Unravis Blackbox (TCP <port>)\`, open only to the addresses you list in **Addresses**. Without it no rule is added.\r
\r
![Where and how, with the Blackbox ticked, dark](screenshots/P11/p11-options-dark.png)\r
![Where and how, with the Blackbox ticked, light](screenshots/P11/p11-options-light.png)\r
\r
## Approval\r
\r
When an install needs administrator approval, setup shows a screen headed **Windows will ask for admin approval** before Windows asks. It says why, for example installing for everyone on this PC or placing the Blackbox, and lists what it **Will change** and what it **Will not change**. Press **Continue** and choose Yes in the Windows prompt. If you choose No there, nothing is changed, setup says so, and **Continue** asks again. When no approval is needed, the step rail shows **Not needed** for Approval.\r
\r
![Windows will ask for admin approval, dark](screenshots/P11/p11-uac-explain-dark.png)\r
![Windows will ask for admin approval, light](screenshots/P11/p11-uac-explain-light.png)\r
\r
## Progress\r
\r
Each step has its own row: **Waiting**, **Running**, **Done** or **Failed**. The steps for an install are:\r
\r
1. Close Unravis (only if it is open)\r
2. Make the program folder\r
3. Copy the program files\r
4. Make the shared data folder, record it, and set up the key keeper service (only for everyone on this PC)\r
5. Set up the Blackbox recorder (only if ticked)\r
6. Open the Blackbox port to the listed PCs (only if asked)\r
7. Add the Start menu shortcut\r
8. Add Unravis to Installed apps\r
9. Save the setup choices\r
10. Check that Unravis starts\r
\r
Every change is written to a journal before it is made. **Cancel** stops the run and puts back everything it changed. Rows then show **Put back**.\r
\r
![Installing, step by step, dark](screenshots/P11/p11-progress-dark.png)\r
![Installing, step by step, light](screenshots/P11/p11-progress-light.png)\r
\r
## Done\r
\r
When you placed a Blackbox, the Done screen shows its read key **once**. Unravis on another PC needs this key to read this Blackbox. Press **Copy** and keep it somewhere safe: **Close** and **Open Unravis** stay off until you have copied it. The key is never written to the log.\r
\r
![Unravis is installed, with the read key, dark](screenshots/P11/p11-done-dark.png)\r
![Unravis is installed, with the read key, light](screenshots/P11/p11-done-light.png)\r
\r
## When it fails\r
\r
If a step fails, setup stops, puts back every step before it in reverse order, and says which step failed and what to do. The heading says whether everything was put back. If something could not be put back, it is listed by name with its path, and the log has the details. After a failed install, **Back to Options** lets you change a choice, for example the port, and run again. After a failed upgrade or repair, **Try again** runs it again (it is off when something could not be put back: read the log first).\r
\r
![Install did not finish, everything put back, dark](screenshots/P11/p11-failed-rolled-back-dark.png)\r
![Install did not finish, everything put back, light](screenshots/P11/p11-failed-rolled-back-light.png)\r
\r
## Silent install\r
\r
For scripted installs, put the choices in a file and run:\r
\r
\`\`\`\r
unravis-setup.exe --silent --config setup.json [--log C:\\path\\setup.log]\r
\`\`\`\r
\r
\`\`\`json\r
{\r
  "scope": "machine",\r
  "location": "C:\\\\Program Files\\\\Unravis",\r
  "startAtSignIn": true,\r
  "blackbox": { "name": "web-01", "folder": "D:\\\\UnravisBlackbox", "keepDays": 7, "maxGB": 2, "minFreePercent": 10, "port": 7788 },\r
  "firewall": { "remoteAddresses": ["10.0.0.5", "10.0.1.0/24"] },\r
  "readKeyFile": "C:\\\\Users\\\\ana\\\\Documents\\\\web-01-read-key.txt"\r
}\r
\`\`\`\r
\r
- \`scope\` is required: \`"user"\` or \`"machine"\`. \`location\` is optional.\r
- \`blackbox\` and \`firewall\` are optional. \`firewall\` needs \`blackbox\`.\r
- \`readKeyFile\` is required when a Blackbox is placed. The read key is written there once, readable only by you. The file must not exist yet: setup never replaces a file, and it makes the new file with its permissions already set. Silent mode shows it nowhere else.\r
- \`startAtSignIn\` defaults to true. \`openWhenDone\` is accepted but ignored: silent mode never opens a window.\r
- Keys are case sensitive. An unknown key, a wrong type, a second JSON value or a file over 64 KB stops setup with a sentence that names the problem, before it changes anything. A Notepad UTF-8 mark at the start is fine.\r
- A key set to \`null\` counts as left out, as if it were not in the file. For \`scope\` that gives the same sentence as a missing \`scope\`.\r
\r
Silent mode never closes a running Unravis. If it is open, the check fails and setup stops.\r
\r
### Exit codes\r
\r
| Code | Meaning |\r
|---|---|\r
| 0 | Done. |\r
| 1 | A check failed. Nothing was changed. |\r
| 2 | The command line or setup.json is wrong. Nothing was changed. |\r
| 3 | The Windows approval was refused. Nothing was changed. |\r
| 4 | A step failed, or the run was cancelled, and everything was put back. |\r
| 5 | Anything else, including items that could not be put back or removed. Read the log. |\r
\r
## The log\r
\r
Every run writes a log to \`%LOCALAPPDATA%\\Unravis\\setup-logs\\setup-YYYYMMDD-HHMMSS.log\`, or to the path given with \`--log\`. The part that runs with administrator approval writes its own log next to it, with \`-admin\` at the end of the name. The Done and failure screens show the path. The log holds steps, paths and errors. It never holds the read key or any password.\r
\r
## Other command-line options\r
\r
| Option | What it does |\r
|---|---|\r
| \`--upgrade\`, \`--repair\`, \`--uninstall\` | Start in that mode. See [Upgrade and uninstall](upgrade-and-uninstall.md). |\r
| \`--allow-downgrade\` | Allow an upgrade to an older version. |\r
| \`--no-browser\` | Print the window's address instead of opening it. |\r
| \`--version\` | Print the version and exit. |\r
\r
Running setup a second time while Unravis is installed does not install it twice. It offers to upgrade, or to repair when it is the same version.\r
`,de=`# Logs\r
\r
Unravis keeps a plain-text log of what it did and what went wrong, so a\r
problem can be traced afterwards — when a read failed, you can see which\r
source, which stage and why, without re-running anything.\r
\r
The log lives in the app's own data folder, in a \`logs\` subfolder\r
(\`unravis.log\`, with up to five rotated copies kept beside it as\r
\`unravis.1.log\` … \`unravis.5.log\`). The **Help** section of the app shows the\r
exact folder and a button that copies its path.\r
\r
## What is logged\r
\r
- Each start: the app version, the operating system, the port and the data\r
  folder, and whether the dashboard opened in the browser or a link was\r
  printed.\r
- Each request the app answers, as one line: a short request id, the route\r
  (never the full address), the status and how long it took. The same id is\r
  shown in error entries, so a problem in the app can be matched to the exact\r
  line.\r
- The journey: a source added or removed, an import started and finished\r
  (counts only), a case created, opened, archived, restored or erased, a\r
  guided analysis started and finished, an export started and finished, a\r
  settings change, a lock and unlock.\r
- Every error the app or this page shows, with a short class (for example\r
  \`source_unreachable\` or \`validation\`) and a cleaned-up message.\r
- Per source during an investigation: when the read started and finished,\r
  how long it took, how much came back, and — when it failed — the reason,\r
  including which time budget ran out.\r
\r
## What is never logged\r
\r
- Passwords, the one-time session token, case keys and connection strings.\r
  Error messages are passed through a filter that replaces anything looking\r
  like a credential, a \`password=\`-style value, a bearer token or a long\r
  base64 secret before it can reach the file, and file paths under your\r
  profile are written as \`%USERPROFILE%\`.\r
- Request and response bodies, audit rows and payloads, and anything typed\r
  into a field. The page reports screens by name and actions by name — never\r
  ids, searches or field contents.\r
\r
## Sending the log\r
\r
Open **Help** in the app, find **Support logs**, and copy the folder path.\r
Send the newest \`unravis.log\` to your contact; it contains no passwords or\r
case data.\r
`,G=`# Manual tools\r
\r
The manual tools are the same facts as the guided steps, one tool at a time, in any order. Use them when you already know what you want to look at, or when you want to try a window before you run all eleven steps.\r
\r
## Two ways of working\r
\r
Open a case, choose **Open guided analysis**, then open **Case actions** and choose **Switch to Manual tools**. The same menu switches back to **Guided analysis**. The choice is remembered for each case in this browser.\r
\r
Switching never loses anything. Your notes, the checklist, the saved views and the last run are kept. A closed case can switch too, because switching only changes what is shown.\r
\r
## The tools\r
\r
![The tools page, dark](screenshots/P6/p6-tools-1-dark.png)\r
![The tools page, light](screenshots/P6/p6-tools-1-light.png)\r
![The Slowest APIs tool after a run, dark](screenshots/P6/p6-tools-2-dark.png)\r
![The Slowest APIs tool after a run, light](screenshots/P6/p6-tools-2-light.png)\r
![The SQL blocking and waits tool, Cannot test yet, dark](screenshots/P6/p6-tools-3-dark.png)\r
![The SQL blocking and waits tool, Cannot test yet, light](screenshots/P6/p6-tools-3-light.png)\r
\r
The tools page is a grid of cards. Each card has the tool name, one line about it, the sources it reads and a status sticker, and one outlined button to open it. There are thirteen tools.\r
\r
| Tool | Reads | Same as |\r
| --- | --- | --- |\r
| Response time over time | Audit | Step 2 |\r
| Slowest APIs | Audit | |\r
| Slowest calls | Audit | |\r
| Time split A C P O | Audit and IIS | Step 3 |\r
| Calls in flight | Audit | Step 4 |\r
| Nearby activity | Audit, IIS, SQL Server, MongoDB, Host | |\r
| Changes timeline | Audit and your change list | Step 5 |\r
| SQL blocking and waits | SQL Server | Step 7 |\r
| MongoDB health | MongoDB | Step 8 |\r
| Host CPU, memory and disk | Blackbox | Step 9 |\r
| SQL host disk, memory and network | Blackbox on the SQL host | Step 9 |\r
| After the fix | Audit | |\r
| Suspect summary | Audit and any other source in the case | Step 10 |\r
\r
A tool that shows a step takes the status word of that step from the last run. A tool that has not run yet reads Waiting.\r
\r
### A tool whose source is missing\r
\r
A tool never shows an empty chart and never says it passed. When a source it needs is not in the case, its card reads **Cannot test yet** and the tool page says which source is missing and offers **Add by location**. That button opens the same drawer as the case home. The time split runs on audit alone and says what IIS would add.\r
\r
### Inside a tool\r
\r
Each tool opens with a **Back to tools** button, the incident and reference windows (folded away when you open a tool with the windows already set; they stay open while you work), the **Run the steps** bar, the tool itself, a place to write a note, saved views, and **Run Guided analysis instead**. The note is added to the checklist under What we learned.\r
\r
**Slowest APIs** lists every API of the last run, ranked by its slow end in the incident window. Two filters narrow it: the fewest calls an API must have, and part of its name. An API with too few calls to have a slow end goes last, never first. **Slowest calls** lists the individual calls that took longest in a window. It shows the API, the start and the time of each call, and nothing about who sent it.\r
\r
## Saved views\r
\r
A saved view keeps a name, a tool, the two windows, the API focus and the tool's own filters. It does not keep a result, a request body, a URL or any secret. Opening a view puts the windows and filters back and opens the tool. It does not run anything.\r
\r
- A name is plain text of up to 60 characters.\r
- A case holds up to 200 views. At the limit the page says so and asks you to delete one.\r
- On a closed case views can be opened but not saved or deleted.\r
- A damaged view opens with whatever is good in it.\r
\r
## Export\r
\r
The export is the last tab of the guided analysis and is described with the other screens in [Guided analysis](guided-analysis.md#export-screens). It works from either way of working.\r
`,K='# Screenshots (for contributors)\r\n\r\nThe pictures in these pages are made by a script, so they match the program. They live in `docs/user/screenshots/<phase>/`, one file for each screen and theme, for example `p1-home-dark.png`.\r\n\r\n## What you need\r\n\r\n- Node.js 22.\r\n- A built `dist\\unravis.exe`. Build it with `.\\build.ps1`.\r\n- The tools, once: in `tools\\screens`, run `npm ci`, then `npx playwright install chromium`.\r\n\r\n## Take the pictures\r\n\r\nFrom the main folder of the project:\r\n\r\n```\r\n.\\tools\\screens\\run-screens.ps1\r\n```\r\n\r\nThis starts the demo with a fixed clock and a set of made-up cases, in a temporary folder that is deleted afterwards. It opens every screen listed in `tools\\screens\\screens.json` and compares it with the saved picture. Any difference fails the run, and the differences are saved in `tools\\screens\\test-results`.\r\n\r\n| Option | What it does |\r\n|---|---|\r\n| `-Update` | Writes new pictures instead of comparing. |\r\n| `-Only p1-home,p1-glossary` | Runs only the screens you name. |\r\n| `-Port 8081` | Uses a fixed port instead of one Windows picks. |\r\n\r\n## Which computer makes the saved pictures\r\n\r\nPictures differ a little between computers, because of fonts and graphics drivers. The reference is the Windows runner used by the continuous check (`.github\\workflows\\ci.yml`). Use `-Update` on your own computer to look at the result, not to commit, unless the team agreed that your computer is the reference.\r\n\r\nThe first saved pictures were made on a developer computer, so until pictures from the runner are committed the continuous check only warns when a screenshot differs. To get the runner\'s own pictures: in GitHub, open Actions, then CI, then Run workflow, and tick "Regenerate screenshot baselines". The run takes new pictures on the runner and keeps them as the artifact `screenshot-baselines`. Unzip it over `docs\\user\\screenshots` in a clone and commit it. It includes `RUNNER-BASELINES.txt`; once that file is committed, a difference fails the check.\r\n\r\n## Add a screen\r\n\r\nAdd an entry to `tools\\screens\\screens.json`:\r\n\r\n- `id`: the name, starting with the phase, for example `p2-close`.\r\n- `phase`: the folder, for example `P2`.\r\n- `doc`: the page in `docs\\user` that shows it.\r\n- `narrow`: `true` to also take a narrow picture.\r\n- `steps`: what to do, in order: `goto`, `click`, `fill`, `waitFor`, `scrollTo` or `theme`. A step may add `"when": "narrow"` or `"wide"`.\r\n\r\nThen link the pictures from the page, using the id in the file name. Run the screens with `-Update`, then check the docs:\r\n\r\n```\r\nnode tools\\screens\\docs-check.mjs\r\n```\r\n\r\nThe check fails if an image link points to a missing file, if a screen is not mentioned in its page, or if a page shows a screen that is no longer captured.\r\n\r\n## The contract screens are written against\r\n\r\nLater phases write their screen steps against exactly this, so it does not change without a decision.\r\n\r\n### Demo settings\r\n\r\n`run-screens.ps1` sets these for the demo it starts. Both count only with `--demo`; without it they are ignored with a line in the log.\r\n\r\n| Variable | Value | What it does |\r\n|---|---|---|\r\n| `UNRAVIS_DEMO_NOW` | `2026-10-20T09:00:00Z` | The demo clock stands still at this time. It is written in three places that must agree: `run-screens.ps1`, the default in `capture.spec.ts`, and the plan (decision B8). A value that is not a time stops the start. |\r\n| `UNRAVIS_DEMO_CASES` | `1` | Seeds the capture-only cases below. It needs `AUDIT_CONFIG_DIR` to point at an empty folder (no real cases, no master key), and the start refuses otherwise. Plain `--demo` writes nothing. |\r\n\r\n### The seeded cases\r\n\r\nScreens click these by their title, for example `role=button[name="Open case Checkout slowdown, 17 October"]`.\r\n\r\n| Title | ID starts with | State |\r\n|---|---|---|\r\n| Checkout slowdown, 17 October | `demoopen` | Open, one import |\r\n| Night batch overrun, 10 October | `democlosed` | Closed 4 days before the demo time |\r\n| Report export timeouts, 18 October | `demofailed` | Open, one import and one file the importer refused |\r\n| Case from another folder | `demountrusted` | Shown as untrusted (the legacy check refuses it) |\r\n\r\nEach ID is the name padded with `a` to 26 characters. A case made during a capture run (a screen that presses "Create case") gets `demonew` followed by a counter: the first is `demonewaaa...`, then `demonewbaa...`. They are the same on every run and never repeat one already in the folder.\r\n\r\n### Narrow and wide\r\n\r\nThe header folds into a "More" menu below 1100 px. A step may add `"when": "narrow"` or `"when": "wide"` to run only on that side, so one screen can reach the same page both ways. The wide pictures are 1440 by 900 and the narrow ones 390 by 844.\r\n\r\n### Exit codes of `run-screens.ps1`\r\n\r\n| Code | Meaning |\r\n|---|---|\r\n| 0 | Every screen matched (or was written, with `-Update`). |\r\n| 1 | The capture failed or a picture differed. |\r\n| 2 | `dist\\unravis.exe` is missing, or the tools are not installed. |\r\n| 3 | The demo did not start. The log is printed. |\r\n| 4 | Another screenshot run is going on this computer. |\r\n\r\n### Adding a seeded state\r\n\r\nSeeded cases are made by the `steps` list in `demoSeeder.seed` (`cmd\\audit-explorer\\demo_cases.go`). To add a state, add an entry there with a fixed ID from `demoCaseID`, a title, an age before the demo time, and a `fill` function that imports or changes the case. The first eight characters of every ID must differ from the others (evidence IDs carry only those), and `TestDemoCaseIDsAreValidAndDistinguishedByTheirFirstEightCharacters` checks it. Add the ID to `demoCaseIDs`, and a line to `TestDemoCasesSeedFourCasesWithFixedIDsTitlesAndTimes`.\r\n',fe=`# Security\r
\r
## What Unravis never does\r
\r
- It never changes your SQL Server, your MongoDB or your logs. It only reads.\r
- It never sends anything over the internet by itself. It listens on your own computer only. The one time it contacts the internet is when you press Check for updates (see [Upgrade and uninstall](upgrade-and-uninstall.md)).\r
- It never shows a saved password in the screens, in its log or in a case file. On a PC set up for everyone, it does not even hold one: the key keeper does (see below).\r
\r
## How you open it\r
\r
Each start gives one one-time link. The link works once. When Unravis opens your browser itself, the link expires after 2 minutes. A link that Unravis prints in its window for you to paste (when you start with \`--no-browser\`) expires after 10 minutes. After that, your browser holds a session for this run only.\r
\r
## Keys: the PC key and a case key\r
\r
Unravis can seal what it keeps on this computer with up to two kinds of key. Both are optional. The demo never asks for either.\r
\r
- The PC key, which the screens call the master key. One key for all cases that do not have their own. You set it from the start screen ("Protect your cases with a master key") and type it once each time Unravis starts. If you choose Not now, those cases are sealed by your Windows sign-in only.\r
- A case key. One key for one case. It is separate from the PC key and from your Windows sign-in. Neither opens it.\r
\r
How they are made and kept:\r
\r
- A key is turned into a sealing key with Argon2id, a slow method built to make guessing hard. The key itself is never saved. Unravis keeps a short check code so it can tell a right key from a wrong one, and shows only the last four characters of it ("Key ending xxxx").\r
- The key is never written to the log, never shown in the screens, never put in an address and never saved in a case file.\r
- A wrong key changes nothing. After five wrong tries in a row, Unravis makes you wait: one second, then doubling, up to one minute.\r
- Master Lock in the top bar locks the PC key and every case key at once.\r
- There is no recovery. If you lose a case key, no one, including us, can open that case. Erase is the only way to free it. Use a key you can keep, for example in your password manager.\r
- Resetting a damaged PC key never touches a case that has its own key.\r
\r
## Erase\r
\r
Erase removes a case from this computer. You type the short case code first. It writes an Erasing mark before it removes anything, so a stop part way is finished on the next start. It leaves only a line with the case code and the time. A case file you saved is not touched. See "Erase a case" in the Cases page.\r
\r
## Saved passwords on a PC set up for everyone (the key keeper)\r
\r
When the installer sets Unravis up for everyone on a PC, it makes the shared data folder \`C:\\ProgramData\\Unravis\` and the Unravis Key Keeper service. From then on Unravis keeps its cases there, sealed for this PC, and it no longer keeps saved passwords itself.\r
\r
- The key keeper holds every saved SQL Server password, every saved MongoDB link that has a password, and every Blackbox read key. It keeps them in its own folder (\`C:\\ProgramData\\Unravis\\keeper\`), sealed with Windows data protection for the keeper's own service account, so no person's sign-in opens them.\r
- Unravis asks the keeper to do a fixed job by name: test this saved connection, run the fixed SQL Server or MongoDB checks with it, read these Blackbox files with this read key, save or forget this connection. The keeper does the job and sends back only the result. There is no request that hands a password or a read key back, and the keeper refuses any request it does not know.\r
- Unravis and the keeper talk over a pipe on this PC only (\`\\\\.\\pipe\\UnravisKeeper\`). The pipe refuses callers from the network. Before it sends anything, Unravis checks that the pipe is served from the services session and is owned by a Windows service account, so a program in someone's desktop session cannot pretend to be the keeper and collect a password you type.\r
- The keeper writes one line per request to its own log (\`audit.log\` in its folder): when, which Windows account, which job, which saved connection by its id, and how it went. It never writes a password, a key, an address or a query window. The log is rotated at 4 MB, and eight older files are kept. The same refusal repeated many times is written once with a count, and each line is written before the answer goes back, so a flood of requests from one account cannot push older lines out of the log quickly. A request the keeper will not run (one it does not understand, from another version, or for a job it does not know) counts against a limit of 30 a minute per account, and only a short, cleaned form of its name (letters, digits, dot, underscore and dash, at most 32 characters, with a \`~\` added when it was cut) is written.\r
- The keeper limits how often one Windows account may ask for each kind of job in a minute (for example 30 connections to a server a minute). Past the limit it answers "too many requests" and does nothing. One Windows session may hold only half of the keeper's open connections, so one person cannot keep everyone else waiting. Connections whose session cannot be told share one such half between them.\r
- A password you type for this session only is still allowed. It is used for that one check and is not saved.\r
- In this setup, Show and Copy for a saved Blackbox read key are refused with a plain message: the key stays in the keeper.\r
- If the keeper is not running, the connections page says so and nothing that needs a saved password runs. Connections that need no password still work.\r
- Saved connections you made before the PC was set up for everyone stay in your own folder until you move them. The connections page offers the move. Your old files are removed only when you press Remove old copies and confirm, and only once every one of them is in the keeper under its own name. If someone else on this PC had already saved the same connection, the keeper keeps theirs, and the page asks you to confirm again before your old copies go.\r
- The keeper opens an audit source from a saved MongoDB link itself, with the password it stores, and reads the audit log for it. The Unravis process never holds a keeper-held MongoDB password, not even for a moment: Unravis asks for named, read-only reads (the analysis, a page of requests, one record, one payload, the timeline) and gets back only the answers. There is no request that runs a query you write, and none that returns a password. Before an answer or an error goes back, the keeper checks it for the stored password (as typed and as written inside JSON): an answer that holds it is refused, and an error has it cut out.\r
- Audit reads have their own limit of 600 a minute per Windows account, apart from the other jobs. The keeper's log writes them once a minute per account, job and outcome with a count, so a busy analysis cannot flood the log.\r
- The keeper keeps the audit sources of each running Unravis apart: a running Unravis can read only the sources it opened (two browser tabs of the same Unravis are one Unravis and share them). Sources of an Unravis that has not asked for anything in 8 hours are closed. At most 16 running Unravis programs, and 4 per Windows account, may have sources open in the keeper at once. Audit reads share a part of the keeper's connections (6 at once, 4 running), so a burst of them cannot lock out the other jobs; a read the keeper has no room for is answered "busy, try again". One read stops after at most 90 seconds, and the keeper does not send an answer larger than 64 MiB.\r
- The keeper runs the MongoDB checks, and opens audit sources, only for a saved link that signs in with a user name and password and names no certificate or key file on this PC. Any other saved MongoDB link is refused by the keeper with a plain message, so the keeper's service account is never used to reach MongoDB in a way a person could not.\r
- Cases you made before the PC was set up for everyone stay in your own Windows folder, sealed for you alone. Unravis in this setup does not open, show or move them, and it does not delete them. The start screen says how many there are.\r
\r
### What the key keeper does not protect against\r
\r
- An administrator of this PC, or a program running as SYSTEM, can stop or replace the keeper, run code as its service account, and read every saved password. The keeper protects saved passwords from the other people who sign in to this PC, not from its administrators.\r
- Cases, the host map, the erased lines and the ledger in the shared folder are sealed for this PC, so any account on the PC could unseal them. The folder's permissions are the only thing that keeps one person out of another's cases. For that reason Unravis uses the shared folder only when it is the very folder the installer made:\r
  - The installer writes a record under \`HKEY_LOCAL_MACHINE\\SOFTWARE\\Unravis\` that names the folder and the file IDs of the folder and of its \`cases\` folder. Only an administrator can write there, and Unravis and the keeper only read it. A folder that was moved or renamed into place, copied, or replaced has another ID or another path, and is not used. With no record there is no shared mode. The record itself must be owned by SYSTEM, Administrators or TrustedInstaller and must not let anyone else change it.\r
  - The folder and its \`cases\` folder must also be owned by SYSTEM, Administrators or TrustedInstaller, must not be a link to somewhere else, and must not let anyone else change their permissions, take them over, or delete or replace what is in them.\r
  - When any of this fails, Unravis keeps your files in your own folder instead and writes why in its log, and the keeper does not start.\r
- The installer must set exactly these permissions, or shared mode never turns on. On \`C:\\ProgramData\\Unravis\`: owner Administrators; SYSTEM, Administrators and the keeper's service account full control; Users read and add files and folders on the folder itself; Users modify only through entries that apply to what is inside (inherit only). On \`cases\`: the same, with inheritance from the parent turned off (a protected list), so the parent's modify entry does not arrive as a plain entry on \`cases\` itself, which the check refuses because modify includes delete. On \`keeper\`: SYSTEM, Administrators and the keeper's service account only.\r
- Every person on the PC can use, save again and forget any saved connection in the keeper. The keeper keeps the passwords from being read, not from being used.\r
- Files in the shared folder are locked machine-wide while one person writes them, so two people signed in at once take turns. Someone who takes that lock first, or makes it before Unravis does, can stop the others writing shared files (the host map, the ledger, adding or erasing a case) while they are signed in. They cannot read or change anything through it.\r
`,pe=`# Sources\r
\r
A source is where Unravis reads from: an audit log, a SQL Server or a MongoDB. Unravis only reads from a source.\r
\r
## The Sources drawer\r
\r
Press the sources button in the header to see the sources Unravis knows and to choose which ones to include in the analysis.\r
\r
![The Sources drawer, dark](screenshots/P1/p1-connections-dark.png)\r
![The Sources drawer, light](screenshots/P1/p1-connections-light.png)\r
\r
The demo uses sample sources and does not let you add real ones, so the pictures above show the drawer with adding blocked. Pictures with a connected source arrive with the connection phase.\r
\r
## SQL Server, MongoDB, grants and the profiler guide\r
\r
A case can read a SQL Server and a MongoDB server on top of its audit logs. They answer two questions the logs cannot: did the database slow down, and when did it start. Unravis only reads. It never changes a server, never runs a grant for you and never switches a profiler on.\r
\r
The case page has a section called SQL Server and MongoDB. It holds six tabs: Connections, Step 7: SQL Server, Step 8: MongoDB, Add data, Add another day and Combinations. The guided steps show the same Step 7 and Step 8 panels.\r
\r
Pictures for this page come from the screenshot tool (see \`screenshots.md\`). Each screen below has a dark and a light picture. They were made on a developer PC and are replaced when the reference runner regenerates the baselines.\r
\r
### Saved connections\r
\r
The connections list shows every saved connection, SQL Server and MongoDB, in one list. Each row shows its name, whether a password is saved, and the result of its last test.\r
\r
![The connections list, dark](screenshots/P7/p7-connections-dark.png)\r
![The connections list, light](screenshots/P7/p7-connections-light.png)\r
\r
- A test says only Works, Failed or Not tested. These three words are for a connection test and nothing else. A source is never "Works". It is Ready, Partly ready, Cannot test yet or Failed.\r
- The last test result is kept only while the page is open. It is not saved in the case.\r
- A saved password is never shown and never sent back to the screen. A row only says that one is saved. Passwords are sealed on this computer with Windows data protection, and only when you tick Remember password.\r
- Forget asks once. It removes the saved connection and its password. It does not touch the server.\r
- You can save up to 20 SQL Server connections. At 20 the add button is disabled and the page says so. If a 21st is sent anyway, the server refuses it (409) and nothing is removed.\r
- A wrong password shows Failed with the server's own message. It never shows the password.\r
- The demo has a sample SQL Server and a sample MongoDB already saved, and it keeps what you add only while it runs. It never opens a real connection and never keeps a password. Type the database name AuditDb to read a day that works, NoGrantDb to see the grant script, EmptyDb to see Nothing read, and for MongoDB ProfilerOffDb to see the profiler guide.\r
\r
### Saved connections on a PC set up for everyone (the key keeper)\r
\r
When Unravis is installed for everyone on a PC, saved passwords live in the Unravis Key Keeper service, not in Unravis (see "Saved passwords on a PC set up for everyone" in the Security page). The connections list looks the same, with three differences.\r
\r
A row whose password is saved says "Password held by the key keeper". Test, the fixed checks and Add data still work with it: the keeper runs them and sends back only the result. The note under the list says where passwords are kept.\r
\r
![A saved connection held by the key keeper, dark](screenshots/P14/p14-connection-saved-dark.png)\r
![A saved connection held by the key keeper, light](screenshots/P14/p14-connection-saved-light.png)\r
\r
When the keeper service is not running, the page says so above the list and offers Check again. Nothing that needs a saved password runs until it is back. Connections that need no password still work. The page does not mistake this for the demo.\r
\r
![The key keeper is not running, dark](screenshots/P14/p14-keeper-stopped-dark.png)\r
![The key keeper is not running, light](screenshots/P14/p14-keeper-stopped-light.png)\r
\r
Saved connections you made before the PC was set up for everyone are still in your own folder. The page counts them and offers Move them, or Not now. After the move, the page offers Remove old copies, which asks once. The old files are removed only then, and only once every one of them is in the keeper. Where this page is the whole screen, Move them is its main button and + SQL Server is outlined while the card shows; on case home, which has its own main button, both are outlined.\r
\r
![Move saved connections to the key keeper, dark](screenshots/P14/p14-migrate-dark.png)\r
![Move saved connections to the key keeper, light](screenshots/P14/p14-migrate-light.png)\r
\r
The demo shows these three states only for the pictures. It never runs a keeper and never keeps a password.\r
\r
#### Audit sources from a saved MongoDB link\r
\r
On a PC set up for everyone, an audit source added from a saved MongoDB link is opened by the key keeper, with the password it stores. Unravis never receives that password. The keeper reads the audit log for it and sends back only the answers. A source whose password you typed is opened by your own Unravis, as before, and the keeper never sees it.\r
\r
In the Audit sources menu, a source the keeper opened says "Password held by the key keeper". This is a sticker, not a button. Nothing asks for a password.\r
\r
![An audit source opened by the key keeper, dark](screenshots/P14b/p14b-source-held-dark.png)\r
![An audit source opened by the key keeper, light](screenshots/P14b/p14b-source-held-light.png)\r
\r
When the keeper is not running, those sources stay in the menu, their sticker says the password cannot be used until the keeper runs again, and a note above the list says they cannot be read. Sources whose password you typed still work. Remove still works: it only takes the source off the list.\r
\r
![Audit sources while the key keeper is not running, dark](screenshots/P14b/p14b-source-stopped-dark.png)\r
![Audit sources while the key keeper is not running, light](screenshots/P14b/p14b-source-stopped-light.png)\r
\r
- A read the keeper refuses because it is not running, or because it closed the source, makes the page read the source list again. A source the keeper closed leaves the list the way Remove takes it off, and you add it again from the saved link.\r
- The keeper closes the sources of an Unravis that has not asked for anything in 8 hours, and when it restarts. It keeps sources open for at most 16 running Unravis programs at once, 4 per Windows account. Past that it answers that it is full (\`keeper-full\`), and you close another Unravis or wait.\r
- One analysis reads either sources the keeper opened or sources your Unravis opened, not both (\`keeper-mixed\`). Only the timeline across sources takes both at once. The limit of 8 sources counts both kinds together.\r
- The keeper opens sources with the normal limits. A broad scan, which \`--allow-broad-scans\` allows in your own Unravis, is refused for a source the keeper opened. A source opened from a saved link always uses these default limits. When a message says to raise a limit (for example \`--allow-broad-scans\` or \`--docs-examined-budget\`), that flag changes nothing for such a source. To scan wider, type the password so your own Unravis opens the source.\r
- The keeper reads for every Unravis on the PC, so it keeps a share of its connections for other work and answers a read it has no room for with "busy, try again" (\`keeper-audit-busy\`, 503). Wait a few seconds and ask again. A read the keeper runs for a source stops after at most 90 seconds, or sooner when the page stops waiting, and an answer larger than 64 MiB is refused as over a limit.\r
- Error codes: \`keeper-stopped\` (503, the keeper is not running), \`keeper-source-gone\` (404, the keeper closed the source), \`keeper-mixed\` (400), \`keeper-full\` (503), \`keeper-audit-busy\` (503, the keeper has no room for another read right now).\r
\r
The demo shows these two states only for the pictures (\`#keeper=saved\` and \`#keeper=stopped\`). It never runs a keeper.\r
\r
This is the form for one SQL Server connection. Save, Test and Cancel are all outlined while the form sits on case home, because case home already has its main button. Leave the password box empty on an edit to keep the saved password.\r
\r
![The SQL Server connection form, dark](screenshots/P7/p7-sql-form-dark.png)\r
![The SQL Server connection form, light](screenshots/P7/p7-sql-form-light.png)\r
\r
This is the same form after Test: the word Works and the server version sit under the buttons, and no password is shown.\r
\r
![A SQL Server connection test that works, dark](screenshots/P7/p7-sql-test-ok-dark.png)\r
![A SQL Server connection test that works, light](screenshots/P7/p7-sql-test-ok-light.png)\r
\r
### Step 7: SQL Server\r
\r
Step 7 reads one day of a SQL Server with four fixed read-only checks. Nothing you type is ever run as a query.\r
\r
| Check | Reads | Answers |\r
|---|---|---|\r
| Server start and thread pool | server start time and thread pool waits | D5, D12, D13 |\r
| Error log | the SQL Server error log | D5 |\r
| Query Store | hourly query timings, regressions and plan changes | D3, D14, D15 |\r
| Agent jobs and backups | jobs and backups that overlap the incident | D11 |\r
\r
A check that cannot run says Cannot test yet and shows the permission it is missing with the lines to fix it. The question is then left unanswered. It is never a No and never a pass. Only a check that ran, and is Ready, gives an answer. The whole read is Ready when every check ran, Partly ready when some did, Cannot test yet when none could and Failed when the server could not be read.\r
\r
This is Step 7. With no SQL Server read yet it says Cannot test yet, shows the grant script and offers to add one.\r
\r
![Step 7, SQL Server, dark](screenshots/P7/p7-step-7-dark.png)\r
![Step 7, SQL Server, light](screenshots/P7/p7-step-7-light.png)\r
\r
### Grants for the owner to run\r
\r
Unravis never runs a grant. The panel writes the script, you copy it, and the owner of the server runs it in SSMS. Run it once per login.\r
\r
SQL Server 2019 and earlier name some permissions differently from 2022 and later, so each has its own script. Do not run the 2022 script on 2019.\r
\r
| Check | SQL Server 2022 or later | SQL Server 2019 or earlier |\r
|---|---|---|\r
| Server start and thread pool | VIEW SERVER PERFORMANCE STATE | VIEW SERVER STATE |\r
| Error log | VIEW ANY ERROR LOG | no narrower grant exists; the check stays Cannot test yet unless the owner chooses to allow securityadmin |\r
| Query Store | VIEW DATABASE PERFORMANCE STATE | VIEW DATABASE STATE |\r
| Agent jobs and backups | read on msdb (SQLAgentReaderRole) | read on msdb (SQLAgentReaderRole) |\r
\r
These pictures show the same panel with each version chosen (2019 and 2022). Type the login and the audit database to fill them into the script. The version is read from the server on a read, and 2022 is assumed when it is unknown.\r
\r
![The grant script for SQL Server 2019 or earlier, dark](screenshots/P7/p7-grant-script-2019-dark.png)\r
![The grant script for SQL Server 2019 or earlier, light](screenshots/P7/p7-grant-script-2019-light.png)\r
![The grant script for SQL Server 2022 or later, dark](screenshots/P7/p7-grant-script-2022-dark.png)\r
![The grant script for SQL Server 2022 or later, light](screenshots/P7/p7-grant-script-2022-light.png)\r
\r
### Step 8: MongoDB\r
\r
Step 8 reads the MongoDB server itself, not the audit logs. The user needs the clusterMonitor role on admin and the read role on the audit database. The panel shows the script for the owner to run in mongosh.\r
\r
Step 8 currently checks the slow operations the profiler recorded (answer M6) and when they began. Other Mongo questions need a time series and are not read yet. The screen says so rather than guessing.\r
\r
This is Step 8. With no MongoDB server read it says Cannot test yet and shows the roles script and the profiler guide.\r
\r
![Step 8, MongoDB, dark](screenshots/P7/p7-step-8-dark.png)\r
![Step 8, MongoDB, light](screenshots/P7/p7-step-8-light.png)\r
\r
### The profiler guide\r
\r
The MongoDB profiler is how slow operations are seen. It is off by default. The Switched on? guide explains why it matters, what it costs, and the steps. It has three scripts: Check that it is on (read-only), Switch it on and Switch it off again. The switch-on script replaces the profiler record collection, so older records are lost. Pick a quiet moment, run it as a MongoDB administrator, then read the database again. Unravis cannot switch it on and never tries. The guide appears on Step 8 and carries the database name you gave.\r
\r
### Add data\r
\r
Screen: Add data tab. It has five cards: SQL Server, MongoDB, Blackbox, Files and Another day. **Add a Blackbox** opens the Blackbox page, the same one the header opens, where a Blackbox is added and then used in this case. It is off in a closed case. Each card says how many saved connections are ready and how many reads the case already holds. Adding reads the new data on its own and does not touch what is there. A step that ran before the new data arrived is marked Out of date, rerun. It does not change by itself.\r
\r
### Add another day\r
\r
Add another day reads more hours of a SQL Server the case already holds, for example the day after a fix. The times start as the same hours on the next day and are UTC. They are read as typed and strictly: a time that cannot be read is refused, not guessed.\r
\r
![Add another day, dark](screenshots/P7/p7-add-day-dark.png)\r
![Add another day, light](screenshots/P7/p7-add-day-light.png)\r
\r
- Only SQL Server can be read by day. MongoDB is listed as Not by day, because the profiler keeps only the latest entries and an older day cannot be read.\r
- A day is added next to the days already held. Nothing is replaced.\r
- Reading exactly the same hours again is a re-check and is allowed.\r
- Hours that overlap a day already held for the same server and database, without being the same, are refused. Nothing is read.\r
- With no SQL Server day in the case, the screen says so and asks you to read one first.\r
\r
### When nothing could be read\r
\r
This screen appears when the server answered but every check came back Cannot test yet and none has a grant to fix. The case is not broken. The hours may be wrong, or that hour is before the system was used. The screen lists what the case can and cannot answer and offers Add another day or Add data. It never says a check passed. In the capture demo it is the case Report export timeouts, 18 October, which holds one SQL Server day with nothing read.\r
\r
![Nothing could be read, dark](screenshots/P7/p7-nothing-read-dark.png)\r
![Nothing could be read, light](screenshots/P7/p7-nothing-read-light.png)\r
\r
### What each combination can answer\r
\r
The Combinations tab shows what each set of sources can answer, so you know what to add next.\r
\r
| What the case holds | Steps it can answer | Steps that say Cannot test yet |\r
|---|---|---|\r
| Mongo audit logs only | 0, 1 (partly), 2, 3 (A, C and D), 4, 6, 10 (from what exists) | 3 (O needs IIS), 5, 7, 8, 9 |\r
| + IIS logs | adds O in step 3, and queueing in 4 | 5, 7, 8, 9 |\r
| + SQL Server | adds 7, and the jobs and error log lanes of 5 | 8, 9 |\r
| + MongoDB server (profiler, clusterMonitor) | adds 8: slow operations first, connections with clusterMonitor | 9, the host lanes of 5 |\r
| + Blackbox (web server), and one on the SQL host | adds 9 and the host lane of 5, and SQL host counters | none from missing sources |\r
\r
The last row of the tab says what the case holds today. Application logs add lines to Step 5 only. A Query Store export is for SQL you cannot connect to. Neither is needed to start.\r
`,me=`# Troubleshooting\r
\r
## The browser did not open\r
\r
Start Unravis with \`--no-browser\`, then paste the one-time link from its window into your browser.\r
\r
## The link says it has expired or was already used\r
\r
The link works once. A link that Unravis opens in your browser for you expires after 2 minutes. A link printed in the window for you to paste (started with \`--no-browser\`) expires after 10 minutes. Restart Unravis to get a new one.\r
\r
## Unravis will not start on my port\r
\r
Leave out \`--port\` and Windows picks a free port for you.\r
\r
## Health checks say they need a real server\r
\r
In the demo, the health checks are off, because the demo has no SQL Server or MongoDB. Connect a real source to use them.\r
\r
## Which version do I have?\r
\r
Run \`unravis.exe --version\`.\r
\r
## Free the space says to set a master key\r
\r
Free and Restore as a new case work only with the master key of this computer. If no master key exists, the drawer says so and nothing is changed. Press "Set a master key", then come back. If the master key is locked, unlock it first.\r
\r
## The archive folder is refused\r
\r
Archive refuses a folder inside Unravis's own folders: the cases folder, the folder that holds it, and the folder with the master key file, saved connections and history. Choose a folder somewhere else, such as a documents or backup folder on a local drive.\r
\r
## "This file can only be opened with the key it was made with"\r
\r
The case file was sealed with a key that is not the one in use now. On the start screen use Restore from a case file, and type the master key of the computer it was saved on, or the passphrase typed when the file was made.\r
\r
## Files left behind after an archive or restore\r
\r
Case files that an archive or a restore finished but did not commit are called left-over files. They appear under "Case files left over" in the health checks of the case. Unravis lists the name, the folder, when it was made and its size. It never deletes, moves or adopts one. Keep it, move it, or delete it yourself in File Explorer.\r
\r
A temporary folder from a check (\`.verify\`) that could not be removed is reported as "Cleanup pending" and is removed the next time Unravis starts.\r
\r
If an archive was stopped by a crash or a power cut, start Unravis again. The case comes back as it was; a half-written \`.part\` file is not a case file and can be deleted.\r
`,he=`# Upgrade and uninstall\r
\r
The same \`unravis-setup.exe\` upgrades, repairs and removes Unravis. Run a newer setup over an installed Unravis and it upgrades it. Run the same version again and it repairs it. To remove Unravis, use **Installed apps** in Windows Settings, or run \`unravis-setup.exe --uninstall\`. For a first install, see [Install](install.md).\r
\r
> NOT RUN: needs a clean VM. Upgrade, repair and uninstall were tested with fakes for the registry, services, firewall and the Windows approval prompt. They have not yet run against a real install.\r
\r
## Checking for a newer version\r
\r
Unravis never checks for a new version by itself. You check when you want to: open **Glossary** in the header (the help panel) and press **Check for updates** at the foot. Unravis asks GitHub for the latest release of \`abhinavjp/unravis\`. The request goes to api.github.com. It sends nothing about you or your cases.\r
\r
There are four answers:\r
\r
- **You are on the latest version (v1.2.0).** Nothing to do.\r
- **Version 1.3.0 is available. You have v1.2.0.** A link, **Open the release page**, opens the release on GitHub.\r
- **Could not check for updates. Check your connection and try again later.** This also shows when you are offline or GitHub has rate-limited the check.\r
- **This is a development build, so there is nothing to compare.** Development builds do not check for updates.\r
\r
Unravis makes no background calls. The check runs only when you press the button.\r
\r
Installing a newer version is still your step. Download the installer from the release page, check it against \`SHA256SUMS.txt\` from the same release, and run it. It upgrades in place.\r
\r
## Upgrade\r
\r
The heading names both versions, for example **Upgrade Unravis 0.9.2 to 1.0.0**. The checks are the same as for an install. There is no Options step: the upgrade keeps the choices of the first install (scope, folder, Blackbox, firewall rule), which setup saved in \`setup-state.json\` in the program folder. If Unravis is open, setup closes it and opens the new version when it is done.\r
\r
![Upgrade Unravis 0.9.2 to 1.0.0, dark](screenshots/P11/p11-upgrade-dark.png)\r
![Upgrade Unravis 0.9.2 to 1.0.0, light](screenshots/P11/p11-upgrade-light.png)\r
\r
What an upgrade does:\r
\r
- Stops the key keeper and Blackbox services, if they are installed, and starts them again with the new programs.\r
- Copies the new program files. The old ones go into a backup folder, \`.setup-backup-<time>\` in the program folder, until the new Unravis has started once. Then the backup is removed. If any step fails, the old files come back from it.\r
- Replaces the Blackbox program and keeps its settings, with a copy in \`blackbox.json.bak\` next to them.\r
\r
What an upgrade never touches:\r
\r
- Your cases and saved connections, in your own folder or in the shared folder \`C:\\ProgramData\\Unravis\`.\r
- The sealed files: saved passwords and the Blackbox read key. They are sealed with Windows data protection for one account. Setup never changes which account the keeper service runs as, because a different account could never open them again.\r
- The Blackbox recordings.\r
\r
**Going back to an older version** is refused unless you run setup with \`--allow-downgrade\`. There is no button for it. An older Unravis may not read cases saved by a newer one.\r
\r
## Repair\r
\r
Repair puts back the program files, shortcuts, Installed apps entry, services and folder permissions, as the first install made them. It touches no data. Use it when Unravis does not start or a shortcut is gone. Run the same version of setup again, or run \`unravis-setup.exe --repair\`.\r
\r
![Repair Unravis 1.0.0, dark](screenshots/P11/p11-repair-dark.png)\r
![Repair Unravis 1.0.0, light](screenshots/P11/p11-repair-light.png)\r
\r
## Uninstall\r
\r
Always removed: the program files, the shortcuts and the Installed apps entry. For an install for everyone on this PC, the key keeper service too. With a Blackbox, the Blackbox service and its firewall rule too.\r
\r
Your data stays unless you tick it:\r
\r
- **Also delete my cases and saved connections** (just for me), or **Also delete the cases and saved connections of everyone on this PC** (for everyone). Ticking it asks you to type \`DELETE\` to confirm. **Remove** stays off until you do. The screen shows the folder and its size.\r
- **Also delete the Blackbox recordings.** Ticking it asks you to type the Blackbox's name exactly.\r
\r
The approval screen repeats what you ticked before Windows asks. Deleted data is deleted for good, not moved to the Recycle Bin. This cannot be undone. Copy anything you want to keep first.\r
\r
![Remove Unravis, with the shared data ticked, dark](screenshots/P11/p11-uninstall-dark.png)\r
![Remove Unravis, with the shared data ticked, light](screenshots/P11/p11-uninstall-light.png)\r
\r
What is never removed:\r
\r
- The shared data of everyone on this PC, unless you tick it and type \`DELETE\`. Without that, the shared folder and its record in the registry stay, so a later install finds every case as it was.\r
- Cases from before the install for everyone, in your own folder \`%LOCALAPPDATA%\\APIAuditExplorer\`. An uninstall for everyone never removes them.\r
- Files you added to the program folder yourself.\r
\r
The Done screen lists what was kept and where. If something could not be removed, it is listed under **Left behind** with its path, and setup exits with code 5.\r
\r
## Silent upgrade, repair and uninstall\r
\r
\`\`\`\r
unravis-setup.exe --silent --upgrade --config setup.json\r
unravis-setup.exe --silent --repair\r
unravis-setup.exe --silent --uninstall [--config setup.json]\r
\`\`\`\r
\r
An upgrade or repair always keeps the choices of the first install, whatever the config file says, so its config file can be just \`{}\`. A silent upgrade or repair never closes a running Unravis: close it first.\r
\r
A silent uninstall without a config file keeps all data. To delete data, the config file must say so with an explicit \`true\`:\r
\r
\`\`\`json\r
{ "uninstall": { "deleteCases": true, "deleteRecordings": false } }\r
\`\`\`\r
\r
A key that is left out or set to \`null\` counts as \`false\` and keeps that data, and \`"uninstall": null\` is the same as no \`uninstall\` object. Write \`true\` and \`false\` without quotes: anything else, for example \`"true"\` in quotes, stops setup with a sentence that names the key. An \`uninstall\` object in a config file for an install is refused. The exit codes and the log are the same as for an install (see [Install](install.md)).\r
`;function ge(e){return e.trim().toLowerCase().replace(/[^\p{L}\p{N} -]/gu,``).replace(/ /g,`-`)}var q=e=>e===void 0||/\s/.test(e),_e=e=>e!==void 0&&/[\p{L}\p{N}]/u.test(e);function J(e,t){if(!t)return;let n=e[e.length-1];n&&n.t===`text`?n.text+=t:e.push({t:`text`,text:t})}function Y(e,t){let n=0;for(;e[t+n]==="`";)n++;return n}function X(e,t){let n=Y(e,t),r=t+n;for(;r<e.length;){if(e[r]!=="`"){r++;continue}let t=Y(e,r);if(t===n)return r+t;r+=t}return-1}function ve(e,t){let n=0;for(let r=t;r<e.length;r++){let t=e[r];if(t==="`"){let t=X(e,r);t>0&&(r=t-1)}else if(t===`[`)n++;else if(t===`]`&&(n--,n===0))return r}return-1}function ye(e,t){if(e[t]!==`(`)return null;let n=0;for(let r=t;r<e.length;r++){let i=e[r];if(i===`(`)n++;else if(i===`)`&&(n--,n===0))return{dest:e.slice(t+1,r).trim().split(/\s+/)[0]??``,end:r+1}}return null}function be(e,t,n){for(let r=t;r<e.length;r++){let i=e[r];if(i==="`"){let t=X(e,r);t>0&&(r=t-1)}else if(n===`**`){if(e.startsWith(`**`,r)&&r>t&&!q(e[r-1]))return r}else if(n===`*`&&i===`*`&&e[r+1]===`*`)r++;else if(i===n&&!(r===t||q(e[r-1]))&&!_e(e[r+1]))return r}return-1}function Z(e){let t=[],n=0;for(;n<e.length;){let r=e[n];if(r==="`"){let r=Y(e,n),i=X(e,n);i<0?(J(t,e.slice(n,n+r)),n+=r):(t.push({t:`code`,text:e.slice(n+r,i-r)}),n=i)}else if(r===`!`&&e[n+1]===`[`){let r=ve(e,n+1),i=r>0?ye(e,r+1):null;r>0&&i?(t.push({t:`image`,alt:e.slice(n+2,r),src:i.dest}),n=i.end):(J(t,`!`),n++)}else if(r===`[`){let r=ve(e,n),i=r>0?ye(e,r+1):null;r>0&&i?(t.push({t:`link`,href:i.dest,children:Z(e.slice(n+1,r))}),n=i.end):(J(t,`[`),n++)}else if(r===`*`&&e[n+1]===`*`){let r=q(e[n+2])?-1:be(e,n+2,`**`);r>0?(t.push({t:`strong`,children:Z(e.slice(n+2,r))}),n=r+2):(J(t,`**`),n+=2)}else{if((r===`*`||r===`_`)&&!_e(e[n-1])&&!q(e[n+1])&&e[n+1]!==r){let i=be(e,n+1,r);if(i>0){t.push({t:`em`,children:Z(e.slice(n+1,i))}),n=i+1;continue}}J(t,r),n++}}return t}function xe(e){return e.map(e=>{switch(e.t){case`text`:case`code`:return e.text;case`image`:return e.alt;default:return xe(e.children)}}).join(``)}var Se=/^\s{0,3}(`{3,})\s*([^`\s]*)[^`]*$/,Ce=/^\s{0,3}(#{1,4})\s+(.*?)\s*$/,we=/^\s{0,3}>/,Te=/^(\s*)([-*]|\d+\.)(?:\s+(.*))?$/,Ee=/^:?-+:?$/,Q=e=>e.length-e.trimStart().length,De=e=>/\d/.test(e);function Oe(e){let t=e.trim();t.startsWith(`|`)&&(t=t.slice(1)),t.endsWith(`|`)&&!t.endsWith(`\\|`)&&(t=t.slice(0,-1));let n=[],r=``;for(let e=0;e<t.length;e++)t[e]===`\\`&&t[e+1]===`|`?(r+=`|`,e++):t[e]===`|`?(n.push(r),r=``):r+=t[e];return n.push(r),n.map(e=>e.trim())}function ke(e,t){let n=e[t],r=e[t+1];if(n===void 0||r===void 0||!n.includes(`|`)||!r.includes(`-`))return!1;let i=Oe(r);return i.every(e=>Ee.test(e))?i.length===Oe(n).length:!1}var Ae=e=>Te.test(e);function je(e,t){let n=e[t];return Se.test(n)||Ce.test(n)||we.test(n)||Ae(n)||ke(e,t)}function Me(e,t,n){let r=Te.exec(e[t]),i=r[1].length,a=De(r[2]),o=[],s=t;for(;s<e.length;){let t=Te.exec(e[s]);if(!t)break;let r=Q(e[s]);if(r<i||r>=i+2||De(t[2])!==a)break;let c=r+t[2].length+1,l=[],u=s+1;for(;u<e.length;){let t=e[u];if(t.trim()===``){let t=u;for(;t<e.length&&e[t].trim()===``;)t++;if(t<e.length&&Q(e[t])>=i+2){for(;u<t;u++)l.push(``);continue}break}if(Q(t)>=i+2)l.push(t),u++;else{if(Ae(t)||l.length>0&&l[l.length-1].trim()===``||je(e,u))break;l.push(t),u++}}let d=l.map(e=>e.slice(Math.min(Q(e),c))),f=0;for(;f<d.length&&d[f].trim()!==``&&!je(d,f);)f++;let p=[(t[3]??``).trim(),...d.slice(0,f).map(e=>e.trim())].filter(e=>e!==``).join(` `);o.push({children:Z(p),sub:Ne(d.slice(f),n)});let m=u;for(;m<e.length&&e[m].trim()===``;)m++;let h=m<e.length?Te.exec(e[m]):null,g=h?Q(e[m]):-1,_=h!==null&&g>=i&&g<i+2&&De(h[2])===a;if(s=_?m:u,!_)break}return{block:{t:`list`,ordered:a,items:o},next:s}}function Ne(e,t){let n=[],r=0;for(;r<e.length;){let i=e[r];if(i.trim()===``){r++;continue}let a=Se.exec(i);if(a){let t=RegExp("^\\s{0,3}`{"+a[1].length+`,}\\s*$`),i=[],o=r+1;for(;o<e.length&&!t.test(e[o]);)i.push(e[o]),o++;n.push({t:`code`,lang:a[2],text:i.join(`
`)}),r=o<e.length?o+1:o;continue}let o=Ce.exec(i);if(o){let e=Z(o[2]),i=ge(xe(e)),a=i;if(t.has(a)){let e=1;for(;t.has(i+`-`+e);)e++;a=i+`-`+e}t.set(a,0),n.push({t:`heading`,level:o[1].length,id:a,children:e}),r++;continue}if(we.test(i)){let i=[],a=r;for(;a<e.length&&we.test(e[a]);)i.push(e[a].replace(/^\s{0,3}> ?/,``)),a++;n.push({t:`quote`,blocks:Ne(i,t)}),r=a;continue}if(ke(e,r)){let t=Oe(e[r]),i=[],a=r+2;for(;a<e.length&&e[a].trim()!==``&&e[a].includes(`|`);){let n=Oe(e[a]);for(;n.length<t.length;)n.push(``);i.push(n.slice(0,t.length).map(Z)),a++}n.push({t:`table`,head:t.map(Z),rows:i}),r=a;continue}if(Ae(i)){let{block:i,next:a}=Me(e,r,t);n.push(i),r=a;continue}let s=[i.trim()],c=r+1;for(;c<e.length&&e[c].trim()!==``&&!je(e,c);)s.push(e[c].trim()),c++;n.push({t:`p`,children:Z(s.join(` `))}),r=c}return n}function Pe(e){try{return Ne(String(e??``).replace(/\r\n?/g,`
`).split(`
`),new Map)}catch{return[]}}var Fe=[`index`,`getting-started`,`install`,`cases`,`sources`,`blackbox`,`guided-analysis`,`manual-tools`,`upgrade-and-uninstall`,`troubleshooting`,`security`,`logs`],$={kind:`none`};function Ie(e,t){if(typeof e!=`string`||e===``||/[\\\u0000- \u007f]/.test(e)||e.startsWith(`//`))return $;if(e.startsWith(`#`))return e.length>1?{kind:`anchor`,anchor:e.slice(1)}:$;if(/^https:\/\/[^/?#]/i.test(e))return{kind:`external`,href:e};if(/^[a-z][a-z0-9+.-]*:/i.test(e))return $;let n=e.indexOf(`#`),r=n<0?e:e.slice(0,n),i=n<0?``:e.slice(n+1),a=/^(?:\.\/)?([^/?]+)\.md$/.exec(r);if(!a)return $;let o=a[1];return t.includes(o)?{kind:`page`,slug:o,anchor:i}:$}function Le(e,t){for(let t of String(e??``).replace(/\r\n?/g,`
`).split(`
`)){let e=/^#[ \t]+(.*?)\s*$/.exec(t);if(e&&e[1]!==``)return e[1]}return t.charAt(0).toUpperCase()+t.slice(1)}var Re=Object.assign({"../../../../docs/user/blackbox.md":H,"../../../../docs/user/cases.md":ce,"../../../../docs/user/getting-started.md":le,"../../../../docs/user/guided-analysis.md":U,"../../../../docs/user/index.md":W,"../../../../docs/user/install.md":ue,"../../../../docs/user/logs.md":de,"../../../../docs/user/manual-tools.md":G,"../../../../docs/user/screenshots.md":K,"../../../../docs/user/security.md":fe,"../../../../docs/user/sources.md":pe,"../../../../docs/user/troubleshooting.md":me,"../../../../docs/user/upgrade-and-uninstall.md":he}),ze=new Map;for(let[e,t]of Object.entries(Re)){let n=(e.split(`/`).pop()??``).replace(/\.md$/,``);ze.set(n,t)}var Be=Fe.filter(e=>ze.has(e)).map(e=>{let t=ze.get(e);return{slug:e,title:Le(t,e),blocks:Pe(t)}}),Ve=Be.map(e=>e.slug);function He(e){return Be.find(t=>t.slug===e)}var Ue=c(`<code class="svelte-1xuxl1p"> </code>`),We=c(`<strong><!></strong>`),Ge=c(`<em><!></em>`),Ke=c(`<span class="guide-image svelte-1xuxl1p"> </span>`),qe=c(`<button type="button" class="guide-link svelte-1xuxl1p"><!></button>`),Je=c(`<a target="_blank" rel="noopener noreferrer"><!></a>`),Ye=c(`<p class="guide-image svelte-1xuxl1p"> </p>`),Xe=c(`<p class="svelte-1xuxl1p"><!></p>`),Ze=c(`<li class="svelte-1xuxl1p"><!><!></li>`),Qe=c(`<pre class="svelte-1xuxl1p"><code class="svelte-1xuxl1p"> </code></pre>`),$e=c(`<blockquote class="svelte-1xuxl1p"><!></blockquote>`),et=c(`<th scope="col" class="svelte-1xuxl1p"><!></th>`),tt=c(`<td class="svelte-1xuxl1p"><!></td>`),nt=c(`<tr></tr>`),rt=c(`<div class="guide-table svelte-1xuxl1p"><table class="svelte-1xuxl1p"><thead><tr></tr></thead><tbody></tbody></table></div>`),it=c(`<li class="svelte-1xuxl1p"><button type="button" class="guide-nav-button svelte-1xuxl1p"> </button></li>`),at=c(`<p class="guide-missing svelte-1xuxl1p"> </p>`),ot=c(`<div class="help-guide svelte-1xuxl1p"><nav><ul class="guide-nav svelte-1xuxl1p"></ul></nav> <div class="guide-body svelte-1xuxl1p" data-testid="guide-body"><!></div></div>`);function st(n,r){x(r,!0);let s=(n,a=m)=>{var c=j(),u=N(c);t(u,17,a,g,(t,n)=>{var a=j(),c=N(a),u=e=>{var t=_();y(()=>o(t,M(n).text)),l(e,t)},d=e=>{var t=Ue(),r=S(t,!0);y(()=>o(r,M(n).text)),l(e,t)},f=e=>{var t=We(),r=E(t);s(r,()=>M(n).children),P(t),l(e,t)},p=e=>{var t=Ge(),r=E(t);s(r,()=>M(n).children),P(t),l(e,t)},m=e=>{var t=Ke(),r=S(t);y(()=>o(r,`${L.guideImage??``}${M(n).alt??``}`)),l(e,t)},g=t=>{let a=v(()=>Ie(M(n).href,Ve));var o=j(),c=N(o),u=t=>{var r=qe(),i=E(r);s(i,()=>M(n).children),P(r),e(`click`,r,()=>re(M(a).slug,M(a).anchor)),l(t,r)},d=t=>{var i=qe(),o=E(i);s(o,()=>M(n).children),P(i),e(`click`,i,()=>void I(r.slug,M(a).anchor)),l(t,i)},f=e=>{var t=Je(),r=E(t);s(r,()=>M(n).children),P(t),y(()=>i(t,`href`,M(a).href)),l(e,t)},p=e=>{s(e,()=>M(n).children)};h(c,e=>{M(a).kind===`page`?e(u):M(a).kind===`anchor`?e(d,1):M(a).kind===`external`?e(f,2):e(p,-1)}),l(t,o)};h(c,e=>{M(n).t===`text`?e(u):M(n).t===`code`?e(d,1):M(n).t===`strong`?e(f,2):M(n).t===`em`?e(p,3):M(n).t===`image`?e(m,4):M(n).t===`link`&&e(g,5)}),l(t,a)}),l(n,c)},c=(e,n=m)=>{var i=j(),f=N(i);t(f,17,n,g,(e,n)=>{var i=j(),f=N(i),m=e=>{var t=j(),i=N(t);u(i,()=>`h`+Math.min(6,M(n).level+2),!1,(e,t)=>{ee(e,()=>({class:`guide-heading`,id:`help-`+r.slug+`-`+M(n).id,tabindex:`-1`}),void 0,void 0,void 0,`svelte-1xuxl1p`),s(t,()=>M(n).children)}),l(e,t)},_=e=>{var t=j(),r=N(t),i=e=>{var t=Ye(),r=S(t);y(()=>o(r,`${L.guideImage??``}${M(n).children[0].alt??``}`)),l(e,t)},a=e=>{var t=Xe(),r=E(t);s(r,()=>M(n).children),P(t),l(e,t)};h(r,e=>{M(n).children.length===1&&M(n).children[0].t===`image`?e(i):e(a,-1)}),l(e,t)},v=e=>{var r=j(),i=N(r);u(i,()=>M(n).ordered?`ol`:`ul`,!1,(e,r)=>{d(e,0,`svelte-1xuxl1p`);var i=j(),a=N(i);t(a,17,()=>M(n).items,g,(e,t)=>{var n=Ze(),r=E(n);s(r,()=>M(t).children);var i=C(r),a=e=>{c(e,()=>M(t).sub)};h(i,e=>{M(t).sub.length&&e(a)}),P(n),l(e,n)}),l(r,i)}),l(e,r)},b=e=>{var t=Qe(),r=E(t),i=S(r,!0);P(t),y(()=>o(i,M(n).text)),l(e,t)},x=e=>{var t=$e(),r=E(t);c(r,()=>M(n).blocks),P(t),l(e,t)},w=e=>{var r=rt(),i=E(r),o=E(i),c=E(o);t(c,21,()=>M(n).head,g,(e,t)=>{var n=et(),r=E(n);s(r,()=>M(t)),P(n),l(e,n)}),P(c),P(o);var u=C(o);t(u,21,()=>M(n).rows,g,(e,n)=>{var r=nt();t(r,21,()=>M(n),g,(e,t)=>{var n=tt(),r=E(n);s(r,()=>M(t)),P(n),l(e,n)}),P(r),l(e,r)}),P(u),P(i),P(r),a(r,(e,t)=>F?.(e,t),()=>M(D).get(M(n))??M(p)?.title??``),l(e,r)};h(f,e=>{M(n).t===`heading`?e(m):M(n).t===`p`?e(_,1):M(n).t===`list`?e(v,2):M(n).t===`code`?e(b,3):M(n).t===`quote`?e(x,4):M(n).t===`table`&&e(w,5)}),l(e,i)}),l(e,i)},f=T(void 0),p=v(()=>He(r.slug));function w(e){return e.map(e=>e.t===`text`||e.t===`code`?e.text:e.t===`image`?e.alt:w(e.children)).join(``)}let D=v(()=>{let e=new Map,t=M(p)?.title??``,n=r=>{for(let i of r)if(i.t===`heading`)t=w(i.children).trim()||t;else if(i.t===`table`)e.set(i,`Table: `+t);else if(i.t===`list`)for(let e of i.items)n(e.sub);else i.t===`quote`&&n(i.blocks)};return n(M(p)?.blocks??[]),e});function F(e,t){let n=t,r=()=>{e.scrollWidth>e.clientWidth?(e.setAttribute(`role`,`region`),e.setAttribute(`aria-label`,n),e.setAttribute(`tabindex`,`0`)):(e.removeAttribute(`role`),e.removeAttribute(`aria-label`),e.removeAttribute(`tabindex`))};r();let i=typeof ResizeObserver>`u`?null:new ResizeObserver(r);return i?.observe(e),e.firstElementChild&&i?.observe(e.firstElementChild),{update(e){n=e,r()},destroy(){i?.disconnect()}}}function ne(e){for(let t=e.parentElement;t;t=t.parentElement){let e=getComputedStyle(t).overflowY;if((e===`auto`||e===`scroll`)&&t.scrollHeight>t.clientHeight)return t}return null}async function I(e,t){if(await k(),!M(f))return;let n=t?document.getElementById(`help-`+e+`-`+t):null;if(n&&M(f).contains(n)){n.scrollIntoView({block:`start`}),n.focus({preventScroll:!0});return}let r=ne(M(f));r&&(r.scrollTop=0),M(f).querySelector(`.guide-heading`)?.focus({preventScroll:!0})}A(()=>{let e=r.slug,t=r.anchor;I(e,t)});function re(e,t){e===r.slug?I(r.slug,t):r.onNavigate(e,t)}var R=ot(),z=E(R),B=E(z);t(B,21,()=>Be,e=>e.slug,(t,n)=>{var a=it(),s=E(a),c=S(s,!0);P(a),y(()=>{i(s,`data-testid`,`guide-nav-`+M(n).slug),i(s,`aria-current`,M(n).slug===r.slug?`page`:void 0),o(c,M(n).title)}),e(`click`,s,()=>re(M(n).slug,``)),l(t,a)}),P(B),P(z);var ie=C(z,2),V=E(ie),ae=e=>{c(e,()=>M(p).blocks)},oe=e=>{var t=at(),n=S(t,!0);y(()=>o(n,L.guideMissing)),l(e,t)};h(V,e=>{M(p)?e(ae):e(oe,-1)}),P(ie),P(R),b(R,e=>O(f,e),()=>M(f)),y(()=>i(z,`aria-label`,L.guideContents)),l(n,R),te()}f([`click`]);var ct=c(`<div class="guide-top svelte-13zp13k"><button type="button" class="update-button svelte-13zp13k" data-testid="guide-back"> </button></div> <!>`,1),lt=c(`<li> </li>`),ut=c(`<dd class="glossary-alias svelte-13zp13k"><span class="svelte-13zp13k">Also called</span> </dd>`),dt=c(`<div tabindex="-1"><dt class="svelte-13zp13k"> </dt> <dd class="glossary-hint svelte-13zp13k"> </dd> <dd class="svelte-13zp13k"> </dd> <dd class="glossary-not svelte-13zp13k"><span class="svelte-13zp13k">What it does not mean</span> </dd> <!></div>`),ft=c(`<p class="build-line svelte-13zp13k" data-testid="build-line"> </p>`),pt=c(`<span class="log-copied svelte-13zp13k" role="status">Copied</span>`),mt=c(`<p class="log-path svelte-13zp13k" data-testid="log-folder-path"> </p> <button type="button" class="update-button svelte-13zp13k" data-testid="copy-log-folder">Copy folder path</button><!>`,1),ht=c(` <a data-testid="update-check-link" target="_blank" rel="noopener noreferrer">Open the release page</a>`,1),gt=c(`<p class="update-note svelte-13zp13k" data-testid="update-check-result"> <!></p>`),_t=c(`<section class="help-start svelte-13zp13k" aria-labelledby="help-start-title"><h3 id="help-start-title" class="svelte-13zp13k"> </h3> <p class="svelte-13zp13k"> </p> <ol class="svelte-13zp13k"></ol> <button type="button" class="update-button guide-open svelte-13zp13k" data-testid="guide-open"> </button></section> <h3 class="glossary-heading svelte-13zp13k"> </h3> <div class="glossary-search svelte-13zp13k"><label for="glossary-filter" class="svelte-13zp13k">Search terms</label> <input id="glossary-filter" type="search" autocomplete="off" spellcheck="false" aria-describedby="glossary-count" class="svelte-13zp13k"/> <span id="glossary-count" class="glossary-count svelte-13zp13k" role="status" aria-live="polite"> </span></div> <dl class="glossary-list svelte-13zp13k"></dl> <!> <section class="support-logs svelte-13zp13k" aria-labelledby="support-logs-title"><h3 id="support-logs-title" class="svelte-13zp13k">Support logs</h3> <p class="svelte-13zp13k">Unravis keeps a log of what it did and what went wrong, so a problem can be traced afterwards.</p> <!> <p class="svelte-13zp13k">Send the newest unravis.log to your contact; it contains no passwords or case data.</p></section> <div class="update-check svelte-13zp13k"><button type="button" class="update-button svelte-13zp13k" data-testid="update-check-button"> </button> <div role="status" aria-live="polite"><!></div></div>`,1);function vt(a,c){x(c,!0);let u=w(c,`focusTerm`,3,null),f=T(``),m=T(void 0),g=T(void 0),_=T(null),ee=T(void 0);async function H(){O(_,null),await k(),M(ee)?.focus()}let ce=v(()=>I(M(f))),le=v(()=>ie(M(ce).length,M(f))),U=T(``);D(()=>{F.version().then(e=>{O(U,se(e),!0)}).catch(()=>{O(U,``)})});let W=T(null),ue=T(!1);D(()=>{F.logsInfo().then(e=>{e&&e.dir&&O(W,{dir:e.dir},!0)}).catch(()=>{O(W,null)})});async function de(){if(M(W))try{await navigator.clipboard.writeText(M(W).dir),O(ue,!0)}catch{O(ue,!1)}}let G=T(!1),K=T(null),fe=T(0),pe=()=>({status:`unreachable`,current:``,latest:``,url:``});async function me(){if(!M(G)){O(G,!0),O(K,null);try{let e=await F.updateCheck(ne());O(K,e&&typeof e.status==`string`?e:pe(),!0)}catch{O(K,pe(),!0)}finally{O(G,!1)}O(fe,M(fe)+1),await k(),document.querySelector(`[data-testid=update-check-result]`)?.scrollIntoView({block:`nearest`})}}D(()=>{u()&&B(u())||M(g)?.focus()}),A(()=>{let e=u();e&&B(e)&&(O(f,``),k().then(()=>{let t=M(m)?.querySelector(`#glossary-`+CSS.escape(e));t&&(t.scrollIntoView({block:`start`}),t.focus({preventScroll:!0}))}))}),ae(a,{class:`glossary-drawer`,get title(){return V.title},titleId:`glossary-title`,closeLabel:`Close help and glossary`,focusable:`button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])`,get onClose(){return c.onClose},get element(){return M(m)},set element(e){O(m,e,!0)},children:(a,c)=>{var m=j(),x=N(m),w=t=>{var n=ct(),r=N(n),i=E(r),a=S(i,!0);P(r),st(C(r,2),{get slug(){return M(_).slug},get anchor(){return M(_).anchor},onNavigate:(e,t)=>{O(_,{slug:e,anchor:t},!0)},onBack:H}),y(()=>o(a,L.guideBack)),e(`click`,i,H),l(t,n)},T=a=>{var c=_t(),m=N(c),x=E(m),w=S(x,!0),T=C(x,2),D=S(T,!0),k=C(T,2);t(k,20,()=>re,e=>e,(e,t)=>{var n=lt(),r=S(n,!0);y(()=>o(r,t)),l(e,n)}),P(k);var A=C(k,2),te=S(A,!0);b(A,e=>O(ee,e),()=>M(ee)),P(m);var F=C(m,2),ne=S(F,!0),I=C(F,2),B=C(E(I),2);p(B),b(B,e=>O(g,e),()=>M(g));var ie=C(B,2),ae=S(ie,!0);P(I);var se=C(I,2);t(se,21,()=>M(ce),e=>e.id,(e,t)=>{var n=dt();let r;var a=E(n),s=S(a,!0),c=C(a,2),f=S(c,!0),p=C(c,2),m=S(p,!0),g=C(p,2),_=C(E(g),1,!0);P(g);var v=C(g,2),b=e=>{var n=ut(),r=C(E(n),1,!0);P(n),y(e=>o(r,e),[()=>M(t).aliases.join(`, `)]),l(e,n)};h(v,e=>{M(t).aliases?.length&&e(b)}),P(n),y(()=>{r=d(n,1,`glossary-entry svelte-13zp13k`,null,r,{"glossary-focused":M(t).id===u()}),i(n,`id`,`glossary-`+M(t).id),o(s,M(t).term),o(f,M(t).hint),o(m,M(t).definition),o(_,M(t).notMeaning)}),l(e,n)}),P(se);var H=C(se,2),pe=e=>{var t=ft(),n=S(t,!0);y(()=>o(n,M(U))),l(e,t)};h(H,e=>{M(U)&&e(pe)});var he=C(H,2),ge=C(E(he),4),q=t=>{var n=mt(),r=N(n),i=S(r,!0),a=C(r,2),s=C(a),c=e=>{var t=pt();l(e,t)};h(s,e=>{M(ue)&&e(c)}),y(()=>o(i,M(W).dir)),e(`click`,a,de),l(t,n)};h(ge,e=>{M(W)&&e(q)}),r(2),P(he);var _e=C(he,2),J=E(_e),Y=S(J,!0),X=C(J,2),ve=E(X),ye=e=>{var t=j(),n=N(t);s(n,()=>M(fe),e=>{var t=gt(),n=E(t,!0),r=C(n),a=e=>{var t=ht(),n=N(t,!0);n.nodeValue=` `;var r=C(n);y(()=>i(r,`href`,M(K).url)),l(e,t)},s=v(()=>M(K).status===`available`&&M(K).url&&R(M(K).url));h(r,e=>{M(s)&&e(a)}),P(t),y((e,r)=>{i(t,`data-state`,e),o(n,r)},[()=>oe(M(K)),()=>z(M(K))]),l(e,t)}),l(e,t)};h(ve,e=>{M(K)&&e(ye)}),P(X),P(_e),y(()=>{o(w,V.startHeading),o(D,V.startIntro),o(te,L.guideButton),o(ne,V.termsHeading),o(ae,M(le)),i(J,`aria-disabled`,M(G)),o(Y,M(G)?`Checking...`:`Check for updates`),i(X,`aria-busy`,M(G))}),e(`click`,A,()=>{O(_,{slug:`index`,anchor:``},!0)}),n(B,()=>M(f),e=>O(f,e)),e(`click`,J,me),l(a,c)};h(x,e=>{M(_)?e(w):e(T,-1)}),l(a,m)},$$slots:{default:!0}}),te()}f([`click`]);export{vt as default};