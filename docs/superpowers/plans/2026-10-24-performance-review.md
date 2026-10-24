# Unravis performance review: findings and plan

Date: 2026-10-24. Status: findings and plan only. No product code was changed and nothing was committed.

## 0. Scope, method and limits

- **What was measured.** A private `dist\unravis.exe --demo --port 18772 --no-browser` instance, run with a throwaway `AUDIT_CONFIG_DIR`, was driven through the real UI in a browser pane. Each user action was measured with the Resource Timing API (`transferSize`, `decodedBodySize`, `duration`, `startTime`) and a fetch hook that recorded each request body. No real Mongo was contacted. The instance and its temp config were removed afterwards.
- **What the times mean.** In demo mode every API call took 1–6 ms on the server, so the demo times measure only app overhead. On a live source, each `/api/analyze`, `/api/breakdown`, `/api/slowest`, `/api/requests`, `/api/activity`, `/api/patterns` and `/api/sources/{id}/coverage` call is one or more Mongo aggregates:
  - each aggregate has a 10 s `maxTimeMS` budget, except coverage at 60 s;
  - each source allows **one query at a time** (`audit.PerSourceConcurrency = 1`, `internal/audit/budgets.go:12`);
  - all sources share a global cap of 4 (`internal/store/store.go`);
  - waiting callers queue in `acquire` (`internal/store/analyze.go:328-342`).

  So the figure that matters here is the **request count per action**. Each redundant call costs at least one live aggregate and blocks that source's single slot for the next useful query.
- **Code reads only, for live behaviour:** `internal/store/*`, `internal/server/*`, `internal/mcp/tools.go` and `bridge.go`, `web/src/lib/*`, `web/src/components/*`.
- **Interference.** Near the end of the session another session navigated the shared browser pane to a different instance. Two runs of the "open evidence" sequence were affected (aborted entries with 0 bytes) and were discarded. The table uses the first clean run only.

## (a) Measured table: action → requests, bytes, time

Demo data: 2 sources. "Bytes" is the decoded response body. API server time was 1–6 ms for every call and is left out unless it matters.

| # | Action | Requests | Bytes | Time / notes |
|---|---|---|---|---|
| 1 | Landing (cold) | 8: `index.html`, 3 assets, `/api/ledger`, `/api/info`, `/api/sources`, `/api/analyze`, `/api/breakdown` | assets 1,158,182; analyze 153,531 (15 min window, 10 s slices); breakdown 1,535; others < 200 each. Total ≈ 1.32 MB | DOMContentLoaded 98 ms. `info` at 98 ms, `sources` at 113 ms (serial), `analyze` at 148 ms, `breakdown` at 381 ms (waits for the analysis) |
| 2 | Reload | same 8 | all 3 assets downloaded again in full (425,843 + 95,131 + 638,108 B transferred) | Assets have no `Cache-Control`, `ETag` or `Last-Modified`, so the browser cannot reuse them. DCL 33 ms |
| 3 | Run analysis, scope unchanged (manual) | 1 analyze | 153,531 | No short-circuit: the same query is sent again |
| 4 | Run analysis again, identical | 1 analyze | 153,531 | Same as #3 |
| 5 | Change window preset (auto-run) | 2: analyze + breakdown | 1 h: 104,143 + 1,615; 4 h: 41,566 + 1,615; 24 h: 21,353 + 1,615 | Fires about 300 ms after the click (debounce). Breakdown starts after the analysis arrives |
| 6 | Window A → B → A | 2 per change | the return to A refetches the full analysis and breakdown | No memory of the previous identical scope |
| 7 | Three presets clicked within 160 ms | 2 | 21,353 + 1,615 | Debounce works: one run |
| 8 | Click the preset already active | 0 | 0 | Auto-run dedupe works |
| 9 | UTC ↔ Local toggle | 0 | 0 | Client-only already |
| 10 | Breakdown dimension tab (User, Status code, then User again) | 1 per click, **including the revisit** | User 4,605; Status 1,615 | `BreakdownPanel` sets `result = null` and reloads on every key change |
| 11 | Inspector tab Failures → Slowest → Breakdown → Failures → Breakdown | 1 breakdown on **every** visit to Failures or Breakdown | 1,615 or 4,605 each | Tabs unmount and remount (`EvidenceView.svelte:79-86`) |
| 12 | Load 50 slowest | 1 slowest | 28,356 | Kept in component state, so it is lost when the tab is remounted (code) |
| 13 | Nearby activity: row 1, row 2, row 1 again | 1 per click, including the repeat | 2,404 each | |
| 14 | Show matching requests (breakdown pick) | 1 requests | 60,381 | |
| 15 | Open record → close → reopen; back to list | open 1, close 0, **reopen 1**, back 0 | 766 / 791 | The record response includes payload bodies (see F-X3) |
| 16 | Open evidence for an API → Back to Pulse | evidence: 1 requests (59,857); Pulse: **1 breakdown** (1,615) | | The Pulse "Where is it?" breakdown remounts and refetches |
| 17 | Patterns: open, run, close, reopen, run again | open 0, run 1, reopen 0 (report gone), **run again 1** | 2,466 each | On live, one patterns run is 1 Analyze plus paged slowest reads per source (`internal/audit/patterns_run.go:217, 384-409`) |
| 18 | Coverage: open, check, close, reopen, check | 1 per check, including the repeat | 994 each | On live, this is a census with a 60 s budget and a broad-scan gate |
| 19 | Ledger drawer: open, close, reopen | 1 GET per open | 3 | Local file only, no Mongo |
| 20 | Process start → first 200 on `/` | n/a | n/a | ≈ 520 ms (3 runs, includes PowerShell polling overhead). Working set ≈ 20 MB idle, 26.5 MB after the session |
| 21 | Bundle | n/a | JS 425,543 + echarts 637,808 + CSS 94,831 = 1,158,182 B. `gzip -9` would give 362,815 B | Served uncompressed |

**Analysis payload breakdown** (15 min, 90 slots, 2 sources, 8 API groups): `timeline` 85,353 B (6 series × 90 points, ≈ 232 B per point), `loadWindows` 41,259, `requestVolume` 14,311, `apis` 8,147, everything else < 1.4 KB each.

## (b) Ranked findings

Rank orders live impact by how often the action happens, with implementation risk counted against it. "Gain" is in live Mongo calls avoided or latency removed. The demo cannot show live milliseconds.

### F1. Evidence panels refetch on every revisit (breakdown, slowest, activity). Rank 1

- **Evidence:** table rows 10, 11, 13 and 16.
  - `web/src/components/BreakdownPanel.svelte:20` builds `requestKey`. The `$effect` at lines 45-51 clears `result` and calls `load(key)` on every key change, with no memo.
  - `EvidenceView.svelte:79-86` mounts panels with `{#if activeTab === …}`, so a revisit is a fresh mount.
  - `TrafficSlowness` and `ActivityContext` also keep their results in component state.
- **Live cost of one revisit:**
  - one breakdown = 2 aggregates per source, counts and bins (`internal/store/breakdown.go:84-135`);
  - slowest = a sorted scan;
  - activity = a range read.

  Each one also takes the source's single slot.
- **Expected gain:** zero Mongo calls for any revisit while the same analysis is on screen. In the measured session, 7 of the 11 breakdown and activity calls were repeats.
- **Risk:** low. Cached data would never be older than the analysis it sits under. The memo dies when that analysis is replaced (§c.1).
- **Recommendation:** add a per-analysis in-memory memo with in-flight dedupe for `/api/breakdown`, `/api/slowest` and `/api/activity`, keyed by the request body. Clear it whenever the analysis changes, a source is removed or the session is forgotten. Specified in §c.1.

### F2. Coverage census result is discarded when its drawer closes. Rank 2

- **Evidence:** table row 18.
  - `CoverageDrawer.svelte:12` wraps the panel in `{#key sourceId}`.
  - `CoveragePanel` keeps the report in component state.
  - The census runs under `audit.CensusQueryBudget` = 60 s (`internal/store/coverage.go:149`). It is a broad scan that §6.5 allows live only with explicit permission.
- **Expected gain:** a re-check after reopening costs up to 60 s and a broad scan. With this change it costs 0 unless the analyst presses Run again. This is the most expensive call in the app.
- **Risk:** low, provided the result always shows its "as of" time and never re-runs by itself.
- **Recommendation:** keep the last report per (sourceId, start, end) in module memory and show it on reopen with its age and a Run again button (§c.2).

### F3. Patterns report is discarded when its drawer closes. Rank 3

- **Evidence:** table row 17. `PatternsPanel` keeps `phase` and `askKey` in component state. On live, one run is an Analyze plus up to `MaxPatternRows` of paged slowest reads per source (`patterns_run.go:217, 405-409`).
- **Expected gain:** a reopen costs 0 live calls instead of a full patterns run.
- **Risk:** low, with the same as-of rule as F2. **Ordering constraint:** worker W2 (patterns UI) is editing `PatternsPanel.svelte` right now. This task must start after W2 merges.
- **Recommendation:** keep the last 3 reports keyed by `askKey` in module memory, with the same display rule as F2 (§c.2).

### F4. Breakdown and request lists read sources one after another; Analyze reads them in parallel. Rank 4

- **Evidence:**
  - `internal/store/breakdown.go:84-119` (counts) and `122-135` (bins) loop over profiles and admit each source in turn.
  - `internal/store/requests.go:207-208` does the same, and slowest and activity pages use this path.
  - `internal/store/analyze.go:100-107` runs one goroutine per source under the same `admit()`.
- **Expected gain:** with S sources, latency drops from the sum of the per-source times to the largest one. For 2 live sources at a few seconds each, breakdown latency roughly halves, and it scales with up to 8 sources.
- **Risk:** medium. More operations run at the same moment across sources, but the per-source cap (1) and the global cap (4) do not change. This is the same policy Analyze already uses, so §6.5 is untouched. The merge order must stay deterministic, and cancellation must release slots.
- **Recommendation:** fan out the per-source phases with goroutines through the existing `admit()`, keeping per-source results in index slots and merging in the original order. For breakdown, phase 2 (bins) still waits for the merged `TopKeys()` from phase 1. Do not parallelise within a source.

### F5. Hashed static assets are never cacheable. Rank 5

- **Evidence:** table rows 1 and 2.
  - `serveAsset` (`internal/server/server.go:199-233`) calls `http.ServeContent` but sends no `Cache-Control`.
  - Embedded files have no modification time, so no `Last-Modified` is sent either.
  - Every load and reload re-transfers 1,158,182 B, and the browser cannot keep its compiled-code cache for the 1 MB of JS.
- **Expected gain:** small on loopback (7–57 ms of transfer, plus JS parse and compile on repeat loads), and it costs almost nothing to build.
- **Risk:** negligible. File names carry content hashes, and `index.html` stays `no-store` (`server.go:192`), so a new build always loads new names.
- **Recommendation:** send `Cache-Control: private, max-age=31536000, immutable` only for `/assets/*` files that exist. Leave 404s, `/` and every `/api/*` response unchanged.

### F6. Analysis payload grows with cohorts × slots, and the 16 MiB cap is checked only after all Mongo work. Rank 6 (measure first)

- **Evidence:**
  - `internal/audit/analyze.go:186-210` appends one timeline series for **every** cohort that has data.
  - Measured: 85,353 of 153,531 B for 6 series × 90 slots, about 232 B per point.
  - `internal/store/analyze.go:192-195` runs `json.Marshal` on the whole analysis to compare it with `maxAnalysisBytes`, after both windows have been read. The handler then encodes it a second time.
  - By arithmetic, about 600 cohorts at 120 slots reaches 16 MiB. The user would then get "processing safety limit" after paying the full query cost. The UI draws only the top series by default (`appState.svelte.ts:687`).
- **Expected gain:** unknown until the live cohort count is known. That is why this item is measurement only (§5.4).
- **Risk of building now:** moving timelines out of the response changes the API and the MCP bridge contract (`internal/mcp/tools.go:145`).
- **Recommendation:** add a benchmark only (task T-E): bytes and encode time against cohort count at 120 slots. Decision rule: build "timelines on demand from a server-held last result" (the MCP bridge already does this with `resultTTL`, `internal/mcp/bridge.go:23`, `tools.go:80-87`) only if realistic cohort counts reach 25 % of the cap or the encode takes over 100 ms. Folding the double encode into one pass rides along with that change. On its own it saves milliseconds and is not worth a separate task.

Insert the block below into docs/superpowers/plans/2026-10-24-performance-review.md directly after the F6 "Recommendation" paragraph (the line ending "...is not worth a separate task.") and before "### F7". Nothing else in the doc changes.

---

**T-E result (2026-10-24, synthetic data only, no product code changed).** `internal/store/analyze_size_bench_test.go`, `TestAnalysisPayloadSizeByCohorts` (run with `-v`). The payload is one `audit.Analysis` per cohort count at 120 slots of 10 s: `timeline`, `apis` (one comparison per cohort, with 30-bin histograms per stats block) and `requestVolume` (one row per cohort and slot). Encode time is the median of 5 `json.Marshal` runs.

| Cohorts | JSON bytes | Share of 16 MiB | Encode median |
|---|---|---|---|
| 10 | 472,017 | 2.8 % | 2.0 ms |
| 100 | 4,742,818 | 28.3 % | 22.6 ms |
| 300 | 14,243,652 | 84.9 % | 68.7 ms |
| 600 | 28,489,898 | 169.8 % | 84.7 ms |

- **Cap reached at 354 cohorts** (16,758,429 B at 353; 16,803,215 B at 354). Encode at 354 cohorts: 46 ms.
- Per cohort: about 47.5 KB, or about 396 B per cohort-slot point for all three parts. The timeline alone is 283 B per point.
- Single runs. Encode medians moved by up to about 25 % between runs (600 cohorts: 85 ms and 105 ms).
- This does not match the "about 600 cohorts" arithmetic above. That used 232 B per point, and the figure does not match this section's own live numbers either: 85,353 B over 6 × 90 = 540 points is 158 B per point. The synthetic payload is heavier than the live timeline because it also carries `apis` and `requestVolume`, which the arithmetic left out.
- Not measured: the benchmark builds the `Analysis` directly, not through `BuildAnalysis`. The real path also enforces `MaxSummaryRows` (40,000 bucket rows), which may stop a large analysis before the byte cap. Check this on live data.

**Decision under the F6 rule: no build now.**
- Encode time: the 100 ms trigger is not reached inside the cap (46 ms at 354 cohorts).
- Cohort trigger: 25 % of the cap is 4 MiB, which this payload reaches at about 88 cohorts. The live cohort count at 120 slots is unknown. The demo has 8 API groups over 2 sources, well below that, but it is not a live measurement.
- Next step: count live API groups in one window (a cohort is a source, controller, action, kind and outcome) and rerun this test with that count. Build timelines on demand only if the live count is above about 88.

### F7. Identical analyses are recomputed (manual re-run, A→B→A, MCP repeats, Patterns' own Analyze). Rank 7 (design ready, do not build yet)

- **Evidence:**
  - Table rows 3, 4 and 6.
  - `runAnalysis` (`appState.svelte.ts:671-695`) does not compare against the analysis already shown.
  - `/api/analyze` has no cache and no single-flight (`server.go:474-493`).
  - The MCP `audit_compare` goes through `/api/analyze` (`internal/mcp/tools.go:145`). Its `comparing` flag only blocks overlap within one MCP session.
- **Why not build now:** a manual re-run of a window near "now" is a legitimate refresh. Audit rows are written in two stages and async records complete later (§6.4.1), so a historical window can still change, and a cache would quietly serve stale numbers. A safe version needs a freshness rule plus "as of" display, which is the design in §c.4. The evidence that repeats are common enough comes only from synthetic flows. Under §5.4, build the cheap counter first (T-F, optional) and the cache only when its trigger is met.

### Checked and not recommended (stop-building rule)

| Candidate | Why not |
|---|---|
| Run current and reference windows in parallel inside a source (`store/analyze.go:146-150`) | Would need two operations at once on one source. That breaks `PerSourceConcurrency = 1` and §6.5. |
| Fetch `info` and `sources` in parallel at landing (`appState.svelte.ts` `initialize()`) | Measured gap is 15 ms and neither call touches Mongo. |
| Record LRU, reopen without refetch (row 15) | `RecordDetail` embeds `Payload{Request, Response}` (`internal/audit/models.go:402-407,435-439`), and payload bodies must never be cached. The call is a cheap `_id` point read. |
| Ledger: skip GET when already loaded (row 19) | Local file, 1 ms, no Mongo. `ledgerStore` already dedupes calls in flight. |
| gzip or brotli for assets and JSON | Loopback only. It saves about 0.8 MB of local transfer (ms) at a CPU cost. Revisit only if the app is ever served over a network. |
| Prefetching breakdown or slowest after an analysis | Adds speculative live load on a per-source slot of 1. This conflicts with the §6.5 cost bounds. |
| Removing the breakdown-after-analyze waterfall | The breakdown query comes from the analysis query, and the per-source slot already serializes them on live. |
| More debounce or cancellation work | Measured fine: 3 clicks gave 1 run, a repeated preset gave 0, and late responses are dropped by sequence number. |

## (c) Minimal cache design

All caches are in RAM inside the browser tab. Nothing goes to `sessionStorage`, `localStorage`, IndexedDB or disk. API responses keep `Cache-Control: no-store`, which is correct for audit data. No cache holds `/api/record`, `/api/payload`, `/api/export*` or any payload body.

### c.1 Evidence memo (F1): build now

| Item | Rule |
|---|---|
| Cached | Successful JSON bodies of `/api/breakdown`, `/api/slowest` (each page, by cursor) and `/api/activity`. **Not cached:** errors, aborted calls, and any response where a source failed (`sources[].ok === false`) or provenance completeness is not `complete`. This keeps retry possible and never pins a partial result. |
| Key | `epoch` + endpoint + the request body as sent (the same canonical JSON the callers build already, for example `BreakdownPanel.requestKey`). |
| Epoch | An integer owned by the memo module and bumped by `appState` on: start of `runAnalysis` (next to `invalidateDetails()`), `deleteSource`, and session forget (401). A bump clears the map. |
| Freshness | Lives exactly as long as the analysis on screen. No TTL. "Run analysis" is the refresh. Each entry stores `fetchedAt`. A panel served from the memo shows "as of HH:MM:SS" in its footer, so nothing is served silently. |
| In flight | Store the pending promise under its key so concurrent callers share one call. On abort, remove the pending entry before rejecting. |
| Bounds | 64 entries, LRU, and 4 MiB total measured by response text length. Measured sizes are 1.6 KB to 60 KB. |
| Privacy | The memo holds only rows already shown (user IDs and IPs appear in breakdown dims). It is cleared on epoch bump and dies with the tab. |
| Headers | Unchanged. |

### c.2 Drawer results (F2, F3): build now

| Item | Rule |
|---|---|
| Cached | The last Patterns report (up to 3, keyed by `askKey` = sourceIds\|start\|end\|stall\|gap) and the last coverage report per (sourceId, start, end), up to 8. Coverage refusals (409) are not cached. |
| Display | On reopen, show the report with "Found at HH:MM (N min ago)" and a **Run again** button. Never re-run automatically. Run again always goes to the server and replaces the entry. |
| Invalidation | On read, drop any entry whose sourceIds include a source missing from the current `sources` list. Clear everything on session forget (401). No TTL, because the age is always visible. |
| Bounds | 3 + 8 entries. Reports are a few KB each. |
| Privacy | RAM only, module state. |

### c.3 Static asset headers (F5): build now

`/assets/<hashed>` files that exist get `Cache-Control: private, max-age=31536000, immutable`. `/`, 404s and `/api/*` keep their current headers, including `no-store` on `writeJSON` (`server.go:699-716`).

### c.4 Server-side analysis cache and single-flight (F7): designed, build only on trigger

- **Trigger:** the T-F counter shows that at least 20 % of live `/api/analyze` calls repeat a key seen in the previous 10 minutes, over at least 50 analyses.
- **Cached:** the normalized `audit.Analysis`, which contains no payload bodies. Never requests, record, payload, export, activity or coverage results. Never a result with a failed source, `completeness != complete`, or any `ErrLimit`.
- **Key:** SHA-256 of the canonical JSON of `Query.Normalized()` (sorted source IDs), plus a per-source generation counter (bumped on add, delete or reconnect), plus query mode and the `allowBroadScans` flag. **Queries with an age range are never cached**, because `HasAgeRange()` sets `q.Now`.
- **Freshness:**
  - A window counts as *settled* when `End ≤ now − settle`, where `settle` = max(calibrated `_id` margin, 15 min). Settled windows get a TTL of 10 min.
  - Any window that ends inside the settle period gets a TTL of 30 s.
  - The response gains `asOf` and `cached: true` fields, and the UI shows "as of".
  - A manual Run sends `fresh: true`, which bypasses and refills the entry.
  - The MCP bridge passes `asOf` through in its page.
- **Single-flight:** callers with the same key share one execution. The shared operation is cancelled when **all** waiting callers cancel, and it never outlives `OperationTimeout`. It runs under the existing `admit()`, budgets and broad-scan refusals unchanged, so §6.4 and §6.5 are not weakened. A refused or partial result is returned to every waiter and is not stored.
- **Bounds:** 8 entries, LRU, 32 MiB total (each analysis is at most 16 MiB). RAM only. Cleared on source delete, source change, session forget and process exit. Never written to disk, as the storage-decision spec requires.
- **Headers:** `/api/analyze` stays `no-store`. The cache lives only on the server.

## (d) Acceptance criteria

Browser criteria use Resource Timing on a demo instance, the same method as §0.

- **T-A evidence memo (c.1)**
  - Breakdown → Failures → Breakdown → Failures issues exactly one `/api/breakdown` per distinct key. Revisits issue 0.
  - User → Status code → User: the second User issues 0.
  - Evidence → Back to Pulse issues 0.
  - Nearby activity on the same row twice: the second issues 0.
  - After "Run analysis", the next tab visit issues 1 again.
  - Unit tests:
    - an epoch bump empties the memo;
    - a response with a failed source is not stored;
    - an aborted pending entry is not served;
    - the 65th entry evicts the least recently used one, and the byte cap evicts too;
    - the allowed endpoints are exactly breakdown, slowest and activity;
    - no `sessionStorage`, `localStorage` or `indexedDB` access (spy).
  - `npm run check` and `npm test` pass.
- **T-B drawer results (c.2)**
  - Patterns run → close → reopen shows the report with its as-of text and issues 0 requests. Run again issues 1.
  - The same holds for coverage.
  - Removing a source hides its cached reports.
  - A 409 refusal is not cached.
  - Existing `web/tests/patterns*.test.cjs` and `drawer.test.cjs` pass.
- **T-C parallel per-source reads (F4)**
  - Store test with 3 fake sources that each block 200 ms per operation: `Breakdown` and `Requests` finish in under 1.5× the time of one source (today it is about 3×).
  - An instrumented counter shows no more than 1 concurrent operation per source and no more than 4 globally.
  - Output is byte-identical to the serial version on existing fixtures.
  - Cancelling mid-run releases every slot (the semaphores are empty afterwards).
  - `go test ./internal/store/...` passes with `-race`.
- **T-D asset headers (c.3)**
  - `GET /assets/<existing hashed file>` returns `Cache-Control: private, max-age=31536000, immutable`.
  - `GET /`, `GET /assets/missing.js` (404) and any `/api/*` keep their current headers.
  - In the browser, a reload shows `transferSize == 0` for all 3 assets.
- **T-E analysis size benchmark (F6, measure only)**
  - The benchmark prints JSON bytes and encode time for 10, 100, 300 and 600 cohorts at 120 slots, and reports the cohort count at which 16 MiB is reached.
  - The result is appended to this document with the build or no-build decision under the F6 rule.
- **T-F repeat counter (F7 trigger, optional)**
  - Counts analyze requests whose key matches one of the last 8 keys within 10 min.
  - Holds hashes only, in RAM, and logs a contents-free line (`analyze repeats: n of m`) at shutdown.
  - Test: two identical calls count 1 repeat, and an age-range query is never counted.

## (e) Parallel worktree tasks (disjoint file sets)

| Task | Files (only these) | Depends on | Notes |
|---|---|---|---|
| **T-A** Evidence memo | new `web/src/lib/evidenceMemo.ts`; new `web/tests/evidenceMemo.test.cjs`; `web/src/components/BreakdownPanel.svelte`; `web/src/components/TrafficSlowness.svelte`; `web/src/components/ActivityContext.svelte`; `web/src/lib/appState.svelte.ts` (epoch bump calls only) | none | Build first: highest live gain, no API change |
| **T-B** Drawer results | new `web/src/lib/drawerResults.svelte.ts`; new `web/tests/drawerResults.test.cjs`; `web/src/components/PatternsPanel.svelte`; `web/src/components/CoveragePanel.svelte` | **after W2 (patterns UI) merges** | Reads `appState` sources read-only. Does not edit `appState` |
| **T-C** Parallel per-source reads | `internal/store/breakdown.go`; `internal/store/requests.go`; new `internal/store/fanout_test.go` | none (check W1 does not touch these files before starting) | No change to `admit.go`, budgets or concurrency values |
| **T-D** Asset cache header | `internal/server/server.go` (`serveAsset` only); new `internal/server/assets_cache_test.go` | none | Smallest task |
| **T-E** Analysis size benchmark | new `internal/store/analyze_size_bench_test.go` | none | Measurement only, no product change |
| **T-F** Repeat counter (optional) | new `internal/server/repeats.go`, `internal/server/repeats_test.go`; one call site in `internal/server/server.go` `analyze` | **after T-D** (same file) | Only needed to decide on §c.4 |
| deferred | §c.4 server cache | T-F trigger met | Not scheduled |

**Build order:** T-A, T-C and T-D in parallel. T-B starts once W2 has merged. T-E runs whenever convenient. T-F and the §c.4 cache wait for a decision.

**Decision for the user:** whether to fund T-F, the repeat counter. Without it there is no evidence for or against the server-side analysis cache. Everything else above can proceed without a decision.
