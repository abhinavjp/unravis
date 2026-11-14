# Wave 13 lane fix — W13-01..W13-04

Branch `feat/blackbox-expansion`. Web-only change; no `.go` file, `blackbox.ts`, `blackboxCopy.ts`, `package.json` or `appState.svelte.ts` was touched (the Go worktree diffs belong to the parallel lane-server worker). W13-05 was already fixed and is not re-addressed here. No commit/push made.

## Measure/unit contract

`callLanes.ts` now carries a typed measure on every lane:

- `LaneMeasure = 'at-once' | 'batch-rate'`. `measureOf(lane)` derives it: reason code `blackbox` (the host PDH `SQLHost...BatchRequestsPerSec` contract) is always `batch-rate`; everything else is `at-once`. An explicit `LaneIn.measure` may also declare `batch-rate`.
- `LaneCell` has two separate series: `atOnce` (average concurrency, from measured `busyMs` only) and `rate` (batches per second, from recorded counts: `count / bucketSeconds`). A count is never renamed to `busyMs`; no duration or concurrency is inferred from a rate, even a zero rate. If a future server adds real `busyMs` to a blackbox lane, `atOnce` is computed from it alongside the rate.
- `CallLane.measure` travels with the cells; `cellValue(cell, measure)` is the single drawable value. `hasShape`/`peak` use the lane's own measure, so positive counts draw a lane and genuine zeros are a real zero shape.
- `dbState` accepts `laneHasObservations` (source availability rests on valid recorded observations, independently of drawable concurrency) and `laneCadenceSeconds`; `DbStateView.measure` exposes the unit. `CallLanes.svelte` draws, hatches, labels and reads out through the measure: `laneRateShape` header, `readDbRate` + `readDbNoAtOnce` readout, conditional `legendRate`, `unitBatches` in the plot's aria-label, and `laneNoCallTimes` ("call times not recorded here") instead of "nothing slow here".
- The minimap (`callMap`/`CallMinimap`, out of edit scope) is a concurrency-only strip: blackbox cells keep `atOnce: null`, so its row stays hatched (unknown), never zero. The inspector's "while this call ran" database line likewise reports concurrency as not recorded; the source sentence explains the throughput measure.

## Per-finding change

- **W13-01 (High):** `laneCells` fills `rate` for batch-rate lanes (complete buckets only; `complete:false` and missing buckets stay null); `buildLane` reports `measure`, measure-aware `hasShape`/`peak`; `dbState.pickKey` accepts `blackbox` on recorded observations. A 100 batches/s recording now draws a nonempty, unit-labelled Blackbox lane; slow-call duration is marked unavailable, never "nothing slow".
- **W13-02 (High):** `SlowCallScreen.toLaneIn` gates the saved-day fallback inside `qsBlockSeconds` to Query Store provenance only (`held-sql`, `layered-sql`, `qs-file`); Blackbox and API-seen never borrow a saved day's interval. `regroupBlocks` averages each series only over cells that hold a number (proven interval) and writes the average back only into those cells; null/incomplete cells stay null through regroup, minimap and readout, including mixed cells within one block.
- **W13-03 (Medium):** `dbSrcBlackbox`/`dbMsgBlackbox`/`dbShortBlackbox` now describe host batch throughput ("Blackbox host batches per second…"), state a cadence only when the source sent one (`cadenceSeconds`, optionally parsed by `model.ts`; config permits 5/15/30/60 s), otherwise say the read interval is unknown. The message separates throughput from concurrency and keeps gaps unknown. The old "every 15 to 60 s" DMV wording is gone. Server label consistency: the Go label still says "every 15 s" (another worker owns that file); the client no longer repeats any fixed cadence it cannot prove.
- **W13-04 (Low):** `SAMPLE_DATE` is only used behind the explicit demo switch (`pretend`); a missing `since` renders "recording start unknown", a missing QS-file import date renders "import date not recorded". `parseTracing` still drops unreadable dates.

## Existing assertions updated (old behaviour encoded)

In `web/tests/drill.blackboxlane.test.cjs`:
- "the label a first-time investigator reads" — old exact string `Blackbox sampling every 15 to 60 s since …` replaced by the host-batch label.
- "no since … on the sample date" — old assertion `dbSrcBlackbox('11 Nov 2026')` replaced by "recording start unknown" (this was the W13-04 lock-in).
- `dbMsgBlackbox`/`dbShortBlackbox` are now cadence functions; calls updated, caps (<320 / <60 chars) kept.
- lint-entry regex updated to the new sample output; banned-wording list extended to the cadence variants.
- header comment corrected: the lane reads host PDH counters, not the BL-101/102/118/119 DMV schedules.

No other existing assertion was weakened; all caps, the copy lint, the 26-key help guard and the screen source-text guards (`lane.blockSeconds ?? qsBlockSeconds(lane.key)`, `queryStore?.intervalMinutes`, `minutes * 60`, one `dbState(` call) still hold.

## Commands and results (run from `web/`)

- `npx svelte-check --output human` → **0 errors, 0 warnings**.
- `node --test tests/drill.blackboxlane.test.cjs tests/drill.blackboxcontract.test.cjs tests/slowcall.lanes.test.cjs tests/slowcall.lanesui.test.cjs tests/drill.copy.test.cjs` → **75 tests, 75 pass, 0 fail**.
- `npm test` (full suite, `node --test tests/*.test.cjs`) → **3067 tests, 3067 pass, 0 fail** (116 s).

New behavioural coverage (`tests/drill.blackboxcontract.test.cjs`) runs the current server shape (counts, no `busyMs`, omitted `blockSeconds`) through `parseTracing → buildLane → regroupBlocks → mapRow/readoutAt → dbState`: positive-only, genuine-zero, mixed, unavailable; saved-QS-day isolation; mixed-block unknowns after every transform; 5/15/30/60 s vs unknown cadence; missing/invalid/valid dates; optional-metadata tolerance; plus source-text wiring guards. CRLF line endings preserved (the blackboxlane test file was LF before and stays LF).

## Still needs a real browser / real recording

- Mounted-component proof: pixels, hatch layout, hover/pin/keyboard readout values and aria speech on the real `CallLanes`/`CallMinimap` with a live response (these tests are pure-function + source-text, per repo style).
- Parity of drawn rates against recorded PDH intervals on an authorized real case, including 5 s cadence, changed cadence, skipped optional group and multi-instance recordings.
- Server-side caption/metadata alignment (`tracingBlackboxLabel` still claims a fixed 15 s; `cadenceSeconds` is not sent yet) — owned by the Go lane worker.
- Wave-12 reader/rate/selection blockers and the exact merge-tree CI run remain open release gates.