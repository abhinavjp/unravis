# Unravis performance notes (2026-10-25)

Measured numbers for Unravis's own CPU, memory and time on large synthetic data.
Everything here was measured, not estimated. Extrapolations are marked as such.

The harness is `internal/perfbench` (tests only, skipped unless `UNRAVIS_BENCH=1`, so
`go test ./...` stays fast). No production code was changed. All data is synthetic
(host `web-a`, `*.example.test`, 2001 dates, sentinel `SENTINEL-7f3a`), generated in a
temp directory, deleted afterwards, and never committed.

## Machine

| | |
|---|---|
| CPU | Intel Core i9-14900K, 24 cores / 32 logical |
| RAM | 63.7 GB |
| OS | Windows 11 Pro 10.0.26300 |
| Go | go1.27.3 windows/amd64, no CGO, no `-race` |
| Store | SQLite (ncruces, wasm) with the adiantum encrypted VFS, case key sealed with DPAPI |
| Disk | D: (local SSD) |

Load noise: other agents were building and testing on the same machine during these runs
(P1f discover routes, security fixes, UI fixes). The measured process is single-threaded
for imports (average 1.00 cores) on a 32-thread machine, so the noise is probably small,
but it is not quantified. The localread, discover and sampler runs overlapped with the
24 h counters query phase (a separate process). The imports and the counters runs were
serial.

## How it is measured

- Imports go through the real HTTP path: `server.New(... WithCases(store, hosts, all.Registry()))`
  under `httptest`, a real case created with `POST /api/cases`, then multipart
  `POST /api/cases/{id}/imports?importer=X&host=web-a` into a real encrypted case store.
  Server, client and store share one process, so the RSS includes all three.
- Memory is sampled every 50 ms (1 s for the sampler run so the sampler does not inflate itself):
  Go `HeapInuse`/`Sys` from `runtime.ReadMemStats`, plus the OS working set (`ws`) and private
  bytes from `GetProcessMemoryInfo`. SQLite runs in wasm, whose linear memory is outside the
  Go heap, so ws and private bytes are the true memory numbers and `HeapInuse` understates.
  CPU is `GetProcessTimes` (user + kernel) over the run. The generator is excluded.
- Case size is the final size of the case directory. "Peak dir" is the largest directory size
  sampled during the run (the WAL doubles it before the checkpoint truncates it).

How to run (PowerShell, from the repo root):

```
$env:UNRAVIS_BENCH = "1"
$env:UNRAVIS_BENCH_OUT = "<file>"   # appends RESULT/GROWTH lines
$env:UNRAVIS_BENCH_DIR = "<dir>"    # must exist; data is created and removed here
go test -c -o perfbench.test.exe ./internal/perfbench/
.\perfbench.test.exe '-test.run=TestBenchCounters' '-test.v' '-test.timeout=90m'
```

Tests: `TestBenchIISImport1M`, `TestBenchIISImport5M`, `TestBenchLog4net1M`,
`TestBenchWinEvents500k`, `TestBenchCounters`, `TestBenchLocalRead`, `TestBenchDiscover`,
`TestBenchSampler`. Size knobs: `UNRAVIS_BENCH_IIS_LINES`, `_IIS5_LINES`, `_LOG4NET_LINES`,
`_EVENTS`, `_COUNTER_SECONDS`, `_QUERIES`, `_QUERIES_SHORT`, `_BIG_MIB`,
`_SAMPLER_SECONDS`, `_SAMPLER_INTERVAL`. Run one test per process so the peak-working-set
figure belongs to that test.

## Results: import

Memory columns are the maximum during the run. "ws" is working set, "priv" is private bytes.

| Case | Input | Rows / events | Wall | Rate | CPU (user+kernel) | ws / priv / HeapInuse (MiB) | case.db | Peak dir |
|---|---|---|---|---|---|---|---|---|
| iis-w3c 1M lines | 164.1 MiB | 1,000,000 rows, 1,000,000 events | 86.9 s | 11.5k rows/s, 1.9 MiB/s | 86.8 s (48.8 + 37.9) | 35 / 68 / 10 | 396.6 MiB | 796 MiB |
| iis-w3c 5M lines | 820.3 MiB | 5,000,000 rows, 5,000,000 events | 549.9 s (9.2 min) | 9.1k rows/s, 1.5 MiB/s | 549.2 s (297.9 + 251.3) | 39 / 68 / 10 | 1982.6 MiB | 3981 MiB |
| log4net-text 1M entries | 147.6 MiB | 1,250,436 rows (continuation lines count), 279,300 events | 21.6 s | 57.8k rows/s, 12.9k events/s | 21.8 s | 59 / 92 / 31 | 107.6 MiB | n/m |
| log4net-text 3M entries (scaling check) | 442.5 MiB | 3,749,582 rows, 779,286 events | 76.8 s | 48.8k rows/s, 10.2k events/s | 77.4 s | 108 / 92 / 31 | 300.7 MiB | 604 MiB |
| win-counters 200 x 24 h @ 15 s | 11.6 MiB | 5,760 rows, 1,140,629 events | 89.4 s | 12.8k events/s | 90.0 s (45.5 + 44.5) | 83 / 67 / 9 | 418.3 MiB | 840 MiB |
| win-counters 200 x 6 h @ 15 s (scaling check) | 2.9 MiB | 1,440 rows, 285,207 events | 18.1 s | 15.8k events/s | 18.0 s | 34 / 67 / 10 | 104.6 MiB | n/m |
| win-events 500k | 82.5 MiB | 500,000 rows, 500,000 events | 39.2 s | 12.8k events/s, 2.1 MiB/s | 39.7 s | 36 / 68 / 10 | 196.2 MiB | 394 MiB |

`n/m`: the 1 s directory-size sampler recorded 0 for those runs, so no peak is claimed.
The log4net importer emits fewer events than rows because only entries it classifies as
events are emitted (279,300 of 1.25M rows); this is the importer's behaviour, not a loss.
Nothing was rejected in any run. Every import returned 201 and status `ok`.

Memory over input size (streaming check): the iis-w3c working set went from 35 MiB at 1M
lines to 39 MiB at 5M lines (5x the input), with private bytes flat at 68 MiB and HeapInuse
at 10 MiB. The IIS, events and counters imports are streaming. Log4net is discussed under
defect D5.

## Results: queries over the imported 24 h counter set

Case: 200 counters x 24 h at 15 s = 1,140,629 events (418 MiB). Requests go through the real HTTP handlers, one
at a time, same process. Selector is one counter (`Processor % Processor Time _Total`) unless stated.
The scan budget is 20M events or 10 s, after which the result is `partial`.

| Query (24 h window) | Requests | p50 | p95 | max | Partial | Events scanned | ws max (MiB) |
|---|---|---|---|---|---|---|---|
| series, 1 selector, 5 min buckets (288) | 50 | 11.33 s | 11.79 s | 12.00 s | yes | 583,323 | 81 |
| series, 10 selectors, 5 min buckets | 10 | 11.32 s | 11.35 s | 11.35 s | yes | 567,471 | 81 |
| series, 50 selectors, 5 min buckets | 10 | 11.32 s | 11.60 s | 11.60 s | yes | 526,054 | 84 |
| series, 1 selector, 1 min buckets (1440) | 10 | 11.32 s | 11.46 s | 11.46 s | yes | 589,474 | 81 |
| resource (RC1-RC9), one 10 min episode | 50 | 11.59 s | 12.20 s | 12.69 s | yes | not reported | 802 (heap 678) |

Scaling check on the 6 h case (285,207 events), which finishes inside the budget:

| Query | Requests | p50 | p95 | Partial | ws max (MiB) |
|---|---|---|---|---|---|
| series, 1 selector, 5 min buckets | 10 | 3.84 s | 4.09 s | no (scanned 285,207) | 31 |
| series, 10 selectors | 3 | 3.92 s | 3.94 s | no | 31 |
| series, 50 selectors | 3 | 4.28 s | 4.34 s | no | 33 |
| series, 1 selector, 1 min buckets | 3 | 3.83 s | 3.86 s | no | 31 |
| resource, one episode | 10 | 4.19 s | 4.44 s | no | 401 (heap 328) |

Cost is about 13 microseconds per scanned event (75k events/s), the same for 1 or 50
selectors. At 24 h the request hits the 10 s budget and returns a partial answer, so the
response covers only the first ~half of the window (583k of 1.14M events, read in time order;
this coverage is an inference from the scanned count). Response sizes: 11.6 kB (1 sel), 123 kB
(10), 468 kB (50), 22 kB (resource).

## Results: localread, discover scan, win-live sampler

### localread over a 2 GiB W3C file

File: 2,147,483,722 bytes, 12.48M lines, one `#Fields` header at the top. Windows are read
through `localread.OpenRange` with a counting `Opener`, so "read from file" is the bytes the
process asked the OS for. ws stayed at 17-20 MiB and HeapInuse at 5-7 MiB in every window.

| Window | Served | Read from file | % of file | Read / served | Wall | Served rate |
|---|---|---|---|---|---|---|
| 64 MiB at ~1 GiB, default limiter (32 MiB/s, 1 MiB burst) | 64.0 MiB | 72.2 MiB | 3.5% | 1.13x | 2.23 s | 28.8 MiB/s |
| 64 MiB at ~1 GiB, no limiter | 64.0 MiB | 72.2 MiB | 3.5% | 1.13x | 0.02 s | cache-hot |
| 1 MiB at ~1 GiB, no limiter | 1.0 MiB | 9.2 MiB | 0.45% | 9.2x | 0.01 s | n/a |
| tail, last ~128 MiB, no limiter | 128.0 MiB | 136.2 MiB | 6.7% | 1.06x | 0.03 s | cache-hot |
| first 16 MiB (offset 0), no limiter | 16.0 MiB | 16.1 MiB | 0.78% | 1.00x | < 0.01 s | cache-hot |

A window deep in the file costs the window plus an 8 MiB backward scan for the header, then a
64 KiB head fallback (header found at offset 97). The limiter counts the scan bytes too (72.2 MiB
over 2.23 s is 32.4 MiB/s). The unlimited rates are page-cache numbers because the file had just
been written; they are not disk throughput.

### discover scan over a 5000-file fake host

In-memory fake environment: applicationHost.config with 10 sites, 3000 IIS logs over 10
`W3SVC` folders, 500 FREB `fr*.xml`, 500 HTTPERR files, and 1000 log4net files under a site
whose `web.config` names the appender. Detectors: IIS, HTTP.sys, application logs, host, web.

| Metric | Value |
|---|---|
| Scan wall | 38-39 ms (3 runs), complete, 0 gaps |
| Findings | 14, covering all 5000 files (iis 3500 including FREB, httperr 500, applog 1000) |
| CPU | 0.03-0.05 s |
| Memory | HeapInuse 9-11 MiB, ws 25-26 MiB, private 62-64 MiB (flat across the run) |

This measures the detectors and the walk only. The file system is in memory, so a real disk's
directory listing and stat latency is not in this number.

### win-live sampler, real Windows source, 60 s at 5 s interval

`sampler.Start` with `OSSource`, flush every 20 s, import callback discarding the body. The
10 s idle baseline of the test process was 0.000% CPU.

| Metric | Value |
|---|---|
| CPU | 0.19 s over 60.0 s (user 0.03, kernel 0.16): 0.31% of one core, 0.0098% of the 32 logical cores |
| Per sample | about 17 ms CPU, mostly kernel time |
| Working set | 15 MiB at start, 16 MiB max (peak 17 MiB); private 55 MiB; HeapInuse 5 MiB |
| Output | 11 samples, 1,748 events, 3 flush batches, 158,101 CSV bytes, 0 partial |

## Defects and findings

Nothing exceeded 2 GB RAM. Nothing exceeded 10 minutes outright, but the 5M-line import came
within 50 s of it. The measurements below were taken before any fix; D1 and D2 have since been fixed (see each entry).

**D1. Series and resource queries cannot finish on a realistic 24 h counter case (high). FIXED, see after-numbers.**
Before: 200 counters x 24 h x 15 s (1.14M events) always returned `partial` after ~11.3 s, for every
query shape, covering about the first half of the window. Cause: `Case.Each` streamed
`SELECT ... ORDER BY kind, ts_ms` filtered only by kind and time, and the selector match ran in Go,
so a 1-selector query cost the same as a 50-selector one (3.8 s vs 4.3 s on the 6 h case).

Fix: `EventQuery` gained `Attrs` (up to 4 exact attribute pairs) and `Unordered`. The sqlite store
now pushes the selector into SQL and uses two additive partial expression indexes
(`ev_kind_counter`, `ev_kind_metric`, schema version 3). Old case files open unchanged; the version is
bumped on open and the indexes are built lazily on the first selector query, with a plain scan as
fallback. `Series` makes one shared pass for selectors that cannot use an index and one narrow pass per
other selector, under one shared scan budget. The response shape is unchanged. Tests: an old-version
(schema 2) fixture migration test, a contract test (`each-attribute-filter`) run against both stores, and
a plan test in `internal/casestore/sqlite/scan_test.go`.

After (synthetic, one PC, 1,140,629 events, 24 h window; `UNRAVIS_BENCH=1`, `TestBenchSynthQueries`):

| Series query | Wall | Partial | Events scanned |
|---|---|---|---|
| 1 selector | 0.08 s | no | 5,700 |
| 10 selectors | 0.51 s | no | 57,035 |
| 50 selectors | 2.06 s | no | 233,821 |

Heap stayed at 6-8 MiB during these. A budget far below the case size (20,000 events) still gives a
partial answer after exactly 20,000 events; the bench asserts both. Not measured: how long the lazy index
build takes on a very old 1.14M-event case (the code comment says seconds to tens of seconds; the
measured case was built with the indexes in place). The indexes add to case.db size, not measured.

**D2. Resource evaluation memory grows with the events scanned (streaming violation, high). FIXED, see after-numbers.**
Before: `/resource` heap was 328 MiB at 285k events and 678 MiB at the 583k events scanned before the
budget cut it off, about 1.15 KB per event (ws 802 MiB at 24 h), bounded only by the scan budget;
projected about 2.3 GB at the 2M-event counters cap.

Fix: the catalogue builder (`internal/resource/catalogue.go`) keeps one compact row per event (about
115 B) with the fields shared by a series stored once, instead of the whole event. It reads with
`Unordered`, and on the sqlite store a table scan when the wanted kinds are at least 1 in 5 of the case
(a unary-plus plan, about 3.8x faster than the index walk on a counter-dominated case). The rows are
re-ordered to the old scan order (kind, start, id), so results are identical; a test compares against a
reference implementation, including shuffled delivery.

After (synthetic, one PC, same 1.14M-event case, whole 24 h window): resource evaluation took 3.64 s,
not partial, HeapInuse max 260 MiB, working set max 575 MiB over a 220 MiB base, total allocation 2,490 MiB.
Before the unordered scan the same case took about 10.2 s and one run was partial, so the scan plan
matters as much as the memory fix. Caveats: 260 MiB is still mostly the compact rows plus the Series
samples, so memory still grows with scanned events (about 230 B per event overall here), but at about
a fifth of before. At the 2M-event counters cap this projects to roughly 0.5 GB (extrapolation, not
measured). The sqlite page cache is now 64 MiB per connection (pool of 4, 256 MiB worst case).

**D3. Import throughput is low and degrades as the case grows (medium).** iis-w3c ran at 11.5k
rows/s for 1M lines and 9.1k rows/s for 5M (a 20k-row smoke run reached 32.6k rows/s). Counters
and events imports run at 12-16k events/s. The import is single-threaded (1.00 cores) and 44-49%
of its CPU is kernel time, which suggests many small page I/O calls through the encrypted VFS
plus a growing WAL. The importer's own cap is 10M rows; at the 5M-line rate that is about 18 min
(extrapolation), over the 10 minute bar. Disk peaks at about 2x the final case (3.98 GiB for a
1.98 GiB case), so a 10M-row import needs about 8 GiB free.

**D4. Storage amplification (medium).** case.db is 2.4x the input for iis-w3c (396.6 MiB from
164.1 MiB), 2.4x for win-events (196.2 MiB from 82.5 MiB), and 36x for counters (418 MiB from an 11.6 MiB CSV, about 366
bytes per event, because a wide CSV row becomes one event per counter with JSON attributes).
A 2M-event counters case is about 730 MiB.

**D5. log4net import working set grows with input while the Go heap does not (low).** ws was
59 MiB at 1M entries and 108 MiB at 3M, while private bytes stayed at 92 MiB and HeapInuse at
31 MiB for both. That points at file-backed or wasm pages rather than a Go-heap leak; I did not
confirm which. Extrapolated to the 10M-row cap it is a few hundred MiB, no threat to 2 GB, but
it is the only importer whose ws moved with input size. Its 31 MiB HeapInuse is also higher than the
others (10 MiB).

Not defects, for context: localread reads only 0.45-6.7% of a 2 GiB file and its memory is flat;
a deep window costs a fixed ~8 MiB header scan (9x for a 1 MiB window), which is by design.
The sampler's cost (0.31% of one core, 1 MiB over the idle process) is negligible. Discovery of
5000 files takes under 40 ms of detector time.

## Caveats

- Everything runs in one process (server, client, store), so ws and private bytes include all three.
- The generators are excluded from the measurements. Data is synthetic and uniform; real logs
  vary in line length and cardinality, which moves rows/s and bytes per event.
- Single run per import case (the queries have 10-50 samples). The 1 s directory sampler missed
  the peak for two cases (marked `n/m`).
- Page-cache effects: files had just been generated, so read and import inputs were cache-hot.
- Other agents were using the machine, see Load noise above.
