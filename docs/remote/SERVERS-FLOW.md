# Servers: the flow, start to finish

Plain version of what happens when you use **Servers** to get Blackbox recordings and logs from a remote machine onto your PC. Prototype: `web/prototype/servers-PROTOTYPE.html` (v5). Research on getting large logs efficiently: `LOG-TRANSFER-RESEARCH.md`. Design and security detail: `SERVERS-DESIGN.md`.

**What the screen is for:** it gets Blackbox recordings and logs from a remote server onto your PC, so nobody copies files by hand. Unravis only reads. It never changes, restarts or deletes anything on a server.

## The route

The main and first path is **through a Salt master** (a Linux or a Windows server that runs Salt). You sign in to that one jump host, the way you already do in PuTTY. Unravis never needs a login to the other machines.

**Straight to one server** (a direct SSH login) is parked: nobody can test it today. The code exists and stays off the screen.

A machine that only runs Blackbox (no login) can still show as a read-only row. Recordings work, browsing and logs stay off.

## The flow


1. **Connect.** Choose the type of the server that runs the Salt master: Linux or Windows. Give its address, port, user name, and how you sign in: a **password** (default, the same one you type in PuTTY) or a key file (optional, only if your admin gave you one). The password is kept only in the sealed vault on this PC. Nothing is downloaded or created for you. Saved servers can be reused in one click.
2. **Trust the key.** Unravis fetches the server's host key without sending any secret and shows its fingerprint. PuTTY asks the same question the first time ("trust this server?"). You compare it with your server admin's, tick the box and trust it. The fingerprint is pinned. If it ever changes, Unravis stops and does not sign in.
3. **Choose machines.** Unravis asks the Salt master which machines it manages (accepted keys only: `salt-key -L`, and who is online). It shows name, operating system and state: online, offline, or waiting for acceptance. Only online machines can be chosen. Unravis never accepts or removes a machine in Salt.
4. **Reach them.** For each chosen machine, through the master: the master answers, the machine answers (`test.ping`), read-only access is allowed, and a look for Blackbox, IIS and logs. A machine that does not answer in 20 seconds is marked, nothing is changed on it, and you can retry or carry on without it.
5. **See what they have.** Unravis reads the file list first (names, sizes, time ranges), nothing copied yet. For a case, the range is read from the case (the investigation window and reference range you already set in the Investigation timeline), shown read-only with a button to change it there; if the case has none yet, Unravis says so and you choose a range per item. Per machine: Blackbox recordings, IIS logs, Windows event logs, SQL Server logs, Redis logs, with where each was found, how many files, how big. Everything found is ticked by default; change any of it and pick the time range (case window plus reference, case window only, 24 hours, 3 days, 7 days; the first two only when the case has them) and whether to squeeze text logs (Auto, Always, Never). The rest of the last 7 days can be fetched quietly in the background afterwards. Only folders on the machine's allowlist can be read; the allowlist is editable per server. A pull over the size limit (20 GB, adjustable) is refused with a plain reason; a slow one gets a warning that names faster routes. Steps 2 and 3 show copy-ready commands you can run yourself or send to your IT admin to check access.
6. **Copy to your PC.** Files stream to disk in pieces. Each file is hash-checked against the server, secrets are masked before anything reaches a case, and a dropped connection resumes at the exact byte. You can stop at any time.
7. **In your timeline.** Each machine's recordings become a saved set in the **Blackbox view** (named like `web-01 (pulled 26 Nov 14:20)`), and logs appear under Files. **Use in the timeline** copies them into a case, where the investigation timeline can pick them as sources. The originals stay as pulled.

## What exists today and what does not

| Part | State |
|---|---|
| Server list, sealed key or password, host key pin and change refusal, test connection, folder allowlist | Built and tested (`internal/remote/*`) |
| Browse, plan, pull with resume, hash check, scrub, size limits | Built and tested, not yet run against a real server |
| HTTP API for all of it (`/api/servers...`) | Built and tested |
| Direct SSH route and Blackbox-over-tunnel | Built, but parked (not on the screen) |
| Salt jump host: list accepted machines, reach a machine through the master, read in 48 KiB chunks (S2b, `internal/remote/saltmaster`) | Built and unit-tested against a fake master. **Not verified on a real Salt master.** Still to do: make the service salt-aware, HTTP routes, bundle-aware pull (5 MB test first). |
| Servers screen in the app | **Not built.** Waits for the prototype to be approved. |
| `unravis ops server ...` commands | Not built; they will call the same functions as the screen. |
| Windows event log export, schedules, remote restart | Not built (restart stays off by default) |

Nothing here is checked on a real server yet, so no backlog item is ticked.

## Blackbox as the main collector (owner wish 2026-11-26)

Owned by the Blackbox thread; plan: `blackbox-everything/PLAN.md` (project library). Servers aligns with it:

- The pull unit is a **bundle**: one zip in the server's `snapshots` folder (recordings in the window, scrubbed config, `facts.json`, scrubbed SQL and Redis log tails, sha256 manifest). Servers pulls it through the Salt master in base64 chunks with a sha256 check, plus any extra log files you tick. Chunk size and the 100 MB practicality are not verified (plan section 4.5: test with 5 MB first).
- Pulled data becomes timeline sources under the plan's names: `sql-errorlog`, `redis-log`, `redis-slowlog`, `win-events`, `iis-*`, and the Blackbox reader for bundles. The Servers screen shows each source name on the landing page.
- Direct connect stays parked there too.
