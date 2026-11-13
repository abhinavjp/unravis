# Research prompt: Brent Ozar's SQL Server tuning -> ideas for Unravis

Written 2026-11-12. Two chained prompts (Claude's prompt-engineering guidance: split big jobs into stages and feed one output into the next). Each is built as a pyramid: goal on top, then context, scope, constraints, output format, and the hard rules repeated at the bottom. Run Prompt 1 first; paste its full output into the marked place in Prompt 2.

Suggested runner: a research-capable agent with web search AND a real browser (YouTube transcripts need a browser; a plain page fetch returns an empty shell).

---

## PROMPT 1: Catalogue what Brent Ozar's tooling checks, and why

### Goal (one sentence)
Build a complete, sourced catalogue of the checks, findings and tuning rules in Brent Ozar's SQL Server tooling and teaching (First Responder Kit, blog, YouTube, free training), so another team can decide what to borrow.

### Why this matters
We are building Unravis, a local, read-only tool that helps an investigator work out why an API was slow in a given time window, using API audit logs, IIS logs and SQL Server evidence. We want our SQL lane to reach the depth of `sp_Blitz`-style tools, but adapted to "what changed between a good window and a bad window". Nothing you write is executed; it only feeds a design decision.

### Sources to cover (go as deep as time allows; record every source you used)
1. **First Responder Kit** (GitHub: BrentOzarULTD/SQL-Server-First-Responder-Kit, plus brentozar.com/blitz and each tool's documentation page): `sp_Blitz`, `sp_BlitzFirst`, `sp_BlitzCache`, `sp_BlitzIndex`, `sp_BlitzWho`, `sp_BlitzLock`, `sp_BlitzBackups`, `sp_DatabaseRestore`, `sp_AllNightLog`. Read the actual check list (the `CheckID` list and priorities in `sp_Blitz`, the warnings in `sp_BlitzCache`, the diagnosis names in `sp_BlitzIndex`), not just the descriptions.
2. **Blog** (brentozar.com): posts explaining individual checks, wait types, parameter sniffing, plan-cache analysis, index rules of thumb, Query Store, tempdb, memory grants, "what to check first on a slow server".
3. **YouTube**: Brent Ozar's channel, including live tuning sessions, Office Hours, and conference talks. Seed example from the owner: `https://www.youtube.com/watch?v=uDFX1YHfRqo` (page title "Microsoft SQL Server Performance Tuning, Live"; contents NOT yet read, so treat as a lead, not a fact). Find the other videos that cover the same ground. For each video: open it in a real browser, get the transcript and chapter list, and note what each segment teaches or checks, with its timestamp. If a video cannot be read, say so and move on; never describe a video from its title alone.
4. **Free training and talks** that are public. Paid course content: do not reproduce it; note only that a topic exists.
5. **Closely related, for contrast** (shorter pass): Erik Darling's DarlingData tools (`sp_PressureDetector`, `sp_QuickieStore`, `sp_HumanEvents`), Adam Machanic's `sp_WhoIsActive`, Glenn Berry's diagnostic queries, Paul Randal's wait-types library (SQLskills), Microsoft documentation for each DMV or catalog view you cite.

### What to extract for every check or rule
- **Name and tool** (exact, as the tool writes it) and **category** (server config, waits, memory, CPU, I/O, tempdb, blocking/deadlocks, plan cache, indexes, Query Store, backups/DR, Agent, security, corruption, other).
- **What it detects and why it hurts**, in two plain sentences a first-time investigator could follow.
- **How it detects it**: the DMV, catalog view, system procedure, extended event or log it reads (exact object names, e.g. `sys.dm_os_wait_stats`).
- **Threshold and rationale**: the number or condition that trips it, and the reason given. If the threshold is a rule of thumb, say who says so and whether they hedge it.
- **Point-in-time or history?** Does it read a counter that is cumulative since restart, a snapshot of "right now", or data kept over time (Query Store, ring buffers, error log, msdb)? This matters because our questions are about a past window.
- **Permission it needs** (e.g. VIEW SERVER STATE, VIEW DATABASE STATE, sysadmin, msdb role, none) and whether it only reads.
- **Cost or risk to run** on a busy production server (long scans, plan cache pressure, locks).
- **Source**: URL, and for video the timestamp. Mark each item `read` (you saw the primary text), `heard` (transcript only), or `unverified` (secondary or recalled).

### Constraints
- Cite a source for every item. If you cannot find one, put it under "Unverified" and say what you would need to confirm it.
- If sources disagree (for example on a threshold), show both.
- Do not copy code or long text. Names, object names, thresholds and one-line paraphrases are enough. Note the licence of any repository you read (the kit is open source; say which licence and what it requires).
- Say plainly when you are unsure or when data is missing; do not fill gaps from memory.
- Stay inside SQL Server. Mention other engines only if a source does.

### Output format
1. A short **"how Brent's approach is organised"** summary (max 12 lines): what he checks first, second, third, and the mindset behind it.
2. **Catalogue table**, one row per check, columns: ID/name | tool | category | detects | reads (objects) | threshold + why | history or snapshot | permission | run cost | source + verification tag.
3. **Video index**: one row per video you read: title, URL, length, what it covers with timestamps, verification tag.
4. **Unverified / could not read** list.
5. **Gaps in my search**: topics you expected to find but did not.

### Hard rules (repeat)
Cite everything. Tag every item read / heard / unverified. Never describe a video you did not open and read. No copied code. Do not guess thresholds.

---

## PROMPT 2: Gap analysis against what Unravis already has

### Goal (one sentence)
Using the catalogue from Prompt 1, produce a ranked list of checks and features that would make Unravis's SQL evidence deeper, saying for each one whether Unravis already has it, what it would need, and what it would cost.

### Input
Paste the full output of Prompt 1 here: `<<<CATALOGUE>>>`

### What Unravis is (context)
Unravis is a local desktop tool for investigating "why was the API slow at time T". The investigator picks a reference window (normal) and an incident window (slow), and the tool lines up evidence from several lanes on one timeline: API audit logs, IIS logs, SQL Server, MongoDB. The SQL lane is meant to answer "what changed in the database between those two windows". It does not tune the database for the owner.

### What the SQL side already does (as of 2026-11-12; facts from the code)
**Timeline SQL lane** (`internal/dbhealth/lane_sql.go`), four checks, each needing one read-only grant:
1. *Server start and thread pool*: `sqlserver_start_time` from `sys.dm_os_sys_info` and the cumulative `THREADPOOL` waiting-task count from `sys.dm_os_wait_stats`. Grant: VIEW SERVER STATE (2019) or VIEW SERVER PERFORMANCE STATE (2022).
2. *Error log*: `sp_readerrorlog` for "cachestore flush" and "SQL Server is starting", plus `sp_enumerrorlogs`, to explain restarts and plan-cache flushes. Grant: VIEW ANY ERROR LOG (2022 only; 2019 has no narrow grant).
3. *Query Store*: reads `sys.query_store_runtime_stats`, `..._interval`, `query_store_plan`, `query_store_query` over the reference and incident windows (72-hour slices, 60 s per query, top 3000 rows). Flags a query when it ran at least 5 times in both windows and is at least 2x slower, or reads at least 1.25x more pages. Per-interval average and max duration for the chart. Also reads `sys.database_query_store_options` (state, interval length, read-only reason) and the first interval start. Grant: VIEW DATABASE (PERFORMANCE) STATE.
4. *Agent jobs and backups in the window*: `msdb.dbo.sysjobhistory`/`sysjobs` and `msdb.dbo.backupset` near the incident start, to catch a job or backup overlapping it. Grant: SQLAgentReaderRole in msdb.

**Health report** (`internal/dbhealth/sql.go`, "Run Analysis"-style snapshot, 13 steps): server (version, edition, uptime, CPUs, memory), CPU last 30 minutes (ring buffer), top waits since restart with plain-language meanings (ASYNC_NETWORK_IO, PAGEIOLATCH, WRITELOG, LCK_M_*, RESOURCE_SEMAPHORE, THREADPOOL, CX*, SOS_SCHEDULER_YIELD, PAGELATCH, PREEMPTIVE, IO_COMPLETION), memory (free MB, page life expectancy, memory grants pending, total vs target), blocked or long-running requests now, connections by program with idle-in-transaction, heaviest cached queries (`sys.dm_exec_query_stats`), Query Store last 24 h hourly average with 3x-spike flag, missing indexes (top 5, hints only), last backups per database, risky database settings (auto-close, auto-shrink, page verify, state), deadlock counter since restart, failed Agent jobs in 24 h.

**File importers**: Query Store CSV/JSON export (`internal/importer/sqlserver/querystore.go`: per-interval duration, CPU, reads, rows, executions, forced plans, execution type, wait categories; query text and plans are deliberately never read), extended-events and IIS importers.

**Rules the design must respect**
- Read-only against the server. Unravis shows grant scripts to the database owner and never runs them (decision A1); one grant per check (decision A30). New ideas that need a new permission or a setting change are allowed, but must say exactly which, and must be shown as an owner-run script.
- No query text, plan XML, parameter values or literals may enter events, reports or error messages (privacy rule in the importer). Ideas that need statement text must say how to keep it out, or be marked as conflicting.
- Fixed query texts only; no values from the user are placed into SQL except formatted datetimes and checked, bracketed identifiers.
- Built for first-time investigators: every finding needs a plain-language explanation.
- Real data first: an idea only counts as "buildable" if the data it needs is available on a live server through the stated objects.

### Tasks
1. Map every item in the catalogue to one of: **Already covered** (name the file/step), **Partly covered** (what is missing), **New**, or **Not a fit** (why: static advice rather than incident evidence, needs write access, too risky, needs text we must not keep, etc.).
2. For each **Partly covered** and **New** item that is worth doing, write one entry (fields below).
3. Rank the entries. Weigh: how often it explains a real "API was slow" incident, whether it can answer a *past* window (history) rather than only "now", effort, and permission cost.
4. Suggest 3 to 5 **bundles** that make sense to build together (for example "tempdb and memory-grant evidence" or "blocking history via extended events").
5. List what would need an **owner-run setup** (a new grant, an extended-events session, Query Store switched on or tuned, a collection job) and say what that setup costs the server.

### Entry fields
Title | Category | What it would show on the timeline or in the report (one sentence) | Reads (exact objects) | History or snapshot (and how far back) | Permission/setup needed | Run cost on production | Privacy check (any text/literals?) | Effort (S/M/L) | Overlap with existing check | Value (high/med/low) with a one-line reason | Source from catalogue.

### Output format
1. **Top 15 ranked entries** (full fields).
2. **Bundles** (3 to 5), each with the entries it contains and a suggested build order.
3. **Already covered** list (short, with where).
4. **Not a fit** list (one line each with the reason).
5. **Needs owner setup** table: setup | what it enables | risk | rollback.
6. **Open questions** for the owner, max 8, each with your recommended answer.

### Hard rules (repeat)
Respect the read-only and no-text rules above. Mark anything resting on an `unverified` catalogue item as unverified. Do not invent check names or DMV columns; if unsure a column exists on SQL Server 2019 and 2022, say "confirm". Rank, do not just list.
