# Log-support gap check

Status as of 2026-10-25. Question: can Unravis take every log that helps find a root cause (IIS, log4net, .NET Framework and .NET 10 Web API, Redis, MongoDB and neighbours), and show server CPU and memory per time and per process? Evidence below is from synthetic fixtures only. Nothing has been run on a real export.

## Source by source

| Source | Importers shipped | Verdict | Missing, with the call |
|---|---|---|---|
| IIS | `iis-w3c` (W3C access logs, UTC, per-site), `iis-httperr` (HTTP.sys), win-events with WAS classifier | Supported | **FREB** (failed-request tracing XML): not built. Add only if an analyst can supply an export; it is per-request and the audit log already carries per-request timing. ANCM stdout is covered by `ancm-stdout`. |
| log4net | `log4net-text`, `log4net-xml` | Supported | Custom pattern layouts beyond the common `%date [%thread] %level %logger - %message` are refused with a reason, not guessed. |
| NLog | in progress (text layout importer on a worktree) | Pending merge | JSON layout is read by the Serilog CLEF or MEL JSON path when keys match. |
| Serilog | `serilog-clef`, `serilog-text` | Supported | None. |
| Microsoft.Extensions.Logging | `mel-console`, `mel-json`, `ancm-stdout` | Supported | None. |
| .NET Framework Web API | log4net, event log (`win-events` with Application classifier), perfmon counters, App Insights export | Supported | ETW traces (`.etl`) are binary and not read. Export to text first. |
| .NET 10 Web API | MEL, Serilog, `dotnet-counters` CSV/JSON, App Insights export | Supported | `dotnet-trace` `.nettrace` is binary and not read. |
| Windows events | `win-events` (EVTX export to XML/CSV) with WAS, Application and Task Scheduler classifiers | Supported | Security log is out of scope on purpose. |
| Schedulers | `schedule-hangfire`, `schedule-agent` (SQL Agent) | Supported | Quartz is not covered. |
| Redis | `redis-slowlog`, `redis-latency`, `redis-info`, `redis-log` | Supported | Node-side Redis proxy logs: not built, no known format to target. |
| MongoDB | `mongod-json` (structured JSON log), `mongo-profile`, `mongo-currentop`, `mongo-serverstatus`, `mongostat-json` | Supported | Verified on synthetic data only. `$collStats`, `getMore` and `maxTimeMS` behaviour and Mongo 3.4 text logs wait on the Mongo owner (held item T0). |
| SQL Server | `sql-xe` (extended events), `sql-errorlog`, `sql-pdh` (perfmon CSV for the instance) | Supported | Query Store export is not read. |
| nginx or Apache access logs | none | Missing | Add `nginx-access` / `apache-combined` only when a reverse proxy appears in a real case. Low risk, one afternoon, Sonnet. Not started: no evidence they are in this estate. |
| Linux sadf, pidstat, vmstat | none | Missing | Same call: only if a Linux host joins a case. |
| Perfmon `.blg` | none | Missing | Binary. The supported path is `relog -f csv`, which `win-counters` reads. |

## CPU and memory, per time and per process

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
