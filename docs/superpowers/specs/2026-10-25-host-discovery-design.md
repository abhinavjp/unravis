# Host discovery: "This computer" scan, direct import and live sampler

Status: design, 2026-10-25. Owner: builder. No code exists yet.
Scope: Unravis runs on the Windows server being investigated (IIS hosting .NET Framework and .NET 10 Web APIs; Redis, MongoDB and SQL Server on the same box or reachable). Today the analyst exports every source by hand and uploads it. This spec makes Unravis find the useful sources on the machine it runs on, show them as a checklist, and import the ones the analyst ticks, through the importers that already exist.

All examples are synthetic: hosts `web-a`, `web-b`, names under `*.example.test`, addresses from RFC 5737 (`192.0.2.0/24`), dates in 2001, and the secret marker `SENTINEL-7f3a`.

Out of scope: remote hosts (other than what is reachable on loopback ports for P4 probes, held), Linux discovery (the Linux importers stay upload-only), any write to the target system, any change to the MCP tool set (R10 stands).

---

## 0. Rulings at a glance

| # | Ruling | Why | Cost if wrong |
|---|---|---|---|
| D1 | New package `internal/discover`; detectors are pure Go over injected interfaces; Windows syscalls only in `*_windows.go`. | Every detector is testable on any OS with fakes; the risky part (file access) is in one small, reviewed file. | A detector needs an API the interfaces do not cover: add a method to `Env`, one fake update. |
| D2 | The read API takes opaque finding IDs from a scan held in memory, never a path from the client. | A token-holding page, a malicious extension or a confused deputy can only read what this scan found. | None for safety; a user who wants an arbitrary file still uploads it. |
| D3 | Config files are parsed for log paths only, by streaming parsers that keep named path attributes and drop every other value. | Configs hold connection strings and keys. Keeping nothing but a validated path is the only rule that is easy to test. | A logger whose path sits in an unusual key is missed; it shows as "not found" and the user uploads by hand. |
| D4 | Run on demand as the interactive analyst, "Run as administrator" optional. No Windows service in v1. | DPAPI keys are per user; the token is delivered through the user's session; a service means always-on attack surface on a production box. | Analysts want it running overnight: add a scheduled task as the same user with profile loaded (P4). |
| D5 | Event logs read with `wevtapi` through `NewLazySystemDLL`, rendered to System values only, fed to the existing `win-events` importer as an in-memory CSV. | No child process, no localised message text, no EventData except the WAS app pool ID, one parser path. | A Windows build where the render call misbehaves: fallback is the documented `Get-WinEvent` export recipe, already supported. |
| D6 | Live sampler default off, 15 s interval, top 15 processes plus server images, stops at a cap; writes source `win-live`. | No per-import delete exists, so unbounded writes would fill the case; a hard stop is safer than rotation. | Users want a rolling window: needs per-import delete in casestore first (not planned). |
| D7 | Historical pulls import non-overlapping, line-aligned byte ranges with a watermark per file identity. | `EventID` includes a random import nonce, so an overlap counts rows twice. | A bug in the watermark double-counts: the store's whole-file SHA check does not catch ranges, so the watermark has its own tests. |
| D8 | Redis, MongoDB and SQL Server in P3 are file-only (logs and configs on disk). Live probes (INFO, SLOWLOG, serverStatus, currentOp) are held behind an R13-style owner approval with user-typed credentials. | Live probes need credentials, and credentials must never come from config files. | Some analysts lack exports: they keep using the redis-cli / mongosh recipes. |
| D9 | Sources whose data is not on disk in a readable form (XE `.xel`, App Insights, binary `.blg`/`.etl`) produce `export-needed` or `remote-only` findings with the exact export step, never a parse attempt. | Matches the binary-refusal rule already shipped. | None. |
| D10 | Full paths are never persisted. Audit rows keep the base name and an HMAC of the canonical path under the case's pseudonym key. | Paths carry site names and user names; the case file is exported. | An analyst wants to know "which file exactly": the scan screen shows it live, not the case. |

---

## 1. Architecture

### 1.1 Package layout

| File | Build tag | Content |
|---|---|---|
| `internal/discover/discover.go` | none | `Detector`, `Env`, `Finding`, `Gap`, `Scan`, `Run(ctx, env, detectors) Scan` |
| `internal/discover/env.go` | none | interfaces `FS`, `Processes`, `Services`, `Registry`, `EventChannels`, `Machine` |
| `internal/discover/fake/fake.go` | none | in-memory implementations for tests |
| `internal/discover/paths.go` | none | path-shape validator, canonical form checks that need no OS |
| `internal/discover/configparse/*.go` | none | streaming XML/JSON/ini extractors for path attributes only |
| `internal/discover/detect_iis.go`, `detect_events.go`, `detect_applog.go`, `detect_redis.go`, `detect_mongo.go`, `detect_sql.go`, `detect_web.go`, `detect_host.go` | none | one detector per source family |
| `internal/discover/winenv_windows.go` | `windows` | real `Env` over x/sys/windows and lazy DLL procs |
| `internal/discover/winenv_other.go` | `!windows` | `NewSystemEnv()` returns `ErrNotSupportedOS` |
| `internal/discover/localread/open_windows.go` | `windows` | canonicalising, read-only, share-all file open |
| `internal/discover/localread/open_other.go` | `!windows` | returns `ErrNotSupportedOS` |
| `internal/discover/evtx/query_windows.go` | `windows` | wevtapi query to win-events CSV rows |
| `internal/sampler/` | split | live sampler (section 3) |

### 1.2 Interfaces

```go
type Detector interface {
    Name() string                                // "iis", "events", "applog", ...
    Detect(ctx context.Context, env Env) ([]Finding, []Gap)
}

type Env struct {
    FS        FS            // Lstat, Stat, ReadDir, Open (bounded reader), Final(path) (canonical, reparse flag)
    Procs     Processes     // List() []Proc{Image, PID, Start}  (image base name only)
    Services  Services      // List() []Service{Name, State, BinaryDir}  (binary dir parsed, args dropped)
    Registry  Registry      // String(key, value), only from an allowlist of keys in env.go
    Channels  EventChannels // List(), CanRead(name) error
    Machine   Machine       // Names() []string, TZKey() string, LogicalCores() int, Elevated() bool
    Importers *importer.Registry
}
```

`Services.BinaryDir` is the directory of the image path from the service config with arguments cut at the first unquoted space; arguments are never returned. The SQL Server `-e` error log argument is the one exception and is extracted by a dedicated parser that returns only the value after `-e` when it passes the path validator.

### 1.3 Finding and Gap

| Field | Type | Notes |
|---|---|---|
| `ID` | string | 26 chars base32 random, new per scan. Opaque. |
| `Detector` | string | e.g. `iis` |
| `Kind` | enum | `log-file`, `log-set` (rotated set in one dir), `event-channel`, `live-series`, `export-needed`, `remote-only` |
| `Importer` | string | must exist in the registry; empty for `export-needed`/`remote-only` |
| `HostLabel` | string | the local host label (section 7.1) |
| `Display` | string | what the UI shows: base name or channel name, plus site ID if any (`W3SVC2`, `u_ex010917.log`). Never a full path. |
| `paths` | []canonical | unexported, kept in the server's in-memory scan store only |
| `Confidence` | 0..100 | 90 config-declared and sniff agrees, 70 default location and sniff agrees, 40 name only |
| `Shows` | wording key | fixed sentence from `discover/wording.go`, linted |
| `SizeBytes`, `Files`, `Newest`, `Oldest` | int64, int, time | from Stat only |
| `NeedsAdmin` | bool | set when the probe failed with access denied, or the source class always needs it |
| `Cost` | enum | `small` (<64 MiB), `medium` (<1 GiB), `large` (>=1 GiB, window required) |
| `How` | wording key | for `export-needed`/`remote-only`: the exact step |

`Gap{Detector, Kind, Display, Reason}` with `Reason` one of `needs-elevation`, `not-found`, `not-supported-os`, `unreadable`, `too-large`, `unsafe-path`, `config-unparsed`. An access-denied error always becomes `needs-elevation`; a detector that swallows it fails the contract test.

### 1.4 Scan run

`Run` calls each detector with a per-detector timeout (10 s) and a global one (60 s), recovers panics into a `Gap{Reason: unreadable}`, sorts findings by the existing `collect` weights (IIS 25, HTTPERR 26, events 27, procsnap/live 30, app log 20, runtime 40, SQL 50, Redis 60, Mongo 70), and de-duplicates by canonical path (two sites sharing one log dir become one finding listing both site IDs). The server keeps at most 4 scans per process, each for 1 h, in memory only. A scan never reads file content beyond the 64 KiB `importer.HeadSize` head used to confirm the importer, and config files capped at 4 MiB.

---

## 2. Security model

### 2.1 Threats in scope

1. A page or process holding the token asks Unravis to read an arbitrary file (SAM hive, another user's profile, a key file).
2. A config file or log name crafted so a detector produces a path outside the expected tree (`..\`, junction, UNC, ADS, 8.3 alias).
3. Secrets from configs leaking into the case, the UI, logs, errors or the audit trail.
4. Unravis harming the production box: locking a log the app writes, heavy I/O, high CPU.
5. Silent partial results that look complete.

### 2.2 Controls

| Control | Rule |
|---|---|
| Allowlist | Read endpoints accept `{scanId, findingIds[]}` only. The server resolves IDs to canonical paths from its in-memory scan. No route accepts a path, a glob or a directory. A finding ID from another scan, an expired scan or another process is refused `404 finding-unknown`. |
| Canonicalisation | Before a detector emits a path: reject if it contains `..` segments, starts with `\\` or `\\?\UNC\` or `\\.\`, has `:` after the drive letter (ADS), contains a `~` segment that `GetLongPathName` changes (8.3 alias), or is not absolute on a local fixed drive (`GetDriveType == DRIVE_FIXED`). Then open with `FILE_FLAG_OPEN_REPARSE_POINT`; refuse if the file or any parent directory is a reparse point (same rule as `casestore.ErrReparsePoint`); compare `GetFinalPathNameByHandle` with the expected path, case-insensitive; refuse on mismatch. Directory walks do not follow reparse points and stop at depth 3 and 5,000 entries. |
| Re-check at read | The read path re-opens and re-runs every check, and compares volume serial + file ID from `GetFileInformationByHandle` with the values taken at scan time. A file swapped between scan and read is refused `unsafe-path`. |
| Open mode | `GENERIC_READ`, share `FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE`, `OPEN_EXISTING`, `FILE_FLAG_SEQUENTIAL_SCAN`. `GetFileType == FILE_TYPE_DISK` and not a directory. Never `FILE_FLAG_BACKUP_SEMANTICS` (that would bypass ACLs when elevated). |
| Frozen length | Length taken at open; the reader is an `io.LimitReader` to that length so a growing file yields a stable range ending at the last full line. |
| Size caps | Each importer's `DefaultLimits` applies unchanged through `importer.Prepare`. A finding whose window slice exceeds the cap is offered with a narrower window or refused `too-large`; it is never truncated silently. |
| Format check | The head must pass `importer.Sniff` (archives and binaries refused, as today) and `Registry.Detect` must pick the finding's importer within `TieWindow`. Mismatch: refused `format-mismatch`, nothing imported. |
| Secrets | Config extractors keep only an allowlist of keys (section 5). Each kept value must pass `paths.ValidLogPath`: absolute or relative to the config dir, no `=`, `;`, `@`, `://`, no whitespace runs, no key words `password`, `pwd`, `secret`, `key=`, `token`, `connectionstring`, `user id`, `accountkey` (case-insensitive), length <= 260 unless the long-path prefix is present (then refused). Values that fail are dropped and reported as `config-unparsed` with no value. Nothing else from a config is stored in a struct field, logged, put into an error string or sent to the UI. |
| Secret test | Every config detector has a test whose fixture puts `SENTINEL-7f3a` in every non-path attribute (connection strings, `requirepass`, `keyFile`, `masterauth`, App Insights instrumentation key, `Password=` in an NLog database target) and asserts the marker is absent from findings, gaps, JSON responses, server log output, audit rows and the case database bytes after import. |
| Consent | Each finding is imported only when the user ticks it and confirms the existing gating notice (`GATING_BANNER` sent as `ackText`, `ackGating=true`). A finding marked `NeedsAdmin` or `Cost=large` shows that on the row before the tick. No "select all" for `large`. |
| Audit trail | One row per read in a new `discover_reads` table inside the case: detector, importer, finding `Display`, `HMAC-SHA256(case pseudonym key, canonical path)`, volume serial + file ID hash, byte range, file size and mtime at open, SHA-256 of bytes read, account SID hash, elevated flag, window, result (accepted, refused reason). Full paths never. Rows are visible in the case's import list and excluded from MCP (R10). |
| Politeness | Reader goroutine sets `THREAD_MODE_BACKGROUND_BEGIN` (`SetThreadPriority` via lazy proc) for low-priority I/O and CPU; 1 MiB read buffer; a token bucket at 32 MiB/s; at most 1 file read at a time per process (reuses the import slot limiter). Scans use `Stat` and 64 KiB heads only. |
| Errors | Error text carries the finding `Display`, a reason code and counts. Never a full path, never a config value. |
| Loopback model | Unchanged: `127.0.0.1` listener, `X-Audit-Token`, single-use launch code, Origin check, DPAPI per-user keys. Discovery adds no listener and no network call. |

### 2.3 Account and privileges

| Source | Non-admin interactive user | Admin |
|---|---|---|
| `applicationHost.config` (`%windir%\System32\inetsrv\config`) | usually denied | read |
| IIS W3C logs (`%SystemDrive%\inetpub\logs\LogFiles`) | often denied (Administrators, SYSTEM, IIS_IUSRS) | read |
| HTTPERR (`%windir%\System32\LogFiles\HTTPERR`) | denied | read |
| FREB (`inetpub\logs\FailedReqLogFiles`) | often denied | read |
| ANCM stdout, app logs under site folders | depends on folder ACL | read |
| Event channels Application, System | readable by default | read |
| `Microsoft-Windows-WAS` (in System), Task Scheduler Operational | System yes; TaskScheduler/Operational often yes | read |
| Security channel | never read; not a detector | not read either |
| Process list, image names, threads (Toolhelp) | all processes | all |
| Other users' process CPU/memory (`OpenProcess` limited query) | denied for w3wp/services | read |
| SQL Server ERRORLOG | denied (service account folder) | read |
| Redis / Mongo log dirs | depends on install | read |

Ruling D4 detail: if the process is elevated, the launch link opens un-elevated through `explorer.exe <url>`. The URL carries only the single-use launch code (2 min), never the session token, which still arrives in the fragment after the redirect as today. The console also prints the link. The UI shows "Running as administrator" in the scan header so the analyst knows why more rows appear. Access denied always surfaces as a `needs-elevation` gap with the text "Run Unravis as administrator to read this." and is never skipped quietly.

---

## 3. Live sampler

| Item | Decision |
|---|---|
| Default | Off. Started per case from the "This computer" panel. Stops when the case closes, the process exits, or the cap is hit. |
| Interval | 15 s default; allowed 5, 15, 30, 60 s. |
| Processes | Top 15 by CPU delta, plus top 15 by private bytes (union, max 30), plus every process whose image is in the procsnap server list (w3wp, dotnet, node, redis-server, memurai, mongod, sqlservr) always. |
| Identity | Image base name, PID, start time. Server images keep the name; others become `Pseudonymize("instance", name)` through a helper exported from procsnap (`procsnap.ProcessLabel`) so both sources label alike. Never command line, never user, never app pool name, never window title. |
| Host metrics | `host.cpu` (from `GetSystemTimes` deltas / logical cores), `host.mem.available`, `host.mem.committed` (from `GlobalMemoryStatusEx` and `GetPerformanceInfo`), logical cores at start. |
| Process metrics | `cpu` (percent of one core, from `GetProcessTimes` delta), `working-set`, `private-bytes` (`K32GetProcessMemoryInfo`), `handles` (`GetProcessHandleCount`), `threads` (Toolhelp `cntThreads`). Plus `process-start` / `process-gone` markers like procsnap. Access denied for a process: name and threads only, one `sampler-partial` note per process per run, and the panel says "Some processes need administrator rights." |
| APIs | Pure Go, CGO off: x/sys/windows for Toolhelp, `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)`, `GetProcessTimes`, `GetActiveProcessorCount`; `NewLazySystemDLL("kernel32")` for `GetSystemTimes`, `GlobalMemoryStatusEx`, `GetProcessHandleCount`, `SetThreadPriority`; `NewLazySystemDLL("psapi")` for `K32GetProcessMemoryInfo`, `GetPerformanceInfo`. Handles closed every tick. |
| Write path | Every 5 min (or on stop) the sampler serialises its buffer to a canonical CSV (`win-live` v1 header, UTC ISO times, one row per metric) and calls `Case.Import` with importer `win-live`, file name `win-live-<2001-09-11T08-00Z>.csv`, `AckGating=true` with the stored consent text from start. First row of each batch is a carry-over row with each tracked process's last CPU seconds so deltas cross batch boundaries. |
| Importer | New `internal/importer/winlive`: `Detect` always 0 (never picked for uploads), `postImport` refuses `importer=win-live` from the network (`400 importer-internal`). Emits kind `metric` with procsnap-compatible attrs plus `metric` values `host-cpu`, `mem-available`, `mem-committed`. |
| Catalogue | Add source `win-live` to the `rules` sources of `host.cpu`, `host.mem.available`, `host.mem.committed`, `proc.cpu`, `proc.ws`, `proc.private`, `proc.handles`, `proc.threads` in `internal/resource/metrics.go`. No new metric IDs. |
| Cap | Stops at 72 h or 2 M events per case, whichever first; the panel shows "Sampling stopped at the limit." Restart needs a new click. |
| Budget | Target < 1% of one core on 8 logical cores at 15 s with 300 processes; < 50 MB RSS added (buffer <= 5 min x 31 series). Measured by a benchmark test on the dev box and re-measured on a real server (section 9.3). |
| Thread | Dedicated goroutine with `runtime.LockOSThread` + background priority; the HTTP server never waits on it. |

`win-live` v1 batch body (internal only, never accepted from upload):

```
# unravis-win-live v1 interval=15 cores=8
time,scope,process,pid,start,metric,value
2001-09-11T08:00:00Z,carry,w3wp,4120,2001-09-11T07:12:03Z,cpu-seconds,812.25
2001-09-11T08:00:15Z,host,,,,host-cpu,37.5
2001-09-11T08:00:15Z,host,,,,mem-available,2147483648
2001-09-11T08:00:15Z,proc,w3wp,4120,2001-09-11T07:12:03Z,cpu,61.2
2001-09-11T08:00:15Z,proc,instance-3fa9c1,7788,2001-09-11T06:00:00Z,private-bytes,104857600
```

`carry` rows feed the next delta and emit nothing. The parser refuses any column outside this header and any `process` value that is not a server image or an `instance-<hash>` label, so a bug in the sampler cannot write a raw name.

---

## 4. Historical pulls

### 4.1 Unit of import

| File shape | Unit |
|---|---|
| Rotated closed file (name date or mtime outside the active day), size <= importer cap | Whole file, if it overlaps the window. |
| Active file (newest in the set, mtime within 5 min) | Range from watermark (or start) to the last full line before the frozen length. |
| Any file > importer cap or > 1 GiB | Window slice: find the first line inside the window with `TimeProber` (4.3); without a prober, import the tail up to the cap and record a `partial-window` gap. |
| Rotated set (IIS `u_ex*.log`, NLog archives, Serilog `log-2001091*.txt`) | One finding; each file imported as its own Import so caps, duplicate SHA and audit rows stay per file. Ordered oldest first. |

### 4.2 Window and watermark

- Default window: the case's incident window when set, else last 24 h. Choices: last 1 h, 6 h, 24 h, 7 days, custom. Window passed as `Options.Window` (honoured today by applog core, dotnetcounters, mel stdout, mongodlog; P1 adds it to iis-w3c and win-events, P2 to the rest). Files fully outside the window by name date and mtime are skipped before opening.
- Watermark store: `discover_watermarks` table in the case, key `(volume serial, file ID, HMAC path, SHA-256 of first 4 KiB)`, value `(byte offset, last line time)`. A rotated or truncated file (size < offset, or first-block hash changed) restarts at 0.
- Header replay: for W3C (`#Fields:`) and CSV-shaped sources the reader prepends the most recent header seen before the offset, so a range parses alone. The header bytes are not counted in the watermark.
- Overlap rule: ranges are half-open `[offset, end)` and line aligned; a second scan of the same file imports only the new tail. Because `EventID` includes the import nonce, the watermark is the only guard against double counting for ranges; whole-file imports also hit the existing duplicate-SHA refusal.

### 4.3 TimeProber (P2)

Optional interface in `internal/importer`: `ProbeTime(line []byte) (time.Time, bool)`. Implemented for iis-w3c, serilog, nlog, log4net-text, mel, mongod-json. The local reader binary-searches byte offsets (seek, skip to next newline, probe) to the first line >= window start, at most 64 probes. Files without a prober and over cap: tail import with gap, as above.

### 4.4 Event logs (ruling D5)

`EvtQuery(NULL, channel, xpath, EvtQueryChannelPath|EvtQueryForwardDirection)` with XPath `*[System[TimeCreated[@SystemTime>='2001-09-11T08:00:00Z' and @SystemTime<'2001-09-12T08:00:00Z']]]` plus a provider/ID filter per classifier. `EvtNext` in batches of 256; `EvtCreateRenderContext(EvtRenderContextSystem)` for TimeCreated, Computer, Channel, Provider, EventID, Level. For WAS events only, a values context on `Event/EventData/Data[@Name='AppPoolID']` (the existing win-events importer pseudonymises AppPool). Never `EvtFormatMessage`, never the Message, never other EventData. Rows are written as the documented `Get-WinEvent` CSV (`TimeCreatedUtc, MachineName, LogName, ProviderName, Id, Level, AppPool`) into an encrypted spool (same as async import jobs) and imported with `win-events`. Channel caps: 256 MiB / 2 M rows (importer default). Watermark for channels: last `EventRecordID` per channel stored like file watermarks.

Rejected: `wevtutil qe` (child process; output contains all EventData and rendered text; localised; the query sits in a command line). `Get-WinEvent` via PowerShell (same, plus execution policy).

---

## 5. Detection recipes

"Unsafe" lists what the detector must never read or keep.

| Source | Where to look | Extract | Importer | Unsafe / never |
|---|---|---|---|---|
| IIS sites | `%windir%\System32\inetsrv\config\applicationHost.config`: `system.applicationHost/sites/site` (`id`), `logFile@directory`, `logFile@logFormat`, `siteDefaults/logFile@directory`; fallback `%SystemDrive%\inetpub\logs\LogFiles\W3SVC*` | site ID, log dir (expand `%SystemDrive%` only), format W3C | `iis-w3c` | bindings, host names, physical paths of content, `applicationPools` identity/password, `virtualDirectory@password`, `userName` |
| HTTPERR | `HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters\ErrorLoggingDir`, else `%windir%\System32\LogFiles\HTTPERR` | dir, `httperr*.log` set | `iis-httperr` | none beyond path rules |
| FREB | `traceFailedRequestsLogging@directory` per site in applicationHost.config, else `%SystemDrive%\inetpub\logs\FailedReqLogFiles\W3SVC*` | newest 500 `fr*.xml` in window | `iis-freb` (being added) | `freb.xsl` ignored |
| ANCM stdout | each site's `web.config` (physical path from applicationHost.config, read as a path only): `system.webServer/aspNetCore@stdoutLogFile`, `@stdoutLogEnabled` | path prefix, `stdout_*_<pid>.log` set | `ancm-stdout` | `environmentVariables` (often secrets), `arguments`, `processPath` args |
| App pools | applicationHost.config `applicationPools/add@name`, `@managedRuntimeVersion` | count and runtime only, for host Runtime hint | none (host hint) | `processModel@userName`, `@password` |
| log4net | `web.config` / `log4net.config` / `*.exe.config` next to site: `log4net/appender[@type*=FileAppender]/file@value`, `datePattern@value`, `layout` type (`XmlLayout` picks xml) | path, rolling pattern | `log4net-text`, `log4net-xml` | `AdoNetAppender` connectionString, `SmtpAppender` credentials |
| NLog | `nlog.config`, `NLog.config`, `web.config` `nlog` section, `appsettings*.json` `NLog` key: `target[@xsi:type=File]@fileName`, `@archiveFileName`; resolve `${basedir}` to the site dir, refuse other layout renderers except `${shortdate}`/`${date:format=...}` (turned into a glob) | path, glob | `nlog-text` | `Database` target `connectionString`, `Mail` target, `${environment}`, `${gdc}` |
| Serilog | `appsettings.json`, `appsettings.<Env>.json`: `Serilog:WriteTo[]` where `Name` is `File` or `RollingFile`: `Args:path`/`pathFormat`, `rollingInterval`, `formatter` (CLEF picks `serilog-clef`) | path, interval | `serilog-text`, `serilog-clef` | `ConnectionStrings`, `Seq` `apiKey`, `ApplicationInsights` keys, any other section |
| MEL file sinks | `Logging:File:Path` / `PathFormat` (Serilog.Extensions.Logging.File, NReco) | path | `mel-json` or `mel-console` per sniff | other `Logging` values kept nowhere |
| App Insights | presence of `ApplicationInsights` section or `ApplicationInsights.config` (boolean only) | `remote-only` finding with KQL export How | none | instrumentation key, connection string |
| Hangfire | presence of `Hangfire` package dll in site `bin` (name only) | `export-needed`: the documented job-history query export | `schedule-hangfire` | storage connection string |
| Task Scheduler | channel `Microsoft-Windows-TaskScheduler/Operational` | event-channel | `win-events` (ClassifyTaskScheduler) | task XML, task arguments |
| Windows events | channels Application, System (WAS provider), TaskScheduler/Operational; `CanRead` per channel | event-channel | `win-events` | Security channel never; Message and EventData never (except WAS AppPoolID) |
| Redis | services with image `redis-server.exe` or `memurai.exe`: config path is the first non-flag argument, read by the service parser only; `redis.conf` keys `logfile`, `dir` | log path | `redis-log` | `requirepass`, `masterauth`, `user` ACL lines, `tls-*-file`; slowlog/INFO need a live probe (held, D8) |
| MongoDB | service `MongoDB` image `mongod.exe` `--config` arg; `mongod.cfg` YAML key `systemLog.path`, `systemLog.destination` | log path (4.4+ JSON) | `mongod-json` | `security.keyFile`, `net.tls.*File`, `setParameter`, `processManagement`; profile/currentOp/serverStatus held (R13, D8) |
| SQL Server | services `MSSQLSERVER` / `MSSQL$<instance>`; registry `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\<inst>\MSSQLServer\Parameters` value `SQLArg*` starting `-e` | ERRORLOG dir, `ERRORLOG*` set | `sql-errorlog` | `-d`/`-l` args, any registry value outside the Parameters key |
| SQL XE / Agent | same `Log` dir: `system_health*.xel`, `SQLAGENT.OUT` | `export-needed` (XE: export to CSV in SSMS; Agent: job history query) | `sql-xe`, `schedule-agent` | `.xel` binary never parsed |
| nginx / Apache | services or processes `nginx.exe`, `httpd.exe`; `conf\nginx.conf` `access_log` directive, `httpd.conf` `CustomLog` (path token only) | log path, format name | `nginx-access`, `apache-combined` | `ssl_certificate_key`, `auth_basic_user_file`, `proxy_pass` targets |
| Host / processes | always | `live-series` finding for the sampler | `win-live` | command lines, users |
| Perfmon | `%SystemDrive%\PerfLogs\Admin\**\*.csv` only | log-set | `win-counters` | `.blg` → `export-needed` (relog to CSV) |
| .NET counters | `*.csv` / `*.json` with dotnet-counters head in site dir or `%TEMP%` of current user only | log-file | `dotnet-counters` | `.nettrace` → `export-needed` |

All detectors expand only `%SystemDrive%`, `%windir%`, `%ProgramData%`, `%ProgramFiles%` and the config file's own directory. Any other environment variable or layout token makes the value `config-unparsed`.

---

## 6. UI flow

Entry: a "This computer" button on `Landing.svelte` and in `CaseTools.svelte` next to "Import file". Hidden with the text "Scanning is available on Windows only." when `GET /api/discover/capabilities` says unsupported.

| Step | Screen | Copy (linted, plain, no causal words) |
|---|---|---|
| 1 | Panel intro | "Unravis can look on this computer for logs and data it can import. It reads file names and sizes first. It opens nothing until you choose." Button: "Scan this computer". Header badge: "Running as administrator" or "Running as a standard user". |
| 2 | Scanning | Progress per detector: "Checking IIS", "Checking event logs", ... Cancel button. |
| 3 | Checklist, grouped (Web server, Windows, App logs, Databases, Live) | Row: display name, what it shows (`Shows` wording), size, newest entry age ("updated 3 min ago"), file count, tags "Needs administrator", "Large", "Export needed". Large rows have no default tick. Gap rows grey with reason. |
| 4 | Window | "Which time range?" Options: incident window (if set, default), last 1 h, 6 h, 24 h (default without incident), 7 days, custom. Line under it: "About 1.2 GB will be read." (sum of window slices). |
| 5 | Notice | Existing gating banner and ack label, unchanged. |
| 6 | Import | Per-row progress from import jobs; result per row: "Added 41,200 rows", "Nothing new to add" (the server answers 409 duplicate both when the range was imported before and when it holds no rows), "Refused: the file format did not match", "Needs administrator". |
| 7 | Live sampling | Toggle "Record CPU and memory every 15 seconds". Help: "Records process names and numbers only. Stops after 72 hours." Status: "Recording since 08:00, 1,440 samples." |

Empty and error states:

| State | Copy |
|---|---|
| Nothing found | "No logs were found in the usual places. You can still import files by hand." |
| All denied | "Most sources need administrator rights. Close Unravis and start it with Run as administrator." |
| Not found (one row) | "IIS logging is set to a folder that does not exist." |
| Export needed | "This data is stored in a format Unravis does not read. Export it first:" plus the step. |
| Unsafe path | "Skipped: the path points somewhere Unravis does not follow." |
| Scan expired | "This scan is over an hour old. Scan again." |

All new strings go in `web/src/lib/copy/discoverCopy.ts` and `internal/discover/wording.go`, both covered by `copylint` (grade <= 8.5, CAUSAL_PATTERNS, DIAGNOSIS_WORDS) and a golden file like `collect/testdata/wording.golden.json`.

---

## 7. API additions

All routes: loopback listener, `X-Audit-Token`, existing Origin check, JSON errors via `writeCaseFailure(status, reason, text)`.

| Method, path | Body / query | Response | Notes |
|---|---|---|---|
| `GET /api/discover/capabilities` | | `{supported, elevated, os}` | |
| `POST /api/discover/scans` | `{}` | `202 {scanId}` | async; one running scan per process (`409 scan-busy`) |
| `GET /api/discover/scans/{scan}` | | `{state, startedAt, findings:[Finding JSON], gaps:[Gap JSON], host:{label, proposed, tz, cores, aliases}}` | Finding JSON omits paths; `scan` is 32 hex |
| `DELETE /api/discover/scans/{scan}` | | `204` | drops the in-memory scan |
| `POST /api/cases/{id}/discover/imports` | `{scanId, findingIds[], window:{from,to}, ackGating, ackText}` | `202 {jobs:[{findingId, jobId}]}` | same case gate, import slot limiter (`429 import-busy`, Retry-After 2), job model, TTL and 64-job cap as `/imports?async=1`; reuses `importFile` with the local reader as `part` |
| `GET /api/cases/{id}/imports/jobs/{job}` | | existing | unchanged |
| `POST /api/cases/{id}/sampler` | `{intervalSec, ackGating, ackText}` | `202 {state}` | one sampler per process (`409 sampler-busy`) |
| `GET /api/cases/{id}/sampler` | | `{state, since, samples, stopReason}` | |
| `DELETE /api/cases/{id}/sampler` | | `204` | flushes the last batch |

Limits: `findingIds` <= 64 per call; `window` <= 31 days; `ackText` <= `MaxAckText`. Unknown fields refused.

Finding as the client sees it (no path field exists in the JSON type, so it cannot leak by accident):

```json
{
  "id": "k3m7q2x5v8b4n6c9d2f5g7h3j4",
  "detector": "iis",
  "kind": "log-set",
  "importer": "iis-w3c",
  "host": "local",
  "display": "W3SVC2 (u_ex010911.log and 13 more)",
  "confidence": 90,
  "shows": "discover.iis.shows",
  "sizeBytes": 1288490188,
  "files": 14,
  "oldest": "2001-08-29T00:00:00Z",
  "newest": "2001-09-11T08:41:00Z",
  "needsAdmin": false,
  "cost": "large"
}
```

Gap:

```json
{ "detector": "sql", "kind": "log-set", "display": "SQL Server error log", "reason": "needs-elevation" }
```

Import request:

```json
{
  "scanId": "9f1c2e7a4b6d8f0a1c3e5a7b9d1f3a5c",
  "findingIds": ["k3m7q2x5v8b4n6c9d2f5g7h3j4"],
  "window": { "from": "2001-09-11T06:00:00Z", "to": "2001-09-11T10:00:00Z" },
  "ackGating": true,
  "ackText": "<GATING_BANNER text>"
}
```

### 7.1 Local host entry

On the first scan the server matches `Machine.Names()` (NetBIOS and DNS names, e.g. `WEB-A`, `web-a.example.test`) against `hosts.json` aliases. Match: use that label. No match: propose the label `local` (so no machine name enters a label), with the names stored as aliases in the sealed `hosts.json`, TZ from registry `TimeZoneKeyName` mapped through `clock.windowsZones`, `LogicalCores` from `GetActiveProcessorCount`, Role guessed (`iis` when IIS sites exist). The UI shows it in `HostsEditor` for confirmation before the first import. Imports pass `HostHint{Label, TZ, LogicalCores}`; the existing tz-mismatch rule (`ReasonTZMismatch`) still quarantines files whose own zone disagrees.

---

## 8. Phasing

Model tier follows the project rule: Sonnet for normal build tasks, Haiku for small mechanical ones, Opus only for final review. "Parallel" means safe in its own worktree at the same time as the other tasks marked y in the same phase.

| ID | Task | Files | Interface / output | Tests | Tier | Parallel |
|---|---|---|---|---|---|---|
| P1a | discover core | `internal/discover/{discover,env,paths,wording}.go`, `fake/`, `winenv_*.go` | `Detector`, `Env`, `Finding`, `Gap`, `Run`, `ValidLogPath` | path validator table (UNC, ADS, 8.3, `..`, non-ASCII), Run timeouts and panic recovery, dedupe of shared dirs, not-supported-os stub | Sonnet | y |
| P1b | local reader | `internal/discover/localread/` | `Open(canonical, expect FileID) (io.ReadCloser, Info, error)`, range + header replay, watermark store | reparse refusal, swap detection, frozen length on growing file, share flags (open while another handle writes and deletes), byte-rate limit | Sonnet | y |
| P1c | IIS detector | `detect_iis.go`, `configparse/apphost.go` | findings for iis-w3c, iis-httperr, ancm-stdout | fixture applicationHost.config with `SENTINEL-7f3a` in passwords and bindings; two sites one dir; missing dir gap; denied gap | Sonnet | y (after P1a interfaces merged) |
| P1d | event channels | `detect_events.go`, `evtx/query_windows.go` | channel findings; CSV rows for win-events; record-ID watermark | fake channel list; real query test behind `windows` tag on Application with a 1 h window; Message never present in output | Sonnet | y |
| P1e | live sampler | `internal/sampler/`, `internal/importer/winlive/`, `resource/metrics.go` rules, `procsnap.ProcessLabel` | sampler start/stop, 5-min batches, win-live importer | carry-over CPU across batches, cap stop, upload of win-live refused, catalogue series appear, benchmark budget | Sonnet | y (touches procsnap: coordinate with importer agents) |
| P1f | API routes + audit | `internal/server/discover.go`, `routes.go`, casestore `discover_reads`, `discover_watermarks` tables | routes in section 7 | token required, path field refused, foreign finding ID refused, expired scan, 429 busy, audit row has HMAC and no path | Sonnet | n (after P1a, P1b) |
| P1g | UI "This computer" | `web/src/lib/components/DiscoverPanel.svelte`, `discoverCopy.ts`, Landing/CaseTools entry | section 6 | copylint, vitest for states, Playwright smoke on demo with fake scan | Sonnet | y (against mocked API) |
| P2a | app-logger configs | `detect_applog.go`, `configparse/{xml,json}.go` | log4net, NLog, Serilog, MEL findings | `SENTINEL-7f3a` everywhere except path keys; layout-renderer refusal; `appsettings.Production.json` override order | Sonnet | y |
| P2b | TimeProber + window everywhere | `internal/importer/prober.go`, per-importer `ProbeTime`, Window in iis-w3c/win-events/serilog/nlog/log4net | binary-search seek | 40 GB sparse synthetic file finds window in <= 64 probes; files without prober tail-import with gap | Sonnet | y |
| P3a | Redis/Mongo/SQL file detectors | `detect_redis.go`, `detect_mongo.go`, `detect_sql.go` | redis-log, mongod-json, sql-errorlog findings; XE/Agent export-needed | `requirepass SENTINEL-7f3a`, `keyFile` with marker, `-e` parsing with odd quoting | Haiku | y |
| P4a | nginx/Apache, FREB, perfmon CSV, dotnet-counters, App Insights/Hangfire hints | `detect_web.go`, `detect_host.go` | findings per section 5 | fixtures per source | Haiku | y |
| P4b | live probes (Redis INFO/SLOWLOG, Mongo serverStatus) | held | | | | held on owner ruling (D8, R13) |
| P4c | scheduled-task wrapper | held | | | | held until analysts ask (D4) |

Merge order: P1a, then P1b-P1e and P1g in parallel, then P1f, then a clumsy-user review (project rule), then P2, P3, P4a.

---

## 9. Testing plan

### 9.1 Unit and contract tests (any OS)

- Every detector runs against `fake.Env` fixtures; golden JSON of findings and gaps per fixture.
- Contract test over all registered detectors: an access-denied error from any fake call must produce a `needs-elevation` gap; no finding may carry a path that fails `ValidLogPath`; every `Importer` exists in `importer/all`.
- Secret sweep: `SENTINEL-7f3a` fixtures for every config parser; assert absence in findings, gaps, server JSON, captured `slog` output, audit rows and the case DB file bytes.
- Copy: golden wording file + `copylint`.
- Non-Windows build: `GOOS=linux go build ./...` and `go vet` in CI; `NewSystemEnv` returns `ErrNotSupportedOS`; routes answer `{supported:false}`.

### 9.2 Clumsy-user list

| Case | Expected |
|---|---|
| Non-admin user | Scan completes; IIS/HTTPERR/SQL rows are `needs-elevation` gaps; event Application/System found; sampler shows partial note. |
| Log dir missing | `not-found` gap naming the site ID. |
| Junction loop under a log dir | Walk refuses reparse points; one `unsafe-path` gap; scan finishes inside the timeout. |
| 40 GB IIS log | `Cost=large`, no default tick; window slice via prober; never reads more than cap. |
| File locked by the writer and growing | Opens with share-all; range ends at last full line at open; next scan imports only the new tail. |
| web.config with a connection string | Path extracted; marker absent everywhere. |
| UNC log path in config | `unsafe-path` gap; nothing read. |
| Non-ASCII path (`D:\Журналы\web-a`) | Found and read; display name intact; HMAC stable. |
| Two sites sharing one log dir | One finding, both site IDs shown, files imported once. |
| App pool recycled mid-scan | New w3wp PID appears in the sampler as a new identity with start marker; scan unaffected. |
| Scan run twice, import twice | Second import: whole files refused "Nothing new to add"; active files import only the new range; counts do not double. |
| Finding ID from a stale scan | `404 finding-unknown`. |
| File swapped for a junction between scan and import | `unsafe-path`, audit row records the refusal. |
| Case closed while sampler runs | Sampler flushes and stops; status "stopped: case closed". |
| Disk nearly full | Import fails with the existing case-cap error; sampler stops at its cap or on write error, never retries in a loop. |

### 9.3 Only verifiable on a real server

- Actual ACLs on IIS, HTTPERR, SQL and Redis/Mongo folders for a standard user vs admin on Windows Server 2016/2019/2022/2025.
- Event channel read rights for interactive users and Event Log Readers members; WAS AppPoolID render path.
- Sampler CPU and memory with real process counts (target < 1% CPU, < 50 MB) and that w3wp/sqlservr query works when elevated.
- Share-all open does not disturb IIS log rollover, NLog archiving or SQL ERRORLOG cycling.
- Background I/O priority effect under real disk load.
- Real config variants (transformed web.config, environment-specific appsettings, log4net in separate files) parse to the right paths.
These go on the held-items list with owner "analyst on a real server", reported as counts and reason codes only.

---

## 10. Risks, limits and rulings

| Risk / limit | Handling |
|---|---|
| Detectors miss non-default layouts | Show "not found" honestly; manual upload stays. Confidence shown per row. |
| ACL behaviour differs by Windows version | Every denial is a typed gap; no silent skip; verify list in 9.3. |
| Reading on a hot production box adds load | Background priority, rate cap, one read at a time, window slicing; numbers verified on a real box before any claim. |
| Sampler data can never be deleted except with the case | Hard cap and default off (D6). |
| An analyst uses a non-admin account and assumes full coverage | Header badge and gap rows make the account level visible; the collect suggestions list stays as the coverage view. |
| Path HMAC is keyed per case, so the same file in two cases does not link | Accepted; linking cases is out of scope. |
| Config extraction is a parser surface on attacker-influenced files | Streaming parsers, 4 MiB cap, depth cap 64, no entity expansion (Go `encoding/xml` does not expand external entities; DTDs refused), fuzz tests on each parser. |

Recorded rulings:

- Ruling: the scan never reads log content beyond a 64 KiB head — why: the user has not consented to import yet, and the head is the same amount uploads already sniff — cost if wrong: freshness is from mtime, which can be stale for buffered writers; acceptable.
- Ruling: no "import everything" button — why: per-finding consent is the safety model and large sources need a window — cost if wrong: a few extra clicks.
- Ruling: local host label defaults to `local`, not the machine name — why: labels reach the case and exports, machine names must not — cost if wrong: users rename it in HostsEditor, one step.
- Ruling: `win-live` is a separate source, not procsnap — why: procsnap means "a file the user exported", and coverage and audit should tell the two apart — cost if wrong: two catalogue entries per metric; small.
- Ruling: wevtapi over wevtutil (D5) — why: no child process, no message text, no command-line exposure — cost if wrong: fall back to the export recipe.
- Ruling: no service in v1 (D4) — why: DPAPI per user, token in user session, attack surface — cost if wrong: P4c wrapper.
- Ruling: live database probes held (D8) — why: credentials must come from a person, never a config — cost if wrong: analysts keep using the export recipes.
