# Log-support gap check

Status as of 2026-10-25. Question: can Unravis take every log that helps find a root cause (IIS, log4net, .NET Framework and .NET 10 Web API, Redis, MongoDB and neighbours), and show server CPU and memory per time and per process? Evidence below is from synthetic fixtures only. Nothing has been run on a real export.

## Source by source

| Source | Importers shipped | Verdict | Missing, with the call |
|---|---|---|---|
| IIS | `iis-w3c` (W3C access logs, UTC, per-site), `iis-httperr` (HTTP.sys), win-events with WAS classifier | Supported | `iis-freb` (failed-request tracing XML) is built. ANCM stdout is covered by `ancm-stdout`. |
| log4net | `log4net-text`, `log4net-xml` | Supported | Custom pattern layouts beyond the common `%date [%thread] %level %logger - %message` are refused with a reason, not guessed. |
| NLog | `nlog-text` (default pipe-delimited layout; time is host-local unless `log-time=utc` is stated) | Supported | Space-delimited and custom layouts are refused as unsupported. JSON layout is read by the Serilog CLEF or MEL JSON path when keys match. |
| Serilog | `serilog-clef`, `serilog-text` | Supported | None. |
| Microsoft.Extensions.Logging | `mel-console`, `mel-json`, `ancm-stdout` | Supported | None. |
| .NET Framework Web API | log4net, event log (`win-events` with Application classifier), perfmon counters, App Insights export | Supported | ETW traces (`.etl`) are binary and not read. Export to text first. |
| .NET 10 Web API | MEL, Serilog, `dotnet-counters` CSV/JSON, App Insights export | Supported | `dotnet-trace` `.nettrace` is binary and not read. |
| Windows events | `win-events` (EVTX export to XML/CSV) with WAS, Application and Task Scheduler classifiers | Supported | Security log is out of scope on purpose. |
| Schedulers | `schedule-hangfire`, `schedule-agent` (SQL Agent), `schedule-quartz` (Quartz.NET text logs) | Supported (done) | Quartz is built end to end: importer, time probe for large-file seeking, discovery ranking and collect suggestions (a Quartz log marks an app host like a Hangfire export). It reads the log4net and MEL-with-timestamp text layouts; message wordings are matched from public sources, not a real export. Discover spots Quartz by its DLL in a site bin folder and tells the user to find the app log that carries it; Quartz has no log file of its own, so a path is never guessed. |
| Redis | `redis-slowlog`, `redis-latency`, `redis-info`, `redis-log` | Supported | Node-side Redis proxy logs: not built, no known format to target. |
| MongoDB | `mongod-json` (structured JSON log), `mongo-profile`, `mongo-currentop`, `mongo-serverstatus`, `mongostat-json` | Supported | Verified on synthetic data only. `$collStats`, `getMore` and `maxTimeMS` behaviour and Mongo 3.4 text logs wait on the Mongo owner (held item T0). |
| SQL Server | `sql-xe` (extended events), `sql-errorlog`, `sql-pdh` (perfmon CSV for the instance), `sql-querystore` (Query Store export, CSV or JSON) | Supported | Query Store export: join `sys.query_store_runtime_stats` to `sys.query_store_runtime_stats_interval` (add `sys.query_store_wait_stats` for waits) and save the result with SSMS Save Results As CSV, `sqlcmd -s, -W`, or a `FOR JSON PATH` query. Keep `start_time`, `end_time`, `query_id`, `plan_id`, `count_executions`, `avg_duration`, `max_duration`, `avg_cpu_time`, `avg_logical_io_reads`, `avg_rowcount`, `is_forced_plan`. Durations are microseconds and interval times are UTC (the importer converts). Query text and plan columns may be in the file but are never read. Rows are interval averages, so a short spike inside an interval is smoothed. |
| nginx or Apache access logs | `nginx-access`, `apache-combined` | Supported | Formats are from documentation, not a real export. Custom `log_format` strings are refused with a reason. |
| Linux sadf, pidstat, vmstat | `linux-sadf`, `linux-pidstat`, `linux-vmstat` | Supported | Formats are from documentation, not a real export. |
| Perfmon `.blg`, ETW `.etl`, `.nettrace` | refused as binary | Export needed | Binary. A scan reports an `export-needed` finding with a How step: `relog -f csv` for `.blg`, then `win-counters` reads it. |

## On-host discovery

Unravis will run on the target Windows server, so the first path is auto-detect and direct import (spec `docs/superpowers/specs/2026-10-25-host-discovery-design.md`). Built and merged: core and hardened reader, IIS (sites, W3C, HTTP.sys, FREB, ANCM), Windows event channels (System fields only, never Security), app-logger configs (log4net, NLog, Serilog, MEL; paths only, no secrets), Redis, MongoDB and SQL Server file detectors, nginx and Apache, Perfmon and host hints, the `win-live` sampler (off by default) and the This-computer UI. In progress: API routes, audit and watermark tables, and the scan service that wires detectors to the UI. Held: live probes of Redis, Mongo and SQL, and the scheduled task.

No detector has run on a real server yet. See the real-server list in `2026-10-25-held-items.md`.

## CPU and memory, per time and per process

- **Live on the host**: the `win-live` sampler records host CPU and memory and per-process figures for server processes at 5, 15, 30 or 60 s, off by default and stopped at 72 h or 2M events. Not yet run on a real server.
- **Host level**: `win-counters` (perfmon CSV from `relog`), `sql-pdh`, `dotnet-counters`, `redis-info`, `mongo-serverstatus` and `mongostat-json` give CPU, available memory, and engine counters per time bucket.
- **Per process**: `win-counters` w3wp and other process instances give CPU, working set, private bytes, handles and threads. `win-procsnap` takes `Get-Process`-style snapshots.
- **Shown where**: Resource panel (`web/src/components/ResourcePanel.svelte`): rule chips RC1 to RC9, a host summary, per-process measure tabs, zoom controls, markers, and gap lines for windows with no data.
- **Evidence (synthetic)**: an import of a perfmon fixture and a SQL PDH fixture into a test case returned host CPU, available memory and per-process cpu, working set, private bytes, handles and threads series from `/api/cases/{id}/resource` and `/series`; the panel rendered chips, host summaries, tabs, markers and charts. The window spanned 2001 and the zoom landed on an empty slice, so the drawn lines themselves were not seen in the browser.
- **Limits stated plainly**: imported counters are not yet on the latency chart's shared time axis; they have their own strip. Time zones must match: a counters file whose bias disagrees with the host map is refused, not shifted.

## Defects found in the clumsiest-user pass on this build

Fixed with tests:
- Hypothesis text and notes accepted control characters and right-to-left overrides. Now refused (`internal/ledger`, `TestTextRefusesControlAndBidiCharacters`).
- A window that flags every line as not observed produced a 25-line wall. The gaps list now shows six lines and a "Show all N gaps" toggle (web test added).
- Erase during an async import, cancel of a finished job, and a wrong or missing erase confirmation all behave correctly (checked against the live API).

Left as is, cosmetic: a 5 MB body to `/api/ledger` drops the connection instead of returning 413.

## Still open, none fixable from inside the repository

See `2026-10-25-held-items.md`. Real-export runs, the Mongo field check, owner items, the 24-hour live scan and the five-tester test each need a person. R-TE is ruled there.

## Known weaknesses

- One running import is tracked in the UI at a time; the server accepts a second concurrent async import into the same case.
- A crash can leave an unreadable spool file in the temp folder.
- No `-race` run: the encrypted SQLite driver needs CGO off.
- Views may hit the 10 s `maxTimeMS` limit on a real collection. Not measured.
- (Fixed) The resource panel's "These files cover" period comes from imports, and recorded samples are imports. The Discover panel now tells the case to re-read its history when the sample count moves and when recording stops, so the period follows the recording without a page reload.

## Verified end to end on the real binary and UI (this PC, test instance)

Scan (5 findings, 1 gap), host confirmation with machine-name aliases, httperr import, Application (418 rows) and System (608 rows) channel imports, "Nothing new to add" for empty ranges, live sampler start and stop (samples kept, resource API returns them). Local only; no real client export was opened.

## Measured limits on synthetic data (full detail in `2026-10-25-performance-notes.md`)

Run with `UNRAVIS_BENCH=1`; synthetic input only, one PC. The benchmarks themselves changed no production code. D1 and D2 were measured as risks first and have since been fixed (additive schema version 3, lazy indexes, compact resource rows; old case files open and migrate lazily, with a migration test).

- **D1 (high) FIXED**: series and resource queries on 200 counters x 24 h x 15 s (1.14M events) used to hit the 10 s budget and return a partial answer covering about the first half of the window. Now selector filtering runs in SQL via partial indexes: series with 1 / 10 / 50 selectors take 0.08 / 0.51 / 2.06 s, not partial (synthetic, one PC). Partial signalling still works when the budget is truly exceeded. Not measured: the one-time lazy index build on a very old large case.
- **D2 (high) FIXED, memory reduced not eliminated**: resource evaluation used about 1.15 KB per scanned event (678 MiB at the budget cut-off). Now it keeps about 115 B per event; the full 24 h, 1.14M-event case evaluates in 3.64 s, not partial, with 260 MiB max heap (synthetic, one PC). Memory still grows with scanned events; the 2M-event cap projects to about 0.5 GB (extrapolation, not measured).
- **D3 (medium)**: import runs at 9 to 11.5k rows/s for IIS and 12 to 16k events/s for counters; a 5M-line IIS import took 550 s and a 10M-row import would take about 18 min. Needs free disk of about 2x the final case.
- **D4 (medium)**: case.db is 2.4x the input for IIS and Windows events, 36x for perfmon counters (about 366 bytes per event).
- **D5 (low)**: log4net import working set grows with input (59 MiB at 1M, 108 MiB at 3M entries) while the Go heap stays flat. Cause not confirmed.
- **Fine**: localread memory is flat on a 2 GiB W3C file; a 5000-file discover scan takes under 40 ms of detector time; the win-live sampler costs 0.31% of one core and 15 to 16 MiB.
- **Practical advice**: a 24 h, 200-counter case (1.14M events) now returns complete series and resource answers on the test PC. Slower disks or CPUs, or cases near the 2M-event counters cap, may still hit the 10 s budget and show a partial result, which the UI flags; fewer selectors per query keep series fast.
