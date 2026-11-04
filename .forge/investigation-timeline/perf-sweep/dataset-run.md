# Perf dataset run (2026-11-04)

Files (all new, nothing staged or committed): internal/perfdata/*.go, cmd/unravis-perfdata/main.go, internal/server/tracing_stress_test.go, tools/e2e/support/live-launcher.ts, tools/e2e/specs/tracing-perf.spec.ts. Datasets: .dev/perf-data/{T,S,M,L}.

## Generation (rows/s, case disk)
| size | span | rows | time | rows/s | disk |
|---|---|---|---|---|---|
| S | 31 d | 2.02M | 3m36 | 9.3k | 0.78 GB |
| M | 92 d | 6.06M | ~17m (ran beside the S stress test) | ~6k | 2.4 GB |
| L | 184 d | 12.12M | 24m38 | 8.2k | 4.78 GB (over the app's default 4 GiB DB cap) |

## Go stress test (200, sums = manifest, <2 MB, heap growth 220-290 MB, no failures)
Wall time per call (1d / 7d / 14d to 186d, incl. off-hour and 3 parallel):
- S: 9-12s / 28-31s / 36s; 21 of 28 calls hit a lane timeout
- M: 13-15s / 25-31s / 39-40s; 22 of 29 timeouts
- L: 25s / 45s / 49-64s; 22 of 29 timeouts. The IIS lane at 186d had 0 complete buckets.

## Real product bug (not fixed, history files are dirty)
Every window of 14 days or more returns partial lanes (reason timeout), and even 1 day takes 9-25s.
Cause: the events table is WITHOUT ROWID, and ev_ts(ts_ms) does not cover `source`, so each query does a random row lookup for every row in the time range, whatever the lane (internal/casestore/sqlite/schema.go, case.go Events and BucketCounts). Probe (copy of S, probe kept at .dev/perf-data/zz_probe_test.go.txt): COUNT 1 day by source 1.9s vs 24ms by ts alone. Adding `CREATE INDEX ev_src_ts ON events(source, ts_ms, import_id)` (9s to build, +12% size): 31-day bucket counts 10s timeout to 1.1s (IIS) and 15ms (SQL); 1-day Events 2s to 0.01s for SQL.
Still slow: the IIS 5xx count (json_extract(attrs_json) per row): 7 days 9.6s, 30 days times out. Needs a stored 5xx flag or partial index.
Proposed minimal fix: add that index to schema.go (plus a migration for existing cases), then design the 5xx column.

## Playwright (S copy, real exe .dev/run/perf/unravis-gate.exe): 2 passed
PERF-01 186d: 33s to chart, 200, 47 KB. PERF-02 7d: 17s.

## Rerun
- `unravis-perfdata.exe --size S --out .dev\perf-data\S`
- `$env:UNRAVIS_STRESS_DIR='...\perf-data\S'; go test ./internal/server -run TestTracingStress -v -timeout 40m -count=1`
- `cd tools/e2e; $env:UNRAVIS_LIVE_EXE=...\unravis-gate.exe; $env:UNRAVIS_PERF_CFG='...\perf-data\S\cfg'; npx playwright test specs/tracing-perf.spec.ts`
