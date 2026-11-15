# P-32 Put the parts together: layout W1, pinned timeline, lane-fit rule

model: sonnet, effort: high. Reason: integration and the one layout rule the owner set; the only file that knows the layout.
risk: high. Review batch: RB-5. Wave: W3. Requirements: R-01, R-02, R-03, R-13, R-16.

## Outcome
`SlowCallScreen.svelte` shows the full Workbench (W1): window control, then the pinned timeline (three lanes, axis, readout, overview strip), then call facts and the two list tabs, with the inspector on the right. The three lanes are fully visible together with no inner scroll at 1440x800 and 1100x760.

## Write scope
- `web/src/components/SlowCallScreen.svelte`
- `web/tests/slowcall.screen.test.cjs`

## Must not change
- The child components (P-26..P-31) and the models: wire them, do not edit them. If a child's props do not fit, STOP and report to the orchestrator (the child's implementer fixes it). The header and state markup from P-25 (testids stay).

## Changes
1. Derived values in the script (all `$derived` from the controller state `s`):
   - `bounds = windowBounds(s.callStartMs, s.window)`; `over = overviewBounds(s.callStartMs)`.
   - `resp = s.visible ?? s.overview`; `step = resp.window.bucketSeconds`.
   - `lanes = { audit: buildLane(lane('audit'), 'audit', resp.window, bounds.start, bounds.end), iis: …, sql: … }`.
   - API bars: replace `lanes.audit.bars` with `packBars(s.slowRows.map(row → { id, label: controller + '.' + action, start, end }))` (the tracing audit lane carries no per-call events worth a bar).
   - `events → markers`: `allSources({ apiRows: s.slowRows, iisBars: lanes.iis.bars, events: s.events, callId, start, end, shown })`; `markers = items.filter(i => i.marker)`.
   - `db = dbState({ laneReasonCode, laneHasShape: lanes.sql.hasShape, laneAsOf, blockSeconds: lanes.sql.blockSeconds, queryStore: newestDay?.lane?.queryStore ?? null, hasEvents, windowStart, windowEnd, pretend }, fmt)`. `newestDay` = newest saved SQL lane day of the case from `fetchLaneDays(caseId, signal)` (`web/src/lib/tracing/lanes.ts`), read once on mount, failure ignored (then `queryStore` is null).
   - when `pretend` is set and its shape differs from the real lane, pass the database lane to `CallLanes` with `hasShape` forced to match `db.shape` (`none` → no cells shown). Pretend only changes what is drawn.
   - map rows: `mapRow(key, buildLane(overviewLane(key), key, s.overview.window, over.start, over.end).cells)`.
   - nearby: `filterRows(s.nearby.rows, s.chip, callRef, bounds.start, bounds.end)`.
2. Local UI state: `shown` (three booleans, all true), `tab` (`'calls' | 'all'`), `selectedKey`, `readout`, `pretend` (null). `demo` comes from the prop.
3. Markup inside `scall-body`, in this DOM order (keyboard order = visual order):
```svelte
<div class="scall-grid" data-layout="w1">
  <div class="scall-main">
    <CallWindowBar … onCommit={(w) => controller.commit(w)} />
    <div class="scall-pin" data-testid="scall-pin">
      <CallLanes … />
      <CallMinimap … hidden={coversOverview(...)} onPreview={(c) => controller.preview(centreOn(...))} onCommit={(c) => controller.commit(centreOn(...))} />
    </div>
    <div class="scall-thiscall">…DrillOverview (moved here from P-25's place)…</div>
    <div role="tablist" data-testid="scall-tabs"> two tabs: tabNearbyCalls(n), tabAllSources(n) </div>
    {#if tab === 'calls'}<CallNearby … />{:else}<CallAllSources … />{/if}
  </div>
  <CallInspector … />
</div>
```
   Tabs follow the WAI-ARIA tabs pattern the app already uses (copy the pattern from `DrillPopup.svelte`'s tabs: roles, arrow keys).
4. CSS, layout W1 (only here):
   - `.scall-grid { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 16px; align-items: start; }` At `max-width: 1180px`: one column, inspector after the lists.
   - `.scall-pin { position: sticky; top: 0; z-index: 2; background: <page surface token>; }` (find the surface token the timeline panels use). The page scrolls; the pinned block does not scroll inside.
   - Lane-fit rule: `.scall-main { --scall-lane-h: clamp(72px, calc((100vh - 470px) / 3), 132px); }` where 470 px is the measured height of nav bar + screen header + window bar + axis + readout + strip. MEASURE it in the browser pane at 1100x760 and 1440x800 and set the constant so that at 760 px tall the three lanes, the axis and the strip are all inside the viewport without scrolling. Report the measured numbers.
   - No `overflow: auto|scroll` and no fixed `height` on `.scall-main`, `.scall-pin`, `.scall-grid`.
5. Switching layout later must stay cheap: all layout rules live under the `[data-layout="w1"]` selector; children carry no grid placement of their own.
6. Extend the text test: all six child components are imported and used once; `data-layout="w1"`; `position: sticky`; `--scall-lane-h`; `controller.preview(` only inside the minimap `onPreview`; `controller.commit(` used by the window bar and minimap; no `overflow: auto|scroll` in the file; `fetchLaneDays(` used.

## Proof
- [ ] `cd web && node --test tests/slowcall.screen.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `cd web && npm test` → 0 fail.
- [ ] Not mounted in the app until P-33. If a dev build with a temporary local mount is used to measure, remove it before reporting and say so; the measured px values are in the report.
- [ ] Lane-fit at both sizes is re-measured after mounting by P-42 (SCALL-04) and P-46.

## Depends on
P-25, P-26, P-27, P-28, P-29, P-30, P-31 (and so P-05..P-12).
