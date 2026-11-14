# Blackbox expansion: prioritised and conditions, 2026-11-13

Sources: Google "Argon" research (raw: `C:\Users\Abhinav\Downloads\unravis-blackbox-google-argon-research.txt`), an Opus security review (`blackbox-sql-security-review.md`) and a Sonnet research pass on memory visibility and overhead. Backlog items: BL-104 to BL-126 in `docs/BACKLOG.md`. Owner ruling 2026-11-13: no "deferred"; design and conditions first, nothing implemented yet.

Counter paths are from Google and not yet verified. Run `typeperf -q` on a real server before building any counter group.

## Conditions every item must meet

1. **Light**: under 0.5% of one core on average, under 50 MB, each group within 1 s, below-normal priority, self-throttle (BL-122). A group that would break the budget ships off or at a slower interval.
2. **Never pauses, restarts or dumps a target.** No attach, no environment variable, no restart of IIS or the service.
3. **Owner-selected targets only** for per-process detail (payroll API pools, email service). Machine-wide counters stay machine-wide.
4. **Both runtimes, auto-detected per process**: .NET Framework 4.7.2 (PDH `.NET CLR` counters) and .NET 10 (EventPipe counters, in-process or out-of-process hosting). Detect by loaded modules, not by config (BL-105).
5. **Unreadable means missing, never 0.** A stopped, recycled or missing target shows "not running" or "not found".
6. **No text**: no query or request text, plan XML, payloads, environment variables or command lines. Event log: IDs and numbers only.
7. **Read-only SQL**: one grant per check, run by the owner; the app never grants anything. Local SQL only. SQL controls C1-C14 (BL-123, BL-124).
8. **Counter engine rules** (BL-121): English counter names, two baselines before a rate, valid-status check, multi-core CPU not capped at 100.
9. **Every item gets a test** with fake readers, plus a check on a real server before it is ticked (live-verified or not, said plainly).

## Order

| Group | Items |
|---|---|
| 1. Foundation (build first) | BL-122 self-budget, BL-121 counter engine, BL-117 target selection, BL-105 resolver and runtime detection |
| 2. Core evidence | BL-104 HTTP.sys queue, BL-106 IIS worker requests, BL-108 disk and CPU split, BL-107 (with BL-100) SQL counters |
| 3. Per-process memory and CPU | BL-109 (Framework targets), BL-110 (.NET 10 targets), BL-116 per-process I/O |
| 4. More host evidence | BL-111 event log, BL-112 TCP, BL-114 ASP.NET queues, BL-115 memory pressure, BL-125 process list shape |
| 5. SQL sampler (after BL-123 and BL-124) | BL-101, BL-102 (wait deltas), BL-118, BL-119, then BL-103 lane source |
| 6. Optional | BL-113 VM steal (VMs only), BL-120 dump file catalogue, BL-126 on-demand allocation sampling |

## Decisions

- **Dumps**: the Blackbox never takes one. A dump does not restart the process, but it freezes it briefly (a snapshot clone: milliseconds, unconfirmed) or for seconds (a direct full dump), then loads disk and memory while writing, and the file holds all customer data. Instead: heap generation, large-object-heap and private-byte counters every 15 s answer "which process, which memory".
- **Which object types hold the memory**: no option gives this without suspending the process (it needs a full garbage collection or a snapshot). Closest safe option: allocation type names sampled while a person has turned it on for a few minutes (BL-126), which shows what is being allocated, not what is retained.
- **Log scan**: already every 5 minutes (`logsEvery`, `recorder.go:25`), capped at 5,000 entries visited, depth 4, 200 files; no data is lost and no change is needed. Google's 15 s premise was wrong, so that item is dropped.
- **Busiest processes**: top 10 by CPU plus top 5 by memory plus every selected target (BL-125). The cost is the same for 5 or 20 because every process is read to rank it; only file size changes. Windows' own processes are included (only Idle is left out).
- **SQL**: allowed with conditions through a separate service account (not a stored password, not SYSTEM). Query-stats deltas and remote SQL are dropped.

## Not yet confirmed (check on a real server before building)

- Exact PDH paths for HTTP.sys, W3SVC_W3WP, WAS_W3WP and Hyper-V (`typeperf -q`).
- Whether SYSTEM can open an app-pool process's `dotnet-diagnostic-{pid}` pipe (decides BL-110).
- Snapshot freeze time, and the cost of EventPipe or ETW inside the target (no measured figure found).
- Which hosting model the .NET 10 API uses on each server.
