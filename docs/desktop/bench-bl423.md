# BL-423 memory tuning and BL-424 idle audit: what was measured

Linux numbers from `internal/perfbench` `TestBenchOpenCaseFootprint` (set `UNRAVIS_BENCH=1`; it runs
on Linux and Windows). The Windows working-set numbers the acceptance asks for (largest real case, 2 GB
VM, 60 s idle soak with the real app) are not measured here; see the end.

Engine (`go list -m`): `github.com/ncruces/go-sqlite3 v0.38.0` with `go-sqlite3-wasm/v6 v6.7.35401`
(SQLite compiled to Go, no wazero). The memory the database uses sits outside the Go heap (heap in use
stays 5 to 20 MiB while the resident set reaches 100 to 230 MiB), so the pool and page cache size are what
bound it, not the GC settings.

## The standard run

Import a synthetic counters case (200 counters, 6 h at 15 s, 285 k events, 149 MiB case file), then open it the
way four charts do: four concurrent `/series` requests (10 selectors each, 5 minute buckets), three
rounds. The case is closed between requests (`openCase` closes it), as in the app.

## Result

Two runs each, same machine, 4 cores, alternating before and after.

| | before (cache 64 MiB x up to 4, 2 idle) | after (cache 16 MiB read, 64 MiB import conn dropped after use, 2 idle, 30 s idle close) |
|---|---|---|
| peak resident during the four chart requests | 226 to 231 MiB | 112 MiB |
| resident after the requests | 40 MiB | 39 to 41 MiB |
| request phase wall (12 requests) | 2.2 to 2.8 s | 2.6 to 2.9 s (from the same up to about 30 percent slower in the worst pairing) |
| import of the 285 k events | 27.2 k and 27.6 k events/s | 29.1 k and 26.5 k events/s |
| idle context switches per minute (rig: handler plus store, nothing open) | 8 and 12 (30 s window) | 8 and 24 (30 s window; noise of a few counts) |

Real `run()` (`--pipe-server --loopback --demo`, `TestIdleWakeups`, 20 s idle): 15 to 18 context switches per minute,
3 goroutines afterwards. The counter is /proc context switches, voluntary and preemptions, of all threads, so it is an upper bound for wakeups. That includes the Go runtime's own sysmon and netpoll; the application adds none.

## Decisions taken from the measurements

- Page cache of the pooled (read) connections: 64 MiB to 16 MiB (spec 3.2 default). Sweep, one run each, taken
  while other jobs shared the machine, so the times are rough: peak resident / request-phase wall: 8 MiB
  84 MiB / 3.3 s; 16 MiB 120 MiB / 4.2 s; 64 MiB 215 MiB / 5.0 s. The quiet before/after pairs above show 16 MiB
  against 64 MiB at 2.6 to 2.9 s against 2.2 to 2.8 s: from about the same up to about 30 percent slower in the worst
  pairing (the spread between runs is itself 10 to 20 percent), for half the peak memory. The spec rule
  (smallest value within 10 percent of the best p95) has to be decided on the owner's largest real case, which this
  Linux run cannot replace.
- The import connection keeps 64 MiB: with 16 MiB for everything, import throughput fell from about 24 k to
  about 14.5 k events per second (-40 %, three runs each), well past the 10 % rule in the spec. The import
  connection is thrown away when the import ends (`driver.ErrBadConn` from `Conn.Raw`), so the big cache is
  not kept in the pool.
- Pool: four connections stay (`TestReadsDoNotWaitForImport`); an idle connection is closed after 30 s. The
  idle size stays at the old 2, not the spec's 1 (see the next section).
- Soft memory limit: `clamp(RAM/16, 256 MiB, 512 MiB)`: 1, 2 and 4 GiB give 256 MiB, 8 GiB and up 512 MiB
  (`memtune.go`; RAM from `GlobalMemoryStatusEx` on Windows, the floor elsewhere). Spec 3.2 gave a formula
  (`min(RAM/4, 512 MiB)`) and a test table (256, 256, 256, 512, 512 MiB for 1, 2, 4, 8, 32 GiB) that disagree
  (the formula gives 512 MiB already at 2 GiB). The table was built because it matches the 2 GB VM in the
  acceptance. A `GOMEMLIMIT` set in the environment is respected (not overridden). Never below 256 MiB.
- GC percent 50 was NOT applied (spec 3.2 proposed it). Import with `GOGC=50` against `GOGC=100`, three runs
  each: CPU 10.8 s against 9.7 s (+10 %), wall +8 to +15 %, heap in use 9 to 15 MiB against 12 to 13 MiB. It
  costs CPU and saves nothing, because the Go heap is not where the memory is. A person's `GOGC` still wins.
- `PRAGMA shrink_memory` and an idle `debug.FreeOSMemory()` were not added: the case is closed per request, so
  there is nothing to shrink, and `FreeOSMemory` only moved the resident set by 5 to 9 MiB. The idle hook belongs
  to BL-425 (`power.Policy`), which must take its "in use" answer from `Updater.InUse` (one definition shared
  with the silent updater, BL-168 and BL-171); this change adds no second idle clock.

## Idle connections: 1 or 2, and what the reconnect costs

The app opens a case per request and closes it after (`Case.Close` shuts the pool down with the last handle),
so in the normal one-request case the pool's idle size and the 30 s idle close never come into play; the
earlier "first request after 35 s idle" row measured nothing and is removed. They matter only while some handle
stays open: an import, a recording, or overlapping requests. `UNRAVIS_BENCH_HOLD=1` holds one handle open
through the standard run, which measures that. Two runs each (idle 1 / idle 2), same four-chart run:

| | handle per request (normal) idle 1 | idle 2 | handle held open, idle 1 | idle 2 |
|---|---|---|---|---|
| peak resident | 107, 112 MiB | 111, 113 MiB | 113, 112 MiB | 111, 107 MiB |
| request phase wall | 2.10, 2.47 s | 2.20, 2.53 s | 1.73, 1.05 s | 0.98, 0.58 s |
| resident right after the requests | 39, 38 MiB | 41, 41 MiB | 60, 56 MiB | 73, 76 MiB |
| resident after 35 s idle (pool closed) | 39, 38 MiB | 41, 41 MiB | 43, 39 MiB | 40, 42 MiB |
| first request after the idle close vs warm | n/a (no pool kept) | n/a | 226 vs 175, 189 vs 161 ms | 165 vs 151, 188 vs 209 ms |

With a handle held, one idle slot made the overlapping chart requests close and reopen connections (each reopen
runs `initConn` and builds a new SQLite instance): 25 to 45 percent slower for the same peak memory, and the
second idle one costs about 17 MiB more only until the 30 s idle close. In the normal case the two are the same.
So the idle size stays at 2. A reconnect after the idle close costs about 25 to 50 ms on a first request.

## BL-424 audit: every ticker, timer and poll in the Go code

`internal/leakcheck/ticker_scan_test.go` parses all non-test Go files and fails on any `time.NewTicker` or
`time.Tick` whose period is not a constant of at least one second, unless the line carries
`// idle-ok: <reason>`. Result of the audit (nothing is sub-second at idle):

| Where | Interval | What it does | Stops when idle? |
|---|---|---|---|
| `internal/server/updater_auto.go` `RunAuto` | 1 min ticker (`autoTick`) | reads settings, returns unless an update is downloaded and apply-by-itself is on | no: one wakeup a minute for the life of the app on a release build; none on a development or demo build (`channel == ""`). Update chain file, not touched. |
| `internal/server/updater.go` `Run` | 60 s first delay, then the check interval (30 min default; 24 h when updates are off) | the update check | yes, long one-shot timers |
| `cmd/audit-explorer/health.go` | 30 s and 60 s one-shots | confirm start / recover cut-off update | yes, fire once |
| `internal/server/import_jobs.go` `cancelCaseUntilStopped` | 50 ms ticker | cancels a case's jobs while an erase waits for the case gate | yes, lives only during an erase (marked idle-ok) |
| `internal/casestore/sqlite/case.go` `lockImport` | 5 ms ticker | polls `TryLock` while a call waits for a running import or index build | yes, bounded by `importWaitMax` or the context (marked idle-ok) |
| `internal/sampler/sampler.go` | 5, 15, 30 or 60 s ticker | live recording | only exists while a recording runs (marked idle-ok) |
| `cmd/unravis-setup/main.go` | 15 s ticker | closes setup when its window is gone | setup program only (marked idle-ok) |
| `internal/blackbox/recorder.go` | configured interval, deadline timer | Blackbox sampling | separate service, not the tray app; one timer re-armed per interval |
| `internal/bbsql/runner.go` | interval, at least `BaseInterval` | SQL health ticks | separate service |
| `internal/blackbox/selfupdate.go` | 1 to 5 min first delay, then the check interval | Blackbox self update | update chain, not touched |
| `internal/blackbox/host.go` `serveWithRetry` | 30 s | retries the status port | only while the port is busy |
| `internal/server/discover_sampler.go` | 1 s `time.After` | waits for an import slot | only while a recording batch waits (bounded by `samplerSlotWait`) |
| `internal/server/archive_free.go`, `casestore/casefile.go` | 100 ms `time.After` | waits for a busy case / checkpoint | only inside one request, bounded by a deadline |
| `internal/casestore/sqlite/lifecycle.go`, `transient.go`, `blackbox/groups.go`, `blackbox/store.go` | 1 to 20 ms `Sleep` | bounded retry loops inside one call | yes |
| `internal/pipesec/listen_windows.go` | 100 ms `Sleep` | after a failed pipe accept only | only on an error path |
| Page timers (web) | 45 s presence ping, 10 min status, 30 s clocks | out of scope here (BL-168 owns presence) | page presence already skips while hidden |

Nothing needed rewriting. Reported, not changed (update chain, owned by the security/performance thread):
`RunAuto` keeps a one minute ticker alive although `autoStep` returns at once when automatic apply is off or
nothing is downloaded; it could sleep on the existing `wake` channel and arm a timer only while a manifest is
waiting. Cost today is one wakeup a minute.

## Spec items left out

- `TestTuneMemoryRespectsGOGCEnv` as written: the app does not set the GC percent, so there is nothing to
  override; a test that GOGC stays untouched replaces it. `TestTuneMemoryRespectsGOMEMLIMITEnv` is added.
- The CI job that sets `GOEXPERIMENT=goroutineleakprofile`: Go 1.27.3 here rejects that experiment and has no
  `goroutineleak` profile, so there is nothing to switch on; `leakcheck.Cycles` checks the profile by name when a
  toolchain has it. The workflow file was not touched.
- `internal/server/idle_audit_test.go` (goroutine profile of the handler at idle) and the `go/ast` scan in
  `internal/server/ticker_scan_test.go`: the scan lives in `internal/leakcheck` instead and covers `cmd` and all of
  `internal`; the idle goroutine count is checked by the `run()` leak test (at most 30) and by `TestIdleWakeups`.
- The tray cycles (`trayapp_test.go` fake deps): `run()` is started and stopped five times through the ui and ag
  pipes, which is the same lifecycle minus the tray package (Windows only).

## Leak tests (BL-424)

`leakcheck.Cycles` runs a start/stop cycle once as warm-up, takes the goroutine baseline, runs it five more
times and fails when the count after any cycle stays above the baseline plus 2 (it prints every goroutine
stack). When the toolchain has a `goroutineleak` profile, it must be empty too; Go 1.27.3 here has no such
profile (no `goroutineleakprofile` experiment), so that check is skipped by name lookup. Applied to:

- `cmd/audit-explorer` `TestServerStartStopLeavesNoGoroutines`: the real `run()` (demo data, loopback listener
  and both pipes) started and shut down five times, idle total at most 30 goroutines.
- `internal/casestore/sqlite`: five case open/import/read/close cycles (and the pool holds no connection after
  close), and five store open/close cycles.
- `internal/blackbox`: the recorder on its real deadline timer, and the whole host (`RunHost`, status server).
- `internal/sampler`: five recordings on the real ticker and five clock-driven ones.

## Not verified here

- Windows working set on the largest real case plus four charts (acceptance: at most 350 MiB, fail above 500).
- 2 GB VM run and the 60 s idle soak CPU on the real app (acceptance: under 30 ms, fail above 120 ms).
- The tray reopen cycles (spec says they use the fake tray deps; the tray package is Windows only).
