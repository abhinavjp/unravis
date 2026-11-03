# Progress: Phase 03 prototype-alignment

## Done

- TSK-17 DONE: `SUFFICIENT_REF = 60`, `SUFFICIENT_REF_P95 = 100`, `SUFFICIENT_INC = 20`; sparse text now "Fewer than 60 events in the reference window — too few to compare against." (number from the constant).
- TSK-18 DONE: `refCmp(tot, refTot, cov, refP95, incCov, counts)`; rates over covered minutes both sides; incident minimum 20; all 4 call sites pass `incCov`.
- TSK-19 DONE: `spreadOk(counts)` (mean-based skip below 1/min) + `SPREAD_*` constants; `refCmp` greys a bunched reference ("reference events are bunched in a few minutes"). Counts built per covered reference minute (audit: `opsFor('all')`/`opMinute`; lanes: `metricMinute` per import, same loop as `laneWin`).
- TSK-20 DONE: removed `max` field and `* 1.6` from `auditBuckets` (no code read bucket `.max`; chart axis already uses p95 values, so no other change); `onsetMinSamples:5`.
- TSK-21 DONE: bucket `p95` is `null` when bucket events < 20; the chart already skips null p95 (`b.filter(x => x.p95)`, `x.p95 || 0`).
- TSK-22 DONE: three gate `<li>` items added before the "Results found" `<li>` in "Rules used".
- TSK-23 DONE (UNMEASURED, see below).
- FIX-1 DONE: SUFFICIENT_REF_P95 now used: `refCmp` drops `refP95` below 100; `p95ok`/`p95d`/`p95bad`/`p95sh` in `traceStats` and `layerRead` need ref events >= 100 (IIS 69 events: grey "only 69 comparison requests; P95 needs 100"); analysis `p95cmp` gates the P95 ratio, headline, reference line and per-operation ratios.
- FIX-2 DONE: `computeAnalysis` sets `incLow` (incident n < 20): `cmp` off, no ratios or headline, grey "Too few requests in this window to compare."
- FIX-3 DONE: `sh` returns `l.cmp.why` (true reason) when rate is null.
- FIX-4 DONE: `sh(l, v)` shows "none in the comparison period" when rate is set but the reference count is zero (5xx cards in t-slow).
- FIX-5 DONE: "What Unravis sees" shows an en dash instead of "0 ms" when there are no requests.

## Evidence

- TSK-17 greps: `SUFFICIENT_REF = 60` 1 hit; `SUFFICIENT_REF_P95 = 100` 1 hit; `SUFFICIENT_INC = 20` 1 hit; no `= 30` next to `SUFFICIENT_REF`. Other "30" hits: line ~1965 ASSUMPTIONS text "at least 30 events and 80% coverage" changed to `' + SUFFICIENT_REF + '`; line ~1557 "at least 30% above" is a different rule (left).
- TSK-18: `refCmp(` has 5+ args at all 4 call sites. Flagged (open decision 5): the zero-event incident case (`!tot` branch, ~line 1214) now returns the grey "too few events in the incident window" result instead of a rate of 0.
- TSK-19: `spreadOk(` 1 definition + 1 use. Browser check (file opened in built-in browser): no console errors; spreadOk([0,0,50,0])=false, [1,1,1,1]=true, [5,5,5,5]=true, []=false, one-burst-with-mean-1=false; real 24h audit reference counts: spread OK.
- TSK-20: no `1.6` near p95; `onsetMinSamples:5` 1 hit.
- TSK-22: `at least 60 events` 1 hit.
- Inline script extracted and `node --check`: OK after every packet group. Page loads with no console error.

## Not fixed

- `doTrace` itself was not executed end to end (needs an open case in the UI; no Playwright).

## UNMEASURED

- UNMEASURED: flow-check not run (Playwright missing). `node prototype/flow-check.cjs` -> `Error: Cannot find module 'playwright'`.
