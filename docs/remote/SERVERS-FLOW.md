# Servers: the flow, start to finish

Plain version of what happens when you use **Servers** to get Blackbox recordings and logs from a remote machine onto your PC. Prototype: `web/prototype/servers-PROTOTYPE.html` (v2). Design and security detail: `SERVERS-DESIGN.md`.

**What the screen is for:** it gets Blackbox recordings and logs from a remote server onto your PC, so nobody copies files by hand. Unravis only reads. It never changes, restarts or deletes anything on a server.

## Two ways in

| Route | Use it when | Steps |
|---|---|---|
| **Through a Salt master** (Linux or Windows server) | Your team manages many machines with Salt and you want one of several | 1 to 7 below |
| **Straight to one server** | You can sign in to the machine itself | Same, minus steps 3 and 4 |

A third case stays: a machine that only runs Blackbox (no login). It shows as a read-only row. Recordings work, browsing and logs stay off.

## The flow (Salt route)

1. **Connect.** Choose the type of the server that runs the Salt master: Linux or Windows. Give its address, port, user name, and how you sign in: a key file (default) or a password (opt-in per server, kept only in the sealed vault). Saved servers can be reused in one click.
2. **Trust the key.** Unravis fetches the server's host key without sending any secret and shows its fingerprint. You compare it with your server admin's, tick the box and trust it. The fingerprint is pinned. If it ever changes, Unravis stops and does not sign in.
3. **Choose machines.** Unravis asks the Salt master which machines it manages (accepted keys only: `salt-key -L`, and who is online). It shows name, operating system and state: online, offline, or waiting for acceptance. Only online machines can be chosen. Unravis never accepts or removes a machine in Salt.
4. **Reach them.** For each chosen machine, through the master: the master answers, the machine answers (`test.ping`), read-only access is allowed, and a look for Blackbox, IIS and logs. A machine that does not answer in 20 seconds is marked, nothing is changed on it, and you can retry or carry on without it.
5. **See what they have.** Per machine: Blackbox recordings, IIS logs, Windows event logs, SQL Server logs, Redis logs, with where each was found, how many files, how big. Everything found is ticked by default; change any of it and pick the time range (24 hours, 3 days, 7 days). Only folders on the machine's allowlist can be read; the allowlist is editable per server. A pull over the size limit (20 GB, adjustable) is refused with a plain reason; a big one streams.
6. **Copy to your PC.** Files stream to disk in pieces. Each file is hash-checked against the server, secrets are masked before anything reaches a case, and a dropped connection resumes at the exact byte. You can stop at any time.
7. **In Blackbox.** Each machine's recordings become a saved set in the **Blackbox view** (named like `web-01 (pulled 26 Nov 14:20)`). Logs appear under Files. **Add to a case** copies them into a case; the originals stay as pulled.

**Straight to one server:** steps 1, 2, then "See what it has" (step 5 for one machine), copy, land in Blackbox. Same checks and same limits.

## What exists today and what does not

| Part | State |
|---|---|
| Server list, sealed key or password, host key pin and change refusal, test connection, folder allowlist | Built and tested (`internal/remote/*`) |
| Browse, plan, pull with resume, hash check, scrub, size limits | Built and tested, not yet run against a real server |
| HTTP API for all of it (`/api/servers...`) | Built and tested |
| Direct SSH route and Blackbox-over-tunnel | Built |
| Salt jump host: list accepted machines, reach a machine through the master (S2b) | **Not built.** Needs PR 348 (the read-only Salt tool) so the same command rules are reused. The registry already stores the chosen machine ids. |
| Servers screen in the app | **Not built.** Waits for the prototype to be approved. |
| `unravis ops server ...` commands | Not built; they will call the same functions as the screen. |
| Windows event log export, schedules, remote restart | Not built (restart stays off by default) |

Nothing here is checked on a real server yet, so no backlog item is ticked.
