# Cold start and new-case wizard: acceptance criteria (draft v4, decisions recorded, awaiting final sign-off)

Wireframe: `.dev/prototypes/coldstart.html` (serve the folder, e.g. `python -m http.server`, and open it). Bottom bar: layout A/B/C, a jump to the **Blackbox** screen, scenario selectors (cases, scan, Blackbox found or not, connection test, code). "Explain simply" shows the ELI10 layer.

## Decisions already made
- One **case** holds everything. Storage is the local encrypted per-case SQLite store.
- **API audit logs are one source among equals.** There is no separate "API audit logs" step. A MongoDB connection is just a connector; for each database in it the user says whether it holds **API audit logs**, **other data**, or isn't used.
- Flow: **Name, detect on this computer, connect servers (Mongo, SQL Server, Redis, IIS, a file, ...), review**.
- Mongo is read-only and for analysing. Nothing is stored in Mongo.
- Detection findings are tick boxes with a plain-English "what it shows". Findings that need a connection (Mongo, SQL, Redis) offer "Connect to it", pre-filled with the detected host. Findings that need an export show numbered how-to steps.
- Option 2 = paste the 26-char case code, or open an exported case file (an encrypted file; restoring needs the master key it was exported with, and always makes a new case). Decision D-1: cases are protected by a user-chosen master key.
- The wizard is large and uncluttered: wide container, 34px headings, 16px body, one decision per screen in the primary layout.

## G. What the scan can and cannot know (checked against `internal/discover`)
Rule: the wizard only shows as "found" what the detectors are certain of, and asks for the rest. Nothing below is guessed.

**Certain (read from files and services on the computer Unravis runs on; no connection, no login):**
- IIS: each site's number (W3SVC1...), its log folder, log format, and the folder of its root application, from `applicationHost.config`; the log files in it (count, size, dates); application pool names. The scan finds the sites on this computer, one finding per site.
- App logs: for each site folder, the logger config (log4net, NLog, Serilog, Microsoft logging) and the log folder and file pattern it names. When the config does not name a folder, the finding says so ("needs the folder") and the user types it.
- Hangfire and Quartz: that the DLL sits in a site folder (presence only).
- SQL Server, MongoDB, Redis (and Memurai): that the Windows service exists, the SQL instance name, and each one's own log file. Not the databases, not the port or password, not whether it holds audit logs.
- nginx and Apache access logs from their config; Perfmon files; the live sampler.

**Not knowable by the scan (the user supplies it; the wireframe asks):**
- Environment. Only the user knows, and it is a free label, not a fixed list: a stage ("Production"), a client ("Client A production", "Client D sandbox") or both. Asked once for this computer ("several" allowed), then per item when this computer hosts more than one.
- Portal and API names. IIS keeps site and application names, but the scan deliberately does not read them (privacy ruling); a folder name such as `D:\Sites\PortalA\Orders` is offered as a suggestion ("Portal A / Orders") the user accepts or edits. Unnamed sources still work and sit under "not named yet".
- Which database names exist (needs a SQL driver and a login, so only after connecting), the MongoDB/Redis endpoint, and everything on **other** computers (the scan reads only the computer it runs on; other servers come through a share, a connection the user adds, or a Blackbox catalog).
- One IIS site is one finding. Several applications inside one site are not told apart today.

## Decisions recorded from the question round (2026-10-25)
- Q2 **Start screen always shows the three cards**, even with one open case.
- Q3 **Restoring from a file always makes a new case**; nothing is overwritten.
- Q4 **Layout B (Workspace)** (chosen earlier).
- Q5 **The user always gets the choice** when a scan-found server matches a saved connection: use the saved one, or fill in fresh. Never auto-picked; passwords never pre-filled.
- Q6 **Wizard first, Blackbox later**: release 1 = start screen + wizard with environments/portals/APIs and the log4net folder step, connection probes, file-based Mongo health, Hangfire CSV; release 2 = Blackbox on this computer and case connection; release 3 = pack for other Windows servers, live Mongo health reads, Hangfire-from-SQL; later = Linux, disk and network metrics.
- Q7 **Quartz stays but is hidden** from the wizard (reachable through "A file or export"; scan keeps noticing it quietly).
- Q8 **Blackbox** is the name.
- Q9 **Several environments in one case** (supersedes "one environment per case"); see 19g.
- Q10 **Blackbox notes where logs are and copies nothing by default**; a snapshot is an explicit action.
- Q11 **Manifest + NDJSON** is the Blackbox format from the start (needs a new reader; the old CSV stays for "measure this computer").
- Q12 **Windows service** for Blackbox on other servers (not a scheduled task).
- Q14 **Environment names are free text** (2026-10-25): any label the user thinks in, such as a stage or a client (Client A production, Client D sandbox). No built-in Production/Staging list or default; the field may be left blank and named later.
- Q13 **The scan covers this computer only** (2026-10-25). Another computer is reached by what the user supplies: a connection string or address for MongoDB, SQL Server and Redis when reachable, or files the user exports or copies (log files, Redis slow log CSV, Hangfire CSV, a Blackbox recording folder). No remote scanning, so the scan never needs credentials or network access. Every scan finding is labelled "this PC"; the wizard asks the environment once, not per computer.

## Layout variants shown in the wireframe (pick one, or mix)
- **A Focus:** one big question per page (4 steps), progress bar, sticky Back / Skip / Next bar. Roomiest; slowest for experts.
- **B Workspace:** 3 tabs (Name, Sources, Review). Sources is a two-pane screen: list of everything found or connected on the left, details and setup on the right, "+ Add a connection" button. Best for many sources.
- **C One page:** all four sections stacked, sticky summary with the Create button on the right. Fastest for experts; longest page.

## A. First screen
1. Shown whenever no case is open. Three cards in order: 1 Use an existing case, 2 Enter an existing case, 3 Create a new one.
2. Each card has a one-sentence description and, with "Explain simply" on, an ELI10 line.
3. With zero local cases, card 1 still shows and leads to an empty state offering Create and Enter. The start screen says so.
4. Fully keyboard-operable; cards are buttons; focus lands on the heading on arrival; Back returns with focus on the card used.

## B. Use an existing case
5. Lists every case on this computer: title, open/closed chip, event count, last opened, and the sources it holds.
6. Open opens it; a closed case offers Reopen. An erased case is not listed.
7. Opening a case lands on the analysis with that case's sources preselected.
8. If the case store is unavailable or a case fails to open, show the reason in plain words and keep the list usable.

## C. Enter an existing case
9. Code field: lowercases and strips spaces; live counter "n of 26"; rejects characters outside a-z and 2-7 with a plain message; Open enables only for a valid code that exists locally.
10. Code not found: say so, suggest a typo check or using the case file. No leak of whether other cases exist.
11. Closed case by code opens read-only with a notice.
12. File: drop or choose a case export (JSON, optional events CSV). Show file name, size, source/event counts and checksum result before anything is written.
13. Restore always creates a **new** case; it never overwrites. A corrupt, wrong-version or oversize file shows a specific error and writes nothing.

## D. Create wizard (unified connector model)
14. Steps: **Name it, Look around (detect), Connect, Review**. Layout B merges the middle two into one "Sources" screen. Steps are clickable backwards freely and forwards only when valid. Nothing is persisted until Create.
15. **Name:** 2-80 chars; Next disabled until valid; hint warns not to put passwords or customer names here.
16. **Detect:** runs the This-computer scan (names and sizes only, no file opened). Each finding shows name, host, readiness chip (ready / found, needs a connection / needs an export), "What it shows" and ELI10. Scan states: scanning (progress), finds things, finds nothing (calm empty state, can continue).
17. **Ready** findings (IIS per portal, app logs per API, a Blackbox) are ticked to include. **Needs-connection** findings offer "Connect to it" which opens the connection form pre-filled with the detected host and marks the finding "connection added". **Needs-export** findings show how-to steps; the file picker for them opens at Create.
18. **Connect:** a gallery of connector kinds: MongoDB, SQL Server, Redis, IIS web server logs (one per portal), App log (one per API), Blackbox recording, A file or export. Any number of connections of any kind. Each has a kind-specific form (server, user, password, etc.) and "Test connection and see what is there". Skippable.
19. **Mongo "holds API audit logs?"** Three choices on the connection form: *Not sure, find out* (default), *Yes*, *No, other data only*. After the test, every database is listed with a chip ("looks like audit" or "other") and a role select: **API audit logs / Other data / Don't use**. Defaults follow the answer (Yes or Not sure: the likeliest audit database preselected as API audit logs; No: nothing preselected as audit). API audit logs are optional; Review warns, not blocks, if none are chosen ("the main analysis will have fewer requests to show").
19a. **A Mongo connection serves two jobs, independently:** (1) its databases as data (API audit logs / other / don't use), and (2) **the MongoDB server's own health**. After the test, a "this MongoDB's own health" list probes everything and reports each item as *available*, *switched off*, *needs permission* or *needs a file*: **Slow operations** (profiler, `system.profile`), **Server vitals** (`serverStatus`: connections, lock queues, opcounters), **Running operations** (`currentOp`), **Server log** (`mongod.log`; not reachable over a connection, so a file or the scan on the server), **mongostat samples** (file). Available items are pre-ticked; unavailable ones show what to do (for example "ask for the read-only clusterMonitor role") and are never auto-fixed. Unravis never switches the profiler on or changes the server. Health checks work with or without any audit database chosen, and Create is valid with health-only.
19b. A connection shows Mongo **now**. History over an incident needs the log or saved samples; the wireframe says so in plain words. Copy states this.
19c. **Build note (not drawn):** today the Mongo evidence families (`mongod-json`, `mongo-profile`, `mongo-currentop`, `mongo-serverstatus`, `mongostat-json`) are file importers. Reading profiler, `serverStatus` and `currentOp` live over a connection is **new backend work**. Phase it: ship the wizard with the connection probe and file-based health first, add live reads after.
19d. **Hangfire recurring jobs come from SQL Server first.** Hangfire stores its jobs in a SQL database, so a SQL Server connection offers a "Hangfire recurring jobs" part with a *where* choice: **this database** (default; the probe reports found/not found and a job count), **a different database on this server** (name field), **a different SQL Server** (button adds it as its own SQL connection; the part is off here), or **a CSV export** (file picker at the end). The scan-found Hangfire item says "easiest: connect SQL Server and tick it there; otherwise export a CSV". Build note: no SQL Server driver exists in the backend today (`go.mod` has none), so the live read is **new backend work** (read-only queries on the Hangfire `Hash`/`Set` tables, plus a driver dependency). Phase 1 can ship the CSV path only, with the exact SQL to run in SSMS shown in the wizard. The existing CSV importer (`schedule-hangfire`) is the landing format either way.
19e. **Quartz.NET is not offered in the wizard.** It was added by the log-support gap check (commits `58cc76a`, `bb64219`, `73ebf08`), not requested. The importer stays and is reachable through "A file or export"; scan detection and the `qz` finding are dropped from the wizard. Decision pending: keep the detector silently or remove it.
19f. **Measuring a server is not part of a case.** It is the separate **Blackbox** feature (section F). A case only *connects* to what a Blackbox recorded, through the kind **Blackbox recording** (a folder or share). The scan offers a Blackbox found on this computer; otherwise the user points to a folder or share.
19g. **Multi-environment, multi-portal, multi-API model.** One case can cover **one or several environments** (free-text names on the first step, comma separated, optional; the product ships with no preset names, the wireframe pre-fills "Client A production, Client A staging, Client B production" only to demo several), because one server can host the APIs or databases of more than one environment. An environment has **portals**; a portal has **APIs**; each API has its own app log (log4net/NLog), each portal has its own IIS log. **A database, Redis or Hangfire source belongs to one environment**; a server that holds several environments' databases is added once per environment (the scan can offer both). Every source is tagged **Environment > Portal > API**; the environment prefix is shown only when the case has more than one. The scan **cannot** know the tags (see section G): it reports what it saw (site ID, folder, service) and offers the folder names as a suggestion the user accepts with one click; the user says which environment each computer is, once. A manual connection asks "which environment / portal / API" (suggesting names already used; the environment question is hidden when there is only one), and a Blackbox catalog arrives pre-tagged. The rail, scan list and review group by environment, then portal (that environment's shared sources first); the summary shows "N environments, M portals, K APIs, S shared sources". Build impact: environment is a tag on every source in the case store and a filter in the analysis.
19h. **App logs (log4net, NLog) need a config, not just a path.** Unravis reads the logger config next to each site (folder, file pattern, rolling, layout) and shows the log as ready. Only when the config does not say where it writes (for example the folder is set in code) does the finding become **"needs the log folder"**: it shows the config found, asks for the folder, and its tick box stays disabled until a folder is typed. A manual **App log** connection takes the config file (best) and/or the log folder; each API is its own connection. Build note: `internal/discover/detect_applog.go` already derives the folder and rolling pattern and raises the "log4net log path" gap only when it cannot; the wizard wires that gap to the folder field.
20. Other kinds list their parts to tick (for example SQL: Query Store, Wait statistics, Hangfire recurring jobs; Redis: Slow log, Latency history; IIS: log files with size and day range).
21. Connection errors are specific: missing server, wrong credentials, unreachable host; each says what to try. Connections are read-only; the UI recommends a read-only user.
22. Passwords are held in memory for the session only. They are never written to the case store, logs, exports, URLs or the saved connection profile.
23. **Review:** name, then every chosen source (found here or connected) with read-only chip and, for Mongo, which databases are API audit logs vs other. "Where your data lives" statement. A consent checkbox gates Create. At least one source is required.
24. Create: creates the encrypted case, saves the connection profiles (no password), queues the ticked sources, shows progress and per-step result, then Open the analysis. A failure part-way keeps what succeeded, says what failed, and offers Retry; no half-made unnamed case is left behind.
25. Leaving the wizard discards everything after a clear label ("nothing is saved until the last step").
26. The summary (name, found here, connections, sources chosen, API audit logs count) is always visible in layouts B (Review) and C (sticky), and as the footer count in A.

## F. Blackbox (separate feature, not part of a case)
Wireframe: the **blackbox** jump in the bar (4 steps). Recommended name: **Blackbox** (a flight recorder: always writing, tidies itself, read after something went wrong). Alternatives: Scout, Tracer, Beacon.
33. **Purpose:** a tiny recorder placed on a server. It records server health over time and keeps a **catalog of where every portal's and API's logs are**. Cases connect to it later; it is not created inside a case. It is reachable from the start screen ("Place a Blackbox") and from the scan.
34. **Place wizard (4 steps):** (1) name the server and choose where it runs: this computer, another Windows server (a pack is produced), Linux ("coming later", disabled); (2) what to record: CPU, memory, handles and threads on; disk and network "coming later"; reading interval 5, 15 (default), 30, 60 s; logs as **catalog only** (default, nothing copied) or **also a copy of the last 24 h**, refreshed hourly; (3) housekeeping; (4) place it.
35. **Housekeeping is mandatory.** It cannot be turned off, and recording stops rather than fill the disk. The user tunes: keep 1/3/**7**/14/30 days, cap 0.5/1/**2**/5/10 GB, pause if free disk is under 5/**10**/20 %, and the output folder. Fixed and shown as locked rows: compress data older than 1 hour, delete oldest first, touch only its own folder, stop recording if it cannot tidy up. It runs at start and hourly inside the recorder (no second background job). The wizard estimates disk (MB per day x days) and says whether it fits the cap.
36. **Lightweight and safe, stated plainly on the last step:** under 1 % of one CPU core at background priority; backs off (doubles its interval) if over budget; about 30 MB memory; one small write a minute; no open ports; sends nothing anywhere; reads logs by size and date only, never contents, unless a snapshot is requested; writes only inside its own folder. These are acceptance targets to be measured, not just claims (the existing sampler already budgets under 1 % of one core).
37. **Output format (readable by software and by an AI):** a `manifest.json` (host, OS, Blackbox version, settings hash, recorded time range, housekeeping policy, and the **log catalog**: for each portal and API its path, size, day range, and layout read from its config) plus NDJSON metric files (units in the key names, one reading per line) plus optional log snapshots. Phase 1 may keep the existing `win-live` CSV as the metric file so the current importer works unchanged.
38. **Placing on this computer:** a Start button turns into a status card: recording, its own CPU, disk used against the cap, housekeeping last run, logs found (for example "3 APIs in 2 portals, 2 IIS sites"), **Snapshot the last 2 hours**, Stop.
39. **Placing on another server:** the wizard produces a **pack** (program, `blackbox.json`, README) with the settings shown for review, a download button, and four plain steps: copy it, run `unravis-blackbox install` once as administrator (registers one small Windows service that starts at boot, runs at low priority and restarts itself if it stops), run `status`, then add **Blackbox recording** in a case. Nothing is installed from the Unravis machine. Windows first; Linux later.
40. **Connecting from a case:** the scan lists a Blackbox found here as "Blackbox recording"; otherwise the user gives its folder or UNC share. After the test the case shows server, recorded range, size, health and housekeeping status, then each recorded item as a tick box tagged Portal > API (server health, each API log, each IIS log, snapshots). Review lists what was chosen.
41. **Build notes (not drawn):** all new. (a) a `blackbox` mode of the Go binary (install and uninstall as a Windows service, status, run, snapshot), reusing `internal/sampler`; (b) manifest and log-catalog writer using the existing Discover detectors; (c) housekeeping engine with tests (cap, retention, free-disk pause, crash and restart safe); (d) a `blackbox` importer reading the manifest and metrics into a case; (e) pack builder endpoint. No SQL or Mongo driver work. Windows only first.

## E. Quality bars
27. Copy is beginner-clear everywhere; technical terms link to the Glossary; ELI10 toggle persists.
28. Accessibility: labelled fields, errors announced (role=alert / aria-live), visible focus, tabs/steps expose current state, colour is never the only signal, contrast meets WCAG AA in both themes.
29. Roomy by default: container up to 1280px, 34px step headings, 16px body, 44px inputs, 40px buttons. At 375px: one column, summary below, no horizontal scroll.
30. No regressions: existing Landing run/start modes, Connections drawer (Mongo profiles are reused: "Connect to Mongo" can pick a saved profile), Import drawer, Discover panel, header nav, "Add more data" button, demo mode (no case store: wizard explains and hides case-store steps).
31. Security: no credentials in the case store, logs, exports or telemetry; case codes are identifiers not secrets; Mongo and other servers only get read commands; scan reads metadata only.
32. Tests: web unit tests for code validation, step gating, summary state (including the audit-role count) and copy; Go tests only if a new endpoint is added; browser pass on 18771 with synthetic data; clumsy-user review after merge.

## Open questions
None outstanding. Q1-Q12 are resolved above. Remaining step: final sign-off on the revised wireframe (multi-environment tags are now drawn: environment field on step 1, environment prefix on groups and chips, a Staging log and a Staging SQL database in the scan).
