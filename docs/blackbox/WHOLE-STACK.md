# Blackbox: collect the whole stack, change it remotely

Date: 26 Nov 2026. Read from `develop` at `cbe4e63`. Nothing here is built. Nothing was run on a real server today.
Replaces the open parts of `/mnt/project-files/blackbox-collect-everything/PLAN.md` (23 Nov). That plan's tables still hold; this one adds Redis, remote settings, auto-detect, bundles.

## 1. Remote access: what the PuTTY host key is

- **No key file was needed because the Salt master takes a password.** The tool logged in with password only (verified live 26 Nov: `test.ping`, `grains`, log reads on 27 minions). Nothing to download. The tool never fetches keys.
- The thing PuTTY showed on first connect is the **server's host key fingerprint** (a "do you trust this server" prompt). The tool pins it in your private `.env` (`SSH_HOST_KEY_FINGERPRINT`) and refuses a server that differs. Not a secret, not yours.
- A client key file (`.ppk`) would be optional and safer than a password. Not needed now. Skip.
- **Direct connection to the servers: parked.** Not needed. Path stays: your PC, SSH to the Salt master, Salt to the server. The Blackbox on the server talks to that server's own SQL and Redis on localhost. Nothing connects from your PC to SQL or Redis.

## 2. What the Blackbox collects now

Host recorder (service `UnravisBlackbox`, LocalSystem, every 15 s, 7 days, 2 GB cap):

| Area | Group | State |
|---|---|---|
| CPU, memory, commit, disk queue and ms, network, disk free | base | built, live |
| Top 20 processes, per-process IO, top-10 detail, handles, threads | `procs`, `proc10`, `procIO` | built |
| Memory pressure, disk and CPU detail | `memPressure`, `diskCpu` | built |
| TCP, HTTP.sys, IIS, ASP.NET, CLR (GC) | `tcp`, `http`, `iis`, `aspnet`, `clr` | built; `aspnet` fails on BG-SRV-UK-01 (BL-604) |
| Windows events | `events` | built; fails on BG-SRV-UK-01 (BL-604) |
| Crash dumps | `dumps` | built; crash capture for the recorder itself merged (PR 374) |
| VM steal and ballooning (VMware) | `vm` | built, probe `vm-probe` |
| SQL host counters (13, no login) | `sqlHost` | built, live |
| SQL ERRORLOG as a file (numbers, 4 kinds) | `sqlLog` | built, never run on Windows. `system_health` .xel reader missing (needs a real sample file) |
| Log file catalogue (names, size, time) | `logs` | built, content never read |
| Snapshot (copy recordings to `snapshots\`) | CLI `snapshot` | built |
| Watch list (app pools, services, up to 32) | `targets` | built |

SQL service `UnravisBlackboxSql` (own Windows service, virtual account, opt-in): 7 Standard samplers (requests, waits, schedulers, semaphores, tempdb, filestats, ag) plus about 20 "Everything" extras (perfctr, mem, conns, trans, waiting, agdetail, locks, facts, grants, workers, pendingio, latches, rg, qhash, qtext). Also built: `setup-sql`, `set-sql-login` (sealed), query hash, scrubbed query text opt-in. **All unit-tested on Linux. None verified on a live SQL Server** (BL-141, BL-551, BL-552, BL-555, BL-548, BL-549, BL-550).

**Not in the Blackbox at all:** Redis, Mongo, remote settings, bundles for pulling, auto-detect of SQL. SQL is not installed on BG-SRV-UK-01 (BL-604).

## 3. Gaps

| # | Gap | Why it matters |
|---|---|---|
| G1 | SQL deep collection is opt-in and CLI only. Nothing detects SQL and says "collection is off, here is why". | On BG-SRV-UK-01 no SQL stats exist. |
| G2 | No Redis collector. Redis log import exists in the main app, nothing live. | Redis is on the stack. |
| G3 | Settings change only on the server: `apply` with a local file. No remote verb. The remote tool is read-only (write mode: restart one service). | You cannot tune a Blackbox from your PC. |
| G4 | No way to add a collector without a new release. | You want to add steps. |
| G5 | `aspnet` and `events` fail on a real server, reason unknown. Status file names it. | Two groups blind. |
| G6 | A snapshot is a folder on the server. No bundle with a manifest, no pull path. | Evidence stays on the server. |
| G7 | Many things are neither collected nor recorded as a path (see section 6). | You asked for "at least the path". |

## 4. Proposal

### 4.1 One collector list, everything addressable by id

Every source is a **collector** with an id (`host.tcp`, `sql.waits`, `redis.slowlog`, `path.iislogs`), a kind, an interval, a load tag and a rights need. `blackbox.json` gets one `collectors` map: `{ "redis.slowlog": {"on": true, "everySec": 60} }`. Existing keys (`record`, `sql.profile`, `sql.extras`, `targets`) keep working and are mapped to ids. Kinds:

- **counter**: PDH path. A new `counters.extra` list lets you add any Windows counter by path with no release. Same source as Perfmon.
- **dmv**: fixed compiled SQL statement (existing rules: one SELECT, row cap, time limit, lint).
- **command**: fixed read-only protocol call (Redis INFO and the like). Allowlist in code, never from settings.
- **file**: read a file with caps (ERRORLOG, redis log tail counts).
- **path**: record only the path, size, time, exists. Never content. This is the "at least the path" kind.

New collectors for a new technology still need a release. What settings can add with no release: counters, paths, which collectors are on, intervals, which logs to catalogue. That is deliberate: settings can never add code or a command.

### 4.2 SQL: detect, check access, then start

1. Recorder finds SQL services (it already does for `sqlHost`). Status shows "SQL found: 2 instances, deep collection off".
2. One check, read-only: can `NT SERVICE\UnravisBlackboxSql` connect and is it inside the rights ceiling? Result in plain words: **ok / grant missing / not installed**.
3. If **grant missing**: show the grant script (never run by the Blackbox, existing rule C14). With Salt you run it yourself, or paste it in SSMS.
4. If **ok**: with `sql.auto: true` the SQL service starts at the **Everything** profile. Without it, one command (`unravis-blackbox sql-enable`) does install, check and start.
5. Deep (query hashes) and query text stay separate ticks, off.

Recommend: `sql.auto` defaults **on in new installs** (you said "almost everything"), off for existing ones. The SQL service is installed stopped by the setup when SQL is found, so "auto" only flips a switch the owner already allowed by installing. A Blackbox that installs a service by itself would break rule C14, so it does not. **Needs Opus security review.**

### 4.3 Redis (new, in the host recorder, local only)

- **Detect**: Windows service (Redis, Memurai), process, listening port, `redis.conf` path. Records the paths even when nothing else works.
- **Connect**: `127.0.0.1` and the detected port only. Own tiny RESP client, no library, **only an allowlist of commands**, refuses anything else in code.
- **Auth**: most local Redis has none. If a password is needed it goes into the same sealed store as the SQL login (machine scope, never in `blackbox.json` or a pack). Redis 6+: a read-only ACL user with only the commands below.
- **Collects** (numbers and fixed names only):

| Id | Command | Every | What |
|---|---|---|---|
| `redis.info` | `INFO` (server, clients, memory, stats, replication, persistence, commandstats, keyspace) | 15 s | ops/s, memory, fragmentation, evictions, hits and misses, connected and blocked clients, rdb/aof state, replica lag, per-command call counts and time |
| `redis.slowlog` | `SLOWLOG GET` (cursor by id) | 60 s | time, duration, **command name only**. Arguments and keys dropped (secrets, personal data). Tick to keep first argument, scrubbed |
| `redis.latency` | `LATENCY LATEST` | 60 s | spikes by event. Redis has it off unless `latency-monitor-threshold` is set; we never change that |
| `redis.clients` | `CLIENT LIST` counts | 60 s | counts by flags and idle bucket. No addresses |
| `redis.config` | `CONFIG GET` fixed list | daily | maxmemory, policy, appendonly, save, dir, log path. **Never** `requirepass`, `masterauth` or anything with a password |
| `path.redis` | files | 5 min | `redis.conf`, log, rdb/aof: path, size, mtime |

- Redis for Windows is old (3.x) or Memurai. Some commands are missing on old builds. Each command reports "not supported" and is skipped, never faked. **Not verified: no Redis was contacted.**

### 4.4 Remote settings (CLI first, Salt second)

The Blackbox gets a small command set. Same JSON output as the existing `--json`.

| Command | Does |
|---|---|
| `unravis-blackbox config show --json` | Settings now, scrubbed, plus the collector list with on/off and reason |
| `unravis-blackbox config set <id> on\|off [--every 60s]` | Change one collector |
| `unravis-blackbox config set counters.extra add '<pdh path>'` | Add a counter (path charset checked) |
| `unravis-blackbox config validate <file>` | Dry run |
| `unravis-blackbox config history` and `rollback` | Last 20 changes, undo the last |

Rules: change is checked, written through the existing locked-file path (BL-139 owner rule), applied with a service restart, and **rolled back by itself if the recorder does not report healthy in 60 s**. Every change is one line in a hash-chained audit log (same design as OPS-09). No new listener. Needs local admin or SYSTEM.

From your PC: the remote tool gets one new write action, `BlackboxConfig`, that can run only those exact verbs. It reuses the existing gate: `WriteRequest` shows the exact command and hash, `WriteApprove` runs it, `REMOTE_MODE=write`. Reads (`config show`, `status`) work in read-only mode. **Salt states are still never applied.** Needs Opus security review. Weak point stated in `REMOTE-SALT.md` stays: the approve gate depends on the operator.

### 4.5 Snapshot and bundle

`unravis-blackbox bundle --since 2h --out <name>` makes one zip in `snapshots\`:

- the recordings in the window (hourly NDJSON, SQL files), `blackbox.json` **scrubbed**, status and run history
- `facts.json`: versions, OS, services and their state, install paths, found logs, drive layout, time sync state
- scrubbed tails of the ERRORLOG and Redis log (last N KB, secrets scrubbed)
- redundancy: a manifest with sha256 of every file, a second copy of the manifest and `facts.json` outside the zip

Auto-snapshot (opt in): when the recorder sees a trigger (crash dump written, service restart, low disk, SQL blocking over N minutes) it makes a small bundle. Capped to 5 and 500 MB.

**Pull**: the existing read-only Salt tool reads files only inside allowed folders, as text. A zip is binary and can be large. Proposal: a `LogPull` recipe that reads the bundle in base64 chunks (about 64 KB each) with sha256 check at the end, only inside the `snapshots\` folder. **Not verified**: Salt master limits on output size, speed over your link, whether 100 MB is practical. First test: pull a 5 MB bundle from one server. Fallback if too slow: bundle goes to a share you name.

### 4.6 Live view

Everything lands in the same hourly NDJSON and the existing charts (`Open charts`, Perfmon-style, log scale).

### 4.7 Timeline contract (nachi 26 Nov: the investigation timeline is the primary starting point; every collector is a timeline source)

**What the timeline reads today.** Importers emit `evidence.Event` (`internal/evidence/event.go`): `Source` (importer name), `Kind`, `Host`, `Start`/`End` in UTC, `DurationMs`, `Value`, `Unit`, `Severity`, `Line` (provenance), `TZRule`, up to 16 allowlisted `Attrs`. Time rules live in `internal/clock`: `utc-field`, `offset-in-field`, `header-bias`, `host-local`. Importers exist already for `sql-errorlog`, `sql-xe`, `sql-querystore`, `sql-pdh`, `redis-info`, `redis-slowlog`, `redis-latency`, `redis-log`, `win-events`, `win-counters`, `iis-*`. The Blackbox itself feeds the timeline only through the **host lane** (CPU, disk ms, top processes; `internal/investigate/hostlane.go`). SQL service samples, `sqlLog`, TCP, HTTP, IIS, CLR and the rest are not read by the timeline yet.

**Rules for every new collector, so the timeline reads it directly:**

1. **Same names as the importers.** A collector id maps to one source name, and where an importer exists the Blackbox uses its name: `sql.errlog` is `sql-errorlog`, `redis.slowlog` is `redis-slowlog`, `redis.info` is `redis-info`, `redis.latency` is `redis-latency`, `host.events` is `win-events`, `host.counters` is `win-counters`, SQL perf counters are `sql-pdh`. New names follow `<tech>-<what>` in lowercase (`sql-waits`, `sql-requests`, `iis-apppool`). One name per source, never renamed (cases store it).
2. **One reader, not a new format per collector.** A single `blackbox-source` reader turns the hourly NDJSON into `evidence.Event` rows. Each collector declares once, in code: source name, `Kind` list, `Unit`, severity rule, allowed `Attrs`. The same declaration drives charts, so a counter cannot be on a chart and missing from the timeline.
3. **Time is UTC at write time.** The recorder stamps `t` in UTC from the host clock (`utc-field`). Nothing is stored in local time. Each hourly file's header repeats the host's IANA zone name and its UTC offset **at the time of the file**, so a later reader can show local time and survive a DST change. Source times that are local (ERRORLOG, Redis log, IIS with local logging) are converted **on the server at collection**, using the host's own zone, and the event keeps `TZRule` = `host-local` plus the zone it used. The timeline never asks you to pick a zone for Blackbox data.
4. **Missing years.** Redis for Windows writes `26 Nov 13:47:27.070` with no year (BL-614). The collector resolves it on the server: the year is the one that puts the line closest to, and not after, the collection time. Lines more than 11 months old when read are dropped and counted, never guessed. Same rule for any year-less log. The reader marks `TZRule` and a `yearInferred` attribute so the timeline can show a hint.
5. **Clock honesty.** Every sample carries the recorder's own clock-health bit (time source, last sync age, offset if known: the `w32tm` collector in section 6). A server whose clock is off by more than 2 s gets a banner on its lanes, and events are shifted **never**, only flagged.
6. **Event vs series.** Two shapes only. **Series** (counters, INFO, DMV samples): a value per interval, drawn as a lane or chart. **Event** (slowlog entry, deadlock, error number, service crash, dump, restart, recycle): a point or a span with `Start`/`End`. A collector says which one. Series can raise events (a threshold the owner sets, or a wait that crossed N seconds) so the timeline shows "SQL blocked 40 s here".
7. **Host and identity.** `Host` is the hosts.json label (same mapping the importers use). The Blackbox writes its own server label once at install, so a bundle from a server names itself the same way every time.
8. **Join keys.** Events carry what the timeline can join on without text: SQL query hash, Redis command name, app pool, process id plus start time, database id. Never text keys. The existing join classes (`unique`, `ambiguous`, `none`) apply as is.
9. **Gaps are data.** A missing sample is "no reading", never zero (existing rule). The reader emits an explicit gap span when more than 3 intervals are missing (the rule `hostLateIntervals` already uses), so the timeline shows grey, not a flat line.
10. **Pull is the same shape.** A bundle (4.5) is read by the same reader. So a case can use a live Blackbox (read key, port 7788), a pulled bundle (Salt), or a copied folder, with identical events.

**Not verified:** how the timeline lanes would show about 40 collectors at once. That is the timeline's own design question (lane grouping, default-on set). Proposal: default lanes are host, SQL waits and blocking, IIS, Redis; everything else under "More sources" in the picker. Decide at B11 with a prototype.

## 5. What stays fixed

- Read-only. Local only (SQL and Redis on the same server). Fixed statements and fixed commands. Settings cannot add code.
- Numbers and fixed names. No query text, no Redis keys or values, no plan XML, unless an opt-in tick says so, and then scrubbed.
- No secret in `blackbox.json`, a pack, a bundle, a log, status, or chat. You never paste a connection string in chat.
- Time limit, row cap, back-off and auto-off on every collector. Total budget stays under 1% of one core.
- Housekeeping stays: age cap, byte cap, free-space floor. A new collector cannot turn it off.

## 6. Other things worth collecting, or at least the path

Level: **N** numbers, **P** path only, **F** file facts (size/time/hash), none reads content.

| Item | Level | Why | Notes |
|---|---|---|---|
| IIS site, binding, app pool settings (recycle, identity name, runtime, queue length, limits) | N, P | explains restarts | identity names scrubbed on request |
| IIS logs folders, HTTPERR, FREB (failed request tracing) folders | P, F | the logs you read later | exists as catalogue, add HTTPERR and FREB |
| Windows Service Control Manager changes: crash, restart, start-failed | N | pairs with events | via the events group once BL-604 is fixed |
| Scheduled tasks: last run result, next run | N | silent failures | names only |
| Certificates: subject, thumbprint, expiry date for local machine store | N | expiry outages | no private keys, ever |
| Pending reboot, last boot time, hotfix list | N | "what changed" | |
| Time sync state (`w32tm`: offset, source) | N | log correlation across servers | |
| .NET: installed runtimes, per-process runtime counters | N | exists as `clr`, `diag-probe` | EventPipe is BL-110 |
| Application logs (config `logPaths`) | P, F | | exists |
| Windows Error Reporting dump folders | P, F | | `dumps` exists |
| SQL: ERRORLOG, `system_health` .xel, default trace, SQL Agent log, backup folder path, data and log file paths | P, F, N | | ERRORLOG built; rest path-only first |
| Redis: conf, log, rdb, aof | P, F | | section 4.3 |
| Mongo (phase 3): serverStatus, replica lag, current operations, `mongod.log` path, `mongod.cfg` path | N, P | needs sealed credentials | `clusterMonitor` role only |
| Salt minion: version, log path, last highstate time (read of a file) | P, F | tells what Salt changed | never state content |
| Unravis app and Blackbox own logs, update history | P, F | | exists in part |
| Network: DNS server list, resolve time of named hosts (from config) | N | DB or Redis "slow" that is really DNS | host list from settings, fixed query type |
| Disk: volume list, free, per-volume queue and latency | N | | exists |
| Firewall and hosts file | F (hash only) | "who changed it" | content never |

## 7. Order of work (each a Sonnet thread unless marked)

| Step | What | Review |
|---|---|---|
| B1 | Collector registry and `collectors` map, ids for every existing group, `config show --json` | Sonnet |
| B2 | Find why `aspnet` and `events` fail on BG-SRV-UK-01 (BL-604). Read the recorder status file through Salt first | Sonnet |
| B3 | SQL detect and access check, `sql-enable`, `sql.auto`, status words, grant text | **Opus** |
| B4 | Redis collector, sealed password, allowlisted RESP client | **Opus** |
| B5 | `config set\|validate\|history\|rollback`, auto-rollback, audit chain | **Opus** |
| B6 | Remote tool `BlackboxConfig` write action and `LogPull` | **Opus** |
| B7 | `counters.extra`, path-only collectors, facts.json | Sonnet |
| B8 | `bundle`, auto-snapshot triggers | Sonnet |
| B9 | UI: stack found, collector list, remote change history (prototype first) | Sonnet + UI audit |
| B10 | Live proof on BG-SRV-UK-01 through Salt, read-only first | you watch |
| B11 | `blackbox-source` reader: Blackbox files into timeline events (section 4.7), host zone header, year inference, gap spans. Timeline lane picker prototype | Sonnet, Opus for time handling if it touches the join |

B1 and B11 share the collector declaration, so build them together. B1, B2, B7 can start in parallel. B3 to B6 are security work and go one at a time with Opus review.

## 8. Not verified

- No SQL Server, Redis or Salt write path was touched today. All SQL extras are unit-tested on Linux only.
- Redis command support on old Windows builds is from memory of the docs, not tested.
- Salt output limits for pulling a zip are a guess.
- The DMV and Redis lists come from my knowledge, not a web check today. Each new statement needs a check against Microsoft Learn and the Redis docs before build.
- I did not read BG-SRV-UK-01's recorder status file. That is B2.

## 9. Decisions I need from you

1. **`sql.auto` on by default for new installs?** Recommend yes.
2. **Remote write through Salt (B5, B6), with the approve gate and auto-rollback?** Recommend yes, one verb at a time.
3. **Redis slowlog keeps command name only; arguments off?** Recommend yes.
4. **Pull bundles through Salt in chunks, test with 5 MB first?** Recommend yes.

I decide the rest myself.
