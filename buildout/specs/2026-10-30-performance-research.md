# Performance research for Unravis tracing targets (2026-10-30)

Hub job 31898 (pi-river deepseek v4.1 flash, no web access: external claims unverified). Orchestrator note: numbers come from the repo's own perf notes; check them against a fresh benchmark (BM-01..12) before quoting.

## Verdict (5 sentences)

The 80–150k rows/s ingest target is **not realistic** on this stack as built: the driver is `github.com/ncruces/go-sqlite3` (WASM SQLite) over the adiantum encrypting VFS (`go.mod:5`, `internal/casestore/sqlite/sqlite.go:22`), measured at 9–16k events/s and 9–58k input-rows/s on a fast Windows SSD (`docs/superpowers/plans/2026-10-25-performance-notes.md`), and the import path issues **one prepared `ExecContext` per event inside a single serializable transaction** (`internal/casestore/sqlite/case.go:496`, `:686`), so the ceiling is per-row overhead, not batch policy. Chart zoom <15 ms is **only realistic with precomputed rollups**, because today a zoom re-scans raw events at ~13 µs/event (`performance-notes.md`), so a 1M-event window costs seconds, not milliseconds; exact counts/sum/min/max rollups can meet it, but honest p95/p99 needs mergeable sketches with a labelled tolerance. Case reopen <100 ms is **plausible but not met today** on large cases, because `Open`→`verify` runs `PRAGMA quick_check` (O(N)) plus `dbManifestImports` (`SELECT import_id, COUNT(*) FROM events GROUP BY import_id`) on every open (`case.go:229`, `:251`, `:328`). The single largest win is to stop re-reading raw events for charts (materialise rollup tables on import) and to make open-time verification lazy/checkpointed; none of this loses accuracy if rollups are exact for counts and sketches are labelled. We currently have no rollup tables for case events at all — `internal/audit/rollup.go` is a Mongo/HTTP-side daily/hourly aggregation for the **live** source, not the encrypted case store — so both the zoom target and the reopen target require new work, and the ingest target requires a full re-architecture (native SQLite or an append-only segment format).

Everything I could not test on a machine is marked unverified; I had **no web access**, so every external claim is marked "unverified (no web)".

## 1 Ingest

**What the stack is.** Driver: `github.com/ncruces/go-sqlite3 v0.35.6` (`go.mod:5`), WAMR/WASM SQLite, CGO-free. Encryption: `lukechampine.com/adiantum` used through the driver's `vfs/adiantum` VFS, registered by a blank import (`internal/casestore/sqlite/sqlite.go:23`), key delivered per connection via `PRAGMA hexkey` in the connection hook, never the DSN (`sqlite.go:546`, `initConn` at `:594`). WAL is set on every connection (`sqlite.go:604`–`605`: `journal_mode=WAL`, `journal_size_limit=67108864`, `cache_size=-65536`, `synchronous=NORMAL`); during an import the dedicated connection raises `synchronous=FULL` and truncates the WAL at the end (`case.go:612`, `:615`, `:657`). One import = one transaction: `BeginTx(ctx, &sqlx.TxOptions{Isolation: sql.LevelSerializable})` (`case.go:616`), and the bulk indexes are created inside that same transaction after the rows (`case.go:686` runs `indexSQL`; `internal/casestore/sqlite/schema.go:37`).

**Measured throughput.** The only honest numbers are the repo's own harness (`internal/perfbench`, gated on `UNRAVIS_BENCH=1`) and the storage-decision scratch benchmark. The perf notes table (i9-14900K, 63.7 GB RAM, Windows 11, local SSD, one process) reports: iis-w3c 1M rows = 86.9 s ≈ **11.5k rows/s**; 5M rows = 549.9 s ≈ **9.1k rows/s** (degrading with case size); log4net 1M = **57.8k rows/s but only 12.9k events/s**; win-counters = **12.8–15.8k events/s** (`docs/superpowers/plans/2026-10-25-performance-notes.md`, import table and D3). The storage-decision micro-benchmark loaded 2M events in 3.6 s ≈ 555k rows/s, but that used plain column-shaped rows, batches of 10k per transaction, and no HTTP/server path (`docs/superpowers/specs/2026-10-24-storage-decision.md`, Benchmark table). The two differ by ~50x, which is the single most important fact in this section: **batching dominates**.

**Why our path is slow.** The import loop prepares one statement and calls `stmt.ExecContext` **once per event** (`case.go:496`, `:686`); there is no multi-row `INSERT`, no `BEGIN`-less batching, no `PRAGMA` tuning for bulk load. The importer emits through a callback (`emit`) that also runs `ValidateFor` and JSON-marshals attrs per event (`case.go:656`). CPU on iis-w3c was 44–49% kernel (`performance-notes.md` D3), consistent with many small encrypted-VFS page writes plus a growing WAL. The 5M case hit 3.98 GiB peak directory (≈2x final) before checkpoint (`performance-notes.md` D3), so WAL growth is a real secondary cost.

**Levers, and which are unsafe.**

| Lever | Expected effect | Safe? |
|---|---|---|
| Multi-row / batched `INSERT` (100–1000 rows/statement), prepared once | Largest single win; 10–50x, per the 3.6 s vs ~90 s gap | Safe. Same transaction, same data. |
| Deferred index creation after bulk (already done, `case.go:686`) | Already applied; correct | Safe. |
| Keep `synchronous=FULL` only on commit | Correct crash safety kept | Safe (already done). |
| `synchronous=NORMAL` for the bulk insert, `FULL` on commit | Faster | Safe **only** because WAL+NORMAL can still lose only the last transaction; on import failure we roll back anyway, so this is defensible, but it must be stated explicitly. |
| Bigger `cache_size`, `temp_store=MEMORY`, larger WAL autocheckpoint | Moderate | Safe for correctness; `temp_store=MEMORY` risks OOM on huge sorts. |
| `mmap_size` | Unknown/limited | **Unverified (no web)** whether the ncruces WASM VFS honours mmap; likely no benefit, and mmap over a decrypting VFS is unsound. |
| `page_size` change | Adiantum block = 4096 = `pageSize` (`schema.go:77`); changing it changes the cipher block granularity | **Unsafe** to change casually; must be fixed at DB creation. |
| `journal_mode=OFF`/`MEMORY`, `locking_mode=EXCLUSIVE` | Faster | **Unsafe**: loses crash atomicity, which the spec requires (`storage-decision.md`, Crash safety). |
| Dropping `secure_delete=ON` | Faster deletes | Unsafe for this product (crypto-erase expectations); `secure_delete` is on per connection (`sqlite.go:602`). |

Realistic target with batching on this hardware: **~100–200k rows/s is plausible for narrow rows if batching is adopted** (extrapolated from the 3.6 s 2M load), but **events/s for counter-style wide rows will stay far lower** because one CSV row can expand to many events with JSON attrs (`performance-notes.md` D4). 80–150k **events/s** for realistic counter/log data is **not realistic** on WASM SQLite + adiantum; it needs native SQLite (CGO) or the append-only segment format the spec already built and rejected for operational reasons (`storage-decision.md`, Rejected options).

## 2 Zoom and rollups

**Today's zoom is a raw re-scan.** The timeline query path is `casestore.Series` (`internal/casestore/series.go:230`), which streams matching events through `Case.Each`, aggregating per-bucket min/mean/max/count in Go (`series.go:262`–`290`). Cost is ~13 µs/scanned event ("75k events/s", `performance-notes.md`, queries section). Caps: 7-day window, ≤2000 buckets (`series.go:24`), ≤50 selectors, scan budget 20M events/10 s (`internal/casestore/store.go:295`). A 15 ms budget therefore allows ~**1,150 raw events**, i.e. nothing above toy windows. Indexed single-series reads help (after D1 fix, 1 selector over 1.14M events = 0.08 s, `performance-notes.md` D1 after-table) but 0.08 s is still 5x over 15 ms and 50 selectors took 2.06 s.

**Options compared.**

- **Raw each time:** exact, zero storage cost, cannot meet 15 ms beyond ~1k events. Distortion: none. Exact: yes.
- **Precomputed multi-level rollup tables** (e.g. 1 s / 1 min / 1 h / 1 d): serve counts/sum/min/max by merging buckets. Exact for count, sum, min, max **iff** buckets are non-overlapping and complete and merged arithmetically (`sum = Σ`, `min = MIN`, `max = MAX`, `count = Σ`) — the same merge already implemented in `RollupBuilder.Merge` (`internal/audit/rollup.go:214`). Query becomes O(#buckets) index reads, easily <15 ms. Distortion: **spikes** survive only if min/max are stored per level (they are); **means** must be weighted by count, never averaged (a real trap); raw **event lists** are not recoverable; and any level built with a coarse time bucket smears sub-bucket timing. Exact: counts/sum/min/max yes; per-bucket **p50/p95/p99 no** unless a sketch is stored.
- **Mergeable percentile sketches** (t-digest, DDSketch, or our existing 1.1^n log histogram, `internal/audit/histogram.go:24`): exact merge is *not* possible for order statistics in bounded space, so percentiles become approximate. The existing histogram already bounds error by bin: bin `b` covers `[1.1^(b-1), 1.1^b)`, ~10% width, and `quantile` returns `lower`/`upper` with a midpoint value and reports `LowerMs/UpperMs` (histogram.go:184–204). That is the honest pattern.
- **LTTB / downsampling** (unverified (no web) — LTTB = Largest-Triangle-Three-Buckets): chooses one real point per bucket to preserve visual shape. It is **lossy and non-additive**: it distorts counts (drops points), distorts tail latency (may keep/drop the max), and cannot merge upward. Use it **only for line rendering**, never for the numbers shown, and label the chart "downsampled". Our current UI has no LTTB; the case timeline is exact per bucket (`internal/casestore/series.go:262`).

**Recommendation.** Store rollups **in the same encrypted `case.db`**, written in the import transaction (so accuracy and crash atomicity are free), at 1-minute and 1-hour levels; store per-bucket `count, sum, min, max` (exact) plus the log-histogram bin counters (approximate percentiles). Zoom = merge the smallest level whose bucket ≤ target resolution. Label in UI: exact counts; for percentiles show the value with `≈` and a lower/upper bound, mirroring the existing `latencyText` "≈ … ms" convention (`web/src/lib/rollup.ts`, `latencyText`), and show "approximate (histogram bucket 475–525 ms)" on hover. Never show a merged mean without dividing by merged count.

## 3 Reopen

**What dominates.** `Store.Open`→`load` (`internal/casestore/sqlite/sqlite.go:686`) then `Case.verify` (`case.go:229`): (1) `readCaseFiles` under a folder lock, unsealing `case.key` + `manifest.dat` (DPAPI `CryptUnprotectData`, or per-case-key Argon2id unwrap — the latter is the expensive one); (2) `openDB`→`driver.Open` + `PRAGMA hexkey` + ping (`sqlite.go:546`); (3) `PRAGMA quick_check` — O(N) over the whole DB (`case.go:236`); (4) `meta` reads; (5) `migrate`; (6) `dbManifestImports`, which runs `SELECT import_id, COUNT(*) FROM events GROUP BY import_id` — a **full scan of `events`** on every open (`case.go:328`); (7) if `idxPending`, a **background** `ensureAttrIndexes` that reads every event (`case.go:263`, `:492`).

WAL recovery is bounded by `journal_size_limit=64 MiB` (`sqlite.go:604`), and shutdown truncates the WAL (`case.go:216`), so recovery is small *if* the last session checkpointed. Key unwrap dominates only when the case uses its own key + Argon2id (`KeyParams`, `sqlite.go:118`); with DPAPI it is milliseconds.

**What can be lazy.** `quick_check` and `COUNT(*) GROUP BY import_id` are the two O(N) costs and both are **provably deferrable**: the manifest already mirrors imports, so `dbManifestImports` only needs to run when the manifest's count disagrees (the code already rebuilds when `len(dbImports) > len(c.man.Imports)`, `case.go:339`). `quick_check` can run after first paint, or be limited to `PRAGMA quick_check(N)`. `ensureAttrIndexes` is already backgrounded. Conclusion: **<100 ms reopen is achievable for large cases only if open stops doing full scans**; today a 5M-event case cannot reopen in 100 ms because steps (3) and (6) read every row.

## 4 Benchmark design (with BM-xx list)

Design principles: realistic skew + bursts, never uniform; separate cold vs warm; encrypted vs plain measured side by side; percentiles not means; a stored baseline with budgets; CI runs **small** sizes only, big sizes are manual nightlies. Existing assets to reuse: `internal/perfbench` harness (gated `UNRAVIS_BENCH=1`, `internal/perfbench/doc.go`) and `scale_measure_test.go` (gated `UNRAVIS_SCALE=1`).

Data generator must include: power-law per-key cardinality (few hot counters, long tail), Poisson/self-exciting bursts (incident spikes), heavy-tailed durations (2% up to 60 s, as the storage benchmark did), duplicate/sub-second events, DST transitions, gaps, mixed units, and 1/10/100 attribute cardinality. Sizes: **100k, 1M, 5M** events; optionally 10M (cap).

Metrics: per-op p50/p95/p99/max and counts (never averages); peak RSS/private bytes (WASM linear memory is outside the Go heap — sample OS working set, as `sampleMem` already does in `scale_measure_test.go`); bytes read/written; rows/s and MiB/s; partial-result rate.

CI flakiness control: run 100k only, assert against a **relative budget** (e.g. p95 ≤ 1.5x stored baseline) rather than absolute ms; pin GOOS/GOARCH; skip on shared CI unless a runner class matches; store baselines as JSON keyed by commit + machine class; fail only on regression, warn on improvement; always discard the first (warm-up) run; median-of-N.

Benchmark tasks (each one work packet):

- **BM-01** Data generator with skew/bursts/heavy tails; deterministic seed; emits any size; unit-tested distribution invariants.
- **BM-02** Ingest throughput, batched vs per-row, encrypted vs plain, cold vs warm, at 100k/1M/5M; assert rows/s + p95 + peak RSS.
- **BM-03** Rollup build cost and size overhead (bytes in case.db per level) at 1M/5M.
- **BM-04** Zoom latency: raw-scan vs rollup merge, p50/p95/p99 at 1M/5M; assert p95 < 15 ms for rollups.
- **BM-05** Rollup accuracy: merge results vs raw-scan for count/sum/min/max at every level (exactness test, no tolerance).
- **BM-06** Sketch accuracy: p95/p99 error vs exact nearest-rank, reported as a distribution of relative error across seeds.
- **BM-07** Reopen latency breakdown (key unwrap / open / quick_check / count / migrate / first query) with and without lazy verify, at 1M/5M; pass/fail <100 ms.
- **BM-08** WAL/disk amplification and peak-dir during ingest (regression guard for the 2x WAL peak).
- **BM-09** Concurrency: import vs concurrent readers (the `SetMaxOpenConns(4)` WAL promise, `sqlite.go:552`), assert readers don't block and see last commit.
- **BM-10** Windows-specific: disk fsync behaviour, cold page cache, DPAPI unwrap cost; plain-vs-encrypted delta.
- **BM-11** Baseline store + CI budget checker (relative p95 budgets, JSON keyed by machine class).
- **BM-12** Crash-safety micro-bench: kill mid-import, assert no partial rows and reopen succeeds (not a speed test but the guard that batching didn't weaken atomicity).

## 5 Accuracy guardrails

**Property tests comparing both paths.** For every rollup level, generate events, compute (a) exact aggregates by the current raw `Series` path and (b) merged rollups, and assert **bit-exact** equality for `count`, `sum` (within float tolerance: sums must use the same order or Kahan), `min`, `max`. Use `testing/quick`-style randomized windows/buckets, shuffled event order, empty buckets, single-event buckets, negative/zero/NaN/Inf values (which `Series` currently skips: `series.go:276`). Add DST-boundary windows and 7-day/2000-bucket edge cases. This is a cheap, high-value property test and mirrors what `scan_test.go`/`differential_test.go` already do for the store.

**Sketch tolerance.** For the existing 1.1^n histogram: relative error per bin is ≤ ~10% (`BinBounds`, `internal/audit/histogram.go:53`); the returned `Quantile` carries `LowerMs`/`UpperMs` bounds. Acceptable tolerance: **the true value must lie within [LowerMs, UpperMs]**, which is a testable, exact guardrail; the displayed midpoint is approximate. Test: for many seeds and many distributions, assert the exact nearest-rank p95 lies inside the reported interval, and that the relative width `(upper-lower)/value` is ≤ ~12%. For any future sketch, require the same interval contract. `MinSampleCount`/`P99MinSampleCount` gating (histogram.go:178) must stay: below 100 samples no p99 is shown, which is itself an accuracy guardrail.

**No silently wrong counts.** Any aggregated/partial result must carry `Partial`/`Scanned` (already in `SeriesResult`, `series.go:107`) and the UI must show it; never render a partial bucket as if complete. Enforce with a test that a budget-stopped query always sets `Partial=true` (already asserted in `performance-notes.md` D1).

## 6 Per-target verdict table

| Target | Verdict | Reasoning | Proposed honest budget |
|---|---|---|---|
| Ingest **80–150k rows/s** | **Not realistic on this stack** | WASM SQLite + adiantum; per-row `ExecContext` (`case.go:496`); measured 9–58k rows/s, 13–16k events/s (`performance-notes.md` D3). Batching may reach ~100–200k narrow rows/s (extrapolated from 3.6 s/2M in `storage-decision.md`), but wide counter rows won't. | Narrow rows: 100k rows/s (batched), 25k events/s. Wide/counter events: 20k events/s. State both, per importer. |
| Chart zoom **<15 ms** | **Only with rollups** | Raw path ~13 µs/event, 1M events = seconds (`performance-notes.md`); 15 ms ≈ 1.1k events raw. Rollup merge is O(buckets), trivially <15 ms. | p95 <15 ms **for rollup-backed zoom**, exact counts; percentiles approximate and labelled. Raw/uncached zoom: publish 250 ms for a 1M-event window or refuse. |
| Case reopen **<100 ms** | **Only with lazy verification** | `quick_check` + `COUNT(*) GROUP BY import_id` are O(N) on every `Open` (`case.go:236`, `:328`). | <100 ms warm (manifest trusted, verify deferred); <300 ms cold with deferred quick_check; publish a "verifying" state instead of blocking. |
| Accuracy (owner requirement) | **Achievable** | Exact rollups for count/sum/min/max; histogram p95 with explicit [lower,upper] bounds (`histogram.go:78`, `:184`). | No silent wrong numbers: every approximate value labelled; partial results always flagged. |

## 7 Open questions for the owner

1. Is 80–150k rows/s a **requirement for events or for input rows**, and for which importer? The two differ by ~4x today (`performance-notes.md`, log4net 57.8k rows/s vs 12.9k events/s).
2. May we **batch inserts and change the transaction shape** (multi-row INSERT, `synchronous=NORMAL` during bulk, `FULL` on commit), given it changes crash-recovery timing but not atomicity?
3. May rollups live **inside `case.db`** (adds size ~10–20%, keeps one encryption boundary and one crash-atomic transaction), or is a separate sidecar file preferred?
4. Is an **approximate p95/p99 with visible lower/upper bounds** acceptable, or does "no losing accuracy" mean percentiles must be exact (which forces storing all values or accepting unbounded cost)?
5. Is a **native/CGO SQLite build** on the table (it would break the current cgo-free build, `storage-decision.md`), or is the WASM stack fixed?
6. For reopen, is a **"verifying, results may be pending"** UX acceptable, i.e. can `quick_check` and the manifest COUNT be deferred off the critical path?
7. Which machine class defines the budget — the documented i9-14900K/SSD, or a slower supported laptop? Budgets must be stored per class.
8. Should zoom be **exact-per-bucket** (rollups) or is **LTTB-style visual downsampling with an "approximate chart" label** acceptable for the line only, numbers always exact?

*Note: I had no web access, so no external URL is cited with confidence; all external characterisations (LTTB, t-digest, SQLite pragma semantics, adiantum properties beyond what the repo states) are marked **unverified (no web)**.*