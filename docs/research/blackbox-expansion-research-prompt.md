# Research prompt: what else should the Unravis Blackbox record?

Paste everything below the line into a deep-research AI (Gemini Deep Research, Claude, ChatGPT). Attach two files: `unravis_sql_evidence_ranked_plan.md` and `docs/user/blackbox.md`.

---

<mission>
Find every measurement a small Windows host-recorder should add so an investigator can answer "why was this API slow at time T?" for a .NET / IIS / SQL Server system. Output a ranked, sourced list of additions and ready-to-paste backlog lines. Research only; write no code.
</mission>

<product>
Unravis is a read-only investigation tool. It compares a good "reference window" with a bad "incident window" and shows API, web-server (IIS) and SQL lanes on one timeline.
The Blackbox is its flight recorder: a Windows service (runs as SYSTEM, reads only its own machine) that every 15 s writes one JSON line per sample to hourly files. Housekeeping keeps 7 days and 2 GB by default.
</product>

<already_built>
Per sample: whole-machine CPU %, available and committed memory, disk queue length, average ms per disk transfer, network bytes in/out, free % of the recordings drive, the busiest processes (CPU, working set, private bytes, handles, threads) and a catalogue of *.log file names, sizes and times. A reading that fails is left out and never written as 0. The case screen (Step 9) reads these for CPU, memory, disk and network.
</already_built>

<backlog_deferred>
- BL-100 SQL performance-counter group via the host's counters: Batch Requests/sec, User Connections, Lock Waits/sec, Processes Blocked. No SQL login.
- BL-101 Sampler of dm_exec_requests and sessions every 15 s (running at once by wait type, blocking). Needs VIEW SERVER STATE and a stored credential.
- BL-102 Sampler of wait-stats and query-stats deltas, no query text.
- BL-103 Show Blackbox SQL data as a lane source labelled "Blackbox sampling every 15 s since <date>".
</backlog_deferred>

<attached_sql_plan>
`unravis_sql_evidence_ranked_plan.md` ranks 15 SQL checks in 4 bundles. They are built into the app's own SQL lane, not the Blackbox, so they only help history that SQL Server already keeps (Query Store, system_health, default trace, ring buffer of about 256 minutes). Many of its DMVs are snapshot or cumulative only: dm_os_schedulers, dm_exec_query_resource_semaphores, dm_io_virtual_file_stats, dm_db_index_operational_stats, tempdb space and version store, AG queues, performance counters. For those, only a recorder sampling them over time can show what happened in a past window.
</attached_sql_plan>

<dotnet>
Most of the load is the .NET API hosted in IIS (w3wp.exe, one process per app pool) and a .NET Windows service on the same servers. Runtime is unknown: could be .NET Framework 4.x or modern .NET (6+); the research must cover both.
Scope: only processes the owner selects are recorded in depth, not everything on the server. Today that means the payroll software's API app pools and the email Windows service. The owner picks them by name (app pool or service) in the Blackbox settings; nothing else gets per-process .NET detail, counters or dumps. Machine-wide counters stay as they are.
The owner wants to see, per process, which one burns CPU and where memory goes, without restarting IIS or the service, and wonders about keeping dumps.
</dotnet>

<questions>
1. Windows performance counters (Perfmon / PDH). List the counters worth sampling every 15 s on a web or SQL server, grouped as Processor, Memory, PhysicalDisk, Network, System, Process, IIS and HTTP.sys, ASP.NET and .NET CLR (see question 5), SQL Server (named and default instances), Hyper-V or VM steal. Seed ideas, not limits: processor queue length, privileged and DPC time, read and write latency split, disk IOPS, pages/sec, TCP retransmits and port use, HTTP.sys request queue, ASP.NET requests queued, CLR % time in GC and thread-pool queue, SQLServer:Buffer Manager and General Statistics.
2. Windows event logs (System, Application, disk or resource-exhaustion events): which event IDs explain a slow window, and can they be read without keeping message text?
3. From the attached SQL plan: sort the 15 entries into (a) SQL already keeps the history, so the app reads it, (b) snapshot only, so the Blackbox should sample it, (c) neither. Say what each (b) item needs: permission, cost per sample, and whether it can run without a SQL login.
4. Anything else professional tools record that is missing (for example PAL, Brent Ozar's First Responder Kit, sp_WhoIsActive, SQL Sentry, Datadog, Windows Performance Recorder). Name the source for every idea.
5. Per-process .NET health, no restart.
   a. Light and continuous: which counters or events show CPU, working set, private bytes, GC heap size, gen0/1/2 collections, % time in GC, allocation rate, large-object heap, thread-pool queue length and threads, lock contention, exceptions per second, handles? Compare PDH ".NET CLR" counters (Framework), System.Runtime EventCounters / EventPipe through the diagnostics IPC (modern .NET, dotnet-counters), and ETW. Say what works without restarting the process or setting an environment variable first, and what the recorder needs (SYSTEM rights, same user, local pipe access).
   b. Mapping a process to its app pool or service name without recording command lines (WAS counters, IIS worker-process APIs, service-to-PID query). Handle app-pool recycles, PID reuse and `w3wp#1` name shifts.
   c. Dumps. Compare full dump, mini dump, GC dump (dotnet-gcdump), dotnet-dump, ProcDump triggers, WER LocalDumps and createdump. For each: does it pause the process and for how long, size, can it be triggered by a rule (for example CPU above X% for Y s), what it exposes, and whether it needs a restart. Dumps hold memory, so they can contain passwords, tokens and request bodies. Propose a safe design (owner-triggered only, or automatic with a size cap, encryption, short retention, never read by Unravis, excluded from case files) or say it should be skipped. Prefer lighter options that answer "which process, which memory" without a dump.
   d. Selection. Design how the owner chooses the targets (a list of app-pool and service names in blackbox.json, picked in the install wizard from what is running). Say what happens when a selected pool or service is stopped, recycled, renamed or not found (show "not running", never 0), and whether the existing "busiest processes" list should stay, shrink, or follow the selection.
6. Which measurements are cheap and safe at 15 s and which need a slower interval? Estimate the extra size per day.
</questions>

<rules>
- Read-only. Never record request or query text, plan XML, payloads, passwords, environment variables or command lines.
- A new SQL permission must be one grant per check and be run by the owner; the app never grants anything.
- A measurement that cannot be read is missing, never 0.
- Per-process detail and any dump apply only to the owner-selected targets. Never attach to, sample or dump an unselected process.
- The recorder must not slow, pause or restart the API or the service. Anything that suspends a process (a dump) must say for how long and be off by default.
- Any security-sensitive item (a stored credential, SQL access from a SYSTEM service) must be flagged for a separate security review.
- Prefer official sources (Microsoft Learn, vendor docs), then well-known practitioners. Tag each claim "read" (you opened the page) or "inferred". Do not invent counter names; give the exact object and counter path, and say how the path differs on a named SQL instance.
- Think hard about wrong readings (counter rollover, cumulative versus rate, cold start, multi-instance names) and say how to avoid them.
</rules>

<output>
1. A table, one row per candidate: ID, what to record (exact counter, DMV or event), why it explains a slow window, where it comes from (PDH, WMI, event log, SQL DMV), history or snapshot, permission, cost per sample, privacy check, effort S/M/L, value H/M/L, source link, verdict (Add to Blackbox, App side only, Skip).
2. Top 10 by value for effort, in the order to build.
3. Backlog lines in this exact form, numbered from BL-104:
   `- [ ] **BL-NNN** Title. One line of why or what. (source: research, <date>)`
4. Open questions the owner must answer, each with your recommended answer.
Start the reply with the top 10 and the backlog lines; put the full table after them.
</output>
