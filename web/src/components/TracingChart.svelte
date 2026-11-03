<script lang="ts">
  // The Investigation Timeline chart (WP-17b): one stacked row per lane (API audit, SQL, IIS) on one shared,
  // server-floored UTC axis. Bars are exactly the buckets the server returned; nothing here sums, estimates
  // or recomputes the axis. WP-17c adds hover/focus text; WP-61 reads the bucket selector's onSelect.
  import type { TracingLane, TracingLaneKey, TracingResponse } from '../lib/tracing/model';
  import {
    LANE_ASK, LANE_NAME, LANE_ORDER, MIXED_NOTE, PANEL_TITLE, TRUNCATED_NOTE,
    axisCaption, barMax, barsFor, laneNote, slotGrid, slotOutside, slotStartMs
  } from '../lib/tracing/lanes';
  import { formatTime } from '../lib/time';
  import { formatDuration } from '../lib/format';
  import { barTipLines, laneHint } from '../lib/tracing/tooltip';
  import {
    CANVAS_TEXT, MIN_ZOOM_SLOTS, PERCENTILES, PERCENTILE_LABEL, absoluteSlot, clampArrow, clampRange, fullRange, hasLatency,
    isZoomed, percentileValue, rangeFromDrag, zoomIn, zoomOut, zoomedText, type Percentile, type ZoomRange
  } from '../lib/tracing/canvas';

  let { response, zone, selected = null, onSelect, visible = {} }: {
    response: TracingResponse;
    zone: string;
    selected?: number | null;
    onSelect?: (slotIndex: number) => void;
    // A row is hidden only when its flag is exactly false (WP-23); WP-11 passes the toolbar's choices.
    visible?: Partial<Record<TracingLaneKey, boolean>>;
  } = $props();

  const SWATCH_STYLE: Record<TracingLaneKey, string> = {
    audit: 'background: var(--lane-audit)',
    sql: 'background: var(--lane-sql)',
    iis: 'background: var(--lane-iis)'
  };

  const grid = $derived(slotGrid(response.window));
  const orderedLanes = $derived(
    LANE_ORDER.filter((key) => visible[key] !== false)
      .map((key) => response.lanes.find((lane) => lane.key === key))
      .filter((lane): lane is TracingLane => !!lane)
  );
  const anyHidden = $derived(Object.values(visible).some((flag) => flag === false));
  const zoneName = $derived(zone === 'UTC' ? 'UTC' : zone);
  // Zoom (WP-23): every slot index in this file is ABSOLUTE into the full grid; `range` only picks which ones are drawn.
  let zoom = $state<ZoomRange | null>(null);
  $effect(() => { void response; zoom = null; });
  const range = $derived<ZoomRange>(grid ? (zoom ? clampRange(zoom, grid.n) : fullRange(grid.n)) : { from: 0, to: 0 });
  const shown = $derived(range.to - range.from);
  const zoomed = $derived(grid ? isZoomed(range, grid.n) : false);
  const slotIndexes = $derived(Array.from({ length: shown }, (_, k) => range.from + k));
  const widthPct = $derived(shown > 0 ? 100 / shown : 0);
  function leftPct(index: number): number { return (index - range.from) * widthPct; }

  // Latency line (WP-23c): the audit lane's per-bucket P50 or P95, drawn as plain SVG over the bars. Null buckets leave a gap.
  let percentile = $state<Percentile>('p95');
  const auditLane = $derived(response.lanes.find((lane) => lane.key === 'audit'));
  const latencyAvailable = $derived(!!auditLane && hasLatency(auditLane.buckets, percentile));
  const latencySegments = $derived.by(() => {
    if (!grid || !auditLane) return { segments: [] as string[], max: 0 };
    const values = new Map<number, number>();
    for (const bucket of auditLane.buckets) {
      const value = percentileValue(bucket, percentile);
      const startMs = Date.parse(bucket.start);
      if (value === null || !Number.isFinite(startMs)) continue;
      const index = Math.floor((startMs - grid.firstMs) / grid.widthMs);
      if (index >= range.from && index < range.to) values.set(index, Math.max(values.get(index) ?? 0, value));
    }
    const max = Math.max(0, ...values.values());
    const segments: string[] = [];
    let run: string[] = [];
    for (const index of slotIndexes) {
      const value = values.get(index);
      if (value === undefined) {
        if (run.length > 0) segments.push(run.join(' '));
        run = [];
        continue;
      }
      const x = leftPct(index) + widthPct / 2;
      const y = max > 0 ? 100 - (value / max) * 100 : 100;
      run.push(x.toFixed(2) + ',' + y.toFixed(2));
    }
    if (run.length > 0) segments.push(run.join(' '));
    return { segments, max };
  });
  function inRange(index: number): boolean { return index >= range.from && index < range.to; }
  const anyOutside = $derived(grid ? slotIndexes.some((i) => slotOutside(grid, i)) : false);
  const tickStep = $derived(Math.max(1, Math.ceil(shown / 6)));
  const tickIndexes = $derived(slotIndexes.filter((i) => (i - range.from) % tickStep === 0));
  const caption = $derived(grid ? axisCaption(zoneName, response.window.bucketSeconds, anyOutside) : '');

  function drawsBars(lane: TracingLane, bars: ReturnType<typeof barsFor>): boolean {
    if (!grid || lane.disabled) return false;
    if (lane.state === 'events') return true;
    if (lane.state === 'incomplete') return bars.length > 0;
    return false;
  }

  let localSelected = $state<number | null>(null);
  $effect(() => { localSelected = selected ?? null; });

  // A selection that scrolls out of the zoomed range is dropped, not left pointing at a hidden bar.
  $effect(() => {
    if (localSelected != null && !inRange(localSelected)) localSelected = null;
  });

  function clampIndex(index: number): number {
    return Math.min(range.to - 1, Math.max(range.from, index));
  }

  function move(delta: number) {
    if (!grid) return;
    localSelected = clampArrow(range, localSelected ?? range.from, delta);
  }

  function doZoomIn() {
    if (!grid) return;
    zoom = zoomIn(range, grid.n, localSelected ?? undefined);
  }
  function doZoomOut() {
    if (!grid) return;
    const next = zoomOut(range, grid.n);
    zoom = isZoomed(next, grid.n) ? next : null;
  }
  function resetZoom() { zoom = null; }

  function selectAt(index: number) {
    localSelected = clampIndex(index);
    onSelect?.(localSelected);
  }

  function onOverlayKey(event: KeyboardEvent) {
    if (!grid) return;
    if (event.key === 'ArrowLeft') { event.preventDefault(); move(-1); }
    else if (event.key === 'ArrowRight') { event.preventDefault(); move(1); }
    else if (event.key === 'Enter') { event.preventDefault(); if (localSelected != null) onSelect?.(localSelected); }
    else if (event.key === 'Escape' && tipSlot != null) { event.preventDefault(); event.stopPropagation(); hideTip(); return; }
    else if (event.key === '+' || event.key === '=') { event.preventDefault(); doZoomIn(); return; }
    else if (event.key === '-' || event.key === '_') { event.preventDefault(); doZoomOut(); return; }
    else if (event.key === '0') { event.preventDefault(); resetZoom(); return; }
    else return;
    if (event.key !== 'Enter') showKeyboardTip();
  }

  // Hover and keyboard text (WP-17c): one shared tooltip box. A pointer shows the lane under it; the keyboard
  // shows every lane that draws bars for the selected bucket. All lines come from tooltip.ts as plain text.
  let tipSlot = $state<number | null>(null);
  let tipLane = $state<TracingLaneKey | null>(null);
  let tipStyle = $state('');
  let overlayEl: HTMLDivElement | undefined;

  function hideTip() { tipSlot = null; tipLane = null; }

  function linesFor(lane: TracingLane, slot: number): string[] {
    if (!grid) return [];
    const bar = barsFor(grid, lane.buckets).find((b) => b.index === slot);
    const start = slotStartMs(grid, slot);
    return barTipLines({
      key: lane.key,
      startText: formatTime(start, { zone, precision: 'minute' }),
      endText: formatTime(start + grid.widthMs, { zone, precision: 'minute' }),
      zoneName,
      count: bar?.count ?? 0,
      errors: bar?.errors ?? 0,
      origin: lane.origin,
      asOfText: formatTime(lane.asOf, { zone, precision: 'minute' }),
      note: lane.state === 'incomplete' ? laneNote(lane) : ''
    });
  }

  const tipGroups = $derived.by(() => {
    if (tipSlot == null || !grid) return [];
    const slot = tipSlot;
    const lanes = tipLane
      ? orderedLanes.filter((lane) => lane.key === tipLane)
      : orderedLanes.filter((lane) => drawsBars(lane, grid ? barsFor(grid, lane.buckets) : []));
    return lanes.map((lane) => ({ key: lane.key, name: LANE_NAME[lane.key], lines: linesFor(lane, slot) }));
  });

  function showKeyboardTip() {
    if (!grid || localSelected == null) return;
    tipLane = null;
    tipSlot = localSelected;
    tipStyle = `left:${Math.min(80, leftPct(localSelected))}%;top:4px`;
  }

  // Drag across the chart to zoom into those bars (WP-23). Pointer capture (taken once the drag leaves its first bucket) keeps it alive outside the overlay.
  let dragFrom = $state<number | null>(null);
  let dragTo = $state<number | null>(null);
  let swallowClick = false;

  function slotAt(event: PointerEvent): number {
    if (!overlayEl) return -1;
    const rect = overlayEl.getBoundingClientRect();
    if (rect.width <= 0) return -1;
    return absoluteSlot(range, (event.clientX - rect.left) / rect.width);
  }

  function onOverlayDown(event: PointerEvent) {
    if (!grid || event.button !== 0) return;
    const slot = slotAt(event);
    if (slot < 0) return;
    dragFrom = slot;
    dragTo = slot;
    hideTip();
  }

  function endDrag(commit: boolean) {
    if (dragFrom != null && dragTo != null && commit && grid && dragFrom !== dragTo) {
      zoom = rangeFromDrag(grid.n, dragFrom, dragTo);
      swallowClick = true;
      setTimeout(() => { swallowClick = false; }, 0);
    }
    dragFrom = null;
    dragTo = null;
  }

  function onOverlayMove(event: PointerEvent) {
    if (!grid || !overlayEl) return;
    const rect = overlayEl.getBoundingClientRect();
    if (rect.width <= 0) return;
    const slot = absoluteSlot(range, (event.clientX - rect.left) / rect.width);
    if (dragFrom != null) {
      if (slot >= 0) dragTo = slot;
      // Capture only once the pointer leaves the bucket it went down on, so a plain click still reaches the column.
      if (slot >= 0 && slot !== dragFrom) {
        try { overlayEl.setPointerCapture(event.pointerId); } catch { /* a synthetic pointer cannot be captured */ }
      }
      hideTip();
      return;
    }
    if (slot < 0) { hideTip(); return; }
    let hovered: TracingLaneKey | null = null;
    overlayEl.parentElement?.querySelectorAll<HTMLElement>('.panel[data-lane]').forEach((panel) => {
      const box = panel.getBoundingClientRect();
      if (event.clientY >= box.top && event.clientY <= box.bottom) hovered = panel.dataset.lane as TracingLaneKey;
    });
    tipSlot = slot;
    tipLane = hovered;
    const x = Math.min(event.clientX - rect.left, rect.width - 220);
    tipStyle = `left:${Math.max(0, x)}px;top:${Math.max(0, event.clientY - rect.top + 14)}px`;
  }
</script>

<div class="tracing-chart">
  {#if response.mixedConsistency}
    <div role="note" class="notice">
      <p class="mixed-title"><strong>{MIXED_NOTE.title}</strong></p>
      <p>{MIXED_NOTE.body}</p>
      <p class="muted">{MIXED_NOTE.fixed}</p>
    </div>
  {/if}

  {#if grid}
    <div class="canvas-bar" role="toolbar" aria-label={CANVAS_TEXT.ariaLabel}>
      <button type="button" class="secondary-button" onclick={doZoomIn} disabled={shown <= Math.min(MIN_ZOOM_SLOTS, grid.n)}>{CANVAS_TEXT.zoomIn}</button>
      <button type="button" class="secondary-button" onclick={doZoomOut} disabled={!zoomed}>{CANVAS_TEXT.zoomOut}</button>
      <button type="button" class="secondary-button" onclick={resetZoom} disabled={!zoomed}>{CANVAS_TEXT.resetZoom}</button>
      <span class="canvas-group" role="group" aria-label={CANVAS_TEXT.latencyLabel}>
        {#each PERCENTILES as option (option)}
          <button type="button" class="secondary-button" aria-pressed={percentile === option} onclick={() => { percentile = option; }}>{PERCENTILE_LABEL[option]}</button>
        {/each}
      </span>
      {#if auditLane && !latencyAvailable}<small class="muted">{CANVAS_TEXT.noLatency}</small>{/if}
      <span class="zoom-note" aria-live="polite">{zoomed ? zoomedText(shown, grid.n) : ''}</span>
      <small class="muted">{CANVAS_TEXT.dragHint}</small>
    </div>
  {/if}

  {#if anyHidden && orderedLanes.length === 0}
    <p role="status" class="lane-note">{CANVAS_TEXT.allHidden}</p>
  {/if}

  <div class="lanes-grid">
    {#each orderedLanes as lane, laneIndex (lane.key)}
      {@const allBars = grid ? barsFor(grid, lane.buckets) : []}
      {@const bars = allBars.filter((bar) => inRange(bar.index))}
      {@const max = barMax(bars)}
      {@const drawBars = drawsBars(lane, allBars)}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="lane-label" tabindex="0" aria-describedby="tc-hint-{lane.key}" data-truncated={lane.truncated ? 'true' : undefined} style="--row:{laneIndex + 1}">
        <span class="swatch" style={SWATCH_STYLE[lane.key]} aria-hidden="true"></span>
        <strong>{LANE_NAME[lane.key]}</strong>
        <small>{LANE_ASK[lane.key]}</small>
        <small class="origin">{lane.origin === 'live' ? 'Read just now' : 'From saved logs'} · {formatTime(lane.asOf, { zone, precision: 'minute' })}</small>
        <span id="tc-hint-{lane.key}" role="tooltip" class="hint">{laneHint(lane.key)}</span>
      </div>
      <div class="axis axis-left" style="--row:{laneIndex + 1}">
        {#if drawBars}
          <span class="tick tick-top">{max}</span>
          <span class="tick tick-bottom">0</span>
        {/if}
      </div>
      <div class="panel" data-lane={lane.key} data-state={lane.state} data-disabled={lane.disabled ?? undefined} style="--row:{laneIndex + 1}">
        <p class="panel-title">{PANEL_TITLE[lane.key]}</p>
        {#if drawBars}
          <div class="bars">
            {#if grid}
              {#each slotIndexes as i (i)}
                {#if slotOutside(grid, i)}
                  <div class="outside-dash" style="left:{leftPct(i)}%;width:{widthPct}%"></div>
                {/if}
              {/each}
            {/if}
            {#each bars as bar (bar.index)}
              {#if grid}
                <div class="bar" style="left:{leftPct(bar.index)}%;width:calc({widthPct}% - 1px);height:{max > 0 ? (bar.count / max) * 100 : 0}%">
                  {#if bar.count > 0 && bar.errors > 0}
                    <div class="bar-err" style="height:{(bar.errors / bar.count) * 100}%"></div>
                  {/if}
                </div>
              {/if}
            {/each}
            {#if lane.key === 'audit' && latencySegments.segments.length > 0}
              <svg class="latency" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true" data-percentile={percentile}>
                {#each latencySegments.segments as points, k (k)}
                  <polyline {points} fill="none" vector-effect="non-scaling-stroke" />
                {/each}
              </svg>
              <span class="latency-max">{PERCENTILE_LABEL[percentile]}: up to {formatDuration(latencySegments.max)}</span>
            {/if}
          </div>
          {#if lane.state === 'incomplete'}
            <p role="status" class="lane-note bad">{laneNote(lane)}</p>
          {/if}
        {:else if lane.state === 'none-seen'}
          <p class="lane-note">No events in this period</p>
        {:else}
          <p class="lane-note">{laneNote(lane)}</p>
        {/if}
      </div>
      <div class="axis axis-right" style="--row:{laneIndex + 1}"></div>
    {/each}

    {#if grid}
      <div class="axisrow" style="--row:{orderedLanes.length + 1}">
        {#each tickIndexes as i (i)}
          <span class="axtick" style="left:{leftPct(i)}%">{formatTime(slotStartMs(grid, i), { zone, precision: 'minute' })}</span>
        {/each}
      </div>
    {/if}

    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      role="group"
      aria-roledescription="bucket selector"
      aria-label="Time buckets. Left and right arrow keys move, Enter opens Run analysis for the selected bucket."
      aria-describedby="tc-tip"
      tabindex="0"
      class="overlay"
      style="--end:{Math.max(2, orderedLanes.length + 1)}"
      bind:this={overlayEl}
      onkeydown={onOverlayKey}
      onpointerdown={onOverlayDown}
      onpointermove={onOverlayMove}
      onpointerup={() => endDrag(true)}
      onpointercancel={() => endDrag(false)}
      onpointerleave={hideTip}
      onblur={hideTip}
    >
      <div id="tc-tip" role="tooltip" class="tip" style={tipStyle} hidden={tipGroups.length === 0}>
        {#each tipGroups as group (group.key)}
          <div class="tip-group">
            <strong>{group.name}</strong>
            {#each group.lines as line, i (i)}<span>{line}</span>{/each}
          </div>
        {/each}
      </div>
      {#if grid}
        {#each slotIndexes as i (i)}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div
            class="column"
            class:selected={localSelected === i}
            style="left:{leftPct(i)}%;width:{widthPct}%"
            onclick={() => { if (!swallowClick) selectAt(i); }}
          ></div>
        {/each}
        {#if dragFrom != null && dragTo != null}
          <div class="brush" style="left:{leftPct(Math.min(dragFrom, dragTo))}%;width:{(Math.abs(dragTo - dragFrom) + 1) * widthPct}%"></div>
        {/if}
      {/if}
    </div>
  </div>

  {#if grid}
    <p class="axcap">{caption}</p>
  {/if}

  {#if response.truncated}
    <p class="foot">{TRUNCATED_NOTE}</p>
  {/if}
</div>

<style>
  .tracing-chart { display: grid; gap: var(--space-3); min-width: 0; }
  .notice { display: grid; gap: var(--space-1); margin: 0; padding: var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-3); }
  .notice p { margin: 0; }
  .mixed-title { font-size: var(--fs-4); }
  .muted { color: var(--text-2); }

  .lanes-grid { position: relative; display: grid; grid-template-columns: auto 3.5rem 1fr 3.5rem; column-gap: var(--space-3); row-gap: var(--space-2); align-items: stretch; min-width: 0; }

  .lane-label, .axis, .panel, .axisrow { grid-row: var(--row); }
  .lane-label { grid-column: 1; display: grid; gap: 2px; align-content: start; min-width: 0; }
  .lane-label .swatch { display: inline-block; width: 10px; height: 10px; border-radius: 50%; margin-right: var(--space-1); }
  .lane-label small { color: var(--text-2); font-size: var(--fs-2); }
  .lane-label { position: relative; }
  .lane-label:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .hint { display: none; position: absolute; left: 0; top: 100%; z-index: 3; width: max-content; max-width: 22rem; padding: var(--space-2); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .lane-label:hover .hint, .lane-label:focus-within .hint { display: block; }
  .lane-label .origin { color: var(--text-2); }

  .axis { position: relative; grid-column: 2; min-height: 72px; font-size: var(--fs-1); color: var(--text-2); }
  .axis-right { grid-column: 4; }
  .axis .tick { position: absolute; right: var(--space-1); }
  .axis .tick-top { top: 0; }
  .axis .tick-bottom { bottom: 0; }

  .panel { grid-column: 3; position: relative; min-height: 72px; padding: var(--space-1) 0; background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  .panel-title { margin: 0; padding: 0 var(--space-2); color: var(--text-2); font-size: var(--fs-2); }
  .bars { position: relative; height: 54px; margin-top: var(--space-1); }
  .bar { position: absolute; bottom: 0; background: var(--accent); border-radius: var(--radius-sm) var(--radius-sm) 0 0; }
  .panel[data-lane='audit'] .bar { background: var(--lane-audit); }
  .panel[data-lane='sql'] .bar { background: var(--lane-sql); }
  .panel[data-lane='iis'] .bar { background: var(--lane-iis); }
  .bar-err { position: absolute; bottom: 0; left: 0; right: 0; background: var(--bad); }
  .outside-dash { position: absolute; bottom: 0; height: 2px; background: var(--line); opacity: .6; }
  .lane-note { margin: var(--space-1) var(--space-2) 0; color: var(--text-2); font-size: var(--fs-3); text-align: center; }
  .lane-note.bad { color: var(--bad); text-align: left; }

  .axisrow { grid-column: 3; position: relative; height: var(--space-5); font-size: var(--fs-1); color: var(--text-2); }
  .axtick { position: absolute; top: 0; white-space: nowrap; transform: translateX(-50%); }

  .overlay { grid-column: 3; grid-row: 1 / var(--end); position: relative; }
  .overlay:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .tip { position: absolute; z-index: 4; display: grid; gap: var(--space-2); width: max-content; max-width: 22rem; padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); pointer-events: none; }
  .tip[hidden] { display: none; }
  .tip-group { display: grid; gap: 2px; }
  .column { position: absolute; top: 0; bottom: 0; margin: 0; padding: 0; border: 0; background: transparent; cursor: pointer; }
  .column.selected { background: color-mix(in srgb, var(--accent) 18%, transparent); outline: 1px solid var(--accent); }

  .brush { position: absolute; top: 0; bottom: 0; background: color-mix(in srgb, var(--accent) 24%, transparent); outline: 1px dashed var(--accent); pointer-events: none; }
  .canvas-bar { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; font-size: var(--fs-2); }
  .canvas-group { display: inline-flex; gap: var(--space-1); }
  .latency { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; pointer-events: none; }
  .latency polyline { stroke: var(--text); stroke-width: 1.5; }
  .latency-max { position: absolute; top: 0; right: var(--space-1); color: var(--text-2); font-size: var(--fs-2); pointer-events: none; }
  .zoom-note { color: var(--text-2); }

  .axcap { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .foot { margin: 0; color: var(--text-2); font-size: var(--fs-2); }

  @media (max-width: 480px) {
    .lanes-grid { grid-template-columns: 3.5rem 1fr 3.5rem; }
    .lane-label { grid-column: 1 / -1; }
    .axis { grid-column: 1; }
    .panel { grid-column: 2; }
    .axis-right { grid-column: 3; }
    .axisrow, .overlay { grid-column: 2; }
    /* Each lane takes two rows: its label, then its panel. The overlay spans them all; the labels stay above it. */
    .lane-label { grid-row: calc(var(--row) * 2 - 1); z-index: 5; }
    .axis, .panel { grid-row: calc(var(--row) * 2); }
    .axisrow { grid-row: calc(var(--row) * 2 - 1); }
    .overlay { grid-row: 1 / calc(var(--end) * 2 - 1); }
    .axtick:not(:nth-child(5n+1)) { display: none; }
  }
</style>
